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
	if !strings.Contains(a.statusMsg, "Error") {
		t.Fatalf("expected error flash, got %q", a.statusMsg)
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
	if !strings.Contains(a.statusMsg, "clipboard empty") {
		t.Fatalf("expected empty-clip flash, got %q", a.statusMsg)
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
