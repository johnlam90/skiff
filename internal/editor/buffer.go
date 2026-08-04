// =============================================================================
// File: internal/editor/buffer.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package editor provides the text-buffer primitives, the syntax highlighter,
// and the Tab type that combines them with view state and rendering. The
// buffer is intentionally simple: one Go string per line. That's plenty fast
// for the review-and-light-edit workloads this editor is aimed at, and keeps
// the rendering and tokenisation code uncluttered.
package editor

import "strings"

// Position is a buffer location measured in lines and rune-indexed columns.
// Line is 0-based; Col is 0-based and counts runes (not bytes, not screen
// cells), so a multi-byte character or a CJK glyph each count as one column.
type Position struct {
	Line int
	Col  int
}

// runeCacheSlots is how many decoded lines a Buffer memoises. The cache
// is direct-mapped (slot = line % runeCacheSlots), so it only has to
// cover the lines a single frame touches: a viewport's worth of rows
// plus the bounded wrap walks around it. 128 covers a full-screen
// terminal with room to spare while bounding retained memory at "the
// 128 longest recently-read lines" instead of "4 bytes per rune of the
// whole file", which is what a cache parallel to Lines would cost.
const runeCacheSlots = 128

// runeCacheEntry is one memoised decode. src is the exact string the
// runes came from, and that is what makes the cache self-validating:
// any edit puts a different string header in Buffer.Lines[line], the
// comparison fails, and the entry is recomputed. No mutation site
// anywhere has to remember to invalidate — which matters because Lines
// is exported and written from several files.
type runeCacheEntry struct {
	line  int
	src   string
	runes []rune
}

// Buffer is a simple editable text buffer backed by one string per line.
// String-per-line keeps the surrounding code readable; Go string ops are
// fast enough that rebuilding a single line on each edit is fine for the
// file sizes this editor actually opens (review + small edits).
//
// Lines never carry a line terminator: NewBuffer strips the CR of a CRLF
// pair on load and Tab restores the file's own ending on save (see
// Tab.LineEnding), so everything between load and save deals in one
// convention.
type Buffer struct {
	Lines []string

	// runeCache memoises LineRunes decodes; see LineRunes.
	runeCache []runeCacheEntry
}

// NewBuffer constructs a Buffer from a string by splitting on newlines.
// A trailing newline produces an empty final line, mirroring how files
// commonly end and what most editors display. The CR of a CRLF pair is
// stripped: leaving it on would append an invisible extra column to
// every line of a Windows-authored file, widen every rune count, and
// get written straight back to disk. The file's real ending is recorded
// on the Tab and restored by Tab.Save.
func NewBuffer(text string) *Buffer {
	if text == "" {
		return &Buffer{Lines: []string{""}}
	}
	lines := strings.Split(text, "\n")
	// Only a segment that was actually followed by a \n can have been
	// half of a CRLF pair. A trailing \r on the final segment is a lone
	// carriage return sitting in the data — not a line ending — and has
	// to survive the round trip untouched.
	for i := range len(lines) - 1 {
		if ln := lines[i]; len(ln) > 0 && ln[len(ln)-1] == '\r' {
			lines[i] = ln[:len(ln)-1]
		}
	}
	return &Buffer{Lines: lines}
}

// String serialises the buffer back to a single LF-joined string. Use
// TextWith when writing to disk so the file keeps the ending it came in
// with.
func (b *Buffer) String() string {
	return b.TextWith("\n")
}

// TextWith serialises the buffer joined by sep. Lines hold no terminator
// of their own, so the separator alone decides the file's line ending.
func (b *Buffer) TextWith(sep string) string {
	return strings.Join(b.Lines, sep)
}

// LineCount returns the total number of lines in the buffer; always >= 1.
func (b *Buffer) LineCount() int {
	return len(b.Lines)
}

// LineRunes returns the runes of the line at i, or nil if i is out of range.
// The caller MUST treat the returned slice as read-only: it is shared with
// the buffer's decode cache, so writing through it corrupts every later
// reader of that line.
//
// The decode is memoised because the render and soft-wrap paths ask for the
// same handful of lines several times per frame — wrap.go walks the viewport
// once for EnsureVisible, again for the clamp, and again to paint — and a
// bare []rune conversion per call allocates O(viewport x lineLen) on every
// repaint. Freshness is checked against the source string instead of being
// tracked by invalidation calls, so an edit through any of the writers of
// Lines (here, comment.go, lineops.go, undo.go, or an outside caller) can
// never be served stale runes.
func (b *Buffer) LineRunes(i int) []rune {
	if i < 0 || i >= len(b.Lines) {
		return nil
	}
	src := b.Lines[i]
	if b.runeCache == nil {
		b.runeCache = make([]runeCacheEntry, runeCacheSlots)
	}
	// String equality short-circuits on length + data pointer, so a hit
	// costs a compare of two words and never touches the text itself.
	e := &b.runeCache[i%runeCacheSlots]
	if e.line == i && e.src == src {
		return e.runes
	}
	runes := []rune(src)
	e.line, e.src, e.runes = i, src, runes
	return runes
}

