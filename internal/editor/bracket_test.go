// =============================================================================
// File: internal/editor/bracket_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// TestMatchBracketAt_OnAndAfterTheBracket pins the caret rule: a caret
// sitting ON a bracket and a caret sitting immediately AFTER one both
// resolve to the same pair. The second case is the one that matters in
// practice — it is where you are the instant you finish typing ")".
func TestMatchBracketAt_OnAndAfterTheBracket(t *testing.T) {
	buf := NewBuffer("a(b)c")

	onOpen := MatchBracketAt(buf, Position{Line: 0, Col: 1})
	if !onOpen.Found || !onOpen.Matched {
		t.Fatalf("caret on '(': %+v", onOpen)
	}
	if onOpen.At != (Position{Line: 0, Col: 1}) || onOpen.Match != (Position{Line: 0, Col: 3}) {
		t.Fatalf("caret on '(': at=%v match=%v", onOpen.At, onOpen.Match)
	}

	afterClose := MatchBracketAt(buf, Position{Line: 0, Col: 4})
	if !afterClose.Found || !afterClose.Matched {
		t.Fatalf("caret after ')': %+v", afterClose)
	}
	if afterClose.At != (Position{Line: 0, Col: 3}) || afterClose.Match != (Position{Line: 0, Col: 1}) {
		t.Fatalf("caret after ')': at=%v match=%v", afterClose.At, afterClose.Match)
	}

	// Caret between two brackets: the rune under the caret wins, and here
	// both rules name the same pair anyway.
	between := MatchBracketAt(NewBuffer("()"), Position{Line: 0, Col: 1})
	if between.At != (Position{Line: 0, Col: 1}) || between.Match != (Position{Line: 0, Col: 0}) {
		t.Fatalf("caret between: at=%v match=%v", between.At, between.Match)
	}
}

// TestMatchBracketAt_RespectsNesting walks past inner pairs of the same
// type instead of stopping at the first closer it sees, in both scan
// directions and across lines.
func TestMatchBracketAt_RespectsNesting(t *testing.T) {
	buf := NewBuffer("func f() {\n\tif x {\n\t\ty()\n\t}\n}")
	//                 line 0 col 9 = outer '{'; line 4 col 0 = its '}'

	forward := MatchBracketAt(buf, Position{Line: 0, Col: 9})
	if !forward.Matched || forward.Match != (Position{Line: 4, Col: 0}) {
		t.Fatalf("outer brace forward: %+v", forward)
	}

	backward := MatchBracketAt(buf, Position{Line: 4, Col: 0})
	if !backward.Matched || backward.Match != (Position{Line: 0, Col: 9}) {
		t.Fatalf("outer brace backward: %+v", backward)
	}

	inner := MatchBracketAt(buf, Position{Line: 1, Col: 6})
	if !inner.Matched || inner.Match != (Position{Line: 3, Col: 1}) {
		t.Fatalf("inner brace: %+v", inner)
	}
}

// TestMatchBracketAt_IgnoresOtherPairTypes documents the forgiving rule:
// only the same pair type nests, so a stray bracket between two braces
// does not turn the braces into a mismatch. Half-written code is normal.
func TestMatchBracketAt_IgnoresOtherPairTypes(t *testing.T) {
	m := MatchBracketAt(NewBuffer("{ [ }"), Position{Line: 0, Col: 0})
	if !m.Matched || m.Match != (Position{Line: 0, Col: 4}) {
		t.Fatalf("brace should still find its brace: %+v", m)
	}
}

// TestMatchBracketAt_Unmatched separates the three answers the matcher can
// give. An unmatched bracket is Found (so the renderer can flag it) but not
// Matched; a caret that is not on a bracket at all is neither.
func TestMatchBracketAt_Unmatched(t *testing.T) {
	open := MatchBracketAt(NewBuffer("foo(bar"), Position{Line: 0, Col: 3})
	if !open.Found || open.Matched {
		t.Fatalf("unmatched '(': %+v", open)
	}
	if open.At != (Position{Line: 0, Col: 3}) {
		t.Fatalf("unmatched '(' at = %v", open.At)
	}

	closing := MatchBracketAt(NewBuffer("foo)bar"), Position{Line: 0, Col: 3})
	if !closing.Found || closing.Matched {
		t.Fatalf("unmatched ')': %+v", closing)
	}

	for _, col := range []int{0, 2, 7} {
		if m := MatchBracketAt(NewBuffer("foo bar"), Position{Line: 0, Col: col}); m.Found {
			t.Fatalf("col %d is not a bracket but reported %+v", col, m)
		}
	}
}

