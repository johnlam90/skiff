// =============================================================================
// File: internal/app/iconpick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the ≡ → Icons… picker: the live preview on the tree, the
// persisted choice, the cancel revert, and the one-time SSH hint at
// startup. Every test points XDG_CONFIG_HOME at a temp dir so the
// user's real config.json is never read or written.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/userconfig"
)

// iconsPickApp opens a project app with a clean temp config and the
// picker up, SSH_CONNECTION cleared so Auto's label is the local one.
func iconsPickApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SSH_CONNECTION", "")
	a := newTestApp(t, t.TempDir())
	a.tree.IconsEnabled = false
	a.openIconsPick()
	if !pickIsOpen(a) {
		t.Fatal("picker should open")
	}
	return a
}

// iconsPickMoveTo walks the highlight to the row for mode and returns
// its index, so a test never depends on where the highlight started.
func iconsPickMoveTo(t *testing.T, a *App, mode userconfig.IconsMode) {
	t.Helper()
	want := -1
	for i, m := range iconsModes() {
		if m == mode {
			want = i
		}
	}
	p := pickPrefab(t, a)
	for tries := 0; tries < 6; tries++ {
		if p.Filtered()[p.Sel()] == want {
			return
		}
		key := tcell.KeyDown
		if p.Filtered()[p.Sel()] > want {
			key = tcell.KeyUp
		}
		a.handleKey(tcell.NewEventKey(key, 0, 0))
	}
	t.Fatalf("could not reach the %q row", mode)
}

// TestIconsPick_PreviewsLiveAndPersists is the picker's core promise:
// moving the highlight onto On stamps glyphs onto the tree before any
// confirmation, Enter writes the mode to config.json, and the tree
// keeps the choice afterwards.
func TestIconsPick_PreviewsLiveAndPersists(t *testing.T) {
	a := iconsPickApp(t)
	iconsPickMoveTo(t, a, userconfig.IconsOn)
	if !a.tree.IconsEnabled {
		t.Fatal("highlighting On must preview glyphs on the tree immediately")
	}
	iconsPickMoveTo(t, a, userconfig.IconsOff)
	if a.tree.IconsEnabled {
		t.Fatal("highlighting Off must preview the glyph-less tree")
	}
	iconsPickMoveTo(t, a, userconfig.IconsOn)
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if pickIsOpen(a) {
		t.Fatal("Enter should close the picker")
	}
	if !a.tree.IconsEnabled {
		t.Fatal("the picked mode must stay applied")
	}
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil || cfg.Icons != userconfig.IconsOn || !cfg.IconsSet {
		t.Fatalf("persisted config: %+v %v, want icons on", cfg, err)
	}
	if !strings.Contains(a.statusMsg, "Icons: On") {
		t.Fatalf("flash = %q, want the chosen mode named", a.statusMsg)
	}
}

// TestIconsPick_EscReverts: cancelling puts the tree back the way it
// was, however far the preview wandered, and writes nothing.
func TestIconsPick_EscReverts(t *testing.T) {
	a := iconsPickApp(t)
	iconsPickMoveTo(t, a, userconfig.IconsOn)
	if !a.tree.IconsEnabled {
		t.Fatal("precondition: the preview should have turned icons on")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	if pickIsOpen(a) {
		t.Fatal("Esc should close the picker")
	}
	if a.tree.IconsEnabled {
		t.Fatal("cancel must restore the original tree state")
	}
	if _, err := os.Stat(userconfig.DefaultPath()); !os.IsNotExist(err) {
		t.Fatalf("cancel must not write config.json (stat err = %v)", err)
	}
}

// TestIconsPick_MarksTheConfiguredModeCurrent pins where the picker
// opens from: the row for the mode in config.json carries the current
// marker, and Auto's label says what auto would do here.
func TestIconsPick_MarksTheConfiguredModeCurrent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SSH_CONNECTION", "")
	if err := userconfig.SetIcons(userconfig.DefaultPath(), userconfig.IconsOff); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, t.TempDir())
	a.openIconsPick()
	p := pickPrefab(t, a)
	for i, item := range p.Items {
		if item.Current != (iconsModes()[i] == userconfig.IconsOff) {
			t.Fatalf("row %q current=%v, want the configured Off row marked", item.Label, item.Current)
		}
	}
	if !strings.HasPrefix(p.Items[0].Label, "Auto (detected:") {
		t.Fatalf("Auto row = %q, want it to say what detection found", p.Items[0].Label)
	}
	t.Setenv("SSH_CONNECTION", "10.0.0.2 1 10.0.0.1 22")
	if got := iconsModeLabel(userconfig.IconsAuto, false); !strings.Contains(got, "SSH") {
		t.Fatalf("Auto over SSH = %q, want it to say detection cannot answer", got)
	}
}

// TestIconsPick_SingleFileModeHasNoRow pins the visibility gate: with
// no tree there is nothing to stamp, so the View row is absent and the
// opener is a no-op rather than a nil dereference.
func TestIconsPick_SingleFileModeHasNoRow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.openIconsPick()
	if pickIsOpen(a) {
		t.Fatal("no tree: the picker must not open")
	}
	if _, ok := menuItemByLabelOK(a, "Icons…"); ok {
		t.Fatal("the Icons… row must hide in single-file mode")
	}
}

// TestLoadUserConfig_HintsAtThePickerOnceOverSSH pins the one-time
// hint: at startup inside an SSH session, a config that has never
// chosen an icons mode flashes where the choice lives — and a config
// that has (even "auto") stays quiet, as does a local session.
func TestLoadUserConfig_HintsAtThePickerOnceOverSSH(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("SSH_CONNECTION", "10.0.0.2 1 10.0.0.1 22")
	a := newTestApp(t, t.TempDir())
	a.statusMsg = ""
	a.loadUserConfig()
	if a.statusMsg != iconsUndecidableHint {
		t.Fatalf("flash = %q, want the picker hint", a.statusMsg)
	}
	if a.tree.IconsEnabled {
		t.Fatal("auto over SSH must resolve to off")
	}

	path := filepath.Join(cfgHome, "skiff", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"icons": "auto"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.statusMsg = ""
	a.loadUserConfig()
	if a.statusMsg != "" {
		t.Fatalf("an explicit icons key is a decision; got flash %q", a.statusMsg)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	t.Setenv("SSH_CONNECTION", "")
	a.statusMsg = ""
	a.loadUserConfig()
	if a.statusMsg == iconsUndecidableHint {
		t.Fatal("a local session can detect; no hint")
	}
}
