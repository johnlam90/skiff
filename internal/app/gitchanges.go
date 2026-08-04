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
// The panel is reachable four ways: the GIT header tab, the branch
// segment in the status bar, ≡ → Git… → Git changes, and Esc-g. The
// first two are pure mouse routes and leave the keyboard with the
// editor; the last two go through focusGitPanel, which also arms
// keyboard mode — ↑↓ walk the rows, Space stages, Enter opens the
// diff, Tab/←→ reach the action row and Esc hands the keys back. That
// mode is not a nicety: Button3 and mouse reporting are exactly what
// macOS Terminal + tmux swallow, so a mouse-first panel with no
// keyboard route strands the SSH user it was built for. While it is
// armed a hint strip docks at the bottom of the panel listing the
// bindings (a strip, not an overlay — see
// docs/adr/0001-strips-are-not-overlays.md) and naming the focused
// action button, which is the only thing that makes the narrow
// [✓][↑][↓][⋯] ladder decodable. There are no Ctrl bindings.
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
	"github.com/johnlam90/skiff/internal/scrollbar"
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
	if a.gitSnap.Branch == "" {
		a.flash(a.gitUnavailableMsg())
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
	if a.gitSnap.Branch == "" {
		a.flash(a.gitUnavailableMsg())
		return
	}
	a.gitPanelActive = true
	a.rebuildGitChangesRows()
	a.refreshGitStatusAsync()
}

// showExplorerPanel switches the sidebar back to the file tree (the
// EXPLORER header tab's click action). Keyboard mode goes with it —
// the tree has its own navigation and the panel's key capture would
// otherwise linger over a view that no longer exists.
func (a *App) showExplorerPanel() {
	a.gitPanelActive = false
	a.exitGitPanelKeys()
}

// menuGitChanges is the ≡ menu entry for the Git panel. It routes
// through focusGitPanel rather than toggleGitPanel because the menu is
// the keyboard user's primary surface (CLAUDE.md: everything must be
// reachable there), so reaching the panel from it has to hand over the
// keyboard too — otherwise the ≡ route opens a view you can't move in.
func (a *App) menuGitChanges() {
	a.closeMenu()
	a.focusGitPanel()
}

// focusGitPanel is the keyboard route into the Git panel (Esc-g and
// ≡ → Git changes). The panel is mouse-first by design, but Button3
// and mouse reporting are exactly what macOS Terminal + tmux swallow,
// so the command gestures arm a keyboard focus that can walk the rows
// and the action row. A panel already up from a mouse route is grabbed
// rather than closed — "take me there" is what the gesture means when
// you aren't there yet — and pressing it again from inside toggles
// back to the explorer, the original toggle contract.
func (a *App) focusGitPanel() {
	if a.gitPanelActive && a.sidebarShown && !a.gitPanelKeys {
		a.enterGitPanelKeys()
		return
	}
	wasOpen := a.gitPanelActive && a.sidebarShown
	a.toggleGitPanel()
	if !wasOpen && a.gitPanelActive {
		a.enterGitPanelKeys()
	}
}

// enterGitPanelKeys arms keyboard mode on an open panel: focus starts
// on the change list with a selection that is guaranteed in range and
// scrolled into view, so the very first ↑/↓ lands somewhere visible.
func (a *App) enterGitPanelKeys() {
	a.gitPanelKeys = true
	a.gitPanelOnBtns = false
	a.gitPanelBtn = 0
	if a.gitPanelSelected < 0 || a.gitPanelSelected >= len(a.gitPanelRows) {
		a.gitPanelSelected = 0
	}
	a.ensureGitRowVisible(a.gitPanelSelected)
}

// exitGitPanelKeys hands the keyboard back to the editor. Called by Esc
// and by any press that lands outside the sidebar; a no-op when the
// mode was never armed, so callers never need to check first.
func (a *App) exitGitPanelKeys() {
	a.gitPanelKeys = false
	a.gitPanelOnBtns = false
}

// gitPanelKeysOn reports whether the Git panel currently owns the
// keyboard. Derived from the panel's visibility rather than trusted on
// its own, so every route that hides the panel — the EXPLORER tab, the
// sidebar toggle, the repo disappearing under a status refresh — drops
// the key capture without having to remember to.
func (a *App) gitPanelKeysOn() bool {
	return a.gitPanelKeys && a.gitPanelActive && a.sidebarShown
}

