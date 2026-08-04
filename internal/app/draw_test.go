// =============================================================================
// File: internal/app/draw_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for draw.go — the render pass, asserted against a
// tcell.SimulationScreen's contents. Covers the four-panel paint, the tab
// strip's scroll geometry and overflow chevrons, the status bar's
// right-aligned branch, and the clipping the empty-editor placeholder needs
// on a narrow pane.

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
)

// TestDetectLangLabel covers the language label helper's three cases.
func TestDetectLangLabel(t *testing.T) {
	cases := map[string]string{
		"":               "text",
		"foo.go":         "go",
		"foo":            "text",
		"path/to/x.py":   "py",
		"archive.tar.gz": "gz",
	}
	for in, want := range cases {
		if got := detectLangLabel(in); got != want {
			t.Errorf("detectLangLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDraw_AllPanels exercises the drawing path so the stdout/screen code
// is covered. Result correctness is exercised manually; here we just make
// sure no panics across several states.
func TestDraw_AllPanels(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.draw() // empty editor + sidebar
	a.openFile(target)
	a.draw() // with a tab
	a.activeTabPtr().Dirty = true
	a.draw() // dirty marker
	a.openMenu()
	a.draw() // with menu modal
	a.closeMenu()
	a.openPrompt("T", "H", "x", nil)
	a.draw()
	a.closeAllModals()
	a.openConfirm("T", "M", nil)
	a.draw()
	a.closeAllModals()
	a.openTreeContext(a.tree.Root, 5, 5)
	a.draw()
	a.closeAllModals()
	a.flash("hello")
	a.draw() // status flash
	a.sidebarShown = false
	a.draw()
	// Tiny window → too-small message.
	a.width, a.height = 5, 5
	a.draw()
}

// TestDrawStatusBar_RendersBranchRightAligned pins down the lower-right
// branch label: when gitBranch is set, the rightmost cells of the
// status bar carry " <branch> " in order, so the user can glance at
// the corner and read which checkout they're on.
func TestDrawStatusBar_RendersBranchRightAligned(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitSnap.Branch = "feat/widgets"
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show() // SimulationScreen serves GetContents from the *front* buffer.

	cells, w, _ := scr.GetContents()
	_, sy, _, _ := a.statusRect()

	want := []rune(" feat/widgets ")
	startX := w - len(want)
	for i, r := range want {
		c := cells[sy*w+startX+i]
		if len(c.Runes) == 0 || c.Runes[0] != r {
			t.Fatalf("status bar col %d = %v, want %q",
				startX+i, c.Runes, r)
		}
	}
}

// TestDrawStatusBar_OmitsBranchWhenEmpty confirms a non-repo project
// (gitBranch == "") doesn't paint a stray label or steal cells from
// the left-side text — the right edge should just be the bar's bg.
func TestDrawStatusBar_OmitsBranchWhenEmpty(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitSnap.Branch = ""
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	cells, w, _ := scr.GetContents()
	_, sy, _, _ := a.statusRect()

	// Tail of the status bar must be blank — the bar's fill character.
	for x := w - 5; x < w; x++ {
		c := cells[sy*w+x]
		if len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("status bar col %d = %v, expected blank tail", x, c.Runes)
		}
	}
}

// TestTrimRunes covers the menu-label clipping helper so long dynamic labels
// cannot overwrite the right-aligned shortcut column.
func TestTrimRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"fits", "Save", 8, "Save"},
		{"clips", "Find file in project", 10, "Find file…"},
		{"one", "Save", 1, "…"},
		{"none", "Save", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimRunes(tt.in, tt.max); got != tt.want {
				t.Fatalf("trimRunes(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

// TestLayoutTabs_IconsExpandWidth pins down the geometry contract:
// turning icons on grows each tab by exactly two cells (the glyph + a
// separator space), and the close-× column shifts right by the same
// amount. Without this, a tab-bar click on the × would land on the
// wrong column whenever icons are enabled.
func TestLayoutTabs_IconsExpandWidth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))

	a.tree.IconsEnabled = false
	off := a.layoutTabs()
	a.tree.IconsEnabled = true
	on := a.layoutTabs()

	if len(off) != 1 || len(on) != 1 {
		t.Fatalf("layoutTabs len off=%d on=%d, want 1 each", len(off), len(on))
	}
	if on[0].Width != off[0].Width+2 {
		t.Fatalf("icons should add 2 cells: off=%d on=%d", off[0].Width, on[0].Width)
	}
	if on[0].CloseX != off[0].CloseX+2 {
		t.Fatalf("CloseX should shift by 2 when icons on: off=%d on=%d",
			off[0].CloseX, on[0].CloseX)
	}
}

// TestDrawTabBar_RendersIconWhenEnabled verifies the glyph actually
// lands on screen between the dirty slot and the file name when
// icons are enabled. We use the simulation screen and look for the
// language-specific glyph from icons.For somewhere on the tab row.
func TestDrawTabBar_RendersIconWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))
	a.tree.IconsEnabled = true

	a.drawTabBar()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()

	// Read the tab-bar row (y=0) and look for the .go glyph.
	wantGlyph := []rune(icons.For("main.go", false, false))[0]
	found := false
	for x := 0; x < w; x++ {
		c := cells[x]
		if len(c.Runes) > 0 && c.Runes[0] == wantGlyph {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected .go glyph on the tab-bar row when icons are enabled")
	}
}

