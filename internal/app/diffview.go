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

// diffOverlay is the diff viewer's overlay: the raw and aligned diff,
// precomputed context styles, scroll state, and the button focus. It is
// bespoke — it needs the App for the git-panel diff walk, file opening,
// and theme — and its state dies with it.
type diffOverlay struct {
	app       *App
	title     string
	raw       []string
	rows      []diffRow
	rowStyles [][]tcell.Style
	openPath  string
	maxLen    int
	scroll    int
	scrollX   int
	// hover is the focused button: 0 = Close, 1 = Open file.
	hover int
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
	d := &diffOverlay{app: a, title: title, raw: raw, openPath: openPath}
	d.rows = parseSideBySideDiff(raw)
	annotateDiffSpans(d.rows)
	d.rowStyles = diffContextStyles(d.rows, langPath, a.theme)
	d.maxLen = diffLongestLine(d.raw, d.rows)
	if openPath != "" {
		d.hover = 1 // focus Open file so Enter is the fast path
	}
	a.overlays.Open(d)
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
func (d *diffOverlay) HandleKey(ev *tcell.EventKey) {
	a := d.app
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeAllModals()
	case tcell.KeyEnter:
		if d.hover == 1 && d.openPath != "" {
			d.openFileButton()
		} else {
			a.closeAllModals()
		}
	case tcell.KeyTab:
		if d.openPath != "" {
			d.hover = 1 - d.hover
		} else {
			a.closeAllModals()
		}
	case tcell.KeyLeft:
		d.scrollByH(-wheelCols)
	case tcell.KeyRight:
		d.scrollByH(wheelCols)
	case tcell.KeyUp:
		if a.diffWalk(-1) {
			return
		}
		d.scrollBy(-1)
	case tcell.KeyDown:
		if a.diffWalk(1) {
			return
		}
		d.scrollBy(1)
	case tcell.KeyPgUp:
		d.scrollBy(-d.visibleRows())
	case tcell.KeyPgDn:
		d.scrollBy(d.visibleRows())
	}
}

// handleDiffMouse mirrors the info modal's mouse contract: the wheel
// scrolls the body (Shift+wheel scrolls it sideways, the same gesture
// the editor uses for long lines), hover tracks the buttons, click
// activates, and a click outside the modal dismisses. Shift state
// comes from lastShiftAt — handleMouse stamps it before dispatching
// here, and the sticky window bridges terminals that split the
// modifier into its own event.
func (d *diffOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	a := d.app
	shift := !a.lastShiftAt.IsZero() && time.Since(a.lastShiftAt) < modifierStickyWindow
	if btn&tcell.WheelUp != 0 {
		if shift {
			d.scrollByH(-wheelCols)
		} else {
			d.scrollBy(-3)
		}
		return
	}
	if btn&tcell.WheelDown != 0 {
		if shift {
			d.scrollByH(wheelCols)
		} else {
			d.scrollBy(3)
		}
		return
	}
	if btn&tcell.WheelLeft != 0 {
		d.scrollByH(-wheelCols)
		return
	}
	if btn&tcell.WheelRight != 0 {
		d.scrollByH(wheelCols)
		return
	}
	mx, my, mw, mh := d.modalRect()
	openRect, closeRect := d.buttonRects()
	if openRect.contains(x, y) {
		d.hover = 1
	} else if closeRect.contains(x, y) {
		d.hover = 0
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeAllModals()
		return
	}
	if openRect.contains(x, y) {
		d.openFileButton()
		return
	}
	if closeRect.contains(x, y) {
		a.closeAllModals()
	}
}

// runDiffOpenFile fires the Open file button: close the modal first
// (capture-then-close, same as confirmYes) and jump to the first
// changed line of the file the diff was showing.
func (d *diffOverlay) openFileButton() {
	a := d.app
	path := d.openPath
	a.closeAllModals()
	if path != "" {
		a.openFileAtFirstChange(path)
	}
}

