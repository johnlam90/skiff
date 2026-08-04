// =============================================================================
// File: internal/app/menudef.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// menudef.go is the action menu's data model: the menuItemDef row shape,
// the built-in group table, the two drill-in tables it demotes rows
// into, the type-to-filter matcher, and the layout pass that stamps a
// relY onto every row. Adding a menu row is adding a struct literal
// here — the behavior in menu.go and the drawing in drawMenu both read
// the layout rather than hard-coding offsets.
//
// The top level is seven groups — File, Edit, Go, Git, View, Custom,
// Quit — and it earns that shape two ways. Rows that cannot apply in the
// current session (git verbs with no repo, edit verbs with no tab) are
// dropped by their `visible` predicate instead of rendered greyed-out,
// because a dimmed row a user can never light up is pure scroll cost.
// And two clusters that used to spend nine and twelve rows — the git
// verbs and the file-clipboard actions — collapse into one "Git…" /
// "File clipboard…" row each that opens an overlay.Pick of the demoted
// actions. CLAUDE.md's rule is that every action stays reachable from
// the ≡ menu; a drill-in honours that, silent removal would not, and
// menuDrillIns is walked by a test that pins exactly which actions the
// top level is allowed to demote.
//
// Custom actions from ~/.config/skiff/actions.json are spliced in at
// layout time rather than baked into the table, so toggling them on or
// off never touches the built-ins.

package app

import "strings"

// Modal chrome rows, as offsets from the modal's top border. The title
// and the filter field are fixed chrome; only rows at menuContentY and
// below scroll. Every hit-test, scroll clamp and draw pass in menu.go
// reads these instead of repeating the magic numbers.
const (
	menuTitleY   = 1 // " Menu" + the esc hint
	menuFilterY  = 2 // type-to-filter input
	menuDividerY = 3 // fixed divider under the filter
	menuContentY = 4 // first scrollable content row
)

// menuItemDef describes one row in the action modal: the label shown to
// the user, the y-offset it lives at inside the modal, the action it runs
// when clicked, and a predicate that returns true when the action is
// applicable in the current context (so we can dim non-applicable rows).
//
// labelFor is an optional dynamic-label hook: when non-nil, drawMenu calls
// it instead of using the static label string. Used by toggle-style rows
// whose label depends on app state ("Show / Hide file explorer").
type menuItemDef struct {
	label    string
	relY     int
	shortcut string
	action   func(*App)
	enabled  func(*App) bool
	labelFor func(*App) string
	// visible, when non-nil, decides whether the item appears in the
	// menu at all (returning false drops the row entirely — not the
	// same as enabled, which renders the row greyed out).
	//
	// The split is deliberate and worth keeping straight when adding a
	// row: `visible` answers "could this action exist at all in the
	// session as it is shaped right now" (no tree, no tab, no repo,
	// nothing on the file clipboard) and `enabled` answers "the action
	// exists but has nothing to chew on this instant" (Save on a clean
	// buffer, Undo with an empty history). The first is noise and gets
	// dropped; the second is a dimmed row that teaches.
	visible func(*App) bool
}

// menuDrillIn is one demoted cluster: the title of the pick it opens
// and the rows that live inside it. Kept as a value (not a closure) so
// tests can walk the rows without opening an overlay.
type menuDrillIn struct {
	title string
	items []menuItemDef
}

// builtinMenuGroups returns the editor's built-in action groups in
// display order: File, Edit, Go, Git, View, Quit. Custom actions loaded
// from ~/.config/skiff/actions.json get spliced in as their own group
// before Quit in menuLayout — they're not included here so toggling
// them on or off doesn't require touching this table.
//
// Each group is rendered as a contiguous block; menuLayout interleaves
// dividers between groups and recomputes every relY. The relY field is
// left zero here on purpose — it gets stamped at layout time.
func builtinMenuGroups() [][]menuItemDef {
	return [][]menuItemDef{
		fileMenuGroup(),
		editMenuGroup(),
		goMenuGroup(),
		gitMenuGroup(),
		viewMenuGroup(),
		quitMenuGroup(),
	}
}

