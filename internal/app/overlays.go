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

// This file holds the shim adapters that put each existing modal on the
// overlay stack. A shim delegates to the modal's existing handlers so the
// stack can be the single routing truth while the modal's state still
// lives on App; each shim dissolves when its modal becomes a real
// overlay adapter (prefab or bespoke). Shims are empty-comparable values
// on purpose: dropOverlay identifies "is mine on top" by equality.

// dropOverlay pops o only when it is the overlay actually on top, so a
// dedicated closer (closeMenu, closeFinder, …) that runs after a chained
// open — action closes its own surface after opening the next — can
// never dismiss the wrong overlay. closeAllModals pops unconditionally.
func (a *App) dropOverlay(o overlay.Overlay) {
	if a.overlays.Top() == o {
		a.overlays.Close()
	}
}

// menuOverlay routes the action menu. Its key path carries the
// Esc/Alt/leader interplay that used to live inline in handleKey — see
// handleMenuKey.
type menuOverlay struct{ a *App }

func (o menuOverlay) HandleKey(ev *tcell.EventKey) { o.a.handleMenuKey(ev) }
func (o menuOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	o.a.updateMenuHover(x, y)
	o.a.handleMenuMouse(x, y, btn)
}
func (o menuOverlay) Draw(tcell.Screen) { o.a.drawMenu() }

// The prompt is a real overlay now — overlay.Prompt — so it has no shim;
// openPrompt constructs the prefab directly.

// The confirm/info and dirty-close modals are real overlays now
// (overlay.Confirm, overlay.Info, overlay.Dirty) — no shims.

// The form and the right-click popup are real overlays now
// (overlay.Form, overlay.Popup) — no shims.

// The list picker is a real overlay now (overlay.Pick) — no shim.

// The finder is a bespoke overlay implementing the contract directly —
// see finder.go's finderOverlay.

// The diff viewer is a bespoke overlay implementing the contract
// directly — see diffview.go's diffOverlay.

// The git log is a bespoke overlay implementing the contract directly —
// see gitlog.go's gitLogOverlay.
