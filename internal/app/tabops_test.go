// =============================================================================
// File: internal/app/tabops_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for tabops.go — opening, saving, and closing tabs plus the
// clipboard operations and the has* menu predicates. The close paths get
// the most attention: a dirty tab must never close without the modal, and
// a failed save must never be followed by a close.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/clipboard"
	"github.com/johnlam90/skiff/internal/editor"
)

// TestOpenFile_Basic opens a file, switches to it on re-open, and updates
// activeFolder to the file's parent.
func TestOpenFile_Basic(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	target := filepath.Join(sub, "file.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, dir)
	a.openFile(target)
	if a.tabs.Len() != 1 {
		t.Fatalf("expected 1 tab, got %d", a.tabs.Len())
	}
	if a.activeFolder != sub {
		t.Fatalf("activeFolder: got %q, want %q", a.activeFolder, sub)
	}

	// Re-opening should switch to existing tab, not create a new one.
	a.tabs.ActivateAt(-1)
	a.openFile(target)
	if a.tabs.Len() != 1 {
		t.Fatalf("re-open created duplicate tab")
	}
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("re-open didn't switch active: got %d", a.tabs.ActiveIndex())
	}
}

// TestOpenFile_ErrorFlash surfaces an error when the path can't be loaded
// (here, a directory rather than a file).
func TestOpenFile_ErrorFlash(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(sub)
	// The flash names the file and the verb — "Error: is a directory"
	// never said which click it was about.
	if !strings.HasPrefix(a.statusMsg, "Couldn't open subdir:") {
		t.Fatalf("expected a flash naming the file, got %q", a.statusMsg)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("expected no tabs, got %d", a.tabs.Len())
	}
}

// TestRequestCloseTab_DirtyOpensModal proves a dirty tab does not close
// on first request and instead opens the unsaved-changes modal so the
// user can pick Save / Discard / Cancel.
func TestRequestCloseTab_DirtyOpensModal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.tabs.At(0).Dirty = true

	a.requestCloseTab(a.tabs.At(0))
	if a.tabs.Len() != 1 {
		t.Fatalf("dirty tab should not close until the user picks an action")
	}
	if !dirtyIsOpen(a) {
		t.Fatal("dirty close modal should be open")
	}
}

// TestRequestCloseTab_CleanClosesImmediately closes a clean tab in one shot.
func TestRequestCloseTab_CleanClosesImmediately(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clean.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.requestCloseTab(a.tabs.At(0))
	if a.tabs.Len() != 0 {
		t.Fatalf("clean tab should close on first request")
	}
}

// TestCloseTab_ClampsActive ensures activeTab never points outside the slice.
func TestCloseTab_ClampsActive(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.openFile(filepath.Join(dir, "b.txt"))
	a.tabs.ActivateAt(1)
	a.closeTab(a.tabs.At(1))
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("activeTab should clamp to 0 after closing last; got %d", a.tabs.ActiveIndex())
	}
	a.closeTab(a.tabs.At(0))
	if a.tabs.ActiveIndex() != 0 {
		t.Fatalf("activeTab should stay >=0 with no tabs; got %d", a.tabs.ActiveIndex())
	}
}

// TestCloseTab_OutOfRange is a no-op.
func TestCloseTab_OutOfRange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.closeTab(nil)
	a.closeTab(a.tabs.At(99))
	a.requestCloseTab(a.tabs.At(99))
}

// TestHasTab_Predicates covers the "is X available?" checks used to dim menu
// rows.
func TestHasTab_Predicates(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if a.hasTab() || a.hasSavableTab() || a.hasSelection() || a.hasClipboard() || a.hasCommentableTab() {
		t.Fatal("fresh app should have no tab/selection/clipboard/comment action")
	}

	a.openFile(target)
	if !a.hasTab() || !a.hasSavableTab() {
		t.Fatal("expected hasTab && hasSavableTab after open")
	}
	if a.hasSelection() {
		t.Fatal("no selection on a fresh tab")
	}
	if a.hasCommentableTab() {
		t.Fatal(".txt should not expose the line-comment action")
	}

	// Make a synthetic selection.
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	if !a.hasSelection() {
		t.Fatal("expected selection after Anchor != Cursor")
	}

	a.clipBuf = "x"
	if !a.hasClipboard() {
		t.Fatal("expected hasClipboard once clipBuf set")
	}
}

