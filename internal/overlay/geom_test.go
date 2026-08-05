// =============================================================================
// File: internal/overlay/geom_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import "testing"

// TestRect_Contains pins the half-open hit test: the left/top edges are
// inside, the right/bottom edges are one past — matching how every modal
// checked outside-clicks (x >= mx+mw is outside).
func TestRect_Contains(t *testing.T) {
	r := Rect{X: 10, Y: 5, W: 4, H: 3}
	inside := [][2]int{{10, 5}, {13, 7}, {11, 6}}
	outside := [][2]int{{9, 5}, {14, 5}, {10, 4}, {10, 8}, {0, 0}}
	for _, p := range inside {
		if !r.Contains(p[0], p[1]) {
			t.Errorf("(%d,%d) should be inside %+v", p[0], p[1], r)
		}
	}
	for _, p := range outside {
		if r.Contains(p[0], p[1]) {
			t.Errorf("(%d,%d) should be outside %+v", p[0], p[1], r)
		}
	}
}

// TestCentered pins the shared centering math, including the clamp that
// pins an oversized modal to the origin instead of a negative offset —
// the exact behavior all nine hand-rolled modal-rect copies implemented.
func TestCentered(t *testing.T) {
	r := Centered(100, 40, 54, 9)
	if r != (Rect{X: 23, Y: 15, W: 54, H: 9}) {
		t.Fatalf("centered rect wrong: %+v", r)
	}
	small := Centered(40, 6, 54, 9)
	if small.X != 0 || small.Y != 0 {
		t.Fatalf("oversized modal must clamp to origin, got %+v", small)
	}
}

// TestCentered_ClampsSizeToScreen is the phone case: a prefab whose
// natural frame is bigger than the terminal must come back at the
// terminal's size, not merely pinned to the origin. Pinning alone leaves
// the right border, the button row and the bottom border in cells that
// do not exist, so the modal loses the very controls it exists to offer.
func TestCentered_ClampsSizeToScreen(t *testing.T) {
	r := Centered(40, 10, 60, 13)
	if r != (Rect{X: 0, Y: 0, W: 40, H: 10}) {
		t.Fatalf("oversized modal must clamp to the screen, got %+v", r)
	}
	if r.X+r.W > 40 || r.Y+r.H > 10 {
		t.Fatalf("frame runs off the screen: %+v", r)
	}
}

// TestCentered_UnknownScreenKeepsRequest guards the pre-resize state: a
// prefab measured before the first EventResize reports a zero screen, and
// treating that as "the screen is zero cells" would collapse every frame
// to nothing.
func TestCentered_UnknownScreenKeepsRequest(t *testing.T) {
	if r := Centered(0, 0, 54, 9); r.W != 54 || r.H != 9 {
		t.Fatalf("zero screen must leave the request alone, got %+v", r)
	}
}

// TestButtonRowCols_CentersAndFits pins the fallback layout the prefabs
// use once a narrow terminal costs them their pinned columns: the group
// is centered, the preferred gap is kept when it fits, and every button
// lands inside the frame with the border column clear.
func TestButtonRowCols_CentersAndFits(t *testing.T) {
	cases := []struct {
		name   string
		frame  int
		gap    int
		widths []int
	}{
		{"confirm at its floor", 40, 6, []int{8, 7}},
		{"prompt at its floor", 40, 6, []int{10, 8}},
		{"three buttons, gap must shrink", 40, 8, []int{10, 11, 8}},
		{"three buttons, barely fits", 35, 8, []int{10, 11, 8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			xs := buttonRowCols(c.frame, c.gap, c.widths)
			if len(xs) != len(c.widths) {
				t.Fatalf("got %d columns for %d buttons", len(xs), len(c.widths))
			}
			if xs[0] < 1 {
				t.Fatalf("first button starts on the border at x=%d", xs[0])
			}
			for i := range xs {
				if end := xs[i] + c.widths[i]; end > c.frame-1 {
					t.Fatalf("button %d ends at %d, past the %d-cell frame", i, end, c.frame)
				}
				if i > 0 && xs[i] < xs[i-1]+c.widths[i-1]+1 {
					t.Fatalf("buttons %d and %d overlap or touch: %v", i-1, i, xs)
				}
			}
			// Centered: the slack left of the first button and right of
			// the last differ by at most a cell of rounding.
			last := len(xs) - 1
			left, right := xs[0], c.frame-(xs[last]+c.widths[last])
			if left-right > 1 || right-left > 1 {
				t.Fatalf("row is not centered: %d cells left, %d right (%v)", left, right, xs)
			}
		})
	}
}
