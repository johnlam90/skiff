// =============================================================================
// File: internal/app/themepick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the live-preview theme picker: previews follow the
// highlight, Enter persists, Esc reverts, the filter narrows and
// previews, and mouse hover/click mirror the keyboard.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// TestApplyTheme_SwapsPaletteAndStalesTabs pins the palette-swap
// contract shared by preview and persist: the struct swaps, open tabs
// re-tokenise, and persist=true lands in config.json.
func TestApplyTheme_SwapsPaletteAndStalesTabs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	path := seedFile(t, dir, "a.txt")
	a := newTestApp(t, dir)
	a.openFile(path)
	a.activeTabPtr().StyleStale = false

	a.applyTheme("dracula", true)
	want, _ := theme.ByID("dracula")
	if a.theme.BG != want.BG || a.themeID != "dracula" {
		t.Fatalf("palette not applied: %+v", a.themeID)
	}
	if !a.activeTabPtr().StyleStale {
		t.Fatal("open tabs must re-tokenise after a theme switch")
	}
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil || cfg.Theme != "dracula" {
		t.Fatalf("persisted config: %+v %v", cfg, err)
	}
}

// TestApplyTheme_UnknownFallsBack: a stale config id keeps the editor
// on the default palette instead of black-on-black.
func TestApplyTheme_UnknownFallsBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.applyTheme("no-such-theme", false)
	if a.themeID != theme.DefaultID || a.theme.BG != theme.Default().BG {
		t.Fatalf("fallback failed: %q", a.themeID)
	}
}

// TestLoadSpiceConfig_AppliesTheme drives the startup path: a config
// with a theme key restyles the app before the first draw.
func TestLoadSpiceConfig_AppliesTheme(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	path := filepath.Join(cfgDir, "skiff", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"theme": "nord"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, t.TempDir())
	a.loadUserConfig()
	if a.themeID != "nord" {
		t.Fatalf("startup theme: got %q, want nord", a.themeID)
	}
}

// TestThemePick_ArrowsPreviewLive is the picker's core promise:
// moving the highlight restyles the editor immediately, before any
// confirmation.
func TestThemePick_ArrowsPreviewLive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.openThemePick()
	if !a.listPickOpen {
		t.Fatal("picker should open")
	}
	before := a.themeID
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if a.themeID == before {
		t.Fatal("arrow move must preview the highlighted theme live")
	}
	entries := theme.List()
	if a.themeID != entries[a.listPickFiltered()[a.listPickSelected]].ID {
		t.Fatalf("preview out of sync: theme %q vs highlight", a.themeID)
	}
}

// TestThemePick_EscReverts: cancelling puts the original palette back,
// no matter how far the preview wandered.
func TestThemePick_EscReverts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	original := a.currentThemeID()
	a.openThemePick()
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if a.listPickOpen {
		t.Fatal("esc should close the picker")
	}
	if a.themeID != original {
		t.Fatalf("esc must revert: got %q, want %q", a.themeID, original)
	}
}

// TestThemePick_EnterPersists: confirming keeps the previewed theme
// and writes it to config.json.
func TestThemePick_EnterPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.openThemePick()
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	picked := a.themeID
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if a.listPickOpen {
		t.Fatal("enter should close the picker")
	}
	if a.themeID != picked {
		t.Fatalf("enter must keep the preview: %q vs %q", a.themeID, picked)
	}
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil || cfg.Theme != picked {
		t.Fatalf("persist failed: %+v %v", cfg, err)
	}
}

// TestThemePick_FilterNarrowsAndPreviews: typing narrows the list and
// immediately previews the first hit — "nord" shows Nord.
func TestThemePick_FilterNarrowsAndPreviews(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.openThemePick()
	for _, r := range "nord" {
		a.handleListPickKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	filtered := a.listPickFiltered()
	if len(filtered) != 1 || theme.List()[filtered[0]].ID != "nord" {
		t.Fatalf("filter: got %+v", filtered)
	}
	if a.themeID != "nord" {
		t.Fatalf("filter should preview the hit, got %q", a.themeID)
	}
}

// TestThemePick_MouseHoverPreviewsClickConfirms mirrors the keyboard
// contract for the mouse: hover previews, click keeps, outside-click
// cancels and reverts.
func TestThemePick_MouseHoverPreviewsClickConfirms(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.openThemePick()
	mx, my, _, _ := a.listPickRect()
	rowY := my + 4 + 2 // third visible row

	a.handleListPickMouse(mx+5, rowY, 0) // hover
	if a.listPickSelected != 2 || a.themeID != theme.List()[a.listPickFiltered()[2]].ID {
		t.Fatalf("hover should preview row 2: sel %d theme %q", a.listPickSelected, a.themeID)
	}
	a.handleListPickMouse(mx+5, rowY, tcell.Button1) // click keeps
	if a.listPickOpen {
		t.Fatal("click should confirm and close")
	}

	// Outside click cancels a fresh session: the wandering preview
	// reverts to the theme confirmed above (the picker's new original).
	confirmed := a.currentThemeID()
	a.openThemePick()
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.handleListPickMouse(0, 0, tcell.Button1)
	if a.listPickOpen {
		t.Fatal("outside click should close")
	}
	if a.currentThemeID() != confirmed {
		t.Fatalf("outside click must revert to %q, got %q", confirmed, a.currentThemeID())
	}
}

// TestThemePick_ModalOverPickerReverts: another modal opening on top
// (via closeAllModals) must not strand a preview.
func TestThemePick_ModalOverPickerReverts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	original := a.currentThemeID()
	a.openThemePick()
	a.handleListPickKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	a.openMenu()
	if a.listPickOpen {
		t.Fatal("menu should close the picker")
	}
	if a.themeID != original {
		t.Fatalf("stranded preview: %q, want %q", a.themeID, original)
	}
}
