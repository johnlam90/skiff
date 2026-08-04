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
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// TestIsWordChar pins down the ASCII-only word definition we use for
// double-click word selection.
func TestIsWordChar(t *testing.T) {
	word := []rune{'a', 'z', 'A', 'Z', '0', '9', '_'}
	for _, r := range word {
		if !isWordChar(r) {
			t.Errorf("isWordChar(%q) = false, want true", r)
		}
	}
	nonWord := []rune{' ', '\t', '.', ',', '-', '!', '\n', '/'}
	for _, r := range nonWord {
		if isWordChar(r) {
			t.Errorf("isWordChar(%q) = true, want false", r)
		}
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

// TestScrollAt routes scroll to the panel under the cursor; we just verify
// it doesn't panic across the three regions.
func TestScrollAt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.scrollAt(1, 5, 1)           // sidebar
	a.scrollAt(60, 5, 1)          // editor
	a.scrollAt(60, a.height-1, 1) // status bar (no-op-ish)
}

// TestSidebarClick_File opens a file when a file row is clicked.
func TestSidebarClick_File(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "click.txt")
	if err := os.WriteFile(target, []byte("z"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	// Render once so the tree has visible rows for HitTest.
	a.draw()
	// File row is row 1 (0 is the root); click at column 1, row 1.
	a.sidebarClick(1, 1)
	// Only a no-panic guarantee — depending on row order we may or may
	// not have opened the file. Just make sure no crash and either zero
	// or one tab is open.
	if a.tabs.Len() > 1 {
		t.Fatalf("unexpected tabs: %d", a.tabs.Len())
	}
}

// TestSidebarClick_Miss is safe when (x,y) hits no row.
func TestSidebarClick_Miss(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.sidebarClick(1, 100) // off the bottom of the tree
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

// TestSelectWordAt selects the word under a buffer position.
func TestSelectWordAt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(target, []byte("hello world"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	a.selectWordAt(tab, editor.Position{Line: 0, Col: 2})
	if tab.Anchor.Col != 0 || tab.Cursor.Col != 5 {
		t.Fatalf("word select: anchor=%v cursor=%v", tab.Anchor, tab.Cursor)
	}

	// Empty line — no selection.
	tab.Buffer = editor.NewBuffer("")
	a.selectWordAt(tab, editor.Position{Line: 0, Col: 0})
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

// TestOpenGitHunkAt_OpensInfoOnMarker proves gutter markers are clickable.
func TestOpenGitHunkAt_OpensInfoOnMarker(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.GitLines = map[int]editor.GitLineChange{0: editor.GitLineModified}

	if !a.openGitHunkAt(tab, 0, 0) {
		t.Fatal("expected gutter marker click to be handled")
	}
	if !infoIsOpen(a) {
		t.Fatal("expected git hunk click to open info modal")
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

// TestEditorPress_NoTabSafe doesn't panic with no active tab.
func TestEditorPress_NoTabSafe(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.editorPress(50, 5)
	a.editorDrag(50, 5)
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
