// =============================================================================
// File: internal/app/fileclip_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the file clipboard: cut / copy / paste / duplicate of tree
// entries, the never-overwrite naming rule, and open tabs following a
// move.

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFile writes content to dir/name (creating parents) and returns the path.
func mkFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestUniqueDestPathSuffixes pins the collision ladder: free name is
// used as-is, then " copy", then " copy 2" — the suffix lands before
// the extension for files and at the end for directories.
func TestUniqueDestPathSuffixes(t *testing.T) {
	dir := t.TempDir()
	if got := uniqueDestPath(dir, "app.ts", false); got != filepath.Join(dir, "app.ts") {
		t.Fatalf("free name: got %q", got)
	}
	mkFile(t, dir, "app.ts", "x")
	if got := uniqueDestPath(dir, "app.ts", false); got != filepath.Join(dir, "app copy.ts") {
		t.Fatalf("first collision: got %q", got)
	}
	mkFile(t, dir, "app copy.ts", "x")
	if got := uniqueDestPath(dir, "app.ts", false); got != filepath.Join(dir, "app copy 2.ts") {
		t.Fatalf("second collision: got %q", got)
	}
	// Directories: the whole name is the stem, dots included.
	if err := os.MkdirAll(filepath.Join(dir, "v1.2"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := uniqueDestPath(dir, "v1.2", true); got != filepath.Join(dir, "v1.2 copy") {
		t.Fatalf("dir collision: got %q", got)
	}
}

// TestPasteCopyKeepsClip: a copied file can be pasted into several
// folders — the clipboard survives each paste.
func TestPasteCopyKeepsClip(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	sub1 := filepath.Join(root, "s1")
	sub2 := filepath.Join(root, "s2")
	for _, d := range []string{sub1, sub2} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	a := newTestApp(t, root)

	a.clipCopyPath(src)
	a.pasteInto(sub1)
	a.pasteInto(sub2)
	for _, want := range []string{filepath.Join(sub1, "a.txt"), filepath.Join(sub2, "a.txt")} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("missing pasted copy %s: %v", want, err)
		}
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("copy must leave the source alone: %v", err)
	}
	if !a.hasFileClip() {
		t.Fatal("copy-paste should keep the clipboard")
	}
}

// TestPasteCutMovesAndClearsClip: cut + paste relocates the file and
// empties the clipboard — a second paste has nothing to do.
func TestPasteCutMovesAndClearsClip(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := newTestApp(t, root)

	a.clipCutPath(src)
	a.pasteInto(sub)
	if _, err := os.Stat(filepath.Join(sub, "a.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("cut source should be gone, stat err = %v", err)
	}
	if a.hasFileClip() {
		t.Fatal("cut-paste should clear the clipboard")
	}
}

// TestPasteDirIntoItselfRefused: pasting a cut folder into itself (or a
// descendant) would eat the folder — it must refuse and leave the disk
// untouched.
func TestPasteDirIntoItselfRefused(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "outer", "inner")
	if err := os.MkdirAll(inner, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := newTestApp(t, root)

	outer := filepath.Join(root, "outer")
	a.clipCutPath(outer)
	a.pasteInto(inner)
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("refused paste must not disturb the tree: %v", err)
	}
	if !a.hasFileClip() {
		t.Fatal("refused paste should keep the clipboard for a retry")
	}
}

// TestPasteOntoFileLandsBeside: pasting with a *file* row as the target
// drops the entry next to that file — no need to aim at the folder row.
func TestPasteOntoFileLandsBeside(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	target := mkFile(t, root, "sub/b.txt", "other")
	a := newTestApp(t, root)

	a.clipCopyPath(src)
	a.pasteInto(pasteDirForPath(target, false))
	if _, err := os.Stat(filepath.Join(root, "sub", "a.txt")); err != nil {
		t.Fatalf("paste-onto-file should land beside it: %v", err)
	}
}

// TestMoveUpdatesOpenTabs: cutting a folder and pasting it elsewhere
// rewrites the paths of every open tab living inside it, so buffers
// stay attached to their (moved) files.
func TestMoveUpdatesOpenTabs(t *testing.T) {
	root := t.TempDir()
	inside := mkFile(t, root, "pkg/file.go", "package pkg")
	dest := filepath.Join(root, "moved")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(inside)

	a.clipCutPath(filepath.Join(root, "pkg"))
	a.pasteInto(dest)
	want := filepath.Join(dest, "pkg", "file.go")
	if got := a.activeTabPtr().Path; got != want {
		t.Fatalf("tab path: got %q, want %q", got, want)
	}
}

// TestDuplicateInPlace: Duplicate is copy+paste-beside in one gesture,
// using the same " copy" naming ladder.
func TestDuplicateInPlace(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	a := newTestApp(t, root)

	a.duplicatePath(src)
	if _, err := os.Stat(filepath.Join(root, "a copy.txt")); err != nil {
		t.Fatalf("duplicate missing: %v", err)
	}
}
