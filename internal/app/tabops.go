// =============================================================================
// File: internal/app/tabops.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// tabops.go is the tab lifecycle and clipboard layer: opening files,
// saving one tab or every dirty one, closing with the unsaved-changes
// guard, and copy/cut/paste.
//
// The has* predicates at the bottom are the menu's enable/disable gates.
// They live beside the operations they guard so a new action and its
// precondition are read together — the menu table in menudef.go only
// references them.

package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/johnlam90/skiff/internal/clipboard"
	"github.com/johnlam90/skiff/internal/editor"
)

// activeTabPtr returns the currently active *editor.Tab, or nil when there
// are no tabs open.
func (a *App) activeTabPtr() *editor.Tab {
	return a.tabs.Active()
}

// OpenFile opens the file at path in a new tab — or switches to it if
// it is already open. Exported so main.go can seed the editor with the
// file the user named on the command line ("skiff foo.go"). Thin
// wrapper around openFile so internal callers keep using the lowercase
// name and the public surface stays small.
func (a *App) OpenFile(path string) { a.openFile(path) }

// openFile opens the file at path in a new permanent tab — or switches
// to it if it is already open (pinning a preview it lands on). Errors
// surface as a flash message. Whatever the path resolves to, its parent
// becomes the active folder so the next New File from the main menu
// lands next to it. The shared implementation lives in preview.go.
func (a *App) openFile(path string) {
	a.openFileMode(path, false)
}

// saveActiveTab writes the active tab's buffer to disk.
func (a *App) saveActiveTab() {
	a.saveTab(a.tabs.Active())
}

// saveTab saves tab. Returns true on success, false on any kind of
// failure (no tab, untitled, IO error). Failures flash a status message
// so the caller doesn't have to. Pulled out from saveActiveTab so the
// dirty-close modal can save a specific tab and branch on success —
// saving and then closing must not eat the user's work when the save
// itself failed.
func (a *App) saveTab(tab *editor.Tab) bool {
	if tab == nil {
		return false
	}
	if tab.Path == "" {
		a.flash("Saving untitled tabs is not supported yet")
		return false
	}
	if err := tab.Save(); err != nil {
		a.flash(fmt.Sprintf("Save failed: %v", err))
		return false
	}
	a.refreshGitStatusAsync()
	a.flash(fmt.Sprintf("Saved %s", filepath.Base(tab.Path)))
	// Format-on-save runs after the disk write succeeds, so a broken
	// formatter never blocks the user's save from landing. The
	// formatter (when configured + trusted) reloads the buffer
	// asynchronously when the formatter job lands — see format.go.
	a.runFormatOnSave(tab)
	return true
}

// saveAllDirty walks every open tab and saves each dirty one. Returns
// true when every dirty tab saved successfully — used by the quit flow
// to decide whether it's safe to actually exit. The first failure
// short-circuits because there's no point cascading more failed saves
// past one we've already flashed about, and the user needs to react to
// the first error before deciding what to do with the rest.
func (a *App) saveAllDirty() bool {
	for _, tab := range a.tabs.Tabs() {
		// A DiskGone tab is included even though it isn't Dirty: saving
		// it writes the buffer back out via os.WriteFile, which creates
		// the file again, resolving the "deleted on disk" state the
		// same way Save always has for a clean write.
		if !tab.Dirty && !tab.DiskGone {
			continue
		}
		if !a.saveTab(tab) {
			return false
		}
	}
	return true
}

// dirtyTabCount returns the number of tabs needing attention before a
// quit: tabs with unsaved edits (Dirty) and tabs whose backing file is
// gone (DiskGone) — both would lose the buffer's only surviving copy if
// the editor exited without asking. Used by the quit flow to decide
// whether to skip the modal entirely.
func (a *App) dirtyTabCount() int {
	n := 0
	for _, tab := range a.tabs.Tabs() {
		if tab.Dirty || tab.DiskGone {
			n++
		}
	}
	return n
}

// requestCloseTab closes tab. A clean tab with its file still on disk
// closes immediately; a dirty tab, or a clean tab whose file is
// DiskGone, opens the unsaved-changes modal so the user can pick Save /
// Discard / Cancel — a DiskGone tab's buffer is the only surviving copy
// of that content, so closing it silently would lose it exactly like a
// dirty tab would. The Save path saves the buffer first and only closes
// the tab on success — a save error would otherwise silently lose the
// user's work. The callbacks capture the tab itself, so any list
// mutation between the modal opening and the user's click (a preview
// replacement, another close) can never redirect them onto the wrong
// tab.
func (a *App) requestCloseTab(tab *editor.Tab) {
	if tab == nil || a.tabs.IndexOf(tab) < 0 {
		return
	}
	if !tab.Dirty && !tab.DiskGone {
		a.closeTab(tab)
		return
	}
	name := filepath.Base(tab.Path)
	if name == "" || name == "." {
		name = "untitled"
	}
	a.openDirtyClose(
		"Unsaved changes",
		name+" has unsaved changes.",
		func(app *App) {
			// Save → close. saveTab flashes its own error, in which
			// case we keep the tab around so the user can react.
			if app.saveTab(tab) {
				app.closeTab(tab)
			}
		},
		func(app *App) { app.closeTab(tab) },
	)
}

