// =============================================================================
// File: internal/overlay/prompt_test.go
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

// testPrompt builds a Prompt on an 80×24 screen, recording the order of
// Close and OnSubmit calls so tests can pin capture-then-close.
func testPrompt() (*Prompt, *[]string) {
	var log []string
	p := &Prompt{
		Title: "T",
		Theme: theme.Default(),
		Size:  func() (int, int) { return 80, 24 },
	}
	p.Close = func() { log = append(log, "close") }
	p.OnSubmit = func(v string) { log = append(log, "submit:"+v) }
	return p, &log
}

// TestPrompt_SubmitClosesBeforeCallback pins the capture-then-close
// ordering every modal flow relies on: the overlay must already be off
// the stack when OnSubmit runs, so a callback that opens the next
// overlay is never popped by this one's teardown. The value arrives
// trimmed.
func TestPrompt_SubmitClosesBeforeCallback(t *testing.T) {
	p, log := testPrompt()
	p.Field.SetText("  hello  ")
	p.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "submit:hello" {
		t.Fatalf("want [close submit:hello], got %v", *log)
	}
}

// TestPrompt_EmptySubmitKeepsOpen pins the empty-value guard: Enter on a
// blank input must neither close nor fire the callback — the user can
// still cancel deliberately with Esc.
func TestPrompt_EmptySubmitKeepsOpen(t *testing.T) {
	p, log := testPrompt()
	p.Field.SetText("   ")
	p.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if len(*log) != 0 {
		t.Fatalf("empty submit must be a no-op, got %v", *log)
	}
}

// TestPrompt_EscCancelsWithoutSubmit pins Esc: close, no callback.
func TestPrompt_EscCancelsWithoutSubmit(t *testing.T) {
	p, log := testPrompt()
	p.Field.SetText("x")
	p.HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("want [close], got %v", *log)
	}
}

// TestPrompt_TypingEditsField pins that non-command keys flow into the
// field: the prompt owns the keyboard, the field owns the editing.
func TestPrompt_TypingEditsField(t *testing.T) {
	p, _ := testPrompt()
	for _, r := range "ab" {
		p.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	p.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if p.Field.Text() != "a" {
		t.Fatalf("typed text wrong: %q", p.Field.Text())
	}
}

// TestPrompt_MouseButtonsAndOutsideClick pins the mouse contract on the
// known 80×24 geometry: OK submits, Cancel cancels, a click outside the
// rect cancels, and motion over a button moves the hover highlight.
func TestPrompt_MouseButtonsAndOutsideClick(t *testing.T) {
	r := Centered(80, 24, promptWidth, promptHeight)

	p, log := testPrompt()
	p.Field.SetText("v")
	p.HandleMouse(r.X+promptBtnOKX+1, r.Y+6, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "submit:v" {
		t.Fatalf("OK click: want submit, got %v", *log)
	}

	p, log = testPrompt()
	p.HandleMouse(r.X+promptBtnCancelX+1, r.Y+6, tcell.Button1)
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("Cancel click: want close, got %v", *log)
	}

	p, log = testPrompt()
	p.HandleMouse(r.X-1, r.Y-1, tcell.Button1)
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("outside click: want close, got %v", *log)
	}

	p, _ = testPrompt()
	p.HandleMouse(r.X+promptBtnCancelX+1, r.Y+6, tcell.ButtonNone)
	if p.Hover != 0 {
		t.Fatalf("hover should move to Cancel, got %d", p.Hover)
	}
}

// TestPrompt_FieldClickMovesCursor pins in-field clicks: the caret jumps
// to the clicked rune, clamped to the value's bounds.
func TestPrompt_FieldClickMovesCursor(t *testing.T) {
	r := Centered(80, 24, promptWidth, promptHeight)
	p, _ := testPrompt()
	p.Field.SetText("hello")
	p.HandleMouse(r.X+3+2, r.Y+4, tcell.Button1) // click on the 'l'
	if p.Field.Cursor != 2 {
		t.Fatalf("cursor should land on clicked rune, got %d", p.Field.Cursor)
	}
	p.HandleMouse(r.X+3+40, r.Y+4, tcell.Button1) // past the value
	if p.Field.Cursor != 5 {
		t.Fatalf("cursor must clamp to value end, got %d", p.Field.Cursor)
	}
}

// TestPrompt_DrawPaintsChromeAndValue pins the painted result: frame
// title, hint line, and the field's value all land on their rows.
func TestPrompt_DrawPaintsChromeAndValue(t *testing.T) {
	scr := simScreen(t)
	p, _ := testPrompt()
	p.Title = "Rename"
	p.Hint = "in /tmp"
	p.Field.SetText("name.go")
	p.Draw(scr)
	scr.Show()

	r := Centered(80, 24, promptWidth, promptHeight)
	rowText := func(y, x, n int) string {
		s := ""
		for i := 0; i < n; i++ {
			s += string(cellAt(scr, x+i, y))
		}
		return s
	}
	if got := rowText(r.Y+1, r.X+1, 7); got != " Rename" {
		t.Fatalf("title row: %q", got)
	}
	if got := rowText(r.Y+3, r.X+2, 7); got != "in /tmp" {
		t.Fatalf("hint row: %q", got)
	}
	if got := rowText(r.Y+4, r.X+3, 7); got != "name.go" {
		t.Fatalf("field row: %q", got)
	}
	if got := rowText(r.Y+6, r.X+promptBtnOKX, 8); got != "[  OK  ]" {
		t.Fatalf("OK button: %q", got)
	}
}
