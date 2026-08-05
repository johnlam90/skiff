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
		log = append(log, "submit:"+v["NAME"]+"/"+v["MODE"])
	}
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

// shortForm builds a three-prompt form — the shape a real custom action
// produces — on a terminal at skiff's 40×10 floor. Its natural height is
// 13 rows, so the frame has to window them.
func shortForm() (*Form, *[]string) {
	f, log := testForm()
	f.Size = func() (int, int) { return 40, 10 }
	f.Rows = []FormRow{
		{Key: "HOST", Label: "Host"},
		{Key: "PATH", Label: "Remote path"},
		{Key: "MODE", Label: "Mode", Options: []string{"fast", "slow"}},
	}
	return f, log
}

// TestForm_ShortScreenWindowsRowsInsteadOfOverflowing is the case that
// forced Form to grow a scroll offset at all: 7 chrome rows plus 2 per
// prompt means a three-prompt action wants 13 rows, and a phone in
// landscape with the keyboard up has ten. Unwindowed, the frame claimed
// all 13 and the Cancel / Submit row — at r.H-3 — landed three rows below
// the last cell of the screen, so the form could be filled in and never
// submitted with the mouse.
func TestForm_ShortScreenWindowsRowsInsteadOfOverflowing(t *testing.T) {
	const scrW, scrH = 40, 10
	f, _ := shortForm()

	r := f.rect()
	if r.X < 0 || r.X+r.W > scrW || r.Y+r.H > scrH {
		t.Fatalf("frame off screen: %+v on %dx%d", r, scrW, scrH)
	}
	if btnY := r.Y + r.H - 3; btnY < r.Y+3 || btnY >= scrH {
		t.Fatalf("button row at %d is outside the screen or over the fields", btnY)
	}
	if n := f.visibleRows(); n != 1 {
		t.Fatalf("a 10-row screen shows %d field rows, want 1", n)
	}
	if f.maxScroll() != 2 {
		t.Fatalf("two rows should be windowed out, maxScroll=%d", f.maxScroll())
	}
}

// TestForm_TabScrollsHiddenRowIntoView pins the only thing that moves the
// window: Tab. A field the user cannot reach is a field they cannot fill,
// so focus and the viewport have to travel together — and the row that
// arrives is the one that gets drawn and clicked.
func TestForm_TabScrollsHiddenRowIntoView(t *testing.T) {
	f, _ := shortForm()
	f.HandleKey(key(tcell.KeyTab, 0))
	f.HandleKey(key(tcell.KeyTab, 0))
	if f.Focus != 2 {
		t.Fatalf("two Tabs should land on row 2, got %d", f.Focus)
	}
	if f.scroll != 2 {
		t.Fatalf("the window should have followed focus, scroll=%d", f.scroll)
	}

	scr := simScreen(t)
	scr.SetSize(40, 10)
	f.Draw(scr)
	scr.Show()
	r := f.rect()
	if got := rowRunes(scr, r.X+2, r.Y+3, 4); got != "Mode" {
		t.Fatalf("the scrolled-to row should be painted, got %q", got)
	}
	// ▲ in the divider says the first two rows are above the fold.
	if got := cellAt(scr, r.X+3, r.Y+2); got != '▲' {
		t.Fatalf("no scrolled-above marker in the divider, got %q", got)
	}

	// A click on the single visible row must focus row 2, not row 0 —
	// the hit test walks the window, not the row list.
	f.HandleMouse(r.X+5, r.Y+3, tcell.Button1)
	if f.Focus != 2 {
		t.Fatalf("click on the visible row focused %d, want 2", f.Focus)
	}
}

// TestForm_TallScreenNeverScrolls is the no-regression half: on an
// ordinary terminal the whole form fits, so the window must be inert and
// the ▼ marker absent — the frame looks exactly as it always did.
func TestForm_TallScreenNeverScrolls(t *testing.T) {
	f, _ := shortForm()
	f.Size = func() (int, int) { return 80, 30 }
	if f.visibleRows() != 3 || f.maxScroll() != 0 {
		t.Fatalf("a 30-row screen must show all 3 rows: visible=%d maxScroll=%d",
			f.visibleRows(), f.maxScroll())
	}
	f.HandleKey(key(tcell.KeyTab, 0))
	f.HandleKey(key(tcell.KeyTab, 0))
	if f.scroll != 0 {
		t.Fatalf("nothing to scroll, yet scroll=%d", f.scroll)
	}

	scr := simScreen(t)
	f.Draw(scr)
	scr.Show()
	r := f.rect()
	if got := cellAt(scr, r.X+3, r.Y+r.H-1); got == '▼' {
		t.Fatal("a form that fits must not claim rows are hidden below")
	}
}

// rowRunes reads n cells starting at (x, y) as a string.
func rowRunes(scr tcell.SimulationScreen, x, y, n int) string {
	out := make([]rune, 0, n)
	for i := range n {
		out = append(out, cellAt(scr, x+i, y))
	}
	return string(out)
}
