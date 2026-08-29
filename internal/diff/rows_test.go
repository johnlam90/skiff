// =============================================================================
// File: internal/diff/rows_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the side-by-side pairing. These moved out of the diff view
// with the logic: the pairing is now the same answer for a git patch and
// for a buffer-versus-disk diff, so it is worth pinning once here rather
// than through an overlay that also has to be drawn.

package diff

import (
	"strings"
	"testing"
)

// sample is the small captured `git diff` the pairing cases build on:
// one modification flanked by context, with git's full framing above it.
func sample(t *testing.T) Patch {
	t.Helper()
	return parsed(t,
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,3 @@",
		" ctx one",
		"-old line",
		"+new line",
		" ctx two",
	)
}

// TestRows_AlignsModification pins the core pairing rule: a deletion run
// and the following addition run pair off line-for-line onto shared
// rows, file headers are gone, and the hunk header keeps its own row.
func TestRows_AlignsModification(t *testing.T) {
	rows := Rows(sample(t))
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (hunk + ctx + change + ctx), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != RowHunk || !strings.HasPrefix(rows[0].Left, "@@") {
		t.Fatalf("row 0 should be the hunk header, got %+v", rows[0])
	}
	if rows[1].Kind != RowContext || rows[1].LeftNo != 1 || rows[1].RightNo != 1 || rows[1].Left != "ctx one" {
		t.Fatalf("row 1 context: %+v", rows[1])
	}
	ch := rows[2]
	if ch.Kind != RowChange || ch.LeftNo != 2 || ch.Left != "old line" || ch.RightNo != 2 || ch.Right != "new line" {
		t.Fatalf("row 2 should pair the modification, got %+v", ch)
	}
	if ch.LeftEmph != (Span{0, 3}) || ch.RightEmph != (Span{0, 3}) {
		t.Fatalf("paired row should carry its intra-line spans, got %+v/%+v", ch.LeftEmph, ch.RightEmph)
	}
	if rows[3].Kind != RowContext || rows[3].LeftNo != 3 || rows[3].RightNo != 3 {
		t.Fatalf("row 3 context: %+v", rows[3])
	}
}

// TestRows_OneSidedRuns verifies pure additions leave the left side
// blank (LeftNo 0) and pure deletions the right — the gaps that make
// insert/delete blocks visually obvious — and that a one-sided row
// carries no emphasis span, because the whole line is the change.
func TestRows_OneSidedRuns(t *testing.T) {
	rows := Rows(parsed(t, "@@ -1,2 +1,2 @@", "-gone", " kept", "+arrived"))
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d: %+v", len(rows), rows)
	}
	del := rows[1]
	if del.Kind != RowChange || del.LeftNo != 1 || del.Left != "gone" || del.RightNo != 0 {
		t.Fatalf("deletion row should be left-only, got %+v", del)
	}
	add := rows[3]
	if add.Kind != RowChange || add.RightNo != 2 || add.Right != "arrived" || add.LeftNo != 0 {
		t.Fatalf("addition row should be right-only, got %+v", add)
	}
	if add.RightEmph != (Span{}) {
		t.Fatalf("one-sided row should carry no span, got %+v", add.RightEmph)
	}
}

// TestRows_UnevenRunsAndSecondHunk covers a 2-del/1-add hunk (the
// leftover deletion gets its own left-only row) and checks line
// numbering restarts correctly at a second hunk header.
func TestRows_UnevenRunsAndSecondHunk(t *testing.T) {
	rows := Rows(parsed(t,
		"@@ -1,2 +1,1 @@",
		"-first",
		"-second",
		"+merged",
		"@@ -10,1 +9,1 @@",
		"-ten",
		"+nine",
	))
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d: %+v", len(rows), rows)
	}
	if rows[1].Left != "first" || rows[1].Right != "merged" {
		t.Fatalf("row 1 should pair first/merged, got %+v", rows[1])
	}
	if rows[2].Left != "second" || rows[2].RightNo != 0 {
		t.Fatalf("row 2 should be the leftover deletion, got %+v", rows[2])
	}
	last := rows[4]
	if last.LeftNo != 10 || last.RightNo != 9 {
		t.Fatalf("second hunk numbering: got left %d right %d, want 10/9", last.LeftNo, last.RightNo)
	}
}

