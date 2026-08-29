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
// The pairing itself is not here: internal/diff turns a patch into rows
// and this file paints them, so the same layout serves a `git diff` and
// a dirty buffer measured against disk. What stays is presentation —
// geometry, tints, syntax styles, scrolling, buttons.
//
// Reached from the Git panel (click a change row) and the editor's
// gutter markers (click a colored bar). [ Open file ] — when the file
// still exists — jumps to the first changed line; Esc, Close, or a
// click outside dismisses. Scroll with the wheel/trackpad or ↑ ↓
// PgUp PgDn.

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/diff"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/git"
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

// diffOverlay is the diff viewer's overlay: the paired rows and their
// unified rendering, precomputed context styles, scroll state, and the
// button focus. It is bespoke — it needs the App for the git-panel diff
// walk, file opening, and theme — and its state dies with it.
type diffOverlay struct {
	app   *App
	title string
	// unified is the patch written back out as diff text — the body of
	// the narrow layout, where there is no room for two columns.
	unified  []string
	rows     []diff.Row
	openPath string
	maxLen   int
	scroll   int
	scrollX  int
	// leftStyles/rightStyles are the per-side Chroma grids — each side
	// of a modified pair holds different text, so each gets its own
	// lex (see diffSideStyles). nil when highlighting is off.
	leftStyles, rightStyles [][]tcell.Style
	// tints are the palette's derived changed-row backgrounds; tinted
	// is false on low-color palettes, where drawSide keeps the legacy
	// colored-foreground painting instead.
	tints  theme.DiffTints
	tinted bool
	// hover is the focused button: 0 = Close, 1 = Open file.
	hover int
}

// diffLoadKind names which surface asked for a diff. The two differ
// only in what they do when git reports nothing: the menu entry flashes
// (the user asked a question and deserves the answer inline), while a
// gutter click opens an info modal — the marker promised a change, so
// its absence is surprising enough to warrant a dialog.
type diffLoadKind int

const (
	// diffLoadFile is ≡ → Diff this file.
	diffLoadFile diffLoadKind = iota
	// diffLoadHunk is a click on an editor gutter change marker.
	diffLoadHunk
)

// diffLoadResult is what a finished background diff load lands: the
// patch plus what the click that asked for it needs to present it.
// tabPath is the staleness guard the handler still owns — see
// handleDiffLoaded. The load's error rides beside it as the job's error:
// git's (or the parser's) reason when there is no usable patch, the
// difference between "clean" and "couldn't ask".
type diffLoadResult struct {
	kind    diffLoadKind
	title   string
	tabPath string
	patch   diff.Patch
}

// requestDiff runs load on the diffLoad job's goroutine and lands the
// result through handleDiffLoaded. Both callers are click paths and
// internal/git's read timeout is ten seconds, so running the diff inline
// meant one click on a gutter marker could freeze the editor for that
// long on a slow or network-mounted repo. load receives an immutable
// *git.Repo and must close over nothing but plain values captured here,
// on the main thread — it never touches App, tab, or tree state.
//
// The job is Supersede: every request spawns. A click is a discrete,
// low-rate gesture bounded by git's own read deadline, so the newest
// click winning matters more than capping concurrency; the job's
// generation check is what makes that safe.
func (a *App) requestDiff(kind diffLoadKind, title, tabPath string, load func(*git.Repo) (diff.Patch, error)) {
	repo := a.readRepo()
	// Acknowledge the click immediately: the diff is a git round trip
	// away, and a click that paints nothing reads as a dropped click.
	a.flash("Loading diff…")
	a.diffLoad.Start(func(context.Context) (diffLoadResult, error) {
		patch, err := load(repo)
		return diffLoadResult{kind: kind, title: title, tabPath: tabPath, patch: patch}, err
	})
}

