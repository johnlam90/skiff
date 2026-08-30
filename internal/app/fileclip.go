// =============================================================================
// File: internal/app/fileclip.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// fileclip.go implements the file clipboard: cut / copy a tree entry,
// paste it into (or beside) another, and duplicate in place. The disk
// work — the never-overwrite " copy" ladder, the into-itself guard,
// the cross-device fallback — is the file manager's; this file owns
// the clipboard state, the background job that keeps a
// node_modules-sized paste from freezing the editor, and the landing
// that hands the manager's Changeset to the main loop. Cut keeps
// the entry on disk until the paste lands; copy keeps the clipboard
// afterwards so one source can be pasted into several folders.
// Reachable from the tree's right-click menu and (per the project
// rule) from the main ≡ menu.

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/filemanager"
	"github.com/johnlam90/skiff/internal/filetree"
)

// fileClip is the file clipboard: the one entry on it and whether the
// next paste moves it (cut) or copies it. The zero value is empty.
type fileClip struct {
	path string // absolute path on the clipboard; "" = empty
	cut  bool   // true = paste moves; false = paste copies
}

// clipCutPath records path for a move-on-paste.
func (a *App) clipCutPath(path string) {
	a.fileClip = fileClip{path: path, cut: true}
	a.flash(fmt.Sprintf("Cut %s — paste to move it", filepath.Base(path)))
}

// clipCopyPath records path for a copy-on-paste.
func (a *App) clipCopyPath(path string) {
	a.fileClip = fileClip{path: path, cut: false}
	a.flash(fmt.Sprintf("Copied %s — paste to drop a copy", filepath.Base(path)))
}

// hasFileClip gates the paste rows: true while something is on the
// file clipboard.
func (a *App) hasFileClip() bool { return a.fileClip.path != "" }

// pasteDirForPath resolves a paste target to a directory: a folder
// takes the paste inside itself, a file takes it beside itself — so the
// user never has to aim at exactly the right row.
func pasteDirForPath(path string, isDir bool) string {
	if isDir {
		return path
	}
	return filepath.Dir(path)
}

// pasteInto drops the clipboard entry into dir, moving (cut) or copying
// (copy), on the background runner. The manager's guards — a folder
// never pasted into itself, a cut never "moved" back into its own
// folder, nothing ever overwritten — run inside the op and come back
// as its error. The one check kept here is the clipboard's own: an
// entry that vanished from disk since it was cut is dropped with a
// flash rather than sent off to fail.
func (a *App) pasteInto(dir string) {
	src := a.fileClip.path
	if src == "" {
		return
	}
	if _, err := os.Stat(src); err != nil {
		a.flash(fmt.Sprintf("%s is gone — clipboard cleared", filepath.Base(src)))
		a.fileClip = fileClip{}
		return
	}
	if a.fileClip.cut {
		a.startFileOp(fileOpMove, src, dir)
	} else {
		a.startFileOp(fileOpCopy, src, dir)
	}
}

// fileOpKind names which manager verb a background file op runs.
type fileOpKind uint8

const (
	fileOpMove fileOpKind = iota
	fileOpCopy
	fileOpDuplicate
)

// label is the verb the flashes are built from: "Move" / "Paste" /
// "Duplicate" — the user's word for the gesture, not the manager's.
func (k fileOpKind) label() string {
	switch k {
	case fileOpMove:
		return "Move"
	case fileOpCopy:
		return "Paste"
	default:
		return "Duplicate"
	}
}

// fileOpResult is what a finished background file operation lands: the
// manager's Changeset (empty on failure — the error rides beside it as
// the job's error) and what the flash needs to name it.
type fileOpResult struct {
	label string
	src   string
	cs    filemanager.Changeset
}

// startFileOp runs a move, copy or duplicate on the fileOp job so a
// node_modules-sized tree never freezes the editor (druk's rule). The
// job is Refuse — one at a time, because racing two ops over the same
// paths helps nobody. The goroutine calls the manager (Move / Copy /
// Duplicate read only its immutable fields, so this is safe beside the
// main loop's trash reads) and lands the Changeset through
// handleFileOpDone; nothing here touches UI state off the loop — the
// progress tick is a Notify whose closure runs on the loop too.
func (a *App) startFileOp(kind fileOpKind, src, dstDir string) {
	label := kind.label()
	scr := a.screen
	files := a.files
	// tick is the on-loop half of a progress report, built here so the
	// goroutine only ever posts it.
	tick := func(n int64) { a.handleFileOpProgress(label, n) }
	started := a.fileOp.Start(func(context.Context) (fileOpResult, error) {
		var count int64
		lastTick := time.Now()
		progress := func() {
			n := atomic.AddInt64(&count, 1)
			if time.Since(lastTick) > 150*time.Millisecond {
				lastTick = time.Now()
				_ = scr.PostEvent(asyncjob.Notify(func() { tick(n) }))
			}
		}
		var cs filemanager.Changeset
		var err error
		switch kind {
		case fileOpMove:
			cs, err = files.Move(src, dstDir, progress)
		case fileOpCopy:
			cs, err = files.Copy(src, dstDir, progress)
		default:
			cs, err = files.Duplicate(src, progress)
		}
		return fileOpResult{label: label, src: src, cs: cs}, err
	})
	if !started {
		a.flash("Another file operation is still running")
		return
	}
	a.flash(gerundOf(label) + " " + filepath.Base(src) + "…")
}

