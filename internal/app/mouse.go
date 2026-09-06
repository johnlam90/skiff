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
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/diff"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/scrollbar"
)

// autoScrollEvent is the custom tcell event our auto-scroll goroutine
// posts at autoScrollTick intervals while the user is drag-selecting past
// the top or bottom edge of the editor pane.
type autoScrollEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *autoScrollEvent) When() time.Time { return e.when }

// clickRecord tracks the last mouse press so the next one can be read
// as the second or third of a multi-click: where it landed, when, and
// how many clicks the run is at. A click a row away, more than
// doubleClickSlop columns off, or past doubleClickWindow starts a new
// run at one.
type clickRecord struct {
	x, y  int
	when  time.Time
	count int
}

// doubleClickSlop is how many columns a repeat click may drift from
// the previous one and still count as the same spot. A finger on a
// phone and a trackpad tap both wobble by a cell; requiring the exact
// cell made double-click a gesture that only worked with a real mouse.
const doubleClickSlop = 1

// clickRun returns the multi-click count a press at (x, y) at time now
// continues: last.count+1 when it lands on the same row within slop and
// window, and 1 otherwise. The run wraps back to a single click after
// the third, so a fourth click places the caret instead of
// re-selecting the line.
func clickRun(last clickRecord, x, y int, now time.Time) int {
	if last.count == 0 || y != last.y || now.Sub(last.when) >= doubleClickWindow {
		return 1
	}
	if dx := x - last.x; dx > doubleClickSlop || dx < -doubleClickSlop {
		return 1
	}
	if last.count >= 3 {
		return 1
	}
	return last.count + 1
}

// mouseState is the dispatcher's memory between events. held is the
// button mask the previous event carried, which is what turns a stream
// of Button1 reports into one press followed by motion: a fresh press is
// a button in the mask now that was not in it last time, and everything
// else with the button set is the same gesture continuing.
type mouseState struct {
	held tcell.ButtonMask
	// pressTop is what sat on the overlay stack when Button1 last went
	// down: nil for the base UI. A press that OPENS an overlay (the ≡
	// button, a menu row that opens a confirm) is not that overlay's
	// press, so the motion of the same held button is delivered to it
	// with Button1 masked off — hover only, nothing to activate.
	pressTop overlay.Overlay
	// menuPress is the action menu's click latch. The prefab overlays
	// carry their own (overlay.Press); the menu's state lives on App,
	// so its latch lives here.
	menuPress overlay.Press
	// autoScrollStep is how many lines each auto-scroll tick moves —
	// set from how far past the edge zone the drag is (see
	// autoScrollStepFor), so a drag parked on the status bar scrolls
	// faster than one hovering the editor's last row without the
	// ticker itself running any faster.
	autoScrollStep int
	// events counts every mouse event the terminal has delivered.
	// Zero after mouseProbeDelay under tmux is the one symptom of
	// `set -g mouse` being off that the editor can observe — see
	// noteMouseProbe.
	events int
	// hintShown records that the tmux mouse hint has flashed once
	// this session; it never flashes twice.
	hintShown bool
}

// mouseProbeDelay is how long after startup the tmux mouse hint waits
// for a first mouse event before concluding none are coming. Ten
// seconds is long enough that a keyboard-first user who has not
// touched the mouse yet is not nagged the moment the editor opens.
const mouseProbeDelay = 10 * time.Second

// mouseHintMsg is the one-time flash when skiff runs under tmux and no
// mouse event has arrived by mouseProbeDelay. tmux swallows mouse
// reporting unless `set -g mouse on` is in its config, and from inside
// the editor that looks exactly like a mouse-first UI ignoring every
// click; the hint names the fix and the keyboard fallback.
const mouseHintMsg = "No mouse events yet — tmux may need `set -g mouse on` (Esc ? for keyboard)"

// tmuxActive reports whether the editor is running inside tmux, the
// one multiplexer whose default config drops mouse reporting.
func tmuxActive() bool {
	return os.Getenv("TMUX") != ""
}

