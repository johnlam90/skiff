// =============================================================================
// File: internal/filetree/scrollbar.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// minScrollbarWidth is the narrowest tree rect that still gets a
// scrollbar. At the app's minSidebarWidth (18) the rect is 17 columns
// and labels keep 16 — plenty — so the bar is present at every width a
// user can drag to. The floor only exists so a pathologically narrow
// rect (a tiny terminal, a test fixture) spends its cells on names
// instead of on a bar with nothing left to point at.
const minScrollbarWidth = 6

// scrollbarVisible reports whether a w-wide rect with a listH-row list
// area draws a bar: only when the last flatten produced more rows than
// fit, and only when the rect can spare the column. A full-height thumb
// says nothing, so it is not drawn — the same rule the editor follows.
func (t *Tree) scrollbarVisible(w, listH int) bool {
	if w < minScrollbarWidth {
		return false
	}
	_, _, ok := scrollbar.Geom(t.flatCount, listH, t.ScrollY)
	return ok
}

// ScrollbarVisible reports whether the tree draws a scrollbar in a w×h
// render rect. The app uses it to decide whether the sidebar's
// rightmost column is a scroll target or an ordinary tree row.
func (t *Tree) ScrollbarVisible(w, h int) bool {
	_, listH := listArea(h)
	return t.scrollbarVisible(w, listH)
}

// ScrollbarGrabWidth is how many columns answer to the bar: the one it
// is painted in plus the one to its left. A one-cell grab is a miss on
// a phone and a near-miss on a trackpad, and the row label's last cell
// is the cheapest neighbour to give up — the row is still clickable
// everywhere else.
const ScrollbarGrabWidth = 2

// ScrollbarHit reports whether a click at rect-local (localX, localY)
// inside a w×h render rect landed on the scrollbar. The bar is painted
// in the rect's rightmost column and answers to ScrollbarGrabWidth
// columns ending there. The sidebar's resize splitter lives outside
// this rect (see App.sidebarRect); the app gives the splitter the
// painted column when both want it, which is why the grab extends
// leftward rather than right.
func (t *Tree) ScrollbarHit(localX, localY, w, h int) bool {
	listOff, listH := listArea(h)
	if !t.scrollbarVisible(w, listH) {
		return false
	}
	return localX > w-1-ScrollbarGrabWidth && localX <= w-1 &&
		localY >= listOff && localY < listOff+listH
}

// ScrollToBarRow scrolls the list so the thumb centers on the rect-local
// row localY of a w×h render rect — the click-to-jump and drag path,
// which are the same gesture as far as the bar is concerned. No-op when
// no bar is drawn.
func (t *Tree) ScrollToBarRow(w, h, localY int) {
	listOff, listH := listArea(h)
	if !t.scrollbarVisible(w, listH) {
		return
	}
	t.ScrollY = scrollbar.TargetForThumb(t.flatCount, listH, localY-listOff)
}

// renderScrollbar paints the list area's one-column bar at x: the same
// shaded track and solid thumb the editor draws, on the sidebar's own
// background so the column reads as part of the panel. The thumb
// brightens to Accent while the user drags it, matching both the editor
// bar and the resize splitter.
func (t *Tree) renderScrollbar(scr tcell.Screen, th theme.Theme, x, y, listH int) {
	thumbStart, thumbLen, ok := scrollbar.Geom(t.flatCount, listH, t.ScrollY)
	if !ok {
		return
	}
	thumbFg := th.Muted
	if t.ScrollbarActive {
		thumbFg = th.Accent
	}
	trackStyle := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	thumbStyle := tcell.StyleDefault.Background(th.SidebarBG).Foreground(thumbFg)
	for row := 0; row < listH; row++ {
		r, st := scrollbar.Track, trackStyle
		if row >= thumbStart && row < thumbStart+thumbLen {
			r, st = scrollbar.Thumb, thumbStyle
		}
		scr.SetContent(x, y+row, r, nil, st)
	}
}
