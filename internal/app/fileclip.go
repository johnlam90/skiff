// =============================================================================
// File: internal/app/fileclip.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// fileclip.go implements the file clipboard: cut / copy a tree entry,
// paste it into (or beside) another, and duplicate in place. Nothing is
// ever overwritten — a taken name walks the " copy" / " copy 2" ladder,
// which is also how duplication works. Cut keeps the entry on disk until
// the paste lands; copy keeps the clipboard afterwards so one source can
// be pasted into several folders. Reachable from the tree's right-click
// menu and (per the project rule) from the main ≡ menu.

package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnlam90/skiff/internal/filetree"
)

// clipCutPath records path for a move-on-paste.
func (a *App) clipCutPath(path string) {
	a.fileClipPath = path
	a.fileClipCut = true
	a.flash(fmt.Sprintf("Cut %s — paste to move it", filepath.Base(path)))
}

// clipCopyPath records path for a copy-on-paste.
func (a *App) clipCopyPath(path string) {
	a.fileClipPath = path
	a.fileClipCut = false
	a.flash(fmt.Sprintf("Copied %s — paste to drop a copy", filepath.Base(path)))
}

// hasFileClip gates the paste rows: true while something is on the
// file clipboard.
func (a *App) hasFileClip() bool { return a.fileClipPath != "" }

// pasteDirForPath resolves a paste target to a directory: a folder
// takes the paste inside itself, a file takes it beside itself — so the
// user never has to aim at exactly the right row.
func pasteDirForPath(path string, isDir bool) string {
	if isDir {
		return path
	}
	return filepath.Dir(path)
}

// pasteInto drops the clipboard entry into dir, moving (cut) or
// copying (copy). Collisions walk the " copy" ladder; a folder can
// never be pasted into itself or a descendant; a cut entry pasted back
// into its own folder is a friendly no-op. Open tabs follow a move.
func (a *App) pasteInto(dir string) {
	src := a.fileClipPath
	if src == "" {
		return
	}
	info, err := os.Stat(src)
	if err != nil {
		a.flash(fmt.Sprintf("%s is gone — clipboard cleared", filepath.Base(src)))
		a.fileClipPath = ""
		return
	}
	if info.IsDir() {
		sep := string(filepath.Separator)
		if dir == src || strings.HasPrefix(dir+sep, src+sep) {
			a.flash("Can't paste a folder into itself")
			return
		}
	}
	if a.fileClipCut && filepath.Dir(src) == dir {
		a.flash(fmt.Sprintf("%s is already here", filepath.Base(src)))
		return
	}
	dest := uniqueDestPath(dir, filepath.Base(src), info.IsDir())
	if a.fileClipCut {
		a.startFileOp("Move", src, dest, true)
	} else {
		a.startFileOp("Paste", src, dest, false)
	}
}

// fileOpDoneEvent reports a finished background file operation.
type fileOpDoneEvent struct {
	when  time.Time
	label string
	src   string
	dest  string
	moved bool
	err   error
}

// When implements tcell.Event.
func (e *fileOpDoneEvent) When() time.Time { return e.when }

// fileOpProgressEvent ticks the status bar while a big copy runs.
type fileOpProgressEvent struct {
	when  time.Time
	label string
	count int64
}

// When implements tcell.Event.
func (e *fileOpProgressEvent) When() time.Time { return e.when }

// startFileOp runs a move or copy in the background so a
// node_modules-sized tree never freezes the editor (druk's rule). One
// at a time — racing two ops over the same paths helps nobody.
func (a *App) startFileOp(label, src, dest string, move bool) {
	if a.fileOpBusy {
		a.flash("Another file operation is still running")
		return
	}
	a.fileOpBusy = true
	a.flash(gerundOf(label) + " " + filepath.Base(src) + "…")
	scr := a.screen
	go func() {
		var count int64
		lastTick := time.Now()
		progress := func() {
			n := atomic.AddInt64(&count, 1)
			if time.Since(lastTick) > 150*time.Millisecond {
				lastTick = time.Now()
				_ = scr.PostEvent(&fileOpProgressEvent{when: time.Now(), label: label, count: n})
			}
		}
		var err error
		if move {
			err = moveEntry(src, dest, progress)
		} else {
			err = copyTree(src, dest, progress)
		}
		_ = scr.PostEvent(&fileOpDoneEvent{when: time.Now(), label: label, src: src, dest: dest, moved: move, err: err})
	}()
}

// handleFileOpProgress keeps the user informed mid-copy.
func (a *App) handleFileOpProgress(e *fileOpProgressEvent) {
	a.flash(fmt.Sprintf("%s… %d files", gerundOf(e.label), e.count))
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

// handleFileOpDone lands a finished op on the main loop: repoint tabs
// after a move, clear the cut clipboard, refresh everything.
func (a *App) handleFileOpDone(e *fileOpDoneEvent) {
	a.fileOpBusy = false
	if e.err != nil {
		a.flash(fmt.Sprintf("%s failed: %v", e.label, e.err))
		a.refreshTree()
		return
	}
	if e.moved {
		a.repointPaths(e.src, e.dest)
		if a.fileClipCut && a.fileClipPath == e.src {
			a.fileClipPath = ""
		}
		a.flash(fmt.Sprintf("Moved %s", filepath.Base(e.dest)))
	} else {
		a.flash(fmt.Sprintf("Pasted %s", filepath.Base(e.dest)))
	}
	a.refreshTree()
	a.refreshGitStatusAsync()
	a.invalidateFinder()
}

// moveEntry relocates src to dest: rename when the filesystem allows,
// copy-then-delete across devices (the one case os.Rename can't do).
func moveEntry(src, dest string, progress func()) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyTree(src, dest, progress); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// duplicatePath copies path beside itself under the next free " copy"
// name — the one-gesture version of copy + paste-beside.
func (a *App) duplicatePath(path string) {
	info, err := os.Stat(path)
	if err != nil {
		a.flash(fmt.Sprintf("Duplicate failed: %v", err))
		return
	}
	dest := uniqueDestPath(filepath.Dir(path), filepath.Base(path), info.IsDir())
	a.startFileOp("Duplicate", path, dest, false)
}

// repointPaths rewrites every open tab (and the active folder) that
// lives at or under oldPath so buffers stay attached to files that just
// moved. Shared by the paste-move path and folder renames.
func (a *App) repointPaths(oldPath, newPath string) {
	prefix := oldPath + string(filepath.Separator)
	for _, t := range a.tabs {
		switch {
		case t.Path == oldPath:
			t.Path = newPath
		case strings.HasPrefix(t.Path, prefix):
			t.Path = filepath.Join(newPath, t.Path[len(prefix):])
		default:
			continue
		}
		if info, err := os.Stat(t.Path); err == nil {
			t.Mtime = info.ModTime()
		} else {
			t.Mtime = time.Time{}
		}
		t.DiskGone = false
	}
	if a.activeFolder == oldPath {
		a.setActiveFolder(newPath)
	} else if strings.HasPrefix(a.activeFolder, prefix) {
		a.setActiveFolder(filepath.Join(newPath, a.activeFolder[len(prefix):]))
	}
	a.syncActiveTreeFile()
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
