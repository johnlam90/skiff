// =============================================================================
// File: internal/app/actions_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for actions.go — one exercise per action-menu handler, driven the
// way a click would drive it (menu open, action runs, menu closes). The
// custom-action cases cover the async shell-out end to end: env expansion
// in, a customActionResult landing out, info modal on failure.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/editor"
)

// TestSidebarToggleLabel flips between Show/Hide based on sidebarShown.
func TestSidebarToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.sidebarToggleLabel() != "Hide file explorer" {
		t.Fatalf("got %q", a.sidebarToggleLabel())
	}
	a.sidebarShown = false
	if a.sidebarToggleLabel() != "Show file explorer" {
		t.Fatalf("got %q", a.sidebarToggleLabel())
	}
}

// TestMenuToggleSidebar flips the sidebarShown flag.
func TestMenuToggleSidebar(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if !a.sidebarShown {
		t.Fatal("sidebar should start visible")
	}
	a.menuToggleSidebar()
	if a.sidebarShown {
		t.Fatal("expected hidden after first toggle")
	}
	a.menuToggleSidebar()
	if !a.sidebarShown {
		t.Fatal("expected shown after second toggle")
	}
}

// TestMenuToggleLineComment runs the menu action against the active tab so the
// app layer and editor-layer primitive stay wired together.
func TestMenuToggleLineComment(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("one\ntwo"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuToggleLineComment()

	if got := a.activeTabPtr().Buffer.String(); got != "// one\ntwo" {
		t.Fatalf("buffer = %q, want current line commented", got)
	}
	if a.statusMsg != "Toggled line comment" {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
}

// TestMenuToggleLineComment_Unsupported flashes a clear no-op instead of
// guessing at block-comment-only formats.
func TestMenuToggleLineComment_Unsupported(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.html")
	if err := os.WriteFile(target, []byte("<main></main>"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuToggleLineComment()

	if got := a.activeTabPtr().Buffer.String(); got != "<main></main>" {
		t.Fatalf("unsupported buffer changed to %q", got)
	}
	if a.statusMsg != "No line comment syntax for this file" {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
}

// TestMenuSaveAndClose saves then closes the active tab.
func TestMenuSaveAndClose(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sc.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().InsertString("Y")
	a.menuSaveAndClose()
	if a.tabs.Len() != 0 {
		t.Fatalf("expected tab closed; got %d tabs", a.tabs.Len())
	}
}

// TestMenuSaveAndClose_NoTab is a no-op when nothing is open.
func TestMenuSaveAndClose_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuSaveAndClose()
}

// TestMenuClickPaths covers menuSave/menuCopy/menuCut/menuPaste/menuClose
// menuQuit and menuRefreshTree as one-liners.
func TestMenuClickPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	// Selection so copy/cut have something to operate on.
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 2}

	a.menuOpen = true
	a.menuSave()
	a.menuOpen = true
	a.menuCopy()
	a.menuOpen = true
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	a.menuCut()
	a.menuOpen = true
	a.menuPaste()
	a.menuOpen = true
	a.menuRefreshTree()

	// Clean the tab before quitting; the dirty-quit path is exercised
	// separately in dirty_modal_test.go.
	tab.Dirty = false
	a.menuOpen = true
	a.menuQuit()
	if !a.quit {
		t.Fatal("menuQuit should set quit flag")
	}
}

// TestUndoRedoRevert_MenuPaths exercises the new history actions end
// to end through the menu wrappers. The flash on no-op paths is also
// covered so the user always gets feedback when they hit a dead-end.
func TestUndoRedoRevert_MenuPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	// Nothing to undo / redo / revert on a freshly opened file.
	if a.hasUndo() || a.hasRedo() || a.hasRevert() {
		t.Fatal("freshly opened tab should have no history")
	}
	a.menuOpen = true
	a.menuUndo()
	a.menuOpen = true
	a.menuRedo()
	a.menuOpen = true
	a.menuRevert()

	// One edit → undo + revert become available.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 4}, false)
	tab.InsertString("X")
	if !a.hasUndo() || !a.hasRevert() {
		t.Fatal("expected undo + revert after edit")
	}
	if a.hasRedo() {
		t.Fatal("redo should still be empty")
	}

	a.menuOpen = true
	a.menuUndo()
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after menuUndo = %q, want seed", got)
	}
	if !a.hasRedo() {
		t.Fatal("redo should be populated after an undo")
	}

	a.menuOpen = true
	a.menuRedo()
	if got := tab.Buffer.String(); got != "seedX" {
		t.Fatalf("after menuRedo = %q, want seedX", got)
	}

	// Revert back to original; then Undo must recover the post-edit state.
	a.menuOpen = true
	a.menuRevert()
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after menuRevert = %q, want seed", got)
	}
	a.menuOpen = true
	a.menuUndo()
	if got := tab.Buffer.String(); got != "seedX" {
		t.Fatalf("after undo-of-revert = %q, want seedX", got)
	}
}