// TestMatchBracketAt_OutOfRangeInputs keeps the matcher total: a nil buffer
// or a position off the end of the buffer answers "nothing here" instead of
// panicking. Render calls this on every frame with whatever the cursor is.
func TestMatchBracketAt_OutOfRangeInputs(t *testing.T) {
	if m := MatchBracketAt(nil, Position{}); m.Found {
		t.Fatalf("nil buffer reported %+v", m)
	}
	buf := NewBuffer("(x)")
	for _, p := range []Position{{Line: -1}, {Line: 9}, {Line: 0, Col: -5}, {Line: 0, Col: 99}} {
		if m := MatchBracketAt(buf, p); m.Found {
			t.Fatalf("position %v reported %+v", p, m)
		}
	}
}

// TestMatchBracketAt_GivesUpPastBudget checks the third answer: past
// bracketScanLimit the matcher reports nothing rather than claiming the
// bracket is unmatched, because painting a balanced brace as an error
// would be a lie the user cannot investigate.
func TestMatchBracketAt_GivesUpPastBudget(t *testing.T) {
	buf := &Buffer{Lines: []string{"(", strings.Repeat("x", bracketScanLimit+10)}}
	if m := MatchBracketAt(buf, Position{Line: 0, Col: 0}); m.Found {
		t.Fatalf("scan past the budget must report nothing, got %+v", m)
	}

	// The same brace with its partner inside the budget resolves normally.
	near := &Buffer{Lines: []string{"(", strings.Repeat("x", 10), ")"}}
	if m := MatchBracketAt(near, Position{Line: 0, Col: 0}); !m.Matched {
		t.Fatalf("partner inside the budget should match, got %+v", m)
	}
}

// TestGoToMatchingBracket jumps the caret onto the partner and reports
// whether it did, so the app can flash when there is nowhere to go. The
// jump also has to be reversible — landing on the partner must re-resolve
// the same pair from the other end.
func TestGoToMatchingBracket(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("if (a) {\n\tb()\n}")}
	tab.Cursor = Position{Line: 0, Col: 7} // the '{'
	tab.Anchor = tab.Cursor

	if !tab.GoToMatchingBracket() {
		t.Fatal("jump reported no match for a balanced brace")
	}
	if want := (Position{Line: 2, Col: 0}); tab.Cursor != want {
		t.Fatalf("cursor = %v, want %v", tab.Cursor, want)
	}
	if tab.HasSelection() {
		t.Fatal("a jump should collapse the selection, not extend it")
	}

	if !tab.GoToMatchingBracket() {
		t.Fatal("jump back reported no match")
	}
	if want := (Position{Line: 0, Col: 7}); tab.Cursor != want {
		t.Fatalf("round trip landed at %v, want %v", tab.Cursor, want)
	}

	// Nothing under the caret: no move, no claim of success.
	tab.Cursor = Position{Line: 1, Col: 1}
	tab.Anchor = tab.Cursor
	if tab.GoToMatchingBracket() {
		t.Fatal("jump claimed success with no bracket under the caret")
	}
	if tab.Cursor != (Position{Line: 1, Col: 1}) {
		t.Fatalf("failed jump moved the caret to %v", tab.Cursor)
	}
}

// TestHasMatchingBracket is the menu row's enable predicate: true only when
// the jump would actually go somewhere.
func TestHasMatchingBracket(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a[b]c")}
	tab.Cursor = Position{Line: 0, Col: 1}
	if !tab.HasMatchingBracket() {
		t.Fatal("balanced bracket under the caret reported no match")
	}
	tab.Cursor = Position{Line: 0, Col: 0}
	if tab.HasMatchingBracket() {
		t.Fatal("plain rune under the caret reported a match")
	}

	unmatched := &Tab{Buffer: NewBuffer("a[b")}
	unmatched.Cursor = Position{Line: 0, Col: 1}
	if unmatched.HasMatchingBracket() {
		t.Fatal("unmatched bracket must not enable the jump")
	}

	img := &Tab{Buffer: NewBuffer("a[b]c"), Mode: imageMode}
	img.Cursor = Position{Line: 0, Col: 1}
	if img.HasMatchingBracket() || img.GoToMatchingBracket() {
		t.Fatal("image tabs have no caret to match brackets with")
	}
}

