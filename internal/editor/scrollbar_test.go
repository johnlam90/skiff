// =============================================================================
// File: internal/editor/scrollbar_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the editor scrollbar: pure thumb geometry, click-to-scroll
// mapping, and the rendered track / thumb / git change-map column.

package editor

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// TestScrollbarGeomProportions pins the thumb arithmetic: proportional
// length, top at scroll 0, bottom of the track at max scroll.
func TestScrollbarGeomProportions(t *testing.T) {
	// 100 lines in a 20-row viewport: thumb 20*20/100 = 4 rows.
	start, length, ok := scrollbarGeom(100, 20, 0)
	if !ok {
		t.Fatal("bar should exist when total > viewH")
	}
	if length != 4 {
		t.Fatalf("thumb length: got %d, want 4", length)
	}
	if start != 0 {
		t.Fatalf("thumb at scroll 0 should start at 0, got %d", start)
	}
	// At max scroll (total - viewH = 80) the thumb hugs the bottom.
	start, length, _ = scrollbarGeom(100, 20, 80)
	if start != 20-length {
		t.Fatalf("thumb at max scroll: start %d, want %d", start, 20-length)
	}
	// Overscroll past max clamps rather than pushing the thumb out.
	start, _, _ = scrollbarGeom(100, 20, 200)
	if start != 20-length {
		t.Fatalf("overscrolled thumb start %d, want %d", start, 20-length)
	}
}

// TestScrollbarGeomHiddenWhenFits: a file shorter than the viewport has
// no bar — a full-height thumb is pure noise.
func TestScrollbarGeomHiddenWhenFits(t *testing.T) {
	if _, _, ok := scrollbarGeom(10, 20, 0); ok {
		t.Fatal("bar should hide when the file fits")
	}
	if _, _, ok := scrollbarGeom(20, 20, 0); ok {
		t.Fatal("bar should hide when the file exactly fits")
	}
}

// TestScrollTargetClamps maps bar clicks to ScrollY: top → 0, bottom →
// max scroll, middle → proportional, all clamped into range.
func TestScrollTargetClamps(t *testing.T) {
	if got := scrollTargetForThumb(100, 20, 0); got != 0 {
		t.Fatalf("top click: got %d, want 0", got)
	}
	if got := scrollTargetForThumb(100, 20, 19); got != 80 {
		t.Fatalf("bottom click: got %d, want 80 (max scroll)", got)
	}
	mid := scrollTargetForThumb(100, 20, 10)
	if mid <= 0 || mid >= 80 {
		t.Fatalf("middle click should land inside the range, got %d", mid)
	}
	if got := scrollTargetForThumb(100, 20, -5); got != 0 {
		t.Fatalf("off-track click must clamp, got %d", got)
	}
}

// TestRenderDrawsThumbAndMarks renders a long buffer and asserts the
// last editor column carries a thumb at the top and a git change mark
// at the scaled position of a modified line.
func TestRenderDrawsThumbAndMarks(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)

	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "x"
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}, StyleStale: true}
	tab.initUndo()
	// Mark line 50 modified — scaled row = 50*10/100 = 5.
	tab.GitLines = map[int]GitLineChange{50: GitLineModified}

	th := theme.Default()
	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()

	cells, w, _ := scr.GetContents()
	barX := 39
	if got := cells[0*w+barX].Runes[0]; got != '┃' {
		t.Fatalf("row 0 of the bar should carry the thumb, got %q", got)
	}
	if got := cells[5*w+barX].Runes[0]; got != '▐' {
		t.Fatalf("row 5 should carry the change mark, got %q", got)
	}
	fg, _, _ := cells[5*w+barX].Style.Decompose()
	if fg != th.GitModified {
		t.Fatalf("mark color: got %v, want GitModified", fg)
	}
}

// TestScrollbarHiddenLeavesColumnToText: with a short buffer the last
// column stays ordinary editor background (no track glyphs).
func TestScrollbarHiddenLeavesColumnToText(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)

	tab := &Tab{Buffer: NewBuffer("just\na few\nlines"), StyleStale: true}
	tab.initUndo()
	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	scr.Show()

	cells, w, _ := scr.GetContents()
	if got := cells[0*w+39].Runes[0]; got != ' ' {
		t.Fatalf("no bar expected for a short file, got %q", got)
	}
}
