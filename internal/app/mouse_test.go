// =============================================================================
// File: internal/app/mouse_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for mouse.go — the mouse dispatcher. Presses, drags, wheels, and
// the panel hit-testers, driven against a tcell.SimulationScreen so the
// routing can be checked without a real tty. The editor is mouse-first, so
// this is the most behavior-dense test file in the package.

package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/git"
)

// TestSelectWordAt_UsesSharedWordPredicate pins the contract that matters
// now that the word definition lives in internal/editor: the span
// double-click produces is exactly a maximal run of editor.IsWordChar
// runes. Caret motion (Alt+Left / Alt+Right, Esc-b / Esc-e) walks by the
// same predicate, so asserting against it here is what keeps the mouse and
// the keyboard from drifting apart. The predicate's own cases are pinned
// in internal/editor/word_test.go.
func TestSelectWordAt_UsesSharedWordPredicate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "w.go")
	if err := os.WriteFile(target, []byte("call(my_arg9, x)\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	a.selectWordAt(tab, editor.Position{Line: 0, Col: 7}) // inside "my_arg9"
	line := tab.Buffer.LineRunes(0)
	start, end := tab.Anchor.Col, tab.Cursor.Col
	if start >= end {
		t.Fatalf("no word selected: anchor=%v cursor=%v", tab.Anchor, tab.Cursor)
	}
	for i := start; i < end; i++ {
		if !editor.IsWordChar(line[i]) {
			t.Errorf("selection covers non-word rune %q at col %d", line[i], i)
		}
	}
	if start > 0 && editor.IsWordChar(line[start-1]) {
		t.Errorf("selection starts mid-word at col %d", start)
	}
	if end < len(line) && editor.IsWordChar(line[end]) {
		t.Errorf("selection ends mid-word at col %d", end)
	}
}

// TestSetActiveFolder writes both the App field and the tree's mirror copy.
func TestSetActiveFolder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.setActiveFolder(sub)
	if a.activeFolder != sub {
		t.Fatalf("activeFolder: got %q, want %q", a.activeFolder, sub)
	}
	if a.tree.ActiveFolder != sub {
		t.Fatalf("tree.ActiveFolder: got %q, want %q", a.tree.ActiveFolder, sub)
	}
}

// TestTabBarClick_OpensMenu clicks the ≡ button cell and verifies the menu
// opens.
func TestTabBarClick_OpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	mx, _, _, _ := a.menuButtonRect()
	a.tabBarClick(mx, 0)
	if !a.menuOpen {
		t.Fatal("clicking ≡ should open menu")
	}
}

// TestTabBarClick_SwitchesTab clicks inside a non-active tab's body and
// verifies activeTab updates.
func TestTabBarClick_SwitchesTab(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt"))
	// b is active. Lay out the tabs and click inside tab 0's body (not the ×).
	a.lastTabRects = a.layoutTabs()
	tabA := a.lastTabRects[0]
	clickX := tabA.X + 1
	if clickX == tabA.CloseX {
		clickX = tabA.X + 2
	}
	a.tabBarClick(clickX, 0)
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("expected activeTab=0, got %d", a.tabs.ActiveIndex())
	}
}

