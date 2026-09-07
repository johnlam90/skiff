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

// TestSidebarToggle_ChevronFacesTheGesture pins the glyph rule and the
// placement: the chevron points the way the panel will move — inward
// («) on the sidebar's footer row while the explorer is open, outward
// (») in the status bar's first cell once it is collapsed — so the
// handle is always at the window's bottom-left and never has to be
// hunted for.
func TestSidebarToggle_ChevronFacesTheGesture(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	fx, fy, fw := a.sidebarFooterRect()
	if fy != a.height-2 {
		t.Fatalf("footer row = %d, want the row above the status bar (%d)", fy, a.height-2)
	}
	row := []rune(screenLine(scr, fy))
	if got := row[fx+fw-sidebarToggleWidth]; got != sidebarCollapseGlyph {
		t.Fatalf("open sidebar: footer cell %d = %q, want %q: %q",
			fx+fw-sidebarToggleWidth, got, sidebarCollapseGlyph, string(row[:fw]))
	}
	if strings.ContainsRune(screenLine(scr, a.height-1), sidebarExpandGlyph) {
		t.Fatal("open sidebar must not paint the » in the status bar")
	}

	a.menuToggleSidebar()
	a.draw()
	a.screen.Show()
	if _, _, fw := a.sidebarFooterRect(); fw != 0 {
		t.Fatalf("collapsed: footer width = %d, want 0", fw)
	}
	status := []rune(screenLine(scr, a.height-1))
	if status[0] != sidebarExpandGlyph {
		t.Fatalf("collapsed sidebar: status bar starts %q, want %q", string(status[:8]), sidebarExpandGlyph)
	}
	if status[1] != ' ' {
		t.Fatalf("the » should be followed by the readout's own pad, got %q", string(status[:8]))
	}
	for y := 0; y < a.height-1; y++ {
		if strings.ContainsRune(screenLine(scr, y), sidebarCollapseGlyph) {
			t.Fatalf("collapsed sidebar must not paint « anywhere (row %d)", y)
		}
	}
}

// TestSidebarToggle_ClicksFlipThePanel is the mouse-first contract: a
// press on the « collapses the explorer, a press on the » brings it
// back, and each also retires any pending responsive restore exactly
// as the ≡ row does — the chevron is the same toggle, not a second one.
func TestSidebarToggle_ClicksFlipThePanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	fx, fy, fw := a.sidebarFooterRect()
	a.sidebarAutoHidden = true
	x := fx + fw - sidebarToggleWidth
	a.handleEvent(tcell.NewEventMouse(x, fy, tcell.Button1, 0))
	a.handleEvent(tcell.NewEventMouse(x, fy, tcell.ButtonNone, 0))
	if a.sidebarShown {
		t.Fatal("clicking « should collapse the sidebar")
	}
	if a.sidebarAutoHidden {
		t.Fatal("an explicit collapse must clear sidebarAutoHidden, like menuToggleSidebar")
	}

	a.draw()
	a.handleEvent(tcell.NewEventMouse(0, a.height-1, tcell.Button1, 0))
	a.handleEvent(tcell.NewEventMouse(0, a.height-1, tcell.ButtonNone, 0))
	if !a.sidebarShown {
		t.Fatal("clicking » should re-open the sidebar")
	}
}

// TestSidebarToggle_FooterIsNotATreeRow pins why the footer is taken
// out of sidebarRect rather than painted over the panel: with a tree
// taller than the sidebar, Tree.HitTest would answer the footer row
// with a real node, and a click beside the « would open a file the
// user never saw. The rest of the footer has to be inert.
func TestSidebarToggle_FooterIsNotATreeRow(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 12)
	seedTreeFiles(t, dir, 40)
	a.refreshTree()
	a.draw()
	fx, fy, _ := a.sidebarFooterRect()
	before := a.tabs.Len()
	a.handleEvent(tcell.NewEventMouse(fx+1, fy, tcell.Button1, 0))
	a.handleEvent(tcell.NewEventMouse(fx+1, fy, tcell.ButtonNone, 0))
	if a.tabs.Len() != before {
		t.Fatal("a click on the footer's blank cells opened a tree row painted nowhere")
	}
	if !a.sidebarShown {
		t.Fatal("the footer's blank cells must not toggle the sidebar")
	}
	if _, _, _, sh := a.sidebarRect(); sh != fy {
		t.Fatalf("sidebar height %d should end exactly at the footer row %d", sh, fy)
	}
}

// TestSidebarToggle_StatusTextYieldsToTheChevron: the » takes the
// status bar's first cell, so the left readout must start after it and
// be measured one cell shorter — otherwise the flash-strip decision
// ("would this be truncated?") and the paint would disagree by a cell.
func TestSidebarToggle_StatusTextYieldsToTheChevron(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	_, _, sw, _ := a.statusRect()
	open := a.statusLeftMax(sw)
	a.menuToggleSidebar()
	if got := a.statusLeftMax(sw); got != open-1 {
		t.Fatalf("collapsed statusLeftMax = %d, want %d (one cell for the »)", got, open-1)
	}
	a.flash("hello")
	a.draw()
	a.screen.Show()
	status := screenLine(a.screen.(tcell.SimulationScreen), a.height-1)
	if !strings.HasPrefix(status, "» hello") {
		t.Fatalf("status bar = %q, want the » then the flash", status)
	}
}

// TestSidebarToggle_NoneInSingleFileMode: a session without a tree has
// no sidebar to expand, so the » must not appear — it would be a
// control that only flashes a refusal — and the status text keeps its
// full width.
func TestSidebarToggle_NoneInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.sidebarShown = false
	if ex, ew := a.sidebarExpandRect(); ex != -1 || ew != 0 {
		t.Fatalf("single-file mode: expand slot = (%d,%d), want none", ex, ew)
	}
	if a.sidebarExpandHit(0) {
		t.Fatal("single-file mode: the status bar's first cell must not be a handle")
	}
	a.draw()
	a.screen.Show()
	if status := screenLine(a.screen.(tcell.SimulationScreen), a.height-1); strings.ContainsRune(status, sidebarExpandGlyph) {
		t.Fatalf("single-file status bar painted a »: %q", status)
	}
}
