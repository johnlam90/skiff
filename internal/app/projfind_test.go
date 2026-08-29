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
	"strings"
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
	a.projFind.query.SetText("abc")
	a.projFind.findMatches = fakeMatches()

	a.closeProjFind()
	if a.projFind.findOpen || a.projFind.query.Text() != "" || a.projFind.findMatches != nil {
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

	a.projFind.query.SetText("al")
	a.projFindQueryChanged() // gen N
	staleGen := a.projFind.findGen
	a.projFind.query.SetText("alpha")
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
	a.projFind.query.SetText("alpha")
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
	// NOT asserted here: the query text appearing in the bar at all.
	// Writing this test surfaced a real layout bug in drawProjFindBar:
	// the hint/counter fit checks (`bw > runeLen(label)+runeLen(hint)+10`
	// and `bw > runeLen(label)+runeLen(counter)+4`) compare against the
	// label's width only, never the three mode chips' ~12 cells, so on
	// this suite's default 120x40 SimulationScreen (30-col sidebar ->
	// a 90-col bar) they both report "fits" while the chips have
	// already eaten into that budget. rightTextStart then lands left of
	// where the chips end and the query field has no box at all, so the
	// bar draws the counter and hint and skips the input — see
	// TestProjFindDraw_BarKeepsItsCounterAndHint, which pins that the
	// right-hand text survives. The input losing instead is the visible
	// symptom of the same fit-check bug, and it reproduces with any
	// nonzero query and match count at this width, not just this test's
	// data. Out of scope for the plan that added this test (plan 019)
	// and for the one that moved the fields onto overlay.Field —
	// reported for a follow-up bug fix in projfind.go.
}

// TestProjFindDraw_BarKeepsTheInputAtTheDefaultSize pins the panel's
// worst case, which is also its most common one: the suite's default
// 120x40 with a sidebar and a real match count. The bar's two fit checks
// measured the label against the hint and the counter but never against
// each other or the ~12 cells of mode chips, so both labels were drawn
// on a bar with no room left and the query field was squeezed out of
// existence — a user typing into an input they cannot see. The input now
// keeps minFieldWidth cells and the labels yield instead.
func TestProjFindDraw_BarKeepsTheInputAtTheDefaultSize(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	query := strings.Repeat("a", minFieldWidth-1)
	a.projFind.query.SetText(query)
	a.projFind.findMatches = fakeMatches()

	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	row := []rune(screenLine(scr, a.height-2))
	at := runeIndexOf(row, query)
	if at < 0 {
		t.Fatalf("the query field lost its cells: bar row = %q", string(row))
	}
	cx, cy, visible := scr.GetCursor()
	if !visible || cy != a.height-2 || cx != at+len(query) {
		t.Fatalf("caret (%d,%d) visible=%v, want it at (%d,%d) just after the query",
			cx, cy, visible, at+len(query), a.height-2)
	}
}

// TestProjFindDraw_BarDropsLabelsWholeNeverInPieces pins the other half
// of the bar's priority order. Which labels appear is a layout decision
// — the input outranks both, and at this width (the suite's default
// 120x40, chips and a real match count) the hint is dropped so the
// counter and the input can have the cells. What must never happen is a
// label drawn and then half-erased: Field.Draw blanks its whole box plus
// a cell either side before painting, so a field allowed to overlap the
// text on its right turns it into rubble. This test was written when it
// did exactly that, leaving "3 m" of the counter and no hint at all; it
// now asserts the weaker, correct rule, because "the hint always
// survives" is not something the bar promises.
func TestProjFindDraw_BarDropsLabelsWholeNeverInPieces(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.projFind.query.SetText("alpha")
	a.projFind.findMatches = fakeMatches()

	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	bar := []rune(screenLine(scr, a.height-2))
	// The counter outranks the hint and fits here, so it must be whole.
	if counter := a.projFindCounterText(); !containsRunes(bar, counter) {
		t.Errorf("bar row %q lost its counter %q", string(bar), counter)
	}
	// The hint may be absent. It may not be a fragment: any of it on
	// screen means all of it.
	hint := "Enter: open · Tab: replace · Esc: close"
	if runeIndexOf(bar, "Enter: open") >= 0 && !containsRunes(bar, hint) {
		t.Errorf("bar row %q left the hint in pieces", string(bar))
	}
	// And the input is never the thing that yields.
	if !containsRunes(bar, "alpha") {
		t.Errorf("bar row %q dropped the query the labels are supposed to yield to", string(bar))
	}
	// The mode chips are controls, not labels: they never yield either.
	for _, chip := range []string{"Aa", "⌇w", ".*"} {
		if !containsRunes(bar, chip) {
			t.Errorf("bar row %q lost the %q chip", string(bar), chip)
		}
	}
}

