// =============================================================================
// File: internal/app/leader_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
)

// TestLeaderActionFor_AllBindingsResolve walks the binding table and
// verifies every entry returns a non-nil action. Catches accidentally
// dropping a method reference when the table is reshuffled.
func TestLeaderActionFor_AllBindingsResolve(t *testing.T) {
	for _, b := range leaderBindings() {
		if leaderActionFor(b.key) == nil {
			t.Errorf("binding %q resolved to nil", b.key)
		}
	}
}

// TestLeaderActionFor_UnboundReturnsNil pins down the contract that
// leaderActionFor reports a miss with nil so handleKey can distinguish
// "leader fired" from "key was unbound — fall through".
func TestLeaderActionFor_UnboundReturnsNil(t *testing.T) {
	if leaderActionFor('y') != nil {
		t.Fatal("'y' should not be a leader binding (no editor action mapped)")
	}
}

// TestHandleKey_LeaderSave saves the active tab via Esc, s. The buffer
// is dirtied before the leader fires so the assertion is meaningful:
// a successful save flips the dirty flag back to false.
func TestHandleKey_LeaderSave(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'x')) // dirty the buffer
	if !a.activeTabPtr().Dirty {
		t.Fatal("expected dirty buffer before save")
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if a.activeTabPtr().Dirty {
		t.Fatal("Esc-s should have saved the buffer (dirty still true)")
	}
}

// TestHandleKey_LeaderUndoRedo round-trips an edit through Esc-u and
// Esc-r. Pins down both bindings at once and the fact that the leader
// state resets between sequences (we re-arm Esc each time).
func TestHandleKey_LeaderUndoRedo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'u'))
	if a.activeTabPtr().Buffer.Lines[0] != "" {
		t.Fatalf("Esc-u should have undone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'r'))
	if a.activeTabPtr().Buffer.Lines[0] != "a" {
		t.Fatalf("Esc-r should have redone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}
}

// TestHandleKey_LeaderToggleSidebar flips sidebarShown via Esc-t. The
// toggle is the simplest leader action with no preconditions, so it's
// the most stable smoke test that the dispatch wiring is intact.
func TestHandleKey_LeaderToggleSidebar(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	before := a.sidebarShown
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 't'))
	if a.sidebarShown == before {
		t.Fatalf("Esc-t should toggle sidebar (still %v)", a.sidebarShown)
	}
}

// TestHandleKey_LeaderToggleLineComment binds Esc-/ to the same action menu
// path, giving keyboard users a fast toggle without adding Ctrl shortcuts.
func TestHandleKey_LeaderToggleLineComment(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("one\ntwo"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, '/'))

	if got := a.activeTabPtr().Buffer.String(); got != "// one\ntwo" {
		t.Fatalf("Esc-/ should comment the cursor line, got %q", got)
	}
}

// TestHandleKey_LeaderGitChanges flips the sidebar to the Git panel via
// Esc-g inside a real repo — pinning both the binding and the fact that
// the panel's guard passes when a branch is detected.
func TestHandleKey_LeaderGitChanges(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "f.txt"), "x\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	a := newTestApp(t, dir)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'g'))

	if !a.gitPanelActive {
		t.Fatal("Esc-g should switch the sidebar to the Git panel")
	}
}

// TestHandleKey_LeaderQuit sets a.quit via Esc-q. We test this directly
// rather than through Run() so we don't have to drive the event loop —
// the quit flag is what Run() polls each tick.
func TestHandleKey_LeaderQuit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'q'))
	if !a.quit {
		t.Fatal("Esc-q should set a.quit = true")
	}
}

// TestHandleKey_LeaderUnboundFallsThrough is the regression test for the
// "stray Esc shouldn't swallow the next keystroke" property: pressing
// Esc and then an unbound letter must still deliver that letter to the
// active tab. Without the fall-through, an accidental Esc tap would
// silently eat the user's next character.
func TestHandleKey_LeaderUnboundFallsThrough(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'y'))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "y" {
		t.Fatalf("unbound key after Esc should reach the editor, got %q", got)
	}
}

// TestHandleKey_LeaderTimesOut verifies the leader state fully expires:
// once menuEscMs has passed since the last Esc, a bound letter must
// reach the editor as a normal keystroke instead of firing the action
// or the timeout hint.
func TestHandleKey_LeaderTimesOut(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	// Backdate the Esc timestamp past every window so the next 's'
	// is treated as a plain keystroke rather than Save or a hint.
	a.lastEscape = time.Now().Add(-2 * menuEscMs)
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "s" {
		t.Fatalf("expired leader window: 's' should insert literally, got %q", got)
	}
}

// TestHandleKey_ExpiredLeaderRuneHints is the timing-cliff regression
// test: a bound rune landing after the leader window but inside the
// menu window (a slow "Esc s" over a laggy SSH link) used to insert
// the rune into the buffer while the user believed they had saved.
// It must be swallowed once with a flash explaining what happened.
func TestHandleKey_ExpiredLeaderRuneHints(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.lastEscape = time.Now().Add(-800 * time.Millisecond) // between the windows
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("slow leader rune must not reach the buffer, got %q", got)
	}
	if !strings.Contains(a.statusMsg, "timed out") {
		t.Fatalf("expected a timeout hint flash, got %q", a.statusMsg)
	}

	// Unbound runes keep the fall-through contract even in the grace
	// window: a stray Esc must not eat ordinary typing.
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.lastEscape = time.Now().Add(-800 * time.Millisecond)
	a.handleKey(keyEv(tcell.KeyRune, 'y'))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "y" {
		t.Fatalf("unbound rune in the grace window should insert, got %q", got)
	}
}

