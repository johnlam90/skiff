// =============================================================================
// File: internal/app/leaderstrip_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the leader cheat-strip: the one-row key overview shown while
// the Esc-leader window is armed.

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestDrawStripSegment_EmojiAdvancesByCells pins the segment cursor's
// unit: a ZWJ family emoji is five runes painted in two cells, and the
// returned x must advance by the CELLS (2), not the rune count (5) —
// otherwise the next segment lands three columns adrift of the glyph.
func TestDrawStripSegment_EmojiAdvancesByCells(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init sim screen: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 5)
	st := tcell.StyleDefault

	const famEmoji = "\U0001F468\u200D\U0001F469\u200D\U0001F466"
	x := drawStripSegment(scr, 0, 0, 40, famEmoji, st)
	if x != 2 {
		t.Fatalf("emoji segment advanced x to %d, want 2", x)
	}
	// The next segment must butt up against the emoji's two cells.
	if x = drawStripSegment(scr, x, 0, 40, "ok", st); x != 4 {
		t.Fatalf("follow-up segment advanced x to %d, want 4", x)
	}
	scr.Show()
	cells, _, _ := scr.GetContents()
	if c := cells[2]; len(c.Runes) == 0 || c.Runes[0] != 'o' {
		t.Fatalf("cell 2 = %q, want o right after the emoji", c.Runes)
	}
}

// TestLeaderBindingsAllHaveDescs pins the invariant the strip depends
// on: every leader binding carries a human-readable description. An
// empty desc would render as a bare rune with no explanation.
func TestLeaderBindingsAllHaveDescs(t *testing.T) {
	for _, b := range leaderBindings() {
		if strings.TrimSpace(b.desc) == "" {
			t.Errorf("leader %q has no desc", b.key)
		}
	}
}

// TestLeaderStripVisibility: the strip shows only while the leader
// window is armed, and never underneath an open modal or bar that owns
// the keyboard (where leader keys can't fire anyway).
func TestLeaderStripVisibility(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.leaderStripVisible() {
		t.Fatal("strip visible with no Esc pressed")
	}
	a.lastEscape = time.Now()
	if !a.leaderStripVisible() {
		t.Fatal("strip should show while the leader window is armed")
	}
	// Overlay presence comes from the stack, so the menu must be opened
	// for real — a hand-flipped flag no longer counts.
	a.openMenu()
	a.lastEscape = time.Now() // re-arm; openMenu's closeAllModals path is irrelevant to the window
	if a.leaderStripVisible() {
		t.Fatal("strip must hide under the menu")
	}
	a.closeMenu()
	a.strip = &findStrip{a: a} // strips live in their own slot, not on the stack
	if a.leaderStripVisible() {
		t.Fatal("strip must hide under the find bar")
	}
	a.strip = nil
	a.lastEscape = time.Now().Add(-doubleEscWindow - time.Millisecond)
	if a.leaderStripVisible() {
		t.Fatal("strip should expire with the leader window")
	}
}

// TestLeaderStripRenders draws the strip and checks EVERY binding is
// visible — the strip must grow rows with the table instead of clipping
// the tail at a fixed cap (which is exactly what happened when the
// clipboard bindings pushed the table past two 120-col rows).
func TestLeaderStripRenders(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.lastEscape = time.Now()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show() // SimulationScreen serves GetContents from the *front* buffer.

	cells, w, h := scr.GetContents()
	readRow := func(y int) string {
		row := make([]rune, 0, w)
		for x := 0; x < w; x++ {
			row = append(row, cells[y*w+x].Runes[0])
		}
		return string(row)
	}
	// Read a generous window above the status bar — the strip sizes
	// itself to the table, so the test shouldn't assume a row count.
	var strip strings.Builder
	for y := h - 9; y < h-1; y++ {
		strip.WriteString(readRow(y))
		strip.WriteString("\n")
	}
	// Collapse whitespace before matching: the wrap breaks between
	// segments, so a key and its description may land on different rows
	// with the continuation indent between them.
	flat := strings.Join(strings.Fields(strip.String()), " ")
	for _, b := range leaderBindings() {
		want := string(b.key) + " " + b.desc
		if !strings.Contains(flat, want) {
			t.Fatalf("strip missing %q:\n%s", want, strip.String())
		}
	}
}

// armedStripApp opens a file at w×h through the resize path, arms the
// leader and paints — the fixture every strip-at-a-size test starts
// from. The returned rows are the painted screen, top to bottom.
func armedStripApp(t *testing.T, w, h int) (*App, []string) {
	t.Helper()
	a := minSizeApp(t, w, h)
	a.lastEscape = time.Now()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	rows := make([]string, h)
	for y := range h {
		rows[y] = screenLine(scr, y)
	}
	return a, rows
}

