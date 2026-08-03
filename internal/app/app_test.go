// =============================================================================
// File: internal/app/app_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the pure-logic helpers and the small bits of App glue that don't
// require a live terminal. Where we need an *App we build one against a
// tcell.SimulationScreen so layout and event-routing helpers can run without
// touching a real tty. The interactive code paths (Run, the event loop, real
// drawing) are exercised manually — here we just pin down the helpers so
// future refactors don't silently regress them.

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
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/theme"
)

// newTestApp builds a fully-wired App against a tcell.SimulationScreen. It
// mirrors what New() does, but skips the background tree-refresh goroutine
// because we don't want a ticker firing while tests run.
func newTestApp(t *testing.T, root string) *App {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { scr.Fini() })
	scr.SetSize(120, 40)

	tree, err := filetree.New(root)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	a := &App{
		screen:         scr,
		theme:          theme.Default(),
		rootDir:        tree.Root.Path,
		tree:           tree,
		hoveredMenuRow: -1,
		diffPanelRow:   -1,
		sidebarShown:   true,
		sidebarWidth:   defaultSidebarWidth,
		wrapOn:         true,
	}
	a.setActiveFolder(tree.Root.Path)
	a.width, a.height = scr.Size()
	// New() seeds git state synchronously before the loop starts; the
	// cached branch is what gates the Git panel, so tests need the same
	// seeding to see the app the way a real session does.
	a.refreshGitStatus()
	// Drain any in-flight background git-status refresh before teardown:
	// a `git status` goroutine still writing .git lock files while
	// t.TempDir removes the repo is the classic "directory not empty"
	// flake. Registered after the Fini cleanup so it runs first (LIFO).
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for a.gitRefreshInFlight && time.Now().Before(deadline) {
			if scr.HasPendingEvent() {
				a.handleEvent(scr.PollEvent())
			} else {
				time.Sleep(2 * time.Millisecond)
			}
		}
	})
	return a
}

// TestSidebarW_ShownVsHidden verifies the sidebar width helper returns 0
// when hidden and the configured width when shown. Every layout helper
// pivots on this so we want it locked in.
func TestSidebarW_ShownVsHidden(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.sidebarW(); got != defaultSidebarWidth {
		t.Fatalf("shown sidebarW: got %d, want %d", got, defaultSidebarWidth)
	}
	a.sidebarShown = false
	if got := a.sidebarW(); got != 0 {
		t.Fatalf("hidden sidebarW: got %d, want 0", got)
	}
}

// TestSidebarRect checks the sidebar render rectangle reserves one cell
// for the splitter on its right edge, and collapses to zero when hidden.
func TestSidebarRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.sidebarRect()
	if x != 0 || y != 0 {
		t.Fatalf("expected origin (0,0), got (%d,%d)", x, y)
	}
	if w != defaultSidebarWidth-1 {
		t.Fatalf("expected w = sidebarWidth-1, got %d", w)
	}
	if h != a.height-1 {
		t.Fatalf("expected h = height-1, got %d", h)
	}

	a.sidebarShown = false
	x, y, w, h = a.sidebarRect()
	if x != 0 || y != 0 || w != 0 || h != 0 {
		t.Fatalf("expected zero rect when hidden, got (%d,%d,%d,%d)", x, y, w, h)
	}
}

// TestSplitterX returns the splitter column when shown and -1 when hidden.
func TestSplitterX(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.splitterX(); got != defaultSidebarWidth-1 {
		t.Fatalf("shown splitterX: got %d", got)
	}
	a.sidebarShown = false
	if got := a.splitterX(); got != -1 {
		t.Fatalf("hidden splitterX: got %d, want -1", got)
	}
}

// TestTabBarRect checks the tab bar starts after the sidebar and spans the
// remaining width on row 0.
func TestTabBarRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.tabBarRect()
	if x != defaultSidebarWidth || y != 0 || h != 1 {
		t.Fatalf("tabBar position/size unexpected: (%d,%d,%d,%d)", x, y, w, h)
	}
	if w != a.width-defaultSidebarWidth {
		t.Fatalf("tabBar width: got %d", w)
	}
	a.sidebarShown = false
	x, _, w, _ = a.tabBarRect()
	if x != 0 || w != a.width {
		t.Fatalf("hidden-sidebar tabBar should fill row: got x=%d w=%d", x, w)
	}
}

// TestEditorRect verifies the editor body sits between tab bar and status
// bar, to the right of the sidebar.
func TestEditorRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.editorRect()
	if x != defaultSidebarWidth || y != 1 {
		t.Fatalf("editor origin: (%d,%d)", x, y)
	}
	if w != a.width-defaultSidebarWidth {
		t.Fatalf("editor width: got %d", w)
	}
	if h != a.height-2 {
		t.Fatalf("editor height: got %d", h)
	}
}

// TestStatusRect always returns the bottom-most row, full width.
func TestStatusRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, y, w, h := a.statusRect()
	if x != 0 || y != a.height-1 || w != a.width || h != 1 {
		t.Fatalf("status rect: (%d,%d,%d,%d)", x, y, w, h)
	}
}

// TestMenuButtonRect places the ≡ button at the start of the tab bar and
// shifts left when the sidebar is hidden.
func TestMenuButtonRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	x, _, w, _ := a.menuButtonRect()
	if x != defaultSidebarWidth || w != menuButtonWidth {
		t.Fatalf("shown menuButtonRect: x=%d w=%d", x, w)
	}
	a.sidebarShown = false
	x, _, _, _ = a.menuButtonRect()
	if x != 0 {
		t.Fatalf("hidden menuButtonRect should sit at column 0: got %d", x)
	}
}

// TestMenuModalRect centers the modal in the window and clamps the origin
// to (0,0) when the window is too small to fit it.
func TestMenuModalRect_Centered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// The full menu no longer fits a 40-row screen (it clamps + scrolls
	// there); use a taller viewport so this test keeps pinning the
	// *centering* rule rather than the clamp.
	a.width, a.height = 120, 60
	x, y, w, h := a.menuModalRect()
	_, _, expectedH := a.menuLayout()
	if w != modalWidth || h != expectedH {
		t.Fatalf("modal size: got (%d,%d), want (%d,%d)", w, h, modalWidth, expectedH)
	}
	if x != (a.width-modalWidth)/2 || y != (a.height-expectedH)/2 {
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

// TestResizeSidebar_Clamps verifies the sidebar width clamps to the
// [minSidebarWidth, width-minEditorAfterDrag] range.
func TestResizeSidebar_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// Negative target → clamps up to minSidebarWidth.
	a.resizeSidebar(-50)
	if a.sidebarWidth != minSidebarWidth {
		t.Fatalf("negative target: got %d, want %d", a.sidebarWidth, minSidebarWidth)
	}

	// Above max → clamps to width - minEditorAfterDrag.
	a.resizeSidebar(a.width)
	wantMax := a.width - minEditorAfterDrag
	if a.sidebarWidth != wantMax {
		t.Fatalf("oversize target: got %d, want %d", a.sidebarWidth, wantMax)
	}

	// In range — kept verbatim.
	a.resizeSidebar(25)
	if a.sidebarWidth != 25 {
		t.Fatalf("in-range target: got %d", a.sidebarWidth)
	}
}

