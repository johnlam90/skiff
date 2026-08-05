// =============================================================================
// File: internal/app/menudef_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for menudef.go — the action menu's row table, the drill-in
// tables, the type-to-filter matcher and the layout pass. The pinned
// relY / height numbers are deliberate: menuLayout is the one place row
// positions come from, so a change that shifts them should require
// updating these expectations on purpose. The catalog tests are the
// safety net for CLAUDE.md's "every action lives in the ≡ menu" rule —
// they walk the top level plus every drill-in and refuse to let an
// action disappear.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/editor"
)

// preRedesignMenuActions is the complete set of actions the ≡ menu
// offered before the seven-group redesign, with the one dynamic label
// ("Paste into <folder>") normalised. Nothing may leave this list: the
// redesign is allowed to demote an action into a drill-in, never to drop
// it. Resolved against an app with the sidebar shown and wrap on.
var preRedesignMenuActions = []string{
	"Save", "Save & close tab", "Close tab", "Reopen closed tab",
	"Undo", "Redo", "Revert file",
	"Find in file", "Find in project", "Go to line", "Find file in project",
	"Git changes", "Diff this file", "History of this file", "Commit history",
	"Commit changes…", "Push", "Pull", "Switch branch…", "More git actions…",
	"New file", "Rename file", "Delete file", "Rename folder", "Delete folder",
	"Undo delete", "Cut file", "Copy file", "Paste into …", "Duplicate file",
	"Copy relative path", "Copy absolute path",
	"Copy selection", "Cut selection", "Paste", "Toggle line comment",
	"Move line up", "Move line down", "Duplicate line",
	"Hide file explorer", "Unwrap long lines", "Theme…",
	"Quit editor",
}

// postRedesignMenuActions is everything the redesign-era menu ADDED to
// the catalog: the two drill-in doors, the navigation rows, the
// disk-conflict escape hatch shipped alongside it, the shortcut
// reference, and the tree's ignored-file toggle. Listing them explicitly
// is what makes the catalog test a two-way pin — a stray new row has to
// be declared here on purpose.
var postRedesignMenuActions = []string{
	"Git…", "File clipboard…",
	"Go to matching bracket", "Move to previous word", "Move to next word",
	"Resolve disk conflict…", "Refresh file tree", "Keyboard shortcuts…",
	// Both forms of the one toggle whose default the tree owns, so the
	// pin doesn't quietly depend on which way that default points.
	"Show ignored files", "Hide ignored files",
}

// menuCatalog flattens every built-in row the ≡ menu can reach — the top
// level plus every drill-in — ignoring visibility, so catalog tests pin
// the action inventory rather than one session's state.
func menuCatalog() []menuItemDef {
	var out []menuItemDef
	for _, g := range builtinMenuGroups() {
		out = append(out, g...)
	}
	for _, d := range menuDrillIns() {
		out = append(out, d.items...)
	}
	return out
}

// menuCatalogLabel resolves a row's user-visible label, collapsing the
// paste row's folder-dependent name to a stable key so assertions don't
// depend on the temp-dir basename.
func menuCatalogLabel(a *App, it menuItemDef) string {
	l := a.menuLabel(it)
	if strings.HasPrefix(l, "Paste into ") {
		return "Paste into …"
	}
	return l
}

// menuItemByLabelOK is the non-fatal sibling of menuItemByLabel: many
// rows are now hidden rather than dimmed, so "is this row on screen at
// all" is a thing tests need to ask without failing.
func menuItemByLabelOK(a *App, label string) (menuItemDef, bool) {
	items, _, _ := a.menuLayout()
	for _, item := range items {
		if a.menuLabel(item) == label {
			return item, true
		}
	}
	return menuItemDef{}, false
}

// drillInItemByLabel finds a demoted row inside a drill-in table, for
// tests that want to assert on a git verb or a file-clipboard action
// specifically as a submenu row. menuItemByLabel searches both levels
// and is the right call when a test only cares that the action exists;
// this one pins WHERE it lives.
func drillInItemByLabel(t *testing.T, a *App, label string) menuItemDef {
	t.Helper()
	for _, d := range menuDrillIns() {
		for _, it := range d.items {
			if menuCatalogLabel(a, it) == label {
				return it
			}
		}
	}
	t.Fatalf("drill-in item %q not found", label)
	return menuItemDef{}
}

