// =============================================================================
// File: internal/app/preview_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for preview tabs: single-click browsing reuses one italic tab
// slot; editing, a second click, or a permanent open pins it.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedTwo writes two small files and returns their paths.
func seedTwo(t *testing.T, dir string) (string, string) {
	t.Helper()
	p1 := filepath.Join(dir, "one.txt")
	p2 := filepath.Join(dir, "two.txt")
	for _, p := range []string{p1, p2} {
		if err := os.WriteFile(p, []byte("hello\nworld\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return p1, p2
}

// TestPreviewReplacedByNextPreview pins the core browsing behavior:
// previewing file B while file A is previewed reuses A's tab slot, so
// tree-browsing never piles up tabs.
func TestPreviewReplacedByNextPreview(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFilePreview(p1)
	if a.tabs.Len() != 1 || !a.tabs.At(0).IsPreview() {
		t.Fatalf("first preview: %d tabs, preview=%v", a.tabs.Len(), a.tabs.At(0).IsPreview())
	}
	a.openFilePreview(p2)
	if a.tabs.Len() != 1 {
		t.Fatalf("second preview should reuse the slot, got %d tabs", a.tabs.Len())
	}
	if a.tabs.At(0).Path != p2 {
		t.Fatalf("slot holds %s, want %s", a.tabs.At(0).Path, p2)
	}
}

// TestPreviewReplaceKeepsSlot: with a pinned tab in slot 0 and a preview
// in slot 1, previewing a third file must land in slot 1 — tab order is
// part of the user's spatial memory.
func TestPreviewReplaceKeepsSlot(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	p3 := filepath.Join(dir, "three.txt")
	if err := os.WriteFile(p3, []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)

	a.openFile(p1)        // pinned, slot 0
	a.openFilePreview(p2) // preview, slot 1
	a.openFilePreview(p3)
	if a.tabs.Len() != 2 {
		t.Fatalf("want 2 tabs, got %d", a.tabs.Len())
	}
	if a.tabs.At(1).Path != p3 {
		t.Fatalf("slot 1 holds %s, want %s", a.tabs.At(1).Path, p3)
	}
	if a.tabs.At(0).Path != p1 {
		t.Fatalf("slot 0 disturbed: %s", a.tabs.At(0).Path)
	}
	if a.tabs.ActiveIndex() != 1 {
		t.Fatalf("activeTab = %d, want 1", a.tabs.ActiveIndex())
	}
}

// TestSecondClickPins: clicking the already-active previewed file again
// is the "keep this one" gesture — the tab pins and stops being
// replaceable.
func TestSecondClickPins(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFilePreview(p1)
	a.openFilePreview(p1) // second click
	if a.tabs.At(0).IsPreview() {
		t.Fatal("second click should pin the preview")
	}
	a.openFilePreview(p2)
	if a.tabs.Len() != 2 {
		t.Fatalf("pinned tab must not be replaced, got %d tabs", a.tabs.Len())
	}
}

// TestEditPinsPreview: typing into a preview makes it real — a dirty
// buffer must never be silently replaced by the next tree click.
func TestEditPinsPreview(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFilePreview(p1)
	a.tabs.At(0).InsertRune('x') // user starts editing
	if a.tabs.At(0).IsPreview() {
		t.Fatal("a dirty tab must not report IsPreview")
	}
	a.openFilePreview(p2)
	if a.tabs.Len() != 2 {
		t.Fatalf("dirty preview replaced — %d tabs, want 2", a.tabs.Len())
	}
}

// TestOpenFilePinsExistingPreview: a permanent open (finder, menu, CLI)
// landing on a previewed file upgrades it in place.
func TestOpenFilePinsExistingPreview(t *testing.T) {
	dir := t.TempDir()
	p1, _ := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFilePreview(p1)
	a.openFile(p1)
	if a.tabs.At(0).IsPreview() {
		t.Fatal("permanent open should pin the existing preview")
	}
}

// TestOpenFile_BinaryFlashesInsteadOfFreezing pins the app-level
// behavior for the freeze report: clicking a zip in the tree must not
// open a tab at all — the user gets a flash naming the problem, and
// the editor keeps breathing.
func TestOpenFile_BinaryFlashesInsteadOfFreezing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.zip")
	data := append([]byte("PK\x03\x04"), make([]byte, 4096)...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	if a.tabs.Len() != 0 {
		t.Fatalf("binary file must not open a tab, got %d tabs", a.tabs.Len())
	}
	if !strings.Contains(a.statusMsg, "binary") {
		t.Fatalf("flash should name the problem, got %q", a.statusMsg)
	}
}

// TestOpenFile_OversizedFileFlashesSizeAndOpensNoTab is the app end of
// editor's open size cap. A click on a several-hundred-megabyte log used
// to read the whole thing into a buffer on the event thread; now the
// open path refuses it the same way it refuses a binary — no tab, and a
// status flash that names the file's size and the limit, so the refusal
// reads as deliberate rather than as the editor losing the file. The
// file is sparse, so the test costs an inode rather than 33 MiB.
func TestOpenFile_OversizedFileFlashesSizeAndOpensNoTab(t *testing.T) {
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge.log")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(33 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	a := newTestApp(t, dir)

	a.openFile(huge)

	if a.tabs.Len() != 0 {
		t.Fatalf("a refused open must not leave a tab behind, got %d", a.tabs.Len())
	}
	for _, want := range []string{"huge.log", "33.0 MiB", "32.0 MiB"} {
		if !strings.Contains(a.statusMsg, want) {
			t.Fatalf("flash %q should name %q", a.statusMsg, want)
		}
	}
}

// TestPreviewCoachMark_FiresOnceThenStaysQuiet pins the whole point of
// the coach mark: preview tabs replace each other silently, which reads
// as "my tabs keep vanishing" until someone names the behavior — so the
// first preview of the session explains it. The second must not, or
// browsing a tree turns into a status bar that shouts the same sentence
// on every click.
func TestPreviewCoachMark_FiresOnceThenStaysQuiet(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFilePreview(p1)
	if !strings.Contains(a.statusMsg, "Preview tab") {
		t.Fatalf("first preview should explain itself, flash was %q", a.statusMsg)
	}
	if !a.previewCoachShown {
		t.Error("the once-per-session flag was never set")
	}

	a.statusMsg = ""
	a.openFilePreview(p2)
	if a.statusMsg != "" {
		t.Errorf("second preview re-explained itself: %q", a.statusMsg)
	}
}

// TestPreviewCoachMark_SilentForPermanentOpens keeps the hint attached
// to the behavior it describes. A finder / menu / CLI open pins its tab
// and has nothing surprising to explain, so it must announce the file
// the way it always did and leave the coach mark armed for the first
// real preview.
func TestPreviewCoachMark_SilentForPermanentOpens(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := seedTwo(t, dir)
	a := newTestApp(t, dir)

	a.openFile(p1)
	if a.previewCoachShown {
		t.Fatal("a permanent open must not spend the session's one coach mark")
	}
	if strings.Contains(a.statusMsg, "Preview tab") {
		t.Errorf("permanent open flashed the preview hint: %q", a.statusMsg)
	}

	a.openFilePreview(p2)
	if !strings.Contains(a.statusMsg, "Preview tab") {
		t.Errorf("the first real preview should still explain itself, got %q", a.statusMsg)
	}
}