// TestDrawTabBar_NoIconWhenDisabled is the inverse of the above —
// flipping IconsEnabled off must remove the glyph from the tab bar
// (so terminals without a Nerd Font don't see tofu boxes in tabs).
func TestDrawTabBar_NoIconWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))
	a.tree.IconsEnabled = false

	a.drawTabBar()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()

	wantGlyph := []rune(icons.For("main.go", false, false))[0]
	for x := 0; x < w; x++ {
		c := cells[x]
		if len(c.Runes) > 0 && c.Runes[0] == wantGlyph {
			t.Fatalf("did not expect glyph %q at x=%d when icons off", string(wantGlyph), x)
		}
	}
}

// TestTabBar_ActiveTabScrollsIntoView is the regression test for the
// invisible-active-tab P1: with more tabs than the strip can hold at
// 80 columns, activating the last tab must scroll the strip so its
// name is actually painted, with a ‹ marker showing tabs are hidden
// to the left. Before the fix the active tab rendered nowhere.
func TestTabBar_ActiveTabScrollsIntoView(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	names := openManyTabs(t, a, dir, 5)

	a.drawTabBar()
	a.screen.Show()

	if !screenHasText(t, a, names[len(names)-1]) {
		t.Fatal("active tab's name should be scrolled into view at 80 columns")
	}
	if !screenHasText(t, a, "‹") {
		t.Fatal("expected a ‹ marker for tabs hidden to the left")
	}
}

// TestTabBar_ChevronsMarkOverflow pins the marker rules: at scroll 0
// with overflow only a › shows; scrolled to the end only a ‹ shows.
func TestTabBar_ChevronsMarkOverflow(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	openManyTabs(t, a, dir, 5)

	a.tabScroll = 0
	a.drawTabBar()
	a.screen.Show()
	if screenHasText(t, a, "‹") {
		t.Fatal("no ‹ expected at scroll 0")
	}
	if !screenHasText(t, a, "›") {
		t.Fatal("expected › with tabs hidden to the right")
	}

	a.tabScroll = a.maxTabScroll()
	a.drawTabBar()
	a.screen.Show()
	if !screenHasText(t, a, "‹") {
		t.Fatal("expected ‹ when scrolled to the end")
	}
	if screenHasText(t, a, "›") {
		t.Fatal("no › expected at max scroll")
	}
}

// TestDrawStatusBar_ArmedEscTag pins the pending-gesture indicator:
// while an Esc is armed (any window still open) the status bar shows
// an "Esc…" tag, and once the window has fully expired the tag is
// gone. The editor's only modifier must not have invisible state.
func TestDrawStatusBar_ArmedEscTag(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleKey(keyEv(tcell.KeyEsc, 0)) // arm
	a.drawStatusBar()
	a.screen.Show()
	if !screenHasText(t, a, "Esc…") {
		t.Fatal("armed Esc should show the Esc… tag in the status bar")
	}

	a.lastEscape = time.Now().Add(-2 * menuEscWindow) // fully expired
	a.drawStatusBar()
	a.screen.Show()
	if screenHasText(t, a, "Esc…") {
		t.Fatal("expired Esc must not keep the Esc… tag")
	}
}

// TestDrawEmptyEditor_ClipsToEditorRect pins the empty-state hint inside
// the editor pane: on a narrow pane the 45-rune hint used to start left
// of the editor rect and overwrite file-tree rows and the splitter.
func TestDrawEmptyEditor_ClipsToEditorRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(60, 24)
	a.width, a.height = scr.Size()
	a.draw()
	a.screen.Show()

	cells, w, _ := scr.GetContents()
	_, ey, _, eh := a.editorRect()
	hintRow := ey + eh/2 + 1 // one below the vertical midpoint
	sx := a.splitterX()
	if r := cells[hintRow*w+sx].Runes[0]; r != '│' {
		t.Fatalf("splitter cell on the hint row = %q — hint bled into the sidebar", r)
	}
	ex, _, _, _ := a.editorRect()
	if r := cells[hintRow*w+ex].Runes[0]; r != 'C' {
		t.Fatalf("hint should start at the editor's left edge, got %q", r)
	}
}

// TestDrawTabBar_CloseButtonEmphasis pins the close-× hierarchy: the
// active tab (the likeliest close target) gets the brighter Muted ×,
// and inactive tabs recede to Subtle — not the other way around.
func TestDrawTabBar_CloseButtonEmphasis(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt")) // b active, a inactive
	a.drawTabBar()
	a.screen.Show()

	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	for _, r := range a.lastTabRects {
		fg, _, _ := cells[0*w+r.CloseX].Style.Decompose()
		if r.Index == a.tabs.ActiveIndex() {
			if fg != a.theme.Muted {
				t.Fatalf("active tab × fg = %v, want Muted (brighter)", fg)
			}
		} else if fg != a.theme.Subtle {
			t.Fatalf("inactive tab × fg = %v, want Subtle (recedes)", fg)
		}
	}
}
