// =============================================================================
// File: internal/app/sidebartoggle.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// sidebartoggle.go is the one-glyph mouse handle for the explorer's
// show/hide toggle. The toggle itself has always existed (≡ → Hide file
// explorer, Esc t); what was missing was a surface the eye lands on
// without opening a menu. The handle is a chevron that points the way
// the panel will move, and it lives at the bottom-left of the window in
// both states, the way herdr's sidebar handle does: « right-aligned on
// the sidebar's footer row while the explorer is open, » in the status
// bar's first cell once it is collapsed. Both route to
// menuToggleSidebar, so the chevron can never disagree with the menu
// row about what a toggle means.
//
// The « is painted in Muted rather than Accent on purpose: the ≡ is the
// loud button, this is a hint that a panel edge is there. The » takes
// the status bar's own foreground so it reads as part of the bar on
// every palette, including the ones whose bar is a bright accent block.
// The two glyphs are Latin-1 (U+00AB / U+00BB), one cell wide
// everywhere, so no terminal renders the handle as a box.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
)

const (
	// sidebarCollapseGlyph is the open-state handle, at the footer's
	// right edge; sidebarExpandGlyph is the collapsed-state handle, at
	// the status bar's left edge.
	sidebarCollapseGlyph = '«'
	sidebarExpandGlyph   = '»'
	// sidebarToggleWidth is the cells the « occupies: the glyph and one
	// pad cell, so it never touches the splitter. The » needs no pad of
	// its own — the status text it precedes starts with one. Each click
	// target is one cell wider than its glyph, the same rule every
	// one-cell control in the editor follows.
	sidebarToggleWidth = 2
)

// sidebarExpandRect returns the status-bar-local x and width of the »
// slot, or (-1, 0) when there is nothing to expand: the sidebar is
// already open, or the session has no tree at all (single-file mode).
// statusLeftMax charges the left text for the slot, so the readout and
// the handle cannot overlap.
func (a *App) sidebarExpandRect() (x, w int) {
	if a.sidebarShown || a.tree == nil {
		return -1, 0
	}
	return 0, 1
}

// sidebarExpandHit reports whether a status-bar press at bar-local x
// lands on the » or the pad cell after it.
func (a *App) sidebarExpandHit(x int) bool {
	ex, ew := a.sidebarExpandRect()
	return ew > 0 && x >= ex && x <= ex+ew
}

// drawSidebarExpandButton paints the » at the status bar's origin in
// the bar's own style and returns the cells it took, which is what the
// left text is then offset by. Zero when the handle is not shown.
func (a *App) drawSidebarExpandButton(sx, sy int, bar tcell.Style) int {
	ex, ew := a.sidebarExpandRect()
	if ew == 0 {
		return 0
	}
	textdraw.Cell(a.screen, sx+ex, sy, sidebarExpandGlyph, nil, bar)
	return ew
}

// sidebarCollapseHit reports whether a footer-row press at localX (an
// offset into a footer sw cells wide) lands on the « slot or the cell
// before it.
func (a *App) sidebarCollapseHit(localX, sw int) bool {
	start := sw - sidebarToggleWidth
	return localX >= start-1 && localX < sw
}

// drawSidebarFooter paints the sidebar's footer row — a SidebarBG fill
// with the « right-aligned — and nothing when the sidebar is hidden.
// It repaints the whole row every frame like every other panel, since
// nothing clears the screen for it.
func (a *App) drawSidebarFooter() {
	fx, fy, fw := a.sidebarFooterRect()
	if fw <= 0 {
		return
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)
	textdraw.Fill(a.screen, fx, fy, fw, 1, style)
	if fw >= sidebarToggleWidth {
		textdraw.Cell(a.screen, fx+fw-sidebarToggleWidth, fy, sidebarCollapseGlyph, nil, style)
	}
}
