// =============================================================================
// File: internal/app/mouse.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// mouse.go is the mouse dispatcher — the editor is mouse-first, so this
// is the primary input surface. It routes each event to whichever panel
// the cursor is over, hit-tests tree rows, tabs, the splitter, the
// scrollbar and the git gutter, and carries the drag state that lets a
// press-and-move extend a selection.
//
// Auto-scroll lives here too: dragging past the top or bottom edge starts
// a goroutine that posts autoScrollEvents, because the main loop can only
// nudge the viewport when it gets an event to react to.

package app

import (
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/diff"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/git"
)

// autoScrollEvent is the custom tcell event our auto-scroll goroutine
// posts at autoScrollTick intervals while the user is drag-selecting past
// the top or bottom edge of the editor pane.
type autoScrollEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *autoScrollEvent) When() time.Time { return e.when }

// clickRecord tracks the last mouse-press location and time so we can
// detect double-clicks (and select the word under the cursor).
type clickRecord struct {
	x, y int
	when time.Time
}

// handleMouse routes a mouse event to whichever panel the cursor is over,
// tracking drag state so a click-drag inside the editor extends the
// selection. When the action menu is open it absorbs all mouse events:
// clicks inside trigger an action, clicks outside dismiss the menu.
func (a *App) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()

	// Remember when we last saw Shift held down on ANY mouse event.
	// Zellij + macOS Terminal split shift+wheel into two events: a
	// ButtonNone+Shift "modifier state" event, then a WheelDown/Up
	// with no modifier. We bridge them via modifierStickyWindow below.
	// That first event is a button-less motion report, so it only
	// reaches us under all-motion tracking — which is now scoped to
	// hover surfaces (mousemode.go). See App.lastShiftAt for why the
	// resulting degradation is the right trade.
	if ev.Modifiers()&tcell.ModShift != 0 {
		a.lastShiftAt = time.Now()
	}

	// The overlay on the stack absorbs all mouse input — same routing
	// truth as the keyboard. The project-find strip comes next: it has
	// real mouse targets (result rows, fold arrows) unlike the find bar,
	// which deliberately passes mouse through to the editor (ADR-0001).
	if ov := a.overlays.Top(); ov != nil {
		ov.HandleMouse(x, y, btn)
		return
	}
	if a.projFind.findOpen {
		a.handleProjFindMouse(x, y, btn)
		return
	}

	// Right-click handling. Over a file-tree row it opens a small context
	// menu with file-management actions for that node; everywhere else
	// it falls through to the main action menu so users have a redundant
	// mouse-only path to it. Note: macOS Terminal + tmux often swallows
	// Button3, which is why every action also lives in the main ≡ menu.
	if btn&tcell.Button3 != 0 {
		if a.tryTreeContextClick(x, y) {
			return
		}
		a.openMenu()
		return
	}

	// Wheel events take priority — they fire even with no button held.
	// Shift+wheel rotates the vertical wheel into horizontal scrolling
	// (the VS Code convention). Most terminals never emit native
	// WheelLeft/WheelRight, so this is the path that actually fires in
	// practice; the dedicated horizontal-wheel branch below is a bonus
	// for terminals that do.
	//
	// We accept "shift was just seen" within modifierStickyWindow as
	// equivalent to shift-on-this-event, because Zellij and friends
	// strip the modifier from the actual wheel event.
	// Wheel over the tab bar scrolls the tab strip when it overflows —
	// the only way narrow tmux panes can browse many tabs without
	// pecking at the chevrons.
	if btn&(tcell.WheelUp|tcell.WheelLeft) != 0 && y == 0 && x >= a.sidebarW() && a.maxTabScroll() > 0 {
		a.scrollTabStrip(-tabScrollStep)
		return
	}
	if btn&(tcell.WheelDown|tcell.WheelRight) != 0 && y == 0 && x >= a.sidebarW() && a.maxTabScroll() > 0 {
		a.scrollTabStrip(tabScrollStep)
		return
	}

	shift := ev.Modifiers()&tcell.ModShift != 0 ||
		(!a.lastShiftAt.IsZero() && time.Since(a.lastShiftAt) < modifierStickyWindow)
	if btn&tcell.WheelUp != 0 {
		if shift {
			a.scrollAtH(x, y, -wheelCols)
		} else {
			a.scrollAt(x, y, -wheelLines)
		}
		return
	}
	if btn&tcell.WheelDown != 0 {
		if shift {
			a.scrollAtH(x, y, wheelCols)
		} else {
			a.scrollAt(x, y, wheelLines)
		}
		return
	}
	if btn&tcell.WheelLeft != 0 {
		a.scrollAtH(x, y, -wheelCols)
		return
	}
	if btn&tcell.WheelRight != 0 {
		a.scrollAtH(x, y, wheelCols)
		return
	}

	leftDown := btn&tcell.Button1 != 0

	// Drag continuation: while we're mid-drag in the editor, every event
	// with the button held extends the selection — even if the cursor has
	// wandered out of the editor pane.
	if leftDown && a.dragMode == dragEditor {
		a.editorDrag(x, y)
		return
	}

	// Sidebar resize drag: keep the splitter glued to the mouse x so the
	// panel reshapes live as the user drags.
	if leftDown && a.dragMode == dragSidebar {
		a.resizeSidebar(x + 1)
		return
	}

	// Scrollbar thumb drag: the thumb stays glued to the mouse row even
	// when the pointer wanders off the bar column.
	if leftDown && a.dragMode == dragScrollbar {
		_, ey, _, _ := a.editorRect()
		a.scrollbarTo(y - ey)
		return
	}

	// File-tree thumb drag: same contract as the editor's — the tree
	// keeps following the pointer's row once the grab has started, even
	// when the pointer leaves the bar's column.
	if leftDown && a.dragMode == dragTreeScrollbar {
		_, sy, _, _ := a.sidebarRect()
		a.treeScrollbarTo(y - sy)
		return
	}

	// Git-panel thumb drag: the change list is the sidebar's other
	// mode, so its bar gets the same grab contract as the tree's.
	if leftDown && a.dragMode == dragGitPanelScrollbar {
		a.gitPanelScrollbarTo(y)
		return
	}

	// Initial press dispatch.
	if leftDown && a.dragMode == dragNone {
		sw := a.sidebarW()
		splitX := a.splitterX()
		// A press anywhere but the sidebar means the user has moved on
		// from the Git panel's keyboard mode — drop the key capture so
		// Enter/Space go back to the editor. No-op when unarmed.
		if !(sw > 0 && x <= splitX) {
			a.exitGitPanelKeys()
		}
		switch {
		case splitX >= 0 && x == splitX:
			a.dragMode = dragSidebar
		case sw > 0 && x < splitX:
			// The tree's bar and the Git panel's sit on the column
			// just left of the splitter — whichever panel is up, that
			// column has to be claimed before the row hit-test the
			// rest of the sidebar falls through to. Only one of the
			// two can hit: each opts out when its panel is hidden.
			if a.treeScrollbarHit(x, y) {
				a.treeScrollbarTo(y)
				a.dragMode = dragTreeScrollbar
				return
			}
			if a.gitPanelScrollbarHit(x, y) {
				a.gitPanelScrollbarTo(y)
				a.dragMode = dragGitPanelScrollbar
				return
			}
			a.sidebarClick(x, y)
		case y == 0:
			a.tabBarClick(x, y)
		case y == a.height-1:
			a.statusBarClick(x)
		case y > 0 && y < a.height-1:
			if localY, ok := a.scrollbarHit(x, y); ok {
				a.scrollbarTo(localY)
				a.dragMode = dragScrollbar
				return
			}
			// Only a press editorPress claims as its own arms the drag.
			// This case's band is wider than the editor rect (an open
			// find bar keeps its row in here, mouse-transparent per
			// ADR-0001) and covers surfaces with no caret at all, so
			// arming unconditionally let the next motion event drag out
			// a selection the user never started — and the release copy
			// it to the clipboard.
			if a.editorPress(x, y) {
				a.dragMode = dragEditor
			}
		}
		return
	}

	// Button released — exit any drag mode we were in. Releasing an
	// editor drag that built a selection copies it (select-to-copy, the
	// tmux/herdr convention): with mouse reporting on, the terminal and
	// any multiplexer never see a selection of their own, so Cmd+C at
	// the terminal level has nothing to grab. A plain click collapses
	// the selection before release, so caret placement never copies.
	if a.dragMode == dragEditor {
		if t := a.activeTabPtr(); t != nil && t.HasSelection() {
			a.copySelection()
		}
	}
	a.dragMode = dragNone
	a.stopAutoScroll()
}

