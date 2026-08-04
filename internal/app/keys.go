// =============================================================================
// File: internal/app/keys.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// keys.go is the keyboard entry point. There are intentionally no Ctrl-
// keyed shortcuts: they collide with terminal flow control and tmux/zellij
// prefixes, so Esc is the only command key. Esc arms a leader window whose
// bindings live in leader.go, and handleKey is where that window, the
// strips (find, project find), the overlay stack, and plain typing get
// ordered against each other.

package app

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
)

// handleKey responds to keyboard events. There are intentionally no Ctrl-
// based shortcuts: every action lives behind the ≡ menu so the editor never
// fights the terminal (Ctrl-S/Q flow control) or a tmux/zellij prefix. The
// only "command" key is Esc, which closes the menu and acts as the leader
// for the hotkey table in leader.go (Esc s = Save, Esc u = Undo, etc.).
func (a *App) handleKey(ev *tcell.EventKey) {
	// During a bracketed paste every key is verbatim content. Raw ESC
	// bytes inside the paste are stripped — delivering them would close
	// whatever modal is open or arm the leader — and the leader window
	// is kept disarmed so pasted text can never fire an action.
	if a.pasting {
		a.lastEscape = time.Time{}
		if ev.Key() == tcell.KeyEsc {
			return
		}
	}

	// The overlay stack owns the whole keyboard while an overlay is up —
	// each overlay's handler understands Esc (cancel), Enter (submit /
	// activate), and the keys relevant to its layout. Strips come next:
	// keyboard-focused but mouse-transparent, and never up at the same
	// time as an overlay because every opener runs closeAllModals first.
	if ov := a.overlays.Top(); ov != nil {
		ov.HandleKey(ev)
		return
	}
	if a.findOpen {
		a.handleFindKey(ev)
		return
	}
	if a.projFindOpen {
		a.handleProjFindKey(ev)
		return
	}

	// tmux (and other multiplexers) with a non-zero escape-time coalesce
	// a fast Esc-then-key into one Alt-modified event: Esc,s arrives as
	// Alt+s and a quick double-Esc as Alt+Esc. Treat both as the gesture
	// the user actually made. An Alt rune is never inserted — outside a
	// paste it is always a mangled Esc sequence, not text.
	if !a.pasting && ev.Modifiers()&tcell.ModAlt != 0 {
		a.lastEscape = time.Time{}
		switch ev.Key() {
		case tcell.KeyEsc:
			// A key arriving here means no overlay is up (the stack
			// routes first), so this can only be an open gesture.
			a.openMenu()
			return
		case tcell.KeyRune:
			if action := leaderActionFor(ev.Rune()); action != nil {
				action(a)
			}
			return
		}
	}

	if ev.Key() == tcell.KeyEsc {
		// Esc is the editor's only command key: it opens the menu on
		// the SECOND Esc within menuEscWindow, while a SINGLE Esc arms the
		// leader table (see below). The close half of the toggle lives
		// in handleMenuKey — with the menu up, the overlay stack routes
		// keys there before this code can run. A lone Esc that isn't
		// followed by a leader binding within the window is
		// intentionally a no-op so the key still feels harmless to
		// mash — and because tmux can munch a fast double-tap into one
		// Esc, "mash Esc until the menu appears" must always work.
		now := time.Now()
		if !a.lastEscape.IsZero() && now.Sub(a.lastEscape) < menuEscWindow {
			a.openMenu()
			a.lastEscape = time.Time{}
			return
		}
		a.lastEscape = now
		// Wake the event loop just after the window closes so the
		// status bar's "Esc…" tag clears itself — the draw cycle only
		// runs on events, and an abandoned Esc may not be followed by
		// one.
		scr := a.screen
		time.AfterFunc(menuEscWindow+50*time.Millisecond, func() {
			_ = scr.PostEvent(&leaderExpiryEvent{when: time.Now()})
		})
		// A second, earlier wake-up right after the leader window shuts
		// so the cheat-strip disappears on time instead of lingering
		// until the menu-window expiry above.
		time.AfterFunc(doubleEscWindow+50*time.Millisecond, func() {
			_ = scr.PostEvent(&leaderExpiryEvent{when: time.Now()})
		})
		return
	}
	// Esc-leader hotkey windows — shared with handleMenuKey so the
	// leader table behaves identically whether or not the menu is up.
	if a.leaderWindowIntercept(ev) {
		return
	}
	// Any other key cancels a pending Esc so a stale half-tap doesn't
	// surprise the user later.
	a.lastEscape = time.Time{}

	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	// Image-preview tabs are read-only — no cursor, no editing, no
	// caret movement. Drop every key here so the user can mash arrow
	// keys without anything mysterious happening behind the splash.
	if tab.IsImage() {
		return
	}
	extend := ev.Modifiers()&tcell.ModShift != 0

	switch ev.Key() {
	case tcell.KeyUp:
		tab.MoveCursor(-1, 0, extend)
	case tcell.KeyDown:
		tab.MoveCursor(1, 0, extend)
	case tcell.KeyLeft:
		tab.MoveCursor(0, -1, extend)
	case tcell.KeyRight:
		tab.MoveCursor(0, 1, extend)
	case tcell.KeyHome:
		tab.MoveLineHome(extend)
	case tcell.KeyEnd:
		tab.MoveLineEnd(extend)
	case tcell.KeyPgUp:
		_, h := a.editorSize()
		tab.MoveCursor(-h, 0, extend)
	case tcell.KeyPgDn:
		_, h := a.editorSize()
		tab.MoveCursor(h, 0, extend)
	case tcell.KeyEnter:
		tab.InsertString("\n")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		tab.Backspace()
	case tcell.KeyDelete:
		tab.Delete()
	case tcell.KeyTab:
		// A Tab inside a paste is a literal \t from the source text;
		// expanding it to IndentUnit would rewrite pasted code.
		if a.pasting {
			tab.InsertString("\t")
		} else {
			tab.InsertString(tab.IndentUnit)
		}
	case tcell.KeyRune:
		tab.InsertRune(ev.Rune())
	}
}

// leaderWindowIntercept applies the Esc-leader windows to one key event.
// Within doubleEscWindow a bound rune fires its action; within the longer
// menuEscWindow grace window a bound rune is swallowed with a hint instead —
// a slow "Esc s" over a laggy link is almost always a save attempt, not
// typing, and the old behavior silently inserted the rune. Returns true
// when the key was consumed either way.
func (a *App) leaderWindowIntercept(ev *tcell.EventKey) bool {
	if !a.lastEscape.IsZero() && time.Since(a.lastEscape) < doubleEscWindow {
		if ev.Key() == tcell.KeyRune {
			if action := leaderActionFor(ev.Rune()); action != nil {
				a.lastEscape = time.Time{}
				action(a)
				return true
			}
		}
	}
	if !a.lastEscape.IsZero() && time.Since(a.lastEscape) < menuEscWindow {
		if ev.Key() == tcell.KeyRune && leaderActionFor(ev.Rune()) != nil {
			a.lastEscape = time.Time{}
			r := ev.Rune()
			a.flash(fmt.Sprintf("Esc %c timed out — tap Esc, then %c right after", r, r))
			return true
		}
	}
	return false
}
