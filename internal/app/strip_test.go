// =============================================================================
// File: internal/app/strip_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/finder"
)

// stubStrip is a strip that does nothing but record what it was handed.
// It reserves a row count no real bar uses (three), so a layout that
// went back to charging findBarHeight instead of asking rows() fails
// here rather than silently agreeing with the find bar.
type stubStrip struct {
	a *App

	nRows   int
	consume bool

	keys        int
	drawnAs     rect
	closed      int
	slotAtClose strip
}

// rows reserves the stub's row count.
func (s *stubStrip) rows() int { return s.nRows }

// handleKey counts the keystrokes the router sent this strip.
func (s *stubStrip) handleKey(*tcell.EventKey) { s.keys++ }

// handleMouse answers whatever the test asked it to, which is the whole
// pass-through / capture switch mouse.go branches on.
func (s *stubStrip) handleMouse(int, int, tcell.ButtonMask) bool { return s.consume }

// draw records the rect layout reserved, so a test can compare it with
// stripRect rather than with a hand-written row number.
func (s *stubStrip) draw(r rect) { s.drawnAs = r }

// close records that the teardown hook ran, and what the slot held when
// it did — dropStrip promises the slot is already empty by then.
func (s *stubStrip) close() {
	s.closed++
	s.slotAtClose = s.a.strip
}

// TestStripSlot_ReservesItsRowsFromTheEditor pins the layout contract
// the whole interface exists for: the editor gives up exactly rows()
// rows, the strip's rect is the band between the editor and the status
// bar, and stripRowBudget charges the same amount. The three used to be
// three copies of "subtract findBarHeight if either bar is open", which
// only stayed in step because nobody had added a third strip.
func TestStripSlot_ReservesItsRowsFromTheEditor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	_, _, _, closedH := a.editorRect()
	closedBudget := a.stripRowBudget()

	s := &stubStrip{a: a, nRows: 3}
	a.strip = s

	_, ey, _, eh := a.editorRect()
	if eh != closedH-s.rows() {
		t.Fatalf("editor height %d, want %d (shrunk by rows()=%d)", eh, closedH-s.rows(), s.rows())
	}
	if got := a.stripRowBudget(); got != closedBudget-s.rows() {
		t.Fatalf("strip budget %d, want %d", got, closedBudget-s.rows())
	}
	r := a.stripRect()
	if r.h != s.rows() || ey+eh != r.y || r.y+r.h != a.height-1 {
		t.Fatalf("strip rect %+v does not sit between the editor (ends %d) and the status bar (%d)",
			r, ey+eh, a.height-1)
	}
	if r.x != a.sidebarW() || r.w != a.width-a.sidebarW() {
		t.Fatalf("strip rect %+v is not aligned with the editor column", r)
	}
}

// TestStripSlot_DrawsIntoTheRectLayoutReserved pins the other half of
// that contract: the draw pass hands the strip its own rect, so no bar
// has to re-derive the row it lives on.
func TestStripSlot_DrawsIntoTheRectLayoutReserved(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := &stubStrip{a: a, nRows: 2}
	a.strip = s

	a.draw()

	if want := a.stripRect(); s.drawnAs != want {
		t.Fatalf("strip drawn into %+v, want the reserved %+v", s.drawnAs, want)
	}
}

// TestStripSlot_OwnsTheKeyboard pins the routing order: while a strip is
// up it takes every key, so nothing types into the buffer underneath it.
func TestStripSlot_OwnsTheKeyboard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFile(t, a, a.rootDir, "k.go", "package p\n")
	s := &stubStrip{a: a, nRows: 1}
	a.strip = s

	a.handleKey(keyEv(tcell.KeyRune, 'x'))

	if s.keys != 1 {
		t.Fatalf("strip saw %d keys, want 1", s.keys)
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "package p" {
		t.Fatalf("the key reached the buffer: %q", got)
	}
}

// TestStripSlot_MouseRoutingHonoursTheAnswer pins the seam itself:
// mouse.go branches on what handleMouse returned, not on which strip is
// up. true stops the event at the strip; false lets it fall through to
// the editor, which is ADR-0001's pass-through expressed as a value the
// adapter chooses.
func TestStripSlot_MouseRoutingHonoursTheAnswer(t *testing.T) {
	for _, consume := range []bool{true, false} {
		a := newTestApp(t, t.TempDir())
		openTestFile(t, a, a.rootDir, "route.go", "package p\nsecond line\nthird line\n")
		a.strip = &stubStrip{a: a, nRows: 1, consume: consume}
		before := a.activeTabPtr().Cursor

		ex, ey, _, _ := a.editorRect()
		a.handleMouse(tcell.NewEventMouse(ex+2, ey+2, tcell.Button1, tcell.ModNone))

		moved := a.activeTabPtr().Cursor != before
		if moved == consume {
			t.Fatalf("consume=%v: editor %s the click", consume,
				map[bool]string{true: "took", false: "never saw"}[moved])
		}
	}
}

