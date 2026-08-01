// =============================================================================
// File: internal/app/leaderstrip.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// leaderstrip.go renders the leader cheat-strip: while the Esc-leader
// window is armed, the row above the status bar lists every bound key
// with a short description (druk's Ctrl+K "peek" adapted to skiff's
// Esc-only world). It clears itself when the window expires — the Esc
// handler schedules a wake-up event just past doubleEscMs.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// leaderStripVisible reports whether the cheat-strip should draw:
// the leader window is armed and nothing that owns the keyboard (menu,
// modals, bars — states where a leader key can't fire) is open.
func (a *App) leaderStripVisible() bool {
	if a.lastEscape.IsZero() || time.Since(a.lastEscape) >= doubleEscMs {
		return false
	}
	if a.menuOpen || a.promptOpen || a.confirmOpen || a.dirtyOpen ||
		a.formOpen || a.contextOpen || a.findOpen || a.finderOpen ||
		a.projFindOpen || a.listPickOpen || a.diffOpen || a.gitLogOpen {
		return false
	}
	return true
}

// drawLeaderStrip paints the key overview above the status bar. On a
// wide terminal it is one row; when the full table doesn't fit, it
// wraps onto a second row rather than silently dropping half the
// bindings — the strip exists precisely for people who don't have the
// table memorised. It overlays the editor for the ~half-second the
// leader window is armed, which is a fair trade.
func (a *App) drawLeaderStrip() {
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
	total := runeLen(" Esc ")
	for i, b := range leaderBindings() {
		sep := " · "
		if i == 0 {
			sep = " "
		}
		segs = append(segs,
			segment{sep, mutedStyle},
			segment{string(b.key), keyStyle},
			segment{" " + b.desc, baseStyle})
		total += runeLen(sep) + 1 + 1 + runeLen(b.desc)
	}

	rows := 1
	if total > a.width {
		rows = 2
	}
	topY := a.height - 1 - rows
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
// returns the x just past what was drawn.
func drawStripSegment(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) int {
	for _, r := range s {
		if x >= maxW {
			return x
		}
		scr.SetContent(x, y, r, nil, st)
		x++
	}
	return x
}