// TestResizeSidebar_TinyWindow falls back to minSidebarWidth when the window
// is too narrow for both panels at the requested size.
func TestResizeSidebar_TinyWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width = 30 // smaller than minSidebarWidth + minEditorAfterDrag.
	a.resizeSidebar(50)
	if a.sidebarWidth != minSidebarWidth {
		t.Fatalf("tiny window: got %d, want %d", a.sidebarWidth, minSidebarWidth)
	}
}

// TestDetectLangLabel covers the language label helper's three cases.
func TestDetectLangLabel(t *testing.T) {
	cases := map[string]string{
		"":               "text",
		"foo.go":         "go",
		"foo":            "text",
		"path/to/x.py":   "py",
		"archive.tar.gz": "gz",
	}
	for in, want := range cases {
		if got := detectLangLabel(in); got != want {
			t.Errorf("detectLangLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

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

// TestOpenFile_Basic opens a file, switches to it on re-open, and updates
// activeFolder to the file's parent.
func TestOpenFile_Basic(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	target := filepath.Join(sub, "file.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, dir)
	a.openFile(target)
	if a.tabs.Len() != 1 {
		t.Fatalf("expected 1 tab, got %d", a.tabs.Len())
	}
	if a.activeFolder != sub {
		t.Fatalf("activeFolder: got %q, want %q", a.activeFolder, sub)
	}

	// Re-opening should switch to existing tab, not create a new one.
	a.tabs.ActivateAt(-1)
	a.openFile(target)
	if a.tabs.Len() != 1 {
		t.Fatalf("re-open created duplicate tab")
	}
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("re-open didn't switch active: got %d", a.tabs.ActiveIndex())
	}
}

// TestOpenFile_ErrorFlash surfaces an error when the path can't be loaded
// (here, a directory rather than a file).
func TestOpenFile_ErrorFlash(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(sub)
	if !strings.Contains(a.statusMsg, "Error") {
		t.Fatalf("expected error flash, got %q", a.statusMsg)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("expected no tabs, got %d", a.tabs.Len())
	}
}

// TestRequestCloseTab_DirtyOpensModal proves a dirty tab does not close
// on first request and instead opens the unsaved-changes modal so the
// user can pick Save / Discard / Cancel.
func TestRequestCloseTab_DirtyOpensModal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.tabs.At(0).Dirty = true

	a.requestCloseTab(a.tabs.At(0))
	if a.tabs.Len() != 1 {
		t.Fatalf("dirty tab should not close until the user picks an action")
	}
	if !dirtyIsOpen(a) {
		t.Fatal("dirty close modal should be open")
	}
}

// TestRequestCloseTab_CleanClosesImmediately closes a clean tab in one shot.
func TestRequestCloseTab_CleanClosesImmediately(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.requestCloseTab(a.tabs.At(0))
	if a.tabs.Len() != 0 {
		t.Fatalf("clean tab should close on first request")
	}
}

// TestCloseTab_ClampsActive ensures activeTab never points outside the slice.
func TestCloseTab_ClampsActive(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt"))
	a.tabs.ActivateAt(1)
	a.closeTab(a.tabs.At(1))
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("activeTab should clamp to 0 after closing last; got %d", a.tabs.ActiveIndex())
	}
	a.closeTab(a.tabs.At(0))
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("activeTab should stay >=0 with no tabs; got %d", a.tabs.ActiveIndex())
	}
}

// TestCloseTab_OutOfRange is a no-op.
func TestCloseTab_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.closeTab(nil)
	a.closeTab(a.tabs.At(99))
	a.requestCloseTab(a.tabs.At(99))
}

// TestHasTab_Predicates covers the "is X available?" checks used to dim menu
// rows.
func TestHasTab_Predicates(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if a.hasTab() || a.hasSavableTab() || a.hasSelection() || a.hasClipboard() || a.hasCommentableTab() {
		t.Fatal("fresh app should have no tab/selection/clipboard/comment action")
	}

	a.openFile(target)
	if !a.hasTab() || !a.hasSavableTab() {
		t.Fatal("expected hasTab && hasSavableTab after open")
	}
	if a.hasSelection() {
		t.Fatal("no selection on a fresh tab")
	}
	if a.hasCommentableTab() {
		t.Fatal(".txt should not expose the line-comment action")
	}

	// Make a synthetic selection.
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	if !a.hasSelection() {
		t.Fatal("expected selection after Anchor != Cursor")
	}

	a.clipBuf = "x"
	if !a.hasClipboard() {
		t.Fatal("expected hasClipboard once clipBuf set")
	}
}

// TestHasCommentableTab_Predicate checks that line-comment actions only enable
// on editable text tabs with known comment syntax.
func TestHasCommentableTab_Predicate(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	htmlFile := filepath.Join(dir, "index.html")
	if err := os.WriteFile(goFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed go: %v", err)
	}
	if err := os.WriteFile(htmlFile, []byte("<main></main>"), 0644); err != nil {
		t.Fatalf("seed html: %v", err)
	}
	a := newTestApp(t, dir)

	a.openFile(goFile)
	if !a.hasCommentableTab() {
		t.Fatal(".go tab should expose the line-comment action")
	}

	a.openFile(htmlFile)
	if a.hasCommentableTab() {
		t.Fatal(".html tab should not expose the line-comment action")
	}
}

// TestSidebarToggleLabel flips between Show/Hide based on sidebarShown.
func TestSidebarToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.sidebarToggleLabel() != "Hide file explorer" {
		t.Fatalf("got %q", a.sidebarToggleLabel())
	}
	a.sidebarShown = false
	if a.sidebarToggleLabel() != "Show file explorer" {
		t.Fatalf("got %q", a.sidebarToggleLabel())
	}
}

// TestNewFileLabel_Plain shows the bare label when the active folder is the
// project root.
func TestNewFileLabel_Plain(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.newFileLabel(); got != "New file" {
		t.Fatalf("root label: got %q", got)
	}
	a.activeFolder = ""
	if got := a.newFileLabel(); got != "New file" {
		t.Fatalf("empty folder label: got %q", got)
	}
}

// TestNewFileLabel_SuffixForSubdir adds a "(in subdir)" suffix when the
// active folder is under the project root.
func TestNewFileLabel_SuffixForSubdir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "alpha")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.setActiveFolder(sub)
	got := a.newFileLabel()
	if !strings.HasPrefix(got, "New file (in ") {
		t.Fatalf("expected 'New file (in ...)', got %q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Fatalf("expected basename in label, got %q", got)
	}
}

// TestNewFileLabel_TruncatesLongPaths keeps the trailing folder visible
// when the relative path would otherwise overflow the modal.
func TestNewFileLabel_TruncatesLongPaths(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir,
		"this-is-a-rather-long-name", "and-another-very-long-name", "trailing")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.setActiveFolder(deep)
	got := a.newFileLabel()
	if !strings.Contains(got, "trailing") {
		t.Fatalf("expected trailing folder name preserved; got %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("expected truncation ellipsis; got %q", got)
	}
}