// handleDiffLoaded opens the diff a background load produced, or
// explains its absence. Two things can have happened while git was
// working and both mean "drop it": an overlay went up (a diff yanking
// itself over a prompt the user is typing into is worse than no diff at
// all), or the active tab moved on, which would leave the diff
// describing a file that is no longer in front of the user. The third —
// a newer click superseded this one — never reaches here; the job drops
// it. An empty patch that came with an error is not "clean" — git could
// not answer, and the reason is what the user needs.
func (a *App) handleDiffLoaded(r diffLoadResult, err error) {
	if a.anyModalOpen() {
		return
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != r.tabPath {
		return
	}
	if r.patch.Empty() {
		switch {
		case err != nil:
			a.openInfo(r.title, []string{"Couldn't load the diff:", "", err.Error()})
		case r.kind == diffLoadHunk:
			a.openInfo("Git change", []string{"No git diff found for this line."})
		default:
			a.flash("No uncommitted changes in this file")
		}
		return
	}
	// The file is already open in front of the user, so the diff view
	// gets no [ Open file ] button — Close is the only way out.
	a.openDiffView(r.title, r.patch, "", r.tabPath)
}

// menuDiffFile is the ≡ menu entry that opens the active tab's own
// diff — the same view a Git panel row click gives, without having to
// leave the file to reach it. The git call itself runs off-thread; see
// requestDiff.
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
	title := filepath.Base(tab.Path)
	if a.tree != nil {
		if rel, ok := relFromRoot(tab.Path, a.tree.Root.Path); ok && rel != "." {
			title = filepath.ToSlash(rel)
		}
	}
	base, path, untracked := a.diffBase, tab.Path, kind == filetree.GitChangeAdded
	a.requestDiff(diffLoadFile, "Diff · "+title, tab.Path, func(repo *git.Repo) (diff.Patch, error) {
		return repoFileDiff(repo, base, path, untracked)
	})
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

// openDiffView shows the diff modal. p is the parsed diff — whether it
// came from `git diff` or from a buffer measured against what is on
// disk, the modal sees the same model: diff.Rows pairs it for the wide
// layout and diffBodyLines writes it back out for the narrow one.
// openPath, when non-empty, arms the [ Open file ] button with that
// file; deleted files pass "" and get a lone [ Close ]. langPath names
// the file for syntax highlighting — it may point at a path that no
// longer exists (deleted files); only its extension matters.
func (a *App) openDiffView(title string, p diff.Patch, openPath, langPath string) {
	a.closeAllModals()
	d := &diffOverlay{app: a, title: title, openPath: openPath}
	d.rows = diff.Rows(p)
	d.unified = diffBodyLines(p)
	d.leftStyles, d.rightStyles = diffSideStyles(d.rows, langPath, a.theme)
	d.tints, d.tinted = a.theme.DiffTints()
	d.maxLen = diffLongestLine(d.unified, d.rows)
	if openPath != "" {
		d.hover = 1 // focus Open file so Enter is the fast path
	}
	a.overlays.Open(d)
}

// diffSideStyles precomputes Chroma syntax styles for BOTH columns of
// the diff, one []tcell.Style per rune per row — context and changed
// rows alike, since changed rows now paint syntax colors over their
// tint. Each side is lexed as its own source (a modified pair holds
// different text left and right), joined so multi-line tokens survive;
// precomputing at open keeps the per-draw cost at zero — draws happen
// on every mouse-motion event. Hunk and file rows stay nil.
func diffSideStyles(rows []diff.Row, langPath string, th theme.Theme) (left, right [][]tcell.Style) {
	if langPath == "" || len(rows) == 0 || len(rows) > diffHighlightCap {
		return nil, nil
	}
	side := func(text func(diff.Row) string) [][]tcell.Style {
		src := make([]string, len(rows))
		for i, r := range rows {
			if r.Kind == diff.RowHunk || r.Kind == diff.RowFile {
				continue
			}
			src[i] = text(r)
		}
		grid := editor.Highlight(langPath, strings.Join(src, "\n"), th)
		styles := make([][]tcell.Style, len(rows))
		for i, r := range rows {
			if (r.Kind != diff.RowContext && r.Kind != diff.RowChange) || i >= len(grid) {
				continue
			}
			// Re-background onto the modal surface — Highlight styles
			// carry the editor's background. drawSide swaps in the row
			// tint for changed rows at draw time.
			row := make([]tcell.Style, len(grid[i]))
			for j, st := range grid[i] {
				row[j] = st.Background(th.LineHL)
			}
			styles[i] = row
		}
		return styles
	}
	left = side(func(r diff.Row) string {
		if r.LeftNo > 0 {
			return r.Left
		}
		return ""
	})
	right = side(func(r diff.Row) string {
		if r.RightNo > 0 {
			return r.Right
		}
		return ""
	})
	return left, right
}

// diffLongestLine returns the widest content line in either layout,
// bounding how far the diff can scroll horizontally.
func diffLongestLine(unified []string, rows []diff.Row) int {
	longest := 0
	for _, l := range unified {
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
// (capture-then-close, same as confirmYes) and jump to the diff's own
// first changed line. The target line comes from d.rows — the diff
// overlay's own paired rows — never from tab.GitLines: the
// tab's async gutter markers may still be nil right after open (plan
// 009 took the inline `git diff` off the tab-open path), and the diff
// overlay already has everything it needs without another git call.
func (d *diffOverlay) openFileButton() {
	a := d.app
	path := d.openPath
	line := diff.FirstChanged(d.rows)
	a.closeAllModals()
	if path != "" {
		a.OpenFileAtLine(path, line)
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
// aligned rows side-by-side, unified diff lines otherwise.
func (d *diffOverlay) bodyCount() int {
	if d.sideBySide() {
		return len(d.rows)
	}
	return len(d.unified)
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
	if len(d.unified) > body {
		body = len(d.unified)
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
			if idx >= len(d.unified) {
				break
			}
			// Style from the full line (the +/-/@ prefix), display from
			// the scrolled slice — so sideways scrolling never changes
			// a line's color.
			line := d.unified[idx]
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
// gutters, two text columns, and a vertical rule between them. Changed
// sides take their Git tint (or the legacy add/delete foreground on
// low-color palettes); blank sides stay empty so pure insertions and
// deletions read as gaps, the way VS Code renders them. idx indexes
// diffRows and both per-side style grids.
func (d *diffOverlay) drawRowSideBySide(scr tcell.Screen, x, y, w, idx int) {
	a := d.app
	row := d.rows[idx]
	bg := a.theme.LineHL
	if row.Kind == diff.RowFile {
		fileStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
		drawAt(scr, x, y, sliceRunes("▸ "+row.Left, 0, w), fileStyle)
		return
	}
	if row.Kind == diff.RowHunk {
		hunkStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft).Bold(true)
		drawAt(scr, x, y, sliceRunes(row.Left, 0, w), hunkStyle)
		return
	}

	leftW := (w - 3) / 2
	rightW := w - 3 - leftW
	rightX := x + leftW + 3
	sepStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	scr.SetContent(x+leftW+1, y, '│', nil, sepStyle)

	var lctx, rctx []tcell.Style
	if idx < len(d.leftStyles) {
		lctx = d.leftStyles[idx]
	}
	if idx < len(d.rightStyles) {
		rctx = d.rightStyles[idx]
	}
	changed := row.Kind == diff.RowChange
	d.drawSide(scr, x, y, leftW, row.LeftNo, row.Left, changed,
		a.theme.GitDeleted, d.tints.DelRow, d.tints.DelEmph, row.LeftEmph, lctx)
	d.drawSide(scr, rightX, y, rightW, row.RightNo, row.Right, changed,
		a.theme.GitAdded, d.tints.AddRow, d.tints.AddEmph, row.RightEmph, rctx)
}

// drawDiffSide paints one half of a side-by-side row: a muted line
// number (blank when the side has none), then the text rune by rune.
// A changed side gets the full-width row tint — gutter, text, and
// trailing pad — with Chroma syntax colors on top and the louder
// emphasis tint under the intra-line span that actually differs, so
// the diff reads like highlighted code on a wash. On low-color
// palettes (no tints) the legacy painting stands: the Git change color
// as foreground, reverse video over the emphasis span. The horizontal
// scroll offset slides the text only; gutters stay put.
func (d *diffOverlay) drawSide(scr tcell.Screen, x, y, w, lineNo int, text string, changed bool, changeColor, rowTint, emphTint tcell.Color, emph diff.Span, ctx []tcell.Style) {
	a := d.app
	if lineNo == 0 {
		return // blank side of a pure addition or deletion — the gap says "nothing was here"
	}
	bg := a.theme.LineHL
	tinted := changed && d.tinted
	if tinted {
		bg = rowTint
		fillRect(scr, x, y, w, 1, tcell.StyleDefault.Background(bg))
	}
	noStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	base := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	if changed && !tinted {
		base = base.Foreground(changeColor)
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
		if i < len(ctx) && (!changed || tinted) {
			st = ctx[i].Background(bg)
		}
		if changed && emph.End > emph.Start && i >= emph.Start && i < emph.End {
			if tinted {
				st = st.Background(emphTint)
			} else {
				st = st.Reverse(true)
			}
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

// diffBodyLines writes a patch back out as unified-diff text — the body
// of the narrow layout, which shows the diff the way `git diff` prints
// it when the terminal is too narrow for two columns. Nothing parses
// this back: it is the display form of a model that is already parsed,
// which is exactly the round trip this editor no longer makes.
//
// A file that carries paths gets git's framing above its hunks, because
// that is what the loader's own output looked like and what a reader of
// `git diff` expects. A buffer measured against disk carries none — the
// two sides are one file, and the modal title already names it.
func diffBodyLines(p diff.Patch) []string {
	var out []string
	for _, f := range p.Files {
		old, new := f.OldPath, f.NewPath
		if old == "" {
			old = f.Path()
		}
		if new == "" {
			new = f.Path()
		}
		if f.Path() != "" {
			out = append(out, "diff --git a/"+old+" b/"+new,
				"--- "+devNullOr(f.OldPath, "a/"),
				"+++ "+devNullOr(f.NewPath, "b/"))
		}
		if f.Binary {
			out = append(out, "Binary files a/"+old+" and b/"+new+" differ")
		}
		for _, h := range f.Hunks {
			out = append(out, h.Header())
			for _, ln := range h.Lines {
				out = append(out, string(ln.Kind.Marker())+ln.Text)
				if ln.NoNewline {
					out = append(out, `\ No newline at end of file`)
				}
			}
		}
	}
	return out
}

// devNullOr renders one `---`/`+++` operand. An empty path is the side
// that does not exist — a file being created or deleted — and git spells
// that /dev/null rather than leaving the operand blank.
func devNullOr(path, prefix string) string {
	if path == "" {
		return "/dev/null"
	}
	return prefix + path
}
