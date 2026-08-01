// =============================================================================
// File: internal/app/themepick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for theme selection: applying a palette live, persisting it,
// falling back on unknown ids, and the picker's form flow.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnlam90/skiff/internal/spiceconfig"
	"github.com/johnlam90/skiff/internal/theme"
)

// TestApplyTheme_SwapsPaletteAndStalesTabs pins the live-switch
// contract: the struct swaps, open tabs re-tokenise, and the choice
// lands in config.json when persist is set.
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
	cfg, err := spiceconfig.Load(spiceconfig.DefaultPath())
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
	a.loadSpiceConfig()
	if a.themeID != "nord" {
		t.Fatalf("startup theme: got %q, want nord", a.themeID)
	}
}

// TestMenuTheme_PickerAppliesChoice walks the form flow: the picker
// opens pre-selected on the current theme, and submitting a different
// name applies that palette.
func TestMenuTheme_PickerAppliesChoice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.menuTheme()
	if !a.formOpen {
		t.Fatal("picker form should open")
	}
	a.formValues["THEME"] = "Nord"
	a.formCallback(a, a.formValues)
	if a.themeID != "nord" {
		t.Fatalf("picked theme not applied: %q", a.themeID)
	}
}
