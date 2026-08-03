// =============================================================================
// File: internal/app/modals_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the secondary-modal logic in modals.go: prompt, confirm, and
// the right-click context menu. We don't render anything; we drive the
// modals directly and assert on the App fields they touch. Mutual
// exclusivity (closeAllModals) is the linchpin so most tests double-check
// no other modal flag is left on.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/overlay"
)

// TestCloseAllModals_ClearsEverything proves the helper turns off every
// modal flag and clears the side-state (drag, auto-scroll dir, callbacks).
func TestCloseAllModals_ClearsEverything(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.contextOpen = true
	a.hoveredMenuRow = 3
	a.contextNode = a.tree.Root
	a.contextItems = []contextItem{{label: "x"}}
	a.dragMode = "editor"
	a.autoScrollDir = 1

	a.closeAllModals()

	if a.menuOpen || a.contextOpen {
		t.Fatal("expected all modal flags off")
	}
	if a.hoveredMenuRow != -1 {
		t.Fatalf("hoveredMenuRow not cleared: %d", a.hoveredMenuRow)
	}
	if a.contextNode != nil || a.contextItems != nil {
		t.Fatal("context state not cleared")
	}
	if a.dragMode != "" {
		t.Fatalf("dragMode not cleared: %q", a.dragMode)
	}
	if a.autoScrollDir != 0 {
		t.Fatalf("autoScrollDir not reset: %d", a.autoScrollDir)
	}
}

// TestAnyModalOpen returns true while an overlay is up and false once
// everything is closed. The report comes from the overlay stack, not
// the per-modal booleans, so each surface must go through its real
// opener — a flag flipped by hand no longer counts as open.
func TestAnyModalOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.anyModalOpen() {
		t.Fatal("none open")
	}
	a.openMenu()
	if !a.anyModalOpen() {
		t.Fatal("expected true with menu open")
	}
	a.openPrompt("T", "", "", nil)
	if !a.anyModalOpen() {
		t.Fatal("expected true with prompt open")
	}
	a.openConfirm("T", "m", nil)
	if !a.anyModalOpen() {
		t.Fatal("expected true with confirm open")
	}
	a.openTreeContext(a.tree.Root, 2, 2)
	if !a.anyModalOpen() {
		t.Fatal("expected true with context open")
	}
	a.closeAllModals()
	if a.anyModalOpen() {
		t.Fatal("expected false after closeAllModals")
	}
}

// keyEv builds a synthetic tcell key event for tests.
func keyEv(k tcell.Key, r rune) *tcell.EventKey {
	return tcell.NewEventKey(k, r, tcell.ModNone)
}

// promptPrefab returns the open prompt overlay, failing the test when
// none is up — the prompt's state lives on the prefab now, not on App.
func promptPrefab(t *testing.T, a *App) *overlay.Prompt {
	t.Helper()
	p, ok := a.overlays.Top().(*overlay.Prompt)
	if !ok {
		t.Fatalf("no prompt overlay open; top = %T", a.overlays.Top())
	}
	return p
}

// promptIsOpen reports whether a prompt overlay is up.
func promptIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*overlay.Prompt)
	return ok
}

// submitPrompt presses Enter through the real routing path so tests
// exercise the same stack traversal the user does.
func submitPrompt(a *App) {
	a.handleKey(keyEv(tcell.KeyEnter, 0))
}

// confirmPrefab / infoPrefab / dirtyPrefab fetch the open overlay of
// the given kind, failing the test when it isn't up — modal state lives
// on the prefabs now, not on App.
func confirmPrefab(t *testing.T, a *App) *overlay.Confirm {
	t.Helper()
	c, ok := a.overlays.Top().(*overlay.Confirm)
	if !ok {
		t.Fatalf("no confirm overlay open; top = %T", a.overlays.Top())
	}
	return c
}

func infoPrefab(t *testing.T, a *App) *overlay.Info {
	t.Helper()
	n, ok := a.overlays.Top().(*overlay.Info)
	if !ok {
		t.Fatalf("no info overlay open; top = %T", a.overlays.Top())
	}
	return n
}

func dirtyPrefab(t *testing.T, a *App) *overlay.Dirty {
	t.Helper()
	d, ok := a.overlays.Top().(*overlay.Dirty)
	if !ok {
		t.Fatalf("no dirty-close overlay open; top = %T", a.overlays.Top())
	}
	return d
}

