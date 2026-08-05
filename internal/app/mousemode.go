// =============================================================================
// File: internal/app/mousemode.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-05
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// mousemode.go decides how much mouse reporting to ask the terminal for.
//
// tcell turns MouseMotionEvents into `\x1b[?1003h` — xterm all-motion
// tracking, where the terminal emits an event for EVERY pointer movement
// whether a button is down or not. Skiff's habitat is SSH, often from a
// phone on cellular, where that is a continuous uplink flood; on a
// touchscreen it buys nothing at all because there is no hover.
//
// So the baseline is `?1000h` + `?1002h` (MouseButtonEvents |
// MouseDragEvents): presses, releases, drags-with-button and the wheel.
// That covers every core gesture — caret placement, drag-select, the
// editor / tree / git-panel scrollbar grabs, the sidebar splitter, and
// wheel scrolling. Wheel events are encoded with bit 6 of the button
// byte and are reported under 1000 like any press, and tcell's SGR
// parser never consults the requested flags (input.go handleMouse), so
// nothing about scrolling depends on 1003.
//
// Motion is switched ON only while a floating surface is up, because
// hover feedback is the one thing that genuinely needs button-less
// motion. See wantMouseFlags for exactly which surfaces.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

const (
	// mouseBaseFlags is what skiff asks for whenever nothing on screen
	// wants hover: clicks, releases, drags and the wheel — `?1000h` +
	// `?1002h`, and no all-motion flood.
	mouseBaseFlags = tcell.MouseButtonEvents | tcell.MouseDragEvents

	// mouseHoverFlags adds `?1003h` on top of the baseline so a
	// hover-sensitive overlay sees the pointer move with no button held.
	mouseHoverFlags = mouseBaseFlags | tcell.MouseMotionEvents
)

// overlayWantsMotion reports whether the overlay on top gives hover
// feedback and therefore needs button-less motion events.
//
// The default is YES, and the opt-out list is deliberately the short
// side: a surface that hovers when it shouldn't costs a burst of
// escape-sequence traffic for as long as one modal is up, while a
// surface that does NOT hover when it should is a silently dead
// highlight. Every other prefab (Prompt, Confirm, Dirty, Popup, Pick)
// plus the bespoke finder, git-log and diff overlays and the action
// menu track a Hover/selected row on motion. Info and Form do not —
// Info ignores everything but the wheel and Button1, Form returns
// early unless Button1 is down — and Info is the long-lived one (a
// 300-line stderr dump or diff preview the user reads and scrolls),
// so it is worth keeping quiet.
func overlayWantsMotion(o overlay.Overlay) bool {
	switch o.(type) {
	case *overlay.Info, *overlay.Form:
		return false
	}
	return true
}

// wantMouseFlags returns the mouse-reporting mode the current UI state
// needs. The overlay stack is the single owner of what is floating, so
// this reads it rather than trusting any individual opener or closer to
// have paired its calls — closeAllModals, the activate-then-close
// action paths and an overlay replacing another all fall out correctly
// because the answer is recomputed from the stack, never accumulated.
func (a *App) wantMouseFlags() tcell.MouseFlags {
	if top := a.overlays.Top(); top != nil && overlayWantsMotion(top) {
		return mouseHoverFlags
	}
	return mouseBaseFlags
}

// syncMouseMode re-requests mouse reporting from the terminal, but only
// when the mode actually changes. tcell's EnableMouse is not free — it
// writes the full disable-then-enable run (`?1000l?1002l?1003l?1006l`
// followed by the enables) every single call, and re-emitting that on
// every overlay open and close would spend bytes on exactly the slow
// link this exists to protect. mouseFlags is the cache; it starts at
// the value the constructors handed the screen.
func (a *App) syncMouseMode() {
	if a.screen == nil {
		return
	}
	want := a.wantMouseFlags()
	if want == a.mouseFlags {
		return
	}
	a.mouseFlags = want
	a.screen.EnableMouse(want)
}
