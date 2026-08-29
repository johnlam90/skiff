// =============================================================================
// File: internal/app/projfind_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the project-search panel: row grouping and folding, stale
// generation dropping, the open-at-line activation path, the mouse
// hit-test (handleProjFindMouse, routed through the public handleMouse
// entry point), the draw pass (drawProjFind* against a SimulationScreen),
// and the matchRuneSpans highlight-span helper.

package app

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

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
	if !a.projFind.findOpen {
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
	a.projFind.findMatches = fakeMatches()

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
	if a.projFind.findSelected != 0 {
		t.Fatalf("selection should snap to the folded header, got %d", a.projFind.findSelected)
	}
}

// TestProjFindStaleGenerationDropped: results from an outdated sweep
// must never land — only the generation the current query started.
func TestProjFindStaleGenerationDropped(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFind.findGen = 7

	a.handleProjFindDone(&projFindDoneEvent{when: time.Now(), gen: 6, matches: fakeMatches()})
	if len(a.projFind.findMatches) != 0 {
		t.Fatal("stale generation applied")
	}
	a.handleProjFindDone(&projFindDoneEvent{when: time.Now(), gen: 7, matches: fakeMatches(), truncated: true})
	if len(a.projFind.findMatches) != 3 || !a.projFind.findTruncated {
		t.Fatalf("current generation should land: %d matches", len(a.projFind.findMatches))
	}
}

// TestProjFindEnterOpensAtLine drives activation: selecting a match row
// and hitting Enter opens the file at that line and closes the panel.
func TestProjFindEnterOpensAtLine(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	a := projFindApp(t, root)
	a.projFind.findMatches = fakeMatches()

	a.projFind.findSelected = 2 // second match row of a.go (header, m0, m1)
	a.projFindActivate()
	if a.projFind.findOpen {
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
	a.projFind.findMatches = fakeMatches()

	a.projFind.findSelected = 0
	a.projFindActivate()
	if !a.projFind.findOpen {
		t.Fatal("folding must not close the panel")
	}
	if !a.projFind.findFolded["a.go"] {
		t.Fatal("header activation should fold the file")
	}
	if a.tabs.Len() != 0 {
		t.Fatal("folding must not open a tab")
	}
}

// TestProjFindEscCloses: Esc dismisses the panel and clears the query
// state so the next open starts fresh.
func TestProjFindEscCloses(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFind.findValue = []rune("abc")
	a.projFind.findMatches = fakeMatches()

	a.closeProjFind()
	if a.projFind.findOpen || a.projFind.findValue != nil || a.projFind.findMatches != nil {
		t.Fatal("close should clear the panel state")
	}
}

// TestProjFindDebounceAndStaleKick: a kick event whose generation was
// superseded by a newer keystroke must not start a sweep, and Enter on
// a match lands the cursor on the match column, not column 0.
func TestProjFindDebounceAndStaleKick(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "xx alpha\n")
	a := projFindApp(t, root)

	a.projFind.findValue = []rune("al")
	a.projFindQueryChanged() // gen N
	staleGen := a.projFind.findGen
	a.projFind.findValue = []rune("alpha")
	a.projFindQueryChanged() // gen N+1 supersedes
	a.handleProjFindKick(&projFindKickEvent{when: time.Now(), gen: staleGen})
	// The stale kick must be inert: no done event for staleGen can win,
	// and busy stays owned by the live generation.
	if a.projFind.findGen != staleGen+1 {
		t.Fatalf("generation bookkeeping broke: %d vs %d", a.projFind.findGen, staleGen)
	}

	// Column landing via activation.
	a.projFind.findMatches = []search.Match{{Path: "a.go", Line: 1, Col: 3, Text: "xx alpha"}}
	a.projFind.findSelected = 1 // header row 0, match row 1
	a.projFindActivate()
	tab := a.activeTabPtr()
	if tab == nil || tab.Cursor.Col != 3 {
		t.Fatalf("activation should land on the match column, got %+v", tab.Cursor)
	}
}

// TestProjFindMouseClickOnMatchRowOpensFile drives handleProjFindMouse
// through the public a.handleMouse entry point (mouse.go:75-78's
// routing is part of what this pins) and checks a Button1 click on a
// match row opens that file at the matched line — the mouse-driven
// twin of TestProjFindEnterOpensAtLine.
func TestProjFindMouseClickOnMatchRowOpensFile(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	a := projFindApp(t, root)
	a.projFind.findMatches = fakeMatches()

	rows := a.projFindRows()
	// rows[1] is a.go's first match (line 3, "alpha one") — scrollY is
	// 0 so its screen row is exactly ey+1.
	if rows[1].IsHeader || rows[1].Path != "a.go" {
		t.Fatalf("row model changed under us: rows[1] = %+v", rows[1])
	}
	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+2, ey+1, tcell.Button1, tcell.ModNone))

	if a.projFind.findOpen {
		t.Fatal("clicking a match row should close the panel")
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no tab opened")
	}
	if want := filepath.Join(root, "a.go"); tab.Path != want {
		t.Fatalf("tab path: got %q, want %q", tab.Path, want)
	}
	if tab.Cursor.Line != 2 {
		t.Fatalf("cursor line: got %d, want 2 (line 3)", tab.Cursor.Line)
	}
}

// TestProjFindMouseClickOnHeaderFolds checks a Button1 click on a
// file-header row folds that group in place — the panel stays open and
// no tab opens, matching what Enter-on-header does in
// TestProjFindActivateHeaderFolds, but driven via the mouse.
func TestProjFindMouseClickOnHeaderFolds(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFind.findMatches = fakeMatches()

	ex, ey, _, _ := a.editorRect()
	// rows[0] is the a.go header at scrollY 0, so its row is exactly ey.
	a.handleMouse(tcell.NewEventMouse(ex+2, ey, tcell.Button1, tcell.ModNone))

	if !a.projFind.findOpen {
		t.Fatal("folding via mouse must not close the panel")
	}
	if !a.projFind.findFolded["a.go"] {
		t.Fatal("clicking the header row should fold its file")
	}
	if a.tabs.Len() != 0 {
		t.Fatal("folding via mouse must not open a tab")
	}
}

