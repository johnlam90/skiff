// =============================================================================
// File: internal/editor/lineops_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
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

// TestIndentLines_ShiftsBlockAndKeepsSelection pins the Tab-over-a-
// selection gesture: every selected line gains one IndentUnit, the
// selection still covers the same lines afterwards, and the whole
// block is one undo step — a Tab that replaced forty lines with four
// spaces was the bug this exists to fix.
func TestIndentLines_ShiftsBlockAndKeepsSelection(t *testing.T) {
	tab := linesTab("a", "b", "c", "d")
	tab.IndentUnit = "  "
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 1}

	if !tab.IndentLines() {
		t.Fatal("IndentLines reported no change")
	}
	if got := strings.Join(tab.Buffer.Lines, ","); got != "  a,  b,  c,d" {
		t.Fatalf("lines: got %q, want   a,  b,  c,d", got)
	}
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor at column 0 must stay put, got %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 2, Col: 3}) {
		t.Fatalf("cursor should ride the text right, got %+v", tab.Cursor)
	}
	if !tab.Dirty {
		t.Fatal("indenting must mark the tab dirty")
	}
	if !tab.Undo() || strings.Join(tab.Buffer.Lines, ",") != "a,b,c,d" {
		t.Fatalf("one undo should restore the block, got %v", tab.Buffer.Lines)
	}
}

// TestIndentLines_SkipsBlankLinesInsideABlock keeps a block indent from
// manufacturing whitespace-only lines, while a lone blank line — the
// only thing the gesture could mean there — is still indented.
func TestIndentLines_SkipsBlankLinesInsideABlock(t *testing.T) {
	tab := linesTab("a", "", "b")
	tab.IndentUnit = "\t"
	tab.Anchor = Position{Line: 0}
	tab.Cursor = Position{Line: 2, Col: 1}
	tab.IndentLines()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "\ta,,\tb" {
		t.Fatalf("lines: got %q", got)
	}

	solo := linesTab("")
	solo.IndentUnit = "\t"
	if !solo.IndentLines() || solo.Buffer.Lines[0] != "\t" {
		t.Fatalf("a lone blank line should still indent, got %q", solo.Buffer.Lines[0])
	}
}

// TestIndentLines_SelectionEndingAtColumnZeroExcludesThatLine shares
// the comment toggle's range rule: a selection that ends at column 0 of
// a line did not "select" that line, so it is left alone.
func TestIndentLines_SelectionEndingAtColumnZeroExcludesThatLine(t *testing.T) {
	tab := linesTab("a", "b", "c")
	tab.IndentUnit = "  "
	tab.Anchor = Position{Line: 0}
	tab.Cursor = Position{Line: 2, Col: 0}
	tab.IndentLines()
	if got := strings.Join(tab.Buffer.Lines, ","); got != "  a,  b,c" {
		t.Fatalf("lines: got %q", got)
	}
	if tab.Cursor != (Position{Line: 2, Col: 0}) {
		t.Fatalf("cursor on the excluded line must not move, got %+v", tab.Cursor)
	}
}

// TestOutdentLines_RemovesOneLevelPerLine pins the removal ladder: a
// whole unit when the line starts with one, a lone tab otherwise, and
// a short run of spaces capped at the unit's width — so a block with
// mixed indentation still de-dents one level per press.
func TestOutdentLines_RemovesOneLevelPerLine(t *testing.T) {
	tab := linesTab("    a", "\tb", "  c", "d", "      e")
	tab.IndentUnit = "    "
	tab.Anchor = Position{Line: 0, Col: 4}
	tab.Cursor = Position{Line: 4, Col: 7}

	if !tab.OutdentLines() {
		t.Fatal("OutdentLines reported no change")
	}
	if got := strings.Join(tab.Buffer.Lines, ","); got != "a,b,c,d,  e" {
		t.Fatalf("lines: got %q", got)
	}
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor should follow the text left, got %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 4, Col: 3}) {
		t.Fatalf("cursor should follow the text left, got %+v", tab.Cursor)
	}
	if !tab.Undo() || tab.Buffer.Lines[0] != "    a" {
		t.Fatalf("one undo should restore the block, got %v", tab.Buffer.Lines)
	}
}

// TestOutdentLines_NothingToRemoveRecordsNoUndo keeps the no-op guard
// honest: reaching edit always records history, so a block with no
// indentation must return false without touching the stack or Dirty.
func TestOutdentLines_NothingToRemoveRecordsNoUndo(t *testing.T) {
	tab := linesTab("a", "b")
	tab.IndentUnit = "    "
	if tab.OutdentLines() {
		t.Fatal("OutdentLines on flush-left lines should report no change")
	}
	if tab.Dirty || tab.CanUndo() {
		t.Fatal("a no-op outdent must leave Dirty and the undo stack alone")
	}
}

// TestOutdentLines_TabUnitTakesUpToATabStopOfSpaces covers the tab-unit
// case: one space per press would be a de-dent nobody can see, so a
// space-indented line under a tab unit loses up to TabStop spaces.
func TestOutdentLines_TabUnitTakesUpToATabStopOfSpaces(t *testing.T) {
	tab := linesTab("      x")
	tab.IndentUnit = "\t"
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.OutdentLines()
	if got := tab.Buffer.Lines[0]; got != "  x" {
		t.Fatalf("line: got %q, want two spaces left", got)
	}
	if tab.Cursor.Col != 0 {
		t.Fatalf("cursor must clamp at column 0, got %d", tab.Cursor.Col)
	}
}

// TestOutdentWidth pins the per-line ladder directly.
func TestOutdentWidth(t *testing.T) {
	cases := []struct {
		line, unit string
		want       int
	}{
		{"    a", "    ", 4},
		{"  a", "    ", 2},
		{"\ta", "    ", 1},
		{"\t\ta", "\t", 1},
		{"   a", "\t", 3},
		{"        a", "\t", TabStop},
		{"a", "    ", 0},
		{"", "    ", 0},
	}
	for _, c := range cases {
		if got := outdentWidth(c.line, c.unit); got != c.want {
			t.Errorf("outdentWidth(%q, %q) = %d, want %d", c.line, c.unit, got, c.want)
		}
	}
}
