// =============================================================================
// File: internal/app/gitchanges_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the sidebar Git panel. Pure row-building and letter/color
// mapping run against hand-built maps; the end-to-end toggle/activate
// flows run against a real `git init`'d repo (skipped when git isn't on
// PATH), mirroring gitstatus_test.go's approach.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// dirtyRepoApp builds a test App rooted at a fresh git repo containing
// one committed-then-modified file (M) and one untracked file (A).
// Returns the app plus the two absolute paths.
func dirtyRepoApp(t *testing.T) (*App, string, string) {
	t.Helper()
	requireGit(t)
	dir := initRepo(t)
	modified := filepath.Join(dir, "modified.txt")
	writeFileT(t, modified, "one\ntwo\nthree\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	writeFileT(t, modified, "one\nTWO\nthree\n")
	untracked := filepath.Join(dir, "untracked.txt")
	writeFileT(t, untracked, "new\n")
	a := newTestApp(t, dir)
	return a, modified, untracked
}

// TestBuildGitChangesRows_SortsByRelPath pins the display order: rows
// come back sorted by relative path regardless of map iteration order,
// so the list is stable across refreshes and the eye can track a file.
func TestBuildGitChangesRows_SortsByRelPath(t *testing.T) {
	root := t.TempDir()
	dirty := map[string]filetree.GitChangeKind{
		filepath.Join(root, "zeta.go"):       filetree.GitChangeModified,
		filepath.Join(root, "alpha.go"):      filetree.GitChangeAdded,
		filepath.Join(root, "sub", "mid.go"): filetree.GitChangeDeleted,
	}
	rows := buildGitChangesRows(dirty, root, nil)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []string{"alpha.go", "sub/mid.go", "zeta.go"}
	for i, w := range want {
		if rows[i].Rel != w {
			t.Fatalf("row %d: got %q, want %q", i, rows[i].Rel, w)
		}
	}
}

// TestBuildGitChangesRows_MarksUntrackedDirs verifies a dirty path the
// collection flagged as a directory gets IsDir plus a trailing slash —
// that's how the row communicates "this reveals in the tree, not a
// diff". A deleted path (absent from the carried map, because its stat
// failed off-thread) must never be marked as a directory. Neither path
// exists on disk here: the builder is pure now, the stat happened in
// collectGitStatus's goroutine.
func TestBuildGitChangesRows_MarksUntrackedDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	sub := filepath.Join(root, "newdir")
	dirty := map[string]filetree.GitChangeKind{
		sub:                             filetree.GitChangeAdded,
		filepath.Join(root, "gone.txt"): filetree.GitChangeDeleted,
	}
	rows := buildGitChangesRows(dirty, root, map[string]bool{sub: true})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Sorted: gone.txt < newdir/.
	if rows[0].IsDir || rows[0].Rel != "gone.txt" {
		t.Fatalf("deleted row should not be a dir: %+v", rows[0])
	}
	if !rows[1].IsDir || rows[1].Rel != "newdir/" {
		t.Fatalf("dir row should carry IsDir + trailing slash: %+v", rows[1])
	}
}

// TestBuildGitChangesRows_SkipsPathsOutsideRoot guards the row builder
// against dirty paths that don't resolve under the project root (e.g. a
// repo whose toplevel sits above the folder the editor opened) — those
// have no meaningful relative path to display.
func TestBuildGitChangesRows_SkipsPathsOutsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	dirty := map[string]filetree.GitChangeKind{
		"/somewhere/else/file.go": filetree.GitChangeModified,
	}
	if rows := buildGitChangesRows(dirty, root, nil); len(rows) != 0 {
		t.Fatalf("expected no rows for out-of-root paths, got %+v", rows)
	}
}

// TestApplyGitStatus_CarriesIsDirToPanelRows drives the whole off-thread
// IsDir pipeline against a real repo: collectGitStatus stats the dirty
// paths on its own (background-safe) side, applyGitStatus rebases and
// stores the flags, and the panel rebuild consumes them — so
// buildGitChangesRows never has to touch the filesystem on the event
// loop again.
func TestApplyGitStatus_CarriesIsDirToPanelRows(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "tracked.txt"), "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	if err := os.Mkdir(filepath.Join(repo, "newdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(repo, "newdir", "inside.txt"), "y\n")

	// newTestApp runs the synchronous refreshGitStatus, which flows
	// through the same collect → apply pipeline as the async tick.
	a := newTestApp(t, repo)
	a.gitPanel.active = true
	a.rebuildGitChangesRows()

	var dirRow *gitChangeRow
	for i := range a.gitPanel.rows {
		if a.gitPanel.rows[i].Abs == filepath.Join(repo, "newdir") {
			dirRow = &a.gitPanel.rows[i]
		}
	}
	if dirRow == nil {
		t.Fatalf("untracked dir missing from panel rows: %+v", a.gitPanel.rows)
	}
	if !dirRow.IsDir || dirRow.Rel != "newdir/" {
		t.Fatalf("untracked dir row should carry IsDir + trailing slash: %+v", dirRow)
	}
}

// TestGitKindLetterAndColor pins the letter badge vocabulary (M/A/D/R)
// and that each kind maps to its dedicated theme color — the same hue
// the file tree paints, so a file reads the same everywhere.
func TestGitKindLetterAndColor(t *testing.T) {
	th := theme.Default()
	tests := []struct {
		kind   filetree.GitChangeKind
		letter string
		color  tcell.Color
	}{
		{filetree.GitChangeModified, "M", th.GitModified},
		{filetree.GitChangeAdded, "A", th.GitAdded},
		{filetree.GitChangeDeleted, "D", th.GitDeleted},
		{filetree.GitChangeRenamed, "R", th.GitRenamed},
		{filetree.GitChangeMixed, "M", th.GitMixed},
	}
	for _, tt := range tests {
		if got := gitKindLetter(tt.kind); got != tt.letter {
			t.Errorf("letter(%d): got %q, want %q", tt.kind, got, tt.letter)
		}
		if got := gitKindColor(th, tt.kind); got != tt.color {
			t.Errorf("color(%d): got %v, want %v", tt.kind, got, tt.color)
		}
	}
}

// TestToggleGitPanel_SingleFileModeFlashes verifies the guard for the
// tree-less single-file invocation: no panel, just the explanatory
// flash — same contract as the finder.
func TestToggleGitPanel_SingleFileModeFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.toggleGitPanel()
	if a.gitPanel.active {
		t.Fatal("panel should not activate in single-file mode")
	}
	if !strings.Contains(a.statusMsg, "single-file") {
		t.Fatalf("expected single-file flash, got %q", a.statusMsg)
	}
}

// TestToggleGitPanel_NonRepoFlashes verifies toggling against a plain
// directory flashes "Not a git repository" instead of showing an empty
// panel that would imply a clean repo.
func TestToggleGitPanel_NonRepoFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.toggleGitPanel()
	if a.gitPanel.active {
		t.Fatal("panel should not activate outside a git repo")
	}
	if !strings.Contains(a.statusMsg, "Not a git repository") {
		t.Fatalf("expected non-repo flash, got %q", a.statusMsg)
	}
}

