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

	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/theme"
)

// TestMain redirects every XDG base directory to a throwaway root before
// any test runs. App tests exercise paths that persist sessions and config
// (closeTab's deferred saveSession, quit flows); without this, each run
// wrote temp-project sessions into the developer's real
// ~/.local/state/skiff/sessions/ and the 50-file prune evicted their real
// project sessions. Per-test t.Setenv overrides still compose on top.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skiff-test-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	// newTestApp constructs through the production newApp core, which
	// loads the user config. Pin icons to "off" in the throwaway config:
	// the default ("auto") shells out to Nerd Font detection for every
	// constructed app, and would make glyph-level draw assertions depend
	// on the fonts installed on whatever machine runs the suite. "off"
	// is also exactly what the old hand-rolled test literal produced.
	cfgDir := filepath.Join(dir, "config", "skiff")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		panic(err)
	}
	cfg := []byte("{\"icons\":\"off\"}\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), cfg, 0o644); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newTestApp builds a fully-wired App against a tcell.SimulationScreen,
// through the same newApp core production runs — so tests see the real
// baseline mouseFlags, the user config, and custom actions (TestMain
// points XDG at a throwaway dir, so both loaders read test-owned state,
// never the developer's). It still skips New's tail on purpose: no
// welcome flash, no startTreeRefresh ticker, no finder index build, and
// no restoreSession — background goroutines and persisted sessions have
// no place under `go test`.
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
	a := newApp(scr, tree.Root.Path, tree, true)
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

// TestNewApp_SharedShape pins the construction core every mode shares:
// the sentinels, the baseline mouse flags, the wrap default, and the
// active-folder anchor. These fields used to be hand-copied across three
// constructors and drifted (the test constructor shipped without
// mouseFlags, so 38 test files validated an App shape production never
// has); newApp is now the single place they are set.
func TestNewApp_SharedShape(t *testing.T) {
	dir := t.TempDir()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { scr.Fini() })

	tree, err := filetree.New(dir)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	a := newApp(scr, tree.Root.Path, tree, true)
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("mouseFlags = %v, want baseline %v", a.mouseFlags, mouseBaseFlags)
	}
	if a.hoveredMenuRow != -1 || a.diffPanelRow != -1 {
		t.Fatalf("sentinels not seeded: hoveredMenuRow=%d diffPanelRow=%d",
			a.hoveredMenuRow, a.diffPanelRow)
	}
	if !a.wrapOn {
		t.Fatal("wrapOn should default to true")
	}
	if a.sidebarWidth != defaultSidebarWidth {
		t.Fatalf("sidebarWidth = %d, want %d", a.sidebarWidth, defaultSidebarWidth)
	}
	if !a.sidebarShown {
		t.Fatal("tree mode should show the sidebar when asked")
	}
	if a.activeFolder != tree.Root.Path {
		t.Fatalf("activeFolder = %q, want tree root %q", a.activeFolder, tree.Root.Path)
	}

	// The tree-less path anchors the active folder on rootDir instead.
	b := newApp(scr, dir, nil, false)
	if b.tree != nil || b.sidebarShown {
		t.Fatal("tree-less mode should have no tree and a hidden sidebar")
	}
	if b.activeFolder != dir {
		t.Fatalf("activeFolder = %q, want rootDir %q", b.activeFolder, dir)
	}
}

// TestNewSingleFileApp_ShapeInvariants pins the single-file mode
// contract: no tree, no sidebar, no finder, and the file open in a tab —
// the state the production tree==nil guard sites (openFinder,
// menuToggleSidebar, the gitstatus tree tint, ...) exist to handle,
// previously reachable in tests only by hand-patching tree=nil onto a
// tree-backed app that still had a sidebar and a finder.
func TestNewSingleFileApp_ShapeInvariants(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "solo.txt")
	if err := os.WriteFile(target, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { scr.Fini() })
	scr.SetSize(120, 40)

	a := newSingleFileApp(scr, target)

	if a.tree != nil {
		t.Fatal("single-file mode must not build a tree")
	}
	if a.sidebarShown {
		t.Fatal("single-file mode must hide the sidebar")
	}
	if a.finder != nil {
		t.Fatal("single-file mode must not build a project index")
	}
	if a.hasTree() {
		t.Fatal("hasTree must report false so tree-dependent menu rows hide")
	}
	if got := a.tabs.Len(); got != 1 {
		t.Fatalf("tabs = %d, want exactly the one file", got)
	}
	if got := a.tabs.Tabs()[0].Path; got != target {
		t.Fatalf("tab path = %q, want %q", got, target)
	}
	wantRoot, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if a.rootDir != wantRoot {
		t.Fatalf("rootDir = %q, want the file's parent %q", a.rootDir, wantRoot)
	}

	// One guarded behavior end to end: the finder opener must decline —
	// flash, no modal — instead of popping an always-empty picker.
	a.openFinder()
	if a.anyModalOpen() {
		t.Fatal("openFinder must not open a modal in single-file mode")
	}
	if !strings.Contains(a.statusMsg, "single-file") {
		t.Fatalf("openFinder should explain itself, statusMsg = %q", a.statusMsg)
	}
}

