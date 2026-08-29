// =============================================================================
// File: internal/app/find_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// seedFindApp opens a tab with content seeded for find tests so each
// test can focus on the behaviour under test rather than fixture setup.
func seedFindApp(t *testing.T, content string) *App {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	return a
}

// findBarOf returns the find bar occupying App's strip slot, failing the
// test when some other strip (or none) is up — the slot is the only
// "is the bar open" answer there is, so a test that reaches past it
// would be asserting against a bar the app is not routing to.
func findBarOf(t *testing.T, a *App) *findStrip {
	t.Helper()
	s := a.findBar()
	if s == nil {
		t.Fatalf("the find bar should own the strip slot, got %T", a.strip)
	}
	return s
}

// TestOpenFind_OpensBarEmpty drops the user into a focused find bar
// with an empty input. Pre-fill from a prior query is intentionally
// not done — closing the bar already clears find state, so each Esc-f
// is a fresh search.
func TestOpenFind_OpensBarEmpty(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	if !a.findBarOpen() {
		t.Fatalf("openFind did not put the bar in the strip slot, got %T", a.strip)
	}
	if len(findBarOf(t, a).query.Value) != 0 {
		t.Fatalf("input should be empty, got %q", findBarOf(t, a).query.Text())
	}
}

// TestOpenFind_NoTabIsNoOp guards against opening the bar when there's
// no text tab to search. Without this, the bar would float over an
// empty editor with nothing to highlight.
func TestOpenFind_NoTabIsNoOp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openFind()
	if a.findBarOpen() {
		t.Fatal("openFind should be a no-op with no tab")
	}
}

// TestHandleFindKey_TypingLiveSearches drives the per-keystroke handler
// the way a user would: type "foo", and the active tab's match list
// should be populated and the cursor should sit on the first match.
func TestHandleFindKey_TypingLiveSearches(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	if len(tab.FindMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(tab.FindMatches))
	}
	if tab.Cursor != (editor.Position{Line: 0, Col: 0}) {
		t.Fatalf("cursor should snap to first match, got %+v", tab.Cursor)
	}
}

// TestHandleFindKey_EnterAdvances simulates Enter inside the bar — it
// should jump to the next match, with wrap-around.
func TestHandleFindKey_EnterAdvances(t *testing.T) {
	a := seedFindApp(t, "foo\nfoo\nfoo")
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	findBarOf(t, a).handleKey(keyEv(tcell.KeyEnter, 0))
	if tab.FindIndex != 1 {
		t.Fatalf("expected FindIndex=1 after Enter, got %d", tab.FindIndex)
	}
	if tab.Cursor.Line != 1 {
		t.Fatalf("cursor should be on line 1, got %+v", tab.Cursor)
	}
}

// TestHandleFindKey_ShiftEnterGoesBack pins down the Shift-Enter -> prev
// behaviour. Enter then Shift-Enter from the first match should leave
// us back at the first match.
func TestHandleFindKey_ShiftEnterGoesBack(t *testing.T) {
	a := seedFindApp(t, "foo\nfoo\nfoo")
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	findBarOf(t, a).handleKey(keyEv(tcell.KeyEnter, 0))
	// Shift+Enter — keyEv default is ModNone, so build it directly.
	findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))
	if a.activeTabPtr().FindIndex != 0 {
		t.Fatalf("Shift-Enter should walk back, got idx=%d", a.activeTabPtr().FindIndex)
	}
}

// TestHandleFindKey_EscClearsHighlights pins the close gesture: Esc
// closes the bar AND wipes the tab's match list so the highlights
// disappear with the UI. Leaving them painted after the bar closes is
// the kind of "did anything happen?" surprise we want to avoid.
func TestHandleFindKey_EscClearsHighlights(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	findBarOf(t, a).handleKey(keyEv(tcell.KeyEsc, 0))
	if a.findBarOpen() {
		t.Fatal("Esc should close the find bar")
	}
	tab := a.activeTabPtr()
	if tab.FindQuery != "" || tab.FindMatches != nil || tab.FindIndex != -1 {
		t.Fatalf("Esc should clear all find state, got %+v", tab)
	}
}

