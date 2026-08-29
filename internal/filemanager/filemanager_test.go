// =============================================================================
// File: internal/filemanager/filemanager_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the manager's root handling, the containment guards, Create
// and Rename. Everything runs against a t.TempDir() with no screen —
// the package is a pure disk module and its tests stay that way.

package filemanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkFile writes content to dir/name (creating parents) and returns the
// absolute path — the one fixture helper every test file here shares.
func mkFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// mkDir creates dir/name (and parents) and returns the absolute path.
func mkDir(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return path
}

// exists reports whether path is present on disk (without following
// symlinks, so a dangling link counts as present).
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestNew_RootIsAbsolute pins why New resolves the root once: a project
// opened as "." must compare equal to its absolute spelling in every
// guard — the spelling mismatch is how the root once passed as a
// deletable subfolder.
func TestNew_RootIsAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	m := New(".")
	if m.Root() != root {
		t.Fatalf("Root() = %q, want %q", m.Root(), root)
	}
	if !m.IsRoot(".") || !m.IsRoot(root) {
		t.Fatal("IsRoot must recognise the root in any spelling")
	}
	if m.IsRoot(filepath.Join(root, "sub")) {
		t.Fatal("a subfolder is not the root")
	}
}

// TestWithin pins the containment contract: a descendant is inside, the
// root itself counts as inside (a session entry naming the project
// root is legitimate, not an escape), a "../" climb is refused, and an
// unrelated absolute path elsewhere is refused too.
func TestWithin(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name      string
		candidate string
		want      bool
	}{
		{"descendant", filepath.Join(root, "sub", "leaf.go"), true},
		{"root itself", root, true},
		{"parent escape", filepath.Join(root, "..", "evil.txt"), false},
		{"unrelated absolute path", t.TempDir(), false},
	}
	for _, c := range cases {
		if got := Within(root, c.candidate); got != c.want {
			t.Errorf("%s: Within(%q, %q) = %v, want %v", c.name, root, c.candidate, got, c.want)
		}
	}
	m := New(root)
	if !m.Contains(root) || !m.Contains(filepath.Join(root, "a")) {
		t.Fatal("Contains must accept the root and its descendants")
	}
	if m.Contains(filepath.Join(root, "..", "x")) {
		t.Fatal("Contains must refuse an escape")
	}
}

// TestCreate_NewFile is the happy path: an empty file appears and the
// Changeset names it as Added.
func TestCreate_NewFile(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	target := filepath.Join(root, "hello.txt")

	cs, err := m.Create(target)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat after create: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected an empty file, got %d bytes", info.Size())
	}
	if len(cs.Added) != 1 || cs.Added[0] != target || len(cs.Removed) != 0 || len(cs.Moved) != 0 {
		t.Fatalf("changeset = %+v, want Added [%s]", cs, target)
	}
}

// TestCreate_RefusesExisting keeps the user's content safe from a typo:
// creating over an existing file is an error and the bytes survive.
func TestCreate_RefusesExisting(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	target := mkFile(t, root, "existing.txt", "keep me")

	cs, err := m.Create(target)
	if err == nil {
		t.Fatal("expected an error creating over an existing file")
	}
	if !cs.Empty() {
		t.Fatalf("a refused create must report an empty changeset, got %+v", cs)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep me" {
		t.Fatalf("file contents were clobbered: %q", got)
	}
}

// TestCreate_MissingParentIsActionable pins the ENOENT translation:
// the manager never mkdirs, and the error tells the user which folder
// to create instead of quoting the raw open(2) failure.
func TestCreate_MissingParentIsActionable(t *testing.T) {
	root := t.TempDir()
	m := New(root)

	_, err := m.Create(filepath.Join(root, "nope", "f.txt"))
	if err == nil {
		t.Fatal("expected an error for a missing parent")
	}
	if !strings.Contains(err.Error(), "create it first") || !strings.Contains(err.Error(), filepath.Join(root, "nope")) {
		t.Fatalf("error should name the missing folder, got %v", err)
	}
}

// TestCreate_RefusesEscapingRoot: a path that climbs out of the project
// (or names the root itself) is refused before touching the disk.
func TestCreate_RefusesEscapingRoot(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	outside := filepath.Join(root, "..", "evil.txt")

	if _, err := m.Create(outside); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("escape: err = %v, want ErrOutsideRoot", err)
	}
	if exists(filepath.Clean(outside)) {
		t.Fatal("refused create must not touch the disk")
	}
	if _, err := m.Create(root); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("root: err = %v, want ErrOutsideRoot", err)
	}
}

