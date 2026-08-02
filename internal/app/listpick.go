// =============================================================================
// File: internal/app/listpick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// listpick.go is the generic pick-one-of-N modal: a finder-style list
// with type-to-filter, arrow/hover highlight, Enter/click to choose,
// Esc/outside-click to cancel. Behavior hooks make it fit different
// jobs — the theme picker adds a live-preview OnMove hook and a revert
// OnCancel; the branch pickers just take OnPick. Anything that would
// otherwise reach for a blind ←→ select belongs here instead.

package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// listPickMaxVisible caps the list height so the modal stays compact
// (and, for previewing pickers, leaves the editor visible around it).
const listPickMaxVisible = 16

// listPickItem is one selectable row.
type listPickItem struct {
	Label   string
	Tag     string // dimmed right-aligned annotation ("light", "remote")
	Current bool   // wears the ● marker
}

// openListPick opens the modal. onPick receives the index into items
// (never the filtered view); onMove (optional) fires whenever the
// highlight lands on a row — live preview; onCancel (optional) fires on
// dismissal without a pick, after any preview may have run.
func (a *App) openListPick(title string, items []listPickItem, onPick, onMove func(*App, int), onCancel func(*App)) {
	a.closeAllModals()
	a.listPickOpen = true
	a.listPickTitle = title
	a.listPickItems = items
	a.listPickQuery = nil
	a.listPickCursor = 0
	a.listPickScroll = 0
	a.listPickInputScroll = 0
	a.listPickOnPick = onPick
	a.listPickOnMove = onMove
	a.listPickOnCancel = onCancel
	a.listPickSelected = 0
	for i, it := range items {
		if it.Current {
			a.listPickSelected = i
			break
		}
	}
	a.ensureListRowVisible()
}

// listPickFiltered returns the indexes of items matching the query —
// case-insensitive substring on the label.
func (a *App) listPickFiltered() []int {
	q := strings.ToLower(strings.TrimSpace(string(a.listPickQuery)))
	out := make([]int, 0, len(a.listPickItems))
	for i, it := range a.listPickItems {
		if q == "" || strings.Contains(strings.ToLower(it.Label), q) {
			out = append(out, i)
		}
	}
	return out
}

// closeListPick tears the modal down without firing any hook.
func (a *App) closeListPick() {
	a.listPickOpen = false
	a.listPickItems = nil
	a.listPickQuery = nil
	a.listPickInputScroll = 0
	a.listPickOnPick = nil
	a.listPickOnMove = nil
	a.listPickOnCancel = nil
}

// adjustListPickInputScroll keeps the filter caret inside the field's
// visible window — same caret-tracking contract as adjustPromptScroll.
func (a *App) adjustListPickInputScroll(width int) {
	if width <= 0 {
		a.listPickInputScroll = 0
		return
	}
	if a.listPickCursor < a.listPickInputScroll {
		a.listPickInputScroll = a.listPickCursor
	}
	if a.listPickCursor >= a.listPickInputScroll+width {
		a.listPickInputScroll = a.listPickCursor - width + 1
	}
	if a.listPickInputScroll < 0 {
		a.listPickInputScroll = 0
	}
}

// confirmListPick fires OnPick for the highlighted row and closes.
func (a *App) confirmListPick() {
	filtered := a.listPickFiltered()
	onPick := a.listPickOnPick
	if len(filtered) == 0 || a.listPickSelected >= len(filtered) {
		a.cancelListPick()
		return
	}
	idx := filtered[a.listPickSelected]
	a.closeListPick()
	if onPick != nil {
		onPick(a, idx)
	}
}

// cancelListPick fires OnCancel (revert hooks live there) and closes.
func (a *App) cancelListPick() {
	onCancel := a.listPickOnCancel
	a.closeListPick()
	if onCancel != nil {
		onCancel(a)
	}
}

// listPickMoved runs after any highlight change: clamp, keep the row on
// screen, and fire the OnMove preview hook.
func (a *App) listPickMoved() {
	filtered := a.listPickFiltered()
	if len(filtered) == 0 {
		return
	}
	if a.listPickSelected >= len(filtered) {
		a.listPickSelected = len(filtered) - 1
	}
	if a.listPickSelected < 0 {
		a.listPickSelected = 0
	}
	a.ensureListRowVisible()
	if a.listPickOnMove != nil {
		a.listPickOnMove(a, filtered[a.listPickSelected])
	}
}

