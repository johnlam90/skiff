// =============================================================================
// File: internal/editor/wrap.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// wrap.go implements soft wrap: long lines flow onto continuation rows
// instead of panning horizontally. The design is anchor-based — the
// scroll position is (ScrollY line, ScrollSeg segment) and every
// computation walks at most a viewport's worth of lines from that
// anchor, so there is no whole-file layout to build or invalidate.
//
// Segment model: WrapSegments splits one line into visual rows. Tab
// stops reset at each segment boundary, which makes every segment
// behave exactly like an independent line — the existing visual-column
// helpers (LineVisualCol, RuneColAtVisual) work on a segment's rune
// subslice unchanged. Breaks prefer word boundaries (the wrapping
// whitespace stays on the earlier row, hanging past the width if it
// must) and fall back to a hard break for words wider than the pane.

package editor

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// isWrapSpace reports whether r is whitespace for wrap-breaking purposes.
// Only the two intra-line whitespace runes matter — newlines never reach
// a line's rune slice.
func isWrapSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// WrapSegments returns the rune start index of each visual row of a line
// wrapped to width cells. The result always holds at least [0], and every
// segment starts strictly after the previous one, so callers can rely on
// progress even for pathological widths. Whitespace never triggers a
// break — a run of spaces or tabs hangs past the right edge (painting
// clips it) so continuation rows never start with the wrapping space.
//
// The walk steps by grapheme cluster, so a break can never land between a
// base rune and its combining mark, and a two-cell glyph that would
// straddle the right edge moves down whole rather than being sliced in
// half. Every returned index is therefore a cluster boundary, which is
// what lets callers hand a segment's rune subslice to the visual-column
// helpers as if it were a line of its own.
func WrapSegments(runes []rune, width int) []int {
	segs := []int{0}
	if width <= 0 {
		return segs
	}
	segStart := 0
	visualCol := 0
	// wsEnd is the boundary just past the current segment's last
	// whitespace, not the whitespace rune's own index: a space can carry a
	// combining mark, and breaking between the two would strand the mark
	// at the head of the next row.
	wsEnd := -1
	for i := 0; i < len(runes); {
		r := runes[i]
		end, w := ClusterAt(runes, i, visualCol)
		if visualCol+w > width && i > segStart && !isWrapSpace(r) {
			brk := i
			// Breaking here would split a word if the previous rune is
			// also part of it — back up to just after the segment's last
			// whitespace when that gives a word-boundary break instead.
			if !isWrapSpace(runes[i-1]) && wsEnd > segStart {
				brk = wsEnd
			}
			segs = append(segs, brk)
			segStart = brk
			visualCol = 0
			wsEnd = -1
			i = brk // re-measure the moved-down runes with fresh tab stops
			continue
		}
		visualCol += w
		if isWrapSpace(r) {
			wsEnd = end
		}
		i = end
	}
	return segs
}

// wrapSegBounds returns the [start, end) rune range of segment seg. The
// last segment ends at the line's rune count.
func wrapSegBounds(segs []int, seg, lineLen int) (start, end int) {
	start = segs[seg]
	end = lineLen
	if seg+1 < len(segs) {
		end = segs[seg+1]
	}
	return start, end
}

// WrapRowOfCol returns the segment index that renders rune column col:
// the largest i with segs[i] <= col. A col at a segment boundary belongs
// to the later row (the cursor sits at that row's first cell), and the
// end-of-line position belongs to the last row.
func WrapRowOfCol(segs []int, col int) int {
	row := 0
	for i := 1; i < len(segs); i++ {
		if segs[i] <= col {
			row = i
		}
	}
	return row
}

// lineSegs is a small convenience: the wrap segments of buffer line i at
// the given width.
func (t *Tab) lineSegs(i, width int) []int {
	return WrapSegments(t.Buffer.LineRunes(i), width)
}

