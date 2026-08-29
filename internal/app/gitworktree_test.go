// =============================================================================
// File: internal/app/gitworktree_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-23
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the worktree side: the create/list/remove flows driven
// through the real overlay routing against real repositories, the
// force-remove offer on a dirty worktree, and the list surface driven
// from a git.Fake with no repository behind it. The porcelain parser
// and the add verb's argv are pinned in internal/git.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/git"
)

// TestMenuGitListWorktrees_FromScriptedFake proves the worktree list
// surface goes through readRepo: the rows the info overlay shows are
// the ones a git.Fake scripted, with no repository on disk.
func TestMenuGitListWorktrees_FromScriptedFake(t *testing.T) {
	a, fake := fakeRepoApp(t)
	fake.Script("worktree list --porcelain",
		"worktree /repo\nHEAD abc\nbranch refs/heads/main\n\n"+
			"worktree /repo-side\nHEAD abc\nbranch refs/heads/side\nlocked\n", nil)

	a.menuGitListWorktrees()
	pumpUntil(t, a, "info overlay", func() bool { return infoIsOpen(a) })
	lines := infoPrefab(t, a).Lines
	if len(lines) != 2 || lines[0] != "* /repo  (main)" || lines[1] != "/repo-side  (side)  [locked]" {
		t.Fatalf("info rows should be the scripted worktrees, got %q", lines)
	}
}

// TestDoGitNewWorktree_RefusesUnsafeBranchBeforeRunning pins the
// early flash: a hostile branch name from the prompt is refused on the
// main thread — no op starts, the busy gate stays open — and the verb
// would refuse it again regardless.
func TestDoGitNewWorktree_RefusesUnsafeBranchBeforeRunning(t *testing.T) {
	a, fake := fakeRepoApp(t)
	a.doGitNewWorktree(filepath.Join(t.TempDir(), "wt"), "--output=/tmp/x", true)
	if a.gitOp.Busy() || fake.CallCount() != 0 {
		t.Fatalf("unsafe branch must not start an op, busy=%v calls=%v", a.gitOp.Busy(), fake.Calls())
	}
	if !strings.Contains(a.statusMsg, "unsafe") {
		t.Fatalf("the refusal should flash, got %q", a.statusMsg)
	}
}

// TestDefaultWorktreePath pins the sibling-directory suggestion (repo
// name plus a -wt suffix — the bare name is the main checkout's own
// directory and can never be the target) and the root-of-filesystem
// fallback (no usable parent).
func TestDefaultWorktreePath(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	want := filepath.Join(filepath.Dir(a.rootDir), filepath.Base(a.rootDir)+"-wt")
	if got := a.defaultWorktreePath(); got != want {
		t.Fatalf("default path: got %q, want %q", got, want)
	}
	if got := a.defaultWorktreePath(); got == a.rootDir {
		t.Fatalf("default path must never be the main checkout's own dir: %q", got)
	}
	root := string(filepath.Separator)
	a.rootDir = root
	if got := a.defaultWorktreePath(); got != "" {
		t.Fatalf("root dir: got %q, want empty", got)
	}
}

// TestMenuGitNewWorktree_EndToEnd drives the whole create flow: branch
// picker, path prompt, real `worktree add`, and the worktree on disk.
// The current branch is excluded from the picker (it is already checked
// out in the main worktree), so row 1 is the side branch.
func TestMenuGitNewWorktree_EndToEnd(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	gitRun(t, dir, "checkout", "-q", "main")

	a := newTestApp(t, dir)
	a.menuGitNewWorktree()
	pumpUntil(t, a, "picker", func() bool { return pickIsOpen(a) })
	// Row 0 is "New branch…"; the current branch (main) is excluded, so
	// row 1 is the side branch.
	pickChoose(t, a, 1)
	if !promptIsOpen(a) {
		t.Fatal("path prompt should open after the branch pick")
	}
	promptPrefab(t, a).Field.SetText(filepath.Join(t.TempDir(), "wt"))
	submitPrompt(a)
	pumpUntil(t, a, "git op", idle(&a.gitOp))

	wts, err := git.Open(dir).Worktrees()
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if len(wts) != 2 || wts[1].Branch != "side" {
		t.Fatalf("worktree should check out side, got %+v", wts)
	}
}