// scrollAt scrolls whichever panel the (x, y) cursor is over.
func (a *App) scrollAt(x, y, delta int) {
	if sw := a.sidebarW(); sw > 0 && x < sw {
		if a.gitPanel.active {
			a.scrollGitPanel(delta)
		} else {
			a.tree.Scroll(delta)
		}
		return
	}
	if y > 0 && y < a.height-1 {
		if t := a.activeTabPtr(); t != nil {
			t.Scroll(delta)
		}
	}
}

// scrollAtH scrolls the panel under (x, y) horizontally by delta cells.
// The file tree has no useful horizontal axis (each row is a single label),
// so we only honor horizontal wheel events when they fall inside the
// editor pane.
func (a *App) scrollAtH(x, y, delta int) {
	if sw := a.sidebarW(); sw > 0 && x < sw {
		return
	}
	if y > 0 && y < a.height-1 {
		if t := a.activeTabPtr(); t != nil {
			t.ScrollH(delta)
		}
	}
}

// tryTreeContextClick opens the right-click context menu when (x, y) lands
// on a tree row. Returns true if it consumed the event so the caller knows
// not to fall back to the main action menu. Right-clicking a node also
// counts as "I'm working here" — the active folder updates so the main
// menu's New File defaults to a sensible target even after the context
// menu closes.
func (a *App) tryTreeContextClick(x, y int) bool {
	sw := a.sidebarW()
	if sw <= 0 {
		return false
	}
	// The Git panel has no per-row context actions — the tree isn't
	// what's on screen, so tree.HitTest would map to invisible rows.
	if a.gitPanel.active {
		return false
	}
	splitX := a.splitterX()
	if x >= splitX {
		return false
	}
	// The scrollbar column is not a row. Left-click already routes it
	// to the bar before the row hit-test; right-click has to skip it
	// too, or the two buttons disagree about what that column is.
	if a.treeScrollbarHit(x, y) {
		return false
	}
	sx, sy, _, _ := a.sidebarRect()
	n, ok := a.tree.HitTest(x-sx, y-sy)
	if !ok {
		return false
	}
	if n.IsDir {
		a.setActiveFolder(n.Path)
	} else {
		a.setActiveFolder(filepath.Dir(n.Path))
	}
	a.openTreeContext(n, x, y)
	return true
}