// confirmIsOpen / infoIsOpen / dirtyIsOpen report whether that overlay
// kind is up.
func confirmIsOpen(a *App) bool { _, ok := a.overlays.Top().(*overlay.Confirm); return ok }
func infoIsOpen(a *App) bool    { _, ok := a.overlays.Top().(*overlay.Info); return ok }
func dirtyIsOpen(a *App) bool   { _, ok := a.overlays.Top().(*overlay.Dirty); return ok }

// confirmYes picks Yes through real routing: Right arms the Yes button,
// Enter fires it.
func confirmYes(a *App) {
	a.handleKey(keyEv(tcell.KeyRight, 0))
	a.handleKey(keyEv(tcell.KeyEnter, 0))
}

// pressEsc dismisses the top overlay through real routing.
func pressEsc(a *App) {
	a.handleKey(keyEv(tcell.KeyEsc, 0))
}

// dirtyChoose activates one dirty-close button (0 Cancel, 1 Discard,
// 2 Save) through real routing.
func dirtyChoose(t *testing.T, a *App, idx int) {
	t.Helper()
	dirtyPrefab(t, a).Hover = idx
	a.handleKey(keyEv(tcell.KeyEnter, 0))
}

// TestOpenPrompt_Submit runs the callback with a trimmed value and closes
// the overlay. The editing/mouse/draw behavior itself is pinned in
// internal/overlay's prompt tests — this pins the App wiring.
func TestOpenPrompt_Submit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	got := ""
	a.openPrompt("Title", "Hint", "  hello  ", func(_ *App, v string) { got = v })
	p := promptPrefab(t, a)
	if p.Field.Cursor != len([]rune("  hello  ")) {
		t.Fatalf("cursor at end of initial: got %d", p.Field.Cursor)
	}
	submitPrompt(a)
	if got != "hello" {
		t.Fatalf("callback got %q, want trimmed 'hello'", got)
	}
	if promptIsOpen(a) {
		t.Fatal("submit should close the prompt overlay")
	}
}

// TestPromptSubmit_EmptyIsNoop keeps the overlay open and skips the
// callback.
func TestPromptSubmit_EmptyIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := false
	a.openPrompt("T", "H", "   ", func(*App, string) { called = true })
	submitPrompt(a)
	if called {
		t.Fatal("empty submit should not run callback")
	}
	if !promptIsOpen(a) {
		t.Fatal("empty submit should keep the prompt open")
	}
}

// TestPromptCancel skips the callback: Esc through real routing.
func TestPromptCancel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := false
	a.openPrompt("T", "H", "x", func(*App, string) { called = true })
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if called {
		t.Fatal("cancel should not run callback")
	}
	if promptIsOpen(a) {
		t.Fatal("cancel should close")
	}
}

// TestOpenConfirm_DefaultsToNo lands on the safe button so accidental
// Enter never does anything destructive.
func TestOpenConfirm_DefaultsToNo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openConfirm("Delete", "Sure?", func(*App) {})
	if c := confirmPrefab(t, a); c.Hover != 0 {
		t.Fatalf("default focus should be No (0); got %d", c.Hover)
	}
}

// TestConfirmYes runs the callback.
func TestConfirmYes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := false
	a.openConfirm("T", "M", func(*App) { called = true })
	confirmYes(a)
	if !called {
		t.Fatal("Yes should run the callback")
	}
	if confirmIsOpen(a) {
		t.Fatal("Yes should close the overlay")
	}
}

// TestConfirmCancel skips the callback.
func TestConfirmCancel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := false
	a.openConfirm("T", "M", func(*App) { called = true })
	pressEsc(a)
	if called {
		t.Fatal("cancel should skip callback")
	}
}

// TestOpenTreeContext_Folder offers New File + Rename + Delete plus the
// two clipboard rows.
func TestOpenTreeContext_Folder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := mkdir(sub); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	// Find the child node in the tree.
	var node *filetree.Node
	for _, c := range a.tree.Root.Children {
		if c.Name == "child" {
			node = c
			break
		}
	}
	if node == nil {
		t.Fatal("child node not in tree")
	}
	a.openTreeContext(node, 5, 5)
	if !a.contextOpen {
		t.Fatal("context should open")
	}
	wantLabels := []string{"New File", "Rename", "Delete", "Cut", "Copy", "Duplicate", "Copy rel path", "Copy abs path"}
	if len(a.contextItems) != len(wantLabels) {
		t.Fatalf("folder context should have %d items, got %d", len(wantLabels), len(a.contextItems))
	}
	for i, w := range wantLabels {
		if a.contextItems[i].label != w {
			t.Fatalf("item %d label: got %q, want %q", i, a.contextItems[i].label, w)
		}
	}
}