// gitPanelRowFocus reports whether keyboard focus sits on the change
// list rather than the action row — the condition for painting a row's
// focus marker.
func (a *App) gitPanelRowFocus() bool {
	return a.gitPanelKeysOn() && !a.gitPanelOnBtns
}

// handleGitPanelKey applies one key to the focused Git panel and
// reports whether it was consumed. Only arrows, Space, Enter, Tab and
// Esc are claimed; everything else (runes included) falls through to
// the editor so typing is never swallowed. There is deliberately no
// Ctrl binding — Ctrl fights tmux, which is the whole reason a
// keyboard mode had to be invented instead of reusing one.
func (a *App) handleGitPanelKey(ev *tcell.EventKey) bool {
	if !a.gitPanelKeysOn() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		// Deliberately not consumed: Esc drops the panel's capture on
		// the way through, then still arms the leader window, so
		// "mash Esc until the menu appears" keeps working from here.
		a.exitGitPanelKeys()
		return false
	case tcell.KeyUp:
		if a.gitPanelOnBtns {
			a.gitPanelOnBtns = false
			return true
		}
		a.moveGitPanelSel(-1)
		return true
	case tcell.KeyDown:
		if a.gitPanelOnBtns {
			a.gitPanelOnBtns = false
			return true
		}
		a.moveGitPanelSel(1)
		return true
	case tcell.KeyRight, tcell.KeyTab:
		a.moveGitPanelFocus(1)
		return true
	case tcell.KeyLeft, tcell.KeyBacktab:
		a.moveGitPanelFocus(-1)
		return true
	case tcell.KeyEnter:
		a.activateGitPanelFocus(false)
		return true
	case tcell.KeyRune:
		if ev.Rune() == ' ' {
			a.activateGitPanelFocus(true)
			return true
		}
	}
	return false
}

// moveGitPanelSel walks the change list by delta rows, clamped at both
// ends (no wrap — a list you can fall off the end of loses your place)
// and scrolled back into view.
func (a *App) moveGitPanelSel(delta int) {
	if len(a.gitPanelRows) == 0 {
		return
	}
	idx := a.gitPanelSelected + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(a.gitPanelRows) {
		idx = len(a.gitPanelRows) - 1
	}
	a.gitPanelSelected = idx
	a.ensureGitRowVisible(idx)
}

// moveGitPanelFocus moves keyboard focus between the change list and
// the action row: Tab / → step forward, Shift-Tab / ← step back, and
// stepping off either end of the button row lands back on the list, so
// the cycle closes without a wrap that would skip past the rows. While
// a mutation is running the row renders "git is working…" instead of
// buttons, so there is nothing to focus.
func (a *App) moveGitPanelFocus(delta int) {
	if a.gitOpBusy {
		return
	}
	_, _, sw, _ := a.sidebarRect()
	n := len(a.gitPanelButtons(sw))
	if n == 0 {
		return
	}
	if !a.gitPanelOnBtns {
		a.gitPanelOnBtns = true
		if delta < 0 {
			a.gitPanelBtn = n - 1
		} else {
			a.gitPanelBtn = 0
		}
		return
	}
	idx := a.gitPanelBtn + delta
	if idx < 0 || idx >= n {
		a.gitPanelOnBtns = false
		return
	}
	a.gitPanelBtn = idx
}

// activateGitPanelFocus runs whatever keyboard focus points at. On the
// list, Enter opens the row's diff and Space toggles its commit
// checkbox — Space is the checkbox key everywhere else, and Enter has
// to stay "show me the change". On the action row both keys run the
// button, which is what every button anywhere does.
func (a *App) activateGitPanelFocus(space bool) {
	if a.gitPanelOnBtns {
		if a.gitOpBusy {
			a.flash("Another git operation is still running")
			return
		}
		sx, sy, sw, _ := a.sidebarRect()
		btns := a.gitPanelButtons(sw)
		if a.gitPanelBtn < 0 || a.gitPanelBtn >= len(btns) {
			return
		}
		b := btns[a.gitPanelBtn]
		// Same anchor the click path passes, so the ⋯ popup opens
		// under the button whether it was clicked or keyed.
		b.action(a, sx+b.x0, sy+3)
		return
	}
	if a.gitPanelSelected < 0 || a.gitPanelSelected >= len(a.gitPanelRows) {
		return
	}
	row := a.gitPanelRows[a.gitPanelSelected]
	if space {
		// Directories are collapsed untracked entries — they commit
		// file by file once expanded, so they carry no checkbox.
		if !row.IsDir {
			a.toggleCommitCheck(row.Abs)
		}
		return
	}
	a.activateGitChangeRow(row)
	if !row.IsDir {
		// Recorded after activation — openDiffView's closeAllModals
		// resets the index, and this write is what arms ↑↓ walking.
		a.diffPanelRow = a.gitPanelSelected
	}
}