// sidebarClick toggles a directory or opens a file when the user clicks a
// row in the file tree. Either action also updates the editor's "active
// folder" so the next New File from the main menu defaults to wherever
// the user is currently focused. Clicking the project-root row only
// resets the active folder — it never toggles the root's expansion
// since the root is always shown and there's no useful "collapsed
// root" state.
func (a *App) sidebarClick(x, y int) {
	sx, sy, _, _ := a.sidebarRect()
	// Header row: the EXPLORER / GIT tabs switch which panel the
	// sidebar shows. Handled before any panel-specific hit-testing so
	// the tabs behave identically from either side.
	if y-sy == 0 {
		switch a.sidebarHeaderHit(x - sx) {
		case "explorer":
			a.showExplorerPanel()
		case "git":
			a.showGitPanel()
		}
		return
	}
	if a.gitPanel.active {
		a.gitPanelClick(x-sx, y-sy)
		return
	}
	n, ok := a.tree.HitTest(x-sx, y-sy)
	if !ok {
		return
	}
	if n == a.tree.Root {
		a.setActiveFolder(a.rootDir)
		return
	}
	if n.IsDir {
		a.setActiveFolder(n.Path)
		a.tree.Toggle(n)
		return
	}
	a.setActiveFolder(filepath.Dir(n.Path))
	a.openFilePreview(n.Path)
}

