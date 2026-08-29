// =============================================================================
// File: internal/overlay/chrome_test.go
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

// simScreen builds an initialized 80×24 simulation screen.
func simScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init sim screen: %v", err)
	}
	scr.SetSize(80, 24)
	return scr
}

// cellAt returns the primary rune at (x, y) after a Show.
func cellAt(scr tcell.SimulationScreen, x, y int) rune {
	cells, w, _ := scr.GetContents()
	c := cells[y*w+x]
	if len(c.Runes) == 0 {
		return ' '
	}
	return c.Runes[0]
}

// TestDrawFrame pins the shared chrome geometry: border corners, the
// title with its leading space, the right-aligned esc hint, and the
// divider (with end caps) under the title row — the exact layout every
// modal hand-rolled before.
func TestDrawFrame(t *testing.T) {
	scr := simScreen(t)
	r := Rect{X: 10, Y: 5, W: 20, H: 7}
	DrawFrame(scr, r, "Title", theme.Default())
	scr.Show()

	corners := []struct {
		x, y int
		want rune
	}{
		{10, 5, '┌'}, {29, 5, '┐'}, {10, 11, '└'}, {29, 11, '┘'},
		{10, 7, '├'}, {29, 7, '┤'}, // divider end caps at r.Y+2
	}
	for _, c := range corners {
		if got := cellAt(scr, c.x, c.y); got != c.want {
			t.Errorf("(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
	// " Title" starts one cell inside the left border on the title row.
	for i, want := range " Title" {
		if got := cellAt(scr, 11+i, 6); got != want {
			t.Errorf("title cell %d = %q, want %q", i, got, want)
		}
	}
	// "esc " hint ends one cell inside the right border.
	for i, want := range "esc " {
		if got := cellAt(scr, 25+i, 6); got != want {
			t.Errorf("hint cell %d = %q, want %q", i, got, want)
		}
	}
}

// TestDrawButton pins the label cells and that focus inverts the style —
// the visual cue that Enter will press this button.
func TestDrawButton(t *testing.T) {
	scr := simScreen(t)
	DrawButton(scr, 3, 2, "[ OK ]", tcell.ColorBlack, tcell.ColorRed, false)
	DrawButton(scr, 3, 4, "[ OK ]", tcell.ColorBlack, tcell.ColorRed, true)
	scr.Show()
	for i, want := range "[ OK ]" {
		if got := cellAt(scr, 3+i, 2); got != want {
			t.Errorf("label cell %d = %q, want %q", i, got, want)
		}
	}
	cells, w, _ := scr.GetContents()
	_, plainBG, _ := cells[2*w+3].Style.Decompose()
	_, focusBG, _ := cells[4*w+3].Style.Decompose()
	if plainBG == focusBG {
		t.Fatal("focused button must invert its background")
	}
}

// TestDrawFrame_LongTitleClipsInsideBorder pins the title clip: before
// textdraw the title was drawn with no bounds at all, so a long path
// painted straight through the ┐ border onto whatever sat behind the
// frame. The budget must stop one gap cell before the esc hint, and the
// hint and corner must survive untouched.
func TestDrawFrame_LongTitleClipsInsideBorder(t *testing.T) {
	scr := simScreen(t)
	r := Rect{X: 5, Y: 2, W: 30, H: 7}
	long := "/very/long/project/path/that/never/ends/at/all/really.go" // ~57 cells
	DrawFrame(scr, r, long, theme.Default())
	scr.Show()

	if got := cellAt(scr, r.X+r.W-1, r.Y); got != '┐' {
		t.Fatalf("top-right corner = %q, want ┐", got)
	}
	if got := cellAt(scr, r.X+r.W-1, r.Y+1); got != '│' {
		t.Fatalf("right border on title row = %q, want │", got)
	}
	// The hint keeps its right-aligned home: "esc " ends one cell inside
	// the right border.
	for i, want := range "esc " {
		if got := cellAt(scr, r.X+r.W-5+i, r.Y+1); got != want {
			t.Errorf("hint cell %d = %q, want %q", i, got, want)
		}
	}
	// The clipped title ends with an ellipsis inside its budget
	// (interior r.W-2, minus the 4-cell hint and one gap = r.W-7 cells
	// from r.X+1), and the gap cell before the hint stays blank.
	budgetEnd := r.X + 1 + (r.W - 7) - 1
	if got := cellAt(scr, budgetEnd, r.Y+1); got != '…' {
		t.Errorf("last title cell = %q, want …", got)
	}
	if got := cellAt(scr, budgetEnd+1, r.Y+1); got != ' ' {
		t.Errorf("gap cell before hint = %q, want blank", got)
	}
}
