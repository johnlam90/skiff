// =============================================================================
// File: internal/app/gitchanges.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-07-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

// Git panel — the source-control side of the sidebar. The sidebar header
// becomes two tabs, EXPLORER and GIT: the first shows the familiar file
// tree, the second lists every uncommitted change in the project sorted
// by path, each row carrying a colored status letter (M modified,
// A added/untracked, D deleted, R renamed) — the same VS Code vocabulary
// and the same colors the tree tint uses.
//
// Clicking a row opens the file's full colored diff in the info modal
// with an [ Open file ] button that jumps to the first changed line —
// mirroring VS Code, where clicking a change in Source Control shows the
// diff. Deleted paths show the diff with no open button (there's nothing
// left to open); untracked directories flip back to the explorer and
// reveal themselves.
//
// The panel is reachable three ways, all mouse-first: the GIT header
// tab, the branch segment in the status bar, and ≡ → Git changes (or
// Esc-g), which toggle between the two sidebar views.
//
// Like everything git in this editor, it is read-only and best-effort.
// Staging and committing stay in the shell — that's a tmux pane away.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudmanic/spice-edit/internal/editor"
	"github.com/cloudmanic/spice-edit/internal/filetree"
	"github.com/cloudmanic/spice-edit/internal/theme"
	"github.com/gdamore/tcell/v2"
)

// sidebarHeaderExplorer / sidebarHeaderGit are the header tab labels.
// The leading space on EXPLORER matches the tree's original header
// indent so the explorer tab sits exactly where the old header did.
const (
	sidebarHeaderExplorer = " EXPLORER"
	sidebarHeaderGit      = "GIT"
	// sidebarHeaderGap is the space between the two header tabs.
	sidebarHeaderGap = 3
)

// gitChangeRow is one entry in the Git panel: the path relative to the
// project root (what the user reads), the absolute path (what the
// actions consume), and the change kind that picks the letter + color.
// IsDir marks untracked directories — git porcelain reports those as a
// single collapsed entry, and activating one means "reveal it in the
// tree" rather than "show a diff".
type gitChangeRow struct {
	Rel   string
	Abs   string
	Kind  filetree.GitChangeKind
	IsDir bool
}

// toggleGitPanel is the Esc-g / ≡ menu / status-bar action: flip the
// sidebar between the explorer and the Git panel, showing the sidebar
// first if it's hidden. Guards mirror the finder: single-file mode has
// no project scope, and a non-repo has nothing to list — both flash
// instead of switching.
func (a *App) toggleGitPanel() {
	if a.tree == nil {
		a.flash("Git changes isn't available in single-file mode")
		return
	}
	if a.gitPanelActive && a.sidebarShown {
		a.showExplorerPanel()
		return
	}
	// Repo-ness comes from the cached branch (stamped at startup and on
	// every 10-second tick) so the toggle never blocks on git; the async
	// kick below brings the rows fully up to date a beat later.
	if a.gitBranch == "" {
		a.flash("Not a git repository")
		return
	}
	a.sidebarShown = true
	a.gitPanelActive = true
	a.gitPanelScroll = 0
	a.rebuildGitChangesRows()
	a.refreshGitStatusAsync()
}

// showGitPanel switches the sidebar to the Git panel (the GIT header
// tab's click action). Unlike toggleGitPanel it never switches away —
// clicking the tab you're already on is a no-op, like every tab bar.
func (a *App) showGitPanel() {
	if a.tree == nil {
		return
	}
	if a.gitBranch == "" {
		a.flash("Not a git repository")
		return
	}
	a.gitPanelActive = true
	a.rebuildGitChangesRows()
	a.refreshGitStatusAsync()
}

// showExplorerPanel switches the sidebar back to the file tree (the
// EXPLORER header tab's click action).
func (a *App) showExplorerPanel() {
	a.gitPanelActive = false
}

// menuGitChanges is the ≡ menu entry for the Git panel.
func (a *App) menuGitChanges() {
	a.closeMenu()
	a.toggleGitPanel()
}

