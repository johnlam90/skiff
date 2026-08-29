// =============================================================================
// File: internal/filetree/filetree_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the filetree package — the lazy file explorer that powers the
// editor's left sidebar. These pin down disk-merge behavior (refresh keeps
// expanded folders open), the small visibility/hide rules, the flatten +
// hit-test math, and a handful of render assertions made via tcell's
// SimulationScreen so we can verify chevrons, the bold active row, etc.

package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// mkTree is a tiny helper that builds a small directory layout under t.TempDir
// and returns the absolute root path. Several tests use the same shape so
// pulling it into a helper keeps each test focused on the behavior it cares
// about rather than scaffolding.
func mkTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "alpha"))
	mustMkdir(t, filepath.Join(root, "Beta"))
	mustMkdir(t, filepath.Join(root, ".git")) // hidden — should be filtered
	mustMkdir(t, filepath.Join(root, "node_modules"))
	mustWrite(t, filepath.Join(root, "zeta.txt"), "z")
	mustWrite(t, filepath.Join(root, "Apple.md"), "a")
	mustWrite(t, filepath.Join(root, ".DS_Store"), "junk")
	mustWrite(t, filepath.Join(root, "alpha", "inner.go"), "package x")
	return root
}

// mustMkdir is a fail-on-error mkdir helper for test setup.
func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

// mustWrite is a fail-on-error file-write helper for test setup.
func mustWrite(t *testing.T, p, contents string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// findChild walks a node's children for an entry named name. Returns nil
// when not present so tests can assert absence as well as presence.
func findChild(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestNew_NonExistentRoot verifies that pointing the tree at a path that
// doesn't exist surfaces an error rather than panicking or producing an
// empty tree (which would silently mislead the user).
func TestNew_NonExistentRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := New(missing); err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

// TestNew_RootIsFile guards the "user passed a filename, not a folder" case.
// The constructor should reject it instead of trying to read children.
func TestNew_RootIsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	mustWrite(t, f, "hi")
	if _, err := New(f); err == nil {
		t.Fatal("expected error when root is a regular file")
	}
}

// TestNew_LoadsAndHides confirms a successful build returns a tree whose
// root is expanded, has its children loaded, and excludes the well-known
// noise entries (.git, node_modules, .DS_Store).
func TestNew_LoadsAndHides(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.Root.IsDir || !tr.Root.Expanded || !tr.Root.Loaded {
		t.Fatalf("root flags wrong: %+v", tr.Root)
	}
	for _, hidden := range []string{".git", ".DS_Store", "node_modules"} {
		if findChild(tr.Root, hidden) != nil {
			t.Fatalf("hidden entry %s should have been filtered", hidden)
		}
	}
	// Sanity: visible names ARE present.
	for _, want := range []string{"alpha", "Beta", "zeta.txt", "Apple.md"} {
		if findChild(tr.Root, want) == nil {
			t.Fatalf("expected child %s to be present", want)
		}
	}
}

// TestLoadChildren_SortOrder asserts directories sort before files and that
// each group is case-insensitive alphabetical — what users expect from a
// VSCode-style sidebar.
func TestLoadChildren_SortOrder(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names := make([]string, 0, len(tr.Root.Children))
	for _, c := range tr.Root.Children {
		names = append(names, c.Name)
	}
	// Expected: alpha, Beta (dirs alpha-by-lower), then Apple.md, zeta.txt.
	want := []string{"alpha", "Beta", "Apple.md", "zeta.txt"}
	if len(names) != len(want) {
		t.Fatalf("child count mismatch: got %v want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("sort mismatch at %d: got %q want %q (full=%v)", i, names[i], n, names)
		}
	}
}

// TestRefresh_PreservesExpandedState verifies that refreshing the tree
// after files appear or vanish on disk keeps the *Node pointers (and
// their Expanded flag) intact for entries that still exist — important
// because the 10-second auto-refresh would otherwise collapse every
// folder the user had opened.
func TestRefresh_PreservesExpandedState(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if alpha == nil {
		t.Fatal("alpha missing")
	}
	tr.Toggle(alpha) // expand + load
	if !alpha.Expanded || !alpha.Loaded {
		t.Fatalf("alpha state after toggle wrong: %+v", alpha)
	}

	// Mutate disk: add a new sibling, remove zeta.txt.
	mustWrite(t, filepath.Join(root, "Newcomer.txt"), "n")
	if err := os.Remove(filepath.Join(root, "zeta.txt")); err != nil {
		t.Fatalf("remove zeta: %v", err)
	}

	tr.Refresh()

	// Pointer identity preserved for survivors.
	alphaAfter := findChild(tr.Root, "alpha")
	if alphaAfter != alpha {
		t.Fatal("alpha pointer changed across refresh")
	}
	if !alphaAfter.Expanded {
		t.Fatal("alpha.Expanded was lost across refresh")
	}
	// New file appears.
	if findChild(tr.Root, "Newcomer.txt") == nil {
		t.Fatal("Newcomer.txt should have been picked up")
	}
	// Deleted file vanished.
	if findChild(tr.Root, "zeta.txt") != nil {
		t.Fatal("zeta.txt should have been removed from the tree")
	}
}

// TestLoadedDirs_OnlyLoadedDirectories pins the work list a background
// sweep gets: every directory the tree has actually read, and nothing
// else. An unexpanded folder must stay off it — re-reading directories
// the user has never opened is exactly the cost the lazy tree exists to
// avoid, and hidden names never become nodes at all.
func TestLoadedDirs_OnlyLoadedDirectories(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := tr.LoadedDirs(); len(got) != 1 || got[0] != tr.Root.Path {
		t.Fatalf("fresh tree should list only the root, got %v", got)
	}

	alpha := findChild(tr.Root, "alpha")
	if alpha == nil {
		t.Fatal("alpha missing")
	}
	tr.Toggle(alpha) // expand + load

	got := tr.LoadedDirs()
	if len(got) != 2 || got[0] != tr.Root.Path || got[1] != alpha.Path {
		t.Fatalf("expanded alpha should join the list depth-first, got %v", got)
	}
	// Beta was never opened, so its contents are nobody's business yet.
	for _, p := range got {
		if strings.HasSuffix(p, "Beta") {
			t.Fatalf("unexpanded Beta must not be scanned: %v", got)
		}
	}
}

// TestScanDirsApplyScan_MatchesRefresh is the equivalence contract for
// the split refresh: reading the disk off-thread (ScanDirs) and merging
// on the main thread (ApplyScan) must land the tree in the same place
// Refresh does, pointer identity and Expanded state included. That
// equivalence is the whole reason the 10-second tick can stop doing its
// ReadDir walk on the event thread.
func TestScanDirsApplyScan_MatchesRefresh(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if alpha == nil {
		t.Fatal("alpha missing")
	}
	tr.Toggle(alpha)
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("inner.go missing")
	}

	mustWrite(t, filepath.Join(root, "Newcomer.txt"), "n")
	if err := os.Remove(filepath.Join(root, "zeta.txt")); err != nil {
		t.Fatalf("remove zeta: %v", err)
	}
	mustWrite(t, filepath.Join(root, "alpha", "second.go"), "package x")

	tr.ApplyScan(ScanDirs(tr.LoadedDirs()))

	if findChild(tr.Root, "alpha") != alpha {
		t.Fatal("alpha pointer changed across the applied scan")
	}
	if !alpha.Expanded || !alpha.Loaded {
		t.Fatalf("alpha state lost across the applied scan: %+v", alpha)
	}
	if findChild(alpha, "inner.go") != inner {
		t.Fatal("a surviving grandchild's pointer changed")
	}
	if findChild(alpha, "second.go") == nil {
		t.Fatal("a nested new file should appear — the sweep is recursive")
	}
	if findChild(tr.Root, "Newcomer.txt") == nil {
		t.Fatal("Newcomer.txt should have been picked up")
	}
	if findChild(tr.Root, "zeta.txt") != nil {
		t.Fatal("zeta.txt should have been removed from the tree")
	}
	if findChild(tr.Root, ".git") != nil {
		t.Fatal("the scan must apply the same hide rules as a reload")
	}
}

// TestApplyScan_LeavesUnscannedAndFailedDirsAlone pins the conservative
// half of the merge. A background scan is always a little stale by the
// time it lands, so a directory the scan never saw — because the user
// expanded it after the sweep started — must keep the children its own
// read just produced, and a directory that failed to read must keep what
// it had rather than blinking empty.
func TestApplyScan_LeavesUnscannedAndFailedDirsAlone(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Sweep captured the root only; the user then opened alpha.
	scans := ScanDirs(tr.LoadedDirs())
	alpha := findChild(tr.Root, "alpha")
	if alpha == nil {
		t.Fatal("alpha missing")
	}
	tr.Toggle(alpha)
	if len(alpha.Children) != 1 {
		t.Fatalf("alpha should have loaded its one child, got %d", len(alpha.Children))
	}

	tr.ApplyScan(scans)
	if len(alpha.Children) != 1 {
		t.Fatalf("a directory absent from the scan must keep its children, got %d",
			len(alpha.Children))
	}

	// A failed read carries an Err and must not empty the branch.
	before := len(tr.Root.Children)
	tr.ApplyScan([]DirScan{{Path: tr.Root.Path, Err: os.ErrPermission}})
	if got := len(tr.Root.Children); got != before {
		t.Fatalf("failed scan emptied the root: %d children, want %d", got, before)
	}
}

// TestShouldHide is an exhaustive table for the small hide list — keeps
// future edits to that list honest by showing exactly what's in/out.
func TestShouldHide(t *testing.T) {
	cases := []struct {
		name string
		hide bool
	}{
		{".git", true},
		{".svn", true},
		{".hg", true},
		{".DS_Store", true},
		{"node_modules", true},
		{".idea", true},
		{".vscode", true},
		{"main.go", false},
		{"README.md", false},
		{".env", false}, // dotfiles are intentionally NOT hidden
		{"git", false},
		{"node_modules2", false},
	}
	for _, tc := range cases {
		if got := shouldHide(tc.name); got != tc.hide {
			t.Errorf("shouldHide(%q) = %v, want %v", tc.name, got, tc.hide)
		}
	}
}

// TestFlattenInto_Collapsed ensures a non-expanded directory contributes
// only itself to the flat list — its children stay hidden until the user
// expands it.
func TestFlattenInto_Collapsed(t *testing.T) {
	dir := &Node{Name: "d", IsDir: true, Expanded: false, Children: []*Node{
		{Name: "c1"}, {Name: "c2"},
	}}
	var out []flatNode
	flattenInto(dir, 0, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 row for collapsed dir, got %d", len(out))
	}
	if out[0].Depth != 0 {
		t.Fatalf("depth wrong: %d", out[0].Depth)
	}
}

// TestFlattenInto_Expanded checks the recursive case: an expanded directory
// flattens itself plus children at depth+1, and nested expansion compounds.
func TestFlattenInto_Expanded(t *testing.T) {
	leaf := &Node{Name: "leaf"}
	inner := &Node{Name: "inner", IsDir: true, Expanded: true, Children: []*Node{leaf}}
	root := &Node{Name: "root", IsDir: true, Expanded: true, Children: []*Node{inner}}

	var out []flatNode
	flattenInto(root, 0, &out)

	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[0].Node.Name != "root" || out[0].Depth != 0 {
		t.Fatalf("row 0 wrong: %+v", out[0])
	}
	if out[1].Node.Name != "inner" || out[1].Depth != 1 {
		t.Fatalf("row 1 wrong: %+v", out[1])
	}
	if out[2].Node.Name != "leaf" || out[2].Depth != 2 {
		t.Fatalf("row 2 wrong: %+v", out[2])
	}
}

