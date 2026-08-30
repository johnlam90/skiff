// =============================================================================
// File: internal/app/fileops.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// fileops.go is the app-side adapter for the file manager's create /
// rename / delete / undo-delete operations. Each one is exposed two
// ways:
//
//   • From the main ≡ action menu, targeting the currently active tab
//     or the active folder.
//
//   • From the right-click context menu over a file-tree row. For
//     folders the menu offers New File (creates a child) plus Rename /
//     Delete; for files it offers Rename / Delete on the file itself.
//
// The disk work and every guard live in internal/filemanager. What is
// left here is the choreography around it: which tab or folder an
// action targets, the prompts and confirms, and applyChangeset — the
// ONE tail every file operation (this file's and fileclip.go's) runs
// after the manager returns, so the tree, the git tint, the finder
// index and the session can never be refreshed by one site and
// forgotten by another.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/clipboard"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filemanager"
	"github.com/johnlam90/skiff/internal/filetree"
)

// -----------------------------------------------------------------------------
// The post-op tail: one owner.
// -----------------------------------------------------------------------------

// applyChangeset is the single place the editor reacts to a file
// operation, successful or not. In order: every open tab under a Moved
// path is repointed (prefix-aware, so a folder move carries the tabs
// beneath it); every tab under a Removed path is closed and remembered
// against that path; the tree is refreshed so a restored path exists
// for Reveal; the tabs remembered for an Added path are reopened where
// the user left them (that is what makes Undo delete a full undo); then
// the git tint, the finder index and the session are refreshed.
//
// Call it with an empty Changeset on the error path too: a
// half-completed cross-device move has already changed the disk, and
// the index and tint would otherwise stay stale until the 10s tick.
func (a *App) applyChangeset(cs filemanager.Changeset) {
	for _, mv := range cs.Moved {
		a.repointPaths(mv.Old, mv.New)
	}
	for _, gone := range cs.Removed {
		a.closeRemovedTabs(gone)
	}
	a.refreshTree()
	for _, added := range cs.Added {
		a.reopenRemovedTabs(added)
	}
	a.refreshGitStatusAsync()
	a.invalidateFinder()
	a.saveSession()
}