// hasGitRepo is the menu enabled-predicate for git-scoped rows: we need
// a project tree and a detected branch (refreshGitStatus clears the
// branch whenever the root stops being a repo).
func (a *App) hasGitRepo() bool {
	return a.tree != nil && a.gitBranch != ""
}

// rebuildGitChangesRows recomputes the sorted row list from the tree's
// dirty-file set and clamps the scroll into range. Called on panel
// activation and from refreshGitStatus while the panel is up, so the
// list tracks the same 10-second cadence as the tree tint.
func (a *App) rebuildGitChangesRows() {
	if a.tree == nil {
		a.gitPanelRows = nil
		return
	}
	a.gitPanelRows = buildGitChangesRows(a.tree.DirtyFiles, a.tree.Root.Path)
	a.scrollGitPanel(0)
}

// buildGitChangesRows converts the dirty-path map into display rows
// sorted by relative path. Pure so tests can drive it without a repo;
// the only filesystem touch is a stat to spot untracked directories
// (which is why deleted paths — stat fails — never get IsDir).
func buildGitChangesRows(dirty map[string]filetree.GitChangeKind, root string) []gitChangeRow {
	rows := make([]gitChangeRow, 0, len(dirty))
	for abs, kind := range dirty {
		rel, ok := relFromRoot(abs, root)
		if !ok || rel == "." {
			continue
		}
		row := gitChangeRow{Rel: filepath.ToSlash(rel), Abs: abs, Kind: kind}
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			row.IsDir = true
			row.Rel += "/"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Rel < rows[j].Rel })
	return rows
}

// gitPanelClick routes a left press inside the Git panel: row 1 is the
// branch label — clicking it opens the branch's commit history — and
// rows 2+ activate the change under the cursor. (Row 0 — the header
// tabs — is handled by sidebarClick before we get here.)
func (a *App) gitPanelClick(y int) {
	if y == 1 {
		a.openGitLog("History · "+a.gitBranch, "")
		return
	}
	idx := a.gitPanelScroll + y - 2
	if y < 2 || idx < 0 || idx >= len(a.gitPanelRows) {
		return
	}
	a.activateGitChangeRow(a.gitPanelRows[idx])
}

// activateGitChangeRow performs a row's action. Files show their full
// colored diff with an [ Open file ] jump; deleted paths show the diff
// alone; untracked directories flip to the explorer and reveal
// themselves in the tree.
func (a *App) activateGitChangeRow(row gitChangeRow) {
	if row.IsDir {
		a.showExplorerPanel()
		a.setActiveFolder(row.Abs)
		_, _, _, sh := a.sidebarRect()
		listH := sh - 2
		if listH < 0 {
			listH = 0
		}
		a.tree.Reveal(row.Abs, listH)
		return
	}

	lines := loadGitFileDiff(a.rootDir, row.Abs, row.Kind == filetree.GitChangeAdded)
	if len(lines) == 0 {
		lines = []string{"No git diff available for this file."}
	}
	openPath := row.Abs
	if row.Kind == filetree.GitChangeDeleted {
		// Nothing left on disk to open — the diff is the whole story.
		openPath = ""
	}
	a.openDiffView("Diff · "+row.Rel, lines, openPath, row.Abs)
}

// openFileAtFirstChange opens path in a tab and parks the cursor on its
// first changed line, so "take me to what I changed" is one gesture.
func (a *App) openFileAtFirstChange(path string) {
	a.openFile(path)
	tab := a.activeTabPtr()
	// openFile flashes and keeps the previous tab on failure — only jump
	// the cursor when the file we asked for is actually the one in front.
	if tab == nil || tab.Path != path || len(tab.GitLines) == 0 {
		return
	}
	first := -1
	for line := range tab.GitLines {
		if first < 0 || line < first {
			first = line
		}
	}
	if first >= 0 {
		tab.MoveCursorTo(editor.Position{Line: first}, false)
	}
}