// normalizeAnchor clamps the (ScrollY, ScrollSeg) anchor into the buffer:
// ScrollY into [0, LineCount), ScrollSeg into that line's segment range.
// Edits and reloads can leave a stale segment index behind; every wrap
// walk normalizes first so it starts from a real row.
func (t *Tab) normalizeAnchor(width int) {
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	if t.ScrollY >= t.Buffer.LineCount() {
		t.ScrollY = t.Buffer.LineCount() - 1
	}
	if t.ScrollSeg < 0 {
		t.ScrollSeg = 0
	}
	if t.ScrollSeg > 0 {
		if segs := t.lineSegs(t.ScrollY, width); t.ScrollSeg >= len(segs) {
			t.ScrollSeg = len(segs) - 1
		}
	}
}

// advanceAnchor moves the (line, seg) anchor forward by n visual rows,
// stopping at the last segment of the last line. n < 0 is treated as 0.
func (t *Tab) advanceAnchor(line, seg, n, width int) (int, int) {
	for n > 0 {
		segs := t.lineSegs(line, width)
		if seg+n < len(segs) {
			return line, seg + n
		}
		if line >= t.Buffer.LineCount()-1 {
			return line, len(segs) - 1
		}
		n -= len(segs) - seg
		line++
		seg = 0
	}
	return line, seg
}

// retreatAnchor moves the (line, seg) anchor backward by n visual rows,
// stopping at (0, 0). n < 0 is treated as 0.
func (t *Tab) retreatAnchor(line, seg, n, width int) (int, int) {
	for n > 0 {
		if seg >= n {
			return line, seg - n
		}
		n -= seg // consume this line's rows above seg 0…
		if line == 0 {
			return 0, 0
		}
		line--
		seg = len(t.lineSegs(line, width)) - 1
		n-- // …and crossing onto the previous line's last row costs one.
	}
	return line, seg
}

// rowsBetween counts the visual rows from anchor (aLine, aSeg) down to
// (bLine, bSeg) — b's row index when a renders at row 0. The caller
// guarantees a <= b in document order. Counting stops early once the
// result exceeds limit, so off-screen distances cost O(limit) not O(file).
func (t *Tab) rowsBetween(aLine, aSeg, bLine, bSeg, width, limit int) int {
	if aLine == bLine {
		return bSeg - aSeg
	}
	rows := len(t.lineSegs(aLine, width)) - aSeg
	for line := aLine + 1; line < bLine; line++ {
		if rows > limit {
			return rows
		}
		rows += len(t.lineSegs(line, width))
	}
	return rows + bSeg
}

// SetWrap switches soft wrap on or off for this tab and resets the view
// state the other mode owns: wrap clears any horizontal pan, unwrap
// clears the segment anchor. cursorMoved is set so the next Render
// brings the cursor back into view in the new geometry.
func (t *Tab) SetWrap(on bool) {
	if t.Wrap == on {
		return
	}
	t.Wrap = on
	if on {
		t.ScrollX = 0
	} else {
		t.ScrollSeg = 0
	}
	t.cursorMoved = true
}

// ensureVisibleWrapped scrolls the wrap-mode anchor so the cursor's
// visual row is inside the viewport. Above the anchor the cursor's own
// (line, seg) becomes the anchor; below, we walk at most a viewport of
// rows to find it, and otherwise pin its row to the bottom by walking
// backward from the cursor — so the cost is bounded by the view height
// no matter how far a goto jumped.
func (t *Tab) ensureVisibleWrapped(width, viewH int) {
	if width < 1 {
		width = 1
	}
	if viewH < 1 {
		viewH = 1
	}
	t.normalizeAnchor(width)
	cLine := t.Cursor.Line
	cSeg := WrapRowOfCol(t.lineSegs(cLine, width), t.Cursor.Col)
	if cLine < t.ScrollY || (cLine == t.ScrollY && cSeg < t.ScrollSeg) {
		t.ScrollY, t.ScrollSeg = cLine, cSeg
		return
	}
	row := t.rowsBetween(t.ScrollY, t.ScrollSeg, cLine, cSeg, width, viewH)
	if row < viewH {
		return
	}
	t.ScrollY, t.ScrollSeg = t.retreatAnchor(cLine, cSeg, viewH-1, width)
}

