// =============================================================================
// File: internal/overlay/scrollbar.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// scrollbar.go is the prefab overlays' scroll indicator. Confirm, Info
// and Pick all window a longer list into a shorter frame, and all three
// used to paint nothing at all to say so — the content below the fold
// was reachable by wheel and arrow key but invisible to anyone who did
// not already know it was there. Worst on Confirm, where the body is
// the argv a format-on-save prompt is asking permission to run: a row
// the user cannot see is a row the user cannot consent to.
//
// The geometry and the glyphs come from internal/scrollbar, the same
// package the editor's bar and the file tree's use, so a scrollbar
// reads identically everywhere in skiff. What lives here is the part
// that is specific to a framed modal: which column the bar owns, and
// the theme pairing it paints with.

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// bodyBar describes one prefab's scroll indicator: where it is painted
// and what content it measures. Each surface derives it from its own
// rect in a single `bar` method that Draw and HandleMouse both call, so
// the painted cells and the clickable cells cannot drift apart — the
// same one-definition rule internal/scrollbar enforces between the
// editor's bar and the tree's.
type bodyBar struct {
	x      int // screen column the bar owns
	top    int // screen row of the first windowed row
	viewH  int // how many rows the window shows
	total  int // how many rows the content has
	scroll int // index of the first visible row
}

// barColumn returns the column a framed overlay's indicator owns: the
// right-hand padding cell, between the last text column and the border.
// Every prefab draws its content at r.X+2 clipped to r.W-4 cells, so
// that cell is already blank — the bar costs no layout, which is
// exactly what lets it appear on surfaces whose geometry is pinned.
func barColumn(r Rect) int { return r.X + r.W - 2 }

// drawn reports whether the bar is painted at all. Content that fits
// gets nothing: a full-height thumb carries no information, which is
// the rule the editor and the tree already follow through Geom's ok.
func (b bodyBar) drawn() bool {
	_, _, ok := scrollbar.Geom(b.total, b.viewH, b.scroll)
	return ok
}

// hit reports whether (x, y) landed on the bar. False when no bar is
// drawn, so an unused column keeps doing whatever the surface normally
// does with it — Pick's rows, for one, run all the way out to the
// padding and must stay clickable when the list fits.
func (b bodyBar) hit(x, y int) bool {
	return x == b.x && y >= b.top && y < b.top+b.viewH && b.drawn()
}

// target maps a press at screen row y to the scroll offset that centers
// the thumb there: click-to-jump, and — because tcell keeps reporting
// the button mask as the pointer moves — thumb dragging for as long as
// the pointer stays on the column.
func (b bodyBar) target(y int) int {
	return scrollbar.TargetForThumb(b.total, b.viewH, y-b.top)
}

// draw paints the bar on the modal body background: the shaded track in
// the dimmer secondary color and the solid thumb in the brighter one,
// the same glyph pair and the same Subtle/Muted pairing the editor's
// bar uses. No new theme color is involved — the two roles already
// exist, and the readability comes from ink coverage (25% shade vs full
// block) rather than from hue.
func (b bodyBar) draw(scr tcell.Screen, th theme.Theme) {
	thumbStart, thumbLen, ok := scrollbar.Geom(b.total, b.viewH, b.scroll)
	if !ok {
		return
	}
	bg := th.LineHL
	trackStyle := tcell.StyleDefault.Background(bg).Foreground(th.Subtle)
	thumbStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)
	for row := range b.viewH {
		r, st := scrollbar.Track, trackStyle
		if row >= thumbStart && row < thumbStart+thumbLen {
			r, st = scrollbar.Thumb, thumbStyle
		}
		scr.SetContent(b.x, b.top+row, r, nil, st)
	}
}