// repointPaths rewrites every open tab (and the active folder) that
// lives at or under oldPath so buffers stay attached to files that just
// moved. tabPathRemoved-style prefix matching with the trailing
// separator avoids the /proj/foo vs /proj/foobar collision a substring
// match would hit. applyChangeset's implementation of a Move; nothing
// else calls it.
func (a *App) repointPaths(oldPath, newPath string) {
	prefix := oldPath + string(filepath.Separator)
	for _, t := range a.tabs.Tabs() {
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

// closeRemovedTabs closes every open tab whose file disappeared when
// gone was trashed, remembering where each one was so reopenRemovedTabs
// can put them back if the path is restored. The records are keyed by
// the removed path, not by tab: a folder delete closes several tabs in
// one gesture, and the restore that brings the folder back is one
// Added for the folder.
func (a *App) closeRemovedTabs(gone string) {
	doomed := a.orphanedTabs(gone)
	if len(doomed) == 0 {
		return
	}
	recs := make([]closedTabRecord, 0, len(doomed))
	for _, t := range doomed {
		recs = append(recs, closedTabRecord{Path: t.Path, Cursor: t.Cursor, ScrollY: t.ScrollY})
		a.closeTab(t)
	}
	if a.removedTabs == nil {
		a.removedTabs = map[string][]closedTabRecord{}
	}
	a.removedTabs[gone] = recs
}

// reopenRemovedTabs reopens the tabs a delete of added closed, in the
// order they were open, and forgets the record either way — once the
// path is back on disk the memory has done its job. A record whose file
// did not come back with the restore (deleted inside the folder before
// the folder itself was) is skipped silently: the restore is what the
// user asked for and it succeeded.
func (a *App) reopenRemovedTabs(added string) {
	recs, ok := a.removedTabs[added]
	if !ok {
		return
	}
	delete(a.removedTabs, added)
	for _, rec := range recs {
		a.reopenClosedTab(rec)
	}
}

// tabPathRemoved reports whether a tab pointing at tabPath is now
// orphaned because deletedPath was removed. True when the tab was
// the deleted file itself, or when it lived inside the deleted
// directory. The "/" separator is appended so /proj/foo deletion
// doesn't also catch /proj/foobar — a substring match would.
func tabPathRemoved(tabPath, deletedPath string) bool {
	if tabPath == "" {
		return false
	}
	if tabPath == deletedPath {
		return true
	}
	prefix := deletedPath + string(filepath.Separator)
	return strings.HasPrefix(tabPath, prefix)
}

// isProjectRoot reports whether folder refers to the project root in
// any spelling. Every root-destructive path (menu predicates, labels,
// and doDeletePath itself) checks this instead of comparing raw
// strings, so no launch form can make the root deletable. The manager
// resolved the root once at construction; this is its answer.
func (a *App) isProjectRoot(folder string) bool {
	return a.files.IsRoot(folder)
}

// -----------------------------------------------------------------------------
// Undo delete: the session trash the manager keeps.
// -----------------------------------------------------------------------------

// hasTrashedEntry is the menu predicate for the Undo delete row: true
// while the session trash holds at least one restorable item.
func (a *App) hasTrashedEntry() bool { return a.files.HasTrash() }

// menuUndoDelete restores the most recently deleted item from the
// session trash and reopens the tabs the delete closed (through
// applyChangeset). The manager refuses to clobber: if something new
// occupies the original path the entry stays in the trash and the
// flash says why, so a failed restore never destroys newer work.
func (a *App) menuUndoDelete() {
	a.closeMenu()
	if !a.files.HasTrash() {
		return
	}
	name := filepath.Base(a.files.LastTrashed())
	cs, err := a.files.Restore()
	if err != nil {
		a.applyChangeset(filemanager.Changeset{})
		a.flash(fmt.Sprintf("Restore failed: %v", err))
		return
	}
	a.applyChangeset(cs)
	a.flash(fmt.Sprintf("Restored %s", name))
}

// undoDeleteLabel is the dynamic label hook for the Undo delete menu
// row: it names what will come back, matching the "say the target
// before the click" rule the other file rows follow.
func (a *App) undoDeleteLabel() string {
	if !a.files.HasTrash() {
		return "Undo delete"
	}
	name := filepath.Base(a.files.LastTrashed())
	return trimRunes("Undo delete ("+name+")", maxLabelSuffix+len("Undo delete"))
}

// -----------------------------------------------------------------------------
// App glue: the op sites. Each one is manager → applyChangeset → flash.
// -----------------------------------------------------------------------------

// doCreateFile creates an empty file inside parent at the relative path
// name, refreshes everything, and opens the new file in a tab. Errors
// are surfaced as a flash. name may contain path separators so the user
// can drop a file into a subdirectory ("subdir/foo.go") — but the
// parent directories must already exist; the manager never silently
// mkdirs, to avoid creating folders the user didn't realise they were
// making.
//
// A name that climbs out of parent via "../" is refused outright — a
// deliberate UX narrowing, not an oversight: the prompt promised "in
// <parent>", and before this guard filepath.Join happily resolved such
// a name to a path outside the folder the user was looking at. The
// manager's own guard is the root; this one is the prompt's promise.
// Users who need to create a file elsewhere should navigate the tree to
// that folder first.
func (a *App) doCreateFile(parent, name string) {
	name = trimSpace(name)
	if name == "" {
		return
	}
	target := filepath.Join(parent, name)
	if !filemanager.Within(parent, target) {
		a.flash("Create refused: name escapes " + filepath.Base(parent))
		return
	}
	cs, err := a.files.Create(target)
	if err != nil {
		a.applyChangeset(filemanager.Changeset{})
		a.flash(fmt.Sprintf("Create failed: %v", err))
		return
	}
	a.applyChangeset(cs)
	a.openFile(target)
	a.flash(fmt.Sprintf("Created %s", name))
}

// doRename gives the file or folder at oldPath the sibling name newName
// and carries every open tab under it to the new path. One site for
// both kinds on purpose: the tree popup once routed folders through a
// file-only rename that rewrote exact-path tabs and nothing beneath a
// directory, stranding every open tab under the renamed folder on a
// dead path. The manager handles directories the same as files, and
// applyChangeset's prefix-aware repoint does the rest.
func (a *App) doRename(oldPath, newName string) {
	newName = trimSpace(newName)
	if newName == "" {
		return
	}
	cs, err := a.files.Rename(oldPath, newName)
	if err != nil {
		a.applyChangeset(filemanager.Changeset{})
		a.flash(fmt.Sprintf("Rename failed: %v", err))
		return
	}
	a.applyChangeset(cs)
	a.flash(fmt.Sprintf("Renamed to %s", newName))
}

// doDeletePath moves path (file or directory) into the session trash,
// closes any open tab whose file is gone as a result, and refreshes
// everything. The refusal to touch the project root is defense-in-
// depth: the menu predicates already gate root-targeting rows out, and
// the manager refuses it again, but this is the last check before the
// unsaved-changes prompt — which would otherwise list every open tab
// as doomed before the manager said no.
//
// Unsaved buffers get their own gate before anything moves. The trash
// only ever holds what was on disk, and the reopen stack remembers a
// path and a cursor — not buffer text — so a delete that silently
// closed a dirty tab destroyed work that Undo delete could not bring
// back. When any doomed tab is dirty the delete pauses on the
// unsaved-changes prompt instead.
func (a *App) doDeletePath(path string) {
	if a.isProjectRoot(path) {
		a.flash("Refusing to delete the project root")
		return
	}
	if dirty := a.dirtyOrphanedTabs(path); len(dirty) > 0 {
		a.confirmDeleteWithUnsaved(path, dirty)
		return
	}
	a.deletePathNow(path)
}

// orphanedTabs returns every open tab whose file disappears when path
// is deleted. For a folder delete that's not just an exact-path tab but
// every tab living *inside* the folder — otherwise the editor would
// keep showing buffers backed by files that no longer exist, and the
// next save would silently re-create them. tabPathRemoved encodes that
// "is this tab orphaned?" check so the loop reads as the rule it's
// enforcing rather than path arithmetic.
func (a *App) orphanedTabs(path string) []*editor.Tab {
	var doomed []*editor.Tab
	for _, t := range a.tabs.Tabs() {
		if tabPathRemoved(t.Path, path) {
			doomed = append(doomed, t)
		}
	}
	return doomed
}

// dirtyOrphanedTabs narrows orphanedTabs to the buffers that would lose
// unsaved work. A folder delete can hit several at once, which is why
// the prompt is built from a list rather than a single name.
func (a *App) dirtyOrphanedTabs(path string) []*editor.Tab {
	var dirty []*editor.Tab
	for _, t := range a.orphanedTabs(path) {
		if t.Dirty {
			dirty = append(dirty, t)
		}
	}
	return dirty
}

// confirmDeleteWithUnsaved raises the unsaved-changes prompt in front of
// a delete that would discard buffer content, naming every file at risk
// — "something has unsaved changes" is not something a user can act on.
//
// Save writes the buffers first, so the copy that lands in the trash is
// the work the user actually did and Undo delete restores it; a save
// that fails aborts the delete (saveTab flashes the reason) rather than
// destroying the file it could not preserve. Discard proceeds as the
// old unguarded path did. Cancel abandons the delete entirely.
//
// The tabs are captured by pointer and re-checked on activation: the
// prompt is modal, but a callback that fires against a tab some other
// path already closed must be a no-op, not a save into a stale buffer.
func (a *App) confirmDeleteWithUnsaved(path string, dirty []*editor.Tab) {
	names := make([]string, 0, len(dirty))
	for _, t := range dirty {
		names = append(names, t.DisplayName())
	}
	msg := fmt.Sprintf("%s has unsaved changes.", names[0])
	if len(names) > 1 {
		msg = fmt.Sprintf("%d open files have unsaved changes: %s.",
			len(names), strings.Join(names, ", "))
	}
	msg += fmt.Sprintf(" Deleting %s discards them unless you save first.",
		filepath.Base(path))
	doomed := append([]*editor.Tab(nil), dirty...)
	a.openDirtyClose(
		"Unsaved changes",
		msg,
		func(app *App) {
			for _, t := range doomed {
				if app.tabs.IndexOf(t) < 0 {
					continue
				}
				if !app.saveTab(t) {
					return
				}
			}
			app.deletePathNow(path)
		},
		func(app *App) { app.deletePathNow(path) },
	)
}

// deletePathNow is the delete itself, past every guard. The manager
// re-runs the root and existence checks because the unsaved-changes
// prompt opens a window in which the world can change — a save can
// replace the file, a background refresh can remove it — and the
// checks are cheap next to what they prevent.
func (a *App) deletePathNow(path string) {
	cs, err := a.files.Trash(path)
	if err != nil {
		a.applyChangeset(filemanager.Changeset{})
		a.flash(fmt.Sprintf("Delete failed: %v", err))
		return
	}
	a.applyChangeset(cs)
	a.flash(fmt.Sprintf("Deleted %s — ≡ Undo delete", filepath.Base(path)))
}

// -----------------------------------------------------------------------------
// Main menu actions: rename / delete the file backing the active tab.
// -----------------------------------------------------------------------------

// menuNewFile prompts the user for a filename and creates an empty file in
// the editor's active folder. The active folder is shown in the prompt's
// hint line so the user can see exactly where the file is going. Path
// separators are allowed in the input — typing "subdir/foo.go" lands the
// new file in subdir, relative to the active folder.
//
// If the active folder has been deleted on disk while the editor was open
// we silently fall back to the project root rather than handing the user
// a prompt rooted at a path that no longer exists.
func (a *App) menuNewFile() {
	a.closeMenu()
	folder := a.activeFolder
	if folder == "" {
		folder = a.rootDir
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		folder = a.rootDir
		a.setActiveFolder(folder)
	}
	hint := "in " + a.relativeFolderLabel(folder)
	a.openPrompt(
		"New file",
		hint,
		"",
		func(app *App, value string) {
			app.doCreateFile(folder, value)
		},
	)
}

// newFileLabel is the dynamic label hook for the New File menu row. It
// shows the bare label when the active folder is the project root and a
// "(in subfolder)" suffix otherwise, so the user can tell at a glance
// where the file will land before they even click.
func (a *App) newFileLabel() string {
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return "New file"
	}
	rel := a.relativeFolderLabel(folder)
	// Truncate so the row never overflows the modal width — see
	// maxLabelSuffix in app.go for why this is shared with the
	// folder-rename / folder-delete labels.
	const maxLen = maxLabelSuffix
	suffix := " (in " + rel + ")"
	if runeLen(suffix) > maxLen {
		// Drop characters from the middle of rel so the trailing folder
		// name (the most informative part) stays visible.
		keep := maxLen - len(" (in …)")
		if keep < 4 {
			keep = 4
		}
		if keep < len(rel) {
			rel = "…" + rel[len(rel)-keep:]
		}
		suffix = " (in " + rel + ")"
	}
	return "New file" + suffix
}

// relativeFolderLabel returns folder rendered relative to the project root,
// or just the basename when folder is the root itself. Used in the New
// File prompt's hint and the menu row's dynamic label.
func (a *App) relativeFolderLabel(folder string) string {
	if a.isProjectRoot(folder) {
		return filepath.Base(a.rootDir) + string(filepath.Separator)
	}
	rel, err := filepath.Rel(a.rootDir, folder)
	if err != nil || rel == "." {
		return filepath.Base(folder) + string(filepath.Separator)
	}
	return rel + string(filepath.Separator)
}

// menuRename opens a prompt pre-filled with the active tab's basename and
// renames the file on submit. Untitled tabs are skipped — the menu row is
// disabled for them anyway via hasSavableTab.
func (a *App) menuRename() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	old := tab.Path
	a.openPrompt(
		"Rename file",
		"in "+filepath.Dir(old),
		filepath.Base(old),
		func(app *App, value string) {
			app.doRename(old, value)
		},
	)
}

