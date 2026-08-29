// =============================================================================
// File: internal/git/log_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/diff"
)

// TestParseLog_CapturedOutput pins the NUL framing: three fields per
// line, a tab inside a subject stays in the subject, and a short line
// is dropped rather than shifting the fields of the ones after it.
func TestParseLog_CapturedOutput(t *testing.T) {
	out := "fea1a07\x00seed\ttab\x000 seconds ago\n" +
		"broken line\n" +
		"abc1234\x00second\x002 days ago\n"
	got := parseLog(out)
	if len(got) != 2 {
		t.Fatalf("want 2 commits, got %+v", got)
	}
	if got[0] != (Commit{Hash: "fea1a07", Subject: "seed\ttab", When: "0 seconds ago"}) {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Hash != "abc1234" || got[1].When != "2 days ago" {
		t.Fatalf("second = %+v", got[1])
	}
	if parseLog("") != nil {
		t.Fatal("empty output should parse to nil")
	}
}

// twoCommitRepo seeds a repo with two commits touching different files
// so branch and file scopes can be told apart.
func twoCommitRepo(t *testing.T) (dir, aFile, bFile string) {
	t.Helper()
	dir = initRepo(t)
	aFile = filepath.Join(dir, "a.txt")
	bFile = filepath.Join(dir, "b.txt")
	writeSeed(t, aFile, "a\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "add a")
	writeSeed(t, bFile, "b\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "add b")
	return dir, aFile, bFile
}

// TestLog_BranchAndFileScopes pins the two scopes against real git: the
// branch log lists every commit newest first, and a path narrows it to
// the commits that touched that file.
func TestLog_BranchAndFileScopes(t *testing.T) {
	dir, aFile, bFile := twoCommitRepo(t)
	r := Open(dir)
	branch, err := r.Log(200, "")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(branch) != 3 || branch[0].Subject != "add b" || branch[2].Subject != "seed" {
		t.Fatalf("branch log = %+v", branch)
	}
	if branch[0].Hash == "" || branch[0].When == "" {
		t.Fatalf("every row carries hash and age: %+v", branch[0])
	}
	onlyB, err := r.Log(200, bFile)
	if err != nil || len(onlyB) != 1 || onlyB[0].Subject != "add b" {
		t.Fatalf("file log for b = %+v, %v", onlyB, err)
	}
	if onlyA, _ := r.Log(200, aFile); len(onlyA) != 1 {
		t.Fatalf("file log for a = %+v", onlyA)
	}
	if capped, _ := r.Log(1, ""); len(capped) != 1 {
		t.Fatalf("limit must cap the list, got %d", len(capped))
	}
}

// TestLog_Failures pins the two ways Log says no: a non-positive limit
// is refused before any git call, and a non-repo errors rather than
// answering with an empty history.
func TestLog_Failures(t *testing.T) {
	f := &Fake{}
	if _, err := OpenWith("/r", f).Log(0, ""); err == nil || f.CallCount() != 0 {
		t.Fatalf("zero limit must be refused without a git call, err %v calls %v", err, joinedCalls(f))
	}
	if _, err := Open(t.TempDir()).Log(10, ""); err == nil {
		t.Fatal("a non-repo must error")
	}
}

// TestShow_WholeAndScoped pins Show against real git: the whole commit
// lists every file it touched, and a path narrows it to that file.
func TestShow_WholeAndScoped(t *testing.T) {
	dir, aFile, _ := twoCommitRepo(t)
	r := Open(dir)
	// A third commit touching both files.
	writeSeed(t, aFile, "a2\n")
	writeSeed(t, filepath.Join(dir, "b.txt"), "b2\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "both")
	log, err := r.Log(1, "")
	if err != nil || len(log) != 1 {
		t.Fatalf("log: %v %+v", err, log)
	}
	whole, err := r.Show(log[0].Hash, "")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if paths := patchPaths(whole); strings.Join(paths, ",") != "a.txt,b.txt" {
		t.Fatalf("whole commit paths = %v", paths)
	}
	scoped, err := r.Show(log[0].Hash, aFile)
	if err != nil {
		t.Fatalf("scoped show: %v", err)
	}
	if paths := patchPaths(scoped); strings.Join(paths, ",") != "a.txt" {
		t.Fatalf("scoped paths = %v", paths)
	}
	if _, err := r.Show("definitely-not-a-commit", ""); err == nil {
		t.Fatal("an unknown hash must error")
	}
}

// TestShow_RefusesOptionLookalikeHash pins the SafeRef gate on the one
// argument that today only ever holds Log's own %h output: a future
// caller handing over a repo-supplied string must still be stopped
// before git's option parser sees it.
func TestShow_RefusesOptionLookalikeHash(t *testing.T) {
	for _, bad := range []string{"--output=/tmp/x", "-p", ""} {
		f := &Fake{}
		_, err := OpenWith("/r", f).Show(bad, "")
		if !errors.Is(err, ErrUnsafeRef) {
			t.Fatalf("Show(%q) = %v, want ErrUnsafeRef", bad, err)
		}
		if f.CallCount() != 0 {
			t.Fatalf("Show(%q) must not reach git, ran %v", bad, joinedCalls(f))
		}
	}
}

// patchPaths lists a patch's file paths in order, for compact asserts.
func patchPaths(p diff.Patch) []string {
	var out []string
	for _, f := range p.Files {
		out = append(out, f.Path())
	}
	return out
}