// TestLeaderStrip_PaintsWithinTheFloor pins the row cap at the two
// sizes that broke it: at 40×10 the full table needed nine rows and
// painted over the tab bar, and at 48×16 eight of sixteen rows went to
// the strip. Now the strip picks a form that fits its budget, row 0
// still holds the ≡ button, and every bound key is still on it — the
// compact forms drop air and descriptions, never keys.
func TestLeaderStrip_PaintsWithinTheFloor(t *testing.T) {
	for _, size := range [][2]int{{40, 10}, {48, 16}} {
		w, h := size[0], size[1]
		a, rows := armedStripApp(t, w, h)
		if !strings.Contains(rows[0], "≡") {
			t.Fatalf("%dx%d: row 0 lost the tab bar: %q", w, h, rows[0])
		}
		painted := 0
		for y := 1; y < h-1; y++ {
			if strings.Contains(rows[y], "Esc") {
				painted++
			}
		}
		if painted == 0 {
			t.Fatalf("%dx%d: the strip painted nothing:\n%s", w, h, strings.Join(rows, "\n"))
		}
		if budget := a.stripRowBudget(); painted > budget {
			t.Fatalf("%dx%d: strip took %d rows, budget is %d", w, h, painted, budget)
		}
		flat := strings.Join(rows[1:h-1], " ")
		for _, b := range leaderBindings() {
			if !strings.ContainsRune(flat, b.key) {
				t.Fatalf("%dx%d: key %q missing from the strip:\n%s", w, h, b.key, strings.Join(rows, "\n"))
			}
		}
	}
}

// TestLeaderStripRows_DegradesBeforeItClips pins the preference order
// on the real table: with rows to spare the verbose form (" · "
// separators) is used; with fewer, the single-space form keeps every
// description; with fewer still, the keys-only form keeps every key and
// points at Esc ?; and only when THAT overruns does the strip clip —
// spending its last row on the tail hint, never returning nothing.
func TestLeaderStripRows_DegradesBeforeItClips(t *testing.T) {
	st := stripStyles{}
	bindings := leaderBindings()
	join := func(rows [][]stripSegment) string {
		var sb strings.Builder
		for _, row := range rows {
			for _, seg := range row {
				sb.WriteString(seg.text)
			}
			sb.WriteString("\n")
		}
		return sb.String()
	}
	forms := leaderStripForms(bindings, st)
	full := len(wrapStripSegments(forms[0], 60))
	compact := len(wrapStripSegments(forms[1], 60))
	keys := len(wrapStripSegments(forms[2], 60))
	if !(full > compact && compact > keys) {
		t.Fatalf("precondition: forms should get shorter at 60 cols, got %d/%d/%d rows", full, compact, keys)
	}

	rows, clipped := leaderStripRows(bindings, 60, full, st)
	if clipped || !strings.Contains(join(rows), " · ") {
		t.Fatalf("with %d rows the verbose form should be used, got clipped=%v:\n%s", full, clipped, join(rows))
	}
	rows, clipped = leaderStripRows(bindings, 60, full-1, st)
	if clipped || strings.Contains(join(rows), " · ") || !strings.Contains(join(rows), bindings[0].desc) {
		t.Fatalf("one row short of verbose should drop to the compact form, got clipped=%v:\n%s", clipped, join(rows))
	}
	rows, clipped = leaderStripRows(bindings, 60, keys, st)
	if clipped || strings.Contains(join(rows), bindings[0].desc) || !strings.Contains(join(rows), "Esc ? for all") {
		t.Fatalf("at %d rows the keys-only form should be used, got clipped=%v:\n%s", keys, clipped, join(rows))
	}
	rows, clipped = leaderStripRows(bindings, 60, keys-1, st)
	if !clipped || len(rows) != keys-1 {
		t.Fatalf("under the keys-only height the strip should clip to %d rows, got %d clipped=%v", keys-1, len(rows), clipped)
	}
	if last := rows[len(rows)-1]; len(last) != 1 || last[0].text != leaderStripTailHint {
		t.Fatalf("clipped strip's last row = %+v, want the tail hint", last)
	}
	if rows, _ := leaderStripRows(bindings, 60, 0, st); rows != nil {
		t.Fatal("no rows to spend must yield no strip, not a hint with nowhere to go")
	}
}

// TestLeaderStrip_NeverTouchesTheTabBar is the fence for the failure
// that motivated the cap: a table taller than the rows above the status
// bar must end on the tail hint below row 0, not paint through the tab
// bar and not silently vanish. A synthetic sixty-binding table at the
// minimum size forces the clip on a real App.
func TestLeaderStrip_NeverTouchesTheTabBar(t *testing.T) {
	a := minSizeApp(t, minWidth, minHeight)
	var many []leaderBinding
	for i := 0; i < 200; i++ {
		many = append(many, leaderBinding{key: rune('a' + i%26), desc: "do a thing"})
	}
	rows, clipped := leaderStripRows(many, a.width, min(a.stripRowBudget(), a.height-2), stripStyles{})
	if !clipped {
		t.Fatal("precondition: two hundred bindings should not fit the minimum size")
	}
	if len(rows) > a.height-2 {
		t.Fatalf("clipped strip still %d rows tall, which would reach row 0", len(rows))
	}
	if last := rows[len(rows)-1][0].text; last != leaderStripTailHint {
		t.Fatalf("last row = %q, want the tail hint", last)
	}
}
