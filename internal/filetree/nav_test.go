// =============================================================================
// File: internal/filetree/nav_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"os"
	"path/filepath"
	"testing"
)

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
