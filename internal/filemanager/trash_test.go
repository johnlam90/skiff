// =============================================================================
// File: internal/filemanager/trash_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the session trash: the round-trip, the refusals, the
// in-place fallback when the rename crosses devices, and the discard
// on EmptyTrash.

package filemanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errExdev is the cross-device failure the rename seam simulates — the
// one os.Rename failure that cannot be provoked inside one t.TempDir().
var errExdev = errors.New("simulated EXDEV")

// crossDeviceRename fails every rename whose destination leaves the
// source's parent directory, exactly the shape of a same-filesystem
// rename succeeding while a hop to $TMPDIR fails.
func crossDeviceRename(oldpath, newpath string) error {
	if filepath.Dir(oldpath) != filepath.Dir(newpath) {
		return errExdev
	}
	return os.Rename(oldpath, newpath)
}

// TestTrash_RoundTrip pins the whole contract: Trash removes the item
// from its path (one Removed), the restore stack knows it, Restore
// brings the bytes back (one Added) and pops the entry.
func TestTrash_RoundTrip(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	target := mkFile(t, root, "doomed.txt", "precious")

	if m.HasTrash() || m.LastTrashed() != "" {
		t.Fatal("a fresh manager has nothing to restore")
	}
	cs, err := m.Trash(target)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if exists(target) {
		t.Fatal("trashed file should be gone from its original path")
	}
	if len(cs.Removed) != 1 || cs.Removed[0] != target || len(cs.Added)+len(cs.Moved) != 0 {
		t.Fatalf("changeset = %+v, want Removed [%s]", cs, target)
	}
	if !m.HasTrash() || m.LastTrashed() != target {
		t.Fatalf("HasTrash=%v LastTrashed=%q after a delete", m.HasTrash(), m.LastTrashed())
	}

	cs, err = m.Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "precious" {
		t.Fatalf("restored content = %q, err %v", got, err)
	}
	if len(cs.Added) != 1 || cs.Added[0] != target || len(cs.Removed)+len(cs.Moved) != 0 {
		t.Fatalf("changeset = %+v, want Added [%s]", cs, target)
	}
	if m.HasTrash() {
		t.Fatal("a successful restore pops the entry")
	}
}

// TestTrash_DirectoryAsUnit: a folder goes to the trash whole and comes
// back whole, so the user never walks leaf-to-root.
func TestTrash_DirectoryAsUnit(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	leaf := mkFile(t, root, "sub/deeper/leaf.txt", "x")
	sub := filepath.Join(root, "sub")

	if _, err := m.Trash(sub); err != nil {
		t.Fatalf("Trash dir: %v", err)
	}
	if exists(sub) || exists(leaf) {
		t.Fatal("directory (and its leaf) should be gone")
	}
	if _, err := m.Restore(); err != nil {
		t.Fatalf("Restore dir: %v", err)
	}
	if !exists(leaf) {
		t.Fatal("restored directory should bring its leaf back")
	}
}

// TestTrash_NewestFirst pins the stack order: two deletes restore in
// reverse, and the label names the newest.
func TestTrash_NewestFirst(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	first := mkFile(t, root, "first.txt", "1")
	second := mkFile(t, root, "second.txt", "2")

	if _, err := m.Trash(first); err != nil {
		t.Fatalf("Trash first: %v", err)
	}
	if _, err := m.Trash(second); err != nil {
		t.Fatalf("Trash second: %v", err)
	}
	if m.LastTrashed() != second {
		t.Fatalf("LastTrashed = %q, want %q", m.LastTrashed(), second)
	}
	cs, err := m.Restore()
	if err != nil || cs.Added[0] != second {
		t.Fatalf("first restore = %+v (%v), want %s", cs, err, second)
	}
	if m.LastTrashed() != first {
		t.Fatalf("LastTrashed after one restore = %q, want %q", m.LastTrashed(), first)
	}
}