// TestUndoRedoRevert_NoTabSafelyNoOps guards against crashes when the
// menu rows somehow fire with no active tab — they should silently
// return rather than dereferencing nil.
func TestUndoRedoRevert_NoTabSafelyNoOps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuUndo()
	a.menuOpen = true
	a.menuRedo()
	a.menuOpen = true
	a.menuRevert()
	if a.hasUndo() || a.hasRedo() || a.hasRevert() {
		t.Fatal("no-tab predicates should all be false")
	}
}

// TestMenuClose_NoTab safely no-ops.
func TestMenuClose_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuClose()
}

// TestRunCustomAction_NoFileStillRuns confirms a prompt-less action
// runs even with no tab open. Earlier the runner short-circuited
// here with a "no file open" flash, but that gate guessed wrong
// for $FILE-free commands like "brew upgrade …" — they got blocked
// even though they had no file dependency at all. The new contract:
// always run; if a $FILE-dependent command then fails because FILE
// is empty, the failure surfaces in the info modal with the actual
// stderr, which is more informative than a generic flash.
func TestRunCustomAction_NoFileStillRuns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	a := newTestApp(t, dir)
	a.customActions = []customactions.Action{{
		Label:   "Touch marker",
		Command: "touch " + marker,
	}}
	a.runCustomAction(0)

	pumpUntil(t, a, "custom action", idle(&a.customAction))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("command did not run: %v", err)
	}
	if strings.Contains(a.statusMsg, "no file open") {
		t.Errorf("status flash should not mention no file open: %q", a.statusMsg)
	}
}

// TestRunCustomAction_OutOfRange is a no-op when idx is bogus. Caller
// should never produce one but the guard keeps a stale layout from
// crashing the editor.
func TestRunCustomAction_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.runCustomAction(0)  // empty list
	a.runCustomAction(99) // out of range
}

// TestRunCustomAction_ExecutesAndPostsEvent runs a real `sh -c`
// command against a small file and confirms that (a) the command
// observed FILE / FILENAME via env, and (b) its landing reaches the
// loop as a success report. The chosen command writes a marker file
// that lets the test verify env reached the subprocess.
func TestRunCustomAction_ExecutesAndPostsEvent(t *testing.T) {
	// Redirect the action log into the test's temp dir so we don't
	// scribble into the developer's real ~/.local/state/skiff/.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	target := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	marker := filepath.Join(dir, "marker.txt")
	a := newTestApp(t, dir)
	a.openFile(target)
	a.customActions = []customactions.Action{{
		Label:   "Mark",
		Command: `printf "%s|%s" "$FILE" "$FILENAME" > ` + marker,
	}}

	a.runCustomAction(0)

	// The action runs on the customAction job and lands through the
	// real handler: a fast, silent success is exactly one flash.
	pumpUntil(t, a, "custom action", idle(&a.customAction))
	if infoIsOpen(a) {
		t.Fatalf("action errored: %v", infoPrefab(t, a).Lines)
	}
	if a.statusMsg != "Mark — done" {
		t.Errorf("landing flash = %q, want the labelled confirmation", a.statusMsg)
	}

	// Verify the subprocess saw the env variables we exported.
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker read: %v", err)
	}
	want := target + "|" + "src.txt"
	if string(got) != want {
		t.Fatalf("marker content = %q, want %q", got, want)
	}
}

