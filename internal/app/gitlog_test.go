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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/diff"
)

// gitLogOv returns the open commit-history overlay, failing the test
// when none is up.
func gitLogOv(t *testing.T, a *App) *gitLogOverlay {
	t.Helper()
	g, ok := a.overlays.Top().(*gitLogOverlay)
	if !ok {
		t.Fatalf("no git log overlay open; top = %T", a.overlays.Top())
	}
	return g
}

// gitLogIsOpen reports whether the commit-history overlay is up.
func gitLogIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*gitLogOverlay)
	return ok
}

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
// only that file's hunks. The loader hands back the parsed model now, so
// "which files" is a list of paths rather than a substring search.
func TestLoadGitCommitDiff_WholeAndScoped(t *testing.T) {
	a, aFile, _ := historyRepoApp(t)
	entries := loadGitLog(a.rootDir, "", gitLogLimit)
	c1 := entries[1] // seeds both files

	paths := func(p diff.Patch) []string {
		var out []string
		for _, f := range p.Files {
			out = append(out, f.Path())
		}
		return out
	}
	whole := paths(loadGitCommitDiff(a.rootDir, c1.SHA, ""))
	if len(whole) != 2 || whole[0] != "a.txt" || whole[1] != "b.txt" {
		t.Fatalf("whole-commit diff should span both files, got %q", whole)
	}
	scoped := paths(loadGitCommitDiff(a.rootDir, c1.SHA, aFile))
	if len(scoped) != 1 || scoped[0] != "a.txt" {
		t.Fatalf("scoped diff should cover a.txt only, got %q", scoped)
	}
}

// TestLoadGitCommitDiff_RefusesOptionLookalikeSHA pins the defense-in-depth
// gate: loadGitCommitDiff routes sha through git.SafeRef before it ever
// reaches argv. The test runs against a REAL repo (not a non-repo temp
// dir) on purpose — git resolves "is this even a repository?" before it
// parses `show`'s own options, so a non-repo root fails identically
// whether or not the gate fires and can't prove the gate did anything.
// Two of the three cases carry an observable side effect that only
// happens if the raw string reaches argv: "--output=pwned" makes a real
// `git show` write a file (proven manually: it does, in a real repo,
// pre-gate), and "-p" makes `git show --format= -p` silently show
// HEAD's diff instead of refusing — both would corrupt this test's repo
// or return a wrong-but-non-nil diff if the gate didn't fire first.
func TestLoadGitCommitDiff_RefusesOptionLookalikeSHA(t *testing.T) {
	a, aFile, _ := historyRepoApp(t)

	for _, sha := range []string{"--output=pwned", "", "-p"} {
		if got := loadGitCommitDiff(a.rootDir, sha, ""); !got.Empty() {
			t.Fatalf("sha %q: got %v, want nothing (refused before any subprocess)", sha, got)
		}
	}
	if _, err := os.Stat(filepath.Join(a.rootDir, "pwned")); err == nil {
		t.Fatal("--output=pwned must not reach argv: file was written")
	}

	entries := loadGitLog(a.rootDir, "", gitLogLimit)
	if len(entries) == 0 {
		t.Fatal("need at least one real commit for the positive case")
	}
	// The trailing "--" always precedes the (possibly absent) path
	// argument; both branches must still resolve correctly with it in
	// place — an unscoped diff (no path after "--") and a path-scoped
	// one (the path lands after it) exercise both shapes.
	if whole := loadGitCommitDiff(a.rootDir, entries[0].SHA, ""); whole.Empty() {
		t.Fatalf("valid sha, no path: got %v, want a diff", whole)
	}
	scoped := loadGitCommitDiff(a.rootDir, entries[0].SHA, aFile)
	if scoped.Empty() {
		t.Fatalf("valid sha, scoped path: got %v, want a diff", scoped)
	}
}

