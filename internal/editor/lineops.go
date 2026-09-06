// =============================================================================
// File: internal/editor/lineops.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// lineops.go implements whole-line editing gestures: move the current
// line (or selected block) up / down, and duplicate it below itself.
// The line span rule is shared with the comment toggle
// (commentLineRange): cursor line when nothing is selected, and a
// selection ending at column 0 doesn't drag that last line along.

package editor

import "strings"

// MoveLinesUp swaps the selected line block with the line above it.
// The cursor and anchor ride along, so a selection survives repeated
// nudges. At the top of the buffer it's a hard no-op — no dirty flag,
// no undo entry.
func (t *Tab) MoveLinesUp() {
	if t.IsImage() {
		return
	}
	first, last := t.commentLineRange()
	if first <= 0 {
		return
	}
	t.edit(undoGroupStructural, func() {
		lines := t.Buffer.Lines
		moved := lines[first-1]
		copy(lines[first-1:last], lines[first:last+1])
		lines[last] = moved
		t.Cursor.Line--
		t.Anchor.Line--
	})
}

// MoveLinesDown is the mirror gesture: the block swaps with the line
// below it, stopping dead at the bottom of the buffer.
func (t *Tab) MoveLinesDown() {
	if t.IsImage() {
		return
	}
	first, last := t.commentLineRange()
	if last >= t.Buffer.LineCount()-1 {
		return
	}
	t.edit(undoGroupStructural, func() {
		lines := t.Buffer.Lines
		moved := lines[last+1]
		copy(lines[first+1:last+2], lines[first:last+1])
		lines[first] = moved
		t.Cursor.Line++
		t.Anchor.Line++
	})
}

// DuplicateLines inserts a copy of the selected line block directly
// below itself. The cursor (and anchor) land on the copy at the same
// column, so "duplicate then edit the copy" needs no extra movement.
func (t *Tab) DuplicateLines() {
	if t.IsImage() {
		return
	}
	first, last := t.commentLineRange()
	t.edit(undoGroupStructural, func() {
		lines := t.Buffer.Lines
		span := make([]string, last-first+1)
		copy(span, lines[first:last+1])

		out := make([]string, 0, len(lines)+len(span))
		out = append(out, lines[:last+1]...)
		out = append(out, span...)
		out = append(out, lines[last+1:]...)
		t.Buffer.Lines = out

		delta := len(span)
		t.Cursor.Line += delta
		t.Anchor.Line += delta
	})
}

// IndentLines shifts the selected line block (or the cursor line) one
// IndentUnit to the right as a single structural undo step. Blank
// lines inside a multi-line block are left alone — indenting them
// would manufacture whitespace-only lines that the next save writes
// to disk — while a lone blank line is indented on request, because
// that is the only thing the gesture could possibly mean there.
// Cursor and anchor ride along with the text they sit in, so a
// selection keeps covering the same lines afterwards. Returns false
// when nothing changed, in which case no undo entry is recorded.
func (t *Tab) IndentLines() bool {
	if t.IsImage() {
		return false
	}
	first, last := t.commentLineRange()
	unit := t.IndentUnit
	if unit == "" {
		unit = defaultSpaceIndent
	}
	shifted := make(map[int]int, last-first+1)
	for i := first; i <= last; i++ {
		if t.Buffer.Lines[i] == "" && first != last {
			continue
		}
		shifted[i] = len([]rune(unit))
	}
	if len(shifted) == 0 {
		return false
	}
	t.edit(undoGroupStructural, func() {
		for i := range shifted {
			t.Buffer.Lines[i] = unit + t.Buffer.Lines[i]
		}
		t.Cursor = shiftIndentPos(t.Cursor, shifted)
		t.Anchor = shiftIndentPos(t.Anchor, shifted)
	})
	return true
}

// OutdentLines removes one level of indentation from the selected line
// block (or the cursor line) as a single structural undo step. Each
// line loses, in order of preference: one IndentUnit, one leading tab,
// or up to an indent unit's width of leading spaces — so a file whose
// indentation drifted between tabs and spaces still de-dents one level
// per press instead of stalling on the line that differs. Cursor and
// anchor ride along, never crossing back past column 0. Returns false
// when no line had indentation to remove, in which case no undo entry
// is recorded.
func (t *Tab) OutdentLines() bool {
	if t.IsImage() {
		return false
	}
	first, last := t.commentLineRange()
	unit := t.IndentUnit
	if unit == "" {
		unit = defaultSpaceIndent
	}
	removed := make(map[int]int, last-first+1)
	for i := first; i <= last; i++ {
		if n := outdentWidth(t.Buffer.Lines[i], unit); n > 0 {
			removed[i] = -n
		}
	}
	if len(removed) == 0 {
		return false
	}
	t.edit(undoGroupStructural, func() {
		for i, n := range removed {
			t.Buffer.Lines[i] = t.Buffer.Lines[i][-n:]
		}
		t.Cursor = shiftIndentPos(t.Cursor, removed)
		t.Anchor = shiftIndentPos(t.Anchor, removed)
	})
	return true
}

// outdentWidth returns how many leading BYTES one outdent removes from
// line: the whole unit when the line starts with it, one tab otherwise,
// or the run of leading spaces capped at the unit's width (TabStop for a
// tab unit, since one space for a tab would be a no-op de-dent). Every
// rune it counts is ASCII whitespace, so bytes and runes agree.
func outdentWidth(line, unit string) int {
	if strings.HasPrefix(line, unit) {
		return len(unit)
	}
	if strings.HasPrefix(line, "\t") {
		return 1
	}
	width := len(unit)
	if unit == "\t" {
		width = TabStop
	}
	n := leadingSpaces(line)
	if n > width {
		n = width
	}
	return n
}

// shiftIndentPos moves p by the column delta recorded for its line in
// shifts, clamped so an outdent can never push a caret left of column 0.
// A caret at column 0 stays there on an indent: it marks "start of the
// line" for a whole-line selection, and dragging it right would shrink
// the selection off the first line's indent.
func shiftIndentPos(p Position, shifts map[int]int) Position {
	d, ok := shifts[p.Line]
	if !ok || p.Col == 0 {
		return p
	}
	p.Col += d
	if p.Col < 0 {
		p.Col = 0
	}
	return p
}