// TestHasCommentableTab_Predicate checks that line-comment actions only enable
// on editable text tabs with known comment syntax.
func TestHasCommentableTab_Predicate(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	htmlFile := filepath.Join(dir, "index.html")
	if err := os.WriteFile(goFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed go: %v", err)
	}
	if err := os.WriteFile(htmlFile, []byte("<main></main>"), 0644); err != nil {
		t.Fatalf("seed html: %v", err)
	}
	a := newTestApp(t, dir)

	a.openFile(goFile)
	if !a.hasCommentableTab() {
		t.Fatal(".go tab should expose the line-comment action")
	}

	a.openFile(htmlFile)
	if a.hasCommentableTab() {
		t.Fatal(".html tab should not expose the line-comment action")
	}
}

// TestActiveTabPtr returns nil with no tabs and the right pointer otherwise.
func TestActiveTabPtr(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if a.activeTabPtr() != nil {
		t.Fatal("expected nil with no tabs")
	}
	a.openFile(target)
	if a.activeTabPtr() != a.tabs.At(0) {
		t.Fatal("activeTabPtr should match the active tab")
	}
	// An out-of-range activation clamps onto a real tab — the
	// "active index points past the list" state cannot exist anymore.
	a.tabs.ActivateAt(99)
	if a.activeTabPtr() != a.tabs.At(0) {
		t.Fatal("out-of-range activation should clamp to a real tab")
	}
}

// TestSaveActiveTab writes the buffer to disk and clears Dirty.
func TestSaveActiveTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "save.txt")
	if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.activeTabPtr().InsertString("X")
	a.saveActiveTab()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "X") {
		t.Fatalf("save did not persist: %q", got)
	}
}

// TestSaveActiveTab_NoTab is a no-op.
func TestSaveActiveTab_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.saveActiveTab()
}

// TestCopyCutPaste pins the buffer effects of the clipboard trio, not
// just the clipBuf bookkeeping: copy leaves the text alone, cut removes
// exactly the selection and collapses the caret, and paste re-inserts
// at the caret rather than at the start of the line. An empty internal
// clipboard refuses with a flash instead of silently inserting nothing.
func TestCopyCutPaste(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	// No selection — copy/cut should be no-ops.
	a.copySelection()
	a.cutSelection()
	if a.clipBuf != "" {
		t.Fatalf("clipBuf should still be empty: %q", a.clipBuf)
	}

	// Make selection of "hello".
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 5}
	a.copySelection()
	if a.clipBuf != "hello" {
		t.Fatalf("copy: clipBuf %q", a.clipBuf)
	}
	if got := string(tab.Buffer.LineRunes(0)); got != "hello" {
		t.Fatalf("copy must not touch the buffer: line 0 = %q, want %q", got, "hello")
	}

	// Cut takes exactly the selection out of the buffer and leaves the
	// caret where the selection started.
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 2}
	a.cutSelection()
	if a.clipBuf != "he" {
		t.Fatalf("cut: clipBuf %q, want %q", a.clipBuf, "he")
	}
	if got := string(tab.Buffer.LineRunes(0)); got != "llo" {
		t.Fatalf("cut: line 0 = %q, want %q", got, "llo")
	}
	if tab.Cursor != (editor.Position{Line: 0, Col: 0}) {
		t.Fatalf("cut: cursor %+v, want line 0 col 0", tab.Cursor)
	}

	// Paste empty path: when clipBuf empty, flash about external paste
	// and leave the buffer alone.
	a.clipBuf = ""
	a.pasteClipboard()
	if !strings.Contains(a.statusMsg, "Nothing copied yet") || strings.Contains(a.statusMsg, "Cmd") {
		t.Fatalf("expected a platform-neutral empty-clip flash, got %q", a.statusMsg)
	}
	if got := string(tab.Buffer.LineRunes(0)); got != "llo" {
		t.Fatalf("refused paste edited the buffer: line 0 = %q, want %q", got, "llo")
	}

	// Paste with content: the text lands at the caret and the caret
	// advances past it.
	a.clipBuf = "he"
	tab.Cursor = editor.Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	a.pasteClipboard()
	if got := string(tab.Buffer.LineRunes(0)); got != "lhelo" {
		t.Fatalf("paste: line 0 = %q, want %q", got, "lhelo")
	}
	if tab.Cursor != (editor.Position{Line: 0, Col: 3}) {
		t.Fatalf("paste: cursor %+v, want line 0 col 3", tab.Cursor)
	}
}