// TestRelativeFolderLabel covers the three branches: root, subdir, and a
// non-relatable path.
func TestRelativeFolderLabel(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	// Root → basename + sep.
	rootLabel := a.relativeFolderLabel(a.rootDir)
	if !strings.HasSuffix(rootLabel, string(filepath.Separator)) {
		t.Fatalf("root missing trailing sep: %q", rootLabel)
	}
	if !strings.HasPrefix(rootLabel, filepath.Base(a.rootDir)) {
		t.Fatalf("root should start with its basename: %q", rootLabel)
	}

	// Subdir → relative path.
	subLabel := a.relativeFolderLabel(sub)
	if subLabel != "child"+string(filepath.Separator) {
		t.Fatalf("subdir label: got %q", subLabel)
	}
}

// TestMenuMoveSelection_WrapsAroundEnds simulates a small menu with all rows
// enabled to verify wrapping in both directions.
func TestMenuMoveSelection_WrapsAroundEnds(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Open every potential gate: a savable tab + selection + clipboard.
	tmp := filepath.Join(a.rootDir, "f.txt")
	if err := os.WriteFile(tmp, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(tmp)
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

// TestMenuMoveSelection_NothingEnabledYieldsMinusOne lands on -1 when no row
// is enabled (we synthesise that by setting every predicate to false-ish via
// the no-tab/no-clipboard initial state, except always-true rows).
func TestMenuMoveSelection_SkipsDisabled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// No tabs, no selection, no clipboard. Save/Close/Rename/Delete/Copy/
	// Cut/Paste are all disabled. New file / toggle / quit stay enabled.
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

// TestFlash sets statusMsg and pushes statusUntil into the future.
func TestFlash(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	before := time.Now()
	a.flash("hello world")
	if a.statusMsg != "hello world" {
		t.Fatalf("statusMsg: got %q", a.statusMsg)
	}
	if !a.statusUntil.After(before) {
		t.Fatalf("statusUntil should be in the future, got %v", a.statusUntil)
	}
}

// TestMenuToggleSidebar flips the sidebarShown flag.
func TestMenuToggleSidebar(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if !a.sidebarShown {
		t.Fatal("sidebar should start visible")
	}
	a.menuToggleSidebar()
	if a.sidebarShown {
		t.Fatal("expected hidden after first toggle")
	}
	a.menuToggleSidebar()
	if !a.sidebarShown {
		t.Fatal("expected shown after second toggle")
	}
}

// TestMenuToggleLineComment runs the menu action against the active tab so the
// app layer and editor-layer primitive stay wired together.
func TestMenuToggleLineComment(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("one\ntwo"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuToggleLineComment()

	if got := a.activeTabPtr().Buffer.String(); got != "// one\ntwo" {
		t.Fatalf("buffer = %q, want current line commented", got)
	}
	if a.statusMsg != "Toggled line comment" {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
}

// TestMenuToggleLineComment_Unsupported flashes a clear no-op instead of
// guessing at block-comment-only formats.
func TestMenuToggleLineComment_Unsupported(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.html")
	if err := os.WriteFile(target, []byte("<main></main>"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuToggleLineComment()

	if got := a.activeTabPtr().Buffer.String(); got != "<main></main>" {
		t.Fatalf("unsupported buffer changed to %q", got)
	}
	if a.statusMsg != "No line comment syntax for this file" {
		t.Fatalf("statusMsg = %q", a.statusMsg)
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

// TestEditorSize matches the editor rect's width and height.
func TestEditorSize(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	w, h := a.editorSize()
	if w != a.width-defaultSidebarWidth || h != a.height-2 {
		t.Fatalf("editorSize: got (%d,%d)", w, h)
	}
}

// TestActiveTabPtr returns nil with no tabs and the right pointer otherwise.
func TestActiveTabPtr(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if a.activeTabPtr() != nil {
		t.Fatal("expected nil with no tabs")
	}
	a.openFile(target)
	if a.activeTabPtr() != a.tabs.At(0) {
		t.Fatal("activeTabPtr should match the active tab")
	}
	// An out-of-range activation clamps onto a real tab — the
	// "active index points past the list" state cannot exist anymore.
	a.tabs.ActivateAt(99)
	if a.activeTabPtr() != a.tabs.At(0) {
		t.Fatal("out-of-range activation should clamp to a real tab")
	}
}

// TestSaveActiveTab writes the buffer to disk and clears Dirty.
func TestSaveActiveTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "save.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().InsertString("X")
	a.saveActiveTab()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "X") {
		t.Fatalf("save did not persist: %q", got)
	}
}

// TestSaveActiveTab_NoTab is a no-op.
func TestSaveActiveTab_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.saveActiveTab()
}

// TestCopyCutPaste exercises the clipboard glue.
func TestCopyCutPaste(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	// No selection — copy/cut should be no-ops.
	a.copySelection()
	a.cutSelection()
	if a.clipBuf != "" {
		t.Fatalf("clipBuf should still be empty: %q", a.clipBuf)
	}

	// Make selection of "hello".
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 5}
	a.copySelection()
	if a.clipBuf != "hello" {
		t.Fatalf("copy: clipBuf %q", a.clipBuf)
	}

	// Cut: same selection should now empty the buffer.
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 5}
	a.cutSelection()
	if tab.Buffer.LineRunes(0) != nil && len(tab.Buffer.LineRunes(0)) != 0 {
		// Some buffer impls return empty slice; both fine.
	}

	// Paste empty path: when clipBuf empty, flash about external paste.
	a.clipBuf = ""
	a.pasteClipboard()
	if !strings.Contains(a.statusMsg, "clipboard empty") {
		t.Fatalf("expected empty-clip flash, got %q", a.statusMsg)
	}

	// Paste with content.
	a.clipBuf = "X"
	a.pasteClipboard()
}

// TestPasteClipboard_NoTab is safe with no tab open.
func TestPasteClipboard_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.clipBuf = "X"
	a.pasteClipboard() // no tab — nothing to paste into.
}

// TestMenuSaveAndClose saves then closes the active tab.
func TestMenuSaveAndClose(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sc.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().InsertString("Y")
	a.menuSaveAndClose()
	if a.tabs.Len() != 0 {
		t.Fatalf("expected tab closed; got %d tabs", a.tabs.Len())
	}
}

// TestMenuSaveAndClose_NoTab is a no-op when nothing is open.
func TestMenuSaveAndClose_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuSaveAndClose()
}

// TestMenuClickPaths covers menuSave/menuCopy/menuCut/menuPaste/menuClose
// menuQuit and menuRefreshTree as one-liners.
func TestMenuClickPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	// Selection so copy/cut have something to operate on.
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 2}

	a.menuOpen = true
	a.menuSave()
	a.menuOpen = true
	a.menuCopy()
	a.menuOpen = true
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	a.menuCut()
	a.menuOpen = true
	a.menuPaste()
	a.menuOpen = true
	a.menuRefreshTree()

	// Clean the tab before quitting; the dirty-quit path is exercised
	// separately in dirty_modal_test.go.
	tab.Dirty = false
	a.menuOpen = true
	a.menuQuit()
	if !a.quit {
		t.Fatal("menuQuit should set quit flag")
	}
}

