// =============================================================================
// File: internal/app/menu_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for menu.go — the action modal's behavior: the type-to-filter
// field, keyboard navigation, hover and click routing, the drill-in
// picks, the scroll window a short terminal needs, and the draw pass.
// The scroll-mapping cases matter most: a click has to hit the row the
// user sees, not the row the unscrolled layout would put there.
//
// The redesigned menu fits an 80×24 split in most states, which is the
// point of it — so the scroll tests deliberately inflate the row count
// with stuffMenu instead of relying on the built-ins overflowing.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/overlay"
)

// stuffMenu appends n custom actions so the menu is guaranteed taller
// than a short terminal. Custom actions are the cheapest honest way to
// grow the row count — they need no tab, no repo and no clipboard, and
// they splice in above Quit so "the last row" stays Quit.
func stuffMenu(a *App, n int) {
	a.customActions = make([]customactions.Action, 0, n)
	for i := range n {
		a.customActions = append(a.customActions, customactions.Action{
			Label:   fmt.Sprintf("Custom action %d", i),
			Command: "true",
		})
	}
}

// openTestFile seeds body into dir/name, opens it as the active tab and
// returns its path — the two-line preamble half these tests need now
// that tab-scoped rows are hidden rather than dimmed.
func openTestFile(t *testing.T, a *App, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(path)
	return path
}

// TestMenuModalRect centers the modal in the window and clamps the origin
// to (0,0) when the window is too small to fit it.
func TestMenuModalRect_Centered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 60
	x, y, w, h := a.menuModalRect()
	_, _, expectedH := a.menuLayout()
	expectedW := a.menuNaturalWidth()
	if w != expectedW || h != expectedH {
		t.Fatalf("modal size: got (%d,%d), want (%d,%d)", w, h, expectedW, expectedH)
	}
	if expectedW < modalWidth {
		t.Fatalf("modal width %d fell below the %d floor", expectedW, modalWidth)
	}
	if x != (a.width-expectedW)/2 || y != (a.height-expectedH)/2 {
		t.Fatalf("modal origin off-center: (%d,%d)", x, y)
	}
}

// TestMenuModalRect_ClampsTinyWindow ensures a window smaller than the
// modal never yields a negative origin, and that the height clamp keeps
// the modal inside the screen instead of letting it overflow (the old
// behavior, which made bottom rows unreachable).
func TestMenuModalRect_ClampsTinyWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 10, 5
	x, y, _, h := a.menuModalRect()
	if x != 0 || y < 0 {
		t.Fatalf("expected non-negative origin at column 0, got (%d,%d)", x, y)
	}
	if h > a.height {
		t.Fatalf("modal height %d overflows the %d-row window", h, a.height)
	}
}

// TestMenuModalRect_OriginStableWhileFiltering pins the anti-jump rule:
// narrowing the list shrinks the modal from the bottom, it does not
// re-center. A frame that hops upward on every keystroke would drag the
// title and the filter caret out from under the user mid-word.
func TestMenuModalRect_OriginStableWhileFiltering(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	x0, y0, _, h0 := a.menuModalRect()

	a.menuFilter.SetText("the") // narrows to "Theme…"
	x1, y1, _, h1 := a.menuModalRect()

	if x1 != x0 || y1 != y0 {
		t.Fatalf("filtering moved the modal origin: (%d,%d) -> (%d,%d)", x0, y0, x1, y1)
	}
	if h1 >= h0 {
		t.Fatalf("filtering should shrink the modal: %d -> %d", h0, h1)
	}
}

// TestMenuMoveSelection_WrapsAroundEnds simulates a small menu with all rows
// enabled to verify wrapping in both directions.
func TestMenuMoveSelection_WrapsAroundEnds(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Open every potential gate: a savable tab + selection + clipboard.
	openTestFile(t, a, a.rootDir, "f.txt", "hello")
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	a.clipBuf = "x"

	// Count the rows currently enabled so we know how many forward
	// steps land us back at the starting row (vs going past it). A
	// hard-coded len breaks every time the menu grows.
	items, _, _ := a.menuLayout()
	enabled := 0
	for _, item := range items {
		if item.enabled(a) {
			enabled++
		}
	}
	if enabled < 2 {
		t.Fatalf("need at least 2 enabled items to test wrap; got %d", enabled)
	}

	// Walk forward exactly `enabled` steps and land on the first row.
	a.hoveredMenuRow = -1
	a.menuMoveSelection(1)
	first := a.hoveredMenuRow
	for i := 1; i < enabled; i++ {
		a.menuMoveSelection(1)
	}
	a.menuMoveSelection(1) // wrap
	if a.hoveredMenuRow != first {
		t.Fatalf("forward wrap: got %d, want %d", a.hoveredMenuRow, first)
	}

	// Same for backward.
	a.hoveredMenuRow = -1
	a.menuMoveSelection(-1)
	last := a.hoveredMenuRow
	for i := 1; i < enabled; i++ {
		a.menuMoveSelection(-1)
	}
	a.menuMoveSelection(-1) // wrap
	if a.hoveredMenuRow != last {
		t.Fatalf("backward wrap: got %d, want %d", a.hoveredMenuRow, last)
	}
}

// TestMenuMoveSelection_SkipsDisabled lands on an enabled row even when
// the initial state (no tab, no clipboard) leaves several rows dimmed.
func TestMenuMoveSelection_SkipsDisabled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.hoveredMenuRow = -1
	a.menuMoveSelection(1)
	if a.hoveredMenuRow < 0 {
		t.Fatal("expected a row to land somewhere")
	}
	idx := a.hoveredMenuRow
	items, _, _ := a.menuLayout()
	if !items[idx].enabled(a) {
		t.Fatalf("landed on disabled row %d", idx)
	}
}