// menuDelete opens a Yes/No confirm modal; on Yes, removes the active tab's
// file from disk and closes the tab.
func (a *App) menuDelete() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	target := tab.Path
	a.openConfirm(
		"Delete file",
		"Permanently delete "+filepath.Base(target)+"?",
		func(app *App) {
			app.doDeletePath(target)
		},
	)
}

// menuRenameFolder opens a prompt pre-filled with the active
// folder's basename and renames the directory on submit. Mirrors
// menuRename but targets a folder rather than a file. The project
// root is gated out by hasActiveSubfolder so this never fires when
// rooted on the working dir itself.
func (a *App) menuRenameFolder() {
	a.closeMenu()
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return
	}
	old := folder
	a.openPrompt(
		"Rename folder",
		"in "+filepath.Dir(old),
		filepath.Base(old),
		func(app *App, value string) {
			app.doRename(old, value)
		},
	)
}

// renameFolderLabel is the dynamic label hook for the Rename Folder
// menu row. Same shape as deleteFolderLabel — bare label at root,
// "(subdir/)" suffix otherwise. Without the suffix the user would
// have no way to tell what's about to be renamed before clicking.
func (a *App) renameFolderLabel() string {
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return "Rename folder"
	}
	rel := a.relativeFolderLabel(folder)
	const maxLen = maxLabelSuffix
	suffix := " (" + rel + ")"
	if runeLen(suffix) > maxLen {
		keep := maxLen - len(" (…)")
		if keep < 4 {
			keep = 4
		}
		if keep < len(rel) {
			rel = "…" + rel[len(rel)-keep:]
		}
		suffix = " (" + rel + ")"
	}
	return "Rename folder" + suffix
}

