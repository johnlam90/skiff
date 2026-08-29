// =============================================================================
// File: internal/overlay/pick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
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

// testPick builds a 3-item pick (second item Current) on an 80×24
// screen with an ordered call log.
func testPick() (*Pick, *[]string) {
	var log []string
	p := &Pick{
		Title: "T",
		Theme: theme.Default(),
		Size:  func() (int, int) { return 80, 24 },
		Items: []PickItem{
			{Label: "alpha"},
			{Label: "beta", Current: true, Tag: "cur"},
			{Label: "gamma"},
		},
	}
	p.Close = func() { log = append(log, "close") }
	p.OnPick = func(i int) { log = append(log, "pick:"+p.Items[i].Label) }
	p.OnMove = func(i int) { log = append(log, "move:"+p.Items[i].Label) }
	p.OnCancel = func() { log = append(log, "cancel") }
	p.Init()
	return p, &log
}

// TestPick_InitSnapsToCurrent pins the open contract: the highlight
// starts on the Current item so Enter with no input keeps the status
// quo.
func TestPick_InitSnapsToCurrent(t *testing.T) {
	p, _ := testPick()
	if p.Sel() != 1 {
		t.Fatalf("Init should land on the Current item, got %d", p.Sel())
	}
}

// TestPick_MoveFiresPreviewAndConfirmPicksOriginalIndex pins the two
// hook contracts: OnMove receives the Items index as the highlight
// travels, and OnPick receives the Items index (never the filtered
// position), with close-before-callback ordering.
func TestPick_MoveFiresPreviewAndConfirmPicksOriginalIndex(t *testing.T) {
	p, log := testPick()
	p.HandleKey(key(tcell.KeyDown, 0))
	if len(*log) != 1 || (*log)[0] != "move:gamma" {
		t.Fatalf("Down should preview gamma, got %v", *log)
	}
	p.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 3 || (*log)[1] != "close" || (*log)[2] != "pick:gamma" {
		t.Fatalf("want [... close pick:gamma], got %v", *log)
	}
}

// TestPick_FilterNarrowsAndRemapsIndices pins type-to-filter: the query
// is a case-insensitive substring on labels, narrowing snaps the
// highlight to the first match with a preview, and a pick through a
// filtered view still reports the original index.
func TestPick_FilterNarrowsAndRemapsIndices(t *testing.T) {
	p, log := testPick()
	for _, r := range "GAM" {
		p.HandleKey(key(tcell.KeyRune, r))
	}
	if got := p.Filtered(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("filter GAM should leave gamma, got %v", got)
	}
	if (*log)[len(*log)-1] != "move:gamma" {
		t.Fatalf("narrowing should preview the surviving row, got %v", *log)
	}
	p.HandleKey(key(tcell.KeyEnter, 0))
	if (*log)[len(*log)-1] != "pick:gamma" {
		t.Fatalf("pick through filter must report the original index, got %v", *log)
	}
}

// TestPick_EnterOnNoMatchesCancels pins the empty-filter dead end:
// Enter with nothing matching cancels (running the revert hook) rather
// than picking garbage.
func TestPick_EnterOnNoMatchesCancels(t *testing.T) {
	p, log := testPick()
	for _, r := range "zzz" {
		p.HandleKey(key(tcell.KeyRune, r))
	}
	p.HandleKey(key(tcell.KeyEnter, 0))
	n := len(*log)
	if n < 2 || (*log)[n-2] != "close" || (*log)[n-1] != "cancel" {
		t.Fatalf("Enter on no matches must cancel, got %v", *log)
	}
}