// TestHandleFindKey_BackspaceLiveUpdates removes a character from the
// input and confirms matches re-resolve. Without this, deleting the
// query would leave stale highlights painted in the editor.
func TestHandleFindKey_BackspaceLiveUpdates(t *testing.T) {
	a := seedFindApp(t, "foo bar foox")
	a.openFind()
	for _, r := range "foox" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	if len(tab.FindMatches) != 1 {
		t.Fatalf("setup expected 1 match for 'foox', got %d", len(tab.FindMatches))
	}
	findBarOf(t, a).handleKey(keyEv(tcell.KeyBackspace, 0))
	if len(tab.FindMatches) != 2 {
		t.Fatalf("after backspace should match 'foo' (2x), got %d", len(tab.FindMatches))
	}
}

// TestEditorRect_ShrinksWhenFindOpen pins down the layout contract: the
// editor body is one row shorter while the find bar is up. Without this
// the bar would paint over the bottom row of code.
func TestEditorRect_ShrinksWhenFindOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	_, _, _, hClosed := a.editorRect()
	a.strip = &findStrip{a: a}
	_, _, _, hOpen := a.editorRect()
	if hOpen != hClosed-findBarHeight {
		t.Fatalf("editor height didn't shrink: closed=%d open=%d", hClosed, hOpen)
	}
}

// TestHasFindable_ImageTabIsFalse keeps the menu's Find row disabled on
// image tabs — there's nothing to search inside an image.
func TestHasFindable_ImageTabIsFalse(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasFindable() {
		t.Fatal("no tab should not be findable")
	}
}

// TestCounterText_Variants pins the three rendered states of the
// counter so a future refactor can't quietly drop "no results" or the
// blank no-query state.
func TestCounterText_Variants(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	if got := findBarOf(t, a).counterText(); got != "" {
		t.Fatalf("empty input should yield blank counter, got %q", got)
	}
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	if got := findBarOf(t, a).counterText(); got != "1 of 2" {
		t.Fatalf("counter for 2 matches should be '1 of 2', got %q", got)
	}
	for _, r := range "zzz" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	if got := findBarOf(t, a).counterText(); got != "no results" {
		t.Fatalf("zero hits should yield 'no results', got %q", got)
	}
}

// TestCloseAllModals_ClosesFindBar guards against a regression where
// opening another modal could leave the find bar focused underneath.
func TestCloseAllModals_ClosesFindBar(t *testing.T) {
	a := seedFindApp(t, "foo")
	a.openFind()
	a.closeAllModals()
	if a.findBarOpen() {
		t.Fatal("closeAllModals should close the find bar")
	}
}

// TestFindBarReplaceFlow pins the Tab-into-replace gesture: Tab opens
// the replace field and focuses it, typed runes land there, Enter
// replaces the current match and advances, Shift+Enter replaces all,
// and Esc tears the whole bar down.
func TestFindBarReplaceFlow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.txt")
	if err := os.WriteFile(path, []byte("foo bar foo\nfoo\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tab := a.activeTabPtr()
	if len(tab.FindMatches) != 3 {
		t.Fatalf("seed matches: %d", len(tab.FindMatches))
	}

	findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if !findBarOf(t, a).replaceOpen || !findBarOf(t, a).focusReplace {
		t.Fatal("Tab should open and focus the replace field")
	}
	for _, r := range "qux" {
		findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	if findBarOf(t, a).replace.Text() != "qux" || findBarOf(t, a).query.Text() != "foo" {
		t.Fatalf("typing went to the wrong field: %q / %q", findBarOf(t, a).replace.Text(), findBarOf(t, a).query.Text())
	}

	findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tab.Buffer.Lines[0] != "qux bar foo" {
		t.Fatalf("replace current: %q", tab.Buffer.Lines[0])
	}
	findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))
	if tab.Buffer.String() != "qux bar qux\nqux\n" {
		t.Fatalf("replace all: %q", tab.Buffer.String())
	}

	// Esc empties the slot, and the replace row's state goes with the
	// strip that held it — there is nothing left to leak into the next
	// Esc-f.
	findBarOf(t, a).handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if a.findBarOpen() {
		t.Fatal("Esc should tear down the whole bar")
	}
}

