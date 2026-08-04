// =============================================================================
// File: internal/app/menu.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// menu.go owns the action modal's behavior and rendering: opening and
// closing it, keyboard navigation, mouse hover/click routing, the scroll
// window a short terminal needs, and the draw pass for both the modal and
// the ≡ button that opens it.
//
// The modal's row and height layout comes from menudef.go's menuLayout,
// and its place in the input routing comes from the overlay stack (see
// overlays.go) — this file is the menu-specific half of both.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/version"
)

// menuModalRect returns the on-screen rectangle of the action modal,
// centered in the window. Height is derived from the current layout
// so adding custom actions grows the modal automatically — but it is
// clamped to the screen (one row of margin) so a short terminal can
// scroll the overflow instead of losing the bottom rows, Quit first.
func (a *App) menuModalRect() (x, y, w, h int) {
	w = modalWidth
	_, _, h = a.menuLayout()
	if maxH := a.height - 2; h > maxH {
		h = maxH
	}
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

// menuMaxScroll returns how many rows the menu content can scroll: the
// overflow between the natural layout height and the clamped modal
// height. Zero when the whole menu fits on screen.
func (a *App) menuMaxScroll() int {
	_, _, natural := a.menuLayout()
	_, _, _, h := a.menuModalRect()
	return natural - h
}

// clampMenuScroll bounds menuScroll to [0, menuMaxScroll] so the content
// region can never scroll past its first or last row.
func (a *App) clampMenuScroll() {
	if maxS := a.menuMaxScroll(); a.menuScroll > maxS {
		a.menuScroll = maxS
	}
	if a.menuScroll < 0 {
		a.menuScroll = 0
	}
}

// ensureMenuRowVisible scrolls the menu so the item at idx sits inside
// the visible content region — the keyboard analogue of the editor's
// EnsureVisible, so arrow-key navigation can reach rows a short terminal
// pushed out of the frame.
func (a *App) ensureMenuRowVisible(idx int) {
	items, _, _ := a.menuLayout()
	if idx < 0 || idx >= len(items) {
		return
	}
	_, _, _, h := a.menuModalRect()
	relY := items[idx].relY
	// Visible virtual rows span [3+menuScroll, h-2+menuScroll].
	if relY < 3+a.menuScroll {
		a.menuScroll = relY - 3
	} else if relY > h-2+a.menuScroll {
		a.menuScroll = relY - (h - 2)
	}
	a.clampMenuScroll()
}

// handleMenuMouse processes mouse events while the action menu is open.
// Left-click outside the modal closes it; left-click on a row runs that
// row's action (if it is currently enabled). Wheel events scroll the
// content when the layout is taller than the terminal.
func (a *App) handleMenuMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		a.menuScroll--
		a.clampMenuScroll()
		// The rows just moved under the stationary pointer — recompute
		// the hover so the highlight tracks what a click would now hit
		// instead of going one row stale until the next mouse motion.
		a.updateMenuHover(x, y)
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.menuScroll++
		a.clampMenuScroll()
		a.updateMenuHover(x, y)
		return
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	mx, my, mw, mh := a.menuModalRect()
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeMenu()
		return
	}
	// Only the scrollable content region (below the title and its
	// divider, above the bottom border) can hold items; clicks on the
	// chrome rows stay inert even when scrolled rows share their
	// virtual position.
	if y < my+3 || y > my+mh-2 {
		return
	}
	relY := y - my + a.menuScroll
	items, _, _ := a.menuLayout()
	for _, item := range items {
		if item.relY != relY {
			continue
		}
		if item.enabled(a) {
			item.action(a)
		}
		return
	}
}

// openMenu shows the action modal. While it is up, the editor doesn't
// receive typed keys, and clicks outside the modal dismiss it. We pre-
// select the first enabled row so Down/Up/Enter keyboard navigation has
// somewhere sensible to start.
func (a *App) openMenu() {
	a.closeAllModals()
	a.menuOpen = true
	a.overlays.Open(menuOverlay{a})
	a.menuScroll = 0
	a.menuMoveSelection(1)
}

// menuMoveSelection advances hoveredMenuRow to the next (dir=+1) or
// previous (dir=-1) enabled menu item, wrapping around at the ends so the
// list feels continuous. Disabled items and dividers are skipped. If no
// item is currently enabled hoveredMenuRow stays -1.
func (a *App) menuMoveSelection(dir int) {
	items, _, _ := a.menuLayout()
	n := len(items)
	if n == 0 {
		return
	}
	start := a.hoveredMenuRow
	if start < 0 {
		// No current selection — start one step before the first row (for
		// Down) or one past the last (for Up) so the loop lands on the
		// first/last enabled item.
		if dir > 0 {
			start = -1
		} else {
			start = n
		}
	}
	for i := 1; i <= n; i++ {
		idx := ((start+dir*i)%n + n) % n
		if items[idx].enabled(a) {
			a.hoveredMenuRow = idx
			a.ensureMenuRowVisible(idx)
			return
		}
	}
	a.hoveredMenuRow = -1
}

