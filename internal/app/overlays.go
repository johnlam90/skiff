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

// promptOverlay routes the single-line text prompt.
type promptOverlay struct{ a *App }

func (o promptOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handlePromptKey(ev) }
func (o promptOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handlePromptMouse(x, y, btn) }
func (o promptOverlay) Draw(tcell.Screen)                          { o.a.drawPrompt() }

// confirmOverlay routes the Yes/No confirm and its single-button info
// flavour — both share the confirm state and handlers.
type confirmOverlay struct{ a *App }

func (o confirmOverlay) HandleKey(ev *tcell.EventKey) { o.a.handleConfirmKey(ev) }
func (o confirmOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	o.a.handleConfirmMouse(x, y, btn)
}
func (o confirmOverlay) Draw(tcell.Screen) { o.a.drawConfirm() }

// dirtyOverlay routes the three-button unsaved-changes modal.
type dirtyOverlay struct{ a *App }

func (o dirtyOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handleDirtyKey(ev) }
func (o dirtyOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handleDirtyMouse(x, y, btn) }
func (o dirtyOverlay) Draw(tcell.Screen)                          { o.a.drawDirtyClose() }

// formOverlay routes the multi-field custom-action form.
type formOverlay struct{ a *App }

func (o formOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handleFormKey(ev) }
func (o formOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handleFormMouse(x, y, btn) }
func (o formOverlay) Draw(tcell.Screen)                          { o.a.drawForm() }

// contextOverlay routes the right-click popup (tree nodes and the git
// extras menu alike).
type contextOverlay struct{ a *App }

func (o contextOverlay) HandleKey(ev *tcell.EventKey) { o.a.handleContextKey(ev) }
func (o contextOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	o.a.handleContextMouse(x, y, btn)
}
func (o contextOverlay) Draw(tcell.Screen) { o.a.drawContext() }

// listPickOverlay routes the filterable list picker.
type listPickOverlay struct{ a *App }

func (o listPickOverlay) HandleKey(ev *tcell.EventKey) { o.a.handleListPickKey(ev) }
func (o listPickOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	o.a.handleListPickMouse(x, y, btn)
}
func (o listPickOverlay) Draw(tcell.Screen) { o.a.drawListPick() }

// finderOverlay routes the fuzzy file finder.
type finderOverlay struct{ a *App }

func (o finderOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handleFinderKey(ev) }
func (o finderOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handleFinderMouse(x, y, btn) }
func (o finderOverlay) Draw(tcell.Screen)                          { o.a.drawFinder() }

// diffOverlay routes the side-by-side diff viewer.
type diffOverlay struct{ a *App }

func (o diffOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handleDiffKey(ev) }
func (o diffOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handleDiffMouse(x, y, btn) }
func (o diffOverlay) Draw(tcell.Screen)                          { o.a.drawDiffView() }

// gitLogOverlay routes the commit-history list.
type gitLogOverlay struct{ a *App }

func (o gitLogOverlay) HandleKey(ev *tcell.EventKey)               { o.a.handleGitLogKey(ev) }
func (o gitLogOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handleGitLogMouse(x, y, btn) }
func (o gitLogOverlay) Draw(tcell.Screen)                          { o.a.drawGitLog() }
