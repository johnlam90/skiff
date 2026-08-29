// =============================================================================
// File: internal/filemanager/copy_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for Move, Copy and Duplicate: the never-overwrite ladder, the
// into-itself guards (plain and through a symlink), the cross-device
// fallback, the progress narration, and the off-main-loop safety rule.

package filemanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestUniqueDestPath_Ladder pins the collision ladder: a free name is
// used as-is, then " copy", then " copy 2" — the suffix lands before
// the extension for files and at the end for directories.
func TestUniqueDestPath_Ladder(t *testing.T) {
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
	mkDir(t, dir, "v1.2")
	if got := uniqueDestPath(dir, "v1.2", true); got != filepath.Join(dir, "v1.2 copy") {
		t.Fatalf("dir collision: got %q", got)
	}
}

// TestMove_File is the happy path: the file lands in dstDir under its
// own name, the source is gone, and the Changeset is one Move.
func TestMove_File(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")
	sub := mkDir(t, root, "sub")

	cs, err := m.Move(src, sub, nil)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	dest := filepath.Join(sub, "a.txt")
	if got, _ := os.ReadFile(dest); string(got) != "data" {
		t.Fatalf("moved content = %q", got)
	}
	if exists(src) {
		t.Fatal("source should be gone after a move")
	}
	if len(cs.Moved) != 1 || cs.Moved[0] != (Move{Old: src, New: dest}) || len(cs.Added)+len(cs.Removed) != 0 {
		t.Fatalf("changeset = %+v, want one Move", cs)
	}
}

// TestMove_DirectoryIsOneMove: a folder moves as a unit and reports the
// dir pair, not one Move per file — the app widens it to open tabs.
func TestMove_DirectoryIsOneMove(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	mkFile(t, root, "pkg/file.go", "package pkg")
	dest := mkDir(t, root, "moved")

	cs, err := m.Move(filepath.Join(root, "pkg"), dest, nil)
	if err != nil {
		t.Fatalf("Move dir: %v", err)
	}
	if !exists(filepath.Join(dest, "pkg", "file.go")) {
		t.Fatal("moved folder should carry its file")
	}
	if len(cs.Moved) != 1 || cs.Moved[0] != (Move{Old: filepath.Join(root, "pkg"), New: filepath.Join(dest, "pkg")}) {
		t.Fatalf("changeset = %+v, want one Move of the dir pair", cs)
	}
}

// TestMove_TakenNameWalksLadder: nothing is ever overwritten — a move
// onto a taken name lands on the next free " copy" name.
func TestMove_TakenNameWalksLadder(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "new")
	sub := mkDir(t, root, "sub")
	taken := mkFile(t, sub, "a.txt", "old")

	cs, err := m.Move(src, sub, nil)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got, _ := os.ReadFile(taken); string(got) != "old" {
		t.Fatalf("existing file was overwritten: %q", got)
	}
	if want := filepath.Join(sub, "a copy.txt"); cs.Moved[0].New != want || !exists(want) {
		t.Fatalf("moved to %q, want %q", cs.Moved[0].New, want)
	}
}

// TestMove_AlreadyHere: a cut pasted back into its own folder is a
// change of mind, not a request for "x copy" — refused with a friendly
// message and nothing moved.
func TestMove_AlreadyHere(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "sub/a.txt", "data")

	cs, err := m.Move(src, filepath.Join(root, "sub"), nil)
	if err == nil || !strings.Contains(err.Error(), "already here") {
		t.Fatalf("err = %v, want an already-here refusal", err)
	}
	if !cs.Empty() || !exists(src) || exists(filepath.Join(root, "sub", "a copy.txt")) {
		t.Fatal("a refused move must change nothing")
	}
}