// setActiveFolder records path as the editor's current working folder and
// mirrors it onto the file tree so the matching row renders with the
// "active" highlight. All writes to a.activeFolder go through here.
func (a *App) setActiveFolder(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	a.activeFolder = path
	if a.tree != nil {
		a.tree.ActiveFolder = path
	}
}

// tabBarClick dispatches clicks in the tab bar: the leftmost menuButtonWidth
// cells open the action menu; remaining cells switch or close tabs based on
// where the click landed within their rendered geometry.
func (a *App) tabBarClick(x, _ int) {
	sw := a.sidebarW()
	if x >= sw && x < sw+menuButtonWidth {
		a.openMenu()
		return
	}
	// The overflow badges scroll the strip; they sit on top of whatever
	// tab is clipped beneath them, so they must win the hit-test. The
	// geometry comes from tabChevrons — the same call drawTabBar paints
	// from — so the count cell beside the chevron is part of the button
	// rather than a dead cell that activates the tab underneath it.
	leftChev, rightChev := a.tabChevrons()
	if leftChev.hit(x) {
		a.scrollTabStrip(-tabScrollStep)
		return
	}
	if rightChev.hit(x) {
		a.scrollTabStrip(tabScrollStep)
		return
	}
	for _, r := range a.lastTabRects {
		if x >= r.X && x < r.X+r.Width {
			if x == r.CloseX {
				a.requestCloseTab(a.tabs.At(r.Index))
				return
			}
			a.tabs.ActivateAt(r.Index)
			a.ensureActiveTabVisible()
			a.syncActiveTreeFile()
			return
		}
	}
}

// syncActiveTreeFile mirrors the active tab path into the file tree.
func (a *App) syncActiveTreeFile() {
	if a.tree == nil {
		return
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		a.tree.ActiveFile = ""
		return
	}
	a.tree.ActiveFile = tab.Path
}

// editorPress handles the initial mouse press inside the editor —
// placing the caret, optionally selecting a word on double-click. It
// reports whether the press belongs to the editor surface at all, which
// is what the dispatcher uses to decide whether a drag is starting.
//
// The band the dispatcher hands us is wider than the editor: an open
// find bar shrinks the rect but keeps its own row inside that band,
// because the strip stays mouse-transparent (ADR-0001). So the rect is
// re-checked here rather than assumed. Image tabs and an empty editor
// have no caret at all, and a gutter click opens a diff preview instead
// of placing one — none of those may arm a drag. A press below the last
// line does: there's no caret to move, but the empty space under a
// short file is still editor space you can drag a selection out of,
// exactly as in every GUI editor.
func (a *App) editorPress(x, y int) bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return false
	}
	if a.openGitHunkAt(tab, x-ex, y-ey) {
		return false
	}
	pos, ok := tab.HitTest(x-ex, y-ey, ew, eh)
	if !ok {
		return true
	}

	now := time.Now()
	if a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickWindow {
		a.selectWordAt(tab, pos)
		a.lastClick = clickRecord{} // prevent triple-click from selecting nothing.
		return true
	}
	a.lastClick = clickRecord{x: x, y: y, when: now}
	tab.MoveCursorTo(pos, false)
	return true
}

