// =============================================================================
// File: internal/app/diffview.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

// Diff view modal — how Skiff shows a git diff. On a wide terminal
// it renders side-by-side: old text on the left, new text on the right,
// line numbers on both, deletions and additions aligned row-for-row the
// way VS Code's diff editor pairs them. When the window is too narrow
// for two readable columns it adapts to the classic unified view — the
// mode is picked per draw, so resizing the terminal reflows the diff
// live.
//
// Reached from the Git panel (click a change row) and the editor's
// gutter markers (click a colored bar). [ Open file ] — when the file
// still exists — jumps to the first changed line; Esc, Close, or a
// click outside dismisses. Scroll with the wheel/trackpad or ↑ ↓
// PgUp PgDn.

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/theme"
)

const (
	// diffModalMaxWidth caps the modal on very wide terminals — beyond
	// ~85 columns per side the eye loses the pairing between columns.
	diffModalMaxWidth = 174
	// diffSideBySideMinBody is the minimum body width (cells inside the
	// borders) for the side-by-side layout. Below it each column would
	// dip under ~45 cells — too cramped for real code lines — so the
	// view adapts to unified.
	diffSideBySideMinBody = 92
	// diffNoGutter is the width of one side's line-number gutter:
	// "9999 " — four digits and a space.
	diffNoGutter = 5
)

// diffRowKind classifies one aligned row of the side-by-side layout.
type diffRowKind int

const (
	// diffRowContext is an unchanged line present on both sides.
	diffRowContext diffRowKind = iota
	// diffRowChange holds a deletion (left), an addition (right), or a
	// modification (both) — a zero line number marks the missing side.
	diffRowChange
	// diffRowHunk is an @@ hunk header spanning the full width.
	diffRowHunk
	// diffRowFile is a file boundary in a multi-file diff (a commit
	// diff from the history views) — the file's path spans the width.
	diffRowFile
)

// span is a half-open rune range [Start, End) inside a line. The zero
// value means "no range".
type span struct {
	Start, End int
}

// diffRow is one display row of the side-by-side diff. LeftNo/RightNo
// are 1-based file line numbers; 0 means that side is blank for this
// row (a pure addition or deletion). Hunk rows carry the header text
// in Left. LeftEmph/RightEmph mark the intra-line changed span on
// paired modification rows (see annotateDiffSpans).
type diffRow struct {
	Kind            diffRowKind
	LeftNo, RightNo int
	Left, Right     string
	LeftEmph        span
	RightEmph       span
}

// diffSpan locates the changed portion of a modified line pair: the
// longest common prefix and suffix are unchanged, and whatever sits
// between differs. Returns the differing rune spans of old and new.
// The suffix never overlaps the prefix, so a pure insertion yields an
// empty span on the old side at the insertion point.
func diffSpan(old, new []rune) (oldSpan, newSpan span) {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	return span{Start: p, End: len(old) - s}, span{Start: p, End: len(new) - s}
}

// annotateDiffSpans stamps intra-line emphasis spans onto paired
// modification rows — the word-level highlight that makes "which
// characters moved" readable at a glance. One-sided rows (pure
// additions/deletions) are left alone: the whole line is the change.
func annotateDiffSpans(rows []diffRow) {
	for i := range rows {
		r := &rows[i]
		if r.Kind != diffRowChange || r.LeftNo == 0 || r.RightNo == 0 {
			continue
		}
		r.LeftEmph, r.RightEmph = diffSpan([]rune(r.Left), []rune(r.Right))
	}
}

