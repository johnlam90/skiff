// =============================================================================
// File: internal/app/menudef.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// menudef.go is the action menu's data model: the menuItemDef row shape,
// the built-in group table, and the layout pass that stamps a relY onto
// every row. Adding a menu row is adding a struct literal here — the
// behavior in menu.go and the drawing in drawMenu both read the layout
// rather than hard-coding offsets.
//
// Custom actions from ~/.config/skiff/actions.json are spliced in at
// layout time rather than baked into the table, so toggling them on or
// off never touches the built-ins.

package app

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
	// same as enabled, which renders the row greyed out). Used to
	// hide the sidebar toggle in single-file mode, where there's no
	// tree to show or hide.
	visible func(*App) bool
}

// builtinMenuGroups returns the editor's built-in action groups in
// display order. Custom actions loaded from
// ~/.config/skiff/actions.json get prepended as their own group
// in menuLayout — they're not included here so toggling them on or
// off doesn't require touching this table.
//
// Each group is rendered as a contiguous block; menuLayout interleaves
// dividers between groups and recomputes every relY. The relY field is
// left zero here on purpose — it gets stamped at layout time.
func builtinMenuGroups() [][]menuItemDef {
	return [][]menuItemDef{
		// Tab actions
		{
			{label: "Save", shortcut: "Esc s", action: (*App).menuSave, enabled: (*App).hasSavableTab},
			{label: "Save & close tab", action: (*App).menuSaveAndClose, enabled: (*App).hasSavableTab},
			{label: "Close tab", shortcut: "Esc w", action: (*App).menuClose, enabled: (*App).hasTab},
			{label: "Reopen closed tab", shortcut: "Esc o", action: (*App).menuReopenTab, enabled: (*App).hasClosedTab},
		},
		// History
		{
			{label: "Undo", shortcut: "Esc u", action: (*App).menuUndo, enabled: (*App).hasUndo},
			{label: "Redo", shortcut: "Esc r", action: (*App).menuRedo, enabled: (*App).hasRedo},
			{label: "Revert file", action: (*App).menuRevert, enabled: (*App).hasRevert},
		},
		// Search
		{
			{label: "Find in file", shortcut: "Esc f", action: (*App).menuFind, enabled: (*App).hasFindable},
			{label: "Find in project", shortcut: "Esc F", action: (*App).menuFindInProject, enabled: (*App).hasFinder, visible: (*App).hasTree},
			{label: "Go to line", shortcut: "Esc l", action: (*App).menuGoToLine, enabled: (*App).hasFindable},
			{label: "Find file in project", shortcut: "Esc p", action: (*App).menuFindFile, enabled: (*App).hasFinder},
		},
		// Git
		{
			{label: "Git changes", shortcut: "Esc g", action: (*App).menuGitChanges, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
			{label: "Diff this file", action: (*App).menuDiffFile, enabled: (*App).hasDiffableTab},
			{label: "History of this file", action: (*App).menuFileHistory, enabled: (*App).hasFileHistoryTab},
			{label: "Commit history", action: (*App).menuCommitHistory, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
			{label: "Commit changes…", action: (*App).menuGitCommit, enabled: (*App).hasGitChanges, visible: (*App).hasTree},
			{label: "Push", action: (*App).menuGitPush, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
			{label: "Pull", action: (*App).menuGitPull, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
			{label: "Switch branch…", action: (*App).menuGitSwitchBranch, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
			{label: "More git actions…", action: (*App).menuGitExtras, enabled: (*App).hasGitRepo, visible: (*App).hasTree},
		},
		// File actions
		{
			{shortcut: "Esc n", action: (*App).menuNewFile, enabled: alwaysTrue, labelFor: (*App).newFileLabel},
			{label: "Rename file", action: (*App).menuRename, enabled: (*App).hasFileTab},
			{label: "Delete file", action: (*App).menuDelete, enabled: (*App).hasFileTab},
			{action: (*App).menuRenameFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).renameFolderLabel},
			{action: (*App).menuDeleteFolder, enabled: (*App).hasActiveSubfolder, labelFor: (*App).deleteFolderLabel},
			{action: (*App).menuUndoDelete, enabled: (*App).hasTrashedEntry, labelFor: (*App).undoDeleteLabel},
			{label: "Cut file", action: (*App).menuCutFile, enabled: (*App).hasFileTab},
			{label: "Copy file", action: (*App).menuCopyFile, enabled: (*App).hasFileTab},
			{action: (*App).menuPasteEntry, enabled: (*App).hasFileClip, labelFor: (*App).pasteEntryLabel, visible: (*App).hasFileClip},
			{label: "Duplicate file", action: (*App).menuDuplicateFile, enabled: (*App).hasFileTab},
			{label: "Copy relative path", action: (*App).menuCopyRelativePath, enabled: (*App).hasFileTab},
			{label: "Copy absolute path", action: (*App).menuCopyAbsolutePath, enabled: (*App).hasFileTab},
		},
		// Clipboard
		{
			{label: "Copy selection", shortcut: "Esc c", action: (*App).menuCopy, enabled: (*App).hasSelection},
			{label: "Cut selection", shortcut: "Esc x", action: (*App).menuCut, enabled: (*App).hasSelection},
			{label: "Paste", shortcut: "Esc v", action: (*App).menuPaste, enabled: (*App).hasClipboard},
			{label: "Toggle line comment", shortcut: "Esc /", action: (*App).menuToggleLineComment, enabled: (*App).hasCommentableTab},
		},
		// Line ops
		{
			{label: "Move line up", shortcut: "Esc k", action: (*App).menuMoveLineUp, enabled: (*App).hasEditableTab},
			{label: "Move line down", shortcut: "Esc j", action: (*App).menuMoveLineDown, enabled: (*App).hasEditableTab},
			{label: "Duplicate line", shortcut: "Esc d", action: (*App).menuDuplicateLine, enabled: (*App).hasEditableTab},
		},
		// View toggle
		{
			{shortcut: "Esc t", action: (*App).menuToggleSidebar, enabled: alwaysTrue, labelFor: (*App).sidebarToggleLabel, visible: (*App).hasTree},
			{shortcut: "Esc z", action: (*App).menuToggleWrap, enabled: alwaysTrue, labelFor: (*App).wrapToggleLabel},
			{label: "Theme…", action: (*App).menuTheme, enabled: alwaysTrue},
		},
		// Quit
		{
			{label: "Quit editor", shortcut: "Esc q", action: (*App).menuQuit, enabled: alwaysTrue},
		},
	}
}

// alwaysTrue is the default predicate for actions that are always applicable
// (currently just Quit — which has no preconditions).
func alwaysTrue(*App) bool { return true }

// menuLayout flattens the visible menu groups into a single ordered
// slice of items with relY positions assigned, plus the divider rows
// and the modal's total cell height. Custom actions (when configured)
// get spliced in as their own group right before the Quit row, so
// they sit at the bottom of the menu where the user reaches for
// "what do I do with this file" actions. Recomputed on every call —
// cheap, and lets the layout react when actions.json is reloaded
// mid-session.
func (a *App) menuLayout() (items []menuItemDef, dividers []int, modalHeight int) {
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
	visibleGroups := make([][]menuItemDef, 0, len(groups))
	for _, g := range groups {
		kept := make([]menuItemDef, 0, len(g))
		for _, it := range g {
			if it.visible != nil && !it.visible(a) {
				continue
			}
			kept = append(kept, it)
		}
		if len(kept) > 0 {
			visibleGroups = append(visibleGroups, kept)
		}
	}
	groups = visibleGroups

	// Title at relY 1, divider under it at relY 2, first item at relY 3.
	dividers = []int{2}
	y := 3
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
	// y now points at the bottom border row; height is one beyond.
	modalHeight = y + 1
	return items, dividers, modalHeight
}

// hasTree is the menu visibility predicate for tree-dependent rows.
// True when the file tree was built at startup; false in single-file
// mode, where we deliberately skipped tree construction to avoid
// indexing the working directory.
func (a *App) hasTree() bool {
	return a.tree != nil
}
