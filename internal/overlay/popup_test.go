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

// TestPopup_DividersAreInert pins the group-divider contract: divider
// rows render between groups but are skipped by keyboard navigation,
// ignore hover, and can never activate — they structure the list, they
// are not part of it.
func TestPopup_DividersAreInert(t *testing.T) {
	var log []string
	p := &Popup{Theme: theme.Default(), At: PlacePopup(80, 24, 10, 5, 19, 3)}
	p.Close = func() { log = append(log, "close") }
	p.Items = []PopupItem{
		{Label: "one", OnPick: func() { log = append(log, "one") }},
		{Divider: true},
		{Label: "two", OnPick: func() { log = append(log, "two") }},
	}

	p.HandleKey(key(tcell.KeyDown, 0))
	if p.Hover != 2 {
		t.Fatalf("Down must skip the divider, hover=%d", p.Hover)
	}
	p.HandleKey(key(tcell.KeyUp, 0))
	if p.Hover != 0 {
		t.Fatalf("Up must skip the divider, hover=%d", p.Hover)
	}

	// Hovering the divider's row must not move the highlight onto it,
	// and clicking it must not activate anything.
	r := p.At
	p.HandleMouse(r.X+3, r.Y+2, tcell.ButtonNone) // divider row
	if p.Hover == 1 {
		t.Fatal("hover must not land on a divider")
	}
	p.HandleMouse(r.X+3, r.Y+2, tcell.Button1)
	if len(log) != 0 {
		t.Fatalf("clicking a divider must be a no-op, got %v", log)
	}

	// The divider row draws as a rule, not as a blank or a label.
	scr := simScreen(t)
	p.Draw(scr)
	scr.Show()
	if got := cellAt(scr, r.X+3, r.Y+2); got != '─' {
		t.Fatalf("divider row should draw a rule, got %q", got)
	}
}

// TestPopupWidth_FitsLongestLabel pins the auto-width contract: the
// popup is sized to its content (chevron indent + label + padding),
// never narrower than min — a fixed width chose for short labels let
// long ones paint past the border into the editor.
func TestPopupWidth_FitsLongestLabel(t *testing.T) {
	items := []PopupItem{
		{Label: "Fetch"},
		{Divider: true},
		{Label: "Compare against…"},
	}
	got := PopupWidth(items, 19)
	if want := 4 + 16 + 2; got != want {
		t.Fatalf("width = %d, want %d", got, want)
	}
	if got := PopupWidth([]PopupItem{{Label: "x"}}, 19); got != 19 {
		t.Fatalf("short labels keep the minimum, got %d", got)
	}
}

// TestPopup_DrawClipsLabelsToBorder pins the belt: even when a label is
// wider than the popup (a clamped width on a tiny screen), it clips at
// the border instead of leaking over whatever sits behind.
func TestPopup_DrawClipsLabelsToBorder(t *testing.T) {
	scr := simScreen(t)
	p := &Popup{
		Theme: theme.Default(),
		At:    Rect{X: 5, Y: 3, W: 12, H: 3},
		Items: []PopupItem{{Label: "much-too-long-label"}},
	}
	p.Close = func() {}
	p.Draw(scr)
	scr.Show()
	if got := cellAt(scr, 5+12-1, 4); got != '│' {
		t.Fatalf("right border must survive a long label, got %q", got)
	}
	if got := cellAt(scr, 5+12, 4); got == 'l' || got == 'a' || got == 'b' {
		t.Fatalf("label leaked past the border: %q", got)
	}
}