// menuDiffFile is the ≡ menu entry that opens the active tab's own
// diff — the same view a Git panel row click gives, without having to
// leave the file to reach it.
func (a *App) menuDiffFile() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" || tab.IsImage() {
		return
	}
	kind := filetree.GitChangeNone
	if a.tree != nil {
		kind = a.tree.DirtyFiles[tab.Path]
	}
	lines := loadGitFileDiff(a.rootDir, a.diffBase, tab.Path, kind == filetree.GitChangeAdded)
	if len(lines) == 0 {
		a.flash("No uncommitted changes in this file")
		return
	}
	title := filepath.Base(tab.Path)
	if a.tree != nil {
		if rel, ok := relFromRoot(tab.Path, a.tree.Root.Path); ok && rel != "." {
			title = filepath.ToSlash(rel)
		}
	}
	// The file is already open in front of the user — no Open button.
	a.openDiffView("Diff · "+title, lines, "", tab.Path)
}

// hasDiffableTab is the menu enabled-predicate for Diff this file: the
// active tab is a real file with uncommitted changes. Gutter markers
// cover the single-file-mode case, where there is no tree to consult.
func (a *App) hasDiffableTab() bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" || tab.IsImage() {
		return false
	}
	if len(tab.GitLines) > 0 {
		return true
	}
	return a.tree != nil && a.tree.DirtyFiles[tab.Path] != filetree.GitChangeNone
}

// diffHighlightCap bounds how many diff lines get Chroma-highlighted.
// Beyond it the lexing cost stops being worth paying at open time and
// context lines fall back to plain text — the changed lines, which are
// what the user came for, look identical either way.
const diffHighlightCap = 4000

// openDiffView shows the diff modal. raw is the unified `git diff`
// output, one line per entry — kept verbatim for the narrow layout and
// parsed into aligned rows for the wide one. openPath, when non-empty,
// arms the [ Open file ] button with that file; deleted files pass ""
// and get a lone [ Close ]. langPath names the file for syntax
// highlighting — it may point at a path that no longer exists (deleted
// files); only its extension matters.
func (a *App) openDiffView(title string, raw []string, openPath, langPath string) {
	a.closeAllModals()
	a.diffOpen = true
	a.overlays.Open(diffOverlay{a})
	a.diffTitle = title
	a.diffRaw = raw
	a.diffRows = parseSideBySideDiff(raw)
	annotateDiffSpans(a.diffRows)
	a.diffRowStyles = diffContextStyles(a.diffRows, langPath, a.theme)
	a.diffMaxLen = diffLongestLine(a.diffRaw, a.diffRows)
	a.diffOpenPath = openPath
	a.diffScroll = 0
	a.diffScrollX = 0
	a.diffHover = 0
	if openPath != "" {
		a.diffHover = 1 // focus Open file so Enter is the fast path
	}
}

// diffContextStyles precomputes Chroma syntax styles for the diff's
// context rows, one []tcell.Style per rune, nil for rows that keep git
// coloring (changes, hunks). The whole diff is lexed as one source so
// multi-line tokens survive; precomputing at open keeps the per-draw
// cost at zero — draws happen on every mouse-motion event.
func diffContextStyles(rows []diffRow, langPath string, th theme.Theme) [][]tcell.Style {
	if langPath == "" || len(rows) == 0 || len(rows) > diffHighlightCap {
		return nil
	}
	src := make([]string, len(rows))
	for i, r := range rows {
		if r.Kind == diffRowHunk || r.Kind == diffRowFile {
			src[i] = ""
			continue
		}
		if r.RightNo > 0 {
			src[i] = r.Right
		} else {
			src[i] = r.Left
		}
	}
	grid := editor.Highlight(langPath, strings.Join(src, "\n"), th)
	styles := make([][]tcell.Style, len(rows))
	for i, r := range rows {
		if r.Kind != diffRowContext || i >= len(grid) {
			continue
		}
		// Re-background onto the modal surface — Highlight styles carry
		// the editor's background.
		row := make([]tcell.Style, len(grid[i]))
		for j, st := range grid[i] {
			row[j] = st.Background(th.LineHL)
		}
		styles[i] = row
	}
	return styles
}