// TestOpenGitLog_ListsCommitsAndEmptyFlashes verifies the modal opens
// with entries for a real repo and flashes instead of opening on a
// history-less directory.
func TestOpenGitLog_ListsCommitsAndEmptyFlashes(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	if !gitLogIsOpen(a) || len(gitLogOv(t, a).entries) != 2 {
		t.Fatalf("want open with 2 entries, top=%T", a.overlays.Top())
	}
	pressEsc(a)

	empty := newTestApp(t, t.TempDir())
	empty.openGitLog("History", "")
	if gitLogIsOpen(empty) {
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
	gitLogOv(t, a).selected = 1 // c1 — the two-file commit
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if gitLogIsOpen(a) {
		t.Fatal("activation should close the history modal")
	}
	if !diffIsOpen(a) {
		t.Fatal("activation should open the diff view")
	}
	if !strings.Contains(diffOv(t, a).title, "Commit ") {
		t.Fatalf("diff title should name the commit, got %q", diffOv(t, a).title)
	}
	files := 0
	for _, r := range diffOv(t, a).rows {
		if r.Kind == diff.RowFile {
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
// syntax path attached. The syntax path is the half that used to go
// unchecked: gitLogOverlay.activate hands g.path to openDiffView as
// langPath, and an empty one silently drops highlighting for the whole
// diff while the title still reads right.
func TestMenuFileHistory_ScopesToActiveTab(t *testing.T) {
	a, _, bFile := historyRepoApp(t)
	a.openFile(bFile)
	item := menuItemByLabel(t, a, "History of this file")
	if !item.enabled(a) {
		t.Fatal("row should be enabled for a file tab in a repo")
	}
	a.menuFileHistory()
	if !gitLogIsOpen(a) || len(gitLogOv(t, a).entries) != 1 {
		t.Fatalf("want open with 1 entry, top=%T", a.overlays.Top())
	}
	// The scoping path is also the syntax path the diff inherits.
	if got := gitLogOv(t, a).path; got != bFile {
		t.Fatalf("history scope: got %q, want the active tab %q", got, bFile)
	}
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if !diffIsOpen(a) || !strings.Contains(diffOv(t, a).title, "b.txt") {
		t.Fatalf("diff should open scoped to b.txt, title %q", diffOv(t, a).title)
	}
	// Precomputed syntax styles exist only when a non-empty langPath
	// reached openDiffView — one style row per diff row, per side.
	d := diffOv(t, a)
	if len(d.rows) == 0 {
		t.Fatal("diff should have rows to style")
	}
	if len(d.leftStyles) != len(d.rows) || len(d.rightStyles) != len(d.rows) {
		t.Fatalf("diff should carry %s's syntax styles on both sides: got %d/%d style rows for %d diff rows",
			filepath.Base(bFile), len(d.leftStyles), len(d.rightStyles), len(d.rows))
	}
}

// TestMenuCommitHistory_Predicates pins the ≡ rows' gating: both
// history rows — demoted into the Git… drill-in by the menu redesign —
// stay disabled outside a repo, which is what keeps them out of the
// pick entirely there.
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
// path: clicking the branch row in the Git panel opens the filterable
// switch-branch picker (history moved to the ⋯ popup and the ≡ menu).
func TestGitPanelClick_BranchRowOpensPicker(t *testing.T) {
	a, aFile, _ := historyRepoApp(t)
	writeFileT(t, aFile, "dirty\n") // give the panel something to list
	a.refreshGitStatus()
	// Activate the panel without toggleGitPanel's async status kick —
	// a background `git status` racing t.TempDir cleanup flakes.
	a.gitPanel.active = true
	a.rebuildGitChangesRows()
	gitRun(t, a.rootDir, "branch", "other")
	a.gitPanelClick(5, 1)
	deadline := time.Now().Add(5 * time.Second)
	for !pickIsOpen(a) {
		if time.Now().After(deadline) {
			t.Fatal("branch row click should open the switch-branch picker")
		}
		if ev := a.screen.PollEvent(); ev != nil {
			a.handleEvent(ev)
		}
	}
}

// TestHandleGitLogMouse_HoverClickAndDismiss mirrors the finder's mouse
// contract on the history modal.
func TestHandleGitLogMouse_HoverClickAndDismiss(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	g := gitLogOv(t, a)
	gr := g.rect()
	mx, my := gr.X, gr.Y

	g.HandleMouse(mx+4, my+4, 0) // hover row 1
	if g.selected != 1 {
		t.Fatalf("hover should select row 1, got %d", g.selected)
	}
	g.HandleMouse(mx+4, my+4, tcell.Button1)
	if gitLogIsOpen(a) || !diffIsOpen(a) {
		t.Fatal("click should activate the row")
	}

	a.closeAllModals()
	a.openGitLog("History · main", "")
	gitLogOv(t, a).HandleMouse(mx-2, my-2, tcell.Button1)
	if gitLogIsOpen(a) {
		t.Fatal("outside click should dismiss")
	}
}

// TestScrollGitLog_Clamps pins the wheel bounds on a long history.
func TestScrollGitLog_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	g := &gitLogOverlay{app: a, entries: make([]gitLogEntry, 40)}
	g.scrollBy(1000)
	if want := 40 - gitLogVisible; g.scroll != want {
		t.Fatalf("scroll past end: got %d, want %d", g.scroll, want)
	}
	g.scrollBy(-1000)
	if g.scroll != 0 {
		t.Fatalf("scroll past top: got %d, want 0", g.scroll)
	}
}

// TestDrawGitLog_Smoke renders the modal and checks the title, a SHA,
// a subject, and an age all land on screen.
func TestDrawGitLog_Smoke(t *testing.T) {
	a, _, _ := historyRepoApp(t)
	a.openGitLog("History · main", "")
	gitLogOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	my := gitLogOv(t, a).rect().Y
	if title := screenLine(scr, my+1); !strings.Contains(title, "History · main") {
		t.Fatalf("title row: %q", title)
	}
	row0 := screenLine(scr, my+3)
	if !strings.Contains(row0, gitLogOv(t, a).entries[0].SHA) || !strings.Contains(row0, "c2") {
		t.Fatalf("row 0 should show SHA and subject: %q", row0)
	}
	if !strings.Contains(row0, "ago") {
		t.Fatalf("row 0 should show a relative age: %q", row0)
	}
}