// TestMove_IntoItselfRefused: pasting a cut folder into itself (or a
// descendant) would eat the folder — it must refuse and leave the disk
// untouched.
func TestMove_IntoItselfRefused(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	inner := mkDir(t, root, "outer/inner")
	outer := filepath.Join(root, "outer")

	for _, dst := range []string{outer, inner} {
		cs, err := m.Move(outer, dst, nil)
		if !errors.Is(err, ErrIntoItself) {
			t.Fatalf("into %s: err = %v, want ErrIntoItself", dst, err)
		}
		if !cs.Empty() {
			t.Fatalf("into %s: refused move must report nothing, got %+v", dst, cs)
		}
	}
	if !exists(inner) {
		t.Fatal("refused move must not disturb the tree")
	}
}

// TestMove_SymlinkedDescendantRefused: the unresolved-path comparison
// has a hole — a destination that LOOKS unrelated to src by string
// prefix but RESOLVES (via a symlink) to somewhere inside src must be
// refused too, or copyTree would walk into its own growing output.
func TestMove_SymlinkedDescendantRefused(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	outer := mkDir(t, root, "outer")
	mkDir(t, outer, "inner")
	elsewhere := mkDir(t, root, "elsewhere")
	link := filepath.Join(elsewhere, "link")
	if err := os.Symlink(outer, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := m.Move(outer, link, nil); !errors.Is(err, ErrIntoItself) {
		t.Fatalf("err = %v, want ErrIntoItself through the symlink", err)
	}
	if _, err := m.Copy(outer, link, nil); !errors.Is(err, ErrIntoItself) {
		t.Fatalf("copy: err = %v, want ErrIntoItself through the symlink", err)
	}
	if !exists(filepath.Join(outer, "inner")) {
		t.Fatal("refused paste must not disturb the tree")
	}
}

// TestMove_Refusals walks the remaining guards shared by Move and Copy:
// the project root as source, a source or destination outside the
// root, a missing source, and a destination that isn't a folder.
func TestMove_Refusals(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")
	sub := mkDir(t, root, "sub")
	outside := t.TempDir()
	far := mkFile(t, outside, "far.txt", "far")

	cases := []struct {
		name    string
		src     string
		dst     string
		want    error
		message string
	}{
		{"root as source", root, sub, ErrProjectRoot, ""},
		{"source outside", far, sub, ErrOutsideRoot, ""},
		{"destination outside", src, outside, ErrOutsideRoot, ""},
		{"missing source", filepath.Join(root, "ghost"), sub, nil, "no such file"},
		{"destination missing", src, filepath.Join(root, "nope"), nil, "not a folder"},
		{"destination is a file", src, mkFile(t, root, "plain.txt", "x"), nil, "not a folder"},
	}
	for _, c := range cases {
		for verb, op := range map[string]func(string, string, func()) (Changeset, error){"Move": m.Move, "Copy": m.Copy} {
			cs, err := op(c.src, c.dst, nil)
			if err == nil {
				t.Errorf("%s %s: expected an error", verb, c.name)
				continue
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("%s %s: err = %v, want %v", verb, c.name, err, c.want)
			}
			if c.message != "" && !strings.Contains(err.Error(), c.message) {
				t.Errorf("%s %s: err = %v, want it to mention %q", verb, c.name, err, c.message)
			}
			if !cs.Empty() {
				t.Errorf("%s %s: refused op must report nothing, got %+v", verb, c.name, cs)
			}
		}
	}
	if !exists(src) || !exists(far) {
		t.Fatal("refused ops must leave every source in place")
	}
}

// TestMove_CrossDeviceCopiesThenDeletes pins the fallback behind a
// paste-move across filesystems: when rename fails the tree is copied
// and the source removed, and progress narrates every entry.
func TestMove_CrossDeviceCopiesThenDeletes(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	m.rename = func(string, string) error { return errExdev }
	mkFile(t, root, "pkg/a.go", "a")
	mkFile(t, root, "pkg/deep/b.go", "b")
	dest := mkDir(t, root, "moved")
	ticks := 0

	cs, err := m.Move(filepath.Join(root, "pkg"), dest, func() { ticks++ })
	if err != nil {
		t.Fatalf("Move across devices: %v", err)
	}
	if exists(filepath.Join(root, "pkg")) {
		t.Fatal("source should be removed after the copy lands")
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "pkg", "deep", "b.go")); string(got) != "b" {
		t.Fatalf("copied content = %q", got)
	}
	// pkg, a.go, deep, b.go — one tick per entry.
	if ticks != 4 {
		t.Fatalf("progress ticks = %d, want 4", ticks)
	}
	if len(cs.Moved) != 1 || cs.Moved[0].New != filepath.Join(dest, "pkg") {
		t.Fatalf("changeset = %+v", cs)
	}
}

