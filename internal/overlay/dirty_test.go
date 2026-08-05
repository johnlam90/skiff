// =============================================================================
// File: internal/overlay/dirty_test.go
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

// testDirty builds a Dirty on an 80×24 screen with an ordered call log.
func testDirty() (*Dirty, *[]string) {
	var log []string
	d := &Dirty{
		Title:   "T",
		Message: "unsaved",
		Theme:   theme.Default(),
		Size:    func() (int, int) { return 80, 24 },
	}
	d.Close = func() { log = append(log, "close") }
	d.OnSave = func() { log = append(log, "save") }
	d.OnDiscard = func() { log = append(log, "discard") }
	return d, &log
}

// TestDirty_DefaultFocusIsCancel pins the safety default: zero Hover is
// Cancel, so a stray Enter dismisses without losing or writing anything.
func TestDirty_DefaultFocusIsCancel(t *testing.T) {
	d, log := testDirty()
	d.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("Enter on default focus must just close, got %v", *log)
	}
}

// TestDirty_FocusCyclingAndActivation pins the three-button keyboard
// contract: Left/Right clamp at the ends, Tab wraps, and Enter fires
// the focused button with close-before-callback ordering.
func TestDirty_FocusCyclingAndActivation(t *testing.T) {
	d, log := testDirty()
	d.HandleKey(key(tcell.KeyLeft, 0))
	if d.Hover != 0 {
		t.Fatal("Left must clamp at Cancel")
	}
	d.HandleKey(key(tcell.KeyRight, 0))
	d.HandleKey(key(tcell.KeyRight, 0))
	d.HandleKey(key(tcell.KeyRight, 0))
	if d.Hover != 2 {
		t.Fatal("Right must clamp at Save")
	}
	d.HandleKey(key(tcell.KeyTab, 0))
	if d.Hover != 0 {
		t.Fatal("Tab must wrap from Save to Cancel")
	}
	d.Hover = 2
	d.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "save" {
		t.Fatalf("want [close save], got %v", *log)
	}

	d, log = testDirty()
	d.Hover = 1
	d.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[1] != "discard" {
		t.Fatalf("want discard, got %v", *log)
	}
}

// TestDirtyButtonAtRelX pins the shared hover/click geometry: hits on
// each button and misses in the gaps and past the edges.
func TestDirtyButtonAtRelX(t *testing.T) {
	cases := []struct {
		rx   int
		want int
	}{
		{dirtyBtnCancelX, 0},
		{dirtyBtnCancelX + dirtyBtnCancelW - 1, 0},
		{dirtyBtnCancelX + dirtyBtnCancelW, -1},
		{dirtyBtnDiscardX + 1, 1},
		{dirtyBtnSaveX + 1, 2},
		{0, -1},
		{dirtyBtnSaveX + dirtyBtnSaveW + 5, -1},
	}
	for _, c := range cases {
		if got := dirtyButtonAtRelX(c.rx); got != c.want {
			t.Errorf("rx=%d: got %d, want %d", c.rx, got, c.want)
		}
	}
}

