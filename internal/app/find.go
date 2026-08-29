// =============================================================================
// File: internal/app/find.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// find.go owns the in-file search UI: the 1-row "Find:" bar that lives
// directly above the status bar, the keystroke dispatch while the bar is
// focused, and the Esc-f / Esc-g leader entry points.
//
// The matching logic itself lives on Tab (see internal/editor/find.go) so
// each tab carries its own query, match list, and current-index. The two
// input fields are overlay.Field values (the same single-line input the
// prompt, form and finder use), so this file only owns the strip: which
// field has the keyboard, the routing keys, and the layout.

package app

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

// findBarHeight is the cell height of the find bar. Always 1 — the bar is
// a single row pinned above the status bar. Pulled out as a constant so
// editorRect / find rect math reads as "subtract the find bar's row" at
// every call site.
const findBarHeight = 1

// minFieldWidth is the fewest cells a find bar will shrink its input to
// before it starts dropping the labels on its right instead. Twelve is
// about the shortest window a query stays readable in while it is being
// typed: the caret window slides one cell at a time, so a narrower field
// degrades into a porthole showing the tail of what you typed. The
// labels lose that contest on purpose — a dropped hint can be recalled
// by widening the terminal, and a dropped counter by reading the results
// list, but text the user is composing exists nowhere else on screen.
const minFieldWidth = 12

// barLabelsThatFit decides which of a find bar's two right-hand labels
// get painted. spare is what is left of the bar once the input has been
// given minFieldWidth cells, and each label costs its own width plus the
// gap separating it from its neighbour. The counter is offered the space
// first: it reports what this search found, which the hint — a fixed
// reminder of the keys — never does.
//
// Returning the decision instead of drawing is what lets both bars share
// one priority order. They used to carry two copies of a check that
// compared each label against the bar's left-hand label and never
// against the other label or the input, so both could be drawn onto
// cells the input needed and the input, painted last, was left to
// overlap them.
func barLabelsThatFit(spare, counterCost, hintCost int) (counter, hint bool) {
	if counterCost > 0 && counterCost <= spare {
		counter = true
		spare -= counterCost
	}
	hint = hintCost > 0 && hintCost <= spare
	return counter, hint
}

// openFind shows the find bar with an empty input. We don't pre-fill
// the user's last query because closing the bar already clears find
// state — Esc means "I'm done searching." Each Esc-f opens a fresh
// search.
func (a *App) openFind() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	a.closeAllModals() // a modal would otherwise eat our keystrokes
	a.findOpen = true
	a.findField = overlay.Field{}
}

// closeFind hides the find bar AND clears the active tab's find state
// so the highlights disappear with the bar. Leaving them painted after
// close is surprising — users expect Esc to mean "I'm done searching."
// Esc-g after a closed bar simply re-opens the bar so the user can type
// a fresh query.
func (a *App) closeFind() {
	a.findOpen = false
	a.findField = overlay.Field{}
	a.replaceOpen = false
	a.replaceField = overlay.Field{}
	a.findFocusReplace = false
	if tab := a.activeTabPtr(); tab != nil {
		tab.ClearFind()
	}
}

// findApplyQuery pushes the current input text into the active tab's
// find state and snaps the cursor to the new "current" match (so the
// user can see their result while still typing). Called on every input
// change so the highlights track the query live.
func (a *App) findApplyQuery() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	tab.SetFindQuery(a.findField.Text())
	tab.FocusCurrentMatch()
}

// findNext is the Enter-in-the-bar action: jump to the next match (with
// wrap). Also reachable from the Esc-g leader.
func (a *App) findNext() {
	if tab := a.activeTabPtr(); tab != nil {
		tab.FindNext()
	}
}

// findPrev is the Shift-Enter action: jump to the previous match.
func (a *App) findPrev() {
	if tab := a.activeTabPtr(); tab != nil {
		tab.FindPrev()
	}
}

// menuFind is the action menu entry point. Behaves identically to the
// Esc-f leader — opens the bar against the active tab.
func (a *App) menuFind() {
	a.closeMenu()
	a.openFind()
}