// TestScrollAt pins the wheel's panel routing, which is the whole point
// of the function: a wheel over the sidebar moves the tree and leaves the
// buffer alone, a wheel over the editor moves the buffer and leaves the
// tree alone, and a wheel on the status bar moves neither. Calling all
// three and asserting nothing (the previous shape) passes just as well
// when every region scrolls the same panel.
func TestScrollAt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	// Enough lines that both panels have somewhere to scroll to.
	if err := os.WriteFile(target, []byte(strings.Repeat("line\n", 200)), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := range 40 {
		sib := filepath.Join(dir, "sib"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(sib, []byte("x"), 0644); err != nil {
			t.Fatalf("seed sibling: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.refreshTree()
	a.openFile(target)
	a.draw() // give the tree a visible-row count to clamp against
	tab := a.activeTabPtr()

	// Sidebar: tree moves, buffer does not.
	treeBefore, tabBefore := a.tree.ScrollY, tab.ScrollY
	a.scrollAt(1, 5, 3)
	if a.tree.ScrollY == treeBefore {
		t.Fatalf("wheel over the sidebar left tree.ScrollY at %d", a.tree.ScrollY)
	}
	if tab.ScrollY != tabBefore {
		t.Fatalf("wheel over the sidebar scrolled the buffer to %d", tab.ScrollY)
	}

	// Editor: buffer moves, tree does not.
	treeBefore = a.tree.ScrollY
	a.scrollAt(60, 5, 3)
	if tab.ScrollY == tabBefore {
		t.Fatalf("wheel over the editor left tab.ScrollY at %d", tab.ScrollY)
	}
	if a.tree.ScrollY != treeBefore {
		t.Fatalf("wheel over the editor scrolled the tree to %d", a.tree.ScrollY)
	}

	// Status bar: neither panel moves.
	treeBefore, tabBefore = a.tree.ScrollY, tab.ScrollY
	a.scrollAt(60, a.height-1, 3)
	if a.tree.ScrollY != treeBefore || tab.ScrollY != tabBefore {
		t.Fatalf("wheel on the status bar scrolled something: tree %d→%d, tab %d→%d",
			treeBefore, a.tree.ScrollY, tabBefore, tab.ScrollY)
	}
}

// TestSidebarClick_File pins what the name has always promised: clicking
// a file row opens that file. The previous body clicked row 1 — which is
// the project-name row, not a file — and then asserted only "at most one
// tab", so it passed with nothing opened at all. Row 1 is the root, so
// the first child sits at row 2.
func TestSidebarClick_File(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "click.txt")
	if err := os.WriteFile(target, []byte("z"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	// Render once so the tree has visible rows for HitTest.
	a.draw()

	a.sidebarClick(1, 2)

	if a.tabs.Len() != 1 {
		t.Fatalf("clicking the file row opened %d tabs, want 1", a.tabs.Len())
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != target {
		t.Fatalf("opened tab = %v, want %q", tab, target)
	}
	// The click also moves the "working folder" to the file's parent —
	// that is what makes a following New File land next to it.
	if a.activeFolder != a.rootDir {
		t.Errorf("active folder = %q, want the file's parent %q", a.activeFolder, a.rootDir)
	}
}

// TestSidebarClick_Miss covers a click below the last tree row: it must
// be an inert no-op, not merely non-fatal. Asserting the state is
// untouched is what distinguishes "the hit-test missed" from "the
// hit-test silently fell through to the last row".
func TestSidebarClick_Miss(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("z"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.draw()
	before := a.activeFolder

	a.sidebarClick(1, 100) // off the bottom of the tree

	if a.tabs.Len() != 0 {
		t.Fatalf("a missed click opened %d tabs", a.tabs.Len())
	}
	if a.activeFolder != before {
		t.Fatalf("a missed click moved the active folder to %q", a.activeFolder)
	}
}

// TestSidebarClick_RootRowResetsActiveFolder pins the bug fix:
// clicking the project-name row in the sidebar (y=1) sets the
// active folder back to the project root. Before this fix, once
// the user picked any subfolder there was no path back to root
// short of restarting the editor — every other row in the tree
// only walks "deeper," not "up." Also confirms the click does not
// open a file or toggle any directory's expansion as a side
// effect; it's purely a navigation/state reset.
func TestSidebarClick_RootRowResetsActiveFolder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.draw() // populate t.visible so HitTest works
	a.setActiveFolder(sub)
	if a.activeFolder == a.rootDir {
		t.Fatal("seed broken: active folder should start as subfolder")
	}

	a.sidebarClick(1, 1) // (col=1, row=1) is the project name row

	if a.activeFolder != a.rootDir {
		t.Errorf("active folder = %q, want root %q", a.activeFolder, a.rootDir)
	}
	if a.tabs.Len() != 0 {
		t.Errorf("clicking root opened tabs: %d", a.tabs.Len())
	}
}

// TestSelectWordAt pins both halves of the double-click contract: over a
// word it selects exactly that word, and over a position with no word
// under it (an empty line) it does nothing at all — it must neither
// fabricate a selection nor clobber the one already there. The second
// half used to be a bare call with no assertion, so a SelectWordAt that
// collapsed the caret onto the clicked point passed it.
func TestSelectWordAt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(target, []byte("hello world\n\nagain\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	a.selectWordAt(tab, editor.Position{Line: 0, Col: 2})
	if tab.Anchor.Col != 0 || tab.Cursor.Col != 5 {
		t.Fatalf("word select: anchor=%v cursor=%v", tab.Anchor, tab.Cursor)
	}

	// Line 1 is empty: the click has no word under it, so the existing
	// "hello" selection must survive untouched.
	wantAnchor, wantCursor := tab.Anchor, tab.Cursor
	a.selectWordAt(tab, editor.Position{Line: 1, Col: 0})
	if tab.Anchor != wantAnchor || tab.Cursor != wantCursor {
		t.Fatalf("empty line moved the selection: anchor %v→%v, cursor %v→%v",
			wantAnchor, tab.Anchor, wantCursor, tab.Cursor)
	}
}

// TestEditorPress_PlacesCaret moves the caret to the clicked spot.
func TestEditorPress_PlacesCaret(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(target, []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	ex, ey, _, _ := a.editorRect()
	a.editorPress(ex+2, ey+1)
	tab := a.activeTabPtr()
	if tab.Cursor.Line != 1 {
		t.Fatalf("expected line 1, got %d", tab.Cursor.Line)
	}
}

// TestOpenGitHunkAt_RequestsDiffOffThread proves gutter markers are
// clickable and that the click no longer pays for the git read inline —
// internal/git's ten-second read timeout used to be a ten-second freeze
// on a slow repo. The click returns handled with nothing on screen but a
// "Loading" flash; the diff arrives on the posted event.
func TestOpenGitHunkAt_RequestsDiffOffThread(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.GitLines = map[int]editor.GitLineChange{0: editor.GitLineModified}

	fake := &git.Fake{}
	fake.Script("diff --unified=3 HEAD -- "+target,
		"@@ -1 +1 @@\n-hell\n+hello\n", nil)
	a.gitRunner = fake

	if !a.openGitHunkAt(tab, 0, 0) {
		t.Fatal("expected gutter marker click to be handled")
	}
	if a.anyModalOpen() {
		t.Fatal("the git read is off-thread; the click must not open anything inline")
	}
	pumpDiffLoad(t, a)
	if !diffIsOpen(a) {
		t.Fatal("the posted event should open the hunk diff")
	}
	if body := strings.Join(diffOv(t, a).raw, "\n"); !strings.Contains(body, "+hello") {
		t.Fatalf("scripted hunk missing, got:\n%s", body)
	}
}

// TestOpenGitHunkAt_IgnoresCleanGutter keeps normal cursor placement intact.
func TestOpenGitHunkAt_IgnoresCleanGutter(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab, err := editor.NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if a.openGitHunkAt(tab, 0, 0) {
		t.Fatal("clean gutter should not be handled as a git preview")
	}
}

// TestEditorPress_DoubleClickSelectsWord triggers the word-select path.
func TestEditorPress_DoubleClickSelectsWord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(target, []byte("hello world"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	ex, ey, _, _ := a.editorRect()
	a.editorPress(ex+2, ey)
	a.editorPress(ex+2, ey) // immediately again — double-click within window
	tab := a.activeTabPtr()
	if tab.Anchor.Col == tab.Cursor.Col {
		t.Fatal("expected a word selection after double-click")
	}
}

// TestEditorPress_NoTabSafe covers the startup screen. Not crashing is
// the floor, not the contract: editorPress must report false so the
// caller does not arm editor drag mode over an empty pane — a press
// that "handled" nothing but armed the drag is how stray motion starts
// mutating a selection that does not exist.
func TestEditorPress_NoTabSafe(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.editorPress(50, 5) {
		t.Fatal("editorPress with no active tab must report unhandled")
	}
	a.editorDrag(50, 5)
	if a.tabs.Len() != 0 {
		t.Fatalf("a press on the empty pane created %d tabs", a.tabs.Len())
	}
}

// TestEditorDrag_AutoScroll arms the auto-scroll direction when dragging
// outside the editor's vertical bounds.
func TestEditorDrag_AutoScroll(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "d.txt")
	if err := os.WriteFile(target, []byte("a\nb\nc\nd\ne\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	ex, ey, _, eh := a.editorRect()
	a.editorDrag(ex+1, ey-1) // above editor → auto-scroll up
	if a.autoScrollDir != -1 {
		t.Fatalf("expected autoScrollDir=-1, got %d", a.autoScrollDir)
	}
	a.editorDrag(ex+1, ey+eh+1) // below → auto-scroll down
	if a.autoScrollDir != 1 {
		t.Fatalf("expected autoScrollDir=1, got %d", a.autoScrollDir)
	}
	a.editorDrag(ex+1, ey+1) // inside → stops
	if a.autoScrollDir != 0 {
		t.Fatalf("expected stopped autoScroll, got %d", a.autoScrollDir)
	}
}

// TestHandleMouse_Wheel routes scroll events to the panel under the cursor.
func TestHandleMouse_Wheel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	ev := tcell.NewEventMouse(60, 5, tcell.WheelDown, tcell.ModNone)
	a.handleMouse(ev)
	ev = tcell.NewEventMouse(60, 5, tcell.WheelUp, tcell.ModNone)
	a.handleMouse(ev)
}

// TestHandleMouse_WheelHorizontal confirms WheelLeft / WheelRight events
// shift the active tab's ScrollX. The test opens a tab with a long line,
// fires WheelRight to scroll horizontally, then WheelLeft to walk it
// back to zero.
func TestHandleMouse_WheelHorizontal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 200)+"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no active tab after openFile")
	}
	// Horizontal scrolling only exists with wrap off; this test is about
	// the wheel routing, so run in the mode where it has an effect.
	tab.SetWrap(false)
	// Aim well inside the editor pane (past the sidebar, below the tab bar).
	editorX := a.sidebarW() + 10
	ev := tcell.NewEventMouse(editorX, 5, tcell.WheelRight, tcell.ModNone)
	a.handleMouse(ev)
	if tab.ScrollX == 0 {
		t.Fatalf("WheelRight should advance ScrollX, still 0")
	}
	startX := tab.ScrollX
	ev = tcell.NewEventMouse(editorX, 5, tcell.WheelLeft, tcell.ModNone)
	a.handleMouse(ev)
	if tab.ScrollX >= startX {
		t.Fatalf("WheelLeft should reduce ScrollX, got %d (was %d)", tab.ScrollX, startX)
	}
}

// TestHandleMouse_ShiftWheelScrollsHorizontally confirms that holding
// shift while turning the vertical wheel scrolls the X axis instead —
// this is the path that actually works in most terminals (which never
// emit native WheelLeft/WheelRight). Without shift, the same wheel
// event must scroll vertically; we check both to make sure the modifier
// is what gates the rotation.
func TestHandleMouse_ShiftWheelScrollsHorizontally(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 200)+"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no active tab after openFile")
	}
	// Same rationale as TestHandleMouse_WheelHorizontal: the horizontal
	// route is only observable with wrap off.
	tab.SetWrap(false)
	editorX := a.sidebarW() + 10

	// Shift+WheelDown → horizontal scroll right.
	ev := tcell.NewEventMouse(editorX, 5, tcell.WheelDown, tcell.ModShift)
	a.handleMouse(ev)
	if tab.ScrollX == 0 {
		t.Fatalf("Shift+WheelDown should scroll horizontally, ScrollX still 0")
	}
	if tab.ScrollY != 0 {
		t.Fatalf("Shift+WheelDown should NOT touch ScrollY, got %d", tab.ScrollY)
	}

	// Shift+WheelUp → horizontal scroll left.
	startX := tab.ScrollX
	ev = tcell.NewEventMouse(editorX, 5, tcell.WheelUp, tcell.ModShift)
	a.handleMouse(ev)
	if tab.ScrollX >= startX {
		t.Fatalf("Shift+WheelUp should reduce ScrollX, got %d (was %d)", tab.ScrollX, startX)
	}

	// Unmodified WheelDown still scrolls vertically. Reset the sticky
	// shift state first — within modifierStickyWindow of the previous
	// shift events it'd still register as a shifted wheel.
	tab.ScrollX = 0
	tab.ScrollY = 0
	a.lastShiftAt = time.Time{}
	ev = tcell.NewEventMouse(editorX, 5, tcell.WheelDown, tcell.ModNone)
	a.handleMouse(ev)
	if tab.ScrollY == 0 {
		t.Fatalf("WheelDown without shift should scroll vertically, ScrollY still 0")
	}
	if tab.ScrollX != 0 {
		t.Fatalf("WheelDown without shift should NOT touch ScrollX, got %d", tab.ScrollX)
	}
}

// TestHandleMouse_ShiftStickyForWheel covers the Zellij quirk where
// Shift arrives in a ButtonNone+Shift event right before an unmodified
// WheelDown. We feed that exact sequence and confirm the wheel event is
// treated as horizontal because the sticky-shift window picked it up.
func TestHandleMouse_ShiftStickyForWheel(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 200)+"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	// Horizontal scrolling only exists with wrap off — this test is about
	// the sticky-shift routing, so run it in the mode where the route has
	// a visible effect.
	tab.SetWrap(false)
	editorX := a.sidebarW() + 10

	// First event: ButtonNone with Shift modifier — what Zellij emits
	// when the user holds shift but hasn't moved or wheeled yet.
	ev := tcell.NewEventMouse(editorX, 5, tcell.ButtonNone, tcell.ModShift)
	a.handleMouse(ev)
	// Second event: WheelDown with NO modifier — what arrives milliseconds
	// later. Without the sticky window this would scroll vertically.
	ev = tcell.NewEventMouse(editorX, 5, tcell.WheelDown, tcell.ModNone)
	a.handleMouse(ev)

	if tab.ScrollX == 0 {
		t.Fatalf("expected sticky-shift to route WheelDown to horizontal, ScrollX still 0")
	}
	if tab.ScrollY != 0 {
		t.Fatalf("sticky-shift WheelDown shouldn't touch ScrollY, got %d", tab.ScrollY)
	}
}

// TestHandleMouse_RightClickOpensMenu falls back to the main menu when the
// right-click isn't on a tree row.
func TestHandleMouse_RightClickOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	ev := tcell.NewEventMouse(60, 5, tcell.Button3, tcell.ModNone)
	a.handleMouse(ev)
	if !a.menuOpen {
		t.Fatal("right-click outside tree should open the main menu")
	}
}

