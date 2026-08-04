// =============================================================================
// File: internal/scrollbar/scrollbar.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package scrollbar is the one definition of what a skiff scrollbar is:
// the thumb arithmetic, its inverse (click row → scroll offset), and the
// two glyphs every bar paints with. It is deliberately free of tcell and
// theme so both the editor's bar and the file tree's bar can import it —
// the whole point is that two bars drawn a package apart can never drift
// on where the thumb sits, how long it is, or what it looks like.
//
// Colour is NOT here: it needs a theme, and each surface pairs the
// glyphs with its own foreground (see internal/editor/scrollbar.go and
// filetree's renderScrollbar). The shape and the math are shared; the
// palette lookup stays with the renderer.
package scrollbar

// Track and Thumb are the scrollbar's glyph vocabulary. Both fill the
// whole cell, which is the entire point: the bar used to be drawn with
// box-drawing strokes (│ and ┃), and a one-pixel vertical hairline in a
// mid-grey is invisible on a dark terminal no matter how well its
// colour scores on a contrast checker. Ink coverage, not contrast
// ratio, is what makes a scrollbar readable at a glance.
//
// Track is the 25%-density light shade and Thumb the solid block, so
// the two differ in coverage (25% vs 100%) as well as in colour — the
// thumb stays findable for a colourblind reader and survives
// internal/theme/degrade.go's low-colour fallback, where hue is thrown
// away entirely. Both live in Unicode's Block Elements range, so any
// font that can draw one can draw the other.
const (
	Track = '░'
	Thumb = '█'
)

// Geom computes the thumb for a total-row list shown in a viewH-row
// viewport scrolled to scrollY: proportional length (min 1 row, always
// leaving some track), start clamped to the track. ok=false means the
// content fits and no bar should draw — a full-height thumb carries no
// information and is pure noise.
func Geom(total, viewH, scrollY int) (thumbStart, thumbLen int, ok bool) {
	if viewH <= 0 || total <= viewH {
		return 0, 0, false
	}
	thumbLen = viewH * viewH / total
	if thumbLen < 1 {
		thumbLen = 1
	}
	if thumbLen >= viewH {
		thumbLen = viewH - 1
	}
	span := viewH - thumbLen
	denom := total - viewH
	thumbStart = scrollY * span / denom
	if thumbStart < 0 {
		thumbStart = 0
	}
	if thumbStart > span {
		// Overscroll (the editor's clampScroll allows half a screen
		// past the end) pins the thumb to the bottom instead of
		// pushing it off-track.
		thumbStart = span
	}
	return thumbStart, thumbLen, true
}

// TargetForThumb maps a click at bar-local row clickY to the scroll
// offset that centers the thumb on that row, clamped to [0, total-viewH].
// The inverse of Geom's position math.
func TargetForThumb(total, viewH, clickY int) int {
	_, thumbLen, ok := Geom(total, viewH, 0)
	if !ok {
		return 0
	}
	span := viewH - thumbLen
	if span < 1 {
		return 0
	}
	pos := clickY - thumbLen/2
	if pos < 0 {
		pos = 0
	}
	if pos > span {
		pos = span
	}
	return pos * (total - viewH) / span
}
