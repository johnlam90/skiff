// =============================================================================
// File: internal/overlay/pick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"

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
	if p.Selected != 1 {
		t.Fatalf("Init should land on the Current item, got %d", p.Selected)
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