// TestRunCustomAction_PromptedSkipsNoFileGuard ensures actions that
// declare prompts can run even when no tab is open. Copy-from-remote
// is the motivating case — without this, the very first thing the
// user wants to do in a fresh session would silently flash "no file
// open" and refuse to show the form.
func TestRunCustomAction_PromptedSkipsNoFileGuard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = []customactions.Action{{
		Label:   "Copy from remote",
		Command: "true",
		Prompts: []customactions.Prompt{
			{Key: "HOST", Type: customactions.PromptSelect, Options: []string{"a", "b"}},
		},
	}}
	a.runCustomAction(0)

	if !formIsOpen(a) {
		t.Fatal("prompted action with no file open should still show the form modal")
	}
	if strings.Contains(a.statusMsg, "no file open") {
		t.Errorf("prompted action should not flash no-file-open: %q", a.statusMsg)
	}
}

// TestRunCustomAction_PromptedExportsValuesAndExpands walks the full
// SCP-from-remote path: the form opens, we fill it in, submit, and
// assert the spawned shell saw both the form-collected env vars
// (HOST, REMOTE_SRC) and the editor-state vars (PROJECT_ROOT). This
// is the contract that makes the feature actually useful — if any of
// these don't reach the shell, the user's command fails silently.
func TestRunCustomAction_PromptedExportsValuesAndExpands(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	a := newTestApp(t, dir)
	a.customActions = []customactions.Action{{
		Label:   "Copy from remote",
		Command: `printf "%s|%s|%s" "$HOST" "$REMOTE_SRC" "$PROJECT_ROOT" > ` + marker,
		Prompts: []customactions.Prompt{
			{Key: "HOST", Type: customactions.PromptSelect, Options: []string{"cascade", "rager"}},
			{Key: "REMOTE_SRC", Type: customactions.PromptText},
		},
	}}

	a.runCustomAction(0)
	if !formIsOpen(a) {
		t.Fatal("form did not open")
	}

	// Fill in REMOTE_SRC by typing into the focused field after Tab'ing
	// past the HOST select — all through real routing.
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	for _, r := range "/etc/hosts" {
		a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if formIsOpen(a) {
		t.Fatal("Enter on last field should submit")
	}

	pumpUntil(t, a, "custom action", idle(&a.customAction))

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker read: %v", err)
	}
	parts := strings.Split(string(got), "|")
	if len(parts) != 3 {
		t.Fatalf("marker = %q, want HOST|REMOTE_SRC|PROJECT_ROOT", got)
	}
	if parts[0] != "cascade" {
		t.Errorf("HOST = %q, want %q", parts[0], "cascade")
	}
	if parts[1] != "/etc/hosts" {
		t.Errorf("REMOTE_SRC = %q, want %q", parts[1], "/etc/hosts")
	}
	if !strings.HasSuffix(parts[2], filepath.Base(dir)) {
		t.Errorf("PROJECT_ROOT = %q, want suffix matching tempdir", parts[2])
	}
}

