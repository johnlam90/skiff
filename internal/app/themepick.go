// =============================================================================
// File: internal/app/themepick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// themepick.go owns theme selection. The picker is a finder-style modal
// with one crucial property: it previews live. Moving the highlight —
// arrows, mouse hover, or narrowing the filter — restyles the entire
// editor under the modal instantly, because a theme is a thing you
// look at, not a name you guess at. Enter (or a click) keeps the
// highlighted theme and persists it to config.json's "theme" key;
// Esc (or a click outside) puts the original theme back.

package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/spiceconfig"
	"github.com/johnlam90/skiff/internal/theme"
)

// themePickMaxVisible caps the list height so the modal leaves editor
// visible around it — the editor *is* the preview.
const themePickMaxVisible = 16

// applyTheme switches the live palette to the registry entry id.
// Every surface reads a.theme at draw time, so the swap is: replace the
// struct, retint the terminal default style, and force every tab to
// re-tokenise (highlight styles bake the palette in). persist=false is
// the preview/startup path.
func (a *App) applyTheme(id string, persist bool) {
	th, ok := theme.ByID(id)
	if !ok {
		a.flash("Unknown theme " + id + " — using " + theme.DefaultID)
		id = theme.DefaultID
		th, _ = theme.ByID(id)
	}
	a.theme = th
	a.themeID = id
	a.screen.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	for _, t := range a.tabs {
		t.StyleStale = true
	}
	if !persist {
		return
	}
	if err := spiceconfig.SetTheme(spiceconfig.DefaultPath(), id); err != nil {
		a.flash("Theme set for this session — saving failed: " + err.Error())
		return
	}
	a.flash("Theme: " + themeDisplayName(id))
}

// themeDisplayName maps a registry id to its picker label.
func themeDisplayName(id string) string {
	for _, e := range theme.List() {
		if e.ID == id {
			return e.Name
		}
	}
	return id
}

// currentThemeID returns the active theme's id, defaulting when the
// app predates the field (tests that build App literals directly).
func (a *App) currentThemeID() string {
	if a.themeID == "" {
		return theme.DefaultID
	}
	return a.themeID
}

// menuTheme is the ≡ → Theme… entry point.
func (a *App) menuTheme() {
	a.closeMenu()
	a.openThemePick()
}

// openThemePick opens the live-preview picker with the highlight on
// the active theme, remembering it for Esc-revert.
func (a *App) openThemePick() {
	a.closeAllModals()
	a.themePickOpen = true
	a.themePickQuery = nil
	a.themePickCursor = 0
	a.themePickScroll = 0
	a.themePickOriginal = a.currentThemeID()
	a.themePickSelected = 0
	for i, e := range a.themePickEntries() {
		if e.ID == a.themePickOriginal {
			a.themePickSelected = i
			break
		}
	}
	a.ensureThemeRowVisible()
}

// themePickEntries returns the registry filtered by the query —
// case-insensitive substring on the display name or id, so "cat"
// narrows to the Catppuccins without exact spelling.
func (a *App) themePickEntries() []theme.Entry {
	all := theme.List()
	q := strings.ToLower(strings.TrimSpace(string(a.themePickQuery)))
	if q == "" {
		return all
	}
	var out []theme.Entry
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(e.ID, q) {
			out = append(out, e)
		}
	}
	return out
}

// themePickPreview applies the highlighted entry without persisting —
// the whole point of the picker. No-op when the filter has no matches.
func (a *App) themePickPreview() {
	entries := a.themePickEntries()
	if len(entries) == 0 {
		return
	}
	if a.themePickSelected >= len(entries) {
		a.themePickSelected = len(entries) - 1
	}
	if a.themePickSelected < 0 {
		a.themePickSelected = 0
	}
	a.applyTheme(entries[a.themePickSelected].ID, false)
}

// confirmThemePick keeps the previewed theme: persist and close.
func (a *App) confirmThemePick() {
	entries := a.themePickEntries()
	if len(entries) > 0 && a.themePickSelected < len(entries) {
		a.applyTheme(entries[a.themePickSelected].ID, true)
	}
	a.themePickOpen = false
	a.themePickQuery = nil
}

// cancelThemePick reverts to the theme the picker opened with and
// closes — Esc means "never mind", including the preview.
func (a *App) cancelThemePick() {
	a.revertThemePreview()
	a.themePickOpen = false
	a.themePickQuery = nil
}