// TestDrawFindBar_ScrollsReplaceFieldToCaret pins the replace field's
// horizontal scroll window: a replacement longer than the field used to
// draw from index 0 forever, so the caret (and every new keystroke)
// landed past the field edge — invisible. The window must slide right
// with the caret and back left when the caret returns home.
func TestDrawFindBar_ScrollsReplaceFieldToCaret(t *testing.T) {
	a := seedFindApp(t, "hello world\n")
	a.openFind()
	findBarOf(t, a).handleKey(keyEv(tcell.KeyTab, 0)) // grow + focus the replace field
	for _, r := range strings.Repeat("r", 80) {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}

	findBarOf(t, a).draw(a.stripRect())
	if findBarOf(t, a).replace.Scroll() == 0 {
		t.Fatal("replace field did not scroll; caret sits off-field")
	}

	findBarOf(t, a).handleKey(keyEv(tcell.KeyHome, 0))
	findBarOf(t, a).draw(a.stripRect())
	if findBarOf(t, a).replace.Scroll() != 0 {
		t.Fatalf("scroll should follow the caret home, got %d", findBarOf(t, a).replace.Scroll())
	}
}

// TestDrawFindBar_ScrollsQueryFieldToCaret is the query field's twin: a
// query wider than the bar has to slide its own window so the caret (and
// the tail the user is typing) stay on screen. Both fields ride the same
// overlay.Field, and this is the test that says so for the query.
func TestDrawFindBar_ScrollsQueryFieldToCaret(t *testing.T) {
	a := seedFindApp(t, "hello world\n")
	a.openFind()
	for _, r := range strings.Repeat("q", 200) {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}

	findBarOf(t, a).draw(a.stripRect())
	a.screen.Show()
	if findBarOf(t, a).query.Scroll() == 0 {
		t.Fatal("query field did not scroll; the caret sits off-bar")
	}
	// The caret must land inside the bar, not past its right edge.
	bar := a.stripRect()
	by, bw, bx := bar.y, bar.w, bar.x
	cx, cy, visible := a.screen.(tcell.SimulationScreen).GetCursor()
	if !visible || cy != by || cx < bx || cx >= bx+bw {
		t.Fatalf("caret (%d,%d) visible=%v is outside the find bar row %d, x in [%d,%d)",
			cx, cy, visible, by, bx, bx+bw)
	}

	findBarOf(t, a).handleKey(keyEv(tcell.KeyHome, 0))
	findBarOf(t, a).draw(a.stripRect())
	if findBarOf(t, a).query.Scroll() != 0 {
		t.Fatalf("scroll should follow the caret home, got %d", findBarOf(t, a).query.Scroll())
	}
}

// runeIndexOf returns the screen column of s inside a rendered row, or
// -1. Rows are indexed in runes, not bytes: the sidebar's box-drawing
// glyphs sit ahead of the bar, so a byte index would land short.
func runeIndexOf(row []rune, s string) int {
	want := []rune(s)
	for i := 0; i+len(want) <= len(row); i++ {
		if string(row[i:i+len(want)]) == s {
			return i
		}
	}
	return -1
}

// TestBarLabelsThatFit pins the find bars' right-hand priority order in
// one place: the counter is offered the space before the hint, and
// neither is offered any until the input has its minimum. The two bars
// used to carry two copies of a check that measured each label against
// the label on the left and never against the other one.
func TestBarLabelsThatFit(t *testing.T) {
	cases := []struct {
		name                  string
		spare, counter, hint  int
		wantCounter, wantHint bool
	}{
		{"both fit", 40, 10, 20, true, true},
		{"only the counter fits", 25, 10, 20, true, false},
		{"counter wins the last cells", 10, 10, 20, true, false},
		{"nothing fits", 5, 10, 20, false, false},
		{"no counter to place leaves the hint the room", 20, 0, 20, false, true},
		{"a negative spare places nothing", -3, 10, 20, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCounter, gotHint := barLabelsThatFit(c.spare, c.counter, c.hint)
			if gotCounter != c.wantCounter || gotHint != c.wantHint {
				t.Fatalf("barLabelsThatFit(%d, %d, %d) = (%v, %v), want (%v, %v)",
					c.spare, c.counter, c.hint, gotCounter, gotHint, c.wantCounter, c.wantHint)
			}
		})
	}
}

