// =============================================================================
// File: internal/editor/word_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import "testing"

// TestIsWordChar pins the shared word predicate: identifiers (including
// snake_case and digits) are word runes, everything a programmer would
// call a separator is not. Double-click selection and caret motion both
// route through this, so a change here is a change to both.
func TestIsWordChar(t *testing.T) {
	for _, r := range "abzABZ_019" {
		if !IsWordChar(r) {
			t.Errorf("IsWordChar(%q) = false, want true", r)
		}
	}
	for _, r := range []rune{' ', '\t', '.', ',', '-', '!', '\n', '/', '(', ')', '"'} {
		if IsWordChar(r) {
			t.Errorf("IsWordChar(%q) = true, want false", r)
		}
	}
}

// TestWordLeftRight_StopsAtBoundaries walks a line of mixed identifiers and
// punctuation and checks that each motion lands on a word edge rather than
// somewhere inside a run.
func TestWordLeftRight_StopsAtBoundaries(t *testing.T) {
	runes := []rune("foo.bar_baz(  qux )")
	// index:        0  3   7   11 14  18

	tests := []struct {
		name      string
		from      int
		wantLeft  int
		wantRight int
	}{
		{"start of line", 0, 0, 3},          // right: one past "foo"
		{"inside first word", 2, 0, 3},      // left: back to "foo" start
		{"on the dot", 3, 0, 11},            // right skips '.', ends "bar_baz"
		{"inside snake word", 6, 4, 11},     // underscore does not split
		{"after the paren", 12, 4, 17},      // right crosses the spaces to "qux"
		{"end of line", 19, 14, 19},         // left: start of "qux"
		{"past end is clamped", 40, 14, 19}, // col beyond the line
		{"negative is clamped", -1, 0, 3},   // col before the line
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WordLeft(runes, tc.from); got != tc.wantLeft {
				t.Errorf("WordLeft(%d) = %d, want %d", tc.from, got, tc.wantLeft)
			}
			if got := WordRight(runes, tc.from); got != tc.wantRight {
				t.Errorf("WordRight(%d) = %d, want %d", tc.from, got, tc.wantRight)
			}
		})
	}
}

// TestWordMotion_AgreesWithSelectionBoundaries is the invariant that
// justifies sharing one predicate: for every caret position inside a word,
// a leftward motion must land exactly where double-click selection starts
// that word, and a rightward motion exactly where it ends it. If the two
// ever drift, this fails.
func TestWordMotion_AgreesWithSelectionBoundaries(t *testing.T) {
	for _, line := range []string{
		"foo.bar_baz(  qux )",
		"a b  c",
		"__init__(self, *args)",
		"no_punctuation_at_all",
		"   leading and trailing   ",
		"",
	} {
		runes := []rune(line)
		for col := 0; col <= len(runes); col++ {
			start, end, ok := WordRangeAt(runes, col)
			if !ok {
				continue
			}
			if col > start {
				if got := WordLeft(runes, col); got != start {
					t.Fatalf("%q col %d: WordLeft = %d, selection starts the word at %d",
						line, col, got, start)
				}
			}
			if col < end {
				if got := WordRight(runes, col); got != end {
					t.Fatalf("%q col %d: WordRight = %d, selection ends the word at %d",
						line, col, got, end)
				}
			}
		}
	}
}

// TestWordRangeAt_MissesNonWordCells checks the "select nothing" contract:
// a caret with no word rune on either side must report ok=false instead of
// returning an empty range the caller would happily install as a selection.
// A caret immediately AFTER a word still resolves to that word — that is
// where a double-click on the token's right edge lands, and it is the
// behavior the app's double-click handler has always had.
func TestWordRangeAt_MissesNonWordCells(t *testing.T) {
	runes := []rune("ab  cd")
	if _, _, ok := WordRangeAt(runes, 3); ok {
		t.Error("caret between two spaces reported a word")
	}
	if s, e, ok := WordRangeAt(runes, 2); !ok || s != 0 || e != 2 {
		t.Errorf("caret just after \"ab\": got (%d,%d,%v), want (0,2,true)", s, e, ok)
	}
	if s, e, ok := WordRangeAt(runes, 6); !ok || s != 4 || e != 6 {
		t.Errorf("end-of-line caret: got (%d,%d,%v), want (4,6,true)", s, e, ok)
	}
	if _, _, ok := WordRangeAt([]rune("   "), 1); ok {
		t.Error("all-whitespace line reported a word")
	}
}

