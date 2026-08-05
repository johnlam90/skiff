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
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/theme"
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

// screenText flattens the simulation screen's front buffer into one rune
// slice so a test can assert that a label was actually painted. Show()
// first: GetContents serves the front buffer, which a bare draw() has
// not yet swapped into.
func screenText(t *testing.T, a *App) []rune {
	t.Helper()
	scr, ok := a.screen.(tcell.SimulationScreen)
	if !ok {
		t.Fatalf("test app is not backed by a SimulationScreen")
	}
	scr.Show()
	cells, _, _ := scr.GetContents()
	out := make([]rune, 0, len(cells))
	for _, c := range cells {
		if len(c.Runes) > 0 {
			out = append(out, c.Runes[0])
		} else {
			out = append(out, ' ')
		}
	}
	return out
}

// TestDraw_AllPanels walks the render pass through every top-level state
// and asserts each one actually paints its distinguishing text. The
// previous version called draw() in each state and asserted nothing at
// all, so a draw() that painted an empty screen — or painted the wrong
// panel in every state — passed it. Cell-exact geometry stays in the
// focused tests below; this one pins "the right surface got drawn".
func TestDraw_AllPanels(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	paints := func(state, want string) {
		t.Helper()
		if !containsRunes(screenText(t, a), want) {
			t.Errorf("%s: screen never painted %q", state, want)
		}
	}
	omits := func(state, unwanted string) {
		t.Helper()
		if containsRunes(screenText(t, a), unwanted) {
			t.Errorf("%s: screen still shows %q", state, unwanted)
		}
	}

	a.draw()
	paints("empty editor", "No file open")

	a.openFile(target)
	a.draw()
	paints("open tab", "f.txt") // tab strip
	paints("open tab", "hi")    // buffer body
	omits("open tab", "No file open")
	omits("open tab", "● f.txt") // clean tab: no modified dot

	a.activeTabPtr().Dirty = true
	a.draw()
	// The status bar still holds openFile's flash here, so the dirty
	// state's visible marker is the tab strip's dot.
	paints("dirty tab", "● f.txt")

	a.openMenu()
	a.draw()
	paints("menu", "Save")
	a.closeMenu()

	a.openPrompt("Rename file", "new name", "f.txt", nil)
	a.draw()
	paints("prompt", "Rename file")
	a.closeAllModals()

	a.openConfirm("Discard?", "Throw away unsaved edits", nil)
	a.draw()
	paints("confirm", "Throw away unsaved edits")
	a.closeAllModals()

	a.openTreeContext(a.tree.Root, 5, 5)
	a.draw()
	paints("tree context", "Copy rel path")
	a.closeAllModals()

	a.flash("hello")
	a.draw()
	paints("status flash", "hello")

	a.sidebarShown = false
	a.draw()
	omits("sidebar hidden", filepath.Base(dir))

	// Tiny window → the resize message replaces every panel.
	a.width, a.height = 5, 5
	a.draw()
	paints("tiny window", "Wind") // the message is clipped to 5 columns
	omits("tiny window", "f.txt")
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

// TestDrawEmptyEditor_NamesKeyboardRoutes pins the second hint line. The
// mouse hint alone is a dead end on an SSH session with no mouse
// reporting, so the empty state has to name the Esc-leader gestures that
// actually open something: Esc p, Esc n, and Esc Esc for the menu.
func TestDrawEmptyEditor_NamesKeyboardRoutes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	a.screen.Show()

	for _, want := range []string{"Esc p", "find file", "Esc n", "new file", "Esc Esc"} {
		if !screenHasText(t, a, want) {
			t.Errorf("empty editor should mention %q", want)
		}
	}
}

// TestDrawEmptyEditor_HintsAreLeaderBindings guards the hint text
// against the bindings drifting out from under it: every key the second
// hint advertises must still be a live leader binding, because a hint
// that names a removed gesture is worse than no hint.
func TestDrawEmptyEditor_HintsAreLeaderBindings(t *testing.T) {
	hint := emptyEditorHints[len(emptyEditorHints)-1]
	bound := map[rune]bool{}
	for _, b := range leaderBindings() {
		bound[b.key] = true
	}
	for _, key := range []rune{'p', 'n'} {
		if !bound[key] {
			t.Fatalf("hint %q advertises Esc %c but nothing is bound to it", hint, key)
		}
	}
}