// menuDeleteFolder removes the editor's active folder (the same folder
// the New File entry targets) and everything inside it. Lives in the
// main menu so folder deletion has a discoverable, non-right-click
// path — macOS Terminal eats Button3, and the project's CLAUDE.md
// rule says every file action must be reachable from the ≡ menu.
//
// The project root is never deletable — hasActiveSubfolder gates the
// row out for that case so the user can't even see the action when
// it would be destructive enough to take down the whole session.
func (a *App) menuDeleteFolder() {
	a.closeMenu()
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		// The active folder vanished externally — bail rather than
		// flashing a confusing "Delete failed" once the confirm fires.
		return
	}
	target := folder
	a.openConfirm(
		"Delete folder",
		"Permanently delete "+filepath.Base(target)+" and everything inside?",
		func(app *App) {
			app.doDeletePath(target)
			// After the directory is gone we can't keep activeFolder
			// pointing at it — fall back to the project root so the
			// next New File doesn't try to write into a deleted dir.
			app.setActiveFolder(app.rootDir)
		},
	)
}

// deleteFolderLabel is the dynamic label hook for the Delete Folder
// menu row. Mirrors newFileLabel: bare "Delete folder" when nothing
// useful is selected, "Delete folder (subdir/)" when there is — so
// the user can tell at a glance what's about to vanish before they
// even open the confirm dialog.
func (a *App) deleteFolderLabel() string {
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return "Delete folder"
	}
	rel := a.relativeFolderLabel(folder)
	const maxLen = maxLabelSuffix
	suffix := " (" + rel + ")"
	if runeLen(suffix) > maxLen {
		keep := maxLen - len(" (…)")
		if keep < 4 {
			keep = 4
		}
		if keep < len(rel) {
			rel = "…" + rel[len(rel)-keep:]
		}
		suffix = " (" + rel + ")"
	}
	return "Delete folder" + suffix
}

