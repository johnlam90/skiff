// =============================================================================
// File: internal/filemanager/copy.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// copy.go is the paste family: Move, Copy and Duplicate. Nothing is
// ever overwritten — a taken name walks the " copy" / " copy 2" ladder,
// which is also how duplication works. These three read only the
// manager's immutable fields, so the app may run them off the main
// loop; see the Manager doc for the rule.

package filemanager

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Move relocates src into dstDir under its own basename (or the next
// free " copy" name). A rename when the filesystem allows, copy-then-
// delete across devices. progress is called once per copied entry so a
// background runner can narrate; nil is allowed. Moving an entry into
// the folder it already lives in is refused with a friendly error
// rather than renamed to "x copy" — a cut pasted back where it came
// from is a change of mind, not a request.
func (m *Manager) Move(src, dstDir string, progress func()) (Changeset, error) {
	s, d, isDir, err := m.pasteGuard(src, dstDir)
	if err != nil {
		return Changeset{}, err
	}
	if filepath.Dir(s) == d {
		return Changeset{}, fmt.Errorf("%s is already here", filepath.Base(s))
	}
	dest := uniqueDestPath(d, filepath.Base(s), isDir)
	if err := m.moveEntry(s, dest, progress); err != nil {
		return Changeset{}, err
	}
	return Changeset{Moved: []Move{{Old: s, New: dest}}}, nil
}

// Copy drops a copy of src into dstDir under its own basename or the
// next free " copy" name. The source is left alone, so one clipboard
// entry can be pasted into several folders.
func (m *Manager) Copy(src, dstDir string, progress func()) (Changeset, error) {
	s, d, isDir, err := m.pasteGuard(src, dstDir)
	if err != nil {
		return Changeset{}, err
	}
	dest := uniqueDestPath(d, filepath.Base(s), isDir)
	if err := copyTree(s, dest, progress); err != nil {
		return Changeset{}, err
	}
	return Changeset{Added: []string{dest}}, nil
}

// Duplicate copies src beside itself under the next free " copy" name
// — the one-gesture version of copy + paste-beside.
func (m *Manager) Duplicate(src string, progress func()) (Changeset, error) {
	s, err := m.guard(src)
	if err != nil {
		return Changeset{}, err
	}
	isDir, err := isDirEntry(s)
	if err != nil {
		return Changeset{}, err
	}
	dest := uniqueDestPath(filepath.Dir(s), filepath.Base(s), isDir)
	if err := copyTree(s, dest, progress); err != nil {
		return Changeset{}, err
	}
	return Changeset{Added: []string{dest}}, nil
}

// pasteGuard is the preamble Move and Copy share: both paths absolute
// and inside the root, the source present, the destination a real
// folder, and a folder never pasted into itself or a descendant.
//
// The into-itself check compares resolved paths, not the raw strings: a
// destination that looks unrelated to src by prefix can still symlink
// to somewhere inside it, and copyTree would then walk into its own
// growing output. EvalSymlinks falls back to the original on error (a
// dangling link, permission trouble) — best-effort, same as every other
// symlink resolution in the editor.
func (m *Manager) pasteGuard(src, dstDir string) (s, d string, isDir bool, err error) {
	s, err = m.guard(src)
	if err != nil {
		return "", "", false, err
	}
	d = abs(dstDir)
	if !Within(m.root, d) {
		return "", "", false, ErrOutsideRoot
	}
	isDir, err = isDirEntry(s)
	if err != nil {
		return "", "", false, err
	}
	if info, err := os.Stat(d); err != nil || !info.IsDir() {
		return "", "", false, fmt.Errorf("%s is not a folder", filepath.Base(d))
	}
	if isDir {
		rSrc, rDir := s, d
		if resolved, err := filepath.EvalSymlinks(s); err == nil {
			rSrc = resolved
		}
		if resolved, err := filepath.EvalSymlinks(d); err == nil {
			rDir = resolved
		}
		sep := string(filepath.Separator)
		if rDir == rSrc || strings.HasPrefix(rDir+sep, rSrc+sep) {
			return "", "", false, ErrIntoItself
		}
	}
	return s, d, isDir, nil
}

// isDirEntry reports whether path is a directory for naming and guard
// purposes, following a symlink so a link to a folder pastes like a
// folder. The Lstat first is the existence check: a dangling link is
// still an entry that can be moved or copied (copyTree re-links it).
func isDirEntry(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return info.IsDir(), nil
	}
	if target, err := os.Stat(path); err == nil {
		return target.IsDir(), nil
	}
	return false, nil
}

// moveEntry relocates src to dest: rename when the filesystem allows,
// copy-then-delete across devices (the one case os.Rename can't do).
func (m *Manager) moveEntry(src, dest string, progress func()) error {
	if err := m.rename(src, dest); err == nil {
		return nil
	}
	if err := copyTree(src, dest, progress); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// uniqueDestPath returns dir/base if free, else the first free name on
// the " copy" / " copy 2" ladder. For files the suffix lands before the
// extension ("app copy.ts"); a directory's whole name is the stem.
func uniqueDestPath(dir, base string, isDir bool) string {
	dest := filepath.Join(dir, base)
	if _, err := os.Lstat(dest); err != nil {
		return dest
	}
	ext := ""
	stem := base
	if !isDir {
		ext = filepath.Ext(base)
		stem = strings.TrimSuffix(base, ext)
	}
	for n := 1; ; n++ {
		name := stem + " copy" + ext
		if n > 1 {
			name = fmt.Sprintf("%s copy %d%s", stem, n, ext)
		}
		dest = filepath.Join(dir, name)
		if _, err := os.Lstat(dest); err != nil {
			return dest
		}
	}
}

// copyTree copies src to dst recursively: directories re-create their
// entries, symlinks re-link their targets, files copy bytes + mode.
// dst must not exist yet (uniqueDestPath guarantees that). progress is
// called once per copied entry so the background runner can narrate;
// nil is allowed for callers that don't care.
func copyTree(src, dst string, progress func()) error {
	if progress != nil {
		progress()
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), progress); err != nil {
				return err
			}
		}
		return nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	default:
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}
