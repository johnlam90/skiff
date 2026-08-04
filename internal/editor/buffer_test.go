// =============================================================================
// File: internal/editor/buffer_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the line-per-string Buffer primitive. Buffer is the foundation
// every higher-level editor operation rests on, so we exhaustively pin down
// its behavior around clamping, multi-line splices, rune-vs-byte indexing,
// and selection ordering. If any of these regress, the whole editor will
// misbehave in subtle, hard-to-debug ways.

package editor

import (
	"strconv"
	"strings"
	"testing"
)

// TestNewBuffer_Empty verifies that constructing a buffer from an empty
// string still yields a single empty line — every Buffer invariant assumes
// LineCount() >= 1, and downstream code crashes if we ever return zero lines.
func TestNewBuffer_Empty(t *testing.T) {
	b := NewBuffer("")
	if b.LineCount() != 1 {
		t.Fatalf("empty buffer should have 1 line, got %d", b.LineCount())
	}
	if b.Lines[0] != "" {
		t.Fatalf("empty buffer line 0 should be empty, got %q", b.Lines[0])
	}
}

// TestNewBuffer_TrailingNewline confirms that a trailing newline produces an
// empty final line — that's how POSIX text files end and how editors render.
func TestNewBuffer_TrailingNewline(t *testing.T) {
	b := NewBuffer("hello\n")
	if b.LineCount() != 2 {
		t.Fatalf("expected 2 lines, got %d", b.LineCount())
	}
	if b.Lines[0] != "hello" || b.Lines[1] != "" {
		t.Fatalf("unexpected lines: %#v", b.Lines)
	}
}

// TestNewBuffer_MultiLine ensures multi-line input is split correctly.
func TestNewBuffer_MultiLine(t *testing.T) {
	b := NewBuffer("a\nb\nc")
	if b.LineCount() != 3 {
		t.Fatalf("expected 3 lines, got %d", b.LineCount())
	}
	if b.Lines[1] != "b" {
		t.Fatalf("line 1 wrong: %q", b.Lines[1])
	}
}

// TestBuffer_String_RoundTrip checks that String() is the inverse of
// NewBuffer for the common cases — load and save must agree.
func TestBuffer_String_RoundTrip(t *testing.T) {
	cases := []string{"", "x", "a\nb", "trailing\n", "a\n\nb"}
	for _, src := range cases {
		got := NewBuffer(src).String()
		if got != src {
			t.Fatalf("round-trip mismatch: %q -> %q", src, got)
		}
	}
}

// TestNewBuffer_StripsCRLF pins the load half of line-ending handling: a
// Windows-authored file must land in the buffer as clean lines. Leaving
// the CR on would put an invisible column at the end of every line, so
// every rune count, every cursor clamp, and every save would carry it.
func TestNewBuffer_StripsCRLF(t *testing.T) {
	b := NewBuffer("alpha\r\nbeta\r\n")
	want := []string{"alpha", "beta", ""}
	if len(b.Lines) != len(want) {
		t.Fatalf("lines = %#v, want %#v", b.Lines, want)
	}
	for i, ln := range want {
		if b.Lines[i] != ln {
			t.Fatalf("line %d = %q, want %q", i, b.Lines[i], ln)
		}
	}
}

// TestNewBuffer_KeepsLoneCarriageReturn guards the other side of the CRLF
// strip: a \r that is not followed by \n is data, not a line ending, and
// dropping it would silently corrupt the file on the next save.
func TestNewBuffer_KeepsLoneCarriageReturn(t *testing.T) {
	for _, src := range []string{"lone\rcarriage", "trailing\r", "mid\rline\nnext"} {
		if got := NewBuffer(src).String(); got != src {
			t.Fatalf("lone CR was eaten: %q -> %q", src, got)
		}
	}
}

// TestBuffer_TextWith_JoinsWithGivenEnding proves the save half: the
// buffer holds unterminated lines, so the separator alone decides the
// file's line ending. Tab.Save passes the ending detected at open.
func TestBuffer_TextWith_JoinsWithGivenEnding(t *testing.T) {
	b := NewBuffer("alpha\r\nbeta")
	if got := b.TextWith("\r\n"); got != "alpha\r\nbeta" {
		t.Fatalf("CRLF join = %q", got)
	}
	if got := b.TextWith("\n"); got != "alpha\nbeta" {
		t.Fatalf("LF join = %q", got)
	}
}