// TestHandleKey_EscDoubleTapStillOpensMenu makes sure adding the leader
// table didn't break the existing double-Esc-opens-menu gesture. The
// second Esc inside the leader window must still be interpreted as
// "open the menu," not as an unbound leader keystroke.
func TestHandleKey_EscDoubleTapStillOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if !a.menuOpen {
		t.Fatal("double-Esc should still open the menu after leader was added")
	}
}

// TestHandleKey_EscDoubleTapWideWindowOpensMenu pins the tmux-friendly
// menu window: tmux's escape-time munches a fast Esc,Esc into ONE
// delivered Esc, so the only tap spacing that produces two separate
// events is slower than the old 500ms window allowed. Two Escs up to
// menuEscMs apart must still open the menu, while the leader keeps its
// tighter doubleEscMs window (next test).
func TestHandleKey_EscDoubleTapWideWindowOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	// Simulate a second tap arriving 800ms later — beyond the leader
	// window, inside the menu window.
	a.lastEscape = time.Now().Add(-800 * time.Millisecond)
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if !a.menuOpen {
		t.Fatal("second Esc 800ms later should still open the menu")
	}
}

// TestHandleKey_LeaderWindowStaysTight is the counterpart: the wider
// menu window must NOT widen the leader itself. A bound rune 800ms
// after Esc never fires its action — a slow Esc-s must not save behind
// the user's back; it lands in the expired-leader grace path instead
// (swallowed with a hint, pinned by TestHandleKey_ExpiredLeaderRuneHints).
func TestHandleKey_LeaderWindowStaysTight(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'x')) // dirty the buffer

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.lastEscape = time.Now().Add(-800 * time.Millisecond)
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if !a.activeTabPtr().Dirty {
		t.Fatal("a bound rune 800ms after Esc must not fire the leader action")
	}
	if strings.Contains(a.statusMsg, "Saved") {
		t.Fatalf("no save flash expected, got %q", a.statusMsg)
	}
}

// TestHandleKey_AltRuneFiresLeader is the regression test for the tmux
// coalescing bug: with a non-zero escape-time, a fast Esc,s reaches the
// editor as a single Alt+s event instead of two keystrokes. That event
// must fire the leader action — and must NOT type the rune into the
// buffer, which is what shipped users saw ("Esc-s inserts an s").
func TestHandleKey_AltRuneFiresLeader(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'x')) // dirty the buffer
	if !a.activeTabPtr().Dirty {
		t.Fatal("expected dirty buffer before save")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt))

	if a.activeTabPtr().Dirty {
		t.Fatal("Alt+s (coalesced Esc,s) should have saved the buffer")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "x" {
		t.Fatalf("Alt+s must not insert the rune, buffer = %q", got)
	}
}

// TestHandleKey_AltRuneUnboundSwallowed pins the "never insert
// Alt-modified runes" rule: an Alt rune is always a mangled Esc
// sequence, so an unbound one is dropped rather than typed. Falling
// through to InsertRune here is exactly the buffer-corruption bug.
func TestHandleKey_AltRuneUnboundSwallowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModAlt))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("unbound Alt rune must be swallowed, buffer = %q", got)
	}
}

// TestHandleKey_AltEscTogglesMenu covers the coalesced double-Esc:
// tmux turns a fast Esc,Esc into one Alt+Esc event, which must toggle
// the menu exactly like a real double-tap (open when shut, close when
// open) instead of merely arming the leader window.
func TestHandleKey_AltEscTogglesMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModAlt))
	if !a.menuOpen {
		t.Fatal("Alt+Esc (coalesced double-Esc) should open the menu")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModAlt))
	if a.menuOpen {
		t.Fatal("Alt+Esc with the menu open should close it")
	}
}

// TestHandleKey_LeaderWrapToggle drives the Esc, z gesture end to end:
// it must flip soft wrap off for the open tab (and back on again),
// matching the ≡ menu row it shadows.
func TestHandleKey_LeaderWrapToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // the toggle persists config
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	if !a.activeTabPtr().Wrap {
		t.Fatal("wrap should start on by default")
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z'))
	if a.wrapOn || a.activeTabPtr().Wrap {
		t.Fatal("Esc z should turn wrap off")
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z'))
	if !a.wrapOn || !a.activeTabPtr().Wrap {
		t.Fatal("second Esc z should turn wrap back on")
	}
}

// TestHandleKey_LeaderCopyCutPaste round-trips the clipboard bindings:
// Esc-c copies the selection, Esc-x cuts it, Esc-v pastes it back.
// These were deliberately unbound while "the terminal's Cmd+C already
// covers copy" — but with mouse reporting on, the terminal never sees
// a selection, so the editor needs its own keys (see leader.go).
func TestHandleKey_LeaderCopyCutPaste(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 5}, true) // "hello"
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'c'))
	if a.clipBuf != "hello" {
		t.Fatalf("Esc-c: clipBuf = %q, want %q", a.clipBuf, "hello")
	}

	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 6}, true) // "hello "
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'x'))
	if a.clipBuf != "hello " {
		t.Fatalf("Esc-x: clipBuf = %q, want %q", a.clipBuf, "hello ")
	}
	if got := tab.Buffer.Lines[0]; got != "world" {
		t.Fatalf("Esc-x should remove the selection from the buffer, line = %q", got)
	}

	tab.MoveCursorTo(editor.Position{Line: 0, Col: 5}, false)
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'v'))
	if got := tab.Buffer.Lines[0]; got != "worldhello " {
		t.Fatalf("Esc-v: line = %q, want %q", got, "worldhello ")
	}
}
