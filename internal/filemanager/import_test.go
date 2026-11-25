// =============================================================================
// File: internal/filemanager/import_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for Import: sources from outside the project, the never-overwrite
// ladder, the guards, the keep-going rule for a batch, and the move
// flavour that adopts a staged entry.

package filemanager

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// readText returns a file's content, failing the test when it cannot be
// read — every Import assertion ends in "and the bytes are there".
func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestImport_FileFromOutside is the reason Import exists: Copy refuses a
// source outside the project, and a dropped file always is one.
func TestImport_FileFromOutside(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	src := mkFile(t, outside, "photo.png", "pixels")
	sub := mkDir(t, root, "assets")

	cs, err := m.Import([]string{src}, sub, false, nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	dest := filepath.Join(sub, "photo.png")
	if got := readText(t, dest); got != "pixels" {
		t.Errorf("imported content = %q, want %q", got, "pixels")
	}
	if !exists(src) {
		t.Error("a copy-import removed its source")
	}
	if !reflect.DeepEqual(cs.Added, []string{dest}) || len(cs.Moved) != 0 || len(cs.Removed) != 0 {
		t.Errorf("Changeset = %+v, want one Added %q", cs, dest)
	}
}

// TestImport_FolderTree pins that a dropped folder arrives whole,
// nested entries included.
func TestImport_FolderTree(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	mkFile(t, outside, "kit/a.txt", "A")
	mkFile(t, outside, "kit/deep/b.txt", "B")

	cs, err := m.Import([]string{filepath.Join(outside, "kit")}, root, false, nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readText(t, filepath.Join(root, "kit", "deep", "b.txt")); got != "B" {
		t.Errorf("nested file = %q, want %q", got, "B")
	}
	if !reflect.DeepEqual(cs.Added, []string{filepath.Join(root, "kit")}) {
		t.Errorf("Added = %q, want the one top-level folder", cs.Added)
	}
}

// TestImport_NeverOverwrites pins the project's rule for every paste: a
// taken name walks the " copy" ladder and the file already there keeps
// its bytes.
func TestImport_NeverOverwrites(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	mkFile(t, root, "notes.md", "mine")
	src := mkFile(t, outside, "notes.md", "theirs")

	cs, err := m.Import([]string{src}, root, false, nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readText(t, filepath.Join(root, "notes.md")); got != "mine" {
		t.Errorf("existing file = %q, want it untouched", got)
	}
	dest := filepath.Join(root, "notes copy.md")
	if got := readText(t, dest); got != "theirs" {
		t.Errorf("import landed %q at %s, want %q", got, dest, "theirs")
	}
	if !reflect.DeepEqual(cs.Added, []string{dest}) {
		t.Errorf("Added = %q, want %q", cs.Added, dest)
	}
}

// TestImport_SeveralSources pins batch order: Added lists the
// destinations in the order the sources were given.
func TestImport_SeveralSources(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	a := mkFile(t, outside, "a.txt", "A")
	b := mkFile(t, outside, "b.txt", "B")

	cs, err := m.Import([]string{a, b}, root, false, nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := []string{filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")}
	if !reflect.DeepEqual(cs.Added, want) {
		t.Errorf("Added = %q, want %q", cs.Added, want)
	}
}

// TestImport_FolderContainingTheProject pins the into-itself guard for
// the case only an outside source can produce: dropping a folder that
// holds the project would copy the project into its own growing copy.
func TestImport_FolderContainingTheProject(t *testing.T) {
	outer := t.TempDir()
	root := mkDir(t, outer, "project")
	m := New(root)

	cs, err := m.Import([]string{outer}, root, false, nil)
	if !errors.Is(err, ErrIntoItself) {
		t.Fatalf("Import of an ancestor: err = %v, want ErrIntoItself", err)
	}
	if !cs.Empty() {
		t.Errorf("Changeset = %+v, want empty", cs)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("the refused import still created %d entries in the project", len(entries))
	}
}

// TestImport_RootIsNeverASource pins that the project root cannot be
// imported into one of its own folders.
func TestImport_RootIsNeverASource(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	sub := mkDir(t, root, "sub")

	if _, err := m.Import([]string{root}, sub, false, nil); !errors.Is(err, ErrProjectRoot) {
		t.Errorf("Import of the root: err = %v, want ErrProjectRoot", err)
	}
}

// TestImport_DestinationRefusals pins the two destination guards: the
// target has to be inside the project, and it has to be a folder.
func TestImport_DestinationRefusals(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	src := mkFile(t, outside, "a.txt", "A")
	file := mkFile(t, root, "plain.txt", "x")

	if _, err := m.Import([]string{src}, outside, false, nil); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("destination outside the project: err = %v, want ErrOutsideRoot", err)
	}
	_, err := m.Import([]string{src}, file, false, nil)
	if err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Errorf("destination that is a file: err = %v, want \"is not a folder\"", err)
	}
	if _, err := m.Import([]string{src}, filepath.Join(root, "gone"), false, nil); err == nil {
		t.Error("destination that does not exist: want an error")
	}
}

// TestImport_OneFailureDoesNotStopTheRest pins the batch rule: a source
// that vanished is reported by name while the others still land, and
// the Changeset says exactly what did.
func TestImport_OneFailureDoesNotStopTheRest(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	ok := mkFile(t, outside, "ok.txt", "fine")
	missing := filepath.Join(outside, "vanished.txt")

	cs, err := m.Import([]string{missing, ok}, root, false, nil)
	if err == nil || !strings.Contains(err.Error(), "vanished.txt") {
		t.Fatalf("err = %v, want one naming vanished.txt", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want it to wrap os.ErrNotExist", err)
	}
	dest := filepath.Join(root, "ok.txt")
	if !reflect.DeepEqual(cs.Added, []string{dest}) {
		t.Errorf("Added = %q, want %q", cs.Added, dest)
	}
	if got := readText(t, dest); got != "fine" {
		t.Errorf("surviving import = %q, want %q", got, "fine")
	}
}

// TestImport_FailedFolderLeavesNoPartialCopy pins the cleanup: a folder
// whose copy dies half-way must not leave a truncated tree in the
// project for the user to mistake for the real thing.
func TestImport_FailedFolderLeavesNoPartialCopy(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	mkFile(t, outside, "kit/a.txt", "A")
	if err := syscall.Mkfifo(filepath.Join(outside, "kit", "pipe"), 0o644); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}

	cs, err := m.Import([]string{filepath.Join(outside, "kit")}, root, false, nil)
	if err == nil {
		t.Fatal("Import of a folder holding a FIFO: want an error")
	}
	if !cs.Empty() {
		t.Errorf("Changeset = %+v, want empty", cs)
	}
	if exists(filepath.Join(root, "kit")) {
		t.Error("the failed import left a partial folder behind")
	}
}

// TestImport_MoveAdoptsTheSource pins the move flavour: the entry ends
// up in the project and its staging copy is gone, reported as Added —
// to the project it is a new entry, not a rename.
func TestImport_MoveAdoptsTheSource(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	m := New(root)
	src := mkFile(t, stage, "staged.bin", "payload")

	cs, err := m.Import([]string{src}, root, true, nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	dest := filepath.Join(root, "staged.bin")
	if got := readText(t, dest); got != "payload" {
		t.Errorf("adopted content = %q, want %q", got, "payload")
	}
	if exists(src) {
		t.Error("a move-import left its source behind")
	}
	if !reflect.DeepEqual(cs.Added, []string{dest}) || len(cs.Moved) != 0 {
		t.Errorf("Changeset = %+v, want one Added and no Moved", cs)
	}
}

// TestImport_MoveAcrossDevices pins the fallback: when the rename
// cannot cross filesystems the entry is copied and the source removed,
// so staging on another volume still ends with one copy.
func TestImport_MoveAcrossDevices(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	m := New(root)
	m.rename = func(string, string) error { return syscall.EXDEV }
	mkFile(t, stage, "kit/a.txt", "A")
	src := filepath.Join(stage, "kit")

	if _, err := m.Import([]string{src}, root, true, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readText(t, filepath.Join(root, "kit", "a.txt")); got != "A" {
		t.Errorf("copied content = %q, want %q", got, "A")
	}
	if exists(src) {
		t.Error("the cross-device move left its source behind")
	}
}

// TestImport_MoveRefusesProjectEntries pins the limit on the move
// flavour: it adopts what the caller staged, and must never relocate a
// project file behind the back of the tabs that have it open.
func TestImport_MoveRefusesProjectEntries(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "A")
	sub := mkDir(t, root, "sub")

	if _, err := m.Import([]string{src}, sub, true, nil); !errors.Is(err, ErrMoveInsideRoot) {
		t.Fatalf("err = %v, want ErrMoveInsideRoot", err)
	}
	if !exists(src) || exists(filepath.Join(sub, "a.txt")) {
		t.Error("the refused move changed the disk")
	}
}

// TestImport_ProjectFileIsCopied pins that a source already inside the
// project is copied like any other — dragging a project file from the
// desktop onto another folder makes a second copy, as VS Code does.
func TestImport_ProjectFileIsCopied(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "A")
	sub := mkDir(t, root, "sub")

	if _, err := m.Import([]string{src}, sub, false, nil); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !exists(src) || readText(t, filepath.Join(sub, "a.txt")) != "A" {
		t.Error("want the original kept and a copy in sub/")
	}
}

// TestImport_ReportsProgress pins the narration hook: one call per
// entry copied, so the background runner can say how far along it is.
func TestImport_ReportsProgress(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	mkFile(t, outside, "kit/a.txt", "A")
	mkFile(t, outside, "kit/b.txt", "B")

	calls := 0
	if _, err := m.Import([]string{filepath.Join(outside, "kit")}, root, false, func() { calls++ }); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if calls != 3 {
		t.Errorf("progress called %d times, want 3 (the folder and its two files)", calls)
	}
}

// TestImport_NothingToDo pins the empty batch: no sources is not an
// error, and nothing is reported as changed.
func TestImport_NothingToDo(t *testing.T) {
	root := t.TempDir()
	cs, err := New(root).Import(nil, root, false, nil)
	if err != nil || !cs.Empty() {
		t.Errorf("Import(nil) = %+v, %v; want an empty Changeset and no error", cs, err)
	}
}

// TestHolds_TheFilesystemRoot pins the into-itself guard for the one
// ancestor a string comparison gets wrong: "/" joined with a separator
// is "//", which is a prefix of nothing. A drop of the root — or of any
// disk's top level — would otherwise copy the project into its own
// growing copy until the path grew too long.
func TestHolds_TheFilesystemRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.VolumeName(dir) + string(filepath.Separator)
	if !holds(root, dir) {
		t.Errorf("holds(%q, %q) = false, want true", root, dir)
	}
	if _, err := New(dir).Import([]string{root}, dir, false, nil); !errors.Is(err, ErrIntoItself) {
		t.Errorf("Import of the filesystem root: err = %v, want ErrIntoItself", err)
	}
}

// TestHolds_AnotherSpellingOfTheSameFolder pins the guard on a
// case-insensitive volume — the default on macOS — where a path the
// user typed or a terminal pasted can name the project's parent with
// different capitals. The folders are the same folder; the strings are
// not.
func TestHolds_AnotherSpellingOfTheSameFolder(t *testing.T) {
	base := t.TempDir()
	parent := mkDir(t, base, "Projects")
	project := mkDir(t, parent, "skiff")
	other := filepath.Join(base, "projects")
	if _, err := os.Stat(other); err != nil {
		t.Skip("this volume is case-sensitive: the other spelling is another folder")
	}
	if !holds(other, project) {
		t.Errorf("holds(%q, %q) = false, want true", other, project)
	}
}

// TestHolds_UnrelatedFolders pins the other direction: siblings, and a
// folder whose name merely starts with the source's, are not inside it.
func TestHolds_UnrelatedFolders(t *testing.T) {
	base := t.TempDir()
	a, ab := mkDir(t, base, "a"), mkDir(t, base, "ab")
	if holds(a, ab) || holds(ab, a) {
		t.Error("sibling folders reported as one inside the other")
	}
	inner := mkDir(t, a, "inner")
	if !holds(a, inner) || !holds(a, a) {
		t.Error("a folder must hold itself and its children")
	}
	if holds(inner, a) {
		t.Error("a child reported as holding its parent")
	}
}
