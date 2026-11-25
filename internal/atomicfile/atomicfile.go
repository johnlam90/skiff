// =============================================================================
// File: internal/atomicfile/atomicfile.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package atomicfile writes small config/state files without ever
// leaving a half-written file behind.
//
// Skiff persists a handful of user-visible JSON files outside the
// project: config.json (theme, wrap, icons), format-trust.json, the
// session store, and a project's .skiff/format.json. Every one of them
// is read back on the next start, so a truncated write is not a lost
// write — it is a *silently reset preference*, or worse, a trust store
// that forgets the user already said no.
//
// The fix is the usual temp-file + rename dance: write the full bytes
// into a sibling temp file, fsync it, then rename over the target.
// rename(2) is atomic within a filesystem, so a reader (or a crash)
// sees either the old file or the new one, never a prefix of the new
// one. Several call sites had grown their own copy of this; this
// package is the one implementation they all share.
package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data, creating the parent
// directory when it doesn't exist yet.
//
// The temp file is created in the *target's* directory rather than
// $TMPDIR because rename(2) only guarantees atomicity within a single
// filesystem — a /tmp on its own mount would silently degrade this to
// a copy. It is also created with a random suffix so two skiff
// instances saving at once can't scribble over each other's temp file
// and produce a garbled result.
//
// perm applies to the final file; the temp file is created 0600 and
// chmod'd before the rename so the bytes are never briefly world
// readable. Any directory MkdirAll has to create along the way is
// owner-only (0700) — Write is the path every per-user config/state
// file (session, trust store) goes through, so a directory it creates
// from scratch should default to private rather than group/other
// readable. This only affects directories Write itself creates:
// MkdirAll never chmods a directory that already exists, so a
// pre-created project directory (e.g. a project's .skiff/, made
// group/other readable by format.InstallCommandIntoProject) is left
// untouched.
func Write(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return replaceFile(path, data, perm)
}

// Replace atomically replaces the file at path with data, following the
// rules a *user's* file needs that Write's config-file contract doesn't:
// the path is resolved through symlinks first (saving through a link must
// update the target, not replace the link with a regular file), and the
// existing file's permission bits are preserved (a 0755 script stays
// executable).
//
// A file that doesn't exist yet is created the way any program creates
// one — 0666 filtered by the process umask — see createFile. A link
// whose target doesn't exist yet is followed to that target, so the
// first save through a dangling link creates what it points at.
//
// Known, accepted limitations of rename-based replace: hard links to
// the file are broken (the other names keep the old inode); xattrs,
// ACLs and ownership are not copied (chown needs privileges). If those
// matter, the alternative is write-in-place + fsync with a backup file
// — a different design.
func Replace(path string, data []byte) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// Nothing at the end of the path yet: a first save, possibly
		// through a link (or a chain of them) to a file not yet made.
		resolved, err = resolveMissing(path)
		if err != nil {
			return err
		}
	}
	info, err := os.Stat(resolved)
	if os.IsNotExist(err) {
		switch err := createFile(resolved, data); {
		case err == nil:
			return nil
		case !os.IsExist(err):
			return err
		}
		// Someone created it between the stat and the create. It is an
		// existing file now and is replaced like one.
		info, err = os.Stat(resolved)
	}
	perm := os.FileMode(0644)
	if err == nil {
		perm = info.Mode().Perm()
	}
	return replaceFile(resolved, data, perm)
}

// maxSymlinkHops bounds resolveMissing's walk, so a cycle of links is
// an error instead of a loop. 40 is the kernel's own customary limit.
const maxSymlinkHops = 40

// resolveMissing finds where a path that does not resolve should be
// created. filepath.EvalSymlinks cannot say: it fails on a dangling
// link exactly as it does on a missing file. Each step resolves the
// directory (which must exist), then looks at the last element without
// following it: missing means "create it here", a symlink means "go
// where it points", read relative to the link's own directory.
//
// Without this, a save through a dangling link renamed a regular file
// over the link, quietly detaching the path from its target.
func resolveMissing(path string) (string, error) {
	for hop := 0; hop < maxSymlinkHops; hop++ {
		dir, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, filepath.Base(path))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		path = target
	}
	return "", &os.PathError{Op: "resolve", Path: path, Err: errors.New("too many levels of symbolic links")}
}

// createFile writes a file that does not exist yet, and fails with an
// IsExist error if it turns out to. There is no previous content to
// protect, so the temp-and-rename dance buys nothing here — and it
// costs the umask: a temp file has to be chmod'ed to its final mode,
// which overrode the user's umask and made the first save of a file
// 0644 even under a private 077. Creating it directly lets the kernel
// apply the umask, as it does for every other program.
//
// A failed write removes the file it created; the data is fsynced and
// the directory entry synced like any other save.
func createFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	_ = syncDir(filepath.Dir(path))
	return nil
}

// replaceFile is the temp/rename/sync core Write and Replace share:
// write the full bytes into a sibling temp file, fsync, rename over the
// target, then sync the directory. The parent directory must already
// exist — Write's MkdirAll and Replace's resolve step each guarantee
// that in their own way.
func replaceFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// From here on every failure has to remove the temp file, or a
	// crashy disk leaves droppings next to the user's config forever.
	if err := writeAndSync(f, data, perm); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// The data rename already succeeded, so the caller's bytes are the
	// file's bytes; a failed directory sync must not report the save as
	// failed. It only narrows the crash window in which the rename
	// itself could be lost.
	_ = syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so a rename that just landed in it
// survives a crash — rename(2) is atomic but not durable until the
// directory entry itself reaches the disk. Best-effort by design: some
// platforms and filesystems refuse to open or sync a directory, and
// degrading quietly there is correct where refusing to save is not.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	_ = d.Sync()
	return nil
}

// writeAndSync pushes data into the open temp file, fixes its mode and
// forces it to stable storage. The fsync is the part people skip: on
// a crash, rename can land before the data blocks do, and the reader
// finds a correctly-named file full of zeros.
func writeAndSync(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	return f.Sync()
}
