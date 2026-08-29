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

	"github.com/johnlam90/skiff/internal/editor"
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
	a.projFind.query.SetText("old")

	a.handleProjFindKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if !a.projFind.replaceOpen || !a.projFind.focusReplace {
		t.Fatal("Tab should open and focus the replace field")
	}
	for _, r := range "new" {
		a.handleProjFindKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	if a.projFind.replace.Text() != "new" || a.projFind.query.Text() != "old" {
		t.Fatalf("typing routed wrong: %q / %q", a.projFind.replace.Text(), a.projFind.query.Text())
	}
	a.handleProjFindKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if a.projFind.focusReplace {
		t.Fatal("second Tab should hop back to the query")
	}
}

// TestApplyMatchesToTab_MultiOccurrenceLineCountsOnce is the buffer-side
// counterpart of the disk-side idempotence fix: two per-occurrence
// matches on one line ("foo(foo)") must rewrite that line exactly once
// (one ReplaceLines staging) and report occ == 2, not 4 — the earlier
// per-match loop staged newLines[i] without mutating the buffer, so the
// second match on the line still verified against the ORIGINAL text and
// got double-counted on top of the first.
func TestApplyMatchesToTab_MultiOccurrenceLineCountsOnce(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "dup.txt", "foo(foo)\n")
	tab, err := editor.NewTab(p)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	group := []search.Match{
		{Path: "dup.txt", Line: 1, Col: 0, Text: "foo(foo)"},
		{Path: "dup.txt", Line: 1, Col: 4, Text: "foo(foo)"},
	}
	occ, skipped := applyMatchesToTab(tab, group, "foo", "bar", search.DefaultOptions())
	if occ != 2 || skipped != 0 {
		t.Fatalf("counts: occ=%d skipped=%d, want occ=2 skipped=0", occ, skipped)
	}
	if tab.Buffer.Lines[0] != "bar(bar)" {
		t.Fatalf("buffer: %q, want %q", tab.Buffer.Lines[0], "bar(bar)")
	}
}

// TestProjReplaceRow_OpenCleanTabSaves: a single-row apply on an open
// clean tab rewrites the buffer, saves it, and one undo restores.
func TestProjReplaceRow_OpenCleanTabSaves(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "a.txt", "keep\nold value\n")
	a := projFindApp(t, root)
	a.openFile(p)
	a.projFind.query.SetText("old")
	a.projFind.replace.SetText("new")
	a.projFind.findMatches = []search.Match{{Path: "a.txt", Line: 2, Col: 0, Text: "old value"}}

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
	a.projFind.query.SetText("old")
	a.projFind.replace.SetText("new")
	// Col must name "old"'s real position (1, after the inserted 'x') —
	// row-apply is occurrence-targeted now, so an inaccurate column would
	// miss the hit entirely instead of just being ignored.
	a.projFind.findMatches = []search.Match{{Path: "a.txt", Line: 1, Col: 1, Text: line0}}

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

// TestProjReplaceRow_TargetsSingleOccurrence_OpenTab: two rows on the
// same open-tab line ("foo(foo)", one at each occurrence) — applying the
// first must rewrite only that occurrence, leave the second "foo" intact
// in the buffer, and flash "Replaced 1", not 2.
func TestProjReplaceRow_TargetsSingleOccurrence_OpenTab(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "dup.txt", "foo(foo)\n")
	a := projFindApp(t, root)
	a.openFile(p)
	a.projFind.query.SetText("foo")
	a.projFind.replace.SetText("bar")
	a.projFind.findMatches = []search.Match{
		{Path: "dup.txt", Line: 1, Col: 0, Text: "foo(foo)"},
		{Path: "dup.txt", Line: 1, Col: 4, Text: "foo(foo)"},
	}

	a.projReplaceRowApply(projFindRow{Path: "dup.txt", MatchIdx: 0})
	tab := a.activeTabPtr()
	if tab.Buffer.Lines[0] != "bar(foo)" {
		t.Fatalf("buffer: %q, want the second occurrence untouched", tab.Buffer.Lines[0])
	}
	if !strings.Contains(a.statusMsg, "Replaced 1 on dup.txt:1") {
		t.Fatalf("flash: %q", a.statusMsg)
	}
}

