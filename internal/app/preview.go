// =============================================================================
// File: internal/app/preview.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// preview.go owns the shared file-open path and the preview-tab rules
// layered on top of it. A single tree click opens a *preview* tab
// (italic label) that the next tree click replaces in place, so
// browsing a project never piles up tabs. Editing the buffer, clicking
// the same file again, or opening it through a permanent path (finder,
// menu, CLI) pins the tab. The flag itself lives on editor.Tab.

package app

import (
	"fmt"
	"path/filepath"

	"github.com/johnlam90/skiff/internal/editor"
)

// openFilePreview opens path as a preview tab — the tree-click entry
// point. See openFileMode for the replace/pin rules.
func (a *App) openFilePreview(path string) {
	a.openFileMode(path, true)
}

// openFileMode is the one true file-open path. preview=false is a
// permanent open (and pins an existing preview of the same file);
// preview=true applies the preview rules:
//
//   - file already active as a preview → second click, pin it
//   - file already open otherwise      → just switch to it
//   - another preview tab exists       → replace it in its slot
//   - otherwise                        → append a fresh preview tab
func (a *App) openFileMode(path string, preview bool) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	a.setActiveFolder(filepath.Dir(path))
	if a.tree != nil {
		a.tree.ActiveFile = path
		// Reveal the file's location in the sidebar: expand every
		// ancestor directory and scroll the row into view. listH
		// mirrors Render's own list-area height (sidebarH - 2) so the
		// "already visible" guard inside Reveal matches the next paint.
		_, _, _, sh := a.sidebarRect()
		listH := sh - 2
		if listH < 0 {
			listH = 0
		}
		a.tree.Reveal(path, listH)
	}
	if t := a.tabs.Lookup(path); t != nil {
		if preview && t == a.tabs.Active() && t.IsPreview() {
			t.Pin()
			return
		}
		if !preview {
			t.Pin()
		}
		a.tabs.Activate(t)
		a.ensureActiveTabVisible()
		t.GitLines = loadGitLineChanges(a.rootDir, a.diffBase, t.Path)
		return
	}
	t, err := a.newTab(path)
	if err != nil {
		a.flash(fmt.Sprintf("Error: %v", err))
		return
	}
	t.Preview = preview
	if preview {
		a.tabs.InsertPreview(t)
	} else {
		a.tabs.Append(t)
	}
	a.finishOpen(t, path)
}

// newTab constructs a tab for path with the app-wide settings applied —
// the ONE construction path, shared by openFileMode and restoreSession,
// so a new per-tab step can never be added to one and forgotten in the
// other.
func (a *App) newTab(path string) (*editor.Tab, error) {
	t, err := editor.NewTab(path)
	if err != nil {
		return nil, err
	}
	t.Wrap = a.wrapOn
	t.GitLines = loadGitLineChanges(a.rootDir, a.diffBase, path)
	return t, nil
}

// finishOpen applies the bookkeeping shared by both new-tab paths:
// scroll the strip and announce the open.
func (a *App) finishOpen(t *editor.Tab, path string) {
	a.ensureActiveTabVisible()
	// Preview opens stay quiet — flashing "Opened X" on every tree
	// click while browsing is noise; a pinned open is worth announcing.
	if !t.IsPreview() {
		a.flash(fmt.Sprintf("Opened %s", filepath.Base(path)))
	}
}