// TestMenuLayout_EmptySession pins the headline claim of the redesign:
// with no tab, no repo and no custom actions the menu collapses to the
// ten rows that can actually do something — New file, the two project
// searches, the six view rows and Quit — and the whole modal is 18
// cells tall, well inside an 80×24 tmux split.
func TestMenuLayout_EmptySession(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = nil
	items, dividers, h := a.menuLayout()

	if h != 18 {
		t.Errorf("modalHeight = %d, want 18", h)
	}
	if got := len(items); got != 10 {
		t.Errorf("item count = %d, want 10; got %v", got, menuLabels(a, items))
	}
	wantDiv := []int{3, 5, 8, 15}
	if len(dividers) != len(wantDiv) {
		t.Fatalf("dividers = %v, want %v", dividers, wantDiv)
	}
	for i, d := range wantDiv {
		if dividers[i] != d {
			t.Errorf("dividers[%d] = %d, want %d", i, dividers[i], d)
		}
	}
	if items[0].relY != menuContentY {
		t.Errorf("first row relY = %d, want %d", items[0].relY, menuContentY)
	}
}

// menuLabels resolves a slice of rows to their labels — test-failure
// output that names rows beats one that counts them.
func menuLabels(a *App, items []menuItemDef) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, a.menuLabel(it))
	}
	return out
}

// TestMenuVisibility_HidesWhatCannotApply is the contract for the
// enabled-vs-visible split: tab-scoped rows are absent entirely with no
// tab open (they used to burn scroll space greyed out) and reappear the
// moment a buffer exists.
func TestMenuVisibility_HidesWhatCannotApply(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	for _, label := range []string{"Save", "Undo", "Paste", "Move line up", "Find in file", "Git…"} {
		if _, ok := menuItemByLabelOK(a, label); ok {
			t.Errorf("row %q should be hidden with no tab open", label)
		}
	}

	a.openFile(target)
	for _, label := range []string{"Save", "Undo", "Paste", "Move line up", "Find in file"} {
		if _, ok := menuItemByLabelOK(a, label); !ok {
			t.Errorf("row %q should appear once a tab is open", label)
		}
	}
	// Still dimmed-but-present is a different thing from hidden: Undo
	// has nothing to undo yet, and that is worth showing.
	undo, _ := menuItemByLabelOK(a, "Undo")
	if undo.enabled(a) {
		t.Error("Undo should be present but disabled on a freshly opened file")
	}
}

// TestMenuLayout_ToggleLineCommentRow ensures the comment action is
// hidden without an editable tab and enabled with one — the same
// predicate the direct app method uses.
func TestMenuLayout_ToggleLineCommentRow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	if _, ok := menuItemByLabelOK(a, "Toggle line comment"); ok {
		t.Fatal("comment row should be hidden without an active tab")
	}

	a.openFile(target)
	item, ok := menuItemByLabelOK(a, "Toggle line comment")
	if !ok {
		t.Fatal("comment row should appear for a .go tab")
	}
	if !item.enabled(a) {
		t.Fatal("comment row should be enabled for a .go tab")
	}
}

// TestMenuCatalog_Shortcuts pins the right-side hint text shown in the
// action menu to the Esc-leader bindings that are meant to be
// discoverable there. It walks the whole catalog (top level + drill-ins)
// because the demoted rows carry their hints into the pick's tag column.
func TestMenuCatalog_Shortcuts(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	want := map[string]string{
		"Save":                   "Esc s",
		"Save & close tab":       "",
		"Close tab":              "Esc w",
		"Reopen closed tab":      "Esc o",
		"Undo":                   "Esc u",
		"Redo":                   "Esc r",
		"Revert file":            "",
		"Find in file":           "Esc f",
		"Find in project":        "Esc F",
		"Go to line":             "Esc l",
		"Go to matching bracket": "Esc %",
		"Move to previous word":  "Esc b",
		"Move to next word":      "Esc e",
		"Find file in project":   "Esc p",
		"New file":               "Esc n",
		"Rename file":            "",
		"Delete file":            "",
		"Copy relative path":     "",
		"Copy absolute path":     "",
		"Copy selection":         "Esc c",
		"Cut selection":          "Esc x",
		"Paste":                  "Esc v",
		"Toggle line comment":    "Esc /",
		"Move line up":           "Esc k",
		"Move line down":         "Esc j",
		"Duplicate line":         "Esc d",
		"Hide file explorer":     "Esc t",
		"Unwrap long lines":      "Esc z",
		"Keyboard shortcuts…":    "Esc ?",
		"Quit editor":            "Esc q",
		// Demoted into the Git drill-in — the hint travels with it.
		"Git changes": "Esc g",
		// The drill-in doors deliberately advertise nothing: Esc g opens
		// the panel, not the pick, and a hint that lies is worse than
		// no hint.
		"Git…":            "",
		"File clipboard…": "",
	}

	seen := make(map[string]string)
	for _, item := range menuCatalog() {
		seen[menuCatalogLabel(a, item)] = item.shortcut
	}
	for label, shortcut := range want {
		if got, ok := seen[label]; !ok {
			t.Errorf("menu item %q not found in the catalog", label)
		} else if got != shortcut {
			t.Errorf("%s shortcut = %q, want %q", label, got, shortcut)
		}
	}
}

