// =============================================================================
// File: internal/app/menu.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// menu.go owns the action modal's behavior and rendering: opening and
// closing it, the type-to-filter field, keyboard navigation, mouse
// hover/click routing, the scroll window a short terminal needs, the
// drill-in picks the top level demotes rows into, and the draw pass for
// both the modal and the ≡ button that opens it.
//
// The modal's row and height layout comes from menudef.go's menuLayout,
// and its place in the input routing comes from the overlay stack (see
// overlays.go) — this file is the menu-specific half of both.

package app

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/version"
)

// menuMinFrameWidth is the narrowest frame the modal will shrink to. Five
// cells go to chrome the row layout cannot give up — the border, the "▸ "
// indent, and the pad cell before the right border, which is exactly what
// menuLabelBudget subtracts — and nineteen are left for the label, enough
// to read the longest built-in row ("Keyboard shortcuts…") whole. Below
// this the frame stops narrowing and clips instead, which only happens on
// a terminal already under minWidth, where draw() has handed the screen
// to drawTooSmall anyway.
const menuMinFrameWidth = 5 + 19

// menuModalRect returns the on-screen rectangle of the action modal,
// centered in the window. Both dimensions are derived from the current
// layout so adding custom actions — or a long one — grows the modal
// automatically, and both are clamped to the screen (one row / column of
// margin) so a short or narrow terminal scrolls and clips the overflow
// instead of painting outside itself.
//
// The origin comes from the UNFILTERED size (menuNaturalWidth /
// menuNaturalHeight) while the height comes from the filtered layout: the
// frame then shrinks from the bottom as a query narrows the list, instead
// of re-centering — and jumping the title and the filter caret out from
// under the user — on every keystroke.
func (a *App) menuModalRect() (x, y, w, h int) {
	w = a.menuNaturalWidth()
	// The frame narrows past modalWidth when it has to. It used to
	// refuse, on the theory that content which does not fit does not fit
	// either way — but the two outcomes are not equivalent: an
	// overflowing frame hangs its right border, its whole shortcut
	// column and the ▼ overflow marker off the edge of the screen, while
	// a narrowed one keeps all three and lets trimRunes ellipsise the
	// labels, which revealMenuRowLabel already flashes in full. That is
	// what makes the menu — the only path to every action in a
	// no-Ctrl editor — usable at phone widths.
	if maxW := a.width - 2; w > maxW {
		w = maxW
	}
	if w < menuMinFrameWidth {
		w = menuMinFrameWidth
	}
	_, _, h = a.menuLayout()
	full := a.menuNaturalHeight()
	if maxH := a.height - 2; full > maxH {
		full = maxH
	}
	if h > full {
		h = full
	}
	x = (a.width - w) / 2
	y = (a.height - full) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// syncMenuList pushes the menu's live shape into its scroll window and
// hands the window back. Both numbers are in VIRTUAL ROWS: the content
// region runs from menuContentY to the row above the bottom border, so
// the layout's natural height and the clamped frame height each lose
// the same fixed chrome, and their difference is exactly the old
// menuMaxScroll. The layout moves on every filter keystroke and the
// frame on every resize, so every reader syncs first.
func (a *App) syncMenuList() *overlay.List {
	_, _, natural := a.menuLayout()
	_, _, _, h := a.menuModalRect()
	a.menuList.SetLen(natural - 1 - menuContentY)
	a.menuList.SetVisible(h - 1 - menuContentY)
	return &a.menuList
}

// menuMaxScroll returns how many rows the menu content can scroll: the
// overflow between the natural layout height and the clamped modal
// height. Zero when the whole menu fits on screen.
func (a *App) menuMaxScroll() int { return a.syncMenuList().MaxScroll() }

// menuBar describes the menu's scroll indicator: the frame's right
// padding column — the same cell every prefab spends on one — over the
// content rows only, so the title, the filter and the borders stay
// clear.
func (a *App) menuBar() overlay.Bar {
	mx, my, mw, mh := a.menuModalRect()
	r := overlay.Rect{X: mx, Y: my, W: mw, H: mh}
	return a.syncMenuList().Bar(overlay.BarColumn(r), my+menuContentY)
}

// ensureMenuRowVisible scrolls the menu so the item at idx sits inside
// the visible content region — the keyboard analogue of the editor's
// EnsureVisible, so arrow-key navigation can reach rows a short terminal
// pushed out of the frame. The item's relY is a virtual row, which is
// the unit the window counts in; hoveredMenuRow keeps the item index.
func (a *App) ensureMenuRowVisible(idx int) {
	items, _, _ := a.menuLayout()
	if idx < 0 || idx >= len(items) {
		return
	}
	l := a.syncMenuList()
	l.Select(items[idx].relY - menuContentY)
	l.EnsureVisible()
}

// handleMenuMouse processes mouse events while the action menu is open.
// Left-click outside the modal closes it; left-click on a row runs that
// row's action (if it is currently enabled). Wheel events — and the
// scroll indicator's own column — scroll the content when the layout is
// taller than the terminal. The filter never changes what a click does;
// it only changes which rows are on screen.
func (a *App) handleMenuMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		a.syncMenuList().ScrollBy(-1)
		// The rows just moved under the stationary pointer — recompute
		// the hover so the highlight tracks what a click would now hit
		// instead of going one row stale until the next mouse motion.
		a.updateMenuHover(x, y)
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.syncMenuList().ScrollBy(1)
		a.updateMenuHover(x, y)
		return
	}
	mx, my, mw, mh := a.menuModalRect()
	// The bar's column is claimed before anything else inside the frame,
	// the same ordering Pick uses: reaching for the thumb must scroll,
	// never run whichever action sits behind it. The hit is false
	// whenever the menu fits, so the column stays row surface then.
	if b := a.menuBar(); b.Hit(x, y) {
		if btn&tcell.Button1 != 0 {
			a.syncMenuList().ScrollToBar(my+menuContentY, y)
		}
		return
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeMenu()
		return
	}
	// Only the scrollable content region (below the title, the filter
	// field and their divider, above the bottom border) can hold items;
	// clicks on the chrome rows stay inert even when scrolled rows share
	// their virtual position. menuRowAt already answers all of that, and
	// it only ever names an ENABLED row.
	if idx := a.menuRowAt(x, y); idx >= 0 {
		items, _, _ := a.menuLayout()
		items[idx].action(a)
	}
}

