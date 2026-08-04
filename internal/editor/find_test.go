// =============================================================================
// File: internal/editor/find_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"reflect"
	"strings"
	"testing"
)

// TestFindAll_BasicMatches walks across multiple lines and pins down the
// document-order ordering plus the rune-indexed Col / Width fields.
func TestFindAll_BasicMatches(t *testing.T) {
	buf := NewBuffer("foo bar foo\nbaz foo\n")
	got := FindAll(buf, "foo")
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 8, Width: 3},
		{Line: 1, Col: 4, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindAll mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindAll_SmartCaseLowerQuery proves an all-lowercase query still
// matches any case in the buffer. Most queries are typed lowercase and
// the forgiving "type to find" loop is the whole point of the bar.
func TestFindAll_SmartCaseLowerQuery(t *testing.T) {
	buf := NewBuffer("Foo FOO foO")
	got := FindAll(buf, "foo")
	if len(got) != 3 {
		t.Fatalf("expected 3 case-insensitive matches, got %d: %v", len(got), got)
	}
}

// TestFindAll_SmartCaseUpperQuery pins the other half of smart case: a
// single uppercase letter makes the query exact, so hunting a symbol
// like ID doesn't drag in every id. Same rule internal/search applies to
// the project panel, so the two search surfaces never disagree.
func TestFindAll_SmartCaseUpperQuery(t *testing.T) {
	buf := NewBuffer("id ID Id iD")
	got := FindAll(buf, "ID")
	if len(got) != 1 {
		t.Fatalf("expected only the exact ID, got %d: %v", len(got), got)
	}
	if got[0].Col != 3 {
		t.Fatalf("matched the wrong occurrence: %+v", got[0])
	}
	if all := FindAll(buf, "id"); len(all) != 4 {
		t.Fatalf("lowercase query should match all four, got %d: %v", len(all), all)
	}
}

// TestFindAll_EmptyQuery returns nil so the UI can render an empty
// state without a special "0 of 0" branch.
func TestFindAll_EmptyQuery(t *testing.T) {
	buf := NewBuffer("anything")
	if got := FindAll(buf, ""); got != nil {
		t.Fatalf("empty query should return nil, got %v", got)
	}
}

// TestFindAll_NonOverlapping pins down the scanner's advance-past-match
// behaviour. "aaa" in "aaaaaa" should yield two non-overlapping hits,
// matching VS Code's default search semantics.
func TestFindAll_NonOverlapping(t *testing.T) {
	buf := NewBuffer("aaaaaa")
	got := FindAll(buf, "aaa")
	want := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 0, Col: 3, Width: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected non-overlapping matches, got %v", got)
	}
}

// TestFindAll_MultiByteRunes pins down the rune-indexed column
// convention. The buffer contains a 3-byte UTF-8 character before the
// match — Col must report 1 (one rune in), not 3 (three bytes in).
func TestFindAll_MultiByteRunes(t *testing.T) {
	buf := NewBuffer("✓foo")
	got := FindAll(buf, "foo")
	want := []Match{{Line: 0, Col: 1, Width: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-byte handling wrong, got %v", got)
	}
}

// TestFindAll_NilBuffer is the defensive guard — callers may hold a
// freshly-zeroed Tab during construction. Returning nil rather than
// panicking lets the UI cope without an explicit nil check.
func TestFindAll_NilBuffer(t *testing.T) {
	if got := FindAll(nil, "x"); got != nil {
		t.Fatalf("nil buffer should return nil, got %v", got)
	}
}

// TestFirstMatchAtOrAfter_BasicForward finds the first match at or
// after the cursor, which is what we want when a user types a query
// in the bar — we shouldn't snap them backwards past where they were
// already looking.
func TestFirstMatchAtOrAfter_BasicForward(t *testing.T) {
	matches := []Match{
		{Line: 0, Col: 0, Width: 3},
		{Line: 1, Col: 4, Width: 3},
		{Line: 2, Col: 0, Width: 3},
	}
	idx := FirstMatchAtOrAfter(matches, Position{Line: 1, Col: 0})
	if idx != 1 {
		t.Fatalf("expected idx=1 (line 1 match), got %d", idx)
	}
}

// TestFirstMatchAtOrAfter_WrapsToTop covers the case where the cursor
// is past every match: we wrap to the top so the user can keep
// pressing Enter to cycle.
func TestFirstMatchAtOrAfter_WrapsToTop(t *testing.T) {
	matches := []Match{{Line: 0, Col: 0, Width: 3}}
	idx := FirstMatchAtOrAfter(matches, Position{Line: 99, Col: 0})
	if idx != 0 {
		t.Fatalf("expected wrap to idx=0, got %d", idx)
	}
}

// TestFirstMatchAtOrAfter_Empty is the no-matches case — return -1 so
// the caller can short-circuit without checking length again.
func TestFirstMatchAtOrAfter_Empty(t *testing.T) {
	if got := FirstMatchAtOrAfter(nil, Position{}); got != -1 {
		t.Fatalf("expected -1 for empty matches, got %d", got)
	}
}

// TestTab_SetFindQuery_PicksNearestMatch installs a query and pins the
// "land on the nearest hit, not always the first hit" contract: with the
// cursor on line 1, the index should point at the line-1 match, not the
// earlier line-0 one.
func TestTab_SetFindQuery_PicksNearestMatch(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.Cursor = Position{Line: 1, Col: 0}

	tab.SetFindQuery("foo")
	if got, want := tab.FindIndex, 1; got != want {
		t.Fatalf("FindIndex = %d, want %d (nearest to cursor)", got, want)
	}
}

// TestTab_SetFindQuery_EmptyClears proves an empty query clears every
// piece of find state. Closing the bar relies on this behaviour to wipe
// out the highlight band.
func TestTab_SetFindQuery_EmptyClears(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo")
	tab.SetFindQuery("foo")
	if tab.FindIndex < 0 {
		t.Fatal("setup expected a current match")
	}
	tab.SetFindQuery("")
	if tab.FindMatches != nil || tab.FindIndex != -1 || tab.FindQuery != "" {
		t.Fatalf("empty query should clear all find state, got %+v", tab)
	}
}

// TestTab_FindNext_WrapsAndMovesCursor exercises the Enter-in-the-bar
// path. After three Next presses we should land on match 0 again (wrap)
// with the cursor on top of it.
func TestTab_FindNext_WrapsAndMovesCursor(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.SetFindQuery("foo") // FindIndex = 0
	tab.FindNext()          // -> 1
	tab.FindNext()          // -> 2
	tab.FindNext()          // -> 0 (wrap)
	if tab.FindIndex != 0 {
		t.Fatalf("expected wrap to 0, got %d", tab.FindIndex)
	}
	if tab.Cursor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("cursor should follow the active match, got %+v", tab.Cursor)
	}
}