// clampScrollWrapped is clampScroll's wrap-mode twin: the anchor may not
// scroll past the point where the file's last visual row sits roughly
// mid-viewport — the same overscroll feel as line mode, computed by
// walking backward from the end of the file instead of by arithmetic.
func (t *Tab) clampScrollWrapped(width, viewH int) {
	t.normalizeAnchor(width)
	overscroll := viewH / 2
	if overscroll < 3 {
		overscroll = 3
	}
	back := viewH - overscroll - 1
	if back < 0 {
		back = 0
	}
	lastLine := t.Buffer.LineCount() - 1
	lastSeg := len(t.lineSegs(lastLine, width)) - 1
	maxLine, maxSeg := t.retreatAnchor(lastLine, lastSeg, back, width)
	if t.ScrollY > maxLine || (t.ScrollY == maxLine && t.ScrollSeg > maxSeg) {
		t.ScrollY, t.ScrollSeg = maxLine, maxSeg
	}
}

// scrollWrapped moves the wrap-mode anchor by delta visual rows. Forward
// motion stops at the file's last row; clampScrollWrapped pulls the
// anchor back inside the overscroll bound on the next render, matching
// how line-mode Scroll defers its own clamping.
func (t *Tab) scrollWrapped(delta, width int) {
	t.normalizeAnchor(width)
	if delta > 0 {
		t.ScrollY, t.ScrollSeg = t.advanceAnchor(t.ScrollY, t.ScrollSeg, delta, width)
	} else if delta < 0 {
		t.ScrollY, t.ScrollSeg = t.retreatAnchor(t.ScrollY, t.ScrollSeg, -delta, width)
	}
}

// hitTestWrapped converts editor-local screen coordinates to a buffer
// position in wrap mode: walk forward from the anchor to the clicked
// visual row, then map the x offset through the segment's own rune
// slice. Gutter clicks land at the row's first rune, and clicks past a
// row's end clamp to the segment boundary.
func (t *Tab) hitTestWrapped(localX, localY, w, h int) (Position, bool) {
	if localY < 0 || localY >= h {
		return Position{}, false
	}
	// Mirror Render's scrollbar reservation so segment widths agree with
	// what was painted.
	if t.ScrollbarVisible(h) && w > 2 {
		w--
	}
	gw := gutterWidthFor(t.Buffer.LineCount())
	contentX := gw + 1
	contentW := w - gw - 1
	if contentW < 1 {
		contentW = 1
	}
	t.normalizeAnchor(contentW)
	line, seg := t.ScrollY, t.ScrollSeg
	n := localY
	var segs []int
	for {
		segs = t.lineSegs(line, contentW)
		if seg+n < len(segs) {
			seg += n
			break
		}
		n -= len(segs) - seg
		line++
		seg = 0
		if line >= t.Buffer.LineCount() {
			return Position{}, false
		}
	}
	runes := t.Buffer.LineRunes(line)
	start, end := wrapSegBounds(segs, seg, len(runes))
	if localX < contentX {
		return Position{Line: line, Col: start}, true
	}
	col := start + RuneColAtVisual(runes[start:end], localX-contentX)
	if col > end {
		col = end
	}
	return Position{Line: line, Col: col}, true
}

