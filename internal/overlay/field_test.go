// =============================================================================
// File: internal/overlay/field_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// key builds a plain key event for field tests.
func key(k tcell.Key, r rune) *tcell.EventKey {
	return tcell.NewEventKey(k, r, tcell.ModNone)
}

// TestField_SetTextParksCursorAtEnd pins the pre-fill convention every
// opener relied on: after SetText the caret sits after the last rune.
func TestField_SetTextParksCursorAtEnd(t *testing.T) {
	var f Field
	f.SetText("abc")
	if f.Text() != "abc" || f.Cursor != 3 {
		t.Fatalf("got %q cursor=%d", f.Text(), f.Cursor)
	}
}

// TestField_EditingKeys pins the single-line editing contract shared by
// all seven former per-modal copies: insertion at the caret, Backspace
// before it, Delete at it, Home/End jumps, and bounds-safe Left/Right.
func TestField_EditingKeys(t *testing.T) {
	var f Field
	for _, r := range "abd" {
		if !f.HandleKey(key(tcell.KeyRune, r)) {
			t.Fatalf("rune %c not consumed", r)
		}
	}
	f.HandleKey(key(tcell.KeyLeft, 0))
	f.HandleKey(key(tcell.KeyRune, 'c')) // insert mid-word
	if f.Text() != "abcd" {
		t.Fatalf("insert at caret broken: %q", f.Text())
	}
	f.HandleKey(key(tcell.KeyHome, 0))
	f.HandleKey(key(tcell.KeyDelete, 0))
	if f.Text() != "bcd" {
		t.Fatalf("Delete at caret broken: %q", f.Text())
	}
	f.HandleKey(key(tcell.KeyEnd, 0))
	f.HandleKey(key(tcell.KeyBackspace2, 0))
	if f.Text() != "bc" {
		t.Fatalf("Backspace broken: %q", f.Text())
	}
	f.HandleKey(key(tcell.KeyLeft, 0))
	f.HandleKey(key(tcell.KeyLeft, 0))
	f.HandleKey(key(tcell.KeyLeft, 0)) // one past home — must clamp
	if f.Cursor != 0 {
		t.Fatalf("Left must clamp at 0, cursor=%d", f.Cursor)
	}
}

// TestField_RejectsControlRunes pins the r < 0x20 filter: control runes
// (a stray ^C pasted into a prompt) must not become field content.
func TestField_RejectsControlRunes(t *testing.T) {
	var f Field
	f.HandleKey(key(tcell.KeyRune, 0x03))
	if f.Text() != "" {
		t.Fatalf("control rune inserted: %q", f.Text())
	}
}

// TestField_DoesNotConsumeOverlayKeys pins the routing split: Esc,
// Enter, and Tab belong to the enclosing overlay, so HandleKey must
// report them unconsumed.
func TestField_DoesNotConsumeOverlayKeys(t *testing.T) {
	var f Field
	for _, k := range []tcell.Key{tcell.KeyEsc, tcell.KeyEnter, tcell.KeyTab, tcell.KeyUp, tcell.KeyDown} {
		if f.HandleKey(key(k, 0)) {
			t.Errorf("key %v must not be consumed by the field", k)
		}
	}
}

// TestField_CaretWindowFollowsCursor pins the caret-window behavior that
// existed as six near-identical adjust*Scroll methods: a caret past the
// right edge slides the window right; returning home slides it back.
func TestField_CaretWindowFollowsCursor(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init sim screen: %v", err)
	}
	scr.SetSize(80, 24)
	var f Field
	f.SetText("0123456789abcdefghij") // 20 runes into a 10-cell window
	f.Draw(scr, 5, 3, 10, tcell.StyleDefault, true)
	if f.Scroll() == 0 {
		t.Fatal("caret at end of long text must scroll the window")
	}
	f.HandleKey(key(tcell.KeyHome, 0))
	f.Draw(scr, 5, 3, 10, tcell.StyleDefault, true)
	if f.Scroll() != 0 {
		t.Fatalf("caret home must scroll window back, scroll=%d", f.Scroll())
	}
}

// TestField_DrawRendersVisibleWindow pins what lands on screen: the
// visible slice of the value at the field's cells, and the terminal
// cursor at the caret when focused.
func TestField_DrawRendersVisibleWindow(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init sim screen: %v", err)
	}
	scr.SetSize(80, 24)
	var f Field
	f.SetText("hello")
	f.Draw(scr, 5, 3, 10, tcell.StyleDefault, true)
	scr.Show()
	cells, w, _ := scr.GetContents()
	got := ""
	for i := 0; i < 5; i++ {
		got += string(cells[3*w+5+i].Runes)
	}
	if got != "hello" {
		t.Fatalf("field cells wrong: %q", got)
	}
	cx, cy, visible := scr.GetCursor()
	if !visible || cx != 5+5 || cy != 3 {
		t.Fatalf("caret wrong: (%d,%d) visible=%v", cx, cy, visible)
	}
}