// openGitHunkAt kicks a diff preview when the user clicks a gutter
// marker, returning whether the click belonged to the gutter. The git
// call runs off-thread (see App.requestDiff) — a gutter click used to
// block the event loop for up to internal/git's ten-second read
// timeout on a slow or network-mounted repo.
func (a *App) openGitHunkAt(tab *editor.Tab, localX, localY int) bool {
	if localX != 0 || localY < 0 {
		return false
	}
	line := tab.ScrollY + localY
	if tab.GitLines[line] == editor.GitLineNone {
		return false
	}
	path := tab.Path
	a.requestDiff(diffLoadHunk, "Git change · "+filepath.Base(path), path,
		func(repo *git.Repo) (diff.Patch, error) { return repoHunkPreview(repo, path, line) })
	return true
}

// editorDrag extends the selection during a click-drag inside the editor.
// (x, y) is clamped to the editor rect so dragging into another pane still
// extends the selection sensibly. When the mouse passes above or below the
// editor we engage auto-scroll so the user can select content that's not
// yet on screen — same feel as VS Code or any GUI text editor. Image tabs
// drop the drag entirely.
func (a *App) editorDrag(x, y int) {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	ex, ey, ew, eh := a.editorRect()

	// Remember where the mouse is so the auto-scroll tick can extend the
	// selection at this column even while the mouse stops moving.
	a.lastDragX = x
	a.lastDragY = y

	// Edge detection: outside the editor's vertical bounds turns on
	// auto-scroll; back inside turns it off.
	switch {
	case y < ey:
		a.startAutoScroll(-1)
	case y >= ey+eh:
		a.startAutoScroll(1)
	default:
		a.stopAutoScroll()
	}

	// Clamp the mouse into the editor and extend the selection there.
	localX := x - ex
	localY := y - ey
	if localX < 0 {
		localX = 0
	}
	if localY < 0 {
		localY = 0
	}
	if localX >= ew {
		localX = ew - 1
	}
	if localY >= eh {
		localY = eh - 1
	}
	pos, ok := tab.HitTest(localX, localY, ew, eh)
	if !ok {
		return
	}
	tab.MoveCursorTo(pos, true)
}

// startAutoScroll begins a timer goroutine that posts autoScrollEvents at
// autoScrollTick intervals so the editor keeps scrolling while the user
// holds the mouse past an edge. dir is -1 (up) or +1 (down). Calling with
// the same direction is a no-op so we don't restart the timer on every
// drag motion event.
func (a *App) startAutoScroll(dir int) {
	if a.autoScrollDir == dir {
		return
	}
	a.stopAutoScroll()
	a.autoScrollDir = dir
	a.autoScrollStop = make(chan struct{})
	stop := a.autoScrollStop
	scr := a.screen
	a.safeGo("auto-scroll", func() {
		ticker := time.NewTicker(autoScrollTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				_ = scr.PostEvent(&autoScrollEvent{when: t})
			}
		}
	})
}

// stopAutoScroll signals the auto-scroll goroutine to exit (idempotent).
func (a *App) stopAutoScroll() {
	if a.autoScrollStop != nil {
		close(a.autoScrollStop)
		a.autoScrollStop = nil
	}
	a.autoScrollDir = 0
}

// handleAutoScroll runs once per autoScrollEvent: nudge the viewport in the
// armed direction and extend the selection to the edge row at the user's
// last known mouse column. Bails out (and stops the timer) if anything
// suggests the user is no longer drag-selecting (button released, menu
// opened, no active tab).
func (a *App) handleAutoScroll() {
	if a.autoScrollDir == 0 || a.dragMode != dragEditor || a.anyModalOpen() {
		a.stopAutoScroll()
		return
	}
	tab := a.activeTabPtr()
	if tab == nil {
		a.stopAutoScroll()
		return
	}
	tab.Scroll(a.autoScrollDir)

	ex, _, ew, eh := a.editorRect()
	localX := a.lastDragX - ex
	if localX < 0 {
		localX = 0
	}
	if localX >= ew {
		localX = ew - 1
	}
	localY := eh - 1
	if a.autoScrollDir < 0 {
		localY = 0
	}
	pos, ok := tab.HitTest(localX, localY, ew, eh)
	if !ok {
		return
	}
	tab.MoveCursorTo(pos, true)
}

