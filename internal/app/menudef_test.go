// =============================================================================
// File: internal/app/menudef_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for menudef.go — the action menu's row table and layout pass.
// The pinned relY / height numbers are deliberate: menuLayout is the one
// place row positions come from, so a change that shifts them should
// require updating these expectations on purpose.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnlam90/skiff/internal/customactions"
)

// TestMenuLayout_NoCustomActions pins down the baseline geometry: with
// zero custom actions the modal still has eight built-in groups and the
// height matches the expected layout total. Catches accidental
// off-by-one regressions when someone tweaks the layout helper.
func TestMenuLayout_NoCustomActions(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = nil
	items, dividers, h := a.menuLayout()

	if h != 54 {
		t.Errorf("modalHeight = %d, want 54", h)
	}
	if got := len(items); got != 42 {
		t.Errorf("item count = %d, want 42 built-ins", got)
	}
	wantDiv := []int{2, 7, 11, 16, 26, 38, 43, 47, 51}
	if len(dividers) != len(wantDiv) {
		t.Fatalf("dividers = %v, want %v", dividers, wantDiv)
	}
	for i, d := range wantDiv {
		if dividers[i] != d {
			t.Errorf("dividers[%d] = %d, want %d", i, dividers[i], d)
		}
	}
}

// TestMenuLayout_ToggleLineCommentRow ensures the comment action is present
// and uses the same enablement predicate as the direct app method.
func TestMenuLayout_ToggleLineCommentRow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	item := menuItemByLabel(t, a, "Toggle line comment")
	if item.enabled(a) {
		t.Fatal("comment row should be disabled without an active tab")
	}

	a.openFile(target)
	item = menuItemByLabel(t, a, "Toggle line comment")
	if !item.enabled(a) {
		t.Fatal("comment row should be enabled for a .go tab")
	}
}

// TestMenuLayout_Shortcuts pins the right-side hint text shown in the action
// menu to the Esc-leader bindings that are meant to be discoverable there.
func TestMenuLayout_Shortcuts(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	want := map[string]string{
		"Save":                 "Esc s",
		"Save & close tab":     "",
		"Close tab":            "Esc w",
		"Undo":                 "Esc u",
		"Redo":                 "Esc r",
		"Revert file":          "",
		"Find in file":         "Esc f",
		"Find file in project": "Esc p",
		"New file":             "Esc n",
		"Rename file":          "",
		"Delete file":          "",
		"Copy relative path":   "",
		"Copy absolute path":   "",
		"Copy selection":       "Esc c",
		"Cut selection":        "Esc x",
		"Paste":                "Esc v",
		"Toggle line comment":  "Esc /",
		"Hide file explorer":   "Esc t",
		"Unwrap long lines":    "Esc z",
		"Quit editor":          "Esc q",
	}

	items, _, _ := a.menuLayout()
	seen := make(map[string]string, len(items))
	for _, item := range items {
		label := item.label
		if item.labelFor != nil {
			label = item.labelFor(a)
		}
		seen[label] = item.shortcut
	}
	for label, shortcut := range want {
		if got, ok := seen[label]; !ok {
			t.Errorf("menu item %q not found", label)
		} else if got != shortcut {
			t.Errorf("%s shortcut = %q, want %q", label, got, shortcut)
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

	if h != 57 { // 54 + 2 items + 1 divider
		t.Errorf("modalHeight = %d, want 57", h)
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
	a.tree = nil // collapses the View-toggle group to empty

	_, dividers, height := a.menuLayout()
	// Dividers must all sit strictly below the title divider (row 2)
	// and strictly above the modal's bottom border (height-1). Two
	// adjacent dividers (gap == 1) would mean we kept a divider for
	// a now-empty group.
	for i := 1; i < len(dividers); i++ {
		if dividers[i]-dividers[i-1] < 2 {
			t.Fatalf("dividers too close: %v (height=%d)", dividers, height)
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
