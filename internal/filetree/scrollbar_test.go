// =============================================================================
// File: internal/filetree/scrollbar_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// minSidebarWidthTreeRect is the tree render rect at the app's narrowest
// sidebar: internal/app clamps the sidebar block to minSidebarWidth (18)
// and sidebarRect hands the tree everything but the splitter's column.
// Pinned here so a change to either number surfaces as a scrollbar test
// failure rather than as a silently squeezed label.
const minSidebarWidthTreeRect = 17

// barRunes returns the rune drawn in each row of the render rect's
// rightmost column — the scrollbar's column when one is drawn.
func barRunes(cells []tcell.SimCell, cw, w, h int) []rune {
	out := make([]rune, h)
	for row := 0; row < h; row++ {
		c := cells[row*cw+w-1]
		if len(c.Runes) == 0 {
			out[row] = ' '
			continue
		}
		out[row] = c.Runes[0]
	}
	return out
}

// TestTreeScrollbar_HiddenWhenListingFits: a tree shorter than its list
// area draws no bar at all. A full-height thumb carries no information,
// and the column is worth more to the file names.
func TestTreeScrollbar_HiddenWhenListingFits(t *testing.T) {
	tr := mkFlatTree(t, 4)
	const w, h = 24, 20
	cells, cw := renderAndCollect(t, tr, w, h)

	for row, r := range barRunes(cells, cw, w, h) {
		if r == scrollbar.Track || r == scrollbar.Thumb {
			t.Fatalf("row %d drew bar glyph %q for a listing that fits", row, r)
		}
	}
	if tr.ScrollbarVisible(w, h) {
		t.Fatal("ScrollbarVisible should agree with what was painted")
	}
}

// TestTreeScrollbar_ThumbTracksScroll: an overflowing listing draws a
// shaded track down the list area with a solid thumb at the scaled
// scroll position — and the header rows above the list stay clear,
// because they are pinned and a bar over them would lie.
func TestTreeScrollbar_ThumbTracksScroll(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	// List area = h - 2 = 10 rows for 60 entries: thumb is 1 row.
	tr.ScrollY = 25
	cells, cw := renderAndCollect(t, tr, w, h)
	col := barRunes(cells, cw, w, h)

	for row := 0; row < listHeaderRows; row++ {
		if col[row] == scrollbar.Track || col[row] == scrollbar.Thumb {
			t.Fatalf("pinned header row %d must not carry the bar, got %q", row, col[row])
		}
	}
	wantStart, wantLen, ok := scrollbar.Geom(60, h-listHeaderRows, 25)
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row := 0; row < h-listHeaderRows; row++ {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got := col[listHeaderRows+row]; got != want {
			t.Fatalf("list row %d: got %q, want %q", row, got, want)
		}
	}
	// Scrolling home walks the thumb back to the top of the track.
	tr.ScrollY = 0
	cells, cw = renderAndCollect(t, tr, w, h)
	col = barRunes(cells, cw, w, h)
	if col[listHeaderRows] != scrollbar.Thumb {
		t.Fatalf("at scroll 0 the thumb should sit on the first list row, got %q", col[listHeaderRows])
	}
}

// TestTreeScrollbar_ReservesTheLabelColumn: the bar takes its column out
// of the row width, so a long name is truncated one cell earlier rather
// than painted underneath the bar.
func TestTreeScrollbar_ReservesTheLabelColumn(t *testing.T) {
	const long = "a-really-long-file-name-that-overflows.go"
	const w, h = 24, 12

	// Same long name, two listings: one that overflows the list area
	// (bar) and one that fits (no bar).
	build := func(fillers int) *Tree {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, long), "x")
		for i := 0; i < fillers; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("z%03d.go", i)), "x")
		}
		tr, err := New(root)
		if err != nil {
			t.Fatalf("tree: %v", err)
		}
		return tr
	}

	cells, cw := renderAndCollect(t, build(60), w, h)
	row := []rune(rowText(cells, cw, listHeaderRows))
	if row[w-1] != scrollbar.Track && row[w-1] != scrollbar.Thumb {
		t.Fatalf("bar column should hold the bar, got %q (row %q)", row[w-1], string(row))
	}
	if row[w-2] == ' ' {
		t.Fatalf("the truncated name should run right up to the bar, row %q", string(row))
	}

	cells, cw = renderAndCollect(t, build(1), w, h)
	row = []rune(rowText(cells, cw, listHeaderRows))
	if row[w-1] == scrollbar.Track || row[w-1] == scrollbar.Thumb {
		t.Fatalf("no bar expected once the listing fits, row %q", string(row))
	}
	if row[w-1] == ' ' {
		t.Fatalf("without the bar the name should reach the last column, row %q", string(row))
	}
}

