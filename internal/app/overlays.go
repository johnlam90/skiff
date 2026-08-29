// =============================================================================
// File: internal/app/overlays.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

// Every floating surface lives on a.overlays (the overlay.Stack) — the
// single routing truth for keyboard, mouse, and draw while one is up.
// The prompt, confirm, info, dirty-close, form, popup, and pick are
// overlay-package prefabs constructed by their openers; the finder,
// diff viewer, and git log are bespoke overlays owning their own state
// (finder.go, diffview.go, gitlog.go). The menu below is the one
// surface whose state stays on App: its items are App actions and its
// labels flex on App state (menuLayout / labelFor), so an adapter over
// App methods is its natural — and permanent — shape.
//
// The stack is also what decides how much mouse reporting skiff asks
// the terminal for: all-motion tracking (`\x1b[?1003h`) exists purely
// so these surfaces can hover, so it is switched on while one is up
// and off again the moment it closes. handleEvent recomputes that from
// a.overlays after every event — see mousemode.go.

// dropOverlay pops o only when it is the overlay actually on top, so a
// dedicated closer (closeMenu) that runs after a chained open — an
// action that closes its own surface after opening the next — can never
// dismiss the wrong overlay. closeAllModals pops unconditionally.
func (a *App) dropOverlay(o overlay.Overlay) {
	if a.overlays.Top() == o {
		a.overlays.Close()
	}
}

// menuOverlay adapts the action menu onto the stack. Its key path
// carries the Esc/Alt/leader interplay that used to live inline in
// handleKey — see handleMenuKey.
type menuOverlay struct{ a *App }

func (o menuOverlay) HandleKey(ev *tcell.EventKey) { o.a.handleMenuKey(ev) }
func (o menuOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	o.a.updateMenuHover(x, y)
	o.a.handleMenuMouse(x, y, btn)
}
func (o menuOverlay) Draw(tcell.Screen) { o.a.drawMenu() }

// WantsMotion is true: the menu tints the enabled row under the pointer
// and flashes a clipped label when the hover lands on a new one, both of
// which need motion reported with no button held.
func (o menuOverlay) WantsMotion() bool { return true }

// Dismiss is a no-op — a menu torn down by the stack simply runs no
// action, and closeMenu's own state reset rides on closeAllModals.
func (o menuOverlay) Dismiss() {}
