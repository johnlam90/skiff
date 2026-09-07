// =============================================================================
// File: internal/app/sidebartoggle_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestSidebarToggle_ChevronFacesTheGesture pins the glyph rule: the
// chevron points the way the panel will move when clicked — inward («)
// while the explorer is open, outward (») once it is collapsed — so a
// user never has to remember which state they are in.
func TestSidebarToggle_ChevronFacesTheGesture(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	a.screen.Show()
	sx, _, sw, _ := a.sidebarRect()
	row := []rune(screenLine(a.screen.(tcell.SimulationScreen), 0))
	if got := row[sx+sw-sidebarToggleWidth]; got != sidebarCollapseGlyph {
		t.Fatalf("open sidebar: cell %d = %q, want %q in the header's last slot: %q",
			sx+sw-sidebarToggleWidth, got, sidebarCollapseGlyph, string(row[:sw]))
	}

	a.menuToggleSidebar()
	a.draw()
	a.screen.Show()
	row = []rune(screenLine(a.screen.(tcell.SimulationScreen), 0))
	ex, ew := a.sidebarExpandRect()
	if ex != menuButtonWidth || ew != sidebarToggleWidth {
		t.Fatalf("collapsed: expand slot = (%d,%d), want it right after the ≡ button (%d,%d)",
			ex, ew, menuButtonWidth, sidebarToggleWidth)
	}
	if got := row[ex]; got != sidebarExpandGlyph {
		t.Fatalf("collapsed sidebar: cell %d = %q, want %q: %q", ex, got, sidebarExpandGlyph, string(row[:12]))
	}
	if strings.ContainsRune(string(row), sidebarCollapseGlyph) {
		t.Fatalf("collapsed sidebar must not also paint the collapse chevron: %q", string(row))
	}
}

// TestSidebarToggle_ClicksFlipThePanel is the mouse-first contract: a
// press on the « collapses the explorer, a press on the » brings it
// back, and each also retires any pending responsive restore exactly
// as the ≡ row does — the chevron is the same toggle, not a second one.
func TestSidebarToggle_ClicksFlipThePanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	sx, _, sw, _ := a.sidebarRect()
	a.sidebarAutoHidden = true
	a.handleEvent(tcell.NewEventMouse(sx+sw-sidebarToggleWidth, 0, tcell.Button1, 0))
	a.handleEvent(tcell.NewEventMouse(sx+sw-sidebarToggleWidth, 0, tcell.ButtonNone, 0))
	if a.sidebarShown {
		t.Fatal("clicking « should collapse the sidebar")
	}
	if a.sidebarAutoHidden {
		t.Fatal("an explicit collapse must clear sidebarAutoHidden, like menuToggleSidebar")
	}

	a.draw()
	ex, _ := a.sidebarExpandRect()
	a.handleEvent(tcell.NewEventMouse(ex, 0, tcell.Button1, 0))
	a.handleEvent(tcell.NewEventMouse(ex, 0, tcell.ButtonNone, 0))
	if !a.sidebarShown {
		t.Fatal("clicking » should re-open the sidebar")
	}
}

// TestSidebarToggle_ExpandSlotShiftsTheTabStrip pins the geometry the
// collapsed state changes: the tab strip starts after the » slot, so a
// tab can never be painted under the chevron, and the ≡ button keeps
// its corner (menuButtonRect is untouched — the menu is the primary
// door and its position is documented).
func TestSidebarToggle_ExpandSlotShiftsTheTabStrip(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuToggleSidebar()
	if mx, _, _, _ := a.menuButtonRect(); mx != 0 {
		t.Fatalf("≡ should stay in the corner when collapsed, got x=%d", mx)
	}
	stripX, _ := a.tabStripRegion()
	if stripX != menuButtonWidth+sidebarToggleWidth {
		t.Fatalf("tab strip x = %d, want %d (≡ + » slot)", stripX, menuButtonWidth+sidebarToggleWidth)
	}
	a.menuToggleSidebar()
	stripX, _ = a.tabStripRegion()
	if stripX != a.sidebarW()+menuButtonWidth {
		t.Fatalf("with the sidebar open the strip should start right after ≡, got %d", stripX)
	}
}

// TestSidebarToggle_NoneInSingleFileMode: a session without a tree has
// no sidebar to expand, so the collapsed-state chevron must not appear
// — it would be a control that only flashes a refusal.
func TestSidebarToggle_NoneInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.sidebarShown = false
	if ex, ew := a.sidebarExpandRect(); ex != -1 || ew != 0 {
		t.Fatalf("single-file mode: expand slot = (%d,%d), want none", ex, ew)
	}
	if stripX, _ := a.tabStripRegion(); stripX != menuButtonWidth {
		t.Fatalf("single-file tab strip x = %d, want %d", stripX, menuButtonWidth)
	}
}

// TestSidebarHeaderHit_CollapseZoneBeatsTheGitBadge covers the
// min-width collision: at 17 columns the "GIT N" label would run into
// the chevron's cells, and the chevron must win both the paint and the
// hit — a click on a visible « that switched panels instead would be
// the control lying about itself.
func TestSidebarHeaderHit_CollapseZoneBeatsTheGitBadge(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.refreshGitStatus()
	a.sidebarWidth = minSidebarWidth
	a.draw()
	a.screen.Show()
	sx, _, sw, _ := a.sidebarRect()
	row := []rune(screenLine(a.screen.(tcell.SimulationScreen), 0))
	if got := row[sx+sw-sidebarToggleWidth]; got != sidebarCollapseGlyph {
		t.Fatalf("min-width header lost its chevron: %q", string(row[:sw]))
	}
	for x := sw - sidebarToggleWidth - 1; x < sw; x++ {
		if got := a.sidebarHeaderHit(x, sw); got != "collapse" {
			t.Fatalf("hit at local x=%d = %q, want collapse", x, got)
		}
	}
	if got := a.sidebarHeaderHit(runeLen(sidebarHeaderExplorer)+sidebarHeaderGap, sw); got != "git" {
		t.Fatalf("the GIT label should still be clickable left of the chevron, got %q", got)
	}
}
