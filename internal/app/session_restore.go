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
	for i, t := range a.tabs {
		if t.Path == "" {
			continue
		}
		rel, err := filepath.Rel(a.rootDir, t.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if i == a.activeTab {
			p.Active = len(p.Tabs)
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
// before the event loop starts.
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
	for _, ts := range p.Tabs {
		abs := filepath.Join(a.rootDir, filepath.FromSlash(ts.Path))
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			continue
		}
		t, err := editor.NewTab(abs)
		if err != nil {
			continue
		}
		t.Cursor = t.Buffer.Clamp(editor.Position{Line: ts.Line, Col: ts.Col})
		t.Anchor = t.Cursor
		t.ScrollY = ts.ScrollY
		t.Preview = ts.Preview
		t.GitLines = loadGitLineChanges(a.rootDir, a.diffBase, abs)
		a.tabs = append(a.tabs, t)
	}
	if len(a.tabs) > 0 {
		a.activeTab = p.Active
		if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
			a.activeTab = 0
		}
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