// openMenu shows the action modal. While it is up, the editor doesn't
// receive typed keys, and clicks outside the modal dismiss it. The
// filter starts empty and focused, and menuFilterChanged pre-selects the
// first enabled row so Down/Up/Enter navigation has somewhere sensible
// to start.
func (a *App) openMenu() {
	a.closeAllModals()
	a.menuOpen = true
	a.overlays.Open(menuOverlay{a})
	a.menuList = overlay.List{}
	a.menuFilter = overlay.Field{}
	a.menuFilterChanged()
}

// menuMoveSelection advances hoveredMenuRow to the next (dir=+1) or
// previous (dir=-1) enabled menu item, wrapping around at the ends so the
// list feels continuous. Disabled items and dividers are skipped. If no
// item is currently enabled hoveredMenuRow stays -1. With a filter typed
// the "list" is the match set, so the arrows walk exactly what's drawn.
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
			// An arrow key is an explicit "show me this row", so it
			// reveals unconditionally — even when the wrap landed back on
			// the row already selected, which is what a single-match
			// filter does.
			a.revealMenuRowLabel(idx)
			return
		}
	}
	a.hoveredMenuRow = -1
}

// menuFilterChanged re-selects after the query moved: scroll back to the
// top and highlight the best-ranked enabled match (ties break toward
// menu order), so Enter runs the row the user was aiming at rather than
// whichever match happens to sit highest in the table. With an empty
// query every row ranks 0 and this degenerates to "select the first
// enabled row" — the pre-filter open behaviour.
func (a *App) menuFilterChanged() {
	a.menuList = overlay.List{}
	items, _, _ := a.menuLayout()
	q := a.menuQuery()
	best, bestRank := -1, 0
	for i, it := range items {
		if !it.enabled(a) {
			continue
		}
		rank := 0
		if q != "" {
			rank = menuMatchRank(strings.ToLower(a.menuLabel(it)), q)
		}
		if best < 0 || rank < bestRank {
			best, bestRank = i, rank
			if rank == 0 {
				break
			}
		}
	}
	a.hoveredMenuRow = best
	if best >= 0 {
		a.ensureMenuRowVisible(best)
	}
	a.syncMenuList().Clamp()
}