// scrollGitPanel nudges the panel viewport by delta rows, clamped so the
// list can't scroll past its own ends. Delta 0 is a pure re-clamp,
// used after the row list shrinks under an existing scroll offset.
func (a *App) scrollGitPanel(delta int) {
	_, _, _, sh := a.sidebarRect()
	max := len(a.gitPanelRows) - (sh - 2)
	if max < 0 {
		max = 0
	}
	a.gitPanelScroll += delta
	if a.gitPanelScroll > max {
		a.gitPanelScroll = max
	}
	if a.gitPanelScroll < 0 {
		a.gitPanelScroll = 0
	}
}

// gitTabLabel returns the GIT header tab's text: the bare word, plus
// the count of changed paths when there are any — the same at-a-glance
// badge VS Code puts on its Source Control icon.
func (a *App) gitTabLabel() string {
	label := sidebarHeaderGit
	if a.tree != nil && len(a.tree.DirtyFiles) > 0 {
		label += " " + itoa(len(a.tree.DirtyFiles))
	}
	return label
}

// sidebarHeaderHit maps a click x-offset on the sidebar's header row to
// the tab it landed on: "explorer", "git", or "" for the space between
// and beyond. The GIT zone includes its count badge so the whole label
// is one target. Shares its geometry with drawSidebarHeader so the two
// can't drift.
func (a *App) sidebarHeaderHit(localX int) string {
	eEnd := runeLen(sidebarHeaderExplorer)
	if localX >= 0 && localX < eEnd {
		return "explorer"
	}
	if !a.hasGitRepo() {
		return ""
	}
	gStart := eEnd + sidebarHeaderGap
	if localX >= gStart && localX < gStart+runeLen(a.gitTabLabel()) {
		return "git"
	}
	return ""
}

// drawSidebarHeader paints the EXPLORER / GIT tab row at the top of the
// sidebar. The active tab keeps the header's traditional muted-bold
// look; the inactive one drops to Subtle so the eye reads one of them
// as "current". The GIT tab only appears for git projects — a plain
// directory keeps the original single-header look.
func (a *App) drawSidebarHeader(sx, sy, sw int) {
	bg := a.theme.SidebarBG
	active := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted).Bold(true)
	inactive := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)

	// Clear the row first — the tree's own header text may sit beneath.
	for cx := sx; cx < sx+sw; cx++ {
		a.screen.SetContent(cx, sy, ' ', nil, active)
	}

	eStyle, gStyle := active, inactive
	if a.gitPanelActive {
		eStyle, gStyle = inactive, active
	}
	// drawClipped (not drawAt) so a count badge on a min-width sidebar
	// can't spill past the splitter into the editor pane.
	drawClipped(a.screen, sx, sy, sw, sidebarHeaderExplorer, eStyle)
	if a.hasGitRepo() {
		gx := sx + runeLen(sidebarHeaderExplorer) + sidebarHeaderGap
		drawClipped(a.screen, gx, sy, sx+sw-gx, a.gitTabLabel(), gStyle)
	}
}

// drawGitPanel paints the Git side of the sidebar: header tabs, the
// branch name where the explorer shows the project root, then one row
// per uncommitted change.
//
// Rows (relative to sy):
//
//	0    EXPLORER   GIT      (drawSidebarHeader)
//	1    branch name
//	2+   change rows — " M name  dir", letter colored, dir dimmed
func (a *App) drawGitPanel(sx, sy, sw, sh int) {
	bg := a.theme.SidebarBG
	fill := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	for cy := sy; cy < sy+sh; cy++ {
		for cx := sx; cx < sx+sw; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, fill)
		}
	}
	a.drawSidebarHeader(sx, sy, sw)

	branchStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text).Bold(true)
	drawClipped(a.screen, sx+1, sy+1, sw-1, a.gitBranch, branchStyle)

	listH := sh - 2
	if listH < 0 {
		listH = 0
	}
	a.scrollGitPanel(0)

	if len(a.gitPanelRows) == 0 {
		muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
		drawClipped(a.screen, sx+1, sy+2, sw-1, "No uncommitted changes", muted)
		return
	}
	for i := 0; i < listH; i++ {
		idx := a.gitPanelScroll + i
		if idx >= len(a.gitPanelRows) {
			break
		}
		a.drawGitPanelRow(sx, sy+2+i, sw, a.gitPanelRows[idx])
	}
}