// TestBuffer_LineRunes_OutOfRange returns nil for negative or past-end
// indices so callers can branch on a nil slice without panicking.
func TestBuffer_LineRunes_OutOfRange(t *testing.T) {
	b := NewBuffer("hello")
	if got := b.LineRunes(-1); got != nil {
		t.Fatalf("expected nil for -1, got %v", got)
	}
	if got := b.LineRunes(99); got != nil {
		t.Fatalf("expected nil for 99, got %v", got)
	}
}

// TestBuffer_LineRunes_Multibyte verifies rune-counting (not byte-counting)
// on lines containing multi-byte characters.
func TestBuffer_LineRunes_Multibyte(t *testing.T) {
	b := NewBuffer("héllo")
	got := b.LineRunes(0)
	if len(got) != 5 {
		t.Fatalf("expected 5 runes, got %d (%v)", len(got), got)
	}
}

// TestBuffer_LineRunes_CacheFollowsMutations is the regression guard for
// the decode cache behind LineRunes. The cache validates against the
// source string rather than being invalidated by hand, so every way a
// line can change — an in-place write, an edit through the buffer API,
// and a splice that shifts every later index — has to be reflected
// immediately. A stale hit here would paint deleted text on screen.
func TestBuffer_LineRunes_CacheFollowsMutations(t *testing.T) {
	b := NewBuffer("one\ntwo\nthree")

	// Warm every line so each has a live cache entry.
	for i := range b.Lines {
		_ = b.LineRunes(i)
	}

	// (a) Direct write to the exported field.
	b.Lines[1] = "TWO!"
	if got := string(b.LineRunes(1)); got != "TWO!" {
		t.Fatalf("direct write served stale runes: %q", got)
	}

	// (b) Edit through the buffer API.
	b.InsertString(Position{Line: 0, Col: 3}, "X")
	if got := string(b.LineRunes(0)); got != "oneX" {
		t.Fatalf("insert served stale runes: %q", got)
	}

	// (c) A splice that shifts the lines below it: line 1's cached entry
	// now names different content at the same index.
	b.InsertString(Position{Line: 0, Col: 0}, "zero\n")
	if got := string(b.LineRunes(1)); got != "oneX" {
		t.Fatalf("index shift served stale runes: %q", got)
	}
	if got := string(b.LineRunes(2)); got != "TWO!" {
		t.Fatalf("index shift served stale runes at 2: %q", got)
	}

	// (d) Deleting a line shifts them back the other way.
	b.DeleteRange(Position{Line: 0, Col: 0}, Position{Line: 1, Col: 0})
	if got := string(b.LineRunes(0)); got != "oneX" {
		t.Fatalf("delete served stale runes: %q", got)
	}
}

// TestBuffer_LineRunes_MemoisesDecode pins the point of the cache, not
// just its freshness: asking for the same unchanged line twice must hand
// back the same slice instead of decoding again. One wrap-mode frame
// walks the viewport three times — EnsureVisible, the clamp, then the
// paint — so a decode per call costs O(viewport x lineLen) allocations
// on every repaint, including idle ones.
func TestBuffer_LineRunes_MemoisesDecode(t *testing.T) {
	b := NewBuffer("some line of text\nanother line")
	first := b.LineRunes(0)
	if len(first) == 0 {
		t.Fatal("expected a decoded line")
	}
	if second := b.LineRunes(0); &second[0] != &first[0] {
		t.Fatal("repeat read re-decoded the line instead of reusing the cache")
	}

	// A neighbouring line lands in its own slot and evicts nothing.
	_ = b.LineRunes(1)
	if third := b.LineRunes(0); &third[0] != &first[0] {
		t.Fatal("reading a neighbouring line dropped the cached decode")
	}

	// An edit replaces the source string, so the next read must decode.
	b.Lines[0] = "some line of text!"
	if after := b.LineRunes(0); &after[0] == &first[0] {
		t.Fatal("edited line was served the stale cached decode")
	}
}