// TestOpenTreeContext_File offers Rename / Delete / Cut / Copy /
// Duplicate plus the two path-copy rows. New File is folder-only.
func TestOpenTreeContext_File(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := writeFile(target, "x"); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	var node *filetree.Node
	for _, c := range a.tree.Root.Children {
		if c.Name == "f.txt" {
			node = c
			break
		}
	}
	if node == nil {
		t.Fatal("file node not in tree")
	}
	a.openTreeContext(node, 5, 5)
	wantLabels := []string{"Rename", "Delete", "Cut", "Copy", "Duplicate", "Copy rel path", "Copy abs path"}
	if len(a.contextItems) != len(wantLabels) {
		t.Fatalf("file context should have %d items, got %d", len(wantLabels), len(a.contextItems))
	}
	for i, w := range wantLabels {
		if a.contextItems[i].label != w {
			t.Fatalf("item %d label: got %q, want %q", i, a.contextItems[i].label, w)
		}
	}
}

// TestOpenTreeContext_Root offers New File and the two clipboard rows —
// Rename / Delete on the project root would be a footgun.
func TestOpenTreeContext_Root(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openTreeContext(a.tree.Root, 5, 5)
	wantLabels := []string{"New File", "Copy rel path", "Copy abs path"}
	if len(a.contextItems) != len(wantLabels) {
		t.Fatalf("root context should have %d items, got %d", len(wantLabels), len(a.contextItems))
	}
	for i, w := range wantLabels {
		if a.contextItems[i].label != w {
			t.Fatalf("item %d label: got %q, want %q", i, a.contextItems[i].label, w)
		}
	}
}

// TestPlaceContext_FlipsLeftAndUp tests that the popup flips when it would
// otherwise overflow the window edges, and clamps at 0.
func TestPlaceContext_FlipsLeftAndUp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 30, 20

	// Click far to the right → expect cx to flip left so the popup ends
	// near the click x.
	cx, cy := a.placeContext(28, 5, 3)
	if cx >= 28 {
		t.Fatalf("expected flip left when near right edge: cx=%d", cx)
	}
	if cy != 5 {
		t.Fatalf("y unchanged at top half: cy=%d", cy)
	}

	// Click near the bottom → expect cy to flip up.
	cx, cy = a.placeContext(5, 19, 5)
	if cy >= 19 {
		t.Fatalf("expected flip up when near bottom: cy=%d", cy)
	}
	_ = cx

	// Click out at -10,-10 → expect clamp to (0,0).
	cx, cy = a.placeContext(-10, -10, 3)
	if cx != 0 || cy != 0 {
		t.Fatalf("expected clamp (0,0); got (%d,%d)", cx, cy)
	}
}

// TestContextActivate runs the highlighted item's action against the node
// the menu was opened for, and closes all modals first.
func TestContextActivate(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := 0
	var seenNode *filetree.Node
	a.contextOpen = true
	a.contextNode = a.tree.Root
	a.contextItems = []contextItem{
		{label: "x", action: func(_ *App, n *filetree.Node) {
			called++
			seenNode = n
		}},
	}
	a.contextHover = 0
	a.contextActivate()
	if called != 1 {
		t.Fatalf("expected action to fire once, got %d", called)
	}
	if seenNode != a.tree.Root {
		t.Fatal("action did not receive the contextNode")
	}
	if a.contextOpen {
		t.Fatal("contextActivate should close modals")
	}
}

// TestContextActivate_OutOfRangeIsNoop guards against stale events.
func TestContextActivate_OutOfRangeIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.contextHover = 5
	a.contextActivate() // no panic, no state change
	a.contextHover = -1
	a.contextActivate()
}