// TestMenuCatalog_ShortcutsAreRealLeaderBindings keeps the hint column
// honest: every "Esc <rune>" a row advertises must actually be bound in
// leaderBindings, or the menu is teaching a keystroke that does nothing.
func TestMenuCatalog_ShortcutsAreRealLeaderBindings(t *testing.T) {
	for _, item := range menuCatalog() {
		if item.shortcut == "" {
			continue
		}
		rs := []rune(item.shortcut)
		if len(rs) != 5 || string(rs[:4]) != "Esc " {
			t.Errorf("shortcut %q on %q is not in 'Esc <rune>' form", item.shortcut, item.label)
			continue
		}
		if leaderActionFor(rs[4]) == nil {
			t.Errorf("shortcut %q on %q is not a real leader binding", item.shortcut, item.label)
		}
	}
}

// TestMenuCatalog_EveryPreRedesignActionStillReachable is the
// reachability contract: collapsing nine git rows and six file-clipboard
// rows into two drill-ins is allowed, dropping an action is not. Walks
// the top level plus every registered drill-in and demands the full
// pre-redesign inventory.
func TestMenuCatalog_EveryPreRedesignActionStillReachable(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	have := make(map[string]bool)
	for _, it := range menuCatalog() {
		have[menuCatalogLabel(a, it)] = true
	}
	for _, label := range preRedesignMenuActions {
		if !have[label] {
			t.Errorf("action %q is no longer reachable from the ≡ menu", label)
		}
	}
}

// TestMenuCatalog_NoUndeclaredAdditions is the other half of the pin:
// anything in the catalog that isn't a pre-redesign action must be
// declared in postRedesignMenuActions, so a new row can't slip in
// without someone deciding it belongs in the menu.
func TestMenuCatalog_NoUndeclaredAdditions(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	known := make(map[string]bool, len(preRedesignMenuActions)+len(postRedesignMenuActions))
	for _, l := range preRedesignMenuActions {
		known[l] = true
	}
	for _, l := range postRedesignMenuActions {
		known[l] = true
	}
	for _, it := range menuCatalog() {
		if label := menuCatalogLabel(a, it); !known[label] {
			t.Errorf("undeclared menu action %q — add it to postRedesignMenuActions on purpose", label)
		}
	}
}

// TestMenuDrillIns_HoldEveryDemotedAction pins the split between the two
// levels: an action lives either at the top level or in exactly one
// drill-in, never in both (which would double-list it) and never in
// neither (which the reachability test already catches from the other
// side).
func TestMenuDrillIns_HoldEveryDemotedAction(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	top := make(map[string]bool)
	for _, g := range builtinMenuGroups() {
		for _, it := range g {
			top[menuCatalogLabel(a, it)] = true
		}
	}
	seen := make(map[string]int)
	for _, d := range menuDrillIns() {
		if len(d.items) == 0 {
			t.Errorf("drill-in %q is empty", d.title)
		}
		for _, it := range d.items {
			label := menuCatalogLabel(a, it)
			seen[label]++
			if top[label] {
				t.Errorf("%q is listed both at the top level and inside %q", label, d.title)
			}
			if it.action == nil {
				t.Errorf("drill-in row %q has no action", label)
			}
		}
	}
	// The nine git verbs and the six file-clipboard actions are exactly
	// what the top level is allowed to demote.
	wantDemoted := []string{
		"Git changes", "Commit changes…", "Push", "Pull", "Switch branch…",
		"Diff this file", "History of this file", "Commit history", "More git actions…",
		"Cut file", "Copy file", "Duplicate file", "Paste into …",
		"Copy relative path", "Copy absolute path",
	}
	if len(seen) != len(wantDemoted) {
		t.Errorf("drill-ins hold %d actions, want %d", len(seen), len(wantDemoted))
	}
	for _, label := range wantDemoted {
		if seen[label] != 1 {
			t.Errorf("demoted action %q appears %d times in the drill-ins, want 1", label, seen[label])
		}
	}
}