// fileMenuGroup is everything that acts on the file (or folder) behind
// the active tab. The tab-scoped rows carry visible: hasTab so the
// group shrinks to a single "New file" on an empty editor, and the
// occasional rows — reopen, undo delete, the folder pair — appear only
// once they have something to act on. Cut / copy / duplicate / paste /
// copy-path live one level down under "File clipboard…".
func fileMenuGroup() []menuItemDef {
	return []menuItemDef{
		{shortcut: "Esc n", action: (*App).menuNewFile, enabled: alwaysTrue, labelFor: (*App).newFileLabel},
		{label: "Save", shortcut: "Esc s", action: (*App).menuSave, enabled: (*App).hasSavableTab, visible: (*App).hasTab},
		{label: "Save & close tab", action: (*App).menuSaveAndClose, enabled: (*App).hasSavableTab, visible: (*App).hasTab},
		{label: "Close tab", shortcut: "Esc w", action: (*App).menuClose, enabled: (*App).hasTab, visible: (*App).hasTab},
		{label: "Reopen closed tab", shortcut: "Esc o", action: (*App).menuReopenTab, enabled: (*App).hasClosedTab, visible: (*App).hasClosedTab},
		{label: "Revert file", action: (*App).menuRevert, enabled: (*App).hasRevert, visible: (*App).hasTab},
		// Only route back to the resolution prompt once the status bar's
		// "⚠ disk conflict" marker has been dismissed — hidden the rest
		// of the time, which is nearly always.
		{label: "Resolve disk conflict…", action: (*App).menuResolveDiskConflict, enabled: (*App).hasDiskConflict, visible: (*App).hasDiskConflict},
		{label: "Rename file", action: (*App).menuRename, enabled: (*App).hasFileTab, visible: (*App).hasTab},
		{label: "Delete file", action: (*App).menuDelete, enabled: (*App).hasFileTab, visible: (*App).hasTab},
		{action: (*App).menuRenameFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).renameFolderLabel, visible: (*App).hasActiveSubfolder},
		{action: (*App).menuDeleteFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).deleteFolderLabel, visible: (*App).hasActiveSubfolder},
		{action: (*App).menuUndoDelete, enabled: (*App).hasTrashedEntry, labelFor: (*App).undoDeleteLabel, visible: (*App).hasTrashedEntry},
		{label: "File clipboard…", action: (*App).menuFileClipboard, enabled: (*App).hasFileClipActions, visible: (*App).hasFileClipActions},
	}
}

// editMenuGroup is the buffer-mutating half of the old History /
// Clipboard / Line-ops trio, merged because "Edit" is the one word a
// user scans for when they want undo, paste or a line gesture. Every
// row needs an editable (non-image) tab to mean anything, so the whole
// group disappears on an empty editor or an image preview.
func editMenuGroup() []menuItemDef {
	return []menuItemDef{
		{label: "Undo", shortcut: "Esc u", action: (*App).menuUndo, enabled: (*App).hasUndo, visible: (*App).hasEditableTab},
		{label: "Redo", shortcut: "Esc r", action: (*App).menuRedo, enabled: (*App).hasRedo, visible: (*App).hasEditableTab},
		{label: "Copy selection", shortcut: "Esc c", action: (*App).menuCopy, enabled: (*App).hasSelection, visible: (*App).hasEditableTab},
		{label: "Cut selection", shortcut: "Esc x", action: (*App).menuCut, enabled: (*App).hasSelection, visible: (*App).hasEditableTab},
		{label: "Paste", shortcut: "Esc v", action: (*App).menuPaste, enabled: (*App).hasClipboard, visible: (*App).hasEditableTab},
		{label: "Toggle line comment", shortcut: "Esc /", action: (*App).menuToggleLineComment, enabled: (*App).hasCommentableTab, visible: (*App).hasEditableTab},
		{label: "Move line up", shortcut: "Esc k", action: (*App).menuMoveLineUp, enabled: (*App).hasEditableTab, visible: (*App).hasEditableTab},
		{label: "Move line down", shortcut: "Esc j", action: (*App).menuMoveLineDown, enabled: (*App).hasEditableTab, visible: (*App).hasEditableTab},
		{label: "Duplicate line", shortcut: "Esc d", action: (*App).menuDuplicateLine, enabled: (*App).hasEditableTab, visible: (*App).hasEditableTab},
	}
}