// drawGitPanelRow paints one change row: the status letter in its kind
// color, the basename in the row's main color, then the parent dir
// dimmed — basename first because the sidebar is narrow and the file
// name is what the user scans for (the same reason VS Code's SCM list
// leads with it).
func (a *App) drawGitPanelRow(sx, ry, sw int, row gitChangeRow) {
	bg := a.theme.SidebarBG
	letterStyle := tcell.StyleDefault.Background(bg).Foreground(gitKindColor(a.theme, row.Kind)).Bold(true)
	nameStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	if a.tree != nil && row.Abs == a.tree.ActiveFile {
		nameStyle = tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	}
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	name := row.Rel
	dir := ""
	if idx := strings.LastIndex(strings.TrimSuffix(row.Rel, "/"), "/"); idx >= 0 {
		name = row.Rel[idx+1:]
		dir = row.Rel[:idx]
	}

	drawAt(a.screen, sx+1, ry, gitKindLetter(row.Kind), letterStyle)
	x := sx + 3
	x = drawClipped(a.screen, x, ry, sx+sw-x, name, nameStyle)
	if dir != "" {
		drawClipped(a.screen, x+2, ry, sx+sw-(x+2), dir, mutedStyle)
	}
}

// drawClipped draws s at (x, y) using at most maxW cells and returns the
// x position just past the last rune drawn. Shared by the git panel's
// rows so clipping stays consistent in a sidebar the user can resize.
func drawClipped(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) int {
	if maxW <= 0 {
		return x
	}
	runes := []rune(s)
	n := len(runes)
	if n > maxW {
		n = maxW
	}
	for i := 0; i < n; i++ {
		scr.SetContent(x+i, y, runes[i], nil, st)
	}
	return x + n
}

// gitKindLetter maps a change kind to its single-letter badge — the
// same vocabulary VS Code trained everyone on.
func gitKindLetter(kind filetree.GitChangeKind) string {
	switch kind {
	case filetree.GitChangeAdded:
		return "A"
	case filetree.GitChangeDeleted:
		return "D"
	case filetree.GitChangeRenamed:
		return "R"
	default:
		return "M"
	}
}

// gitKindColor maps a change kind to its theme color — the same mapping
// the file tree uses, so a file reads the same color everywhere.
func gitKindColor(th theme.Theme, kind filetree.GitChangeKind) tcell.Color {
	switch kind {
	case filetree.GitChangeAdded:
		return th.GitAdded
	case filetree.GitChangeDeleted:
		return th.GitDeleted
	case filetree.GitChangeRenamed:
		return th.GitRenamed
	case filetree.GitChangeMixed:
		return th.GitMixed
	default:
		return th.GitModified
	}
}

// statusGitSegment returns the status bar's right-hand git text: the
// branch, ↑ ↓ arrows when the branch has diverged from its upstream,
// and " · N" when N paths have uncommitted changes. Empty when the
// project isn't a repo. Pure so the draw path and the click hit-test
// derive the segment's width from the same source and can't drift.
func (a *App) statusGitSegment() string {
	if a.gitBranch == "" {
		return ""
	}
	seg := " " + a.gitBranch
	if a.gitAhead > 0 {
		seg += " ↑" + itoa(a.gitAhead)
	}
	if a.gitBehind > 0 {
		seg += " ↓" + itoa(a.gitBehind)
	}
	if a.tree != nil && len(a.tree.DirtyFiles) > 0 {
		seg += " · " + itoa(len(a.tree.DirtyFiles))
	}
	return seg + " "
}

// statusBarClick handles a left press on the status bar row. The git
// segment on the right is the one live target — clicking it flips the
// sidebar to the Git panel, the mouse-first sibling of Esc-g.
func (a *App) statusBarClick(x int) {
	seg := a.statusGitSegment()
	if seg == "" {
		return
	}
	sx, _, sw, _ := a.statusRect()
	rw := runeLen(seg)
	if rw >= sw {
		return
	}
	if x >= sx+sw-rw && x < sx+sw {
		a.toggleGitPanel()
	}
}
