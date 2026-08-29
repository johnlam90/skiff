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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/diff"
	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/scrollbar"
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

// TestOpenGitLog_FromScriptedFake proves the history surface — the
// overlay's rows and the commit diff a row opens — is driven entirely
// through readRepo: a git.Fake scripts the log and the show against a
// plain temp directory with no repository behind it.
func TestOpenGitLog_FromScriptedFake(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := &git.Fake{}
	fake.Script("log --format=%h%x00%s%x00%cr -n 200 --",
		"abc1234\x00scripted subject\x002 days ago\n"+
			"def5678\x00older\x003 days ago\n", nil)
	fake.Script("show --format= --src-prefix=a/ --dst-prefix=b/ abc1234 --", strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 0000000..1111111 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1 +1 @@",
		"-old",
		"+scripted line",
		"",
	}, "\n"), nil)
	a.gitRunner = fake

	a.openGitLog("History · main", "")
	g := gitLogOv(t, a)
	if len(g.entries) != 2 || g.entries[0].Hash != "abc1234" || g.entries[0].Subject != "scripted subject" {
		t.Fatalf("overlay rows should be the scripted log, got %+v", g.entries)
	}
	g.activate()
	if !diffIsOpen(a) {
		t.Fatalf("activating a row should open the scripted commit diff; top = %T", a.overlays.Top())
	}
	if body := strings.Join(diffOv(t, a).unified, "\n"); !strings.Contains(body, "+scripted line") {
		t.Fatalf("diff body should come from the scripted show, got:\n%s", body)
	}
}

