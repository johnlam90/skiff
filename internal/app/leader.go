// =============================================================================
// File: internal/app/leader.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// leader.go defines the editor's Esc-leader hotkey table. Esc-Esc still opens
// the action menu (handled in handleKey); the bindings here handle the
// "Esc, then one rune within doubleEscWindow" sequences for common
// actions. We deliberately avoid Ctrl-key shortcuts because they fight
// tmux/zellij prefixes and the terminal's own bindings — Esc is the only
// modifier we trust over SSH.

package app

// leaderBinding is one Esc-leader entry: the trigger rune and the App method
// that fires when the user presses Esc, <rune> in quick succession. Each method
// already handles its own preconditions — calling menuUndo with no active tab,
// for example, is a safe no-op — so the leader dispatch doesn't need to
// re-check enable predicates.
type leaderBinding struct {
	key    rune
	action func(*App)
	// desc is the two-or-three-word label the cheat-strip renders next
	// to the key while the leader window is armed. See leaderstrip.go.
	desc string
	// group is the cheat-overlay heading this binding files under. The
	// names deliberately match the ≡ menu's top-level groups so a user
	// learns one vocabulary, not two. See leaderDisplayGroups.
	group string
}

// leaderBindings is the editor's full Esc-leader table — the single
// source for every surface that talks about shortcuts: the dispatch
// (leaderActionFor), the armed-window cheat-strip (leaderstrip.go), and
// the Esc ? overlay (cheatsheet.go). Nothing hand-writes a second copy,
// because a shortcut list that can disagree with the dispatch teaches a
// gesture that does nothing.
//
// The slice order is presentational for the strip; the overlay regroups
// it by the group field. Letter bindings are chosen to be mnemonic and
// avoid collisions; punctuation bindings mirror familiar editor
// gestures where they make sense.
//
// c / x / v are bound even though the host terminal has its own
// Cmd+C/V: with mouse reporting on (always, in skiff) the terminal and
// any multiplexer never build a selection of their own, so Cmd+C has
// nothing to grab — the editor's clipboard keys are the only keyboard
// path. Mouse users get select-to-copy on drag release (handleMouse).
//
// b / e / % are borrowed straight from vi's motions (back a word, end of
// word, jump to the matching bracket) because that is the vocabulary a
// terminal user already has. Alt+Left / Alt+Right run the same two word
// motions for people whose terminal sends them; see keys.go.
//
// Intentionally not bound:
//   - rename / delete / revert — destructive enough that we want the
//     menu's confirm dialog to gate the action as a deliberate gesture.
func leaderBindings() []leaderBinding {
	return []leaderBinding{
		{'s', (*App).menuSave, "save", "File"},
		{'n', (*App).menuNewFile, "new file", "File"},
		{'w', (*App).menuClose, "close tab", "File"},
		{'o', (*App).menuReopenTab, "reopen tab", "File"},
		{'u', (*App).menuUndo, "undo", "Edit"},
		{'r', (*App).menuRedo, "redo", "Edit"},
		{'c', (*App).menuCopy, "copy", "Edit"},
		{'x', (*App).menuCut, "cut", "Edit"},
		{'v', (*App).menuPaste, "paste", "Edit"},
		{'/', (*App).menuToggleLineComment, "comment", "Edit"},
		{'k', (*App).menuMoveLineUp, "line up", "Edit"},
		{'j', (*App).menuMoveLineDown, "line down", "Edit"},
		{'d', (*App).menuDuplicateLine, "duplicate", "Edit"},
		{'f', (*App).openFind, "find", "Go"},
		{'F', (*App).menuFindInProject, "find in project", "Go"},
		{'l', (*App).menuGoToLine, "goto line", "Go"},
		{'p', (*App).openFinder, "open file", "Go"},
		{'b', (*App).menuMoveWordLeft, "word left", "Go"},
		{'e', (*App).menuMoveWordRight, "word right", "Go"},
		{'%', (*App).menuGoToMatchingBracket, "match bracket", "Go"},
		{'g', (*App).focusGitPanel, "git panel", "Git"},
		{'t', (*App).menuToggleSidebar, "sidebar", "View"},
		{'z', (*App).menuToggleWrap, "wrap", "View"},
		{'?', (*App).menuKeyboardShortcuts, "shortcuts", "View"},
		{'q', (*App).menuQuit, "quit", "Quit"},
	}
}

// leaderActionFor looks up the App method bound to r in the leader table,
// or returns nil when r isn't bound. Returning nil rather than a no-op
// lets the caller distinguish "leader fired" from "key was unbound — fall
// through to normal handling", which matters for typing flow: pressing
// Esc then a non-leader letter must still let that letter reach the
// editor's normal key handler.
func leaderActionFor(r rune) func(*App) {
	for _, b := range leaderBindings() {
		if b.key == r {
			return b.action
		}
	}
	return nil
}

// leaderGroup is one titled block of the shortcut overlay: a heading
// and the bindings filed under it, in leaderBindings order.
type leaderGroup struct {
	title    string
	bindings []leaderBinding
}

// leaderGroupOrder is the order the shortcut overlay prints the
// headings in. It mirrors the ≡ menu's top-level groups so both
// surfaces answer "where does this action live?" the same way; a
// binding tagged with something not listed here is still shown, in a
// trailing heading of its own.
func leaderGroupOrder() []string {
	return []string{"File", "Edit", "Go", "Git", "View", "Quit"}
}

// leaderDisplayGroups buckets the live binding table into printable
// groups — the form cheatsheet.go renders.
func leaderDisplayGroups() []leaderGroup {
	return groupLeaderBindings(leaderBindings())
}

// groupLeaderBindings is the pure half of leaderDisplayGroups, split out
// so the drop-nothing guarantee can be tested against a synthetic table
// instead of only against whatever leaderBindings happens to hold today.
//
// Every binding lands in exactly one group and none can be dropped: an
// unrecognised group name appends a new heading rather than falling on
// the floor, because a shortcut missing from the cheat sheet is
// precisely the drift this indirection exists to prevent. Empty
// headings are removed, so a heading whose bindings all left leaves no
// orphan title behind.
func groupLeaderBindings(bindings []leaderBinding) []leaderGroup {
	order := leaderGroupOrder()
	at := make(map[string]int, len(order))
	groups := make([]leaderGroup, 0, len(order))
	for _, title := range order {
		at[title] = len(groups)
		groups = append(groups, leaderGroup{title: title})
	}
	for _, b := range bindings {
		i, ok := at[b.group]
		if !ok {
			i = len(groups)
			at[b.group] = i
			groups = append(groups, leaderGroup{title: b.group})
		}
		groups[i].bindings = append(groups[i].bindings, b)
	}
	// Filter in place: the write index never overtakes the read index,
	// so no second backing array is needed.
	kept := groups[:0]
	for _, g := range groups {
		if len(g.bindings) > 0 {
			kept = append(kept, g)
		}
	}
	return kept
}
