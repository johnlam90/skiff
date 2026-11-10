// =============================================================================
// File: internal/overlay/field_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
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

// fieldRow reads n cells of row y from x as painted, joining each
// cell's runes (base + combining) so a cluster reads back whole. The
// continuation cell of a wide glyph is stepped over, which is what
// lets "日本語" read back as "日本語".
func fieldRow(scr tcell.SimulationScreen, x, y, n int) string {
	cells, w, _ := scr.GetContents()
	out := ""
	for i := 0; i < n; {
		c := cells[y*w+x+i]
		out += string(c.Runes)
		step := uniseg.StringWidth(string(c.Runes))
		if step < 1 {
			step = 1
		}
		i += step
	}
	return out
}

// TestField_DrawPaintsWideGlyphsByCell pins the audit's repro: a field
// holding "日本語.go" used to treat one rune as one cell, so each
// ideograph was overwritten by the half of its neighbour and the caret
// sat three cells short. Every glyph must survive, at its own cells,
// with the caret after 3×2 + 3 = 9 cells of text.
func TestField_DrawPaintsWideGlyphsByCell(t *testing.T) {
	scr := simScreen(t)
	var f Field
	f.SetText("日本語.go")
	f.Draw(scr, 2, 1, 20, tcell.StyleDefault, true)
	scr.Show()
	if got := strings.TrimRight(fieldRow(scr, 2, 1, 20), " "); got != "日本語.go" {
		t.Fatalf("painted %q, want every glyph of 日本語.go", got)
	}
	if got := cellAt(scr, 2+6, 1); got != '.' {
		t.Fatalf("the dot belongs at cell 6 (after three 2-cell glyphs), found %q", got)
	}
	if cx, cy, vis := scr.GetCursor(); !vis || cx != 2+9 || cy != 1 {
		t.Fatalf("caret at (%d,%d) visible=%v, want x=%d after 9 cells of text", cx, cy, vis, 2+9)
	}
}

// TestField_CombiningMarkStaysOnItsBase pins the zero-width half of the
// same bug: "e" + U+0301 is one cluster in one cell. It must paint as a
// single cell carrying both runes, and the text after it must not be
// pushed a cell to the right by a mark that occupies none.
func TestField_CombiningMarkStaysOnItsBase(t *testing.T) {
	scr := simScreen(t)
	var f Field
	f.SetText("café!")
	f.Draw(scr, 2, 1, 20, tcell.StyleDefault, true)
	scr.Show()
	cells, w, _ := scr.GetContents()
	if got := string(cells[1*w+2+3].Runes); got != "é" {
		t.Fatalf("cell 3 holds %q, want the e with its accent", got)
	}
	if got := cellAt(scr, 2+4, 1); got != '!' {
		t.Fatalf("cell 4 holds %q, want the '!' right after the accented e", got)
	}
	if cx, _, _ := scr.GetCursor(); cx != 2+5 {
		t.Fatalf("caret x = %d, want %d (five cells of text)", cx, 2+5)
	}
}

// TestField_MovesAndDeletesByCluster pins the editing half: Left and
// Right step over a whole cluster and Backspace / Delete remove one, so
// the caret never rests between a letter and its combining mark or
// inside a ZWJ family, and no half-cluster is ever left in the value.
func TestField_MovesAndDeletesByCluster(t *testing.T) {
	const family = "👨‍👩‍👧"
	var f Field
	f.SetText("aé" + family)
	f.HandleKey(key(tcell.KeyLeft, 0))
	if f.Cursor != 3 {
		t.Fatalf("Left over the family should land before it at rune 3, got %d", f.Cursor)
	}
	f.HandleKey(key(tcell.KeyLeft, 0))
	if f.Cursor != 1 {
		t.Fatalf("Left over e+accent should land at rune 1, got %d", f.Cursor)
	}
	f.HandleKey(key(tcell.KeyRight, 0))
	if f.Cursor != 3 {
		t.Fatalf("Right over e+accent should land at rune 3, got %d", f.Cursor)
	}
	f.HandleKey(key(tcell.KeyBackspace2, 0))
	if f.Text() != "a"+family || f.Cursor != 1 {
		t.Fatalf("Backspace should remove e and its accent together, got %q cursor=%d", f.Text(), f.Cursor)
	}
	f.HandleKey(key(tcell.KeyDelete, 0))
	if f.Text() != "a" || f.Cursor != 1 {
		t.Fatalf("Delete should remove the whole family, got %q cursor=%d", f.Text(), f.Cursor)
	}
}

// TestField_ScrollWindowCountsCells pins the horizontal scroll: ten
// ideographs are twenty cells, so in a 10-cell field the window must
// slide until the caret's cell is inside it — a rune-counting window
// saw ten runes "fitting" and left the caret off the right edge.
func TestField_ScrollWindowCountsCells(t *testing.T) {
	scr := simScreen(t)
	var f Field
	f.SetText(strings.Repeat("日", 10))
	f.Draw(scr, 5, 3, 10, tcell.StyleDefault, true)
	if f.Scroll() == 0 {
		t.Fatal("twenty cells of text in a ten-cell field must scroll")
	}
	cx, _, vis := scr.GetCursor()
	if !vis || cx < 5 || cx >= 5+10 {
		t.Fatalf("caret x = %d (visible=%v), want it inside the field [5,15)", cx, vis)
	}
	scr.Show()
	// Nothing may paint past the field's right padding cell.
	if got := cellAt(scr, 5+11, 3); got != ' ' {
		t.Fatalf("text leaked past the field: %q at x=%d", got, 5+11)
	}
}

// TestField_ClickAtMapsCellsToClusterBoundaries pins click-to-caret:
// the cell offset is walked in cells, so a click on either half of a
// two-cell glyph lands before that glyph, a click on a later glyph
// counts the wide ones before it as two cells each, and a click past
// the text parks at the end.
func TestField_ClickAtMapsCellsToClusterBoundaries(t *testing.T) {
	var f Field
	f.SetText("日本語.go")
	for rel, want := range map[int]int{0: 0, 1: 0, 2: 1, 3: 1, 4: 2, 6: 3, 7: 4, 8: 5, 9: 6, 40: 6, -1: 0} {
		f.ClickAt(rel)
		if f.Cursor != want {
			t.Fatalf("click at cell %d: cursor %d, want %d", rel, f.Cursor, want)
		}
	}
	f.SetText("éx")
	f.ClickAt(1)
	if f.Cursor != 2 {
		t.Fatalf("click on the cell after e+accent: cursor %d, want 2 (never between e and its mark)", f.Cursor)
	}
}