// TestMenuActivate_RunsHovered runs the action attached to the highlighted
// row — here the sidebar toggle, found by its dynamic label.
func TestMenuActivate_RunsHovered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()

	items, _, _ := a.menuLayout()
	a.hoveredMenuRow = -1
	for i, item := range items {
		if l := a.menuLabel(item); l == "Show file explorer" || l == "Hide file explorer" {
			a.hoveredMenuRow = i
			break
		}
	}
	if a.hoveredMenuRow < 0 {
		t.Fatal("could not find sidebar-toggle row")
	}
	before := a.sidebarShown
	a.menuActivate()
	if a.sidebarShown == before {
		t.Fatal("expected sidebarShown to flip after menuActivate")
	}
}

// TestMenuActivate_OutOfRange pins both guards on menuActivate: an index
// that is not a row, and a row this session has dimmed, must run nothing
// at all. hoveredMenuRow is set by hover math and by a layout that
// shrinks under the filter, so a stale or dimmed index is ordinary — and
// every menu action closes the menu on its way out, which makes "the
// menu is still up and nothing else moved" the whole contract.
func TestMenuActivate_OutOfRange(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "t.txt", "hello\n")
	a.openMenu()

	type snapshot struct {
		menuOpen     bool
		sidebarShown bool
		quit         bool
		statusMsg    string
		tabs         int
	}
	take := func() snapshot {
		return snapshot{a.menuOpen, a.sidebarShown, a.quit, a.statusMsg, a.tabs.Len()}
	}
	before := take()
	if !before.menuOpen {
		t.Fatal("precondition: openMenu should have left the menu up")
	}

	for _, row := range []int{-1, 999} {
		a.hoveredMenuRow = row
		a.menuActivate()
		if got := take(); got != before {
			t.Fatalf("menuActivate at row %d changed the app: got %+v, want %+v", row, got, before)
		}
	}

	// The dimmed half: a freshly opened, clean buffer leaves Undo, Paste
	// and friends visible but inapplicable, and Enter on one of those
	// must be as inert as Enter on nothing.
	items, _, _ := a.menuLayout()
	dimmed := -1
	for i, it := range items {
		if !it.enabled(a) {
			dimmed = i
			break
		}
	}
	if dimmed < 0 {
		t.Fatalf("fixture: expected at least one dimmed row, got %v", menuLabels(a, items))
	}
	a.hoveredMenuRow = dimmed
	a.menuActivate()
	if got := take(); got != before {
		t.Fatalf("menuActivate on dimmed row %q changed the app: got %+v, want %+v",
			a.menuLabel(items[dimmed]), got, before)
	}
}

// TestUpdateMenuHover snaps to the right row when over an enabled row, and
// to -1 when outside.
func TestUpdateMenuHover(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()

	// Find an always-enabled row and hover its relY.
	items, _, _ := a.menuLayout()
	var pickIdx, pickRelY int
	for i, item := range items {
		if item.enabled(a) {
			pickIdx = i
			pickRelY = item.relY
			break
		}
	}
	a.updateMenuHover(mx+5, my+pickRelY)
	if a.hoveredMenuRow != pickIdx {
		t.Fatalf("hoveredMenuRow: got %d, want %d", a.hoveredMenuRow, pickIdx)
	}

	// The filter row is chrome, never a hoverable action.
	a.updateMenuHover(mx+5, my+menuFilterY)
	if a.hoveredMenuRow != -1 {
		t.Fatalf("filter row should not hover a action row, got %d", a.hoveredMenuRow)
	}

	// Outside the modal → -1.
	a.updateMenuHover(0, 0)
	if a.hoveredMenuRow != -1 {
		t.Fatalf("outside modal: got %d", a.hoveredMenuRow)
	}
}

// TestHandleMenuMouse_ClicksRowAndOutside both fires the row action and
// dismisses on outside click — the mouse-first path the whole editor is
// built around, unchanged by the filter.
func TestHandleMenuMouse_ClicksRowAndOutside(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 60
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()
	// Click on the sidebar toggle row — flips the sidebar.
	items, _, _ := a.menuLayout()
	toggleRelY := -1
	for _, item := range items {
		if a.menuLabel(item) == "Hide file explorer" {
			toggleRelY = item.relY
			break
		}
	}
	if toggleRelY < 0 {
		t.Fatal("sidebar toggle row not found")
	}
	before := a.sidebarShown
	a.handleMenuMouse(mx+5, my+toggleRelY, tcell.Button1)
	if a.sidebarShown == before {
		t.Fatal("expected toggle to fire")
	}

	// Click outside — closes.
	a.openMenu()
	a.handleMenuMouse(0, 0, tcell.Button1)
	if a.menuOpen {
		t.Fatal("outside click should close menu")
	}
}

// TestHandleMenuMouse_ClickRunsFilteredRow keeps the mouse honest while
// a query is up: the row under the pointer is a row of the MATCH list,
// so the click must run that action and not whatever occupied the same
// screen line before filtering.
func TestHandleMenuMouse_ClickRunsFilteredRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.menuFilter.SetText("quit")
	a.menuFilterChanged()

	items, _, _ := a.menuLayout()
	if len(items) != 1 || items[0].label != "Quit editor" {
		t.Fatalf("filter 'quit' matched %v, want just Quit editor", items)
	}
	mx, my, _, _ := a.menuModalRect()
	a.handleMenuMouse(mx+5, my+items[0].relY, tcell.Button1)
	if !a.quit {
		t.Fatal("clicking the single filtered row should have run Quit")
	}
}