// TestHandleCustomActionDone_FailureOpensInfoModal pins the error
// reporting upgrade. The pre-fix behaviour was a one-line status
// flash that truncated scp's stderr exactly when the user most
// needed to read it. Now failures route into the info modal so the
// stderr lines stay visible until dismissed.
func TestHandleCustomActionDone_FailureOpensInfoModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCustomActionDone(customActionResult{
		label:  "Copy from remote",
		output: []byte("scp: /etc/missing: No such file or directory\n"),
	}, fmt.Errorf("exit status 1"))
	n := infoPrefab(t, a)
	joined := strings.Join(n.Lines, "\n")
	if !strings.Contains(joined, "scp:") || !strings.Contains(joined, "missing") {
		t.Errorf("info body missing stderr preview: %q", joined)
	}
	if !strings.Contains(n.Title, "Copy from remote") {
		t.Errorf("title = %q, want it to mention the action label", n.Title)
	}
}

// TestHandleCustomActionDone_SuccessRefreshesTree confirms a
// successful action triggers an immediate tree refresh so a
// freshly-pulled file appears without waiting on the 10-second
// auto-refresh tick. Pinning this avoids a regression where a user
// runs Copy-from-remote, sees "done", and then has to pause before
// the new file becomes clickable in the sidebar. "Immediate" now means
// "this tick, not the next one": the ReadDir walk happens on a
// goroutine and lands through the treeScan job, so the test pumps
// that event the way the real loop does.
func TestHandleCustomActionDone_SuccessRefreshesTree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	a := newTestApp(t, dir)
	// Drop a file directly on disk that the tree hasn't seen yet —
	// without an explicit refresh it would only show up on the next
	// 10-second tick.
	newFile := filepath.Join(dir, "fresh.txt")
	if err := os.WriteFile(newFile, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	beforeChildren := len(a.tree.Root.Children)
	a.handleCustomActionDone(customActionResult{label: "X"}, nil)
	pumpUntil(t, a, "tree scan", idle(&a.treeScan))
	if got := len(a.tree.Root.Children); got <= beforeChildren {
		t.Errorf("tree was not refreshed: %d → %d children", beforeChildren, got)
	}
}

// TestHandleCustomActionDone_SuccessWithOutputOpensInfo pins the
// asymmetry fix: failure got the full modal while success got a
// two-second flash, so anyone watching a slow scp finish had no
// confirmation and never saw its stdout. An action that printed
// something now reports through the same surface as a failure.
func TestHandleCustomActionDone_SuccessWithOutputOpensInfo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCustomActionDone(customActionResult{
		label:  "Copy from remote",
		output: []byte("sent 4 files, 1.2 MB\n"),
	}, nil)
	n := infoPrefab(t, a)
	if !strings.Contains(strings.Join(n.Lines, "\n"), "sent 4 files") {
		t.Errorf("info body missing stdout: %q", n.Lines)
	}
	if strings.Contains(n.Title, "failed") {
		t.Errorf("success title reads as a failure: %q", n.Title)
	}
}

// TestHandleCustomActionDone_SlowSilentSuccessOpensInfo covers the other
// trigger: a long run with no output still deserves a confirmation,
// because a flash that expires while the user is looking at another pane
// is the same as no confirmation at all.
func TestHandleCustomActionDone_SlowSilentSuccessOpensInfo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCustomActionDone(customActionResult{
		label:    "Deploy",
		duration: 9 * time.Second,
	}, nil)
	n := infoPrefab(t, a)
	if !strings.Contains(strings.Join(n.Lines, "\n"), "9s") {
		t.Errorf("slow-run report should state the duration: %q", n.Lines)
	}
}

// TestHandleCustomActionDone_FastSilentSuccessOnlyFlashes is the
// restraint half. A quick, silent action must not make the user dismiss
// a modal — the flash is proportionate and the overlay would be noise.
func TestHandleCustomActionDone_FastSilentSuccessOnlyFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCustomActionDone(customActionResult{
		label:    "Format",
		duration: 50 * time.Millisecond,
	}, nil)
	if a.overlays.IsOpen() {
		t.Fatalf("fast silent action should not open an overlay; top = %T", a.overlays.Top())
	}
	if !strings.Contains(a.statusMsg, "Format — done") {
		t.Fatalf("status = %q, want the brief confirmation", a.statusMsg)
	}
}