// TestPasteClipboard_NoTab is safe with no tab open.
func TestPasteClipboard_NoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.clipBuf = "X"
	a.pasteClipboard() // no tab — nothing to paste into.
}

// TestOpenFile_StampsWrapPreference verifies a freshly opened tab copies
// the app-level wrap preference — the path future tabs take after the
// user has toggled wrap off.
func TestOpenFile_StampsWrapPreference(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.wrapOn = false
	a.openFile(target)
	if a.activeTabPtr().Wrap {
		t.Fatal("tab should inherit wrap-off from the app preference")
	}
}

// TestCopySelection_OversizedFlashesDistinctly wires the clipboard cap
// to the user. OSC 52 above the cap is silently dropped by tmux, so the
// old code flashed "Copied" over a copy that never happened. The
// contract now has three parts: the internal clipboard still gets the
// text (paste inside skiff keeps working), the flash names the real
// problem, and it is not the generic "clipboard unavailable" — that one
// is nothing the user can act on, this one is (select less).
func TestCopySelection_OversizedFlashesDistinctly(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", clipboard.MaxPayloadBytes+16)
	target := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(target, []byte(big), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: len(big)}
	a.copySelection()

	if len(a.clipBuf) != len(big) {
		t.Fatalf("internal clipboard must still hold the selection, got %d bytes", len(a.clipBuf))
	}
	if !strings.Contains(a.statusMsg, "too large") {
		t.Fatalf("flash must name the size problem, got %q", a.statusMsg)
	}
	if strings.Contains(a.statusMsg, "unavailable") {
		t.Fatalf("oversized copy must not reuse the generic failure copy, got %q", a.statusMsg)
	}
}

// TestActivateNextPrevTab pins keyboard tab switching at the app layer:
// the switch wraps, the tree's active file follows, and a single tab
// is a harmless no-op.
func TestActivateNextPrevTab(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	pa := openTestFile(t, a, dir, "a.txt", "a")
	pb := openTestFile(t, a, dir, "b.txt", "b")
	a.activateNextTab()
	if got := a.activeTabPtr().Path; got != pa {
		t.Fatalf("Next from the last tab should wrap to %s, got %s", pa, got)
	}
	if a.tree.ActiveFile != pa {
		t.Fatalf("the tree should follow the switch, got %q", a.tree.ActiveFile)
	}
	a.activatePrevTab()
	if got := a.activeTabPtr().Path; got != pb {
		t.Fatalf("Prev should wrap back to %s, got %s", pb, got)
	}

	empty := newTestApp(t, t.TempDir())
	empty.activateNextTab()
	empty.activatePrevTab()
}

// TestCloseOtherTabs pins the close-others contract: clean siblings
// close and land on the reopen stack, the active tab survives, and a
// dirty sibling makes the whole action refuse with a flash instead of
// closing half the strip.
func TestCloseOtherTabs(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	openTestFile(t, a, dir, "a.txt", "a")
	openTestFile(t, a, dir, "b.txt", "b")
	keep := openTestFile(t, a, dir, "c.txt", "c")

	dirtyTab := a.tabs.Lookup(filepath.Join(dir, "a.txt"))
	dirtyTab.InsertRune('x')
	if n := a.closeOtherTabs(); n != 0 || a.tabs.Len() != 3 {
		t.Fatalf("a dirty sibling must refuse the close: closed %d, %d tabs left", n, a.tabs.Len())
	}
	if !strings.Contains(a.statusMsg, "unsaved") {
		t.Fatalf("the refusal should explain itself, got %q", a.statusMsg)
	}
	dirtyTab.Undo()
	dirtyTab.Dirty = false

	if n := a.closeOtherTabs(); n != 2 {
		t.Fatalf("closed %d tabs, want 2", n)
	}
	if a.tabs.Len() != 1 || a.activeTabPtr().Path != keep {
		t.Fatalf("the active tab should be the survivor, got %d tabs, active %s", a.tabs.Len(), a.activeTabPtr().Path)
	}
	if len(a.closedTabs) != 2 {
		t.Fatalf("closed siblings should be reopenable, stack has %d", len(a.closedTabs))
	}
	if a.hasOtherTabs() {
		t.Fatal("hasOtherTabs must be false on a single tab")
	}
}

