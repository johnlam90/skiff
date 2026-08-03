// =============================================================================
// File: internal/overlay/overlay.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package overlay owns skiff's floating surfaces — the overlays. An
// overlay (menu, prompt, confirm, picker, …) floats over the editor and
// captures all keyboard and mouse input until dismissed. The Stack is
// the single source of truth for which overlay is up; strips (find bar,
// project-find bar, leader strip) are deliberately not overlays and
// never appear here — see docs/adr/0001-strips-are-not-overlays.md.
package overlay

import "github.com/gdamore/tcell/v2"

// Overlay is the contract a floating surface implements to live on the
// Stack. The stack routes every key and mouse event to the top overlay
// and draws it above everything else; the overlay owns its own geometry,
// dismissal rules, and rendering.
type Overlay interface {
	// HandleKey processes one key event. The overlay owns the whole
	// keyboard while it is up — there is no fall-through.
	HandleKey(ev *tcell.EventKey)
	// HandleMouse processes one mouse event at screen cell (x, y).
	// Outside-click dismissal is the overlay's own decision.
	HandleMouse(x, y int, btn tcell.ButtonMask)
	// Draw paints the overlay above the base UI.
	Draw(scr tcell.Screen)
}

// Stack owns which overlay is up. Opening replaces any current overlay,
// so at most one is ever up — the mutual exclusion closeAllModals used
// to enforce by convention is behavior here.
type Stack struct {
	top Overlay
}

// Open makes o the current overlay, replacing any overlay already up.
func (s *Stack) Open(o Overlay) { s.top = o }

// Close dismisses the current overlay, if any.
func (s *Stack) Close() { s.top = nil }

// Top returns the current overlay, or nil when none is up.
func (s *Stack) Top() Overlay { return s.top }

// IsOpen reports whether an overlay is up.
func (s *Stack) IsOpen() bool { return s.top != nil }

// HandleKey routes ev to the current overlay; no-op when none is up.
func (s *Stack) HandleKey(ev *tcell.EventKey) {
	if s.top != nil {
		s.top.HandleKey(ev)
	}
}

// HandleMouse routes a mouse event to the current overlay; no-op when
// none is up.
func (s *Stack) HandleMouse(x, y int, btn tcell.ButtonMask) {
	if s.top != nil {
		s.top.HandleMouse(x, y, btn)
	}
}

// Draw paints the current overlay; no-op when none is up.
func (s *Stack) Draw(scr tcell.Screen) {
	if s.top != nil {
		s.top.Draw(scr)
	}
}