// TestPick_EscAndOutsideClickCancel pins both dismissal paths and their
// hook: the revert OnCancel runs after close.
func TestPick_EscAndOutsideClickCancel(t *testing.T) {
	p, log := testPick()
	p.HandleKey(key(tcell.KeyEsc, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "cancel" {
		t.Fatalf("Esc: want [close cancel], got %v", *log)
	}

	p, log = testPick()
	p.HandleMouse(0, 0, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("outside click: want cancel, got %v", *log)
	}
}

// TestPick_MouseHoverPreviewsAndClickPicks pins the mouse contract on
// the computed geometry: hover moves the highlight with a preview,
// click confirms that row.
func TestPick_MouseHoverPreviewsAndClickPicks(t *testing.T) {
	p, log := testPick()
	r := p.rect()
	rowY := r.Y + 4 // first visible row: alpha
	p.HandleMouse(r.X+3, rowY, tcell.ButtonNone)
	if (*log)[len(*log)-1] != "move:alpha" {
		t.Fatalf("hover should preview alpha, got %v", *log)
	}
	p.HandleMouse(r.X+3, rowY, tcell.Button1)
	if (*log)[len(*log)-1] != "pick:alpha" {
		t.Fatalf("click should pick alpha, got %v", *log)
	}
}

// TestPick_DrawShowsRowsMarkerAndTag pins the painted list: the current
// row's ● marker, labels in order, and the right-aligned tag.
func TestPick_DrawShowsRowsMarkerAndTag(t *testing.T) {
	scr := simScreen(t)
	p, _ := testPick()
	p.Draw(scr)
	scr.Show()
	r := p.rect()
	if got := cellAt(scr, r.X+2, r.Y+5); got != '●' {
		t.Fatalf("current row marker missing, got %q", got)
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		got := ""
		for j := 0; j < len(want); j++ {
			got += string(cellAt(scr, r.X+4+j, r.Y+4+i))
		}
		if got != want {
			t.Fatalf("row %d = %q, want %q", i, got, want)
		}
	}
	tag := ""
	for j := 0; j < 3; j++ {
		tag += string(cellAt(scr, r.X+r.W-2-3+j, r.Y+5))
	}
	if tag != "cur" {
		t.Fatalf("tag = %q, want cur", tag)
	}
	// Empty filter shows the placeholder.
	ph := ""
	for j := 0; j < 4; j++ {
		ph += string(cellAt(scr, r.X+3+j, r.Y+3))
	}
	if ph != "type" {
		t.Fatalf("placeholder missing, got %q", ph)
	}
}

// -----------------------------------------------------------------------------
// List scroll indicator
// -----------------------------------------------------------------------------

// longPick builds an n-item pick on an 80×24 screen — long enough to
// overflow pickMaxVisible when n is large, and to fit when it is small.
func longPick(n int) (*Pick, *[]string) {
	var log []string
	p := &Pick{
		Title: "T",
		Theme: theme.Default(),
		Size:  func() (int, int) { return 80, 24 },
		Items: make([]PickItem, n),
	}
	for i := range n {
		p.Items[i] = PickItem{Label: "branch-" + string(rune('a'+i%26))}
	}
	p.Close = func() { log = append(log, "close") }
	p.OnPick = func(i int) { log = append(log, "pick:"+p.Items[i].Label) }
	p.Init()
	return p, &log
}

// pickBarColumn draws p and reads its indicator column back across the
// list rows.
func pickBarColumn(t *testing.T, scr tcell.SimulationScreen, p *Pick) string {
	t.Helper()
	p.Draw(scr)
	scr.Show()
	return barCol(scr, p.bar(p.rect()))
}

// TestPick_NoIndicatorWhenListFits pins the no-noise half. It matters
// more here than elsewhere: Pick sizes its frame to the item count, so
// most picks fit exactly and a bar would be permanent decoration.
func TestPick_NoIndicatorWhenListFits(t *testing.T) {
	scr := simScreen(t)
	p, _ := longPick(5)
	col := pickBarColumn(t, scr, p)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a 5-item list fits in its own frame, got %q", col)
	}
}

// TestPick_IndicatorTracksScroll pins the overflow case now that Pick
// is the menu's drill-in surface: past pickMaxVisible rows the list
// windows, and the bar reports both that it does and where the window
// sits.
func TestPick_IndicatorTracksScroll(t *testing.T) {
	scr := simScreen(t)
	p, _ := longPick(40)

	top := pickBarColumn(t, scr, p)
	if !strings.HasPrefix(top, string(scrollbar.Thumb)) || !strings.ContainsRune(top, scrollbar.Track) {
		t.Fatalf("40 items in a %d-row window: got %q", p.Visible(), top)
	}

	p.scroll = len(p.Items) - p.Visible()
	bottom := pickBarColumn(t, scr, p)
	if !strings.HasSuffix(bottom, string(scrollbar.Thumb)) {
		t.Fatalf("at the end the thumb must finish the track, got %q", bottom)
	}
	wantStart, wantLen, ok := scrollbar.Geom(len(p.Items), p.Visible(), p.scroll)
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row, got := range []rune(bottom) {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got != want {
			t.Fatalf("bar row %d: got %q, want %q (col %q)", row, got, want, bottom)
		}
	}
}

// TestPick_IndicatorShrinksWithTheFilter pins that the bar measures the
// FILTERED list, not len(Items): typing a query that narrows 40 items
// down to a handful must retire the bar, because the remaining rows all
// fit and the column goes back to being row surface.
func TestPick_IndicatorShrinksWithTheFilter(t *testing.T) {
	scr := simScreen(t)
	p, _ := longPick(40)
	if !strings.ContainsRune(pickBarColumn(t, scr, p), scrollbar.Thumb) {
		t.Fatal("unfiltered 40-item list should show a bar")
	}
	p.Filter.Value = []rune("branch-a")
	p.filterChanged()
	col := pickBarColumn(t, scr, p)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a narrowed list that fits must drop the bar, got %q", col)
	}
}