// TestFlattenInto_NilSafe documents that a nil *Node is a tolerated input
// (defensive: avoids requiring callers to nil-check before recursing).
func TestFlattenInto_NilSafe(t *testing.T) {
	var out []flatNode
	flattenInto(nil, 0, &out)
	if len(out) != 0 {
		t.Fatalf("nil node should produce no rows, got %d", len(out))
	}
}

// TestToggle_LoadsThenFlips verifies the two-step contract for Toggle:
// the first call on a never-loaded directory loads its children AND flips
// Expanded; subsequent calls just flip Expanded without re-reading disk.
func TestToggle_LoadsThenFlips(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if alpha.Loaded || alpha.Expanded {
		t.Fatalf("alpha should start unloaded+collapsed: %+v", alpha)
	}

	tr.Toggle(alpha)
	if !alpha.Expanded || !alpha.Loaded {
		t.Fatalf("after first toggle alpha should be expanded+loaded: %+v", alpha)
	}
	if len(alpha.Children) == 0 {
		t.Fatal("expected alpha's children to be loaded")
	}

	tr.Toggle(alpha)
	if alpha.Expanded {
		t.Fatal("second toggle should collapse")
	}
	tr.Toggle(alpha)
	if !alpha.Expanded {
		t.Fatal("third toggle should re-expand")
	}
}

// TestToggle_FileIsNoop ensures Toggle on a file doesn't mutate state —
// only directories have an open/closed concept.
func TestToggle_FileIsNoop(t *testing.T) {
	tr := &Tree{Root: &Node{Name: "r", IsDir: true}}
	f := &Node{Name: "x.txt"}
	tr.Toggle(f)
	if f.Expanded || f.Loaded {
		t.Fatalf("file node should not be mutated: %+v", f)
	}
}

// TestScroll_ClampsAtZero exercises Tree.Scroll's lower bound: scrolling
// past the top should pin at 0 rather than going negative.
func TestScroll_ClampsAtZero(t *testing.T) {
	tr := &Tree{Root: &Node{IsDir: true}}
	tr.Scroll(-5)
	if tr.ScrollY != 0 {
		t.Fatalf("ScrollY should clamp to 0, got %d", tr.ScrollY)
	}
	tr.Scroll(3)
	if tr.ScrollY != 3 {
		t.Fatalf("expected ScrollY=3, got %d", tr.ScrollY)
	}
	tr.Scroll(-10)
	if tr.ScrollY != 0 {
		t.Fatalf("ScrollY should clamp to 0 after big up-scroll, got %d", tr.ScrollY)
	}
}

// TestClampScroll_AllCases tabulates clampScroll's three regimes: list
// fits entirely (=> 0), overflow with valid scroll (=> unchanged), and
// scroll past max (=> pinned to total-viewH).
func TestClampScroll_AllCases(t *testing.T) {
	cases := []struct {
		label  string
		start  int
		total  int
		viewH  int
		expect int
	}{
		{"fits entirely", 4, 5, 10, 0},
		{"in range", 3, 20, 10, 3},
		{"past max", 50, 20, 10, 10},
		{"negative", -5, 20, 10, 0},
	}
	for _, c := range cases {
		tr := &Tree{ScrollY: c.start}
		tr.clampScroll(c.total, c.viewH)
		if tr.ScrollY != c.expect {
			t.Errorf("%s: ScrollY=%d want %d", c.label, tr.ScrollY, c.expect)
		}
	}
}

// TestHitTest_ExplorerHeaderMisses confirms y=0 (the all-caps
// "EXPLORER" row) is not a click target — clicking it should
// neither set the active folder nor open anything.
func TestHitTest_ExplorerHeaderMisses(t *testing.T) {
	tr := &Tree{visible: []*Node{{Name: "a"}}}
	if n, ok := tr.HitTest(0, 0); ok || n != nil {
		t.Fatalf("EXPLORER row should miss, got ok=%v node=%v", ok, n)
	}
}

// TestHitTest_ProjectRootRowReturnsRoot pins the "click the project
// name to reset active folder" behaviour. Without this, once a user
// has selected any subfolder there's no way to set the active folder
// back to the project root short of restarting the editor.
func TestHitTest_ProjectRootRowReturnsRoot(t *testing.T) {
	root := &Node{Name: "proj", IsDir: true, Path: "/proj"}
	tr := &Tree{Root: root, visible: []*Node{{Name: "a"}}}
	n, ok := tr.HitTest(0, 1)
	if !ok || n != root {
		t.Fatalf("y=1 should map to root, got ok=%v node=%v", ok, n)
	}
}

// TestHitTest_ValidRow checks the happy path: a click on a real row maps
// back to the same Node we recorded during the last Render.
func TestHitTest_ValidRow(t *testing.T) {
	target := &Node{Name: "x"}
	tr := &Tree{visible: []*Node{target, nil}}
	n, ok := tr.HitTest(5, 2) // first list row
	if !ok || n != target {
		t.Fatalf("expected hit on target, got ok=%v n=%v", ok, n)
	}
	// nil entry (blank padding row) should miss.
	if n, ok := tr.HitTest(5, 3); ok || n != nil {
		t.Fatalf("blank row should miss, got ok=%v n=%v", ok, n)
	}
}

// TestHitTest_OutOfRange covers clicks below the last visible row — the
// renderer pads with nil but the hit test should still cleanly miss.
func TestHitTest_OutOfRange(t *testing.T) {
	tr := &Tree{visible: []*Node{{Name: "a"}}}
	if n, ok := tr.HitTest(0, 99); ok || n != nil {
		t.Fatalf("out-of-range should miss, got ok=%v n=%v", ok, n)
	}
}

// renderAndCollect is a small helper that builds a SimulationScreen, runs
// Tree.Render, and returns the cell buffer + width so individual tests
// can inspect both runes and styles.
func renderAndCollect(t *testing.T, tr *Tree, w, h int) ([]tcell.SimCell, int) {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(w, h)
	tr.Render(scr, theme.Default(), 0, 0, w, h)
	scr.Show() // flush back buffer to front so GetContents sees it
	cells, cw, _ := scr.GetContents()
	return cells, cw
}

// rowText reconstructs the visible text of a single screen row, which is
// far more readable in test failures than dumping the raw cell array.
func rowText(cells []tcell.SimCell, w, y int) string {
	row := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) == 0 {
			row = append(row, ' ')
			continue
		}
		row = append(row, c.Runes[0])
	}
	return string(row)
}

// TestRender_ProjectNameAndChevrons asserts that the explorer header shows
// the project (root) name on row 1 and that an expanded directory renders
// with a '▾' while a collapsed sibling renders with a '▸'.
func TestRender_ProjectNameAndChevrons(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// alpha will appear collapsed (default), Beta the same. Force alpha
	// expanded so we can see both chevrons in one render.
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand alpha

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Row 1 should contain the project (root) folder name.
	rootName := filepath.Base(root)
	if got := rowText(cells, w, 1); !containsRune(got, rootName) {
		t.Fatalf("row 1 missing project name %q: got %q", rootName, got)
	}

	// Find the row containing alpha; verify '▾' present.
	if !findRowWithBoth(cells, w, 20, "alpha", '▾') {
		t.Fatal("expected an expanded-row showing alpha with '▾'")
	}
	// Beta is collapsed — verify '▸' present.
	if !findRowWithBoth(cells, w, 20, "Beta", '▸') {
		t.Fatal("expected a collapsed-row showing Beta with '▸'")
	}
}

// TestRender_EmptyRootShowsPlaceholder pins the empty-project state. A
// bare root row with nothing under it reads as "the tree failed to
// load"; the muted placeholder says which of the two it is. This is the
// very first screen after `mkdir proj && skiff proj`.
func TestRender_EmptyRootShowsPlaceholder(t *testing.T) {
	root := t.TempDir()
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	if got := rowText(cells, w, 2); !containsRune(got, EmptyFolderLabel) {
		t.Fatalf("first list row = %q, want %q", got, EmptyFolderLabel)
	}
	// Muted, not Text: it's an explanation, not a file.
	fg, _, _ := cells[2*w+1].Style.Decompose()
	if fg != theme.Default().Muted {
		t.Fatalf("placeholder fg = %v, want Muted", fg)
	}
	// It is not a row you can click — HitTest must miss it, or the
	// placeholder would behave like a file and open nothing.
	if n, ok := tr.HitTest(0, 2); ok || n != nil {
		t.Fatalf("placeholder must not be a hit target, got %v", n)
	}
}

// TestRender_EmptyRootClipsToSidebarWidth keeps the placeholder inside
// the sidebar: like every other row it is drawn through drawString, so a
// sidebar narrower than the label truncates instead of painting over the
// splitter and the editor beyond it.
func TestRender_EmptyRootClipsToSidebarWidth(t *testing.T) {
	tr, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const narrow = 8 // narrower than "(folder is empty)"
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 20)
	tr.Render(scr, theme.Default(), 0, 0, narrow, 20)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// Cells the renderer never touched carry no runes at all; anything
	// with a visible glyph past the sidebar edge is a bleed.
	for x := narrow; x < w; x++ {
		if c := cells[2*w+x]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("placeholder painted past the sidebar at x=%d: %q", x, c.Runes[0])
		}
	}
	if got := rowText(cells, w, 2)[:narrow]; !strings.HasPrefix(got, " (folder") {
		t.Fatalf("clipped placeholder = %q", got)
	}
}

// TestRender_NonEmptyRootHasNoPlaceholder is the negative: a project
// with files must never show the empty-folder row.
func TestRender_NonEmptyRootHasNoPlaceholder(t *testing.T) {
	tr, err := New(mkTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)
	for y := range 20 {
		if containsRune(rowText(cells, w, y), EmptyFolderLabel) {
			t.Fatalf("row %d shows the empty placeholder in a populated tree", y)
		}
	}
}

// TestRender_ActiveFolderIsBold sets ActiveFolder to alpha's path and
// checks that alpha's row carries the AttrBold style — the visual cue
// the user uses to confirm where "New file" will land.
func TestRender_ActiveFolderIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Find any cell on the alpha row; assert the foreground style has Bold.
	rowY := -1
	for y := 2; y < 20; y++ {
		if containsRune(rowText(cells, w, y), "alpha") {
			rowY = y
			break
		}
	}
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	// Scan the row for any cell with AttrBold set.
	bold := false
	for x := 0; x < w; x++ {
		_, _, attr := cells[rowY*w+x].Style.Decompose()
		if attr&tcell.AttrBold != 0 {
			bold = true
			break
		}
	}
	if !bold {
		t.Fatal("expected alpha row to be rendered bold (active folder)")
	}
}

// TestRender_ActiveFileIsBold verifies the open file itself is visible in the tree.
func TestRender_ActiveFileIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	tr.ActiveFile = inner.Path

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find active file row")
	}
	if !rowHasBold(cells, w, rowY) {
		t.Fatal("active file row should be bold")
	}
}

