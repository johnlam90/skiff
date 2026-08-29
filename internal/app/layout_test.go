// =============================================================================
// File: internal/app/layout_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for layout.go — the panel rectangles every renderer and mouse
// hit-tester derives from the window size. These pin the exact geometry,
// including the sidebar-hidden reflow and the splitter's min-width clamps,
// because a one-cell drift here silently misaligns clicks.

package app

import (
	"testing"
	"time"
)

// TestSidebarW_ShownVsHidden verifies the sidebar width helper returns 0
// when hidden and the configured width when shown. Every layout helper
// pivots on this so we want it locked in.
func TestSidebarW_ShownVsHidden(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.sidebarW(); got != defaultSidebarWidth {
		t.Fatalf("shown sidebarW: got %d, want %d", got, defaultSidebarWidth)
	}
	a.sidebarShown = false
	if got := a.sidebarW(); got != 0 {
		t.Fatalf("hidden sidebarW: got %d, want 0", got)
	}
}

// TestSidebarRect checks the sidebar render rectangle reserves one cell
// for the splitter on its right edge, and collapses to zero when hidden.
func TestSidebarRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.sidebarRect()
	if x != 0 || y != 0 {
		t.Fatalf("expected origin (0,0), got (%d,%d)", x, y)
	}
	if w != defaultSidebarWidth-1 {
		t.Fatalf("expected w = sidebarWidth-1, got %d", w)
	}
	if h != a.height-1 {
		t.Fatalf("expected h = height-1, got %d", h)
	}

	a.sidebarShown = false
	x, y, w, h = a.sidebarRect()
	if x != 0 || y != 0 || w != 0 || h != 0 {
		t.Fatalf("expected zero rect when hidden, got (%d,%d,%d,%d)", x, y, w, h)
	}
}

// TestSplitterX returns the splitter column when shown and -1 when hidden.
func TestSplitterX(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.splitterX(); got != defaultSidebarWidth-1 {
		t.Fatalf("shown splitterX: got %d", got)
	}
	a.sidebarShown = false
	if got := a.splitterX(); got != -1 {
		t.Fatalf("hidden splitterX: got %d, want -1", got)
	}
}

// TestTabBarRect checks the tab bar starts after the sidebar and spans the
// remaining width on row 0.
func TestTabBarRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.tabBarRect()
	if x != defaultSidebarWidth || y != 0 || h != 1 {
		t.Fatalf("tabBar position/size unexpected: (%d,%d,%d,%d)", x, y, w, h)
	}
	if w != a.width-defaultSidebarWidth {
		t.Fatalf("tabBar width: got %d", w)
	}
	a.sidebarShown = false
	x, _, w, _ = a.tabBarRect()
	if x != 0 || w != a.width {
		t.Fatalf("hidden-sidebar tabBar should fill row: got x=%d w=%d", x, w)
	}
}

// TestEditorRect verifies the editor body sits between tab bar and status
// bar, to the right of the sidebar.
func TestEditorRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.editorRect()
	if x != defaultSidebarWidth || y != 1 {
		t.Fatalf("editor origin: (%d,%d)", x, y)
	}
	if w != a.width-defaultSidebarWidth {
		t.Fatalf("editor width: got %d", w)
	}
	if h != a.height-2 {
		t.Fatalf("editor height: got %d", h)
	}
}

// TestStatusRect always returns the bottom-most row, full width.
func TestStatusRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.statusRect()
	if x != 0 || y != a.height-1 || w != a.width || h != 1 {
		t.Fatalf("status rect: (%d,%d,%d,%d)", x, y, w, h)
	}
}

// TestMenuButtonRect places the ≡ button at the start of the tab bar and
// shifts left when the sidebar is hidden.
func TestMenuButtonRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, _, w, _ := a.menuButtonRect()
	if x != defaultSidebarWidth || w != menuButtonWidth {
		t.Fatalf("shown menuButtonRect: x=%d w=%d", x, w)
	}
	a.sidebarShown = false
	x, _, _, _ = a.menuButtonRect()
	if x != 0 {
		t.Fatalf("hidden menuButtonRect should sit at column 0: got %d", x)
	}
}

// TestResizeSidebar_Clamps verifies the sidebar width clamps to the
// [minSidebarWidth, width-minEditorAfterDrag] range.
func TestResizeSidebar_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// Negative target → clamps up to minSidebarWidth.
	a.resizeSidebar(-50)
	if a.sidebarWidth != minSidebarWidth {
		t.Fatalf("negative target: got %d, want %d", a.sidebarWidth, minSidebarWidth)
	}

	// Above max → clamps to width - minEditorAfterDrag.
	a.resizeSidebar(a.width)
	wantMax := a.width - minEditorAfterDrag
	if a.sidebarWidth != wantMax {
		t.Fatalf("oversize target: got %d, want %d", a.sidebarWidth, wantMax)
	}

	// In range — kept verbatim.
	a.resizeSidebar(25)
	if a.sidebarWidth != 25 {
		t.Fatalf("in-range target: got %d", a.sidebarWidth)
	}
}