// TestDirty_MouseActivatesAndOutsideCancels pins the mouse contract on
// known geometry: button clicks fire their callbacks, an outside click
// closes without firing anything, and motion moves the hover.
func TestDirty_MouseActivatesAndOutsideCancels(t *testing.T) {
	r := Centered(80, 24, dirtyWidth, dirtyHeight)

	d, log := testDirty()
	d.HandleMouse(r.X+dirtyBtnSaveX+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "save" {
		t.Fatalf("Save click: got %v", *log)
	}

	d, log = testDirty()
	d.HandleMouse(r.X-1, r.Y, tcell.Button1)
	if len(*log) != 1 || (*log)[0] != "close" {
		t.Fatalf("outside click must only close, got %v", *log)
	}

	d, _ = testDirty()
	d.HandleMouse(r.X+dirtyBtnDiscardX+1, r.Y+5, tcell.ButtonNone)
	if d.Hover != 1 {
		t.Fatal("motion over Discard must move the hover highlight")
	}
}

// TestDirty_DefaultLabelsKeepPinnedGeometry is the backwards-compatibility
// pin for the optional Labels field: leaving it zero must reproduce the
// hand-tuned Cancel / Discard / Save columns exactly, so the stock
// unsaved-changes modal renders where it always did.
//
// The numbers are written out rather than read back from the dirtyBtn*
// constants: columns() returns those same constants on the default path,
// so an expectation sourced from them would follow any retune and pin
// nothing at all. Change the constants and this test is supposed to fail.
func TestDirty_DefaultLabelsKeepPinnedGeometry(t *testing.T) {
	d, _ := testDirty()
	xs, ws := d.columns()
	wantX := [3]int{5, 22, 42}
	wantW := [3]int{10, 11, 8} // "[ Cancel ]", "[ Discard ]", "[ Save ]"
	if xs != wantX || ws != wantW {
		t.Fatalf("default columns = %v/%v, want %v/%v", xs, ws, wantX, wantW)
	}
	wantLabels := [3]string{"[ Cancel ]", "[ Discard ]", "[ Save ]"}
	if got := d.labels(); got != wantLabels {
		t.Fatalf("default labels = %v, want %v", got, wantLabels)
	}
	// The captions have to actually fit the columns they were tuned for.
	for i, l := range wantLabels {
		if len([]rune(l)) != wantW[i] {
			t.Errorf("label %q is %d cells wide, but its column is %d", l, len([]rune(l)), wantW[i])
		}
	}
}

// TestDirty_CustomLabelsLayOutAndHitTest covers the relabelled trio the
// disk-conflict prompt uses: the buttons stay inside the modal, don't
// overlap, and the hit test agrees with where they were drawn.
func TestDirty_CustomLabelsLayOutAndHitTest(t *testing.T) {
	d, log := testDirty()
	d.Labels = [3]string{"[ Keep mine ]", "[ Reload ]", "[ Diff ]"}

	xs, ws := d.columns()
	if xs[0] < 0 || xs[2]+ws[2] > dirtyWidth {
		t.Fatalf("buttons escape the modal: %v/%v", xs, ws)
	}
	for i := range 2 {
		if xs[i]+ws[i] >= xs[i+1] {
			t.Fatalf("buttons %d and %d overlap: %v/%v", i, i+1, xs, ws)
		}
	}
	for i := range xs {
		if got := d.buttonAtRelX(xs[i] + 1); got != i {
			t.Fatalf("hit test at button %d returned %d", i, got)
		}
	}

	r := Centered(80, 24, dirtyWidth, dirtyHeight)
	d.HandleMouse(r.X+xs[1]+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "discard" {
		t.Fatalf("middle button should fire OnDiscard, got %v", *log)
	}
}

// TestDirty_OnCancelFiresOnEveryDismissal pins the hook the disk-conflict
// prompt hangs its "keeping your edits" note on: the left button, Esc,
// and an outside click are all the same decision and must all report it.
func TestDirty_OnCancelFiresOnEveryDismissal(t *testing.T) {
	r := Centered(80, 24, dirtyWidth, dirtyHeight)
	dismiss := map[string]func(*Dirty){
		"button":  func(d *Dirty) { d.HandleKey(key(tcell.KeyEnter, 0)) },
		"esc":     func(d *Dirty) { d.HandleKey(key(tcell.KeyEsc, 0)) },
		"outside": func(d *Dirty) { d.HandleMouse(r.X-1, r.Y, tcell.Button1) },
	}
	for name, act := range dismiss {
		d, log := testDirty()
		d.OnCancel = func() { *log = append(*log, "cancel") }
		act(d)
		if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "cancel" {
			t.Errorf("%s dismissal log = %v, want close then cancel", name, *log)
		}
	}
}

// TestDirty_DrawRendersCustomLabels asserts the relabelled buttons
// actually reach the screen at the columns the hit test uses.
func TestDirty_DrawRendersCustomLabels(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(80, 24)

	d, _ := testDirty()
	d.Labels = [3]string{"[ Keep mine ]", "[ Reload ]", "[ Diff ]"}
	d.Draw(scr)
	scr.Show()

	r := d.rect()
	xs, ws := d.columns()
	cells, w, _ := scr.GetContents()
	for i, want := range d.Labels {
		got := make([]rune, 0, ws[i])
		for c := range ws[i] {
			got = append(got, cells[(r.Y+5)*w+r.X+xs[i]+c].Runes[0])
		}
		if string(got) != want {
			t.Errorf("button %d drew %q, want %q", i, string(got), want)
		}
	}
}

// TestDirty_PhoneWidthKeepsAllThreeButtonsOnScreen is the widest fixed
// button row in the editor and therefore the surface that sets skiff's
// minWidth: Cancel / Discard / Save is 29 cells of label in a 60-cell
// frame, with Save pinned at column 42. On a 40-column phone the
// unclamped frame put Save entirely off the right edge, so the only
// button that saves your work was unreachable by mouse.
func TestDirty_PhoneWidthKeepsAllThreeButtonsOnScreen(t *testing.T) {
	const scrW, scrH = 40, 10
	d, log := testDirty()
	d.Size = func() (int, int) { return scrW, scrH }

	r := d.rect()
	if r.X < 0 || r.X+r.W > scrW || r.Y+r.H > scrH {
		t.Fatalf("frame off screen: %+v on %dx%d", r, scrW, scrH)
	}
	xs, ws := d.columns()
	for i := range xs {
		if xs[i] < 1 || xs[i]+ws[i] > r.W-1 {
			t.Fatalf("button %d spans %d..%d, outside the %d-cell frame", i, xs[i], xs[i]+ws[i], r.W)
		}
		if i > 0 && xs[i] < xs[i-1]+ws[i-1]+1 {
			t.Fatalf("buttons %d and %d touch: %v", i-1, i, xs)
		}
		if got := d.buttonAtRelX(xs[i] + 1); got != i {
			t.Fatalf("hit test at squeezed button %d returned %d", i, got)
		}
	}

	d.HandleMouse(r.X+xs[2]+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "save" {
		t.Fatalf("Save click at the squeezed column: got %v", *log)
	}
}

// TestDirty_WideScreenKeepsPinnedColumns is the compatibility half: the
// squeeze must engage only when the terminal forces it, so on any screen
// that can hold 60 cells the hand-tuned columns are byte-identical to
// what they always were.
func TestDirty_WideScreenKeepsPinnedColumns(t *testing.T) {
	d, _ := testDirty()
	xs, ws := d.columns()
	wantX := [3]int{dirtyBtnCancelX, dirtyBtnDiscardX, dirtyBtnSaveX}
	wantW := [3]int{dirtyBtnCancelW, dirtyBtnDiscardW, dirtyBtnSaveW}
	if xs != wantX || ws != wantW {
		t.Fatalf("pinned columns drifted: xs=%v ws=%v", xs, ws)
	}
}
