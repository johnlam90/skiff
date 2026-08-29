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
	keys, mice, draws, dismissals int
	motion                        bool
}

func (s *spyOverlay) HandleKey(*tcell.EventKey)              { s.keys++ }
func (s *spyOverlay) HandleMouse(int, int, tcell.ButtonMask) { s.mice++ }
func (s *spyOverlay) Draw(tcell.Screen)                      { s.draws++ }
func (s *spyOverlay) WantsMotion() bool                      { return s.motion }
func (s *spyOverlay) Dismiss()                               { s.dismissals++ }

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

// Every overlay this package ships satisfies the whole contract,
// WantsMotion and Dismiss included. The block is deliberately one list:
// adding a prefab and forgetting the two capability methods is a
// compile error here rather than a surface that is silently classified
// by whichever type-switch branch it failed to match — which is exactly
// what the app used to do.
var (
	_ Overlay = (*Prompt)(nil)
	_ Overlay = (*Confirm)(nil)
	_ Overlay = (*Info)(nil)
	_ Overlay = (*Dirty)(nil)
	_ Overlay = (*Form)(nil)
	_ Overlay = (*Popup)(nil)
	_ Overlay = (*Pick)(nil)
)

// TestOverlay_MotionOptOutIsTheShortList pins the classification the
// app used to type-switch for. Motion tracking is `?1003h` — an event
// per pointer movement, forever, over what is often a phone's uplink —
// so it is bought only by surfaces that actually highlight something
// under the pointer. Info (wheel and Button1 only) and Form (Button1
// only) do not; every other prefab does.
func TestOverlay_MotionOptOutIsTheShortList(t *testing.T) {
	for _, tc := range []struct {
		name string
		ov   Overlay
		want bool
	}{
		{"prompt", &Prompt{}, true},
		{"confirm", &Confirm{}, true},
		{"dirty", &Dirty{}, true},
		{"popup", &Popup{}, true},
		{"pick", &Pick{}, true},
		{"info", &Info{}, false},
		{"form", &Form{}, false},
	} {
		if got := tc.ov.WantsMotion(); got != tc.want {
			t.Errorf("%s: WantsMotion = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPick_DismissRevertsWithoutClosing is why Dismiss exists at all.
// The theme picker previews a palette on every highlight move and
// reverts it in OnCancel; a modal opening on top pops the pick off the
// stack, and without this the preview would be stranded — the user's
// theme silently changed to whatever row they last hovered. Close must
// NOT fire: the stack has already popped, and re-entering the teardown
// it is unwinding is how a closer ends up dismissing the wrong overlay.
func TestPick_DismissRevertsWithoutClosing(t *testing.T) {
	var reverted, closed int
	p := &Pick{
		OnCancel: func() { reverted++ },
		Close:    func() { closed++ },
	}
	p.Dismiss()
	if reverted != 1 {
		t.Fatalf("Dismiss must fire OnCancel exactly once, got %d", reverted)
	}
	if closed != 0 {
		t.Fatalf("Dismiss must not call Close, got %d calls", closed)
	}

	// A pick with no revert hook is as inert as every other overlay.
	(&Pick{}).Dismiss()
}

// TestOverlay_DismissIsANoOpWithoutAHook pins the other side of the
// contract: for every overlay but Pick a teardown is simply "you never
// answered", so Dismiss must do nothing observable — most importantly
// it must not fire the callbacks the user's own gesture owns.
func TestOverlay_DismissIsANoOpWithoutAHook(t *testing.T) {
	var fired int
	bump := func() { fired++ }
	for _, ov := range []Overlay{
		&Prompt{OnSubmit: func(string) { fired++ }, Close: bump},
		&Confirm{OnYes: bump, Close: bump},
		&Info{Close: bump},
		&Dirty{OnSave: bump, OnDiscard: bump, Close: bump},
		&Form{OnSubmit: func(map[string]string) { fired++ }, Close: bump},
		&Popup{Close: bump},
	} {
		ov.Dismiss()
	}
	if fired != 0 {
		t.Fatalf("Dismiss fired %d callback(s); it must be inert everywhere but Pick", fired)
	}
}