// TestRuneLen counts visible cells one-per-rune.
func TestRuneLen(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"abc":   3,
		"héllo": 5, // five runes, one cell each by this helper's contract
		"日本":    2,
	}
	for s, want := range cases {
		if got := runeLen(s); got != want {
			t.Errorf("runeLen(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestTrimSpace strips ASCII whitespace from both ends.
func TestTrimSpace(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"   ":         "",
		"abc":         "abc",
		"  abc  ":     "abc",
		"\t\nabc\r\n": "abc",
		"a b c":       "a b c", // interior whitespace untouched
	}
	for in, want := range cases {
		if got := trimSpace(in); got != want {
			t.Errorf("trimSpace(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestContextRect returns origin + width + count-derived height.
func TestContextRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.contextX, a.contextY = 10, 5
	a.contextItems = []contextItem{{label: "a"}, {label: "b"}}
	x, y, w, h := a.contextRect()
	if x != 10 || y != 5 || w != contextMenuWidth || h != 4 {
		t.Fatalf("contextRect: (%d,%d,%d,%d)", x, y, w, h)
	}
}

// TestHandleContextKey covers Up/Down clamps, Esc cancels, Enter activates.
func TestHandleContextKey(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := 0
	a.contextOpen = true
	a.contextNode = a.tree.Root
	a.contextItems = []contextItem{
		{label: "one", action: func(*App, *filetree.Node) { called++ }},
		{label: "two", action: func(*App, *filetree.Node) { called += 10 }},
	}
	a.contextHover = 0

	// Up at top — clamp.
	a.handleContextKey(keyEv(tcell.KeyUp, 0))
	if a.contextHover != 0 {
		t.Fatalf("Up clamp: got %d", a.contextHover)
	}
	// Down advances.
	a.handleContextKey(keyEv(tcell.KeyDown, 0))
	if a.contextHover != 1 {
		t.Fatalf("Down: got %d", a.contextHover)
	}
	// Down at bottom — clamp.
	a.handleContextKey(keyEv(tcell.KeyDown, 0))
	if a.contextHover != 1 {
		t.Fatalf("Down clamp: got %d", a.contextHover)
	}
	// Enter activates the second item.
	a.handleContextKey(keyEv(tcell.KeyEnter, 0))
	if called != 10 {
		t.Fatalf("Enter: called=%d", called)
	}
	// Re-open and Esc.
	a.contextOpen = true
	a.handleContextKey(keyEv(tcell.KeyEsc, 0))
	if a.contextOpen {
		t.Fatal("Esc should close")
	}
}

// TestHandleContextMouse_HoverAndClick verifies hover updates and click
// activates; outside click dismisses.
func TestHandleContextMouse_HoverAndClick(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := 0
	a.contextOpen = true
	a.contextX, a.contextY = 5, 5
	a.contextNode = a.tree.Root
	a.contextItems = []contextItem{
		{label: "one", action: func(*App, *filetree.Node) { called++ }},
		{label: "two", action: func(*App, *filetree.Node) { called += 10 }},
	}
	a.contextHover = 0

	// Hover row index 1 (relY=2 inside box at y=5 → screen y=7).
	a.handleContextMouse(7, 7, 0)
	if a.contextHover != 1 {
		t.Fatalf("hover: got %d", a.contextHover)
	}
	// Click row 0.
	a.contextOpen = true
	a.contextX, a.contextY = 5, 5
	a.contextItems = []contextItem{
		{label: "one", action: func(*App, *filetree.Node) { called++ }},
	}
	a.handleContextMouse(7, 6, tcell.Button1)
	if called == 0 {
		t.Fatal("click on row should activate")
	}

	// Outside click closes.
	a.contextOpen = true
	a.contextItems = []contextItem{{label: "x"}}
	a.handleContextMouse(0, 0, tcell.Button1)
	if a.contextOpen {
		t.Fatal("outside click should close")
	}
}

// -- small filesystem helpers used by the context-menu tests above ----------

// mkdir is a thin wrapper around os.Mkdir so the test bodies stay readable.
func mkdir(path string) error { return os.Mkdir(path, 0755) }

// writeFile writes payload to path. Wraps os.WriteFile to keep call sites
// terse.
func writeFile(path, payload string) error {
	return os.WriteFile(path, []byte(payload), 0644)
}

// -----------------------------------------------------------------------------
// Save / Discard / Cancel modal (unsaved-changes prompt)
// -----------------------------------------------------------------------------

// seedDirtyApp opens a tab and immediately marks it dirty so each test
// can drive the close / quit flow without re-typing the same fixture.
func seedDirtyApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().InsertString(" edit") // dirty
	return a
}

// TestOpenDirtyClose_DefaultsToCancel pins down the "an accidental
// Enter never loses work" property: focus lands on Cancel (idx 0) when
// the modal opens, so a stray Enter just dismisses.
func TestOpenDirtyClose_DefaultsToCancel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDirtyClose("T", "M", func(*App) {}, func(*App) {})
	if !dirtyIsOpen(a) {
		t.Fatal("modal should open")
	}
	if dirtyPrefab(t, a).Hover != 0 {
		t.Fatalf("default focus should be Cancel (0), got %d", dirtyPrefab(t, a).Hover)
	}
}

// TestRequestCloseTab_DirtySaveClosesTab walks the Save path: opening
// the modal, picking Save should both write the file (dirty flag clears)
// and close the tab.
func TestRequestCloseTab_DirtySaveClosesTab(t *testing.T) {
	a := seedDirtyApp(t)
	target := a.tabs[0].Path

	a.requestCloseTab(0)
	if !dirtyIsOpen(a) {
		t.Fatal("dirty close should open the modal")
	}
	dirtyChoose(t, a, 2)

	if len(a.tabs) != 0 {
		t.Fatalf("Save should also close the tab; %d tabs left", len(a.tabs))
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if string(got) != " editseed" {
		t.Fatalf("unexpected file contents after save: %q", got)
	}
}

// TestRequestCloseTab_DirtyDiscardClosesWithoutSaving is the
// counterpart: Discard should drop the tab without touching disk.
func TestRequestCloseTab_DirtyDiscardClosesWithoutSaving(t *testing.T) {
	a := seedDirtyApp(t)
	target := a.tabs[0].Path

	a.requestCloseTab(0)
	dirtyChoose(t, a, 1)

	if len(a.tabs) != 0 {
		t.Fatal("Discard should close the tab")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "seed" {
		t.Fatalf("Discard should not touch disk; got %q", got)
	}
}

// TestRequestCloseTab_DirtyCancelKeepsTab proves Cancel leaves
// everything alone — the tab stays open, the buffer stays dirty.
func TestRequestCloseTab_DirtyCancelKeepsTab(t *testing.T) {
	a := seedDirtyApp(t)

	a.requestCloseTab(0)
	dirtyChoose(t, a, 0)

	if len(a.tabs) != 1 {
		t.Fatalf("Cancel should keep the tab; got %d", len(a.tabs))
	}
	if !a.activeTabPtr().Dirty {
		t.Fatal("Cancel should not flip the dirty flag")
	}
	if dirtyIsOpen(a) {
		t.Fatal("Cancel should dismiss the modal")
	}
}

// TestMenuQuit_NoDirtyTabsExitsImmediately keeps the fast path: when
// nothing is dirty, the modal must NOT open and a.quit flips on the
// spot.
func TestMenuQuit_NoDirtyTabsExitsImmediately(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.menuQuit()
	if dirtyIsOpen(a) {
		t.Fatal("clean state should skip the modal")
	}
	if !a.quit {
		t.Fatal("clean menuQuit should set quit")
	}
}

// TestMenuQuit_DirtyOpensModal proves a dirty tab blocks the immediate
// exit and routes through the Save / Discard / Cancel modal.
func TestMenuQuit_DirtyOpensModal(t *testing.T) {
	a := seedDirtyApp(t)
	a.menuQuit()
	if a.quit {
		t.Fatal("dirty quit should not exit until the user picks an action")
	}
	if !dirtyIsOpen(a) {
		t.Fatal("dirty quit should open the modal")
	}
}

// TestMenuQuit_DirtySaveSavesAllAndQuits drives the Save path on quit:
// every dirty tab is written and a.quit flips. This is the test that
// would catch a regression where Save quits without writing — losing
// the user's edits.
func TestMenuQuit_DirtySaveSavesAllAndQuits(t *testing.T) {
	a := seedDirtyApp(t)
	target := a.tabs[0].Path

	a.menuQuit()
	dirtyChoose(t, a, 2)

	if !a.quit {
		t.Fatal("Save in quit modal should set quit")
	}
	got, _ := os.ReadFile(target)
	if string(got) != " editseed" {
		t.Fatalf("Save in quit modal should have written; got %q", got)
	}
}

// TestMenuQuit_DirtyDiscardQuitsWithoutSaving proves Discard skips the
// save and exits anyway.
func TestMenuQuit_DirtyDiscardQuitsWithoutSaving(t *testing.T) {
	a := seedDirtyApp(t)
	target := a.tabs[0].Path

	a.menuQuit()
	dirtyChoose(t, a, 1)

	if !a.quit {
		t.Fatal("Discard should still quit")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "seed" {
		t.Fatalf("Discard must not touch disk; got %q", got)
	}
}

// TestSaveAllDirty_SavesEveryTab covers the multi-tab quit path: every
// dirty tab is written; a clean tab is left alone.
func TestSaveAllDirty_SavesEveryTab(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("seed"), 0644); err != nil {
			t.Fatal(err)
		}
		a.openFile(full)
	}
	// Dirty tabs 0 and 2; leave 1 clean.
	a.tabs[0].Buffer.InsertString(a.tabs[0].Cursor, "x")
	a.tabs[0].Dirty = true
	a.tabs[2].Buffer.InsertString(a.tabs[2].Cursor, "y")
	a.tabs[2].Dirty = true

	if !a.saveAllDirty() {
		t.Fatal("expected saveAllDirty to succeed")
	}
	if a.tabs[0].Dirty || a.tabs[2].Dirty {
		t.Fatal("dirty flags should clear after save")
	}
}

// TestDirtyTabCount counts only tabs whose Dirty flag is set.
func TestDirtyTabCount(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	for _, name := range []string{"a.txt", "b.txt"} {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte("seed"), 0644); err != nil {
			t.Fatal(err)
		}
		a.openFile(full)
	}
	if got := a.dirtyTabCount(); got != 0 {
		t.Fatalf("expected 0 dirty, got %d", got)
	}
	a.tabs[0].Dirty = true
	if got := a.dirtyTabCount(); got != 1 {
		t.Fatalf("expected 1 dirty, got %d", got)
	}
} // TestDirtyButtonAtRelX_HitsAndMisses pins the geometry helper so the
// TestCloseAllModals_ClearsReplaceState pins the full find/replace
// teardown. Opening a modal over a find-with-replace used to reset only
// the find fields, leaving the replace row armed (replaceOpen,
// findFocusReplace, stale text) for the next Esc-g and the tab's match
// highlights painted under the modal. closeAllModals must tear down the
// whole family, exactly like closeFind.
func TestCloseAllModals_ClearsReplaceState(t *testing.T) {
	a := seedFindApp(t, "foo bar foo\n")
	a.openFind()
	a.findValue = []rune("foo")
	a.findApplyQuery()
	a.replaceOpen = true
	a.findFocusReplace = true
	a.replaceValue = []rune("qux")
	a.replaceCursor = 3
	a.replaceScroll = 1

	a.closeAllModals()

	if a.replaceOpen || a.findFocusReplace {
		t.Fatal("replace row must not stay armed after closeAllModals")
	}
	if a.replaceValue != nil || a.replaceCursor != 0 || a.replaceScroll != 0 {
		t.Fatalf("replace field state leaked: value=%q cursor=%d scroll=%d",
			string(a.replaceValue), a.replaceCursor, a.replaceScroll)
	}
	if tab := a.activeTabPtr(); tab != nil && tab.FindQuery != "" {
		t.Fatalf("tab find highlights leaked: %q", tab.FindQuery)
	}
}

// TestAnyModalOpen_ListPickIsOverlay pins the list picker's membership:
// it floats over the editor and captures all input, so the auto-scroll
// guard (and any future caller) must see it. Its omission left the
// guard able to keep scrolling under the picker.
func TestAnyModalOpen_ListPickIsOverlay(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openListPick("Pick", []listPickItem{{Label: "one"}}, func(*App, int) {}, nil, nil)
	if !a.anyModalOpen() {
		t.Fatal("list picker is an overlay; anyModalOpen must report it")
	}
}

// TestAnyModalOpen_FindStripIsNotOverlay pins the strip/overlay split
// (ADR-0001): the find bar deliberately passes mouse through so the
// user can keep drag-selecting in the editor while it's open. Counting
// it as a modal silently disabled auto-scroll during exactly those
// drags.
func TestAnyModalOpen_FindStripIsNotOverlay(t *testing.T) {
	a := seedFindApp(t, "hello\n")
	a.openFind()
	if !a.findOpen {
		t.Fatal("precondition: find bar should be open")
	}
	if a.anyModalOpen() {
		t.Fatal("the find bar is a strip, not an overlay; it must not suppress editor input")
	}
}
