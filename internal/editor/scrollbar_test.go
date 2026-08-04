// =============================================================================
// File: internal/editor/scrollbar_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the editor scrollbar: the visibility rule, the click →
// ScrollY mapping, and the rendered track / thumb / git change-map
// column. The thumb arithmetic itself lives in (and is tested by)
// internal/scrollbar — these tests pin what the editor paints with it.

package editor

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// longTab builds a text tab of n identical lines — the fixture every
// bar-rendering test below needs.
func longTab(n int) *Tab {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "x"
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}, StyleStale: true}
	tab.initUndo()
	return tab
}

// barColumn renders tab into a fresh w×h simulation screen and returns
// the runes and foregrounds of the rightmost column — the scrollbar.
func barColumn(t *testing.T, tab *Tab, th theme.Theme, w, h int) ([]rune, []tcell.Color, []tcell.Color) {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(w, h)
	tab.Render(scr, th, 0, 0, w, h)
	scr.Show()

	cells, cw, _ := scr.GetContents()
	runes := make([]rune, h)
	fgs := make([]tcell.Color, h)
	bgs := make([]tcell.Color, h)
	for row := 0; row < h; row++ {
		c := cells[row*cw+w-1]
		runes[row] = c.Runes[0]
		fgs[row], bgs[row], _ = c.Style.Decompose()
	}
	return runes, fgs, bgs
}

// TestScrollbarVisibleRule: only text tabs taller than the viewport get
// a bar. A file that fits, a zero-height viewport, and an image preview
// all draw nothing.
func TestScrollbarVisibleRule(t *testing.T) {
	tab := longTab(100)
	if !tab.ScrollbarVisible(20) {
		t.Fatal("100 lines in 20 rows should draw a bar")
	}
	if tab.ScrollbarVisible(100) {
		t.Fatal("a file that exactly fits should not draw a bar")
	}
	if tab.ScrollbarVisible(0) {
		t.Fatal("a zero-height viewport should not draw a bar")
	}
	tab.Mode = imageMode
	if tab.ScrollbarVisible(20) {
		t.Fatal("image previews have no scrollbar")
	}
}

// TestScrollTargetForClickClamps: the tab's click mapping is the shared
// inverse — top row parks at 0, bottom row at max scroll.
func TestScrollTargetForClickClamps(t *testing.T) {
	tab := longTab(100)
	if got := tab.ScrollTargetForClick(20, 0); got != 0 {
		t.Fatalf("top click: got %d, want 0", got)
	}
	if got := tab.ScrollTargetForClick(20, 19); got != 80 {
		t.Fatalf("bottom click: got %d, want 80", got)
	}
}

// TestRenderDrawsSolidThumbOnShadedTrack is the regression test for the
// bug this bar had: it was painted with box-drawing hairlines (│ / ┃),
// two thin strokes in two similar mid-blues, and users could not see it
// at all. Every row must now be a full-cell block, and the thumb rows
// must carry a different glyph from the track rows.
func TestRenderDrawsSolidThumbOnShadedTrack(t *testing.T) {
	// 100 lines in 10 rows: thumb 10*10/100 = 1 row, at the top.
	tab := longTab(100)
	th := theme.Default()
	runes, fgs, _ := barColumn(t, tab, th, 40, 10)

	if runes[0] != scrollbar.Thumb {
		t.Fatalf("row 0 should be the solid thumb %q, got %q", scrollbar.Thumb, runes[0])
	}
	if fgs[0] != th.Muted {
		t.Fatalf("idle thumb fg: got %v, want Muted", fgs[0])
	}
	for row := 1; row < 10; row++ {
		if runes[row] != scrollbar.Track {
			t.Fatalf("row %d should be track %q, got %q", row, scrollbar.Track, runes[row])
		}
		if fgs[row] != th.Subtle {
			t.Fatalf("row %d track fg: got %v, want Subtle", row, fgs[row])
		}
	}
}

// TestRenderThumbFollowsScroll: scrolling moves the thumb down the
// track, and the rows it vacates go back to being track.
func TestRenderThumbFollowsScroll(t *testing.T) {
	tab := longTab(100)
	tab.ScrollY = 90 // past max (100-10); the thumb pins to the bottom.
	runes, _, _ := barColumn(t, tab, theme.Default(), 40, 10)

	if runes[9] != scrollbar.Thumb {
		t.Fatalf("scrolled to the end, row 9 should be the thumb, got %q", runes[9])
	}
	if runes[0] != scrollbar.Track {
		t.Fatalf("row 0 should be back to track, got %q", runes[0])
	}
}

// TestRenderThumbBrightensWhileDragging pins the drag feedback: idle the
// thumb sits in Muted, and with the app's presentational flag set it
// brightens to Accent — the same idle/active language drawSplitter uses.
func TestRenderThumbBrightensWhileDragging(t *testing.T) {
	th := theme.Default()

	idle := longTab(100)
	_, idleFgs, _ := barColumn(t, idle, th, 40, 10)
	if idleFgs[0] != th.Muted {
		t.Fatalf("idle thumb: got %v, want Muted", idleFgs[0])
	}

	dragging := longTab(100)
	dragging.ScrollbarActive = true
	dragRunes, dragFgs, _ := barColumn(t, dragging, th, 40, 10)
	if dragFgs[0] != th.Accent {
		t.Fatalf("dragged thumb: got %v, want Accent", dragFgs[0])
	}
	if dragRunes[0] != scrollbar.Thumb {
		t.Fatalf("dragging must not change the glyph, got %q", dragRunes[0])
	}
	// Only the thumb brightens — the track stays put.
	if dragFgs[5] != th.Subtle {
		t.Fatalf("track fg while dragging: got %v, want Subtle", dragFgs[5])
	}
}

// TestRenderGitMarksRideTheBar: change marks keep their own glyph and
// git colour, and on a thumb row they take the thumb's colour as their
// background so the bar reads as continuous rather than holed.
func TestRenderGitMarksRideTheBar(t *testing.T) {
	tab := longTab(100)
	// Row = line*viewH/total: line 0 → row 0 (on the thumb), line 50 →
	// row 5 (on the track).
	tab.GitLines = map[int]GitLineChange{0: GitLineAdded, 50: GitLineModified}
	th := theme.Default()
	runes, fgs, bgs := barColumn(t, tab, th, 40, 10)

	if runes[0] != gitMarkGlyph || runes[5] != gitMarkGlyph {
		t.Fatalf("marks should win over track and thumb, got %q / %q", runes[0], runes[5])
	}
	if fgs[0] != th.GitAdded {
		t.Fatalf("row 0 mark colour: got %v, want GitAdded", fgs[0])
	}
	if bgs[0] != th.Muted {
		t.Fatalf("a mark on the thumb must sit on the thumb colour, got %v", bgs[0])
	}
	if fgs[5] != th.GitModified {
		t.Fatalf("row 5 mark colour: got %v, want GitModified", fgs[5])
	}
	if bgs[5] != th.BG {
		t.Fatalf("a mark on the track sits on the editor bg, got %v", bgs[5])
	}
}

// TestScrollbarHiddenLeavesColumnToText: with a short buffer the last
// column stays ordinary editor background (no track glyphs).
func TestScrollbarHiddenLeavesColumnToText(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("just\na few\nlines"), StyleStale: true}
	tab.initUndo()
	runes, _, _ := barColumn(t, tab, theme.Default(), 40, 10)
	if runes[0] != ' ' {
		t.Fatalf("no bar expected for a short file, got %q", runes[0])
	}
}