// startMouseProbe schedules the tmux mouse hint: after `after`, a
// one-shot timer posts a Notify event that runs noteMouseProbe on the
// loop. Nothing is scheduled outside tmux — a bare terminal with mouse
// reporting off is a choice, not a misconfiguration. The timer's
// callback goes through runGuarded so a panic there still reaches the
// crash guard, and the mutation itself happens on the loop, never in
// the timer goroutine.
func (a *App) startMouseProbe(inTmux bool, after time.Duration) {
	if !inTmux {
		return
	}
	scr := a.screen
	time.AfterFunc(after, func() {
		a.runGuarded("mouse-probe", func() {
			_ = scr.PostEvent(asyncjob.Notify(a.noteMouseProbe))
		})
	})
}

// noteMouseProbe is the probe's on-loop half: flash the tmux hint if no
// mouse event has arrived, and only once per session.
func (a *App) noteMouseProbe() {
	if a.mouse.events > 0 || a.mouse.hintShown {
		return
	}
	a.mouse.hintShown = true
	a.flash(mouseHintMsg)
}

// autoScrollEdgeRows is how many rows at the top and bottom of the
// editor rect arm auto-scroll during a drag. The trigger used to be
// leaving the rect — one row of tab bar or status bar — which a finger
// cannot park on, and which a tmux pane border sits on top of, so
// drags in a split never scrolled at all. The zone now starts inside
// the rect.
const autoScrollEdgeRows = 2

// autoScrollMaxStep caps the per-tick step: ~16 ticks a second times
// eight lines is fast enough to cross any file, and past it a drag
// overshoots what the user can watch.
const autoScrollMaxStep = 8

// autoScrollStepFor maps how many rows past the zone's inner row the
// pointer is to the lines each tick scrolls: one at the inner row, one
// more per row beyond it, capped at autoScrollMaxStep. Distance drives
// speed so a long drag scrolls fast without a faster ticker.
func autoScrollStepFor(past int) int {
	return min(1+max(past, 0), autoScrollMaxStep)
}

// fresh reports which buttons btn presses for the first time — set now
// and not on the previous event — and records btn as the new baseline.
// Called exactly once per event, at the top of handleMouse, so every
// branch below reads the same answer.
func (m *mouseState) fresh(btn tcell.ButtonMask) tcell.ButtonMask {
	const buttons = tcell.Button1 | tcell.Button2 | tcell.Button3
	pressed := btn & buttons &^ m.held
	m.held = btn & buttons
	return pressed
}