// TestMenuGitNewWorktree_NewBranch drives the three-prompt flow: branch
// picker → path → branch name, ending in a fresh branch in a fresh tree.
func TestMenuGitNewWorktree_NewBranch(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")

	a := newTestApp(t, dir)
	a.menuGitNewWorktree()
	pumpUntil(t, a, "picker", func() bool { return pickIsOpen(a) })
	pickChoose(t, a, 0) // New branch…
	if !promptIsOpen(a) {
		t.Fatal("path prompt should open")
	}
	wt := filepath.Join(t.TempDir(), "wt")
	promptPrefab(t, a).Field.SetText(wt)
	submitPrompt(a)
	if !promptIsOpen(a) {
		t.Fatal("branch-name prompt should open")
	}
	promptPrefab(t, a).Field.SetText("fresh")
	submitPrompt(a)
	pumpUntil(t, a, "git op", idle(&a.gitOp))

	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	out, err := exec.Command("git", "-C", wt, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(out)) != "fresh" {
		t.Fatalf("worktree branch: %v %q", err, out)
	}
}

// TestMenuGitListWorktrees_EndToEnd creates a worktree, then opens the
// list and checks both rows render — main starred, side with its branch.
func TestMenuGitListWorktrees_EndToEnd(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	gitRun(t, dir, "checkout", "-q", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, dir, "worktree", "add", "-q", wt, "side")

	a := newTestApp(t, dir)
	a.menuGitListWorktrees()
	pumpUntil(t, a, "info overlay", func() bool { return infoIsOpen(a) })
	lines := strings.Join(infoPrefab(t, a).Lines, "\n")
	if !strings.Contains(lines, "* "+dir) {
		t.Fatalf("main worktree should be starred:\n%s", lines)
	}
	if !strings.Contains(lines, wt+"  (side)") {
		t.Fatalf("side worktree row missing:\n%s", lines)
	}
}

// TestMenuGitRemoveWorktree_EndToEnd picks the side worktree, confirms,
// and checks the directory is gone. The main worktree must never appear
// in the picker.
func TestMenuGitRemoveWorktree_EndToEnd(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	gitRun(t, dir, "checkout", "-q", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, dir, "worktree", "add", "-q", wt, "side")

	a := newTestApp(t, dir)
	a.menuGitRemoveWorktree()
	pumpUntil(t, a, "picker", func() bool { return pickIsOpen(a) })
	if n := len(pickPrefab(t, a).Items); n != 1 {
		t.Fatalf("picker should list only the side worktree, got %d rows", n)
	}
	pickChoose(t, a, 0)
	if !confirmIsOpen(a) {
		t.Fatal("remove needs a confirm first")
	}
	confirmYes(a)
	pumpUntil(t, a, "git op", idle(&a.gitOp))
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
}

// TestMenuGitRemoveWorktree_ForceOffer pins the two-confirm ladder: a
// plain remove on a dirty worktree fails, the handler offers force, and
// accepting it removes the tree.
func TestMenuGitRemoveWorktree_ForceOffer(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	gitRun(t, dir, "checkout", "-q", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, dir, "worktree", "add", "-q", wt, "side")
	writeFileT(t, filepath.Join(wt, "dirty.txt"), "wip\n")

	a := newTestApp(t, dir)
	a.menuGitRemoveWorktree()
	pumpUntil(t, a, "picker", func() bool { return pickIsOpen(a) })
	pickChoose(t, a, 0)
	confirmYes(a)
	pumpUntil(t, a, "git op", idle(&a.gitOp))
	if c := confirmPrefab(t, a); !strings.Contains(c.Message, "Force remove") {
		t.Fatalf("dirty remove should offer force, got %q", c.Message)
	}
	confirmYes(a)
	pumpUntil(t, a, "git op", idle(&a.gitOp))
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
}
