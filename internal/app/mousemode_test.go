// =============================================================================
// File: internal/app/mousemode_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-05
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for mousemode.go. The interesting assertions here are about
// bytes on the wire, not app fields: the whole point of the change is
// that skiff stops asking the terminal for all-motion tracking, and the
// only honest proof of that is the escape sequence the screen actually
// writes. tcell's SimulationScreen throws mouse flags away
// (simulation.go's EnableMouse just sets a bool), so the transition
// tests drive a real terminfo-backed tScreen over a recording Tty and
// grep its output. The behavioral half — every core gesture still works
// with motion off — runs on the usual SimulationScreen app.

package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/theme"
)

// The xterm private modes tcell emits for each tracking level. Asserting
// on these strings rather than on tcell.MouseFlags is deliberate: the
// flags are our request, these are what the far end of the SSH link
// actually receives.
const (
	modeButtons   = "\x1b[?1000h" // press / release reporting
	modeDrag      = "\x1b[?1002h" // + motion while a button is held
	modeAllMotion = "\x1b[?1003h" // + motion with NO button — the flood
	modeSGR       = "\x1b[?1006h" // SGR extended coordinates
	modeDisable   = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l"
)

// recTty is a tcell.Tty that never produces input and records every byte
// the screen writes. It is the only way to observe the mouse-mode escape
// sequences, which is what these tests are actually about.
type recTty struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	wake chan struct{}
	once sync.Once
}

// newRecTty builds a recording tty whose reader blocks until the screen
// tears down, so tcell's input goroutine parks instead of spinning on
// an immediate EOF.
func newRecTty() *recTty { return &recTty{wake: make(chan struct{})} }

// Start satisfies tcell.Tty; there is no real terminal state to save.
func (r *recTty) Start() error { return nil }

// Drain releases the blocked reader — tcell calls it before Stop.
func (r *recTty) Drain() error { r.release(); return nil }

// Stop releases the blocked reader and ends the session.
func (r *recTty) Stop() error { r.release(); return nil }

// Close releases the blocked reader for good.
func (r *recTty) Close() error { r.release(); return nil }

// release unblocks Read exactly once, however many teardown hooks fire.
func (r *recTty) release() { r.once.Do(func() { close(r.wake) }) }

// NotifyResize is a no-op: this tty never changes size.
func (r *recTty) NotifyResize(func()) {}

// WindowSize reports a roomy terminal so no layout hits a minimum.
func (r *recTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: 120, Height: 40}, nil
}

// Read blocks until teardown, then reports EOF so tcell's input loop
// exits cleanly.
func (r *recTty) Read([]byte) (int, error) { <-r.wake; return 0, io.EOF }

// Write records everything the screen emits.
func (r *recTty) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

// take returns and clears everything written since the previous take, so
// each assertion looks only at the bytes its own step produced.
func (r *recTty) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.buf.String()
	r.buf.Reset()
	return s
}

// ttyApp builds an App over a real terminfo screen writing into a
// recording tty, wired the way New() leaves it: baseline mouse flags on
// the screen and the same value cached on the App. Everything the screen
// wrote while initialising is discarded, so the first take() a test does
// sees only what that test caused.
func ttyApp(t *testing.T) (*App, *recTty) {
	t.Helper()
	// "xterm" is compiled into the terminfo package (terminfo/base is
	// imported by default), so this resolves with no TERM and no
	// infocmp — CI has neither.
	ti, err := terminfo.LookupTerminfo("xterm")
	if err != nil {
		t.Fatalf("terminfo: %v", err)
	}
	tty := newRecTty()
	scr, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		t.Fatalf("screen: %v", err)
	}
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { scr.Fini() })

	a := &App{
		screen:         scr,
		theme:          theme.Default(),
		rootDir:        t.TempDir(),
		hoveredMenuRow: -1,
		diffPanelRow:   -1,
		sidebarWidth:   defaultSidebarWidth,
		wrapOn:         true,
	}
	a.width, a.height = scr.Size()

	tty.take() // drop the init preamble
	scr.EnableMouse(mouseBaseFlags)
	a.mouseFlags = mouseBaseFlags
	return a, tty
}

