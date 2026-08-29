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
//
// None of that is visible on screen, so the first preview of a session
// says it out loud once — see notePreviewCreated.

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
		// Same rationale as newTab: no inline `git diff` on a click. The
		// coalescing async refresh converges the gutter shortly after —
		// see refreshGitStatusAsync's doc comment.
		a.refreshGitStatusAsync()
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
	// A newly opened tab starts with GitLines nil (see newTab) — kick the
	// async refresh so its gutter arrives without waiting for the 10s
	// tick.
	a.refreshGitStatusAsync()
	if preview {
		a.notePreviewCreated()
	}
}

// newTab constructs a tab for path with the app-wide settings applied —
// the ONE construction path, shared by openFileMode and restoreSession,
// so a new per-tab step can never be added to one and forgotten in the
// other. Deliberately does NOT load git gutter lines inline: that used
// to be one `git diff` subprocess per call, so session restore's loop
// serialized N subprocess waits before the first paint. GitLines starts
// nil and arrives via the async git-status pipeline instead (see
// refreshGitStatusAsync / applyGitStatus) — a fresh tab renders without
// gutter marks for one round trip.
func (a *App) newTab(path string) (*editor.Tab, error) {
	t, err := editor.NewTab(path)
	if err != nil {
		return nil, err
	}
	t.Wrap = a.wrapOn
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

// notePreviewCreated explains preview tabs the first time this session
// makes one. The behavior is good and completely silent: the label goes
// italic and the next single tree click reuses the slot, which reads as
// "my tabs keep vanishing" to anyone who has never heard the word
// preview. One flash per session, not per preview — the rule lands the
// moment it is read, and repeating it on every tree click would be
// exactly the browsing noise finishOpen stays quiet to avoid.
func (a *App) notePreviewCreated() {
	if a.previewCoachShown {
		return
	}
	a.previewCoachShown = true
	a.flash("Preview tab — edit it or click again to keep it open")
}