// TestToggleGitPanel_ActivatesAndLists is the end-to-end happy path: a
// repo with one modified and one untracked file activates the panel
// with exactly those two rows, sorted, with the right kinds — and the
// status refresh happens on toggle (no waiting for the 10-second tick).
func TestToggleGitPanel_ActivatesAndLists(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if !a.gitPanel.active {
		t.Fatal("panel should be active")
	}
	if len(a.gitPanel.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(a.gitPanel.rows), a.gitPanel.rows)
	}
	if a.gitPanel.rows[0].Rel != "modified.txt" || a.gitPanel.rows[0].Kind != filetree.GitChangeModified {
		t.Fatalf("row 0: %+v", a.gitPanel.rows[0])
	}
	if a.gitPanel.rows[1].Rel != "untracked.txt" || a.gitPanel.rows[1].Kind != filetree.GitChangeAdded {
		t.Fatalf("row 1: %+v", a.gitPanel.rows[1])
	}
}

// TestToggleGitPanel_TogglesBackToExplorer pins the Esc-g round trip:
// the same gesture that opens the Git panel returns to the explorer,
// mirroring how Esc-t toggles the sidebar itself.
func TestToggleGitPanel_TogglesBackToExplorer(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if !a.gitPanel.active {
		t.Fatal("first toggle should activate the panel")
	}
	a.toggleGitPanel()
	if a.gitPanel.active {
		t.Fatal("second toggle should return to the explorer")
	}
}

// TestToggleGitPanel_ShowsHiddenSidebar verifies the panel is reachable
// even when the sidebar is hidden: Esc-g shows the sidebar in Git mode
// rather than toggling an invisible panel.
func TestToggleGitPanel_ShowsHiddenSidebar(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.sidebarShown = false
	a.toggleGitPanel()
	if !a.sidebarShown || !a.gitPanel.active {
		t.Fatalf("expected sidebar shown in git mode, got shown=%v git=%v",
			a.sidebarShown, a.gitPanel.active)
	}
}

// TestSidebarHeaderClick_SwitchesPanels drives the EXPLORER / GIT tabs
// through sidebarClick the way a mouse would: clicking GIT activates
// the panel, clicking EXPLORER returns to the tree.
func TestSidebarHeaderClick_SwitchesPanels(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.refreshGitStatus() // populate gitBranch so the GIT tab exists
	gx := runeLen(sidebarHeaderExplorer) + sidebarHeaderGap
	a.sidebarClick(gx, 0) // click "GIT"
	if !a.gitPanel.active {
		t.Fatal("clicking the GIT tab should activate the panel")
	}
	a.sidebarClick(1, 0) // click "EXPLORER"
	if a.gitPanel.active {
		t.Fatal("clicking the EXPLORER tab should return to the tree")
	}
}

// TestGitTabLabel_CountsChanges pins the count badge: bare "GIT" when
// clean, "GIT n" when paths are dirty — and the header hit zone grows
// with it so the badge is part of the click target.
func TestGitTabLabel_CountsChanges(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	if got := a.gitTabLabel(); got != "GIT 2" {
		t.Fatalf("dirty label = %q, want %q", got, "GIT 2")
	}
	countX := runeLen(sidebarHeaderExplorer) + sidebarHeaderGap + 4 // the "2"
	if zone := a.sidebarHeaderHit(countX); zone != "git" {
		t.Fatalf("count badge should be clickable, got %q", zone)
	}
	a.tree.DirtyFiles = nil
	if got := a.gitTabLabel(); got != "GIT" {
		t.Fatalf("clean label = %q, want %q", got, "GIT")
	}
}

// TestSidebarHeaderHit_NoGitTabOutsideRepo verifies the GIT tab is
// simply absent for non-repos — clicking where it would sit does
// nothing instead of flashing errors from a dead tab.
func TestSidebarHeaderHit_NoGitTabOutsideRepo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	gx := runeLen(sidebarHeaderExplorer) + sidebarHeaderGap
	if zone := a.sidebarHeaderHit(gx); zone != "" {
		t.Fatalf("non-repo GIT tab hit = %q, want none", zone)
	}
}

// TestGitPanelClick_ShowsDiffWithOpenButton verifies the core flow:
// clicking a modified row opens the diff view with the [ Open file ]
// button armed and focused, and Enter opens the file with the cursor
// on the first changed line.
func TestGitPanelClick_ShowsDiffWithOpenButton(t *testing.T) {
	a, modified, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	a.gitPanelClick(5, gitPanelListTop) // first row: modified.txt
	if !diffIsOpen(a) {
		t.Fatal("row click should open the diff view")
	}
	if !strings.Contains(diffOv(t, a).title, "modified.txt") {
		t.Fatalf("diff title should carry the file name, got %q", diffOv(t, a).title)
	}
	body := strings.Join(diffOv(t, a).unified, "\n")
	if !strings.Contains(body, "-two") || !strings.Contains(body, "+TWO") {
		t.Fatalf("diff body should show the change, got:\n%s", body)
	}
	if diffOv(t, a).openPath != modified || diffOv(t, a).hover != 1 {
		t.Fatalf("Open file should be armed and focused, path=%q hover=%d",
			diffOv(t, a).openPath, diffOv(t, a).hover)
	}

	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if diffIsOpen(a) {
		t.Fatal("Enter should close the diff view")
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != modified {
		t.Fatalf("expected %s to be the active tab", modified)
	}
	// Line 2 ("TWO") is the only change — zero-based line 1.
	if tab.Cursor.Line != 1 {
		t.Fatalf("cursor should land on the first changed line, got %d", tab.Cursor.Line)
	}
}

// TestGitPanelClick_UntrackedShowsAllAddedDiff verifies a brand-new
// file still gets a diff (via the --no-index fallback) instead of the
// "no diff available" placeholder.
func TestGitPanelClick_UntrackedShowsAllAddedDiff(t *testing.T) {
	a, _, untracked := dirtyRepoApp(t)
	a.toggleGitPanel()
	a.gitPanelClick(5, gitPanelListTop+1) // second row: untracked.txt
	if !diffIsOpen(a) {
		t.Fatal("row click should open the diff view")
	}
	body := strings.Join(diffOv(t, a).unified, "\n")
	if !strings.Contains(body, "+new") {
		t.Fatalf("untracked diff should show the file as added, got:\n%s", body)
	}
	if diffOv(t, a).openPath != untracked {
		t.Fatal("untracked files exist on disk — Open file should be armed")
	}
}

// TestGitPanelClick_DeletedHasNoOpenButton verifies a deleted path
// shows its diff without the Open file action — there is no file left
// to open.
func TestGitPanelClick_DeletedHasNoOpenButton(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	doomed := filepath.Join(dir, "doomed.txt")
	writeFileT(t, doomed, "so long\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	if err := os.Remove(doomed); err != nil {
		t.Fatalf("remove: %v", err)
	}
	a := newTestApp(t, dir)
	a.toggleGitPanel()
	if len(a.gitPanel.rows) != 1 || a.gitPanel.rows[0].Kind != filetree.GitChangeDeleted {
		t.Fatalf("expected one deleted row, got %+v", a.gitPanel.rows)
	}
	a.gitPanelClick(5, gitPanelListTop)
	if !diffIsOpen(a) {
		t.Fatal("deleted row should open the diff view")
	}
	if diffOv(t, a).openPath != "" {
		t.Fatal("deleted files must not arm Open file")
	}
	body := strings.Join(diffOv(t, a).unified, "\n")
	if !strings.Contains(body, "-so long") {
		t.Fatalf("diff body should show the removed line, got %q", body)
	}
}

// TestActivateGitChangeRow_NoDiffExplainsItself pins the empty answer:
// a row whose diff git cannot produce owes the user a sentence, and a
// sentence belongs in the info prefab rather than in a diff view with
// one line of prose where a diff should be.
func TestActivateGitChangeRow_NoDiffExplainsItself(t *testing.T) {
	a := newTestApp(t, t.TempDir()) // no repo: every diff comes back empty
	a.activateGitChangeRow(gitChangeRow{Rel: "ghost.txt", Abs: filepath.Join(a.rootDir, "ghost.txt")})
	if diffIsOpen(a) {
		t.Fatal("there is no diff to show — the diff view must not open")
	}
	if !infoIsOpen(a) {
		t.Fatalf("expected the info prefab to explain itself; top = %T", a.overlays.Top())
	}
}

// TestGitPanelClick_UntrackedDirRevealsInExplorer verifies activating
// an untracked-directory row flips back to the explorer with that
// folder active, rather than erroring on "diff a directory".
func TestGitPanelClick_UntrackedDirRevealsInExplorer(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "base.txt"), "x\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	sub := filepath.Join(dir, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(sub, "inside.txt"), "y\n")
	a := newTestApp(t, dir)
	a.toggleGitPanel()
	if len(a.gitPanel.rows) != 1 || !a.gitPanel.rows[0].IsDir {
		t.Fatalf("expected one dir row, got %+v", a.gitPanel.rows)
	}
	a.gitPanelClick(5, gitPanelListTop)
	if a.gitPanel.active {
		t.Fatal("dir activation should switch back to the explorer")
	}
	if a.activeFolder != sub {
		t.Fatalf("active folder: got %q, want %q", a.activeFolder, sub)
	}
}

