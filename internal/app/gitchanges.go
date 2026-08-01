// =============================================================================
// File: internal/app/gitchanges.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
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
// The write side lives here too: a branch line (click to switch), a
// button row ([ Commit ] [ Push ] [ Pull ] [ ⋯ ]), and a commit
// checkbox on every change row — the mutations themselves are in
// gitops.go. While a panel-opened diff is up, ↑↓ walk the change list
// file by file (diffWalk), which is the way to read a review.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/theme"
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
	// Drop commit-check entries whose paths are no longer dirty, and
	// keep the walk selection inside the new list. Without the prune, a
	// file unchecked, committed elsewhere, and dirtied again would come
	// back silently unchecked.
	if len(a.gitCommitChecks) > 0 {
		live := make(map[string]bool, len(a.gitPanelRows))
		for _, r := range a.gitPanelRows {
			live[r.Abs] = true
		}
		for abs := range a.gitCommitChecks {
			if !live[abs] {
				delete(a.gitCommitChecks, abs)
			}
		}
	}
	if a.gitPanelSelected >= len(a.gitPanelRows) {
		a.gitPanelSelected = len(a.gitPanelRows) - 1
	}
	if a.gitPanelSelected < 0 {
		a.gitPanelSelected = 0
	}
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
// branch line (opens the switch-branch picker), row 2 the button row,
// and rows below activate — or checkbox-toggle — the change under the
// cursor. (Row 0 — the header tabs — is handled by sidebarClick before
// we get here.)
func (a *App) gitPanelClick(x, y int) {
	if y == 1 {
		a.menuGitSwitchBranch()
		return
	}
	if y == 2 {
		for _, b := range a.gitPanelButtons() {
			if x >= b.x0 && x < b.x1 {
				sx, sy, _, _ := a.sidebarRect()
				b.action(a, sx+b.x0, sy+3)
				return
			}
		}
		return
	}
	idx := a.gitPanelScroll + y - gitPanelListTop
	if y < gitPanelListTop || idx < 0 || idx >= len(a.gitPanelRows) {
		return
	}
	row := a.gitPanelRows[idx]
	// The leading checkbox cell toggles the row in/out of the commit
	// set without opening anything.
	if x <= 2 && !row.IsDir {
		a.toggleCommitCheck(row.Abs)
		return
	}
	a.gitPanelSelected = idx
	a.activateGitChangeRow(row)
	if !row.IsDir {
		// Recorded after activation — openDiffView's closeAllModals
		// resets the index, and this write is what arms ↑↓ walking.
		a.diffPanelRow = idx
	}
}

// gitPanelListTop is the sidebar row where the change list starts:
// header tabs, branch line, buttons row, then files.
const gitPanelListTop = 3

// gitPanelBtn is one clickable action on the panel's button row, with
// its x-range in sidebar-local cells.
type gitPanelBtn struct {
	label  string
	x0, x1 int
	action func(a *App, anchorX, anchorY int)
}

// gitPanelButtons lays out the button row. Computed on the fly so the
// click handler and the renderer can never disagree about geometry.
func (a *App) gitPanelButtons() []gitPanelBtn {
	labels := []string{"[ Commit ]", "[ Push ]", "[ Pull ]", "[ ⋯ ]"}
	actions := []func(a *App, x, y int){
		func(app *App, _, _ int) { app.menuGitCommit() },
		func(app *App, _, _ int) { app.menuGitPush() },
		func(app *App, _, _ int) { app.menuGitPull() },
		func(app *App, x, y int) { app.openGitExtras(x, y) },
	}
	out := make([]gitPanelBtn, 0, len(labels))
	x := 1
	for i, l := range labels {
		w := runeLen(l)
		out = append(out, gitPanelBtn{label: l, x0: x, x1: x + w, action: actions[i]})
		x += w + 1
	}
	return out
}

// toggleCommitCheck flips a path's membership in the commit set.
// Absent means checked, so the first toggle writes an explicit false.
func (a *App) toggleCommitCheck(abs string) {
	if a.gitCommitChecks == nil {
		a.gitCommitChecks = map[string]bool{}
	}
	if checked, explicit := a.gitCommitChecks[abs]; explicit {
		a.gitCommitChecks[abs] = !checked
		return
	}
	a.gitCommitChecks[abs] = false
}

// ensureGitRowVisible scrolls the panel list so row idx is on screen.
func (a *App) ensureGitRowVisible(idx int) {
	_, _, _, sh := a.sidebarRect()
	listH := sh - gitPanelListTop
	if listH < 1 {
		return
	}
	if idx < a.gitPanelScroll {
		a.gitPanelScroll = idx
	}
	if idx >= a.gitPanelScroll+listH {
		a.gitPanelScroll = idx - listH + 1
	}
}

