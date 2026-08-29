// =============================================================================
// File: internal/filetree/helpers_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

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