// TestPick_BarPressScrollsInsteadOfPicking is the click-steal
// regression: the bar's column is inside the row band, so without the
// hit-test claiming it first, reaching for the scrollbar would pick
// whatever row sits behind the thumb — checking out a branch instead of
// scrolling to the one you wanted.
func TestPick_BarPressScrollsInsteadOfPicking(t *testing.T) {
	scr := simScreen(t)
	p, log := longPick(40)
	pickBarColumn(t, scr, p)

	b := p.bar(p.rect())
	p.HandleMouse(b.x, b.top+b.viewH-1, tcell.Button1)
	if want := len(p.Items) - p.Visible(); p.scroll != want {
		t.Fatalf("press at the foot of the bar: scroll %d, want %d", p.scroll, want)
	}
	if len(*log) != 0 {
		t.Fatalf("a bar press must not pick or close, got %v", *log)
	}

	// Hovering the bar must not drag the highlight around either.
	before := p.Sel()
	p.HandleMouse(b.x, b.top, tcell.ButtonNone)
	if p.Sel() != before {
		t.Fatalf("hovering the bar moved the highlight: %d → %d", before, p.Sel())
	}

	// With the bar gone the same cell is ordinary row surface again.
	short, shortLog := longPick(5)
	sr := short.rect()
	short.HandleMouse(BarColumn(sr), sr.Y+4, tcell.Button1)
	if len(*shortLog) == 0 {
		t.Fatal("without a bar the padding column must still pick its row")
	}
}

// TestPick_ClampsToPhoneSizedScreen pins both floors against the
// terminal. The 26-cell minimum width is a preference — on a screen
// narrower than that the screen has to win, or the frame's right border
// and every row's tag column land in columns that do not exist. Same for
// the height: a list that cannot show one row answers nothing, but it
// still may not run past the last line of the terminal.
func TestPick_ClampsToPhoneSizedScreen(t *testing.T) {
	cases := [][2]int{{40, 10}, {26, 8}, {20, 7}}
	for _, sz := range cases {
		scrW, scrH := sz[0], sz[1]
		p, _ := testPick()
		p.Size = func() (int, int) { return scrW, scrH }
		r := p.rect()
		if r.X < 0 || r.X+r.W > scrW {
			t.Fatalf("%dx%d: frame spans %d..%d", scrW, scrH, r.X, r.X+r.W)
		}
		if r.Y < 0 || r.Y+r.H > scrH {
			t.Fatalf("%dx%d: frame spans rows %d..%d", scrW, scrH, r.Y, r.Y+r.H)
		}
		if p.Visible() < 1 {
			t.Fatalf("%dx%d: no list rows at all", scrW, scrH)
		}
	}
}
