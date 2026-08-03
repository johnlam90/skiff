// =============================================================================
// File: internal/app/finder_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/finder"
)

// finderOv returns the open finder overlay, failing the test when none
// is up.
func finderOv(t *testing.T, a *App) *finderOverlay {
	t.Helper()
	fo, ok := a.overlays.Top().(*finderOverlay)
	if !ok {
		t.Fatalf("no finder overlay open; top = %T", a.overlays.Top())
	}
	return fo
}

// finderIsOpen reports whether the finder overlay is up.
func finderIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*finderOverlay)
	return ok
}

// waitForFinderReady spins until the App's finder reports
// StateReady or the timeout expires. Pulled out so each test
// can read as the scenario it's pinning down rather than the
// goroutine-sync boilerplate.
func waitForFinderReady(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.finder != nil && a.finder.State() == finder.StateReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("finder did not reach StateReady within 2s")
}

// withFinder wires up an App + an indexed Finder rooted at a
// tempdir we seed with a few files. Tests use it as the entry
// point so they don't repeat the setup chain.
func withFinder(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	files := []string{
		"main.go",
		"internal/app/app.go",
		"internal/app/tab.go",
		"internal/finder/score.go",
		"README.md",
	}
	for _, f := range files {
		abs := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.finder = finder.New(a.rootDir)
	a.finder.Rebuild(nil)
	waitForFinderReady(t, a)
	return a, dir
}

// TestOpenFinder_PopulatesResults pins the central wiring: opening
// the finder with a warm index immediately fills finderResults so
// the first frame shows real paths, not "Indexing…". Without this
// the user would see an empty list and worry the feature broke.
func TestOpenFinder_PopulatesResults(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()

	if !finderIsOpen(a) {
		t.Fatal("finderOpen should be true after openFinder")
	}
	if len(finderOv(t, a).results) == 0 {
		t.Fatal("expected initial result list (empty query → alphabetical)")
	}
}

// TestFinderKey_TypingFiltersResults walks the keystroke loop:
// typing "tab" narrows the list to paths matching that query, and
// the highlighted match should bubble tab.go to the top.
func TestFinderKey_TypingFiltersResults(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	for _, r := range "tab" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}

	if len(finderOv(t, a).results) == 0 {
		t.Fatal("expected results after typing query")
	}
	if !endsWith(finderOv(t, a).results[0].Path, "tab.go") {
		t.Fatalf("top result: got %q, want ends-with tab.go", finderOv(t, a).results[0].Path)
	}
}

// TestFinderKey_BackspaceShrinksQuery checks the inverse: deleting
// characters re-broadens the result set. Catches a regression
// where backspace edits the query but forgets to rerun the search.
func TestFinderKey_BackspaceShrinksQuery(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	for _, r := range "score" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	narrow := len(finderOv(t, a).results)

	// Backspace twice — the query becomes "sco", broader match.
	a.handleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))

	if len(finderOv(t, a).results) < narrow {
		t.Fatalf("backspace should not shrink results: was %d, now %d", narrow, len(finderOv(t, a).results))
	}
	if got := finderOv(t, a).query.Text(); got != "sco" {
		t.Fatalf("query: got %q, want %q", got, "sco")
	}
}

