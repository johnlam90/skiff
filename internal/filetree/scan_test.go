// =============================================================================
// File: internal/filetree/scan_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