// TestMenuLayout_WithCustomActions checks the splice-before-Quit
// behaviour: two custom actions land as their own group sitting
// directly above the Quit row, with a divider on each side. Modal
// height grows by 3 rows (2 items + 1 divider).
func TestMenuLayout_WithCustomActions(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = []customactions.Action{
		{Label: "Open on Rager", Command: "echo r"},
		{Label: "Open on Cascade", Command: "echo c"},
	}
	items, _, h := a.menuLayout()

	if h != 21 { // 18 + 2 items + 1 divider
		t.Errorf("modalHeight = %d, want 21", h)
	}
	// Custom actions should be the second-to-last and third-to-last
	// rows, with Quit as the final row.
	last := len(items) - 1
	if items[last].label != "Quit editor" {
		t.Fatalf("last row = %q, want Quit editor", items[last].label)
	}
	if items[last-1].label != "Open on Cascade" {
		t.Errorf("row above Quit = %q, want Open on Cascade", items[last-1].label)
	}
	if items[last-2].label != "Open on Rager" {
		t.Errorf("two above Quit = %q, want Open on Rager", items[last-2].label)
	}
}

// TestMenuLayout_CustomActionsAlwaysEnabled pins the rule that the
// menu never disables a custom action — neither prompted ones (where
// the form modal owns the file-or-no-file question) nor plain ones
// (whose commands may not touch $FILE at all, like "brew upgrade").
// Trying to gate prompt-less actions on hasFileTab guessed wrong
// for actions like Upgrade Skiff and made them appear broken.
func TestMenuLayout_CustomActionsAlwaysEnabled(t *testing.T) {
	a := newTestApp(t, t.TempDir()) // no tabs opened

	a.customActions = []customactions.Action{
		{Label: "Plain", Command: "echo p"},
		{Label: "Prompted", Command: "echo q",
			Prompts: []customactions.Prompt{
				{Key: "X", Type: customactions.PromptText},
			}},
	}
	items, _, _ := a.menuLayout()

	var plain, prompted *menuItemDef
	for i := range items {
		switch items[i].label {
		case "Plain":
			plain = &items[i]
		case "Prompted":
			prompted = &items[i]
		}
	}
	if plain == nil || prompted == nil {
		t.Fatalf("custom actions missing from layout: %v", items)
	}
	if !plain.enabled(a) {
		t.Error("plain action should be enabled even with no tab open")
	}
	if !prompted.enabled(a) {
		t.Error("prompted action should be enabled even with no tab open")
	}
}

// TestHasTree_TrueAndFalse pins the visibility predicate that drives
// the single-file-mode menu filter: any app with a non-nil tree
// reports true; setting tree to nil flips it false.
func TestHasTree_TrueAndFalse(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if !a.hasTree() {
		t.Fatal("expected hasTree=true on a normal-mode app")
	}
	a.tree = nil
	if a.hasTree() {
		t.Fatal("expected hasTree=false when tree is nil")
	}
}

