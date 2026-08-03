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
	a.findOpen = true // strips are still flag-tracked (not on the stack)
	if a.leaderStripVisible() {
		t.Fatal("strip must hide under the find bar")
	}
	a.findOpen = false
	a.lastEscape = time.Now().Add(-doubleEscMs - time.Millisecond)
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
