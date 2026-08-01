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
		a.diffOpen || a.gitLogOpen {
		return false
	}
	return true
}

// drawLeaderStrip paints the one-row key overview on the row above the
// status bar, spanning the full window width. Entries render until the
// row runs out — on a narrow terminal the tail is simply clipped, which
// beats wrapping onto the editor.
func (a *App) drawLeaderStrip() {
	if !a.leaderStripVisible() {
		return
	}
	y := a.height - 2
	if y < 0 {
		return
	}
	bg := a.theme.LineHL
	baseStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	keyStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	for x := 0; x < a.width; x++ {
		a.screen.SetContent(x, y, ' ', nil, baseStyle)
	}

	x := 0
	x = drawStripSegment(a.screen, x, y, a.width, " Esc ", keyStyle)
	for i, b := range leaderBindings() {
		if i > 0 {
			x = drawStripSegment(a.screen, x, y, a.width, " · ", mutedStyle)
		} else {
			x = drawStripSegment(a.screen, x, y, a.width, " ", mutedStyle)
		}
		x = drawStripSegment(a.screen, x, y, a.width, string(b.key), keyStyle)
		x = drawStripSegment(a.screen, x, y, a.width, " "+b.desc, baseStyle)
		if x >= a.width {
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
