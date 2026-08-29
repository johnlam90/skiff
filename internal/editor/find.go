// =============================================================================
// File: internal/editor/find.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// find.go implements the editor's in-file search primitives. Matching is
// smart-case substring on rune-decoded lines: an all-lowercase query
// matches any case, a single uppercase letter makes it exact — the same
// rule internal/search applies to the project-wide panel, so the two
// search surfaces never disagree about what a query means. Decoding to
// runes keeps multi-byte characters at one column each (consistent with
// how the cursor / selection already treat columns elsewhere). Regex and
// whole-word toggles are intentionally out of scope for v1 — the 80/20
// here is the VS-Code-style "type and jump" loop, not power-user search.

package editor

import "unicode"

// Match describes one find hit. Line and Col follow the same rune-indexed
// convention as Position; Width is the rune count of the query so the
// renderer can paint the right number of cells without re-running the
// matcher.
type Match struct {
	Line  int
	Col   int
	Width int
}

// FindAll returns every substring match of query inside buf, in document
// order. Matching is smart-case: an all-lowercase query matches any
// case, any uppercase letter in the query makes the match exact — so
// "id" finds ID and id, while "ID" finds only ID. An empty query returns
// nil — the caller is expected to clear its UI rather than show "0 of 0"
// results. Matches do not overlap: after a hit the scanner advances past
// the matched run, so "aaaa" with query "aa" yields two matches at
// columns 0 and 2.
func FindAll(buf *Buffer, query string) []Match {
	if query == "" || buf == nil {
		return nil
	}
	caseSensitive := hasUpper(query)
	needle := []rune(query)
	if len(needle) == 0 {
		return nil
	}
	if !caseSensitive {
		lowerRunes(needle)
	}
	var out []Match
	for lineIdx, raw := range buf.Lines {
		// Decoded here rather than through Buffer.LineRunes: the fold
		// below writes in place and LineRunes hands out a shared slice.
		hay := []rune(raw)
		if !caseSensitive {
			lowerRunes(hay)
		}
		col := 0
		for col+len(needle) <= len(hay) {
			if runesEqual(hay[col:col+len(needle)], needle) {
				out = append(out, Match{Line: lineIdx, Col: col, Width: len(needle)})
				col += len(needle)
				continue
			}
			col++
		}
	}
	return out
}

// runesEqual returns true when two equal-length rune slices match
// element-for-element. Inlined so the hot inner loop of FindAll doesn't
// pay for a generic slices.Equal call (which exists in 1.21+ but pulls in
// the slices package).
func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lowerRunes case-folds a decoded line in place. Folding per rune rather
// than via strings.ToLower keeps the rune count — which is what Col and
// Width are measured in — identical to the unfolded line, and decodes
// the line once instead of once per case form.
func lowerRunes(rs []rune) {
	for i, r := range rs {
		rs[i] = unicode.ToLower(r)
	}
}

// hasUpper reports whether s contains an uppercase letter — the
// smart-case trigger. Deliberately the same predicate internal/search
// uses; duplicated rather than exported across the package boundary
// because it is six lines and the alternative is an import edge from the
// editor onto the project-search engine.
func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// FirstMatchAtOrAfter returns the index into matches of the first hit at
// or after cursor, or 0 when cursor sits past the last match (we wrap
// around to the top — that's what the user expects after typing a query
// at the bottom of a file).
//
// Returns -1 when matches is empty so callers can short-circuit without
// re-checking the length.
func FirstMatchAtOrAfter(matches []Match, cursor Position) int {
	if len(matches) == 0 {
		return -1
	}
	for i, m := range matches {
		if m.Line > cursor.Line || (m.Line == cursor.Line && m.Col >= cursor.Col) {
			return i
		}
	}
	// Cursor is past every match — wrap to the top.
	return 0
}

// MatchPosition returns the cursor-friendly Position at the start of m.
// Trivial helper, but it keeps callers from constructing Position literals
// by hand (which loses the "rune-indexed" intent at the call site).
func MatchPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col}
}

// MatchEndPosition returns the position one past the end of m — useful
// when the caller wants to set a selection that covers the match.
func MatchEndPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col + m.Width}
}

