// =============================================================================
// File: internal/editor/word.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// word.go owns the editor's single definition of "a word" and the caret
// motions built on it.
//
// The definition used to live in internal/app/mouse.go, private to
// double-click selection. Once the caret grew word-wise motion the two
// surfaces had to agree — a user who double-clicks a token and a user who
// walks to it with Alt+Left must be told the same story about where that
// token starts. So the predicate moved here, next to the buffer it
// classifies, and mouse.go calls into it.
//
// The predicate is Unicode-aware: letters, digits, underscore, and
// combining marks are word runes. That is a deliberate call on the two
// scripts that make "a word" ambiguous:
//
//   - CJK ideographs and kana COUNT as word runes. They are letters, and
//     the alternative is worse in both directions: double-clicking a Han
//     run would select nothing, and word-wise motion would treat the whole
//     unspaced sentence as one boundary run and leap over it in a single
//     press. Counting them means a run of Han between two punctuation
//     marks selects as one word — long, but predictable, and it is what
//     VS Code does with its default separator list.
//   - Combining marks count too, so "café" written with a combining acute
//     is one word rather than "caf" plus a stray accent. Motion also steps
//     by grapheme cluster (see cluster.go), which makes that structural
//     rather than a happy accident.
//
// Latin text with accents ("naïve") is the case that quietly improved: the
// old ASCII-only predicate split it mid-word.

package editor

import (
	"unicode"
	"unicode/utf8"
)

// IsWordChar reports whether r counts as part of a word for selection and
// caret motion. Underscore is included because snake_case identifiers read
// as one token to a programmer; everything else — punctuation, whitespace,
// operators — is a boundary. See the file comment for why letters outside
// ASCII (including CJK) are in.
func IsWordChar(r rune) bool {
	// Source code is overwhelmingly ASCII and this runs per rune inside
	// every motion, so the common case never touches the Unicode tables.
	if r < utf8.RuneSelf {
		return r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// WordLeft returns the column a leftward word motion from col lands on:
// skip backwards over any boundary clusters, then over the word run behind
// them, so the caret stops at the START of the word to its left. col is
// clamped into runes at both ends — the caret can legitimately sit one
// past the last rune, and clamping here means no caller has to.
//
// The walk steps by grapheme cluster and classifies a cluster by its base
// rune, so a motion can never stop between a letter and its accent.
func WordLeft(runes []rune, col int) int {
	i := ClusterStart(runes, clampCol(runes, col))
	for i > 0 {
		p := PrevCluster(runes, i)
		if IsWordChar(runes[p]) {
			break
		}
		i = p
	}
	for i > 0 {
		p := PrevCluster(runes, i)
		if !IsWordChar(runes[p]) {
			break
		}
		i = p
	}
	return i
}

// WordRight returns the column a rightward word motion from col lands on:
// skip forward over any boundary clusters, then over the word run past
// them, so the caret stops at the END of the word to its right. This
// mirrors WordLeft — left lands on a word's first rune, right lands one
// past its last — which is what makes a left-then-right round trip
// re-select the same token.
func WordRight(runes []rune, col int) int {
	i := ClusterStart(runes, clampCol(runes, col))
	for i < len(runes) && !IsWordChar(runes[i]) {
		i = NextCluster(runes, i)
	}
	for i < len(runes) && IsWordChar(runes[i]) {
		i = NextCluster(runes, i)
	}
	return i
}

// clampCol pins a column into [0, len(runes)] — the caret's legal range,
// one past the last rune included. Shared by the word helpers so each one
// isn't re-deriving the same two bounds checks.
func clampCol(runes []rune, col int) int {
	if col > len(runes) {
		return len(runes)
	}
	if col < 0 {
		return 0
	}
	return col
}

// WordRangeAt returns the half-open [start, end) column span of the word
// covering col. ok is false when col does not sit on a word rune (the
// click landed in whitespace or punctuation) — callers treat that as
// "select nothing" rather than selecting the empty range, which would
// silently drop whatever selection the user already had.
//
// A caret sitting immediately after a word (col == end of the run) still
// resolves to that word: that is where a double-click at the right edge of
// a token puts you, and it is what makes Alt+Right then double-click agree.
func WordRangeAt(runes []rune, col int) (start, end int, ok bool) {
	col = ClusterStart(runes, clampCol(runes, col))
	start = col
	for start > 0 {
		p := PrevCluster(runes, start)
		if !IsWordChar(runes[p]) {
			break
		}
		start = p
	}
	end = col
	for end < len(runes) && IsWordChar(runes[end]) {
		end = NextCluster(runes, end)
	}
	if start == end {
		return 0, 0, false
	}
	return start, end, true
}

// SelectWordAt selects the word under the buffer position p, or does
// nothing when p sits in whitespace / punctuation. Used by double-click in
// the app layer.
//
// cursorMoved is deliberately NOT set: the word was just clicked, so it is
// on screen by construction, and flagging a scroll here would fight the
// auto-scroll a drag may already be running.
func (t *Tab) SelectWordAt(p Position) {
	runes := t.Buffer.LineRunes(p.Line)
	if len(runes) == 0 {
		return
	}
	start, end, ok := WordRangeAt(runes, p.Col)
	if !ok {
		return
	}
	t.Anchor = Position{Line: p.Line, Col: start}
	t.Cursor = Position{Line: p.Line, Col: end}
}

// MoveWordLeft walks the caret one word to the left, extending the
// selection when extend is set. At column 0 the motion wraps to the end of
// the previous line — one step, not "wrap and keep hunting" — so the
// gesture stays predictable at a line boundary, exactly like Left does.
func (t *Tab) MoveWordLeft(extend bool) {
	if t.IsImage() {
		return
	}
	cur := t.Cursor
	if cur.Col <= 0 {
		if cur.Line == 0 {
			t.MoveCursorTo(Position{}, extend)
			return
		}
		cur.Line--
		cur.Col = len(t.Buffer.LineRunes(cur.Line))
		t.MoveCursorTo(cur, extend)
		return
	}
	cur.Col = WordLeft(t.Buffer.LineRunes(cur.Line), cur.Col)
	t.MoveCursorTo(cur, extend)
}

// MoveWordRight walks the caret one word to the right, extending the
// selection when extend is set. Past the last rune of a line the motion
// wraps to column 0 of the next line, mirroring MoveWordLeft.
func (t *Tab) MoveWordRight(extend bool) {
	if t.IsImage() {
		return
	}
	cur := t.Cursor
	runes := t.Buffer.LineRunes(cur.Line)
	if cur.Col >= len(runes) {
		if cur.Line >= t.Buffer.LineCount()-1 {
			t.MoveCursorTo(Position{Line: cur.Line, Col: len(runes)}, extend)
			return
		}
		t.MoveCursorTo(Position{Line: cur.Line + 1}, extend)
		return
	}
	cur.Col = WordRight(runes, cur.Col)
	t.MoveCursorTo(cur, extend)
}
