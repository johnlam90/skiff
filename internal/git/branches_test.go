// =============================================================================
// File: internal/git/branches_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"strings"
	"testing"
)

// TestParseBranches_CapturedOutput pins the ordering rules against
// for-each-ref output captured from a clone: current first, then the
// other locals, then remotes minus the spellings that duplicate a
// local, with origin/HEAD dropped because it is a symref — not because
// its name contains HEAD.
func TestParseBranches_CapturedOutput(t *testing.T) {
	out := " \x00refs/heads/fix-HEADER\x00\n" +
		"*\x00refs/heads/main\x00\n" +
		" \x00refs/heads/zeta\x00\n" +
		" \x00refs/remotes/origin/HEAD\x00refs/remotes/origin/main\n" +
		" \x00refs/remotes/origin/main\x00\n" +
		" \x00refs/remotes/origin/remote-only\x00\n"
	got := parseBranches(out)
	want := []string{"main", "fix-HEADER", "zeta", "origin/remote-only"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseBranches = %v, want %v", got, want)
	}
	if got := parseBranches(""); len(got) != 0 {
		t.Fatalf("no refs should parse to no names, got %v", got)
	}
}

// TestBranches_RealGit pins the list against real git: HEAD noise
// filtered, remote spellings of local branches hidden, the current
// branch first.
func TestBranches_RealGit(t *testing.T) {
	dir := initRepo(t)
	bareOrigin(t, dir)
	gitT(t, dir, "push", "-q", "-u", "origin", "main")
	gitT(t, dir, "branch", "local-only")
	gitT(t, dir, "checkout", "-q", "-b", "remote-only")
	gitT(t, dir, "push", "-q", "-u", "origin", "remote-only")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "branch", "-q", "-D", "remote-only")

	names, err := Open(dir).Branches()
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	if names[0] != "main" {
		t.Fatalf("current branch should lead, got %v", names)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "local-only") || !strings.Contains(joined, "origin/remote-only") {
		t.Fatalf("missing branches: %v", names)
	}
	if strings.Contains(joined, "origin/main") {
		t.Fatalf("remote spelling of a local branch should hide: %v", names)
	}
	if strings.Contains(joined, "HEAD") {
		t.Fatalf("HEAD noise should be filtered: %v", names)
	}
	if _, err := Open(t.TempDir()).Branches(); err == nil {
		t.Fatal("a non-repo must error rather than list nothing")
	}
}

// TestHasUpstream_RealGit pins the probe Push builds its argv on: false
// before the first push with -u, true after.
func TestHasUpstream_RealGit(t *testing.T) {
	dir := initRepo(t)
	bareOrigin(t, dir)
	r := Open(dir)
	if r.HasUpstream() {
		t.Fatal("no upstream before the first push")
	}
	gitT(t, dir, "push", "-q", "-u", "origin", "main")
	if !r.HasUpstream() {
		t.Fatal("upstream should exist after push -u")
	}
}

// TestBranchExists pins the existence probe: a real local answers
// true, an absent one false, and an unsafe name false without a git
// call — it cannot be a branch we would act on.
func TestBranchExists(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	if !r.BranchExists("main") {
		t.Fatal("main exists")
	}
	if r.BranchExists("nope") {
		t.Fatal("nope does not exist")
	}
	f := &Fake{}
	if OpenWith("/r", f).BranchExists("--output=/tmp/x") || f.CallCount() != 0 {
		t.Fatalf("unsafe name must answer false without reaching git, ran %v", joinedCalls(f))
	}
}

// TestCurrentBranch_DetachedIsNotABranch pins the difference from
// Snapshot's branch label: a detached HEAD answers ok=false rather than
// a SHA, so Push never pastes a commit hash into --set-upstream.
func TestCurrentBranch_DetachedIsNotABranch(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	if name, ok := r.currentBranch(); !ok || name != "main" {
		t.Fatalf("on a branch: got %q %v", name, ok)
	}
	gitT(t, dir, "checkout", "-q", "--detach")
	if name, ok := r.currentBranch(); ok {
		t.Fatalf("detached HEAD must not be a branch, got %q", name)
	}
}
