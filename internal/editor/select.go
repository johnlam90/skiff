// =============================================================================
// File: internal/editor/select.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// select.go holds the whole-buffer and whole-line selection gestures.
// Both are pure view changes: they move the anchor and the caret and
// never touch the buffer, so they record no undo state — but they do
// break the open coalescing group, because a selection is where the
// user's next edit starts from.

package editor

// SelectAll selects the entire buffer: anchor at the origin, caret just
// past the last rune. The caret goes to the END so the view follows the
// gesture the way every other editor does — Cut or Copy work from
// either end, and the user is left looking at the tail of what they
// grabbed. No-op on image tabs, which have nothing to select.
func (t *Tab) SelectAll() {
	if t.IsImage() {
		return
	}
	t.Anchor = Position{}
	t.MoveCursorTo(t.Buffer.EndPos(), true)
}

// SelectLine selects the caret's whole line: anchor at column 0, caret
// at column 0 of the NEXT line so the selection carries the newline and
// a Cut removes the line rather than leaving an empty one behind. On the
// last line there is no newline to carry, so the caret stops at the
// line's end. No-op on image tabs.
func (t *Tab) SelectLine() {
	if t.IsImage() {
		return
	}
	line := t.Buffer.Clamp(t.Cursor).Line
	t.Anchor = Position{Line: line}
	end := Position{Line: line + 1}
	if line >= t.Buffer.LineCount()-1 {
		end = Position{Line: line, Col: len(t.Buffer.LineRunes(line))}
	}
	t.MoveCursorTo(end, true)
}
