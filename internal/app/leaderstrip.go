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

// draw paints the key overview above the status bar. On a wide terminal
// it is one row; when the full table doesn't fit, it wraps onto as many
// rows as the table needs rather than silently dropping bindings — the
// strip exists precisely for people who don't have the table memorised,
// so a fixed row cap that clips the tail defeats it. It overlays the
// editor for the ~half-second the leader window is armed, which is a
// fair trade.
//
// r is the floor it stacks up from, not a box it paints inside: the
// table spans the full terminal width, sidebar included, and grows
// upward from the row above r.
func (s leaderStrip) draw(r rect) {
	a := s.a
	if !a.leaderStripVisible() {
		return
	}
	bg := a.theme.LineHL
	baseStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	keyStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	type segment struct {
		text  string
		style tcell.Style
	}
	segs := []segment{{" Esc ", keyStyle}}
	for i, b := range leaderBindings() {
		sep := " · "
		if i == 0 {
			sep = " "
		}
		segs = append(segs,
			segment{sep, mutedStyle},
			segment{string(b.key), keyStyle},
			segment{" " + b.desc, baseStyle})
	}

	// Two passes over the same wrap rule: first count the rows the whole
	// table needs, then paint. Keeping the passes rule-identical is what
	// guarantees the paint loop never runs out of rows mid-table.
	rows := 1
	simX := 0
	for _, seg := range segs {
		w := runeLen(seg.text)
		if simX+w > a.width {
			rows++
			simX = 2 // continuation indent under " Esc "
		}
		simX += w
	}
	topY := r.y - rows
	if topY < 0 {
		return
	}
	for r := 0; r < rows; r++ {
		for x := 0; x < a.width; x++ {
			a.screen.SetContent(x, topY+r, ' ', nil, baseStyle)
		}
	}
	x, y := 0, topY
	for _, seg := range segs {
		w := runeLen(seg.text)
		if x+w > a.width && y < topY+rows-1 {
			x, y = 2, y+1 // continuation indent under " Esc "
		}
		x = drawStripSegment(a.screen, x, y, a.width, seg.text, seg.style)
		if x >= a.width && y == topY+rows-1 {
			break
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
