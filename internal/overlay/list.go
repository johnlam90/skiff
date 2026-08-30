// =============================================================================
// File: internal/overlay/list.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// list.go is the one definition of a scrolled, selectable row window.
//
// Five surfaces need exactly the same arithmetic — the Pick prefab, the
// action menu, the file finder, the commit-history overlay and the git
// panel's change list — and each of them used to carry its own copy:
// its own clamp, its own "keep the selection on screen", its own wheel
// branch and its own screen-row-to-index hit-test. Five copies of one
// calculation are five chances to disagree, and they already had: only
// Pick ever drew a scroll indicator, and the finder had no scroll state
// at all, so forty of its fifty results were unreachable.
//
// So the window lives here, once. A List knows four numbers — how many
// rows the content has, how many the window shows, which row is
// selected, and which row sits at the top — and every question a
// surface asks about scrolling is answered from those. What it
// deliberately does NOT know is what a row IS: the menu's rows carry
// groups and dividers, the git panel's carry change kinds, Pick's carry
// a filter. That stays with the surface, which is why embedding a List
// costs a caller nothing but the two setters that tell it the current
// shape.
//
// The scroll indicator comes from internal/scrollbar through Bar (see
// scrollbar.go), so a list's bar is the same glyphs, the same thumb
// geometry and the same click inverse as the editor's and the tree's.

package overlay

// List owns the selection and the scroll offset of a window of rows.
// It is a value type meant to be embedded: a surface declares one,
// pushes the live row count and window height in with SetLen /
// SetVisible, and then asks it everything else.
//
// Both dimensions are pushed rather than pulled because they are
// layout, and layout is the surface's business — a Pick's window comes
// from its frame, the git panel's from the sidebar minus its hint
// strip. Pushing them keeps the List free of geometry and keeps the
// derivation in the one place that already owns it.
type List struct {
	// n is how many rows the content currently has.
	n int
	// visible is how many rows the window shows at once.
	visible int
	// sel is the index of the highlighted row.
	sel int
	// scroll is the index of the row painted at the top of the window.
	scroll int
}

// SetLen records how many rows the content has. Pure: it never moves
// the selection or the scroll on its own, because a surface that has
// just narrowed a filter usually wants to re-aim the selection itself
// (Pick snaps to the first match) rather than have the list guess.
func (l *List) SetLen(n int) {
	if n < 0 {
		n = 0
	}
	l.n = n
}

// Len reports how many rows the content has.
func (l *List) Len() int { return l.n }

// SetVisible records how many rows the window shows. Callers push this
// on every resize-sensitive path so nothing downstream — the clamp, the
// hit-test, the bar — has to re-derive a height the surface already
// computed.
func (l *List) SetVisible(h int) {
	if h < 0 {
		h = 0
	}
	l.visible = h
}

// Visible reports the window height.
func (l *List) Visible() int { return l.visible }

// Sel reports the selected row index. Zero on an empty list, which is
// what every caller's own bounds check already expects.
func (l *List) Sel() int { return l.sel }

// Select moves the selection to i, clamped into the list. Clamping
// rather than refusing is deliberate: every caller reached for the
// selection because a gesture asked for a row, and the ends of a list
// are where a gesture that overshoots should land.
func (l *List) Select(i int) {
	l.sel = clampIndex(i, l.n)
}

// Move steps the selection by delta rows, clamped at both ends. No
// wrap: a list you can fall off the end of loses your place.
func (l *List) Move(delta int) { l.Select(l.sel + delta) }

// Scroll reports the index of the row at the top of the window.
func (l *List) Scroll() int { return l.scroll }

// ScrollBy nudges the window by delta rows — the wheel gesture — and
// clamps it. It deliberately leaves the selection alone: scrolling is
// looking, not choosing, and a wheel that dragged the highlight with it
// would pick a different row than the one the user then clicks.
func (l *List) ScrollBy(delta int) {
	l.scroll += delta
	l.clampScroll()
}

// MaxScroll reports the largest scroll offset that still fills the
// window — zero when the content fits, which is also the signal every
// caller uses to decide whether an overflow marker or a bar is worth
// drawing.
func (l *List) MaxScroll() int {
	if max := l.n - l.visible; max > 0 {
		return max
	}
	return 0
}

// Clamp pulls both the selection and the scroll back inside the list.
// It is the call for "the content changed under me" — a filter
// narrowed, a git refresh dropped rows — where either number may now
// point past the end.
func (l *List) Clamp() {
	l.sel = clampIndex(l.sel, l.n)
	l.clampScroll()
}

// clampScroll bounds the window offset to [0, MaxScroll]. Separate from
// Clamp because the wheel must not disturb the selection.
func (l *List) clampScroll() {
	if max := l.MaxScroll(); l.scroll > max {
		l.scroll = max
	}
	if l.scroll < 0 {
		l.scroll = 0
	}
}

// EnsureVisible slides the window the shortest distance that brings the
// selected row back on screen — the list analogue of the editor's
// EnsureVisible, and what makes arrow keys able to walk past either
// edge of a window shorter than the content.
func (l *List) EnsureVisible() {
	if l.sel < l.scroll {
		l.scroll = l.sel
	}
	if l.visible > 0 && l.sel >= l.scroll+l.visible {
		l.scroll = l.sel - l.visible + 1
	}
	l.clampScroll()
}

// RowAt maps a screen row y to the row index under it, given the screen
// row firstRowY the window's first row is painted on. ok is false when
// y falls outside the window or lands on a row the content does not
// have — a short list in a tall window, which every surface must treat
// as blank space rather than as row -1.
func (l *List) RowAt(firstRowY, y int) (idx int, ok bool) {
	if y < firstRowY || y >= firstRowY+l.visible {
		return -1, false
	}
	i := l.scroll + (y - firstRowY)
	if i < 0 || i >= l.n {
		return -1, false
	}
	return i, true
}

// Bar describes this list's scroll indicator painted in column x with
// its first row on screen row top. The returned value carries the
// window the list is showing, so a caller never re-derives the numbers
// the bar is measuring — which is exactly how the painted cells and the
// clickable cells used to drift apart.
func (l *List) Bar(x, top int) Bar {
	return Bar{x: x, top: top, viewH: l.visible, total: l.n, scroll: l.scroll}
}

// ScrollToBar answers a press on the indicator: scroll so the thumb
// centers on screen row y, with top the bar's first screen row. The
// inverse of Bar's geometry, shared with the editor and the tree so
// click-to-jump feels identical everywhere.
func (l *List) ScrollToBar(top, y int) {
	l.scroll = l.Bar(0, top).Target(y)
	l.clampScroll()
}

// clampIndex bounds i to a valid row of an n-row list, answering 0 for
// an empty one. The one place the "empty list selects row zero"
// convention is written down.
func clampIndex(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}