// TestHandleMenuMouse_NoButtonIsNoop ignores motion-only events.
func TestHandleMenuMouse_NoButtonIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.handleMenuMouse(0, 0, 0)
	if !a.menuOpen {
		t.Fatal("motion-only event should not close menu")
	}
}

// TestMenuFilter_NarrowsAndEnterRunsBestMatch is the headline behavior:
// typing narrows the rows across every group, the highlight snaps to the
// best-ranked match, and Enter runs it without an arrow key.
func TestMenuFilter_NarrowsAndEnterRunsBestMatch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()

	for _, r := range "theme" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	items, _, _ := a.menuLayout()
	if len(items) != 1 || a.menuLabel(items[0]) != "Theme…" {
		t.Fatalf("typing 'theme' matched %v, want just Theme…", menuLabels(a, items))
	}
	if a.hoveredMenuRow != 0 {
		t.Fatalf("the single match should be highlighted, got row %d", a.hoveredMenuRow)
	}

	a.handleMenuKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if !pickIsOpen(a) {
		t.Fatalf("Enter should have run Theme… and opened the theme picker; top = %T", a.overlays.Top())
	}
}

// TestMenuFilter_BestMatchBeatsMenuOrder pins the ranking tie-break: a
// word-prefix hit wins over a row that merely contains the query and
// happens to sit higher in the table, so Enter runs what the user meant.
//
// "in" is the query where the tiers and the table disagree. Edit's
// "Toggle line comment" carries it mid-word (rank 2) and comes first;
// Go's "Find in file" has it at a word start (rank 1) and comes later,
// so rank has to override position. The winner is spelled out by hand
// on purpose — an expectation recomputed from menuMatchRank would stay
// green if the tiers were swapped, which is the regression this guards.
func TestMenuFilter_BestMatchBeatsMenuOrder(t *testing.T) {
	const (
		winner          = "Find in file"        // rank 1, lower in the table
		higherButLooser = "Toggle line comment" // rank 2, higher in the table
	)

	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "main.go", "package main\n")
	a.openMenu()

	for _, r := range "in" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	items, _, _ := a.menuLayout()

	// Both rows have to be live candidates or the comparison below is
	// theatre: a hidden or dimmed loser can't lose.
	live := make(map[string]bool, len(items))
	for _, it := range items {
		if it.enabled(a) {
			live[a.menuLabel(it)] = true
		}
	}
	for _, want := range []string{winner, higherButLooser} {
		if !live[want] {
			t.Fatalf("fixture: %q must be an enabled match for 'in'; matches = %v", want, menuLabels(a, items))
		}
	}

	if a.hoveredMenuRow < 0 || a.hoveredMenuRow >= len(items) {
		t.Fatalf("filter 'in' left the selection at row %d of %d matches %v",
			a.hoveredMenuRow, len(items), menuLabels(a, items))
	}
	switch got := a.menuLabel(items[a.hoveredMenuRow]); got {
	case winner:
		// The row the ranking must pick.
	case higherButLooser:
		t.Fatalf("menu order beat the rank: selected %q, want the better-ranked %q", got, winner)
	default:
		t.Fatalf("filter 'in' selected %q, want %q; matches = %v", got, winner, menuLabels(a, items))
	}
}

// TestMenuFilter_SubsequenceFindsDemotedDoor pins the loose match that
// makes the palette worth having: "fcb" with no substring anywhere still
// finds "File clipboard…".
func TestMenuFilter_SubsequenceFindsDemotedDoor(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "a.txt", "x")
	a.openMenu()

	for _, r := range "fcb" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if _, ok := menuItemByLabelOK(a, "File clipboard…"); !ok {
		items, _, _ := a.menuLayout()
		t.Fatalf("subsequence 'fcb' should find File clipboard…, got %v", menuLabels(a, items))
	}
}

// TestMenuFilter_ClearRestoresFullList checks the round trip: whatever
// the filter hid comes back the moment the query empties, with the
// highlight parked back on the first enabled row.
func TestMenuFilter_ClearRestoresFullList(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	full, _, fullH := a.menuLayout()

	for _, r := range "theme" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if narrowed, _, _ := a.menuLayout(); len(narrowed) >= len(full) {
		t.Fatalf("filter did not narrow: %d rows before, %d after", len(full), len(narrowed))
	}

	for range "theme" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))
	}
	restored, _, restoredH := a.menuLayout()
	if len(restored) != len(full) || restoredH != fullH {
		t.Fatalf("clearing the filter restored %d rows / height %d, want %d / %d",
			len(restored), restoredH, len(full), fullH)
	}
	if a.hoveredMenuRow != 0 {
		t.Fatalf("highlight should return to the first enabled row, got %d", a.hoveredMenuRow)
	}
}

// TestMenuFilter_EscClearsThenCloses pins Esc unwinding one layer at a
// time: the first press throws away a typed query (a typo shouldn't cost
// the whole menu), the second dismisses the menu.
func TestMenuFilter_EscClearsThenCloses(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	for _, r := range "quit" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if a.menuFilter.Text() != "quit" {
		t.Fatalf("filter = %q, want %q", a.menuFilter.Text(), "quit")
	}

	a.handleMenuKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if !a.menuOpen {
		t.Fatal("the first Esc must clear the filter, not close the menu")
	}
	if a.menuFilter.Text() != "" {
		t.Fatalf("filter should be empty after Esc, got %q", a.menuFilter.Text())
	}

	a.handleMenuKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.menuOpen || a.overlays.IsOpen() {
		t.Fatal("the second Esc must close the menu")
	}
}