// scrollDiff nudges the body viewport by delta rows, clamped to the
// active layout's row count. Delta 0 is a pure re-clamp — the draw
// path calls it so a resize that flips the layout can't leave the
// scroll pointing past the end.
func (d *diffOverlay) scrollBy(delta int) {
	max := d.bodyCount() - d.visibleRows()
	if max < 0 {
		max = 0
	}
	d.scroll += delta
	if d.scroll > max {
		d.scroll = max
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

// scrollDiffH slides the body sideways by delta cells so long lines
// aren't dead-ends. Clamped so the longest line can't scroll more than
// a comfortable margin past the left edge — over-scrolling to a fully
// blank body is disorienting.
func (d *diffOverlay) scrollByH(delta int) {
	max := d.maxLen - 10
	if max < 0 {
		max = 0
	}
	d.scrollX += delta
	if d.scrollX > max {
		d.scrollX = max
	}
	if d.scrollX < 0 {
		d.scrollX = 0
	}
}

// diffSideBySide reports whether the current terminal is wide enough
// for the two-column layout. Checked at draw and scroll time rather
// than stored, so resizing the window adapts the view live.
func (d *diffOverlay) sideBySide() bool {
	_, _, mw, _ := d.modalRect()
	return mw-4 >= diffSideBySideMinBody && len(d.rows) > 0
}

// diffBodyCount returns how many body rows the active layout has:
// aligned rows side-by-side, raw diff lines unified.
func (d *diffOverlay) bodyCount() int {
	if d.sideBySide() {
		return len(d.rows)
	}
	return len(d.raw)
}

// diffVisibleRows returns how many body rows fit in the modal.
func (d *diffOverlay) visibleRows() int {
	_, _, _, mh := d.modalRect()
	return mh - 7
}

// diffModalRect returns the modal's on-screen rectangle. Diffs are the
// content, not a notification, so the modal takes most of the screen —
// which is also what makes room for two columns.
func (d *diffOverlay) modalRect() (x, y, w, h int) {
	a := d.app
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
	body := len(d.rows)
	if len(d.raw) > body {
		body = len(d.raw)
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
func (d *diffOverlay) buttonRects() (open, closeBtn btnRect) {
	mx, my, mw, mh := d.modalRect()
	btnY := my + mh - 3
	cw := runeLen("[ Close ]")
	if d.openPath == "" {
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
func (d *diffOverlay) Draw(scr tcell.Screen) {
	a := d.app
	mx, my, mw, mh := d.modalRect()
	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	fillRect(scr, mx, my, mw, mh, bgStyle)
	drawBorder(scr, mx, my, mw, mh, borderStyle)
	drawHDivider(scr, mx, my+2, mw, borderStyle)

	drawAt(scr, mx+1, my+1, " "+d.title, titleStyle)
	hint := "esc "
	drawAt(scr, mx+mw-1-runeLen(hint), my+1, hint, mutedStyle)

	d.scrollBy(0)
	bodyX, bodyW := mx+2, mw-4
	visible := d.visibleRows()
	if d.sideBySide() {
		for i := 0; i < visible; i++ {
			idx := d.scroll + i
			if idx >= len(d.rows) {
				break
			}
			d.drawRowSideBySide(scr, bodyX, my+3+i, bodyW, idx)
		}
	} else {
		for i := 0; i < visible; i++ {
			idx := d.scroll + i
			if idx >= len(d.raw) {
				break
			}
			// Style from the full line (the +/-/@ prefix), display from
			// the scrolled slice — so sideways scrolling never changes
			// a line's color.
			line := d.raw[idx]
			st := overlay.DiffLineStyle(a.theme, bg, line)
			drawAt(scr, bodyX, my+3+i, sliceRunes(line, d.scrollX, bodyW), st)
		}
	}

	openRect, closeRect := d.buttonRects()
	if d.openPath != "" {
		drawButton(scr, openRect.x, openRect.y, "[ Open file ]", bg, a.theme.Accent, d.hover == 1)
		drawButton(scr, closeRect.x, closeRect.y, "[ Close ]", bg, a.theme.Text, d.hover == 0)
	} else {
		drawButton(scr, closeRect.x, closeRect.y, "[ Close ]", bg, a.theme.Accent, true)
	}
	scr.HideCursor()
}

// drawDiffRowSideBySide paints one aligned row: two line-number
// gutters, two text columns, and a vertical rule between them. The
// changed side takes the Git add/delete color; blank sides stay empty
// so pure insertions and deletions read as gaps, the way VS Code
// renders them. idx indexes diffRows/diffRowStyles.
func (d *diffOverlay) drawRowSideBySide(scr tcell.Screen, x, y, w, idx int) {
	a := d.app
	row := d.rows[idx]
	bg := a.theme.LineHL
	if row.Kind == diffRowFile {
		fileStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
		drawAt(scr, x, y, sliceRunes("▸ "+row.Left, 0, w), fileStyle)
		return
	}
	if row.Kind == diffRowHunk {
		hunkStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft).Bold(true)
		drawAt(scr, x, y, sliceRunes(row.Left, 0, w), hunkStyle)
		return
	}

	leftW := (w - 3) / 2
	rightW := w - 3 - leftW
	rightX := x + leftW + 3
	sepStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	scr.SetContent(x+leftW+1, y, '│', nil, sepStyle)

	var ctx []tcell.Style
	if idx < len(d.rowStyles) {
		ctx = d.rowStyles[idx]
	}
	changed := row.Kind == diffRowChange
	d.drawSide(scr, x, y, leftW, row.LeftNo, row.Left, changed, a.theme.GitDeleted, row.LeftEmph, ctx)
	d.drawSide(scr, rightX, y, rightW, row.RightNo, row.Right, changed, a.theme.GitAdded, row.RightEmph, ctx)
}

// drawDiffSide paints one half of a side-by-side row: a muted line
// number (blank when the side has none), then the text rune by rune —
// Chroma styles for context lines, the Git change color for changed
// ones, and reverse video over the intra-line span that actually
// differs on paired modifications. The horizontal scroll offset slides
// the text only; gutters stay put.
func (d *diffOverlay) drawSide(scr tcell.Screen, x, y, w, lineNo int, text string, changed bool, changeColor tcell.Color, emph span, ctx []tcell.Style) {
	a := d.app
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
	drawAt(scr, x+diffNoGutter-1-runeLen(no)-1, y, no+" ", noStyle)
	tw := w - diffNoGutter
	if tw <= 0 {
		return
	}
	runes := []rune(text)
	for col := 0; col < tw; col++ {
		i := d.scrollX + col
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
		scr.SetContent(x+diffNoGutter+col, y, runes[i], nil, st)
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