// TestMove_CrossDeviceCopyFailureKeepsSource: when the fallback copy
// itself dies the source is kept — a failed move must never be a
// delete.
func TestMove_CrossDeviceCopyFailureKeepsSource(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	m.rename = func(string, string) error { return errExdev }
	src := mkFile(t, root, "a.txt", "data")
	sub := mkDir(t, root, "sub")
	// Plant the destination name after the ladder check would have run
	// by aiming copyTree at a name that already exists: the O_EXCL
	// create fails, so the copy fails.
	m.rename = func(_, newpath string) error {
		mkFile(t, filepath.Dir(newpath), filepath.Base(newpath), "planted")
		return errExdev
	}

	_, err := m.Move(src, sub, nil)
	if err == nil {
		t.Fatal("expected the copy to fail on the planted destination")
	}
	if got, _ := os.ReadFile(src); string(got) != "data" {
		t.Fatalf("failed move must keep the source, got %q", got)
	}
}

// TestCopy_File: a copied file lands beside its namesake as Added, and
// the source stays so it can be pasted again elsewhere.
func TestCopy_File(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")
	s1 := mkDir(t, root, "s1")
	s2 := mkDir(t, root, "s2")

	for _, dir := range []string{s1, s2} {
		cs, err := m.Copy(src, dir, nil)
		if err != nil {
			t.Fatalf("Copy into %s: %v", dir, err)
		}
		want := filepath.Join(dir, "a.txt")
		if got, _ := os.ReadFile(want); string(got) != "data" {
			t.Fatalf("copied content = %q", got)
		}
		if len(cs.Added) != 1 || cs.Added[0] != want || len(cs.Moved)+len(cs.Removed) != 0 {
			t.Fatalf("changeset = %+v, want Added [%s]", cs, want)
		}
	}
	if !exists(src) {
		t.Fatal("copy must leave the source alone")
	}
}

