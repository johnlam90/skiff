// =============================================================================
// File: internal/overlay/scrollbar_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// barCol reads the indicator column out of a drawn screen: n rows of
// the bar starting at its top, as a string.
func barCol(scr tcell.SimulationScreen, b Bar) string {
	out := make([]rune, 0, b.viewH)
	for i := range b.viewH {
		out = append(out, cellAt(scr, b.x, b.top+i))
	}
	return string(out)
}

// TestBarColumn_IsTheFramePaddingCell pins the placement claim the
// whole design rests on: the indicator lands on the padding cell
// between the last text column and the right border. That cell is
// already blank in every prefab, which is why the bar can appear on
// surfaces whose frame size is pinned by other tests without moving a
// single other cell.
func TestBarColumn_IsTheFramePaddingCell(t *testing.T) {
	r := Rect{X: 8, Y: 4, W: 84, H: 22}
	// Prefabs draw content at r.X+2 clipped to r.W-4 cells.
	lastText := r.X + 2 + (r.W - 4) - 1
	border := r.X + r.W - 1
	if got := BarColumn(r); got != lastText+1 || got != border-1 {
		t.Fatalf("bar column %d should sit between text (%d) and border (%d)", got, lastText, border)
	}
}

// TestBodyBar_DrawnOnlyOnOverflow pins the no-noise rule shared with
// the editor and the file tree: a window that already shows everything
// gets no bar, because a full-height thumb tells the reader nothing.
func TestBodyBar_DrawnOnlyOnOverflow(t *testing.T) {
	fits := Bar{x: 5, top: 2, viewH: 10, total: 10}
	if fits.Drawn() {
		t.Fatal("a body that fits must not draw a bar")
	}
	over := Bar{x: 5, top: 2, viewH: 10, total: 11}
	if !over.Drawn() {
		t.Fatal("one row past the window is already overflow")
	}
}

// TestBodyBar_HitNeedsADrawnBar is the click-steal guard in miniature:
// the column only belongs to the bar while the bar is on screen. Pick's
// rows run out to the padding cell, so a hit that ignored visibility
// would swallow a legitimate row click on every short list.
func TestBodyBar_HitNeedsADrawnBar(t *testing.T) {
	over := Bar{x: 5, top: 2, viewH: 4, total: 40}
	if !over.Hit(5, 2) || !over.Hit(5, 5) {
		t.Fatal("both ends of the span must be hits")
	}
	if over.Hit(4, 3) || over.Hit(5, 1) || over.Hit(5, 6) {
		t.Fatal("the bar owns exactly one column and exactly its own rows")
	}
	fits := Bar{x: 5, top: 2, viewH: 4, total: 4}
	if fits.Hit(5, 3) {
		t.Fatal("with no bar drawn the column belongs to the surface")
	}
}

// TestBodyBar_TargetSpansTheWholeRange pins the click-to-jump inverse:
// the top of the track means the first row and the bottom means the
// last window, so a press anywhere on the bar can reach anywhere in the
// content.
func TestBodyBar_TargetSpansTheWholeRange(t *testing.T) {
	b := Bar{x: 5, top: 2, viewH: 10, total: 100}
	if got := b.Target(2); got != 0 {
		t.Fatalf("press on the first bar row: got %d, want 0", got)
	}
	if got, want := b.Target(2+9), 100-10; got != want {
		t.Fatalf("press on the last bar row: got %d, want %d", got, want)
	}
}

// TestBodyBar_DrawUsesTheSharedGlyphsAndRoles pins the visual
// vocabulary: the shared Track/Thumb glyphs, the thumb in the brighter
// secondary color and the track in the dimmer one, both on the modal
// body background — identical to what the editor's bar paints, so a
// scrollbar looks the same everywhere in skiff.
func TestBodyBar_DrawUsesTheSharedGlyphsAndRoles(t *testing.T) {
	scr := simScreen(t)
	th := theme.Default()
	b := Bar{x: 10, top: 3, viewH: 8, total: 32, scroll: 0}
	b.Draw(scr, th)
	scr.Show()

	wantStart, wantLen, ok := scrollbar.Geom(b.total, b.viewH, b.scroll)
	if !ok {
		t.Fatal("fixture should overflow")
	}
	cells, w, _ := scr.GetContents()
	for row := range b.viewH {
		want, wantFg := scrollbar.Track, th.Subtle
		if row >= wantStart && row < wantStart+wantLen {
			want, wantFg = scrollbar.Thumb, th.Muted
		}
		cell := cells[(b.top+row)*w+b.x]
		if cell.Runes[0] != want {
			t.Fatalf("row %d: got %q, want %q", row, cell.Runes[0], want)
		}
		fg, bg, _ := cell.Style.Decompose()
		if fg != wantFg {
			t.Fatalf("row %d fg = %v, want %v", row, fg, wantFg)
		}
		if bg != th.LineHL {
			t.Fatalf("row %d bg = %v, want the modal body %v", row, bg, th.LineHL)
		}
	}
}
