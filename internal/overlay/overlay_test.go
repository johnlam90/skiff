// =============================================================================
// File: internal/overlay/overlay_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// spyOverlay counts routed events so tests can prove where the stack
// sends input without dragging in any real UI.
type spyOverlay struct {
	keys, mice, draws int
}

func (s *spyOverlay) HandleKey(*tcell.EventKey)              { s.keys++ }
func (s *spyOverlay) HandleMouse(int, int, tcell.ButtonMask) { s.mice++ }
func (s *spyOverlay) Draw(tcell.Screen)                      { s.draws++ }

// TestStack_OpenReplaces pins the at-most-one invariant: opening while
// another overlay is up replaces it, it never stacks — the mutual
// exclusion closeAllModals used to enforce by convention is module
// behavior now.
func TestStack_OpenReplaces(t *testing.T) {
	var s Stack
	first, second := &spyOverlay{}, &spyOverlay{}
	s.Open(first)
	if s.Top() != Overlay(first) {
		t.Fatal("first overlay should be on top after Open")
	}
	s.Open(second)
	if s.Top() != Overlay(second) {
		t.Fatal("second Open must replace the first overlay, not stack on it")
	}
	s.HandleKey(nil)
	if first.keys != 0 || second.keys != 1 {
		t.Fatalf("keys must route only to the replacement: first=%d second=%d", first.keys, second.keys)
	}
}

// TestStack_CloseEmpties pins teardown: Close leaves nothing up and a
// second Close stays harmless, since closeAllModals fires it
// unconditionally on every modal open.
func TestStack_CloseEmpties(t *testing.T) {
	var s Stack
	s.Open(&spyOverlay{})
	s.Close()
	if s.IsOpen() || s.Top() != nil {
		t.Fatal("Close must leave the stack empty")
	}
	s.Close() // must not panic when already empty
}

// TestStack_RoutesToTop pins that every event kind — key, mouse, draw —
// reaches the top overlay exactly once per call: the stack is the one
// routing traversal that replaces the old hand-maintained cascades.
func TestStack_RoutesToTop(t *testing.T) {
	var s Stack
	spy := &spyOverlay{}
	s.Open(spy)
	s.HandleKey(nil)
	s.HandleMouse(3, 4, tcell.Button1)
	s.Draw(nil)
	if spy.keys != 1 || spy.mice != 1 || spy.draws != 1 {
		t.Fatalf("routing miscounted: keys=%d mice=%d draws=%d", spy.keys, spy.mice, spy.draws)
	}
}

// TestStack_EmptyIsInert pins the no-overlay case: routing on an empty
// stack must be a silent no-op so the caller can route unconditionally
// before falling through to strips and the editor.
func TestStack_EmptyIsInert(t *testing.T) {
	var s Stack
	if s.IsOpen() {
		t.Fatal("zero-value stack must start empty")
	}
	s.HandleKey(nil)
	s.HandleMouse(0, 0, tcell.ButtonNone)
	s.Draw(nil) // reaching here without a panic is the assertion
}