// closeTab removes tab without any dirty-check. The tab is recorded on
// the reopen stack first so Esc-o can bring it back; a tab that is no
// longer in the list (already closed by another path) is a no-op.
func (a *App) closeTab(tab *editor.Tab) {
	delete(a.mdPreview, tab)
	if tab == nil || a.tabs.IndexOf(tab) < 0 {
		return
	}
	a.recordClosedTab(tab)
	a.tabs.Remove(tab)
	defer a.saveSession()
	a.ensureActiveTabVisible()
	a.syncActiveTreeFile()
}

// copySelection puts the active tab's selection on the system clipboard
// (via OSC 52) and into the editor's internal clipboard.
//
// The internal copy happens first and unconditionally: even when the
// selection is too big for the terminal to carry, paste-inside-skiff
// still works, so the flash says what was lost rather than "copy
// failed". An oversized OSC 52 is the one failure the user can act on
// (select less), which is why it gets its own message instead of the
// generic "clipboard unavailable".
func (a *App) copySelection() {
	tab := a.activeTabPtr()
	if tab == nil || !tab.HasSelection() {
		return
	}
	txt := tab.SelectionText()
	a.clipBuf = txt
	if err := clipboard.CopyToSystem(txt); err != nil {
		if errors.Is(err, clipboard.ErrTooLarge) {
			a.flash("Selection too large for the terminal clipboard — copied inside skiff only")
			return
		}
		a.flash("Copied (system clipboard unavailable)")
		return
	}
	a.flash("Copied")
}

// cutSelection copies the selection then deletes it.
func (a *App) cutSelection() {
	tab := a.activeTabPtr()
	if tab == nil || !tab.HasSelection() {
		return
	}
	a.copySelection()
	tab.DeleteSelection()
}

// pasteClipboard inserts the editor's internal clipboard at the cursor.
// We can't read the system clipboard from a TUI, so external pastes have
// to come in through the user's terminal paste (Cmd-V / right-click paste).
func (a *App) pasteClipboard() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	if a.clipBuf == "" {
		a.flash("Internal clipboard empty — paste from your terminal (Cmd-V)")
		return
	}
	tab.InsertString(a.clipBuf)
}

// hasTab reports whether there is an active tab to act on.
func (a *App) hasTab() bool { return a.activeTabPtr() != nil }

// hasSavableTab reports whether the active tab is one we can persist —
// it must exist, have a path on disk, and not be a read-only image
// preview. Used by Save and Save & Close.
func (a *App) hasSavableTab() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != "" && !t.IsImage()
}

// hasFileTab reports whether the active tab is backed by a real file
// (text or image). Used by Rename / Delete which act on the file
// regardless of how the tab is rendered.
func (a *App) hasFileTab() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != ""
}

// hasSelection reports whether the active tab has a non-empty selection.
func (a *App) hasSelection() bool {
	t := a.activeTabPtr()
	return t != nil && t.HasSelection()
}

// hasCommentableTab reports whether the active tab is editable text with a
// known single-line comment marker.
func (a *App) hasCommentableTab() bool {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return false
	}
	_, ok := editor.LineCommentPrefix(t.Path)
	return ok
}

// hasClipboard reports whether the editor's internal clipboard has content
// to paste.
func (a *App) hasClipboard() bool { return a.clipBuf != "" }

// hasUndo reports whether the active tab has anything to undo. Used to
// enable / disable the Undo row in the action menu.
func (a *App) hasUndo() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanUndo()
}

// hasRedo reports whether the active tab has anything to redo.
func (a *App) hasRedo() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanRedo()
}

// hasRevert reports whether the active tab differs from its on-open
// (or last-reload) baseline — i.e. there is something to revert.
func (a *App) hasRevert() bool {
	t := a.activeTabPtr()
	return t != nil && t.CanRevert()
}

// hasEditableTab reports whether the active tab accepts text edits —
// the gate for the line-op menu rows (image tabs and no-tab both fail).
func (a *App) hasEditableTab() bool {
	t := a.activeTabPtr()
	return t != nil && !t.IsImage()
}
