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

// Centered returns a w×h rectangle centered in a scrW×scrH screen.
//
// The requested size is clamped to the screen BEFORE centering. Pinning
// an oversized frame to the top-left — what this used to do — leaves the
// right border, the button row and the bottom border in cells that do
// not exist, so on a phone-sized terminal a modal would silently lose
// the very controls it exists to offer. Clamping instead hands the
// prefab a rect it can lay out inside; the prefabs that carry pinned
// button columns re-place them through buttonRowCols when that happens.
//
// A zero screen dimension (the pre-resize state a freshly built overlay
// can be measured in) is treated as "unknown" and leaves the request
// alone, so nothing changes for a modal built before the first resize.
func Centered(scrW, scrH, w, h int) Rect {
	w, h = fit(w, scrW), fit(h, scrH)
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

// fit clamps a prefab's natural span — a frame width or height — to what
// the terminal actually has. Zero or negative avail means the screen
// size isn't known yet and the natural span is kept.
func fit(natural, avail int) int {
	if avail > 0 && natural > avail {
		return avail
	}
	return natural
}

// buttonRowCols lays a row of buttons out inside a frame w cells wide:
// the group is centered, the gap between neighbours shrinks (never below
// one cell) until the row fits, and the leftmost button never starts on
// the frame's own border column.
//
// Prefabs call this ONLY when the terminal forced their frame below its
// natural width. At full width they keep their pinned, hand-tuned
// columns, so nothing moves on an ordinary terminal and the geometry
// tests that spell those columns out stay true.
func buttonRowCols(w, gap int, widths []int) []int {
	n := len(widths)
	if n == 0 {
		return nil
	}
	total := 0
	for _, bw := range widths {
		total += bw
	}
	for gap > 1 && total+gap*(n-1) > w-2 {
		gap--
	}
	x := (w - (total + gap*(n-1))) / 2
	if x < 1 {
		x = 1
	}
	xs := make([]int, n)
	for i, bw := range widths {
		xs[i] = x
		x += bw + gap
	}
	return xs
}