// goMenuGroup is "take me somewhere": the in-file find, the two project
// searches, and the caret jumps. Tab-scoped rows hide with no tab; the
// project rows hide in single-file mode, where there is no tree to
// search. Go to matching bracket keeps a live enabled predicate on
// purpose — it dims when the caret isn't on a bracket, which is how the
// row teaches what it needs.
func goMenuGroup() []menuItemDef {
	return []menuItemDef{
		{label: "Find in file", shortcut: "Esc f", action: (*App).menuFind, enabled: (*App).hasFindable, visible: (*App).hasTab},
		{label: "Find in project", shortcut: "Esc F", action: (*App).menuFindInProject, enabled: (*App).hasFinder, visible: (*App).hasTree},
		{label: "Go to line", shortcut: "Esc l", action: (*App).menuGoToLine, enabled: (*App).hasFindable, visible: (*App).hasTab},
		{label: "Go to matching bracket", shortcut: "Esc %", action: (*App).menuGoToMatchingBracket, enabled: (*App).hasMatchingBracket, visible: (*App).hasEditableTab},
		{label: "Move to previous word", shortcut: "Esc b", action: (*App).menuMoveWordLeft, enabled: (*App).hasEditableTab, visible: (*App).hasEditableTab},
		{label: "Move to next word", shortcut: "Esc e", action: (*App).menuMoveWordRight, enabled: (*App).hasEditableTab, visible: (*App).hasEditableTab},
		{label: "Find file in project", shortcut: "Esc p", action: (*App).menuFindFile, enabled: (*App).hasFinder, visible: (*App).hasTree},
	}
}

// gitMenuGroup is the whole git surface as one row. Nine top-level git
// verbs was the single biggest reason the menu overflowed an 80×24
// split, and eight of the nine already have a home on the Git panel, so
// the menu keeps one door ("Git…") and puts the verbs behind it.
func gitMenuGroup() []menuItemDef {
	return []menuItemDef{
		// No shortcut hint on purpose: Esc g opens the Git panel, not
		// this pick, and it is advertised on the "Git changes" row
		// inside the drill-in where it is actually true.
		{label: "Git…", action: (*App).menuGitMenu, enabled: (*App).hasGitActions, visible: (*App).hasGitActions},
	}
}

// viewMenuGroup is the chrome toggles, the manual tree refresh, the
// theme picker and the shortcut reference — always applicable, except
// the two tree-dependent rows in single-file mode where there is no
// tree to show, hide or rescan.
func viewMenuGroup() []menuItemDef {
	return []menuItemDef{
		{shortcut: "Esc t", action: (*App).menuToggleSidebar, enabled: alwaysTrue, labelFor: (*App).sidebarToggleLabel, visible: (*App).hasTree},
		{shortcut: "Esc z", action: (*App).menuToggleWrap, enabled: alwaysTrue, labelFor: (*App).wrapToggleLabel},
		// The 10s auto-refresh covers local edits; the manual row is for
		// the cases the ticker can't win — an NFS or sshfs mount where
		// the walk is slow enough that "I know it changed, look again"
		// beats waiting.
		{label: "Refresh file tree", action: (*App).menuRefreshTree, enabled: alwaysTrue, visible: (*App).hasTree},
		{label: "Theme…", action: (*App).menuTheme, enabled: alwaysTrue},
		// Last row of the group on purpose: it is the one row that
		// teaches the other rows. Sourced from leaderBindings(), so it
		// can never advertise a gesture the dispatch dropped.
		{label: "Keyboard shortcuts…", shortcut: "Esc ?", action: (*App).menuKeyboardShortcuts, enabled: alwaysTrue},
	}
}

