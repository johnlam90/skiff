// =============================================================================
// File: internal/app/reopen_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the reopen-closed-tab stack: what closeTab records and what
// menuReopenTab restores.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/editor"
)

// seedFile writes a small file and returns its absolute path.
func seedFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return path
}

// TestReopenRestoresCursor pins the core promise: closing a tab and
// reopening it puts the user back where they were — same file, same
// cursor, same scroll.
func TestReopenRestoresCursor(t *testing.T) {
	dir := t.TempDir()
	path := seedFile(t, dir, "a.txt")
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 3, Col: 1}
	tab.Anchor = tab.Cursor
	tab.ScrollY = 2

	a.closeTab(a.tabs.Active())
	if a.tabs.Len() != 0 {
		t.Fatalf("tab should be gone, have %d", a.tabs.Len())
	}
	if !a.hasClosedTab() {
		t.Fatal("closeTab should have recorded the tab")
	}

	a.menuReopenTab()
	tab = a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("reopened tab: got %+v, want %s", tab, path)
	}
	if tab.Cursor != (editor.Position{Line: 3, Col: 1}) {
		t.Fatalf("cursor: got %+v, want line 3 col 1", tab.Cursor)
	}
	if tab.ScrollY != 2 {
		t.Fatalf("scroll: got %d, want 2", tab.ScrollY)
	}
	if a.hasClosedTab() {
		t.Fatal("reopen should consume the stack entry")
	}
}

// TestReopenSkipsDeletedFile: a recorded file deleted from disk between
// close and reopen is dropped with a flash naming it, never opened as
// an empty ghost buffer and never dropped in silence.
func TestReopenSkipsDeletedFile(t *testing.T) {
	dir := t.TempDir()
	path := seedFile(t, dir, "gone.txt")
	a := newTestApp(t, dir)
	a.openFile(path)
	a.closeTab(a.tabs.Active())
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	a.statusMsg = ""
	a.menuReopenTab()
	if a.tabs.Len() != 0 {
		t.Fatalf("deleted file must not reopen, got %d tabs", a.tabs.Len())
	}
	if a.hasClosedTab() {
		t.Fatal("the dead entry should be consumed")
	}
	if !strings.Contains(a.statusMsg, "gone.txt") {
		t.Fatalf("the drop must flash the missing file's name, got %q", a.statusMsg)
	}
}

// TestReopenStackCap keeps the stack bounded: closing more tabs than
// the cap drops the oldest records, newest-first order preserved.
func TestReopenStackCap(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	for i := 0; i < maxClosedTabs+5; i++ {
		p := seedFile(t, dir, fmt.Sprintf("f%02d.txt", i))
		a.openFile(p)
		a.closeTab(a.tabs.Active())
	}
	if got := len(a.closedTabs); got != maxClosedTabs {
		t.Fatalf("stack len = %d, want cap %d", got, maxClosedTabs)
	}
	// The newest close must be the first reopened.
	a.menuReopenTab()
	want := fmt.Sprintf("f%02d.txt", maxClosedTabs+4)
	if got := filepath.Base(a.activeTabPtr().Path); got != want {
		t.Fatalf("reopened %s, want %s", got, want)
	}
}

// TestCloseUntitledNotRecorded: untitled scratch tabs have no path to
// come back to — closing one records nothing.
func TestCloseUntitledNotRecorded(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab, err := editor.NewTab("")
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	a.tabs.Append(tab)
	a.tabs.ActivateAt(0)

	a.closeTab(a.tabs.At(0))
	if a.hasClosedTab() {
		t.Fatal("untitled tab should not be recorded")
	}
}