// TestGitPanelClick_IgnoresChrome verifies the panel's dead cells stay
// dead: the gaps between the action buttons, the rows inside the list
// viewport that sit past the last change, and everything below the
// viewport. Only a real row activates, and nothing else in the panel
// moves. The branch row is deliberately not covered here — it is a
// live control that opens the switch-branch picker, pinned by
// TestGitPanelClick_BranchRowOpensPicker in gitlog_test.go.
func TestGitPanelClick_IgnoresChrome(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if len(a.gitPanel.rows) != 2 {
		t.Fatalf("fixture: want 2 change rows, got %d", len(a.gitPanel.rows))
	}
	_, _, sw, _ := a.sidebarRect()
	btns := a.gitPanelButtons(sw)
	if len(btns) < 2 || btns[0].x1 >= btns[1].x0 {
		t.Fatalf("fixture: want a button row with a dead cell between buttons, got %+v", btns)
	}
	// A row past the last change but still inside the list window —
	// a different guard from the far-below case, and the one that
	// would index past the row slice if it went missing.
	listH, _ := a.gitPanelBody()
	pastRows := gitPanelListTop + len(a.gitPanel.rows) + 1
	if pastRows-gitPanelListTop >= listH {
		t.Fatalf("fixture: list window (%d rows) too short to click past the rows inside it", listH)
	}
	a.gitPanel.selected = 1
	checks, msg := len(a.gitCommitChecks), a.statusMsg
	for _, tc := range []struct {
		name string
		x, y int
	}{
		// One cell past [Commit] and before [Push]: hit zones are
		// half-open, so this cell belongs to neither button.
		{"gap in the action row", btns[0].x1, 2},
		{"past the last row, inside the list window", 5, pastRows},
		{"far below the list window", 5, 50},
	} {
		a.gitPanelClick(tc.x, tc.y)
		if a.overlays.IsOpen() {
			t.Fatalf("%s: click opened %T", tc.name, a.overlays.Top())
		}
		if !a.gitPanel.active || a.gitPanel.selected != 1 || a.gitPanel.scroll != 0 {
			t.Fatalf("%s: panel state moved — active %v, selected %d, scroll %d",
				tc.name, a.gitPanel.active, a.gitPanel.selected, a.gitPanel.scroll)
		}
		if len(a.gitCommitChecks) != checks {
			t.Fatalf("%s: click touched the commit checkboxes", tc.name)
		}
		if a.statusMsg != msg {
			t.Fatalf("%s: click flashed %q", tc.name, a.statusMsg)
		}
	}
}

// TestScrollGitPanel_Clamps pins the wheel-scroll bounds: a list
// shorter than the viewport can't scroll at all, and a long list stops
// at the last full window.
func TestScrollGitPanel_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.rows = make([]gitChangeRow, 100)
	_, _, _, sh := a.sidebarRect()
	a.scrollGitPanel(1000)
	if want := 100 - (sh - gitPanelListTop); a.gitPanel.scroll != want {
		t.Fatalf("scroll past end: got %d, want %d", a.gitPanel.scroll, want)
	}
	a.scrollGitPanel(-1000)
	if a.gitPanel.scroll != 0 {
		t.Fatalf("scroll past top: got %d, want 0", a.gitPanel.scroll)
	}
	a.gitPanel.rows = a.gitPanel.rows[:3]
	a.scrollGitPanel(5)
	if a.gitPanel.scroll != 0 {
		t.Fatalf("short list should never scroll, got %d", a.gitPanel.scroll)
	}
}

// TestDrawGitPanel_Smoke renders the active panel on a simulation
// screen and checks the header tabs, branch, letter badge, and file
// name all land — plus the empty state when the row list is empty.
func TestDrawGitPanel_Smoke(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	a.draw()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	header := screenLine(scr, 0)
	if !strings.Contains(header, "EXPLORER") || !strings.Contains(header, "GIT") {
		t.Fatalf("header row: %q", header)
	}
	if branch := screenLine(scr, 1); !strings.Contains(branch, "main") {
		t.Fatalf("branch row: %q", branch)
	}
	if buttons := screenLine(scr, 2); !strings.Contains(buttons, "Commit") || !strings.Contains(buttons, "Push") {
		t.Fatalf("buttons row: %q", buttons)
	}
	row0 := screenLine(scr, gitPanelListTop)
	if !strings.Contains(row0, "M") || !strings.Contains(row0, "modified.txt") {
		t.Fatalf("row 0: %q", row0)
	}
	if got := []rune(row0)[1]; got != '●' {
		t.Fatalf("checkbox cell: got %q, want ●", got)
	}
	if got := []rune(row0)[3]; got != 'M' {
		t.Fatalf("letter cell: got %q", got)
	}

	// Empty state: pretend the repo went clean under us.
	a.gitPanel.rows = nil
	a.draw()
	a.screen.Show()
	if body := screenLine(scr, gitPanelListTop); !strings.Contains(body, "No uncommitted changes") {
		t.Fatalf("empty state row: %q", body)
	}
}

// TestDrawSidebarHeader_ExplorerModeShowsGitTab verifies the explorer
// view also carries the GIT tab (overdrawn onto the tree's header row)
// so the panel is discoverable from the default view.
func TestDrawSidebarHeader_ExplorerModeShowsGitTab(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.refreshGitStatus()
	a.draw()
	a.screen.Show()
	header := screenLine(a.screen.(tcell.SimulationScreen), 0)
	if !strings.Contains(header, "EXPLORER") || !strings.Contains(header, "GIT") {
		t.Fatalf("explorer header should show both tabs, got %q", header)
	}
}