// TestTab_FindPrev_WrapsBackwards is the Shift-Enter equivalent — from
// the first match, Prev wraps to the last.
func TestTab_FindPrev_WrapsBackwards(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo\nfoo\nfoo")
	tab.SetFindQuery("foo")
	tab.FindPrev()
	if tab.FindIndex != 2 {
		t.Fatalf("expected wrap to last (2), got %d", tab.FindIndex)
	}
}

// TestTab_FindNext_NoMatchesIsSafe pins the contract that Find ops are
// no-ops when there's nothing to find. Without this, a stray hotkey on
// an empty result set would crash.
func TestTab_FindNext_NoMatchesIsSafe(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world")
	tab.SetFindQuery("zzz")
	tab.FindNext() // must not panic
	tab.FindPrev() // must not panic
	if tab.FindIndex != -1 {
		t.Fatalf("FindIndex should stay -1 with no matches, got %d", tab.FindIndex)
	}
}

// TestTab_ClearFind wipes everything so the renderer stops highlighting.
func TestTab_ClearFind(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo")
	tab.SetFindQuery("foo")
	tab.ClearFind()
	if tab.FindQuery != "" || tab.FindMatches != nil || tab.FindIndex != -1 {
		t.Fatalf("ClearFind left residue: %+v", tab)
	}
}

// TestMatchAtRune_HitAndMiss proves the per-cell renderer probe finds
// the right match index for cells inside a hit and -1 outside.
func TestMatchAtRune_HitAndMiss(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("foo bar foo")
	tab.SetFindQuery("foo") // matches at (0,0) and (0,8)

	if got := tab.matchAtRune(0, 1); got != 0 {
		t.Fatalf("col 1 should be inside match 0, got %d", got)
	}
	if got := tab.matchAtRune(0, 4); got != -1 {
		t.Fatalf("col 4 (the space) should miss, got %d", got)
	}
	if got := tab.matchAtRune(0, 9); got != 1 {
		t.Fatalf("col 9 should be inside match 1, got %d", got)
	}
}

// linearMatchAtRune is the pre-index implementation of matchAtRune: the
// straight scan that returns the first match covering the cell. It is
// kept here purely as the oracle the indexed version is checked against.
func linearMatchAtRune(matches []Match, line, col int) int {
	for i, m := range matches {
		if m.Line != line {
			continue
		}
		if col >= m.Col && col < m.Col+m.Width {
			return i
		}
	}
	return -1
}

