// =============================================================================
// File: internal/app/menu_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for menu.go — the action modal's behavior: keyboard navigation,
// hover and click routing, the scroll window a short terminal needs, and
// the draw pass. The scroll-mapping cases matter most: a click has to hit
// the row the user sees, not the row the unscrolled layout would put there.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// TestMenuModalRect centers the modal in the window and clamps the origin
// to (0,0) when the window is too small to fit it.
func TestMenuModalRect_Centered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// The full menu no longer fits a 40-row screen (it clamps + scrolls
	// there); use a taller viewport so this test keeps pinning the
	// *centering* rule rather than the clamp.
	a.width, a.height = 120, 60
	x, y, w, h := a.menuModalRect()
	_, _, expectedH := a.menuLayout()
	if w != modalWidth || h != expectedH {
		t.Fatalf("modal size: got (%d,%d), want (%d,%d)", w, h, modalWidth, expectedH)
	}
	if x != (a.width-modalWidth)/2 || y != (a.height-expectedH)/2 {
		t.Fatalf("modal origin off-center: (%d,%d)", x, y)
	}
}

// TestMenuModalRect_ClampsTinyWindow ensures a window smaller than the
// modal never yields a negative origin, and that the height clamp keeps
// the modal inside the screen instead of letting it overflow (the old
// behavior, which made bottom rows unreachable).
func TestMenuModalRect_ClampsTinyWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 10, 5
	x, y, _, h := a.menuModalRect()
	if x != 0 || y < 0 {
		t.Fatalf("expected non-negative origin at column 0, got (%d,%d)", x, y)
	}
	if h > a.height {
		t.Fatalf("modal height %d overflows the %d-row window", h, a.height)
	}
}

// TestMenuMoveSelection_WrapsAroundEnds simulates a small menu with all rows
// enabled to verify wrapping in both directions.
func TestMenuMoveSelection_WrapsAroundEnds(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Open every potential gate: a savable tab + selection + clipboard.
	tmp := filepath.Join(a.rootDir, "f.txt")
	if err := os.WriteFile(tmp, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(tmp)
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	a.clipBuf = "x"

	// Count the rows currently enabled so we know how many forward
	// steps land us back at the starting row (vs going past it). A
	// hard-coded len breaks every time the menu grows.
	items, _, _ := a.menuLayout()
	enabled := 0
	for _, item := range items {
		if item.enabled(a) {
			enabled++
		}
	}
	if enabled < 2 {
		t.Fatalf("need at least 2 enabled items to test wrap; got %d", enabled)
	}

	// Walk forward exactly `enabled` steps and land on the first row.
	a.hoveredMenuRow = -1
	a.menuMoveSelection(1)
	first := a.hoveredMenuRow
	for i := 1; i < enabled; i++ {
		a.menuMoveSelection(1)
	}
	a.menuMoveSelection(1) // wrap
	if a.hoveredMenuRow != first {
		t.Fatalf("forward wrap: got %d, want %d", a.hoveredMenuRow, first)
	}

	// Same for backward.
	a.hoveredMenuRow = -1
	a.menuMoveSelection(-1)
	last := a.hoveredMenuRow
	for i := 1; i < enabled; i++ {
		a.menuMoveSelection(-1)
	}
	a.menuMoveSelection(-1) // wrap
	if a.hoveredMenuRow != last {
		t.Fatalf("backward wrap: got %d, want %d", a.hoveredMenuRow, last)
	}
}

// TestMenuMoveSelection_NothingEnabledYieldsMinusOne lands on -1 when no row
// is enabled (we synthesise that by setting every predicate to false-ish via
// the no-tab/no-clipboard initial state, except always-true rows).
func TestMenuMoveSelection_SkipsDisabled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// No tabs, no selection, no clipboard. Save/Close/Rename/Delete/Copy/
	// Cut/Paste are all disabled. New file / toggle / quit stay enabled.
	a.hoveredMenuRow = -1
	a.menuMoveSelection(1)
	if a.hoveredMenuRow < 0 {
		t.Fatal("expected a row to land somewhere")
	}
	idx := a.hoveredMenuRow
	items, _, _ := a.menuLayout()
	if !items[idx].enabled(a) {
		t.Fatalf("landed on disabled row %d", idx)
	}
}

// TestMenuActivate_RunsHovered runs the action attached to the highlighted
// row.
func TestMenuActivate_RunsHovered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	// Force highlight onto the sidebar-toggle row (always enabled, label
	// supplied dynamically via labelFor), then activate.
	items, _, _ := a.menuLayout()
	for i, item := range items {
		if item.labelFor != nil && item.label == "" && item.action != nil {
			// labelFor + empty static label is the marker for the toggle
			// row. The newFile row also uses labelFor, so disambiguate by
			// flipping the sidebar and checking afterward.
			a.hoveredMenuRow = i
			a.menuActivate()
			a.openMenu()
		}
	}
	// Re-find and run the toggle row via its dynamic label.
	a.hoveredMenuRow = -1
	items, _, _ = a.menuLayout()
	for i, item := range items {
		if item.labelFor != nil && (item.labelFor(a) == "Show file explorer" || item.labelFor(a) == "Hide file explorer") {
			a.hoveredMenuRow = i
			break
		}
	}
	if a.hoveredMenuRow < 0 {
		t.Fatal("could not find sidebar-toggle row")
	}
	before := a.sidebarShown
	a.menuActivate()
	if a.sidebarShown == before {
		t.Fatal("expected sidebarShown to flip after menuActivate")
	}
}