// TestStatusGitSegment covers the three status-bar shapes: no repo →
// empty, clean repo → branch only, dirty repo → branch plus count.
func TestStatusGitSegment(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.statusGitSegment(); got != "" {
		t.Fatalf("non-repo segment should be empty, got %q", got)
	}
	a.gitSnap.Branch = "main"
	if got := a.statusGitSegment(); got != " main " {
		t.Fatalf("clean segment: got %q", got)
	}
	a.tree.DirtyFiles = map[string]filetree.GitChangeKind{
		"/x/a": filetree.GitChangeModified,
		"/x/b": filetree.GitChangeAdded,
	}
	if got := a.statusGitSegment(); got != " main · 2 " {
		t.Fatalf("dirty segment: got %q", got)
	}
	a.gitSnap.Ahead, a.gitSnap.Behind = 2, 1
	if got := a.statusGitSegment(); got != " main ↑2 ↓1 · 2 " {
		t.Fatalf("diverged segment: got %q", got)
	}
	a.gitSnap.Behind = 0
	if got := a.statusGitSegment(); got != " main ↑2 · 2 " {
		t.Fatalf("ahead-only segment: got %q", got)
	}
}

// TestStatusBarClick_TogglesGitPanel verifies a click landing inside
// the branch segment activates the panel, and one landing left of it
// doesn't — the mouse-first path from anywhere in the editor.
func TestStatusBarClick_TogglesGitPanel(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.refreshGitStatus() // populate gitBranch so the segment exists
	if a.statusGitSegment() == "" {
		t.Fatal("expected a git segment to click")
	}
	sx, _, sw, _ := a.statusRect()
	a.statusBarClick(sx + sw - 2) // inside the segment
	if !a.gitPanel.active {
		t.Fatal("click on the git segment should activate the panel")
	}
	a.showExplorerPanel()
	a.statusBarClick(sx + 1) // far left — file info territory
	if a.gitPanel.active {
		t.Fatal("click outside the segment should not activate the panel")
	}
}

// TestMenuGitChangesRow pins the ≡ menu contract from CLAUDE.md: the
// feature must be reachable from the main menu. Since the git verbs
// moved behind the "Git…" drill-in, the row lives there — it still
// carries the Esc-g shortcut, is greyed out outside a repo, and is the
// keyboard route into the panel: firing it shows the panel *and* hands
// it the keyboard. A row that only made the panel visible would strand
// the user who reached for the menu precisely because they have no
// mouse, which is the whole reason menuGitChanges routes through
// focusGitPanel rather than toggleGitPanel.
func TestMenuGitChangesRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	item := drillInItemByLabel(t, a, "Git changes")
	if item.shortcut != "Esc g" {
		t.Fatalf("shortcut: got %q, want %q", item.shortcut, "Esc g")
	}
	if item.enabled(a) {
		t.Fatal("row should be disabled outside a git repo")
	}
	// Repo-ness is the explicit IsRepo fact now — a branch name alone
	// no longer implies a repository.
	a.gitSnap.IsRepo = true
	if !item.enabled(a) {
		t.Fatal("row should be enabled inside a git repo")
	}
	// The panel gate is the cached branch; seed it the way a real
	// session's startup status refresh does.
	a.gitSnap.Branch = "main"
	a.menuGitChanges()
	if !a.gitPanel.active {
		t.Fatal("the menu row should show the Git panel")
	}
	if !a.gitPanelKeysOn() {
		t.Fatal("the menu row must hand the panel the keyboard, not just show it")
	}
}

// TestRefreshGitStatus_RebuildsActivePanel verifies the 10-second
// refresh path keeps an active panel live: a file changed on disk after
// activation shows up on the next status refresh without re-toggling.
func TestRefreshGitStatus_RebuildsActivePanel(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if len(a.gitPanel.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(a.gitPanel.rows))
	}
	writeFileT(t, filepath.Join(a.rootDir, "another.txt"), "hi\n")
	a.refreshGitStatus()
	if len(a.gitPanel.rows) != 3 {
		t.Fatalf("active panel should track new changes, got %d rows", len(a.gitPanel.rows))
	}
}

// TestDrawSidebarHeader_LabelsReadable pins the header contrast fix:
// the inactive tab label used to render in Subtle (3.4:1 — below the
// text bar), and the active/inactive split leaned on brightness alone.
// Now the active label is Text bold and the inactive one Muted, both
// clearing WCAG AA on the sidebar background.
func TestDrawSidebarHeader_LabelsReadable(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel() // GIT active, EXPLORER inactive
	a.draw()
	a.screen.Show()

	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	// Row 0: " EXPLORER   GIT n". The 'E' sits at col 1.
	fg, _, _ := cells[0*w+1].Style.Decompose()
	if fg == a.theme.Subtle {
		t.Fatal("inactive header label still uses Subtle (3.4:1); want Muted")
	}
	if fg != a.theme.Muted {
		t.Fatalf("inactive header label fg = %v, want Muted", fg)
	}
	gx := 1 + len("EXPLORER") + sidebarHeaderGap
	fg, _, _ = cells[0*w+gx].Style.Decompose()
	if fg != a.theme.Text {
		t.Fatalf("active header label fg = %v, want Text", fg)
	}
}