// TestStripPassThrough_LetsAClickReachTheEditor drives the real find bar
// through the public mouse entry point: its handleMouse answers false,
// so a press inside the editor still places the caret. This is
// ADR-0001's pass-through — the user keeps clicking and drag-selecting
// while the bar is open — and it is now a property of the adapter rather
// than an absent branch in mouse.go.
func TestStripPassThrough_LetsAClickReachTheEditor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFile(t, a, a.rootDir, "click.go", "package p\nsecond line\nthird line\n")
	a.openFind()
	if !a.findBarOpen() {
		t.Fatalf("precondition: the find bar should own the slot, got %T", a.strip)
	}

	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+2, ey+2, tcell.Button1, tcell.ModNone))

	// The row is what proves the press was hit-tested against the editor
	// rect; the column depends on the line-number gutter's width, which
	// this test has no business pinning.
	if got := a.activeTabPtr().Cursor; got.Line != 2 {
		t.Fatalf("the click never reached the editor: cursor %+v", got)
	}
	if !a.findBarOpen() {
		t.Fatal("a pass-through click must not dismiss the strip")
	}
}

// TestStripCapture_KeepsTheClickOffTheEditor is the other answer: the
// project-find panel consumes mouse events (it has real targets — mode
// chips, fold arrows, match rows), so the same press must not move the
// caret underneath its results.
func TestStripCapture_KeepsTheClickOffTheEditor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFile(t, a, a.rootDir, "click.go", "package p\nsecond line\nthird line\n")
	a.finder = finder.New(a.rootDir)
	a.openProjFind()
	if !a.projFindOpen() {
		t.Fatalf("precondition: the panel should own the slot, got %T", a.strip)
	}
	before := a.activeTabPtr().Cursor

	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+2, ey+2, tcell.Button1, tcell.ModNone))

	if got := a.activeTabPtr().Cursor; got != before {
		t.Fatalf("a captured click reached the editor: cursor %+v, was %+v", got, before)
	}
}

// TestCloseAllModals_EmptiesTheStripSlot pins the teardown: one slot to
// clear, and the occupant's close hook runs after it is empty (the same
// pop-then-hook order the overlay stack uses), so a hook that opens the
// next surface cannot be undone by the teardown that preceded it.
func TestCloseAllModals_EmptiesTheStripSlot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := &stubStrip{a: a, nRows: 1}
	a.strip = s

	a.closeAllModals()

	if a.strip != nil {
		t.Fatalf("closeAllModals left %T in the slot", a.strip)
	}
	if s.closed != 1 {
		t.Fatalf("close hook ran %d times, want 1", s.closed)
	}
	if s.slotAtClose != nil {
		t.Fatalf("the slot still held %T when close ran", s.slotAtClose)
	}
	// A second sweep has nothing to tear down and must not re-run the
	// hook against a strip that is already gone.
	a.closeAllModals()
	if s.closed != 1 {
		t.Fatalf("close hook ran again on an empty slot: %d", s.closed)
	}
}

// TestStripSlot_HoldsOneStripAtATime pins the mutual exclusion the row
// arithmetic depends on: layout subtracts one strip's rows, and that is
// only right because opening either bar runs closeAllModals first. The
// bar that loses the slot is torn down properly on the way out — the
// find bar's match highlights go with it.
func TestStripSlot_HoldsOneStripAtATime(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFile(t, a, a.rootDir, "excl.go", "package p\n")
	a.finder = finder.New(a.rootDir)

	a.openFind()
	for _, r := range "package" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	if len(a.activeTabPtr().FindMatches) == 0 {
		t.Fatal("precondition: the query should have matched")
	}

	a.openProjFind()

	if !a.projFindOpen() || a.findBarOpen() {
		t.Fatalf("the slot should hold the panel alone, got %T", a.strip)
	}
	if tab := a.activeTabPtr(); tab.FindQuery != "" || tab.FindMatches != nil {
		t.Fatalf("the displaced find bar left its highlights behind: %q", tab.FindQuery)
	}
}

// TestStripRect_IsTheOneRowTheBarsPaintOn pins the single row formula
// against both real bars: each paints its label on stripRect().y. They
// used to compute that row three separate ways — twice for drawing,
// once for the project-find hit-test — so a change to one of them moved
// a bar out from under its own clicks.
func TestStripRect_IsTheOneRowTheBarsPaintOn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFile(t, a, a.rootDir, "row.go", "package p\n")
	a.finder = finder.New(a.rootDir)
	scr := a.screen.(tcell.SimulationScreen)

	a.openFind()
	a.draw()
	scr.Show()
	if row := screenLine(scr, a.stripRect().y); runeIndexOf([]rune(row), "Find:") < 0 {
		t.Fatalf("the find bar is not on its reserved row: %q", row)
	}

	a.openProjFind()
	a.draw()
	scr.Show()
	if row := screenLine(scr, a.stripRect().y); runeIndexOf([]rune(row), "Search project:") < 0 {
		t.Fatalf("the project-find bar is not on its reserved row: %q", row)
	}
}