// SetFindQuery installs a new search query on the tab, recomputes the
// match list against the current buffer, and points FindIndex at the
// first match at or after the cursor (so the user lands on the nearest
// hit, not always the first hit in the file). An empty query clears all
// find state — symmetrical with closing the bar via Esc.
//
// The cursor is left where it is; SetFindQuery only updates state. It is
// the caller's job to call FocusCurrentMatch when they want the cursor
// to actually move (which is what happens on the first non-empty query
// and on every Enter / Shift-Enter press).
func (t *Tab) SetFindQuery(query string) {
	t.FindQuery = query
	t.findRows = nil // the per-line index belongs to the old match list
	if query == "" {
		t.FindMatches = nil
		t.FindIndex = -1
		return
	}
	t.FindMatches = FindAll(t.Buffer, query)
	t.FindIndex = FirstMatchAtOrAfter(t.FindMatches, t.Cursor)
}

// refreshFindMatches re-runs the active query after the buffer changed,
// so the highlights keep naming the text the user searched for instead
// of the offsets it happened to sit at when the query was typed. Every
// mutation reaches this through Tab.edit; matchAtRune's findRowsFor
// guard cannot stand in for it, because an edit ahead of a hit moves
// every span on the line without changing the match COUNT.
//
// The current-match index is preserved rather than re-derived from the
// caret: "3 of 7" must not renumber itself under a cursor that is only
// where it is because the user is typing. An edit that destroyed the
// current match falls through to the hit nearest the caret — the answer
// SetFindQuery already computed, which is -1 when nothing survived.
//
// An idle query costs nothing: closing the find bar calls ClearFind, so
// a tab with no search running never re-scans on a keystroke. A live one
// costs a full FindAll per edit, which is the same per-keystroke scan the
// find bar's own input already pays (App.findApplyQuery re-queries on
// every character typed into it) — a buffer keystroke is not the place to
// start being cheaper than the query field.
func (t *Tab) refreshFindMatches() {
	if t.FindQuery == "" {
		return
	}
	keep := t.FindIndex
	t.SetFindQuery(t.FindQuery)
	if keep >= len(t.FindMatches) {
		keep = len(t.FindMatches) - 1
	}
	if keep >= 0 {
		t.FindIndex = keep
	}
}

// FocusCurrentMatch moves the cursor (and anchor — we don't want a
// dangling selection from an earlier action) to the start of the
// currently-pointed match. No-op when FindIndex is out of range, so
// callers don't have to re-check it themselves.
func (t *Tab) FocusCurrentMatch() {
	if t.FindIndex < 0 || t.FindIndex >= len(t.FindMatches) {
		return
	}
	m := t.FindMatches[t.FindIndex]
	t.Cursor = MatchPosition(m)
	t.Anchor = t.Cursor
	t.cursorMoved = true
}

// FindNext advances FindIndex by one (wrapping at the end) and moves
// the cursor onto the new match. No-op when there are no matches. Used
// by Enter inside the find bar and by the Esc-g "again" leader.
func (t *Tab) FindNext() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex = (t.FindIndex + 1) % len(t.FindMatches)
	t.FocusCurrentMatch()
}

// FindPrev moves FindIndex backwards by one (wrapping at the start) and
// moves the cursor onto the new match. Used by Shift-Enter inside the
// find bar.
func (t *Tab) FindPrev() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex--
	if t.FindIndex < 0 {
		t.FindIndex = len(t.FindMatches) - 1
	}
	t.FocusCurrentMatch()
}

// findRowSpan is the half-open range of FindMatches living on one buffer
// line. FindAll emits matches in document order, so a line's hits are
// always contiguous and one span describes all of them.
type findRowSpan struct{ start, end int }

// buildFindRows indexes FindMatches by line. Built lazily on first
// lookup: the package's only writers of FindMatches (SetFindQuery,
// ClearFind) drop the index, and the recorded length catches anyone who
// assigns the exported field directly.
func (t *Tab) buildFindRows() {
	rows := make(map[int]findRowSpan)
	for i, m := range t.FindMatches {
		sp, ok := rows[m.Line]
		if !ok {
			sp.start = i
		}
		sp.end = i + 1
		rows[m.Line] = sp
	}
	t.findRows = rows
	t.findRowsFor = len(t.FindMatches)
}