// diffLongestLine returns the widest content line in either layout,
// bounding how far the diff can scroll horizontally.
func diffLongestLine(raw []string, rows []diffRow) int {
	longest := 0
	for _, l := range raw {
		if n := runeLen(l); n > longest {
			longest = n
		}
	}
	for _, r := range rows {
		if n := runeLen(r.Left); n > longest {
			longest = n
		}
		if n := runeLen(r.Right); n > longest {
			longest = n
		}
	}
	return longest
}

// handleDiffKey routes keyboard input while the diff view is open:
// Esc dismisses, Enter activates the focused button, Tab/←/→ move
// between buttons, and the usual scroll keys move the body.
func (a *App) handleDiffKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeAllModals()
	case tcell.KeyEnter:
		if a.diffHover == 1 && a.diffOpenPath != "" {
			a.runDiffOpenFile()
		} else {
			a.closeAllModals()
		}
	case tcell.KeyTab:
		if a.diffOpenPath != "" {
			a.diffHover = 1 - a.diffHover
		} else {
			a.closeAllModals()
		}
	case tcell.KeyLeft:
		a.scrollDiffH(-wheelCols)
	case tcell.KeyRight:
		a.scrollDiffH(wheelCols)
	case tcell.KeyUp:
		if a.diffWalk(-1) {
			return
		}
		a.scrollDiff(-1)
	case tcell.KeyDown:
		if a.diffWalk(1) {
			return
		}
		a.scrollDiff(1)
	case tcell.KeyPgUp:
		a.scrollDiff(-a.diffVisibleRows())
	case tcell.KeyPgDn:
		a.scrollDiff(a.diffVisibleRows())
	}
}

// handleDiffMouse mirrors the info modal's mouse contract: the wheel
// scrolls the body (Shift+wheel scrolls it sideways, the same gesture
// the editor uses for long lines), hover tracks the buttons, click
// activates, and a click outside the modal dismisses. Shift state
// comes from lastShiftAt — handleMouse stamps it before dispatching
// here, and the sticky window bridges terminals that split the
// modifier into its own event.
func (a *App) handleDiffMouse(x, y int, btn tcell.ButtonMask) {
	shift := !a.lastShiftAt.IsZero() && time.Since(a.lastShiftAt) < modifierStickyWindow
	if btn&tcell.WheelUp != 0 {
		if shift {
			a.scrollDiffH(-wheelCols)
		} else {
			a.scrollDiff(-3)
		}
		return
	}
	if btn&tcell.WheelDown != 0 {
		if shift {
			a.scrollDiffH(wheelCols)
		} else {
			a.scrollDiff(3)
		}
		return
	}
	if btn&tcell.WheelLeft != 0 {
		a.scrollDiffH(-wheelCols)
		return
	}
	if btn&tcell.WheelRight != 0 {
		a.scrollDiffH(wheelCols)
		return
	}
	mx, my, mw, mh := a.diffModalRect()
	openRect, closeRect := a.diffButtonRects()
	if openRect.contains(x, y) {
		a.diffHover = 1
	} else if closeRect.contains(x, y) {
		a.diffHover = 0
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeAllModals()
		return
	}
	if openRect.contains(x, y) {
		a.runDiffOpenFile()
		return
	}
	if closeRect.contains(x, y) {
		a.closeAllModals()
	}
}

// runDiffOpenFile fires the Open file button: close the modal first
// (capture-then-close, same as confirmYes) and jump to the first
// changed line of the file the diff was showing.
func (a *App) runDiffOpenFile() {
	path := a.diffOpenPath
	a.closeAllModals()
	if path != "" {
		a.openFileAtFirstChange(path)
	}
}

// scrollDiff nudges the body viewport by delta rows, clamped to the
// active layout's row count. Delta 0 is a pure re-clamp — the draw
// path calls it so a resize that flips the layout can't leave the
// scroll pointing past the end.
func (a *App) scrollDiff(delta int) {
	max := a.diffBodyCount() - a.diffVisibleRows()
	if max < 0 {
		max = 0
	}
	a.diffScroll += delta
	if a.diffScroll > max {
		a.diffScroll = max
	}
	if a.diffScroll < 0 {
		a.diffScroll = 0
	}
}

