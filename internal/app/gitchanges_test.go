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
	rows := buildGitChangesRows(dirty, root)
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

// TestBuildGitChangesRows_MarksUntrackedDirs verifies a dirty path that
// is a directory on disk gets IsDir plus a trailing slash — that's how
// the row communicates "this reveals in the tree, not a diff". A
// deleted path (stat fails) must never be marked as a directory.
func TestBuildGitChangesRows_MarksUntrackedDirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dirty := map[string]filetree.GitChangeKind{
		sub:                             filetree.GitChangeAdded,
		filepath.Join(root, "gone.txt"): filetree.GitChangeDeleted,
	}
	rows := buildGitChangesRows(dirty, root)
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
	if rows := buildGitChangesRows(dirty, root); len(rows) != 0 {
		t.Fatalf("expected no rows for out-of-root paths, got %+v", rows)
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
	if a.gitPanelActive {
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
	if a.gitPanelActive {
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
	if !a.gitPanelActive {
		t.Fatal("panel should be active")
	}
	if len(a.gitPanelRows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(a.gitPanelRows), a.gitPanelRows)
	}
	if a.gitPanelRows[0].Rel != "modified.txt" || a.gitPanelRows[0].Kind != filetree.GitChangeModified {
		t.Fatalf("row 0: %+v", a.gitPanelRows[0])
	}
	if a.gitPanelRows[1].Rel != "untracked.txt" || a.gitPanelRows[1].Kind != filetree.GitChangeAdded {
		t.Fatalf("row 1: %+v", a.gitPanelRows[1])
	}
}

// TestToggleGitPanel_TogglesBackToExplorer pins the Esc-g round trip:
// the same gesture that opens the Git panel returns to the explorer,
// mirroring how Esc-t toggles the sidebar itself.
func TestToggleGitPanel_TogglesBackToExplorer(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if !a.gitPanelActive {
		t.Fatal("first toggle should activate the panel")
	}
	a.toggleGitPanel()
	if a.gitPanelActive {
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
	if !a.sidebarShown || !a.gitPanelActive {
		t.Fatalf("expected sidebar shown in git mode, got shown=%v git=%v",
			a.sidebarShown, a.gitPanelActive)
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
	if !a.gitPanelActive {
		t.Fatal("clicking the GIT tab should activate the panel")
	}
	a.sidebarClick(1, 0) // click "EXPLORER"
	if a.gitPanelActive {
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
	a.gitPanelClick(2) // first row: modified.txt
	if !a.diffOpen {
		t.Fatal("row click should open the diff view")
	}
	if !strings.Contains(a.diffTitle, "modified.txt") {
		t.Fatalf("diff title should carry the file name, got %q", a.diffTitle)
	}
	body := strings.Join(a.diffRaw, "\n")
	if !strings.Contains(body, "-two") || !strings.Contains(body, "+TWO") {
		t.Fatalf("diff body should show the change, got:\n%s", body)
	}
	if a.diffOpenPath != modified || a.diffHover != 1 {
		t.Fatalf("Open file should be armed and focused, path=%q hover=%d",
			a.diffOpenPath, a.diffHover)
	}

	a.handleDiffKey(keyEv(tcell.KeyEnter, 0))
	if a.diffOpen {
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
	a.gitPanelClick(3) // second row: untracked.txt
	if !a.diffOpen {
		t.Fatal("row click should open the diff view")
	}
	body := strings.Join(a.diffRaw, "\n")
	if !strings.Contains(body, "+new") {
		t.Fatalf("untracked diff should show the file as added, got:\n%s", body)
	}
	if a.diffOpenPath != untracked {
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
	if len(a.gitPanelRows) != 1 || a.gitPanelRows[0].Kind != filetree.GitChangeDeleted {
		t.Fatalf("expected one deleted row, got %+v", a.gitPanelRows)
	}
	a.gitPanelClick(2)
	if !a.diffOpen {
		t.Fatal("deleted row should open the diff view")
	}
	if a.diffOpenPath != "" {
		t.Fatal("deleted files must not arm Open file")
	}
	body := strings.Join(a.diffRaw, "\n")
	if !strings.Contains(body, "-so long") {
		t.Fatalf("diff body should show the removed line, got %q", body)
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
	if len(a.gitPanelRows) != 1 || !a.gitPanelRows[0].IsDir {
		t.Fatalf("expected one dir row, got %+v", a.gitPanelRows)
	}
	a.gitPanelClick(2)
	if a.gitPanelActive {
		t.Fatal("dir activation should switch back to the explorer")
	}
	if a.activeFolder != sub {
		t.Fatalf("active folder: got %q, want %q", a.activeFolder, sub)
	}
}

// TestGitPanelClick_IgnoresChrome verifies clicks on the branch row and
// below the last change row are inert — only real rows activate.
func TestGitPanelClick_IgnoresChrome(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	a.gitPanelClick(1) // branch row
	a.gitPanelClick(50)
	if a.diffOpen || a.confirmOpen {
		t.Fatal("chrome clicks should not open anything")
	}
}

// TestScrollGitPanel_Clamps pins the wheel-scroll bounds: a list
// shorter than the viewport can't scroll at all, and a long list stops
// at the last full window.
func TestScrollGitPanel_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanelRows = make([]gitChangeRow, 100)
	_, _, _, sh := a.sidebarRect()
	a.scrollGitPanel(1000)
	if want := 100 - (sh - 2); a.gitPanelScroll != want {
		t.Fatalf("scroll past end: got %d, want %d", a.gitPanelScroll, want)
	}
	a.scrollGitPanel(-1000)
	if a.gitPanelScroll != 0 {
		t.Fatalf("scroll past top: got %d, want 0", a.gitPanelScroll)
	}
	a.gitPanelRows = a.gitPanelRows[:3]
	a.scrollGitPanel(5)
	if a.gitPanelScroll != 0 {
		t.Fatalf("short list should never scroll, got %d", a.gitPanelScroll)
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
	row0 := screenLine(scr, 2)
	if !strings.Contains(row0, "M") || !strings.Contains(row0, "modified.txt") {
		t.Fatalf("row 0: %q", row0)
	}
	if got := []rune(row0)[1]; got != 'M' {
		t.Fatalf("letter cell: got %q", got)
	}

	// Empty state: pretend the repo went clean under us.
	a.gitPanelRows = nil
	a.draw()
	a.screen.Show()
	if body := screenLine(scr, 2); !strings.Contains(body, "No uncommitted changes") {
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
	a.gitBranch = "main"
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
	a.gitAhead, a.gitBehind = 2, 1
	if got := a.statusGitSegment(); got != " main ↑2 ↓1 · 2 " {
		t.Fatalf("diverged segment: got %q", got)
	}
	a.gitBehind = 0
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
	if !a.gitPanelActive {
		t.Fatal("click on the git segment should activate the panel")
	}
	a.showExplorerPanel()
	a.statusBarClick(sx + 1) // far left — file info territory
	if a.gitPanelActive {
		t.Fatal("click outside the segment should not activate the panel")
	}
}

// TestMenuGitChangesRow pins the ≡ menu contract from CLAUDE.md: the
// feature must be reachable from the main menu. The row exists, carries
// the Esc-g shortcut, is greyed out outside a repo, and enabled inside
// one.
func TestMenuGitChangesRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	item := menuItemByLabel(t, a, "Git changes")
	if item.shortcut != "Esc g" {
		t.Fatalf("shortcut: got %q, want %q", item.shortcut, "Esc g")
	}
	if item.enabled(a) {
		t.Fatal("row should be disabled outside a git repo")
	}
	a.gitBranch = "main"
	if !item.enabled(a) {
		t.Fatal("row should be enabled inside a git repo")
	}
}

// TestRefreshGitStatus_RebuildsActivePanel verifies the 10-second
// refresh path keeps an active panel live: a file changed on disk after
// activation shows up on the next status refresh without re-toggling.
func TestRefreshGitStatus_RebuildsActivePanel(t *testing.T) {
	a, _, _ := dirtyRepoApp(t)
	a.toggleGitPanel()
	if len(a.gitPanelRows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(a.gitPanelRows))
	}
	writeFileT(t, filepath.Join(a.rootDir, "another.txt"), "hi\n")
	a.refreshGitStatus()
	if len(a.gitPanelRows) != 3 {
		t.Fatalf("active panel should track new changes, got %d rows", len(a.gitPanelRows))
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
