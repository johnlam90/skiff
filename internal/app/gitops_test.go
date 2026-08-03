// =============================================================================
// File: internal/app/gitops_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the git write side: command builders, the plain-language
// error explainer, the async-done handler's routing, and end-to-end
// mutations against real repositories (with a local bare dir as origin
// where a remote is needed).

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bareOrigin creates a bare repo and wires it as dir's origin.
func bareOrigin(t *testing.T, dir string) string {
	t.Helper()
	origin := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-q", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	gitRun(t, dir, "remote", "add", "origin", origin)
	return origin
}

// commitAll makes a commit of everything so the repo has a HEAD.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A", ".")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

// porcelain returns git status --porcelain for asserts.
func porcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return string(out)
}

// TestExplainGit_Mappings pins the headline for each recognised
// failure shape — the sentence is the UX, so it's worth locking down.
func TestExplainGit_Mappings(t *testing.T) {
	cases := []struct{ out, want string }{
		{"! [rejected] main -> main (fetch first)", "origin has commits you don't — pull first, then push"},
		{"fatal: Not possible to fast-forward, aborting.", "local and origin have diverged — pull needs a merge"},
		{"CONFLICT (content): Merge conflict in a.go", "merge conflict — fix the marked files, then commit"},
		{"nothing to commit, working tree clean", "nothing to commit"},
		{"No stash entries found.", "no stash to pop"},
		{"fatal: could not read from remote repository", "couldn't reach origin — check network / credentials"},
		{"error: Your local changes to the following files would be overwritten by checkout:", "uncommitted changes are in the way — commit or stash first"},
		{"something novel", "git reported an error:"},
	}
	for _, c := range cases {
		if got := explainGit(c.out); got != c.want {
			t.Errorf("explainGit(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// TestGitCommitCmds pins druk's staging semantics: add -A scoped to
// the paths, then a path-scoped commit.
func TestGitCommitCmds(t *testing.T) {
	cmds := gitCommitCmds([]string{"/r/a.go", "/r/b.go"}, "msg")
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d", len(cmds))
	}
	if got := strings.Join(cmds[0], " "); got != "add -A -- /r/a.go /r/b.go" {
		t.Fatalf("add: %q", got)
	}
	if got := strings.Join(cmds[1], " "); got != "commit -m msg -- /r/a.go /r/b.go" {
		t.Fatalf("commit: %q", got)
	}
}

// TestGitPushCmds_UpstreamVsNot: a branch's first push sets the
// upstream; later pushes are plain.
func TestGitPushCmds_UpstreamVsNot(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	bareOrigin(t, dir)

	cmds := gitPushCmds(dir, "main")
	if got := strings.Join(cmds[0], " "); got != "push --set-upstream origin main" {
		t.Fatalf("first push: %q", got)
	}
	if out, err := execGitSequence(dir, cmds); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	cmds = gitPushCmds(dir, "main")
	if got := strings.Join(cmds[0], " "); got != "push" {
		t.Fatalf("second push: %q", got)
	}
}

// TestCommitEndToEnd commits one of two dirty files through the real
// sequence and checks the other stays uncommitted — the path scoping
// is the whole point of the checkbox column.
func TestCommitEndToEnd(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	writeFileT(t, one, "1\n")
	writeFileT(t, two, "2\n")
	commitAll(t, dir, "seed")
	writeFileT(t, one, "1 changed\n")
	writeFileT(t, two, "2 changed\n")

	if out, err := execGitSequence(dir, gitCommitCmds([]string{one}, "only one")); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	status := porcelain(t, dir)
	if strings.Contains(status, "one.txt") {
		t.Fatalf("one.txt should be committed, status:\n%s", status)
	}
	if !strings.Contains(status, "two.txt") {
		t.Fatalf("two.txt should still be dirty, status:\n%s", status)
	}
}

// TestUndoAndStashEndToEnd drives reset --soft and stash push/pop
// through the same sequences the menu actions use.
func TestUndoAndStashEndToEnd(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	f := filepath.Join(dir, "a.txt")
	writeFileT(t, f, "x\n")
	commitAll(t, dir, "seed")
	writeFileT(t, f, "y\n")
	commitAll(t, dir, "second")

	if out, err := execGitSequence(dir, [][]string{{"reset", "--soft", "HEAD~1"}}); err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}
	if !strings.Contains(porcelain(t, dir), "a.txt") {
		t.Fatal("undo should leave the change staged/dirty")
	}

	if out, err := execGitSequence(dir, [][]string{{"stash", "push", "-u"}}); err != nil {
		t.Fatalf("stash: %v\n%s", err, out)
	}
	if strings.Contains(porcelain(t, dir), "a.txt") {
		t.Fatal("stash should clean the tree")
	}
	if out, err := execGitSequence(dir, [][]string{{"stash", "pop"}}); err != nil {
		t.Fatalf("pop: %v\n%s", err, out)
	}
	if !strings.Contains(porcelain(t, dir), "a.txt") {
		t.Fatal("pop should bring the change back")
	}
}

// TestGitSwitchCmds_Tracking pins druk's remote-pick rule: the first
// switch to origin/x creates a tracking local x, the second reuses it.
func TestGitSwitchCmds_Tracking(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	bareOrigin(t, dir)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "push", "-q", "-u", "origin", "feature")
	gitRun(t, dir, "checkout", "-q", "main")
	gitRun(t, dir, "branch", "-q", "-D", "feature")

	cmds := gitSwitchCmds(dir, "origin/feature")
	if got := strings.Join(cmds[0], " "); got != "checkout -b feature --track origin/feature" {
		t.Fatalf("first switch: %q", got)
	}
	if out, err := execGitSequence(dir, cmds); err != nil {
		t.Fatalf("switch: %v\n%s", err, out)
	}
	gitRun(t, dir, "checkout", "-q", "main")
	cmds = gitSwitchCmds(dir, "origin/feature")
	if got := strings.Join(cmds[0], " "); got != "checkout feature" {
		t.Fatalf("second switch: %q", got)
	}
}

// TestGitBranchNames filters HEAD noise, hides remote spellings of
// local branches, and puts the current branch first.
func TestGitBranchNames(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	bareOrigin(t, dir)
	gitRun(t, dir, "push", "-q", "-u", "origin", "main")
	gitRun(t, dir, "branch", "local-only")
	gitRun(t, dir, "checkout", "-q", "-b", "remote-only")
	gitRun(t, dir, "push", "-q", "-u", "origin", "remote-only")
	gitRun(t, dir, "checkout", "-q", "main")
	gitRun(t, dir, "branch", "-q", "-D", "remote-only")

	names := gitBranchNames(dir, "main")
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
}

// TestHandleGitOpDone_Routing pins the three outcomes: success
// flashes, a rejected push offers the pull-then-push confirm, any
// other failure opens the info modal with the explainer headline.
func TestHandleGitOpDone_Routing(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)

	a.gitOpBusy = true
	a.handleGitOpDone(&gitOpDoneEvent{when: time.Now(), label: "Push", okFlash: "Pushed"})
	if a.gitOpBusy {
		t.Fatal("done must clear the busy gate")
	}
	if a.statusMsg != "Pushed" {
		t.Fatalf("success should flash, got %q", a.statusMsg)
	}

	a.handleGitOpDone(&gitOpDoneEvent{
		when: time.Now(), label: "Push", err: errFake,
		output: "! [rejected] main -> main (fetch first)",
	})
	if c := confirmPrefab(t, a); c.Title != "Push rejected" {
		t.Fatalf("rejected push should offer pull-then-push, got title %q", c.Title)
	}
	a.closeAllModals()

	a.handleGitOpDone(&gitOpDoneEvent{
		when: time.Now(), label: "Pull", err: errFake, output: "CONFLICT (content)",
	})
	n := infoPrefab(t, a)
	if len(n.Lines) == 0 || !strings.Contains(n.Lines[0], "merge conflict") {
		t.Fatalf("info should lead with the explainer, got %v", n.Lines)
	}
}