// TestDrawEmptyEditor_ClipsEveryHintRow extends the narrow-pane clip
// guarantee to the keyboard hint: the longer second line is the one most
// likely to overrun, and it must not paint on the splitter either.
func TestDrawEmptyEditor_ClipsEveryHintRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(60, 24)
	a.width, a.height = scr.Size()
	a.draw()
	a.screen.Show()

	cells, w, _ := scr.GetContents()
	_, ey, _, eh := a.editorRect()
	sx := a.splitterX()
	for row := range len(emptyEditorHints) {
		y := ey + eh/2 + 1 + row
		if r := cells[y*w+sx].Runes[0]; r != '│' {
			t.Fatalf("hint row %d bled onto the splitter: %q", row, r)
		}
	}
}

// TestDrawStatusBar_DiskConflictMarker pins the persistent conflict
// warning: dismissing the conflict overlay must not mean forgetting the
// conflict, so the status bar keeps a marker until the tab is saved or
// reloaded.
func TestDrawStatusBar_DiskConflictMarker(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("disk\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.draw()
	a.screen.Show()
	if screenHasText(t, a, "disk conflict") {
		t.Fatal("no conflict recorded, marker must not show")
	}

	a.activeTabPtr().Dirty = true
	a.noteDiskConflict(target, time.Now())
	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, "disk conflict") {
		t.Fatal("unresolved conflict should keep a status-bar marker")
	}

	// Saving resolves it — a clean buffer can't still be in conflict.
	a.activeTabPtr().Dirty = false
	a.draw()
	a.screen.Show()
	if screenHasText(t, a, "disk conflict") {
		t.Fatal("clean buffer must drop the conflict marker")
	}
}

// TestDrawStatusBar_LowColorUsesAttributes pins the degraded-palette
// wiring: with StatusBG/StatusFg collapsed onto the terminal default,
// reverse video is the only thing left that says "this row is a bar".
func TestDrawStatusBar_LowColorUsesAttributes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.theme = theme.Degrade(a.theme, 16)
	a.drawStatusBar()
	a.screen.Show()

	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	_, sy, _, _ := a.statusRect()
	_, _, attrs := cells[sy*w].Style.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Fatalf("degraded status bar attrs = %v, want AttrReverse", attrs)
	}
}

// TestDrawTabBar_LowColorMarksActiveTabWithAttributes covers the tab
// strip half of the same fallback: on a degraded palette the active
// tab's background matches every other tab's, so Attrs.ActiveTab has to
// carry the distinction.
func TestDrawTabBar_LowColorMarksActiveTabWithAttributes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt"))
	a.theme = theme.Degrade(a.theme, 16)
	a.drawTabBar()
	a.screen.Show()

	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	want := a.theme.Attrs.ActiveTab
	if want == 0 {
		t.Fatal("degraded palette should define an ActiveTab attribute")
	}
	for _, r := range a.lastTabRects {
		_, _, attrs := cells[0*w+r.X].Style.Decompose()
		active := r.Index == a.tabs.ActiveIndex()
		if active && attrs&want != want {
			t.Fatalf("active tab attrs = %v, want %v set", attrs, want)
		}
		if !active && attrs&want == want {
			t.Fatalf("inactive tab must not wear the active attributes (%v)", attrs)
		}
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

// TestWrapFlashLines covers the strip's wrap helper: a message that fits
// stays one line, a long one breaks on spaces rather than mid-word, a
// single over-long run breaks hard, and anything past the row cap is
// ellipsised instead of silently dropped.
func TestWrapFlashLines(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		w    int
		want []string
	}{
		{"fits", "Saved", 20, []string{"Saved"}},
		{"exact", "Saved", 5, []string{"Saved"}},
		{"word break", "Saved main.go to disk", 10, []string{"Saved", "main.go to", "disk"}},
		{"hard break", "aaaaaaaaaa", 4, []string{"aaaa", "aaaa", "aa"}},
		{"cap ellipsises", "aaaaaaaaaaaaaaaaaaaa", 4, []string{"aaaa", "aaaa", "aaa…"}},
		{"zero width", "Saved", 0, nil},
		{"empty", "", 10, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wrapFlashLines(c.msg, c.w)
			if len(got) != len(c.want) {
				t.Fatalf("wrapFlashLines(%q, %d) = %q, want %q", c.msg, c.w, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("line %d = %q, want %q (all %q)", i, got[i], c.want[i], got)
				}
			}
			if len(got) > flashStripMaxRows {
				t.Fatalf("%d rows exceeds the %d-row cap", len(got), flashStripMaxRows)
			}
		})
	}
}