// TestRender_TinyHeightDoesNotPanic guards against an off-by-one when the
// caller hands Render a height smaller than the 2-row header — listH goes
// to zero and we shouldn't blow up dividing or indexing.
func TestRender_TinyHeightDoesNotPanic(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(20, 1)
	tr.Render(scr, theme.Default(), 0, 0, 20, 1) // listH would be -1 -> clamped to 0
	// no panic = pass; also visible must be empty.
	if len(tr.visible) != 0 {
		t.Fatalf("expected empty visible slice, got len=%d", len(tr.visible))
	}
}

// TestRender_DirtyFileUsesModifiedColor seeds the tree's DirtyFiles set
// with one path and asserts the renderer paints that row in
// theme.Modified — the colour the editor uses everywhere else (tab dot,
// future status indicators) for "uncommitted change".
func TestRender_DirtyFileUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("alpha/inner.go missing from fixture")
	}
	tr.DirtyFiles = map[string]GitChangeKind{inner.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatalf("expected inner.go row to be drawn in Modified color")
	}
}

// TestRender_DirtyFolderUsesModifiedColor proves that a folder appearing
// in DirtyFolders gets the Modified colour even when none of its visible
// children do — collapsed branches still need to signal "something
// changed inside".
func TestRender_DirtyFolderUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatal("expected alpha folder row to be drawn in Modified color")
	}
}

// TestRender_DirtyRootUsesModifiedColor ensures the project name itself
// reflects git changes when any descendant is dirty.
func TestRender_DirtyRootUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFolders = map[string]GitChangeKind{tr.Root.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	if !rowHasColor(cells, w, 1, theme.Default().GitModified) {
		t.Fatal("expected root project row to be drawn in Modified color")
	}
}

// TestRender_DirtyAndActiveStaysBold confirms that the active-folder
// styling (bold) and the dirty-folder styling (Modified colour) compose
// cleanly — the user shouldn't lose the "current target" cue just
// because the folder also has uncommitted changes.
func TestRender_DirtyAndActiveStaysBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Error("expected alpha row to be Modified colour")
	}
	if !rowHasBold(cells, w, rowY) {
		t.Error("expected alpha row to remain bold")
	}
}

// findRowY scans the rendered cell buffer for the first row whose
// reconstructed text contains needle.
func findRowY(cells []tcell.SimCell, w, h int, needle string) int {
	for y := 0; y < h; y++ {
		if containsRune(rowText(cells, w, y), needle) {
			return y
		}
	}
	return -1
}

// rowHasColor reports whether any non-blank cell in row y was drawn with
// the given foreground colour. The tree pads rows with blank spaces; we
// ignore those so a leading-pad colour mismatch isn't reported.
func rowHasColor(cells []tcell.SimCell, w, y int, want tcell.Color) bool {
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) == 0 || c.Runes[0] == ' ' {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg == want {
			return true
		}
	}
	return false
}

// rowHasBold reports whether any cell in row y carries tcell.AttrBold.
func rowHasBold(cells []tcell.SimCell, w, y int) bool {
	for x := 0; x < w; x++ {
		_, _, attr := cells[y*w+x].Style.Decompose()
		if attr&tcell.AttrBold != 0 {
			return true
		}
	}
	return false
}

// containsRune is a tiny "string contains substring" wrapper that keeps
// the imports of this test file lean.
func containsRune(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// findRowWithBoth scans the simulation buffer for any row that contains
// both the given name substring and the given chevron rune — used to
// assert "Beta is shown collapsed" / "alpha is shown expanded".
func findRowWithBoth(cells []tcell.SimCell, w, h int, name string, chev rune) bool {
	for y := 0; y < h; y++ {
		text := rowText(cells, w, y)
		if !containsRune(text, name) {
			continue
		}
		for _, r := range text {
			if r == chev {
				return true
			}
		}
	}
	return false
}

// TestRender_IconsDisabledByDefault pins down the default look — a tree
// whose IconsEnabled flag was never flipped should not embed any Nerd
// Font glyph in its output. Important so users on terminals without a
// Nerd Font don't see broken-glyph "tofu" boxes after upgrading.
func TestRender_IconsDisabledByDefault(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	// Walk every visible row and assert none of the file-default,
	// folder-open, or folder-closed glyphs appear.
	for y := 0; y < 20; y++ {
		row := rowText(cells, w, y)
		for _, g := range []string{icons.FileDefault, icons.FolderOpen, icons.FolderClosed} {
			if containsRune(row, g) {
				t.Fatalf("row %d unexpectedly contains glyph %q: %q", y, g, row)
			}
		}
	}
}

// TestRender_IconsEnabledShowsFolderGlyph verifies that flipping
// IconsEnabled actually emits the folder-closed glyph for an
// unexpanded directory — the most common visible case.
func TestRender_IconsEnabledShowsFolderGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "Beta") // collapsed
	if rowY < 0 {
		t.Fatal("could not find Beta row")
	}
	if !containsRune(rowText(cells, w, rowY), icons.FolderClosed) {
		t.Fatalf("expected FolderClosed glyph on Beta row, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledShowsFileGlyph picks the .go file inside
// alpha/, expands the parent so it's visible, and checks the
// language-specific glyph from icons.For lands on its row.
func TestRender_IconsEnabledShowsFileGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand so inner.go renders

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}
	want := icons.For("inner.go", false, false)
	if !containsRune(rowText(cells, w, rowY), want) {
		t.Fatalf("expected glyph %q on inner.go row, got %q",
			want, rowText(cells, w, rowY))
	}
}

// TestRender_DotFileRendersMuted verifies hidden / dotted entries
// fall back to the theme's Muted colour rather than FileColor — this
// is the visual cue users rely on to skim a tree full of metadata
// (.gitignore, .env, .github/) and find the source files at a glance.
func TestRender_DotFileRendersMuted(t *testing.T) {
	root := mkTree(t)
	// mkTree already creates .git but it's filtered by shouldHide. Add
	// a .env file that *will* show up so we can assert against its row.
	mustWrite(t, filepath.Join(root, ".env"), "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Muted) {
		t.Fatalf(".env row should render in Muted; got %q", rowText(cells, w, rowY))
	}
	// Sanity check: a non-dot file on the same level should *not* be muted.
	zetaY := findRowY(cells, w, 20, "zeta.txt")
	if zetaY < 0 {
		t.Fatal("could not find zeta.txt row")
	}
	if rowHasColor(cells, w, zetaY, theme.Default().Muted) {
		t.Fatalf("non-dot file zeta.txt should not be muted")
	}
}

// TestRender_DirtyOverridesDotMute verifies the priority cascade
// documented in drawNodeRow: a modified .env should still flip to the
// Modified colour rather than staying muted, because "this file has
// uncommitted changes" is louder information than "this is metadata".
func TestRender_DirtyOverridesDotMute(t *testing.T) {
	root := mkTree(t)
	envPath := filepath.Join(root, ".env")
	mustWrite(t, envPath, "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]GitChangeKind{envPath: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatalf("dirty .env should override Muted with Modified, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledColoursGlyphPerLanguage proves the glyph cell
// is drawn in icons.ColorFor's mapped colour rather than the row's
// regular file fg. Without this, every glyph would inherit the same
// FileColor and the visual cue (Go cyan / Markdown blue / etc.) would
// be lost.
func TestRender_IconsEnabledColoursGlyphPerLanguage(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha)

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}

	// Locate the cell carrying the .go glyph and assert its fg is the
	// per-language colour, not the row's FileColor.
	wantGlyph := []rune(icons.For("inner.go", false, false))[0]
	wantColor := icons.ColorFor("inner.go", false, theme.Default().FileColor)
	found := false
	for x := 0; x < w; x++ {
		c := cells[rowY*w+x]
		if len(c.Runes) == 0 || c.Runes[0] != wantGlyph {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg != wantColor {
			t.Fatalf("glyph fg = %v, want %v (per-language)", fg, wantColor)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("no cell carried glyph %q on inner.go row", string(wantGlyph))
	}
}

// TestRender_IconsEnabledFolderOpenSwitches verifies the open/closed
// folder glyph pair flips correctly when the user expands a folder —
// the visual cue most users will rely on more than the chevron.
func TestRender_IconsEnabledFolderOpenSwitches(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")

	// Collapsed: should show closed-folder glyph, not the open one.
	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (collapsed)")
	}
	collapsed := rowText(cells, w, rowY)
	if !containsRune(collapsed, icons.FolderClosed) {
		t.Fatalf("collapsed alpha row missing FolderClosed: %q", collapsed)
	}
	if containsRune(collapsed, icons.FolderOpen) {
		t.Fatalf("collapsed alpha row should not show FolderOpen: %q", collapsed)
	}

	// Expanded: should switch to open-folder glyph.
	tr.Toggle(alpha)
	cells, w = renderAndCollect(t, tr, 40, 20)
	rowY = findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (expanded)")
	}
	expanded := rowText(cells, w, rowY)
	if !containsRune(expanded, icons.FolderOpen) {
		t.Fatalf("expanded alpha row missing FolderOpen: %q", expanded)
	}
}

// mkNested builds a deeper layout than mkTree so Reveal has a real ancestor
// chain to walk. The shape:
//
//	root/
//	  a/
//	    b/
//	      deep.go
//	      other.go
//	  top.go
//	  zeta.txt
//	  ...
//
// The top-level files give the flat list enough rows for the scroll tests
// to have a target that genuinely sits below the viewport.
func mkNested(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a"))
	mustMkdir(t, filepath.Join(root, "a", "b"))
	mustWrite(t, filepath.Join(root, "a", "b", "deep.go"), "x")
	mustWrite(t, filepath.Join(root, "a", "b", "other.go"), "x")
	mustWrite(t, filepath.Join(root, "top.go"), "x")
	mustWrite(t, filepath.Join(root, "zeta.txt"), "x")
	mustWrite(t, filepath.Join(root, "Apple.md"), "x")
	return root
}

// TestReveal_ExpandsAncestorsAndScrolls is the headline case: a file buried
// two directories deep is invisible until Reveal walks the chain, lazily
// loads + expands each ancestor, and brings the row into the viewport.
// Without this the finder would open the file and the sidebar would still
// show a collapsed "a/" — the bug the feature exists to fix. With a large
// viewport the row lands in view purely from the expansion, so the real
// contract being pinned here is "ancestors expanded AND row on screen".
func TestReveal_ExpandsAncestorsAndScrolls(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deep := filepath.Join(root, "a", "b", "deep.go")
	const viewH = 20

	tr.Reveal(deep, viewH)

	a := findChild(tr.Root, "a")
	if a == nil {
		t.Fatal("a missing")
	}
	if !a.Expanded || !a.Loaded {
		t.Fatalf("a should be expanded+loaded after reveal: %+v", a)
	}
	b := findChild(a, "b")
	if b == nil {
		t.Fatal("b missing")
	}
	if !b.Expanded || !b.Loaded {
		t.Fatalf("b should be expanded+loaded after reveal: %+v", b)
	}
	deepNode := findChild(b, "deep.go")
	if deepNode == nil {
		t.Fatal("deep.go missing after reveal")
	}
	wantIdx := tr.flatIndexOf(deepNode)
	if wantIdx < 0 {
		t.Fatal("deep.go should be in the flat list after ancestors expanded")
	}
	if wantIdx < tr.ScrollY || wantIdx >= tr.ScrollY+viewH {
		t.Fatalf("deep.go (idx %d) should be inside viewport [ScrollY=%d, +%d)", wantIdx, tr.ScrollY, viewH)
	}
}