// TestTrash_SameBasenameTwice: deleting two files that share a name
// (or the same file twice, restored in between) must not collide in
// the trash dir — the sequence number, not the basename, is the key.
func TestTrash_SameBasenameTwice(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	a := mkFile(t, root, "a/f.txt", "A")
	b := mkFile(t, root, "b/f.txt", "B")

	if _, err := m.Trash(a); err != nil {
		t.Fatalf("Trash a: %v", err)
	}
	if _, err := m.Trash(b); err != nil {
		t.Fatalf("Trash b: %v", err)
	}
	if _, err := m.Restore(); err != nil {
		t.Fatalf("Restore b: %v", err)
	}
	if _, err := m.Restore(); err != nil {
		t.Fatalf("Restore a: %v", err)
	}
	if got, _ := os.ReadFile(a); string(got) != "A" {
		t.Fatalf("a restored as %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "B" {
		t.Fatalf("b restored as %q", got)
	}
}

// TestTrash_Refusals: the project root, a path outside it, and a path
// that isn't there are all refused with the disk untouched and the
// stack unchanged.
func TestTrash_Refusals(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	keep := mkFile(t, root, "keep.txt", "x")
	outside := mkFile(t, t.TempDir(), "far.txt", "far")

	if _, err := m.Trash(root); !errors.Is(err, ErrProjectRoot) {
		t.Fatalf("root: err = %v, want ErrProjectRoot", err)
	}
	if _, err := m.Trash(outside); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside: err = %v, want ErrOutsideRoot", err)
	}
	if _, err := m.Trash(filepath.Join(root, "ghost")); err == nil {
		t.Fatal("missing: expected an error")
	}
	if !exists(keep) || !exists(outside) {
		t.Fatal("refused deletes must leave the disk alone")
	}
	if m.HasTrash() {
		t.Fatal("refused deletes must not push a restore entry")
	}
}

// TestRestore_RefusesClobber covers the collision case: if something
// new occupies the original path, Restore must not overwrite it — the
// entry stays in the trash and the error says why.
func TestRestore_RefusesClobber(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	target := mkFile(t, root, "f.txt", "old")

	if _, err := m.Trash(target); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	mkFile(t, root, "f.txt", "new occupant")

	cs, err := m.Restore()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want an already-exists refusal", err)
	}
	if !cs.Empty() {
		t.Fatalf("refused restore must report nothing, got %+v", cs)
	}
	if got, _ := os.ReadFile(target); string(got) != "new occupant" {
		t.Fatalf("restore clobbered the new file: %q", got)
	}
	if !m.HasTrash() {
		t.Fatal("a failed restore keeps the entry for later")
	}
}

// TestRestore_EmptyTrash: nothing to restore is a typed error, not a
// panic on an empty stack.
func TestRestore_EmptyTrash(t *testing.T) {
	m := New(t.TempDir())
	if _, err := m.Restore(); !errors.Is(err, ErrNoTrash) {
		t.Fatalf("err = %v, want ErrNoTrash", err)
	}
}

// TestRestore_RenameFailureKeepsEntry: when the rename back fails (the
// parent folder vanished under us) the entry stays so a later restore
// can still succeed, and the error reaches the caller.
func TestRestore_RenameFailureKeepsEntry(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	target := mkFile(t, root, "sub/f.txt", "x")
	if _, err := m.Trash(target); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatalf("remove parent: %v", err)
	}

	if _, err := m.Restore(); err == nil {
		t.Fatal("expected the rename back to fail without its parent")
	}
	if !m.HasTrash() {
		t.Fatal("the entry must survive a failed restore")
	}
	mkDir(t, root, "sub")
	if _, err := m.Restore(); err != nil {
		t.Fatalf("restore after re-creating the parent: %v", err)
	}
	if !exists(target) {
		t.Fatal("second restore should land the file")
	}
}

