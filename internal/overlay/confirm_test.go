// =============================================================================
// File: internal/overlay/confirm_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// testConfirm builds a Confirm on an 80×24 screen with an ordered call
// log, so tests can pin both what fired and that Close came first.
func testConfirm() (*Confirm, *[]string) {
	var log []string
	c := &Confirm{
		Title:   "T",
		Message: "sure?",
		Theme:   theme.Default(),
		Size:    func() (int, int) { return 80, 24 },
	}
	c.Close = func() { log = append(log, "close") }
	c.OnYes = func() { log = append(log, "yes") }
	c.OnCancel = func() { log = append(log, "cancel") }
	return c, &log
}

// TestConfirm_DefaultFocusIsNo pins the safety default: a zero Hover is
// the No button, so an accidental Enter cancels rather than confirms a
// destructive action.
func TestConfirm_DefaultFocusIsNo(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("Enter on default focus must cancel, got %v", *log)
	}
}

// TestConfirm_FocusKeysAndYes pins the focus keys — Right arms Yes, Tab
// toggles, Left returns to No — and that Enter on Yes closes before
// OnYes runs (capture-then-close).
func TestConfirm_FocusKeysAndYes(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyRight, 0))
	if c.Hover != 1 {
		t.Fatal("Right must focus Yes")
	}
	c.HandleKey(key(tcell.KeyTab, 0))
	if c.Hover != 0 {
		t.Fatal("Tab must toggle back to No")
	}
	c.HandleKey(key(tcell.KeyTab, 0))
	c.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "yes" {
		t.Fatalf("want [close yes], got %v", *log)
	}
}

// TestConfirm_EscRunsCancelHook pins the dismissal contract the
// formatter-trust flow depends on: Esc closes first, then OnCancel
// records the denial — and the hook lives on this value, so it can
// never leak into an unrelated confirm.
func TestConfirm_EscRunsCancelHook(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyEsc, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "cancel" {
		t.Fatalf("want [close cancel], got %v", *log)
	}
}

// TestConfirm_MouseZonesMatchDrawnButtons pins that the click zones sit
// exactly where the buttons are painted — the draw and hit-test share
// the confirmBtn* columns, so they can't drift.
func TestConfirm_MouseZonesMatchDrawnButtons(t *testing.T) {
	r := Centered(80, 24, confirmWidth, confirmHeight)

	c, log := testConfirm()
	c.HandleMouse(r.X+confirmBtnYesX+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "yes" {
		t.Fatalf("Yes click: got %v", *log)
	}

	c, log = testConfirm()
	c.HandleMouse(r.X+confirmBtnNoX+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("No click: got %v", *log)
	}

	c, log = testConfirm()
	c.HandleMouse(r.X-1, r.Y, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("outside click must cancel, got %v", *log)
	}

	c, _ = testConfirm()
	c.HandleMouse(r.X+confirmBtnYesX+1, r.Y+5, tcell.ButtonNone)
	if c.Hover != 1 {
		t.Fatal("motion over Yes must move the hover highlight")
	}
}

// TestConfirm_DrawTruncatesMessageByRunes pins the rune-safe
// truncation: a multibyte message longer than the modal must clip at a
// rune boundary with an ellipsis, never split a rune into garbage.
func TestConfirm_DrawTruncatesMessageByRunes(t *testing.T) {
	scr := simScreen(t)
	c, _ := testConfirm()
	for i := 0; i < 40; i++ {
		c.Message += "über"
	}
	c.Draw(scr)
	scr.Show()
	r := Centered(80, 24, confirmWidth, confirmHeight)
	row := ""
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		row += string(cellAt(scr, x, r.Y+4))
	}
	if len([]rune(row)) > r.W-2 {
		t.Fatalf("message row overflows the modal: %d runes", len([]rune(row)))
	}
	found := false
	for _, ch := range row {
		if ch == '…' {
			found = true
		}
	}
	if !found {
		t.Fatal("truncated message must end in an ellipsis")
	}
}