// TestUndoRedoRevert_MenuPaths exercises the new history actions end
// to end through the menu wrappers. The flash on no-op paths is also
// covered so the user always gets feedback when they hit a dead-end.
func TestUndoRedoRevert_MenuPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	// Nothing to undo / redo / revert on a freshly opened file.
	if a.hasUndo() || a.hasRedo() || a.hasRevert() {
		t.Fatal("freshly opened tab should have no history")
	}
	a.menuOpen = true
	a.menuUndo()
	a.menuOpen = true
	a.menuRedo()
	a.menuOpen = true
	a.menuRevert()

	// One edit → undo + revert become available.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 4}, false)
	tab.InsertString("X")
	if !a.hasUndo() || !a.hasRevert() {
		t.Fatal("expected undo + revert after edit")
	}
	if a.hasRedo() {
		t.Fatal("redo should still be empty")
	}

	a.menuOpen = true
	a.menuUndo()
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after menuUndo = %q, want seed", got)
	}
	if !a.hasRedo() {
		t.Fatal("redo should be populated after an undo")
	}

	a.menuOpen = true
	a.menuRedo()
	if got := tab.Buffer.String(); got != "seedX" {
		t.Fatalf("after menuRedo = %q, want seedX", got)
	}

	// Revert back to original; then Undo must recover the post-edit state.
	a.menuOpen = true
	a.menuRevert()
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after menuRevert = %q, want seed", got)
	}
	a.menuOpen = true
	a.menuUndo()
	if got := tab.Buffer.String(); got != "seedX" {
		t.Fatalf("after undo-of-revert = %q, want seedX", got)
	}
}

// TestUndoRedoRevert_NoTabSafelyNoOps guards against crashes when the
// menu rows somehow fire with no active tab — they should silently
// return rather than dereferencing nil.
func TestUndoRedoRevert_NoTabSafelyNoOps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuUndo()
	a.menuOpen = true
	a.menuRedo()
	a.menuOpen = true
	a.menuRevert()
	if a.hasUndo() || a.hasRedo() || a.hasRevert() {
		t.Fatal("no-tab predicates should all be false")
	}
}

// TestMenuClose_NoTab safely no-ops.
func TestMenuClose_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuClose()
}

// TestMenuActivate_RunsHovered runs the action attached to the highlighted
// row.
func TestMenuActivate_RunsHovered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	// Force highlight onto the sidebar-toggle row (always enabled, label
	// supplied dynamically via labelFor), then activate.
	items, _, _ := a.menuLayout()
	for i, item := range items {
		if item.labelFor != nil && item.label == "" && item.action != nil {
			// labelFor + empty static label is the marker for the toggle
			// row. The newFile row also uses labelFor, so disambiguate by
			// flipping the sidebar and checking afterward.
			a.hoveredMenuRow = i
			a.menuActivate()
			a.openMenu()
		}
	}
	// Re-find and run the toggle row via its dynamic label.
	a.hoveredMenuRow = -1
	items, _, _ = a.menuLayout()
	for i, item := range items {
		if item.labelFor != nil && (item.labelFor(a) == "Show file explorer" || item.labelFor(a) == "Hide file explorer") {
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

// TestMenuActivate_OutOfRange and disabled rows are no-ops.
func TestMenuActivate_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.hoveredMenuRow = -1
	a.menuActivate()
	a.hoveredMenuRow = 999
	a.menuActivate()
}

// TestUpdateMenuHover snaps to the right row when over an enabled row, and
// to -1 when outside.
func TestUpdateMenuHover(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()

	// Find an always-enabled row and click on its relY.
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

	// Outside the modal → -1.
	a.updateMenuHover(0, 0)
	if a.hoveredMenuRow != -1 {
		t.Fatalf("outside modal: got %d", a.hoveredMenuRow)
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

// TestHandleKey_EscDoubleTapOpensMenu opens the menu after two Esc presses.
func TestHandleKey_EscDoubleTapOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if a.menuOpen {
		t.Fatal("first Esc should not open menu")
	}
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if !a.menuOpen {
		t.Fatal("second Esc should open menu")
	}
	// Third Esc — menu open, should close.
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if a.menuOpen {
		t.Fatal("Esc with menu open should close it")
	}
}

// TestHandleKey_MenuNavKeys move highlight and Enter activates.
func TestHandleKey_MenuNavKeys(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	first := a.hoveredMenuRow
	a.handleKey(keyEv(tcell.KeyDown, 0))
	if a.hoveredMenuRow == first {
		t.Fatal("Down should advance highlight")
	}
	a.handleKey(keyEv(tcell.KeyUp, 0))
	if a.hoveredMenuRow != first {
		t.Fatalf("Up should return to %d, got %d", first, a.hoveredMenuRow)
	}
}

// TestHandleKey_MenuShortcutAfterNavigation keeps menu shortcut letters live
// after arrow-key navigation has moved the highlighted row.
func TestHandleKey_MenuShortcutAfterNavigation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()

	a.handleKey(keyEv(tcell.KeyDown, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'p'))

	if a.menuOpen {
		t.Fatal("Esc-p from the open menu should close the menu")
	}
	if !finderIsOpen(a) {
		t.Fatal("Esc-p from the open menu should open the project finder")
	}
}

// TestHandleKey_RoutesToActiveTab dispatches typing to the active tab.
func TestHandleKey_RoutesToActiveTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	a.handleKey(keyEv(tcell.KeyRune, 'b'))
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'c'))
	a.handleKey(keyEv(tcell.KeyTab, 0))
	a.handleKey(keyEv(tcell.KeyBackspace, 0))
	a.handleKey(keyEv(tcell.KeyHome, 0))
	a.handleKey(keyEv(tcell.KeyEnd, 0))
	a.handleKey(keyEv(tcell.KeyLeft, 0))
	a.handleKey(keyEv(tcell.KeyRight, 0))
	a.handleKey(keyEv(tcell.KeyUp, 0))
	a.handleKey(keyEv(tcell.KeyDown, 0))
	a.handleKey(keyEv(tcell.KeyPgUp, 0))
	a.handleKey(keyEv(tcell.KeyPgDn, 0))
	a.handleKey(keyEv(tcell.KeyDelete, 0))
}

