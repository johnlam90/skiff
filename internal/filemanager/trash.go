// =============================================================================
// File: internal/filemanager/trash.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// trash.go is the session trash: Trash relocates instead of destroying,
// Restore pops the newest entry back, EmptyTrash discards everything
// when the session ends. The undo window is deliberately the session
// lifetime, not forever, so deleted work doesn't accumulate invisibly
// on disk.

package filemanager

import (
	"fmt"
	"os"
	"path/filepath"
)

// Trash moves the file or directory at path into the session trash and
// pushes a restore entry. The primary destination is a per-session temp
// dir; when that rename crosses filesystems (os.Rename fails on tmpfs
// /tmp, network mounts, …) the next stop is a folder inside the
// project's own .git directory — same device as the working tree, and
// the one place in a repository git itself never reports, stages or
// stashes. Only when there is no .git directory either is the item
// renamed in place to a hidden TrashPrefix sibling, which is always
// same-device. The tree, the finder and the git panel all filter that
// prefix so a fallback entry never surfaces in the UI.
func (m *Manager) Trash(path string) (Changeset, error) {
	p, err := m.guard(path)
	if err != nil {
		return Changeset{}, err
	}
	if _, err := os.Lstat(p); err != nil {
		return Changeset{}, err
	}
	base := filepath.Base(p)
	n := m.seq
	m.seq++
	if m.trashDir == "" {
		if dir, err := os.MkdirTemp("", "skiff-trash-"); err == nil {
			m.trashDir = dir
		}
	}
	if m.trashDir != "" {
		stored := filepath.Join(m.trashDir, fmt.Sprintf("%d-%s", n, base))
		if err := m.rename(p, stored); err == nil {
			m.trashed = append(m.trashed, trashEntry{orig: p, stored: stored})
			return Changeset{Removed: []string{p}}, nil
		}
	}
	if dir := m.gitTrashDir(); dir != "" {
		stored := filepath.Join(dir, fmt.Sprintf("%d-%s", n, base))
		if err := m.rename(p, stored); err == nil {
			m.trashed = append(m.trashed, trashEntry{orig: p, stored: stored})
			return Changeset{Removed: []string{p}}, nil
		}
	}
	stored := filepath.Join(filepath.Dir(p), fmt.Sprintf("%s%d-%s", TrashPrefix, n, base))
	if err := m.rename(p, stored); err != nil {
		return Changeset{}, err
	}
	m.trashed = append(m.trashed, trashEntry{orig: p, stored: stored})
	return Changeset{Removed: []string{p}}, nil
}

// gitTrashDir returns (creating it on demand) the trash folder inside
// the project's .git directory, or "" when the root has none — a plain
// folder, or a linked worktree whose .git is a file. An in-place
// TrashPrefix sibling is an untracked file as far as git is concerned:
// `git status` listed it, a commit could stage it, and `stash -u` moved
// it away so Undo delete had nothing left to restore.
func (m *Manager) gitTrashDir() string {
	gitDir := filepath.Join(m.root, ".git")
	if info, err := os.Lstat(gitDir); err != nil || !info.IsDir() {
		return ""
	}
	if m.gitTrash != "" {
		return m.gitTrash
	}
	parent := filepath.Join(gitDir, gitTrashName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return ""
	}
	// A folder of this session's own: entry names are numbered per
	// process, so two skiffs on one repo sharing a flat folder would
	// both write "0-index.ts" — and one's Undo delete would restore the
	// other's file.
	dir, err := os.MkdirTemp(parent, "s-")
	if err != nil {
		return ""
	}
	m.gitTrash = dir
	return dir
}

// gitTrashName is the folder under .git that holds trashed items when
// the temp dir is on another filesystem.
const gitTrashName = "skiff-trash"

// Restore puts the most recently trashed item back where it lived. It
// refuses to clobber: if something new occupies the original path the
// entry stays in the trash and the error says why, so a failed restore
// never destroys newer work. A parent folder that has since been renamed
// or deleted is recreated: the restore stack is strictly newest-first,
// so an entry that can never go back would also strand every older
// delete behind it until exit emptied the trash.
func (m *Manager) Restore() (Changeset, error) {
	if len(m.trashed) == 0 {
		return Changeset{}, ErrNoTrash
	}
	e := m.trashed[len(m.trashed)-1]
	if _, err := os.Lstat(e.orig); err == nil {
		return Changeset{}, fmt.Errorf("%s already exists", filepath.Base(e.orig))
	}
	if err := os.MkdirAll(filepath.Dir(e.orig), 0o755); err != nil {
		return Changeset{}, err
	}
	if err := m.rename(e.stored, e.orig); err != nil {
		return Changeset{}, err
	}
	m.trashed = m.trashed[:len(m.trashed)-1]
	return Changeset{Added: []string{e.orig}}, nil
}

// HasTrash reports whether Restore has anything to put back — the
// menu predicate for the Undo delete row.
func (m *Manager) HasTrash() bool { return len(m.trashed) > 0 }

// LastTrashed returns the original path of the item Restore would put
// back next, or "" when the trash is empty — what the "Undo delete (X)"
// label names.
func (m *Manager) LastTrashed() string {
	if len(m.trashed) == 0 {
		return ""
	}
	return m.trashed[len(m.trashed)-1].orig
}

// EmptyTrash permanently discards everything the session trash holds,
// the temp dir included. Best-effort: a stored entry that will not
// delete is not worth failing an exit over.
func (m *Manager) EmptyTrash() {
	for _, e := range m.trashed {
		_ = os.RemoveAll(e.stored)
	}
	if m.trashDir != "" {
		_ = os.RemoveAll(m.trashDir)
		m.trashDir = ""
	}
	if m.gitTrash != "" {
		_ = os.RemoveAll(m.gitTrash)
		m.gitTrash = ""
	}
	// Remove, not RemoveAll: another skiff on the same repo may still be
	// holding its own session folder under the shared parent.
	_ = os.Remove(filepath.Join(m.root, ".git", gitTrashName))
	m.trashed = nil
}
