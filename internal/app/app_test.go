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
