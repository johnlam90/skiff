// =============================================================================
// File: internal/overlay/list_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// listOf returns a List of n rows showing visible of them, which is the
// only shape every test here needs to state.
func listOf(n, visible int) *List {
	var l List
	l.SetLen(n)
	l.SetVisible(visible)
	return &l
}

// TestList_SetLenAndSetVisibleRefuseNegatives pins the floor on both
// setters. A negative row count would make MaxScroll and RowAt answer
// nonsense, and both numbers come from layout arithmetic — a frame
// minus its chrome — which goes negative on a terminal small enough
// that draw() has already bailed.
func TestList_SetLenAndSetVisibleRefuseNegatives(t *testing.T) {
	l := listOf(-4, -3)
	if l.Len() != 0 || l.Visible() != 0 {
		t.Fatalf("negatives should floor at zero: len %d visible %d", l.Len(), l.Visible())
	}
	if got := l.MaxScroll(); got != 0 {
		t.Fatalf("an empty list can't scroll, got MaxScroll %d", got)
	}
}

// TestList_ClampPullsBothNumbersHome is the "the content changed under
// me" case every surface hits: a filter narrows, a git refresh drops
// rows, and the selection and the window are suddenly past the end. One
// call has to fix both, because a surface that clamped only the scroll
// went on painting a highlight for a row that no longer exists.
func TestList_ClampPullsBothNumbersHome(t *testing.T) {
	l := listOf(40, 10)
	l.Select(35)
	l.EnsureVisible()

	l.SetLen(8) // the filter just narrowed to eight matches
	l.Clamp()
	if l.Sel() != 7 {
		t.Fatalf("selection should clamp to the last row, got %d", l.Sel())
	}
	if l.Scroll() != 0 {
		t.Fatalf("eight rows fit in ten, so the window is home: got %d", l.Scroll())
	}

	l.SetLen(0) // ...and then to nothing
	l.Clamp()
	if l.Sel() != 0 || l.Scroll() != 0 {
		t.Fatalf("an empty list selects row zero at the top: sel %d scroll %d", l.Sel(), l.Scroll())
	}
}

// TestList_MoveClampsWithoutWrapping pins the arrow-key contract shared
// by every surface: overshooting either end lands on that end. Wrapping
// is deliberately not on offer — a list you can fall off the end of
// loses your place.
func TestList_MoveClampsWithoutWrapping(t *testing.T) {
	l := listOf(5, 3)
	l.Move(100)
	if l.Sel() != 4 {
		t.Fatalf("overshooting the end should land on it, got %d", l.Sel())
	}
	l.Move(-100)
	if l.Sel() != 0 {
		t.Fatalf("overshooting the top should land on it, got %d", l.Sel())
	}
}

// TestList_EnsureVisibleFollowsTheSelectionAtBothEdges is the window
// follow: stepping past the bottom scrolls by exactly one row (the
// selection sits on the last visible line, not re-centred), stepping
// past the top does the mirror, and a selection already inside the
// window never moves it.
func TestList_EnsureVisibleFollowsTheSelectionAtBothEdges(t *testing.T) {
	l := listOf(40, 10)

	l.Select(9)
	l.EnsureVisible()
	if l.Scroll() != 0 {
		t.Fatalf("the last row of the window is already visible, got scroll %d", l.Scroll())
	}

	l.Select(10)
	l.EnsureVisible()
	if l.Scroll() != 1 {
		t.Fatalf("stepping one past the bottom should scroll one row, got %d", l.Scroll())
	}

	l.Select(39)
	l.EnsureVisible()
	if want := 30; l.Scroll() != want {
		t.Fatalf("the last row: scroll %d, want %d", l.Scroll(), want)
	}

	l.Select(29)
	l.EnsureVisible()
	if l.Scroll() != 29 {
		t.Fatalf("stepping above the window should scroll to the row, got %d", l.Scroll())
	}

	// And the window never runs past the end of the content.
	l.Select(0)
	l.EnsureVisible()
	if l.Scroll() != 0 {
		t.Fatalf("row zero should bring the window home, got %d", l.Scroll())
	}
}

// TestList_ScrollByIsTheWheelAndLeavesTheSelectionAlone pins the wheel:
// it clamps at both ends, and it deliberately does not drag the
// highlight with it. Scrolling is looking, not choosing — a wheel that
// moved the selection would pick a different row than the one the user
// then clicks.
func TestList_ScrollByIsTheWheelAndLeavesTheSelectionAlone(t *testing.T) {
	l := listOf(40, 10)
	l.Select(3)

	l.ScrollBy(1000)
	if want := 30; l.Scroll() != want {
		t.Fatalf("scroll past the end: got %d, want %d", l.Scroll(), want)
	}
	l.ScrollBy(-1000)
	if l.Scroll() != 0 {
		t.Fatalf("scroll past the top: got %d, want 0", l.Scroll())
	}
	if l.Sel() != 3 {
		t.Fatalf("the wheel moved the highlight to %d", l.Sel())
	}

	// A list that fits cannot scroll at all.
	short := listOf(4, 10)
	short.ScrollBy(5)
	if short.Scroll() != 0 {
		t.Fatalf("a list that fits must never scroll, got %d", short.Scroll())
	}
}