// TestHandleMouse_LeftPressInEditor enters editor drag mode.
func TestHandleMouse_LeftPressInEditor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("ab\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	ev := tcell.NewEventMouse(60, 5, tcell.Button1, tcell.ModNone)
	a.handleMouse(ev)
	if a.dragMode != "editor" {
		t.Fatalf("expected dragMode=editor, got %q", a.dragMode)
	}
	// Release.
	ev = tcell.NewEventMouse(60, 5, 0, tcell.ModNone)
	a.handleMouse(ev)
	if a.dragMode != "" {
		t.Fatalf("expected drag cleared on release, got %q", a.dragMode)
	}
}

// TestHandleMouse_SidebarSplitterDrag enters splitter drag and resizes.
func TestHandleMouse_SidebarSplitterDrag(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	splitX := a.splitterX()
	ev := tcell.NewEventMouse(splitX, 5, tcell.Button1, tcell.ModNone)
	a.handleMouse(ev)
	if a.dragMode != "sidebar" {
		t.Fatalf("expected sidebar drag, got %q", a.dragMode)
	}
	// Continue dragging — resizes.
	ev = tcell.NewEventMouse(splitX+5, 5, tcell.Button1, tcell.ModNone)
	a.handleMouse(ev)
}

// TestScrollbarPressScrollsAndDrags pins the mouse path: pressing the
// editor's rightmost column on a long file jumps the scroll and enters
// the "scrollbar" drag mode; dragging keeps following the row; release
// exits the mode.
func TestScrollbarPressScrollsAndDrags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	content := strings.Repeat("line\n", 300)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)

	ex, ey, ew, eh := a.editorRect()
	barX := ex + ew - 1
	// Press near the bottom of the bar.
	a.handleMouse(tcell.NewEventMouse(barX, ey+eh-1, tcell.Button1, 0))
	if a.dragMode != "scrollbar" {
		t.Fatalf("dragMode = %q, want scrollbar", a.dragMode)
	}
	tab := a.activeTabPtr()
	if tab.ScrollY == 0 {
		t.Fatal("bottom press should scroll the tab")
	}
	// Drag back to the top row.
	a.handleMouse(tcell.NewEventMouse(barX, ey, tcell.Button1, 0))
	if tab.ScrollY != 0 {
		t.Fatalf("drag to top should return to 0, got %d", tab.ScrollY)
	}
	// Release exits the mode.
	a.handleMouse(tcell.NewEventMouse(barX, ey, tcell.ButtonNone, 0))
	if a.dragMode != "" {
		t.Fatalf("release should clear dragMode, got %q", a.dragMode)
	}
}

