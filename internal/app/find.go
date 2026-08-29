// =============================================================================
// File: internal/app/find.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// find.go owns the in-file search UI: findStrip, the 1-row "Find:" bar
// that docks directly above the status bar, and the Esc-f / ≡ menu
// entry points that put it in App's strip slot (see strip.go).
//
// The matching logic itself lives on Tab (see internal/editor/find.go) so
// each tab carries its own query, match list, and current-index. The two
// input fields are overlay.Field values (the same single-line input the
// prompt, form and finder use), so this file only owns the strip: which
// field has the keyboard, the routing keys, and the painting.

package app

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

// findBarHeight is the cell height of the find bar. Always 1 — the bar is
// a single row pinned above the status bar. It is what findStrip.rows
// answers, so editorRect / stripRowBudget charge exactly this much for
// the bar without knowing which strip is up.
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

// findStrip is the in-file find bar. Unlike the project-find panel —
// whose state has its own bundle on App because a background sweep
// reads it — the bar's state lives here: the query and the match list
// belong to the active Tab, so the two fields and the replace row's
// open/focus flags are the whole of what the strip knows, and nothing
// outside it reads them. They left App with the slot, so a closed bar
// is no state at all rather than five fields that must be remembered to
// reset.
type findStrip struct {
	a *App

	// query and replace (Tab opens the second one) are the same
	// overlay.Field the prompt, form and finder use: the strip owns
	// which field has the keyboard, the field owns the text, the caret
	// and its horizontal window.
	query   overlay.Field
	replace overlay.Field

	// replaceOpen is whether Tab has grown the replace field; focus
	// says which of the two owns the keyboard.
	replaceOpen  bool
	focusReplace bool
}

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
	a.closeAllModals() // a modal (or the other strip) would eat our keystrokes
	a.strip = &findStrip{a: a}
}

// findBar returns the find bar when it is the strip that is up, else
// nil. The slot is the open flag — there is no second boolean that can
// disagree with it — so this is how everything asks.
func (a *App) findBar() *findStrip {
	s, _ := a.strip.(*findStrip)
	return s
}

// findBarOpen reports whether the in-file find bar owns the strip slot.
func (a *App) findBarOpen() bool {
	return a.findBar() != nil
}

// closeFind hides the find bar, and findStrip.close clears the active
// tab's find state so the highlights disappear with it. Leaving them
// painted after close is surprising — users expect Esc to mean "I'm
// done searching." It drops the slot only when the bar is the strip
// that is up, so a stray call can't dismiss the project-find panel.
func (a *App) closeFind() {
	if a.findBarOpen() {
		a.dropStrip()
	}
}

// rows is findBarHeight: the bar reserves the single row above the
// status bar, and editorRect gives up exactly that much.
func (s *findStrip) rows() int { return findBarHeight }

// close clears the active tab's find state. The bar owns no highlights
// itself; this is the one thing it leaves behind, so dropping the slot
// has to take it too.
func (s *findStrip) close() {
	if tab := s.a.activeTabPtr(); tab != nil {
		tab.ClearFind()
	}
}

// handleMouse passes every event through to the editor underneath. This
// is ADR-0001's pass-through — clicking and drag-selecting stay live
// while the bar is open — expressed as the adapter's answer rather than
// as an absent branch in mouse.go, which an architecture review once
// misread as a missing feature. Do not "fix" it by consuming clicks on
// the bar's own row: see docs/adr/0001-strips-are-not-overlays.md.
func (s *findStrip) handleMouse(int, int, tcell.ButtonMask) bool { return false }

// handleKey dispatches a keystroke while the find bar is focused.
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
func (s *findStrip) handleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		s.a.closeFind()
		return
	case tcell.KeyTab:
		// Tab grows the bar a replace field (druk's gesture) and then
		// toggles which field owns the keyboard.
		if !s.replaceOpen {
			s.replaceOpen = true
			s.focusReplace = true
		} else {
			s.focusReplace = !s.focusReplace
		}
		return
	case tcell.KeyEnter:
		s.enter(ev.Modifiers()&tcell.ModShift != 0)
		return
	}
	s.editKey(ev)
}

