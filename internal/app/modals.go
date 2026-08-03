// =============================================================================
// File: internal/app/modals.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// modals.go holds the openers for the prefab overlays — prompt, confirm,
// info, and dirty-close (see internal/overlay for their behavior) — plus
// closeAllModals and the right-click context menu, which is still an
// App-side modal pending its own conversion.
//
// Every surface stays mutually exclusive with every other: opening any
// one calls closeAllModals() first, and the overlay stack replaces on
// open, so two can never be up at once.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/overlay"
)

// contextMenuWidth is fixed so the popup geometry stays predictable —
// the labels are short enough to fit comfortably.
const contextMenuWidth = 19

// closeAllModals dismisses every modal in one shot and parks any in-flight
// drag / auto-scroll state. Every "open this modal" helper calls it first
// so the modals stay mutually exclusive and a stale drag from before the
// modal opened can't keep extending a selection underneath it. The find
// bar is torn down through closeFind — one teardown path — so the
// replace row and the tab's match highlights go with it rather than
// staying armed under the modal.
func (a *App) closeAllModals() {
	// A modal opening over a pick cancels it properly — popped first,
	// hook after — so a preview hook (the theme picker's live preview)
	// gets reverted rather than stranded. The pop happens before the
	// hook so the hook can never re-enter this teardown.
	if pick, ok := a.overlays.Top().(*overlay.Pick); ok {
		a.overlays.Close()
		if pick.OnCancel != nil {
			pick.OnCancel()
		}
	}
	a.overlays.Close()
	a.menuOpen = false
	a.closeFind()
	a.diffPanelRow = -1
	a.projFindOpen = false
	a.projFindValue = nil
	a.projFindCursor = 0
	a.projFindScroll = 0
	a.projFindMatches = nil
	a.projFindFolded = nil
	a.projFindBusy = false
	a.hoveredMenuRow = -1
	a.dragMode = ""
	a.stopAutoScroll()
}

// anyModalOpen reports whether an overlay — a floating surface that
// captures all input — is on screen. Strips (the find bar, the
// project-find bar) are deliberately absent: they pass mouse through so
// the editor stays interactive underneath them, and counting them here
// would suppress editor behaviours (drag auto-scroll) that must keep
// working while a strip is up. See docs/adr/0001-strips-are-not-overlays.md.
func (a *App) anyModalOpen() bool {
	return a.overlays.IsOpen()
}

// -----------------------------------------------------------------------------
// Prompt modal (text input + OK / Cancel)
// -----------------------------------------------------------------------------

// openPrompt shows a single-line text input overlay — an
// overlay.Prompt prefab. title is the heading, hint is a small subtitle
// (e.g. "in /path/to/folder"), initial pre-fills the input field, and
// callback runs with the trimmed value when the user confirms with
// Enter or clicks OK. An empty submit is ignored.
func (a *App) openPrompt(title, hint, initial string, callback func(*App, string)) {
	a.closeAllModals()
	p := &overlay.Prompt{Title: title, Hint: hint, Hover: 1, Theme: a.theme}
	p.Field.SetText(initial)
	p.Size = func() (int, int) { return a.width, a.height }
	p.Close = func() { a.closeAllModals() }
	if callback != nil {
		p.OnSubmit = func(v string) { callback(a, v) }
	}
	a.overlays.Open(p)
}

// -----------------------------------------------------------------------------
// Confirm modal (Yes / No)
// -----------------------------------------------------------------------------

// openConfirm shows a Yes/No confirmation overlay — an
// overlay.Confirm prefab. message is the body text; callback runs only
// when the user picks Yes. Default focus lands on No so an accidental
// Enter is harmless — important for destructive actions like Delete.
// Returns the prefab so flows that must react to dismissal (the
// formatter trust prompts) can attach OnCancel; the hook dies with the
// overlay, so it can never leak into an unrelated confirm.
func (a *App) openConfirm(title, message string, callback func(*App)) *overlay.Confirm {
	a.closeAllModals()
	c := &overlay.Confirm{Title: title, Message: message, Theme: a.theme}
	c.Size = func() (int, int) { return a.width, a.height }
	c.Close = func() { a.closeAllModals() }
	if callback != nil {
		c.OnYes = func() { callback(a) }
	}
	a.overlays.Open(c)
	return c
}

// openInfo shows the single-button report overlay — an overlay.Info —
// for passive reporting: most importantly, the full stderr from a
// failed custom action where the status-bar flash isn't enough room.
// Empty input falls back to a single "(no output captured)" line so the
// dialog never looks broken.
func (a *App) openInfo(title string, lines []string) {
	a.closeAllModals()
	if len(lines) == 0 {
		lines = []string{"(no output captured)"}
	}
	n := &overlay.Info{Title: title, Lines: lines, Theme: a.theme}
	n.Size = func() (int, int) { return a.width, a.height }
	n.Close = func() { a.closeAllModals() }
	a.overlays.Open(n)
}

// btnRect is a one-row button hit zone shared by a modal's draw and
// mouse paths so the highlight and the click can't disagree. Used by
// the diff view's [ Open file ] / [ Close ] pair.
type btnRect struct {
	x, y, w int
}