// TestSaveTab_UntitledNamesTheNextStep pins the refusal on an untitled
// buffer: "not supported yet" promised a feature; the flash now says
// what to do instead (≡ New file… creates the named file).
func TestSaveTab_UntitledNamesTheNextStep(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scratch, err := editor.NewTab("")
	if err != nil {
		t.Fatalf("untitled tab: %v", err)
	}
	if a.saveTab(scratch) {
		t.Fatal("an untitled tab has nowhere to save to")
	}
	if !strings.Contains(a.statusMsg, "New file…") || strings.Contains(a.statusMsg, "not supported") {
		t.Fatalf("flash should point at ≡ New file…, got %q", a.statusMsg)
	}
}

// readDisk returns path's contents as a string, failing the test when
// the file cannot be read — the save-guard tests all end on "what is on
// disk now".
func readDisk(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestSaveTab_ExternalChangePromptsInsteadOfOverwriting is the data-loss
// fix: a file rewritten since the last ten-second tick must not be
// silently overwritten by Save. The first Save opens the conflict
// prompt and leaves the disk alone; after Keep mine the second Save is
// the user's informed choice and goes through.
func TestSaveTab_ExternalChangePromptsInsteadOfOverwriting(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")

	if a.saveTab(a.activeTabPtr()) {
		t.Fatal("a save over an unseen external change must not report success")
	}
	if got := readDisk(t, path); got != "theirs\n" {
		t.Fatalf("the external change was overwritten: disk = %q", got)
	}
	d := dirtyOverlay(t, a)
	if d.Title != "Changed on disk" {
		t.Fatalf("expected the disk-conflict prompt, got %q", d.Title)
	}

	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) // Keep mine
	if !a.saveTab(a.activeTabPtr()) {
		t.Fatalf("Keep mine then Save must save; flash = %q", a.statusMsg)
	}
	if got := readDisk(t, path); got != "mine\n" {
		t.Fatalf("after Keep mine the buffer should win: disk = %q", got)
	}
	if a.overlays.IsOpen() {
		t.Fatalf("the acknowledged save must not prompt again; top = %T", a.overlays.Top())
	}
}

// TestSaveTab_OlderExternalMtimeStillPrompts pins "differs", not
// "newer": a file restored from a backup carries an older timestamp and
// is just as much somebody else's bytes.
func TestSaveTab_OlderExternalMtimeStillPrompts(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "restored\n")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if a.saveTab(a.activeTabPtr()) {
		t.Fatal("a save over an older-dated replacement must not go through")
	}
	if got := readDisk(t, path); got != "restored\n" {
		t.Fatalf("the restored file was overwritten: disk = %q", got)
	}
}

// TestSaveTab_CleanTabReloadsInsteadOfOverwriting covers the clean
// buffer: nothing of the user's is at stake, so Save takes the disk
// copy (what the tick would do a moment later) rather than writing a
// stale buffer over it or asking a question with one sensible answer.
func TestSaveTab_CleanTabReloadsInsteadOfOverwriting(t *testing.T) {
	a, path := conflictSetup(t, "original\n", "theirs\n")
	tab := a.activeTabPtr()
	tab.Dirty = false

	if a.saveTab(tab) {
		t.Fatal("a clean tab over a changed file must not write")
	}
	if got := readDisk(t, path); got != "theirs\n" {
		t.Fatalf("disk = %q, want the external change intact", got)
	}
	if tab.Buffer.Lines[0] != "theirs" {
		t.Fatalf("the clean buffer should have taken the disk copy, got %q", tab.Buffer.Lines[0])
	}
	if a.overlays.IsOpen() {
		t.Fatalf("a clean reload needs no prompt; top = %T", a.overlays.Top())
	}
	if !strings.Contains(a.statusMsg, "reloaded instead of saving") {
		t.Fatalf("flash = %q, want it to say the save became a reload", a.statusMsg)
	}
}

