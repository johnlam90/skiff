// =============================================================================
// File: internal/overlay/pick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
	"github.com/johnlam90/skiff/internal/theme"
)

// pickMaxVisible caps the list height so the modal stays compact (and,
// for previewing pickers, leaves the editor visible around it).
const pickMaxVisible = 16

// PickItem is one selectable row of a Pick.
type PickItem struct {
	Label   string
	Tag     string // dimmed right-aligned annotation ("light", "remote")
	Current bool   // wears the ● marker
}

// Pick is the generic pick-one-of-N overlay: a finder-style list with
// type-to-filter, arrow/hover highlight, Enter/click to choose, and
// Esc/outside-click to cancel. Behavior hooks make it fit different
// jobs — the theme picker adds a live-preview OnMove hook and a revert
// OnCancel; the branch pickers just take OnPick. It sits in the upper
// third of the screen so the editor stays visible around it.
type Pick struct {
	Title  string
	Items  []PickItem
	Filter Field
	// Selected is the highlight index into the filtered view.
	Selected int
	// scroll is the first visible filtered row.
	scroll int
	Theme  theme.Theme

	Size  func() (w, h int)
	Close func()
	// OnPick receives the index into Items (never the filtered view).
	OnPick func(int)
	// OnMove fires whenever the highlight lands on a row — live
	// preview. Receives the Items index.
	OnMove func(int)
	// OnCancel fires on dismissal without a pick, after any preview may
	// have run — revert hooks live here.
	OnCancel func()
}

// Init snaps the highlight onto the Current item and scrolls it into
// view — called by the opener once Items are set.
func (p *Pick) Init() {
	p.Selected = 0
	for i, it := range p.Items {
		if it.Current {
			p.Selected = i
			break
		}
	}
	p.ensureRowVisible()
}

// Filtered returns the indexes of items matching the query —
// case-insensitive substring on the label.
func (p *Pick) Filtered() []int {
	q := strings.ToLower(strings.TrimSpace(p.Filter.Text()))
	out := make([]int, 0, len(p.Items))
	for i, it := range p.Items {
		if q == "" || strings.Contains(strings.ToLower(it.Label), q) {
			out = append(out, i)
		}
	}
	return out
}

// Confirm fires OnPick for the highlighted row and closes; with no
// matching row it cancels instead.
func (p *Pick) Confirm() {
	filtered := p.Filtered()
	onPick := p.OnPick
	if len(filtered) == 0 || p.Selected >= len(filtered) {
		p.Cancel()
		return
	}
	idx := filtered[p.Selected]
	p.Close()
	if onPick != nil {
		onPick(idx)
	}
}

// Cancel fires OnCancel (revert hooks live there) and closes —
// capture-then-close like every overlay.
func (p *Pick) Cancel() {
	onCancel := p.OnCancel
	p.Close()
	if onCancel != nil {
		onCancel()
	}
}

// moved runs after any highlight change: clamp, keep the row on screen,
// and fire the OnMove preview hook.
func (p *Pick) moved() {
	filtered := p.Filtered()
	if len(filtered) == 0 {
		return
	}
	if p.Selected >= len(filtered) {
		p.Selected = len(filtered) - 1
	}
	if p.Selected < 0 {
		p.Selected = 0
	}
	p.ensureRowVisible()
	if p.OnMove != nil {
		p.OnMove(filtered[p.Selected])
	}
}

// filterChanged snaps the highlight to the first match and fires the
// preview hook — narrowing to one row should show its effect without
// waiting for an arrow key.
func (p *Pick) filterChanged() {
	p.Selected = 0
	p.scroll = 0
	p.moved()
}