// TestMenuFilter_ResetOnReopen makes sure a query never outlives one
// showing of the menu — reopening always starts from the full list.
func TestMenuFilter_ResetOnReopen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.menuFilter.SetText("theme")
	a.menuFilterChanged()
	a.closeMenu()
	if a.menuFilter.Text() != "" {
		t.Fatalf("closeMenu left the filter at %q", a.menuFilter.Text())
	}
	a.openMenu()
	if a.menuFilter.Text() != "" {
		t.Fatalf("openMenu started with a stale filter %q", a.menuFilter.Text())
	}
}

// TestMenuFilter_ArrowsWalkTheMatchSet pins that Up/Down still navigate
// while a query is up — they walk the filtered rows, not the full table.
func TestMenuFilter_ArrowsWalkTheMatchSet(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "main.go", "package main\n")
	a.openMenu()
	for _, r := range "file" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	items, _, _ := a.menuLayout()
	if len(items) < 2 {
		t.Fatalf("need several matches for 'file', got %v", menuLabels(a, items))
	}
	first := a.hoveredMenuRow
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.hoveredMenuRow == first {
		t.Fatal("Down should move the highlight inside the match set")
	}
	if a.hoveredMenuRow >= len(items) {
		t.Fatalf("highlight %d escaped the %d-row match set", a.hoveredMenuRow, len(items))
	}
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if a.hoveredMenuRow != first {
		t.Fatalf("Up should return to %d, got %d", first, a.hoveredMenuRow)
	}
}

// TestMenuDrillIn_FileClipboardHasEveryDemotedAction is the drill-in
// contract from the user's side: opening "File clipboard…" must produce
// a pick holding every action the top level demoted into it.
func TestMenuDrillIn_FileClipboardHasEveryDemotedAction(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	path := openTestFile(t, a, dir, "a.txt", "x")
	a.clipCopyPath(path) // arms the paste row too

	a.openMenu()
	row, ok := menuItemByLabelOK(a, "File clipboard…")
	if !ok {
		t.Fatal("File clipboard… row missing with a file tab open")
	}
	row.action(a)

	pick := pickPrefab(t, a)
	if pick.Title != "File clipboard" {
		t.Errorf("pick title = %q, want File clipboard", pick.Title)
	}
	got := make(map[string]bool, len(pick.Items))
	for _, it := range pick.Items {
		got[it.Label] = true
	}
	for _, want := range []string{"Cut file", "Copy file", "Duplicate file", "Copy relative path", "Copy absolute path"} {
		if !got[want] {
			t.Errorf("drill-in is missing %q; has %v", want, pick.Items)
		}
	}
	pasteFound := false
	for _, it := range pick.Items {
		if strings.HasPrefix(it.Label, "Paste into ") {
			pasteFound = true
		}
	}
	if !pasteFound {
		t.Errorf("drill-in is missing the paste row; has %v", pick.Items)
	}
	if a.menuOpen {
		t.Error("the drill-in replaces the menu; menuOpen should be false")
	}
}

// TestMenuDrillIn_RunsThePickedAction closes the loop: choosing a row in
// the drill-in must run that row's action, not just close the pick.
func TestMenuDrillIn_RunsThePickedAction(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	path := openTestFile(t, a, dir, "a.txt", "x")

	a.openMenu()
	row, ok := menuItemByLabelOK(a, "File clipboard…")
	if !ok {
		t.Fatal("File clipboard… row missing with a file tab open")
	}
	row.action(a)

	pick := pickPrefab(t, a)
	idx := -1
	for i, it := range pick.Items {
		if it.Label == "Copy file" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("Copy file missing from the drill-in: %v", pick.Items)
	}
	pick.Selected = idx
	pick.Confirm()
	if a.fileClipPath != path {
		t.Fatalf("picking Copy file should have loaded the file clipboard, got %q", a.fileClipPath)
	}
}