// handleMouse routes a mouse event to whichever panel the cursor is over,
// tracking drag state so a click-drag inside the editor extends the
// selection. When the action menu is open it absorbs all mouse events:
// clicks inside trigger an action, clicks outside dismiss the menu.
func (a *App) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()
	a.mouse.events++
	pressed := a.mouse.fresh(btn)
	// Under minWidth/minHeight draw() paints only the "too small"
	// notice, so there is nothing on screen to hit — but the tab rects
	// from the last real frame were still there to hit-test against,
	// and a tap on the notice could close a tab it never showed. Drop
	// them and route nothing until the window grows back.
	if a.width < minWidth || a.height < minHeight {
		a.lastTabRects = nil
		return
	}
	leftDown := btn&tcell.Button1 != 0
	leftPress := pressed&tcell.Button1 != 0
	if leftPress {
		a.mouse.pressTop = a.overlays.Top()
	}

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
	// truth as the keyboard. The docked strip comes next, and it is the
	// strip that decides: the project-find panel consumes the event (it
	// has real targets — result rows, fold arrows), while the find bar
	// answers false and lets the press reach the editor underneath
	// (ADR-0001's pass-through, now the adapter's answer rather than an
	// absent branch here).
	if ov := a.overlays.Top(); ov != nil {
		// The overlay owns the pointer now, so whatever base-UI drag
		// was in progress is over — an overlay opened mid-drag would
		// otherwise leave the mode latched until a release the overlay
		// swallows.
		a.dragMode = dragNone
		a.stopAutoScroll()
		// A press that opened this overlay is not its press: the ≡
		// button, a tree context row or a menu row that opens a confirm
		// all fire on the press, and the drag that follows used to reach
		// the new surface as a Button1 event over its own targets. The
		// overlays compare by identity, which is safe because every
		// opener hands the stack a pointer (or the one-word menu
		// adapter).
		if leftDown && !leftPress && ov != a.mouse.pressTop {
			btn &^= tcell.Button1
		}
		ov.HandleMouse(x, y, btn)
		return
	}
	if a.strip != nil && a.strip.handleMouse(x, y, btn) {
		return
	}

	// Middle-click closes the tab under it — the browser and VS Code
	// convention, and a second path to × that needs no aim at one cell.
	// A fresh press only: the motion of a held middle button crossing
	// the strip must not close every tab in its path, and a held one
	// is otherwise inert.
	if pressed&tcell.Button2 != 0 {
		if r, ok := a.tabRectAt(x, y); ok {
			a.requestCloseTab(a.tabs.At(r.Index))
		}
		return
	}
	if btn&tcell.Button2 != 0 {
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

	// Drag continuation: while we're mid-drag in the editor, every event
	// with the button held extends the selection — even if the cursor has
	// wandered out of the editor pane.
	if leftDown && a.dragMode == dragMdPreview {
		if t := a.activeTabPtr(); t != nil {
			if st := a.mdPreviewFor(t); st != nil {
				a.mdPreviewDragTo(st, x, y)
			}
		}
		return
	}
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

	// The preview's thumb drag: the bar is painted by drawMdPreview,
	// so the grab contract is the editor bar's, applied to the rendered
	// view's own scroll offset.
	if leftDown && a.dragMode == dragMdPreviewScrollbar {
		if st := a.activeMdPreview(); st != nil {
			a.mdPreviewScrollbarTo(st, y)
		}
		return
	}

	// Motion with the button still held and no drag claimed above is
	// nothing: the press already ran its handler, and running it again
	// at every cell the pointer crosses is how a sideways drag used to
	// close every tab in its path and a downward one toggled every
	// folder. Only a FRESH press — Button1 set now, clear on the
	// previous event — reaches the dispatch, so it runs once per press.
	if leftDown && !leftPress {
		return
	}

	// Initial press dispatch. A drag mode still set here is a stale
	// latch (the release never reached us), and a new press ends it.
	if leftPress {
		a.dragMode = dragNone
		a.stopAutoScroll()
		sw := a.sidebarW()
		splitX := a.splitterX()
		// A press anywhere but the sidebar means the user has moved on
		// from the Git panel's keyboard mode — drop the key capture so
		// Enter/Space go back to the editor. No-op when unarmed.
		if !(sw > 0 && x <= splitX) {
			a.exitGitPanelKeys()
		}
		switch {
		case a.splitterHit(x, y):
			a.dragMode = dragSidebar
		case sw > 0 && x < splitX:
			// The tree's bar and the Git panel's sit on the columns
			// just left of the splitter — whichever panel is up, they
			// have to be claimed before the row hit-test the rest of
			// the sidebar falls through to. Only one of the two can
			// hit: each opts out when its panel is hidden.
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
			// The preview replaces the editor's surface wholesale, bar
			// included: its bar has to be tested before the editor's,
			// or a long markdown file's own scrollbar (still "visible"
			// on the tab) claims the column the preview painted.
			if st := a.activeMdPreview(); st != nil {
				if a.mdPreviewScrollbarHit(st, x, y) {
					a.mdPreviewScrollbarTo(st, y)
					a.dragMode = dragMdPreviewScrollbar
					return
				}
				if a.mdPreviewPress(st, x, y) {
					a.dragMode = dragMdPreview
				}
				return
			}
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
	// The preview's select-to-copy: same convention, rendered text.
	if a.dragMode == dragMdPreview {
		if t := a.activeTabPtr(); t != nil {
			if st := a.mdPreviewFor(t); st != nil {
				a.copyMdPreviewSelection(st)
			}
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
			if st := a.mdPreviewFor(t); st != nil {
				st.scrollBy(delta)
				return
			}
			t.Scroll(delta)
			a.followCaret(t)
		}
	}
}

// followCaret applies the opt-in "caret follows scroll" clamp after a
// viewport-only scroll gesture. A no-op unless the user turned the
// preference on; the clamp itself refuses to touch an active selection
// and lands the caret inside the viewport, so the render pass's
// EnsureVisible cannot scroll back (see Tab.ClampCursorToView).
func (a *App) followCaret(t *editor.Tab) {
	if !a.scrollCaret {
		return
	}
	_, _, _, eh := a.editorRect()
	t.ClampCursorToView(eh)
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
	if r, ok := a.tabRectAt(x, 0); ok {
		// The × is painted in one cell; the space before it is part of
		// the target, because one cell is a coin toss on a phone and
		// the miss — activating the tab you meant to close — is the
		// gesture's own opposite. The same one-cell-wider rule the
		// splitter and every scrollbar follow.
		if x >= r.CloseX-1 && x <= r.CloseX {
			a.requestCloseTab(a.tabs.At(r.Index))
			return
		}
		a.tabs.ActivateAt(r.Index)
		a.ensureActiveTabVisible()
		a.syncActiveTreeFile()
	}
}

// tabRectAt returns the tab rect under screen cell (x, y), reading the
// geometry the last frame painted — the one hit-test every tab-strip
// gesture (activate, ×, middle-click) shares.
func (a *App) tabRectAt(x, y int) (tabRect, bool) {
	if y != 0 {
		return tabRect{}, false
	}
	for _, r := range a.lastTabRects {
		if x >= r.X && x < r.X+r.Width {
			return r, true
		}
	}
	return tabRect{}, false
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
	count := clickRun(a.lastClick, x, y, now)
	a.lastClick = clickRecord{x: x, y: y, when: now, count: count}
	switch count {
	case 2:
		a.selectWordAt(tab, pos)
	case 3:
		a.selectLineAt(tab, pos.Line)
	default:
		tab.MoveCursorTo(pos, false)
	}
	return true
}

// selectLineAt selects the whole of buffer line `line` including its
// line break — the triple-click gesture, so a copy or delete of the
// selection takes the line out cleanly rather than leaving an empty
// one. The last line has no break to take, so the selection ends at
// its end. Built from MoveCursorTo so the caret-moved flag and the
// undo-group break come for free.
func (a *App) selectLineAt(tab *editor.Tab, line int) {
	tab.MoveCursorTo(editor.Position{Line: line, Col: 0}, false)
	end := editor.Position{Line: line + 1, Col: 0}
	if line+1 >= tab.Buffer.LineCount() {
		end = editor.Position{Line: line, Col: len(tab.Buffer.LineRunes(line))}
	}
	tab.MoveCursorTo(end, true)
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

	// Edge detection: the autoScrollEdgeRows at either end of the rect
	// and everything beyond them turn on auto-scroll, faster the
	// further out the pointer is; the middle turns it off.
	top := ey + autoScrollEdgeRows - 1
	bottom := ey + eh - autoScrollEdgeRows
	switch {
	case y <= top:
		a.startAutoScroll(-1, autoScrollStepFor(top-y))
	case y >= bottom:
		a.startAutoScroll(1, autoScrollStepFor(y-bottom))
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
// holds the mouse past an edge. dir is -1 (up) or +1 (down); step is
// the lines per tick. Calling with the same direction only updates the
// step, so the timer is not restarted on every drag motion event.
func (a *App) startAutoScroll(dir, step int) {
	a.mouse.autoScrollStep = step
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

// handleAutoScroll runs once per autoScrollEvent: nudge the viewport in
// the armed direction by the armed step and extend the selection to the
// user's last known mouse cell, clamped into the rect. Bails out (and
// stops the timer) if anything suggests the user is no longer
// drag-selecting (button released, menu opened, no active tab).
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
	tab.Scroll(a.autoScrollDir * max(a.mouse.autoScrollStep, 1))

	ex, ey, ew, eh := a.editorRect()
	localX := min(max(a.lastDragX-ex, 0), ew-1)
	localY := min(max(a.lastDragY-ey, 0), eh-1)
	pos, ok := tab.HitTest(localX, localY, ew, eh)
	if !ok {
		return
	}
	tab.MoveCursorTo(pos, true)
}

// scrollbarGrabWidth is how many editor columns answer to the bar
// painted in the rightmost one: the bar and the cell to its left. The
// same two-cell grab the sidebar's bars get, for the same reason — a
// one-cell target is a miss on a touchscreen, and the cell it borrows
// is the last text column, where a press otherwise just places the
// caret at the end of a long line.
const scrollbarGrabWidth = 2

// scrollbarHit reports whether (x, y) lands on the active tab's
// scrollbar, returning the bar-local row when it does. The geometry
// must mirror Render's: the bar is the rightmost editor column, only
// when the file is taller than the viewport; the grab zone is
// scrollbarGrabWidth columns ending there.
func (a *App) scrollbarHit(x, y int) (int, bool) {
	tab := a.activeTabPtr()
	if tab == nil {
		return 0, false
	}
	ex, ey, ew, eh := a.editorRect()
	if ew <= scrollbarGrabWidth+1 || !tab.ScrollbarVisible(eh) {
		return 0, false
	}
	if x <= ex+ew-1-scrollbarGrabWidth || x > ex+ew-1 || y < ey || y >= ey+eh {
		return 0, false
	}
	return y - ey, true
}

// splitterHit reports whether a press at (x, y) grabs the sidebar's
// resize splitter. The splitter is painted in one column and answers
// to three: itself and a neighbour on each side, because a one-cell
// drag handle is the hardest target on the screen to hit from a phone.
// Both neighbours have owners, and each overlap goes to whichever
// target is PAINTED there: a cell that shows a scrollbar thumb or a git
// change marker is a promise, and a press on it has to keep it. On the
// left that is the sidebar bar's column while a bar is drawn (the bar's
// own grab widens inward instead, see filetree.ScrollbarGrabWidth); on
// the right it is the editor's gutter marker on that row, which opens a
// hunk diff. A plain row cell or an unmarked gutter cell goes to the
// splitter, whose miss is the cheapest — a grab released in place
// changes nothing.
func (a *App) splitterHit(x, y int) bool {
	splitX := a.splitterX()
	if splitX < 0 {
		return false
	}
	switch x {
	case splitX:
		return true
	case splitX - 1:
		return !a.treeScrollbarHit(x, y) && !a.gitPanelScrollbarHit(x, y)
	case splitX + 1:
		return !a.gutterMarkerAt(y)
	}
	return false
}

// gutterMarkerAt reports whether the editor row at screen row y carries
// a git change marker in its gutter column — the one cell of the
// editor's first column that is a click target of its own (see
// openGitHunkAt).
func (a *App) gutterMarkerAt(y int) bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() || a.activeMdPreview() != nil {
		return false
	}
	_, ey, _, eh := a.editorRect()
	if y < ey || y >= ey+eh {
		return false
	}
	return tab.GitLines[tab.ScrollY+(y-ey)] != editor.GitLineNone
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
	a.followCaret(tab)
}

// treeScrollbarHit reports whether (x, y) lands on the file tree's
// scrollbar. The bar is painted in the tree rect's rightmost column,
// the cell immediately LEFT of the resize splitter (sidebarRect is one
// column narrower than the sidebar block), and answers to that column
// and the one to its left (filetree.ScrollbarGrabWidth).
//
// The invariant is that the splitter, the bar and the tree rows occupy
// three distinct column ranges at any y and each keeps its own clicks
// — but the ranges are hit zones, wider than what is painted, and the
// press dispatch resolves them in a fixed order: the splitter first
// (its zone reaches one column into the bar's painted cell only while
// no bar is drawn there, see splitterHit), then the bar (whose grab
// reaches one column into the rows), then the rows. So at any y,
// walking right to left: splitter zone, bar zone, row zone — never
// interleaved, never a cell with two owners.
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

// activeMdPreview returns the active tab's markdown preview state, or
// nil when the active tab is not in preview mode — the one question
// every preview branch in the dispatcher asks first.
func (a *App) activeMdPreview() *mdPreviewState {
	t := a.activeTabPtr()
	if t == nil {
		return nil
	}
	return a.mdPreviewFor(t)
}

// mdPreviewScrollbarHit reports whether (x, y) lands on the preview's
// scrollbar: the editor rect's rightmost column plus the editor bar's
// scrollbarGrabWidth, and only while drawMdPreview paints a bar there
// (a document that fits has none).
func (a *App) mdPreviewScrollbarHit(st *mdPreviewState, x, y int) bool {
	ex, ey, ew, eh := a.editorRect()
	if x <= ex+ew-1-scrollbarGrabWidth || x > ex+ew-1 || y < ey || y >= ey+eh {
		return false
	}
	_, _, ok := scrollbar.Geom(len(st.lines), eh, st.scroll)
	return ok
}

// mdPreviewScrollbarTo scrolls the preview so its thumb centers on
// screen row y — shared by the press and the drag, like every other bar.
func (a *App) mdPreviewScrollbarTo(st *mdPreviewState, y int) {
	_, ey, _, eh := a.editorRect()
	if _, _, ok := scrollbar.Geom(len(st.lines), eh, st.scroll); !ok {
		return
	}
	st.scroll = scrollbar.TargetForThumb(len(st.lines), eh, y-ey)
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
