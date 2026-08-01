// =============================================================================
// File: internal/app/projreplace_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for project-wide replace: the Tab field gesture, the open-tab
// vs closed-file routing, the verify-skip guard, and the dirty-tab
// save semantics.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/search"
)

// pumpReplaceDone waits out the background disk apply and lands its
// report through the real handler.
func pumpReplaceDone(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.fileOpBusy {
		if time.Now().After(deadline) {
			t.Fatal("replace never finished")
		}
		if ev := a.screen.PollEvent(); ev != nil {
			a.handleEvent(ev)
		}
	}
}

// TestProjReplaceTabGesture: Tab grows and focuses the replace field,
// typed runes land there (leaving the query alone), and Tab hops back.
func TestProjReplaceTabGesture(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.txt", "old\n")
	a := projFindApp(t, root)
	a.projFindValue = []rune("old")

	a.handleProjFindKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if !a.projReplaceOpen || !a.projFocusReplace {
		t.Fatal("Tab should open and focus the replace field")
	}
	for _, r := range "new" {
		a.handleProjFindKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	if string(a.projReplaceValue) != "new" || string(a.projFindValue) != "old" {
		t.Fatalf("typing routed wrong: %q / %q", a.projReplaceValue, a.projFindValue)
	}
	a.handleProjFindKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if a.projFocusReplace {
		t.Fatal("second Tab should hop back to the query")
	}
}

// TestProjReplaceRow_OpenCleanTabSaves: a single-row apply on an open
// clean tab rewrites the buffer, saves it, and one undo restores.
func TestProjReplaceRow_OpenCleanTabSaves(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "a.txt", "keep\nold value\n")
	a := projFindApp(t, root)
	a.openFile(p)
	a.projFindValue = []rune("old")
	a.projReplaceValue = []rune("new")
	a.projFindMatches = []search.Match{{Path: "a.txt", Line: 2, Col: 0, Text: "old value"}}

	a.projReplaceRowApply(projFindRow{Path: "a.txt", MatchIdx: 0})
	tab := a.activeTabPtr()
	if tab.Buffer.Lines[1] != "new value" {
		t.Fatalf("buffer: %q", tab.Buffer.Lines[1])
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "new value") {
		t.Fatalf("clean tab should have saved: %q", data)
	}
	tab.Undo()
	if tab.Buffer.Lines[1] != "old value" {
		t.Fatalf("undo should restore: %q", tab.Buffer.Lines[1])
	}
}

// TestProjReplaceRow_DirtyTabStaysDirty: an open dirty tab gets the
// replacement in its buffer but the disk file is left alone — the save
// decision stays with the user.
func TestProjReplaceRow_DirtyTabStaysDirty(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "a.txt", "old value\n")
	a := projFindApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()
	tab.InsertRune('x') // dirty, unrelated edit on line 1 start
	line0 := tab.Buffer.Lines[0]
	a.projFindValue = []rune("old")
	a.projReplaceValue = []rune("new")
	a.projFindMatches = []search.Match{{Path: "a.txt", Line: 1, Col: 0, Text: line0}}

	a.projReplaceRowApply(projFindRow{Path: "a.txt", MatchIdx: 0})
	if !strings.Contains(tab.Buffer.Lines[0], "new") {
		t.Fatalf("buffer should carry the replacement: %q", tab.Buffer.Lines[0])
	}
	if !tab.Dirty {
		t.Fatal("dirty tab must stay dirty (no auto-save)")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "new") {
		t.Fatalf("disk must be untouched for dirty tabs: %q", data)
	}
}

// TestProjReplaceAll_MixedRouting drives the whole confirm flow: an
// open dirty tab applies in-buffer, a closed file rewrites on disk, a
// drifted closed file is skipped, and the combined report says so.
func TestProjReplaceAll_MixedRouting(t *testing.T) {
	root := t.TempDir()
	openPath := mkFile(t, root, "open.txt", "old here\n")
	mkFile(t, root, "closed.txt", "old there\n")
	mkFile(t, root, "drift.txt", "old gone\n")
	a := projFindApp(t, root)
	a.openFile(openPath)
	tab := a.activeTabPtr()
	tab.Dirty = true // simulate unsaved state without changing line 1

	a.projFindValue = []rune("old")
	a.projReplaceValue = []rune("new")
	a.projFindMatches = []search.Match{
		{Path: "open.txt", Line: 1, Col: 0, Text: "old here"},
		{Path: "closed.txt", Line: 1, Col: 0, Text: "old there"},
		{Path: "drift.txt", Line: 1, Col: 0, Text: "old gone"},
	}
	// Drift the third file after the "sweep".
	if err := os.WriteFile(filepath.Join(root, "drift.txt"), []byte("edited since\n"), 0644); err != nil {
		t.Fatalf("drift: %v", err)
	}

	a.projReplaceConfirmAll()
	if !a.confirmOpen || !strings.Contains(a.confirmMessage, "3 match(es) in 3 file(s)") {
		t.Fatalf("confirm: %v %q", a.confirmOpen, a.confirmMessage)
	}
	cb := a.confirmCallback
	a.closeAllModals()
	cb(a)
	pumpReplaceDone(t, a)

	if tab.Buffer.Lines[0] != "new here" || !tab.Dirty {
		t.Fatalf("open tab: %q dirty=%v", tab.Buffer.Lines[0], tab.Dirty)
	}
	data, _ := os.ReadFile(filepath.Join(root, "open.txt"))
	if strings.Contains(string(data), "new") {
		t.Fatal("dirty tab's disk file must be untouched")
	}
	data, _ = os.ReadFile(filepath.Join(root, "closed.txt"))
	if string(data) != "new there\n" {
		t.Fatalf("closed file: %q", data)
	}
	data, _ = os.ReadFile(filepath.Join(root, "drift.txt"))
	if string(data) != "edited since\n" {
		t.Fatalf("drifted file must be skipped: %q", data)
	}
	if !strings.Contains(a.statusMsg, "Replaced 2 in 2 file(s)") ||
		!strings.Contains(a.statusMsg, "1 skipped") {
		t.Fatalf("report flash: %q", a.statusMsg)
	}
}