// quitMenuGroup is the last group on purpose: menuLayout splices custom
// actions in directly above it, and several tests lean on Quit being
// the final row.
func quitMenuGroup() []menuItemDef {
	return []menuItemDef{
		{label: "Quit editor", shortcut: "Esc q", action: (*App).menuQuit, enabled: alwaysTrue},
	}
}

// gitDrillIn is the pick behind the "Git…" row. Order follows how often
// the verb gets used rather than the old table order, and every entry
// keeps the exact label, action and predicate it had as a top-level
// row so muscle memory and the Esc-g hint survive the demotion.
func gitDrillIn() menuDrillIn {
	return menuDrillIn{title: "Git", items: []menuItemDef{
		{label: "Git changes", shortcut: "Esc g", action: (*App).menuGitChanges, enabled: (*App).hasGitRepo},
		{label: "Commit changes…", action: (*App).menuGitCommit, enabled: (*App).hasGitChanges},
		{label: "Push", action: (*App).menuGitPush, enabled: (*App).hasGitRepo},
		{label: "Pull", action: (*App).menuGitPull, enabled: (*App).hasGitRepo},
		{label: "Switch branch…", action: (*App).menuGitSwitchBranch, enabled: (*App).hasGitRepo},
		{label: "Diff this file", action: (*App).menuDiffFile, enabled: (*App).hasDiffableTab},
		{label: "History of this file", action: (*App).menuFileHistory, enabled: (*App).hasFileHistoryTab},
		{label: "Commit history", action: (*App).menuCommitHistory, enabled: (*App).hasGitRepo},
		{label: "More git actions…", action: (*App).menuGitExtras, enabled: (*App).hasGitRepo},
	}}
}

// fileClipDrillIn is the pick behind the "File clipboard…" row: the
// cut / copy / duplicate / paste quartet plus the two copy-path rows,
// which are the same gesture ("put something about this file on a
// clipboard") and were the other half of the twelve-row File group.
func fileClipDrillIn() menuDrillIn {
	return menuDrillIn{title: "File clipboard", items: []menuItemDef{
		{label: "Cut file", action: (*App).menuCutFile, enabled: (*App).hasFileTab},
		{label: "Copy file", action: (*App).menuCopyFile, enabled: (*App).hasFileTab},
		{label: "Duplicate file", action: (*App).menuDuplicateFile, enabled: (*App).hasFileTab},
		{action: (*App).menuPasteEntry, enabled: (*App).hasFileClip, labelFor: (*App).pasteEntryLabel},
		{label: "Copy relative path", action: (*App).menuCopyRelativePath, enabled: (*App).hasFileTab},
		{label: "Copy absolute path", action: (*App).menuCopyAbsolutePath, enabled: (*App).hasFileTab},
	}}
}

// menuDrillIns lists every drill-in the top level demotes actions into.
// It exists so the reachability test can walk "top level plus every
// drill-in" generically: a future drill-in is covered the moment it is
// registered here, and an action can never quietly leave the ≡ menu.
func menuDrillIns() []menuDrillIn {
	return []menuDrillIn{gitDrillIn(), fileClipDrillIn()}
}

// alwaysTrue is the default predicate for actions with no preconditions
// at all — Quit, the view toggles, and New file.
func alwaysTrue(*App) bool { return true }

// hasTree is the menu visibility predicate for tree-dependent rows.
// True when the file tree was built at startup; false in single-file
// mode, where we deliberately skipped tree construction to avoid
// indexing the working directory.
func (a *App) hasTree() bool {
	return a.tree != nil
}

