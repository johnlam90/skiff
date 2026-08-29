// =============================================================================
// File: internal/app/session_restore.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// session_restore.go bridges App and the internal/session store: capture
// the restorable state on quit, re-apply it on startup. Both directions
// are best-effort — a missing or stale session silently degrades to a
// fresh start, and single-file mode (no tree, no project) opts out of
// the whole mechanism.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/session"
)

// captureSession snapshots the pieces of App state worth restoring:
// project-relative tab paths with cursor/scroll, the active tab,
// expanded folders, and the sidebar geometry. Untitled tabs and files
// outside the project root are skipped — they have no stable identity
// to come back to.
func (a *App) captureSession() session.Project {
	p := session.Project{
		SidebarWidth: a.sidebarWidth,
		SidebarShown: a.sidebarShown,
	}
	for i, t := range a.tabs.Tabs() {
		if t.Path == "" {
			continue
		}
		rel, err := filepath.Rel(a.rootDir, t.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if i == a.tabs.ActiveIndex() {
			p.Active = len(p.Tabs)
			p.ActivePath = rel
		}
		p.Tabs = append(p.Tabs, session.TabState{
			Path:    rel,
			Line:    t.Cursor.Line,
			Col:     t.Cursor.Col,
			ScrollY: t.ScrollY,
			Preview: t.IsPreview(),
		})
	}
	if a.tree != nil {
		p.Expanded = a.tree.ExpandedDirs()
	}
	return p
}

// restoreSession re-applies the saved session for this project root, if
// any: expanded folders first (cheap, always safe), then tabs whose
// files still exist, then the active tab and sidebar. Called from New
// before the event loop starts. The active tab is matched by path, not
// by saved index — skipping a vanished file shifts every later index,
// so an index would focus the wrong tab.
func (a *App) restoreSession() {
	if a.tree == nil {
		return
	}
	p, ok := session.Load(a.rootDir)
	if !ok {
		return
	}
	a.tree.ExpandDirs(p.Expanded)
	if p.SidebarWidth >= minSidebarWidth {
		a.sidebarWidth = p.SidebarWidth
	}
	a.sidebarShown = p.SidebarShown
	active, legacyActive := -1, -1
	for i, ts := range p.Tabs {
		abs := filepath.Join(a.rootDir, filepath.FromSlash(ts.Path))
		// A hand-edited or corrupted session entry could name a path
		// that climbs out of the project root via "../" — skip it
		// silently, the same shape as the stat/IsDir skip right below:
		// a stale or malformed session entry isn't worth a flash per tab.
		if !withinRoot(a.rootDir, abs) {
			continue
		}
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			continue
		}
		t, err := a.newTab(abs)
		if err != nil {
			continue
		}
		t.Cursor = t.Buffer.Clamp(editor.Position{Line: ts.Line, Col: ts.Col})
		t.Anchor = t.Cursor
		t.ScrollY = ts.ScrollY
		t.Preview = ts.Preview
		a.tabs.Append(t)
		if p.ActivePath != "" && ts.Path == p.ActivePath {
			active = a.tabs.Len() - 1
		}
		// Sessions written before ActivePath existed only have an
		// index; translate it through the survivors we actually opened.
		if i == p.Active {
			legacyActive = a.tabs.Len() - 1
		}
	}
	if active < 0 {
		active = legacyActive
	}
	if a.tabs.Len() > 0 {
		if active < 0 {
			active = 0
		}
		a.tabs.ActivateAt(active)
		a.syncActiveTreeFile()
	}
}

// saveSession persists the current session. Best-effort by design: a
// read-only home directory must never turn quitting into an error.
func (a *App) saveSession() {
	if a.tree == nil {
		return
	}
	p := a.captureSession()
	p.SavedAt = time.Now()
	_ = session.Save(a.rootDir, p)
}