// clearMenuFilter empties the query and re-selects against the restored
// full list.
func (a *App) clearMenuFilter() {
	a.menuFilter = overlay.Field{}
	a.menuFilterChanged()
}

// handleMenuKey owns the keyboard while the action menu is up: Up/Down
// walk the (possibly filtered) rows, Enter runs the highlighted one,
// Esc unwinds one layer — a typed filter first, then the menu — and
// every other printable key edits the filter. Pasted content is ignored
// outright: with no text focus behind the menu, dispatching pasted runes
// would fire arbitrary actions.
//
// # Filter vs. Esc-leader runes
//
// Before type-to-filter, a bare rune pressed with the menu up fired its
// leader action — the menu doubles as the shortcut cheat-sheet, so the
// shortcuts had to work while it showed. That cannot survive a focused
// text input, and not for a stylistic reason: 21 of the 26 letters are
// bound in leaderBindings, so honouring bare runes would make almost
// every filter untypeable ("switch branch" would save the file on its
// first keystroke), and silently running an action while a caret blinks
// in an input is exactly the class of surprise the no-Ctrl rule exists
// to avoid. So bare runes always go to the filter.
//
// The cheat-sheet role is intact, and is now literally accurate:
//   - every row still renders its "Esc s" hint, filtered or not;
//   - Alt+<rune> still fires the action, and that is not a fallback —
//     it is how tmux and most terminals actually deliver a fast
//     "Esc s" (the Alt block above), so the printed gesture keeps
//     working with the menu up on the setups skiff targets;
//   - an already-armed leader window still beats the filter through
//     leaderWindowIntercept, below, for the terminals that send Esc and
//     the rune as two separate events. That is the same precedence the
//     editor itself applies — an armed Esc window wins over typing into
//     the buffer (handleKey) — so the filter is not a special case.
//
// Esc itself stays the menu's own key (clear filter, then close) rather
// than re-arming the leader: dismissing a modal must not leave a live
// hotkey window that turns the user's next keystroke into an action.
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
		// One layer at a time: bailing straight out of a narrowed menu
		// would throw away the user's place over a typo they could have
		// fixed with one more keystroke.
		if len(a.menuFilter.Value) > 0 {
			a.clearMenuFilter()
		} else {
			a.closeMenu()
		}
		a.lastEscape = time.Time{}
		return
	}
	if a.leaderWindowIntercept(ev) {
		return
	}
	a.lastEscape = time.Time{}
	switch ev.Key() {
	case tcell.KeyDown:
		a.menuMoveSelection(1)
		return
	case tcell.KeyUp:
		a.menuMoveSelection(-1)
		return
	case tcell.KeyEnter:
		a.menuActivate()
		return
	}
	// Everything else is text. Value length changes exactly when an edit
	// changed the query — cursor motion keeps it, insert/delete moves
	// it — so only a real edit pays for a re-layout. Same test the pick
	// overlay uses; the two surfaces should feel identical.
	before := len(a.menuFilter.Value)
	a.menuFilter.HandleKey(ev)
	if len(a.menuFilter.Value) != before {
		a.menuFilterChanged()
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
	a.menuList = overlay.List{}
	a.menuFilter = overlay.Field{}
}

