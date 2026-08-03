// =============================================================================
// File: internal/overlay/popup_test.go
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

// testPopup builds a 3-item popup anchored at (10, 5) on an 80×24
// screen, logging picks and closes in order.
func testPopup() (*Popup, *[]string) {
	var log []string
	p := &Popup{
		Theme: theme.Default(),
		At:    PlacePopup(80, 24, 10, 5, 19, 3),
	}
	p.Close = func() { log = append(log, "close") }
	for _, name := range []string{"one", "two", "three"} {
		n := name
		p.Items = append(p.Items, PopupItem{Label: n, OnPick: func() { log = append(log, n) }})
	}
	return p, &log
}

// TestPlacePopup_FlipsAtEdges pins the anchor math: a popup that would
// fall off the right or bottom edge flips to the other side of the
// click point, and coordinates clamp at the origin.
func TestPlacePopup_FlipsAtEdges(t *testing.T) {
	r := PlacePopup(80, 24, 10, 5, 19, 3)
	if r != (Rect{X: 10, Y: 5, W: 19, H: 5}) {
		t.Fatalf("plain anchor wrong: %+v", r)
	}
	right := PlacePopup(80, 24, 78, 5, 19, 3)
	if right.X != 78-19+1 {
		t.Fatalf("right-edge flip wrong: %+v", right)
	}
	bottom := PlacePopup(80, 24, 10, 23, 19, 3)
	if bottom.Y != 23-5+1 {
		t.Fatalf("bottom-edge flip wrong: %+v", bottom)
	}
	corner := PlacePopup(20, 5, 0, 0, 19, 10)
	if corner.X != 0 || corner.Y != 0 {
		t.Fatalf("must clamp at origin: %+v", corner)
	}
}

// TestPopup_KeyNavigationAndActivate pins the keyboard contract:
// Down/Up move the highlight with clamping (no wrap — the popup sits at
// an anchor), Enter closes first and then runs the pick.
func TestPopup_KeyNavigationAndActivate(t *testing.T) {
	p, log := testPopup()
	p.HandleKey(key(tcell.KeyUp, 0))
	if p.Hover != 0 {
		t.Fatal("Up at the top must clamp")
	}
	p.HandleKey(key(tcell.KeyDown, 0))
	p.HandleKey(key(tcell.KeyDown, 0))
	p.HandleKey(key(tcell.KeyDown, 0))
	if p.Hover != 2 {
		t.Fatal("Down at the bottom must clamp")
	}
	p.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "three" {
		t.Fatalf("want [close three], got %v", *log)
	}
}

// TestPopup_MouseHoverClickAndOutside pins the mouse contract: motion
// highlights the row under the pointer, a click runs it, and a click
// outside dismisses without running anything.
func TestPopup_MouseHoverClickAndOutside(t *testing.T) {
	p, log := testPopup()
	r := p.At
	p.HandleMouse(r.X+3, r.Y+2, tcell.ButtonNone)
	if p.Hover != 1 {
		t.Fatalf("hover should track row under pointer, got %d", p.Hover)
	}
	p.HandleMouse(r.X+3, r.Y+2, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "two" {
		t.Fatalf("click should run the row, got %v", *log)
	}

	p, log = testPopup()
	p.HandleMouse(0, 0, tcell.Button1)
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("outside click must only close, got %v", *log)
	}
}

// TestPopup_EscCloses pins Esc: dismiss without a pick.
func TestPopup_EscCloses(t *testing.T) {
	p, log := testPopup()
	p.HandleKey(key(tcell.KeyEsc, 0))
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("want [close], got %v", *log)
	}
}

// TestPopup_DrawShowsRowsAndHighlight pins the painted rows: chevron
// and label per item at the anchored rectangle.
func TestPopup_DrawShowsRowsAndHighlight(t *testing.T) {
	scr := simScreen(t)
	p, _ := testPopup()
	p.Draw(scr)
	scr.Show()
	r := p.At
	for i, want := range []string{"one", "two", "three"} {
		got := ""
		for j := 0; j < len(want); j++ {
			got += string(cellAt(scr, r.X+4+j, r.Y+1+i))
		}
		if got != want {
			t.Fatalf("row %d = %q, want %q", i, got, want)
		}
	}
}