// TestReveal_NoScrollWhenAlreadyVisible guards the click path: when the row
// is already on screen Reveal must leave ScrollY alone, otherwise clicking a
// visible row would snap it to the top — a surprising jump the user didn't
// ask for.
func TestReveal_NoScrollWhenAlreadyVisible(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Open a/b so deep.go is in the flat list, then park the viewport so
	// deep.go is the second visible row.
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	tr.Toggle(b)
	deepNode := findChild(b, "deep.go")
	idx := tr.flatIndexOf(deepNode)
	tr.ScrollY = idx - 1 // deep.go one row into the viewport

	deep := filepath.Join(root, "a", "b", "deep.go")
	tr.Reveal(deep, 10)

	if tr.ScrollY != idx-1 {
		t.Fatalf("ScrollY should be unchanged when target is visible: got %d, want %d", tr.ScrollY, idx-1)
	}
}

// TestReveal_ScrollsWhenTargetBelowViewport checks the inverse: a target
// below the current viewport must move ScrollY so the row lands on screen.
// Without this the reveal would be a no-op for files the user scrolled past.
func TestReveal_ScrollsWhenTargetBelowViewport(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Open the nested chain so the flat list has the deep row at a
	// non-zero index, then keep the viewport pinned at the top. One
	// Toggle is the whole setup: a/b is a single-child chain, so
	// expanding a opens b with it.
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	deepNode := findChild(b, "deep.go")
	wantIdx := tr.flatIndexOf(deepNode)
	tr.ScrollY = 0

	// One-row viewport: the a/b chain row occupies it, so the target
	// (row 1) is genuinely below the fold and must trigger the scroll.
	deep := filepath.Join(root, "a", "b", "deep.go")
	tr.Reveal(deep, 1)

	if tr.ScrollY != wantIdx {
		t.Fatalf("ScrollY: got %d, want %d", tr.ScrollY, wantIdx)
	}
}

// TestReveal_DirectChildOfRoot covers the no-ancestor case: a file sitting
// directly under the root has no directories to expand, but Reveal should
// still scroll to it when it's off-screen.
func TestReveal_DirectChildOfRoot(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	zeta := findChild(tr.Root, "zeta.txt")
	if zeta == nil {
		t.Fatal("zeta.txt missing")
	}
	// Park the viewport below zeta.txt so it's off-screen, then reveal.
	wantIdx := tr.flatIndexOf(zeta)
	tr.ScrollY = wantIdx + 5

	tr.Reveal(filepath.Join(root, "zeta.txt"), 2)

	if tr.ScrollY != wantIdx {
		t.Fatalf("ScrollY: got %d, want %d", tr.ScrollY, wantIdx)
	}
}

// TestReveal_ViewHZeroExpandsButDoesNotScroll pins the sidebar-hidden
// contract: when viewH is 0 there's no viewport to scroll, but ancestors
// should still be expanded so the tree is correct the next time the sidebar
// is shown.
func TestReveal_ViewHZeroExpandsButDoesNotScroll(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deep := filepath.Join(root, "a", "b", "deep.go")

	tr.Reveal(deep, 0)

	a := findChild(tr.Root, "a")
	b := findChild(a, "b")
	if !a.Expanded || !b.Expanded {
		t.Fatalf("ancestors should still expand with viewH=0: a=%+v b=%+v", a, b)
	}
	if tr.ScrollY != 0 {
		t.Fatalf("ScrollY should not change with viewH=0: got %d", tr.ScrollY)
	}
}

// TestReveal_HiddenDirIsNoop verifies Reveal gives up on paths that pass
// through a filtered directory. .git is in the hide list, so a file under it
// has no reachable ancestor — Reveal must bail without expanding anything
// and without touching ScrollY.
func TestReveal_HiddenDirIsNoop(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, ".git", "config"), "x")
	mustWrite(t, filepath.Join(root, "visible.go"), "x")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scrollBefore := tr.ScrollY

	tr.Reveal(filepath.Join(root, ".git", "config"), 10)

	if tr.ScrollY != scrollBefore {
		t.Fatalf("ScrollY should not change for hidden path: was %d now %d", scrollBefore, tr.ScrollY)
	}
}

// TestReveal_OutsideRootIsNoop guards against a path that isn't under the
// tree at all. filepath.Rel yields a ".." prefix in that case; Reveal must
// return without mutating the tree.
func TestReveal_OutsideRootIsNoop(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "unrelated.go")
	mustWrite(t, outside, "x")
	scrollBefore := tr.ScrollY

	tr.Reveal(outside, 10)

	if tr.ScrollY != scrollBefore {
		t.Fatalf("ScrollY should not change for outside path: was %d now %d", scrollBefore, tr.ScrollY)
	}
}

// TestReveal_RootItselfIsNoop pins the degenerate "reveal the root" case:
// filepath.Rel(root, root) == ".", which Reveal treats as nothing to do.
func TestReveal_RootItselfIsNoop(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scrollBefore := tr.ScrollY

	tr.Reveal(root, 10)

	if tr.ScrollY != scrollBefore {
		t.Fatalf("ScrollY should not change for root path: was %d now %d", scrollBefore, tr.ScrollY)
	}
}

// TestFlatIndexOf_MatchesRenderOrder cross-checks the helper against the
// real flattenInto walk: every node's index from flatIndexOf must agree with
// its position in flattenInto's output. A drift here would scroll to the
// wrong row.
func TestFlatIndexOf_MatchesRenderOrder(t *testing.T) {
	root := mkNested(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Expand the chain so the nested rows are in the flat list.
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	tr.Toggle(b)

	var flat []flatNode
	for _, c := range tr.Root.Children {
		flattenInto(c, 0, &flat)
	}
	for i, fn := range flat {
		if got := tr.flatIndexOf(fn.Node); got != i {
			t.Fatalf("flatIndexOf(%s): got %d, want %d", fn.Node.Name, got, i)
		}
	}
}

// TestRender_DirtyRowsShowStatusLetter pins the non-hue git channel:
// row color alone can't carry status for colorblind users (added-green
// vs deleted-red is the classic deuteranopia collision), so every dirty
// row must also show the GIT panel's one-cell letter, right-aligned.
func TestRender_DirtyRowsShowStatusLetter(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("alpha/inner.go missing from fixture")
	}
	tr.DirtyFiles = map[string]GitChangeKind{inner.Path: GitChangeModified}
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeAdded}

	cells, w := renderAndCollect(t, tr, 40, 20)

	fileY := findRowY(cells, w, 20, "inner.go")
	if fileY < 0 {
		t.Fatal("could not find inner.go row in render output")
	}
	c := cells[fileY*w+(w-2)]
	if len(c.Runes) == 0 || c.Runes[0] != 'M' {
		t.Fatalf("modified file row should end with an 'M' letter, got %q", c.Runes)
	}

	folderY := findRowY(cells, w, 20, "alpha")
	if folderY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	c = cells[folderY*w+(w-2)]
	if len(c.Runes) == 0 || c.Runes[0] != 'A' {
		t.Fatalf("added folder row should end with an 'A' letter, got %q", c.Runes)
	}
}

// TestRender_CJKFilenameKeepsStatusLetterAligned pins the tree's
// wide-glyph layout: each ideograph of a CJK filename occupies two cells
// (base cell plus an untouched continuation cell), the dirty-status
// letter still lands at the row's right edge, and nothing paints past
// the sidebar width. Before textdraw, drawString painted one rune per
// COLUMN, so consecutive ideographs landed in adjacent cells and
// rendered as overlapping garbage.
func TestRender_CJKFilenameKeepsStatusLetterAligned(t *testing.T) {
	root := t.TempDir()
	const name = "日本語ファイル.go"
	mustWrite(t, filepath.Join(root, name), "package x\n")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]GitChangeKind{filepath.Join(root, name): GitChangeModified}

	// Narrow tree inside a wider screen, so painting past the sidebar
	// edge would be visible — the same shape as the placeholder clip test.
	const treeW = 30
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 20)
	tr.Render(scr, theme.Default(), 0, 0, treeW, 20)
	scr.Show()
	cells, w, _ := scr.GetContents()

	fileY := findRowY(cells, w, 20, "日")
	if fileY < 0 {
		t.Fatal("could not find the CJK file row in render output")
	}
	// Locate the first ideograph, then check the cluster layout: base
	// cell, untouched continuation cell (tcell paints the wide glyph
	// through it), and the next ideograph exactly two cells later.
	col := -1
	for x := 0; x < treeW; x++ {
		if c := cells[fileY*w+x]; len(c.Runes) > 0 && c.Runes[0] == '日' {
			col = x
			break
		}
	}
	if col < 0 {
		t.Fatal("日 not found on the CJK file row")
	}
	if c := cells[fileY*w+col+1]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
		t.Fatalf("continuation cell after 日 holds a glyph: %q", c.Runes)
	}
	if c := cells[fileY*w+col+2]; len(c.Runes) == 0 || c.Runes[0] != '本' {
		t.Fatalf("cell two after 日 = %q, want 本", c.Runes)
	}
	// The status letter keeps its right-edge home.
	if c := cells[fileY*w+(treeW-2)]; len(c.Runes) == 0 || c.Runes[0] != 'M' {
		t.Fatalf("status letter cell = %q, want M at column %d", c.Runes, treeW-2)
	}
	// And no glyph escapes the sidebar.
	for x := treeW; x < w; x++ {
		if c := cells[fileY*w+x]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("glyph painted past the sidebar at x=%d: %q", x, c.Runes[0])
		}
	}
}

// TestReload_HidesTrashEntries pins the session-trash filter: an
// in-place trash entry (TrashPrefix rename fallback) is deleted
// content awaiting Undo and must never appear as a tree row.
func TestReload_HidesTrashEntries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.txt"), "x")
	mustWrite(t, filepath.Join(root, TrashPrefix+"0-dead.txt"), "x")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, c := range tr.Root.Children {
		if strings.HasPrefix(c.Name, TrashPrefix) {
			t.Fatalf("trash entry %q leaked into the tree", c.Name)
		}
	}
	if len(tr.Root.Children) != 1 || tr.Root.Children[0].Name != "real.txt" {
		t.Fatalf("expected only real.txt, got %v", tr.Root.Children)
	}
}

// TestExpandedDirsRoundTrip pins the session-persistence pair: expand
// some nested folders, capture ExpandedDirs, collapse everything, and
// ExpandDirs must restore exactly the expanded set.
func TestExpandedDirsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"a/b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	tr, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	tr.ExpandDirs([]string{"a", filepath.Join("a", "b")})

	got := tr.ExpandedDirs()
	want := []string{"a", filepath.Join("a", "b")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ExpandedDirs: got %v, want %v", got, want)
	}

	// Fresh tree — restore from the captured list.
	tr2, err := New(dir)
	if err != nil {
		t.Fatalf("new 2: %v", err)
	}
	tr2.ExpandDirs(got)
	got2 := tr2.ExpandedDirs()
	if len(got2) != 2 {
		t.Fatalf("restore: got %v", got2)
	}
}

