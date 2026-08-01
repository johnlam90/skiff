// =============================================================================
// File: internal/editor/lineops_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the whole-line operations: move up / move down / duplicate.

package editor

import (
	"strings"
	"testing"
)

// linesTab builds a text tab around the given lines with undo seeded,
// mirroring what NewTab does without touching the filesystem.
func linesTab(lines ...string) *Tab {
	t := &Tab{Buffer: &Buffer{Lines: append([]string{}, lines...)}}
	t.initUndo()
	return t
}

// TestMoveLinesUpSingle pins the basic gesture: the cursor line swaps
// with the line above and the cursor rides along with it.
func TestMoveLinesUpSingle(t *testing.T) {
	tab := linesTab("a", "b", "c")
	tab.Cursor = Position{Line: 1}
	tab.Anchor = tab.Cursor

	tab.MoveLinesUp()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "b,a,c" {
		t.Fatalf("lines: got %q, want b,a,c", got)
	}
	if tab.Cursor.Line != 0 {
		t.Fatalf("cursor should follow the line up, got line %d", tab.Cursor.Line)
	}
	if !tab.Dirty {
		t.Fatal("moving a line must mark the tab dirty")
	}
}

// TestMoveLinesUpAtTopNoop / down-at-bottom: the edges are hard stops —
// no rotation, no dirty flag, no undo entry.
func TestMoveLinesUpAtTopNoop(t *testing.T) {
	tab := linesTab("a", "b")
	tab.MoveLinesUp()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b" {
		t.Fatalf("lines changed at top edge: %q", got)
	}
	if tab.Dirty {
		t.Fatal("edge no-op must not dirty the tab")
	}
	if tab.CanUndo() {
		t.Fatal("edge no-op must not push an undo entry")
	}
}

// TestMoveLinesDownAtBottomNoop is the mirror-image edge case.
func TestMoveLinesDownAtBottomNoop(t *testing.T) {
	tab := linesTab("a", "b")
	tab.Cursor = Position{Line: 1}
	tab.Anchor = tab.Cursor
	tab.MoveLinesDown()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b" {
		t.Fatalf("lines changed at bottom edge: %q", got)
	}
	if tab.Dirty {
		t.Fatal("edge no-op must not dirty the tab")
	}
}

// TestMoveLinesSelectionSpan verifies a multi-line selection moves as a
// block and the selection endpoints travel with it.
func TestMoveLinesSelectionSpan(t *testing.T) {
	tab := linesTab("a", "b", "c", "d")
	tab.Anchor = Position{Line: 1, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 1}

	tab.MoveLinesDown()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,d,b,c" {
		t.Fatalf("lines: got %q, want a,d,b,c", got)
	}
	if tab.Anchor.Line != 2 || tab.Cursor.Line != 3 {
		t.Fatalf("selection should travel: anchor %d cursor %d", tab.Anchor.Line, tab.Cursor.Line)
	}
}

// TestMoveLinesSelectionExcludesTrailingCol0 pins the VS-Code rule the
// comment toggle already uses: a selection ending at column 0 of a line
// does not drag that line along.
func TestMoveLinesSelectionExcludesTrailingCol0(t *testing.T) {
	tab := linesTab("a", "b", "c")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 1, Col: 0} // "selects" only line 0

	tab.MoveLinesDown()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "b,a,c" {
		t.Fatalf("lines: got %q, want b,a,c (only line 0 moves)", got)
	}
}

// TestDuplicateLinesCursorOnCopy: the span is copied directly below
// itself and the cursor lands on the copy, ready for immediate editing.
func TestDuplicateLinesCursorOnCopy(t *testing.T) {
	tab := linesTab("a", "b", "c")
	tab.Cursor = Position{Line: 1, Col: 1}
	tab.Anchor = tab.Cursor

	tab.DuplicateLines()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b,b,c" {
		t.Fatalf("lines: got %q, want a,b,b,c", got)
	}
	if tab.Cursor != (Position{Line: 2, Col: 1}) {
		t.Fatalf("cursor should land on the copy, got %+v", tab.Cursor)
	}
}

// TestLineOpsUndoOneStep pins the undo granularity: one gesture = one
// undo step, restoring both the text and the cursor.
func TestLineOpsUndoOneStep(t *testing.T) {
	tab := linesTab("a", "b", "c")
	tab.Cursor = Position{Line: 1}
	tab.Anchor = tab.Cursor

	tab.MoveLinesUp()
	if !tab.Undo() {
		t.Fatal("undo should have something to pop")
	}
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b,c" {
		t.Fatalf("undo left %q, want a,b,c", got)
	}
	if tab.Cursor.Line != 1 {
		t.Fatalf("undo should restore the cursor, got line %d", tab.Cursor.Line)
	}

	tab.DuplicateLines()
	tab.Undo()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b,c" {
		t.Fatalf("duplicate undo left %q, want a,b,c", got)
	}
}