// TestToggleCommitCheck_AndCheckedPaths: absent means checked, a
// toggle writes explicit false, a second flips it back, and
// checkedChangePaths honours the set.
func TestToggleCommitCheck_AndCheckedPaths(t *testing.T) {
	a, modified, untracked := dirtyRepoApp(t)
	a.toggleGitPanel()
	if got := len(a.checkedChangePaths()); got != 2 {
		t.Fatalf("default checked count: got %d, want 2", got)
	}
	a.toggleCommitCheck(modified)
	paths := a.checkedChangePaths()
	if len(paths) != 1 || paths[0] != untracked {
		t.Fatalf("after uncheck: %v", paths)
	}
	a.toggleCommitCheck(modified)
	if got := len(a.checkedChangePaths()); got != 2 {
		t.Fatalf("re-check should restore, got %d", got)
	}
}

// TestMenuGitCommit_OpensPromptAndCommits drives the whole commit flow
// synchronously: prompt opens with the file count, and submitting a
// message runs the real commit (waiting out the async op).
func TestMenuGitCommit_OpensPromptAndCommits(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.menuGitCommit()
	if p := promptPrefab(t, a); p.Title != "Commit message" {
		t.Fatalf("commit prompt should open, got title %q", p.Title)
	}
	promptPrefab(t, a).Field.SetText("from the panel")
	submitPrompt(a)
	waitGitIdle(t, a)
	if out, err := exec.Command("git", "-C", a.rootDir, "log", "-1", "--format=%s").Output(); err != nil ||
		!strings.Contains(string(out), "from the panel") {
		t.Fatalf("commit missing: %v %q", err, out)
	}
}