// TestMenuDrillIn_GitPickHoldsEveryVerb runs the git drill-in against a
// real repo with a dirty tracked file open, which is the state that
// makes all nine demoted verbs applicable at once.
func TestMenuDrillIn_GitPickHoldsEveryVerb(t *testing.T) {
	dir := initRepoWithCommit(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\nthere\n"), 0644); err != nil {
		t.Fatalf("dirty the tracked file: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "f.txt"))
	a.refreshGitStatus() // synchronous: fills DirtyFiles + the tab's gutter lines

	a.openMenu()
	row, ok := menuItemByLabelOK(a, "Git…")
	if !ok {
		t.Fatal("Git… row missing in a repo")
	}
	row.action(a)

	pick := pickPrefab(t, a)
	got := make(map[string]bool, len(pick.Items))
	for _, it := range pick.Items {
		got[it.Label] = true
	}
	for _, want := range []string{
		"Git changes", "Commit changes…", "Push", "Pull", "Switch branch…",
		"Diff this file", "History of this file", "Commit history", "More git actions…",
	} {
		if !got[want] {
			t.Errorf("git drill-in is missing %q; has %v", want, pick.Items)
		}
	}
	// The Esc-leader hint travels into the pick's tag column so the
	// menu keeps teaching the shortcut it used to print inline.
	for _, it := range pick.Items {
		if it.Label == "Git changes" && it.Tag != "Esc g" {
			t.Errorf("Git changes tag = %q, want Esc g", it.Tag)
		}
	}
}

// TestMenuDrillIn_OmitsInapplicableVerbs pins why a pick filters rather
// than dims: with no repo and no tab there is nothing to show, and with
// a repo but a clean tree the commit row stays out of the way.
func TestMenuDrillIn_OmitsInapplicableVerbs(t *testing.T) {
	dir := initRepoWithCommit(t)
	a := newTestApp(t, dir)
	a.openMenu()
	row, ok := menuItemByLabelOK(a, "Git…")
	if !ok {
		t.Fatal("Git… row missing in a repo")
	}
	row.action(a)

	pick := pickPrefab(t, a)
	for _, it := range pick.Items {
		switch it.Label {
		case "Commit changes…":
			t.Error("a clean tree has nothing to commit; row should be omitted")
		case "Diff this file", "History of this file":
			t.Errorf("%q needs an open tab; row should be omitted", it.Label)
		}
	}
}

// TestOpenMenuDrillIn_EmptyFlashesInsteadOfOpening covers the defensive
// branch: predicates and contents can drift, and an empty frame is worse
// than a sentence in the status bar.
func TestOpenMenuDrillIn_EmptyFlashesInsteadOfOpening(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.openMenuDrillIn(menuDrillIn{title: "Git", items: []menuItemDef{
		{label: "Nope", action: func(*App) {}, enabled: func(*App) bool { return false }},
	}})
	if pickIsOpen(a) {
		t.Fatal("an all-disabled drill-in must not open an empty pick")
	}
	if !strings.Contains(a.statusMsg, "git actions") {
		t.Fatalf("expected a flash explaining the empty drill-in, got %q", a.statusMsg)
	}
}

// TestDrawMenu_RightAlignsShortcuts verifies the shortcut column is painted at
// the modal's right edge instead of being appended to the command label.
func TestDrawMenu_RightAlignsShortcuts(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "a.txt", "x")
	a.drawMenu()
	a.screen.Show()

	mx, my, mw, _ := a.menuModalRect()
	save, ok := menuItemByLabelOK(a, "Save")
	if !ok {
		t.Fatal("Save row missing with a file tab open")
	}
	shortcutX := mx + mw - 2 - runeLen(save.shortcut)
	line := screenLine(a.screen.(tcell.SimulationScreen), my+save.relY)
	lineRunes := []rune(line)
	if got := string(lineRunes[shortcutX : shortcutX+runeLen(save.shortcut)]); got != save.shortcut {
		t.Fatalf("right shortcut = %q, want %q on line %q", got, save.shortcut, line)
	}
	if strings.Contains(string(lineRunes[mx+4:shortcutX-1]), save.shortcut) {
		t.Fatalf("shortcut should not be appended to label area: %q", line)
	}
}

// TestDrawMenu_FilterFieldAndPlaceholder pins the affordance that tells
// the user they can type: an empty filter shows its placeholder, a typed
// one shows the query, and a query that matches nothing says so instead
// of drawing an empty box.
func TestDrawMenu_FilterFieldAndPlaceholder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.drawMenu()
	a.screen.Show()
	if !screenHasText(t, a, "type to filter actions…") {
		t.Fatal("an empty filter should show its placeholder")
	}

	a.menuFilter.SetText("quit")
	a.menuFilterChanged()
	a.drawMenu()
	a.screen.Show()
	if screenHasText(t, a, "type to filter actions…") {
		t.Fatal("the placeholder should give way to the typed query")
	}
	if !screenHasText(t, a, "quit") {
		t.Fatal("the typed query should be painted in the filter field")
	}

	a.menuFilter.SetText("zzzz")
	a.menuFilterChanged()
	a.drawMenu()
	a.screen.Show()
	if !screenHasText(t, a, "no matches") {
		t.Fatal("a query that matches nothing should say so")
	}
}

// TestMenuModalRect_ClampsToShortTerminal is the geometry half of the
// clipped-menu regression: at the app's declared 80×24 minimum an
// overflowing modal must fit the screen (with a one-row margin) and
// report the overflow as scroll rather than rendering off the bottom.
func TestMenuModalRect_ClampsToShortTerminal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	stuffMenu(a, 20)
	resizeTestApp(t, a, 80, 24)
	_, _, _, h := a.menuModalRect()
	if h > a.height-2 {
		t.Fatalf("modal height %d exceeds screen height-2 (%d)", h, a.height-2)
	}
	if a.menuMaxScroll() <= 0 {
		t.Fatal("expected the clamped menu to report scrollable overflow")
	}
}

// preRedesignMenuHeight is what the old flat list measured: 42 rows in
// eight groups, 54 cells tall, in every state. It's the yardstick the
// regroup has to beat.
const preRedesignMenuHeight = 54

// TestMenuLayout_ShortTerminalNeedsNoScrollToReachAnAction is the
// redesign's real acceptance criterion. An 80×24 tmux split clamps the
// modal to 22 cells, 5 of which are chrome — 17 content rows for ~28
// applicable actions, so "every action visible at once" is arithmetic
// nobody can win without burying Copy / Paste / Rename behind a third
// level. What the redesign promises instead, and what this pins:
//
//  1. an empty editor's menu fits outright, no chevrons at all;
//  2. with a file open the modal is far shorter than the old flat list;
//  3. and for EVERY top-level row, typing that row's label leaves the
//     modal at zero scroll with the row still in it — so no action is
//     ever behind a scrollbar, which is what "the menu replaces hot-key
//     archaeology" actually requires.
func TestMenuLayout_ShortTerminalNeedsNoScrollToReachAnAction(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	if got := a.menuMaxScroll(); got != 0 {
		_, _, h := a.menuLayout()
		t.Fatalf("empty-editor menu overflows 80×24 by %d rows (height %d)", got, h)
	}

	openTestFile(t, a, dir, "main.go", "package main\n")
	items, _, h := a.menuLayout()
	if h >= preRedesignMenuHeight {
		t.Errorf("one-tab menu is %d cells tall, no better than the %d-cell flat list", h, preRedesignMenuHeight)
	}

	a.openMenu()
	for _, label := range menuLabels(a, items) {
		a.menuFilter.SetText(label)
		a.menuFilterChanged()
		if got := a.menuMaxScroll(); got != 0 {
			t.Errorf("typing %q still leaves %d rows of scroll at 80×24", label, got)
		}
		if _, ok := menuItemByLabelOK(a, label); !ok {
			t.Errorf("typing %q filtered out the row itself", label)
		}
	}
	a.clearMenuFilter()
}

