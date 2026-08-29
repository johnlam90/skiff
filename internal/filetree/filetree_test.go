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
)

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

// realTempDir returns a t.TempDir with symlinks resolved. The merge
// fast-path deliberately refuses nodes whose Real differs from Path
// (an ancestor symlink can retarget under us), and on macOS the temp
// root itself lives behind /var -> /private/var — resolving up front
// keeps these tests about the fast-path, not the platform.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

// childrenBacking returns the address of a node's children backing
// array — the cheap witness that distinguishes the fast-path (slice
// untouched) from a full merge (fresh slice, even when every pointer
// in it survived).
func childrenBacking(n *Node) *(*Node) {
	if len(n.Children) == 0 {
		return nil
	}
	return &n.Children[0]
}

// TestMerge_FastPathPreservesNodes pins the tick's quiescent case: an
// unchanged scan of an unchanged directory returns before the filter/
// sort/alloc pipeline, so the children slice — not just the *Node
// pointers in it — is exactly the one the previous merge built, and
// merge reports no membership change.
func TestMerge_FastPathPreservesNodes(t *testing.T) {
	root := realTempDir(t)
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(tr.Root.lastScan) == 0 {
		t.Fatal("the first merge should stamp the raw-scan fingerprint")
	}
	before := tr.Root.Children
	beforeBacking := childrenBacking(tr.Root)

	if changed := tr.merge(tr.Root, readDir(root)); changed {
		t.Fatal("an unchanged scan must not report a membership change")
	}
	if childrenBacking(tr.Root) != beforeBacking {
		t.Fatal("an unchanged scan must keep the children slice, not rebuild it")
	}
	for i, c := range tr.Root.Children {
		if c != before[i] {
			t.Fatalf("child %d lost node identity across the fast-path", i)
		}
	}
}

// TestMerge_FastPathDisabledForSymlinks pins the safety rule: Real and
// Loop are recomputed on every full merge precisely because an
// ancestor's link target can move, so a listing containing any symlink
// must take the full path every time.
func TestMerge_FastPathDisabledForSymlinks(t *testing.T) {
	root := realTempDir(t)
	target := filepath.Join(root, "target")
	mustMkdir(t, target)
	mustSymlink(t, target, filepath.Join(root, "linked"))
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	beforeBacking := childrenBacking(tr.Root)

	tr.merge(tr.Root, readDir(root))
	if childrenBacking(tr.Root) == beforeBacking {
		t.Fatal("a listing with a symlink must take the full merge")
	}
}

// TestMerge_ChangeInIgnoreBytesForcesFullMerge: the directory's own
// .gitignore is one of the merge's filter inputs, so edited bytes must
// defeat the fast-path and refilter — and count as a change, because
// they move the finder's membership too.
func TestMerge_ChangeInIgnoreBytesForcesFullMerge(t *testing.T) {
	root := realTempDir(t)
	mustWrite(t, filepath.Join(root, ".gitignore"), "a.txt\n")
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "a.txt") != nil {
		t.Fatal("setup: a.txt should start hidden")
	}

	mustWrite(t, filepath.Join(root, ".gitignore"), "b.txt\n")
	if changed := tr.merge(tr.Root, readDir(root)); !changed {
		t.Fatal("edited ignore bytes must report a change")
	}
	if findChild(tr.Root, "a.txt") == nil || findChild(tr.Root, "b.txt") != nil {
		t.Fatal("edited ignore bytes must refilter the children")
	}
}

