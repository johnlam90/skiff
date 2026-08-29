// =============================================================================
// File: internal/git/tree_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestToplevel pins the root answer against real git — from the root
// and from a subdirectory, which resolves to the same root — and the
// failure outside a repository that Status reads as "not a repo".
func TestToplevel(t *testing.T) {
	dir := initRepo(t)
	want, _ := filepath.EvalSymlinks(dir)
	top, err := Open(dir).Toplevel()
	if err != nil || top != want {
		t.Fatalf("toplevel = %q, %v; want %q", top, err, want)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if top, err := Open(sub).Toplevel(); err != nil || top != want {
		t.Fatalf("from a subdirectory = %q, %v; want %q", top, err, want)
	}
	if _, err := Open(t.TempDir()).Toplevel(); err == nil {
		t.Fatal("a non-repo must error")
	}
	f := &Fake{}
	f.Script("rev-parse --show-toplevel", "\n", nil)
	if _, err := OpenWith("/r", f).Toplevel(); err == nil {
		t.Fatal("an empty answer is not a root")
	}
}

// TestLsFiles pins the finder's index source: tracked and untracked
// files listed, ignored ones not, and a non-repo erroring so the
// caller falls back to its directory walk.
func TestLsFiles(t *testing.T) {
	dir := initRepo(t)
	writeSeed(t, filepath.Join(dir, ".gitignore"), "ignored.txt\n")
	writeSeed(t, filepath.Join(dir, "ignored.txt"), "x\n")
	writeSeed(t, filepath.Join(dir, "untracked.txt"), "x\n")
	paths, err := Open(dir).LsFiles()
	if err != nil {
		t.Fatalf("ls-files: %v", err)
	}
	joined := strings.Join(paths, ",")
	for _, want := range []string{"f.txt", "untracked.txt", ".gitignore"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, paths)
		}
	}
	if strings.Contains(joined, "ignored.txt") {
		t.Fatalf("ignored file must not be listed: %v", paths)
	}
	if _, err := Open(t.TempDir()).LsFiles(); err == nil {
		t.Fatal("a non-repo must error")
	}
}