// TestMenuScroll_KeyboardScrollsQuitIntoView is the regression test for
// the P1 "Quit editor is unreachable at 80×24" bug: selecting the last
// row via keyboard must scroll it into the visible region and actually
// paint it. Before the fix the row was drawn past the screen edge and
// silently dropped.
func TestMenuScroll_KeyboardScrollsQuitIntoView(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	stuffMenu(a, 20)
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	a.menuMoveSelection(-1) // wrap from the first row to the last (Quit — always enabled)

	items, _, _ := a.menuLayout()
	if a.hoveredMenuRow != len(items)-1 {
		t.Fatalf("expected wrap-around to select the last row, got %d", a.hoveredMenuRow)
	}

	a.drawMenu()
	a.screen.Show()
	if !screenHasText(t, a, "Quit editor") {
		t.Fatal("Quit editor row should be scrolled into view and drawn at 80×24")
	}
}

// TestMenuMouse_WheelScrollsAndClamps drives the wheel over the open
// menu: down-ticks must advance menuScroll toward menuMaxScroll and
// never past it; up-ticks must clamp back at zero.
func TestMenuMouse_WheelScrollsAndClamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	stuffMenu(a, 20)
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	mx, my, mw, mh := a.menuModalRect()
	cx, cy := mx+mw/2, my+mh/2

	max := a.menuMaxScroll()
	if max <= 0 {
		t.Fatal("test setup needs an overflowing menu")
	}
	for range max + 5 {
		a.handleMenuMouse(cx, cy, tcell.WheelDown)
	}
	if a.menuScroll != max {
		t.Fatalf("wheel-down should clamp at maxScroll %d, got %d", max, a.menuScroll)
	}
	for range max + 5 {
		a.handleMenuMouse(cx, cy, tcell.WheelUp)
	}
	if a.menuScroll != 0 {
		t.Fatalf("wheel-up should clamp at 0, got %d", a.menuScroll)
	}
}

// TestMenuClick_MapsThroughScroll pins the click hit-test under scroll:
// with the menu scrolled to the bottom, clicking the on-screen row where
// Quit now sits must activate Quit, not whichever row used to own that
// screen line. A click that lands on the bottom border must do nothing.
func TestMenuClick_MapsThroughScroll(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	stuffMenu(a, 20)
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	a.menuScroll = a.menuMaxScroll()
	if a.menuScroll <= 0 {
		t.Fatal("test setup needs an overflowing menu")
	}

	items, _, _ := a.menuLayout()
	quit := items[len(items)-1]
	mx, my, _, mh := a.menuModalRect()

	borderY := my + mh - 1
	a.handleMenuMouse(mx+2, borderY, tcell.Button1)
	if a.quit {
		t.Fatal("clicking the bottom border must not activate a hidden row")
	}

	quitY := my + quit.relY - a.menuScroll
	if quitY < my+menuContentY || quitY > my+mh-2 {
		t.Fatalf("test setup: Quit row not in visible region (y=%d)", quitY)
	}
	a.handleMenuMouse(mx+2, quitY, tcell.Button1)
	if !a.quit {
		t.Fatal("clicking the scrolled Quit row should quit the editor")
	}
}

// TestDrawMenu_HoveredShortcutUsesTextFg pins the hover-row contrast
// fix: the shortcut hint on the highlighted row used to render in Muted
// on the Selection background (~2.6:1, illegible on the very row the
// user is reading). It must use the Text foreground instead.
func TestDrawMenu_HoveredShortcutUsesTextFg(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.menuMoveSelection(-1) // wrap to the last row: Quit editor, "Esc q"

	a.drawMenu()
	a.screen.Show()

	items, _, _ := a.menuLayout()
	quit := items[len(items)-1]
	mx, my, mw, _ := a.menuModalRect()
	shortcutX := mx + mw - 2 - runeLen(quit.shortcut)
	cy := my + quit.relY - a.menuScroll

	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	fg, bg, _ := cells[cy*w+shortcutX].Style.Decompose()
	if bg != a.theme.Selection {
		t.Fatalf("expected hover bg under the shortcut, got %v", bg)
	}
	if fg != a.theme.Text {
		t.Fatalf("hovered shortcut fg: got %v, want Text %v", fg, a.theme.Text)
	}
}