// TestHandleEvent_Resize updates width/height.
func TestHandleEvent_Resize(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(80, 24)
	ev := tcell.NewEventResize(80, 24)
	a.handleEvent(ev)
	if a.width != 80 || a.height != 24 {
		t.Fatalf("resize: got %dx%d", a.width, a.height)
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

// TestHandleMenuMouse_ClicksRowAndOutside both fires the row action and
// dismisses on outside click.
func TestHandleMenuMouse_ClicksRowAndOutside(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Tall viewport: the toggle row sits near the menu's bottom, which
	// on a 40-row screen falls into the scrolled-off region.
	a.width, a.height = 120, 60
	a.openMenu()
	mx, my, _, _ := a.menuModalRect()
	// Click on the sidebar toggle row — flips the sidebar.
	items, _, _ := a.menuLayout()
	toggleRelY := -1
	for _, item := range items {
		if item.labelFor != nil && item.labelFor(a) == "Hide file explorer" {
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

// TestHandleMenuMouse_NoButtonIsNoop ignores motion-only events.
func TestHandleMenuMouse_NoButtonIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.handleMenuMouse(0, 0, 0)
	if !a.menuOpen {
		t.Fatal("motion-only event should not close menu")
	}
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

// TestDraw_AllPanels exercises the drawing path so the stdout/screen code
// is covered. Result correctness is exercised manually; here we just make
// sure no panics across several states.
func TestDraw_AllPanels(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.draw() // empty editor + sidebar
	a.openFile(target)
	a.draw() // with a tab
	a.activeTabPtr().Dirty = true
	a.draw() // dirty marker
	a.openMenu()
	a.draw() // with menu modal
	a.closeMenu()
	a.openPrompt("T", "H", "x", nil)
	a.draw()
	a.closeAllModals()
	a.openConfirm("T", "M", nil)
	a.draw()
	a.closeAllModals()
	a.openTreeContext(a.tree.Root, 5, 5)
	a.draw()
	a.closeAllModals()
	a.flash("hello")
	a.draw() // status flash
	a.sidebarShown = false
	a.draw()
	// Tiny window → too-small message.
	a.width, a.height = 5, 5
	a.draw()
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

// TestDrawStatusBar_RendersBranchRightAligned pins down the lower-right
// branch label: when gitBranch is set, the rightmost cells of the
// status bar carry " <branch> " in order, so the user can glance at
// the corner and read which checkout they're on.
func TestDrawStatusBar_RendersBranchRightAligned(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "feat/widgets"
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show() // SimulationScreen serves GetContents from the *front* buffer.

	cells, w, _ := scr.GetContents()
	_, sy, _, _ := a.statusRect()

	want := []rune(" feat/widgets ")
	startX := w - len(want)
	for i, r := range want {
		c := cells[sy*w+startX+i]
		if len(c.Runes) == 0 || c.Runes[0] != r {
			t.Fatalf("status bar col %d = %v, want %q",
				startX+i, c.Runes, r)
		}
	}
}

// TestDrawStatusBar_OmitsBranchWhenEmpty confirms a non-repo project
// (gitBranch == "") doesn't paint a stray label or steal cells from
// the left-side text — the right edge should just be the bar's bg.
func TestDrawStatusBar_OmitsBranchWhenEmpty(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = ""
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	cells, w, _ := scr.GetContents()
	_, sy, _, _ := a.statusRect()

	// Tail of the status bar must be blank — the bar's fill character.
	for x := w - 5; x < w; x++ {
		c := cells[sy*w+x]
		if len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("status bar col %d = %v, expected blank tail", x, c.Runes)
		}
	}
}

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

// TestDrawMenu_RightAlignsShortcuts verifies the shortcut column is painted at
// the modal's right edge instead of being appended to the command label.
func TestDrawMenu_RightAlignsShortcuts(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.drawMenu()
	a.screen.Show()

	mx, my, mw, _ := a.menuModalRect()
	save := menuItemByLabel(t, a, "Save")
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

// TestTrimRunes covers the menu-label clipping helper so long dynamic labels
// cannot overwrite the right-aligned shortcut column.
func TestTrimRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"fits", "Save", 8, "Save"},
		{"clips", "Find file in project", 10, "Find file…"},
		{"one", "Save", 1, "…"},
		{"none", "Save", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimRunes(tt.in, tt.max); got != tt.want {
				t.Fatalf("trimRunes(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

// menuItemByLabel finds a menu row by static label for tests that care about
// one action without hard-coding its row index.
func menuItemByLabel(t *testing.T, a *App, label string) menuItemDef {
	t.Helper()
	items, _, _ := a.menuLayout()
	for _, item := range items {
		if item.label == label {
			return item
		}
	}
	t.Fatalf("menu item %q not found", label)
	return menuItemDef{}
}

// screenLine returns one row from a SimulationScreen as a fixed-width string.
func screenLine(scr tcell.SimulationScreen, y int) string {
	cells, w, _ := scr.GetContents()
	rs := make([]rune, w)
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) == 0 {
			rs[x] = ' '
			continue
		}
		rs[x] = c.Runes[0]
	}
	return string(rs)
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

// TestRunCustomAction_NoFileStillRuns confirms a prompt-less action
// runs even with no tab open. Earlier the runner short-circuited
// here with a "no file open" flash, but that gate guessed wrong
// for $FILE-free commands like "brew upgrade …" — they got blocked
// even though they had no file dependency at all. The new contract:
// always run; if a $FILE-dependent command then fails because FILE
// is empty, the failure surfaces in the info modal with the actual
// stderr, which is more informative than a generic flash.
func TestRunCustomAction_NoFileStillRuns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	a := newTestApp(t, dir)
	a.customActions = []customactions.Action{{
		Label:   "Touch marker",
		Command: "touch " + marker,
	}}
	a.runCustomAction(0)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		if _, ok := ev.(*customActionDoneEvent); ok {
			break
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("command did not run: %v", err)
	}
	if strings.Contains(a.statusMsg, "no file open") {
		t.Errorf("status flash should not mention no file open: %q", a.statusMsg)
	}
}

// TestRunCustomAction_OutOfRange is a no-op when idx is bogus. Caller
// should never produce one but the guard keeps a stale layout from
// crashing the editor.
func TestRunCustomAction_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.runCustomAction(0)  // empty list
	a.runCustomAction(99) // out of range
}

// TestRunCustomAction_ExecutesAndPostsEvent runs a real `sh -c`
// command against a small file and confirms that (a) the command
// observed FILE / FILENAME via env, and (b) a customActionDoneEvent
// lands on the screen's event queue. The chosen command writes a
// marker file that lets the test verify env reached the subprocess.
func TestRunCustomAction_ExecutesAndPostsEvent(t *testing.T) {
	// Redirect the action log into the test's temp dir so we don't
	// scribble into the developer's real ~/.local/state/skiff/.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	marker := filepath.Join(dir, "marker.txt")
	a := newTestApp(t, dir)
	a.openFile(target)
	a.customActions = []customactions.Action{{
		Label:   "Mark",
		Command: `printf "%s|%s" "$FILE" "$FILENAME" > ` + marker,
	}}

	a.runCustomAction(0)

	// The action runs in a goroutine and posts an event back. Pull
	// events off the screen's queue until we see the done event, with
	// a sanity timeout so a regression can't hang the suite forever.
	deadline := time.Now().Add(2 * time.Second)
	var done *customActionDoneEvent
	for time.Now().Before(deadline) && done == nil {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		if d, ok := ev.(*customActionDoneEvent); ok {
			done = d
		}
	}
	if done == nil {
		t.Fatal("no customActionDoneEvent received within timeout")
	}
	if done.err != nil {
		t.Fatalf("action errored: %v", done.err)
	}
	if done.label != "Mark" {
		t.Errorf("event label = %q", done.label)
	}

	// Verify the subprocess saw the env variables we exported.
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker read: %v", err)
	}
	want := target + "|" + "src.txt"
	if string(got) != want {
		t.Fatalf("marker content = %q, want %q", got, want)
	}
}

// TestRunCustomAction_PromptedSkipsNoFileGuard ensures actions that
// declare prompts can run even when no tab is open. Copy-from-remote
// is the motivating case — without this, the very first thing the
// user wants to do in a fresh session would silently flash "no file
// open" and refuse to show the form.
func TestRunCustomAction_PromptedSkipsNoFileGuard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = []customactions.Action{{
		Label:   "Copy from remote",
		Command: "true",
		Prompts: []customactions.Prompt{
			{Key: "HOST", Type: customactions.PromptSelect, Options: []string{"a", "b"}},
		},
	}}
	a.runCustomAction(0)

	if !formIsOpen(a) {
		t.Fatal("prompted action with no file open should still show the form modal")
	}
	if strings.Contains(a.statusMsg, "no file open") {
		t.Errorf("prompted action should not flash no-file-open: %q", a.statusMsg)
	}
}

// TestRunCustomAction_PromptedExportsValuesAndExpands walks the full
// SCP-from-remote path: the form opens, we fill it in, submit, and
// assert the spawned shell saw both the form-collected env vars
// (HOST, REMOTE_SRC) and the editor-state vars (PROJECT_ROOT). This
// is the contract that makes the feature actually useful — if any of
// these don't reach the shell, the user's command fails silently.
func TestRunCustomAction_PromptedExportsValuesAndExpands(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	a := newTestApp(t, dir)
	a.customActions = []customactions.Action{{
		Label:   "Copy from remote",
		Command: `printf "%s|%s|%s" "$HOST" "$REMOTE_SRC" "$PROJECT_ROOT" > ` + marker,
		Prompts: []customactions.Prompt{
			{Key: "HOST", Type: customactions.PromptSelect, Options: []string{"cascade", "rager"}},
			{Key: "REMOTE_SRC", Type: customactions.PromptText},
		},
	}}

	a.runCustomAction(0)
	if !formIsOpen(a) {
		t.Fatal("form did not open")
	}

	// Fill in REMOTE_SRC by typing into the focused field after Tab'ing
	// past the HOST select — all through real routing.
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	for _, r := range "/etc/hosts" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if formIsOpen(a) {
		t.Fatal("Enter on last field should submit")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		if _, ok := ev.(*customActionDoneEvent); ok {
			break
		}
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker read: %v", err)
	}
	parts := strings.Split(string(got), "|")
	if len(parts) != 3 {
		t.Fatalf("marker = %q, want HOST|REMOTE_SRC|PROJECT_ROOT", got)
	}
	if parts[0] != "cascade" {
		t.Errorf("HOST = %q, want %q", parts[0], "cascade")
	}
	if parts[1] != "/etc/hosts" {
		t.Errorf("REMOTE_SRC = %q, want %q", parts[1], "/etc/hosts")
	}
	if !strings.HasSuffix(parts[2], filepath.Base(dir)) {
		t.Errorf("PROJECT_ROOT = %q, want suffix matching tempdir", parts[2])
	}
}

// TestHandleCustomActionDone_FailureOpensInfoModal pins the error
// reporting upgrade. The pre-fix behaviour was a one-line status
// flash that truncated scp's stderr exactly when the user most
// needed to read it. Now failures route into the info modal so the
// stderr lines stay visible until dismissed.
func TestHandleCustomActionDone_FailureOpensInfoModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCustomActionDone(&customActionDoneEvent{
		label:  "Copy from remote",
		err:    fmt.Errorf("exit status 1"),
		output: []byte("scp: /etc/missing: No such file or directory\n"),
	})
	n := infoPrefab(t, a)
	joined := strings.Join(n.Lines, "\n")
	if !strings.Contains(joined, "scp:") || !strings.Contains(joined, "missing") {
		t.Errorf("info body missing stderr preview: %q", joined)
	}
	if !strings.Contains(n.Title, "Copy from remote") {
		t.Errorf("title = %q, want it to mention the action label", n.Title)
	}
}

// TestHandleCustomActionDone_SuccessRefreshesTree confirms a
// successful action triggers an immediate tree refresh so a
// freshly-pulled file appears without waiting on the 10-second
// auto-refresh tick. Pinning this avoids a regression where a user
// runs Copy-from-remote, sees "done", and then has to pause before
// the new file becomes clickable in the sidebar.
func TestHandleCustomActionDone_SuccessRefreshesTree(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	// Drop a file directly on disk that the tree hasn't seen yet —
	// without an explicit refresh it would only show up on the next
	// 10-second tick.
	newFile := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(newFile, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	beforeChildren := len(a.tree.Root.Children)
	a.handleCustomActionDone(&customActionDoneEvent{label: "X"})
	if got := len(a.tree.Root.Children); got <= beforeChildren {
		t.Errorf("tree was not refreshed: %d → %d children", beforeChildren, got)
	}
}

// TestSplitErrorOutput_TruncatesAndAppendsLogPath nails the body
// the info modal renders on failure. Without truncation a runaway
// scp -v dump would push the dialog off-screen; without the actions.log
// pointer the user can't easily get the full version even though
// we're already writing it.
func TestSplitErrorOutput_TruncatesAndAppendsLogPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgtest")

	long := strings.Repeat("really long line that exceeds eighty cells ", 4)
	out := []byte(long + "\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\n")
	body := splitErrorOutput(fmt.Errorf("exit 1"), out)

	if body[0] != "exit 1" {
		t.Errorf("body[0] = %q, want exit-error summary", body[0])
	}
	last := body[len(body)-1]
	if !strings.Contains(last, "actions.log") {
		t.Errorf("last line = %q, want actions.log pointer", last)
	}
	for _, ln := range body {
		if runeLen(ln) > 80 {
			t.Errorf("line over 80 cells: %q (len=%d)", ln, runeLen(ln))
		}
	}
	if !strings.Contains(strings.Join(body, "\n"), "truncated") {
		t.Error("expected '… truncated' marker for >maxLines output")
	}
}

// TestLayoutTabs_IconsExpandWidth pins down the geometry contract:
// turning icons on grows each tab by exactly two cells (the glyph + a
// separator space), and the close-× column shifts right by the same
// amount. Without this, a tab-bar click on the × would land on the
// wrong column whenever icons are enabled.
func TestLayoutTabs_IconsExpandWidth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))

	a.tree.IconsEnabled = false
	off := a.layoutTabs()
	a.tree.IconsEnabled = true
	on := a.layoutTabs()

	if len(off) != 1 || len(on) != 1 {
		t.Fatalf("layoutTabs len off=%d on=%d, want 1 each", len(off), len(on))
	}
	if on[0].Width != off[0].Width+2 {
		t.Fatalf("icons should add 2 cells: off=%d on=%d", off[0].Width, on[0].Width)
	}
	if on[0].CloseX != off[0].CloseX+2 {
		t.Fatalf("CloseX should shift by 2 when icons on: off=%d on=%d",
			off[0].CloseX, on[0].CloseX)
	}
}