// TestExpandDirsUnknownSkipped: stale session entries (deleted folders,
// files where folders used to be) are skipped without side effects.
func TestExpandDirsUnknownSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tr, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	tr.ExpandDirs([]string{"ghost", "file.txt", "real"})
	got := tr.ExpandedDirs()
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("only 'real' should expand, got %v", got)
	}
}

// skipIfRoot bails out of a permission test when the process can read
// anything regardless of mode bits. chmod 000 does not restrict root, so
// the directory the test just made unreadable would still list fine and
// the assertion would fail for an environment reason rather than a code
// defect.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not restrict root, so an unreadable directory cannot be simulated")
	}
}

// mkUnreadable creates dir under parent and strips every permission bit
// so os.ReadDir on it fails, restoring the bits on cleanup so t.TempDir
// can remove the tree.
func mkUnreadable(t *testing.T, parent, name string) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(p, "hidden.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed %s: %v", p, err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", p, err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	return p
}

// TestUnreadableDirIsMarkedNotEmpty is the whole point of Node.ReadErr:
// before it, a directory we lacked permission to read rendered exactly
// like a directory with nothing in it, so the tree confidently reported
// "nothing here" about a place it had never managed to look.
func TestUnreadableDirIsMarkedNotEmpty(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mkUnreadable(t, root, "locked")
	mustMkdir(t, filepath.Join(root, "vacant"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	locked, vacant := findChild(tr.Root, "locked"), findChild(tr.Root, "vacant")
	if locked == nil || vacant == nil {
		t.Fatalf("expected both dirs as children, got %v", tr.Root.Children)
	}
	tr.Toggle(locked)
	tr.Toggle(vacant)

	if locked.ReadErr == nil {
		t.Fatal("an unreadable directory must carry ReadErr")
	}
	if vacant.ReadErr != nil {
		t.Fatalf("a readable empty directory must not be marked: %v", vacant.ReadErr)
	}

	cells, w := renderAndCollect(t, tr, 40, 20)
	lockedRow, vacantRow := rowText(cells, w, 2), rowText(cells, w, 3)
	if !strings.Contains(lockedRow, "locked/") || !strings.Contains(lockedRow, UnreadableLabel) {
		t.Fatalf("locked row must carry the marker, got %q", lockedRow)
	}
	if strings.Contains(vacantRow, UnreadableLabel) {
		t.Fatalf("empty row must stay unmarked, got %q", vacantRow)
	}
	if lockedRow == vacantRow {
		t.Fatal("unreadable and empty directories must not render identically")
	}
}

// TestUnreadableMarkClearsOnRefresh pins the other half of the mark: the
// identity-preserving refresh has to retract it the moment the directory
// becomes readable, or a one-off permission blip leaves a permanent lie
// on the row.
func TestUnreadableMarkClearsOnRefresh(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	locked := mkUnreadable(t, root, "locked")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	n := findChild(tr.Root, "locked")
	tr.Toggle(n)
	if n.ReadErr == nil {
		t.Fatal("setup: expected the node to be marked unreadable")
	}

	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	tr.Refresh()

	if n.ReadErr != nil {
		t.Fatalf("mark must clear once the directory reads: %v", n.ReadErr)
	}
	if findChild(n, "hidden.txt") == nil {
		t.Fatalf("children must load on the clearing refresh, got %v", n.Children)
	}
}

// TestApplyScanMarksUnreadable covers the background sweep's half. The
// sweep is where a directory that went unreadable between ticks is
// noticed; ApplyScan used to drop failed listings entirely, which kept
// the stale children on screen with no hint they were stale.
func TestApplyScanMarksUnreadable(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWrite(t, filepath.Join(root, "sub", "a.txt"), "a")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sub := findChild(tr.Root, "sub")
	tr.Toggle(sub)
	if len(sub.Children) != 1 {
		t.Fatalf("setup: expected one child, got %v", sub.Children)
	}

	tr.ApplyScan([]DirScan{{Path: sub.Path, Err: os.ErrPermission}})
	if sub.ReadErr == nil {
		t.Fatal("a failed scan must mark the node")
	}
	if len(sub.Children) != 1 {
		t.Fatalf("a failed scan must keep the children it had, got %v", sub.Children)
	}

	tr.ApplyScan([]DirScan{{Path: sub.Path, Entries: []ScanEntry{{Name: "a.txt"}, {Name: "b.txt"}}}})
	if sub.ReadErr != nil {
		t.Fatalf("a successful scan must clear the mark: %v", sub.ReadErr)
	}
	if len(sub.Children) != 2 {
		t.Fatalf("successful scan should merge both entries, got %v", sub.Children)
	}
}

// TestUnreadableRootLabel: the root has no row of its own under the
// project name, so an unreadable root falls through to the placeholder
// line — which said "(folder is empty)" and was the most confident lie
// in the tree.
func TestUnreadableRootLabel(t *testing.T) {
	tr := &Tree{Root: &Node{Path: "/nope", Name: "nope", IsDir: true, Expanded: true, Loaded: true}}
	cells, w := renderAndCollect(t, tr, 40, 10)
	if got := rowText(cells, w, 2); !strings.Contains(got, EmptyFolderLabel) {
		t.Fatalf("a readable empty root says so, got %q", got)
	}

	tr.Root.ReadErr = os.ErrPermission
	cells, w = renderAndCollect(t, tr, 40, 10)
	got := rowText(cells, w, 2)
	if !strings.Contains(got, UnreadableLabel) || strings.Contains(got, EmptyFolderLabel) {
		t.Fatalf("an unreadable root must say so, got %q", got)
	}
}

// TestMaxDirChildren_SentinelRow pins the truncation contract: a
// directory past the cap keeps exactly MaxDirChildren real entries and
// gains one visible "… N more" row, so the user can see that the tree
// stopped listing rather than believing the directory ends there.
func TestMaxDirChildren_SentinelRow(t *testing.T) {
	root := t.TempDir()
	over := 7
	for i := range MaxDirChildren + over {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%05d.txt", i)), "x")
	}

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := len(tr.Root.Children); got != MaxDirChildren+1 {
		t.Fatalf("children: got %d, want %d real + 1 sentinel", got, MaxDirChildren)
	}
	last := tr.Root.Children[len(tr.Root.Children)-1]
	if !last.Sentinel {
		t.Fatalf("the final child must be the sentinel, got %+v", last)
	}
	if last.Path != "" {
		t.Fatalf("the sentinel must have no filesystem path, got %q", last.Path)
	}
	if want := fmt.Sprintf(moreRowFormat, over); last.Name != want {
		t.Fatalf("sentinel label: got %q, want %q", last.Name, want)
	}

	// The retained slice is the head of the sorted order, so the row the
	// truncation drops is the last name, not an arbitrary one.
	if first := tr.Root.Children[0].Name; first != "f00000.txt" {
		t.Fatalf("truncation must keep the sorted head, got %q first", first)
	}
	if lastReal := tr.Root.Children[MaxDirChildren-1].Name; lastReal != fmt.Sprintf("f%05d.txt", MaxDirChildren-1) {
		t.Fatalf("truncation must cut the sorted tail, got %q last", lastReal)
	}
}

// TestMaxDirChildren_SentinelRendersAndIsInert: the sentinel has to be
// legible on screen and do nothing when clicked. Returning it from
// HitTest would hand callers a Node with no path — every one of them
// goes on to open, expand or target what it gets back.
func TestMaxDirChildren_SentinelRendersAndIsInert(t *testing.T) {
	root := t.TempDir()
	for i := range MaxDirChildren + 3 {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%05d.txt", i)), "x")
	}
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Scroll so the tail of the list — sentinel included — is on screen.
	tr.ScrollY = MaxDirChildren
	cells, w := renderAndCollect(t, tr, 40, 10)

	sentinelRow := -1
	for y := 2; y < 10; y++ {
		if strings.Contains(rowText(cells, w, y), "3 more") {
			sentinelRow = y
			break
		}
	}
	if sentinelRow < 0 {
		t.Fatal("the sentinel row never rendered")
	}
	if n, ok := tr.HitTest(4, sentinelRow); ok || n != nil {
		t.Fatalf("clicking the sentinel must land on nothing, got ok=%v n=%+v", ok, n)
	}

	// A real row on the same screen still hit-tests, so the guard is not
	// simply breaking the whole list.
	if n, ok := tr.HitTest(4, sentinelRow-1); !ok || n == nil || n.Sentinel {
		t.Fatalf("the row above the sentinel must still be clickable, got ok=%v n=%+v", ok, n)
	}
}

// TestMaxDirChildren_SentinelSurvivesRefresh: the sentinel is rebuilt by
// every merge, so a refresh must neither duplicate it nor let it be
// mistaken for a surviving dirent and carried over as a real node.
func TestMaxDirChildren_SentinelSurvivesRefresh(t *testing.T) {
	root := t.TempDir()
	for i := range MaxDirChildren + 2 {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%05d.txt", i)), "x")
	}
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustWrite(t, filepath.Join(root, "f99999.txt"), "x")
	tr.Refresh()

	sentinels := 0
	for _, c := range tr.Root.Children {
		if c.Sentinel {
			sentinels++
		}
	}
	if sentinels != 1 {
		t.Fatalf("exactly one sentinel expected after refresh, got %d", sentinels)
	}
	if want := fmt.Sprintf(moreRowFormat, 3); tr.Root.Children[len(tr.Root.Children)-1].Name != want {
		t.Fatalf("sentinel must track the new entry count, want %q", want)
	}
	if len(tr.Root.Children) != MaxDirChildren+1 {
		t.Fatalf("child count must stay capped, got %d", len(tr.Root.Children))
	}
}

// TestMaxDirChildren_MergeStaysBounded is the responsiveness claim
// stated as an assertion: a directory listing of any size collapses to a
// fixed number of retained nodes, so the flatten walk, the render pass
// and every later refresh of that branch cost the same whether the
// directory holds a thousand entries or a hundred thousand. Driving
// merge directly keeps the test off disk — os.ReadDir's own cost is not
// what the cap is about.
func TestMaxDirChildren_MergeStaysBounded(t *testing.T) {
	const total = 100_000
	entries := make([]ScanEntry, 0, total)
	for i := range total {
		entries = append(entries, ScanEntry{Name: fmt.Sprintf("f%06d", i)})
	}
	n := &Node{Path: "/huge", Name: "huge", IsDir: true}
	tr := &Tree{Root: n, HideIgnored: true}
	tr.merge(n, DirScan{Path: n.Path, Entries: entries})

	if got := len(n.Children); got != MaxDirChildren+1 {
		t.Fatalf("retained %d nodes for %d entries; the cap is not holding", got, total)
	}
	if want := fmt.Sprintf(moreRowFormat, total-MaxDirChildren); n.Children[len(n.Children)-1].Name != want {
		t.Fatalf("sentinel label: got %q, want %q", n.Children[len(n.Children)-1].Name, want)
	}

	var flat []flatNode
	n.Expanded = true
	flattenInto(n, 0, &flat)
	if got := len(flat); got != MaxDirChildren+2 {
		t.Fatalf("flatten walked %d rows; the render pass is not bounded", got)
	}

	// A second merge of the same listing must not grow the graph — the
	// sentinel from round one is synthetic and has to be rebuilt, never
	// carried over as a surviving dirent.
	tr.merge(n, DirScan{Path: n.Path, Entries: entries})
	if got := len(n.Children); got != MaxDirChildren+1 {
		t.Fatalf("re-merge grew the graph to %d nodes", got)
	}
}

// minSidebarWidthTreeRect is the tree render rect at the app's narrowest
// sidebar: internal/app clamps the sidebar block to minSidebarWidth (18)
// and sidebarRect hands the tree everything but the splitter's column.
// Pinned here so a change to either number surfaces as a scrollbar test
// failure rather than as a silently squeezed label.
const minSidebarWidthTreeRect = 17

// mkFlatTree builds a root holding n same-shaped files, which is the
// only fixture the scrollbar tests need: a listing whose length they
// control exactly.
func mkFlatTree(t *testing.T, n int) *Tree {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("f%03d.go", i)), "x")
	}
	tr, err := New(root)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	return tr
}