// TestMenuWheel_RecomputesHoverAfterScroll pins the hover highlight to
// the row actually under the pointer: a wheel tick used to scroll the
// menu after hover was computed, leaving the highlight one row stale
// until the next mouse motion.
func TestMenuWheel_RecomputesHoverAfterScroll(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	stuffMenu(a, 20)
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(120, 24) // short terminal so the menu overflows and scrolls
	a.width, a.height = scr.Size()
	openTestFile(t, a, dir, "a.go", "package a\n")
	a.openMenu()
	if a.menuMaxScroll() <= 0 {
		t.Fatal("test setup needs an overflowing menu")
	}

	// Park the pointer on an enabled row ("Find in file"), then wheel.
	items, _, _ := a.menuLayout()
	relY := -1
	for _, it := range items {
		if it.label == "Find in file" {
			relY = it.relY
			break
		}
	}
	if relY < 0 {
		t.Fatal("Find in file row not found")
	}
	mx, my, _, _ := a.menuModalRect()
	x, y := mx+6, my+relY
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.WheelDown, 0))
	if a.menuScroll == 0 {
		t.Fatal("wheel should have scrolled the menu")
	}
	got := a.hoveredMenuRow
	a.updateMenuHover(x, y)
	if got != a.hoveredMenuRow {
		t.Fatalf("hover after wheel = %d, recomputed for the same pointer = %d — stale hover", got, a.hoveredMenuRow)
	}
}

// TestMenuFilter_AltRuneStillFiresLeaderAction is the other half of the
// filter-vs-cheat-sheet resolution documented on handleMenuKey: bare
// runes are text, but Alt+<rune> — how tmux delivers a fast "Esc t" —
// must still run the action with the menu up, even mid-query.
func TestMenuFilter_AltRuneStillFiresLeaderAction(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	for _, r := range "xyz" {
		a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if a.menuFilter.Text() != "xyz" {
		t.Fatalf("bare runes should have filled the filter, got %q", a.menuFilter.Text())
	}
	before := a.sidebarShown
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModAlt))
	if a.sidebarShown == before {
		t.Fatal("Alt+t should still fire the sidebar toggle with the menu up")
	}
}

// TestMenuFilter_ArmedLeaderWindowBeatsTheFilter pins the third arm of
// the filter-vs-cheat-sheet resolution: on a terminal that delivers Esc
// and the rune as two separate events, an Esc window armed before the
// menu opened still fires its action instead of typing. That is the
// same precedence handleKey gives the leader over typing into the
// buffer, so the filter is not a special case.
func TestMenuFilter_ArmedLeaderWindowBeatsTheFilter(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.lastEscape = time.Now() // e.g. Esc, then a click on the ≡ button

	before := a.sidebarShown
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone))
	if a.sidebarShown == before {
		t.Fatal("an armed Esc window should fire Esc-t, not type into the filter")
	}
	if got := a.menuFilter.Text(); got != "" {
		t.Fatalf("the consumed rune must not also reach the filter, got %q", got)
	}
}

// TestMenuFilter_IgnoredDuringPaste keeps bracketed paste out of the
// filter: pasted runes must never reach the menu at all, or a paste
// lands in a field the user never focused.
func TestMenuFilter_IgnoredDuringPaste(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.pasting = true
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if a.menuFilter.Text() != "" {
		t.Fatalf("pasted rune reached the filter: %q", a.menuFilter.Text())
	}
}

// TestDrawMenu_ShowsFilterCaret pins the focus affordance: the filter is
// focused from the moment the menu opens, so the terminal caret must sit
// in the field rather than being hidden like every other modal row.
func TestDrawMenu_ShowsFilterCaret(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.menuFilter.SetText("qu")
	a.drawMenu()
	a.screen.Show()

	_, my, _, _ := a.menuModalRect()
	cx, cy, visible := a.screen.(tcell.SimulationScreen).GetCursor()
	if !visible {
		t.Fatal("the focused filter field should show the terminal caret")
	}
	if cy != my+menuFilterY {
		t.Fatalf("caret row = %d, want the filter row %d", cy, my+menuFilterY)
	}
	if cx <= 0 {
		t.Fatalf("caret column = %d, want it inside the field", cx)
	}
}

// TestMenuOverlay_FieldIsTheOverlayPackageOne is a tiny type pin: the
// menu must reuse overlay.Field rather than growing a third hand-rolled
// text input beside the pick's and the prompt's.
func TestMenuOverlay_FieldIsTheOverlayPackageOne(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var f overlay.Field = a.menuFilter
	f.SetText("x")
	if f.Text() != "x" {
		t.Fatal("menuFilter must be an overlay.Field")
	}
}

// longCustomAction is a user-named custom action wide enough that it
// cannot fit the base modal — the actions.json case that motivated the
// grow-or-reveal fix.
const longCustomAction = "Deploy staging, run migrations and tail the deploy log"

// TestMenuModalRect_GrowsForLongLabelClampsToScreen pins both halves of
// the width rule: a wide terminal lets the frame grow until the label
// fits, and a narrow one clamps it inside the screen instead of painting
// past the edge.
func TestMenuModalRect_GrowsForLongLabelClampsToScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = []customactions.Action{{Label: longCustomAction, Command: "true"}}

	resizeTestApp(t, a, 120, 40)
	_, _, wide, _ := a.menuModalRect()
	row := menuItemByLabel(t, a, longCustomAction)
	if menuLabelBudget(wide, row) < runeLen(longCustomAction) {
		t.Fatalf("modal width %d still clips the label on a 120-col terminal", wide)
	}

	resizeTestApp(t, a, 60, 40)
	x, _, narrow, _ := a.menuModalRect()
	if narrow > a.width-2 {
		t.Fatalf("modal width %d overflows a %d-column terminal", narrow, a.width)
	}
	if x+narrow > a.width {
		t.Fatalf("modal spans [%d,%d) past the %d-column screen", x, x+narrow, a.width)
	}
}

