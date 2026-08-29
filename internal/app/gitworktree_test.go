// =============================================================================
// File: internal/app/gitworktree_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-23
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the worktree side: the porcelain parser, the command builder's
// argv hardening, the create/list/remove flows driven through the real
// overlay routing, and the force-remove offer on a dirty worktree.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestGitWorktreeAddCmds pins the argv for every create shape: new
// branch, existing local, and remote-tracking pick (tracking local
// created on first use, reused after).
func TestGitWorktreeAddCmds(t *testing.T) {
	skipInShortMode(t)
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "a.txt"), "x\n")
	commitAll(t, dir, "seed")
	bareOrigin(t, dir)
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	gitRun(t, dir, "push", "-q", "-u", "origin", "feature")
	gitRun(t, dir, "checkout", "-q", "main")
	gitRun(t, dir, "branch", "-q", "-D", "feature")

	// New branch: -b value, no separator protects it — SafeRef covers it.
	cmds := gitWorktreeAddCmds(dir, "/r/wt", "side", true)
	if got := strings.Join(cmds[0], " "); got != "worktree add /r/wt -b side --" {
		t.Fatalf("new branch: %q", got)
	}
	// Existing local: plain positional after the path.
	cmds = gitWorktreeAddCmds(dir, "/r/wt", "main", false)
	if got := strings.Join(cmds[0], " "); got != "worktree add /r/wt main --" {
		t.Fatalf("local: %q", got)
	}
	// Remote pick, local absent: create the tracking local.
	cmds = gitWorktreeAddCmds(dir, "/r/wt", "origin/feature", false)
	if got := strings.Join(cmds[0], " "); got != "worktree add /r/wt -b feature --track origin/feature --" {
		t.Fatalf("remote first: %q", got)
	}
}

// TestGitWorktreeAddCmds_HardensArgv is the regression test for the
// flag-injection hole: a clone can ship a branch named --output=/tmp/x,
// and a worktree path prompt could carry anything. Unsafe names yield no
// command at all; a path in the option position is refused.
func TestGitWorktreeAddCmds_HardensArgv(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"--output=/tmp/x", "-b", "", "origin/--output=/tmp/x"} {
		if got := gitWorktreeAddCmds(dir, "/r/wt", bad, false); got != nil {
			t.Fatalf("gitWorktreeAddCmds(%q) = %v, want nil", bad, got)
		}
	}
	if got := gitWorktreeAddCmds(dir, "/r/wt", "--output=/tmp/x", true); got != nil {
		t.Fatalf("new branch %q = %v, want nil", "--output=/tmp/x", got)
	}
	if got := gitWorktreeAddCmds(dir, "-evil", "main", false); got != nil {
		t.Fatalf("option-position path = %v, want nil", got)
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
	waitListPick(t, a)
	// Row 0 is "New branch…"; the current branch (main) is excluded, so
	// row 1 is the side branch.
	pickChoose(t, a, 1)
	if !promptIsOpen(a) {
		t.Fatal("path prompt should open after the branch pick")
	}
	promptPrefab(t, a).Field.SetText(filepath.Join(t.TempDir(), "wt"))
	submitPrompt(a)
	waitGitIdle(t, a)

	out, err := execGitSequence(dir, [][]string{{"worktree", "list", "--porcelain"}})
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if !strings.Contains(out, "branch refs/heads/side") {
		t.Fatalf("worktree should check out side:\n%s", out)
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
	waitListPick(t, a)
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
	waitGitIdle(t, a)

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
	waitInfo(t, a)
	lines := strings.Join(infoPrefab(t, a).Lines, "\n")
	if !strings.Contains(lines, "* "+dir) {
		t.Fatalf("main worktree should be starred:\n%s", lines)
	}
	if !strings.Contains(lines, wt+"  (side)") {
		t.Fatalf("side worktree row missing:\n%s", lines)
	}
}

// waitInfo pumps events until the async worktree list opens its overlay.
func waitInfo(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !infoIsOpen(a) {
		if time.Now().After(deadline) {
			t.Fatal("info overlay never opened")
		}
		if ev := a.screen.PollEvent(); ev != nil {
			a.handleEvent(ev)
		}
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
	waitListPick(t, a)
	if n := len(pickPrefab(t, a).Items); n != 1 {
		t.Fatalf("picker should list only the side worktree, got %d rows", n)
	}
	pickChoose(t, a, 0)
	if !confirmIsOpen(a) {
		t.Fatal("remove needs a confirm first")
	}
	confirmYes(a)
	waitGitIdle(t, a)
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
	waitListPick(t, a)
	pickChoose(t, a, 0)
	confirmYes(a)
	waitGitIdle(t, a)
	if c := confirmPrefab(t, a); !strings.Contains(c.Message, "Force remove") {
		t.Fatalf("dirty remove should offer force, got %q", c.Message)
	}
	confirmYes(a)
	waitGitIdle(t, a)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, stat err = %v", err)
	}
}

// TestExplainGit_Worktree pins the worktree-specific headlines.
func TestExplainGit_Worktree(t *testing.T) {
	cases := []struct{ out, want string }{
		{"fatal: '/r/side' is already checked out at '/r/other'", "that branch is checked out in another worktree — remove or switch it first"},
		{"fatal: refname 'side' already exists", "that path or branch already exists — pick another"},
		{"fatal: worktree '/r/side' is locked", "that worktree is locked — unlock it first (git worktree unlock)"},
	}
	for _, c := range cases {
		if got := explainGit(c.out); got != c.want {
			t.Errorf("explainGit(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
