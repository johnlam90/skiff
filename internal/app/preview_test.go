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
