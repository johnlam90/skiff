// =============================================================================
// File: internal/git/worktrees_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestParseWorktreeList pins the porcelain shape: worktree blocks, short
// branch names, and the flag lines. Unknown fields must not cost a row.
func TestParseWorktreeList(t *testing.T) {
	out := "worktree /repo\n" +
		"HEAD abc123\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /repo-side\n" +
		"HEAD abc123\n" +
		"branch refs/heads/side\n" +
		"locked\n" +
		"\n" +
		"worktree /repo-detached\n" +
		"HEAD def456\n" +
		"detached\n"
	wts := parseWorktreeList(out)
	if len(wts) != 3 {
		t.Fatalf("want 3 worktrees, got %d: %+v", len(wts), wts)
	}
	if wts[0].Path != "/repo" || wts[0].Branch != "main" || !wts[0].Main {
		t.Fatalf("main row: %+v", wts[0])
	}
	if wts[1].Path != "/repo-side" || wts[1].Branch != "side" || wts[1].Main {
		t.Fatalf("side row: %+v", wts[1])
	}
	if len(wts[1].Flags) != 1 || wts[1].Flags[0] != "locked" {
		t.Fatalf("side flags: %v", wts[1].Flags)
	}
	if wts[2].Branch != "" || len(wts[2].Flags) != 1 || wts[2].Flags[0] != "detached" {
		t.Fatalf("detached row: %+v", wts[2])
	}
}

// TestWorktrees_AddListRemove drives the three verbs against real git:
// add checks out a branch in a sibling tree, the list shows both rows
// with the main one marked, and remove takes the sibling away.
func TestWorktrees_AddListRemove(t *testing.T) {
	dir := initRepo(t)
	gitT(t, dir, "branch", "side")
	r := Open(dir)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(wt, "side", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	wts, err := r.Worktrees()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(wts) != 2 || !wts[0].Main || wts[1].Main {
		t.Fatalf("list = %+v, want main then side", wts)
	}
	// git reports the worktree's resolved path; on macOS t.TempDir lives
	// under /var, a symlink to /private/var, so the expectation has to
	// be resolved the same way — as initRepo already does for the root.
	wantWt, _ := filepath.EvalSymlinks(wt)
	if wts[1].Branch != "side" || wts[1].Path != wantWt {
		t.Fatalf("side row = %+v, want path %s", wts[1], wantWt)
	}
	if err := r.WorktreeRemove(wt, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
	if _, err := Open(t.TempDir()).Worktrees(); err == nil {
		t.Fatal("a non-repo must error rather than list nothing")
	}
}

// TestWorktreeAdd_NewBranchAndDirtyRemove pins the create flag (a fresh
// branch from HEAD in the new tree) and the remove ladder: a plain
// remove of a dirty tree comes back with WorktreeDirty, force removes.
func TestWorktreeAdd_NewBranchAndDirtyRemove(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(wt, "fresh", true); err != nil {
		t.Fatalf("add -b: %v", err)
	}
	if got := head(t, wt); got != "fresh" {
		t.Fatalf("new tree on %q, want fresh", got)
	}
	writeSeed(t, filepath.Join(wt, "dirty.txt"), "wip\n")
	err := r.WorktreeRemove(wt, false)
	var opErr *OpError
	if !errors.As(err, &opErr) || !opErr.WorktreeDirty {
		t.Fatalf("removing a dirty tree must report WorktreeDirty, got %v", err)
	}
	if err := r.WorktreeRemove(wt, true); err != nil {
		t.Fatalf("force remove: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
}

// TestWorktreeAdd_ArgvShapes pins the argv for every create shape
// through the Fake: new branch, existing local, and the remote-tracking
// pick whose existence probe decides between reuse and create.
func TestWorktreeAdd_ArgvShapes(t *testing.T) {
	cases := []struct {
		name         string
		branch       string
		create       bool
		localPresent bool
		want         string
	}{
		{"new branch", "side", true, false, "worktree add /r/wt -b side --"},
		{"local", "main", false, false, "worktree add /r/wt main --"},
		{"remote, local absent", "origin/feature", false, false, "worktree add /r/wt -b feature --track origin/feature --"},
		{"remote, local present", "origin/feature", false, true, "worktree add /r/wt feature --"},
	}
	for _, c := range cases {
		f := &Fake{}
		if c.localPresent {
			f.Script("rev-parse --verify --quiet refs/heads/feature", "abc\n", nil)
		}
		f.Script(c.want, "", nil)
		if err := OpenWith("/r", f).WorktreeAdd("/r/wt", c.branch, c.create); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !f.Called(c.want) {
			t.Fatalf("%s: want %q, ran %v", c.name, c.want, joinedCalls(f))
		}
	}
}

// TestWorktreeVerbs_HardenArgv is the regression test for the
// flag-injection hole: a clone can ship a branch named --output=/tmp/x,
// and a worktree path prompt could carry anything. Unsafe names never
// reach git; a path in the option position is refused with its own
// sentinel, because a path is not a ref.
func TestWorktreeVerbs_HardenArgv(t *testing.T) {
	for _, bad := range []string{"--output=/tmp/x", "-b", "", "origin/--output=/tmp/x"} {
		f := &Fake{}
		if err := OpenWith("/r", f).WorktreeAdd("/r/wt", bad, false); !errors.Is(err, ErrUnsafeRef) {
			t.Fatalf("WorktreeAdd(%q) = %v, want ErrUnsafeRef", bad, err)
		}
		if f.CallCount() != 0 {
			t.Fatalf("WorktreeAdd(%q) must not reach git, ran %v", bad, joinedCalls(f))
		}
	}
	f := &Fake{}
	if err := OpenWith("/r", f).WorktreeAdd("/r/wt", "--output=/tmp/x", true); !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("new branch %q = %v, want ErrUnsafeRef", "--output=/tmp/x", err)
	}
	if err := OpenWith("/r", f).WorktreeAdd("-evil", "main", false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("option-position path = %v, want ErrUnsafePath", err)
	}
	if err := OpenWith("/r", f).WorktreeRemove("-evil", true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("option-position remove path = %v, want ErrUnsafePath", err)
	}
	if f.CallCount() != 0 {
		t.Fatalf("refused verbs must not reach git, ran %v", joinedCalls(f))
	}
}