// revertThemePreview restores the pre-picker theme if a preview is
// showing. Called by cancel and by closeAllModals, so another modal
// opening over the picker can't strand a half-chosen theme.
func (a *App) revertThemePreview() {
	if a.themePickOpen && a.themeID != a.themePickOriginal {
		a.applyTheme(a.themePickOriginal, false)
	}
}

// themePickMove shifts the highlight and previews the new row.
func (a *App) themePickMove(delta int) {
	entries := a.themePickEntries()
	if len(entries) == 0 {
		return
	}
	a.themePickSelected += delta
	if a.themePickSelected < 0 {
		a.themePickSelected = 0
	}
	if a.themePickSelected >= len(entries) {
		a.themePickSelected = len(entries) - 1
	}
	a.ensureThemeRowVisible()
	a.themePickPreview()
}

// handleThemePickKey owns the keyboard while the picker is open:
// arrows preview, Enter keeps, Esc reverts, anything text-ish narrows
// the filter (and previews the first hit).
func (a *App) handleThemePickKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.cancelThemePick()
	case tcell.KeyEnter:
		a.confirmThemePick()
	case tcell.KeyUp:
		a.themePickMove(-1)
	case tcell.KeyDown:
		a.themePickMove(1)
	case tcell.KeyPgUp:
		a.themePickMove(-themePickMaxVisible)
	case tcell.KeyPgDn:
		a.themePickMove(themePickMaxVisible)
	case tcell.KeyLeft:
		if a.themePickCursor > 0 {
			a.themePickCursor--
		}
	case tcell.KeyRight:
		if a.themePickCursor < len(a.themePickQuery) {
			a.themePickCursor++
		}
	case tcell.KeyHome:
		a.themePickCursor = 0
	case tcell.KeyEnd:
		a.themePickCursor = len(a.themePickQuery)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.themePickCursor > 0 {
			a.themePickQuery = append(a.themePickQuery[:a.themePickCursor-1], a.themePickQuery[a.themePickCursor:]...)
			a.themePickCursor--
			a.themePickFilterChanged()
		}
	case tcell.KeyDelete:
		if a.themePickCursor < len(a.themePickQuery) {
			a.themePickQuery = append(a.themePickQuery[:a.themePickCursor], a.themePickQuery[a.themePickCursor+1:]...)
			a.themePickFilterChanged()
		}
	case tcell.KeyRune:
		r := ev.Rune()
		if r < 0x20 {
			return
		}
		next := make([]rune, 0, len(a.themePickQuery)+1)
		next = append(next, a.themePickQuery[:a.themePickCursor]...)
		next = append(next, r)
		next = append(next, a.themePickQuery[a.themePickCursor:]...)
		a.themePickQuery = next
		a.themePickCursor++
		a.themePickFilterChanged()
	}
}

// themePickFilterChanged resets the highlight to the first match and
// previews it — narrowing to "nord" should *show* Nord, not wait for
// an arrow key.
func (a *App) themePickFilterChanged() {
	a.themePickSelected = 0
	a.themePickScroll = 0
	a.themePickPreview()
}

// handleThemePickMouse: hovering a row previews it (mouse-first,
// like arrowing), clicking keeps it, clicking outside cancels, the
// wheel scrolls the list.
func (a *App) handleThemePickMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		a.themePickScroll -= 3
		a.clampThemeScroll()
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.themePickScroll += 3
		a.clampThemeScroll()
		return
	}
	idx, inside := a.themePickRowAt(x, y)
	if btn&tcell.Button1 != 0 {
		if idx >= 0 {
			a.themePickSelected = idx
			a.confirmThemePick()
			return
		}
		if !inside {
			a.cancelThemePick()
		}
		return
	}
	// Motion: hover-preview, only when the pointer is actually on a row
	// so wiggling over the editor doesn't flicker the palette.
	if idx >= 0 && idx != a.themePickSelected {
		a.themePickSelected = idx
		a.themePickPreview()
	}
}

// themePickRowAt maps screen coordinates to a filtered-entry index
// (-1 when not on a row); inside reports whether the point is anywhere
// within the modal.
func (a *App) themePickRowAt(x, y int) (idx int, inside bool) {
	mx, my, mw, mh := a.themePickRect()
	inside = x >= mx && x < mx+mw && y >= my && y < my+mh
	if !inside {
		return -1, false
	}
	rowsStart := my + 4
	i := a.themePickScroll + (y - rowsStart)
	if y < rowsStart || y >= my+mh-1 || i < 0 || i >= len(a.themePickEntries()) {
		return -1, true
	}
	return i, true
}

