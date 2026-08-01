// =============================================================================
// File: internal/search/search_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the project-search engine: literal matching, smart-case,
// binary/oversize skipping, and the result caps.

package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed writes name → content under root and returns the relative name.
func seed(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return name
}

// TestSearchLiteralHits pins the basics: hits carry the right file,
// 1-based line, rune column, and line text, in file order.
func TestSearchLiteralHits(t *testing.T) {
	root := t.TempDir()
	f1 := seed(t, root, "a.go", "package a\n\nfunc Hello() {}\n")
	f2 := seed(t, root, "b.go", "// Hello again\n")

	got, trunc := Search(root, []string{f1, f2}, "Hello", DefaultOptions())
	if trunc {
		t.Fatal("no caps should trip here")
	}
	if len(got) != 2 {
		t.Fatalf("matches: got %d, want 2", len(got))
	}
	if got[0].Path != "a.go" || got[0].Line != 3 || got[0].Col != 5 {
		t.Fatalf("first match wrong: %+v", got[0])
	}
	if got[1].Path != "b.go" || got[1].Line != 1 {
		t.Fatalf("second match wrong: %+v", got[1])
	}
	if got[0].Text != "func Hello() {}" {
		t.Fatalf("text: got %q", got[0].Text)
	}
}

// TestSearchSmartCase: a lowercase query matches any case; one capital
// letter makes the query exact.
func TestSearchSmartCase(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "x.txt", "Hello\nhello\nHELLO\n")

	got, _ := Search(root, []string{f}, "hello", DefaultOptions())
	if len(got) != 3 {
		t.Fatalf("lowercase query should hit all 3 casings, got %d", len(got))
	}
	got, _ = Search(root, []string{f}, "Hello", DefaultOptions())
	if len(got) != 1 || got[0].Line != 1 {
		t.Fatalf("cased query should hit only the exact line, got %+v", got)
	}
}

// TestSearchSkipsBinary: a NUL byte in the head marks a file binary and
// excludes it entirely.
func TestSearchSkipsBinary(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "bin.dat", "match\x00me\nmatch\n")

	got, _ := Search(root, []string{f}, "match", DefaultOptions())
	if len(got) != 0 {
		t.Fatalf("binary file should be skipped, got %d matches", len(got))
	}
}

// TestSearchCapsAndTruncatedFlag: the per-file and total caps stop the
// sweep and report truncation instead of flooding the UI.
func TestSearchCapsAndTruncatedFlag(t *testing.T) {
	root := t.TempDir()
	many := strings.Repeat("hit\n", 100)
	f1 := seed(t, root, "one.txt", many)
	f2 := seed(t, root, "two.txt", many)

	opts := Options{MaxTotal: 60, MaxPerFile: 50, MaxFileSize: 1 << 20}
	got, trunc := Search(root, []string{f1, f2}, "hit", opts)
	if !trunc {
		t.Fatal("caps were hit — truncated must be true")
	}
	if len(got) != 60 {
		t.Fatalf("total cap: got %d, want 60", len(got))
	}
	perFile := 0
	for _, m := range got {
		if m.Path == "one.txt" {
			perFile++
		}
	}
	if perFile != 50 {
		t.Fatalf("per-file cap: got %d from one.txt, want 50", perFile)
	}
}

// TestSearchMissingFileSkipped: a stale index entry (file deleted since
// the last build) is skipped without error.
func TestSearchMissingFileSkipped(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "real.txt", "needle\n")

	got, _ := Search(root, []string{"ghost.txt", f}, "needle", DefaultOptions())
	if len(got) != 1 || got[0].Path != "real.txt" {
		t.Fatalf("expected only the real file's hit, got %+v", got)
	}
}

// TestSearchEmptyQuery returns nothing rather than matching every line.
func TestSearchEmptyQuery(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "x.txt", "anything\n")
	if got, _ := Search(root, []string{f}, "   ", DefaultOptions()); len(got) != 0 {
		t.Fatalf("blank query must return nothing, got %d", len(got))
	}
}

// TestSearchCancellation: a cancelled sweep abandons between files and
// returns nothing — its results would be dropped anyway, so the walk
// must not keep paying for disk reads.
func TestSearchCancellation(t *testing.T) {
	root := t.TempDir()
	f1 := seed(t, root, "a.txt", "hit\n")
	f2 := seed(t, root, "b.txt", "hit\n")
	opts := DefaultOptions()
	opts.Cancelled = func() bool { return true }
	if got, _ := Search(root, []string{f1, f2}, "hit", opts); got != nil {
		t.Fatalf("cancelled sweep should return nothing, got %v", got)
	}
}