// handleMenuKey owns the keyboard while the action menu is up. Only the
// navigation keys do anything — editing keys are blocked — but leader
// runes still fire their actions (the menu doubles as the shortcut
// cheat-sheet, so the shortcuts must work while it shows). Pasted
// content is ignored outright: with no text focus behind the menu,
// dispatching pasted runes as leader shortcuts would fire arbitrary
// actions. Esc and Alt+Esc close the menu — the same keys that open it.
func (a *App) handleMenuKey(ev *tcell.EventKey) {
	if a.pasting {
		return
	}
	if ev.Modifiers()&tcell.ModAlt != 0 {
		a.lastEscape = time.Time{}
		switch ev.Key() {
		case tcell.KeyEsc:
			a.closeMenu()
			return
		case tcell.KeyRune:
			if action := leaderActionFor(ev.Rune()); action != nil {
				action(a)
			}
			return
		}
	}
	if ev.Key() == tcell.KeyEsc {
		a.closeMenu()
		a.lastEscape = time.Time{}
		return
	}
	if a.leaderWindowIntercept(ev) {
		return
	}
	a.lastEscape = time.Time{}
	if ev.Key() == tcell.KeyRune {
		if action := leaderActionFor(ev.Rune()); action != nil {
			action(a)
			return
		}
	}
	switch ev.Key() {
	case tcell.KeyDown:
		a.menuMoveSelection(1)
	case tcell.KeyUp:
		a.menuMoveSelection(-1)
	case tcell.KeyEnter:
		a.menuActivate()
	}
}

// menuActivate runs the currently-highlighted menu item, if any. It's the
// keyboard-Enter equivalent of clicking a row.
func (a *App) menuActivate() {
	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow < 0 || a.hoveredMenuRow >= len(items) {
		return
	}
	item := items[a.hoveredMenuRow]
	if !item.enabled(a) {
		return
	}
	item.action(a)
}

// closeMenu hides the action modal without running any action.
func (a *App) closeMenu() {
	a.menuOpen = false
	a.dropOverlay(menuOverlay{a})
	a.hoveredMenuRow = -1
	a.menuScroll = 0
}

// updateMenuHover sets hoveredMenuRow to the index of the enabled menu row
// at (x, y), or to -1 when the mouse is over a disabled row, a divider, the
// title, or anywhere outside the modal.
func (a *App) updateMenuHover(x, y int) {
	a.hoveredMenuRow = -1
	mx, my, mw, mh := a.menuModalRect()
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		return
	}
	// Chrome rows (title, divider, borders) never hover; the content
	// region maps through the scroll offset like the click hit-test.
	if y < my+3 || y > my+mh-2 {
		return
	}
	relY := y - my + a.menuScroll
	items, _, _ := a.menuLayout()
	for i, item := range items {
		if item.relY == relY && item.enabled(a) {
			a.hoveredMenuRow = i
			return
		}
	}
}

// drawMenuButton paints the ≡ icon in the leftmost cells of the tab bar.
// It's deliberately big and accent-coloured so it reads as a button.
func (a *App) drawMenuButton() {
	mx, my, mw, _ := a.menuButtonRect()
	bg := a.theme.SidebarBG
	fg := a.theme.Accent
	if a.menuOpen {
		// Visually press the button while the menu is up.
		bg = a.theme.Accent
		fg = a.theme.BG
	}
	style := tcell.StyleDefault.Background(bg).Foreground(fg).Bold(true)
	for cx := mx; cx < mx+mw; cx++ {
		a.screen.SetContent(cx, my, ' ', nil, style)
	}
	// Center the ≡ glyph in the button's mw cells.
	a.screen.SetContent(mx+mw/2, my, '≡', nil, style)
}