// scrollDiffH slides the body sideways by delta cells so long lines
// aren't dead-ends. Clamped so the longest line can't scroll more than
// a comfortable margin past the left edge — over-scrolling to a fully
// blank body is disorienting.
func (a *App) scrollDiffH(delta int) {
	max := a.diffMaxLen - 10
	if max < 0 {
		max = 0
	}
	a.diffScrollX += delta
	if a.diffScrollX > max {
		a.diffScrollX = max
	}
	if a.diffScrollX < 0 {
		a.diffScrollX = 0
	}
}

// diffSideBySide reports whether the current terminal is wide enough
// for the two-column layout. Checked at draw and scroll time rather
// than stored, so resizing the window adapts the view live.
func (a *App) diffSideBySide() bool {
	_, _, mw, _ := a.diffModalRect()
	return mw-4 >= diffSideBySideMinBody && len(a.diffRows) > 0
}

// diffBodyCount returns how many body rows the active layout has:
// aligned rows side-by-side, raw diff lines unified.
func (a *App) diffBodyCount() int {
	if a.diffSideBySide() {
		return len(a.diffRows)
	}
	return len(a.diffRaw)
}

// diffVisibleRows returns how many body rows fit in the modal.
func (a *App) diffVisibleRows() int {
	_, _, _, mh := a.diffModalRect()
	return mh - 7
}