// TestTrash_CrossDeviceFallsBackInPlace pins the fallback: when the
// rename into the temp trash dir fails (a tmpfs /tmp, a network mount)
// the item is renamed in place to a hidden TrashPrefix sibling — always
// same-device — and Restore still brings it back from there.
func TestTrash_CrossDeviceFallsBackInPlace(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	m.rename = crossDeviceRename
	target := mkFile(t, root, "sub/doomed.txt", "x")

	cs, err := m.Trash(target)
	if err != nil {
		t.Fatalf("Trash with cross-device rename: %v", err)
	}
	if exists(target) {
		t.Fatal("file should have left its original path")
	}
	if len(cs.Removed) != 1 || cs.Removed[0] != target {
		t.Fatalf("changeset = %+v, want Removed [%s]", cs, target)
	}
	entries, err := os.ReadDir(filepath.Join(root, "sub"))
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), TrashPrefix) && strings.HasSuffix(e.Name(), "-doomed.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %s sibling in the parent, got %v", TrashPrefix, entries)
	}

	if _, err := m.Restore(); err != nil {
		t.Fatalf("Restore from in-place fallback: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "x" {
		t.Fatalf("restored content = %q", got)
	}
}

// TestTrash_NoTempDirFallsBackInPlace covers the other way the primary
// path can be unavailable: $TMPDIR itself cannot be created in. The
// item still goes to the in-place sibling and the delete succeeds.
func TestTrash_NoTempDirFallsBackInPlace(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does", "not", "exist"))
	root := t.TempDir()
	m := New(root)
	target := mkFile(t, root, "doomed.txt", "x")

	if _, err := m.Trash(target); err != nil {
		t.Fatalf("Trash without a usable TMPDIR: %v", err)
	}
	if m.trashDir != "" {
		t.Fatalf("trashDir should stay empty when MkdirTemp fails, got %q", m.trashDir)
	}
	if exists(target) {
		t.Fatal("file should have left its original path")
	}
	if !strings.HasPrefix(filepath.Base(m.trashed[0].stored), TrashPrefix) {
		t.Fatalf("stored = %q, want an in-place %s sibling", m.trashed[0].stored, TrashPrefix)
	}
}

// TestTrash_EveryRenameFailsIsAnError: when neither the temp dir nor
// the in-place sibling can be reached the delete fails cleanly and the
// file stays where it was.
func TestTrash_EveryRenameFailsIsAnError(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	m.rename = func(string, string) error { return errExdev }
	target := mkFile(t, root, "stuck.txt", "x")

	if _, err := m.Trash(target); !errors.Is(err, errExdev) {
		t.Fatalf("err = %v, want the rename failure", err)
	}
	if !exists(target) || m.HasTrash() {
		t.Fatal("a failed delete must leave the file and push nothing")
	}
}

// TestEmptyTrash_DiscardsStored pins the "session trash" half of the
// name: EmptyTrash removes every stored copy — the temp dir and the
// in-place siblings alike — and clears the stack.
func TestEmptyTrash_DiscardsStored(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	primary := mkFile(t, root, "a.txt", "a")
	if _, err := m.Trash(primary); err != nil {
		t.Fatalf("Trash a: %v", err)
	}
	m.rename = crossDeviceRename
	fallback := mkFile(t, root, "sub/b.txt", "b")
	if _, err := m.Trash(fallback); err != nil {
		t.Fatalf("Trash b: %v", err)
	}
	stored := []string{m.trashed[0].stored, m.trashed[1].stored}
	trashDir := m.trashDir

	m.EmptyTrash()

	for _, s := range stored {
		if exists(s) {
			t.Fatalf("EmptyTrash should remove %s", s)
		}
	}
	if exists(trashDir) {
		t.Fatal("EmptyTrash should remove the temp trash dir")
	}
	if m.HasTrash() || m.trashDir != "" {
		t.Fatal("EmptyTrash should clear the stack and forget the dir")
	}
	// A later delete creates a fresh trash dir rather than writing into
	// the one just removed.
	again := mkFile(t, root, "c.txt", "c")
	m.rename = os.Rename
	if _, err := m.Trash(again); err != nil {
		t.Fatalf("Trash after EmptyTrash: %v", err)
	}
	if m.trashDir == "" || !exists(m.trashed[0].stored) {
		t.Fatal("a delete after EmptyTrash should land in a new trash dir")
	}
}
