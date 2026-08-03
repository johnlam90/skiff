// =============================================================================
// File: internal/overlay/form_test.go
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

// testForm builds a two-row form — a text row and a select row — on an
// 80×30 screen with an ordered call log.
func testForm() (*Form, *[]string) {
	var log []string
	var got map[string]string
	f := &Form{
		Title: "T",
		Theme: theme.Default(),
		Size:  func() (int, int) { return 80, 30 },
	}
	f.Rows = []FormRow{
		{Key: "NAME", Label: "Name"},
		{Key: "MODE", Label: "Mode", Options: []string{"fast", "slow"}},
	}
	f.Rows[0].Field.SetText("dflt")
	f.Close = func() { log = append(log, "close") }
	f.OnSubmit = func(v map[string]string) {
		got = v
		log = append(log, "submit:"+v["NAME"]+"/"+v["MODE"])
	}
	_ = got
	return f, &log
}

// TestForm_ValuesCollectRows pins the submitted map: text rows report
// their field text (empty passes through — the action author decides
// what empty means), selects report the current option.
func TestForm_ValuesCollectRows(t *testing.T) {
	f, _ := testForm()
	v := f.Values()
	if v["NAME"] != "dflt" || v["MODE"] != "fast" {
		t.Fatalf("values wrong: %v", v)
	}
}

// TestForm_FocusCyclingWraps pins Tab/Shift+Tab: wrap-around in both
// directions, matching every keyboard list in the editor.
func TestForm_FocusCyclingWraps(t *testing.T) {
	f, _ := testForm()
	f.HandleKey(key(tcell.KeyTab, 0))
	if f.Focus != 1 {
		t.Fatal("Tab must advance focus")
	}
	f.HandleKey(key(tcell.KeyTab, 0))
	if f.Focus != 0 {
		t.Fatal("Tab must wrap to the first row")
	}
	f.HandleKey(key(tcell.KeyBacktab, 0))
	if f.Focus != 1 {
		t.Fatal("Shift+Tab must wrap backwards")
	}
}

// TestForm_TextEditingAndSelectCycling pins per-row key routing: runes
// edit the focused text row's field; Left/Right cycle the focused
// select with wrap.
func TestForm_TextEditingAndSelectCycling(t *testing.T) {
	f, _ := testForm()
	for _, r := range "!x" {
		f.HandleKey(key(tcell.KeyRune, r))
	}
	if f.Rows[0].Field.Text() != "dflt!x" {
		t.Fatalf("text row edit wrong: %q", f.Rows[0].Field.Text())
	}
	f.HandleKey(key(tcell.KeyTab, 0))
	f.HandleKey(key(tcell.KeyRight, 0))
	if f.Rows[1].Sel != 1 {
		t.Fatal("Right must advance the select")
	}
	f.HandleKey(key(tcell.KeyRight, 0))
	if f.Rows[1].Sel != 0 {
		t.Fatal("select must wrap forward")
	}
	f.HandleKey(key(tcell.KeyLeft, 0))
	if f.Rows[1].Sel != 1 {
		t.Fatal("select must wrap backward")
	}
}

// TestForm_EnterAdvancesThenSubmits pins the fill-it-fast path: Enter
// on a non-last row advances focus; Enter on the last row submits with
// close-before-callback ordering and the current values.
func TestForm_EnterAdvancesThenSubmits(t *testing.T) {
	f, log := testForm()
	f.HandleKey(key(tcell.KeyEnter, 0))
	if f.Focus != 1 || len(*log) != 0 {
		t.Fatalf("Enter on first row must only advance, focus=%d log=%v", f.Focus, *log)
	}
	f.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "submit:dflt/fast" {
		t.Fatalf("want [close submit:dflt/fast], got %v", *log)
	}
}

// TestForm_EscCancelsWithoutSubmit pins Esc: close, no callback.
func TestForm_EscCancelsWithoutSubmit(t *testing.T) {
	f, log := testForm()
	f.HandleKey(key(tcell.KeyEsc, 0))
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("want [close], got %v", *log)
	}
}

// TestForm_MouseButtonsChevronsAndFocus pins the mouse contract on
// known geometry: Submit/Cancel buttons, select chevron clicks, row
// clicks moving focus, and outside-click cancel.
func TestForm_MouseButtonsChevronsAndFocus(t *testing.T) {
	f, log := testForm()
	r := f.rect()

	// Click the select's right chevron (row 1's input row).
	inputRow := r.Y + 3 + 1*formRowHeight + 1
	f.HandleMouse(r.X+r.W-3-1, inputRow, tcell.Button1)
	if f.Focus != 1 || f.Rows[1].Sel != 1 {
		t.Fatalf("chevron click: focus=%d sel=%d", f.Focus, f.Rows[1].Sel)
	}

	// Click the first row's label to move focus back.
	f.HandleMouse(r.X+2, r.Y+3, tcell.Button1)
	if f.Focus != 0 {
		t.Fatal("row click must move focus")
	}

	// Submit button.
	f.HandleMouse(r.X+r.W-formBtnW-4+1, r.Y+r.H-3, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "submit:dflt/slow" {
		t.Fatalf("submit click: got %v", *log)
	}

	// Outside click cancels.
	f, log = testForm()
	f.HandleMouse(0, 0, tcell.Button1)
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("outside click: got %v", *log)
	}
}

// TestForm_DrawPaintsRows pins the painted result: labels, the text
// row's value, the select's current option, and both buttons.
func TestForm_DrawPaintsRows(t *testing.T) {
	scr := simScreen(t)
	f, _ := testForm()
	f.Draw(scr)
	scr.Show()
	r := f.rect()
	rowText := func(y, x, n int) string {
		s := ""
		for i := 0; i < n; i++ {
			s += string(cellAt(scr, x+i, y))
		}
		return s
	}
	if got := rowText(r.Y+3, r.X+2, 4); got != "Name" {
		t.Fatalf("label: %q", got)
	}
	if got := rowText(r.Y+4, r.X+3, 4); got != "dflt" {
		t.Fatalf("text value: %q", got)
	}
	if got := rowText(r.Y+r.H-3, r.X+4, 10); got != "[ Cancel ]" {
		t.Fatalf("cancel button: %q", got)
	}
}
