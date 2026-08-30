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

import "github.com/gdamore/tcell/v2"

const (
	// mouseBaseFlags is what skiff asks for whenever nothing on screen
	// wants hover: clicks, releases, drags and the wheel — `?1000h` +
	// `?1002h`, and no all-motion flood.
	mouseBaseFlags = tcell.MouseButtonEvents | tcell.MouseDragEvents

	// mouseHoverFlags adds `?1003h` on top of the baseline so a
	// hover-sensitive overlay sees the pointer move with no button held.
	mouseHoverFlags = mouseBaseFlags | tcell.MouseMotionEvents
)

// wantMouseFlags returns the mouse-reporting mode the current UI state
// needs. The overlay stack is the single owner of what is floating, so
// this reads it rather than trusting any individual opener or closer to
// have paired its calls — closeAllModals, the activate-then-close
// action paths and an overlay replacing another all fall out correctly
// because the answer is recomputed from the stack, never accumulated.
//
// The surface answers for itself through Overlay.WantsMotion. This used
// to be a type switch here with a two-entry opt-out list, which meant a
// new overlay was classified by whichever branch it failed to match —
// silently, and always as "hovers". Nearly every surface does hover
// (Prompt, Confirm, Dirty, Popup, Pick, the action menu, the finder,
// the git log and the diff view all track a row or a button under the
// pointer), so the default was right far more often than not; but Info
// and Form do not, and Info is the long-lived one — a 300-line stderr
// dump or diff preview the user reads and scrolls — so getting it wrong
// costs a continuous uplink flood for as long as it is up. Now the
// classification lives on the type that knows.
func (a *App) wantMouseFlags() tcell.MouseFlags {
	if top := a.overlays.Top(); top != nil && top.WantsMotion() {
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
