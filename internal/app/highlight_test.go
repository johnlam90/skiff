// =============================================================================
// File: internal/app/highlight_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// TestKeystroke_HighlightLandsOffLoop pins the shape of a keystroke
// now: the frame after typing paints from a patched grid without
// touching the lexer, a background re-lex is in flight, and once it
// lands the grid equals what a synchronous window lex would produce.
func TestKeystroke_HighlightLandsOffLoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\n// a comment\nfunc main() {\n\tx := 1\n}\n"), 0o644)
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	a.draw()
	if tab.HighlightPending() || a.highlight.Busy() {
		t.Fatal("a freshly opened tab should have an exact grid and no lex in flight")
	}

	a.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	a.draw()
	if !tab.HighlightPending() {
		t.Fatal("the keystroke frame should paint a patched grid pending a re-lex")
	}
	if !a.highlight.Busy() {
		t.Fatal("draw should have started the background re-lex")
	}
	pumpUntil(t, a, "highlight landing", idle(&a.highlight))
	if tab.HighlightPending() {
		t.Fatal("the landing should have cleared the pending grid")
	}
	_, _, _, eh := a.editorRect()
	want, _, _ := editor.HighlightWindow(path, tab.Buffer.Lines, tab.ScrollY, eh, a.theme)
	for i := range want {
		if len(want[i]) != len(tab.Styles[i]) {
			t.Fatalf("line %d: %d styles after landing, want %d", i, len(tab.Styles[i]), len(want[i]))
		}
		for j := range want[i] {
			if want[i][j] != tab.Styles[i][j] {
				t.Fatalf("line %d rune %d: landed style differs from a synchronous lex", i, j)
			}
		}
	}
}

// TestHighlight_ClosedTabLandingIsDropped pins that a re-lex landing
// after its tab was closed is ignored rather than applied to a tab
// nothing displays — the handler checks membership before touching it.
func TestHighlight_ClosedTabLandingIsDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n"), 0o644)
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	a.draw()
	a.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	a.draw()
	if !a.highlight.Busy() {
		t.Fatal("no re-lex in flight")
	}
	a.tabs.Remove(tab)
	pumpUntil(t, a, "highlight landing", idle(&a.highlight))
	if !tab.HighlightPending() {
		t.Fatal("the landing was applied to a closed tab")
	}
}
