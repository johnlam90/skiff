// =============================================================================
// File: internal/scrollbar/scrollbar_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the shared scrollbar math: thumb proportions, the hide rule,
// the click → scroll inverse, and the round-trip between the two.

package scrollbar

import "testing"

// TestGeomProportions pins the thumb arithmetic every bar in the app
// depends on: proportional length, top of the track at scroll 0, bottom
// of the track at (and past) max scroll.
func TestGeomProportions(t *testing.T) {
	// 100 rows in a 20-row viewport: thumb 20*20/100 = 4 rows.
	start, length, ok := Geom(100, 20, 0)
	if !ok {
		t.Fatal("bar should exist when total > viewH")
	}
	if length != 4 {
		t.Fatalf("thumb length: got %d, want 4", length)
	}
	if start != 0 {
		t.Fatalf("thumb at scroll 0 should start at 0, got %d", start)
	}
	start, length, _ = Geom(100, 20, 80)
	if start != 20-length {
		t.Fatalf("thumb at max scroll: start %d, want %d", start, 20-length)
	}
	start, _, _ = Geom(100, 20, 200)
	if start != 20-length {
		t.Fatalf("overscrolled thumb start %d, want %d", start, 20-length)
	}
}

// TestGeomHiddenWhenFits: content shorter than (or exactly as tall as)
// the viewport has no bar, and a zero/negative viewport never draws one.
func TestGeomHiddenWhenFits(t *testing.T) {
	for _, tc := range []struct{ total, viewH int }{{10, 20}, {20, 20}, {100, 0}, {100, -3}} {
		if _, _, ok := Geom(tc.total, tc.viewH, 0); ok {
			t.Fatalf("Geom(%d, %d): bar should hide", tc.total, tc.viewH)
		}
	}
}

// TestGeomThumbAlwaysLeavesTrack: a barely-overflowing list still shows
// a row of track, otherwise the thumb fills the column and the bar
// stops saying anything.
func TestGeomThumbAlwaysLeavesTrack(t *testing.T) {
	_, length, ok := Geom(21, 20, 0)
	if !ok {
		t.Fatal("21 rows in 20 should draw a bar")
	}
	if length != 19 {
		t.Fatalf("thumb length %d, want 19 (one row of track left over)", length)
	}
	// A viewport far taller than the overflow still gets a 1-row thumb.
	if _, length, _ = Geom(100000, 5, 0); length != 1 {
		t.Fatalf("tiny thumb should floor at 1 row, got %d", length)
	}
}

// TestTargetForThumbClamps maps bar clicks to a scroll offset: top → 0,
// bottom → max scroll, middle → proportional, off-track → clamped.
func TestTargetForThumbClamps(t *testing.T) {
	if got := TargetForThumb(100, 20, 0); got != 0 {
		t.Fatalf("top click: got %d, want 0", got)
	}
	if got := TargetForThumb(100, 20, 19); got != 80 {
		t.Fatalf("bottom click: got %d, want 80 (max scroll)", got)
	}
	mid := TargetForThumb(100, 20, 10)
	if mid <= 0 || mid >= 80 {
		t.Fatalf("middle click should land inside the range, got %d", mid)
	}
	if got := TargetForThumb(100, 20, -5); got != 0 {
		t.Fatalf("off-track click must clamp, got %d", got)
	}
	if got := TargetForThumb(10, 20, 5); got != 0 {
		t.Fatalf("no bar means no scroll, got %d", got)
	}
}

// TestTargetRoundTrip: clicking a track row and then asking Geom where
// the thumb went must land the thumb on (or adjacent to) the clicked
// row. This is the property that makes drag feel glued to the pointer;
// integer division is why "adjacent" is the honest bound.
func TestTargetRoundTrip(t *testing.T) {
	const total, viewH = 400, 22
	_, thumbLen, _ := Geom(total, viewH, 0)
	for click := 0; click < viewH; click++ {
		scroll := TargetForThumb(total, viewH, click)
		start, _, ok := Geom(total, viewH, scroll)
		if !ok {
			t.Fatalf("click %d: bar vanished", click)
		}
		want := click - thumbLen/2
		if want < 0 {
			want = 0
		}
		if max := viewH - thumbLen; want > max {
			want = max
		}
		if diff := start - want; diff < -1 || diff > 1 {
			t.Fatalf("click %d: thumb at %d, want ~%d", click, start, want)
		}
	}
}

// TestGlyphsFillTheCell guards the fix this package exists for: the
// track and thumb must be block elements (full-cell ink), not the
// box-drawing hairlines the editor used to paint, and they must differ
// from each other.
func TestGlyphsFillTheCell(t *testing.T) {
	if Track == Thumb {
		t.Fatal("track and thumb must be distinguishable glyphs")
	}
	for name, r := range map[string]rune{"Track": Track, "Thumb": Thumb} {
		if r < 0x2580 || r > 0x259F {
			t.Fatalf("%s = %q is not a Block Elements glyph — a hairline is what made the bar invisible", name, r)
		}
	}
}