// hasGitRepo is the menu enabled-predicate for git-scoped rows: we need
// a project tree and a detected branch (refreshGitStatus clears the
// branch whenever the root stops being a repo).
func (a *App) hasGitRepo() bool {
	return a.tree != nil && a.gitSnap.IsRepo
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
		_, _, sw, _ := a.sidebarRect()
		for i, b := range a.gitPanelButtons(sw) {
			if x >= b.x0 && x < b.x1 {
				sx, sy, _, _ := a.sidebarRect()
				// A mouse user in keyboard mode gets the focus ring
				// moved to what they clicked, so the two input paths
				// never disagree about where "here" is.
				if a.gitPanelKeysOn() && !a.gitOpBusy {
					a.gitPanelOnBtns, a.gitPanelBtn = true, i
				}
				b.action(a, sx+b.x0, sy+3)
				return
			}
		}
		return
	}
	listH, _ := a.gitPanelBody()
	// The bar owns the panel's rightmost column, so it has to be
	// claimed before the row hit-test everything below falls through
	// to — otherwise a press on the thumb opens the diff of whatever
	// row happens to sit behind it. Branch line (y 1), button row
	// (y 2) and the hint strip below the list are outside its span.
	if a.gitPanelBarHit(x, y) {
		a.gitPanelScrollToBar(y)
		return
	}
	idx := a.gitPanelScroll + y - gitPanelListTop
	if y < gitPanelListTop || y-gitPanelListTop >= listH || idx < 0 || idx >= len(a.gitPanelRows) {
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
	a.gitPanelOnBtns = false
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

// gitPanelBarMinWidth is the narrowest panel that still spends a column
// on the scroll indicator. The sidebar's own minimum (18) leaves the
// panel 17 columns, so the bar is present at every width a user can
// drag to; the floor only keeps a pathologically narrow rect (a tiny
// terminal, a test fixture) spending its cells on file names instead
// of on a bar with nothing left to point at. Mirrors the file tree's
// minScrollbarWidth, for the same reason.
const gitPanelBarMinWidth = 6

// gitPanelBar returns the sidebar-local column the change list's scroll
// indicator owns in a sw-wide panel with a listH-row list, and whether
// it is drawn at all. The column is the panel rect's rightmost — one to
// the LEFT of the resize splitter, exactly where the file tree puts its
// bar, so splitter, bar and rows stay three distinct column ranges at
// any y. ok is false when the list fits (a full-height thumb says
// nothing) or the panel cannot spare the cell.
func (a *App) gitPanelBar(sw, listH int) (x int, ok bool) {
	if sw < gitPanelBarMinWidth {
		return 0, false
	}
	if _, _, fits := scrollbar.Geom(len(a.gitPanelRows), listH, a.gitPanelScroll); !fits {
		return 0, false
	}
	return sw - 1, true
}

// gitPanelBarHit reports whether a sidebar-local press at (x, y) landed
// on the change list's scroll indicator. The span stops at the list's
// last row, so the keyboard hint strip docked under it keeps its own
// cells.
func (a *App) gitPanelBarHit(x, y int) bool {
	_, _, sw, _ := a.sidebarRect()
	listH, _ := a.gitPanelBody()
	barX, ok := a.gitPanelBar(sw, listH)
	return ok && x == barX && y >= gitPanelListTop && y < gitPanelListTop+listH
}

// gitPanelScrollToBar scrolls the change list so its thumb centers on
// sidebar-local row y — the click-to-jump gesture the tree's bar
// answers, sharing the same inverse math so the two cannot drift.
func (a *App) gitPanelScrollToBar(y int) {
	listH, _ := a.gitPanelBody()
	a.gitPanelScroll = scrollbar.TargetForThumb(len(a.gitPanelRows), listH, y-gitPanelListTop)
}

// drawGitPanelBar paints the change list's one-column bar at screen
// column x: the same shaded track and solid thumb the file tree and the
// editor draw, on the sidebar's own background so the column reads as
// part of the panel rather than as a hole in it.
func (a *App) drawGitPanelBar(x, top, listH int) {
	thumbStart, thumbLen, ok := scrollbar.Geom(len(a.gitPanelRows), listH, a.gitPanelScroll)
	if !ok {
		return
	}
	bg := a.theme.SidebarBG
	trackStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	thumbStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	for row := range listH {
		r, st := scrollbar.Track, trackStyle
		if row >= thumbStart && row < thumbStart+thumbLen {
			r, st = scrollbar.Thumb, thumbStyle
		}
		a.screen.SetContent(x, top+row, r, nil, st)
	}
}

// gitPanelBtn is one clickable action on the panel's button row, with
// its x-range in sidebar-local cells. verb is the button's full name,
// which the ladder's compact tiers ([✓][↑][↓][⋯]) drop entirely — the
// keyboard hint strip renders it so a focused glyph is still decodable
// on a minimum-width sidebar.
type gitPanelBtn struct {
	label  string
	verb   string
	x0, x1 int
	action func(a *App, anchorX, anchorY int)
}

// gitPanelButtons lays out the button row for a sidebar sw cells wide.
// Computed on the fly so the click handler and the renderer can never
// disagree about geometry. The labels are self-explaining — commit
// carries the checked-file count, push/pull carry the ahead/behind
// counts — and the row adapts down a ladder as the sidebar narrows:
// full labels with counts, glyphs with counts, then bare glyphs, so no
// button is ever painted past the splitter or silently dropped.
func (a *App) gitPanelButtons(sw int) []gitPanelBtn {
	checked := 0
	for _, r := range a.gitPanelRows {
		if !r.IsDir && a.commitCheckOn(r.Abs) {
			checked++
		}
	}
	wide := []string{"[ Commit ]", "[ Push ]", "[ Pull ]", "[ ⋯ ]"}
	if checked > 0 {
		wide[0] = fmt.Sprintf("[ Commit %d ]", checked)
	}
	if a.gitSnap.Ahead > 0 {
		wide[1] = fmt.Sprintf("[ Push ↑%d ]", a.gitSnap.Ahead)
	}
	if a.gitSnap.Behind > 0 {
		wide[2] = fmt.Sprintf("[ Pull ↓%d ]", a.gitSnap.Behind)
	}
	// Words beat counts: at middle widths the ladder drops the counts
	// before it drops the verbs, because "[Commit]" teaches a new user
	// what the button does and "[✓2]" doesn't.
	medium := []string{"[Commit]", "[Push]", "[Pull]", "[⋯]"}
	compact := []string{"[✓]", "[↑]", "[↓]", "[⋯]"}
	if checked > 0 {
		compact[0] = fmt.Sprintf("[✓%d]", checked)
	}
	if a.gitSnap.Ahead > 0 {
		compact[1] = fmt.Sprintf("[↑%d]", a.gitSnap.Ahead)
	}
	if a.gitSnap.Behind > 0 {
		compact[2] = fmt.Sprintf("[↓%d]", a.gitSnap.Behind)
	}
	bare := []string{"[✓]", "[↑]", "[↓]", "[⋯]"}

	labels, gap := wide, 1
	if 1+rowWidth(labels, gap) > sw {
		labels, gap = medium, 1
	}
	if 1+rowWidth(labels, gap) > sw {
		labels, gap = compact, 0
	}
	if 1+rowWidth(labels, gap) > sw {
		labels, gap = bare, 0
	}

	actions := []func(a *App, x, y int){
		func(app *App, _, _ int) { app.menuGitCommit() },
		func(app *App, _, _ int) { app.menuGitPush() },
		func(app *App, _, _ int) { app.menuGitPull() },
		func(app *App, x, y int) { app.openGitExtras(x, y) },
	}
	verbs := []string{"Commit", "Push", "Pull", "More actions"}
	out := make([]gitPanelBtn, 0, len(labels))
	x := 1
	for i, l := range labels {
		w := runeLen(l)
		out = append(out, gitPanelBtn{label: l, verb: verbs[i], x0: x, x1: x + w, action: actions[i]})
		x += w + gap
	}
	return out
}

// rowWidth sums a button row's cell width for the fit ladder.
func rowWidth(labels []string, gap int) int {
	total := 0
	for i, l := range labels {
		if i > 0 {
			total += gap
		}
		total += runeLen(l)
	}
	return total
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
	listH, _ := a.gitPanelBody()
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
	listH, _ := a.gitPanelBody()
	max := len(a.gitPanelRows) - listH
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

// gitPanelHintMaxRows caps the keyboard hint strip. Four rows is what
// the full binding list needs at the minimum sidebar width — the cap
// is generous on purpose, because a strip that silently drops its tail
// drops "esc exit", which is the one binding a stuck user needs. Short
// terminals are handled by gitPanelBody yielding rows back to the list
// instead.
const gitPanelHintMaxRows = 4

// gitPanelBody splits the panel's vertical space between the change
// list and the keyboard hint strip. One function so the renderer, the
// scroll clamp and the click hit-test can never disagree about where
// the list ends — and the hint gives its rows back one at a time
// rather than starve the list it exists to explain.
func (a *App) gitPanelBody() (listH int, hint []string) {
	_, _, sw, sh := a.sidebarRect()
	avail := sh - gitPanelListTop
	if avail < 0 {
		avail = 0
	}
	hint = a.gitPanelHint(sw)
	for len(hint) > 0 && avail-len(hint) < 1 {
		hint = hint[:len(hint)-1]
	}
	return avail - len(hint), hint
}

// gitPanelHint wraps the keyboard cheat text for a sw-wide sidebar, or
// returns nil when the panel doesn't own the keyboard. It is a strip,
// not an overlay (see docs/adr/0001-strips-are-not-overlays.md): it
// docks, reflows the list above it, and captures nothing.
func (a *App) gitPanelHint(sw int) []string {
	if !a.gitPanelKeysOn() {
		return nil
	}
	return wrapHintSegments(a.gitPanelHintSegs(), sw-1, gitPanelHintMaxRows)
}

// gitPanelHintSegs is the hint's key/meaning pairs for the current
// focus. On the action row the first segment names the focused
// button — that is what makes the compact [✓][↑][↓][⋯] ladder
// decodable at minimum sidebar width, where the verbs are gone.
func (a *App) gitPanelHintSegs() []string {
	if a.gitPanelOnBtns {
		return []string{"⏎ " + a.gitPanelBtnVerb(), "←→ button", "⇥ list", "esc exit"}
	}
	return []string{"↑↓ move", "␣ stage", "⏎ diff", "⇥ buttons", "esc exit"}
}

// gitPanelBtnVerb names the focused action-row button, or "" when the
// focus index has fallen outside a re-laid-out row.
func (a *App) gitPanelBtnVerb() string {
	_, _, sw, _ := a.sidebarRect()
	btns := a.gitPanelButtons(sw)
	if a.gitPanelBtn < 0 || a.gitPanelBtn >= len(btns) {
		return ""
	}
	return btns[a.gitPanelBtn].verb
}

// wrapHintSegments greedily packs segments into rows at most w cells
// wide, never splitting a segment (half a binding is worse than none)
// and stopping at maxRows. A segment wider than the whole row still
// gets its own row and is clipped at paint time.
func wrapHintSegments(segs []string, w, maxRows int) []string {
	if w <= 0 || maxRows <= 0 {
		return nil
	}
	var rows []string
	cur := ""
	for _, s := range segs {
		cand := s
		if cur != "" {
			cand = cur + "  " + s
		}
		if cur == "" || runeLen(cand) <= w {
			cur = cand
			continue
		}
		rows = append(rows, cur)
		if len(rows) == maxRows {
			return rows
		}
		cur = s
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return rows
}

// drawGitPanelHint paints the keyboard strip docked at the bottom of
// the panel. Muted on the highlight background: it is a standing
// reminder, not the leader strip's half-second flash, so it stays
// quieter than everything it sits under.
func (a *App) drawGitPanelHint(sx, sy, sw, sh int, rows []string) {
	st := tcell.StyleDefault.Background(a.theme.LineHL).Foreground(a.theme.Muted)
	top := sy + sh - len(rows)
	for i, r := range rows {
		for cx := sx; cx < sx+sw; cx++ {
			a.screen.SetContent(cx, top+i, ' ', nil, st)
		}
		drawClipped(a.screen, sx+1, top+i, sw-1, r, st)
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
//
// A change list taller than the visible rows reserves the panel's
// rightmost column for a scrollbar and draws the rows one cell
// narrower, so a 40-file repo says "there is more below" instead of
// just stopping. The bar spans only the list: the branch line, the
// button row and the keyboard hint strip are not scrollable, and a bar
// drawn over them would claim they are.
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
	// The trailing chevron marks the row as the branch picker's opener —
	// without it the control renders exactly like a static label.
	branchLine := "⎇ " + a.gitSnap.Branch + " ▾"
	if a.diffBase != "" {
		branchLine += " ⇆ " + a.diffBase
	}
	if a.gitSnap.Ahead > 0 {
		branchLine += fmt.Sprintf(" ↑%d", a.gitSnap.Ahead)
	}
	if a.gitSnap.Behind > 0 {
		branchLine += fmt.Sprintf(" ↓%d", a.gitSnap.Behind)
	}
	drawClipped(a.screen, sx+1, sy+1, sw-1, branchLine, branchStyle)

	// Buttons row — commit / push / pull / everything else. While a
	// mutation runs, say so instead of pretending clicks would work.
	if a.gitOpBusy {
		muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
		drawClipped(a.screen, sx+1, sy+2, sw-1, "git is working…", muted)
	} else {
		onBtns := a.gitPanelKeysOn() && a.gitPanelOnBtns
		for i, b := range a.gitPanelButtons(sw) {
			if b.x1 > sw {
				break // belt: never paint past the splitter
			}
			drawButton(a.screen, sx+b.x0, sy+2, b.label, bg, a.theme.Accent, onBtns && i == a.gitPanelBtn)
		}
	}

	listH, hint := a.gitPanelBody()
	a.scrollGitPanel(0)

	// Reserve the bar's column before a single row is drawn, so the
	// labels, the status letter and the stage checkbox all stop where
	// the bar starts instead of being painted underneath it — the same
	// ordering Tree.Render uses for the sidebar's other bar.
	rowW := sw
	barX, bar := a.gitPanelBar(sw, listH)
	if bar {
		rowW--
	}

	if len(a.gitPanelRows) == 0 {
		muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
		drawClipped(a.screen, sx+1, sy+gitPanelListTop, rowW-1, a.gitPanelEmptyLabel(), muted)
	} else {
		rowFocus := a.gitPanelRowFocus()
		for i := range listH {
			idx := a.gitPanelScroll + i
			if idx >= len(a.gitPanelRows) {
				break
			}
			sel := idx == a.gitPanelSelected
			a.drawGitPanelRow(sx, sy+gitPanelListTop+i, rowW, a.gitPanelRows[idx], sel, sel && rowFocus)
		}
	}
	if bar {
		a.drawGitPanelBar(sx+barX, sy+gitPanelListTop, listH)
	}
	if len(hint) > 0 {
		a.drawGitPanelHint(sx, sy, sw, sh, hint)
	}
}

// drawGitPanelRow paints one change row: the status letter in its kind
// color, the basename in the row's main color, then the parent dir
// dimmed — basename first because the sidebar is narrow and the file
// name is what the user scans for (the same reason VS Code's SCM list
// leads with it).
//
// selected is the panel's current row (highlight background, set by a
// click or the diff walk); keyFocused additionally means the keyboard
// is on it, and borrows the file tree's active-row language — Accent
// bold — plus a leading caret. The caret is the part that matters on a
// monochrome or low-color terminal, where the theme's focus hue
// degrades to nothing and a color-only cue would vanish.
func (a *App) drawGitPanelRow(sx, ry, sw int, row gitChangeRow, selected, keyFocused bool) {
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
	if keyFocused || (a.tree != nil && row.Abs == a.tree.ActiveFile) {
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
	if keyFocused {
		a.screen.SetContent(sx, ry, '›', nil,
			tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true))
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
	if a.gitSnap.Branch == "" {
		return ""
	}
	seg := " " + a.gitSnap.Branch
	if a.diffBase != "" {
		seg += " ⇆ " + a.diffBase
	}
	if a.gitSnap.Ahead > 0 {
		seg += " ↑" + itoa(a.gitSnap.Ahead)
	}
	if a.gitSnap.Behind > 0 {
		seg += " ↓" + itoa(a.gitSnap.Behind)
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
