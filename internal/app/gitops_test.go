// =============================================================================
// File: internal/app/gitops_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the git write side: the async-done handler's routing over
// the git package's typed errors, the flows driven through a git.Fake
// with no repository behind them (the seam reaches every verb), and
// end-to-end mutations against real repositories. The verbs' own argv
// and behaviour are pinned in internal/git; these tests are about what
// the app does with them.

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/git"
)

// commitAll makes a commit of everything so the repo has a HEAD.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A", ".")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

// skipInShortMode gates the git suite's end-to-end tests behind -short:
// every test that calls this forks one or more real git processes
// (requireGit + initRepo, or dirtyRepoApp which wraps both), which is
// the "genuinely slow" half of the package's test count -short exists
// to skip. Called before requireGit(t)/dirtyRepoApp(t) so a -short run
// needs neither the extra wall-clock nor git on PATH.
func skipInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("forks real git processes — slow; run without -short")
	}
}

// gatedRunner is a git.Fake whose upstream probe blocks until the test
// releases it. It is how the "no git on the event loop" contract is
// made observable: if menuGitPush ran the probe inline, it could not
// return before release; if it hands the verb to a goroutine, it
// returns at once and the probe completes only after release.
type gatedRunner struct {
	*git.Fake
	release chan struct{}
}

// Output blocks the upstream probe on the gate, then answers from the
// Fake's script like every other read.
func (g *gatedRunner) Output(root string, timeout time.Duration, args ...string) ([]byte, error) {
	if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
		<-g.release
	}
	return g.Fake.Output(root, timeout, args...)
}

// fakeRepoApp builds an App over a plain temp directory with a git.Fake
// installed as its runner and the snapshot marked as a repo, so the
// hasGitRepo gates open without any repository on disk.
func fakeRepoApp(t *testing.T) (*App, *git.Fake) {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	fake := &git.Fake{}
	a.gitRunner = fake
	a.gitSnap.IsRepo = true
	a.gitSnap.Branch = "main"
	return a, fake
}

// TestMenuGitPush_ReturnsBeforeUpstreamProbe is the "no git on the
// event loop to decide argv" contract. Push has to ask git whether the
// branch has an upstream before it knows its own argv; that probe used
// to run inline in a builder, up to the ten-second read timeout with
// the UI frozen. Now the verb decides on runGitOp's goroutine: the
// menu handler returns while the probe is still blocked on the gate,
// and the push that follows — the first-push --set-upstream shape —
// reaches the Fake only after the gate opens.
func TestMenuGitPush_ReturnsBeforeUpstreamProbe(t *testing.T) {
	a, fake := fakeRepoApp(t)
	gate := &gatedRunner{Fake: fake, release: make(chan struct{})}
	a.gitRunner = gate
	fake.Script("symbolic-ref --short HEAD", "main\n", nil)
	fake.Script("push --set-upstream origin main", "", nil)

	returned := make(chan struct{})
	go func() {
		a.menuGitPush()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("menuGitPush blocked on the upstream probe — git ran on the event loop to decide argv")
	}
	if fake.Called("push --set-upstream origin main") {
		t.Fatal("the push cannot have run before its upstream probe was answered")
	}
	close(gate.release)
	waitGitIdle(t, a)
	if !fake.Called("rev-parse --abbrev-ref @{upstream}") || !fake.Called("push --set-upstream origin main") {
		t.Fatalf("push must reach the runner seam through readRepo, ran %v", fake.Calls())
	}
	if a.statusMsg != "Pushed" {
		t.Fatalf("success should flash, got %q", a.statusMsg)
	}
}

// TestMenuGitPush_ScriptedRejectionOffersPullAndPush drives the push
// flow against a scripted non-fast-forward refusal with no repository
// behind it: the done handler reads the OpError's flag (never stderr),
// offers the one-gesture fix, and accepting it runs the merge-pull then
// the push through the same seam.
func TestMenuGitPush_ScriptedRejectionOffersPullAndPush(t *testing.T) {
	a, fake := fakeRepoApp(t)
	fake.Script("rev-parse --abbrev-ref @{upstream}", "origin/main\n", nil)
	fake.Script("push", "! [rejected] main -> main (fetch first)\n", errors.New("exit status 1"))

	a.menuGitPush()
	waitGitIdle(t, a)
	c := confirmPrefab(t, a)
	if c.Title != "Push rejected" {
		t.Fatalf("a non-fast-forward push should offer pull-then-push, got %q", c.Title)
	}

	fake.Script("pull --no-rebase --no-edit", "Merge made by the 'ort' strategy.\n", nil)
	fake.Script("push", "", nil)
	confirmYes(a)
	waitGitIdle(t, a)
	if !fake.Called("pull --no-rebase --no-edit") {
		t.Fatalf("accepting the offer must pull first, ran %v", fake.Calls())
	}
	if a.statusMsg != "Pulled and pushed" {
		t.Fatalf("the accepted offer should flash its success, got %q", a.statusMsg)
	}
}