// TestMouseMode_BaselineNeverAsksForAllMotion is the headline: what a
// freshly started skiff puts on the wire. Clicks, drags and SGR
// coordinates yes; `?1003h` — an event per pointer pixel, forever, over
// a phone's uplink — no.
func TestMouseMode_BaselineNeverAsksForAllMotion(t *testing.T) {
	_, tty := ttyApp(t)
	out := tty.take()

	for _, want := range []string{modeButtons, modeDrag, modeSGR} {
		if !strings.Contains(out, want) {
			t.Fatalf("baseline is missing %q; wrote %q", want, out)
		}
	}
	if strings.Contains(out, modeAllMotion) {
		t.Fatalf("baseline enabled all-motion tracking; wrote %q", out)
	}
}

// TestMouseMode_HoverSurfaceAddsMotionAndClosingDropsIt walks the whole
// point of the feature: the menu hovers, so opening it buys 1003 and
// closing it hands it straight back.
func TestMouseMode_HoverSurfaceAddsMotionAndClosingDropsIt(t *testing.T) {
	a, tty := ttyApp(t)
	tty.take()

	a.overlays.Open(menuOverlay{a})
	a.syncMouseMode()
	out := tty.take()
	if !strings.Contains(out, modeAllMotion) {
		t.Fatalf("opening the menu should enable all-motion; wrote %q", out)
	}
	if a.mouseFlags != mouseHoverFlags {
		t.Fatalf("cached flags = %v, want %v", a.mouseFlags, mouseHoverFlags)
	}

	a.overlays.Close()
	a.syncMouseMode()
	out = tty.take()
	if strings.Contains(out, modeAllMotion) {
		t.Fatalf("closing the menu should not re-enable all-motion; wrote %q", out)
	}
	if !strings.Contains(out, modeButtons) || !strings.Contains(out, modeDrag) {
		t.Fatalf("closing the menu must restore the baseline; wrote %q", out)
	}
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("cached flags = %v, want %v", a.mouseFlags, mouseBaseFlags)
	}
}

// TestMouseMode_UnchangedStateWritesNothing pins the idempotence rule.
// syncMouseMode runs after every single event — a mouse sweep with the
// menu up is hundreds of them — and tcell's EnableMouse re-emits the
// full disable-then-enable run on every call. Spending that on the slow
// link this feature exists to protect would be self-defeating.
func TestMouseMode_UnchangedStateWritesNothing(t *testing.T) {
	a, tty := ttyApp(t)
	tty.take()

	for range 5 {
		a.syncMouseMode()
	}
	if out := tty.take(); out != "" {
		t.Fatalf("re-syncing the baseline wrote %q, want nothing", out)
	}

	a.overlays.Open(menuOverlay{a})
	a.syncMouseMode()
	tty.take() // the one legitimate transition

	for range 5 {
		a.syncMouseMode()
	}
	if out := tty.take(); out != "" {
		t.Fatalf("re-syncing with the menu up wrote %q, want nothing", out)
	}
}

// TestMouseMode_PerOverlayMotionOptOut checks the surface classification
// rather than the transport. Info (a stderr dump or diff preview the
// user reads and scrolls, ignoring everything but the wheel and Button1)
// and Form (Button1 only) get no motion; every hovering surface does.
func TestMouseMode_PerOverlayMotionOptOut(t *testing.T) {
	a, tty := ttyApp(t)

	cases := []struct {
		name string
		open func()
		want tcell.MouseFlags
	}{
		{"info", func() { a.overlays.Open(&overlay.Info{Theme: a.theme}) }, mouseBaseFlags},
		{"form", func() { a.overlays.Open(&overlay.Form{Theme: a.theme}) }, mouseBaseFlags},
		{"menu", func() { a.overlays.Open(menuOverlay{a}) }, mouseHoverFlags},
		{"confirm", func() { a.overlays.Open(&overlay.Confirm{Theme: a.theme}) }, mouseHoverFlags},
		{"pick", func() { a.overlays.Open(&overlay.Pick{Theme: a.theme}) }, mouseHoverFlags},
		{"popup", func() { a.overlays.Open(&overlay.Popup{Theme: a.theme}) }, mouseHoverFlags},
		{"prompt", func() { a.overlays.Open(&overlay.Prompt{Theme: a.theme}) }, mouseHoverFlags},
		{"dirty", func() { a.overlays.Open(&overlay.Dirty{Theme: a.theme}) }, mouseHoverFlags},
		{"none", func() { a.overlays.Close() }, mouseBaseFlags},
	}
	for _, tc := range cases {
		tc.open()
		if got := a.wantMouseFlags(); got != tc.want {
			t.Errorf("%s: wantMouseFlags = %v, want %v", tc.name, got, tc.want)
		}
	}

	// And the opt-out really does stay off the wire.
	a.overlays.Close()
	a.syncMouseMode()
	tty.take()
	a.overlays.Open(&overlay.Info{Theme: a.theme})
	a.syncMouseMode()
	if out := tty.take(); out != "" {
		t.Fatalf("opening Info changed the mouse mode; wrote %q", out)
	}
}