// TestSplitActionOutput_Thresholds pins the routing rule itself, without
// a screen: nil means "a flash says it all".
func TestSplitActionOutput_Thresholds(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if got := splitActionOutput(nil, 10*time.Millisecond); got != nil {
		t.Errorf("fast + silent should stay a flash, got %v", got)
	}
	if got := splitActionOutput([]byte("x\n"), time.Millisecond); len(got) == 0 {
		t.Error("output alone should earn the modal")
	}
	if got := splitActionOutput(nil, customActionQuietRun); len(got) == 0 {
		t.Error("a run at the slow threshold should earn the modal")
	}

	// Over-long lines are ellipsised by the same helper the failure
	// path uses, so the two reports can't drift apart.
	long := strings.Repeat("z", 200)
	body := splitActionOutput([]byte(long), 0)
	for _, ln := range body {
		if runeLen(ln) > 80 {
			t.Errorf("line over 80 cells: %q", ln)
		}
	}
	if !strings.HasSuffix(body[0], "…") {
		t.Errorf("truncated line = %q, want a trailing ellipsis", body[0])
	}
}

// TestSplitErrorOutput_TruncatesAndAppendsLogPath nails the body
// the info modal renders on failure. Without truncation a runaway
// scp -v dump would push the dialog off-screen; without the actions.log
// pointer the user can't easily get the full version even though
// we're already writing it.
func TestSplitErrorOutput_TruncatesAndAppendsLogPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgtest")

	long := strings.Repeat("really long line that exceeds eighty cells ", 4)
	out := []byte(long + "\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\n")
	body := splitErrorOutput(fmt.Errorf("exit 1"), out)

	if body[0] != "exit 1" {
		t.Errorf("body[0] = %q, want exit-error summary", body[0])
	}
	last := body[len(body)-1]
	if !strings.Contains(last, "actions.log") {
		t.Errorf("last line = %q, want actions.log pointer", last)
	}
	for _, ln := range body {
		if runeLen(ln) > 80 {
			t.Errorf("line over 80 cells: %q (len=%d)", ln, runeLen(ln))
		}
	}
	if !strings.Contains(strings.Join(body, "\n"), "truncated") {
		t.Error("expected '… truncated' marker for >maxLines output")
	}
}

// TestMenuToggleSidebar_NoPanicInSingleFileMode is a regression guard for
// a crash: the Esc-t leader calls menuToggleSidebar directly, bypassing the
// menu row's hasTree gate. In single-file mode (tree == nil) flipping
// sidebarShown true would send draw() into a.tree.Render on a nil tree and
// panic. The toggle must stay a no-op so the sidebar can't be shown when
// there's no tree behind it.
func TestMenuToggleSidebar_NoPanicInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil // single-file mode
	a.sidebarShown = false

	a.menuToggleSidebar() // simulates the Esc-t leader

	if a.sidebarShown {
		t.Fatal("sidebar must stay hidden in single-file mode — no tree to render")
	}
	a.draw() // would panic on nil a.tree.Render if the toggle flipped it on
}