// waitGitIdle waits for the in-flight git op to post its done event,
// then applies it — tests run without the tcell event loop, so the
// event is fished off the simulation screen's queue manually.
func waitGitIdle(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.gitOpBusy {
		if time.Now().After(deadline) {
			t.Fatal("git op never finished")
		}
		ev := a.screen.PollEvent()
		if e, ok := ev.(*gitOpDoneEvent); ok {
			a.handleGitOpDone(e)
			return
		}
	}
}

// errFake is a sentinel error for handler-routing tests.
var errFake = fakeErr{}

type fakeErr struct{}

func (fakeErr) Error() string { return "exit status 1" }

// waitListPick pumps events until the async branch-list collection
// opens its picker.
func waitListPick(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !pickIsOpen(a) {
		if time.Now().After(deadline) {
			t.Fatal("picker never opened")
		}
		if ev := a.screen.PollEvent(); ev != nil {
			a.handleEvent(ev)
		}
	}
}

// TestGitMergeBranchEndToEnd merges a side branch through the picker
// flow and checks its file lands on the current branch.
func TestGitMergeBranchEndToEnd(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "base.txt"), "b\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	writeFileT(t, filepath.Join(dir, "feat.txt"), "f\n")
	commitAll(t, dir, "feature work")
	gitRun(t, dir, "checkout", "-q", "main")

	a := newTestApp(t, dir)
	a.menuGitMergeBranch()
	waitListPick(t, a)
	names := otherNames(gitBranchNames(dir, a.gitSnap.Branch), a.gitSnap.Branch, false)
	idx := -1
	for i, n := range names {
		if n == "feature" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("feature branch missing from %v", names)
	}
	pickChoose(t, a, idx)
	waitGitIdle(t, a)
	if _, err := os.Stat(filepath.Join(dir, "feat.txt")); err != nil {
		t.Fatalf("merge should bring feat.txt onto main: %v", err)
	}
}