// longFlash is a save-report-shaped message too wide for a narrow status
// bar — the case where clipping hides exactly the part the user needs.
const longFlash = "Format failed: gofmt exited 2 — internal/app/draw.go:412:1: expected declaration, found '}'"

// narrowFlashApp is the shape a tmux split on a laptop actually produces:
// a terminal under autoHideSidebarWidth, resized through the event loop so
// applyResponsiveSidebar collapses the explorer for its own reasons rather
// than the test poking sidebarShown. That leaves the status bar as wide as
// it will ever be here — which is precisely when a clipped flash is the
// user's only copy of the message.
func narrowFlashApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	resizeApp(t, a, autoHideSidebarWidth-2, 24)
	if a.sidebarShown {
		t.Fatal("precondition: the explorer should have auto-collapsed")
	}
	return a
}

// TestFlashStrip_LongFlashTakesItsOwnRow is the headline behavior: a
// message the status bar would clip moves onto a transient row above it,
// the editor reflows to make space, and the bar goes back to the file
// readout instead of showing half a sentence.
func TestFlashStrip_LongFlashTakesItsOwnRow(t *testing.T) {
	a := narrowFlashApp(t)
	openTestFile(t, a, a.rootDir, "reflow.go", "package p\n")

	_, _, _, before := a.editorRect()
	a.flash(longFlash)

	rows := a.flashStripRows()
	if rows < 2 {
		t.Fatalf("a %d-rune message on 60 columns should wrap, got %d row(s)",
			runeLen(longFlash), rows)
	}
	_, _, _, after := a.editorRect()
	if after != before-rows {
		t.Fatalf("editor height %d -> %d, want a drop of exactly %d rows",
			before, after, rows)
	}

	a.draw()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, sy, _, _ := a.flashStripRect()
	var painted string
	for i := 0; i < rows; i++ {
		painted += strings.TrimSpace(screenLine(scr, sy+i)) + " "
	}
	// Every word of the message has to be readable somewhere on the strip
	// — that is the whole contract, and it is what clipping broke.
	for _, word := range strings.Fields(longFlash) {
		if !strings.Contains(painted, word) {
			t.Fatalf("word %q missing from the strip: %q", word, painted)
		}
	}
	// The bar underneath must carry the file readout, not a clipped copy.
	bar := screenLine(scr, a.height-1)
	if !strings.Contains(bar, "Ln 1, Col 1") {
		t.Fatalf("status bar should fall back to the file readout, got %q", bar)
	}
}

// TestFlashStrip_RowGoesAwayWhenFlashExpires pins the other half of the
// reflow: the editor gets its row back the moment the flash's window
// closes, and nothing stale is left painted on it.
func TestFlashStrip_RowGoesAwayWhenFlashExpires(t *testing.T) {
	a := narrowFlashApp(t)
	a.flash(longFlash)
	if a.flashStripRows() == 0 {
		t.Fatal("precondition: the long flash should have opened the strip")
	}
	_, _, _, withStrip := a.editorRect()
	_, sy, _, _ := a.flashStripRect()

	// Expire the window the way statusFlashFor does, without sleeping.
	a.statusUntil = time.Now().Add(-time.Millisecond)

	if got := a.flashStripRows(); got != 0 {
		t.Fatalf("expired flash still claims %d row(s)", got)
	}
	_, _, _, restored := a.editorRect()
	if restored <= withStrip {
		t.Fatalf("editor height stayed at %d after expiry (was %d with the strip)",
			restored, withStrip)
	}

	a.draw()
	a.screen.Show()
	line := screenLine(a.screen.(tcell.SimulationScreen), sy)
	if strings.Contains(line, "gofmt") {
		t.Fatalf("expired strip is still painted: %q", line)
	}
}