// HandleKey owns the keyboard: Esc cancels, Enter confirms, vertical
// keys move the highlight, and everything else edits the filter field —
// an edit that changes the query re-runs the filter.
func (p *Pick) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		p.Cancel()
		return
	case tcell.KeyEnter:
		p.Confirm()
		return
	case tcell.KeyUp:
		p.Selected--
		p.moved()
		return
	case tcell.KeyDown:
		p.Selected++
		p.moved()
		return
	case tcell.KeyPgUp:
		p.Selected -= pickMaxVisible
		p.moved()
		return
	case tcell.KeyPgDn:
		p.Selected += pickMaxVisible
		p.moved()
		return
	}
	// Value length changes exactly when an edit changed the query —
	// cursor motion keeps it, insert/delete moves it.
	before := len(p.Filter.Value)
	p.Filter.HandleKey(ev)
	if len(p.Filter.Value) != before {
		p.filterChanged()
	}
}

// HandleMouse: hover moves the highlight (with preview), click picks,
// outside-click cancels, wheel scrolls — except on the scroll
// indicator's column, which the bar claims for itself.
func (p *Pick) HandleMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		p.scroll -= 3
		p.clampScroll()
		return
	}
	if btn&tcell.WheelDown != 0 {
		p.scroll += 3
		p.clampScroll()
		return
	}
	// Claimed before rowAt: without this a press on the bar would fall
	// through and *pick* whichever row sits behind the thumb, which is
	// the worst possible answer to "let me see further down the list".
	// The hit is false whenever no bar is drawn, so a list that fits
	// keeps the column as ordinary row surface.
	if b := p.bar(p.rect()); b.hit(x, y) {
		if btn&tcell.Button1 != 0 {
			p.scroll = b.target(y)
		}
		return
	}
	idx, inside := p.rowAt(x, y)
	if btn&tcell.Button1 != 0 {
		if idx >= 0 {
			p.Selected = idx
			p.Confirm()
			return
		}
		if !inside {
			p.Cancel()
		}
		return
	}
	if idx >= 0 && idx != p.Selected {
		p.Selected = idx
		p.moved()
	}
}

// rowAt maps screen coordinates to a filtered-row index (-1 when not on
// a row); inside reports containment in the modal.
func (p *Pick) rowAt(x, y int) (idx int, inside bool) {
	r := p.rect()
	inside = r.Contains(x, y)
	if !inside {
		return -1, false
	}
	rowsStart := r.Y + 4
	i := p.scroll + (y - rowsStart)
	if y < rowsStart || y >= r.Y+r.H-1 || i < 0 || i >= len(p.Filtered()) {
		return -1, true
	}
	return i, true
}

// visibleRows returns how many list rows the modal shows.
func (p *Pick) visibleRows() int {
	r := p.rect()
	rows := r.H - pickChromeRows
	if rows < 1 {
		rows = 1
	}
	return rows
}

// ensureRowVisible keeps the highlight inside the viewport.
func (p *Pick) ensureRowVisible() {
	rows := p.visibleRows()
	if p.Selected < p.scroll {
		p.scroll = p.Selected
	}
	if p.Selected >= p.scroll+rows {
		p.scroll = p.Selected - rows + 1
	}
	p.clampScroll()
}

