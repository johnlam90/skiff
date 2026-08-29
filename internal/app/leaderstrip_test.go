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