// scrollbarHit reports whether (x, y) lands on the active tab's
// scrollbar column, returning the bar-local row when it does. The
// geometry must mirror Render's: rightmost editor column, only when the
// file is taller than the viewport.
func (a *App) scrollbarHit(x, y int) (int, bool) {
	tab := a.activeTabPtr()
	if tab == nil {
		return 0, false
	}
	ex, ey, ew, eh := a.editorRect()
	if ew <= 2 || !tab.ScrollbarVisible(eh) {
		return 0, false
	}
	if x != ex+ew-1 || y < ey || y >= ey+eh {
		return 0, false
	}
	return y - ey, true
}

// scrollbarTo scrolls the active tab so the thumb centers on the
// bar-local row — shared by the initial press and the drag. Clamping
// lives in the editor's ScrollTargetForClick.
func (a *App) scrollbarTo(localY int) {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	_, _, _, eh := a.editorRect()
	tab.ScrollY = tab.ScrollTargetForClick(eh, localY)
	// The thumb maps to a buffer line; land at its first visual row so a
	// stale wrap segment from the previous anchor can't offset the jump.
	tab.ScrollSeg = 0
}

// treeScrollbarHit reports whether (x, y) lands on the file tree's
// scrollbar. The bar owns the tree rect's rightmost column, which is
// the cell immediately LEFT of the resize splitter (sidebarRect is one
// column narrower than the sidebar block) — so the splitter, the bar
// and the tree rows occupy three distinct column ranges at any y and
// each keeps its own clicks.
//
// The Git panel draws its own list over the same rect and has no tree
// bar, so it opts out entirely.
func (a *App) treeScrollbarHit(x, y int) bool {
	if a.tree == nil || a.gitPanel.active {
		return false
	}
	sx, sy, sw, sh := a.sidebarRect()
	if sw <= 0 {
		return false
	}
	return a.tree.ScrollbarHit(x-sx, y-sy, sw, sh)
}

// treeScrollbarTo scrolls the file tree so its thumb centers on screen
// row y — shared by the initial press and the drag, exactly like the
// editor's scrollbarTo. Clamping lives in the tree.
func (a *App) treeScrollbarTo(y int) {
	if a.tree == nil {
		return
	}
	_, sy, sw, sh := a.sidebarRect()
	a.tree.ScrollToBarRow(sw, sh, y-sy)
}

// gitPanelScrollbarHit reports whether a screen-space press at (x, y)
// landed on the Git panel's scroll indicator. The panel's own geometry
// helpers work in sidebar-local cells (the whole panel is drawn that
// way), so this is the screen-space wrapper the mouse dispatcher needs
// — the tree-side mirror of the same conversion.
//
// Opts out when the explorer is up, exactly as treeScrollbarHit opts
// out when the panel is: the two share a column and only one of them
// is ever painted on it.
func (a *App) gitPanelScrollbarHit(x, y int) bool {
	if !a.gitPanel.active {
		return false
	}
	sx, sy, _, _ := a.sidebarRect()
	return a.gitPanelBarHit(x-sx, y-sy)
}

// gitPanelScrollbarTo scrolls the change list so its thumb centers on
// screen row y — shared by the initial press and the drag, so a grab
// and a click can never disagree about where the thumb lands.
func (a *App) gitPanelScrollbarTo(y int) {
	if !a.gitPanel.active {
		return
	}
	_, sy, _, _ := a.sidebarRect()
	a.gitPanelScrollToBar(y - sy)
}

// selectWordAt selects the word under the buffer position p (or does
// nothing if p sits in whitespace / punctuation).
//
// The word boundary rule itself lives in internal/editor so double-click
// selection and the Alt+arrow / Esc-b / Esc-e caret motions can never
// disagree about where a token starts — see editor.IsWordChar.
func (a *App) selectWordAt(tab *editor.Tab, p editor.Position) {
	tab.SelectWordAt(p)
}