// TestTabBarClick_ClosesViaX clicks the × in a tab and verifies the close
// path runs (clean tab → tab removed).
func TestTabBarClick_ClosesViaX(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.lastTabRects = a.layoutTabs()
	r := a.lastTabRects[0]
	a.tabBarClick(r.CloseX, 0)
	if a.tabs.Len() != 0 {
		t.Fatalf("expected close, got %d tabs", a.tabs.Len())
	}
}

// TestTabBar_ClickMapsThroughScroll pins hit-testing under scroll: the
// stored rects are in screen coordinates, so clicking the visible
// active tab must select it and clicking the ‹ cell must scroll the
// strip instead of activating whatever tab is drawn beneath it.
func TestTabBar_ClickMapsThroughScroll(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	openManyTabs(t, a, dir, 5)

	a.drawTabBar()
	a.screen.Show()

	// Click inside the second-to-last tab's visible rect.
	target := a.tabs.Len() - 2
	var rect tabRect
	found := false
	for _, r := range a.lastTabRects {
		if r.Index == target {
			rect, found = r, true
			break
		}
	}
	if !found {
		t.Fatal("second-to-last tab should have a stored rect")
	}
	a.tabBarClick(rect.X+3, 0)
	if a.tabs.ActiveIndex() != target {
		t.Fatalf("click on scrolled tab selected %d, want %d", a.tabs.ActiveIndex(), target)
	}

	// Clicking the ‹ marker cell scrolls left rather than selecting.
	a.tabScroll = a.maxTabScroll()
	before := a.tabScroll
	stripX, _ := a.tabStripRegion()
	a.tabBarClick(stripX, 0)
	if a.tabScroll >= before {
		t.Fatalf("clicking ‹ should scroll the strip left (scroll %d -> %d)", before, a.tabScroll)
	}
}

