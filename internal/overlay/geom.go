// =============================================================================
// File: internal/overlay/geom.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

// Rect is an overlay's on-screen rectangle in cells. Overlays compute
// their own Rect from the screen size; the hit-testing and centering
// math lives here so it exists exactly once.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether the cell (x, y) lies inside the rectangle —
// the outside-click dismissal test every overlay shares.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Centered returns a w×h rectangle centered in a scrW×scrH screen,
// clamped so a modal larger than the screen pins to the top-left rather
// than falling off it.
func Centered(scrW, scrH, w, h int) Rect {
	x := (scrW - w) / 2
	y := (scrH - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return Rect{X: x, Y: y, W: w, H: h}
}