// TestRows_PairingStopsAtTheHunkBoundary keeps a deletion at the end of
// one hunk from being paired with an addition at the start of the next:
// they are different parts of the file, and pairing them would invent a
// modification nobody made.
func TestRows_PairingStopsAtTheHunkBoundary(t *testing.T) {
	rows := Rows(parsed(t,
		"@@ -1,1 +1,0 @@",
		"-dropped",
		"@@ -9,0 +9,1 @@",
		"+appended",
	))
	if len(rows) != 4 {
		t.Fatalf("expected two hunk rows and two one-sided change rows, got %+v", rows)
	}
	if rows[1].RightNo != 0 || rows[3].LeftNo != 0 {
		t.Fatalf("the two changes must stay on their own rows: %+v / %+v", rows[1], rows[3])
	}
}

// TestRows_MultiFileBoundaries pins commit-diff support: each file
// becomes a boundary row and hunk numbering resets per file — and, the
// flip side, a single-file patch keeps no boundary row at all, because
// the diff view's title already names the file.
func TestRows_MultiFileBoundaries(t *testing.T) {
	rows := Rows(parsed(t,
		"diff --git a/one.go b/one.go",
		"index 111..222 100644",
		"--- a/one.go",
		"+++ b/one.go",
		"@@ -1,1 +1,1 @@",
		"-alpha",
		"+ALPHA",
		"diff --git a/two.go b/two.go",
		"index 333..444 100644",
		"--- a/two.go",
		"+++ b/two.go",
		"@@ -5,1 +5,1 @@",
		"-beta",
		"+BETA",
	))
	// file, hunk, change, file, hunk, change.
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != RowFile || rows[0].Left != "one.go" {
		t.Fatalf("row 0 should announce one.go, got %+v", rows[0])
	}
	if rows[3].Kind != RowFile || rows[3].Left != "two.go" {
		t.Fatalf("row 3 should announce two.go, got %+v", rows[3])
	}
	if rows[5].LeftNo != 5 || rows[5].RightNo != 5 {
		t.Fatalf("numbering should reset per file, got %+v", rows[5])
	}
	for _, r := range Rows(sample(t)) {
		if r.Kind == RowFile {
			t.Fatal("single-file patches must not carry a boundary row")
		}
	}
}

// TestChangedSpans_LocatesChangedRunes pins the intra-line diff math:
// common prefix and suffix are excluded, and what remains is the span
// that gets the word-level highlight.
func TestChangedSpans_LocatesChangedRunes(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		oldSpan  Span
		newSpan  Span
	}{
		{"middle word", "println(\"two\")", "println(\"TWO\")", Span{9, 12}, Span{9, 12}},
		{"pure insertion", "ab", "aXb", Span{1, 1}, Span{1, 2}},
		{"pure removal", "aXb", "ab", Span{1, 2}, Span{1, 1}},
		{"whole line", "abc", "xyz", Span{0, 3}, Span{0, 3}},
		{"identical", "same", "same", Span{4, 4}, Span{4, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, n := changedSpans([]rune(tt.old), []rune(tt.new))
			if o != tt.oldSpan || n != tt.newSpan {
				t.Fatalf("got old %+v new %+v, want %+v / %+v", o, n, tt.oldSpan, tt.newSpan)
			}
		})
	}
}

// TestFirstChanged_PrefersRightNoFallsBackToLeftNo pins the jump target
// a diff hands its "open the file" button: the new-file line where there
// is one, the old-file line for a pure deletion, and zero — no jump —
// when nothing changed at all.
func TestFirstChanged_PrefersRightNoFallsBackToLeftNo(t *testing.T) {
	if got := FirstChanged(Rows(sample(t))); got != 2 {
		t.Fatalf("modification should jump to the new line 2, got %d", got)
	}
	deletion := []Row{
		{Kind: RowHunk, Left: "@@"},
		{Kind: RowContext, LeftNo: 1, RightNo: 1},
		{Kind: RowChange, LeftNo: 7},
	}
	if got := FirstChanged(deletion); got != 7 {
		t.Fatalf("a pure deletion should fall back to the old line, got %d", got)
	}
	if got := FirstChanged([]Row{{Kind: RowContext, LeftNo: 4, RightNo: 4}}); got != 0 {
		t.Fatalf("no change row means no jump target, got %d", got)
	}
}
