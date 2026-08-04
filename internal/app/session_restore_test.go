// =============================================================================
// File: internal/app/session_restore_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the session capture/restore bridge between App and the
// internal/session store.

package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/session"
)

// TestCaptureSessionContents pins what gets remembered: project-
// relative tab paths with cursor/scroll, the active-tab mapping that
// skips untitled tabs, the expanded folders (recorded project-relative
// like the tab paths, root excluded), and the sidebar geometry.
func TestCaptureSessionContents(t *testing.T) {
	root := t.TempDir()
	p1 := mkFile(t, root, "one.go", "package x\nvar A = 1\n")
	mkFile(t, root, "sub/two.go", "package sub\n")
	a := newTestApp(t, root)
	// Expand a folder before capturing: the open directories are part
	// of the sidebar state the session promises to bring back, and
	// nothing else in this fixture would expand one.
	a.tree.ExpandDirs([]string{"sub"})
	a.openFile(p1)
	a.activeTabPtr().Cursor = editor.Position{Line: 1, Col: 4}
	a.activeTabPtr().ScrollY = 1

	// An untitled tab in the middle must not shift the active index.
	scratch, err := editor.NewTab("")
	if err != nil {
		t.Fatalf("scratch: %v", err)
	}
	a.tabs.Append(scratch)
	a.openFile(filepath.Join(root, "sub", "two.go"))
	a.tabs.ActivateAt(2) // two.go
	a.sidebarWidth = 31
	a.sidebarShown = false

	got := a.captureSession()
	if len(got.Tabs) != 2 {
		t.Fatalf("tabs: got %d, want 2 (untitled skipped)", len(got.Tabs))
	}
	if got.Tabs[0].Path != "one.go" || got.Tabs[0].Line != 1 || got.Tabs[0].ScrollY != 1 {
		t.Fatalf("tab 0: %+v", got.Tabs[0])
	}
	if got.Tabs[1].Path != filepath.Join("sub", "two.go") {
		t.Fatalf("tab 1 path: %q", got.Tabs[1].Path)
	}
	if got.Active != 1 {
		t.Fatalf("active: got %d, want 1 (untitled skipped)", got.Active)
	}
	if got.ActivePath != filepath.Join("sub", "two.go") {
		t.Fatalf("activePath: got %q", got.ActivePath)
	}
	if len(got.Expanded) != 1 || got.Expanded[0] != "sub" {
		t.Fatalf("expanded: got %v, want [sub]", got.Expanded)
	}
	if got.SidebarWidth != 31 || got.SidebarShown {
		t.Fatalf("sidebar: %+v", got)
	}
}

// TestRestoreActiveTabByPath: the active tab is remembered by path, so
// a vanished earlier file (which shifts every later index) still lands
// focus on the tab the user left it on.
func TestRestoreActiveTabByPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	mkFile(t, root, "target.go", "l1\n")
	mkFile(t, root, "other.go", "l1\n")
	if err := session.Save(root, session.Project{
		Tabs: []session.TabState{
			{Path: "gone.go"},
			{Path: "target.go"},
			{Path: "other.go"},
		},
		Active:       1,
		ActivePath:   "target.go",
		SidebarShown: true,
		SavedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	a := newTestApp(t, root)
	a.restoreSession()
	if a.tabs.Len() != 2 {
		t.Fatalf("tabs: got %d, want 2", a.tabs.Len())
	}
	tab := a.activeTabPtr()
	if tab == nil || filepath.Base(tab.Path) != "target.go" {
		t.Fatalf("active tab: %+v", tab)
	}
}

// TestRestoreSkipsMissingFiles: restoring a session whose files have
// partially vanished opens only the survivors and clamps the active
// index instead of pointing at a ghost.
func TestRestoreSkipsMissingFiles(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	mkFile(t, root, "keep.go", "l1\nl2\nl3\nl4\nl5\n")
	if err := session.Save(root, session.Project{
		Tabs: []session.TabState{
			{Path: "gone.go", Line: 1},
			{Path: "keep.go", Line: 2, Col: 1, ScrollY: 1},
		},
		Active:       1,
		SidebarWidth: 25,
		SidebarShown: true,
		SavedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	a := newTestApp(t, root)
	a.restoreSession()
	if a.tabs.Len() != 1 {
		t.Fatalf("tabs: got %d, want 1 (ghost skipped)", a.tabs.Len())
	}
	tab := a.activeTabPtr()
	if tab == nil || filepath.Base(tab.Path) != "keep.go" {
		t.Fatalf("active tab: %+v", tab)
	}
	if tab.Cursor != (editor.Position{Line: 2, Col: 1}) || tab.ScrollY != 1 {
		t.Fatalf("view state: cursor %+v scroll %d", tab.Cursor, tab.ScrollY)
	}
	if a.sidebarWidth != 25 {
		t.Fatalf("sidebar width: got %d", a.sidebarWidth)
	}
}

// TestRestoreNoSessionIsNoop: a project with no saved session starts
// clean — no tabs, defaults untouched.
func TestRestoreNoSessionIsNoop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.restoreSession()
	if a.tabs.Len() != 0 {
		t.Fatalf("expected no tabs, got %d", a.tabs.Len())
	}
	if !a.sidebarShown || a.sidebarWidth != defaultSidebarWidth {
		t.Fatal("defaults must survive a no-session restore")
	}
}

// TestSaveSessionRoundTrip drives saveSession → restoreSession end to
// end through the real store.
func TestSaveSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	p1 := mkFile(t, root, "a.go", "l1\nl2\nl3\n")
	a := newTestApp(t, root)
	a.openFile(p1)
	a.activeTabPtr().Cursor = editor.Position{Line: 2}
	a.saveSession()

	b := newTestApp(t, root)
	b.restoreSession()
	if b.tabs.Len() != 1 || b.activeTabPtr().Cursor.Line != 2 {
		t.Fatalf("round trip failed: %d tabs", b.tabs.Len())
	}
}

// TestSessionPreservesPreviewFlag: a preview tab comes back as a
// preview, not silently promoted to a real tab.
func TestSessionPreservesPreviewFlag(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	p1 := mkFile(t, root, "a.go", "l1\n")
	a := newTestApp(t, root)
	a.openFilePreview(p1)
	a.saveSession()

	b := newTestApp(t, root)
	b.restoreSession()
	if b.tabs.Len() != 1 || !b.tabs.At(0).IsPreview() {
		t.Fatalf("preview flag lost: %d tabs", b.tabs.Len())
	}
}

// TestCloseTabSavesSession: closing a tab persists immediately, so a
// killed terminal after a close still remembers the right set.
func TestCloseTabSavesSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	p1 := mkFile(t, root, "a.go", "x\n")
	p2 := mkFile(t, root, "b.go", "y\n")
	a := newTestApp(t, root)
	a.openFile(p1)
	a.openFile(p2)
	a.closeTab(a.tabs.At(1))

	b := newTestApp(t, root)
	b.restoreSession()
	if b.tabs.Len() != 1 || filepath.Base(b.tabs.At(0).Path) != "a.go" {
		t.Fatalf("close should have persisted one tab, got %d", b.tabs.Len())
	}
}
