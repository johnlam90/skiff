// =============================================================================
// File: internal/editor/bracket.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// bracket.go finds the partner of the bracket the caret is touching, so the
// renderer can mark the pair and the user can jump between them.
//
// Deliberate non-goals, so nobody "fixes" them later:
//
//   - No string / comment awareness. Chroma tokens are available but they
//     are computed for a window around the viewport, not the whole file,
//     and a matcher that silently changed answers depending on how far you
//     had scrolled would be worse than one that is consistently naive.
//     A brace inside a string literal is matched like any other brace.
//   - Only the same pair type nests. "{ [ }" resolves the brace to the
//     brace and ignores the stray bracket, rather than declaring a
//     mismatch. Half-written code is the normal state of an editor buffer;
//     being forgiving there is the useful behavior.
//   - The scan is capped (bracketScanLimit). Past the cap we report
//     nothing at all rather than guessing "unmatched" — see BracketMatch.
//
// Rendering decision (Tab.cellStyle): a MATCHED pair paints both cells in
// the accent color, bold and underlined, so it survives theme.Degrade on a
// low-color terminal where hue is gone. An UNMATCHED bracket paints the one
// cell the caret is touching in the error color. Rendering nothing for an
// unmatched bracket was rejected: it is indistinguishable from "this editor
// has no bracket matching", and an unclosed brace is exactly the thing a
// user wants to be told about. It cannot become ambient noise because the
// marker only ever appears on the bracket under the caret.

package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// bracketScanLimit caps how many runes one match attempt may read. A
// matching bracket is nearly always a few hundred runes away; the cap
// exists for the pathological case (a caret parked next to an unmatched
// brace at the top of a minified megabyte), where an uncapped scan would
// re-walk the whole file on every repaint. 100k runes is far past any real
// code block and costs well under a millisecond.
const bracketScanLimit = 100_000

// BracketMatch is the answer to "what bracket is the caret touching, and
// where is its partner". Found is false both when the caret is not on a
// bracket and when the scan hit bracketScanLimit without deciding — in
// either case the honest render is no highlight at all, so the two collapse
// into one flag on purpose. Matched distinguishes "found the partner" from
// "this bracket is unbalanced", which render differently.
type BracketMatch struct {
	Found   bool
	Matched bool
	At      Position
	Match   Position
}

// bracketPartner classifies r: partner is the rune that closes (or opens)
// the pair, opens says which side r is, and ok is false for every rune that
// is not one of ()[]{}.
func bracketPartner(r rune) (partner rune, opens, ok bool) {
	switch r {
	case '(':
		return ')', true, true
	case '[':
		return ']', true, true
	case '{':
		return '}', true, true
	case ')':
		return '(', false, true
	case ']':
		return '[', false, true
	case '}':
		return '{', false, true
	}
	return 0, false, false
}

// bracketAt resolves which bracket the caret at p is touching. The rune
// UNDER the caret wins over the rune BEFORE it, which is what makes both
// halves of the common gestures work: land on an opener with a click or an
// arrow key and you match forward from it, and finish typing "foo()" — the
// caret now sits past the closer — and you match backward from that.
//
// For a caret between two brackets, "(|)", the two rules pick different
// runes but the same pair, so the distinction is invisible there.
func bracketAt(buf *Buffer, p Position) (Position, bool) {
	if buf == nil || p.Line < 0 || p.Line >= buf.LineCount() {
		return Position{}, false
	}
	runes := buf.LineRunes(p.Line)
	if p.Col >= 0 && p.Col < len(runes) {
		if _, _, ok := bracketPartner(runes[p.Col]); ok {
			return Position{Line: p.Line, Col: p.Col}, true
		}
	}
	if p.Col-1 >= 0 && p.Col-1 < len(runes) {
		if _, _, ok := bracketPartner(runes[p.Col-1]); ok {
			return Position{Line: p.Line, Col: p.Col - 1}, true
		}
	}
	return Position{}, false
}

// scanOutcome is why a scan stopped: it found the partner, it walked out of
// the buffer without finding one, or it ran out of budget and therefore
// knows nothing. The third case must not be reported as "unmatched" — that
// would paint a perfectly balanced brace as an error.
type scanOutcome int

const (
	scanFound scanOutcome = iota
	scanMissing
	scanGaveUp
)