// TestDrawMenu_LongLabelNeverPaintsPastTheFrame is the regression test for
// the bleed: a shortcut-less row got no width budget at all and drawAt
// does no bounds checking, so a long custom-action label painted straight
// through the right border and onto the editor underneath.
func TestDrawMenu_LongLabelNeverPaintsPastTheFrame(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 60, 40)
	a.customActions = []customactions.Action{{Label: longCustomAction, Command: "true"}}
	a.openMenu()
	a.draw()
	a.screen.Show()

	mx, my, mw, _ := a.menuModalRect()
	row := menuItemByLabel(t, a, longCustomAction)
	line := []rune(screenLine(a.screen.(tcell.SimulationScreen), my+row.relY))
	// The border cell is the tell: drawAt has no bounds checking, so an
	// unbudgeted label overwrites it on its way onto the editor.
	if got := line[mx+mw-1]; got != '│' {
		t.Fatalf("right border overwritten by the label: %q", got)
	}
	budget := menuLabelBudget(mw, row)
	if budget >= runeLen(longCustomAction) {
		t.Fatalf("precondition: 60 columns should not fit the label (budget %d)", budget)
	}
	want := trimRunes(longCustomAction, budget)
	if got := string(line[mx+4 : mx+4+runeLen(want)]); got != want {
		t.Fatalf("row label = %q, want it clipped to %q", got, want)
	}
	if !strings.HasSuffix(want, "…") {
		t.Fatalf("a clipped label must show it was clipped: %q", want)
	}
}

// TestMenu_ClippedLabelRevealedOnHover is the recourse half of the fix:
// when the terminal is too narrow for the frame to grow, pointing at the
// row flashes the whole label so it is never an unidentifiable prefix. A
// row that fits must stay silent — otherwise every hover would stomp the
// status bar.
func TestMenu_ClippedLabelRevealedOnHover(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 60, 40)
	a.customActions = []customactions.Action{{Label: longCustomAction, Command: "true"}}
	a.openMenu()

	mx, my, _, _ := a.menuModalRect()
	long := menuItemByLabel(t, a, longCustomAction)
	quit := menuItemByLabel(t, a, "Quit editor")

	a.statusMsg = ""
	a.updateMenuHover(mx+5, my+quit.relY)
	if a.statusMsg != "" {
		t.Fatalf("a row that fits must not flash, got %q", a.statusMsg)
	}

	a.updateMenuHover(mx+5, my+long.relY)
	if a.statusMsg != longCustomAction {
		t.Fatalf("hovering the clipped row flashed %q, want the full label", a.statusMsg)
	}

	// Re-entering the same row must not re-arm the flash on every motion
	// event — only a change of row reveals.
	a.statusMsg = ""
	a.updateMenuHover(mx+6, my+long.relY)
	if a.statusMsg != "" {
		t.Fatalf("staying on the row re-flashed: %q", a.statusMsg)
	}
}

// TestMenu_ClippedLabelRevealedByArrowKeys covers the keyboard route to
// the same recourse: skiff is mouse-first but lives over SSH, where there
// may be no mouse to hover with.
func TestMenu_ClippedLabelRevealedByArrowKeys(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 60, 40)
	a.customActions = []customactions.Action{{Label: longCustomAction, Command: "true"}}
	a.openMenu()
	a.menuFilter.SetText("deploy")
	a.menuFilterChanged()

	a.statusMsg = ""
	a.handleMenuKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.statusMsg != longCustomAction {
		t.Fatalf("arrowing onto the clipped row flashed %q, want the full label", a.statusMsg)
	}
}

// TestMenuModalRect_NarrowsBelowModalWidth is the phone case. The frame
// used to refuse to shrink past modalWidth on the theory that content
// which does not fit does not fit either way — but the two outcomes are
// not the same: at skiff's 40-column floor a 48-cell frame hung its right
// border, its whole shortcut column and the ▼ overflow marker off the
// screen. The menu is the only route to every action in a no-Ctrl editor,
// so it narrows instead, down to menuMinFrameWidth and never past it.
func TestMenuModalRect_NarrowsBelowModalWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, minWidth, minHeight)

	x, y, w, h := a.menuModalRect()
	if w >= modalWidth {
		t.Fatalf("frame is %d cells on a %d-column screen; it never narrowed", w, a.width)
	}
	if w != a.width-2 {
		t.Fatalf("frame should take the screen less a column of margin: got %d, want %d", w, a.width-2)
	}
	if w < menuMinFrameWidth {
		t.Fatalf("frame %d fell below the %d readability floor", w, menuMinFrameWidth)
	}
	if x < 0 || x+w > a.width || y < 0 || y+h > a.height {
		t.Fatalf("modal %d,%d %dx%d escapes a %dx%d screen", x, y, w, h, a.width, a.height)
	}

	// Narrowing is only worth doing if the rows stay identifiable: every
	// built-in row must still get a label column it fits in, or an
	// ellipsised prefix is all the menu ever shows.
	items, _, _ := a.menuLayout()
	for _, it := range items {
		if menuLabelBudget(w, it) < 1 {
			t.Fatalf("row %q gets no label column at all in a %d-cell frame", a.menuLabel(it), w)
		}
	}
}

// TestMenuModalRect_KeepsModalWidthWhenItFits is the no-regression half:
// the narrowing must be forced by the terminal, never volunteered, so any
// screen that can hold the frame gets exactly the frame it always got.
func TestMenuModalRect_KeepsModalWidthWhenItFits(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, w := range []int{modalWidth + 2, 80, 120} {
		resizeTestApp(t, a, w, 40)
		if _, _, got, _ := a.menuModalRect(); got != a.menuNaturalWidth() {
			t.Fatalf("%d columns: frame %d, want the natural %d", w, got, a.menuNaturalWidth())
		}
	}
}