// TestMouseMode_ReconcilesThroughHandleEvent is the anti-stranding test.
// No opener or closer calls syncMouseMode itself — handleEvent does,
// after the dispatch — so the mode has to be right no matter which path
// put a surface up or took it down, including closeAllModals yanking one
// out from under an action.
func TestMouseMode_ReconcilesThroughHandleEvent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.syncMouseMode()
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("idle app: flags = %v, want baseline", a.mouseFlags)
	}

	// Right-click in the editor opens the action menu.
	ex, ey, _, _ := a.editorRect()
	a.handleEvent(tcell.NewEventMouse(ex+2, ey+1, tcell.Button3, 0))
	if !a.menuOpen {
		t.Fatal("right-click should have opened the menu")
	}
	if a.mouseFlags != mouseHoverFlags {
		t.Fatalf("menu open: flags = %v, want hover", a.mouseFlags)
	}

	// Esc dismisses it, and the mode follows without the closer knowing.
	a.handleEvent(keyEv(tcell.KeyEsc, 0))
	if a.overlays.IsOpen() {
		t.Fatal("Esc should have dismissed the menu")
	}
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("menu closed: flags = %v, want baseline", a.mouseFlags)
	}

	// The blunt teardown path: a surface up, then closeAllModals rips it
	// away mid-event. The next reconcile still lands on baseline.
	a.openMenu()
	a.syncMouseMode()
	if a.mouseFlags != mouseHoverFlags {
		t.Fatalf("reopened menu: flags = %v, want hover", a.mouseFlags)
	}
	a.closeAllModals()
	a.syncMouseMode()
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("after closeAllModals: flags = %v, want baseline", a.mouseFlags)
	}
}

// TestMouseMode_CloseLeavesNoTracking covers shutdown. Whatever mode the
// last overlay left us in, the terminal the user gets back must not
// still be reporting the mouse — Fini's finalize path is what guarantees
// that, and this is the test that would catch it regressing.
func TestMouseMode_CloseLeavesNoTracking(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, tty := ttyApp(t)

	// Exit from the worst case: all-motion tracking still armed.
	a.overlays.Open(menuOverlay{a})
	a.syncMouseMode()
	tty.take()

	a.Close()
	out := tty.take()
	if !strings.Contains(out, modeDisable) {
		t.Fatalf("Close must disable mouse tracking; wrote %q", out)
	}
	for _, mode := range []string{modeButtons, modeDrag, modeAllMotion, modeSGR} {
		if strings.Contains(out, mode) {
			t.Fatalf("Close re-enabled %q; wrote %q", mode, out)
		}
	}
}