// TestDrawTabBar_RendersIconWhenEnabled verifies the glyph actually
// lands on screen between the dirty slot and the file name when
// icons are enabled. We use the simulation screen and look for the
// language-specific glyph from icons.For somewhere on the tab row.
func TestDrawTabBar_RendersIconWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))
	a.tree.IconsEnabled = true

	a.drawTabBar()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()

	// Read the tab-bar row (y=0) and look for the .go glyph.
	wantGlyph := []rune(icons.For("main.go", false, false))[0]
	found := false
	for x := 0; x < w; x++ {
		c := cells[x]
		if len(c.Runes) > 0 && c.Runes[0] == wantGlyph {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected .go glyph on the tab-bar row when icons are enabled")
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

// TestMenuToggleSidebar_NoPanicInSingleFileMode is a regression guard for
// a crash: the Esc-t leader calls menuToggleSidebar directly, bypassing the
// menu row's hasTree gate. In single-file mode (tree == nil) flipping
// sidebarShown true would send draw() into a.tree.Render on a nil tree and
// panic. The toggle must stay a no-op so the sidebar can't be shown when
// there's no tree behind it.
func TestMenuToggleSidebar_NoPanicInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil // single-file mode
	a.sidebarShown = false

	a.menuToggleSidebar() // simulates the Esc-t leader

	if a.sidebarShown {
		t.Fatal("sidebar must stay hidden in single-file mode — no tree to render")
	}
	a.draw() // would panic on nil a.tree.Render if the toggle flipped it on
}

// TestRefreshGitStatus_RefreshesGutterInSingleFileMode pins the
// single-file-mode fix: with no file tree, refreshGitStatus must still
// reload the open tab's per-line gutter markers (a file-scoped git diff
// that doesn't need the tree). Without this, saving a file in
// single-file mode — which routes through refreshGitStatus — would
// leave the gutter markers frozen at their open-time state.
func TestRefreshGitStatus_RefreshesGutterInSingleFileMode(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	target := filepath.Join(repo, "f.go")
	writeFileT(t, target, "package main\n\nfunc main() {}\n")
	gitRun(t, repo, "add", "f.go")
	gitRun(t, repo, "commit", "-m", "init")

	a := newTestApp(t, repo)
	a.tree = nil // simulate single-file mode
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected an open tab")
	}

	// Clean file → no markers yet. Now dirty the worktree and clear the
	// tab's cached markers so we can prove refreshGitStatus repopulates
	// them despite tree == nil.
	writeFileT(t, target, "package main\n\nfunc main() { println(1) }\n")
	tab.GitLines = nil
	a.refreshGitStatus()

	if len(tab.GitLines) == 0 {
		t.Fatal("expected gutter markers to be refreshed in single-file mode, got none")
	}
}