// TestGitLogActivate_ReportsGitFailure pins the difference between an
// empty diff and a git that said no: the first flashes "no diff", the
// second flashes git's reason, because a silent empty answer would send
// the user looking for a problem in the commit rather than in git.
func TestGitLogActivate_ReportsGitFailure(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := &git.Fake{}
	fake.Script("log --format=%h%x00%s%x00%cr -n 200 --", "abc1234\x00s\x00now\n", nil)
	a.gitRunner = fake
	a.openGitLog("History · main", "")
	gitLogOv(t, a).activate()
	if diffIsOpen(a) || !strings.Contains(a.statusMsg, "Couldn't load commit abc1234") {
		t.Fatalf("an unscripted show is git saying no; flash = %q", a.statusMsg)
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
	gitLogOv(t, a).Select(1) // c1 — the two-file commit
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
	if g.Sel() != 1 {
		t.Fatalf("hover should select row 1, got %d", g.Sel())
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
	g := &gitLogOverlay{app: a, entries: make([]git.Commit, 40)}
	g.sync()
	g.ScrollBy(1000)
	if want := 40 - gitLogVisible; g.Scroll() != want {
		t.Fatalf("scroll past end: got %d, want %d", g.Scroll(), want)
	}
	g.ScrollBy(-1000)
	if g.Scroll() != 0 {
		t.Fatalf("scroll past top: got %d, want 0", g.Scroll())
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
	if !strings.Contains(row0, gitLogOv(t, a).entries[0].Hash) || !strings.Contains(row0, "c2") {
		t.Fatalf("row 0 should show SHA and subject: %q", row0)
	}
	if !strings.Contains(row0, "ago") {
		t.Fatalf("row 0 should show a relative age: %q", row0)
	}
}

// gitLogBarColumn draws g and reads its scroll-indicator column back
// off the screen across the commit rows.
func gitLogBarColumn(t *testing.T, a *App, g *gitLogOverlay) string {
	t.Helper()
	a.screen.Clear()
	g.Draw(a.screen)
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	r := g.rect()
	barX := overlay.BarColumn(r)
	var b strings.Builder
	for row := range g.Visible() {
		if rs := cells[(r.Y+3+row)*w+barX].Runes; len(rs) > 0 {
			b.WriteRune(rs[0])
		}
	}
	return b.String()
}

// TestDrawGitLog_ScrollbarShowsOnlyWhenHistoryOverflows pins the
// history overlay's half of the shared indicator. A 200-commit log
// scrolled to the middle of a 14-row window is the case where "there is
// more below" is not obvious from anything else on screen; a history
// short enough to fit must paint nothing, so the padding column stays
// ordinary row surface.
func TestDrawGitLog_ScrollbarShowsOnlyWhenHistoryOverflows(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	long := &gitLogOverlay{app: a, title: "History", entries: make([]git.Commit, 60)}
	long.sync()
	// Scroll the way a user does — the wheel — and leave the highlight
	// on row 0, which is the state a fresh open is in and the one that
	// used to have the window snapped back on the next frame.
	long.ScrollBy(20)
	col := gitLogBarColumn(t, a, long)
	if long.Scroll() != 20 {
		t.Fatalf("the paint moved the window: scroll %d, want 20", long.Scroll())
	}
	if !strings.ContainsRune(col, scrollbar.Thumb) || !strings.ContainsRune(col, scrollbar.Track) {
		t.Fatalf("60 commits in a %d-row window must paint a bar, got %q", long.Visible(), col)
	}
	wantStart, wantLen, ok := scrollbar.Geom(long.Len(), long.Visible(), long.Scroll())
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row, got := range []rune(col) {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got != want {
			t.Fatalf("bar row %d = %q, want %q (col %q)", row, got, want, col)
		}
	}

	short := &gitLogOverlay{app: a, title: "History", entries: make([]git.Commit, 3)}
	short.sync()
	col = gitLogBarColumn(t, a, short)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a history that fits must paint no bar, got %q", col)
	}
}

// TestGitLogBarPress_ScrollsInsteadOfOpeningTheCommitBehindIt is the
// click-steal regression: the bar's column is inside the commit rows,
// so without the hit-test claiming it first, reaching for the thumb
// would open the diff of whichever commit sits behind it.
func TestGitLogBarPress_ScrollsInsteadOfOpeningTheCommitBehindIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	g := &gitLogOverlay{app: a, title: "History", entries: make([]git.Commit, 60)}
	g.sync()
	a.overlays.Open(g)
	r := g.rect()
	barX := overlay.BarColumn(r)

	g.HandleMouse(barX, r.Y+3+g.Visible()-1, tcell.Button1)
	// Paint before asserting. A scroll that survives the press but not
	// the next frame is a scrollbar the user cannot drag at all, and
	// asserting straight off the press would never notice.
	g.Draw(a.screen)
	if want := g.MaxScroll(); g.Scroll() != want {
		t.Fatalf("a press at the foot of the bar: scroll %d after a paint, want %d", g.Scroll(), want)
	}
	if !gitLogIsOpen(a) {
		t.Fatal("a bar press must not activate a row or dismiss the overlay")
	}
	before := g.Sel()
	g.HandleMouse(barX, r.Y+3, tcell.ButtonNone)
	if g.Sel() != before {
		t.Fatalf("hovering the bar moved the highlight: %d → %d", before, g.Sel())
	}
}

// gitLogEntriesN builds n identifiable commit rows so a test can read
// off the screen WHICH commit the window starts on, rather than only
// trusting the offset the overlay reports about itself.
func gitLogEntriesN(n int) []git.Commit {
	out := make([]git.Commit, n)
	for i := range out {
		out[i] = git.Commit{
			Hash:     fmt.Sprintf("sha%03d", i),
			Subject: fmt.Sprintf("commit %d", i),
			When:     "1 day ago",
		}
	}
	return out
}

// TestGitLogWheel_SurvivesAPaint is the other half of the same defect
// the bar press exposes. Draw used to call EnsureVisible on every
// frame, so with the selection on row 0 — the state on every fresh
// open — any offset the wheel set was pulled straight back to 0 by the
// next paint: the history scrolled for zero frames and the user saw
// nothing move. Scrolling is looking, not choosing (see overlay.List),
// so a wheel tick has to outlive the frame that follows it, and the
// commit it brings to the top of the window has to be the one painted
// there.
func TestGitLogWheel_SurvivesAPaint(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	g := &gitLogOverlay{app: a, title: "History", entries: gitLogEntriesN(60)}
	g.sync()
	a.overlays.Open(g)
	r := g.rect()

	g.HandleMouse(r.X+4, r.Y+4, tcell.WheelDown)
	scrolled := g.Scroll()
	if scrolled == 0 {
		t.Fatal("the wheel should have scrolled the history")
	}
	if g.Sel() != 0 {
		t.Fatalf("the wheel must not drag the highlight, got %d", g.Sel())
	}

	g.Draw(a.screen)
	if g.Scroll() != scrolled {
		t.Fatalf("a paint undid the wheel: scroll %d → %d", scrolled, g.Scroll())
	}
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	top := screenLine(scr, r.Y+3)
	if want := g.entries[scrolled].Hash; !strings.Contains(top, want) {
		t.Fatalf("the window's first row should paint %q, got %q", want, top)
	}
}
