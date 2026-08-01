// =============================================================================
// File: internal/app/gitlog_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the commit-history modal: the git log / show loaders run
// against a real repo (skipped without git on PATH), the modal flows
// mirror the finder's contract, and drawing runs on a SimulationScreen.

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// historyRepoApp builds a test App over a repo with two commits: c1
// creates both a.txt and b.txt, c2 modifies only a.txt. That shape
// distinguishes branch history (2 commits) from b.txt's file history
// (1 commit).
func historyRepoApp(t *testing.T) (*App, string, string) {
	t.Helper()
	requireGit(t)
	dir := initRepo(t)
	aFile := filepath.Join(dir, "a.txt")
	bFile := filepath.Join(dir, "b.txt")
	writeFileT(t, aFile, "alpha\n")
	writeFileT(t, bFile, "beta\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "c1: seed both")
	writeFileT(t, aFile, "ALPHA\n")
	gitRun(t, dir, "commit", "-q", "-am", "c2: touch a only")
	app := newTestApp(t, dir)
	return app, aFile, bFile
}

// TestLoadGitLog_BranchAndFileScopes pins the two loader modes: the
// branch log sees every commit, a file path narrows it to commits that
// touched that file, newest first.
func TestLoadGitLog_BranchAndFileScopes(t *testing.T) {
	a, aFile, bFile := historyRepoApp(t)
	branch := loadGitLog(a.rootDir, "", gitLogLimit)
	if len(branch) != 2 {
		t.Fatalf("branch log: got %d entries, want 2", len(branch))
	}
	if !strings.Contains(branch[0].Subject, "c2") {
		t.Fatalf("newest first: got %q", branch[0].Subject)
	}
	if branch[0].SHA == "" || branch[0].Age == "" {
		t.Fatalf("entry should carry SHA and age: %+v", branch[0])
	}

	onlyB := loadGitLog(a.rootDir, bFile, gitLogLimit)
	if len(onlyB) != 1 || !strings.Contains(onlyB[0].Subject, "c1") {
		t.Fatalf("file log for b.txt: got %+v, want just c1", onlyB)
	}
	if both := loadGitLog(a.rootDir, aFile, gitLogLimit); len(both) != 2 {
		t.Fatalf("file log for a.txt: got %d entries, want 2", len(both))
	}
}

// TestLoadGitLog_Degrades confirms the best-effort contract: non-repos
// and bad arguments yield nil, never an error surfaced to the UI.
func TestLoadGitLog_Degrades(t *testing.T) {
	if got := loadGitLog(t.TempDir(), "", 10); got != nil {
		t.Fatalf("non-repo log = %v, want nil", got)
	}
	if got := loadGitLog("", "", 10); got != nil {
		t.Fatalf("empty root log = %v, want nil", got)
	}
	if got := loadGitLog(t.TempDir(), "", 0); got != nil {
		t.Fatalf("zero limit log = %v, want nil", got)
	}
}

// TestLoadGitCommitDiff_WholeAndScoped pins the diff loader: a commit's
// full diff includes every touched file, and the path-scoped variant
// only that file's hunks.
func TestLoadGitCommitDiff_WholeAndScoped(t *testing.T) {
	a, aFile, _ := historyRepoApp(t)
	entries := loadGitLog(a.rootDir, "", gitLogLimit)
	c1 := entries[1] // seeds both files

	whole := strings.Join(loadGitCommitDiff(a.rootDir, c1.SHA, ""), "\n")
	if !strings.Contains(whole, "a.txt") || !strings.Contains(whole, "b.txt") {
		t.Fatalf("whole-commit diff should span both files, got:\n%s", whole)
	}
	scoped := strings.Join(loadGitCommitDiff(a.rootDir, c1.SHA, aFile), "\n")
	if !strings.Contains(scoped, "a.txt") || strings.Contains(scoped, "b.txt") {
		t.Fatalf("scoped diff should cover a.txt only, got:\n%s", scoped)
	}
}

// TestOpenGitLog_ListsCommitsAndEmptyFlashes verifies the modal opens
// with entries for a real repo and flashes instead of opening on a
// history-less directory.
func TestOpenGitLog_ListsCommitsAndEmptyFlashes(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	if !a.gitLogOpen || len(a.gitLogEntries) != 2 {
		t.Fatalf("open=%v entries=%d, want open with 2", a.gitLogOpen, len(a.gitLogEntries))
	}
	a.closeGitLog()

	empty := newTestApp(t, t.TempDir())
	empty.openGitLog("History", "")
	if empty.gitLogOpen {
		t.Fatal("no history should not open the modal")
	}
	if !strings.Contains(empty.statusMsg, "No commit history") {
		t.Fatalf("expected a flash, got %q", empty.statusMsg)
	}
}

// TestActivateGitLogRow_OpensCommitDiff drives Enter on a commit: the
// history modal hands off to the diff view carrying that commit's
// changes, with multi-file boundaries for whole-branch commits.
func TestActivateGitLogRow_OpensCommitDiff(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	a.gitLogSelected = 1 // c1 — the two-file commit
	a.handleGitLogKey(keyEv(tcell.KeyEnter, 0))
	if a.gitLogOpen {
		t.Fatal("activation should close the history modal")
	}
	if !a.diffOpen {
		t.Fatal("activation should open the diff view")
	}
	if !strings.Contains(a.diffTitle, "Commit ") {
		t.Fatalf("diff title should name the commit, got %q", a.diffTitle)
	}
	files := 0
	for _, r := range a.diffRows {
		if r.Kind == diffRowFile {
			files++
		}
	}
	if files != 2 {
		t.Fatalf("two-file commit should show 2 boundary rows, got %d", files)
	}
}

// TestMenuFileHistory_ScopesToActiveTab verifies the ≡ row opens the
// active file's history — b.txt sees only the commit that created it —
// and that activating an entry scopes the diff to that file with its
// syntax path attached.
func TestMenuFileHistory_ScopesToActiveTab(t *testing.T) {
	a, _, bFile := historyRepoApp(t)
	a.openFile(bFile)
	item := menuItemByLabel(t, a, "History of this file")
	if !item.enabled(a) {
		t.Fatal("row should be enabled for a file tab in a repo")
	}
	a.menuFileHistory()
	if !a.gitLogOpen || len(a.gitLogEntries) != 1 {
		t.Fatalf("open=%v entries=%d, want open with 1", a.gitLogOpen, len(a.gitLogEntries))
	}
	a.handleGitLogKey(keyEv(tcell.KeyEnter, 0))
	if !a.diffOpen || !strings.Contains(a.diffTitle, "b.txt") {
		t.Fatalf("diff should open scoped to b.txt, title %q", a.diffTitle)
	}
}

// TestMenuCommitHistory_Predicates pins the ≡ rows' gating: both
// history rows grey out outside a repo.
func TestMenuCommitHistory_Predicates(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if menuItemByLabel(t, a, "Commit history").enabled(a) {
		t.Fatal("Commit history should be disabled outside a repo")
	}
	if menuItemByLabel(t, a, "History of this file").enabled(a) {
		t.Fatal("File history should be disabled outside a repo")
	}
}

// TestGitPanelClick_BranchRowOpensPicker verifies the mouse-first
// path: clicking the branch row in the Git panel opens the
// switch-branch form (history moved to the ⋯ popup and the ≡ menu).
func TestGitPanelClick_BranchRowOpensPicker(t *testing.T) {
	a, aFile, _ := historyRepoApp(t)
	writeFileT(t, aFile, "dirty\n") // give the panel something to list
	a.refreshGitStatus()
	// Activate the panel without toggleGitPanel's async status kick —
	// a background `git status` racing t.TempDir cleanup flakes.
	a.gitPanelActive = true
	a.rebuildGitChangesRows()
	gitRun(t, a.rootDir, "branch", "other")
	a.gitPanelClick(5, 1)
	if !a.formOpen {
		t.Fatal("branch row click should open the switch-branch form")
	}
}

// TestHandleGitLogMouse_HoverClickAndDismiss mirrors the finder's mouse
// contract on the history modal.
func TestHandleGitLogMouse_HoverClickAndDismiss(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	mx, my, _, _ := a.gitLogModalRect()

	a.handleGitLogMouse(mx+4, my+4, 0) // hover row 1
	if a.gitLogSelected != 1 {
		t.Fatalf("hover should select row 1, got %d", a.gitLogSelected)
	}
	a.handleGitLogMouse(mx+4, my+4, tcell.Button1)
	if a.gitLogOpen || !a.diffOpen {
		t.Fatal("click should activate the row")
	}

	a.closeAllModals()
	a.openGitLog("History · main", "")
	a.handleGitLogMouse(mx-2, my-2, tcell.Button1)
	if a.gitLogOpen {
		t.Fatal("outside click should dismiss")
	}
}

// TestScrollGitLog_Clamps pins the wheel bounds on a long history.
func TestScrollGitLog_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLogEntries = make([]gitLogEntry, 40)
	a.scrollGitLog(1000)
	if want := 40 - gitLogVisible; a.gitLogScroll != want {
		t.Fatalf("scroll past end: got %d, want %d", a.gitLogScroll, want)
	}
	a.scrollGitLog(-1000)
	if a.gitLogScroll != 0 {
		t.Fatalf("scroll past top: got %d, want 0", a.gitLogScroll)
	}
}

// TestDrawGitLog_Smoke renders the modal and checks the title, a SHA,
// a subject, and an age all land on screen.
func TestDrawGitLog_Smoke(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	a.drawGitLog()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := a.gitLogModalRect()
	if title := screenLine(scr, my+1); !strings.Contains(title, "History · main") {
		t.Fatalf("title row: %q", title)
	}
	row0 := screenLine(scr, my+3)
	if !strings.Contains(row0, a.gitLogEntries[0].SHA) || !strings.Contains(row0, "c2") {
		t.Fatalf("row 0 should show SHA and subject: %q", row0)
	}
	if !strings.Contains(row0, "ago") {
		t.Fatalf("row 0 should show a relative age: %q", row0)
	}
}