// TestSaveTab_FormatterInFlightIsNotASecondWriter keeps format-on-save
// from tripping the guard: while a formatter run for this path is out,
// the file's mtime moving is the editor's own doing.
func TestSaveTab_FormatterInFlightIsNotASecondWriter(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "formatted\n")
	a.formatInFlight = map[string]int{path: 1}

	if !a.saveTab(a.activeTabPtr()) {
		t.Fatalf("a save during the formatter's own rewrite must go through; flash = %q", a.statusMsg)
	}
	if got := readDisk(t, path); got != "mine\n" {
		t.Fatalf("disk = %q, want the saved buffer", got)
	}
}

// TestSaveTab_ClearsDiskConflictMarker pins the other half of resolving
// by saving: the memo goes with the save, so the next edit does not
// relight "disk conflict" against a file this tab just wrote.
func TestSaveTab_ClearsDiskConflictMarker(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.reconcileOpenTabsWithDisk()
	dirtyOverlay(t, a).HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) // Keep mine
	tab := a.activeTabPtr()
	if !a.tabDiskConflict(tab) {
		t.Fatal("precondition: a dismissed prompt leaves the marker up")
	}

	if !a.saveTab(tab) {
		t.Fatalf("save failed: %q", a.statusMsg)
	}
	if _, ok := a.diskConflicts[path]; ok {
		t.Fatal("saving resolves the conflict; the memo must be dropped")
	}
	tab.InsertString("more")
	if a.tabDiskConflict(tab) {
		t.Fatal("an edit after the resolving save must not read as a conflict")
	}
}

// TestCloseTab_DropsDiskConflictMemo: the memo is keyed by path, so one
// left behind by a closed tab would greet the file's next tab with a
// conflict marker on its first edit.
func TestCloseTab_DropsDiskConflictMemo(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.reconcileOpenTabsWithDisk()
	a.closeAllModals()

	a.closeTab(a.activeTabPtr())
	if _, ok := a.diskConflicts[path]; ok {
		t.Fatal("closing the tab must forget its conflict")
	}
	a.openFile(path)
	tab := a.activeTabPtr()
	tab.InsertString("x")
	if a.tabDiskConflict(tab) {
		t.Fatal("a freshly opened tab must not inherit the closed tab's conflict")
	}
}

// TestMenuQuit_SaveStopsAtDiskConflict is the quit path through the
// same guard: Save-all must not write over an external change on the
// way out, and must not quit having skipped the save.
func TestMenuQuit_SaveStopsAtDiskConflict(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.menuQuit()
	dirtyChoose(t, a, 2) // Save

	if a.quit {
		t.Fatal("quit must wait for the conflict to be answered")
	}
	if got := readDisk(t, path); got != "theirs\n" {
		t.Fatalf("quit-save overwrote the external change: disk = %q", got)
	}
	if d := dirtyOverlay(t, a); d.Title != "Changed on disk" {
		t.Fatalf("expected the disk-conflict prompt, got %q", d.Title)
	}
}

// TestHasMutableTab_FalseOverMarkdownPreview pins the read-only gate
// the edit rows hang off: an editable tab stops being mutable the
// moment its preview is up, and is mutable again when it comes down.
func TestHasMutableTab_FalseOverMarkdownPreview(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasMutableTab() || a.hasRevertableTab() {
		t.Fatal("no tab: nothing is mutable")
	}
	seedMarkdownTab(t, a, "notes.md", "# Title\n")
	if !a.hasMutableTab() || !a.hasRevertableTab() {
		t.Fatal("a markdown tab in edit mode is mutable")
	}
	a.menuTogglePreviewMarkdown()
	if a.hasMutableTab() || a.hasRevertableTab() {
		t.Fatal("a previewed tab is read-only")
	}
	if !a.hasEditableTab() {
		t.Fatal("the tab is still a text tab — copy and find keep their rows")
	}
	a.menuTogglePreviewMarkdown()
	if !a.hasMutableTab() {
		t.Fatal("leaving the preview makes the tab mutable again")
	}
}