// TestFlashStrip_ShortFlashStaysInTheStatusBar keeps the strip rare: the
// overwhelming majority of flashes fit the bar, and stealing an editor row
// for them would be a permanent tax for a three-second message.
func TestFlashStrip_ShortFlashStaysInTheStatusBar(t *testing.T) {
	a := narrowFlashApp(t)
	a.flash("Saved")

	if a.flashStripVisible() {
		t.Fatal("a five-rune message does not need its own row")
	}
	a.draw()
	a.screen.Show()
	if bar := screenLine(a.screen.(tcell.SimulationScreen), a.height-1); !strings.Contains(bar, "Saved") {
		t.Fatalf("short flash should stay in the bar, got %q", bar)
	}
}

// TestFlashStrip_SkippedWhenItWouldShowLess is the guard against a
// pointless reflow. The strip's edge is that it can wrap onto three rows,
// so it usually beats the bar even at equal width — but drag the sidebar
// out to its legal maximum on a wide terminal and the strip is left ~40
// columns while the bar keeps nearly all of them. Reflowing the editor to
// show LESS of the message is strictly worse, so the strip stays shut.
func TestFlashStrip_SkippedWhenItWouldShowLess(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 120, 40)
	a.resizeSidebar(a.width) // clamps to the widest legal sidebar
	a.flash(strings.Repeat("x", 130))

	if a.flashStripCapacity() > a.statusFlashRoom() {
		t.Fatalf("precondition: strip capacity (%d) should not beat the bar (%d)",
			a.flashStripCapacity(), a.statusFlashRoom())
	}
	if a.flashStripVisible() {
		t.Fatal("the strip must not open when it would show less than the bar")
	}
	if _, _, _, h := a.editorRect(); h != a.height-2 {
		t.Fatalf("editor reflowed for a strip that never opened: h = %d", h)
	}
}

// TestFlashStrip_StacksAboveTheFindBar pins the bottom-chrome order: the
// find bar keeps the row directly above the status bar (its documented
// home, and where find_test.go looks for it) and the flash strip stacks on
// top of it, with the editor reflowing for both.
func TestFlashStrip_StacksAboveTheFindBar(t *testing.T) {
	a := narrowFlashApp(t)
	openTestFile(t, a, a.rootDir, "stack.go", "package p\n")
	a.openFind()
	a.flash(longFlash)

	_, fy, _, _ := a.findBarRect()
	_, sy, _, sh := a.flashStripRect()
	if sy+sh != fy {
		t.Fatalf("strip rows [%d,%d) should end exactly at the find bar row %d", sy, sy+sh, fy)
	}
	_, ey, _, eh := a.editorRect()
	if ey+eh != sy {
		t.Fatalf("editor [%d,%d) should end where the strip starts (%d)", ey, ey+eh, sy)
	}
}

// TestFlashStrip_NotAnOverlay pins ADR-0001 for the new strip: it reflows
// the editor rather than floating over it, so it never reaches the overlay
// stack (which would give it the keyboard), and a click on its row falls
// through instead of being captured — the caret stays where it was and no
// drag is armed, exactly as with the find bar.
func TestFlashStrip_NotAnOverlay(t *testing.T) {
	a := narrowFlashApp(t)
	openTestFile(t, a, a.rootDir, "adr.go", "package p\nsecond\nthird\n")
	a.flash(longFlash)
	if !a.flashStripVisible() {
		t.Fatal("precondition: the strip should be up")
	}
	if a.overlays.IsOpen() || a.anyModalOpen() {
		t.Fatal("the flash strip must not be an overlay")
	}

	tab := a.activeTabPtr()
	before := tab.Cursor
	_, sy, _, _ := a.flashStripRect()
	a.handleMouse(tcell.NewEventMouse(a.sidebarW()+2, sy, tcell.Button1, tcell.ModNone))
	if tab.Cursor != before {
		t.Fatalf("a click on the strip moved the caret %v -> %v", before, tab.Cursor)
	}
	if a.dragMode != "" {
		t.Fatalf("a click on the strip armed drag mode %q", a.dragMode)
	}
}