// contains reports whether the cell (px, py) falls inside the button.
// A zero-width rect (an unarmed button) contains nothing.
func (r btnRect) contains(px, py int) bool {
	return r.w > 0 && py == r.y && px >= r.x && px < r.x+r.w
}

// -----------------------------------------------------------------------------
// Save / Discard / Cancel modal (unsaved-changes prompt)
// -----------------------------------------------------------------------------

// openDirtyClose shows the unsaved-changes overlay — an overlay.Dirty
// prefab with Cancel / Discard / Save. saveCB runs when the user picks
// Save (typically: save the tab(s), then proceed); discardCB when they
// pick Discard (skip saving, proceed anyway). Cancel just dismisses.
func (a *App) openDirtyClose(title, message string, saveCB, discardCB func(*App)) {
	a.closeAllModals()
	d := &overlay.Dirty{Title: title, Message: message, Theme: a.theme}
	d.Size = func() (int, int) { return a.width, a.height }
	d.Close = func() { a.closeAllModals() }
	if saveCB != nil {
		d.OnSave = func() { saveCB(a) }
	}
	if discardCB != nil {
		d.OnDiscard = func() { discardCB(a) }
	}
	a.overlays.Open(d)
}

// -----------------------------------------------------------------------------
// Tree right-click context menu
// -----------------------------------------------------------------------------

// openTreeContext opens the right-click popup over the file tree — an
// overlay.Popup anchored near (x, y). The items shown depend on whether
// n is a file or a folder; renaming or deleting the project root is
// intentionally not allowed. Each item is a closure over the node, so
// the popup itself needs no tree knowledge.
func (a *App) openTreeContext(n *filetree.Node, x, y int) {
	a.closeAllModals()

	items := []overlay.PopupItem{}
	add := func(label string, action func(*App, *filetree.Node)) {
		items = append(items, overlay.PopupItem{Label: label, OnPick: func() { action(a, n) }})
	}
	if n.IsDir {
		add("New File", ctxNewFile)
	}
	if n != a.tree.Root {
		add("Rename", ctxRename)
		add("Delete", ctxDelete)
		add("Cut", ctxCutNode)
		add("Copy", ctxCopyNode)
		add("Duplicate", ctxDuplicateNode)
	}
	if a.hasFileClip() {
		add("Paste here", ctxPasteNode)
	}
	add("Copy rel path", ctxCopyRelativePath)
	add("Copy abs path", ctxCopyAbsolutePath)

	a.openPopup(items, x, y)
}

// openPopup places and opens an anchored action popup — the shared tail
// of the tree context menu and the git extras menu.
func (a *App) openPopup(items []overlay.PopupItem, x, y int) {
	pop := &overlay.Popup{
		Items: items,
		Theme: a.theme,
		At:    overlay.PlacePopup(a.width, a.height, x, y, contextMenuWidth, len(items)),
	}
	pop.Close = func() { a.closeAllModals() }
	a.overlays.Open(pop)
}

// -----------------------------------------------------------------------------
// Drawing helpers shared across modals.
// -----------------------------------------------------------------------------

// fillRect paints a rectangle of (w x h) cells starting at (x, y) with the
// given style.
func fillRect(scr tcell.Screen, x, y, w, h int, st tcell.Style) {
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, st)
		}
	}
}

// drawBorder draws a single-line box border around the rectangle.
func drawBorder(scr tcell.Screen, x, y, w, h int, st tcell.Style) {
	scr.SetContent(x, y, '┌', nil, st)
	scr.SetContent(x+w-1, y, '┐', nil, st)
	scr.SetContent(x, y+h-1, '└', nil, st)
	scr.SetContent(x+w-1, y+h-1, '┘', nil, st)
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, '─', nil, st)
		scr.SetContent(cx, y+h-1, '─', nil, st)
	}
	for cy := y + 1; cy < y+h-1; cy++ {
		scr.SetContent(x, cy, '│', nil, st)
		scr.SetContent(x+w-1, cy, '│', nil, st)
	}
}

// drawHDivider draws a horizontal divider with ├ ┤ end caps inside an
// existing border.
func drawHDivider(scr tcell.Screen, x, y, w int, st tcell.Style) {
	scr.SetContent(x, y, '├', nil, st)
	scr.SetContent(x+w-1, y, '┤', nil, st)
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, '─', nil, st)
	}
}

// drawButton renders a "button" — really just bracketed label — at (x, y).
// Active buttons get a tinted background so they read as the focused option.
func drawButton(scr tcell.Screen, x, y int, label string, modalBG tcell.Color, fg tcell.Color, focused bool) {
	bg := modalBG
	st := tcell.StyleDefault.Background(bg).Foreground(fg).Bold(true)
	if focused {
		// Focused button: invert — the label sits on a tinted block.
		st = tcell.StyleDefault.Background(fg).Foreground(modalBG).Bold(true)
	}
	col := 0
	for _, r := range label {
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// runeLen returns the visible cell count of s (one cell per rune).
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// trimSpace strips ASCII whitespace from both ends of s. Tiny dependency-free
// substitute for strings.TrimSpace so this file doesn't grow imports.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