// TestTreeScrollbar_HitTestSeparatesColumns: only the rect's last column
// and only the scrollable rows belong to the bar. The pinned header and
// root rows, and every label column, stay tree.
func TestTreeScrollbar_HitTestSeparatesColumns(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	renderAndCollect(t, tr, w, h)

	if !tr.ScrollbarHit(w-1, listHeaderRows, w, h) {
		t.Fatal("first list row of the last column is the bar")
	}
	if !tr.ScrollbarHit(w-1, h-1, w, h) {
		t.Fatal("last list row of the last column is the bar")
	}
	if tr.ScrollbarHit(w-2, listHeaderRows, w, h) {
		t.Fatal("the column left of the bar belongs to the tree row")
	}
	for row := 0; row < listHeaderRows; row++ {
		if tr.ScrollbarHit(w-1, row, w, h) {
			t.Fatalf("pinned row %d is not part of the bar", row)
		}
	}
	if tr.ScrollbarHit(w-1, h, w, h) {
		t.Fatal("below the list area is not the bar")
	}
}

// TestTreeScrollbar_ClickToJump: clicking a bar row scrolls the listing
// there, top and bottom included, and a tree with no bar ignores it.
func TestTreeScrollbar_ClickToJump(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	renderAndCollect(t, tr, w, h)

	tr.ScrollToBarRow(w, h, h-1) // bottom of the bar
	if want := 60 - (h - listHeaderRows); tr.ScrollY != want {
		t.Fatalf("bottom click: ScrollY %d, want %d", tr.ScrollY, want)
	}
	tr.ScrollToBarRow(w, h, listHeaderRows) // top of the bar
	if tr.ScrollY != 0 {
		t.Fatalf("top click: ScrollY %d, want 0", tr.ScrollY)
	}
	mid := h/2 + listHeaderRows/2
	tr.ScrollToBarRow(w, h, mid)
	if tr.ScrollY <= 0 || tr.ScrollY >= 60-(h-listHeaderRows) {
		t.Fatalf("middle click should land inside the range, got %d", tr.ScrollY)
	}

	short := mkFlatTree(t, 3)
	renderAndCollect(t, short, w, h)
	short.ScrollToBarRow(w, h, h-1)
	if short.ScrollY != 0 {
		t.Fatalf("a tree with no bar must not scroll, got %d", short.ScrollY)
	}
}

// TestTreeScrollbar_WidthFloor: the bar is present at the narrowest
// sidebar the app allows (minSidebarWidth 18 → a 17-column tree rect)
// but gives the column back on a rect too narrow to spare it.
func TestTreeScrollbar_WidthFloor(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const h = 12

	// 17 columns is what minSidebarWidth (18) leaves the tree once the
	// splitter takes its column — the narrowest rect a user can drag to.
	// Render first: the bar's proportions are measured against the row
	// count the last paint flattened, exactly like HitTest's row map.
	const minRect = minSidebarWidthTreeRect
	minCells, minCW := renderAndCollect(t, tr, minRect, h)
	if !tr.ScrollbarVisible(minRect, h) {
		t.Fatal("the bar must survive the app's minimum sidebar width")
	}
	if r := barRunes(minCells, minCW, minRect, h)[listHeaderRows]; r != scrollbar.Track && r != scrollbar.Thumb {
		t.Fatalf("bar should paint at a %d-column rect, got %q", minRect, r)
	}

	narrow := minScrollbarWidth - 1
	if tr.ScrollbarVisible(narrow, h) {
		t.Fatalf("a %d-column rect should spend its cells on names", narrow)
	}
	if tr.ScrollbarHit(narrow-1, listHeaderRows, narrow, h) {
		t.Fatal("no bar means no bar clicks")
	}
	cells, cw := renderAndCollect(t, tr, narrow, h)
	for row, r := range barRunes(cells, cw, narrow, h) {
		if r == scrollbar.Track || r == scrollbar.Thumb {
			t.Fatalf("row %d painted a bar on a %d-column rect: %q", row, narrow, r)
		}
	}
}

// TestTreeScrollbar_ThumbBrightensWhileDragging: the tree's thumb speaks
// the same idle-Muted / dragging-Accent language as the editor's bar and
// the sidebar splitter. The track never brightens.
func TestTreeScrollbar_ThumbBrightensWhileDragging(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	th := theme.Default()

	cells, cw := renderAndCollect(t, tr, w, h)
	fg, _, _ := cells[listHeaderRows*cw+w-1].Style.Decompose()
	if fg != th.Muted {
		t.Fatalf("idle thumb fg: got %v, want Muted", fg)
	}

	tr.ScrollbarActive = true
	cells, cw = renderAndCollect(t, tr, w, h)
	fg, bg, _ := cells[listHeaderRows*cw+w-1].Style.Decompose()
	if fg != th.Accent {
		t.Fatalf("dragged thumb fg: got %v, want Accent", fg)
	}
	if bg != th.SidebarBG {
		t.Fatalf("the bar sits on the sidebar background, got %v", bg)
	}
	trackFg, _, _ := cells[(h-1)*cw+w-1].Style.Decompose()
	if trackFg != th.Subtle {
		t.Fatalf("track fg while dragging: got %v, want Subtle", trackFg)
	}
}
