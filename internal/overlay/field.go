// =============================================================================
// File: internal/overlay/field.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"

	"github.com/johnlam90/skiff/internal/textdraw"
)

// Field is a single-line text input: value, cursor, and a horizontal
// scroll window that keeps the caret visible when the text outgrows the
// field. It replaces the per-modal copies of the same rune-editing and
// caret-window code (prompt, find, replace, finder, list pick, form).
//
// Three units, as everywhere else (CLAUDE.md "Three units"): Value
// stores RUNES and Cursor / scroll index them, the caret steps and
// deletes by grapheme CLUSTER, and Draw lays the window out in CELLS.
// Treating a rune as a cell is what painted "日本語.go" with every second
// glyph missing and put the caret three cells short of the text.
type Field struct {
	Value  []rune
	Cursor int
	// scroll is the index of the first visible rune, always a cluster
	// start. Draw maintains it; it is not part of the field's interface.
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

// clusterWindow is how many runes clusterEnd hands the segmenter. The
// longest clusters in real text (ZWJ emoji sequences, stacked combining
// marks) are well under it; a pathological longer one is merely split.
const clusterWindow = 64

// clusterEnd returns the rune index just past the grapheme cluster that
// starts at rune index i, plus that cluster's width in cells. uniseg is
// the engine tcell's cell buffer resolves widths with, so the field's
// column math and what Put paints agree by construction. At or past the
// end it returns (len, 0).
func (f *Field) clusterEnd(i int) (end, width int) {
	if i >= len(f.Value) {
		return len(f.Value), 0
	}
	// Only a bounded window is converted: one cluster never needs the
	// whole tail, and converting it on every call made each frame
	// quadratic in the field's length — a long paste into the find bar
	// froze the editor for seconds.
	tail := f.Value[i:]
	if len(tail) > clusterWindow {
		tail = tail[:clusterWindow]
	}
	cluster, _, w, _ := uniseg.FirstGraphemeClusterInString(string(tail), -1)
	n := len([]rune(cluster))
	if n == 0 {
		// Defensive: a non-empty string always yields a cluster, but a
		// zero step here would loop every caller forever.
		n = 1
	}
	return i + n, w
}

// clusterStart returns the start of the cluster containing rune index
// i — i itself when it already sits on a boundary. Clusters are found
// by walking from the front: a boundary depends on what precedes it (a
// flag is a PAIR of regional indicators), so there is no reliable way
// to step backwards from i alone, and a field's text is short.
func (f *Field) clusterStart(i int) int {
	start := 0
	for start < len(f.Value) {
		end, _ := f.clusterEnd(start)
		if end > i {
			break
		}
		start = end
	}
	return start
}

// prevBoundary returns the cluster boundary strictly before rune index
// i (0 at the start) — where Left lands and where Backspace cuts from.
func (f *Field) prevBoundary(i int) int {
	if i <= 0 {
		return 0
	}
	return f.clusterStart(i - 1)
}

// nextBoundary returns the cluster boundary strictly after rune index i
// (len at the end) — where Right lands and where Delete cuts to. It
// goes through clusterStart so a caret that typing left inside a
// cluster still leaves at that cluster's real end.
func (f *Field) nextBoundary(i int) int {
	if i >= len(f.Value) {
		return len(f.Value)
	}
	end, _ := f.clusterEnd(f.clusterStart(i))
	return end
}

// cellsBetween returns the cell width of the whole clusters between
// rune indexes from and to (from a cluster start). A cluster that
// straddles to is not counted, so a caret parked inside one paints at
// that cluster's left edge rather than part-way through a glyph.
func (f *Field) cellsBetween(from, to int) int {
	cells := 0
	for from < to {
		end, w := f.clusterEnd(from)
		if end > to {
			break
		}
		cells += w
		from = end
	}
	return cells
}

// ClickAt moves the caret to the cluster boundary under cell offset
// rel, measured from the field's left edge as Draw last painted it —
// the inverse of Draw's cell advance, so a click on either half of a
// two-cell ideograph lands the caret before that glyph and never
// between the runes of a cluster. A click past the text parks the caret
// at the end; a negative offset at the window's first rune.
func (f *Field) ClickAt(rel int) {
	i, acc := f.clusterStart(f.scroll), 0
	for i < len(f.Value) {
		end, w := f.clusterEnd(i)
		if acc+w > rel {
			break
		}
		acc += w
		i = end
	}
	f.Cursor = i
}

// HandleKey applies one key event to the field: cursor movement (Left,
// Right, Home, End), deletion (Backspace, Delete), and printable-rune
// insertion. Movement and deletion step by grapheme cluster, so the
// caret never rests inside one. Returns true when the key was consumed, false for keys the
// field doesn't own (Esc, Enter, Tab, arrows up/down) so the enclosing
// overlay can route them.
func (f *Field) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyLeft:
		f.Cursor = f.prevBoundary(f.Cursor)
	case tcell.KeyRight:
		f.Cursor = f.nextBoundary(f.Cursor)
	case tcell.KeyHome:
		f.Cursor = 0
	case tcell.KeyEnd:
		f.Cursor = len(f.Value)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		// A whole cluster goes at once: deleting one rune of "e" +
		// U+0301 or of a ZWJ family leaves a fragment no caret can sit
		// beside and the user never typed.
		if from := f.prevBoundary(f.Cursor); from < f.Cursor {
			f.Value = append(f.Value[:from], f.Value[f.Cursor:]...)
			f.Cursor = from
		}
	case tcell.KeyDelete:
		if to := f.nextBoundary(f.Cursor); to > f.Cursor {
			f.Value = append(f.Value[:f.Cursor], f.Value[to:]...)
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
// blinking caret like any other text input. The window is painted by
// whole clusters through textdraw (a wide glyph that would straddle the
// right edge is left out rather than split) and the caret x is the cell
// width of the text before it, not a rune count.
func (f *Field) Draw(scr tcell.Screen, x, y, w int, st tcell.Style, focused bool) {
	f.ensureVisible(w)
	for cx := x - 1; cx <= x+w; cx++ {
		scr.SetContent(cx, y, ' ', nil, st)
	}
	if f.scroll < len(f.Value) {
		textdraw.DrawClipped(scr, x, y, w, string(f.Value[f.scroll:]), st)
	}
	if focused {
		caret := x + f.cellsBetween(f.scroll, f.Cursor)
		if caret >= x && caret <= x+w {
			scr.ShowCursor(caret, y)
		}
	}
}

// ensureVisible slides the scroll window so the caret stays inside a
// w-cell view. The window only ever starts on a cluster boundary, and
// it advances a cluster at a time until the text before the caret fits
// in w-1 cells — the last cell is the caret's own.
func (f *Field) ensureVisible(w int) {
	if w <= 0 {
		f.scroll = 0
		return
	}
	if f.scroll > len(f.Value) {
		f.scroll = len(f.Value)
	}
	f.scroll = f.clusterStart(f.scroll)
	if f.Cursor < f.scroll {
		f.scroll = f.clusterStart(f.Cursor)
	}
	for f.scroll < f.Cursor && f.cellsBetween(f.scroll, f.Cursor) >= w {
		next, _ := f.clusterEnd(f.scroll)
		if next > f.Cursor {
			break
		}
		f.scroll = next
	}
}
