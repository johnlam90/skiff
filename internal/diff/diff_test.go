// =============================================================================
// File: internal/diff/diff_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the model itself: the handful of derived answers every
// consumer reads instead of re-deriving — a line's wire marker, a hunk's
// header, a file's display name, and whether a patch is worth opening.

package diff

import "testing"

// TestKindMarker_MatchesTheWireFormat pins the prefix bytes the unified
// format is read by. Skiff's own line styling keys off them, so a wrong
// marker silently paints an addition as context.
func TestKindMarker_MatchesTheWireFormat(t *testing.T) {
	cases := map[Kind]byte{Context: ' ', Add: '+', Del: '-'}
	for k, want := range cases {
		if got := k.Marker(); got != want {
			t.Fatalf("Kind(%d).Marker() = %q, want %q", k, got, want)
		}
	}
	if got := Kind(99).Marker(); got != ' ' {
		t.Fatalf("an unknown kind must degrade to context, got %q", got)
	}
}

// TestHunkHeader_WritesBothLengths pins the one header renderer both
// producers share, including the function-context suffix git puts after
// the closing @@ and the zero-length side a pure insertion reports.
func TestHunkHeader_WritesBothLengths(t *testing.T) {
	h := Hunk{OldStart: 1, OldLen: 3, NewStart: 1, NewLen: 3}
	if got := h.Header(); got != "@@ -1,3 +1,3 @@" {
		t.Fatalf("header = %q", got)
	}
	h.Section = "func main() {"
	if got := h.Header(); got != "@@ -1,3 +1,3 @@ func main() {" {
		t.Fatalf("header with section = %q", got)
	}
	empty := Hunk{OldStart: 0, OldLen: 0, NewStart: 1, NewLen: 2}
	if got := empty.Header(); got != "@@ -0,0 +1,2 @@" {
		t.Fatalf("zero-length side = %q", got)
	}
}

// TestFilePath_PrefersTheNewName pins the display name: a rename shows
// where the file ended up (which is also what an open tab holds), and a
// deletion — with no new side at all — falls back to the old name rather
// than going nameless.
func TestFilePath_PrefersTheNewName(t *testing.T) {
	if got := (File{OldPath: "old.go", NewPath: "new.go"}).Path(); got != "new.go" {
		t.Fatalf("rename path = %q", got)
	}
	if got := (File{OldPath: "gone.txt", IsDeleted: true}).Path(); got != "gone.txt" {
		t.Fatalf("deletion path = %q", got)
	}
	if got := (File{}).Path(); got != "" {
		t.Fatalf("a nameless file should stay nameless, got %q", got)
	}
}

// TestPatchEmpty_SeparatesNoiseFromContent is the check every caller
// makes before opening a diff view: header-only files (a mode change)
// are nothing to show, while hunks and binary notes are.
func TestPatchEmpty_SeparatesNoiseFromContent(t *testing.T) {
	if !(Patch{}).Empty() {
		t.Fatal("a patch with no files is empty")
	}
	if !(Patch{Files: []File{{OldPath: "f", NewPath: "f"}}}).Empty() {
		t.Fatal("a file with no hunks and no binary note is header noise")
	}
	if (Patch{Files: []File{{Hunks: []Hunk{{}}}}}).Empty() {
		t.Fatal("a hunk is something to show")
	}
	if (Patch{Files: []File{{Binary: true}}}).Empty() {
		t.Fatal("a binary note is something to show")
	}
}