// applyQuery pushes the current input text into the active tab's find
// state and snaps the cursor to the new "current" match (so the user can
// see their result while still typing). Called on every input change so
// the highlights track the query live.
func (s *findStrip) applyQuery() {
	tab := s.a.activeTabPtr()
	if tab == nil {
		return
	}
	tab.SetFindQuery(s.query.Text())
	tab.FocusCurrentMatch()
}

// next is the Enter-in-the-bar action: jump to the next match (with
// wrap).
func (s *findStrip) next() {
	if tab := s.a.activeTabPtr(); tab != nil {
		tab.FindNext()
	}
}

// prev is the Shift-Enter action: jump to the previous match.
func (s *findStrip) prev() {
	if tab := s.a.activeTabPtr(); tab != nil {
		tab.FindPrev()
	}
}

// enter resolves Enter for whichever field has the keyboard: the query
// field walks the match list, the replace field rewrites the current
// match (shift: all of them).
func (s *findStrip) enter(shift bool) {
	if !s.focusReplace {
		if shift {
			s.prev()
		} else {
			s.next()
		}
		return
	}
	tab := s.a.activeTabPtr()
	if tab == nil {
		return
	}
	if shift {
		if n := tab.ReplaceAllMatches(s.replace.Text()); n > 0 {
			s.a.flash(fmt.Sprintf("Replaced %d match(es)", n))
		}
		return
	}
	if !tab.ReplaceCurrentMatch(s.replace.Text()) {
		s.a.flash("Nothing to replace")
	}
}

// editKey hands a non-routing key to the focused field and re-runs the
// search when the query's text actually changed — a caret move must not
// re-snap the editor onto the current match. Same "edit, then react to
// the edit" shape as menu.go's filter and the finder's query.
func (s *findStrip) editKey(ev *tcell.EventKey) {
	if s.focusReplace {
		s.replace.HandleKey(ev)
		return
	}
	before := len(s.query.Value)
	s.query.HandleKey(ev)
	if len(s.query.Value) != before {
		s.applyQuery()
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

// draw renders the 1-row find bar into the rect layout reserved for it.
// Layout (left to right):
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
func (s *findStrip) draw(r rect) {
	a := s.a
	bx, by, bw := r.x, r.y, r.w

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
	if s.replaceOpen && s.focusReplace {
		hint = " Enter: replace · Shift+Enter: all · Tab: query · Esc: close "
	}
	counter := s.counterText()
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
		if s.hasNoMatches() {
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
	if s.replaceOpen {
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
	replaceFocused := s.replaceOpen && s.focusReplace
	s.query.Draw(a.screen, inputStart, by, inputWidth, barStyle, !replaceFocused)
	if s.replaceOpen {
		rw := (rightTextStart - 1) - replaceStart
		if rw < 1 {
			rw = 1
		}
		s.replace.Draw(a.screen, replaceStart, by, rw, barStyle, replaceFocused)
	}
}

// counterText renders the "N of M" indicator. Returns "" when there is
// no query so the renderer can skip drawing the field entirely.
func (s *findStrip) counterText() string {
	if len(s.query.Value) == 0 {
		return ""
	}
	tab := s.a.activeTabPtr()
	if tab == nil {
		return ""
	}
	if len(tab.FindMatches) == 0 {
		return "no results"
	}
	return fmt.Sprintf("%d of %d", tab.FindIndex+1, len(tab.FindMatches))
}

// hasNoMatches reports whether the user has typed a query that returned
// zero hits, so the counter can flip color.
func (s *findStrip) hasNoMatches() bool {
	if len(s.query.Value) == 0 {
		return false
	}
	tab := s.a.activeTabPtr()
	return tab != nil && len(tab.FindMatches) == 0
}