// MatchBracketAt returns the bracket the caret at p is touching together
// with its partner. The scan starts on the bracket itself so its own depth
// contribution needs no special case.
func MatchBracketAt(buf *Buffer, p Position) BracketMatch {
	at, ok := bracketAt(buf, p)
	if !ok {
		return BracketMatch{}
	}
	r := buf.LineRunes(at.Line)[at.Col]
	partner, opens, _ := bracketPartner(r)

	var match Position
	var outcome scanOutcome
	if opens {
		match, outcome = scanBracketForward(buf, at, r, partner)
	} else {
		match, outcome = scanBracketBackward(buf, at, partner, r)
	}
	switch outcome {
	case scanFound:
		return BracketMatch{Found: true, Matched: true, At: at, Match: match}
	case scanMissing:
		return BracketMatch{Found: true, At: at}
	}
	return BracketMatch{}
}

// scanBracketForward walks from the opener at `from` toward the end of the
// buffer, tracking nesting depth of this pair type only, and returns the
// position of the closer that brings the depth back to zero.
func scanBracketForward(buf *Buffer, from Position, open, closing rune) (Position, scanOutcome) {
	depth := 0
	budget := bracketScanLimit
	col := from.Col
	for line := from.Line; line < buf.LineCount(); line++ {
		runes := buf.LineRunes(line)
		if line != from.Line {
			col = 0
		}
		for ; col < len(runes); col++ {
			switch runes[col] {
			case open:
				depth++
			case closing:
				depth--
				if depth == 0 {
					return Position{Line: line, Col: col}, scanFound
				}
			}
			budget--
			if budget <= 0 {
				return Position{}, scanGaveUp
			}
		}
		// The line break costs a unit too, so a file of a million empty
		// lines still exhausts the budget instead of walking forever.
		budget--
		if budget <= 0 {
			return Position{}, scanGaveUp
		}
	}
	return Position{}, scanMissing
}

// scanBracketBackward is scanBracketForward's mirror: it walks from the
// closer at `from` toward the start of the buffer looking for the opener
// that balances it.
func scanBracketBackward(buf *Buffer, from Position, open, closing rune) (Position, scanOutcome) {
	depth := 0
	budget := bracketScanLimit
	col := from.Col
	for line := from.Line; line >= 0; line-- {
		runes := buf.LineRunes(line)
		if line != from.Line {
			col = len(runes) - 1
		}
		for ; col >= 0; col-- {
			switch runes[col] {
			case closing:
				depth++
			case open:
				depth--
				if depth == 0 {
					return Position{Line: line, Col: col}, scanFound
				}
			}
			budget--
			if budget <= 0 {
				return Position{}, scanGaveUp
			}
		}
		budget--
		if budget <= 0 {
			return Position{}, scanGaveUp
		}
	}
	return Position{}, scanMissing
}

// refreshBracketMatch recomputes the caret's bracket pair when the cursor
// moved or the buffer changed since the last computation, and is a no-op
// otherwise. Render calls it once per frame before painting (cellStyle then
// only does two Position compares per cell) and the menu's enable predicate
// calls it too, so the cache is what keeps a menu that is redrawn every
// frame from re-scanning the buffer for each row.
//
// StyleStale is the buffer-changed signal: every mutator in the package
// sets it, and Render clears it in the same pass that re-tokenises.
func (t *Tab) refreshBracketMatch() {
	if t.bracketCached && t.bracketFor == t.Cursor && !t.StyleStale {
		return
	}
	t.bracket = MatchBracketAt(t.Buffer, t.Cursor)
	t.bracketFor = t.Cursor
	t.bracketCached = true
}

// HasMatchingBracket reports whether the caret is touching a bracket whose
// partner we found. The action menu uses it to dim "Go to matching bracket"
// when the jump would do nothing.
func (t *Tab) HasMatchingBracket() bool {
	if t.IsImage() || t.Buffer == nil {
		return false
	}
	t.refreshBracketMatch()
	return t.bracket.Found && t.bracket.Matched
}

// GoToMatchingBracket jumps the caret onto the partner of the bracket it is
// touching and returns true. False means there was nothing to jump to, so
// the caller can say so instead of leaving the user wondering.
//
// The jump lands ON the partner rather than past it, which means the pair
// stays highlighted from the far end and pressing the action again returns
// you to where you started.
func (t *Tab) GoToMatchingBracket() bool {
	if t.IsImage() || t.Buffer == nil {
		return false
	}
	t.refreshBracketMatch()
	if !t.bracket.Found || !t.bracket.Matched {
		return false
	}
	t.MoveCursorTo(t.bracket.Match, false)
	return true
}

// bracketCellStyle applies the bracket marker to a cell that has already
// been resolved for syntax, line background, and selection. It changes the
// foreground and attributes only, never the background, so it composes with
// the cursor-line tint and with a selection instead of punching a hole in
// them.
func bracketCellStyle(th theme.Theme, st tcell.Style, matched bool) tcell.Style {
	if matched {
		return st.Foreground(th.Accent).Bold(true).Underline(true)
	}
	return st.Foreground(th.Error)
}