// themePickVisibleRows returns how many list rows the modal shows.
func (a *App) themePickVisibleRows() int {
	_, _, _, mh := a.themePickRect()
	rows := mh - 5 // borders, title, divider, input
	if rows < 1 {
		rows = 1
	}
	return rows
}

// ensureThemeRowVisible keeps the highlight inside the list viewport.
func (a *App) ensureThemeRowVisible() {
	rows := a.themePickVisibleRows()
	if a.themePickSelected < a.themePickScroll {
		a.themePickScroll = a.themePickSelected
	}
	if a.themePickSelected >= a.themePickScroll+rows {
		a.themePickScroll = a.themePickSelected - rows + 1
	}
	a.clampThemeScroll()
}

// clampThemeScroll keeps the scroll offset inside the filtered list.
func (a *App) clampThemeScroll() {
	max := len(a.themePickEntries()) - a.themePickVisibleRows()
	if max < 0 {
		max = 0
	}
	if a.themePickScroll > max {
		a.themePickScroll = max
	}
	if a.themePickScroll < 0 {
		a.themePickScroll = 0
	}
}

// themePickRect sizes the modal: narrow and anchored in the upper
// third (finder-style), so plenty of editor stays visible around it —
// the editor is the preview.
func (a *App) themePickRect() (x, y, w, h int) {
	w = 44
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 26 {
		w = 26
	}
	visible := len(theme.List())
	if visible > themePickMaxVisible {
		visible = themePickMaxVisible
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

// drawThemePick paints the picker. Layout (relY):
//
//	0     top border
//	1     title — " Theme — previews live        esc"
//	2     divider
//	3     filter input
//	4..N  theme rows (● = active, "light" tag on light palettes)
//	N+1   bottom border
func (a *App) drawThemePick() {
	if !a.themePickOpen {
		return
	}
	mx, my, mw, mh := a.themePickRect()
	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	fillRect(a.screen, mx, my, mw, mh, bgStyle)
	drawBorder(a.screen, mx, my, mw, mh, borderStyle)
	drawHDivider(a.screen, mx, my+2, mw, borderStyle)

	drawAt(a.screen, mx+1, my+1, " Theme — previews live", titleStyle)
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
	if len(a.themePickQuery) == 0 {
		drawAt(a.screen, fieldStart, my+3, "type to filter…", tcell.StyleDefault.Background(inputBg).Foreground(a.theme.Subtle))
	}
	for i, r := range a.themePickQuery {
		if fieldStart+i > fieldEnd {
			break
		}
		a.screen.SetContent(fieldStart+i, my+3, r, nil, inputStyle)
	}
	caret := fieldStart + a.themePickCursor
	if caret >= fieldStart && caret <= fieldEnd {
		a.screen.ShowCursor(caret, my+3)
	}

	entries := a.themePickEntries()
	rows := a.themePickVisibleRows()
	rowsStart := my + 4
	for i := 0; i < rows; i++ {
		ry := rowsStart + i
		idx := a.themePickScroll + i
		if idx >= len(entries) {
			continue
		}
		a.drawThemePickRow(mx, ry, mw, entries[idx], idx == a.themePickSelected)
	}
	if len(entries) == 0 {
		drawAt(a.screen, mx+2, rowsStart, "no themes match", mutedStyle)
	}
}

// drawThemePickRow paints one theme row: an ● on the currently applied
// theme, the display name, and a dimmed "light" tag on light palettes
// so the two families scan apart.
func (a *App) drawThemePickRow(mx, ry, mw int, e theme.Entry, selected bool) {
	bg := a.theme.LineHL
	fg := a.theme.Text
	if selected {
		bg = a.theme.Selection
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(fg)
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}
	if e.ID == a.currentThemeID() {
		a.screen.SetContent(mx+2, ry, '●', nil, rowStyle.Foreground(a.theme.Accent))
	}
	style := rowStyle
	if selected {
		style = style.Bold(true)
	}
	drawClipped(a.screen, mx+4, ry, mw-12, e.Name, style)
	if isLightTheme(e) {
		tag := "light"
		drawAt(a.screen, mx+mw-2-runeLen(tag), ry, tag, rowStyle.Foreground(a.theme.Muted))
	}
}

// isLightTheme classifies a palette by its background: contrast vs
// pure black above ~9:1 means a luminous background — a light theme.
func isLightTheme(e theme.Entry) bool {
	return theme.ContrastRatio(e.Build().BG, tcell.NewRGBColor(0, 0, 0)) > 9
}
