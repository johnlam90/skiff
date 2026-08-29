// =============================================================================
// File: internal/app/layout.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// layout.go is the single source of truth for the editor's screen
// geometry. Every panel — sidebar, splitter, tab bar, editor body, status
// bar — is derived here from a.width/a.height and a.sidebarWidth, so the
// renderer and the mouse hit-testers can never disagree about where a
// panel starts and ends.
//
// Nothing in here draws or mutates layout state except resizeSidebar,
// which owns the min-width clamping for the splitter drag.

package app

// sidebarW is the effective width of the sidebar block (file tree +
// splitter): zero when hidden, a.sidebarWidth otherwise. Every layout
// helper and click router goes through this so toggling/resizing the
// panel reshapes the entire UI in one place.
func (a *App) sidebarW() int {
	if !a.sidebarShown {
		return 0
	}
	return a.sidebarWidth
}

// sidebarRect returns the file tree's render rectangle (one column
// narrower than the sidebar block — the rightmost column belongs to the
// resize splitter). Zero width when the sidebar is hidden.
func (a *App) sidebarRect() (x, y, w, h int) {
	sw := a.sidebarW()
	if sw <= 0 {
		return 0, 0, 0, 0
	}
	return 0, 0, sw - 1, a.height - 1
}

// splitterX returns the x coordinate of the resize splitter column, or -1
// when the sidebar is hidden (no splitter to draw or click).
func (a *App) splitterX() int {
	if !a.sidebarShown {
		return -1
	}
	return a.sidebarWidth - 1
}

// resizeSidebar applies the user's desired sidebar width while clamping it
// to a sensible range — the file tree stays wide enough to read names and
// the editor keeps at least minEditorAfterDrag columns. Tiny windows that
// can't satisfy both fall back to the minimum and let the editor shrink.
func (a *App) resizeSidebar(target int) {
	if target < minSidebarWidth {
		target = minSidebarWidth
	}
	max := a.width - minEditorAfterDrag
	if max < minSidebarWidth {
		max = minSidebarWidth
	}
	if target > max {
		target = max
	}
	a.sidebarWidth = target
}

// tabBarRect returns the tab bar's screen rectangle (one row tall).
func (a *App) tabBarRect() (x, y, w, h int) {
	sw := a.sidebarW()
	return sw, 0, a.width - sw, 1
}

// editorMinRows is the fewest rows the editor body is ever handed. The
// caret, the scrollbar, the wrap walk and every hit-test derive from the
// editor rect, so a zero- or negative-height rect is not "no editor" —
// it is arithmetic over cells that do not exist.
const editorMinRows = 1

// stripRowBudget is how many rows the transient strips docked under the
// editor may take before the body would drop under editorMinRows. The
// find bar is charged against the budget rather than being part of it:
// it is a fixed, user-invoked row, so what comes back is what is left
// for the flash strip to wrap onto.
//
// On the shortest terminal skiff runs in this never binds — minHeight is
// 10, so even with the find bar up the budget is 6 against a
// flashStripMaxRows of 3. That is the point: the number is derived
// rather than assumed, so lowering minHeight or raising the strip's row
// cap cannot quietly starve the editor, and a test can say so.
func (a *App) stripRowBudget() int {
	room := a.height - 2 - editorMinRows
	if a.findOpen || a.projFind.findOpen {
		room -= findBarHeight
	}
	if room < 0 {
		room = 0
	}
	return room
}

// editorRect returns the editor body's screen rectangle (everything to the
// right of the sidebar, between the tab bar and the status bar). Rows are
// taken out of the bottom for every transient strip pinned there — the
// find bar and the flash strip — because the editor's scrollbar, caret
// and hit-testing all derive from this rect and have to describe the
// region that is actually painted.
//
// The floor is not decoration. draw() refuses to paint below minHeight,
// but the key handlers that ask for editorSize (page up/down, the wrap
// walk) run on every event, including the ones that arrive while the
// terminal is mid-resize and two rows tall.
func (a *App) editorRect() (x, y, w, h int) {
	sw := a.sidebarW()
	h = a.height - 2
	if a.findOpen || a.projFind.findOpen {
		h -= findBarHeight
	}
	h -= a.flashStripRows()
	if h < editorMinRows {
		h = editorMinRows
	}
	return sw, 1, a.width - sw, h
}

// statusRect returns the status bar's screen rectangle (full-width bottom row).
func (a *App) statusRect() (x, y, w, h int) {
	return 0, a.height - 1, a.width, 1
}

// editorSize returns just the (width, height) of the editor body. Used by
// keyboard handlers that need to compute page-up / page-down deltas.
func (a *App) editorSize() (int, int) {
	_, _, w, h := a.editorRect()
	return w, h
}

// menuButtonRect returns the on-screen rectangle of the ≡ icon in the tab
// bar. Click hit-tests in tabBarClick consult this directly. When the
// sidebar is hidden the icon shifts left to fill the corner.
func (a *App) menuButtonRect() (x, y, w, h int) {
	return a.sidebarW(), 0, menuButtonWidth, 1
}