// diffModalRect returns the modal's on-screen rectangle. Diffs are the
// content, not a notification, so the modal takes most of the screen —
// which is also what makes room for two columns.
func (a *App) diffModalRect() (x, y, w, h int) {
	w = a.width - 6
	if w > diffModalMaxWidth {
		w = diffModalMaxWidth
	}
	if w < 40 {
		w = a.width - 2
	}
	// Chrome: border + title + divider above the body, blank + buttons
	// + border below = 7 rows. The body gets whatever remains, capped
	// by how many rows the widest layout actually has.
	maxBody := a.height - 9
	if maxBody < 3 {
		maxBody = 3
	}
	body := len(a.diffRows)
	if len(a.diffRaw) > body {
		body = len(a.diffRaw)
	}
	if body > maxBody {
		body = maxBody
	}
	if body < 1 {
		body = 1
	}
	h = body + 7
	x = (a.width - w) / 2
	y = (a.height - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// diffButtonRects returns the button hit zones: Open file (when armed)
// and Close, centered on the button row. Shared by draw and mouse so
// the highlight and the click can't disagree.
func (a *App) diffButtonRects() (open, closeBtn btnRect) {
	mx, my, mw, mh := a.diffModalRect()
	btnY := my + mh - 3
	cw := runeLen("[ Close ]")
	if a.diffOpenPath == "" {
		return btnRect{}, btnRect{x: mx + (mw-cw)/2, y: btnY, w: cw}
	}
	ow := runeLen("[ Open file ]")
	total := ow + 4 + cw
	startX := mx + (mw-total)/2
	return btnRect{x: startX, y: btnY, w: ow}, btnRect{x: startX + ow + 4, y: btnY, w: cw}
}

// drawDiffView paints the modal in whichever layout fits.
//
// Rows (relY):
//
//	0     top border
//	1     title — " Diff · path             esc"
//	2     divider
//	3..N  diff body (side-by-side or unified)
//	N+1   blank
//	N+2   buttons — [ Open file ]    [ Close ]
//	N+3   bottom border
func (a *App) drawDiffView() {
	mx, my, mw, mh := a.diffModalRect()
	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	fillRect(a.screen, mx, my, mw, mh, bgStyle)
	drawBorder(a.screen, mx, my, mw, mh, borderStyle)
	drawHDivider(a.screen, mx, my+2, mw, borderStyle)

	drawAt(a.screen, mx+1, my+1, " "+a.diffTitle, titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-runeLen(hint), my+1, hint, mutedStyle)

	a.scrollDiff(0)
	bodyX, bodyW := mx+2, mw-4
	visible := a.diffVisibleRows()
	if a.diffSideBySide() {
		for i := 0; i < visible; i++ {
			idx := a.diffScroll + i
			if idx >= len(a.diffRows) {
				break
			}
			a.drawDiffRowSideBySide(bodyX, my+3+i, bodyW, idx)
		}
	} else {
		for i := 0; i < visible; i++ {
			idx := a.diffScroll + i
			if idx >= len(a.diffRaw) {
				break
			}
			// Style from the full line (the +/-/@ prefix), display from
			// the scrolled slice — so sideways scrolling never changes
			// a line's color.
			line := a.diffRaw[idx]
			st := overlay.DiffLineStyle(a.theme, bg, line)
			drawAt(a.screen, bodyX, my+3+i, sliceRunes(line, a.diffScrollX, bodyW), st)
		}
	}

	openRect, closeRect := a.diffButtonRects()
	if a.diffOpenPath != "" {
		drawButton(a.screen, openRect.x, openRect.y, "[ Open file ]", bg, a.theme.Accent, a.diffHover == 1)
		drawButton(a.screen, closeRect.x, closeRect.y, "[ Close ]", bg, a.theme.Text, a.diffHover == 0)
	} else {
		drawButton(a.screen, closeRect.x, closeRect.y, "[ Close ]", bg, a.theme.Accent, true)
	}
	a.screen.HideCursor()
}

// drawDiffRowSideBySide paints one aligned row: two line-number
// gutters, two text columns, and a vertical rule between them. The
// changed side takes the Git add/delete color; blank sides stay empty
// so pure insertions and deletions read as gaps, the way VS Code
// renders them. idx indexes diffRows/diffRowStyles.
func (a *App) drawDiffRowSideBySide(x, y, w, idx int) {
	row := a.diffRows[idx]
	bg := a.theme.LineHL
	if row.Kind == diffRowFile {
		fileStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
		drawAt(a.screen, x, y, sliceRunes("▸ "+row.Left, 0, w), fileStyle)
		return
	}
	if row.Kind == diffRowHunk {
		hunkStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft).Bold(true)
		drawAt(a.screen, x, y, sliceRunes(row.Left, 0, w), hunkStyle)
		return
	}

	leftW := (w - 3) / 2
	rightW := w - 3 - leftW
	rightX := x + leftW + 3
	sepStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	a.screen.SetContent(x+leftW+1, y, '│', nil, sepStyle)

	var ctx []tcell.Style
	if idx < len(a.diffRowStyles) {
		ctx = a.diffRowStyles[idx]
	}
	changed := row.Kind == diffRowChange
	a.drawDiffSide(x, y, leftW, row.LeftNo, row.Left, changed, a.theme.GitDeleted, row.LeftEmph, ctx)
	a.drawDiffSide(rightX, y, rightW, row.RightNo, row.Right, changed, a.theme.GitAdded, row.RightEmph, ctx)
}

// drawDiffSide paints one half of a side-by-side row: a muted line
// number (blank when the side has none), then the text rune by rune —
// Chroma styles for context lines, the Git change color for changed
// ones, and reverse video over the intra-line span that actually
// differs on paired modifications. The horizontal scroll offset slides
// the text only; gutters stay put.
func (a *App) drawDiffSide(x, y, w, lineNo int, text string, changed bool, changeColor tcell.Color, emph span, ctx []tcell.Style) {
	if lineNo == 0 {
		return // blank side of a pure addition or deletion
	}
	bg := a.theme.LineHL
	noStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	base := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	if changed {
		base = tcell.StyleDefault.Background(bg).Foreground(changeColor)
	}
	no := itoa(lineNo)
	drawAt(a.screen, x+diffNoGutter-1-runeLen(no)-1, y, no+" ", noStyle)
	tw := w - diffNoGutter
	if tw <= 0 {
		return
	}
	runes := []rune(text)
	for col := 0; col < tw; col++ {
		i := a.diffScrollX + col
		if i >= len(runes) {
			break
		}
		st := base
		if !changed && i < len(ctx) {
			st = ctx[i]
		}
		if changed && emph.End > emph.Start && i >= emph.Start && i < emph.End {
			st = st.Reverse(true)
		}
		a.screen.SetContent(x+diffNoGutter+col, y, runes[i], nil, st)
	}
}

// sliceRunes returns at most w runes of s starting at offset — the
// shared clipping rule for horizontally scrolled diff text.
func sliceRunes(s string, offset, w int) string {
	runes := []rune(s)
	if offset >= len(runes) || w <= 0 {
		return ""
	}
	runes = runes[offset:]
	if len(runes) > w {
		runes = runes[:w]
	}
	return string(runes)
}

// parseSideBySideDiff converts unified `git diff` output into aligned
// two-column rows: context lines occupy both sides, each run of
// deletions pairs line-for-line with the following run of additions
// (the standard hunk shape for a modification), and leftovers become
// one-sided rows. File headers (diff --git, index, ---, +++) are
// dropped — the modal title already names the file. Pure, so tests can
// feed it captured diffs without a repo.
func parseSideBySideDiff(raw []string) []diffRow {
	type numbered struct {
		no   int
		text string
	}
	var rows []diffRow
	var dels, adds []numbered
	oldNo, newNo := 0, 0
	inHunk := false

	flush := func() {
		n := len(dels)
		if len(adds) > n {
			n = len(adds)
		}
		for i := 0; i < n; i++ {
			row := diffRow{Kind: diffRowChange}
			if i < len(dels) {
				row.LeftNo, row.Left = dels[i].no, dels[i].text
			}
			if i < len(adds) {
				row.RightNo, row.Right = adds[i].no, adds[i].text
			}
			rows = append(rows, row)
		}
		dels, adds = nil, nil
	}

	for _, line := range raw {
		if strings.HasPrefix(line, "diff --git ") {
			// A new file starts (commit diffs from the history views
			// concatenate several). Close out the previous file and
			// mark the boundary so the reader knows where they are.
			flush()
			inHunk = false
			rows = append(rows, diffRow{Kind: diffRowFile, Left: diffGitPath(line)})
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			flush()
			oldStart, _, newStart, _, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			oldNo, newNo = oldStart, newStart
			inHunk = true
			rows = append(rows, diffRow{Kind: diffRowHunk, Left: line})
			continue
		}
		if !inHunk {
			continue // file headers before the first hunk
		}
		switch {
		case strings.HasPrefix(line, "-"):
			dels = append(dels, numbered{no: oldNo, text: line[1:]})
			oldNo++
		case strings.HasPrefix(line, "+"):
			adds = append(adds, numbered{no: newNo, text: line[1:]})
			newNo++
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" — metadata, not content.
			continue
		default:
			flush()
			text := strings.TrimPrefix(line, " ")
			rows = append(rows, diffRow{Kind: diffRowContext, LeftNo: oldNo, RightNo: newNo, Left: text, Right: text})
			oldNo++
			newNo++
		}
	}
	flush()
	// A single-file diff doesn't need to announce its one file — the
	// modal title already names it. Only multi-file diffs keep their
	// boundary rows.
	fileRows := 0
	for _, r := range rows {
		if r.Kind == diffRowFile {
			fileRows++
		}
	}
	if fileRows == 1 {
		trimmed := rows[:0]
		for _, r := range rows {
			if r.Kind != diffRowFile {
				trimmed = append(trimmed, r)
			}
		}
		rows = trimmed
	}
	return rows
}

// diffGitPath extracts the file path from a "diff --git a/X b/Y"
// boundary line, preferring the b side (the file's current name after
// a rename). Falls back to the raw line when the format surprises us —
// better an ugly header than a missing one.
func diffGitPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if idx := strings.LastIndex(rest, " b/"); idx >= 0 {
		return rest[idx+len(" b/"):]
	}
	return rest
}