// TestDrawFindBar_InputSurvivesAWidthThatFitsBothLabels pins the
// priority the bar's doc comment promises, at the band of widths that
// used to break it. The two fit checks each measured the label against
// their own text and never against each other, so both the counter and
// the hint were drawn on a bar with no room for them; the input was
// handed the leftovers, which is to say the cells they were already
// painted in. Now the labels yield: the input keeps minFieldWidth cells
// and the caret stays inside them.
func TestDrawFindBar_InputSurvivesAWidthThatFitsBothLabels(t *testing.T) {
	a := seedFindApp(t, "hello\n")
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(109, 24)
	a.width, a.height = 109, 24
	a.openFind()
	query := strings.Repeat("z", minFieldWidth-1) // no matches: counter reads "no results"
	for _, r := range query {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}

	a.draw()
	scr.Show()

	by := a.stripRect().y
	row := []rune(screenLine(scr, by))
	at := runeIndexOf(row, query)
	if at < 0 {
		t.Fatalf("the input lost its cells: bar row = %q", string(row))
	}
	cx, cy, visible := scr.GetCursor()
	if !visible || cy != by || cx != at+len(query) {
		t.Fatalf("caret (%d,%d) visible=%v, want it at (%d,%d) just after the query",
			cx, cy, visible, at+len(query), by)
	}
}

// TestDrawFindBar_HidesTheCaretWhenTheBarHasNoRoomAtAll pins the last
// resort. When even a minimum field will not fit, the bar paints no
// field — and Field.Draw owns the frame's only ShowCursor call, so
// returning without it would leave the hardware cursor wherever the
// editor's own render parked it: a caret blinking over static text,
// pointing at a buffer that does not have the keyboard.
func TestDrawFindBar_HidesTheCaretWhenTheBarHasNoRoomAtAll(t *testing.T) {
	a := seedFindApp(t, "hello\n")
	scr := a.screen.(tcell.SimulationScreen)
	scr.SetSize(60, 24)
	a.width, a.height = 60, 24
	// A sidebar dragged this wide leaves the bar too narrow for even the
	// "Find:" label plus one cell of input, while the editor beside it
	// is still wide enough to render — and to park the hardware cursor
	// on its own caret, which is the cursor that must not survive.
	a.sidebarShown, a.sidebarWidth = true, 52
	a.openFind()

	a.draw()
	scr.Show()

	if cx, cy, visible := scr.GetCursor(); visible {
		t.Fatalf("a bar with no field left a caret at (%d,%d); it must be hidden", cx, cy)
	}
}

// TestHandleFindKey_CaretMoveDoesNotReSearch pins the "react to the edit,
// not to the keystroke" rule the delegation to overlay.Field is built on:
// Left/Right/Home/End change the caret, not the query, so they must not
// re-run the search and yank the editor back onto the current match.
func TestHandleFindKey_CaretMoveDoesNotReSearch(t *testing.T) {
	a := seedFindApp(t, "foo\nfoo\nfoo")
	a.openFind()
	for _, r := range "foo" {
		findBarOf(t, a).handleKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	findBarOf(t, a).handleKey(keyEv(tcell.KeyEnter, 0)) // advance to the second match
	if tab.FindIndex != 1 {
		t.Fatalf("setup: expected FindIndex=1, got %d", tab.FindIndex)
	}

	findBarOf(t, a).handleKey(keyEv(tcell.KeyHome, 0))
	findBarOf(t, a).handleKey(keyEv(tcell.KeyRight, 0))
	if tab.FindIndex != 1 {
		t.Fatalf("a caret move re-ran the search: FindIndex=%d", tab.FindIndex)
	}
	if findBarOf(t, a).query.Cursor != 1 {
		t.Fatalf("caret should have moved inside the query, cursor=%d", findBarOf(t, a).query.Cursor)
	}
}