// TestCopy_DirectoryWithSymlink pins copyTree's three cases at once: a
// directory re-creates its entries, a file copies bytes and mode, and
// a symlink is re-linked to the same target rather than dereferenced.
func TestCopy_DirectoryWithSymlink(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	mkFile(t, root, "pkg/a.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(root, "pkg", "a.sh"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.Symlink("a.sh", filepath.Join(root, "pkg", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dest := mkDir(t, root, "dest")

	cs, err := m.Copy(filepath.Join(root, "pkg"), dest, nil)
	if err != nil {
		t.Fatalf("Copy dir: %v", err)
	}
	if cs.Added[0] != filepath.Join(dest, "pkg") {
		t.Fatalf("changeset = %+v", cs)
	}
	info, err := os.Stat(filepath.Join(dest, "pkg", "a.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("copied file mode = %v (err %v), want 0755", info, err)
	}
	target, err := os.Readlink(filepath.Join(dest, "pkg", "link"))
	if err != nil || target != "a.sh" {
		t.Fatalf("symlink target = %q (err %v), want a.sh", target, err)
	}
}

// TestCopy_IntoOwnFolderWalksLadder: unlike Move, copying beside the
// source is legitimate — it is how "a copy.txt" comes to be.
func TestCopy_IntoOwnFolderWalksLadder(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")

	cs, err := m.Copy(src, root, nil)
	if err != nil {
		t.Fatalf("Copy beside: %v", err)
	}
	if want := filepath.Join(root, "a copy.txt"); cs.Added[0] != want || !exists(want) {
		t.Fatalf("copied to %q, want %q", cs.Added[0], want)
	}
}

// TestDuplicate_FileAndDir: Duplicate is copy + paste-beside in one
// gesture, using the same ladder for files and folders.
func TestDuplicate_FileAndDir(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")
	mkFile(t, root, "pkg/x.go", "x")

	cs, err := m.Duplicate(src, nil)
	if err != nil {
		t.Fatalf("Duplicate file: %v", err)
	}
	if want := filepath.Join(root, "a copy.txt"); cs.Added[0] != want || !exists(want) {
		t.Fatalf("duplicate = %+v, want Added [%s]", cs, want)
	}
	cs, err = m.Duplicate(filepath.Join(root, "pkg"), nil)
	if err != nil {
		t.Fatalf("Duplicate dir: %v", err)
	}
	if want := filepath.Join(root, "pkg copy"); cs.Added[0] != want || !exists(filepath.Join(want, "x.go")) {
		t.Fatalf("duplicate dir = %+v, want Added [%s] with its file", cs, want)
	}
	if _, err := m.Duplicate(src, nil); err != nil {
		t.Fatalf("second Duplicate: %v", err)
	}
	if !exists(filepath.Join(root, "a copy 2.txt")) {
		t.Fatal("a second duplicate walks to ' copy 2'")
	}
}

// TestDuplicate_Refusals: the root, a path outside it, and a missing
// path are refused.
func TestDuplicate_Refusals(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	if _, err := m.Duplicate(root, nil); !errors.Is(err, ErrProjectRoot) {
		t.Fatalf("root: err = %v, want ErrProjectRoot", err)
	}
	if _, err := m.Duplicate(mkFile(t, t.TempDir(), "far.txt", "x"), nil); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside: err = %v, want ErrOutsideRoot", err)
	}
	if _, err := m.Duplicate(filepath.Join(root, "ghost"), nil); err == nil {
		t.Fatal("missing: expected an error")
	}
}

// TestIsDirEntry_SymlinkToDir pins the naming rule for links: a link to
// a folder pastes like a folder (whole-name ladder), a dangling link is
// still an entry — it can be moved and re-linked — but not a folder.
func TestIsDirEntry_SymlinkToDir(t *testing.T) {
	root := t.TempDir()
	dir := mkDir(t, root, "real")
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if isDir, err := isDirEntry(link); err != nil || !isDir {
		t.Fatalf("link to dir: isDir=%v err=%v", isDir, err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if isDir, err := isDirEntry(dangling); err != nil || isDir {
		t.Fatalf("dangling link: isDir=%v err=%v, want false, nil", isDir, err)
	}
	if _, err := isDirEntry(filepath.Join(root, "ghost")); err == nil {
		t.Fatal("a missing path is an error, not a file")
	}
}

// TestCopy_OffMainLoopIsRaceFree pins the thread-safety rule the app
// depends on: a Copy on a goroutine and the trash reads the menu makes
// on the main loop touch disjoint fields, so they run together without
// a lock. The race detector is the assertion.
func TestCopy_OffMainLoopIsRaceFree(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "a.txt", "data")
	doomed := mkFile(t, root, "b.txt", "b")
	dest := mkDir(t, root, "dest")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := m.Copy(src, dest, nil); err != nil {
			t.Errorf("Copy: %v", err)
		}
	}()
	if _, err := m.Trash(doomed); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	_ = m.HasTrash()
	_ = m.LastTrashed()
	wg.Wait()
	if !exists(filepath.Join(dest, "a.txt")) {
		t.Fatal("copy should have landed")
	}
}
