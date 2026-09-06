// =============================================================================
// File: internal/editor/select_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for select.go — the select-all and select-line gestures.

package editor

import "testing"

// TestSelectAll pins the whole-buffer selection: anchor at the origin,
// caret past the last rune, the text is everything, and the gesture is
// a view change only — no undo entry, no dirty flag.
func TestSelectAll(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab\ncd")}
	tab.initUndo()
	tab.Cursor = Position{Line: 1, Col: 1}
	tab.Anchor = tab.Cursor
	tab.SelectAll()
	if tab.Anchor != (Position{}) || tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("SelectAll: anchor %+v cursor %+v", tab.Anchor, tab.Cursor)
	}
	if got := tab.SelectionText(); got != "ab\ncd" {
		t.Fatalf("selection = %q", got)
	}
	if tab.Dirty || tab.CanUndo() {
		t.Fatal("a selection must not record an edit")
	}
	if !tab.cursorMoved {
		t.Fatal("the caret moved; the view has to follow")
	}
}

// TestSelectLine pins the line selection: the newline rides along so a
// Cut removes the line, the last line stops at its own end, and the
// caret's column does not matter.
func TestSelectLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab\ncd\nef")}
	tab.initUndo()
	tab.Cursor = Position{Line: 1, Col: 1}
	tab.Anchor = tab.Cursor
	tab.SelectLine()
	if got := tab.SelectionText(); got != "cd\n" {
		t.Fatalf("middle line selection = %q, want it to carry the newline", got)
	}
	tab.DeleteSelection()
	if got := tab.Buffer.String(); got != "ab\nef" {
		t.Fatalf("cutting a line selection should remove the whole line, got %q", got)
	}

	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.SelectLine()
	if got := tab.SelectionText(); got != "ef" {
		t.Fatalf("last line selection = %q", got)
	}
}

// TestSelect_ImageTabsAreInert keeps both gestures away from image
// previews, which have no text and no caret.
func TestSelect_ImageTabsAreInert(t *testing.T) {
	img := &Tab{Buffer: NewBuffer(""), Mode: imageMode}
	img.SelectAll()
	img.SelectLine()
	if img.HasSelection() || img.cursorMoved {
		t.Fatal("image tabs must ignore selection gestures")
	}
}