// hasActiveSubfolder is the menu predicate shared by every "act on
// the active folder" row (Delete, Rename, …). True when activeFolder
// points at a real subdirectory of the project root. Lives next to
// hasFileTab so the file/folder predicates form a matched pair.
func (a *App) hasActiveSubfolder() bool {
	folder := a.activeFolder
	if folder == "" || a.isProjectRoot(folder) {
		return false
	}
	info, err := os.Stat(folder)
	return err == nil && info.IsDir()
}

// -----------------------------------------------------------------------------
// Context-menu actions: rename / delete / new-file against a tree node.
// -----------------------------------------------------------------------------

// ctxNewFile opens a prompt to name a new empty file inside n. n is always
// a directory — openTreeContext only adds this row for folder nodes. The
// folder is auto-expanded so the new file is visible immediately after the
// post-create tree refresh.
func ctxNewFile(a *App, n *filetree.Node) {
	if !n.IsDir {
		return
	}
	parent := n.Path
	if !n.Expanded {
		a.tree.Toggle(n)
	}
	a.openPrompt(
		"New file",
		"in "+parent,
		"",
		func(app *App, value string) {
			app.doCreateFile(parent, value)
		},
	)
}

// ctxRename opens a prompt pre-filled with n's basename and renames the
// file or folder on submit — the same doRename the ≡ rows use, so a
// folder renamed from the popup carries its open tabs exactly as one
// renamed from the menu does.
func ctxRename(a *App, n *filetree.Node) {
	if n == a.tree.Root {
		return
	}
	old := n.Path
	a.openPrompt(
		"Rename",
		"in "+filepath.Dir(old),
		n.Name,
		func(app *App, value string) {
			app.doRename(old, value)
		},
	)
}