// TestHasGitActions_SurvivesSingleFileMode pins the one reason the
// "Git…" row is gated on hasGitActions rather than hasGitRepo: Diff this
// file and History of this file deliberately work with no tree, so the
// door must stay open there instead of stranding them off-menu.
func TestHasGitActions_SurvivesSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasGitActions() {
		t.Fatal("a plain temp dir with no tab has no git actions")
	}

	dir := initRepoWithCommit(t)
	repoApp := newTestApp(t, dir)
	if !repoApp.hasGitActions() {
		t.Fatal("a git repo should open the Git… row")
	}

	// Single-file mode: no tree at all, so hasGitRepo is false — but a
	// tab with gutter changes still has a diff to show.
	repoApp.tree = nil
	repoApp.openFile(filepath.Join(dir, "f.txt"))
	if repoApp.hasGitRepo() {
		t.Fatal("test setup: hasGitRepo must be false with no tree")
	}
	tab := repoApp.activeTabPtr()
	tab.GitLines = map[int]editor.GitLineChange{0: editor.GitLineModified}
	if !repoApp.hasGitActions() {
		t.Fatal("Git… must stay reachable in single-file mode for Diff / History")
	}
}

// TestHasFileClipActions_TabOrClipboard pins the drill-in door's gate:
// either a file-backed tab (something to cut / copy / name) or a loaded
// file clipboard (somewhere to paste) is enough.
func TestHasFileClipActions_TabOrClipboard(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if a.hasFileClipActions() {
		t.Fatal("no tab and no clipboard means no file-clipboard actions")
	}
	a.clipCopyPath(target)
	if !a.hasFileClipActions() {
		t.Fatal("a loaded file clipboard should open the drill-in")
	}
	a.fileClipPath = ""
	a.openFile(target)
	if !a.hasFileClipActions() {
		t.Fatal("a file-backed tab should open the drill-in")
	}
}

// TestMenuLayout_HidesSidebarToggleInSingleFileMode is the contract
// test for the single-file-mode feature: with no file tree, the
// 'Show / Hide file explorer' row must not appear in the action
// menu — it's nonsensical there because the sidebar isn't built.
// With a tree present (normal mode), the row appears.
func TestMenuLayout_HidesSidebarToggleInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// Sanity: the toggle row IS present in normal mode.
	items, _, _ := a.menuLayout()
	if !containsSidebarToggle(items, a) {
		t.Fatal("expected sidebar-toggle row in normal mode")
	}

	// Simulate single-file mode by clearing the tree.
	a.tree = nil
	items, _, _ = a.menuLayout()
	if containsSidebarToggle(items, a) {
		t.Fatal("expected sidebar-toggle row to be absent when tree is nil")
	}
}

// TestMenuLayout_NoEmptyDividerAfterFiltering guards against the
// regression where filtering the sole item of a group out would
// leave a dangling divider row, doubling the gap in the menu.
func TestMenuLayout_NoEmptyDividerAfterFiltering(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil // collapses the Go group to empty

	_, dividers, height := a.menuLayout()
	// Dividers must all sit strictly below the chrome divider and
	// strictly above the modal's bottom border (height-1). Two adjacent
	// dividers (gap == 1) would mean we kept a divider for a now-empty
	// group.
	for i := 1; i < len(dividers); i++ {
		if dividers[i]-dividers[i-1] < 2 {
			t.Fatalf("dividers too close: %v (height=%d)", dividers, height)
		}
	}
}

// TestMenuLayout_FilterFlattensGroups pins what a query does to the
// layout: the group structure collapses to one divider-free block of
// matches, the rows keep their contiguous relY run, and a query that
// hits nothing still reserves a row for the "no matches" line.
func TestMenuLayout_FilterFlattensGroups(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.menuFilter.SetText("the")
	items, dividers, h := a.menuLayout()
	if len(items) != 1 || a.menuLabel(items[0]) != "Theme…" {
		t.Fatalf("filter 'the' matched %v, want just Theme…", menuLabels(a, items))
	}
	if len(dividers) != 1 || dividers[0] != menuDividerY {
		t.Errorf("filtered layout should keep only the chrome divider, got %v", dividers)
	}
	if h != menuContentY+2 {
		t.Errorf("one-row modal height = %d, want %d", h, menuContentY+2)
	}

	a.menuFilter.SetText("zzzz")
	items, _, h = a.menuLayout()
	if len(items) != 0 {
		t.Fatalf("filter 'zzzz' should match nothing, got %v", menuLabels(a, items))
	}
	if h != menuContentY+2 {
		t.Errorf("no-match modal height = %d, want %d (one row for 'no matches')", h, menuContentY+2)
	}
}