// TestResizeSidebar_TinyWindow falls back to minSidebarWidth when the window
// is too narrow for both panels at the requested size.
func TestResizeSidebar_TinyWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width = 30 // smaller than minSidebarWidth + minEditorAfterDrag.
	a.resizeSidebar(50)
	if a.sidebarWidth != minSidebarWidth {
		t.Fatalf("tiny window: got %d, want %d", a.sidebarWidth, minSidebarWidth)
	}
}

// TestEditorSize matches the editor rect's width and height.
func TestEditorSize(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	w, h := a.editorSize()
	if w != a.width-defaultSidebarWidth || h != a.height-2 {
		t.Fatalf("editorSize: got (%d,%d)", w, h)
	}
}

// TestStripRowBudget_LeavesRoomForEveryStrip is the fence under the
// minimum size. minHeight, findBarHeight and flashStripMaxRows are three
// constants in three files, and the editor only survives their sum by
// arithmetic: on the shortest terminal skiff agrees to run, with the find
// bar up, there must still be room for a full-height flash strip AND a
// row of editor underneath it. Lowering minHeight past this is the
// mistake this test exists to catch.
func TestStripRowBudget_LeavesRoomForEveryStrip(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, minWidth, minHeight)
	a.strip = &findStrip{a: a}

	if got := a.stripRowBudget(); got < flashStripMaxRows {
		t.Fatalf("budget %d at %dx%d cannot hold a %d-row flash strip",
			got, minWidth, minHeight, flashStripMaxRows)
	}
}

// TestEditorRect_NeverStarvesUnderStrips walks every strip combination at
// the minimum size: the rect the caret, the scrollbar and every hit-test
// derive from must stay at least one row tall and must never describe
// rows below the status bar.
func TestEditorRect_NeverStarvesUnderStrips(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, minWidth, minHeight)

	for _, find := range []bool{false, true} {
		for _, msg := range []string{"", longFlash} {
			a.strip = nil
			if find {
				a.strip = &findStrip{a: a}
			}
			a.statusMsg, a.statusUntil = msg, time.Now().Add(time.Minute)
			_, y, _, h := a.editorRect()
			if h < editorMinRows {
				t.Fatalf("find=%v flash=%v: editor height %d", find, msg != "", h)
			}
			if bottom := y + h + a.flashStripRows(); bottom > a.height-1 {
				t.Fatalf("find=%v flash=%v: strips reach row %d past the status bar at %d",
					find, msg != "", bottom, a.height-1)
			}
		}
	}
}

// TestEditorRect_FloorsBelowTheMinimumSize pins the guard for the events
// the size check does not cover. draw() bails to drawTooSmall under
// minHeight, but keyboard handlers ask for editorSize on every keystroke
// — including the ones that arrive while a phone is mid-rotation and the
// terminal reports two rows. A zero or negative page height there is a
// division and an index away from a panic.
func TestEditorRect_FloorsBelowTheMinimumSize(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, h := range []int{0, 1, 2, 3} {
		resizeTestApp(t, a, 10, h)
		if _, eh := a.editorSize(); eh < editorMinRows {
			t.Fatalf("screen height %d: editor height %d", h, eh)
		}
	}
}

// TestSidebarPanelsSurviveTheMinimumHeight surveys the two surfaces that
// stack a fixed prefix of rows inside the sidebar before their scrollable
// list starts: the git panel (header, branch line, button row —
// gitPanelListTop) and the file tree (its EXPLORER header and the project
// root row, one fewer). The git panel is therefore the binding one, and
// on the shortest terminal skiff runs in it must still have a list, even
// with its keyboard hint strip docked at the bottom taking rows back.
func TestSidebarPanelsSurviveTheMinimumHeight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, minWidth, minHeight)
	a.sidebarShown = true
	a.gitPanel.active = true
	a.gitPanel.keys = true

	_, _, _, sh := a.sidebarRect()
	if sh-gitPanelListTop < 1 {
		t.Fatalf("a %d-row sidebar leaves no list under a %d-row header",
			sh, gitPanelListTop)
	}
	listH, hint := a.gitPanelBody()
	if listH < 1 {
		t.Fatalf("git panel list starved to %d rows (hint took %d)", listH, len(hint))
	}
	if listH+len(hint) != sh-gitPanelListTop {
		t.Fatalf("panel rows do not add up: list %d + hint %d != %d",
			listH, len(hint), sh-gitPanelListTop)
	}
}
