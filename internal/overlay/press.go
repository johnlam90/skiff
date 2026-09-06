// =============================================================================
// File: internal/overlay/press.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import "github.com/gdamore/tcell/v2"

// Press is the one-button latch every overlay with click targets keeps.
// A terminal reports a drag as a stream of events that all carry
// Button1, so "Button1 is set" is not "the user clicked here": a press
// in a confirm's scrollable body followed by a downward drag delivered
// a Button1 event on the Yes cells and confirmed. Fresh separates the
// first event of that stream from the rest, so a target activates on
// the press that lands on it and never on motion that merely crosses
// it — while a scroll indicator, which WANTS to follow a drag, keeps
// reading the raw mask.
type Press struct {
	down bool
}

// Fresh reports whether btn carries a Button1 that was up on the
// previous event, and records the button state for the next call. It
// must be called exactly once per mouse event, before any branch acts
// on the answer; a release (or any button-less event) re-arms it.
func (p *Press) Fresh(btn tcell.ButtonMask) bool {
	down := btn&tcell.Button1 != 0
	fresh := down && !p.down
	p.down = down
	return fresh
}

// Sync sets the latch from btn without answering, for an opener whose
// surface may appear under a button that is already held. A menu
// re-opened by the row that closed it must not read the held motion
// that follows as a fresh press; a menu opened from the keyboard,
// after a release the previous surface never saw, must not inherit a
// latch still reading "down". Seeding from the dispatcher's own held
// mask gets both right.
func (p *Press) Sync(btn tcell.ButtonMask) {
	p.down = btn&tcell.Button1 != 0
}