// TestMenuLayout_FilterCrossesGroups is the whole point of the filter:
// a query finds rows wherever they live, so "file" pulls matches out of
// more than one group at once and the result is still one flat block.
func TestMenuLayout_FilterCrossesGroups(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuFilter.SetText("file")
	items, dividers, _ := a.menuLayout()
	got := menuLabels(a, items)
	if len(dividers) != 1 {
		t.Errorf("a filtered list has no group dividers, got %v", dividers)
	}
	for _, want := range []string{"New file", "Rename file", "Delete file", "Find in file", "File clipboard…"} {
		found := false
		for _, l := range got {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("filter 'file' should match %q; got %v", want, got)
		}
	}
	for _, unwanted := range []string{"Undo", "Quit editor", "Theme…"} {
		for _, l := range got {
			if l == unwanted {
				t.Errorf("filter 'file' should not match %q", unwanted)
			}
		}
	}
}

// TestMenuMatchRank_Tiers pins the ranking the filter uses to decide
// which row Enter runs: whole-label prefix beats word prefix beats a
// substring beats a loose subsequence, and a miss is a miss.
func TestMenuMatchRank_Tiers(t *testing.T) {
	cases := []struct {
		label, query string
		want         int
	}{
		{"switch branch…", "sw", 0},
		{"switch branch…", "branch", 1},
		{"save & close tab", "close", 1},
		{"toggle line comment", "ggle", 2},
		{"switch branch…", "sb", 3},
		{"quit editor", "zzz", -1},
		{"push", "pushpush", -1},
		{"push", "", 0},
	}
	for _, c := range cases {
		if got := menuMatchRank(c.label, c.query); got != c.want {
			t.Errorf("menuMatchRank(%q, %q) = %d, want %d", c.label, c.query, got, c.want)
		}
	}
}

// containsSidebarToggle is the menu-test helper that locates the
// dynamic-label row whose label flips between "Show file explorer"
// and "Hide file explorer". We match on labelFor's resolved string
// because the sidebar-toggle row is the only one that uses those
// exact labels.
func containsSidebarToggle(items []menuItemDef, a *App) bool {
	for _, it := range items {
		if it.labelFor == nil {
			continue
		}
		l := it.labelFor(a)
		if l == "Show file explorer" || l == "Hide file explorer" {
			return true
		}
	}
	return false
}

// TestMenuLabelBudget_InvertsRowWidth is the invariant that keeps the
// modal's sizing and drawMenu's clipping from drifting apart: the width a
// row asks for is exactly the width at which its label stops being
// clipped. Break the pair and either the modal grows a column short (every
// row ellipsised at full width) or a column long (dead space forever).
func TestMenuLabelBudget_InvertsRowWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	rows := []menuItemDef{
		{label: "Save", shortcut: "Esc s"},
		{label: "Theme…"},
		{label: "A rather long user-named custom action", shortcut: "Esc %"},
		{label: "x"},
	}
	for _, it := range rows {
		want := runeLen(a.menuLabel(it))
		w := a.menuRowWidth(it)
		if got := menuLabelBudget(w, it); got != want {
			t.Errorf("%q: budget at its own width = %d, want %d", it.label, got, want)
		}
		if got := menuLabelBudget(w-1, it); got >= want {
			t.Errorf("%q: one column narrower must clip, budget = %d", it.label, got)
		}
	}
}

// TestMenuNaturalWidth_GrowsForLongLabel pins the "let the modal grow"
// half of the long-label fix: a user-named custom action wider than the
// base frame widens it enough to render whole, and a menu of ordinary rows
// never shrinks below modalWidth.
func TestMenuNaturalWidth_GrowsForLongLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = nil
	if got := a.menuNaturalWidth(); got != modalWidth {
		t.Fatalf("default menu width = %d, want the %d floor", got, modalWidth)
	}

	long := "Deploy the staging environment and tail the logs"
	a.customActions = []customactions.Action{{Label: long, Command: "true"}}
	w := a.menuNaturalWidth()
	if w <= modalWidth {
		t.Fatalf("width %d did not grow for a %d-rune label", w, runeLen(long))
	}
	row := menuItemByLabel(t, a, long)
	if got := menuLabelBudget(w, row); got < runeLen(long) {
		t.Fatalf("grown width %d still clips the label (budget %d < %d)",
			w, got, runeLen(long))
	}
}