// TestBuffer_LineRunes_CacheSurvivesIndexAliasing pins the direct-mapped
// cache's collision behaviour: two lines exactly runeCacheSlots apart
// share a slot, and reading them alternately must never hand one line's
// runes back for the other.
func TestBuffer_LineRunes_CacheSurvivesIndexAliasing(t *testing.T) {
	lines := make([]string, runeCacheSlots+1)
	for i := range lines {
		lines[i] = "line-" + strconv.Itoa(i)
	}
	b := &Buffer{Lines: lines}
	for range 3 {
		if got := string(b.LineRunes(0)); got != "line-0" {
			t.Fatalf("slot collision returned %q for line 0", got)
		}
		if got := string(b.LineRunes(runeCacheSlots)); got != "line-"+strconv.Itoa(runeCacheSlots) {
			t.Fatalf("slot collision returned %q for the aliasing line", got)
		}
	}
}

// TestBuffer_Clamp_AllAxes verifies clamping in every direction (negative
// line, past-end line, negative col, past-end col) and confirms that a col
// equal to the rune length is allowed (cursor sits at end-of-line).
func TestBuffer_Clamp_AllAxes(t *testing.T) {
	b := NewBuffer("ab\ncde")

	cases := []struct {
		in, want Position
	}{
		{Position{Line: -5, Col: 0}, Position{Line: 0, Col: 0}},
		{Position{Line: 99, Col: 0}, Position{Line: 1, Col: 0}},
		{Position{Line: 0, Col: -3}, Position{Line: 0, Col: 0}},
		{Position{Line: 0, Col: 99}, Position{Line: 0, Col: 2}},
		{Position{Line: 1, Col: 3}, Position{Line: 1, Col: 3}}, // end-of-line allowed
		{Position{Line: 0, Col: 1}, Position{Line: 0, Col: 1}}, // already valid
	}
	for _, c := range cases {
		got := b.Clamp(c.in)
		if got != c.want {
			t.Errorf("Clamp(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestBuffer_InsertString_Empty is a no-op insertion: the returned position
// is the (clamped) input position and the buffer is unchanged.
func TestBuffer_InsertString_Empty(t *testing.T) {
	b := NewBuffer("hello")
	end := b.InsertString(Position{Line: 0, Col: 2}, "")
	if end != (Position{Line: 0, Col: 2}) {
		t.Fatalf("unexpected end: %+v", end)
	}
	if b.String() != "hello" {
		t.Fatalf("buffer changed: %q", b.String())
	}
}

// TestBuffer_InsertString_SingleLine inserts text without newlines and checks
// the returned end position points just after the inserted runes.
func TestBuffer_InsertString_SingleLine(t *testing.T) {
	b := NewBuffer("hello")
	end := b.InsertString(Position{Line: 0, Col: 5}, " world")
	if b.String() != "hello world" {
		t.Fatalf("got %q", b.String())
	}
	if end != (Position{Line: 0, Col: 11}) {
		t.Fatalf("end pos wrong: %+v", end)
	}
}

// TestBuffer_InsertString_AcrossLineBoundary verifies that inserting text
// containing newlines splits a single buffer line into the right number of
// lines and that the returned end-position points at the final inserted col.
func TestBuffer_InsertString_AcrossLineBoundary(t *testing.T) {
	b := NewBuffer("abXYZ")
	end := b.InsertString(Position{Line: 0, Col: 2}, "1\n22\n333")
	want := "ab1\n22\n333XYZ"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
	if end != (Position{Line: 2, Col: 3}) {
		t.Fatalf("end pos wrong: %+v", end)
	}
	if b.LineCount() != 3 {
		t.Fatalf("expected 3 lines, got %d", b.LineCount())
	}
}

// TestBuffer_InsertString_TwoLine covers exactly two parts (one newline) —
// the boundary between single-line and 3+ line insert paths.
func TestBuffer_InsertString_TwoLine(t *testing.T) {
	b := NewBuffer("abcd")
	end := b.InsertString(Position{Line: 0, Col: 2}, "X\nY")
	if b.String() != "abX\nYcd" {
		t.Fatalf("got %q", b.String())
	}
	if end != (Position{Line: 1, Col: 1}) {
		t.Fatalf("end pos wrong: %+v", end)
	}
}

// TestBuffer_InsertString_Multibyte confirms rune-indexed columns behave
// correctly when inserting around multi-byte text.
func TestBuffer_InsertString_Multibyte(t *testing.T) {
	b := NewBuffer("héllo")
	end := b.InsertString(Position{Line: 0, Col: 1}, "X")
	if b.String() != "hXéllo" {
		t.Fatalf("got %q", b.String())
	}
	if end != (Position{Line: 0, Col: 2}) {
		t.Fatalf("end pos wrong: %+v", end)
	}
}

// TestBuffer_DeleteRange_SameLine deletes a span on a single line and
// returns the smaller endpoint as the new cursor.
func TestBuffer_DeleteRange_SameLine(t *testing.T) {
	b := NewBuffer("abcdef")
	pos := b.DeleteRange(Position{Line: 0, Col: 1}, Position{Line: 0, Col: 4})
	if b.String() != "aef" {
		t.Fatalf("got %q", b.String())
	}
	if pos != (Position{Line: 0, Col: 1}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestBuffer_DeleteRange_ReversedOrder ensures DeleteRange normalises its
// inputs — passing endpoints out of order yields the same result.
func TestBuffer_DeleteRange_ReversedOrder(t *testing.T) {
	b := NewBuffer("abcdef")
	pos := b.DeleteRange(Position{Line: 0, Col: 4}, Position{Line: 0, Col: 1})
	if b.String() != "aef" {
		t.Fatalf("got %q", b.String())
	}
	if pos != (Position{Line: 0, Col: 1}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestBuffer_DeleteRange_Empty is a no-op when both positions are equal.
func TestBuffer_DeleteRange_Empty(t *testing.T) {
	b := NewBuffer("abc")
	pos := b.DeleteRange(Position{Line: 0, Col: 1}, Position{Line: 0, Col: 1})
	if b.String() != "abc" {
		t.Fatalf("buffer changed: %q", b.String())
	}
	if pos != (Position{Line: 0, Col: 1}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestBuffer_DeleteRange_MultiLine joins lines correctly when a range spans
// multiple lines.
func TestBuffer_DeleteRange_MultiLine(t *testing.T) {
	b := NewBuffer("hello\nbig\nworld")
	pos := b.DeleteRange(Position{Line: 0, Col: 2}, Position{Line: 2, Col: 2})
	if b.String() != "herld" {
		t.Fatalf("got %q", b.String())
	}
	if pos != (Position{Line: 0, Col: 2}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
	if b.LineCount() != 1 {
		t.Fatalf("expected 1 line, got %d", b.LineCount())
	}
}

// TestBuffer_Substring_SameLine returns the slice of text on one line.
func TestBuffer_Substring_SameLine(t *testing.T) {
	b := NewBuffer("hello world")
	got := b.Substring(Position{Line: 0, Col: 6}, Position{Line: 0, Col: 11})
	if got != "world" {
		t.Fatalf("got %q", got)
	}
}

// TestBuffer_Substring_Reversed returns text in document order regardless of
// the input order.
func TestBuffer_Substring_Reversed(t *testing.T) {
	b := NewBuffer("hello world")
	got := b.Substring(Position{Line: 0, Col: 11}, Position{Line: 0, Col: 6})
	if got != "world" {
		t.Fatalf("got %q", got)
	}
}

// TestBuffer_Substring_MultiLine joins lines with '\n' between the start
// and end positions.
func TestBuffer_Substring_MultiLine(t *testing.T) {
	b := NewBuffer("foo\nbar\nbaz")
	got := b.Substring(Position{Line: 0, Col: 1}, Position{Line: 2, Col: 2})
	want := "oo\nbar\nba"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestBuffer_Substring_Empty returns "" when the range is empty.
func TestBuffer_Substring_Empty(t *testing.T) {
	b := NewBuffer("hello")
	got := b.Substring(Position{Line: 0, Col: 2}, Position{Line: 0, Col: 2})
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// TestBuffer_EndPos returns the position just after the last rune.
func TestBuffer_EndPos(t *testing.T) {
	cases := []struct {
		src  string
		want Position
	}{
		{"", Position{Line: 0, Col: 0}},
		{"abc", Position{Line: 0, Col: 3}},
		{"abc\n", Position{Line: 1, Col: 0}},
		{"abc\nde", Position{Line: 1, Col: 2}},
	}
	for _, c := range cases {
		got := NewBuffer(c.src).EndPos()
		if got != c.want {
			t.Errorf("EndPos(%q) = %+v want %+v", c.src, got, c.want)
		}
	}
}

// TestPosLess_AndOrdered exercises the position-comparison helpers used by
// selection logic — every selection operation depends on this being correct.
func TestPosLess_AndOrdered(t *testing.T) {
	a := Position{Line: 0, Col: 5}
	b := Position{Line: 1, Col: 0}
	c := Position{Line: 0, Col: 5}

	if !PosLess(a, b) {
		t.Fatal("a should be < b")
	}
	if PosLess(b, a) {
		t.Fatal("b should not be < a")
	}
	if PosLess(a, c) {
		t.Fatal("equal positions should not be Less")
	}

	// Same line, different col.
	d := Position{Line: 1, Col: 3}
	e := Position{Line: 1, Col: 7}
	if !PosLess(d, e) {
		t.Fatal("d should be < e")
	}

	// PosOrdered normalises pairs.
	x, y := PosOrdered(b, a)
	if x != a || y != b {
		t.Fatalf("PosOrdered wrong: %+v %+v", x, y)
	}
	x, y = PosOrdered(a, b)
	if x != a || y != b {
		t.Fatalf("PosOrdered should be stable for already-ordered: %+v %+v", x, y)
	}
}

// FuzzBufferInsertDelete pins the primitive every editing gesture is built
// from: an insert followed by a delete of exactly the span the insert
// reported must leave the buffer bit-identical to where it started. Undo,
// paste, replace, and the multi-line splice path all lean on
// InsertString's returned end position being the true end of what it
// wrote, so the target also checks that Substring over that span reads
// back the inserted text and that the buffer never loses its "always at
// least one line" invariant.
func FuzzBufferInsertDelete(f *testing.F) {
	seeds := []struct {
		text string
		ins  string
		line int
		col  int
	}{
		{"", "", 0, 0},
		{"hello", " world", 0, 5},
		{"one\ntwo\nthree", "\n", 1, 1},
		{"one\ntwo\nthree", "a\nb\nc", 2, 0},
		{"日本語", "テキスト", 0, 1},
		{"e\u0301", "\u0301", 0, 1},
		{"trailing\n", "x", 1, 0},
		{"a", strings.Repeat("z", 5000), 0, 1},
		{"        ", "\t", 0, 4},
		{"lone\rcarriage\r", "\r", 0, 4},
		{"👨\u200d👩\u200d👦", "\u200d👦", 0, 5},
		{"🇯🇵", "🇺🇸", 0, 1},
		{"a\u0301", "\u0302", 0, 2},
		{"日本語", "\t", 0, 2},
		{"❤\ufe0f", "\ufe0e", 0, 1},
		{"clamp me", "x", 999, 999},
		{"clamp me", "x", -5, -5},
	}
	for _, s := range seeds {
		f.Add(s.text, s.ins, s.line, s.col)
	}

	f.Fuzz(func(t *testing.T, text, ins string, line, col int) {
		// Buffer positions are rune-indexed by design (Position.Col counts
		// runes), so every edit re-decodes its line through []rune and
		// invalid UTF-8 collapses to RuneError on the way. That lossiness
		// is a standing property of the buffer, not the behaviour this
		// target pins — normalise the inputs into the buffer's own domain
		// so the round trip below is an exact equality rather than a
		// re-statement of the encoding.
		text = string([]rune(text))
		ins = string([]rune(ins))

		buf := NewBuffer(text)
		before := buf.String()
		// The buffer is deliberately LF-normalised on load — the CR of a
		// CRLF pair is stripped and Tab.Save puts it back — so the round
		// trip is against the normalised text, not the raw input. Every
		// other byte, including a lone CR, must survive untouched.
		if want := strings.ReplaceAll(text, "\r\n", "\n"); before != want {
			t.Fatalf("NewBuffer/String is not a round trip: %q != %q", before, want)
		}

		start := buf.Clamp(Position{Line: line, Col: col})
		end := buf.InsertString(Position{Line: line, Col: col}, ins)

		if buf.LineCount() < 1 {
			t.Fatal("buffer must always hold at least one line")
		}
		if got := buf.Substring(start, end); got != ins {
			t.Fatalf("inserted span reads back %q, want %q", got, ins)
		}
		if clamped := buf.Clamp(end); clamped != end {
			t.Fatalf("insert returned out-of-buffer position %+v (clamps to %+v)", end, clamped)
		}

		got := buf.DeleteRange(start, end)
		if got != start {
			t.Fatalf("DeleteRange returned %+v, want the span start %+v", got, start)
		}
		if after := buf.String(); after != before {
			t.Fatalf("insert+delete did not round trip:\n got %q\nwant %q", after, before)
		}
	})
}