// TestMatchAtRune_MatchesLinearScan is the equivalence proof for turning
// the per-cell probe from an O(matches) scan into a per-line index plus a
// binary search. Every cell of every line — including the ones between,
// before, and after hits — has to resolve to the same match index the
// old scan returned, for adjacent hits, hits that touch the line's edges,
// and hand-built overlapping spans the scanner itself never emits.
func TestMatchAtRune_MatchesLinearScan(t *testing.T) {
	cases := []struct {
		name    string
		lines   string
		matches []Match
	}{
		{
			name:    "adjacent hits",
			lines:   "aaaaaa\nbb aa",
			matches: []Match{{0, 0, 2}, {0, 2, 2}, {0, 4, 2}, {1, 3, 2}},
		},
		{
			name:    "overlapping spans",
			lines:   "abcabcabc",
			matches: []Match{{0, 0, 4}, {0, 2, 4}, {0, 3, 6}},
		},
		{
			name:    "hit at both edges",
			lines:   "xyz\nlonger line here",
			matches: []Match{{0, 0, 1}, {0, 2, 1}, {1, 0, 6}, {1, 12, 4}},
		},
		{
			name:    "no matches on some lines",
			lines:   "one\ntwo\nthree",
			matches: []Match{{1, 1, 2}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := &Tab{Buffer: NewBuffer(tc.lines)}
			tab.FindMatches = tc.matches
			for line := -1; line <= tab.Buffer.LineCount(); line++ {
				for col := -1; col <= 20; col++ {
					want := linearMatchAtRune(tc.matches, line, col)
					if got := tab.matchAtRune(line, col); got != want {
						t.Fatalf("matchAtRune(%d, %d) = %d, linear scan says %d",
							line, col, got, want)
					}
				}
			}
		})
	}
}

// TestMatchAtRune_IndexRebuildsAfterQueryChange guards the cached index's
// freshness. The renderer probes thousands of cells between edits, so the
// index is built lazily and dropped by the writers of FindMatches — if a
// re-query left the old index in place the highlight would paint the
// previous query's hits.
func TestMatchAtRune_IndexRebuildsAfterQueryChange(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("foo bar foo")}
	tab.SetFindQuery("foo")
	if got := tab.matchAtRune(0, 0); got != 0 { // builds the index
		t.Fatalf("seed probe = %d", got)
	}

	tab.SetFindQuery("bar")
	if got := tab.matchAtRune(0, 0); got != -1 {
		t.Fatalf("stale index still reports a hit at col 0: %d", got)
	}
	if got := tab.matchAtRune(0, 4); got != 0 {
		t.Fatalf("new query's hit not found at col 4: %d", got)
	}

	// A direct write to the exported field is caught by the recorded
	// match count, without any invalidation call. Document order is the
	// documented shape of the field, so the replacement respects it.
	tab.FindMatches = []Match{{Line: 0, Col: 0, Width: 3}, {Line: 0, Col: 8, Width: 3}}
	if got := tab.matchAtRune(0, 8); got != 1 {
		t.Fatalf("direct assignment not picked up: %d", got)
	}
	if got := tab.matchAtRune(0, 1); got != 0 {
		t.Fatalf("direct assignment lost the first hit: %d", got)
	}

	tab.ClearFind()
	if got := tab.matchAtRune(0, 0); got != -1 {
		t.Fatalf("ClearFind left the index live: %d", got)
	}
}

// TestReplaceCurrentMatch pins the single-replace contract: the match
// text swaps, the query re-runs (count shrinks), the walk stays on the
// next match forward, and one undo restores everything.
func TestReplaceCurrentMatch(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("foo bar foo\nfoo end")}
	tab.initUndo()
	tab.SetFindQuery("foo")
	if len(tab.FindMatches) != 3 {
		t.Fatalf("seed: %d matches", len(tab.FindMatches))
	}
	if !tab.ReplaceCurrentMatch("qux") {
		t.Fatal("replace should succeed")
	}
	if tab.Buffer.Lines[0] != "qux bar foo" {
		t.Fatalf("line 0: %q", tab.Buffer.Lines[0])
	}
	if len(tab.FindMatches) != 2 {
		t.Fatalf("count after replace: %d", len(tab.FindMatches))
	}
	if !tab.Undo() || tab.Buffer.Lines[0] != "foo bar foo" {
		t.Fatalf("undo should restore, got %q", tab.Buffer.Lines[0])
	}
}

// TestReplaceAllMatches: every hit swaps in one undo step, including
// several on one line (applied right-to-left so spans stay valid).
func TestReplaceAllMatches(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("foo foo\nmid\nfoo")}
	tab.initUndo()
	tab.SetFindQuery("foo")
	if got := tab.ReplaceAllMatches("x"); got != 3 {
		t.Fatalf("replaced %d, want 3", got)
	}
	if tab.Buffer.String() != "x x\nmid\nx" {
		t.Fatalf("buffer: %q", tab.Buffer.String())
	}
	if len(tab.FindMatches) != 0 {
		t.Fatal("no matches should remain")
	}
	tab.Undo()
	if tab.Buffer.String() != "foo foo\nmid\nfoo" {
		t.Fatalf("one undo should restore all: %q", tab.Buffer.String())
	}
}