// TestSelectWordAt_MatchesDoubleClickContract keeps the behavior the app's
// double-click handler used to own: a word gets anchored and selected, an
// empty line and a whitespace click leave the selection untouched.
func TestSelectWordAt_MatchesDoubleClickContract(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world\n\n  x")}

	tab.SelectWordAt(Position{Line: 0, Col: 2})
	if tab.Anchor != (Position{Line: 0, Col: 0}) || tab.Cursor != (Position{Line: 0, Col: 5}) {
		t.Fatalf("word select: anchor=%v cursor=%v", tab.Anchor, tab.Cursor)
	}

	// Empty line: no panic, no change.
	anchor, cursor := tab.Anchor, tab.Cursor
	tab.SelectWordAt(Position{Line: 1, Col: 0})
	if tab.Anchor != anchor || tab.Cursor != cursor {
		t.Fatal("empty line moved the selection")
	}

	// Whitespace click: no change.
	tab.SelectWordAt(Position{Line: 2, Col: 0})
	if tab.Anchor != anchor || tab.Cursor != cursor {
		t.Fatal("whitespace click moved the selection")
	}
}

// TestMoveWordLeftRight_WrapsAtLineEdges pins the line-boundary rule: one
// motion crosses one boundary and stops, so the gesture stays predictable
// at the top and bottom of a line instead of skipping a whole line's worth
// of words in a single press.
func TestMoveWordLeftRight_WrapsAtLineEdges(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("alpha beta\ngamma")}

	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.MoveWordLeft(false)
	if want := (Position{Line: 0, Col: 10}); tab.Cursor != want {
		t.Fatalf("wrap left: cursor = %v, want %v", tab.Cursor, want)
	}

	tab.MoveWordRight(false)
	if want := (Position{Line: 1, Col: 0}); tab.Cursor != want {
		t.Fatalf("wrap right: cursor = %v, want %v", tab.Cursor, want)
	}

	// Start of buffer and end of buffer are hard stops.
	tab.Cursor = Position{}
	tab.Anchor = tab.Cursor
	tab.MoveWordLeft(false)
	if tab.Cursor != (Position{}) {
		t.Fatalf("left at buffer start moved to %v", tab.Cursor)
	}
	tab.Cursor = Position{Line: 1, Col: 5}
	tab.Anchor = tab.Cursor
	tab.MoveWordRight(false)
	if want := (Position{Line: 1, Col: 5}); tab.Cursor != want {
		t.Fatalf("right at buffer end moved to %v", tab.Cursor)
	}
}

// TestMoveWord_ExtendKeepsAnchor checks shift-extends-selection: the anchor
// stays put while the caret walks, and a plain motion collapses it.
func TestMoveWord_ExtendKeepsAnchor(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("alpha beta gamma")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.MoveWordRight(true)
	tab.MoveWordRight(true)
	if tab.Anchor != (Position{}) {
		t.Fatalf("extend moved the anchor to %v", tab.Anchor)
	}
	if got := tab.SelectionText(); got != "alpha beta" {
		t.Fatalf("selection = %q, want %q", got, "alpha beta")
	}

	tab.MoveWordLeft(false)
	if tab.HasSelection() {
		t.Fatal("plain motion left a selection behind")
	}
}

// TestMoveWord_IgnoresImageTabs keeps caret motion off read-only image
// previews, which have no text buffer to walk.
func TestMoveWord_IgnoresImageTabs(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("alpha beta"), Mode: imageMode}
	tab.MoveWordRight(false)
	tab.MoveWordLeft(false)
	if tab.Cursor != (Position{}) {
		t.Fatalf("image tab caret moved to %v", tab.Cursor)
	}
}