// TestRename_File is the happy path: the source is gone, the sibling
// carries the bytes, and the Changeset is one Move.
func TestRename_File(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "before.txt", "payload")

	cs, err := m.Rename(src, "after.txt")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	dst := filepath.Join(root, "after.txt")
	if exists(src) {
		t.Fatal("source still exists after rename")
	}
	if got, _ := os.ReadFile(dst); string(got) != "payload" {
		t.Fatalf("payload mismatch: %q", got)
	}
	want := Changeset{Moved: []Move{{Old: src, New: dst}}}
	if len(cs.Moved) != 1 || cs.Moved[0] != want.Moved[0] || len(cs.Added)+len(cs.Removed) != 0 {
		t.Fatalf("changeset = %+v, want %+v", cs, want)
	}
}

// TestRename_Directory pins that one Rename serves files AND folders:
// the folder moves as a unit and the Changeset is still ONE Move (the
// dir pair) — the app widens it to the tabs underneath.
func TestRename_Directory(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	leaf := mkFile(t, root, "old/deep/leaf.go", "package x\n")
	old := filepath.Join(root, "old")

	cs, err := m.Rename(old, "renamed")
	if err != nil {
		t.Fatalf("Rename dir: %v", err)
	}
	if !exists(filepath.Join(root, "renamed", "deep", "leaf.go")) || exists(leaf) {
		t.Fatal("directory contents did not move with the rename")
	}
	if len(cs.Moved) != 1 || cs.Moved[0] != (Move{Old: old, New: filepath.Join(root, "renamed")}) {
		t.Fatalf("changeset = %+v, want one Move of the dir pair", cs)
	}
}

// TestRename_SameNameIsNoop: the prompt pre-fills the old name, so an
// unedited submit must succeed with nothing to report.
func TestRename_SameNameIsNoop(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "same.txt", "same")

	cs, err := m.Rename(src, "same.txt")
	if err != nil {
		t.Fatalf("same-name rename: %v", err)
	}
	if !cs.Empty() {
		t.Fatalf("same-name rename should report nothing, got %+v", cs)
	}
	if got, _ := os.ReadFile(src); string(got) != "same" {
		t.Fatalf("contents changed: %q", got)
	}
}

// TestRename_Refusals walks every guard: clobbering a sibling, a
// separator in the name, an empty name, the project root, and a path
// outside it. Each leaves the disk exactly as it was.
func TestRename_Refusals(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	src := mkFile(t, root, "src.txt", "src")
	dst := mkFile(t, root, "dst.txt", "dst")
	outside := mkFile(t, t.TempDir(), "far.txt", "far")

	cases := []struct {
		name    string
		path    string
		newName string
		want    error // nil: assert on the message instead
		message string
	}{
		{"clobber", src, "dst.txt", nil, "already exists"},
		{"separator", src, "nested/inside", ErrSeparator, ""},
		{"backslash", src, `a\b`, ErrSeparator, ""},
		{"empty", src, "", ErrEmptyName, ""},
		{"root", root, "other", ErrProjectRoot, ""},
		{"outside", outside, "x.txt", ErrOutsideRoot, ""},
	}
	for _, c := range cases {
		cs, err := m.Rename(c.path, c.newName)
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if c.message != "" && !strings.Contains(err.Error(), c.message) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.message)
		}
		if !cs.Empty() {
			t.Errorf("%s: refused rename must report an empty changeset, got %+v", c.name, cs)
		}
	}
	if got, _ := os.ReadFile(src); string(got) != "src" {
		t.Fatalf("src corrupted: %q", got)
	}
	if got, _ := os.ReadFile(dst); string(got) != "dst" {
		t.Fatalf("dst corrupted: %q", got)
	}
	if !exists(outside) || !exists(root) {
		t.Fatal("refused renames must leave every path in place")
	}
}

// TestRename_MissingSource: renaming what isn't there surfaces the OS
// error rather than inventing a destination.
func TestRename_MissingSource(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	if _, err := m.Rename(filepath.Join(root, "ghost"), "spirit"); err == nil {
		t.Fatal("expected an error renaming a missing path")
	}
	if exists(filepath.Join(root, "spirit")) {
		t.Fatal("a failed rename must not create the destination")
	}
}

// TestChangeset_Empty pins the predicate the app's error tails rely on:
// a zero value is empty, any named path is not.
func TestChangeset_Empty(t *testing.T) {
	if !(Changeset{}).Empty() {
		t.Fatal("zero changeset should be empty")
	}
	for _, cs := range []Changeset{
		{Added: []string{"/a"}},
		{Removed: []string{"/a"}},
		{Moved: []Move{{Old: "/a", New: "/b"}}},
	} {
		if cs.Empty() {
			t.Fatalf("%+v should not be empty", cs)
		}
	}
}