// TestProjReplaceRow_TargetsSingleOccurrence_ClosedFile is the disk-path
// counterpart: applying the first of two same-line rows on a CLOSED file
// leaves the second occurrence on disk and flashes "Replaced 1".
func TestProjReplaceRow_TargetsSingleOccurrence_ClosedFile(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "dup.txt", "foo(foo)\n")
	a := projFindApp(t, root)
	a.projFind.query.SetText("foo")
	a.projFind.replace.SetText("bar")
	a.projFind.findMatches = []search.Match{
		{Path: "dup.txt", Line: 1, Col: 0, Text: "foo(foo)"},
		{Path: "dup.txt", Line: 1, Col: 4, Text: "foo(foo)"},
	}

	a.projReplaceRowApply(projFindRow{Path: "dup.txt", MatchIdx: 0})
	data, err := os.ReadFile(filepath.Join(root, "dup.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "bar(foo)\n" {
		t.Fatalf("file: %q, want the second occurrence untouched", data)
	}
	if !strings.Contains(a.statusMsg, "Replaced 1 on dup.txt:1") {
		t.Fatalf("flash: %q", a.statusMsg)
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

	a.projFind.query.SetText("old")
	a.projFind.replace.SetText("new")
	a.projFind.findMatches = []search.Match{
		{Path: "open.txt", Line: 1, Col: 0, Text: "old here"},
		{Path: "closed.txt", Line: 1, Col: 0, Text: "old there"},
		{Path: "drift.txt", Line: 1, Col: 0, Text: "old gone"},
	}
	// Drift the third file after the "sweep".
	if err := os.WriteFile(filepath.Join(root, "drift.txt"), []byte("edited since\n"), 0644); err != nil {
		t.Fatalf("drift: %v", err)
	}

	a.projReplaceConfirmAll()
	if c := confirmPrefab(t, a); !strings.Contains(c.Message, "3 match(es) in 3 file(s)") {
		t.Fatalf("confirm: %q", c.Message)
	}
	confirmYes(a)
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

// TestProjReplaceAll_ReportsBufferSaveFailure pins the swallowed-error
// regression: replace-all re-saved clean open tabs with `_ = tab.Save()`,
// so a failed write vanished and the done handler still flashed a plain
// success count. The user was told N files were replaced while one of
// them was never written. The report has to name the file and the tab has
// to stay dirty so the work is still recoverable.
func TestProjReplaceAll_ReportsBufferSaveFailure(t *testing.T) {
	root := t.TempDir()
	p := mkFile(t, root, "a.txt", "old value\n")
	a := projFindApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	// Force a real save failure that doesn't depend on who is running the
	// test: swap the file for a directory of the same name, so the write
	// hits EISDIR even as root.
	if err := os.Remove(p); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := os.Mkdir(p, 0755); err != nil {
		t.Fatalf("swap: %v", err)
	}

	a.projFind.query.SetText("old")
	a.projFind.replace.SetText("new")
	a.projFind.findMatches = []search.Match{{Path: "a.txt", Line: 1, Col: 0, Text: "old value"}}

	a.projReplaceConfirmAll()
	confirmYes(a)
	pumpReplaceDone(t, a)

	if tab.Buffer.Lines[0] != "new value" {
		t.Fatalf("buffer should still carry the replacement: %q", tab.Buffer.Lines[0])
	}
	if !tab.Dirty {
		t.Fatal("a tab whose save failed must stay dirty")
	}
	if !strings.Contains(a.statusMsg, "a.txt") {
		t.Fatalf("report must name the file that failed to save: %q", a.statusMsg)
	}
	if !strings.Contains(a.statusMsg, "save failed") {
		t.Fatalf("report must say the save failed, not just count successes: %q", a.statusMsg)
	}
}