// diffWalk moves the panel-opened diff to the previous/next changed
// file (druk's ↑↓ walk: the diff is how you read the changes, so the
// arrows move between them). Returns false when the open diff didn't
// come from the panel — the caller falls back to plain scrolling.
// Directory rows (reveal-in-tree, no diff) are skipped; the ends clamp.
func (a *App) diffWalk(dir int) bool {
	if a.diffPanelRow < 0 || !a.gitPanelActive {
		return false
	}
	idx := a.diffPanelRow
	for {
		idx += dir
		if idx < 0 || idx >= len(a.gitPanelRows) {
			return true // consume the key; stay on the current file
		}
		if !a.gitPanelRows[idx].IsDir {
			break
		}
	}
	a.gitPanelSelected = idx
	a.ensureGitRowVisible(idx)
	a.activateGitChangeRow(a.gitPanelRows[idx])
	a.diffPanelRow = idx
	return true
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

	lines := loadGitFileDiff(a.rootDir, a.diffBase, row.Abs, row.Kind == filetree.GitChangeAdded)
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
	max := len(a.gitPanelRows) - (sh - gitPanelListTop)
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
// sidebar. The active tab is Text bold, the inactive one Muted — both
// clear WCAG AA on the sidebar (the old Subtle inactive label sat at
// 3.4:1, below the text bar), and the weight+brightness pair still
// reads one of them as "current". The GIT tab only appears for git
// projects — a plain directory keeps the original single-header look.
func (a *App) drawSidebarHeader(sx, sy, sw int) {
	bg := a.theme.SidebarBG
	active := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text).Bold(true)
	inactive := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

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

	// Branch line: name plus how far it is from upstream — the same
	// vocabulary as the status-bar segment, promoted to where the
	// source-control work happens. Clicking it opens the branch picker.
	branchStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text).Bold(true)
	branchLine := "⎇ " + a.gitBranch
	if a.diffBase != "" {
		branchLine += " ⇆ " + a.diffBase
	}
	if a.gitAhead > 0 {
		branchLine += fmt.Sprintf(" ↑%d", a.gitAhead)
	}
	if a.gitBehind > 0 {
		branchLine += fmt.Sprintf(" ↓%d", a.gitBehind)
	}
	drawClipped(a.screen, sx+1, sy+1, sw-1, branchLine, branchStyle)

	// Buttons row — commit / push / pull / everything else. While a
	// mutation runs, say so instead of pretending clicks would work.
	if a.gitOpBusy {
		muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
		drawClipped(a.screen, sx+1, sy+2, sw-1, "git is working…", muted)
	} else {
		for _, b := range a.gitPanelButtons() {
			drawButton(a.screen, sx+b.x0, sy+2, b.label, bg, a.theme.Accent, false)
		}
	}

	listH := sh - gitPanelListTop
	if listH < 0 {
		listH = 0
	}
	a.scrollGitPanel(0)

	if len(a.gitPanelRows) == 0 {
		muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
		drawClipped(a.screen, sx+1, sy+gitPanelListTop, sw-1, "No uncommitted changes", muted)
		return
	}
	for i := 0; i < listH; i++ {
		idx := a.gitPanelScroll + i
		if idx >= len(a.gitPanelRows) {
			break
		}
		a.drawGitPanelRow(sx, sy+gitPanelListTop+i, sw, a.gitPanelRows[idx], idx == a.gitPanelSelected)
	}
}

// drawGitPanelRow paints one change row: the status letter in its kind
// color, the basename in the row's main color, then the parent dir
// dimmed — basename first because the sidebar is narrow and the file
// name is what the user scans for (the same reason VS Code's SCM list
// leads with it).
func (a *App) drawGitPanelRow(sx, ry, sw int, row gitChangeRow, selected bool) {
	bg := a.theme.SidebarBG
	if selected {
		bg = a.theme.LineHL
	}
	rowFill := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	for cx := sx; cx < sx+sw; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowFill)
	}
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

	// Commit checkbox: ● in / ○ out of the next commit. Directories
	// (untracked collapsed entries) reveal in the tree instead — their
	// contents commit file-by-file once expanded, so no box.
	if !row.IsDir {
		check, style := '●', letterStyle
		if !a.commitCheckOn(row.Abs) {
			check, style = '○', mutedStyle
		}
		a.screen.SetContent(sx+1, ry, check, nil, style)
	}
	drawAt(a.screen, sx+3, ry, gitKindLetter(row.Kind), letterStyle)
	x := sx + 5
	x = drawClipped(a.screen, x, ry, sx+sw-x, name, nameStyle)
	if dir != "" {
		drawClipped(a.screen, x+2, ry, sx+sw-(x+2), dir, mutedStyle)
	}
}

// commitCheckOn reports whether abs is in the commit set (absent =
// checked — see checkedChangePaths).
func (a *App) commitCheckOn(abs string) bool {
	checked, explicit := a.gitCommitChecks[abs]
	return !explicit || checked
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
	if a.diffBase != "" {
		seg += " ⇆ " + a.diffBase
	}
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