// TestHandleProjFindKey_TypingEditsQueryAndCaretMovesDoNotResweep pins
// the panel's half of the overlay.Field delegation: printable runes and
// Backspace land in the query and each start a fresh sweep generation,
// while Home/Right only move the caret. A caret move that spent a
// generation would launch a redundant disk walk on every arrow press.
func TestHandleProjFindKey_TypingEditsQueryAndCaretMovesDoNotResweep(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	for _, r := range "alpha" {
		a.handleProjFindKey(keyEv(tcell.KeyRune, r))
	}
	if a.projFind.query.Text() != "alpha" {
		t.Fatalf("typing did not reach the query field: %q", a.projFind.query.Text())
	}

	gen := a.projFind.findGen
	a.handleProjFindKey(keyEv(tcell.KeyHome, 0))
	a.handleProjFindKey(keyEv(tcell.KeyRight, 0))
	if a.projFind.findGen != gen {
		t.Fatalf("a caret move started a new sweep generation: %d -> %d", gen, a.projFind.findGen)
	}
	if a.projFind.query.Cursor != 1 {
		t.Fatalf("caret should have moved inside the query, cursor=%d", a.projFind.query.Cursor)
	}

	a.handleProjFindKey(keyEv(tcell.KeyBackspace2, 0))
	if a.projFind.query.Text() != "lpha" {
		t.Fatalf("backspace did not edit the query: %q", a.projFind.query.Text())
	}
	if a.projFind.findGen == gen {
		t.Fatal("an edit must start a new sweep generation")
	}
}

// TestProjFindDraw_ShowsCaretInsideALongReplaceField pins the replace
// field's caret window. The field used to paint from rune 0 forever, so
// a replacement longer than the field pushed the caret past the field's
// right edge, where drawProjFindBar's guard dropped the ShowCursor call
// entirely — the user kept typing into an input with no visible caret.
// The in-file find bar had already fixed exactly this for its own
// replace field; the panel's copy had drifted.
func TestProjFindDraw_ShowsCaretInsideALongReplaceField(t *testing.T) {
	a := projFindApp(t, t.TempDir())
	a.handleProjFindKey(keyEv(tcell.KeyTab, 0)) // grow + focus the replace field
	if !a.projFind.replaceOpen || !a.projFind.focusReplace {
		t.Fatal("precondition: Tab should open and focus the replace field")
	}
	for _, r := range strings.Repeat("r", 80) {
		a.handleProjFindKey(keyEv(tcell.KeyRune, r))
	}

	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	x0, x1 := a.projFind.replaceFieldX0, a.projFind.replaceFieldX1
	if x1 <= x0 {
		t.Fatalf("precondition: replace field has no room, [%d,%d)", x0, x1)
	}
	cx, cy, visible := scr.GetCursor()
	if !visible || cy != a.height-2 || cx < x0 || cx > x1 {
		t.Fatalf("caret (%d,%d) visible=%v is outside the replace field [%d,%d] on bar row %d",
			cx, cy, visible, x0, x1, a.height-2)
	}
}

// TestMatchRuneSpans_NonASCIICapitalStaysCaseSensitive pins the
// highlighter's smart-case trigger to the engine's. internal/search arms
// case-sensitivity with unicode.IsUpper, so a query carrying a non-ASCII
// capital searches case-sensitively there. The highlighter tested
// r >= 'A' && r <= 'Z', read the same query as all-lowercase, folded
// both sides, and tinted hits the engine never reported.
func TestMatchRuneSpans_NonASCIICapitalStaysCaseSensitive(t *testing.T) {
	if got := matchRuneSpans("été été", "Été"); got != nil {
		t.Fatalf("capital É should make the query case-sensitive, got spans %v", got)
	}
	if got, want := matchRuneSpans("Été été", "Été"), [][2]int{{0, 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-sensitive hit: got %v, want %v", got, want)
	}
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