// TestMenuActivate_OutOfRange and disabled rows are no-ops.
func TestMenuActivate_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.hoveredMenuRow = -1
	a.menuActivate()
	a.hoveredMenuRow = 999
	a.menuActivate()
}

// TestUpdateMenuHover snaps to the right row when over an enabled row, and
// to -1 when outside.
func TestUpdateMenuHover(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()

	// Find an always-enabled row and click on its relY.
	items, _, _ := a.menuLayout()
	var pickIdx, pickRelY int
	for i, item := range items {
		if item.enabled(a) {
			pickIdx = i
			pickRelY = item.relY
			break
		}
	}
	a.updateMenuHover(mx+5, my+pickRelY)
	if a.hoveredMenuRow != pickIdx {
		t.Fatalf("hoveredMenuRow: got %d, want %d", a.hoveredMenuRow, pickIdx)
	}

	// Outside the modal → -1.
	a.updateMenuHover(0, 0)
	if a.hoveredMenuRow != -1 {
		t.Fatalf("outside modal: got %d", a.hoveredMenuRow)
	}
}

// TestHandleMenuMouse_ClicksRowAndOutside both fires the row action and
// dismisses on outside click.
func TestHandleMenuMouse_ClicksRowAndOutside(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Tall viewport: the toggle row sits near the menu's bottom, which
	// on a 40-row screen falls into the scrolled-off region.
	a.width, a.height = 120, 60
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()
	// Click on the sidebar toggle row — flips the sidebar.
	items, _, _ := a.menuLayout()
	toggleRelY := -1
	for _, item := range items {
		if item.labelFor != nil && item.labelFor(a) == "Hide file explorer" {
			toggleRelY = item.relY
			break
		}
	}
	if toggleRelY < 0 {
		t.Fatal("sidebar toggle row not found")
	}
	before := a.sidebarShown
	a.handleMenuMouse(mx+5, my+toggleRelY, tcell.Button1)
	if a.sidebarShown == before {
		t.Fatal("expected toggle to fire")
	}

	// Click outside — closes.
	a.openMenu()
	a.handleMenuMouse(0, 0, tcell.Button1)
	if a.menuOpen {
		t.Fatal("outside click should close menu")
	}
}

// TestHandleMenuMouse_NoButtonIsNoop ignores motion-only events.
func TestHandleMenuMouse_NoButtonIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.handleMenuMouse(0, 0, 0)
	if !a.menuOpen {
		t.Fatal("motion-only event should not close menu")
	}
}

// TestDrawMenu_RightAlignsShortcuts verifies the shortcut column is painted at
// the modal's right edge instead of being appended to the command label.
func TestDrawMenu_RightAlignsShortcuts(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.drawMenu()
	a.screen.Show()

	mx, my, mw, _ := a.menuModalRect()
	save := menuItemByLabel(t, a, "Save")
	shortcutX := mx + mw - 2 - runeLen(save.shortcut)
	line := screenLine(a.screen.(tcell.SimulationScreen), my+save.relY)
	lineRunes := []rune(line)
	if got := string(lineRunes[shortcutX : shortcutX+runeLen(save.shortcut)]); got != save.shortcut {
		t.Fatalf("right shortcut = %q, want %q on line %q", got, save.shortcut, line)
	}
	if strings.Contains(string(lineRunes[mx+4:shortcutX-1]), save.shortcut) {
		t.Fatalf("shortcut should not be appended to label area: %q", line)
	}
}

// TestMenuModalRect_ClampsToShortTerminal is the geometry half of the
// clipped-menu regression: at the app's declared 80×24 minimum the modal
// must fit the screen (with a one-row margin) instead of rendering its
// natural ~38-row layout off the bottom edge.
func TestMenuModalRect_ClampsToShortTerminal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)
	_, _, _, h := a.menuModalRect()
	if h > a.height-2 {
		t.Fatalf("modal height %d exceeds screen height-2 (%d)", h, a.height-2)
	}
	if a.menuMaxScroll() <= 0 {
		t.Fatal("expected the clamped menu to report scrollable overflow")
	}
}

