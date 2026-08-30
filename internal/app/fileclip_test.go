// =============================================================================
// File: internal/app/fileclip_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the file clipboard: cut / copy / paste / duplicate of tree
// entries through the background runner, the clipboard's own state
// across those, and what the done event does on the main loop — tabs
// following a move, the refreshes on success AND failure. The naming
// ladder and the guards are the manager's (internal/filemanager); the
// refusal tests here pin that their errors surface as flashes.

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/finder"
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
	a.pasteInto(sub2)
	pumpUntil(t, a, "file op", idle(&a.fileOp))
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
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
// descendant) would eat the folder — the manager refuses inside the
// background op, and the refusal has to come back as a flash with the
// disk untouched and the clipboard kept.
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
	if !strings.Contains(a.statusMsg, "into itself") {
		t.Fatalf("expected the into-itself flash, got %q", a.statusMsg)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("refused paste must not disturb the tree: %v", err)
	}
	if !a.hasFileClip() {
		t.Fatal("refused paste should keep the clipboard for a retry")
	}
}

// TestPasteInto_SymlinkedDescendantRefused: the unresolved-path
// comparison in the into-itself guard has a hole — a destination that
// LOOKS unrelated to src by string prefix but RESOLVES (via a symlink)
// to somewhere inside src must be refused too, or the copy would walk
// into its own growing output. The fixture: root/outer/inner is a real
// directory; root/elsewhere/link is a symlink pointing at outer itself,
// so pasting outer into elsewhere/link resolves to pasting outer into
// outer — no different in effect from the plain into-itself case, just
// reached through a symlink the unresolved-string check can't see. The
// manager owns the resolution; this pins that its answer reaches the
// user through the app's paste path.
func TestPasteInto_SymlinkedDescendantRefused(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	if err := os.MkdirAll(filepath.Join(outer, "inner"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(elsewhere, "link")
	if err := os.Symlink(outer, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a := newTestApp(t, root)

	a.clipCutPath(outer)
	a.pasteInto(link)
	pumpUntil(t, a, "file op", idle(&a.fileOp))

	if !strings.Contains(a.statusMsg, "into itself") {
		t.Fatalf("expected the into-itself flash, got %q", a.statusMsg)
	}
	if _, err := os.Stat(filepath.Join(outer, "inner")); err != nil {
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
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
	pumpUntil(t, a, "file op", idle(&a.fileOp))
	if _, err := os.Stat(filepath.Join(root, "a copy.txt")); err != nil {
		t.Fatalf("duplicate missing: %v", err)
	}
}

// TestHandleFileOpDone_FailureStillRefreshesFinderAndGit pins the error
// tail of a background move. A move that dies part-way (here the
// destination folder does not exist; a cross-device copy that runs out
// of disk is the field case) may already have changed the disk, so the
// finder index and the git tint have to be refreshed exactly as a
// successful op refreshes them. The error path used to refresh only the
// tree and left both stale.
func TestHandleFileOpDone_FailureStillRefreshesFinderAndGit(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	a := newTestApp(t, root)
	rebuilds := 0
	a.finder = finder.New(root)
	// Run the index build inline so the count is settled by the time
	// the assertion reads it, with no goroutine to drain.
	a.finder.PanicGuard = func(_ string, fn func()) { rebuilds++; fn() }
	if a.gitStatus.Busy() {
		t.Fatal("setup: no git refresh should be in flight before the op")
	}

	a.clipCutPath(src)
	a.pasteInto(filepath.Join(root, "nope"))
	pumpUntil(t, a, "file op", idle(&a.fileOp))

	if !strings.Contains(a.statusMsg, "failed") {
		t.Fatalf("expected the failure flash, got %q", a.statusMsg)
	}
	if rebuilds == 0 {
		t.Fatal("a failed move must invalidate and rebuild the finder index")
	}
	if !a.gitStatus.Busy() && !a.gitStatus.Queued() {
		t.Fatal("a failed move must request a git status refresh")
	}
}

// TestFileOpGate_IsSeparateFromProjectReplace pins the split of the two
// gates: a project replace in flight is no reason to refuse a paste —
// the features are unrelated and used to share one busy flag.
func TestFileOpGate_IsSeparateFromProjectReplace(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "a.txt", "data")
	a := newTestApp(t, root)
	release := make(chan struct{})
	a.projReplace.Start(func(context.Context) (projReplaceResult, error) {
		<-release
		return projReplaceResult{}, nil
	})

	a.duplicatePath(src)
	pumpUntil(t, a, "file op", idle(&a.fileOp))

	if _, err := os.Stat(filepath.Join(root, "a copy.txt")); err != nil {
		t.Fatalf("duplicate should run while a replace is busy: %v", err)
	}
	if !strings.Contains(a.statusMsg, "Duplicated") {
		t.Fatalf("expected the done flash, got %q", a.statusMsg)
	}
	close(release)
	pumpUntil(t, a, "replace", idle(&a.projReplace))
}