// hasGitActions gates the collapsed "Git…" row. A repo makes every verb
// available, but Diff this file and History of this file deliberately
// work in single-file mode too (they ask git about the tab's own path,
// with no tree to consult), so the row survives there with a two-entry
// drill-in rather than stranding those actions off-menu.
func (a *App) hasGitActions() bool {
	return a.hasGitRepo() || a.hasDiffableTab() || a.hasFileHistoryTab()
}

// hasFileClipActions gates the "File clipboard…" row: a file-backed tab
// gives it something to cut, copy, duplicate or name, and a loaded file
// clipboard gives it somewhere to paste even when the active tab is an
// unsaved buffer.
func (a *App) hasFileClipActions() bool {
	return a.hasFileTab() || a.hasFileClip()
}

// menuLabel resolves a row's display label, preferring the dynamic
// labelFor hook. Every surface that needs the user-visible string —
// drawMenu, the drill-in picks, the filter matcher — goes through here
// so none of them can disagree about what a row is called.
func (a *App) menuLabel(it menuItemDef) string {
	if it.labelFor != nil {
		return it.labelFor(a)
	}
	return it.label
}

// menuQuery returns the normalised type-to-filter query: lower-cased
// and trimmed, empty when the user hasn't typed anything.
func (a *App) menuQuery() string {
	return strings.ToLower(strings.TrimSpace(a.menuFilter.Text()))
}

// menuGroups returns the groups the menu shows with no filter typed:
// the built-ins with custom actions spliced in before Quit, minus every
// row whose visibility predicate says it can't apply right now.
func (a *App) menuGroups() [][]menuItemDef {
	groups := append([][]menuItemDef{}, builtinMenuGroups()...)
	if len(a.customActions) > 0 {
		ca := make([]menuItemDef, 0, len(a.customActions))
		for i := range a.customActions {
			i := i // capture
			// Custom actions are user-defined shell — we don't try to
			// guess from the command string whether it needs $FILE.
			// "Upgrade Skiff" obviously doesn't; "Open on
			// computer" obviously does. Both should be runnable from
			// the menu; if a $FILE-dependent command is invoked with
			// no tab open it'll fail with a real error and our info
			// modal surfaces it. Better that than getting the
			// heuristic wrong half the time.
			ca = append(ca, menuItemDef{
				label:   a.customActions[i].Label,
				action:  func(app *App) { app.runCustomAction(i) },
				enabled: alwaysTrue,
			})
		}
		// Splice in just before the final group (Quit). builtinMenuGroups
		// guarantees Quit is last; if anyone reorders that, the test
		// pinning custom-actions placement catches it.
		quit := groups[len(groups)-1]
		groups = append(groups[:len(groups)-1], ca, quit)
	}

	// Drop items whose visibility predicate (if any) says they don't
	// belong here right now — e.g. single-file mode hides the sidebar
	// toggle because there's no tree to toggle. A group emptied by
	// filtering vanishes too, so we don't leave a hanging divider
	// between two surviving groups.
	visible := make([][]menuItemDef, 0, len(groups))
	for _, g := range groups {
		kept := make([]menuItemDef, 0, len(g))
		for _, it := range g {
			if it.visible != nil && !it.visible(a) {
				continue
			}
			kept = append(kept, it)
		}
		if len(kept) > 0 {
			visible = append(visible, kept)
		}
	}
	return visible
}

// menuLayout flattens the menu into a single ordered slice of items with
// relY positions assigned, plus the divider rows and the modal's total
// cell height. With the filter empty that's the grouped layout; with a
// query typed the groups collapse into one flat match list, because a
// search result set has no meaningful group structure and dividers
// between one-row groups would be all frame and no content. Recomputed
// on every call — cheap, and it lets the layout react to a keystroke in
// the filter, a reloaded actions.json, or a tab opening underneath.
func (a *App) menuLayout() (items []menuItemDef, dividers []int, modalHeight int) {
	groups := a.menuGroups()
	if q := a.menuQuery(); q != "" {
		groups = a.matchMenuGroups(groups, q)
	}
	return layoutMenuGroups(groups)
}

