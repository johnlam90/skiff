// =============================================================================
// File: internal/filemanager/import.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// import.go is the one verb that takes its source from OUTSIDE the
// project. Move, Copy and Duplicate all refuse a path that is not under
// the root, because the clipboard only ever holds a tree entry; a file
// dropped onto the editor is outside by definition. Import keeps every
// other rule of the paste family — the destination is inside the root,
// nothing is overwritten, a folder never lands inside itself — and
// reads only the manager's immutable fields, so the app runs it on the
// same background job.

package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrMoveInsideRoot: Import was asked to move a source that is already
// part of the project. The move flavour adopts what the caller staged
// outside; relocating a project entry is Move's job, because Move
// reports the pair the app needs to repoint open tabs.
var ErrMoveInsideRoot = errors.New("can't move a project entry in")

// Import brings entries from anywhere on disk into dstDir, each under
// its own basename or the next free " copy" name. With move set the
// source is relocated rather than copied — for entries the caller owns
// and wants gone, a staging directory — and falls back to copy-then-
// delete across devices. progress is called once per copied entry; nil
// is allowed.
//
// A batch keeps going: a source that fails is reported by name in the
// returned error and its partial destination is removed, while the
// others still land. The Changeset therefore always lists exactly what
// is now in the project, error or not, and the caller should apply it
// either way. A bad destination fails the whole call before anything
// is touched.
func (m *Manager) Import(srcs []string, dstDir string, move bool, progress func()) (Changeset, error) {
	d := abs(dstDir)
	if !Within(m.root, d) {
		return Changeset{}, ErrOutsideRoot
	}
	if info, err := os.Stat(d); err != nil || !info.IsDir() {
		return Changeset{}, fmt.Errorf("%s is not a folder", filepath.Base(d))
	}
	var cs Changeset
	var failures []error
	for _, src := range srcs {
		dest, err := m.importOne(abs(src), d, move, progress)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", filepath.Base(src), plainPathErr(err)))
			continue
		}
		cs.Added = append(cs.Added, dest)
	}
	return cs, errors.Join(failures...)
}

// importOne lands a single source in d and returns where it went. A
// failed copy removes its own partial output (see copyTree); nothing
// is removed here, because a failure can also mean "the copy landed
// but the original could not be deleted" — and deleting the copy then
// would leave neither.
func (m *Manager) importOne(s, d string, move bool, progress func()) (string, error) {
	if s == m.root {
		return "", ErrProjectRoot
	}
	if move && Within(m.root, s) {
		return "", ErrMoveInsideRoot
	}
	isDir, err := isDirEntry(s)
	if err != nil {
		return "", err
	}
	if isDir && holds(s, d) {
		return "", ErrIntoItself
	}
	dest := uniqueDestPath(d, filepath.Base(s), isDir)
	if move {
		err = m.moveEntry(s, dest, progress)
	} else {
		err = copyTree(s, dest, progress)
	}
	if err != nil {
		return "", err
	}
	return dest, nil
}

// plainPathErr strips the "lstat /long/absolute/path:" prefix off a
// path error, keeping the cause wrapped. The caller already names the
// entry, and the message is bound for a one-line flash where the full
// path would push the reason off the screen.
func plainPathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
