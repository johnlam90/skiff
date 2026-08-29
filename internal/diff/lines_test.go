// =============================================================================
// File: internal/diff/lines_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the buffer-versus-disk constructor. The first one is the
// safety net for the deletion it replaced: skiff used to render this
// alignment as git-diff text and parse it straight back, so the rows the
// old round trip produced are the contract the direct construction has
// to keep.

package diff

import (
	"reflect"
	"testing"
)

// TestLines_MatchesTheOldTextRoundTrip pins Lines against the rows the
// deleted pipeline produced. Every expectation below was captured from
// the old code — unifiedDiff → parseSideBySideDiff → annotateDiffSpans —
// running on the disk-conflict tests' own fixtures, so a regression in
// the alignment, the context grouping, the hunk ranges or the pairing
// shows up as a row that no longer matches what users used to see.
func TestLines_MatchesTheOldTextRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		old, new []string
		want     []Row
	}{
		{
			name: "identical sides diff to nothing",
			old:  []string{"a", "b"},
			new:  []string{"a", "b"},
			want: nil,
		},
		{
			name: "one changed line keeps three lines of context",
			old:  []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
			new:  []string{"1", "2", "3", "4", "CHANGED", "6", "7", "8", "9"},
			want: []Row{
				{Kind: RowHunk, Left: "@@ -2,7 +2,7 @@"},
				{Kind: RowContext, LeftNo: 2, RightNo: 2, Left: "2", Right: "2"},
				{Kind: RowContext, LeftNo: 3, RightNo: 3, Left: "3", Right: "3"},
				{Kind: RowContext, LeftNo: 4, RightNo: 4, Left: "4", Right: "4"},
				{Kind: RowChange, LeftNo: 5, RightNo: 5, Left: "5", Right: "CHANGED",
					LeftEmph: Span{0, 1}, RightEmph: Span{0, 7}},
				{Kind: RowContext, LeftNo: 6, RightNo: 6, Left: "6", Right: "6"},
				{Kind: RowContext, LeftNo: 7, RightNo: 7, Left: "7", Right: "7"},
				{Kind: RowContext, LeftNo: 8, RightNo: 8, Left: "8", Right: "8"},
			},
		},
		{
			name: "pure insertion leaves the left side blank",
			old:  []string{"a"},
			new:  []string{"a", "b"},
			want: []Row{
				{Kind: RowHunk, Left: "@@ -1,1 +1,2 @@"},
				{Kind: RowContext, LeftNo: 1, RightNo: 1, Left: "a", Right: "a"},
				{Kind: RowChange, RightNo: 2, Right: "b"},
			},
		},
		{
			name: "pure deletion leaves the right side blank",
			old:  []string{"a", "b"},
			new:  []string{"a"},
			want: []Row{
				{Kind: RowHunk, Left: "@@ -1,2 +1,1 @@"},
				{Kind: RowContext, LeftNo: 1, RightNo: 1, Left: "a", Right: "a"},
				{Kind: RowChange, LeftNo: 2, Left: "b"},
			},
		},
		{
			name: "emptying a file reports a zero-length new side",
			old:  []string{"a"},
			new:  nil,
			want: []Row{
				{Kind: RowHunk, Left: "@@ -1,1 +0,0 @@"},
				{Kind: RowChange, LeftNo: 1, Left: "a"},
			},
		},
		{
			name: "the disk-conflict fixture pairs the modified line",
			old:  []string{"one", "THEIRS", "three"},
			new:  []string{"one", "MINE", "three"},
			want: []Row{
				{Kind: RowHunk, Left: "@@ -1,3 +1,3 @@"},
				{Kind: RowContext, LeftNo: 1, RightNo: 1, Left: "one", Right: "one"},
				{Kind: RowChange, LeftNo: 2, RightNo: 2, Left: "THEIRS", Right: "MINE",
					LeftEmph: Span{0, 6}, RightEmph: Span{0, 4}},
				{Kind: RowContext, LeftNo: 3, RightNo: 3, Left: "three", Right: "three"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rows(Patch{Files: []File{Lines(tt.old, tt.new)}})
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rows =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// TestLines_DistantChangesGetSeparateHunks is the other half of the
// round trip's contract: two edits far apart must not drag the whole
// file in as one hunk of context. The expected rows are again what the
// old text pipeline produced, numbering included.
func TestLines_DistantChangesGetSeparateHunks(t *testing.T) {
	old := make([]string, 40)
	for i := range old {
		old[i] = "line"
	}
	new := append([]string(nil), old...)
	new[2] = "top"
	new[37] = "bottom"

	want := []Row{
		{Kind: RowHunk, Left: "@@ -1,7 +1,6 @@"},
		{Kind: RowContext, LeftNo: 1, RightNo: 1, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 2, RightNo: 2, Left: "line", Right: "line"},
		{Kind: RowChange, LeftNo: 3, RightNo: 3, Left: "line", Right: "top", LeftEmph: Span{0, 4}, RightEmph: Span{0, 3}},
		{Kind: RowChange, LeftNo: 4, Left: "line"},
		{Kind: RowContext, LeftNo: 5, RightNo: 4, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 6, RightNo: 5, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 7, RightNo: 6, Left: "line", Right: "line"},
		{Kind: RowHunk, Left: "@@ -36,5 +35,6 @@"},
		{Kind: RowContext, LeftNo: 36, RightNo: 35, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 37, RightNo: 36, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 38, RightNo: 37, Left: "line", Right: "line"},
		{Kind: RowChange, RightNo: 38, Right: "bottom"},
		{Kind: RowContext, LeftNo: 39, RightNo: 39, Left: "line", Right: "line"},
		{Kind: RowContext, LeftNo: 40, RightNo: 40, Left: "line", Right: "line"},
	}
	got := Rows(Patch{Files: []File{Lines(old, new)}})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows =\n%+v\nwant\n%+v", got, want)
	}
}

// TestLines_IdenticalInputHasNoHunks pins the early-out the disk-conflict
// prompt reads: a file touched but not changed must diff to nothing, so
// the prompt can resolve itself instead of opening an empty diff view.
func TestLines_IdenticalInputHasNoHunks(t *testing.T) {
	f := Lines([]string{"same"}, []string{"same"})
	if len(f.Hunks) != 0 {
		t.Fatalf("identical sides should produce no hunks, got %+v", f.Hunks)
	}
	if !(Patch{Files: []File{f}}).Empty() {
		t.Fatal("a hunkless file should read as an empty patch")
	}
	if f.OldPath != "" || f.NewPath != "" {
		t.Fatalf("two revisions of one thing carry no paths, got %q/%q", f.OldPath, f.NewPath)
	}
}

// TestAlignLines_OversizedFallsBackToBlockReplace pins the memory guard:
// past pairCap the aligner stops building an LCS table and emits a
// coarse but honest replace-everything alignment.
func TestAlignLines_OversizedFallsBackToBlockReplace(t *testing.T) {
	n := 1200 // 1200^2 > pairCap
	old := make([]string, n)
	new := make([]string, n)
	for i := range old {
		old[i] = "old"
		new[i] = "new"
	}
	ops := alignLines(old, new)
	if len(ops) != 2*n {
		t.Fatalf("block replace should emit every line twice, got %d", len(ops))
	}
	for _, o := range ops[:n] {
		if o.kind != Del {
			t.Fatalf("first half should be deletions, saw %v", o.kind)
		}
	}
	for _, o := range ops[n:] {
		if o.kind != Add {
			t.Fatalf("second half should be additions, saw %v", o.kind)
		}
	}
}

// TestGroupHunks_MergesNearbyChanges checks the merge rule directly:
// changes closer together than twice the context share one hunk, so the
// output never lists a line twice.
func TestGroupHunks_MergesNearbyChanges(t *testing.T) {
	ops := []op{
		{Del, "a"},
		{Context, "b"},
		{Context, "c"},
		{Del, "d"},
	}
	hunks := groupHunks(ops, contextLines)
	if len(hunks) != 1 {
		t.Fatalf("two nearby changes should merge into one hunk, got %d", len(hunks))
	}
	if got := hunks[0].Header(); got != "@@ -1,4 +1,2 @@" {
		t.Fatalf("merged header = %q", got)
	}
}
