// =============================================================================
// File: internal/diff/parse_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the git-text constructor. The samples are real `git diff`
// output shapes — spaced and C-quoted paths, renames, /dev/null sides,
// the "no newline" note — because those are the shapes that used to be
// re-derived in two places and are the reason this package exists.

package diff

import (
	"reflect"
	"strings"
	"testing"
)

// parsed is the happy-path helper: parse or fail the test, so each case
// below reads as an assertion about the model rather than about errors.
func parsed(t *testing.T, lines ...string) Patch {
	t.Helper()
	p, err := Parse([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

// TestParse_SingleFileHunk pins the everyday shape: git's framing is
// consumed into paths, and the hunk's lines come back numbered on both
// sides so nothing downstream has to count them again.
func TestParse_SingleFileHunk(t *testing.T) {
	p := parsed(t,
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,3 @@ func main() {",
		" ctx one",
		"-old line",
		"+new line",
		" ctx two",
	)
	if len(p.Files) != 1 {
		t.Fatalf("want one file, got %d: %+v", len(p.Files), p.Files)
	}
	f := p.Files[0]
	if f.OldPath != "f.txt" || f.NewPath != "f.txt" || f.Path() != "f.txt" {
		t.Fatalf("paths = %q/%q", f.OldPath, f.NewPath)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("want one hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldLen != 3 || h.NewStart != 1 || h.NewLen != 3 {
		t.Fatalf("hunk range = %+v", h)
	}
	if h.Section != "func main() {" {
		t.Fatalf("section = %q, want git's function context", h.Section)
	}
	want := []Line{
		{Kind: Context, OldNo: 1, NewNo: 1, Text: "ctx one"},
		{Kind: Del, OldNo: 2, Text: "old line"},
		{Kind: Add, NewNo: 2, Text: "new line"},
		{Kind: Context, OldNo: 3, NewNo: 3, Text: "ctx two"},
	}
	if !reflect.DeepEqual(h.Lines, want) {
		t.Fatalf("lines =\n%+v\nwant\n%+v", h.Lines, want)
	}
}

// TestParse_MultiFileWithQuotedAndDevNullPaths is the attribution
// contract the gutter depends on: one stream, four files, and every
// path shape git can print — a name with spaces (which carries git's
// trailing TAB), a rename (the new name wins), a deletion (+++ is
// /dev/null, so the --- line has to answer), and a C-quoted name.
func TestParse_MultiFileWithQuotedAndDevNullPaths(t *testing.T) {
	p := parsed(t,
		"diff --git a/a dir/spaced name.txt b/a dir/spaced name.txt",
		"index 0000000..1111111 100644",
		"--- a/a dir/spaced name.txt\t",
		"+++ b/a dir/spaced name.txt\t",
		"@@ -2,0 +3 @@ ctx",
		"+added line",
		"diff --git a/old.go b/renamed_new.go",
		"similarity index 90%",
		"rename from old.go",
		"rename to renamed_new.go",
		"index 2222222..3333333 100644",
		"--- a/old.go",
		"+++ b/renamed_new.go",
		"@@ -5 +5 @@ ctx",
		"-x",
		"+y",
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"index 4444444..0000000",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
		"-a",
		"-b",
		"-c",
		`diff --git "a/qu\totes.txt" "b/qu\totes.txt"`,
		"index 5555555..6666666 100644",
		`--- "a/qu\totes.txt"`,
		`+++ "b/qu\totes.txt"`,
		"@@ -1 +1 @@",
		"-old",
		"+new",
	)
	var paths []string
	for _, f := range p.Files {
		paths = append(paths, f.Path())
	}
	want := []string{"a dir/spaced name.txt", "renamed_new.go", "gone.txt", "qu\totes.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	if p.Files[1].OldPath != "old.go" {
		t.Fatalf("a rename must keep its old name too, got %q", p.Files[1].OldPath)
	}
	gone := p.Files[2]
	if !gone.IsDeleted || gone.NewPath != "" {
		t.Fatalf("deleted file: IsDeleted=%v NewPath=%q", gone.IsDeleted, gone.NewPath)
	}
	if h := p.Files[0].Hunks[0]; h.NewStart != 3 || h.NewLen != 1 {
		t.Fatalf("an elided length means 1, got %+v", h)
	}
}

// TestParse_BodyLinesNeverReopenTheHeader guards against patch content
// that mimics headers: an added line whose text begins "++ " renders as
// "+++ ", and only the block above the first hunk may be read as a
// header — otherwise one file's diff would be filed under another's
// name.
func TestParse_BodyLinesNeverReopenTheHeader(t *testing.T) {
	p := parsed(t,
		"diff --git a/f.txt b/f.txt",
		"index 0000000..1111111 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1 +1,2 @@",
		"-old",
		"+++ b/decoy.txt",
		"+--- a/decoy2.txt",
	)
	if len(p.Files) != 1 || p.Files[0].Path() != "f.txt" {
		t.Fatalf("want one file named f.txt, got %+v", p.Files)
	}
	lines := p.Files[0].Hunks[0].Lines
	if len(lines) != 3 || lines[1].Text != "++ b/decoy.txt" || lines[2].Text != "--- a/decoy2.txt" {
		t.Fatalf("decoy lines are content, not headers: %+v", lines)
	}
}

// TestParse_NewFileAndBinary records the two facts the paths alone
// cannot tell a reader apart: an all-new file (--- is /dev/null) and a
// binary one, which has no hunks yet is still something to show.
func TestParse_NewFileAndBinary(t *testing.T) {
	p := parsed(t,
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"index 0000000..587be6b",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1 @@",
		"+x",
		"diff --git a/img.png b/img.png",
		"index 1111111..2222222 100644",
		"Binary files a/img.png and b/img.png differ",
	)
	if len(p.Files) != 2 {
		t.Fatalf("want two files, got %d", len(p.Files))
	}
	fresh := p.Files[0]
	if !fresh.IsNew || fresh.OldPath != "" || fresh.Path() != "new.txt" {
		t.Fatalf("new file: IsNew=%v old=%q path=%q", fresh.IsNew, fresh.OldPath, fresh.Path())
	}
	bin := p.Files[1]
	if !bin.Binary || len(bin.Hunks) != 0 {
		t.Fatalf("binary file: Binary=%v hunks=%d", bin.Binary, len(bin.Hunks))
	}
	if p.Empty() {
		t.Fatal("a patch carrying a binary note has something to show")
	}
}

// TestParse_NoNewlineMarkerBelongsToItsLine keeps git's "\ No newline"
// note out of the line stream — it describes the line above it, so it
// rides along as a flag rather than becoming content nobody can pair.
func TestParse_NoNewlineMarkerBelongsToItsLine(t *testing.T) {
	p := parsed(t,
		"@@ -1 +1 @@",
		"-a",
		`\ No newline at end of file`,
		"+b",
		`\ No newline at end of file`,
	)
	lines := p.Files[0].Hunks[0].Lines
	want := []Line{
		{Kind: Del, OldNo: 1, Text: "a", NoNewline: true},
		{Kind: Add, NewNo: 1, Text: "b", NoNewline: true},
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines =\n%+v\nwant\n%+v", lines, want)
	}
}

// TestParse_BareHunksMakeOneImplicitFile covers input with no `diff
// --git` framing at all — a hunk preview, or anything hand-built. The
// hunks still need somewhere to live, and a nameless file is the honest
// answer.
func TestParse_BareHunksMakeOneImplicitFile(t *testing.T) {
	p := parsed(t, "@@ -1,2 +1,2 @@", "-gone", " kept", "+arrived")
	if len(p.Files) != 1 || p.Files[0].Path() != "" {
		t.Fatalf("want one nameless file, got %+v", p.Files)
	}
	if len(p.Files[0].Hunks) != 1 || len(p.Files[0].Hunks[0].Lines) != 3 {
		t.Fatalf("hunk lines = %+v", p.Files[0].Hunks)
	}
}

// TestParse_EmptyAndNonDiffInput pins the two quiet answers every
// best-effort caller relies on: nothing in, nothing out, and no error to
// report about text that was never a diff.
func TestParse_EmptyAndNonDiffInput(t *testing.T) {
	for _, in := range []string{"", "\n", "fatal: not a git repository\n"} {
		p, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%q) errored: %v", in, err)
		}
		if !p.Empty() || len(p.Files) != 0 {
			t.Fatalf("Parse(%q) = %+v, want an empty patch", in, p)
		}
	}
}

// TestParse_MalformedHunkHeaderReportsAndContinues is the failure mode:
// a header we cannot read ends that hunk and is named in the error, but
// everything around it still parses — a partial diff beats none on every
// git-aware surface in skiff.
func TestParse_MalformedHunkHeaderReportsAndContinues(t *testing.T) {
	p, err := Parse([]byte(strings.Join([]string{
		"@@ -bogus +1 @@",
		"+orphan",
		"@@ -1 +1 @@",
		"-a",
		"+b",
		"",
	}, "\n")))
	if err == nil {
		t.Fatal("a malformed hunk header should be reported")
	}
	if !strings.Contains(err.Error(), "-bogus") {
		t.Fatalf("error should name the header, got %v", err)
	}
	if len(p.Files) != 1 || len(p.Files[0].Hunks) != 1 {
		t.Fatalf("the good hunk should survive, got %+v", p.Files)
	}
	if got := p.Files[0].Hunks[0].Header(); got != "@@ -1,1 +1,1 @@" {
		t.Fatalf("surviving hunk = %q", got)
	}
}

// TestGitHeaderPaths_PrefersTheBSide pins the boundary-line split,
// including the rename case where the b side is the current name and the
// surprising-format case, where handing the whole remainder over beats
// leaving the file nameless.
func TestGitHeaderPaths_PrefersTheBSide(t *testing.T) {
	old, new := gitHeaderPaths("diff --git a/old/name.go b/new/name.go")
	if old != "old/name.go" || new != "new/name.go" {
		t.Fatalf("rename boundary: got %q/%q", old, new)
	}
	if old, new = gitHeaderPaths("diff --git mangled"); old != "" || new != "mangled" {
		t.Fatalf("fallback should hand over the remainder, got %q/%q", old, new)
	}
}

// TestHeaderPath_SeparatesDevNullFromMalformed keeps the two "no path"
// answers apart: /dev/null is a fact about the file (it was created or
// deleted) while an unrecognised operand is a parse failure that must
// leave the name it already had alone.
func TestHeaderPath_SeparatesDevNullFromMalformed(t *testing.T) {
	if p, dev := headerPath("/dev/null", "a/"); p != "" || !dev {
		t.Fatalf("/dev/null: got %q dev=%v", p, dev)
	}
	if p, dev := headerPath("b/f.txt", "b/"); p != "f.txt" || dev {
		t.Fatalf("plain path: got %q dev=%v", p, dev)
	}
	if p, dev := headerPath("nonsense", "b/"); p != "" || dev {
		t.Fatalf("malformed: got %q dev=%v", p, dev)
	}
	if p, _ := headerPath(`"b/bad`, "b/"); p != "" {
		t.Fatalf("unterminated quote should fail closed, got %q", p)
	}
}

// TestUnquoteGitPath covers the C-style quoting git applies to header
// paths with control or non-ASCII bytes — the escapes quote.c emits,
// octal included — plus the malformed shapes that must fail closed
// (empty string) rather than mis-attribute a section.
func TestUnquoteGitPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"b/plain.txt"`, "b/plain.txt"},
		{`"b/qu\totes.txt"`, "b/qu\totes.txt"},
		{`"b/back\\slash"`, `b/back\slash`},
		{`"b/dq\".txt"`, `b/dq".txt`},
		{`"b/na\303\257ve.txt"`, "b/naïve.txt"},
		{`"b/bell\a"`, "b/bell\a"},
		{`"b/ws\b\f\n\r\v"`, "b/ws\b\f\n\r\v"},
		{`b/notquoted`, ""},
		{`"unterminated`, ""},
		{`"bad\q"`, ""},
		{`"bad\30"`, ""},
		{`"`, ""},
	}
	for _, c := range cases {
		if got := unquoteGitPath(c.in); got != c.want {
			t.Fatalf("unquoteGitPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseDiffRange_ElidedLengthAndRefusals pins the range grammar the
// hunk header rests on: a missing length means one line (git elides it),
// and anything unparsable refuses rather than guessing a number that
// would silently move a marker.
func TestParseDiffRange_ElidedLengthAndRefusals(t *testing.T) {
	if start, count, ok := parseDiffRange("-12,3"); !ok || start != 12 || count != 3 {
		t.Fatalf("-12,3 = %d,%d,%v", start, count, ok)
	}
	if start, count, ok := parseDiffRange("+7"); !ok || start != 7 || count != 1 {
		t.Fatalf("+7 = %d,%d,%v", start, count, ok)
	}
	for _, bad := range []string{"", "-", "-x", "-1,x"} {
		if _, _, ok := parseDiffRange(bad); ok {
			t.Fatalf("%q should not parse", bad)
		}
	}
	if _, ok := parseHunkHeader("@@ -1,1 @@"); ok {
		t.Fatal("a header missing its new range should not parse")
	}
	if _, ok := parseHunkHeader("@@ -1,1 +bad @@"); ok {
		t.Fatal("a header with an unparsable new range should not parse")
	}
}