// barRunes returns the rune drawn in each row of the render rect's
// rightmost column — the scrollbar's column when one is drawn.
func barRunes(cells []tcell.SimCell, cw, w, h int) []rune {
	out := make([]rune, h)
	for row := 0; row < h; row++ {
		c := cells[row*cw+w-1]
		if len(c.Runes) == 0 {
			out[row] = ' '
			continue
		}
		out[row] = c.Runes[0]
	}
	return out
}

// TestTreeScrollbar_HiddenWhenListingFits: a tree shorter than its list
// area draws no bar at all. A full-height thumb carries no information,
// and the column is worth more to the file names.
func TestTreeScrollbar_HiddenWhenListingFits(t *testing.T) {
	tr := mkFlatTree(t, 4)
	const w, h = 24, 20
	cells, cw := renderAndCollect(t, tr, w, h)

	for row, r := range barRunes(cells, cw, w, h) {
		if r == scrollbar.Track || r == scrollbar.Thumb {
			t.Fatalf("row %d drew bar glyph %q for a listing that fits", row, r)
		}
	}
	if tr.ScrollbarVisible(w, h) {
		t.Fatal("ScrollbarVisible should agree with what was painted")
	}
}

// TestTreeScrollbar_ThumbTracksScroll: an overflowing listing draws a
// shaded track down the list area with a solid thumb at the scaled
// scroll position — and the header rows above the list stay clear,
// because they are pinned and a bar over them would lie.
func TestTreeScrollbar_ThumbTracksScroll(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	// List area = h - 2 = 10 rows for 60 entries: thumb is 1 row.
	tr.ScrollY = 25
	cells, cw := renderAndCollect(t, tr, w, h)
	col := barRunes(cells, cw, w, h)

	for row := 0; row < listHeaderRows; row++ {
		if col[row] == scrollbar.Track || col[row] == scrollbar.Thumb {
			t.Fatalf("pinned header row %d must not carry the bar, got %q", row, col[row])
		}
	}
	wantStart, wantLen, ok := scrollbar.Geom(60, h-listHeaderRows, 25)
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row := 0; row < h-listHeaderRows; row++ {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got := col[listHeaderRows+row]; got != want {
			t.Fatalf("list row %d: got %q, want %q", row, got, want)
		}
	}
	// Scrolling home walks the thumb back to the top of the track.
	tr.ScrollY = 0
	cells, cw = renderAndCollect(t, tr, w, h)
	col = barRunes(cells, cw, w, h)
	if col[listHeaderRows] != scrollbar.Thumb {
		t.Fatalf("at scroll 0 the thumb should sit on the first list row, got %q", col[listHeaderRows])
	}
}

// TestTreeScrollbar_ReservesTheLabelColumn: the bar takes its column out
// of the row width, so a long name is truncated one cell earlier rather
// than painted underneath the bar.
func TestTreeScrollbar_ReservesTheLabelColumn(t *testing.T) {
	const long = "a-really-long-file-name-that-overflows.go"
	const w, h = 24, 12

	// Same long name, two listings: one that overflows the list area
	// (bar) and one that fits (no bar).
	build := func(fillers int) *Tree {
		root := t.TempDir()
		mustWrite(t, filepath.Join(root, long), "x")
		for i := 0; i < fillers; i++ {
			mustWrite(t, filepath.Join(root, fmt.Sprintf("z%03d.go", i)), "x")
		}
		tr, err := New(root)
		if err != nil {
			t.Fatalf("tree: %v", err)
		}
		return tr
	}

	cells, cw := renderAndCollect(t, build(60), w, h)
	row := []rune(rowText(cells, cw, listHeaderRows))
	if row[w-1] != scrollbar.Track && row[w-1] != scrollbar.Thumb {
		t.Fatalf("bar column should hold the bar, got %q (row %q)", row[w-1], string(row))
	}
	if row[w-2] == ' ' {
		t.Fatalf("the truncated name should run right up to the bar, row %q", string(row))
	}

	cells, cw = renderAndCollect(t, build(1), w, h)
	row = []rune(rowText(cells, cw, listHeaderRows))
	if row[w-1] == scrollbar.Track || row[w-1] == scrollbar.Thumb {
		t.Fatalf("no bar expected once the listing fits, row %q", string(row))
	}
	if row[w-1] == ' ' {
		t.Fatalf("without the bar the name should reach the last column, row %q", string(row))
	}
}

// TestTreeScrollbar_HitTestSeparatesColumns: only the rect's last column
// and only the scrollable rows belong to the bar. The pinned header and
// root rows, and every label column, stay tree.
func TestTreeScrollbar_HitTestSeparatesColumns(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	renderAndCollect(t, tr, w, h)

	if !tr.ScrollbarHit(w-1, listHeaderRows, w, h) {
		t.Fatal("first list row of the last column is the bar")
	}
	if !tr.ScrollbarHit(w-1, h-1, w, h) {
		t.Fatal("last list row of the last column is the bar")
	}
	if tr.ScrollbarHit(w-2, listHeaderRows, w, h) {
		t.Fatal("the column left of the bar belongs to the tree row")
	}
	for row := 0; row < listHeaderRows; row++ {
		if tr.ScrollbarHit(w-1, row, w, h) {
			t.Fatalf("pinned row %d is not part of the bar", row)
		}
	}
	if tr.ScrollbarHit(w-1, h, w, h) {
		t.Fatal("below the list area is not the bar")
	}
}

// TestTreeScrollbar_ClickToJump: clicking a bar row scrolls the listing
// there, top and bottom included, and a tree with no bar ignores it.
func TestTreeScrollbar_ClickToJump(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	renderAndCollect(t, tr, w, h)

	tr.ScrollToBarRow(w, h, h-1) // bottom of the bar
	if want := 60 - (h - listHeaderRows); tr.ScrollY != want {
		t.Fatalf("bottom click: ScrollY %d, want %d", tr.ScrollY, want)
	}
	tr.ScrollToBarRow(w, h, listHeaderRows) // top of the bar
	if tr.ScrollY != 0 {
		t.Fatalf("top click: ScrollY %d, want 0", tr.ScrollY)
	}
	mid := h/2 + listHeaderRows/2
	tr.ScrollToBarRow(w, h, mid)
	if tr.ScrollY <= 0 || tr.ScrollY >= 60-(h-listHeaderRows) {
		t.Fatalf("middle click should land inside the range, got %d", tr.ScrollY)
	}

	short := mkFlatTree(t, 3)
	renderAndCollect(t, short, w, h)
	short.ScrollToBarRow(w, h, h-1)
	if short.ScrollY != 0 {
		t.Fatalf("a tree with no bar must not scroll, got %d", short.ScrollY)
	}
}

// TestTreeScrollbar_WidthFloor: the bar is present at the narrowest
// sidebar the app allows (minSidebarWidth 18 → a 17-column tree rect)
// but gives the column back on a rect too narrow to spare it.
func TestTreeScrollbar_WidthFloor(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const h = 12

	// 17 columns is what minSidebarWidth (18) leaves the tree once the
	// splitter takes its column — the narrowest rect a user can drag to.
	// Render first: the bar's proportions are measured against the row
	// count the last paint flattened, exactly like HitTest's row map.
	const minRect = minSidebarWidthTreeRect
	minCells, minCW := renderAndCollect(t, tr, minRect, h)
	if !tr.ScrollbarVisible(minRect, h) {
		t.Fatal("the bar must survive the app's minimum sidebar width")
	}
	if r := barRunes(minCells, minCW, minRect, h)[listHeaderRows]; r != scrollbar.Track && r != scrollbar.Thumb {
		t.Fatalf("bar should paint at a %d-column rect, got %q", minRect, r)
	}

	narrow := minScrollbarWidth - 1
	if tr.ScrollbarVisible(narrow, h) {
		t.Fatalf("a %d-column rect should spend its cells on names", narrow)
	}
	if tr.ScrollbarHit(narrow-1, listHeaderRows, narrow, h) {
		t.Fatal("no bar means no bar clicks")
	}
	cells, cw := renderAndCollect(t, tr, narrow, h)
	for row, r := range barRunes(cells, cw, narrow, h) {
		if r == scrollbar.Track || r == scrollbar.Thumb {
			t.Fatalf("row %d painted a bar on a %d-column rect: %q", row, narrow, r)
		}
	}
}

// TestTreeScrollbar_ThumbBrightensWhileDragging: the tree's thumb speaks
// the same idle-Muted / dragging-Accent language as the editor's bar and
// the sidebar splitter. The track never brightens.
func TestTreeScrollbar_ThumbBrightensWhileDragging(t *testing.T) {
	tr := mkFlatTree(t, 60)
	const w, h = 24, 12
	th := theme.Default()

	cells, cw := renderAndCollect(t, tr, w, h)
	fg, _, _ := cells[listHeaderRows*cw+w-1].Style.Decompose()
	if fg != th.Muted {
		t.Fatalf("idle thumb fg: got %v, want Muted", fg)
	}

	tr.ScrollbarActive = true
	cells, cw = renderAndCollect(t, tr, w, h)
	fg, bg, _ := cells[listHeaderRows*cw+w-1].Style.Decompose()
	if fg != th.Accent {
		t.Fatalf("dragged thumb fg: got %v, want Accent", fg)
	}
	if bg != th.SidebarBG {
		t.Fatalf("the bar sits on the sidebar background, got %v", bg)
	}
	trackFg, _, _ := cells[(h-1)*cw+w-1].Style.Decompose()
	if trackFg != th.Subtle {
		t.Fatalf("track fg while dragging: got %v, want Subtle", trackFg)
	}
}

// mkIgnoreTree builds the fixture the gitignore tests share: a project
// whose root .gitignore excludes a build directory, a log file and a
// dotfile, plus one ordinary source file that must survive every state.
func mkIgnoreTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "dist/\n*.log\n.env\n")
	mustMkdir(t, filepath.Join(root, "dist"))
	mustWrite(t, filepath.Join(root, "dist", "bundle.js"), "// built")
	mustWrite(t, filepath.Join(root, "app.log"), "noise")
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	mustWrite(t, filepath.Join(root, ".env"), "SECRET=1")
	return root
}

// mustSymlink creates a symlink for test setup. A platform that refuses
// (Windows without developer mode) cannot express the fixture at all, so
// the test skips rather than failing a machine for lacking a capability.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