// renderWrappedBody paints the wrap-mode editor body (gutter, wrapped
// content rows, cursor) into (x, y, w, h). The caller has already
// reserved the scrollbar column, painted the base background, refreshed
// the highlight window, and normalized the anchor via clampScrollWrapped.
func (t *Tab) renderWrappedBody(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	bg := th.BG
	selStart, selEnd := PosOrdered(t.Anchor, t.Cursor)
	hasSel := t.HasSelection()

	gw := gutterWidthFor(t.Buffer.LineCount())
	contentX := x + gw + 1
	contentW := w - gw - 1
	if contentW < 1 {
		contentW = 1
	}

	cursorX, cursorY := -1, -1
	row := 0
	seg := t.ScrollSeg
	for line := t.ScrollY; row < h && line < t.Buffer.LineCount(); line++ {
		runes := t.Buffer.LineRunes(line)
		segs := WrapSegments(runes, contentW)
		if seg >= len(segs) {
			seg = len(segs) - 1 // defensive; normalizeAnchor already ran
		}
		var styles []tcell.Style
		if line < len(t.Styles) {
			styles = t.Styles[line]
		}
		isCursorLine := line == t.Cursor.Line
		lineBg := bg
		if isCursorLine {
			lineBg = th.LineHL
		}
		lineBgStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Text)

		for ; seg < len(segs) && row < h; seg++ {
			cy := y + row
			// Re-paint this row with its (possibly highlighted) bg. The
			// cursor-line tint covers every segment of the logical line so
			// a wrapped cursor line still reads as one unit.
			for cx := x; cx < x+w; cx++ {
				scr.SetContent(cx, cy, ' ', nil, lineBgStyle)
			}

			// Gutter: line number and git marker on the line's first row
			// only. Continuation rows keep a blank gutter, which is what
			// makes a wrap visually distinct from a new line.
			if seg == 0 {
				numStr := fmt.Sprintf("%*d", gw-1, line+1)
				gutterStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
				if isCursorLine {
					gutterStyle = gutterStyle.Foreground(th.AccentSoft)
				}
				if marker, ok := t.GitLines[line]; ok && marker != GitLineNone {
					scr.SetContent(x, cy, gitLineMarkerRune(marker), nil, gutterStyle.Foreground(gitLineMarkerColor(th, marker)))
				}
				for i, r := range numStr {
					if i == 0 && t.GitLines[line] != GitLineNone {
						continue
					}
					scr.SetContent(x+i, cy, r, nil, gutterStyle)
				}
			}

			start, end := wrapSegBounds(segs, seg, len(runes))
			segRunes := runes[start:end]
			visualCol := 0 // segment-local; tab stops reset per row
			for j := 0; j < len(segRunes); {
				next, width := ClusterAt(segRunes, j, visualCol)
				st := t.cellStyle(th, styles, line, start+j, lineBg, hasSel, selStart, selEnd)
				glyph, comb := segRunes[j], segRunes[j+1:next]
				if glyph == '\t' {
					glyph, comb = ' ', nil
				}
				for cell := 0; cell < width; cell++ {
					sc := visualCol + cell
					if sc >= contentW {
						break
					}
					// The glyph (with any combining marks) goes in the
					// cluster's first cell; the rest are blanks that carry
					// the row background under a wide glyph's second half.
					if cell > 0 {
						scr.SetContent(contentX+sc, cy, ' ', nil, st)
						continue
					}
					scr.SetContent(contentX+sc, cy, glyph, comb, st)
				}
				visualCol += width
				j = next
			}

			if isCursorLine && WrapRowOfCol(segs, t.Cursor.Col) == seg {
				cCol := LineVisualCol(runes[start:end], t.Cursor.Col-start)
				if cCol >= contentW {
					// A row that ends exactly at full width puts the
					// end-of-line cursor one past the last cell — clamp it
					// onto the row rather than into the scrollbar column.
					cCol = contentW - 1
				}
				cursorX, cursorY = contentX+cCol, cy
			}
			row++
		}
		seg = 0
	}

	if cursorX >= 0 {
		scr.ShowCursor(cursorX, cursorY)
	} else {
		scr.HideCursor()
	}
}
