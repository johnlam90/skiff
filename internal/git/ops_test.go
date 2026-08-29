// =============================================================================
// File: internal/git/ops_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bareOrigin creates a bare repo and wires it as dir's origin, so the
// push/pull verbs have a remote that needs no network.
func bareOrigin(t *testing.T, dir string) string {
	t.Helper()
	origin := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-q", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	// initRepo works on main; point the bare HEAD there so a clone of
	// this origin checks out the branch the pushes actually land on.
	gitT(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	gitT(t, dir, "remote", "add", "origin", origin)
	return origin
}

// cloneOf makes a second working clone of origin with a committer
// identity — the "other developer" whose pushes make ours non-fast-
// forward.
func cloneOf(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", origin, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	gitT(t, dir, "config", "user.email", "o@example.com")
	gitT(t, dir, "config", "user.name", "Other")
	gitT(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

// porcelain returns `git status --porcelain` for asserts.
func porcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return string(out)
}

// head returns the checked-out branch name for asserts.
func head(t *testing.T, dir string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	return strings.TrimSpace(string(out))
}

// joinedCalls renders a Fake's call log one argv per line, for argv
// assertions that read like the command they pin.
func joinedCalls(f *Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// TestCommit_ScopesToPaths commits one of two dirty files and checks
// the other stays uncommitted — the path scoping is the whole point of
// the git panel's checkbox column.
func TestCommit_ScopesToPaths(t *testing.T) {
	dir := initRepo(t)
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	writeSeed(t, one, "1\n")
	writeSeed(t, two, "2\n")

	if err := Open(dir).Commit([]string{one}, "only one"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	status := porcelain(t, dir)
	if strings.Contains(status, "one.txt") {
		t.Fatalf("one.txt should be committed, status:\n%s", status)
	}
	if !strings.Contains(status, "two.txt") {
		t.Fatalf("two.txt should still be dirty, status:\n%s", status)
	}
}

// TestCommit_ArgvAndEmptyPaths pins the staging semantics as argv —
// add -A scoped to the paths, then a path-scoped commit — and the
// refusal of an empty path list, which would otherwise become an
// unscoped `add -A` of the whole tree.
func TestCommit_ArgvAndEmptyPaths(t *testing.T) {
	f := &Fake{}
	f.Script("add -A -- /r/a.go /r/b.go", "", nil)
	f.Script("commit -m msg -- /r/a.go /r/b.go", "", nil)
	if err := OpenWith("/r", f).Commit([]string{"/r/a.go", "/r/b.go"}, "msg"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	want := []string{"add -A -- /r/a.go /r/b.go", "commit -m msg -- /r/a.go /r/b.go"}
	if got := joinedCalls(f); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("argv = %v, want %v", got, want)
	}

	empty := &Fake{}
	err := OpenWith("/r", empty).Commit(nil, "msg")
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Advice != "nothing to commit" {
		t.Fatalf("empty paths should be refused with the nothing-to-commit advice, got %v", err)
	}
	if empty.CallCount() != 0 {
		t.Fatalf("empty paths must not reach git, ran %v", joinedCalls(empty))
	}
}

// TestPush_FirstPushSetsUpstream pins the first-push rule against real
// git: with no upstream the verb sets one, and the second push is
// plain — the upstream probe happens inside the verb, which is what
// takes it off the event loop.
func TestPush_FirstPushSetsUpstream(t *testing.T) {
	dir := initRepo(t)
	bareOrigin(t, dir)
	r := Open(dir)
	if r.HasUpstream() {
		t.Fatal("fixture: no upstream yet")
	}
	if err := r.Push(); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !r.HasUpstream() {
		t.Fatal("the first push must set the upstream")
	}
	if err := r.Push(); err != nil {
		t.Fatalf("second push: %v", err)
	}
}

// TestPush_ArgvShapes pins the three argv shapes through the Fake: an
// upstream means a plain push; none means --set-upstream with the
// current branch; and a detached HEAD or a branch name git would read
// as an option drops the positional entirely, because push takes no
// `--` separator to hide it behind.
func TestPush_ArgvShapes(t *testing.T) {
	cases := []struct {
		name     string
		upstream bool
		branch   string // "" scripts a detached HEAD
		want     string
	}{
		{"upstream", true, "main", "push"},
		{"first push", false, "main", "push --set-upstream origin main"},
		{"detached", false, "", "push"},
		{"unsafe branch", false, "--upload-pack=touch /tmp/pwn", "push"},
	}
	for _, c := range cases {
		f := &Fake{}
		if c.upstream {
			f.Script("rev-parse --abbrev-ref @{upstream}", "origin/main\n", nil)
		}
		if c.branch != "" {
			f.Script("symbolic-ref --short HEAD", c.branch+"\n", nil)
		}
		f.Script(c.want, "", nil)
		if err := OpenWith("/r", f).Push(); err != nil {
			t.Fatalf("%s: push: %v", c.name, err)
		}
		if !f.Called(c.want) {
			t.Fatalf("%s: want argv %q, ran %v", c.name, c.want, joinedCalls(f))
		}
	}
}

// TestPush_NonFastForwardThenPullAndPush drives the real refusal: a
// clone pushes first, so our push is non-fast-forward and comes back
// with the flag set; PullAndPush then merges and lands it.
func TestPush_NonFastForwardThenPullAndPush(t *testing.T) {
	dir := initRepo(t)
	origin := bareOrigin(t, dir)
	r := Open(dir)
	if err := r.Push(); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	other := cloneOf(t, origin)
	writeSeed(t, filepath.Join(other, "theirs.txt"), "t\n")
	gitT(t, other, "add", ".")
	gitT(t, other, "commit", "-q", "-m", "theirs")
	gitT(t, other, "push", "-q")

	writeSeed(t, filepath.Join(dir, "ours.txt"), "o\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "ours")
	err := r.Push()
	var opErr *OpError
	if !errors.As(err, &opErr) || !opErr.NonFastForward {
		t.Fatalf("a push behind origin must report NonFastForward, got %v", err)
	}
	if opErr.Advice != "origin has commits you don't — pull first, then push" {
		t.Fatalf("advice = %q", opErr.Advice)
	}
	if err := r.PullAndPush(); err != nil {
		t.Fatalf("pull & push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "theirs.txt")); err != nil {
		t.Fatalf("the merge-pull should bring their file: %v", err)
	}
	if snap := r.Status(""); snap.Ahead != 0 || snap.Behind != 0 {
		t.Fatalf("after pull & push we should be level with origin, ahead %d behind %d", snap.Ahead, snap.Behind)
	}
}

// TestPullAndFetch pins the read-only remote verbs: Fetch learns about
// origin's new commit without touching the tree (behind goes to 1, the
// file is absent), and a fast-forward Pull brings it in.
func TestPullAndFetch(t *testing.T) {
	dir := initRepo(t)
	origin := bareOrigin(t, dir)
	r := Open(dir)
	if err := r.Push(); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	other := cloneOf(t, origin)
	writeSeed(t, filepath.Join(other, "theirs.txt"), "t\n")
	gitT(t, other, "add", ".")
	gitT(t, other, "commit", "-q", "-m", "theirs")
	gitT(t, other, "push", "-q")

	if err := r.Fetch(); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snap := r.Status(""); snap.Behind != 1 {
		t.Fatalf("fetch should learn we are behind by 1, got %d", snap.Behind)
	}
	if _, err := os.Stat(filepath.Join(dir, "theirs.txt")); err == nil {
		t.Fatal("fetch must not touch the working tree")
	}
	if err := r.Pull(true); err != nil {
		t.Fatalf("ff pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "theirs.txt")); err != nil {
		t.Fatalf("pull should bring their file: %v", err)
	}
}

// TestSwitch_TrackingRule pins the remote-pick rule against real git:
// the first switch to origin/x creates a tracking local x, the second
// reuses it, and a plain local name is a plain checkout.
func TestSwitch_TrackingRule(t *testing.T) {
	dir := initRepo(t)
	bareOrigin(t, dir)
	gitT(t, dir, "checkout", "-q", "-b", "feature")
	gitT(t, dir, "push", "-q", "-u", "origin", "feature")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "branch", "-q", "-D", "feature")
	r := Open(dir)

	if err := r.Switch("origin/feature"); err != nil {
		t.Fatalf("first switch: %v", err)
	}
	if got := head(t, dir); got != "feature" {
		t.Fatalf("first switch should create local feature, on %q", got)
	}
	if !r.HasUpstream() {
		t.Fatal("the created local must track origin/feature")
	}
	if err := r.Switch("main"); err != nil {
		t.Fatalf("switch main: %v", err)
	}
	if err := r.Switch("origin/feature"); err != nil {
		t.Fatalf("second switch: %v", err)
	}
	if got := head(t, dir); got != "feature" {
		t.Fatalf("second switch should reuse local feature, on %q", got)
	}
}

// TestSwitch_ArgvShapes pins the checkout argv for each spelling through
// the Fake: the existence probe decides between reuse and create, and
// every ref is followed by `--`.
func TestSwitch_ArgvShapes(t *testing.T) {
	f := &Fake{}
	f.Script("checkout feature --", "", nil)
	if err := OpenWith("/r", f).Switch("feature"); err != nil {
		t.Fatalf("local: %v", err)
	}
	if !f.Called("checkout feature --") {
		t.Fatalf("local switch argv: %v", joinedCalls(f))
	}

	absent := &Fake{}
	absent.Script("checkout -b feature --track origin/feature --", "", nil)
	if err := OpenWith("/r", absent).Switch("origin/feature"); err != nil {
		t.Fatalf("remote, local absent: %v", err)
	}
	if !absent.Called("checkout -b feature --track origin/feature --") {
		t.Fatalf("remote-first argv: %v", joinedCalls(absent))
	}

	present := &Fake{}
	present.Script("rev-parse --verify --quiet refs/heads/feature", "abc\n", nil)
	present.Script("checkout feature --", "", nil)
	if err := OpenWith("/r", present).Switch("origin/feature"); err != nil {
		t.Fatalf("remote, local present: %v", err)
	}
	if !present.Called("checkout feature --") {
		t.Fatalf("remote-reuse argv: %v", joinedCalls(present))
	}
}

// TestSwitch_RefusesUnsafeRefs is the regression test for the
// flag-injection hole: a clone can ship a branch named --output=/tmp/x,
// and `git checkout <that>` turns a branch switch into an arbitrary
// file write. A name git's option parser would claim never reaches git
// — including one hiding behind a remote prefix, where the local name
// is what lands in the option position.
func TestSwitch_RefusesUnsafeRefs(t *testing.T) {
	for _, bad := range []string{"--output=/tmp/x", "-b", "", "origin/--output=/tmp/x"} {
		f := &Fake{}
		err := OpenWith("/r", f).Switch(bad)
		if !errors.Is(err, ErrUnsafeRef) {
			t.Fatalf("Switch(%q) = %v, want ErrUnsafeRef", bad, err)
		}
		if f.CallCount() != 0 {
			t.Fatalf("Switch(%q) must not reach git, ran %v", bad, joinedCalls(f))
		}
	}
}

// TestNewBranch pins creation from HEAD and from an explicit start
// point, and the argv each produces.
func TestNewBranch(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	if err := r.NewBranch("side", ""); err != nil {
		t.Fatalf("new branch: %v", err)
	}
	if got := head(t, dir); got != "side" {
		t.Fatalf("on %q, want side", got)
	}
	if err := r.NewBranch("from-main", "main"); err != nil {
		t.Fatalf("new branch from main: %v", err)
	}
	if got := head(t, dir); got != "from-main" {
		t.Fatalf("on %q, want from-main", got)
	}
	if err := r.NewBranch("--evil", ""); !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("unsafe name = %v, want ErrUnsafeRef", err)
	}

	f := &Fake{}
	f.Script("checkout -b x main --", "", nil)
	if err := OpenWith("/r", f).NewBranch("x", "main"); err != nil || !f.Called("checkout -b x main --") {
		t.Fatalf("argv with start point: %v %v", err, joinedCalls(f))
	}
}

// TestDeleteBranch_NotMergedThenForce pins the two-step ladder against
// real git: -d refuses an unmerged branch with NotMerged set, and the
// forced retry removes it.
func TestDeleteBranch_NotMergedThenForce(t *testing.T) {
	dir := initRepo(t)
	gitT(t, dir, "checkout", "-q", "-b", "orphan")
	writeSeed(t, filepath.Join(dir, "o.txt"), "o\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "unmerged")
	gitT(t, dir, "checkout", "-q", "main")
	r := Open(dir)

	err := r.DeleteBranch("orphan", false)
	var opErr *OpError
	if !errors.As(err, &opErr) || !opErr.NotMerged {
		t.Fatalf("-d on an unmerged branch must report NotMerged, got %v", err)
	}
	if err := r.DeleteBranch("orphan", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if r.BranchExists("orphan") {
		t.Fatal("orphan should be gone")
	}
	if err := r.DeleteBranch("-D", false); !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("unsafe name = %v, want ErrUnsafeRef", err)
	}
}

// TestRenameAndMerge pins the two remaining branch verbs against real
// git: rename moves HEAD's name, merge brings a side branch's file in.
func TestRenameAndMerge(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	if err := r.RenameBranch("main", "mainline"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := head(t, dir); got != "mainline" {
		t.Fatalf("on %q, want mainline", got)
	}
	gitT(t, dir, "checkout", "-q", "-b", "feature")
	writeSeed(t, filepath.Join(dir, "feat.txt"), "f\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "feature work")
	gitT(t, dir, "checkout", "-q", "mainline")
	if err := r.Merge("feature"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "feat.txt")); err != nil {
		t.Fatalf("merge should bring feat.txt: %v", err)
	}
	if err := r.Merge("--no-verify"); !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("unsafe merge ref = %v, want ErrUnsafeRef", err)
	}
}

// TestStashAndUndoCommit drives stash push/pop and the soft reset —
// the tree-rewriting verbs the menu offers.
func TestStashAndUndoCommit(t *testing.T) {
	dir := initRepo(t)
	f := filepath.Join(dir, "f.txt")
	writeSeed(t, f, "y\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "second")
	r := Open(dir)

	if err := r.UndoCommit(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !strings.Contains(porcelain(t, dir), "f.txt") {
		t.Fatal("undo should leave the change staged/dirty")
	}
	if err := r.StashPush(); err != nil {
		t.Fatalf("stash: %v", err)
	}
	if strings.Contains(porcelain(t, dir), "f.txt") {
		t.Fatal("stash should clean the tree")
	}
	if err := r.StashPop(); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if !strings.Contains(porcelain(t, dir), "f.txt") {
		t.Fatal("pop should bring the change back")
	}
	err := r.StashPop()
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Advice != "no stash to pop" {
		t.Fatalf("popping an empty stash should carry the advice, got %v", err)
	}
}
