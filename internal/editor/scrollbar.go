// =============================================================================
// File: internal/editor/scrollbar.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// scrollbar.go gives long files a one-column scrollbar on the editor's
// right edge: a proportional thumb plus the file's git change marks
// scaled onto the track — so "where are the changes in this file" is
// answered at a glance and every mark is somewhere to click. The
// geometry is pure math (testable without a screen); Render calls
// renderScrollbar; the mouse mapping lives in the app layer via
// ScrollTargetForClick.

package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// scrollbarGeom computes the thumb for a total-line buffer in a
// viewH-row viewport at scrollY: proportional length (min 1 row, always
// leaving some track), start clamped to the track. ok=false means the
// file fits and no bar should draw.
func scrollbarGeom(total, viewH, scrollY int) (thumbStart, thumbLen int, ok bool) {
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
		// Overscroll (clampScroll allows half a screen past the end)
		// pins the thumb to the bottom instead of pushing it off-track.
		thumbStart = span
	}
	return thumbStart, thumbLen, true
}

// scrollTargetForThumb maps a click at bar-local row clickY to the
// ScrollY that centers the thumb on that row, clamped to [0, max
// scroll]. The inverse of scrollbarGeom's position math.
func scrollTargetForThumb(total, viewH, clickY int) int {
	_, thumbLen, ok := scrollbarGeom(total, viewH, 0)
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

// ScrollbarVisible reports whether the tab draws a scrollbar in a
// viewH-row viewport — text tabs taller than the view only.
func (t *Tab) ScrollbarVisible(viewH int) bool {
	if t.IsImage() {
		return false
	}
	return viewH > 0 && t.Buffer.LineCount() > viewH
}

// ScrollTargetForClick maps a click at bar-local row clickY to the
// ScrollY it requests — the app's mouse handler funnels both the
// initial press and thumb drags through this.
func (t *Tab) ScrollTargetForClick(viewH, clickY int) int {
	return scrollTargetForThumb(t.Buffer.LineCount(), viewH, clickY)
}

// renderScrollbar paints the bar column at x: a subtle track, a heavier
// proportional thumb, and the file's git change marks scaled onto it.
// Marks win over the thumb — they're the "somewhere to scroll to"
// signal, and the thumb's position is still legible around them.
func (t *Tab) renderScrollbar(scr tcell.Screen, th theme.Theme, x, y, viewH int) {
	total := t.Buffer.LineCount()
	thumbStart, thumbLen, ok := scrollbarGeom(total, viewH, t.ScrollY)
	if !ok {
		return
	}
	trackStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Subtle)
	thumbStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Muted)
	for row := 0; row < viewH; row++ {
		r, st := '│', trackStyle
		if row >= thumbStart && row < thumbStart+thumbLen {
			r, st = '┃', thumbStyle
		}
		scr.SetContent(x, y+row, r, nil, st)
	}
	// Collapse the change map onto track rows first so the paint order
	// (and therefore the color at a shared row) is deterministic — the
	// highest-severity kind wins, not whichever map key iterated last.
	marks := make([]GitLineChange, viewH)
	for line, kind := range t.GitLines {
		if kind == GitLineNone || line < 0 || line >= total {
			continue
		}
		row := line * viewH / total
		if row >= viewH {
			row = viewH - 1
		}
		if kind > marks[row] {
			marks[row] = kind
		}
	}
	for row, kind := range marks {
		if kind == GitLineNone {
			continue
		}
		st := tcell.StyleDefault.Background(th.BG).Foreground(gitLineMarkerColor(th, kind))
		scr.SetContent(x, y+row, '▐', nil, st)
	}
}