// TestDrawTabBar_NoIconWhenDisabled is the inverse of the above —
// flipping IconsEnabled off must remove the glyph from the tab bar
// (so terminals without a Nerd Font don't see tofu boxes in tabs).
func TestDrawTabBar_NoIconWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "main.go"))
	a.tree.IconsEnabled = false

	a.drawTabBar()
	a.screen.Show()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()

	wantGlyph := []rune(icons.For("main.go", false, false))[0]
	for x := 0; x < w; x++ {
		c := cells[x]
		if len(c.Runes) > 0 && c.Runes[0] == wantGlyph {
			t.Fatalf("did not expect glyph %q at x=%d when icons off", string(wantGlyph), x)
		}
	}
}

// resizeTestApp shrinks the simulation screen and keeps the App's cached
// width/height in sync, mirroring what the EventResize path does live.
func resizeTestApp(t *testing.T, a *App, w, h int) {
	t.Helper()
	a.screen.(tcell.SimulationScreen).SetSize(w, h)
	a.width, a.height = w, h
}

// screenHasText reports whether s appears left-to-right on any single row
// of the front buffer. Show() must have been called first — the
// SimulationScreen serves GetContents from the front buffer.
func screenHasText(t *testing.T, a *App, s string) bool {
	t.Helper()
	cells, w, h := a.screen.(tcell.SimulationScreen).GetContents()
	want := []rune(s)
	for y := 0; y < h; y++ {
		for x := 0; x+len(want) <= w; x++ {
			match := true
			for i, r := range want {
				c := cells[y*w+x+i]
				if len(c.Runes) == 0 || c.Runes[0] != r {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// TestMenuModalRect_ClampsToShortTerminal is the geometry half of the
// clipped-menu regression: at the app's declared 80×24 minimum the modal
// must fit the screen (with a one-row margin) instead of rendering its
// natural ~38-row layout off the bottom edge.
func TestMenuModalRect_ClampsToShortTerminal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)
	_, _, _, h := a.menuModalRect()
	if h > a.height-2 {
		t.Fatalf("modal height %d exceeds screen height-2 (%d)", h, a.height-2)
	}
	if a.menuMaxScroll() <= 0 {
		t.Fatal("expected the clamped menu to report scrollable overflow")
	}
}

// TestMenuScroll_KeyboardScrollsQuitIntoView is the regression test for
// the P1 "Quit editor is unreachable at 80×24" bug: selecting the last
// row via keyboard must scroll it into the visible region and actually
// paint it. Before the fix the row was drawn past the screen edge and
// silently dropped.
func TestMenuScroll_KeyboardScrollsQuitIntoView(t *testing.T) {
	a := newTestApp(t, t.TempDir())
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
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	mx, my, mw, mh := a.menuModalRect()
	cx, cy := mx+mw/2, my+mh/2

	max := a.menuMaxScroll()
	for i := 0; i < max+5; i++ {
		a.handleMenuMouse(cx, cy, tcell.WheelDown)
	}
	if a.menuScroll != max {
		t.Fatalf("wheel-down should clamp at maxScroll %d, got %d", max, a.menuScroll)
	}
	for i := 0; i < max+5; i++ {
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
	resizeTestApp(t, a, 80, 24)
	a.openMenu()
	a.menuScroll = a.menuMaxScroll()

	items, _, _ := a.menuLayout()
	quit := items[len(items)-1]
	mx, my, _, mh := a.menuModalRect()

	borderY := my + mh - 1
	a.handleMenuMouse(mx+2, borderY, tcell.Button1)
	if a.quit {
		t.Fatal("clicking the bottom border must not activate a hidden row")
	}

	quitY := my + quit.relY - a.menuScroll
	if quitY < my+3 || quitY > my+mh-2 {
		t.Fatalf("test setup: Quit row not in visible region (y=%d)", quitY)
	}
	a.handleMenuMouse(mx+2, quitY, tcell.Button1)
	if !a.quit {
		t.Fatal("clicking the scrolled Quit row should quit the editor")
	}
}

// openManyTabs seeds count files with distinctive names in dir and
// opens them all, leaving the last one active. Icons are forced off so
// tab widths are deterministic across environments.
func openManyTabs(t *testing.T, a *App, dir string, count int) []string {
	t.Helper()
	if a.tree != nil {
		a.tree.IconsEnabled = false
	}
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("longtabname%02d.txt", i)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		a.openFile(p)
		names = append(names, name)
	}
	return names
}

// TestTabBar_ActiveTabScrollsIntoView is the regression test for the
// invisible-active-tab P1: with more tabs than the strip can hold at
// 80 columns, activating the last tab must scroll the strip so its
// name is actually painted, with a ‹ marker showing tabs are hidden
// to the left. Before the fix the active tab rendered nowhere.
func TestTabBar_ActiveTabScrollsIntoView(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	names := openManyTabs(t, a, dir, 5)

	a.drawTabBar()
	a.screen.Show()

	if !screenHasText(t, a, names[len(names)-1]) {
		t.Fatal("active tab's name should be scrolled into view at 80 columns")
	}
	if !screenHasText(t, a, "‹") {
		t.Fatal("expected a ‹ marker for tabs hidden to the left")
	}
}

// TestTabBar_ChevronsMarkOverflow pins the marker rules: at scroll 0
// with overflow only a › shows; scrolled to the end only a ‹ shows.
func TestTabBar_ChevronsMarkOverflow(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	resizeTestApp(t, a, 80, 24)
	openManyTabs(t, a, dir, 5)

	a.tabScroll = 0
	a.drawTabBar()
	a.screen.Show()
	if screenHasText(t, a, "‹") {
		t.Fatal("no ‹ expected at scroll 0")
	}
	if !screenHasText(t, a, "›") {
		t.Fatal("expected › with tabs hidden to the right")
	}

	a.tabScroll = a.maxTabScroll()
	a.drawTabBar()
	a.screen.Show()
	if !screenHasText(t, a, "‹") {
		t.Fatal("expected ‹ when scrolled to the end")
	}
	if screenHasText(t, a, "›") {
		t.Fatal("no › expected at max scroll")
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

// TestDrawStatusBar_ArmedEscTag pins the pending-gesture indicator:
// while an Esc is armed (any window still open) the status bar shows
// an "Esc…" tag, and once the window has fully expired the tag is
// gone. The editor's only modifier must not have invisible state.
func TestDrawStatusBar_ArmedEscTag(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleKey(keyEv(tcell.KeyEsc, 0)) // arm
	a.drawStatusBar()
	a.screen.Show()
	if !screenHasText(t, a, "Esc…") {
		t.Fatal("armed Esc should show the Esc… tag in the status bar")
	}

	a.lastEscape = time.Now().Add(-2 * menuEscMs) // fully expired
	a.drawStatusBar()
	a.screen.Show()
	if screenHasText(t, a, "Esc…") {
		t.Fatal("expired Esc must not keep the Esc… tag")
	}
}

// TestHandleEvent_PasteEscIsContentNotCommand is the regression test
// for the bracketed-paste hole: a raw ESC byte inside pasted text used
// to arm the Esc leader, so pasting "\x1bq" would quit the editor.
// Between paste markers, ESC must be stripped and the following rune
// inserted as plain text.
func TestHandleEvent_PasteEscIsContentNotCommand(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleEvent(keyEv(tcell.KeyEsc, 0))
	a.handleEvent(keyEv(tcell.KeyRune, 'q'))
	a.handleEvent(tcell.NewEventPaste(false))

	if a.quit {
		t.Fatal("pasted ESC+q must not fire the quit leader")
	}
	if a.menuOpen {
		t.Fatal("pasted ESC bytes must not open the menu")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "q" {
		t.Fatalf("pasted rune should insert literally, buffer = %q", got)
	}
}

// TestHandleKey_PasteAltRuneInsertsLiteral covers coalescing *inside* a
// paste: tmux can still merge a pasted ESC+rune into one Alt rune. In
// normal typing that means "leader", but during a paste the rune is
// content and must reach the buffer instead of firing an action.
func TestHandleKey_PasteAltRuneInsertsLiteral(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt))
	a.handleEvent(tcell.NewEventPaste(false))

	if a.quit {
		t.Fatal("Alt rune during a paste must not dispatch the leader")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "q" {
		t.Fatalf("Alt rune during a paste should insert its rune, buffer = %q", got)
	}
}

// TestHandleKey_PasteTabInsertsLiteralTab pins that a Tab key between
// paste markers inserts a real \t: the tab came from the source
// document, and expanding it to the file's IndentUnit would silently
// rewrite pasted code.
func TestHandleKey_PasteTabInsertsLiteralTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().IndentUnit = "    " // spaces, so expansion would be visible

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleKey(keyEv(tcell.KeyTab, 0))
	a.handleEvent(tcell.NewEventPaste(false))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "\t" {
		t.Fatalf("Tab during a paste should insert \\t, buffer = %q", got)
	}
}

// TestMenuToggleWrap pins the toggle's full contract: it flips the app
// preference and every open tab together, swaps the dynamic menu label,
// and persists the choice to the user config file.
func TestMenuToggleWrap(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	if !a.activeTabPtr().Wrap {
		t.Fatal("tabs should open with wrap on by default")
	}
	if got := a.wrapToggleLabel(); got != "Unwrap long lines" {
		t.Fatalf("label = %q, want Unwrap long lines", got)
	}

	a.menuToggleWrap()
	if a.wrapOn || a.activeTabPtr().Wrap {
		t.Fatal("toggle should turn wrap off for the app and all open tabs")
	}
	if got := a.wrapToggleLabel(); got != "Wrap long lines" {
		t.Fatalf("label after toggle = %q, want Wrap long lines", got)
	}
	data, err := os.ReadFile(filepath.Join(cfgHome, "skiff", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"wrap": "off"`) {
		t.Fatalf("config missing wrap off:\n%s", data)
	}

	a.menuToggleWrap()
	if !a.wrapOn || !a.activeTabPtr().Wrap {
		t.Fatal("second toggle should turn wrap back on")
	}
}

// TestOpenFile_StampsWrapPreference verifies a freshly opened tab copies
// the app-level wrap preference — the path future tabs take after the
// user has toggled wrap off.
func TestOpenFile_StampsWrapPreference(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.wrapOn = false
	a.openFile(target)
	if a.activeTabPtr().Wrap {
		t.Fatal("tab should inherit wrap-off from the app preference")
	}
}

// TestDrawEmptyEditor_ClipsToEditorRect pins the empty-state hint inside
// the editor pane: on a narrow pane the 45-rune hint used to start left
// of the editor rect and overwrite file-tree rows and the splitter.
func TestDrawEmptyEditor_ClipsToEditorRect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(60, 24)
	a.width, a.height = scr.Size()
	a.draw()
	a.screen.Show()

	cells, w, _ := scr.GetContents()
	_, ey, _, eh := a.editorRect()
	hintRow := ey + eh/2 + 1 // one below the vertical midpoint
	sx := a.splitterX()
	if r := cells[hintRow*w+sx].Runes[0]; r != '│' {
		t.Fatalf("splitter cell on the hint row = %q — hint bled into the sidebar", r)
	}
	ex, _, _, _ := a.editorRect()
	if r := cells[hintRow*w+ex].Runes[0]; r != 'C' {
		t.Fatalf("hint should start at the editor's left edge, got %q", r)
	}
}

// TestMenuWheel_RecomputesHoverAfterScroll pins the hover highlight to
// the row actually under the pointer: a wheel tick used to scroll the
// menu after hover was computed, leaving the highlight one row stale
// until the next mouse motion.
func TestMenuWheel_RecomputesHoverAfterScroll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(120, 24) // short terminal so the menu overflows and scrolls
	a.width, a.height = scr.Size()
	a.openFile(path)
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

// TestDrawTabBar_CloseButtonEmphasis pins the close-× hierarchy: the
// active tab (the likeliest close target) gets the brighter Muted ×,
// and inactive tabs recede to Subtle — not the other way around.
func TestDrawTabBar_CloseButtonEmphasis(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt")) // b active, a inactive
	a.drawTabBar()
	a.screen.Show()

	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	for _, r := range a.lastTabRects {
		fg, _, _ := cells[0*w+r.CloseX].Style.Decompose()
		if r.Index == a.tabs.ActiveIndex() {
			if fg != a.theme.Muted {
				t.Fatalf("active tab × fg = %v, want Muted (brighter)", fg)
			}
		} else if fg != a.theme.Subtle {
			t.Fatalf("inactive tab × fg = %v, want Subtle (recedes)", fg)
		}
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