// TestDragRelease_CopiesSelection pins select-to-copy, the tmux/herdr
// convention: releasing a mouse drag that produced a selection puts it
// on the clipboard. With mouse reporting on, the terminal/multiplexer
// never has a selection of its own, so Cmd+C at the terminal level has
// nothing to grab — release-to-copy is the mouse-first replacement.
func TestDragRelease_CopiesSelection(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 5}, true) // drag built "hello"
	a.dragMode = "editor"

	a.handleMouse(tcell.NewEventMouse(40, 5, tcell.ButtonNone, 0)) // release

	if a.clipBuf != "hello" {
		t.Fatalf("release should copy the selection, clipBuf = %q", a.clipBuf)
	}
	if a.dragMode != "" {
		t.Fatalf("release should end the drag, dragMode = %q", a.dragMode)
	}
}

// TestDragRelease_NoCopyWithoutSelection proves a plain click (press
// then release with a collapsed selection) leaves the clipboard alone —
// select-to-copy must not clobber it on every caret placement.
func TestDragRelease_NoCopyWithoutSelection(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.clipBuf = "keep me"
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 0, Col: 3}, false)
	a.dragMode = "editor"

	a.handleMouse(tcell.NewEventMouse(40, 5, tcell.ButtonNone, 0))

	if a.clipBuf != "keep me" {
		t.Fatalf("collapsed-selection release must not copy, clipBuf = %q", a.clipBuf)
	}
}

