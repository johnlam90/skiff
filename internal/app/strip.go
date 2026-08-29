// =============================================================================
// File: internal/app/strip.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// strip.go is ADR-0001 written as code. A strip is the second kind of
// chrome above the editor: where an overlay floats over the editor and
// captures every event, a strip docks at the bottom, reflows the
// editor's rect instead of covering it, owns the keyboard while it is
// up, and deliberately lets the mouse fall through so the user can keep
// clicking and drag-selecting underneath it.
//
// The ADR said all of that in prose while nothing in code said what a
// strip was: the find bar, the project-find bar and the leader
// cheat-strip were wired by hand at nine points — key dispatch, mouse
// dispatch, draw order, layout twice, the flash strip's geometry,
// mutual exclusion, teardown — and the bar's row was computed three
// separate ways. Here they are one interface and one slot (App.strip),
// so "which strip is up" has a single answer and the row has a single
// formula (stripRect, layout.go).
//
// Strips are NOT overlays and never reach a.overlays: the stack's
// outside-click dismissal and input capture would break the
// pass-through, and the stack would have to learn about layout reflow,
// which is app's job. See docs/adr/0001-strips-are-not-overlays.md.

package app

import "github.com/gdamore/tcell/v2"

// rect is a screen rectangle in cells — the shape layout.go's (x, y, w,
// h) tuples describe, named so a strip can be handed its region instead
// of re-deriving it from a.width / a.height. Deriving was how the three
// bars each grew their own copy of the same row formula.
type rect struct{ x, y, w, h int }

// strip is the docked-bar contract: what app has to know about a
// surface to reserve its rows, route input to it and paint it. It is
// deliberately app-side and unexported rather than another prefab in
// internal/overlay — reflowing the editor is layout's business, and the
// overlay package must not learn about it (ADR-0001).
type strip interface {
	// rows is the height the strip reserves above the status bar.
	// editorRect and stripRowBudget subtract it, so a strip that
	// paints outside the rows it claims paints over an editor rect the
	// caret, the scrollbar and every hit-test still believe in.
	rows() int

	// handleKey takes the keystroke. A strip owns the keyboard while
	// it is up — handleKey routes to it ahead of the leader window and
	// of typing — and there is deliberately no "I did not want this
	// one" answer: a strip that could decline a key would need its own
	// precedence rule against the buffer, which is the complexity the
	// overlay stack exists to keep in one place.
	handleKey(ev *tcell.EventKey)

	// handleMouse reports whether the strip consumed the event. false
	// is ADR-0001's pass-through — the editor underneath stays
	// clickable and drag-selectable — and it is a property of the
	// adapter now rather than an absence in mouse.go that reads like a
	// missing branch.
	handleMouse(x, y int, btn tcell.ButtonMask) bool

	// draw paints the strip. r is the region stripRect reserved for it
	// (see rows), so nothing in a draw path needs to know which row a
	// bar lives on.
	draw(r rect)

	// close is the teardown hook dropStrip runs once the slot is
	// empty: whatever the strip left outside itself — the tab's match
	// highlights, the in-flight sweep's generation — goes with it. It
	// is what lets closeAllModals tear down "the strip" without naming
	// the strips.
	close()
}

// The three strips, checked at compile time so a method that drifts out
// of the interface is a build failure rather than a surface that
// quietly stops being routable.
var (
	_ strip = (*findStrip)(nil)
	_ strip = projFindStrip{}
	_ strip = leaderStrip{}
)

// dropStrip empties the slot and then runs the occupant's teardown —
// slot first, hook after, the same order closeAllModals pops an overlay
// before calling Dismiss. A hook that opens the next surface can then
// never be undone by the teardown that preceded it.
func (a *App) dropStrip() {
	s := a.strip
	if s == nil {
		return
	}
	a.strip = nil
	s.close()
}