// matchAtRune returns the index into FindMatches of the match that
// covers (line, col), or -1 when none does. The renderer calls this once
// per visible cell, so it goes through the per-line index and a binary
// search rather than scanning the list: a query with thousands of hits
// made every repaint O(cells x matches).
//
// The answer matches the old linear scan exactly — the lowest-indexed
// covering match wins. Within a line FindAll emits non-decreasing ends,
// so "first match on this line whose end is past col" is that same
// match, whether the hits are adjacent or overlapping.
func (t *Tab) matchAtRune(line, col int) int {
	if len(t.FindMatches) == 0 {
		return -1
	}
	if t.findRows == nil || t.findRowsFor != len(t.FindMatches) {
		t.buildFindRows()
	}
	sp, ok := t.findRows[line]
	if !ok {
		return -1
	}
	lo, hi := sp.start, sp.end
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if m := t.FindMatches[mid]; m.Col+m.Width <= col {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= sp.end {
		return -1
	}
	if m := t.FindMatches[lo]; col >= m.Col {
		return lo
	}
	return -1
}

// ClearFind drops every piece of find state. The app calls this when the
// buffer has been edited enough that the cached match list is stale and
// can't safely be re-used; the user will re-type their query.
func (t *Tab) ClearFind() {
	t.FindQuery = ""
	t.FindMatches = nil
	t.FindIndex = -1
	t.findRows = nil
}

// ReplaceCurrentMatch swaps the current find match for repl and
// re-runs the query so the highlights (and the match count) stay
// truthful. The cursor lands just after the replacement and the
// current index stays put, so "replace, replace, replace" walks the
// file forward naturally. Returns false when there is nothing to
// replace.
func (t *Tab) ReplaceCurrentMatch(repl string) bool {
	if t.IsImage() || t.FindIndex < 0 || t.FindIndex >= len(t.FindMatches) {
		return false
	}
	m := t.FindMatches[t.FindIndex]
	t.edit(undoGroupStructural, func() {
		start := Position{Line: m.Line, Col: m.Col}
		end := Position{Line: m.Line, Col: m.Col + m.Width}
		t.Buffer.DeleteRange(start, end)
		after := t.Buffer.InsertString(start, repl)
		t.Cursor = after
		t.Anchor = after
	})
	return true
}

// ReplaceAllMatches swaps every match for repl as ONE undo step and
// returns how many were replaced. Matches are applied last-to-first so
// earlier spans stay valid while later ones are rewritten.
func (t *Tab) ReplaceAllMatches(repl string) int {
	if t.IsImage() || len(t.FindMatches) == 0 {
		return 0
	}
	// Hold the list being replaced: the edit trailer re-runs the query,
	// so t.FindMatches stops describing the spans this call swapped.
	matches := t.FindMatches
	t.edit(undoGroupStructural, func() {
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			start := Position{Line: m.Line, Col: m.Col}
			end := Position{Line: m.Line, Col: m.Col + m.Width}
			t.Buffer.DeleteRange(start, end)
			t.Buffer.InsertString(start, repl)
		}
	})
	return len(matches)
}

// ReplaceLines swaps whole lines (0-based index → new content) as ONE
// undo step — project-wide replace routes open buffers through here so
// a tab keeps its history and its dirty-state semantics. Out-of-range
// indexes are ignored (the caller verified against a live buffer, but
// buffers move). Returns how many lines were actually swapped.
func (t *Tab) ReplaceLines(newLines map[int]string) int {
	if t.IsImage() || len(newLines) == 0 {
		return 0
	}
	// Count the in-range swaps first: an edit always costs an undo entry,
	// so a map naming only vanished lines must not leave the user with a
	// history step that changed nothing.
	n := 0
	for i := range newLines {
		if i >= 0 && i < len(t.Buffer.Lines) {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	t.edit(undoGroupStructural, func() {
		for i, text := range newLines {
			if i < 0 || i >= len(t.Buffer.Lines) {
				continue
			}
			t.Buffer.Lines[i] = text
		}
		t.Cursor = t.Buffer.Clamp(t.Cursor)
		t.Anchor = t.Cursor
	})
	return n
}
