// =============================================================================
// File: internal/app/cheatsheet.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// cheatsheet.go is the Esc ? shortcut reference: the whole Esc-leader
// table, grouped, plus the paragraph about the ≡ menu that no amount of
// key-poking teaches. It is the discoverability answer that does not
// grow the flat menu — one row ("Keyboard shortcuts…") buys the user
// every binding at once.
//
// The binding half is GENERATED from leaderBindings() through
// leaderDisplayGroups. Nothing here restates a key or a label, so the
// reference cannot survive a rebind while still printing the old
// gesture — the failure mode that makes most in-app cheat sheets worse
// than none.
//
// leaderstrip.go renders the same table while the leader window is
// armed, and the two share exactly what matters — the table. They do
// not share their formatting, and shouldn't: the strip paints styled
// segments that wrap across the width of the terminal, this paints a
// fixed-column block into a scrollable overlay body. Folding those into
// one "renderer" would couple two layouts that have nothing in common
// but their input.

package app

import "fmt"

// cheatSheetWidth is the column budget every authored line in this file
// respects: the usable text width of an overlay.Info frame on a
// minWidth-wide terminal (frame minus a border and a pad cell on each
// side). Pinned by a test rather than eyeballed, because the whole
// point of the overlay is to be readable in the narrow tmux pane where
// the user has forgotten a binding.
const cheatSheetWidth = minWidth - 4

// cheatSheetLines builds the body of the shortcut overlay: two lines of
// framing, one titled block per leader group, then the ≡ menu section.
//
// Groups come from leaderDisplayGroups, which is fed by the live
// dispatch table — so adding a binding to leader.go adds a row here and
// removing one removes it, with no second list to remember.
func cheatSheetLines() []string {
	groups := leaderDisplayGroups()
	bindings := leaderBindings()
	help := menuHelpLines()
	// Three intro rows, a blank + heading per group, every binding, the
	// spacer before the ≡ section, and the section itself.
	lines := make([]string, 0, 3+2*len(groups)+len(bindings)+1+len(help))
	lines = append(lines,
		"No Ctrl shortcuts — Esc is the",
		"only leader. Tap Esc, then the key,",
		"within half a second.")
	for _, g := range groups {
		lines = append(lines, "", g.title)
		for _, b := range g.bindings {
			// One rune per trigger, so a fixed key column aligns every
			// description without measuring anything.
			lines = append(lines, fmt.Sprintf("  Esc %c   %s", b.key, b.desc))
		}
	}
	lines = append(lines, "")
	return append(lines, help...)
}

// menuHelpLines is the hand-written half of the reference — the part
// that isn't a keystroke. It covers the three things a user cannot
// deduce by pressing keys: that ≡ and a double Esc open the same menu,
// that the menu filters across every group at once, and that
// right-click is a convenience rather than a door, because tmux and
// macOS Terminal routinely swallow Button3.
//
// Lines are pre-wrapped to cheatSheetWidth: overlay.Info truncates
// rather than wraps, and a reference that loses its last word on a
// narrow pane fails exactly where it is needed. The budget got tighter
// when minWidth dropped to 40 — these lines are the ones a phone reads.
func menuHelpLines() []string {
	return []string{
		"The ≡ menu",
		"  Click ≡ at the top-left, or tap",
		"  Esc twice. Right-click opens it",
		"  too, but tmux and macOS Terminal",
		"  often swallow that — ≡ and Esc",
		"  Esc always work.",
		"  Type to filter every action at",
		"  once: \"sb\" finds \"Switch",
		"  branch…\". Enter runs the best",
		"  match; Esc clears the filter,",
		"  Esc again closes the menu.",
	}
}

// menuKeyboardShortcuts opens the shortcut reference — the ≡ →
// "Keyboard shortcuts…" row and the Esc ? leader behind one method.
//
// It rides overlay.Info rather than a bespoke surface because the
// content is exactly what Info already is: a read-only, scrollable,
// left-aligned body with a single way out (Esc, Enter, the OK button,
// or a click outside). A hand-rolled overlay would re-implement the
// scroll clamp and the dismissal keys to no visible benefit.
func (a *App) menuKeyboardShortcuts() {
	a.closeMenu()
	a.openInfo("Keyboard shortcuts", cheatSheetLines())
}