// openMenuDrillIn replaces the action menu with an overlay.Pick over one
// demoted cluster's rows — the git verbs, the file-clipboard actions.
// Only rows that are visible AND enabled right now make it in: a pick
// has no dimmed state, and a drill-in the user opened deliberately
// should list what it can actually do. The Esc-leader hint rides along
// in the pick's dimmed tag column so the cheat-sheet survives the
// demotion.
func (a *App) openMenuDrillIn(d menuDrillIn) {
	a.closeMenu()
	rows := make([]menuItemDef, 0, len(d.items))
	items := make([]listPickItem, 0, len(d.items))
	for _, it := range d.items {
		if it.visible != nil && !it.visible(a) {
			continue
		}
		if it.enabled != nil && !it.enabled(a) {
			continue
		}
		rows = append(rows, it)
		items = append(items, listPickItem{Label: a.menuLabel(it), Tag: it.shortcut})
	}
	if len(items) == 0 {
		// The row's own visibility predicate should have hidden it, but
		// predicates and contents can drift — say so instead of opening
		// an empty frame.
		a.flash("No " + strings.ToLower(d.title) + " actions available right now")
		return
	}
	a.openListPick(d.title, items, func(app *App, i int) { rows[i].action(app) }, nil, nil)
}

// menuGitMenu is the ≡ → Git… row: the nine git verbs as a filterable
// pick instead of nine rows of the main menu.
func (a *App) menuGitMenu() { a.openMenuDrillIn(gitDrillIn()) }

// menuFileClipboard is the ≡ → File clipboard… row: cut / copy /
// duplicate / paste plus the two copy-path actions.
func (a *App) menuFileClipboard() { a.openMenuDrillIn(fileClipDrillIn()) }

// updateMenuHover sets hoveredMenuRow to the index of the enabled menu row
// at (x, y), or to -1 when the mouse is over a disabled row, a divider, the
// title, or anywhere outside the modal. Landing on a new row also reveals
// its label when the modal had to clip it — gated on the row actually
// changing, because this runs on every motion event.
func (a *App) updateMenuHover(x, y int) {
	// The bar owns its column outright, and the menu's hover IS its
	// keyboard selection — reaching for the thumb must not drop the row
	// Enter would have run. Same rule Pick follows for its own bar.
	if a.menuBar().Hit(x, y) {
		return
	}
	prev := a.hoveredMenuRow
	a.hoveredMenuRow = a.menuRowAt(x, y)
	if a.hoveredMenuRow >= 0 && a.hoveredMenuRow != prev {
		a.revealMenuRowLabel(a.hoveredMenuRow)
	}
}

// menuRowAt returns the index of the enabled menu row under (x, y), or -1
// for a disabled row, a chrome row, the scroll indicator's column, or
// anywhere outside the modal.
func (a *App) menuRowAt(x, y int) int {
	mx, my, mw, mh := a.menuModalRect()
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		return -1
	}
	// The bar owns its column outright — hovering it must not drag the
	// highlight around any more than pressing it should run a row.
	if a.menuBar().Hit(x, y) {
		return -1
	}
	// Chrome rows (title, filter, divider, borders) never hover; the
	// window maps the content region through the scroll offset, and
	// answers false for both.
	row, ok := a.syncMenuList().RowAt(my+menuContentY, y)
	if !ok {
		return -1
	}
	relY := row + menuContentY
	items, _, _ := a.menuLayout()
	for i, item := range items {
		if item.relY == relY && item.enabled(a) {
			return i
		}
	}
	return -1
}

