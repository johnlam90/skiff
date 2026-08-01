// =============================================================================
// File: internal/editor/lineops.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// lineops.go implements whole-line editing gestures: move the current
// line (or selected block) up / down, and duplicate it below itself.
// The line span rule is shared with the comment toggle
// (commentLineRange): cursor line when nothing is selected, and a
// selection ending at column 0 doesn't drag that last line along.

package editor

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
	t.pushUndo(undoGroupStructural)
	lines := t.Buffer.Lines
	moved := lines[first-1]
	copy(lines[first-1:last], lines[first:last+1])
	lines[last] = moved
	t.Cursor.Line--
	t.Anchor.Line--
	t.markLineOpEdit()
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
	t.pushUndo(undoGroupStructural)
	lines := t.Buffer.Lines
	moved := lines[last+1]
	copy(lines[first+1:last+2], lines[first:last+1])
	lines[first] = moved
	t.Cursor.Line++
	t.Anchor.Line++
	t.markLineOpEdit()
}

// DuplicateLines inserts a copy of the selected line block directly
// below itself. The cursor (and anchor) land on the copy at the same
// column, so "duplicate then edit the copy" needs no extra movement.
func (t *Tab) DuplicateLines() {
	if t.IsImage() {
		return
	}
	first, last := t.commentLineRange()
	t.pushUndo(undoGroupStructural)
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
	t.markLineOpEdit()
}

// markLineOpEdit applies the bookkeeping every successful line op
// shares: the buffer changed (dirty + re-highlight) and the cursor
// moved (so Render keeps it visible).
func (t *Tab) markLineOpEdit() {
	t.Dirty = true
	t.StyleStale = true
	t.cursorMoved = true
}