// TestGitignore_HidesIgnoredEntriesUntilToggledOff is the headline
// contract: with filtering on the sidebar answers "is this project
// noise?" the way the finder's index already does, and with it off the
// same directory shows everything again. The toggle is only meaningful
// if both directions work, so both are asserted from one fixture.
func TestGitignore_HidesIgnoredEntriesUntilToggledOff(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.HideIgnored {
		t.Fatal("filtering must default on so the tree and the finder agree out of the box")
	}
	for _, name := range []string{"dist", "app.log"} {
		if findChild(tr.Root, name) != nil {
			t.Fatalf("%q is gitignored and must not be a row", name)
		}
	}
	if findChild(tr.Root, "main.go") == nil {
		t.Fatal("main.go is not ignored and must stay visible")
	}

	tr.HideIgnored = false
	tr.Refresh()
	for _, name := range []string{"dist", "app.log", "main.go"} {
		if findChild(tr.Root, name) == nil {
			t.Fatalf("%q must come back once filtering is off", name)
		}
	}
}

// TestGitignore_DotfilesStayVisibleInBothStates pins the deliberate
// split between the two axes. .env is the most commonly gitignored file
// there is and the least acceptable one to lose over SSH, so gitignore
// filtering never removes a dotfile — including .gitignore itself,
// which would otherwise hide the very rule set the user is debugging.
func TestGitignore_DotfilesStayVisibleInBothStates(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, on := range []bool{true, false} {
		tr.HideIgnored = on
		tr.Refresh()
		for _, name := range []string{".env", ".gitignore"} {
			if findChild(tr.Root, name) == nil {
				t.Fatalf("HideIgnored=%v hid the dotfile %q", on, name)
			}
		}
	}
}

// TestGitignore_NestedFileAppliesToItsSubtreeOnly is the nested-support
// claim stated as behaviour: a rule written in src/.gitignore hides the
// matching name below src/ and leaves the identically-named file at the
// root alone. Getting only one of those two right is the failure mode
// worth catching.
func TestGitignore_NestedFileAppliesToItsSubtreeOnly(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "*.tmp\n")
	mustWrite(t, filepath.Join(root, "generated.go"), "root copy")
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "src", ".gitignore"), "generated.go\n")
	mustWrite(t, filepath.Join(root, "src", "generated.go"), "built")
	mustWrite(t, filepath.Join(root, "src", "real.go"), "package src")
	mustWrite(t, filepath.Join(root, "src", "scratch.tmp"), "x")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "generated.go") == nil {
		t.Fatal("the root's generated.go is outside src/.gitignore's reach and must stay")
	}
	src := findChild(tr.Root, "src")
	if src == nil {
		t.Fatal("src/ missing")
	}
	tr.Toggle(src)

	if findChild(src, "generated.go") != nil {
		t.Fatal("src/.gitignore must hide src/generated.go")
	}
	if findChild(src, "scratch.tmp") != nil {
		t.Fatal("the root .gitignore's *.tmp must still reach into src/")
	}
	if findChild(src, "real.go") == nil {
		t.Fatal("src/real.go matches nothing and must stay visible")
	}
}

// TestGitignore_MatcherCacheFollowsTheChildren pins the cache's one
// invalidation rule: a directory's compiled matcher is replaced exactly
// when its listing is. Editing .gitignore and refreshing must move the
// filter, and an unchanged file must reuse the compiled matcher rather
// than rebuilding a regexp per pattern line on every tick.
func TestGitignore_MatcherCacheFollowsTheChildren(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "a.txt\n")
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "a.txt") != nil || findChild(tr.Root, "b.txt") == nil {
		t.Fatal("initial state should hide a.txt only")
	}

	compiled := tr.ignoreCache[tr.Root.Path].gi
	if compiled == nil {
		t.Fatal("the root .gitignore should be cached after the first read")
	}
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi != compiled {
		t.Fatal("unchanged .gitignore bytes must reuse the compiled matcher")
	}

	mustWrite(t, filepath.Join(root, ".gitignore"), "b.txt\n")
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi == compiled {
		t.Fatal("edited .gitignore must recompile")
	}
	if findChild(tr.Root, "a.txt") == nil {
		t.Fatal("a.txt is no longer ignored and must reappear")
	}
	if findChild(tr.Root, "b.txt") != nil {
		t.Fatal("b.txt is newly ignored and must disappear")
	}

	// Deleting the file drops the entry entirely rather than leaving a
	// stale matcher behind.
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tr.Refresh()
	if _, ok := tr.ignoreCache[tr.Root.Path]; ok {
		t.Fatal("a removed .gitignore must leave no cache entry")
	}
	if findChild(tr.Root, "b.txt") == nil {
		t.Fatal("b.txt must return once the rules are gone")
	}
}

// TestGitignore_OpenTabNeverVanishes is the safety rule: the user can
// legitimately be editing a file inside an ignored directory (a
// generated file, a vendored copy), and the sidebar must not pretend it
// does not exist. The ignored directory reappears carrying exactly the
// pinned file — the rest of the build output stays filtered.
func TestGitignore_OpenTabNeverVanishes(t *testing.T) {
	root := mkIgnoreTree(t)
	mustWrite(t, filepath.Join(root, "dist", "other.js"), "// also built")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("test setup: dist/ should start hidden")
	}

	open := filepath.Join(root, "dist", "bundle.js")
	tr.SetOpenFiles([]string{open})
	tr.Refresh()

	dist := findChild(tr.Root, "dist")
	if dist == nil {
		t.Fatal("the directory holding an open tab must be reachable")
	}
	tr.Toggle(dist)
	if findChild(dist, "bundle.js") == nil {
		t.Fatal("the open file must be a row")
	}
	if findChild(dist, "other.js") != nil {
		t.Fatal("pinning one file must not un-ignore its whole directory")
	}

	// Reveal takes the same path from cold: it pins the target itself and
	// re-reads the ancestors the filter had emptied.
	fresh, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fresh.Reveal(open, 20)
	revealed := findChild(fresh.Root, "dist")
	if revealed == nil || findChild(revealed, "bundle.js") == nil {
		t.Fatal("Reveal must surface a target inside an ignored directory")
	}

	// Closing the tab un-pins it again.
	tr.SetOpenFiles(nil)
	tr.Refresh()
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("dist/ should return to hidden once nothing inside it is open")
	}
}

// TestGitignore_BackgroundScanFiltersLikeRefresh extends the split
// refresh's equivalence contract to the filter. The 10s sweep reads
// directories off-thread and merges on the event loop; if only the
// synchronous path filtered, every tick would flash the build output
// back into the sidebar.
func TestGitignore_BackgroundScanFiltersLikeRefresh(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.ApplyScan(ScanDirs(tr.LoadedDirs()))
	if findChild(tr.Root, "dist") != nil || findChild(tr.Root, "app.log") != nil {
		t.Fatal("the background sweep must apply the same filter Refresh does")
	}
	if findChild(tr.Root, "main.go") == nil {
		t.Fatal("the sweep dropped an unignored file")
	}
}