// TestBaselineMouse_CoreGesturesNeedNoMotionEvents is the regression
// fence for the desktop experience. Under `?1000h` + `?1002h` a terminal
// sends presses, releases, motion WITH a button held, and the wheel —
// and nothing else. This feeds exactly that alphabet and walks every
// core gesture, so a gesture that had quietly come to depend on
// button-less motion would fail here. It also proves no ordinary
// gesture flips the mode.
func TestBaselineMouse_CoreGesturesNeedNoMotionEvents(t *testing.T) {
	dir := t.TempDir()
	seedTreeFiles(t, dir, 80)
	path := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("hello world\n", 300)), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.syncMouseMode()
	a.draw() // the real loop always paints before the first event

	// press/drag/release/wheel only — the 1002 alphabet.
	press := func(x, y int) { a.handleEvent(tcell.NewEventMouse(x, y, tcell.Button1, 0)) }
	release := func(x, y int) { a.handleEvent(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0)) }
	wheel := func(x, y int, b tcell.ButtonMask) { a.handleEvent(tcell.NewEventMouse(x, y, b, 0)) }

	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()

	// 1. A click places the caret.
	press(ex+8, ey+2)
	release(ex+8, ey+2)
	if tab.Cursor.Line != tab.ScrollY+2 {
		t.Fatalf("click should place the caret on the clicked row, got line %d", tab.Cursor.Line)
	}

	// 2. Press, drag with the button held, release: a selection that
	//    lands on the clipboard.
	a.clipBuf = ""
	press(ex+8, ey+1)
	press(ex+13, ey+1) // motion WITH Button1 down — a 1002 drag report
	if a.dragMode != dragEditor {
		t.Fatalf("drag mode = %q, want editor", a.dragMode)
	}
	if !tab.HasSelection() {
		t.Fatal("dragging with the button held must extend the selection")
	}
	release(ex+13, ey+1)
	if a.clipBuf == "" {
		t.Fatal("releasing a drag-selection must copy it")
	}

	// 3. The wheel scrolls the editor.
	tab.ScrollY = 0
	wheel(ex+5, ey+3, tcell.WheelDown)
	if tab.ScrollY == 0 {
		t.Fatal("WheelDown over the editor must scroll it")
	}

	// 4. The editor scrollbar: press jumps, held drag tracks, release ends.
	barX := ex + ew - 1
	press(barX, ey+eh-1)
	if a.dragMode != dragScrollbar {
		t.Fatalf("scrollbar press: dragMode = %q", a.dragMode)
	}
	if tab.ScrollY == 0 {
		t.Fatal("pressing the bottom of the bar must scroll")
	}
	press(barX, ey)
	if tab.ScrollY != 0 {
		t.Fatalf("dragging the thumb to the top must return to 0, got %d", tab.ScrollY)
	}
	release(barX, ey)
	if a.dragMode != dragNone {
		t.Fatalf("release must clear dragMode, got %q", a.dragMode)
	}

	// 5. The tree scrollbar, the sidebar's own drag target.
	_, sy, sw, sh := a.sidebarRect()
	treeBarX := sw - 1
	press(treeBarX, sy+sh-1)
	if a.dragMode != dragTreeScrollbar {
		t.Fatalf("tree bar press: dragMode = %q", a.dragMode)
	}
	if a.tree.ScrollY == 0 {
		t.Fatal("pressing the bottom of the tree bar must scroll it")
	}
	press(treeBarX, sy+2)
	if a.tree.ScrollY != 0 {
		t.Fatalf("dragging the tree thumb up must return to 0, got %d", a.tree.ScrollY)
	}
	release(treeBarX, sy+2)

	// 6. The sidebar splitter resizes while the button is held.
	splitX := a.splitterX()
	before := a.sidebarWidth
	press(splitX, sy+3)
	if a.dragMode != dragSidebar {
		t.Fatalf("splitter press: dragMode = %q", a.dragMode)
	}
	press(splitX+6, sy+3)
	if a.sidebarWidth <= before {
		t.Fatalf("splitter drag should widen the sidebar: %d -> %d", before, a.sidebarWidth)
	}
	release(splitX+6, sy+3)
	if a.dragMode != dragNone {
		t.Fatalf("release must clear dragMode, got %q", a.dragMode)
	}

	// Not one of those gestures had any business changing the mode.
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("core gestures left the mode at %v, want baseline %v", a.mouseFlags, mouseBaseFlags)
	}
}

// TestBaselineMouse_ShiftWheelStillRidesTheWheelEvent guards the one
// gesture that reads a modifier off a mouse event. Terminals that fold
// Shift into the wheel event itself — the common case — keep working
// under 1002, because the wheel report is a button press with bit 6 set
// and 1003 has nothing to do with it. (The Zellij-style split, where the
// modifier arrives as a separate button-less motion event, is the known
// casualty; see modifierStickyWindow in mouse.go.)
func TestBaselineMouse_ShiftWheelStillRidesTheWheelEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 400)+"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.wrapOn = false
	tab := a.activeTabPtr()
	tab.Wrap = false
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	a.syncMouseMode()

	ex, ey, _, _ := a.editorRect()
	a.handleEvent(tcell.NewEventMouse(ex+5, ey+1, tcell.WheelDown, tcell.ModShift))

	if tab.ScrollX == 0 {
		t.Fatal("Shift+WheelDown must scroll horizontally")
	}
	if tab.ScrollY != 0 {
		t.Fatalf("Shift+WheelDown must not scroll vertically, ScrollY = %d", tab.ScrollY)
	}
	if a.mouseFlags != mouseBaseFlags {
		t.Fatalf("wheel handling changed the mode to %v", a.mouseFlags)
	}
}
