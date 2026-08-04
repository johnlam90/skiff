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

// -----------------------------------------------------------------------------
// Multi-line Body mode
// -----------------------------------------------------------------------------

// bodyConfirm builds a Body-mode Confirm with n numbered rows so tests
// can reason about which row is visible at a given scroll offset.
func bodyConfirm(n int) (*Confirm, *[]string) {
	c, log := testConfirm()
	c.Body = make([]string, n)
	for i := range n {
		c.Body[i] = "row" + string(rune('A'+i))
	}
	return c, log
}

// TestConfirm_EmptyBodyKeepsClassicGeometry is the compatibility pin:
// every existing confirm in the app passes only Message, so the frame
// size and the button row must stay exactly where they were before Body
// existed — otherwise every Yes/No hit zone in the editor shifts.
func TestConfirm_EmptyBodyKeepsClassicGeometry(t *testing.T) {
	c, _ := testConfirm()
	r := c.rect()
	want := Centered(80, 24, confirmWidth, confirmHeight)
	if r != want {
		t.Fatalf("message-only rect drifted: got %+v want %+v", r, want)
	}
	if c.buttonRow() != 5 || c.buttonOffset() != 0 {
		t.Fatalf("button geometry drifted: row=%d offset=%d", c.buttonRow(), c.buttonOffset())
	}
}

// TestConfirm_BodyGrowsFrameAndMovesButtons pins the Body layout: the
// frame widens to fit commands and gets one row per body line, and the
// buttons move below the content instead of being painted over it.
func TestConfirm_BodyGrowsFrameAndMovesButtons(t *testing.T) {
	c, _ := bodyConfirm(5)
	r := c.rect()
	if r.W != ConfirmBodyWidth {
		t.Fatalf("body frame width: got %d want %d", r.W, ConfirmBodyWidth)
	}
	if r.H != confirmChromeRows+5 {
		t.Fatalf("body frame height: got %d want %d", r.H, confirmChromeRows+5)
	}
	if c.buttonRow() != 9 {
		t.Fatalf("buttons should sit below 5 body rows, got relY %d", c.buttonRow())
	}
}

// TestConfirm_BodyButtonsStayClickable is the one that would silently
// break the trust prompt: with the taller frame, the drawn buttons and
// the mouse hit zones must still agree, or Yes stops responding.
func TestConfirm_BodyButtonsStayClickable(t *testing.T) {
	c, log := bodyConfirm(5)
	r := c.rect()
	yesX := r.X + c.buttonOffset() + confirmBtnYesX
	c.HandleMouse(yesX+1, r.Y+c.buttonRow(), tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "yes" {
		t.Fatalf("Yes click in Body mode: got %v", *log)
	}

	c, log = bodyConfirm(5)
	noX := r.X + c.buttonOffset() + confirmBtnNoX
	c.HandleMouse(noX+1, r.Y+c.buttonRow(), tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("No click in Body mode: got %v", *log)
	}
}

// TestConfirm_BodyScrollsPastTheCap covers a body longer than the row
// cap: the overflow must stay reachable by wheel and arrow keys. A
// hostile format.json with dozens of entries would otherwise push its
// payload out of view, which is the whole hole Body exists to close.
func TestConfirm_BodyScrollsPastTheCap(t *testing.T) {
	c, _ := bodyConfirm(confirmMaxBodyRows + 6)
	if c.bodyRows() != confirmMaxBodyRows {
		t.Fatalf("visible rows should clamp to the cap, got %d", c.bodyRows())
	}
	c.HandleKey(key(tcell.KeyDown, 0))
	if c.Scroll() != 1 {
		t.Fatalf("Down should scroll the body, got %d", c.Scroll())
	}
	c.HandleMouse(40, 12, tcell.WheelDown)
	if c.Scroll() != 4 {
		t.Fatalf("wheel should scroll 3 rows, got %d", c.Scroll())
	}
	c.HandleKey(key(tcell.KeyPgDn, 0))
	if got, want := c.Scroll(), len(c.Body)-c.bodyRows(); got != want {
		t.Fatalf("PgDn should clamp to the last page: got %d want %d", got, want)
	}
	c.HandleKey(key(tcell.KeyPgUp, 0))
	if c.Scroll() != 0 {
		t.Fatalf("PgUp should return to the top, got %d", c.Scroll())
	}
}

// TestConfirm_MessageFormCannotScroll pins that the scroll plumbing is
// inert for the one-line form — a stray wheel event over a delete
// confirm must not blank its message.
func TestConfirm_MessageFormCannotScroll(t *testing.T) {
	c, _ := testConfirm()
	c.HandleMouse(40, 12, tcell.WheelDown)
	c.HandleKey(key(tcell.KeyDown, 0))
	if c.Scroll() != 0 {
		t.Fatalf("message-only confirm must not scroll, got %d", c.Scroll())
	}
}

// TestConfirm_DrawsBodyRowsLeftAligned pins the rendering: body rows are
// painted left-aligned inside the padding, because commands and paths
// are read left-to-right and centering them makes columns unscannable.
func TestConfirm_DrawsBodyRowsLeftAligned(t *testing.T) {
	scr := simScreen(t)
	c, _ := bodyConfirm(3)
	c.Draw(scr)
	scr.Show()

	r := c.rect()
	for i := range 3 {
		got := ""
		for x := r.X + 2; x < r.X+2+len(c.Body[i]); x++ {
			got += string(cellAt(scr, x, r.Y+4+i))
		}
		if got != c.Body[i] {
			t.Fatalf("body row %d: got %q want %q", i, got, c.Body[i])
		}
	}
}
