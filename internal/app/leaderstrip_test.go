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
	a.menuOpen = true
	if a.leaderStripVisible() {
		t.Fatal("strip must hide under the menu")
	}
	a.menuOpen = false
	a.findOpen = true
	if a.leaderStripVisible() {
		t.Fatal("strip must hide under the find bar")
	}
	a.findOpen = false
	a.lastEscape = time.Now().Add(-doubleEscMs - time.Millisecond)
	if a.leaderStripVisible() {
		t.Fatal("strip should expire with the leader window")
	}
}

// TestLeaderStripRenders draws the strip and checks every binding is
// visible — on a 120-col screen the full table needs two rows, and the
// wrap must carry the tail instead of clipping it.
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
	both := readRow(h-3) + "\n" + readRow(h-2)
	for _, want := range []string{"s save", "u undo", "g git panel"} {
		if !strings.Contains(both, want) {
			t.Fatalf("strip missing %q:\n%s", want, both)
		}
	}
}