// TestMenuScroll_KeyboardScrollsQuitIntoView is the regression test for
// the P1 "Quit editor is unreachable at 80×24" bug: selecting the last
// row via keyboard must scroll it into the visible region and actually
// paint it. Before the fix the row was drawn past the screen edge and
// silently dropped.
func TestMenuScroll_KeyboardScrollsQuitIntoView(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	a.menuMoveSelection(-1) // wrap from the first row to the last (Quit — always enabled)

	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow != len(items)-1 {
		t.Fatalf("expected wrap-around to select the last row, got %d", a.hoveredMenuRow)
	}

	a.drawMenu()
	a.screen.Show()
	if !screenHasText(t, a, "Quit editor") {
		t.Fatal("Quit editor row should be scrolled into view and drawn at 80×24")
	}
}

// TestMenuMouse_WheelScrollsAndClamps drives the wheel over the open
// menu: down-ticks must advance menuScroll toward menuMaxScroll and
// never past it; up-ticks must clamp back at zero.
func TestMenuMouse_WheelScrollsAndClamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	mx, my, mw, mh := a.menuModalRect()
	cx, cy := mx+mw/2, my+mh/2

	max := a.menuMaxScroll()
	for i := 0; i < max+5; i++ {
		a.handleMenuMouse(cx, cy, tcell.WheelDown)
	}
	if a.menuScroll != max {
		t.Fatalf("wheel-down should clamp at maxScroll %d, got %d", max, a.menuScroll)
	}
	for i := 0; i < max+5; i++ {
		a.handleMenuMouse(cx, cy, tcell.WheelUp)
	}
	if a.menuScroll != 0 {
		t.Fatalf("wheel-up should clamp at 0, got %d", a.menuScroll)
	}
}

// TestMenuClick_MapsThroughScroll pins the click hit-test under scroll:
// with the menu scrolled to the bottom, clicking the on-screen row where
// Quit now sits must activate Quit, not whichever row used to own that
// screen line. A click that lands on the bottom border must do nothing.
func TestMenuClick_MapsThroughScroll(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	a.menuScroll = a.menuMaxScroll()

	items, _, _ := a.menuLayout()
	quit := items[len(items)-1]
	mx, my, _, mh := a.menuModalRect()

	borderY := my + mh - 1
	a.handleMenuMouse(mx+2, borderY, tcell.Button1)
	if a.quit {
		t.Fatal("clicking the bottom border must not activate a hidden row")
	}

	quitY := my + quit.relY - a.menuScroll
	if quitY < my+3 || quitY > my+mh-2 {
		t.Fatalf("test setup: Quit row not in visible region (y=%d)", quitY)
	}
	a.handleMenuMouse(mx+2, quitY, tcell.Button1)
	if !a.quit {
		t.Fatal("clicking the scrolled Quit row should quit the editor")
	}
}

// TestDrawMenu_HoveredShortcutUsesTextFg pins the hover-row contrast
// fix: the shortcut hint on the highlighted row used to render in Muted
// on the Selection background (~2.6:1, illegible on the very row the
// user is reading). It must use the Text foreground instead.
func TestDrawMenu_HoveredShortcutUsesTextFg(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.menuMoveSelection(-1) // wrap to the last row: Quit editor, "Esc q"

	a.drawMenu()
	a.screen.Show()

	items, _, _ := a.menuLayout()
	quit := items[len(items)-1]
	mx, my, mw, _ := a.menuModalRect()
	shortcutX := mx + mw - 2 - runeLen(quit.shortcut)
	cy := my + quit.relY - a.menuScroll

	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	fg, bg, _ := cells[cy*w+shortcutX].Style.Decompose()
	if bg != a.theme.Selection {
		t.Fatalf("expected hover bg under the shortcut, got %v", bg)
	}
	if fg != a.theme.Text {
		t.Fatalf("hovered shortcut fg: got %v, want Text %v", fg, a.theme.Text)
	}
}

// TestMenuWheel_RecomputesHoverAfterScroll pins the hover highlight to
// the row actually under the pointer: a wheel tick used to scroll the
// menu after hover was computed, leaving the highlight one row stale
// until the next mouse motion.
func TestMenuWheel_RecomputesHoverAfterScroll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(120, 24) // short terminal so the menu overflows and scrolls
	a.width, a.height = scr.Size()
	a.openFile(path)
	a.openMenu()
	if a.menuMaxScroll() <= 0 {
		t.Fatal("test setup needs an overflowing menu")
	}

	// Park the pointer on an enabled row ("Find in file"), then wheel.
	items, _, _ := a.menuLayout()
	relY := -1
	for _, it := range items {
		if it.label == "Find in file" {
			relY = it.relY
			break
		}
	}
	if relY < 0 {
		t.Fatal("Find in file row not found")
	}
	mx, my, _, _ := a.menuModalRect()
	x, y := mx+6, my+relY
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.WheelDown, 0))
	if a.menuScroll == 0 {
		t.Fatal("wheel should have scrolled the menu")
	}
	got := a.hoveredMenuRow
	a.updateMenuHover(x, y)
	if got != a.hoveredMenuRow {
		t.Fatalf("hover after wheel = %d, recomputed for the same pointer = %d — stale hover", got, a.hoveredMenuRow)
	}
}