// TestDiffWalk_ArrowsMoveBetweenFiles pins druk's walking gesture: with
// a panel-opened diff up, ↓/↑ move to the next/previous changed file
// (clamped at the ends), and closing the diff disarms the walk so a
// menu-opened diff scrolls normally.
func TestDiffWalk_ArrowsMoveBetweenFiles(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	a.gitPanelClick(5, gitPanelListTop) // modified.txt
	if a.diffPanelRow != 0 {
		t.Fatalf("panel click should arm walking, got %d", a.diffPanelRow)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if a.diffPanelRow != 1 || !strings.Contains(diffOv(t, a).title, "untracked.txt") {
		t.Fatalf("down should walk to the next file, row %d title %q", a.diffPanelRow, diffOv(t, a).title)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if a.diffPanelRow != 1 {
		t.Fatalf("walk should clamp at the last file, got %d", a.diffPanelRow)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if !strings.Contains(diffOv(t, a).title, "modified.txt") {
		t.Fatalf("up should walk back, title %q", diffOv(t, a).title)
	}
	if a.gitPanel.selected != 0 {
		t.Fatalf("panel selection should track the walk, got %d", a.gitPanel.selected)
	}
	a.closeAllModals()
	if a.diffPanelRow != -1 {
		t.Fatalf("closing the diff must disarm walking, got %d", a.diffPanelRow)
	}
}

// TestGitPanelButtons_CarryLiveState pins the self-explaining row: with
// commits to push/pull and files checked for commit, the wide labels
// carry those counts so the buttons say why you'd press them.
func TestGitPanelButtons_CarryLiveState(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitSnap.IsRepo = true
	a.gitSnap.Ahead, a.gitSnap.Behind = 2, 1
	a.gitPanel.rows = []gitChangeRow{
		{Abs: "/p/a.go"}, {Abs: "/p/b.go"}, {Abs: "/p/dir", IsDir: true},
	}
	labels := []string{}
	for _, b := range a.gitPanelButtons(60) {
		labels = append(labels, b.label)
	}
	want := []string{"[ Commit 2 ]", "[ Push ↑2 ]", "[ Pull ↓1 ]", "[ ⋯ ]"}
	for i, w := range want {
		if labels[i] != w {
			t.Fatalf("label %d = %q, want %q (all: %v)", i, labels[i], w, labels)
		}
	}
}

// TestGitPanelButtons_CollapseToGlyphsWhenNarrow pins the adaptive row:
// at the minimum sidebar width the wide labels don't fit, so the row
// falls back to glyph buttons — and every button's hit zone stays
// inside the sidebar instead of overdrawing the splitter and editor.
func TestGitPanelButtons_CollapseToGlyphsWhenNarrow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitSnap.IsRepo = true
	btns := a.gitPanelButtons(minSidebarWidth)
	if len(btns) != 4 {
		t.Fatalf("all four buttons must survive narrow widths, got %d", len(btns))
	}
	for _, b := range btns {
		if b.x1 > minSidebarWidth {
			t.Fatalf("button %q ends at %d, past the %d-cell sidebar", b.label, b.x1, minSidebarWidth)
		}
	}
}

// TestDrawGitPanel_CJKPathClipsInsidePanel pins the panel's wide-glyph
// layout: a CJK path's ideographs land two cells apart (base cell plus
// the continuation cell tcell paints through), and a name longer than
// the sidebar clips inside the panel width instead of drifting. Before
// textdraw, drawClipped painted one rune per COLUMN, so consecutive
// ideographs collapsed onto each other's continuation cells.
func TestDrawGitPanel_CJKPathClipsInsidePanel(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	// 18 ideographs (36 cells) + ".txt" — wider than the default panel.
	const cjk = "日本語のファイル名がとても長いテスト.txt"
	writeFileT(t, filepath.Join(a.rootDir, cjk), "x\n")
	a.refreshGitStatus()
	a.toggleGitPanel()
	// Draw ONLY the panel on a cleared screen: in a full draw() the
	// splitter and editor paint after the sidebar, masking any bleed.
	a.screen.Clear()
	sx, sy, sw, sh := a.sidebarRect()
	a.drawGitPanel(sx, sy, sw, sh)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()

	rowY := -1
	for y := sy; y < sy+sh; y++ {
		if strings.ContainsRune(screenLine(scr, y), '日') {
			rowY = y
			break
		}
	}
	if rowY < 0 {
		t.Fatal("could not find the CJK row in the git panel")
	}
	// The name starts after "› ● M " chrome at sx+5: base cell, skipped
	// continuation cell, next ideograph two cells later.
	if c := cells[rowY*w+sx+5]; len(c.Runes) == 0 || c.Runes[0] != '日' {
		t.Fatalf("name cell = %q, want 日 at column %d", c.Runes, sx+5)
	}
	if c := cells[rowY*w+sx+6]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
		t.Fatalf("continuation cell after 日 holds a glyph: %q", c.Runes)
	}
	if c := cells[rowY*w+sx+7]; len(c.Runes) == 0 || c.Runes[0] != '本' {
		t.Fatalf("cell two after 日 = %q, want 本", c.Runes)
	}
	// The over-long name must not paint past the panel width.
	for x := sx + sw; x < w; x++ {
		if c := cells[rowY*w+x]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("glyph painted past the panel at x=%d: %q", x, c.Runes[0])
		}
	}
}

// TestDrawGitPanel_BranchRowShowsPickerAffordance pins the ▾ cue: the
// branch row opens the branch picker on click, so it must not render as
// a static label.
func TestDrawGitPanel_BranchRowShowsPickerAffordance(t *testing.T) {
	a := newTestApp(t, initRepo(t))
	a.gitSnap.IsRepo = true
	a.gitSnap.Branch = "main"
	a.showGitPanel()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	row := ""
	for x := 0; x < 30; x++ {
		if len(cells[1*w+x].Runes) > 0 {
			row += string(cells[1*w+x].Runes[0])
		}
	}
	if !strings.Contains(row, "main ▾") {
		t.Fatalf("branch row should carry the picker chevron, got %q", row)
	}
}

// keyboardGitApp returns a dirty-repo app in the state Esc-g leaves the
// user in: Git panel up, keyboard mode armed, two change rows
// (modified.txt then untracked.txt, sorted).
func keyboardGitApp(t *testing.T) *App {
	t.Helper()
	a, _, _ := dirtyRepoApp(t)
	a.focusGitPanel()
	if !a.gitPanelKeysOn() {
		t.Fatal("Esc-g should hand the keyboard to the Git panel")
	}
	if len(a.gitPanel.rows) != 2 {
		t.Fatalf("expected 2 change rows, got %d", len(a.gitPanel.rows))
	}
	return a
}

// gitScreenRow draws the app and reads sidebar row y back off the
// simulation screen — the only way to prove a focus cue is painted
// rather than merely computed.
func gitScreenRow(t *testing.T, a *App, y int) string {
	t.Helper()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show() // GetContents serves the front buffer.
	cells, w, _ := scr.GetContents()
	_, _, sw, _ := a.sidebarRect()
	var b strings.Builder
	for x := range sw {
		if r := cells[y*w+x].Runes; len(r) > 0 {
			b.WriteRune(r[0])
		}
	}
	return b.String()
}

// gitHintText draws the app and returns the keyboard hint strip as it
// is actually painted at the bottom of the sidebar.
func gitHintText(t *testing.T, a *App) string {
	t.Helper()
	_, hint := a.gitPanelBody()
	_, _, _, sh := a.sidebarRect()
	out := make([]string, 0, len(hint))
	for i := range hint {
		out = append(out, gitScreenRow(t, a, sh-len(hint)+i))
	}
	return strings.Join(out, " ")
}