// clampScroll keeps the scroll offset inside the filtered list.
func (p *Pick) clampScroll() {
	max := len(p.Filtered()) - p.visibleRows()
	if max < 0 {
		max = 0
	}
	if p.scroll > max {
		p.scroll = max
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

// bar describes the list's scroll indicator inside frame r. total is
// the FILTERED length, not len(Items): the filtered view is the list
// the user is scrolling, so narrowing the query has to shrink the bar
// with it — and retire it entirely once the matches fit.
func (p *Pick) bar(r Rect) bodyBar {
	return bodyBar{
		x:      barColumn(r),
		top:    r.Y + 4,
		viewH:  p.visibleRows(),
		total:  len(p.Filtered()),
		scroll: p.scroll,
	}
}

// Rect exposes the live geometry for callers and tests that hit-test
// against the pick from outside the package.
func (p *Pick) Rect() Rect { return p.rect() }

// pickMinWidth is the narrowest frame the list is still readable in:
// the border and pad on each side plus about twenty cells of label.
// Below it the modal takes the terminal instead, because a frame wider
// than the screen loses its right border and every row's tag column.
const pickMinWidth = 26

// pickChromeRows is the non-list height: border, title, divider, the
// filter input, and the bottom border.
const pickChromeRows = 5

// rect sizes the modal: narrow, anchored in the upper third so the
// editor stays visible around it (preview hooks need that). Both
// dimensions are clamped to the terminal — the minimum width is a
// preference, not a promise, and on a phone-sized screen the screen
// wins.
func (p *Pick) rect() Rect {
	scrW, scrH := p.Size()
	w := 44
	if w > scrW-4 {
		w = scrW - 4
	}
	if w < pickMinWidth {
		w = pickMinWidth
	}
	w = fit(w, scrW)
	visible := len(p.Items)
	if visible > pickMaxVisible {
		visible = pickMaxVisible
	}
	if visible < 1 {
		visible = 1
	}
	h := visible + pickChromeRows
	if h > scrH-2 {
		h = scrH - 2
	}
	// One list row below the chrome, always: a frame with no room for a
	// single match is a frame that answers nothing.
	if h < pickChromeRows+1 {
		h = pickChromeRows + 1
	}
	h = fit(h, scrH)
	x := (scrW - w) / 2
	y := (scrH - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return Rect{X: x, Y: y, W: w, H: h}
}

// Draw paints the pick: frame, filter field with its placeholder, and
// the windowed rows — ● on the current item, dimmed right-aligned tags,
// the highlight on the selection color.
func (p *Pick) Draw(scr tcell.Screen) {
	r := p.rect()
	th := p.Theme
	DrawFrame(scr, r, p.Title, th)

	bg := th.LineHL
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)

	inputStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	fieldStart := r.X + 3
	fieldW := (r.X + r.W - 3) - fieldStart + 1
	p.Filter.Draw(scr, fieldStart, r.Y+3, fieldW, inputStyle, true)
	if len(p.Filter.Value) == 0 {
		drawText(scr, fieldStart, r.Y+3, fieldW, "type to filter…",
			tcell.StyleDefault.Background(th.BG).Foreground(th.Subtle))
	}

	filtered := p.Filtered()
	rows := p.visibleRows()
	rowsStart := r.Y + 4
	for i := 0; i < rows; i++ {
		idx := p.scroll + i
		if idx >= len(filtered) {
			continue
		}
		p.drawRow(scr, r, rowsStart+i, p.Items[filtered[idx]], idx == p.Selected)
	}
	if len(filtered) == 0 {
		drawText(scr, r.X+2, rowsStart, r.W-4, "no matches", mutedStyle)
	}
	// After the rows: drawRow fills the frame's inner width, padding
	// column included, so the bar has to land on top of it.
	p.bar(r).draw(scr, th)
}

// drawRow paints one row: ● on the current item, the label, and a
// dimmed right-aligned tag.
func (p *Pick) drawRow(scr tcell.Screen, r Rect, ry int, it PickItem, selected bool) {
	th := p.Theme
	bg := th.LineHL
	if selected {
		bg = th.Selection
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	for cx := r.X + 1; cx < r.X+r.W-1; cx++ {
		scr.SetContent(cx, ry, ' ', nil, rowStyle)
	}
	if it.Current {
		scr.SetContent(r.X+2, ry, '●', nil, rowStyle.Foreground(th.Accent))
	}
	style := rowStyle
	if selected {
		style = style.Bold(true)
	}
	drawClippedText(scr, r.X+4, ry, r.W-6-runeLen(it.Tag), it.Label, style)
	if it.Tag != "" {
		drawText(scr, r.X+r.W-2-runeLen(it.Tag), ry, runeLen(it.Tag), it.Tag, rowStyle.Foreground(th.Muted))
	}
}

// drawClippedText writes s at (x, y) truncated to maxW cells,
// cluster-aware via textdraw so wide glyphs clip on cell boundaries.
func drawClippedText(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) {
	textdraw.DrawClipped(scr, x, y, maxW, s, st)
}