// TestRefreshBracketMatch_CacheFollowsCursorAndEdits guards the cache that
// keeps cellStyle cheap: it must recompute when the caret moves AND when
// the buffer changes underneath a stationary caret, or the renderer paints
// a marker on a bracket that is no longer there.
func TestRefreshBracketMatch_CacheFollowsCursorAndEdits(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("(a)b")}
	tab.initUndo()

	tab.Cursor = Position{Line: 0, Col: 0}
	tab.refreshBracketMatch()
	if !tab.bracket.Matched {
		t.Fatalf("initial: %+v", tab.bracket)
	}

	// Caret moves off the bracket.
	tab.Cursor = Position{Line: 0, Col: 4}
	tab.refreshBracketMatch()
	if tab.bracket.Found {
		t.Fatalf("cache kept a stale pair after the caret moved: %+v", tab.bracket)
	}

	// Buffer changes under a caret that does not move: deleting the ')'
	// leaves the '(' unmatched, and StyleStale is how the cache hears
	// about it.
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.refreshBracketMatch()
	if !tab.bracket.Matched {
		t.Fatal("pre-edit sanity: expected a match")
	}
	tab.Buffer.Lines[0] = "(ab"
	tab.StyleStale = true
	tab.refreshBracketMatch()
	if !tab.bracket.Found || tab.bracket.Matched {
		t.Fatalf("cache missed the edit: %+v", tab.bracket)
	}
}

// TestTab_Render_MarksBracketPair is the end-to-end check that the marker
// reaches the screen: both halves of a matched pair take the accent color
// with bold + underline (attributes so the marker survives on a low-color
// terminal), and the runes between them are untouched. Both render paths
// are checked — wrap mode paints its own body and would silently miss the
// marker if it ever stopped going through cellStyle.
func TestTab_Render_MarksBracketPair(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab := &Tab{Buffer: NewBuffer("a(bb)c"), StyleStale: true}
	tab.initUndo()
	tab.Cursor = Position{Line: 0, Col: 1} // on the '('
	tab.Anchor = tab.Cursor

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()
	for _, col := range []int{1, 4} { // '(' and ')'
		fg, _, attrs := cells[0*w+contentX+col].Style.Decompose()
		if fg != th.Accent {
			t.Errorf("col %d fg = %v, want Accent %v", col, fg, th.Accent)
		}
		if attrs&tcell.AttrBold == 0 || attrs&tcell.AttrUnderline == 0 {
			t.Errorf("col %d attrs = %v, want bold+underline", col, attrs)
		}
	}
	if _, _, attrs := cells[0*w+contentX+2].Style.Decompose(); attrs&tcell.AttrUnderline != 0 {
		t.Error("the rune inside the pair should not be marked")
	}
}

// TestTab_Render_MarksBracketPairWrapped is the same assertion against the
// soft-wrap body, which draws its own rows.
func TestTab_Render_MarksBracketPairWrapped(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab := &Tab{Buffer: NewBuffer("a(bb)c"), StyleStale: true, Wrap: true}
	tab.initUndo()
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()
	for _, col := range []int{1, 4} {
		fg, _, attrs := cells[0*w+contentX+col].Style.Decompose()
		if fg != th.Accent || attrs&tcell.AttrUnderline == 0 {
			t.Errorf("wrapped col %d: fg=%v attrs=%v, want Accent + underline", col, fg, attrs)
		}
	}
}

// TestTab_Render_MarksUnmatchedBracket documents the decision that an
// unmatched bracket renders distinctly rather than silently: the single
// cell under the caret takes the error color.
func TestTab_Render_MarksUnmatchedBracket(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab := &Tab{Buffer: NewBuffer("a(bb"), StyleStale: true}
	tab.initUndo()
	tab.Cursor = Position{Line: 0, Col: 2} // just after the '('
	tab.Anchor = tab.Cursor

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()
	if fg, _, _ := cells[0*w+contentX+1].Style.Decompose(); fg != th.Error {
		t.Fatalf("unmatched '(' fg = %v, want Error %v", fg, th.Error)
	}
}

// TestBracketCellStyle_KeepsBackground checks that the marker only ever
// changes foreground and attributes, so it composes with the cursor-line
// tint and with a selection instead of punching a hole in either.
func TestBracketCellStyle_KeepsBackground(t *testing.T) {
	th := theme.Default()
	base := tcell.StyleDefault.Background(th.Selection).Foreground(th.Text)
	for _, matched := range []bool{true, false} {
		_, bg, _ := bracketCellStyle(th, base, matched).Decompose()
		if bg != th.Selection {
			t.Fatalf("matched=%v changed the background to %v", matched, bg)
		}
	}
}