// TestGitDeleteBranchForceOffer pins the two-confirm ladder: -d on an
// unmerged branch fails, the handler offers force delete, accepting it
// runs -D and the branch is gone.
func TestGitDeleteBranchForceOffer(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "base.txt"), "b\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "orphan")
	writeFileT(t, filepath.Join(dir, "o.txt"), "o\n")
	commitAll(t, dir, "unmerged work")
	gitRun(t, dir, "checkout", "-q", "main")

	a := newTestApp(t, dir)
	a.menuGitDeleteBranch()
	waitListPick(t, a)
	names := otherNames(gitBranchNames(dir, a.gitSnap.Branch), a.gitSnap.Branch, true)
	if len(names) != 1 || names[0] != "orphan" {
		t.Fatalf("local candidates: %v", names)
	}
	pickChoose(t, a, 0)
	if !confirmIsOpen(a) {
		t.Fatal("delete needs a confirm first")
	}
	confirmYes(a) // yes, delete
	waitGitIdle(t, a)
	if c := confirmPrefab(t, a); !strings.Contains(c.Message, "Force delete") {
		t.Fatalf("unmerged delete should offer force, got %q", c.Message)
	}
	confirmYes(a) // yes, force
	waitGitIdle(t, a)
	out, _ := exec.Command("git", "-C", dir, "branch", "--list", "orphan").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("orphan should be gone, got %q", out)
	}
}

// TestGitRenameBranchEndToEnd renames the current branch via the
// prompt flow.
func TestGitRenameBranchEndToEnd(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")

	a := newTestApp(t, dir)
	a.menuGitRenameBranch()
	if !promptIsOpen(a) {
		t.Fatal("rename should prompt for the new name")
	}
	promptPrefab(t, a).Field.SetText("mainline")
	submitPrompt(a)
	waitGitIdle(t, a)
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(out)) != "mainline" {
		t.Fatalf("rename failed: %q %v", out, err)
	}
}

// TestDiffBaseStatusAndGuard: with a compare base set, the dirty map
// follows the base (a committed-but-different file shows up), the
// gutter diffs against the base, and committing is gated off.
func TestDiffBaseStatusAndGuard(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	f := filepath.Join(dir, "a.txt")
	writeFileT(t, f, "one\n")
	commitAll(t, dir, "first")
	writeFileT(t, f, "two\n")
	commitAll(t, dir, "second") // worktree clean vs HEAD, dirty vs HEAD~1

	st := loadGitStatus(dir, "HEAD~1")
	if len(st.Files) != 1 {
		t.Fatalf("vs HEAD~1 should show 1 change, got %+v", st.Files)
	}
	if st2 := loadGitStatus(dir, ""); len(st2.Files) != 0 {
		t.Fatalf("vs HEAD should be clean, got %+v", st2.Files)
	}
	if lines := loadGitLineChanges(dir, "HEAD~1", f); len(lines) == 0 {
		t.Fatal("gutter vs base should mark the changed line")
	}

	a := newTestApp(t, dir)
	a.diffBase = "HEAD~1"
	a.menuGitCommit()
	if promptIsOpen(a) {
		t.Fatal("commit must be gated off while comparing against a base")
	}
	if a.statusMsg == "" {
		t.Fatal("the gate should explain itself")
	}
}

// TestComparePickSetsAndClearsBase drives the picker: picking a branch
// sets the base, picking HEAD (row 0) clears it, and picking the
// current branch degrades to HEAD.
func TestComparePickSetsAndClearsBase(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "branch", "other")
	a := newTestApp(t, dir)

	a.openComparePick([]string{"other", "main"})
	if !pickIsOpen(a) {
		t.Fatal("picker should open")
	}
	pickChoose(t, a, 1) // "other"
	if a.diffBase != "other" {
		t.Fatalf("base: got %q, want other", a.diffBase)
	}

	a.openComparePick([]string{"other", "main"})
	pickChoose(t, a, 0) // HEAD row
	if a.diffBase != "" {
		t.Fatalf("HEAD row should clear the base, got %q", a.diffBase)
	}

	a.setDiffBase("main") // current branch → degrades to HEAD
	if a.diffBase != "" {
		t.Fatalf("current-branch base should degrade to HEAD, got %q", a.diffBase)
	}
}