// menuNaturalHeight is the height the current row set wants with the
// filter ignored. menuModalRect anchors the modal's origin on it so the
// frame's top edge — and with it the title and the filter caret — stays
// nailed in place while the user types; only the bottom edge climbs as
// rows drop out.
func (a *App) menuNaturalHeight() int {
	_, _, h := layoutMenuGroups(a.menuGroups())
	return h
}

// matchMenuGroups narrows every group down to the rows matching q and
// returns them as a single flat group (nil when nothing matched).
// Matching runs across all groups at once — that's the point of the
// filter: "branch" should find Switch branch without the user knowing
// it lives behind Git.
func (a *App) matchMenuGroups(groups [][]menuItemDef, q string) [][]menuItemDef {
	var hits []menuItemDef
	for _, g := range groups {
		for _, it := range g {
			if menuMatchRank(strings.ToLower(a.menuLabel(it)), q) >= 0 {
				hits = append(hits, it)
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return [][]menuItemDef{hits}
}

// layoutMenuGroups stamps relY onto every row of groups and reports the
// divider rows and the modal height the result needs.
func layoutMenuGroups(groups [][]menuItemDef) (items []menuItemDef, dividers []int, modalHeight int) {
	dividers = []int{menuDividerY}
	y := menuContentY
	for gi, g := range groups {
		for _, it := range g {
			it.relY = y
			items = append(items, it)
			y++
		}
		if gi < len(groups)-1 {
			dividers = append(dividers, y)
			y++
		}
	}
	if len(items) == 0 {
		// Reserve one content row for the "no matches" line so a query
		// that hits nothing still draws a frame with a body instead of
		// collapsing onto its own bottom border.
		y++
	}
	// y now points at the bottom border row; height is one beyond.
	modalHeight = y + 1
	return items, dividers, modalHeight
}

// menuMatchRank scores an already-lower-cased label against an
// already-lower-cased query. Lower is better: 0 for a whole-label
// prefix, 1 for a word prefix ("branch" → "Switch branch…"), 2 for a
// substring anywhere, 3 for a subsequence ("sb" → "Switch branch…").
// -1 means no match at all. The tiers exist so Enter can run the single
// best match rather than whatever happens to sit highest in the table.
func menuMatchRank(label, q string) int {
	if q == "" {
		return 0
	}
	lr, qr := []rune(label), []rune(q)
	if len(qr) > len(lr) {
		return -1
	}
	best := -1
	for i := 0; i+len(qr) <= len(lr); i++ {
		if !runesHavePrefix(lr[i:], qr) {
			continue
		}
		if i == 0 {
			return 0
		}
		if menuWordBreak(lr[i-1]) {
			return 1
		}
		best = 2
	}
	if best >= 0 {
		return best
	}
	if runesSubsequence(lr, qr) {
		return 3
	}
	return -1
}

// menuWordBreak reports whether r ends a word for match-ranking
// purposes — the separators that actually show up in action labels.
func menuWordBreak(r rune) bool {
	switch r {
	case ' ', '-', '_', '/', '&', '(', '.':
		return true
	}
	return false
}

// runesHavePrefix is strings.HasPrefix over rune slices, so ranking
// never has to reason about byte offsets landing mid-rune.
func runesHavePrefix(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

// runesSubsequence reports whether every rune of q appears in s in
// order — the loose "type the initials" match that makes "sb" find
// "Switch branch…".
func runesSubsequence(s, q []rune) bool {
	i := 0
	for _, r := range s {
		if i < len(q) && r == q[i] {
			i++
		}
	}
	return i == len(q)
}