// TestMenuGitSwitchBranch_ListsScriptedBranches drives the branch
// picker from a scripted ref list: the names the Fake answers with are
// the rows the picker shows, and picking one runs the switch verb —
// whose own existence probe is answered by the Fake too.
func TestMenuGitSwitchBranch_ListsScriptedBranches(t *testing.T) {
	a, fake := fakeRepoApp(t)
	fake.Script("for-each-ref --format=%(HEAD)%00%(refname)%00%(symref) refs/heads refs/remotes",
		"*\x00refs/heads/main\x00\n \x00refs/heads/side\x00\n \x00refs/remotes/origin/remote-only\x00\n", nil)
	fake.Script("checkout -b remote-only --track origin/remote-only --", "", nil)

	a.menuGitSwitchBranch()
	waitListPick(t, a)
	items := pickPrefab(t, a).Items
	if len(items) != 3 || items[0].Label != "main" || items[1].Label != "side" || items[2].Label != "origin/remote-only" {
		t.Fatalf("picker rows should be the scripted branches, got %+v", items)
	}
	pickChoose(t, a, 2)
	waitGitIdle(t, a)
	if !fake.Called("checkout -b remote-only --track origin/remote-only --") {
		t.Fatalf("picking the remote spelling should create the tracking local, ran %v", fake.Calls())
	}
	if a.statusMsg != "On remote-only" {
		t.Fatalf("success should flash the local name, got %q", a.statusMsg)
	}
}

// TestSetDiffBase_RejectsUnsafeRef pins where the compare base gets
// validated. Once set it is pasted onto the argv of every diff the
// editor runs — tree tint, gutter, panel, diff view — and stays there
// until cleared, so a hostile ref has to be refused at the door rather
// than at four call sites that would each have to remember.
func TestSetDiffBase_RejectsUnsafeRef(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	a := newTestApp(t, dir)

	a.setDiffBase("--output=/tmp/x")
	if a.diffBase != "" {
		t.Fatalf("unsafe base was accepted: %q", a.diffBase)
	}
	a.setDiffBase("HEAD~1")
	if a.diffBase != "HEAD~1" {
		t.Fatalf("legitimate base rejected: %q", a.diffBase)
	}
}

// TestHandleGitOpDone_Routing pins the four outcomes: success flashes,
// a push whose OpError carries NonFastForward offers the pull-then-push
// confirm, any other OpError opens the info modal led by its advice
// with git's words below, and a refusal that never ran git (an unsafe
// ref) shows its one line. The handler reads flags and advice, never
// the output text — the rejection here is recognisable only by its
// flag.
func TestHandleGitOpDone_Routing(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.gitOpBusy = true
	a.handleGitOpDone(&gitOpDoneEvent{when: time.Now(), label: "Push", okFlash: "Pushed"})
	if a.gitOpBusy {
		t.Fatal("done must clear the busy gate")
	}
	if a.statusMsg != "Pushed" {
		t.Fatalf("success should flash, got %q", a.statusMsg)
	}

	a.handleGitOpDone(&gitOpDoneEvent{
		when: time.Now(), label: "Push",
		err: &git.OpError{Op: "Push", Output: "opaque", Advice: "n/a", NonFastForward: true},
	})
	if c := confirmPrefab(t, a); c.Title != "Push rejected" {
		t.Fatalf("rejected push should offer pull-then-push, got title %q", c.Title)
	}
	a.closeAllModals()

	a.handleGitOpDone(&gitOpDoneEvent{
		when: time.Now(), label: "Pull",
		err: &git.OpError{Op: "Pull", Output: "CONFLICT (content)\n\nAutomatic merge failed", Advice: "merge conflict — fix the marked files, then commit"},
	})
	n := infoPrefab(t, a)
	if len(n.Lines) < 3 || !strings.Contains(n.Lines[0], "merge conflict") || n.Lines[2] != "CONFLICT (content)" {
		t.Fatalf("info should lead with the advice and carry git's words, got %v", n.Lines)
	}
	a.closeAllModals()

	a.handleGitOpDone(&gitOpDoneEvent{when: time.Now(), label: "Merge", err: git.ErrUnsafeRef})
	if n := infoPrefab(t, a); len(n.Lines) != 1 || !strings.Contains(n.Lines[0], "unsafe") {
		t.Fatalf("a refusal that never ran git should show its one line, got %v", n.Lines)
	}
}

// TestToggleCommitCheck_AndCheckedPaths: absent means checked, a
// toggle writes explicit false, a second flips it back, and
// checkedChangePaths honours the set.
func TestToggleCommitCheck_AndCheckedPaths(t *testing.T) {
	skipInShortMode(t)
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
	skipInShortMode(t)
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
	skipInShortMode(t)
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
	all, err := git.Open(dir).Branches()
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	names := otherNames(all, a.gitSnap.Branch, false)
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
	skipInShortMode(t)
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
	all, err := git.Open(dir).Branches()
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	names := otherNames(all, a.gitSnap.Branch, true)
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
	skipInShortMode(t)
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
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	f := filepath.Join(dir, "a.txt")
	writeFileT(t, f, "one\n")
	commitAll(t, dir, "first")
	writeFileT(t, f, "two\n")
	commitAll(t, dir, "second") // worktree clean vs HEAD, dirty vs HEAD~1

	st := git.Open(dir).Status("HEAD~1")
	if len(st.Files) != 1 {
		t.Fatalf("vs HEAD~1 should show 1 change, got %+v", st.Files)
	}
	if st2 := git.Open(dir).Status(""); len(st2.Files) != 0 {
		t.Fatalf("vs HEAD should be clean, got %+v", st2.Files)
	}
	if lines := repoLineChanges(git.Open(dir), "HEAD~1", f); len(lines) == 0 {
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
	skipInShortMode(t)
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