// TestFinderKey_ArrowsMoveSelection pins the navigation contract:
// ↓ moves to the next row, ↑ moves back, neither runs off the end.
// A regression here would let Enter open the wrong file.
func TestFinderKey_ArrowsMoveSelection(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()

	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if finderOv(t, a).selected != 1 {
		t.Fatalf("selected after ↓: got %d, want 1", finderOv(t, a).selected)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if finderOv(t, a).selected != 0 {
		t.Fatalf("selected after ↑: got %d, want 0", finderOv(t, a).selected)
	}
	// ↑ at the top stays put.
	a.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if finderOv(t, a).selected != 0 {
		t.Fatalf("selected at top after ↑: got %d, want 0", finderOv(t, a).selected)
	}
}

// TestFinderKey_EnterOpensFile is the headline outcome: pressing
// Enter on a result actually opens that file as a tab and
// dismisses the modal. Without this the whole feature is nothing
// but a viewer.
func TestFinderKey_EnterOpensFile(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinder()
	for _, r := range "score" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if len(finderOv(t, a).results) == 0 {
		t.Fatal("expected score results")
	}
	want := filepath.Join(dir, finderOv(t, a).results[0].Path)

	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if finderIsOpen(a) {
		t.Fatal("modal should close after Enter")
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected an active tab after opening result")
	}
	if tab.Path != want {
		t.Fatalf("opened path: got %q, want %q", tab.Path, want)
	}
}

// TestFinderKey_EscClosesModal pins the cancel path: Esc must
// dismiss without opening anything, and clear transient state so
// reopening starts fresh (no stale query, no stale selection).
func TestFinderKey_EscClosesModal(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if finderIsOpen(a) {
		t.Fatal("Esc should close the finder")
	}
	// The overlay is gone entirely — its state dies with it, so there
	// is no query left to leak into a future open.
}

// TestFinderMouse_ClickOpensRow walks the click path: clicking on
// a result row both selects it and opens the file. We use the
// modal rect from the same helper the renderer uses so the row
// math stays in sync if either side changes.
func TestFinderMouse_ClickOpensRow(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinder()
	if len(finderOv(t, a).results) < 2 {
		t.Fatalf("need at least 2 results for click test, got %d", len(finderOv(t, a).results))
	}
	fr := finderOv(t, a).rect()
	mx, my := fr.X, fr.Y
	target := filepath.Join(dir, finderOv(t, a).results[1].Path)

	finderOv(t, a).HandleMouse(mx+5, my+4+1, tcell.Button1)

	if finderIsOpen(a) {
		t.Fatal("modal should close after click-open")
	}
	if got := a.activeTabPtr().Path; got != target {
		t.Fatalf("opened path: got %q, want %q", got, target)
	}
}

// TestFinderMouse_ClickOutsideCloses guards the dismiss path: a
// click that lands outside the modal should close it without
// opening anything. Otherwise a stray click could leave a tab
// open the user didn't ask for.
func TestFinderMouse_ClickOutsideCloses(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	tabsBefore := len(a.tabs)

	finderOv(t, a).HandleMouse(0, 0, tcell.Button1)

	if finderIsOpen(a) {
		t.Fatal("modal should close on outside click")
	}
	if len(a.tabs) != tabsBefore {
		t.Fatalf("tab count changed unexpectedly: %d → %d", tabsBefore, len(a.tabs))
	}
}

// TestLeader_PFiresFinder pins the Esc-p binding so a future
// refactor of leader.go can't quietly drop it.
func TestLeader_PFiresFinder(t *testing.T) {
	a, _ := withFinder(t)
	if action := leaderActionFor('p'); action == nil {
		t.Fatal("Esc-p has no leader binding")
	} else {
		action(a)
	}
	if !finderIsOpen(a) {
		t.Fatal("Esc-p should open the finder")
	}
}

// TestFinder_EnterRevealsFileInTree is the headline fix for the sidebar-sync
// bug: opening a file via the finder (Esc-p → type → Enter) used to set the
// active-file highlight on a row nobody could see, because the tree stayed
// collapsed at the top. After the fix, openFile calls tree.Reveal, which
// expands every ancestor and scrolls the row into view. This test opens a
// nested file under internal/finder/ through the finder keystroke loop and
// asserts both that the ancestor dir is expanded and that the file's row is
// inside the tree's viewport (via the public HitTest contract).
func TestFinder_EnterRevealsFileInTree(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinder()
	for _, r := range "score" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if len(finderOv(t, a).results) == 0 {
		t.Fatal("expected score results")
	}
	rel := finderOv(t, a).results[0].Path
	want := filepath.Join(dir, rel)

	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	// The finder returns paths like "internal/finder/score.go" — so the
	// "internal" ancestor must now be expanded, and the file's row must be
	// inside the tree's viewport.
	internal := treeChildByName(a.tree.Root, "internal")
	if internal == nil {
		t.Fatal("internal/ ancestor missing from tree")
	}
	if !internal.Expanded {
		t.Fatal("internal/ should be expanded after opening via finder")
	}
	if a.tree.ActiveFile != want {
		t.Fatalf("ActiveFile: got %q, want %q", a.tree.ActiveFile, want)
	}
	// Re-render so the tree's visible-rows cache reflects the post-reveal
	// flat list, then walk the list rows via HitTest and confirm the file
	// is on screen. Using the public HitTest contract keeps the test honest
	// about what a user would actually see.
	sx, sy, sw, sh := a.sidebarRect()
	a.tree.Render(a.screen, a.theme, sx, sy, sw, sh)
	listH := sh - 2
	if listH < 0 {
		listH = 0
	}
	found := false
	for row := 0; row < listH; row++ {
		n, ok := a.tree.HitTest(0, row+2) // list rows start at localY 2
		if !ok || n == nil {
			continue
		}
		if n.Path == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("opened file %q not visible in tree after reveal (ScrollY=%d)", rel, a.tree.ScrollY)
	}
}

// treeChildByName returns the direct child of n named name, or nil. A tiny
// local helper so the finder-reveal test can inspect ancestor expansion
// without reaching into the package's private fields.
func treeChildByName(n *filetree.Node, name string) *filetree.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// endsWith is a tiny string suffix check pulled in so the result-
// path assertions in this file read as the rule they're enforcing.
func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// TestOpenFinder_NoOpInSingleFileMode pins the single-file-mode guard: the
// Esc-p leader reaches openFinder directly (unlike the menu row, which is
// gated by hasTree), so in single-file mode (tree == nil) openFinder must
// not pop an always-empty modal — it flashes an explanation and leaves
// finderOpen false instead.
func TestOpenFinder_NoOpInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil   // single-file mode: no project tree...
	a.finder = nil // ...and no project index

	a.openFinder()

	if finderIsOpen(a) {
		t.Fatal("openFinder should not open the modal when finder is nil")
	}
	if a.statusMsg == "" {
		t.Fatal("expected a flash explaining the finder is unavailable")
	}
}