// TestMerge_EntryAddedForcesFullMerge: a name appearing or vanishing is
// exactly what the fast-path exists to distinguish from noise — both
// directions must rebuild and report the change.
func TestMerge_EntryAddedForcesFullMerge(t *testing.T) {
	root := realTempDir(t)
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mustWrite(t, filepath.Join(root, "new.txt"), "n")
	if changed := tr.merge(tr.Root, readDir(root)); !changed {
		t.Fatal("an added entry must report a membership change")
	}
	if findChild(tr.Root, "new.txt") == nil {
		t.Fatal("the added entry must appear")
	}

	if err := os.Remove(filepath.Join(root, "new.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if changed := tr.merge(tr.Root, readDir(root)); !changed {
		t.Fatal("a removed entry must report a membership change")
	}
	if findChild(tr.Root, "new.txt") != nil {
		t.Fatal("the removed entry must vanish")
	}
}

// TestMerge_PinChangeForcesFullMerge guards the open-tab exemption
// against the fast-path: pinning a path inside an ignored directory is
// a filter-input change the raw listing cannot see, so the epoch bump
// must force the full merge that surfaces the entry — and un-pinning
// must re-hide it, exactly as before the fast-path existed.
func TestMerge_PinChangeForcesFullMerge(t *testing.T) {
	root := realTempDir(t)
	mustWrite(t, filepath.Join(root, ".gitignore"), "dist/\n")
	mustMkdir(t, filepath.Join(root, "dist"))
	mustWrite(t, filepath.Join(root, "dist", "bundle.js"), "// built")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("setup: dist/ should start hidden")
	}

	tr.SetOpenFiles([]string{filepath.Join(root, "dist", "bundle.js")})
	tr.merge(tr.Root, readDir(root))
	if findChild(tr.Root, "dist") == nil {
		t.Fatal("pinning must defeat the fast-path and surface the directory")
	}

	tr.SetOpenFiles(nil)
	tr.merge(tr.Root, readDir(root))
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("un-pinning must defeat the fast-path and re-hide the directory")
	}
}

// TestMerge_HideIgnoredFlipForcesFullMerge: the visibility toggle is a
// merge input carried in the fingerprint's flag byte, so flipping it
// must refilter even though the raw listing is byte-identical.
func TestMerge_HideIgnoredFlipForcesFullMerge(t *testing.T) {
	root := realTempDir(t)
	mustWrite(t, filepath.Join(root, ".gitignore"), "a.txt\n")
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "a.txt") != nil {
		t.Fatal("setup: a.txt should start hidden")
	}

	tr.HideIgnored = false
	tr.merge(tr.Root, readDir(root))
	if findChild(tr.Root, "a.txt") == nil {
		t.Fatal("flipping HideIgnored off must defeat the fast-path")
	}
}

// TestMerge_AncestorIgnoreEditRefiltersChildren is the cross-directory
// soundness case the per-directory bytes check alone cannot catch: an
// edited root .gitignore changes what a SUBDIRECTORY's unchanged
// listing filters to, so the epoch bump must push every later merge in
// the same sweep (ancestors merge first — both Refresh and ApplyScan
// walk top-down) through the full path.
func TestMerge_AncestorIgnoreEditRefiltersChildren(t *testing.T) {
	root := realTempDir(t)
	mustWrite(t, filepath.Join(root, ".gitignore"), "nothing\n")
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWrite(t, filepath.Join(root, "sub", "gen.txt"), "g")
	mustWrite(t, filepath.Join(root, "sub", "keep.txt"), "k")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sub := findChild(tr.Root, "sub")
	tr.Toggle(sub)
	if findChild(sub, "gen.txt") == nil {
		t.Fatal("setup: gen.txt should start visible")
	}

	mustWrite(t, filepath.Join(root, ".gitignore"), "gen.txt\n")
	tr.Refresh()
	if findChild(sub, "gen.txt") != nil {
		t.Fatal("an ancestor ignore edit must refilter the subdirectory on the same sweep")
	}
	if findChild(sub, "keep.txt") == nil {
		t.Fatal("the refilter must keep unignored entries")
	}
}

// BenchmarkMergeUnchanged measures the tick's steady state: one merge
// of a 1000-entry directory whose scan is identical to the last one.
// The fast-path turns this from a filter+sort+alloc pass per loaded
// directory every ten seconds into a fingerprint walk with no
// allocations.
func BenchmarkMergeUnchanged(b *testing.B) {
	root, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatalf("EvalSymlinks: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.txt", i)), []byte("x"), 0o644); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	tr, err := New(root)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	ds := readDir(root)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tr.merge(tr.Root, ds)
	}
}