// handleListPickKey owns the keyboard while the modal is open.
func (a *App) handleListPickKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.cancelListPick()
	case tcell.KeyEnter:
		a.confirmListPick()
	case tcell.KeyUp:
		a.listPickSelected--
		a.listPickMoved()
	case tcell.KeyDown:
		a.listPickSelected++
		a.listPickMoved()
	case tcell.KeyPgUp:
		a.listPickSelected -= listPickMaxVisible
		a.listPickMoved()
	case tcell.KeyPgDn:
		a.listPickSelected += listPickMaxVisible
		a.listPickMoved()
	case tcell.KeyLeft:
		if a.listPickCursor > 0 {
			a.listPickCursor--
		}
	case tcell.KeyRight:
		if a.listPickCursor < len(a.listPickQuery) {
			a.listPickCursor++
		}
	case tcell.KeyHome:
		a.listPickCursor = 0
	case tcell.KeyEnd:
		a.listPickCursor = len(a.listPickQuery)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.listPickCursor > 0 {
			a.listPickQuery = append(a.listPickQuery[:a.listPickCursor-1], a.listPickQuery[a.listPickCursor:]...)
			a.listPickCursor--
			a.listPickFilterChanged()
		}
	case tcell.KeyDelete:
		if a.listPickCursor < len(a.listPickQuery) {
			a.listPickQuery = append(a.listPickQuery[:a.listPickCursor], a.listPickQuery[a.listPickCursor+1:]...)
			a.listPickFilterChanged()
		}
	case tcell.KeyRune:
		r := ev.Rune()
		if r < 0x20 {
			return
		}
		next := make([]rune, 0, len(a.listPickQuery)+1)
		next = append(next, a.listPickQuery[:a.listPickCursor]...)
		next = append(next, r)
		next = append(next, a.listPickQuery[a.listPickCursor:]...)
		a.listPickQuery = next
		a.listPickCursor++
		a.listPickFilterChanged()
	}
}

// listPickFilterChanged snaps the highlight to the first match and
// fires the preview hook — narrowing to one row should show its effect
// without waiting for an arrow key.
func (a *App) listPickFilterChanged() {
	a.listPickSelected = 0
	a.listPickScroll = 0
	a.listPickMoved()
}

// handleListPickMouse: hover moves the highlight (with preview), click
// picks, outside-click cancels, wheel scrolls.
func (a *App) handleListPickMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		a.listPickScroll -= 3
		a.clampListScroll()
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.listPickScroll += 3
		a.clampListScroll()
		return
	}
	idx, inside := a.listPickRowAt(x, y)
	if btn&tcell.Button1 != 0 {
		if idx >= 0 {
			a.listPickSelected = idx
			a.confirmListPick()
			return
		}
		if !inside {
			a.cancelListPick()
		}
		return
	}
	if idx >= 0 && idx != a.listPickSelected {
		a.listPickSelected = idx
		a.listPickMoved()
	}
}

// listPickRowAt maps screen coordinates to a filtered-row index
// (-1 when not on a row); inside reports containment in the modal.
func (a *App) listPickRowAt(x, y int) (idx int, inside bool) {
	mx, my, mw, mh := a.listPickRect()
	inside = x >= mx && x < mx+mw && y >= my && y < my+mh
	if !inside {
		return -1, false
	}
	rowsStart := my + 4
	i := a.listPickScroll + (y - rowsStart)
	if y < rowsStart || y >= my+mh-1 || i < 0 || i >= len(a.listPickFiltered()) {
		return -1, true
	}
	return i, true
}

// listPickVisibleRows returns how many list rows the modal shows.
func (a *App) listPickVisibleRows() int {
	_, _, _, mh := a.listPickRect()
	rows := mh - 5 // borders, title, divider, input
	if rows < 1 {
		rows = 1
	}
	return rows
}

// ensureListRowVisible keeps the highlight inside the viewport.
func (a *App) ensureListRowVisible() {
	rows := a.listPickVisibleRows()
	if a.listPickSelected < a.listPickScroll {
		a.listPickScroll = a.listPickSelected
	}
	if a.listPickSelected >= a.listPickScroll+rows {
		a.listPickScroll = a.listPickSelected - rows + 1
	}
	a.clampListScroll()
}

