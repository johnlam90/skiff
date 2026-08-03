// =============================================================================
// File: internal/app/listpick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// listpick.go opens the generic pick-one-of-N overlay (overlay.Pick):
// a finder-style list with type-to-filter, arrow/hover highlight,
// Enter/click to choose, Esc/outside-click to cancel. Behavior hooks
// make it fit different jobs — the theme picker adds a live-preview
// OnMove hook and a revert OnCancel; the branch pickers just take
// OnPick. Anything that would otherwise reach for a blind ←→ select
// belongs here instead.

package app

import "github.com/johnlam90/skiff/internal/overlay"

// listPickItem is one selectable row — the overlay's item type under
// its historical app-side name, so the five picker call sites read
// unchanged.
type listPickItem = overlay.PickItem

// openListPick opens the pick overlay. onPick receives the index into
// items (never the filtered view); onMove (optional) fires whenever the
// highlight lands on a row — live preview; onCancel (optional) fires on
// dismissal without a pick, after any preview may have run.
func (a *App) openListPick(title string, items []listPickItem, onPick, onMove func(*App, int), onCancel func(*App)) {
	a.closeAllModals()
	pick := &overlay.Pick{Title: title, Items: items, Theme: a.theme}
	pick.Size = func() (int, int) { return a.width, a.height }
	pick.Close = func() { a.closeAllModals() }
	if onPick != nil {
		pick.OnPick = func(i int) { onPick(a, i) }
	}
	if onMove != nil {
		pick.OnMove = func(i int) { onMove(a, i) }
	}
	if onCancel != nil {
		pick.OnCancel = func() { onCancel(a) }
	}
	pick.Init()
	a.overlays.Open(pick)
}