// TestList_RowAtMapsOnlyTheWindow is the hit-test in both directions:
// a screen row inside the window maps through the scroll offset, and
// everything else — the chrome above, the chrome below, and blank space
// past the end of a short list — answers "not a row" so the surface can
// treat it as its own.
func TestList_RowAtMapsOnlyTheWindow(t *testing.T) {
	const firstRowY = 12
	l := listOf(40, 10)
	l.ScrollBy(5)

	if idx, ok := l.RowAt(firstRowY, firstRowY); !ok || idx != 5 {
		t.Fatalf("the window's first row is content row 5: got %d, ok %v", idx, ok)
	}
	if idx, ok := l.RowAt(firstRowY, firstRowY+9); !ok || idx != 14 {
		t.Fatalf("the window's last row is content row 14: got %d, ok %v", idx, ok)
	}
	if _, ok := l.RowAt(firstRowY, firstRowY-1); ok {
		t.Fatal("the row above the window is chrome, not a row")
	}
	if _, ok := l.RowAt(firstRowY, firstRowY+10); ok {
		t.Fatal("the row below the window is chrome, not a row")
	}

	// A three-row list in a ten-row window: rows 3..9 are blank space.
	short := listOf(3, 10)
	if idx, ok := short.RowAt(firstRowY, firstRowY+2); !ok || idx != 2 {
		t.Fatalf("the last real row should hit: got %d, ok %v", idx, ok)
	}
	if _, ok := short.RowAt(firstRowY, firstRowY+3); ok {
		t.Fatal("blank space past the end of a short list is not a row")
	}
}

// barColumnOf paints l's bar at column x from screen row top and reads
// the column back — the only honest proof of what a bar draws.
func barColumnOf(t *testing.T, l *List, x, top int) string {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 40)
	scr.Clear()
	l.Bar(x, top).Draw(scr, theme.Default())
	scr.Show()

	cells, w, _ := scr.GetContents()
	var b strings.Builder
	for row := range l.Visible() {
		rs := cells[(top+row)*w+x].Runes
		if len(rs) == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(rs[0])
	}
	return b.String()
}

// TestList_BarThumbTracksTheContentLength pins the indicator at three
// lengths: a list that fits draws nothing at all (a full-height thumb
// carries no information), a list barely over shows a long thumb, and a
// long list a short one — each matching internal/scrollbar's geometry
// exactly, because the bar and the editor's and the tree's must all be
// the same arithmetic.
func TestList_BarThumbTracksTheContentLength(t *testing.T) {
	const (
		x      = 7
		top    = 3
		window = 10
	)
	for _, tc := range []struct {
		name  string
		total int
		drawn bool
	}{
		{"fits", 10, false},
		{"barely over", 12, true},
		{"far over", 200, true},
	} {
		l := listOf(tc.total, window)
		l.ScrollBy(3)
		bar := l.Bar(x, top)
		if got := bar.Drawn(); got != tc.drawn {
			t.Fatalf("%s: Drawn = %v, want %v", tc.name, got, tc.drawn)
		}
		col := barColumnOf(t, l, x, top)
		if !tc.drawn {
			if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
				t.Fatalf("%s: a list that fits must paint no bar, got %q", tc.name, col)
			}
			continue
		}
		wantStart, wantLen, ok := scrollbar.Geom(tc.total, window, l.Scroll())
		if !ok {
			t.Fatalf("%s: fixture should overflow", tc.name)
		}
		for row, got := range []rune(col) {
			want := scrollbar.Track
			if row >= wantStart && row < wantStart+wantLen {
				want = scrollbar.Thumb
			}
			if got != want {
				t.Fatalf("%s: bar row %d = %q, want %q (col %q)", tc.name, row, got, want, col)
			}
		}
	}
}

// TestList_BarHitAndScrollToBarAreInverses pins the grab: the bar's
// column is claimable only while a bar is drawn, and a press at the
// foot of it scrolls to the end — the same click-to-jump the editor and
// the tree answer, through the same shared inverse.
func TestList_BarHitAndScrollToBarAreInverses(t *testing.T) {
	const (
		x      = 7
		top    = 3
		window = 10
	)
	l := listOf(200, window)
	bar := l.Bar(x, top)
	if !bar.Hit(x, top) || !bar.Hit(x, top+window-1) {
		t.Fatal("the bar owns its whole column across the window")
	}
	if bar.Hit(x+1, top) || bar.Hit(x, top-1) || bar.Hit(x, top+window) {
		t.Fatal("the bar must claim nothing outside its own column and span")
	}

	l.ScrollToBar(top, top+window-1)
	if want := l.MaxScroll(); l.Scroll() != want {
		t.Fatalf("a press at the foot of the bar: scroll %d, want %d", l.Scroll(), want)
	}
	l.ScrollToBar(top, top)
	if l.Scroll() != 0 {
		t.Fatalf("a press at the head of the bar: scroll %d, want 0", l.Scroll())
	}

	// With no bar drawn the column is ordinary surface again, so the
	// surface behind it keeps whatever it normally does with the cell.
	fits := listOf(4, window)
	if fits.Bar(x, top).Hit(x, top) {
		t.Fatal("a list that fits must not claim its padding column")
	}
}