// TestRun_DrainsBurstAndExits drives the real event loop end to end: a
// queued wheel burst must be fully applied (the drain loop's whole
// reason to exist is one draw per burst, not one per event), and the
// Esc-leader quit must terminate the loop through the same dispatch a
// user's keystrokes take. The trash teardown is asserted through the
// Run→Close composition main.go actually runs — the trash discard
// moved from Run's tail into Close so signal quits and recovered
// panics empty it too, and TestClose_EmptiesTrash pins Close's half
// directly.
func TestRun_DrainsBurstAndExits(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("line\n", 200)), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	doomed := filepath.Join(dir, "doomed.txt")
	if err := os.WriteFile(doomed, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, dir)
	scr, ok := a.screen.(tcell.SimulationScreen)
	if !ok {
		t.Fatal("newTestApp must build on a SimulationScreen")
	}
	a.openFile(big)
	if _, err := a.files.Trash(doomed); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if !a.files.HasTrash() {
		t.Fatal("trash entry should exist before Run")
	}

	// Quiesce before injecting: openFile kicks an async git refresh
	// whose completion event would otherwise share the simulation
	// screen's 10-slot queue with the burst below (postEvent blocks
	// when it is full).
	deadline := time.Now().Add(3 * time.Second)
	for (a.gitRefreshInFlight || scr.HasPendingEvent()) && time.Now().Before(deadline) {
		if scr.HasPendingEvent() {
			a.handleEvent(scr.PollEvent())
		} else {
			time.Sleep(2 * time.Millisecond)
		}
	}

	// Queue the whole session up front: a wheel burst in the editor pane,
	// then the two-keystroke Esc-leader quit. 7 events stay safely under
	// the queue's 10-event capacity.
	const burst = 5
	x, y := a.sidebarW()+10, 5
	for i := 0; i < burst; i++ {
		scr.InjectMouse(x, y, tcell.WheelDown, tcell.ModNone)
	}
	scr.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	scr.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run hung: Esc q never terminated the loop (blocked in PollEvent?)")
	}

	if !a.quit {
		t.Fatal("Run returned without quit set")
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no active tab after Run")
	}
	if want := burst * wheelLines; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want the full burst %d", tab.ScrollY, want)
	}

	// main.go defers Close around Run; the composed exit path is what
	// discards the delete-undo window.
	a.Close()
	if a.files.HasTrash() {
		t.Fatal("Run+Close should have emptied the trash")
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

// TestRelativeFolderLabel covers all three branches: the project root
// (basename plus separator), a subdirectory (path relative to the root),
// and a folder filepath.Rel cannot relate to the root at all, which has to
// degrade to the folder's own basename rather than leak a broken path into
// the New File hint.
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

	// Non-relatable → filepath.Rel refuses to relate a relative folder
	// to the absolute root, so the label falls back to the basename.
	oddLabel := a.relativeFolderLabel(filepath.Join("not-under", "the-root"))
	if want := "the-root" + string(filepath.Separator); oddLabel != want {
		t.Fatalf("non-relatable label: got %q, want %q", oddLabel, want)
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

// menuItemByLabel finds a menu row by static label for tests that care about
// one action without hard-coding its row index. Drill-in rows (the git
// verbs, the file-clipboard actions) are searched too: demoting a row
// into a submenu doesn't stop it being a menu action, so tests keep
// asserting on labels rather than on which level a row lives at.
func menuItemByLabel(t *testing.T, a *App, label string) menuItemDef {
	t.Helper()
	items, _, _ := a.menuLayout()
	for _, item := range items {
		if item.label == label {
			return item
		}
	}
	for _, d := range menuDrillIns() {
		for _, item := range d.items {
			if item.label == label {
				return item
			}
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

// lowColorScreen wraps a SimulationScreen and lies about its colour
// count. tcell's simulation screen always reports 256, so this is the
// only way to exercise the degraded-palette path without a real
// 16-colour terminal.
type lowColorScreen struct {
	tcell.SimulationScreen
	colors int
}

// Colors reports the fabricated depth applyColorDepth reads.
func (s *lowColorScreen) Colors() int { return s.colors }

// TestApplyColorDepth_DegradesBelow256 pins the low-colour fallback
// wiring. theme.Degrade existed but nothing called it, so on a plain
// TERM=xterm every gray in the palette collapsed onto one ANSI colour
// and the status bar, the selection, and the active tab stopped being
// distinguishable. The App has to ask the screen and act on the answer.
func TestApplyColorDepth_DegradesBelow256(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sim := a.screen.(tcell.SimulationScreen)
	authored := a.theme

	a.screen = &lowColorScreen{SimulationScreen: sim, colors: 16}
	a.applyColorDepth()

	if a.screenColors != 16 {
		t.Fatalf("screenColors = %d, want 16", a.screenColors)
	}
	if !a.theme.LowColor {
		t.Fatal("a 16-colour terminal should get the degraded palette")
	}
	if a.theme.Attrs.StatusBar == 0 || a.theme.Attrs.ActiveTab == 0 {
		t.Fatalf("degraded palette must carry attribute fallbacks: %+v", a.theme.Attrs)
	}
	if a.theme == authored {
		t.Fatal("palette was left untouched")
	}
	if a.theme != theme.Degrade(authored, 16) {
		t.Fatal("live palette should equal theme.Degrade of the authored one")
	}
}

// TestApplyColorDepth_TruecolorIsUntouched is the no-cost half: at 256
// colours or better the authored palette must survive byte-for-byte,
// including the zero Attrs that let every draw site skip a branch.
func TestApplyColorDepth_TruecolorIsUntouched(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	authored := a.theme

	a.applyColorDepth() // SimulationScreen reports 256

	if a.theme != authored {
		t.Fatal("a 256-colour terminal must render the palette as authored")
	}
	if a.theme.LowColor || a.theme.Attrs != (theme.Attrs{}) {
		t.Fatalf("truecolor palette picked up degrade state: %+v", a.theme.Attrs)
	}
}

// TestApplyTheme_KeepsDegradeOnLowColor guards the mid-session path: the
// registry always hands back the authored 24-bit palette, so a theme
// switch on a 16-colour terminal would silently undo the startup
// degrade if applyTheme didn't re-run it.
func TestApplyTheme_KeepsDegradeOnLowColor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.screen = &lowColorScreen{SimulationScreen: a.screen.(tcell.SimulationScreen), colors: 8}
	a.applyColorDepth()

	var other string
	for _, e := range theme.List() {
		if e.ID != a.currentThemeID() {
			other = e.ID
			break
		}
	}
	if other == "" {
		t.Skip("theme registry has only one entry")
	}
	a.applyTheme(other, false)

	if !a.theme.LowColor {
		t.Fatalf("switching to %q dropped the low-colour fallback", other)
	}
}

// resizeApp drives a real terminal resize through the event loop's own
// path — SetSize then EventResize — so the responsive-sidebar rule is
// exercised the way tcell delivers it rather than by poking fields.
func resizeApp(t *testing.T, a *App, w, h int) {
	t.Helper()
	a.screen.(tcell.SimulationScreen).SetSize(w, h)
	a.handleEvent(tcell.NewEventResize(w, h))
}

// TestApplyResponsiveSidebar_HidesWhenNarrowRestoresWhenWide is the
// headline behavior: a tmux split dragged under autoHideSidebarWidth
// gives the whole window to the editor, says why, and hands the tree
// back when the pane grows again. Asserted against the painted screen,
// not just the flag, because the point is that the tree stops taking
// columns.
func TestApplyResponsiveSidebar_HidesWhenNarrowRestoresWhenWide(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sidebarfile.go"), []byte("package p\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.refreshTree()

	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, "sidebarfile.go") {
		t.Fatal("precondition: the tree should be visible on a wide terminal")
	}

	resizeApp(t, a, autoHideSidebarWidth-1, 30)

	if a.sidebarShown {
		t.Error("the explorer should collapse below autoHideSidebarWidth")
	}
	if !a.sidebarAutoHidden {
		t.Error("the collapse must be recorded as automatic so it can be undone")
	}
	if !strings.Contains(a.statusMsg, "file explorer") {
		t.Errorf("a silent collapse looks like a bug; flash was %q", a.statusMsg)
	}
	a.draw()
	a.screen.Show()
	if screenHasText(t, a, "sidebarfile.go") {
		t.Error("the tree is still painting after the auto-collapse")
	}

	resizeApp(t, a, 100, 30)

	if !a.sidebarShown {
		t.Error("growing back past the threshold should restore the explorer")
	}
	if a.sidebarAutoHidden {
		t.Error("the auto-hidden flag must clear once the panel is back")
	}
	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, "sidebarfile.go") {
		t.Error("the tree never came back")
	}
}

// TestApplyResponsiveSidebar_LeavesUserHiddenPanelAlone is the rule that
// keeps the automatic behavior from overriding a decision: a panel the
// user closed from the ≡ menu stays closed however wide the terminal
// gets. Tracking auto-hidden separately from hidden is what buys this.
func TestApplyResponsiveSidebar_LeavesUserHiddenPanelAlone(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.menuToggleSidebar() // explicit hide, on a wide terminal
	if a.sidebarShown {
		t.Fatal("precondition: the toggle should have hidden the panel")
	}

	resizeApp(t, a, autoHideSidebarWidth-1, 30)
	if a.sidebarAutoHidden {
		t.Error("narrowing must not claim credit for a panel the user hid")
	}

	resizeApp(t, a, 120, 40)
	if a.sidebarShown {
		t.Fatal("widening reopened a panel the user closed on purpose")
	}
}

// TestApplyResponsiveSidebar_ExplicitHideRetiresPendingRestore is the
// case that actually needs menuToggleSidebar to clear the auto-hidden
// flag. The panel collapses on its own, the user brings it back, then
// changes their mind and closes it — the pending automatic restore from
// the first step must be retired, or widening the terminal reopens a
// panel the user shut two keystrokes ago.
func TestApplyResponsiveSidebar_ExplicitHideRetiresPendingRestore(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	resizeApp(t, a, autoHideSidebarWidth-1, 30)
	if !a.sidebarAutoHidden {
		t.Fatal("precondition: the panel should have auto-collapsed")
	}

	a.menuToggleSidebar() // show
	a.menuToggleSidebar() // and hide again, deliberately
	if a.sidebarAutoHidden {
		t.Error("an explicit toggle must retire the pending automatic restore")
	}

	resizeApp(t, a, 120, 40)
	if a.sidebarShown {
		t.Fatal("widening reopened a panel the user had just closed")
	}
}

// TestApplyResponsiveSidebar_ExplicitShowSurvivesNarrowResizes covers the
// other half of "never fight the user": someone who reopens the explorer
// with Esc t inside a cramped pane wants it there. Because the rule is
// edge-triggered on the threshold, later resizes that stay narrow leave
// it alone instead of re-hiding it on every drag tick.
func TestApplyResponsiveSidebar_ExplicitShowSurvivesNarrowResizes(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	resizeApp(t, a, autoHideSidebarWidth-1, 30)
	if a.sidebarShown {
		t.Fatal("precondition: the panel should have auto-collapsed")
	}

	a.menuToggleSidebar() // "no, I want it"
	resizeApp(t, a, autoHideSidebarWidth-6, 30)
	if !a.sidebarShown {
		t.Error("a still-narrow resize re-hid a panel the user just reopened")
	}

	resizeApp(t, a, 120, 40)
	if !a.sidebarShown {
		t.Error("widening should have left the user's panel alone")
	}
}

// TestApplyResponsiveSidebar_SingleFileModeIsInert pins the guard for
// the mode with no tree at all: there is no panel crowding the editor,
// so narrowing must change nothing and must not flash an explanation
// for a panel the session never had.
func TestApplyResponsiveSidebar_SingleFileModeIsInert(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.sidebarShown = false
	a.statusMsg = ""

	resizeApp(t, a, autoHideSidebarWidth-1, 30)

	if a.sidebarShown || a.sidebarAutoHidden {
		t.Errorf("single-file mode touched sidebar state: shown=%v auto=%v", a.sidebarShown, a.sidebarAutoHidden)
	}
	if a.statusMsg != "" {
		t.Errorf("flashed %q about a panel that does not exist", a.statusMsg)
	}
}