// revealMenuRowLabel flashes the full label of the row at idx, but only
// when the modal genuinely had to clip it. On a wide terminal the frame
// grows instead (menuNaturalWidth) so this never fires; when the terminal
// itself is the constraint, a row reading as an unidentifiable prefix is
// the failure this recovers from — a user-named custom action is the
// common case.
//
// The status bar is the cheapest surface that costs the modal no layout,
// and it is the same trick the git panel uses to decode its compact button
// ladder. A label longer than the bar rides the flash strip, so even the
// pathological 100-rune action name is fully readable.
func (a *App) revealMenuRowLabel(idx int) {
	items, _, _ := a.menuLayout()
	if idx < 0 || idx >= len(items) {
		return
	}
	it := items[idx]
	label := a.menuLabel(it)
	_, _, mw, _ := a.menuModalRect()
	if runeLen(label) <= menuLabelBudget(mw, it) {
		return
	}
	a.flash(label)
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
// custom actions, new built-in groups, or a filter query doesn't
// require touching this function.
func (a *App) drawMenu() {
	mx, my, mw, mh := a.menuModalRect()
	items, dividers, _ := a.menuLayout()
	scroll := a.syncMenuList().Scroll()

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
	// menuLayout — including the always-on row under the filter — so it
	// stays in sync with whatever rows are actually being drawn. The
	// chrome divider (menuDividerY) is fixed; the rest live in the
	// scrollable content region and map through the scroll offset,
	// dropping out when they leave the visible window.
	for _, dy := range dividers {
		cy := my + dy
		if dy > menuDividerY {
			cy -= scroll
			if cy < my+menuContentY || cy > my+mh-2 {
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
	drawAt(a.screen, mx+1, my+menuTitleY, " Menu", titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-len([]rune(hint)), my+menuTitleY, hint, mutedStyle)

	// Filter field, on the darker editor background so it reads as an
	// input well rather than another row — same treatment, same
	// geometry offsets and same placeholder voice as overlay.Pick, so
	// the two filter surfaces feel like one idea.
	fieldStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	fieldX := mx + 3
	fieldW := mw - 5
	a.menuFilter.Draw(a.screen, fieldX, my+menuFilterY, fieldW, fieldStyle, true)
	if len(a.menuFilter.Value) == 0 {
		drawAt(a.screen, fieldX, my+menuFilterY, "type to filter actions…",
			tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Subtle))
	}

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
		cy := my + item.relY - scroll
		// Rows scrolled out of the content window aren't drawn; the
		// ▲/▼ chrome markers below tell the user they exist.
		if cy < my+menuContentY || cy > my+mh-2 {
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
		// Every row is clipped to menuLabelBudget, shortcut or not.
		// Without a shortcut there used to be no budget at all and
		// drawAt does no bounds checking, so a long custom-action label
		// painted straight through the right border and onto the editor
		// underneath. trimRunes leaves an ellipsis; hovering or selecting
		// the row flashes the whole thing (revealMenuRowLabel).
		label := trimRunes(a.menuLabel(item), menuLabelBudget(mw, item))
		drawAt(a.screen, mx+2, cy, "▸", chevStyle)
		drawAt(a.screen, mx+4, cy, label, labelStyle)
		if item.shortcut != "" {
			drawAt(a.screen, mx+mw-2-runeLen(item.shortcut), cy, item.shortcut, shortcutStyle)
		}
	}
	if len(items) == 0 {
		drawAt(a.screen, mx+4, my+menuContentY, "no matches", mutedStyle)
	}

	// Overflow markers, drawn into the fixed chrome (the divider under
	// the filter and the bottom border) so they cost no content rows:
	// ▲ when rows are hidden above, ▼ when rows are hidden below.
	// Accent-colored — they're the only hint that more actions exist
	// off-frame.
	moreStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent)
	if scroll > 0 {
		drawAt(a.screen, mx+2, my+menuDividerY, " ▲ ", moreStyle)
	}
	if scroll < a.menuMaxScroll() {
		drawAt(a.screen, mx+2, my+mh-1, " ▼ ", moreStyle)
	}
	// The scroll indicator, painted last: a hovered row fills the frame's
	// inner width, padding column included, so the bar has to land on top
	// of it. The ▲/▼ chrome markers say that more rows exist; the bar says
	// HOW MANY and where in them you are, and it is the only one of the
	// three you can grab.
	a.menuBar().Draw(a.screen, a.theme)
	// No HideCursor here on purpose: the filter field is focused and
	// Field.Draw parked the terminal caret in it, which is the whole
	// affordance telling the user they can just type.
}