// TestGitPanelKeys_DownMovesSelection pins the fix for the panel being
// mouse-only: with keyboard mode armed, ↓ walks the change list, the
// caret cue follows it on screen, and the ends clamp instead of
// wrapping so a held arrow key can't lose your place.
func TestGitPanelKeys_DownMovesSelection(t *testing.T) {
	a := keyboardGitApp(t)
	if got := gitScreenRow(t, a, gitPanelListTop); !strings.HasPrefix(got, "›") {
		t.Fatalf("first row should carry the keyboard caret, got %q", got)
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if a.gitPanel.selected != 1 {
		t.Fatalf("down should select row 1, got %d", a.gitPanel.selected)
	}
	if got := gitScreenRow(t, a, gitPanelListTop+1); !strings.HasPrefix(got, "›") {
		t.Fatalf("caret should have moved to row 1, got %q", got)
	}
	if got := gitScreenRow(t, a, gitPanelListTop); strings.HasPrefix(got, "›") {
		t.Fatalf("row 0 should have released the caret, got %q", got)
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if a.gitPanel.selected != 1 {
		t.Fatalf("selection must clamp at the last row, got %d", a.gitPanel.selected)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if a.gitPanel.selected != 0 {
		t.Fatalf("selection must clamp at the first row, got %d", a.gitPanel.selected)
	}
}

// TestGitPanelKeys_SpaceStagesSelectedRowOnly pins the staging key:
// Space flips the selected row's commit checkbox and leaves every other
// row alone, so a keyboard user can deselect one file out of twenty
// without a mouse.
func TestGitPanelKeys_SpaceStagesSelectedRowOnly(t *testing.T) {
	a := keyboardGitApp(t)
	first, second := a.gitPanel.rows[0].Abs, a.gitPanel.rows[1].Abs

	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if a.commitCheckOn(second) {
		t.Fatal("space should unstage the selected row")
	}
	if !a.commitCheckOn(first) {
		t.Fatal("space must not touch any row but the selected one")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if !a.commitCheckOn(second) {
		t.Fatal("space should stage the row back")
	}
}

// TestGitPanelKeys_EnterOpensDiff pins Enter's meaning on a row: it
// shows the change, the same thing a click does, and arms the diff's
// ↑↓ file walk.
func TestGitPanelKeys_EnterOpensDiff(t *testing.T) {
	a := keyboardGitApp(t)
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !diffIsOpen(a) {
		t.Fatalf("enter on a row should open the diff, top = %T", a.overlays.Top())
	}
	if !strings.Contains(diffOv(t, a).title, "modified.txt") {
		t.Fatalf("diff should be for the selected row, title %q", diffOv(t, a).title)
	}
	if a.diffPanelRow != 0 {
		t.Fatalf("enter should arm the diff walk, got row %d", a.diffPanelRow)
	}
}

// TestGitPanelKeys_TabReachesButtonsAndEnterRuns pins the second half of
// the keyboard route: Tab hops from the list to the action row, the
// focused button actually renders focused (it used to always draw
// focused=false), ← → walk the row, and Enter runs what's focused.
func TestGitPanelKeys_TabReachesButtonsAndEnterRuns(t *testing.T) {
	a := keyboardGitApp(t)
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if !a.gitPanel.onBtns || a.gitPanel.btn != 0 {
		t.Fatalf("tab should focus the first button, onBtns=%v idx=%d", a.gitPanel.onBtns, a.gitPanel.btn)
	}

	// drawButton's focus language is an inverted block: the label sits
	// on the accent color instead of the sidebar background.
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	btns := a.gitPanelButtons(29)
	_, bg, _ := cells[2*w+btns[0].x0].Style.Decompose()
	if bg != a.theme.Accent {
		t.Fatalf("focused button bg = %v, want the Accent block DrawButton paints", bg)
	}
	_, bg, _ = cells[2*w+btns[1].x0].Style.Decompose()
	if bg == a.theme.Accent {
		t.Fatal("only the focused button may render inverted")
	}

	for range 3 {
		a.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	}
	if a.gitPanel.btn != 3 {
		t.Fatalf("→ should walk the button row, got %d", a.gitPanel.btn)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if labels := popupLabels(t, a); len(labels) == 0 {
		t.Fatal("enter on the ⋯ button should open the git extras popup")
	}
}

// TestGitPanelKeys_FocusFallsBackToTheList pins the closed cycle:
// stepping off either end of the button row returns to the change
// list rather than wrapping around it.
func TestGitPanelKeys_FocusFallsBackToTheList(t *testing.T) {
	a := keyboardGitApp(t)
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	for range 4 {
		a.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	}
	if a.gitPanel.onBtns {
		t.Fatal("stepping past the last button should return to the list")
	}
	// Shift-Tab from the list enters the row at its far end.
	a.handleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	if !a.gitPanel.onBtns || a.gitPanel.btn != 3 {
		t.Fatalf("shift-tab should focus the last button, onBtns=%v idx=%d", a.gitPanel.onBtns, a.gitPanel.btn)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if a.gitPanel.onBtns {
		t.Fatal("up from the button row should return to the list")
	}
}

// TestGitPanelKeys_EscReturnsToEditor pins the exit: Esc drops the
// panel's key capture (leaving the panel itself up) and the arrow keys
// go back to moving the caret.
func TestGitPanelKeys_EscReturnsToEditor(t *testing.T) {
	a, modified, _ := dirtyRepoApp(t)
	a.openFile(modified)
	a.focusGitPanel()
	if !a.gitPanelKeysOn() {
		t.Fatal("panel should own the keyboard")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if a.gitPanelKeysOn() {
		t.Fatal("esc should hand the keyboard back to the editor")
	}
	if !a.gitPanel.active {
		t.Fatal("esc releases focus, it does not close the panel")
	}

	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected the opened file in front")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tab.Cursor.Line != 1 {
		t.Fatalf("arrows should move the caret again, cursor line %d", tab.Cursor.Line)
	}
	if a.gitPanel.selected != 0 {
		t.Fatalf("the panel must not react after esc, selection %d", a.gitPanel.selected)
	}
}

// TestGitPanelKeys_IgnoredWhileOverlayOpen pins the routing order the
// overlay stack owns: an open overlay takes the whole keyboard, so the
// panel underneath it never sees a key.
func TestGitPanelKeys_IgnoredWhileOverlayOpen(t *testing.T) {
	a := keyboardGitApp(t)
	a.openMenu()
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if a.gitPanel.selected != 0 {
		t.Fatalf("selection moved under an open overlay, got %d", a.gitPanel.selected)
	}
	if a.gitPanel.onBtns {
		t.Fatal("focus moved to the buttons under an open overlay")
	}
	if len(a.gitCommitChecks) != 0 {
		t.Fatalf("staging changed under an open overlay: %v", a.gitCommitChecks)
	}
}

// TestGitPanelKeys_NoCtrlBindings pins CLAUDE.md's hard rule: the panel
// grew a keyboard mode without growing a single Ctrl chord, because
// Ctrl is exactly what tmux and terminal flow control eat. (KeyCtrlI /
// KeyCtrlM are Tab / Enter and are deliberately not in this list —
// they're the same bytes as the bindings we do want.)
func TestGitPanelKeys_NoCtrlBindings(t *testing.T) {
	a := keyboardGitApp(t)
	ctrl := []tcell.Key{
		tcell.KeyCtrlA, tcell.KeyCtrlB, tcell.KeyCtrlD, tcell.KeyCtrlE,
		tcell.KeyCtrlF, tcell.KeyCtrlG, tcell.KeyCtrlK, tcell.KeyCtrlN,
		tcell.KeyCtrlP, tcell.KeyCtrlS, tcell.KeyCtrlU, tcell.KeyCtrlW,
	}
	for _, k := range ctrl {
		if a.handleGitPanelKey(tcell.NewEventKey(k, 0, tcell.ModCtrl)) {
			t.Fatalf("Ctrl key %v must not be bound in the Git panel", k)
		}
	}
	if a.gitPanel.selected != 0 || a.gitPanel.onBtns || len(a.gitCommitChecks) != 0 {
		t.Fatal("Ctrl keys must leave the panel completely untouched")
	}
}

// TestGitPanelHint_DocumentsKeysAndNamesFocusedButton pins the hint
// strip: while the panel has the keyboard it documents its bindings
// bottom-docked (a strip, not an overlay), and on the action row it
// names the focused button — which is what makes the minimum-width
// [✓][↑][↓][⋯] ladder decodable at all, since that tier has no verbs.
func TestGitPanelHint_DocumentsKeysAndNamesFocusedButton(t *testing.T) {
	a := keyboardGitApp(t)
	a.resizeSidebar(minSidebarWidth)
	a.gitSnap.Ahead = 2

	painted := gitHintText(t, a)
	for _, want := range []string{"↑↓", "␣", "⏎", "⇥", "esc"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("hint strip is missing %q, got %q", want, painted)
		}
	}

	// Focus Push: the compact ladder renders it as "[↑2]".
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	a.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if got := a.gitPanelButtons(minSidebarWidth - 1)[1].label; !strings.Contains(got, "↑") {
		t.Fatalf("expected the glyph ladder at minimum width, got %q", got)
	}
	painted = gitHintText(t, a)
	if !strings.Contains(painted, "Push") {
		t.Fatalf("hint strip must name the focused button, got %q", painted)
	}
}

// TestGitPanelHint_YieldsRowsToTheList pins the strip's manners: on a
// terminal too short to hold both, the hint gives its rows back rather
// than starving the change list it exists to explain.
func TestGitPanelHint_YieldsRowsToTheList(t *testing.T) {
	a := keyboardGitApp(t)
	// Sidebar height is a.height-1, so this leaves exactly one row
	// below the panel's fixed header rows for list + hint to share.
	a.height = gitPanelListTop + 2
	listH, hint := a.gitPanelBody()
	if listH != 1 || len(hint) != 0 {
		t.Fatalf("the one spare row belongs to the list: list %d, hint %d", listH, len(hint))
	}

	// Given room for both, the split still adds up to what's available.
	a.height = gitPanelListTop + 8
	listH, hint = a.gitPanelBody()
	if len(hint) == 0 {
		t.Fatal("with room to spare the hint should draw")
	}
	if listH+len(hint) != 7 {
		t.Fatalf("body split must add up: list %d + hint %d, want 7", listH, len(hint))
	}
}

// TestWrapHintSegments pins the strip's wrap rule: segments pack
// greedily, never split mid-binding (half a key hint is worse than
// none), and stop at the row cap.
func TestWrapHintSegments(t *testing.T) {
	got := wrapHintSegments([]string{"aaa", "bbb", "cc"}, 8, 3)
	want := []string{"aaa  bbb", "cc"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("greedy pack: got %v, want %v", got, want)
	}
	if got := wrapHintSegments([]string{"abcdefghij"}, 4, 2); len(got) != 1 || got[0] != "abcdefghij" {
		t.Fatalf("an oversize segment keeps its own row for clipping, got %v", got)
	}
	if got := wrapHintSegments([]string{"aaa", "bbb", "ccc"}, 3, 1); len(got) != 1 {
		t.Fatalf("maxRows must cap the strip, got %v", got)
	}
	if got := wrapHintSegments([]string{"aaa"}, 0, 3); got != nil {
		t.Fatalf("a zero-width sidebar has no room for a hint, got %v", got)
	}
}

// TestFocusGitPanel_GrabsAMousePanelThenToggles pins Esc-g's two jobs:
// on a panel already opened by mouse it takes the keyboard (you're
// asking to get in, not to leave), and pressing it again from inside
// falls back to the original toggle-to-explorer contract.
func TestFocusGitPanel_GrabsAMousePanelThenToggles(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.showGitPanel() // the mouse route: GIT header tab
	if a.gitPanelKeysOn() {
		t.Fatal("a mouse route must not steal the keyboard")
	}
	a.focusGitPanel()
	if !a.gitPanelKeysOn() || !a.gitPanel.active {
		t.Fatal("Esc-g on an open panel should grab focus, not close it")
	}
	a.focusGitPanel()
	if a.gitPanel.active || a.gitPanelKeysOn() {
		t.Fatal("Esc-g from inside should toggle back to the explorer")
	}
}

// TestGitPanelKeys_EditorPressDropsCapture pins the mouse half of the
// handoff: a press inside the sidebar keeps keyboard mode, a press
// anywhere else releases it. Without this a user who reached the panel
// from the ≡ menu and then clicked into the editor would find Enter
// and Space still being eaten by the panel.
func TestGitPanelKeys_EditorPressDropsCapture(t *testing.T) {
	a := keyboardGitApp(t)

	// The checkbox column: a sidebar press that opens no overlay.
	a.handleMouse(tcell.NewEventMouse(1, gitPanelListTop, tcell.Button1, 0))
	a.handleMouse(tcell.NewEventMouse(1, gitPanelListTop, tcell.ButtonNone, 0))
	if !a.gitPanelKeysOn() {
		t.Fatal("a press inside the sidebar must keep the keyboard mode")
	}

	a.handleMouse(tcell.NewEventMouse(60, 5, tcell.Button1, 0))
	a.handleMouse(tcell.NewEventMouse(60, 5, tcell.ButtonNone, 0))
	if a.gitPanelKeysOn() {
		t.Fatal("a press outside the sidebar must release the keyboard mode")
	}
	if !a.gitPanel.active {
		t.Fatal("the press releases focus, it does not close the panel")
	}
}

// -----------------------------------------------------------------------------
// Change-list scroll indicator
// -----------------------------------------------------------------------------

// gitPanelApp builds an app showing the Git panel with n synthetic
// change rows. Synthetic on purpose: the scroll indicator's whole
// subject is list length, and a fixture that has to `git init` forty
// files to test a thumb position is a fixture nobody will maintain.
func gitPanelApp(t *testing.T, n int) *App {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.gitSnap.IsRepo = true
	a.gitSnap.Branch = "main"
	a.gitPanel.active = true
	a.gitPanel.rows = make([]gitChangeRow, n)
	for i := range n {
		name := "file" + itoa(i) + ".go"
		a.gitPanel.rows[i] = gitChangeRow{
			Rel:  name,
			Abs:  filepath.Join(a.rootDir, name),
			Kind: filetree.GitChangeModified,
		}
	}
	return a
}

// gitPanelBarColumn draws the app and reads back the panel's rightmost
// column across the change list's rows — the bar's column when one is
// drawn.
func gitPanelBarColumn(t *testing.T, a *App) string {
	t.Helper()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	_, sy, sw, _ := a.sidebarRect()
	listH, _ := a.gitPanelBody()
	out := make([]rune, 0, listH)
	for i := range listH {
		c := cells[(sy+gitPanelListTop+i)*w+sw-1]
		if len(c.Runes) == 0 {
			out = append(out, ' ')
			continue
		}
		out = append(out, c.Runes[0])
	}
	return string(out)
}

// TestGitPanelScrollbar_HiddenWhenListFits pins the no-noise rule: a
// repo with a handful of changes shows no bar, and the column stays
// with the file names.
func TestGitPanelScrollbar_HiddenWhenListFits(t *testing.T) {
	a := gitPanelApp(t, 4)
	col := gitPanelBarColumn(t, a)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a 4-row list must draw no bar, got %q", col)
	}
	_, _, sw, _ := a.sidebarRect()
	listH, _ := a.gitPanelBody()
	if _, ok := a.gitPanelBar(sw, listH); ok {
		t.Fatal("gitPanelBar should agree with what was painted")
	}
}

// TestGitPanelScrollbar_ThumbTracksScroll is the bug this closes: a
// repo with forty changed files used to show about thirty and simply
// stop, with nothing saying more existed or where in the list you were.
// The bar spans the change rows only — the branch line, the button row
// and everything above stay clear, because they do not scroll.
func TestGitPanelScrollbar_ThumbTracksScroll(t *testing.T) {
	a := gitPanelApp(t, 60)
	listH, _ := a.gitPanelBody()
	if len(a.gitPanel.rows) <= listH {
		t.Fatalf("fixture should overflow: %d rows in %d", len(a.gitPanel.rows), listH)
	}

	top := gitPanelBarColumn(t, a)
	if !strings.HasPrefix(top, string(scrollbar.Thumb)) || !strings.ContainsRune(top, scrollbar.Track) {
		t.Fatalf("60 rows in a %d-row list: got %q", listH, top)
	}
	// The chrome above the list must not carry the bar.
	scr := a.screen.(tcell.SimulationScreen)
	_, _, sw, _ := a.sidebarRect()
	for y := range gitPanelListTop {
		if got := []rune(screenLine(scr, y))[sw-1]; got == scrollbar.Track || got == scrollbar.Thumb {
			t.Fatalf("chrome row %d carries the bar (%q) — it does not scroll", y, got)
		}
	}

	a.scrollGitPanel(len(a.gitPanel.rows))
	bottom := gitPanelBarColumn(t, a)
	if !strings.HasSuffix(bottom, string(scrollbar.Thumb)) {
		t.Fatalf("at the end the thumb must finish the track, got %q", bottom)
	}
	wantStart, wantLen, ok := scrollbar.Geom(len(a.gitPanel.rows), listH, a.gitPanel.scroll)
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row, got := range []rune(bottom) {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got != want {
			t.Fatalf("bar row %d: got %q, want %q (col %q)", row, got, want, bottom)
		}
	}
}

// TestGitPanelScrollbar_ReservesTheRowColumn pins the width math: the
// bar's column comes out of the row width before anything is drawn, so
// a long path is truncated one cell earlier instead of being painted
// underneath the bar. The stage checkbox is on the other end of the
// row, so it is untouched either way.
func TestGitPanelScrollbar_ReservesTheRowColumn(t *testing.T) {
	a := gitPanelApp(t, 60)
	long := strings.Repeat("deep/", 12) + "name.go"
	a.gitPanel.rows[0] = gitChangeRow{
		Rel:  long,
		Abs:  filepath.Join(a.rootDir, long),
		Kind: filetree.GitChangeModified,
	}
	row := []rune(gitScreenRow(t, a, gitPanelListTop))
	_, _, sw, _ := a.sidebarRect()
	if got := row[sw-1]; got != scrollbar.Thumb && got != scrollbar.Track {
		t.Fatalf("bar column should hold the bar, got %q (row %q)", got, string(row))
	}
	if row[sw-2] == ' ' {
		t.Fatalf("the label should run right up to the bar, got %q", string(row))
	}
	if row[1] != '●' {
		t.Fatalf("the stage checkbox must keep its cell, got %q", string(row))
	}

	// Once the listing fits, the column goes back to the label.
	a.gitPanel.rows = a.gitPanel.rows[:2]
	row = []rune(gitScreenRow(t, a, gitPanelListTop))
	if got := row[sw-1]; got == scrollbar.Thumb || got == scrollbar.Track {
		t.Fatalf("no bar expected once the list fits, got %q", string(row))
	}
}

// TestGitPanelScrollbar_ClickScrollsAndLeavesTheRestAlone is the
// hit-test three-way: the bar's column scrolls, the row beside it still
// opens its diff, and the checkbox column still stages — all at the
// same y, so the bar cannot be silently eating row clicks.
func TestGitPanelScrollbar_ClickScrollsAndLeavesTheRestAlone(t *testing.T) {
	a := gitPanelApp(t, 60)
	a.draw()
	_, _, sw, _ := a.sidebarRect()
	listH, _ := a.gitPanelBody()
	barX, ok := a.gitPanelBar(sw, listH)
	if !ok {
		t.Fatal("fixture should draw a bar")
	}
	y := gitPanelListTop + listH - 1

	// The bar: scrolls, opens nothing, moves no selection. Driven
	// through the real dispatcher, because the routing is half the
	// claim — the press has to survive the splitter check and the
	// tree-bar check before sidebarClick ever sees it.
	a.handleMouse(tcell.NewEventMouse(barX, y, tcell.Button1, 0))
	a.handleMouse(tcell.NewEventMouse(barX, y, tcell.ButtonNone, 0))
	if want := len(a.gitPanel.rows) - listH; a.gitPanel.scroll != want {
		t.Fatalf("bar click: scroll %d, want %d", a.gitPanel.scroll, want)
	}
	if a.overlays.IsOpen() {
		t.Fatal("a bar click must not open the diff")
	}
	if a.gitPanel.selected != 0 {
		t.Fatalf("a bar click must not move the selection, got %d", a.gitPanel.selected)
	}

	// The checkbox column at the same y: stages, opens nothing.
	idx := a.gitPanel.scroll + y - gitPanelListTop
	abs := a.gitPanel.rows[idx].Abs
	a.gitPanelClick(1, y)
	if a.commitCheckOn(abs) {
		t.Fatal("the checkbox column must still toggle the stage mark")
	}
	if a.overlays.IsOpen() {
		t.Fatal("the checkbox must not open the diff")
	}

	// The label between them: still activates the row.
	a.gitPanelClick(6, y)
	if a.gitPanel.selected != idx {
		t.Fatalf("a row click should select row %d, got %d", idx, a.gitPanel.selected)
	}

	// The button row and the branch line are above the bar's span.
	if a.gitPanelBarHit(barX, 1) || a.gitPanelBarHit(barX, 2) {
		t.Fatal("the bar must not claim the branch or button rows")
	}
	if a.gitPanelBarHit(barX, gitPanelListTop+listH) {
		t.Fatal("the bar must stop where the list does")
	}
}

// TestGitPanelScrollbar_KeepsClearOfTheHintStrip pins the interaction
// with keyboard mode: the hint strip docks at the bottom of the panel
// and takes its rows out of the list, so the bar has to shorten with
// the list rather than paint over the bindings.
func TestGitPanelScrollbar_KeepsClearOfTheHintStrip(t *testing.T) {
	a := gitPanelApp(t, 60)
	a.gitPanel.keys = true
	listH, hint := a.gitPanelBody()
	if len(hint) == 0 {
		t.Fatal("keyboard mode should dock a hint strip")
	}
	// Every list row carries the bar, so its span is provably the
	// list's — not just the window this helper happened to read.
	for row, got := range []rune(gitPanelBarColumn(t, a)) {
		if got != scrollbar.Track && got != scrollbar.Thumb {
			t.Fatalf("list row %d of %d has no bar cell, got %q", row, listH, got)
		}
	}
	scr := a.screen.(tcell.SimulationScreen)
	_, _, sw, sh := a.sidebarRect()
	for i := range hint {
		if got := []rune(screenLine(scr, sh-len(hint)+i))[sw-1]; got == scrollbar.Track || got == scrollbar.Thumb {
			t.Fatalf("hint row %d carries the bar (%q)", i, got)
		}
	}
}

// TestGitPanelScrollbar_ThumbBrightensWhileDragging pins the panel bar
// into the same idle/active language the splitter, the editor bar and
// the tree bar already speak: Muted at rest, Accent under the hand. The
// colour is derived from dragMode at paint time rather than latched on
// press, so a drag that ends through some other route cannot leave a
// thumb lit with nothing grabbing it — the second half of this test is
// what pins that.
func TestGitPanelScrollbar_ThumbBrightensWhileDragging(t *testing.T) {
	a := gitPanelApp(t, 80)
	a.draw()
	_, sy, sw, _ := a.sidebarRect()
	listH, _ := a.gitPanelBody()
	barX, ok := a.gitPanelBar(sw, listH)
	if !ok {
		t.Fatal("fixture should draw a bar")
	}
	thumbY := sy + gitPanelListTop

	if r, fg := barCell(t, a, barX, thumbY); r != scrollbar.Thumb || fg != a.theme.Muted {
		t.Fatalf("idle thumb: got %q/%v, want %q/%v", r, fg, scrollbar.Thumb, a.theme.Muted)
	}

	a.handleMouse(tcell.NewEventMouse(barX, thumbY, tcell.Button1, 0))
	if r, fg := barCell(t, a, barX, thumbY); r != scrollbar.Thumb || fg != a.theme.Accent {
		t.Fatalf("dragged thumb: got %q/%v, want %q/%v", r, fg, scrollbar.Thumb, a.theme.Accent)
	}

	// Not via release: an overlay stealing the drag is exactly the
	// route a latched flag would get wrong.
	a.dragMode = dragNone
	if _, fg := barCell(t, a, barX, thumbY); fg != a.theme.Muted {
		t.Fatalf("thumb stayed lit after the drag ended, fg %v", fg)
	}
}