// TestDragRelease_NonEditorDragDoesNotCopy proves finishing a sidebar or
// scrollbar drag never touches the clipboard, even while a text
// selection happens to exist in the active tab.
func TestDragRelease_NonEditorDragDoesNotCopy(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 5}, true)
	a.clipBuf = "keep me"
	a.dragMode = "sidebar"

	a.handleMouse(tcell.NewEventMouse(20, 5, tcell.ButtonNone, 0))

	if a.clipBuf != "keep me" {
		t.Fatalf("sidebar-drag release must not copy, clipBuf = %q", a.clipBuf)
	}
}

// TestHandleMouse_FindBarPressDoesNotArmEditorDrag pins the press-branch
// regression: dragMode used to be set to "editor" for every left press in
// the middle band of the screen, whether or not the press actually landed
// on text. With the find bar open the editor rect shrinks by a row, so the
// bar's own row falls inside that band — editorPress correctly no-ops
// there (the hit-test rejects it), but the drag armed anyway and the next
// motion event dragged out a selection the user never started. The bar
// stays mouse-transparent (ADR-0001); it just must not arm a drag.
func TestHandleMouse_FindBarPressDoesNotArmEditorDrag(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(target, []byte("hello world\nsecond line\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.openFind()

	_, fy, _, _ := a.findBarRect()
	x := a.sidebarW() + 10
	a.handleMouse(tcell.NewEventMouse(x, fy, tcell.Button1, tcell.ModNone))

	if a.dragMode != "" {
		t.Fatalf("press on the find-bar row must not arm a drag, got %q", a.dragMode)
	}

	// The follow-up motion is where the old behavior did its damage.
	a.handleMouse(tcell.NewEventMouse(x+8, fy, tcell.Button1, tcell.ModNone))
	if tab := a.activeTabPtr(); tab.HasSelection() {
		t.Fatalf("sliding along the find bar must not select editor text: anchor=%v cursor=%v",
			tab.Anchor, tab.Cursor)
	}
}

// TestHandleMouse_EmptyEditorPressDoesNotArmEditorDrag covers the other
// half of the same bug: with no tab open there is no caret to place, so a
// press in the empty editor body must leave the drag state alone rather
// than parking a stale "editor" drag that survives until the next release.
func TestHandleMouse_EmptyEditorPressDoesNotArmEditorDrag(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleMouse(tcell.NewEventMouse(a.sidebarW()+10, 5, tcell.Button1, tcell.ModNone))

	if a.dragMode != "" {
		t.Fatalf("press with no open tab must not arm a drag, got %q", a.dragMode)
	}
}

// seedTreeFiles writes n files into dir so the sidebar's listing
// overflows the list area and the tree grows a scrollbar.
func seedTreeFiles(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "f"+strconv.Itoa(100+i)+".txt")
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
}

// barCell returns the rune and foreground painted at (x, y) on the app's
// simulation screen, after a draw.
func barCell(t *testing.T, a *App, x, y int) (rune, tcell.Color) {
	t.Helper()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	c := cells[y*w+x]
	if len(c.Runes) == 0 {
		return ' ', tcell.ColorDefault
	}
	fg, _, _ := c.Style.Decompose()
	return c.Runes[0], fg
}

// TestTreeScrollbarPressScrollsAndDrags: the sidebar's bar is a real
// mouse target. Pressing near its bottom jumps the tree there and enters
// the "treescrollbar" drag mode, dragging keeps the listing glued to the
// pointer's row, and release ends the drag without leaving the tree
// scrolled somewhere the user didn't ask for.
func TestTreeScrollbarPressScrollsAndDrags(t *testing.T) {
	dir := t.TempDir()
	seedTreeFiles(t, dir, 80)
	a := newTestApp(t, dir)
	a.draw() // the real loop always paints before the first event

	_, sy, sw, sh := a.sidebarRect()
	barX := sw - 1

	a.handleMouse(tcell.NewEventMouse(barX, sy+sh-1, tcell.Button1, 0))
	if a.dragMode != "treescrollbar" {
		t.Fatalf("dragMode = %q, want treescrollbar", a.dragMode)
	}
	bottom := a.tree.ScrollY
	if bottom == 0 {
		t.Fatal("pressing the bottom of the bar should scroll the tree")
	}

	// Drag back to the top of the list area.
	a.handleMouse(tcell.NewEventMouse(barX, sy+2, tcell.Button1, 0))
	if a.tree.ScrollY != 0 {
		t.Fatalf("dragging to the top should return to 0, got %d", a.tree.ScrollY)
	}
	// The drag keeps following even once the pointer leaves the column.
	a.handleMouse(tcell.NewEventMouse(barX-6, sy+sh-1, tcell.Button1, 0))
	if a.tree.ScrollY != bottom {
		t.Fatalf("drag off-column should still track the row: got %d, want %d", a.tree.ScrollY, bottom)
	}

	a.handleMouse(tcell.NewEventMouse(barX, sy+sh-1, tcell.ButtonNone, 0))
	if a.dragMode != "" {
		t.Fatalf("release should clear dragMode, got %q", a.dragMode)
	}
	if a.tree.ScrollY != bottom {
		t.Fatalf("release must not move the tree, got %d", a.tree.ScrollY)
	}
}

// TestSidebarThreeWayHitTestAtSameY is the fiddly one: at a single screen
// row the sidebar has three neighbouring targets — the resize splitter,
// the tree's scrollbar one column to its left, and the tree rows to the
// left of that. Each must do its own thing and nothing else.
func TestSidebarThreeWayHitTestAtSameY(t *testing.T) {
	dir := t.TempDir()
	seedTreeFiles(t, dir, 80)
	a := newTestApp(t, dir)
	a.draw()

	splitX := a.splitterX()
	_, sy, sw, sh := a.sidebarRect()
	barX := sw - 1
	rowX := 4
	// Low in the list area: far enough down the bar that a click there
	// has somewhere to scroll to, and still a real tree row.
	y := sy + sh - 4

	if barX != splitX-1 {
		t.Fatalf("bar column %d should sit immediately left of the splitter %d", barX, splitX)
	}

	// 1. The splitter starts a resize and leaves the tree alone.
	before := a.tree.ScrollY
	a.handleMouse(tcell.NewEventMouse(splitX, y, tcell.Button1, 0))
	if a.dragMode != "sidebar" {
		t.Fatalf("splitter press: dragMode = %q, want sidebar", a.dragMode)
	}
	if a.tree.ScrollY != before {
		t.Fatalf("splitter press scrolled the tree to %d", a.tree.ScrollY)
	}
	a.handleMouse(tcell.NewEventMouse(splitX, y, tcell.ButtonNone, 0))

	// 2. The scrollbar scrolls and opens no file.
	openTabs := a.tabs.Len()
	a.handleMouse(tcell.NewEventMouse(barX, y, tcell.Button1, 0))
	if a.dragMode != "treescrollbar" {
		t.Fatalf("bar press: dragMode = %q, want treescrollbar", a.dragMode)
	}
	if a.tree.ScrollY == before {
		t.Fatal("bar press should have scrolled the tree")
	}
	if a.tabs.Len() != openTabs {
		t.Fatal("bar press must not open a tree row")
	}
	a.handleMouse(tcell.NewEventMouse(barX, y, tcell.ButtonNone, 0))

	// 3. A row click opens that file and starts no drag.
	a.tree.ScrollY = 0
	a.draw()
	scrolled := a.tree.ScrollY
	a.handleMouse(tcell.NewEventMouse(rowX, y, tcell.Button1, 0))
	if a.dragMode != "" {
		t.Fatalf("row press: dragMode = %q, want none", a.dragMode)
	}
	if a.tree.ScrollY != scrolled {
		t.Fatalf("row press scrolled the tree to %d", a.tree.ScrollY)
	}
	if a.tabs.Len() != openTabs+1 {
		t.Fatalf("row press should have opened a file, tabs %d → %d", openTabs, a.tabs.Len())
	}
}

// TestTreeScrollbarRightClickIsNotARow: left- and right-click must agree
// about what the bar column is. Right-clicking it opens no tree context
// menu for the row hiding behind the bar.
func TestTreeScrollbarRightClickIsNotARow(t *testing.T) {
	dir := t.TempDir()
	seedTreeFiles(t, dir, 80)
	a := newTestApp(t, dir)
	a.draw()

	_, sy, sw, _ := a.sidebarRect()
	y := sy + 6
	if a.tryTreeContextClick(sw-1, y) {
		t.Fatal("the scrollbar column is not a context-menu target")
	}
	if !a.tryTreeContextClick(4, y) {
		t.Fatal("a real row still opens the context menu")
	}
}

// TestScrollbarThumbBrightensOnDrag pins the app half of the drag
// feedback: pressing a thumb paints it in Accent on the next frame and
// releasing returns it to Muted, for both the editor's bar and the
// tree's. The flag is derived from dragMode at paint time, so this also
// proves a drag can't strand a thumb lit.
func TestScrollbarThumbBrightensOnDrag(t *testing.T) {
	dir := t.TempDir()
	seedTreeFiles(t, dir, 80)
	path := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 300)), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.draw()

	ex, ey, ew, _ := a.editorRect()
	editorBarX := ex + ew - 1
	_, sy, sw, _ := a.sidebarRect()
	treeBarX := sw - 1

	// Editor bar: press the very top row so the thumb is under it.
	a.handleMouse(tcell.NewEventMouse(editorBarX, ey, tcell.Button1, 0))
	if r, fg := barCell(t, a, editorBarX, ey); fg != a.theme.Accent {
		t.Fatalf("dragged editor thumb: rune %q fg %v, want Accent", r, fg)
	}
	a.handleMouse(tcell.NewEventMouse(editorBarX, ey, tcell.ButtonNone, 0))
	if r, fg := barCell(t, a, editorBarX, ey); fg != a.theme.Muted {
		t.Fatalf("released editor thumb: rune %q fg %v, want Muted", r, fg)
	}

	// Tree bar: same gesture, same language.
	a.tree.ScrollY = 0
	a.handleMouse(tcell.NewEventMouse(treeBarX, sy+2, tcell.Button1, 0))
	if r, fg := barCell(t, a, treeBarX, sy+2); fg != a.theme.Accent {
		t.Fatalf("dragged tree thumb: rune %q fg %v, want Accent", r, fg)
	}
	a.handleMouse(tcell.NewEventMouse(treeBarX, sy+2, tcell.ButtonNone, 0))
	if r, fg := barCell(t, a, treeBarX, sy+2); fg != a.theme.Muted {
		t.Fatalf("released tree thumb: rune %q fg %v, want Muted", r, fg)
	}
}
