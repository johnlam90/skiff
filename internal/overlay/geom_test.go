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
