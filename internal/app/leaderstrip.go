// =============================================================================
// File: internal/app/leaderstrip.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// leaderstrip.go renders the leader cheat-strip: while the Esc-leader
// window is armed, the row above the status bar lists every bound key
// with a short description (druk's Ctrl+K "peek" adapted to skiff's
// Esc-only world). It clears itself when the window expires — the Esc
// handler schedules a wake-up event just past doubleEscWindow.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
)

// leaderStrip is the cheat-strip as a strip (strip.go) — and the one
// strip that deliberately never takes the slot. Two reasons, both
// structural rather than stylistic. It must not own the keyboard: a
// rune typed inside the armed window that no binding claims has to
// reach the buffer, and strip.handleKey has no way to decline a key.
// And it reserves no rows — it overlays the editor for the
// ~half-second the window is armed instead of reflowing it, and it
// expires on a timer that posts no event of its own, so nothing would
// be left to clear a slot it had taken. So draw() paints it only when
// the slot is empty, which is also how it stays out from under a bar
// that owns those rows without naming the other strips.
type leaderStrip struct{ a *App }

// rows is zero: the strip paints over the editor rather than taking
// rows off it, so layout has nothing to reserve. See the type comment.
func (s leaderStrip) rows() int { return 0 }

// handleKey is a no-op — the leader window's dispatch lives in
// handleKey / leaderWindowIntercept precisely so an unbound rune can
// fall through to the buffer, which a strip that owned the keyboard
// could not offer.
func (s leaderStrip) handleKey(*tcell.EventKey) {}

// handleMouse passes everything through: the strip is a momentary
// reference, and the editor under it stays live (ADR-0001).
func (s leaderStrip) handleMouse(int, int, tcell.ButtonMask) bool { return false }

// close is a no-op — the strip holds no state; the armed window it
// renders expires on its own.
func (s leaderStrip) close() {}

// leaderStripVisible reports whether the cheat-strip should draw:
// the leader window is armed and nothing that owns the keyboard (menu,
// modals, a docked strip — states where a leader key can't fire) is up.
func (a *App) leaderStripVisible() bool {
	if a.lastEscape.IsZero() || time.Since(a.lastEscape) >= doubleEscWindow {
		return false
	}
	if a.overlays.IsOpen() || a.strip != nil {
		return false
	}
	return true
}

// leaderStripTailHint is what the strip's last row says when even the
// keys-only form cannot fit: the one gesture that reaches the whole
// table. It is spent as a row of its own rather than appended, so it
// can never be the thing that gets clipped.
const leaderStripTailHint = "… Esc ? for all"

// stripSegment is one styled run of text on a strip row.
type stripSegment struct {
	text  string
	style tcell.Style
}

// stripStyles are the three styles a leader strip row is painted in.
type stripStyles struct {
	base, key, muted tcell.Style
}

// leaderStripForms returns the strip's candidate layouts from most to
// least verbose: the full table with " · " between entries, the same
// table with single-space separators, and the bare keys with a trailing
// pointer at the reference. leaderStripRows takes the first that fits
// its row budget, so a narrow terminal loses air before it loses
// descriptions, and descriptions before it loses keys.
func leaderStripForms(bindings []leaderBinding, st stripStyles) [][]stripSegment {
	table := func(sep string) []stripSegment {
		segs := []stripSegment{{" Esc ", st.key}}
		for i, b := range bindings {
			s := sep
			if i == 0 {
				s = " "
			}
			segs = append(segs,
				stripSegment{s, st.muted},
				stripSegment{string(b.key), st.key},
				stripSegment{" " + b.desc, st.base})
		}
		return segs
	}
	keys := []stripSegment{{" Esc ", st.key}}
	for _, b := range bindings {
		keys = append(keys, stripSegment{" ", st.muted}, stripSegment{string(b.key), st.key})
	}
	keys = append(keys, stripSegment{"  Esc ? for all", st.muted})
	return [][]stripSegment{table(" · "), table(" "), keys}
}

// wrapStripSegments breaks segs into rows of at most width cells,
// moving a segment that would cross the edge onto the next row under a
// two-cell continuation indent (the " Esc " lead's shoulder). Segments
// are never split; a single segment wider than the row is clipped by
// the paint, which is the one case the wrap cannot solve.
func wrapStripSegments(segs []stripSegment, width int) [][]stripSegment {
	var rows [][]stripSegment
	var row []stripSegment
	x := 0
	for _, seg := range segs {
		w := textdraw.Width(seg.text)
		if x+w > width && len(row) > 0 {
			rows = append(rows, row)
			row, x = nil, 2
		}
		row = append(row, seg)
		x += w
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// leaderStripRows picks the strip's rows for a terminal width cells wide
// with maxRows rows to spend: the most verbose form that fits, or — when
// even the keys-only form overruns — its first maxRows-1 rows with the
// tail hint as the last, so the strip always paints and always says
// where the rest went. clipped reports the latter. maxRows under one
// yields nothing: a strip with no row is not a strip.
func leaderStripRows(bindings []leaderBinding, width, maxRows int, st stripStyles) (rows [][]stripSegment, clipped bool) {
	if maxRows < 1 || width < 1 {
		return nil, false
	}
	forms := leaderStripForms(bindings, st)
	for _, form := range forms {
		rows = wrapStripSegments(form, width)
		if len(rows) <= maxRows {
			return rows, false
		}
	}
	rows = rows[:maxRows-1]
	rows = append(rows, []stripSegment{{leaderStripTailHint, st.muted}})
	return rows, true
}

// draw paints the key overview above the status bar. On a wide terminal
// it is one row; when the full table doesn't fit, it wraps onto more —
// the strip exists precisely for people who don't have the table
// memorised, so it prefers dropping air, then descriptions, over
// dropping bindings (leaderStripRows). It overlays the editor for the
// ~half-second the leader window is armed, which is a fair trade.
//
// r is the floor it stacks up from, not a box it paints inside: the
// table spans the full terminal width, sidebar included, and grows
// upward from the row above r. Its row budget is the smaller of what
// the editor can spare (stripRowBudget) and the rows above r that are
// not the tab bar — row 0 is never painted, and a table too tall for
// what is left ends on the tail hint rather than silently drawing
// nothing, which is what an unbounded strip did the moment one more
// binding pushed its top past the screen.
func (s leaderStrip) draw(r rect) {
	a := s.a
	if !a.leaderStripVisible() {
		return
	}
	bg := a.theme.LineHL
	st := stripStyles{
		base:  tcell.StyleDefault.Background(bg).Foreground(a.theme.Text),
		key:   tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true),
		muted: tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted),
	}
	rows, _ := leaderStripRows(leaderBindings(), a.width, min(a.stripRowBudget(), r.y-1), st)
	if len(rows) == 0 {
		return
	}
	topY := r.y - len(rows)
	for i, row := range rows {
		y := topY + i
		for x := 0; x < a.width; x++ {
			a.screen.SetContent(x, y, ' ', nil, st.base)
		}
		x := 0
		if i > 0 {
			x = 2 // continuation indent under " Esc "
		}
		for _, seg := range row {
			x = drawStripSegment(a.screen, x, y, a.width, seg.text, seg.style)
		}
	}
}

// drawStripSegment draws s at (x, y) clipped to maxW total columns and
// returns the x just past what was drawn. maxW is an absolute column, so
// the cell budget handed to textdraw is the remaining maxW-x columns —
// cluster-aware, an emoji in a description advances x by its two cells.
func drawStripSegment(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) int {
	return textdraw.DrawClipped(scr, x, y, maxW-x, s, st)
}