// hasFindable reports whether the active tab is a text tab — used to
// gray out the menu row on image tabs / no-tab states.
func (a *App) hasFindable() bool {
	t := a.activeTabPtr()
	return t != nil && !t.IsImage()
}

// findBarRect returns the on-screen rectangle of the find bar. Always
// the row directly above the status bar; height is findBarHeight.
// Caller is expected to check a.findOpen before drawing.
func (a *App) findBarRect() (x, y, w, h int) {
	sw := a.sidebarW()
	return sw, a.height - 1 - findBarHeight, a.width - sw, findBarHeight
}

// handleFindKey dispatches a keystroke while the find bar is focused.
// The strip owns only the keys that route rather than type:
//
//	Esc                     close the bar
//	Tab                     open / swap focus to the replace field
//	Enter                   next match, or replace when the replace
//	                        field has the keyboard
//	Shift+Enter             previous match, or replace-all
//
// Everything else is text editing and belongs to the focused
// overlay.Field — the same split prompt.go and form.go use. Keys no
// field claims (Up/Down, function keys) are dropped on the floor: the
// find bar owns the keyboard while it's open.
func (a *App) handleFindKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeFind()
		return
	case tcell.KeyTab:
		// Tab grows the bar a replace field (druk's gesture) and then
		// toggles which field owns the keyboard.
		if !a.replaceOpen {
			a.replaceOpen = true
			a.findFocusReplace = true
		} else {
			a.findFocusReplace = !a.findFocusReplace
		}
		return
	case tcell.KeyEnter:
		a.findEnter(ev.Modifiers()&tcell.ModShift != 0)
		return
	}
	a.findEditKey(ev)
}

// findEnter resolves Enter for whichever field has the keyboard: the
// query field walks the match list, the replace field rewrites the
// current match (shift: all of them).
func (a *App) findEnter(shift bool) {
	if !a.findFocusReplace {
		if shift {
			a.findPrev()
		} else {
			a.findNext()
		}
		return
	}
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	if shift {
		if n := tab.ReplaceAllMatches(a.replaceField.Text()); n > 0 {
			a.flash(fmt.Sprintf("Replaced %d match(es)", n))
		}
		return
	}
	if !tab.ReplaceCurrentMatch(a.replaceField.Text()) {
		a.flash("Nothing to replace")
	}
}

// findEditKey hands a non-routing key to the focused field and re-runs
// the search when the query's text actually changed — a caret move must
// not re-snap the editor onto the current match. Same "edit, then react
// to the edit" shape as menu.go's filter and the finder's query.
func (a *App) findEditKey(ev *tcell.EventKey) {
	if a.findFocusReplace {
		a.replaceField.HandleKey(ev)
		return
	}
	before := len(a.findField.Value)
	a.findField.HandleKey(ev)
	if len(a.findField.Value) != before {
		a.findApplyQuery()
	}
}