// copyPathToSystemClipboard pushes path onto the host system clipboard via
// OSC 52 and flashes a confirmation (or the underlying error) so the user
// gets feedback — the OS clipboard is invisible from inside the TUI, and a
// silent action would leave the user wondering whether anything happened.
//
// label is the short word used in the success / error flash ("relative
// path" / "absolute path") so both menu paths share one helper without
// duplicating copy.
func (a *App) copyPathToSystemClipboard(path, label string) {
	if err := clipboard.CopyToSystem(path); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	a.flash(fmt.Sprintf("Copied %s: %s", label, path))
}

// relativePathFor returns path rendered relative to the project root. We
// resolve the root via tree.Root.Path because that is always absolute —
// App.rootDir keeps the user-supplied string verbatim ("." in the common
// case), and filepath.Rel refuses to mix a relative base with an absolute
// target. Tab and tree node paths are always absolute, so basing on the
// absolute root is what gives a clean repo-relative result.
//
// On the rare error path (different volume, etc.) we fall back to the
// absolute path so the user still gets something useful on the clipboard.
func (a *App) relativePathFor(path string) string {
	base := a.rootDir
	if a.tree != nil && a.tree.Root != nil {
		base = a.tree.Root.Path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

// absolutePathFor resolves path to an absolute filesystem path. Failures
// fall back to the input unchanged — Tab.Path and tree node paths are
// already absolute in normal use, so this is just defence-in-depth.
func absolutePathFor(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// menuCopyRelativePath copies the active tab's path, rendered relative to
// the project root, onto the host system clipboard via OSC 52. Works the
// same locally and over SSH — the terminal emulator is the thing that
// actually receives the clipboard write, regardless of where the editor
// is running.
func (a *App) menuCopyRelativePath() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	a.copyPathToSystemClipboard(a.relativePathFor(tab.Path), "relative path")
}

// menuCopyAbsolutePath copies the active tab's absolute path onto the host
// system clipboard. Useful when pasting the path into a shell on the same
// remote machine (e.g. another tmux pane running over the same SSH session).
func (a *App) menuCopyAbsolutePath() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	a.copyPathToSystemClipboard(absolutePathFor(tab.Path), "absolute path")
}

// ctxCopyRelativePath copies n's path (relative to the project root) onto
// the host system clipboard. Tree-context counterpart to menuCopyRelativePath.
func ctxCopyRelativePath(a *App, n *filetree.Node) {
	a.copyPathToSystemClipboard(a.relativePathFor(n.Path), "relative path")
}

// ctxCopyAbsolutePath copies n's absolute path onto the host system
// clipboard. Tree-context counterpart to menuCopyAbsolutePath.
func ctxCopyAbsolutePath(a *App, n *filetree.Node) {
	a.copyPathToSystemClipboard(absolutePathFor(n.Path), "absolute path")
}

// ctxDelete confirms and removes the file or folder the user clicked.
// Folder deletion is recursive (the whole subtree goes to the trash as
// a unit) so the confirm copy spells out "and everything inside" — the
// stakes are much higher than a single-file delete and the user should
// see that before clicking Yes. The project root itself is never
// deletable.
func ctxDelete(a *App, n *filetree.Node) {
	if n == a.tree.Root {
		return
	}
	target := n.Path
	title := "Delete file"
	msg := "Permanently delete " + n.Name + "?"
	if n.IsDir {
		title = "Delete folder"
		msg = "Permanently delete " + n.Name + " and everything inside?"
	}
	a.openConfirm(title, msg, func(app *App) {
		app.doDeletePath(target)
	})
}
