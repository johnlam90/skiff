// =============================================================================
// File: internal/app/projfind_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the project-search panel: row grouping and folding, stale
// generation dropping, and the open-at-line activation path.

package app

import (
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/finder"
	"github.com/johnlam90/skiff/internal/search"
)

// projFindApp builds an app with a finder wired (openProjFind requires
// one) and the panel open.
func projFindApp(t *testing.T, root string) *App {
	t.Helper()
	a := newTestApp(t, root)
	a.finder = finder.New(root)
	a.openProjFind()
	if !a.projFindOpen {
		t.Fatal("panel should be open")
	}
	return a
}

// fakeMatches seeds the panel with a deterministic result set spanning
// two files.
func fakeMatches() []search.Match {
	return []search.Match{
		{Path: "a.go", Line: 3, Col: 0, Text: "alpha one"},
		{Path: "a.go", Line: 9, Col: 0, Text: "alpha two"},
		{Path: "b.go", Line: 1, Col: 0, Text: "beta"},
	}
}

// TestProjFindRowsGroupAndFold pins the row model: one header per file
// with its match count, match rows beneath, and folding a file removes
// its match rows while the header (and other files) stay put.
func TestProjFindRowsGroupAndFold(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFindMatches = fakeMatches()

	rows := a.projFindRows()
	if len(rows) != 5 {
		t.Fatalf("rows: got %d, want 5 (2 headers + 3 matches)", len(rows))
	}
	if !rows[0].IsHeader || rows[0].Path != "a.go" || rows[0].Count != 2 {
		t.Fatalf("header 0 wrong: %+v", rows[0])
	}
	if rows[3].IsHeader != true && rows[3].Path != "b.go" {
		t.Fatalf("expected b.go header near the end: %+v", rows[3])
	}

	a.projFindToggleFold("a.go")
	rows = a.projFindRows()
	if len(rows) != 3 {
		t.Fatalf("folded rows: got %d, want 3 (a.go header + b.go header + 1 match)", len(rows))
	}
	if !rows[0].IsHeader || rows[0].Path != "a.go" {
		t.Fatalf("folded header should remain: %+v", rows[0])
	}
	if a.projFindSelected != 0 {
		t.Fatalf("selection should snap to the folded header, got %d", a.projFindSelected)
	}
}

// TestProjFindStaleGenerationDropped: results from an outdated sweep
// must never land — only the generation the current query started.
func TestProjFindStaleGenerationDropped(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFindGen = 7

	a.handleProjFindDone(&projFindDoneEvent{when: time.Now(), gen: 6, matches: fakeMatches()})
	if len(a.projFindMatches) != 0 {
		t.Fatal("stale generation applied")
	}
	a.handleProjFindDone(&projFindDoneEvent{when: time.Now(), gen: 7, matches: fakeMatches(), truncated: true})
	if len(a.projFindMatches) != 3 || !a.projFindTruncated {
		t.Fatalf("current generation should land: %d matches", len(a.projFindMatches))
	}
}

// TestProjFindEnterOpensAtLine drives activation: selecting a match row
// and hitting Enter opens the file at that line and closes the panel.
func TestProjFindEnterOpensAtLine(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	a := projFindApp(t, root)
	a.projFindMatches = fakeMatches()

	a.projFindSelected = 2 // second match row of a.go (header, m0, m1)
	a.projFindActivate()
	if a.projFindOpen {
		t.Fatal("activation should close the panel")
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no tab opened")
	}
	if tab.Cursor.Line != 8 {
		t.Fatalf("cursor line: got %d, want 8 (line 9)", tab.Cursor.Line)
	}
}

// TestProjFindActivateHeaderFolds: Enter on a header folds instead of
// opening anything.
func TestProjFindActivateHeaderFolds(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFindMatches = fakeMatches()

	a.projFindSelected = 0
	a.projFindActivate()
	if !a.projFindOpen {
		t.Fatal("folding must not close the panel")
	}
	if !a.projFindFolded["a.go"] {
		t.Fatal("header activation should fold the file")
	}
	if len(a.tabs) != 0 {
		t.Fatal("folding must not open a tab")
	}
}

// TestProjFindEscCloses: Esc dismisses the panel and clears the query
// state so the next open starts fresh.
func TestProjFindEscCloses(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFindValue = []rune("abc")
	a.projFindMatches = fakeMatches()

	a.closeProjFind()
	if a.projFindOpen || a.projFindValue != nil || a.projFindMatches != nil {
		t.Fatal("close should clear the panel state")
	}
}