// Clamp adjusts a position so that Line and Col fall within the buffer.
// Col is clamped to the rune length of its line (so it can sit one past the
// last rune, which is where the cursor lives at end-of-line).
func (b *Buffer) Clamp(p Position) Position {
	if p.Line < 0 {
		p.Line = 0
	}
	if p.Line >= len(b.Lines) {
		p.Line = len(b.Lines) - 1
	}
	runes := b.LineRunes(p.Line)
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > len(runes) {
		p.Col = len(runes)
	}
	return p
}

// InsertString inserts text (which may contain newlines) at p and returns
// the position immediately after the inserted text. p is clamped first.
func (b *Buffer) InsertString(p Position, text string) Position {
	p = b.Clamp(p)
	if text == "" {
		return p
	}
	line := b.LineRunes(p.Line)
	before := string(line[:p.Col])
	after := string(line[p.Col:])

	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		b.Lines[p.Line] = before + parts[0] + after
		return Position{Line: p.Line, Col: p.Col + len([]rune(parts[0]))}
	}

	// Multi-line insert: splice new lines into the buffer.
	newLines := make([]string, 0, len(parts))
	newLines = append(newLines, before+parts[0])
	for i := 1; i < len(parts)-1; i++ {
		newLines = append(newLines, parts[i])
	}
	last := parts[len(parts)-1]
	newLines = append(newLines, last+after)

	out := make([]string, 0, len(b.Lines)+len(newLines)-1)
	out = append(out, b.Lines[:p.Line]...)
	out = append(out, newLines...)
	out = append(out, b.Lines[p.Line+1:]...)
	b.Lines = out

	return Position{Line: p.Line + len(parts) - 1, Col: len([]rune(last))}
}

// DeleteRange removes everything between a and b (in any order) and returns
// the resulting position (the smaller of the two). Both endpoints are
// clamped first; an empty range is a no-op.
func (b *Buffer) DeleteRange(a, c Position) Position {
	a = b.Clamp(a)
	c = b.Clamp(c)
	if posLess(c, a) {
		a, c = c, a
	}
	if a == c {
		return a
	}
	aRunes := b.LineRunes(a.Line)
	cRunes := b.LineRunes(c.Line)
	head := string(aRunes[:a.Col])
	tail := string(cRunes[c.Col:])

	out := make([]string, 0, len(b.Lines)-(c.Line-a.Line))
	out = append(out, b.Lines[:a.Line]...)
	out = append(out, head+tail)
	out = append(out, b.Lines[c.Line+1:]...)
	b.Lines = out
	return a
}

// Substring returns the text between a and b. The returned string is always
// in document order, regardless of the order of the inputs.
func (b *Buffer) Substring(a, c Position) string {
	a = b.Clamp(a)
	c = b.Clamp(c)
	if posLess(c, a) {
		a, c = c, a
	}
	if a == c {
		return ""
	}
	if a.Line == c.Line {
		runes := b.LineRunes(a.Line)
		return string(runes[a.Col:c.Col])
	}
	var sb strings.Builder
	aRunes := b.LineRunes(a.Line)
	sb.WriteString(string(aRunes[a.Col:]))
	sb.WriteByte('\n')
	for i := a.Line + 1; i < c.Line; i++ {
		sb.WriteString(b.Lines[i])
		sb.WriteByte('\n')
	}
	cRunes := b.LineRunes(c.Line)
	sb.WriteString(string(cRunes[:c.Col]))
	return sb.String()
}

// EndPos returns the position just after the last rune of the buffer.
// Useful for select-all and end-of-document navigation.
func (b *Buffer) EndPos() Position {
	last := len(b.Lines) - 1
	return Position{Line: last, Col: len(b.LineRunes(last))}
}

// posLess reports whether a comes before b in document order.
func posLess(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// PosLess is the exported version of posLess for use from neighbouring
// packages that need to compare positions.
func PosLess(a, b Position) bool { return posLess(a, b) }

// PosOrdered returns (a, b) sorted in document order.
func PosOrdered(a, b Position) (Position, Position) {
	if posLess(b, a) {
		return b, a
	}
	return a, b
}
