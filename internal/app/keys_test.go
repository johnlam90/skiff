// =============================================================================
// File: internal/app/keys_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for keys.go — handleKey's routing and the Esc-leader windows.
// The paste cases are the sharp edge: bracketed-paste content must reach
// the buffer as literal text and never be interpreted as leader commands.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// TestHandleKey_EscDoubleTapOpensMenu opens the menu after two Esc presses.
func TestHandleKey_EscDoubleTapOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if a.menuOpen {
		t.Fatal("first Esc should not open menu")
	}
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if !a.menuOpen {
		t.Fatal("second Esc should open menu")
	}
	// Third Esc — menu open, should close.
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if a.menuOpen {
		t.Fatal("Esc with menu open should close it")
	}
}

// TestHandleKey_MenuNavKeys move highlight and Enter activates.
func TestHandleKey_MenuNavKeys(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	first := a.hoveredMenuRow
	a.handleKey(keyEv(tcell.KeyDown, 0))
	if a.hoveredMenuRow == first {
		t.Fatal("Down should advance highlight")
	}
	a.handleKey(keyEv(tcell.KeyUp, 0))
	if a.hoveredMenuRow != first {
		t.Fatalf("Up should return to %d, got %d", first, a.hoveredMenuRow)
	}
}

// TestHandleKey_MenuShortcutAfterNavigation pins how the ≡ menu splits
// typing from shortcuts now that it has a focused type-to-filter field.
// A bare rune is text — it narrows the list and leaves the menu up —
// while Alt+<rune>, which is how tmux and most terminals deliver a fast
// "Esc p", still fires the leader action straight out of the menu. Arrow
// navigation before either must not change that.
func TestHandleKey_MenuShortcutAfterNavigation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()

	a.handleKey(keyEv(tcell.KeyDown, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'p'))

	if !a.menuOpen {
		t.Fatal("a bare rune is filter text — it must not close the menu")
	}
	if got := a.menuFilter.Text(); got != "p" {
		t.Fatalf("bare rune should reach the filter, got %q", got)
	}

	a.clearMenuFilter()
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModAlt))

	if a.menuOpen {
		t.Fatal("Alt+p from the open menu should close the menu")
	}
	if !finderIsOpen(a) {
		t.Fatal("Alt+p from the open menu should open the project finder")
	}
}

// TestHandleKey_RoutesToActiveTab dispatches typing to the active tab.
func TestHandleKey_RoutesToActiveTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	a.handleKey(keyEv(tcell.KeyRune, 'b'))
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'c'))
	a.handleKey(keyEv(tcell.KeyTab, 0))
	a.handleKey(keyEv(tcell.KeyBackspace, 0))
	a.handleKey(keyEv(tcell.KeyHome, 0))
	a.handleKey(keyEv(tcell.KeyEnd, 0))
	a.handleKey(keyEv(tcell.KeyLeft, 0))
	a.handleKey(keyEv(tcell.KeyRight, 0))
	a.handleKey(keyEv(tcell.KeyUp, 0))
	a.handleKey(keyEv(tcell.KeyDown, 0))
	a.handleKey(keyEv(tcell.KeyPgUp, 0))
	a.handleKey(keyEv(tcell.KeyPgDn, 0))
	a.handleKey(keyEv(tcell.KeyDelete, 0))
}

// TestHandleKey_PasteAltRuneInsertsLiteral covers coalescing *inside* a
// paste: tmux can still merge a pasted ESC+rune into one Alt rune. In
// normal typing that means "leader", but during a paste the rune is
// content and must reach the buffer instead of firing an action.
func TestHandleKey_PasteAltRuneInsertsLiteral(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt))
	a.handleEvent(tcell.NewEventPaste(false))

	if a.quit {
		t.Fatal("Alt rune during a paste must not dispatch the leader")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "q" {
		t.Fatalf("Alt rune during a paste should insert its rune, buffer = %q", got)
	}
}

// TestHandleKey_PasteTabInsertsLiteralTab pins that a Tab key between
// paste markers inserts a real \t: the tab came from the source
// document, and expanding it to the file's IndentUnit would silently
// rewrite pasted code.
func TestHandleKey_PasteTabInsertsLiteralTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().IndentUnit = "    " // spaces, so expansion would be visible

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleKey(keyEv(tcell.KeyTab, 0))
	a.handleEvent(tcell.NewEventPaste(false))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "\t" {
		t.Fatalf("Tab during a paste should insert \\t, buffer = %q", got)
	}
}

// TestHandleKey_EnterAutoIndents pins that Enter goes through the editor's
// auto-indent path rather than a bare "\n": the new line opens at the same
// indentation as the one it was split from, in the file's own characters.
func TestHandleKey_EnterAutoIndents(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("func f() {\n\tx := 1\n}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 1, Col: 7} // end of "\tx := 1"
	tab.Anchor = tab.Cursor

	a.handleKey(keyEv(tcell.KeyEnter, 0))

	if got := tab.Buffer.Lines[2]; got != "\t" {
		t.Fatalf("Enter produced %q, want a tab-indented line", got)
	}
}

// TestHandleKey_PasteEnterSkipsAutoIndent keeps pasted code from doubling
// its indentation: text arriving between paste markers already carries the
// source document's leading whitespace on every line.
func TestHandleKey_PasteEnterSkipsAutoIndent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("\tx := 1\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{Line: 0, Col: 7}
	tab.Anchor = tab.Cursor

	a.handleEvent(tcell.NewEventPaste(true))
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	a.handleEvent(tcell.NewEventPaste(false))

	if got := tab.Buffer.Lines[1]; got != "" {
		t.Fatalf("Enter during a paste indented: %q", got)
	}
}

// TestHandleKey_AltArrowsMoveByWord pins the Alt+Left / Alt+Right binding
// and its Shift-extends variant. Alt is safe here in a way Ctrl is not —
// no multiplexer or terminal claims Alt+arrow — and no leader sequence
// uses an arrow, so the tmux Esc-coalescing branch can't be confused by it.
func TestHandleKey_AltArrowsMoveByWord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(target, []byte("alpha beta gamma\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{}
	tab.Anchor = tab.Cursor

	a.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))
	if want := (editor.Position{Line: 0, Col: 5}); tab.Cursor != want {
		t.Fatalf("Alt+Right: cursor = %v, want %v", tab.Cursor, want)
	}
	if tab.HasSelection() {
		t.Fatal("Alt+Right without Shift left a selection")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt|tcell.ModShift))
	if got := tab.SelectionText(); got != " beta" {
		t.Fatalf("Shift+Alt+Right selected %q, want %q", got, " beta")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt))
	if want := (editor.Position{Line: 0, Col: 6}); tab.Cursor != want {
		t.Fatalf("Alt+Left: cursor = %v, want %v", tab.Cursor, want)
	}
}
