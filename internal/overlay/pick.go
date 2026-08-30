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
	// List owns the highlight and the scroll window over the FILTERED
	// view — sync pushes the current match count and frame height in
	// before anything reads it.
	List
	Title  string
	Items  []PickItem
	Filter Field
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
	p.sync()
	p.Select(0)
	for i, it := range p.Items {
		if it.Current {
			p.Select(i)
			break
		}
	}
	p.EnsureVisible()
}

// sync pushes the live shape of the list — how many rows the filter
// left and how many the frame can show — into the embedded List, and
// hands back the frame it measured. Every entry point calls it first,
// because both numbers move underneath the overlay: the match count on
// every keystroke, the window on every terminal resize.
func (p *Pick) sync() Rect {
	r := p.rect()
	rows := r.H - pickChromeRows
	if rows < 1 {
		rows = 1
	}
	p.SetLen(len(p.Filtered()))
	p.SetVisible(rows)
	return r
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
	if len(filtered) == 0 || p.Sel() >= len(filtered) {
		p.Cancel()
		return
	}
	idx := filtered[p.Sel()]
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
	p.sync()
	p.Clamp()
	p.EnsureVisible()
	if p.OnMove != nil {
		p.OnMove(filtered[p.Sel()])
	}
}

// filterChanged snaps the highlight to the first match and fires the
// preview hook — narrowing to one row should show its effect without
// waiting for an arrow key.
func (p *Pick) filterChanged() {
	p.sync()
	p.Select(0)
	// Clamp carries the empty-match case: with no rows moved() bails
	// before EnsureVisible could pull the window home, and a query that
	// matches nothing must not leave the last window's offset behind.
	p.Clamp()
	p.moved()
}

// HandleKey owns the keyboard: Esc cancels, Enter confirms, vertical
// keys move the highlight, and everything else edits the filter field —
// an edit that changes the query re-runs the filter.
func (p *Pick) HandleKey(ev *tcell.EventKey) {
	p.sync()
	switch ev.Key() {
	case tcell.KeyEsc:
		p.Cancel()
		return
	case tcell.KeyEnter:
		p.Confirm()
		return
	case tcell.KeyUp:
		p.Move(-1)
		p.moved()
		return
	case tcell.KeyDown:
		p.Move(1)
		p.moved()
		return
	case tcell.KeyPgUp:
		p.Move(-pickMaxVisible)
		p.moved()
		return
	case tcell.KeyPgDn:
		p.Move(pickMaxVisible)
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
	r := p.sync()
	if btn&tcell.WheelUp != 0 {
		p.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		p.ScrollBy(3)
		return
	}
	// Claimed before the row hit-test: without this a press on the bar
	// would fall through and *pick* whichever row sits behind the thumb,
	// which is the worst possible answer to "let me see further down the
	// list". The hit is false whenever no bar is drawn, so a list that
	// fits keeps the column as ordinary row surface.
	if b := p.bar(r); b.Hit(x, y) {
		if btn&tcell.Button1 != 0 {
			p.ScrollToBar(r.Y+4, y)
		}
		return
	}
	inside := r.Contains(x, y)
	idx := -1
	if inside {
		if i, ok := p.RowAt(r.Y+4, y); ok {
			idx = i
		}
	}
	if btn&tcell.Button1 != 0 {
		if idx >= 0 {
			p.Select(idx)
			p.Confirm()
			return
		}
		if !inside {
			p.Cancel()
		}
		return
	}
	if idx >= 0 && idx != p.Sel() {
		p.Select(idx)
		p.moved()
	}
}

// bar describes the list's scroll indicator inside frame r. The List it
// reads was synced against the FILTERED length, not len(Items): the
// filtered view is the list the user is scrolling, so narrowing the
// query shrinks the bar with it — and retires it entirely once the
// matches fit.
func (p *Pick) bar(r Rect) Bar { return p.List.Bar(BarColumn(r), r.Y+4) }

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
	r := p.sync()
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
	rowsStart := r.Y + 4
	for i := range p.Visible() {
		idx := p.Scroll() + i
		if idx >= len(filtered) {
			continue
		}
		p.drawRow(scr, r, rowsStart+i, p.Items[filtered[idx]], idx == p.Sel())
	}
	if len(filtered) == 0 {
		drawText(scr, r.X+2, rowsStart, r.W-4, "no matches", mutedStyle)
	}
	// After the rows: drawRow fills the frame's inner width, padding
	// column included, so the bar has to land on top of it.
	p.bar(r).Draw(scr, th)
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

// WantsMotion is true: hovering a row moves the highlight and fires the
// OnMove preview, which is how the theme picker previews a palette.
func (p *Pick) WantsMotion() bool { return true }

// Dismiss fires OnCancel without closing — the stack has already popped
// this overlay. It is the one prefab with something to undo on a
// teardown: OnMove may have previewed a theme, and OnCancel is the
// revert. Close is deliberately not called, so a teardown can never
// re-enter the stack it is already unwinding.
func (p *Pick) Dismiss() {
	if p.OnCancel != nil {
		p.OnCancel()
	}
}