// TestReplaceLines pins the open-buffer path of project replace: whole
// lines swap in one undo step, out-of-range indexes are ignored, and
// the tab reports dirty.
func TestReplaceLines(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("one\ntwo\nthree")}
	tab.initUndo()
	n := tab.ReplaceLines(map[int]string{1: "TWO", 99: "ghost"})
	if n != 1 || tab.Buffer.Lines[1] != "TWO" || !tab.Dirty {
		t.Fatalf("swap: n=%d lines=%v dirty=%v", n, tab.Buffer.Lines, tab.Dirty)
	}
	tab.Undo()
	if tab.Buffer.Lines[1] != "two" {
		t.Fatalf("undo should restore: %v", tab.Buffer.Lines)
	}
}

// FuzzFindAll pins the span contract the find UI is built on. The renderer
// asks "is this cell inside a match?" once per painted cell and FindNext /
// FindPrev walk the slice assuming document order, so a match that
// overlaps its neighbour, points past the end of its line, or doesn't
// actually cover the query would paint garbage and jump the cursor
// nowhere. The completeness half matters just as much: if the lowered
// line contains the lowered query, at least one match must be reported.
func FuzzFindAll(f *testing.F) {
	seeds := []struct {
		text  string
		query string
	}{
		{"", ""},
		{"foo bar foo", "foo"},
		{"aaaa", "aa"},
		{"MiXeD case Line\nmixed CASE line", "mixed"},
		{"日本語テキスト\n日本語", "日本語"},
		{"e\u0301e\u0301e\u0301", "e\u0301"},
		{"line one\r\nline two\r\n", "\r"},
		{strings.Repeat("ab", 4096), "aba"},
		{"          ", " "},
		{"tab\tsep\ttab", "\t"},
		{"ünïcödé", "Ï"},
	}
	for _, s := range seeds {
		f.Add(s.text, s.query)
	}

	f.Fuzz(func(t *testing.T, text, query string) {
		// FindAll compares DECODED runes on both sides, so invalid UTF-8
		// has already collapsed to RuneError before any comparison
		// happens — "\x81" and "\xcc" are the same rune to the matcher.
		// Normalise the inputs into that domain so the assertions below
		// check the spans the matcher produced instead of re-litigating
		// the encoding. Match positions are unaffected: FindAll decodes
		// every line anyway.
		text = string([]rune(text))
		query = string([]rune(query))

		buf := NewBuffer(text)
		matches := FindAll(buf, query)

		if query == "" {
			if matches != nil {
				t.Fatalf("empty query must report no matches, got %v", matches)
			}
			return
		}

		// Smart case: an all-lowercase query folds the haystack, any
		// uppercase letter in it makes the comparison exact.
		caseSensitive := hasUpper(query)
		needle := query
		if !caseSensitive {
			needle = strings.ToLower(query)
		}
		wantWidth := len([]rune(needle))

		perLine := make(map[int]int, len(matches))
		prev := Match{Line: -1}
		for _, m := range matches {
			if m.Line < prev.Line || (m.Line == prev.Line && m.Col < prev.Col+prev.Width) {
				t.Fatalf("matches must ascend without overlapping: %+v then %+v", prev, m)
			}
			if m.Line < 0 || m.Line >= buf.LineCount() {
				t.Fatalf("match line %d outside buffer of %d lines", m.Line, buf.LineCount())
			}
			if m.Width != wantWidth {
				t.Fatalf("match width %d, want the query's %d runes", m.Width, wantWidth)
			}
			hay := []rune(buf.Lines[m.Line])
			if !caseSensitive {
				hay = []rune(strings.ToLower(buf.Lines[m.Line]))
			}
			if m.Col < 0 || m.Col+m.Width > len(hay) {
				t.Fatalf("match span [%d,%d) escapes the %d-rune line %d",
					m.Col, m.Col+m.Width, len(hay), m.Line)
			}
			if got := string(hay[m.Col : m.Col+m.Width]); got != needle {
				t.Fatalf("match at %d:%d covers %q, want %q", m.Line, m.Col, got, needle)
			}
			perLine[m.Line]++
			prev = m
		}

		// A line that visibly contains the query must produce a hit —
		// silently missing matches is the failure mode a "no panic" fuzz
		// target would never catch.
		for i, raw := range buf.Lines {
			line := raw
			if !caseSensitive {
				line = strings.ToLower(raw)
			}
			if strings.Contains(line, needle) && perLine[i] == 0 {
				t.Fatalf("line %d (%q) contains %q but reported no match", i, raw, query)
			}
		}
	})
}
