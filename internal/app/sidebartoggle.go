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
// the panel will move: « sits at the right end of the sidebar header
// while the explorer is open, » sits just right of the ≡ button once it
// is collapsed. Both cells route to menuToggleSidebar, so the chevron
// can never disagree with the menu row about what a toggle means.
//
// It is painted in Muted rather than Accent on purpose: the ≡ is the
// loud button, this is a hint that a panel edge is there. The two
// glyphs are Latin-1 (U+00AB / U+00BB), one cell wide everywhere, so
// no terminal renders the handle as a box.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
)

const (
	// sidebarCollapseGlyph is the open-state handle, at the header's
	// right edge; sidebarExpandGlyph is the collapsed-state handle,
	// beside the ≡ button.
	sidebarCollapseGlyph = '«'
	sidebarExpandGlyph   = '»'
	// sidebarToggleWidth is the cells each handle occupies: the glyph
	// and one pad cell, so it never touches the splitter on one side or
	// the first tab on the other. The click target is one cell wider
	// still (see the hit-tests), the same rule every one-cell control
	// in the editor follows.
	sidebarToggleWidth = 2
)

// sidebarExpandRect returns the screen x and width of the » slot in the
// tab bar, or (-1, 0) when there is nothing to expand: the sidebar is
// already open, or the session has no tree at all (single-file mode).
// tabStripRegion starts the tabs after this slot, so the chevron and
// the first tab cannot overlap.
func (a *App) sidebarExpandRect() (x, w int) {
	if a.sidebarShown || a.tree == nil {
		return -1, 0
	}
	return a.sidebarW() + menuButtonWidth, sidebarToggleWidth
}

// sidebarExpandHit reports whether a tab-bar press at x lands on the »
// slot. The cell before the slot belongs to the ≡ button, so the
// one-cell-wider allowance runs only to the right.
func (a *App) sidebarExpandHit(x int) bool {
	ex, ew := a.sidebarExpandRect()
	return ew > 0 && x >= ex && x <= ex+ew
}

// drawSidebarExpandButton paints the » in its tab-bar slot. The bar
// row is already filled by drawTabBar; this only places the glyph.
func (a *App) drawSidebarExpandButton() {
	ex, ew := a.sidebarExpandRect()
	if ew == 0 {
		return
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)
	textdraw.Cell(a.screen, ex, 0, sidebarExpandGlyph, nil, style)
}

// sidebarCollapseHit reports whether a header-row press at localX (an
// offset into a header sw cells wide) lands on the « slot or the cell
// before it.
func (a *App) sidebarCollapseHit(localX, sw int) bool {
	start := sw - sidebarToggleWidth
	return localX >= start-1 && localX < sw
}

// drawSidebarCollapseButton paints the « at the right end of a header
// row sx..sx+sw. It runs after the header labels so the chevron owns
// its cells even when a "GIT N" badge on a min-width sidebar would
// otherwise reach them.
func (a *App) drawSidebarCollapseButton(sx, sy, sw int) {
	if sw < sidebarToggleWidth {
		return
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)
	x := sx + sw - sidebarToggleWidth
	textdraw.Cell(a.screen, x, sy, sidebarCollapseGlyph, nil, style)
	textdraw.Cell(a.screen, x+1, sy, ' ', nil, style)
}