// clampListScroll keeps the scroll offset inside the filtered list.
func (a *App) clampListScroll() {
	max := len(a.listPickFiltered()) - a.listPickVisibleRows()
	if max < 0 {
		max = 0
	}
	if a.listPickScroll > max {
		a.listPickScroll = max
	}
	if a.listPickScroll < 0 {
		a.listPickScroll = 0
	}
}

// listPickRect sizes the modal: narrow, anchored in the upper third so
// the editor stays visible around it (preview hooks need that).
func (a *App) listPickRect() (x, y, w, h int) {
	w = 44
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 26 {
		w = 26
	}
	visible := len(a.listPickItems)
	if visible > listPickMaxVisible {
		visible = listPickMaxVisible
	}
	if visible < 1 {
		visible = 1
	}
	h = visible + 5
	if h > a.height-2 {
		h = a.height - 2
	}
	x = (a.width - w) / 2
	y = (a.height - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// drawListPick paints the modal. Layout (relY): border, title, divider,
// filter input, rows, border.
func (a *App) drawListPick() {
	if !a.listPickOpen {
		return
	}
	mx, my, mw, mh := a.listPickRect()
	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	fillRect(a.screen, mx, my, mw, mh, bgStyle)
	drawBorder(a.screen, mx, my, mw, mh, borderStyle)
	drawHDivider(a.screen, mx, my+2, mw, borderStyle)

	drawClipped(a.screen, mx+1, my+1, mw-6, " "+a.listPickTitle, titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-runeLen(hint), my+1, hint, mutedStyle)

	// Filter input row.
	inputBg := a.theme.BG
	inputStyle := tcell.StyleDefault.Background(inputBg).Foreground(a.theme.Text)
	fieldStart := mx + 3
	fieldEnd := mx + mw - 3
	for cx := fieldStart - 1; cx <= fieldEnd; cx++ {
		a.screen.SetContent(cx, my+3, ' ', nil, inputStyle)
	}
	if len(a.listPickQuery) == 0 {
		drawAt(a.screen, fieldStart, my+3, "type to filter…", tcell.StyleDefault.Background(inputBg).Foreground(a.theme.Subtle))
	}
	// Caret-tracking scroll window, so a query longer than the field
	// keeps the caret (and fresh keystrokes) visible.
	fieldW := fieldEnd - fieldStart + 1
	a.adjustListPickInputScroll(fieldW)
	for i := 0; i < fieldW; i++ {
		idx := a.listPickInputScroll + i
		if idx >= len(a.listPickQuery) {
			break
		}
		a.screen.SetContent(fieldStart+i, my+3, a.listPickQuery[idx], nil, inputStyle)
	}
	caret := fieldStart + (a.listPickCursor - a.listPickInputScroll)
	if caret >= fieldStart && caret <= fieldEnd {
		a.screen.ShowCursor(caret, my+3)
	}

	filtered := a.listPickFiltered()
	rows := a.listPickVisibleRows()
	rowsStart := my + 4
	for i := 0; i < rows; i++ {
		idx := a.listPickScroll + i
		if idx >= len(filtered) {
			continue
		}
		a.drawListPickRow(mx, rowsStart+i, mw, a.listPickItems[filtered[idx]], idx == a.listPickSelected)
	}
	if len(filtered) == 0 {
		drawAt(a.screen, mx+2, rowsStart, "no matches", mutedStyle)
	}
}

// drawListPickRow paints one row: ● on the current item, the label,
// and a dimmed right-aligned tag.
func (a *App) drawListPickRow(mx, ry, mw int, it listPickItem, selected bool) {
	bg := a.theme.LineHL
	if selected {
		bg = a.theme.Selection
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}
	if it.Current {
		a.screen.SetContent(mx+2, ry, '●', nil, rowStyle.Foreground(a.theme.Accent))
	}
	style := rowStyle
	if selected {
		style = style.Bold(true)
	}
	drawClipped(a.screen, mx+4, ry, mw-6-runeLen(it.Tag), it.Label, style)
	if it.Tag != "" {
		drawAt(a.screen, mx+mw-2-runeLen(it.Tag), ry, it.Tag, rowStyle.Foreground(a.theme.Muted))
	}
}