// TestDrawTabBar_ActiveTabUnderlined pins the colour-independent focus
// marker: the active tab's cells carry a solid underline (and inactive
// ones do not), so "which tab am I in?" survives a monochrome terminal and
// a strip crowded with italic preview tabs.
func TestDrawTabBar_ActiveTabUnderlined(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openManyTabs(t, a, dir, 3)
	// Assert on the monochrome palette too: that is the case where colour
	// carries nothing at all.
	for _, mono := range []bool{false, true} {
		if mono {
			a.theme = theme.Degrade(a.theme, 2)
		}
		a.drawTabBar()
		a.screen.Show()
		cells, _, _ := a.screen.(tcell.SimulationScreen).GetContents()
		for _, r := range a.lastTabRects {
			// Row 0 of a flat cell buffer indexes straight by column.
			st := cells[r.X].Style
			active := r.Index == a.tabs.ActiveIndex()
			// GetUnderlineStyle, not the attribute bit: the terminal
			// driver emits the escape from the underline STYLE, so an
			// AttrUnderline with a None style paints nothing.
			if ul := st.GetUnderlineStyle() != tcell.UnderlineStyleNone; ul != active {
				t.Fatalf("mono=%v tab %d: underlined=%v, want %v", mono, r.Index, ul, active)
			}
			_, _, attrs := st.Decompose()
			if got := attrs&tcell.AttrUnderline != 0; got != active {
				t.Fatalf("mono=%v tab %d: underline attribute=%v, want %v",
					mono, r.Index, got, active)
			}
		}
	}
}

// TestTabChevrons_CountHiddenTabs pins the overflow badge: scrolled to the
// end of a crowded strip it names how many tabs are off-screen to the
// left, and only tabs that are entirely hidden are counted — a partially
// visible tab is still clickable, so counting it would overstate the
// promise.
func TestTabChevrons_CountHiddenTabs(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	openManyTabs(t, a, dir, 6)

	a.tabScroll = a.maxTabScroll()
	left, right := a.tabChevrons()
	if right.Label != "" {
		t.Fatalf("no right badge expected at max scroll, got %q", right.Label)
	}
	nl, _ := a.tabOverflow()
	if nl == 0 {
		t.Fatal("precondition: scrolling to the end should hide tabs on the left")
	}
	if want := "‹" + itoa(nl); left.Label != want {
		t.Fatalf("left badge = %q, want %q", left.Label, want)
	}

	a.drawTabBar()
	a.screen.Show()
	row := []rune(screenLine(a.screen.(tcell.SimulationScreen), 0))
	if got := string(row[left.X : left.X+runeLen(left.Label)]); got != left.Label {
		t.Fatalf("painted badge = %q, want %q", got, left.Label)
	}
}

// TestTabChevrons_DropCountsOnACrampedStrip keeps the badge from eating
// the tabs it points at: when the strip can't hold both counts and a
// readable tab, the counts go and the clickable chevrons stay.
func TestTabChevrons_DropCountsOnACrampedStrip(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	openManyTabs(t, a, dir, 12)
	a.tabScroll = a.maxTabScroll() / 2 // hidden on both sides

	if nl, nr := a.tabOverflow(); nl == 0 || nr == 0 {
		t.Fatalf("precondition: want tabs hidden both ways, got %d/%d", nl, nr)
	}
	// Squeeze the strip until the badges no longer earn their counts.
	a.sidebarWidth = a.width - menuButtonWidth - minVisibleTabCells - 1
	left, right := a.tabChevrons()
	if left.Label != "‹" || right.Label != "›" {
		t.Fatalf("cramped strip badges = %q / %q, want the bare chevrons",
			left.Label, right.Label)
	}
	if !left.hit(left.X) || !right.hit(right.X) {
		t.Fatal("the bare chevrons must still be click targets")
	}
}
