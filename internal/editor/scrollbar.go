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
// geometry and the glyphs come from internal/scrollbar so the file
// tree's bar and this one can never disagree; Render calls
// renderScrollbar; the mouse mapping lives in the app layer via
// ScrollTargetForClick.

package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// gitMarkGlyph is the half-block the change map paints over the bar. It
// is deliberately a DIFFERENT shape from the thumb's full block: a mark
// landing on the thumb has to stay readable as a mark rather than a
// discoloured piece of thumb, and half-vs-full is a shape difference
// that survives a monochrome terminal.
const gitMarkGlyph = '▐'

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
	return scrollbar.TargetForThumb(t.Buffer.LineCount(), viewH, clickY)
}

// renderScrollbar paints the bar column at x: a light-shade track, a
// solid proportional thumb, and the file's git change marks scaled onto
// it. Marks win over the thumb — they're the "somewhere to scroll to"
// signal — but on a thumb row they keep the thumb's colour as their
// background, so a mark reads as a coloured notch ON the bar instead of
// a hole punched THROUGH it.
//
// The thumb brightens to Accent while the user is dragging it, matching
// the sidebar splitter's idle-Subtle / dragging-Accent language so
// "this is the thing under my finger" reads the same everywhere.
func (t *Tab) renderScrollbar(scr tcell.Screen, th theme.Theme, x, y, viewH int) {
	total := t.Buffer.LineCount()
	thumbStart, thumbLen, ok := scrollbar.Geom(total, viewH, t.ScrollY)
	if !ok {
		return
	}
	thumbFg := th.Muted
	if t.ScrollbarActive {
		thumbFg = th.Accent
	}
	trackStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Subtle)
	thumbStyle := tcell.StyleDefault.Background(th.BG).Foreground(thumbFg)
	onThumb := func(row int) bool { return row >= thumbStart && row < thumbStart+thumbLen }
	for row := 0; row < viewH; row++ {
		r, st := scrollbar.Track, trackStyle
		if onThumb(row) {
			r, st = scrollbar.Thumb, thumbStyle
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
		bg := th.BG
		if onThumb(row) {
			bg = thumbFg
		}
		st := tcell.StyleDefault.Background(bg).Foreground(gitLineMarkerColor(th, kind))
		scr.SetContent(x, y+row, gitMarkGlyph, nil, st)
	}
}
