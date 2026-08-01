// =============================================================================
// File: internal/app/preview.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
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
	for i, t := range a.tabs {
		if t.Path != path {
			continue
		}
		if preview && i == a.activeTab && t.IsPreview() {
			t.Pin()
			return
		}
		if !preview {
			t.Pin()
		}
		a.activeTab = i
		a.ensureActiveTabVisible()
		t.GitLines = loadGitLineChanges(a.rootDir, t.Path)
		return
	}
	t, err := editor.NewTab(path)
	if err != nil {
		a.flash(fmt.Sprintf("Error: %v", err))
		return
	}
	t.Preview = preview
	if preview {
		if idx := a.previewTabIndex(); idx >= 0 {
			// Reuse the existing preview's slot so tab order — part of
			// the user's spatial memory — survives browsing.
			a.tabs[idx] = t
			a.activeTab = idx
			a.finishOpen(t, path)
			return
		}
	}
	a.tabs = append(a.tabs, t)
	a.activeTab = len(a.tabs) - 1
	a.finishOpen(t, path)
}

// finishOpen applies the bookkeeping shared by both new-tab paths:
// scroll the strip, seed git line marks, and announce the open.
func (a *App) finishOpen(t *editor.Tab, path string) {
	a.ensureActiveTabVisible()
	t.GitLines = loadGitLineChanges(a.rootDir, t.Path)
	// Preview opens stay quiet — flashing "Opened X" on every tree
	// click while browsing is noise; a pinned open is worth announcing.
	if !t.IsPreview() {
		a.flash(fmt.Sprintf("Opened %s", filepath.Base(path)))
	}
}

// previewTabIndex returns the index of the current preview tab, or -1.
// There is at most one: every preview open either replaces or pins it.
func (a *App) previewTabIndex() int {
	for i, t := range a.tabs {
		if t.IsPreview() {
			return i
		}
	}
	return -1
}