// TestProjFindMouseClickBelowLastRowIsNoop checks a click past the end
// of the row list is inert: no selection change, no panic. A stale
// hit-test that mapped an out-of-range y to a real row would apply an
// activation (or, worse for project-replace, a substitution) against
// the wrong match.
func TestProjFindMouseClickBelowLastRowIsNoop(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFind.findMatches = fakeMatches() // 5 rows total
	beforeSelected := a.projFind.findSelected
	beforeMatches := len(a.projFind.findMatches)

	ex, ey, _, eh := a.editorRect()
	if eh < 11 {
		t.Fatalf("editor too short for this test to stay inside the panel: eh=%d", eh)
	}
	a.handleMouse(tcell.NewEventMouse(ex+2, ey+10, tcell.Button1, tcell.ModNone))

	if !a.projFind.findOpen {
		t.Fatal("a click below the last row must not close the panel")
	}
	if a.projFind.findSelected != beforeSelected {
		t.Fatalf("selection changed on an out-of-range click: got %d, want %d", a.projFind.findSelected, beforeSelected)
	}
	if len(a.projFind.findMatches) != beforeMatches {
		t.Fatal("match set changed on an out-of-range click")
	}
	if a.tabs.Len() != 0 {
		t.Fatal("an out-of-range click must not open a tab")
	}
}

// TestProjFindDraw_PaintsResultsAndBar drives the whole draw path
// (drawProjFind -> drawProjFindResults -> drawProjFindRow and
// drawProjFindBar) against a SimulationScreen, the way
// TestDraw_AllPanels checks other panels: assert the file header, a
// match's line text, and the bar's counter text all land on the rows
// the layout math says they should.
func TestProjFindDraw_PaintsResultsAndBar(t *testing.T) {
	root := t.TempDir()
	a := projFindApp(t, root)
	a.projFind.findValue = []rune("alpha")
	a.projFind.findMatches = fakeMatches()

	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	_, ey, _, _ := a.editorRect()
	headerRow := screenLine(scr, ey)
	if !containsRunes([]rune(headerRow), "a.go") || !containsRunes([]rune(headerRow), "(2)") {
		t.Fatalf("header row %d = %q, want it to show a.go's path and count", ey, headerRow)
	}
	matchRow := screenLine(scr, ey+1)
	if !containsRunes([]rune(matchRow), "alpha one") {
		t.Fatalf("match row %d = %q, want the matched line text", ey+1, matchRow)
	}
	secondHeaderRow := screenLine(scr, ey+3)
	if !containsRunes([]rune(secondHeaderRow), "b.go") {
		t.Fatalf("header row %d = %q, want it to show b.go's path", ey+3, secondHeaderRow)
	}

	barRow := screenLine(scr, a.height-2)
	if !containsRunes([]rune(barRow), "Search project:") {
		t.Fatalf("bar row = %q, want the search label", barRow)
	}
	// NOT asserted here: a.projFindCounterText() ("3 matches in 2
	// files") actually appearing intact in the bar. Writing this test
	// surfaced a real layout bug in drawProjFindBar: the hint/counter
	// fit checks (`bw > runeLen(label)+runeLen(hint)+10` and
	// `bw > runeLen(label)+runeLen(counter)+4`) compare against the
	// label's width only, never the three mode chips' ~12 cells, so on
	// this suite's default 120x40 SimulationScreen (30-col sidebar ->
	// a 90-col bar) they both report "fits" while the chips have
	// already eaten into that budget. rightTextStart then lands left of
	// where the chips end, inputEnd <= inputStart trips the "no room"
	// fallback (inputEnd = bx+bw-1), and the query field's own draw
	// (which runs last) repaints across the just-drawn counter — here
	// turning "3 matches in 2 files" into "3 maalpha in 2 files" and
	// swallowing the ".*" chip entirely. This reproduces with any
	// nonzero query and match count at this width, not just this
	// test's data. Out of scope to fix in this test-only plan (plan
	// 019) — reported for a follow-up bug fix in projfind.go.
}

// TestMatchRuneSpans pins matchRuneSpans' contract: spans are rune
// (not byte) indexed, so a case runs multibyte text ahead of the match
// to check the byte-to-rune conversion stays aligned — a regression
// here would highlight the wrong cells without ever failing a
// byte-oriented comparison.
func TestMatchRuneSpans(t *testing.T) {
	cases := []struct {
		name        string
		text, query string
		want        [][2]int
	}{
		{
			name:  "ascii match",
			text:  "hello world",
			query: "world",
			want:  [][2]int{{6, 11}},
		},
		{
			name:  "repeated query on one line",
			text:  "ababab",
			query: "ab",
			want:  [][2]int{{0, 2}, {2, 4}, {4, 6}},
		},
		{
			// "héllo match": h é l l o SPACE m a t c h — the accented
			// rune is one rune but two UTF-8 bytes, so a byte-indexed
			// span would land one cell short of "match".
			name:  "multibyte text before the match",
			text:  "héllo match",
			query: "match",
			want:  [][2]int{{6, 11}},
		},
		{
			name:  "no match",
			text:  "hello",
			query: "xyz",
			want:  nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchRuneSpans(c.text, c.query)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("matchRuneSpans(%q, %q) = %v, want %v", c.text, c.query, got, c.want)
			}
		})
	}
}