// TestMenuToggleWrap pins the toggle's full contract: it flips the app
// preference and every open tab together, swaps the dynamic menu label,
// and persists the choice to the user config file.
func TestMenuToggleWrap(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	if !a.activeTabPtr().Wrap {
		t.Fatal("tabs should open with wrap on by default")
	}
	if got := a.wrapToggleLabel(); got != "Unwrap long lines" {
		t.Fatalf("label = %q, want Unwrap long lines", got)
	}

	a.menuToggleWrap()
	if a.wrapOn || a.activeTabPtr().Wrap {
		t.Fatal("toggle should turn wrap off for the app and all open tabs")
	}
	if got := a.wrapToggleLabel(); got != "Wrap long lines" {
		t.Fatalf("label after toggle = %q, want Wrap long lines", got)
	}
	data, err := os.ReadFile(filepath.Join(cfgHome, "skiff", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"wrap": "off"`) {
		t.Fatalf("config missing wrap off:\n%s", data)
	}

	a.menuToggleWrap()
	if !a.wrapOn || !a.activeTabPtr().Wrap {
		t.Fatal("second toggle should turn wrap back on")
	}
}

// TestMenuToggleGitignore pins the ≡ View row end to end: the tree
// starts filtering (so the sidebar agrees with the finder), the row
// flips it and re-reads the tree so the change is on screen immediately,
// the label names the action it will perform, and the choice is
// persisted to config.json. The re-read is the part worth pinning —
// flipping the flag alone leaves the sidebar showing the old listing
// until the next background tick.
func TestMenuToggleGitignore(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	dir := t.TempDir()
	for name, body := range map[string]string{
		".gitignore": "build.out\n",
		"build.out":  "generated",
		"main.go":    "package main",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	a := newTestApp(t, dir)

	if !a.tree.HideIgnored {
		t.Fatal("the tree should start hiding ignored entries")
	}
	if findTreeChild(a, "build.out") != nil {
		t.Fatal("build.out is gitignored and must not be a row")
	}
	if got := a.gitignoreToggleLabel(); got != "Show ignored files" {
		t.Fatalf("label = %q, want Show ignored files", got)
	}

	a.menuToggleGitignore()
	if a.tree.HideIgnored {
		t.Fatal("the toggle should turn filtering off")
	}
	if findTreeChild(a, "build.out") == nil {
		t.Fatal("the toggle must re-read the tree, not just flip the flag")
	}
	if got := a.gitignoreToggleLabel(); got != "Hide ignored files" {
		t.Fatalf("label after toggle = %q, want Hide ignored files", got)
	}
	data, err := os.ReadFile(filepath.Join(cfgHome, "skiff", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"gitignore": "off"`) {
		t.Fatalf("config missing gitignore off:\n%s", data)
	}

	a.menuToggleGitignore()
	if !a.tree.HideIgnored || findTreeChild(a, "build.out") != nil {
		t.Fatal("second toggle should hide the ignored file again")
	}
	if findTreeChild(a, "main.go") == nil {
		t.Fatal("an unignored file must survive both directions")
	}
}

// TestMenuToggleGitignore_NoPanicInSingleFileMode mirrors the sidebar
// row's guard. The menu row is hidden without a tree, but the handler is
// a plain method and must not dereference a nil tree if it is ever
// reached another way.
func TestMenuToggleGitignore_NoPanicInSingleFileMode(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil

	a.menuToggleGitignore()

	if got := a.gitignoreToggleLabel(); got == "" {
		t.Fatal("the label must still resolve without a tree")
	}
}

// TestMenuSaveAndClose_GoesThroughSaveTab pins that Save & close reuses
// the one shared save path instead of calling tab.Save() itself. The
// observable difference is format-on-save: a project with an untrusted
// .skiff/format.json must raise the trust prompt on this action exactly
// as it does on a plain Save. Before the fix Save & close skipped
// formatting entirely, so the same buffer landed on disk formatted or
// unformatted depending on which menu row the user clicked.
func TestMenuSaveAndClose_GoesThroughSaveTab(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"txt":["echo","ran","$FILE"]}}`)
	target := filepath.Join(root, "sc.txt")
	if err := os.WriteFile(target, []byte("seed\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(target)
	a.activeTabPtr().InsertString("Y")

	a.menuSaveAndClose()

	if a.tabs.Len() != 0 {
		t.Fatalf("expected the tab closed; got %d tabs", a.tabs.Len())
	}
	if !confirmIsOpen(a) {
		t.Fatalf("Save & close must run format-on-save like a plain Save; top = %T", a.overlays.Top())
	}
}

// TestMenuSaveAndClose_FailedSaveKeepsTab is the other half of routing
// through saveTab: a save that can't land must flash and leave the tab
// open, never close it and drop the buffer.
func TestMenuSaveAndClose_FailedSaveKeepsTab(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sc.txt")
	if err := os.WriteFile(target, []byte("seed\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(target)
	a.activeTabPtr().InsertString("Y")
	// A directory in the file's place makes the write fail for everyone,
	// root included.
	if err := os.Remove(target); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("swap: %v", err)
	}

	a.menuSaveAndClose()

	if a.tabs.Len() != 1 {
		t.Fatalf("a failed save must not close the tab; got %d tabs", a.tabs.Len())
	}
	if !strings.Contains(a.statusMsg, "Save failed") {
		t.Fatalf("expected a Save failed flash, got %q", a.statusMsg)
	}
}

// newNavTestApp opens a small Go file and returns the app plus its tab,
// shared by the caret-navigation action tests below.
func newNavTestApp(t *testing.T, content string) (*App, *editor.Tab) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "nav.go")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.Cursor = editor.Position{}
	tab.Anchor = tab.Cursor
	return a, tab
}

// TestMenuMoveWord_WalksTheCaret drives the two word-motion rows the way a
// click would: the menu closes and the caret lands on a word boundary.
func TestMenuMoveWord_WalksTheCaret(t *testing.T) {
	a, tab := newNavTestApp(t, "alpha beta gamma\n")

	a.openMenu()
	a.menuMoveWordRight()
	if a.menuOpen {
		t.Fatal("menuMoveWordRight left the menu open")
	}
	if want := (editor.Position{Line: 0, Col: 5}); tab.Cursor != want {
		t.Fatalf("cursor = %v, want %v", tab.Cursor, want)
	}

	a.menuMoveWordLeft()
	if want := (editor.Position{}); tab.Cursor != want {
		t.Fatalf("cursor = %v, want %v", tab.Cursor, want)
	}
}

// TestMenuMoveWord_NoTabIsSafe covers the startup screen: the rows are
// reachable before any file is open and must not panic.
func TestMenuMoveWord_NoTabIsSafe(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuMoveWordLeft()
	a.menuMoveWordRight()
}

// TestMenuGoToMatchingBracket jumps to the partner and says so when there
// isn't one — a silent no-op on a menu row reads as a broken editor.
func TestMenuGoToMatchingBracket(t *testing.T) {
	a, tab := newNavTestApp(t, "func f() {\n\tg()\n}\n")
	tab.Cursor = editor.Position{Line: 0, Col: 9} // the '{'
	tab.Anchor = tab.Cursor

	a.openMenu()
	a.menuGoToMatchingBracket()
	if a.menuOpen {
		t.Fatal("menuGoToMatchingBracket left the menu open")
	}
	if want := (editor.Position{Line: 2, Col: 0}); tab.Cursor != want {
		t.Fatalf("cursor = %v, want %v", tab.Cursor, want)
	}

	tab.Cursor = editor.Position{Line: 1, Col: 1} // on 'g', no bracket
	tab.Anchor = tab.Cursor
	a.menuGoToMatchingBracket()
	if !strings.Contains(a.statusMsg, "No matching bracket") {
		t.Fatalf("expected a no-match flash, got %q", a.statusMsg)
	}
}

// TestHasMatchingBracket_GatesTheMenuRow checks the enable predicate the
// row carries: true only where the jump would actually go somewhere, and
// false with no tab at all.
func TestHasMatchingBracket_GatesTheMenuRow(t *testing.T) {
	a, tab := newNavTestApp(t, "x := f(1)\n")
	tab.Cursor = editor.Position{Line: 0, Col: 6} // the '('
	if !a.hasMatchingBracket() {
		t.Fatal("caret on a balanced '(' should enable the row")
	}
	tab.Cursor = editor.Position{Line: 0, Col: 0}
	if a.hasMatchingBracket() {
		t.Fatal("caret on a plain rune should disable the row")
	}

	empty := newTestApp(t, t.TempDir())
	if empty.hasMatchingBracket() {
		t.Fatal("no tab should disable the row")
	}
}