// drawMenu renders the action modal centered in the window. The
// item / divider / height layout comes from menuLayout so adding
// custom actions or new built-in groups doesn't require touching this
// function.
func (a *App) drawMenu() {
	mx, my, mw, mh := a.menuModalRect()
	items, dividers, _ := a.menuLayout()

	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	chevronStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft)

	// Fill the entire modal rect with the modal bg.
	for cy := my; cy < my+mh; cy++ {
		for cx := mx; cx < mx+mw; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Outer border.
	a.screen.SetContent(mx, my, '┌', nil, borderStyle)
	a.screen.SetContent(mx+mw-1, my, '┐', nil, borderStyle)
	a.screen.SetContent(mx, my+mh-1, '└', nil, borderStyle)
	a.screen.SetContent(mx+mw-1, my+mh-1, '┘', nil, borderStyle)
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, my, '─', nil, borderStyle)
		a.screen.SetContent(cx, my+mh-1, '─', nil, borderStyle)
	}
	for cy := my + 1; cy < my+mh-1; cy++ {
		a.screen.SetContent(mx, cy, '│', nil, borderStyle)
		a.screen.SetContent(mx+mw-1, cy, '│', nil, borderStyle)
	}

	// Horizontal dividers between action groups. The dy list comes from
	// menuLayout — including the always-on row under the title — so it
	// stays in sync with whatever rows are actually being drawn. The
	// title divider (relY 2) is fixed chrome; the rest live in the
	// scrollable content region and map through menuScroll, dropping
	// out when they leave the visible window.
	for _, dy := range dividers {
		cy := my + dy
		if dy > 2 {
			cy -= a.menuScroll
			if cy < my+3 || cy > my+mh-2 {
				continue
			}
		}
		a.screen.SetContent(mx, cy, '├', nil, borderStyle)
		a.screen.SetContent(mx+mw-1, cy, '┤', nil, borderStyle)
		for cx := mx + 1; cx < mx+mw-1; cx++ {
			a.screen.SetContent(cx, cy, '─', nil, borderStyle)
		}
	}

	// Title row: " Menu" on the left, "esc " on the right.
	drawAt(a.screen, mx+1, my+1, " Menu", titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-len([]rune(hint)), my+1, hint, mutedStyle)

	// Version stamp baked into the bottom border, right-aligned. A small
	// pad of dashes is left between the version text and the corner so it
	// reads as part of the frame rather than a label awkwardly butted up
	// against the border.
	verLabel := " v" + version.Version + " "
	verLen := len([]rune(verLabel))
	verX := mx + mw - 2 - verLen
	if verX > mx+1 {
		drawAt(a.screen, verX, my+mh-1, verLabel, mutedStyle)
	}

	// Action rows. Hovered (enabled) rows get a tinted full-width
	// background so they read like a hovered button in a GUI menu.
	hoverBg := a.theme.Selection
	hoverStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.Text).Bold(true)
	hoverChevStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.AccentSoft).Bold(true)
	for i, item := range items {
		cy := my + item.relY - a.menuScroll
		// Rows scrolled out of the content window aren't drawn; the
		// ▲/▼ chrome markers below tell the user they exist.
		if cy < my+3 || cy > my+mh-2 {
			continue
		}
		enabled := item.enabled(a)
		hovered := enabled && i == a.hoveredMenuRow

		var labelStyle, chevStyle, shortcutStyle tcell.Style
		switch {
		case hovered:
			// Paint the row's interior with the hover background first.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, cy, ' ', nil, hoverStyle)
			}
			labelStyle = hoverStyle
			chevStyle = hoverChevStyle
			// Text, not Muted: Muted lands around 2.6:1 on the Selection
			// hover bg — the one row the user is actively reading is the
			// last place the shortcut hint should go dim.
			shortcutStyle = tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.Text).Bold(true)
		case enabled:
			labelStyle = bgStyle
			chevStyle = chevronStyle
			shortcutStyle = mutedStyle
		default:
			labelStyle = mutedStyle
			chevStyle = mutedStyle
			shortcutStyle = mutedStyle
		}
		// Dynamic label (e.g. the file-explorer toggle row) takes precedence
		// over the static one when present.
		label := item.label
		if item.labelFor != nil {
			label = item.labelFor(a)
		}
		drawAt(a.screen, mx+2, cy, "▸", chevStyle)
		if item.shortcut == "" {
			drawAt(a.screen, mx+4, cy, label, labelStyle)
			continue
		}
		shortcutX := mx + mw - 2 - runeLen(item.shortcut)
		label = trimRunes(label, shortcutX-(mx+4)-2)
		drawAt(a.screen, mx+4, cy, label, labelStyle)
		drawAt(a.screen, shortcutX, cy, item.shortcut, shortcutStyle)
	}

	// Overflow markers, drawn into the fixed chrome (the title divider
	// and the bottom border) so they cost no content rows: ▲ when rows
	// are hidden above, ▼ when rows are hidden below. Accent-colored —
	// they're the only hint that more actions exist off-frame.
	moreStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent)
	if a.menuScroll > 0 {
		drawAt(a.screen, mx+2, my+2, " ▲ ", moreStyle)
	}
	if a.menuScroll < a.menuMaxScroll() {
		drawAt(a.screen, mx+2, my+mh-1, " ▼ ", moreStyle)
	}

	a.screen.HideCursor()
}