// handleFileOpProgress keeps the user informed mid-copy.
func (a *App) handleFileOpProgress(label string, count int64) {
	a.flash(fmt.Sprintf("%s… %d files", gerundOf(label), count))
}

// gerundOf maps an op verb to its progress form.
func gerundOf(label string) string {
	switch label {
	case "Move":
		return "Moving"
	case "Paste":
		return "Pasting"
	case "Duplicate":
		return "Duplicating"
	default:
		return label
	}
}

// doneVerbOf maps an op verb to its past-tense flash.
func doneVerbOf(label string) string {
	switch label {
	case "Move":
		return "Moved"
	case "Paste":
		return "Pasted"
	case "Duplicate":
		return "Duplicated"
	default:
		return label
	}
}

// handleFileOpDone lands a finished op on the main loop: clear a cut
// clipboard once its entry has moved, hand the Changeset to
// applyChangeset (tabs follow a move; tree, tint, index and session
// refresh), then flash. The error path runs the same tail with an
// empty Changeset — a cross-device move that died half-way has already
// changed the disk, and refreshing only the tree left the finder and
// the git tint stale.
func (a *App) handleFileOpDone(r fileOpResult, err error) {
	if err != nil {
		a.applyChangeset(filemanager.Changeset{})
		a.flash(fmt.Sprintf("%s failed: %v", r.label, err))
		return
	}
	if len(r.cs.Moved) > 0 && a.fileClip.cut && a.fileClip.path == r.src {
		a.fileClip = fileClip{}
	}
	a.applyChangeset(r.cs)
	name := filepath.Base(r.src)
	switch {
	case len(r.cs.Moved) > 0:
		name = filepath.Base(r.cs.Moved[0].New)
	case len(r.cs.Added) > 0:
		name = filepath.Base(r.cs.Added[0])
	}
	a.flash(fmt.Sprintf("%s %s", doneVerbOf(r.label), name))
}

// duplicatePath copies path beside itself under the next free " copy"
// name — the one-gesture version of copy + paste-beside.
func (a *App) duplicatePath(path string) {
	a.startFileOp(fileOpDuplicate, path, "")
}

// -----------------------------------------------------------------------------
// Menu entry points
// -----------------------------------------------------------------------------

// menuCutFile cuts the active tab's file onto the file clipboard.
func (a *App) menuCutFile() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil && t.Path != "" {
		a.clipCutPath(t.Path)
	}
}

// menuCopyFile copies the active tab's file onto the file clipboard.
func (a *App) menuCopyFile() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil && t.Path != "" {
		a.clipCopyPath(t.Path)
	}
}

// menuPasteEntry pastes the clipboard entry into the active folder.
func (a *App) menuPasteEntry() {
	a.closeMenu()
	a.pasteInto(a.activeFolder)
}

// pasteEntryLabel names the paste row after its target folder, the same
// way the New-file row does — "Paste into src/", not a mystery verb.
func (a *App) pasteEntryLabel() string {
	return "Paste into " + a.relativeFolderLabel(a.activeFolder)
}

// menuDuplicateFile duplicates the active tab's file beside itself.
func (a *App) menuDuplicateFile() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil && t.Path != "" {
		a.duplicatePath(t.Path)
	}
}

// -----------------------------------------------------------------------------
// Tree context-menu entry points
// -----------------------------------------------------------------------------

// ctxCutNode cuts the clicked tree entry.
func ctxCutNode(a *App, n *filetree.Node) { a.clipCutPath(n.Path) }

// ctxCopyNode copies the clicked tree entry.
func ctxCopyNode(a *App, n *filetree.Node) { a.clipCopyPath(n.Path) }

// ctxDuplicateNode duplicates the clicked tree entry beside itself.
func ctxDuplicateNode(a *App, n *filetree.Node) { a.duplicatePath(n.Path) }

// ctxPasteNode pastes into the clicked folder, or beside the clicked file.
func ctxPasteNode(a *App, n *filetree.Node) {
	a.pasteInto(pasteDirForPath(n.Path, n.IsDir))
}
