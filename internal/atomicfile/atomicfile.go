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
// readable.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

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