// drawFindBar renders the 1-row find bar at the bottom of the editor
// area. Layout (left to right):
//
//	" Find: <input>                       3 of 12   Enter: next · Esc: close "
//
// The hint on the right is dropped first when the window is too narrow
// to fit it; the match counter is dropped next; the input yields to
// neither, because it is the whole point of the bar. Where the bar is
// wide enough to grant it, the input gets at least minFieldWidth cells —
// that is the budget the labels are measured against. Narrower than
// that there is nothing left to drop on its behalf, so the input simply
// takes what remains, down to a single cell; narrower still and the bar
// paints no field at all and hides the cursor with it.
func (a *App) drawFindBar() {
	if !a.findOpen {
		return
	}
	bx, by, bw, _ := a.findBarRect()

	bg := a.theme.LineHL
	barStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	labelStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	emptyStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Error).Bold(true)

	// Clear the row.
	for cx := bx; cx < bx+bw; cx++ {
		a.screen.SetContent(cx, by, ' ', nil, barStyle)
	}

	// "Find:" label.
	label := " Find: "
	drawAt(a.screen, bx, by, label, labelStyle)
	inputStart := bx + runeLen(label)

	// Right side: counter + hint. They are placed before the input so
	// the input can be clipped against them, but they only get the cells
	// left over once the input has minFieldWidth — the input outranks
	// both, and the counter outranks the hint.
	hint := " Enter: next · Shift+Enter: prev · Tab: replace · Esc: close "
	if a.replaceOpen && a.findFocusReplace {
		hint = " Enter: replace · Shift+Enter: all · Tab: query · Esc: close "
	}
	counter := a.findCounterText()
	counterCost := 0
	if counter != "" {
		counterCost = runeLen(counter) + 2
	}
	hintCost := runeLen(hint) + 1
	spare := (bx + bw) - (inputStart + minFieldWidth + 1)
	showCounter, showHint := barLabelsThatFit(spare, counterCost, hintCost)

	rightTextStart := bx + bw
	if showHint {
		rightTextStart -= hintCost
		drawAt(a.screen, rightTextStart, by, hint, mutedStyle)
	}
	if showCounter {
		// Right-align before the hint, or against the bar's right edge
		// when the hint was dropped. Color the counter red on a query
		// with no matches so the user gets immediate negative feedback
		// without having to read the digits.
		rightTextStart -= counterCost
		style := mutedStyle
		if a.findHasNoMatches() {
			style = emptyStyle
		}
		drawAt(a.screen, rightTextStart, by, counter, style)
	}

	// Input geometry. The fields carry their own caret windows, so the
	// bar only decides how many cells each gets — and they end one cell
	// short of the right-hand text, because Field.Draw blanks its whole
	// box plus a cell either side before painting and would otherwise
	// erase a label rather than share its cells.
	//
	// The labels have already yielded everything they could by here, so
	// reaching this bail means the bar cannot hold its own label plus a
	// single cell of input — a terminal narrower than anything the
	// layout can serve. HideCursor is not optional on that path:
	// Field.Draw owns this frame's only ShowCursor call, so returning
	// without it strands the hardware cursor wherever the editor's
	// render parked it, blinking over static text and pointing at a
	// buffer that does not have the keyboard.
	inputEnd := rightTextStart - 1
	if inputEnd <= inputStart {
		a.screen.HideCursor()
		return
	}
	// With the replace field open, the query keeps the left half and
	// the replacement takes the right — both always visible, the caret
	// in whichever owns the keyboard.
	replaceStart := 0
	if a.replaceOpen {
		half := inputStart + (inputEnd-inputStart)/2
		rlabel := " ⇒ "
		drawAt(a.screen, half, by, rlabel, labelStyle)
		replaceStart = half + runeLen(rlabel)
		inputEnd = half - 1
	}
	inputWidth := inputEnd - inputStart
	if inputWidth < 1 {
		inputWidth = 1
	}
	// Field.Draw carries the caret window and the ShowCursor call, so
	// the focused flag is the whole "where does the caret blink"
	// decision. It also clears one cell either side of the field, which
	// the layout leaves blank on purpose: the label's trailing space and
	// the gap before the " ⇒ " marker / the right-hand text.
	replaceFocused := a.replaceOpen && a.findFocusReplace
	a.findField.Draw(a.screen, inputStart, by, inputWidth, barStyle, !replaceFocused)
	if a.replaceOpen {
		rw := (rightTextStart - 1) - replaceStart
		if rw < 1 {
			rw = 1
		}
		a.replaceField.Draw(a.screen, replaceStart, by, rw, barStyle, replaceFocused)
	}
}

// findCounterText renders the "N of M" indicator. Returns "" when there
// is no query so the renderer can skip drawing the field entirely.
func (a *App) findCounterText() string {
	if len(a.findField.Value) == 0 {
		return ""
	}
	tab := a.activeTabPtr()
	if tab == nil {
		return ""
	}
	if len(tab.FindMatches) == 0 {
		return "no results"
	}
	return fmt.Sprintf("%d of %d", tab.FindIndex+1, len(tab.FindMatches))
}

// findHasNoMatches reports whether the user has typed a query that
// returned zero hits, so the counter can flip color.
func (a *App) findHasNoMatches() bool {
	if len(a.findField.Value) == 0 {
		return false
	}
	tab := a.activeTabPtr()
	return tab != nil && len(tab.FindMatches) == 0
}