// TestSymlinkDir_ExpandsThroughTheLink pins the fix for the dirent
// classification bug: e.IsDir() describes the link, not its target, so a
// symlinked package used to render as an unopenable file row. Stat
// resolves it, and the row still says it is a link.
func TestSymlinkDir_ExpandsThroughTheLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	mustMkdir(t, target)
	mustWrite(t, filepath.Join(target, "leaf.go"), "package leaf")
	mustSymlink(t, target, filepath.Join(root, "linked"))
	mustSymlink(t, filepath.Join(root, "nowhere"), filepath.Join(root, "broken"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	linked := findChild(tr.Root, "linked")
	if linked == nil {
		t.Fatal("linked/ missing")
	}
	if !linked.IsDir || !linked.IsLink {
		t.Fatalf("linked should be a directory AND a link: IsDir=%v IsLink=%v", linked.IsDir, linked.IsLink)
	}
	tr.Toggle(linked)
	if findChild(linked, "leaf.go") == nil {
		t.Fatal("expanding a symlinked directory must list its target's children")
	}

	// A dangling link has no target to classify: it stays a file row
	// rather than becoming a directory that can never be read.
	broken := findChild(tr.Root, "broken")
	if broken == nil {
		t.Fatal("broken link missing from the listing")
	}
	if broken.IsDir {
		t.Fatal("a dangling symlink must not be classified as a directory")
	}
	if !broken.IsLink {
		t.Fatal("a dangling symlink is still a link and the row should say so")
	}
}

// TestSymlinkLoop_TerminatesInsteadOfHanging is the regression the
// symlink fix could easily have introduced. Following links without
// tracking resolved ancestors makes `self -> .` and `up -> ..` recurse
// forever on expand AND on the background sweep, so both are driven
// here — under a wall-clock guard, because the failure mode is a hang
// rather than a wrong answer.
func TestSymlinkLoop_TerminatesInsteadOfHanging(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.go"), "package main")
	mustSymlink(t, root, filepath.Join(root, "self"))
	mustSymlink(t, "..", filepath.Join(root, "up"))

	done := make(chan *Tree, 1)
	go func() {
		tr, err := New(root)
		if err != nil {
			done <- nil
			return
		}
		for _, name := range []string{"self", "up"} {
			if n := findChild(tr.Root, name); n != nil {
				tr.Toggle(n)
			}
		}
		// The periodic pipeline walks the same graph; a node that
		// escaped the guard would grow the work list without bound.
		tr.Refresh()
		tr.ApplyScan(ScanDirs(tr.LoadedDirs()))
		done <- tr
	}()

	var tr *Tree
	select {
	case tr = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("symlink loop never terminated — the ancestor guard is not holding")
	}
	if tr == nil {
		t.Fatal("New failed on the loop fixture")
	}

	for _, name := range []string{"self", "up"} {
		n := findChild(tr.Root, name)
		if n == nil {
			t.Fatalf("%q missing from the listing", name)
		}
		if !n.Loop {
			t.Fatalf("%q resolves onto its own ancestor chain and must be marked Loop", name)
		}
		if n.Expanded || len(n.Children) != 0 {
			t.Fatalf("%q must never load children (expanded=%v, %d children)", name, n.Expanded, len(n.Children))
		}
	}
	if got := len(tr.LoadedDirs()); got != 1 {
		t.Fatalf("only the root should be loaded, got %d directories: %v", got, tr.LoadedDirs())
	}
	if findChild(tr.Root, "real.go") == nil {
		t.Fatal("the loop guard must not cost the directory its real entries")
	}
}

// TestSymlinkLoop_CatchesMutualLinks covers the case a parent-only check
// misses: two links that each point at the other's directory look
// innocent one hop at a time and only close the cycle two levels down.
// Comparing against every ancestor's resolved path is what catches it.
func TestSymlinkLoop_CatchesMutualLinks(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	mustMkdir(t, a)
	mustMkdir(t, b)
	mustSymlink(t, b, filepath.Join(a, "toB"))
	mustSymlink(t, a, filepath.Join(b, "toA"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nodeA := findChild(tr.Root, "a")
	if nodeA == nil {
		t.Fatal("a/ missing")
	}
	tr.Toggle(nodeA)
	toB := findChild(nodeA, "toB")
	if toB == nil {
		t.Fatal("a/toB missing")
	}
	if toB.Loop {
		t.Fatal("a/toB points at a sibling, not an ancestor — it must stay expandable")
	}
	tr.Toggle(toB)

	toA := findChild(toB, "toA")
	if toA == nil {
		t.Fatal("a/toB/toA missing")
	}
	if !toA.Loop {
		t.Fatal("a/toB/toA resolves back onto ancestor a/ and must be refused")
	}
	tr.Toggle(toA)
	if toA.Expanded || len(toA.Children) != 0 {
		t.Fatal("a refused link must not open")
	}
}

// TestRender_SymlinkRowsAreMarked checks the user-visible half: a link
// is not silently drawn as an ordinary row, and a refused link says why
// instead of offering a chevron onto nothing.
func TestRender_SymlinkRowsAreMarked(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	mustMkdir(t, target)
	mustSymlink(t, target, filepath.Join(root, "linked"))
	mustSymlink(t, root, filepath.Join(root, "self"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = false
	cells, w := renderAndCollect(t, tr, 44, 12)

	linkedY := findRowY(cells, w, 12, "linked/")
	if linkedY < 0 {
		t.Fatal("linked/ row not rendered")
	}
	if !strings.Contains(rowText(cells, w, linkedY), SymlinkLabel) {
		t.Fatalf("link row should carry %q: %q", SymlinkLabel, rowText(cells, w, linkedY))
	}
	if !strings.Contains(rowText(cells, w, linkedY), "▸") {
		t.Fatalf("an openable link keeps its chevron: %q", rowText(cells, w, linkedY))
	}

	selfY := findRowY(cells, w, 12, LoopLabel)
	if selfY < 0 {
		t.Fatalf("the refused link must be labelled %q", LoopLabel)
	}
	if strings.Contains(rowText(cells, w, selfY), "▸") {
		t.Fatalf("a link that never opens must not draw a chevron: %q", rowText(cells, w, selfY))
	}
	// The real directory next to it is unaffected — no stray markers.
	targetY := findRowY(cells, w, 12, "target/")
	if targetY < 0 {
		t.Fatal("target/ row not rendered")
	}
	if strings.Contains(rowText(cells, w, targetY), SymlinkLabel) {
		t.Fatalf("an ordinary directory must not be marked as a link: %q", rowText(cells, w, targetY))
	}
}

// mkChain builds root/{a/b/c/inner.txt, top.txt}: a three-directory
// single-child chain plus a root-level sibling file — the fixture the
// compact-folder tests share.
func mkChain(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a"))
	mustMkdir(t, filepath.Join(root, "a", "b"))
	mustMkdir(t, filepath.Join(root, "a", "b", "c"))
	mustWrite(t, filepath.Join(root, "a", "b", "c", "inner.txt"), "x")
	mustWrite(t, filepath.Join(root, "top.txt"), "t")
	return root
}

// TestFlattenInto_CompactsSingleDirChain is the core compact-folders
// behavior: a directory whose only child is another directory folds
// into one row carrying the joined path, anchored on the deepest node,
// with the deepest dir's children rendered directly beneath it.
func TestFlattenInto_CompactsSingleDirChain(t *testing.T) {
	leaf := &Node{Name: "inner.txt"}
	c := &Node{Name: "c", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{leaf}}
	b := &Node{Name: "b", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{c}}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{b}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 2 {
		t.Fatalf("expected chain row + leaf, got %d rows", len(out))
	}
	if out[0].Node != c || out[0].Top != a || out[0].Display != "a/b/c" {
		t.Fatalf("chain row wrong: %+v", out[0])
	}
	if out[1].Node != leaf || out[1].Depth != 1 {
		t.Fatalf("leaf should sit directly under the chain row: %+v", out[1])
	}
}

// TestFlattenInto_ChainStopsAtUnloadedDir gates compaction on
// knowledge: a directory whose children were never read cannot claim
// to have exactly one — the chain ends at the last loaded link, so a
// stale claim can never fold a dir whose siblings just haven't been
// seen yet.
func TestFlattenInto_ChainStopsAtUnloadedDir(t *testing.T) {
	c := &Node{Name: "c", IsDir: true}
	b := &Node{Name: "b", IsDir: true, Loaded: false, Children: []*Node{c}}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Children: []*Node{b}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 1 {
		t.Fatalf("expected 1 row, got %d", len(out))
	}
	if out[0].Node != b || out[0].Display != "a/b" {
		t.Fatalf("chain should stop at the unloaded dir: %+v", out[0])
	}
}

// TestFlattenInto_SiblingBreaksChain: two children means no chain —
// the dir renders alone and its children indent under it as before.
func TestFlattenInto_SiblingBreaksChain(t *testing.T) {
	b := &Node{Name: "b", IsDir: true}
	x := &Node{Name: "x.txt"}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{b, x}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[0].Node != a || out[0].Display != "" {
		t.Fatalf("row 0 should be plain a: %+v", out[0])
	}
}

// TestFlattenInto_FileChildDoesNotCompact: a single child that is a
// file keeps the dir as an ordinary row — only dir-into-dir folds.
func TestFlattenInto_FileChildDoesNotCompact(t *testing.T) {
	f := &Node{Name: "only.txt"}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{f}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 2 || out[0].Node != a || out[0].Display != "" {
		t.Fatalf("file child must not fold: %+v", out)
	}
}

// TestFlattenInto_LinksNeverJoinChains: a symlink keeps its own row in
// both directions — a link is never absorbed as a chain segment (its
// "(symlink)" label would vanish mid-path) and a link never absorbs
// its child.
func TestFlattenInto_LinksNeverJoinChains(t *testing.T) {
	linkChild := &Node{Name: "b", IsDir: true, IsLink: true}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Children: []*Node{linkChild}}
	var out []flatNode
	flattenInto(a, 0, &out)
	if len(out) != 1 || out[0].Node != a || out[0].Display != "" {
		t.Fatalf("link child must not be absorbed: %+v", out)
	}

	plain := &Node{Name: "d", IsDir: true}
	link := &Node{Name: "l", IsDir: true, IsLink: true, Loaded: true, Children: []*Node{plain}}
	out = out[:0]
	flattenInto(link, 0, &out)
	if len(out) != 1 || out[0].Node != link || out[0].Display != "" {
		t.Fatalf("a link must not absorb its child: %+v", out)
	}
}

// TestToggle_ExpandsChainToDeepest pins the one-click contract: expanding
// a dir that turns out to head a single-child chain loads and expands
// every link down to the deepest, so the compact row appears open after
// one click instead of demanding a click per segment.
func TestToggle_ExpandsChainToDeepest(t *testing.T) {
	root := mkChain(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := findChild(tr.Root, "a")
	tr.Toggle(a)

	b := findChild(a, "b")
	if b == nil || !b.Loaded || !b.Expanded {
		t.Fatalf("b should be loaded+expanded after the chain toggle: %+v", b)
	}
	c := findChild(b, "c")
	if c == nil || !c.Loaded || !c.Expanded {
		t.Fatalf("c should be loaded+expanded after the chain toggle: %+v", c)
	}
}

// TestFlatIndexOf_FindsDirFoldedIntoChain: a directory that no longer
// has its own row still resolves to the chain row that contains it —
// otherwise revealing it would silently fail to scroll.
func TestFlatIndexOf_FindsDirFoldedIntoChain(t *testing.T) {
	tr, err := New(mkChain(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	c := findChild(b, "c")

	ci := tr.flatIndexOf(c)
	if ci < 0 {
		t.Fatal("deepest chain dir must be indexable")
	}
	if bi := tr.flatIndexOf(b); bi != ci {
		t.Fatalf("mid-chain dir should resolve to the chain row: got %d, want %d", bi, ci)
	}
	if ai := tr.flatIndexOf(a); ai != ci {
		t.Fatalf("chain top should resolve to the chain row: got %d, want %d", ai, ci)
	}
}

// TestRender_CompactChainShowsJoinedPath: the screen shows one
// "a/b/c/" row with the expanded chevron, and no standalone "b/" row —
// the visual half of the compact-folders contract.
func TestRender_CompactChainShowsJoinedPath(t *testing.T) {
	tr, err := New(mkChain(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.Toggle(findChild(tr.Root, "a"))

	cells, w := renderAndCollect(t, tr, 40, 20)
	if !findRowWithBoth(cells, w, 20, "a/b/c/", '▾') {
		t.Fatal("expected an expanded chain row showing a/b/c/")
	}
	for y := 0; y < 20; y++ {
		row := rowText(cells, w, y)
		if strings.Contains(row, "b/") && !strings.Contains(row, "a/b/c/") {
			t.Fatalf("mid-chain dir must not get its own row: %q", row)
		}
	}
}

// TestRender_ActiveFolderInsideChainHighlights: making a folded-away
// mid-chain dir the active folder must light up the chain row that
// contains it — the row is that dir's only representation on screen.
func TestRender_ActiveFolderInsideChainHighlights(t *testing.T) {
	tr, err := New(mkChain(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	tr.ActiveFolder = b.Path

	cells, w := renderAndCollect(t, tr, 40, 20)
	y := findRowY(cells, w, 20, "a/b/c/")
	if y < 0 {
		t.Fatal("chain row not rendered")
	}
	fg, _, attrs := cells[y*w+2].Style.Decompose()
	if fg != theme.Default().Accent || attrs&tcell.AttrBold == 0 {
		t.Fatalf("chain row must show the active-folder highlight: fg=%v attrs=%v", fg, attrs)
	}
}

// TestReveal_FileInsideCompactChain: revealing a file whose ancestors
// fold into a chain must land the viewport on the file's actual row —
// the scroll math has to agree with the compacted render order.
func TestReveal_FileInsideCompactChain(t *testing.T) {
	root := mkChain(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.ScrollY = 9 // far past the handful of real rows
	tr.Reveal(filepath.Join(root, "a", "b", "c", "inner.txt"), 2)

	a := findChild(tr.Root, "a")
	b := findChild(a, "b")
	c := findChild(b, "c")
	inner := findChild(c, "inner.txt")
	idx := tr.flatIndexOf(inner)
	if idx < 0 {
		t.Fatal("revealed file must be indexable")
	}
	if tr.ScrollY != idx {
		t.Fatalf("viewport should scroll to the revealed row: ScrollY=%d, want %d", tr.ScrollY, idx)
	}
}

// TestRender_DotDirChainRendersMuted: a chain headed by a dot-dir reads
// as metadata exactly like a standalone dot-dir — the mute keys off the
// label the user sees, not the deepest segment's name.
func TestRender_DotDirChainRendersMuted(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".config"))
	mustMkdir(t, filepath.Join(root, ".config", "app"))
	mustWrite(t, filepath.Join(root, ".config", "app", "cfg.txt"), "x")
	mustWrite(t, filepath.Join(root, "main.go"), "package m")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.Toggle(findChild(tr.Root, ".config"))

	cells, w := renderAndCollect(t, tr, 40, 20)
	y := findRowY(cells, w, 20, ".config/app/")
	if y < 0 {
		t.Fatal("dot chain row not rendered")
	}
	fg, _, _ := cells[y*w+2].Style.Decompose()
	if fg != theme.Default().Muted {
		t.Fatalf("dot chain row fg = %v, want Muted", fg)
	}
}
