// =============================================================================
// File: internal/overlay/field.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import "github.com/gdamore/tcell/v2"

// Field is a single-line text input: value, cursor, and a horizontal
// scroll window that keeps the caret visible when the text outgrows the
// field. It replaces the per-modal copies of the same rune-editing and
// caret-window code (prompt, find, replace, finder, list pick, form).
type Field struct {
	Value  []rune
	Cursor int
	// scroll is the index of the first visible rune. Draw maintains it;
	// it is not part of the field's interface.
	scroll int
}

// SetText replaces the field's content and parks the cursor at the end —
// the convention every opener used when pre-filling input.
func (f *Field) SetText(s string) {
	f.Value = []rune(s)
	f.Cursor = len(f.Value)
	f.scroll = 0
}

// Text returns the field's content as a string.
func (f *Field) Text() string { return string(f.Value) }

// Scroll exposes the first visible rune index for tests and callers
// that hit-test clicks inside the field.
func (f *Field) Scroll() int { return f.scroll }

// HandleKey applies one key event to the field: cursor movement (Left,
// Right, Home, End), deletion (Backspace, Delete), and printable-rune
// insertion. Returns true when the key was consumed, false for keys the
// field doesn't own (Esc, Enter, Tab, arrows up/down) so the enclosing
// overlay can route them.
func (f *Field) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyLeft:
		if f.Cursor > 0 {
			f.Cursor--
		}
	case tcell.KeyRight:
		if f.Cursor < len(f.Value) {
			f.Cursor++
		}
	case tcell.KeyHome:
		f.Cursor = 0
	case tcell.KeyEnd:
		f.Cursor = len(f.Value)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if f.Cursor > 0 {
			f.Value = append(f.Value[:f.Cursor-1], f.Value[f.Cursor:]...)
			f.Cursor--
		}
	case tcell.KeyDelete:
		if f.Cursor < len(f.Value) {
			f.Value = append(f.Value[:f.Cursor], f.Value[f.Cursor+1:]...)
		}
	case tcell.KeyRune:
		r := ev.Rune()
		// Control runes (a pasted ^C) are input noise, not content.
		if r < 0x20 {
			return true
		}
		next := make([]rune, 0, len(f.Value)+1)
		next = append(next, f.Value[:f.Cursor]...)
		next = append(next, r)
		next = append(next, f.Value[f.Cursor:]...)
		f.Value = next
		f.Cursor++
	default:
		return false
	}
	return true
}

// Draw renders the field at (x, y), w cells wide: a cleared strip with
// one cell of padding either side, the visible window of the value, and
// — when focused — the terminal cursor at the caret so the user sees a
// blinking caret like any other text input.
func (f *Field) Draw(scr tcell.Screen, x, y, w int, st tcell.Style, focused bool) {
	f.ensureVisible(w)
	for cx := x - 1; cx <= x+w; cx++ {
		scr.SetContent(cx, y, ' ', nil, st)
	}
	for i := 0; i < w; i++ {
		idx := f.scroll + i
		if idx >= len(f.Value) {
			break
		}
		scr.SetContent(x+i, y, f.Value[idx], nil, st)
	}
	if focused {
		caret := x + (f.Cursor - f.scroll)
		if caret >= x && caret <= x+w {
			scr.ShowCursor(caret, y)
		}
	}
}

// ensureVisible slides the scroll window so the caret stays inside a
// w-cell view.
func (f *Field) ensureVisible(w int) {
	if w <= 0 {
		f.scroll = 0
		return
	}
	if f.Cursor < f.scroll {
		f.scroll = f.Cursor
	}
	if f.Cursor >= f.scroll+w {
		f.scroll = f.Cursor - w + 1
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}
