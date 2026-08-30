// =============================================================================
// File: internal/userconfig/userconfig_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain redirects every XDG base directory to a throwaway root before
// any test runs. Most tests here already redirect per-test, but TestMain
// is the backstop that makes a forgotten redirect in a future test
// harmless instead of a write into the developer's real
// ~/.config/skiff/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skiff-test-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestDefaults pins the documented default — icons mode "auto" — so a
// future refactor of the Defaults helper can't silently flip user-
// visible behaviour for everyone who has no config file.
func TestDefaults(t *testing.T) {
	got := Defaults()
	if got.Icons != IconsAuto {
		t.Fatalf("Defaults().Icons = %q, want %q", got.Icons, IconsAuto)
	}
}

// TestLoadEmptyPath verifies that calling Load with no path resolves
// to defaults rather than an error — the editor uses this when
// XDG_CONFIG_HOME is unset and the user has no home directory (CI,
// containers without HOME, etc.).
func TestLoadEmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): unexpected error: %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("Load(\"\").Icons = %q, want %q", cfg.Icons, IconsAuto)
	}
}

// TestLoadMissingFile verifies a non-existent config file is treated
// as "no preferences set" — the common case for fresh installs.
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load(missing): unexpected error: %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("missing file should yield default IconsAuto, got %q", cfg.Icons)
	}
}

// TestLoadEmptyFile covers the "user touched the file but didn't
// write anything" edge case — should be indistinguishable from no
// file at all.
func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load(empty): %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("empty file should yield default, got %q", cfg.Icons)
	}
}

// TestLoadHappyValues exercises every recognised icons mode exactly
// once so a typo in the switch arms shows up immediately.
func TestLoadHappyValues(t *testing.T) {
	cases := map[string]IconsMode{
		`{"icons":"auto"}`: IconsAuto,
		`{"icons":"on"}`:   IconsOn,
		`{"icons":"off"}`:  IconsOff,
		`{"icons":"AUTO"}`: IconsAuto, // case-insensitive
		`{"icons":" On "}`: IconsOn,   // whitespace-tolerant
		`{}`:               IconsAuto, // omitted field uses default
	}
	for body, want := range cases {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, err := Load(p)
			if err != nil {
				t.Fatalf("Load(%s): %v", body, err)
			}
			if cfg.Icons != want {
				t.Fatalf("Load(%s).Icons = %q, want %q", body, cfg.Icons, want)
			}
		})
	}
}

// TestLoadUnknownValue verifies a typo in the icons field surfaces as
// a clear error rather than silently reverting to defaults — that's
// the bug we want users to notice and fix in their config file.
func TestLoadUnknownValue(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"icons":"yes-please"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err == nil {
		t.Fatalf("expected error for unknown value, got nil")
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("on error should still return safe defaults, got %q", cfg.Icons)
	}
}

// TestLoadMalformedJSON verifies a syntactically broken config doesn't
// crash the editor — the user gets an error and the editor uses
// defaults until they fix the file.
func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}
}

// TestLoadForwardCompat verifies the loader ignores top-level fields
// it doesn't recognise — so a future config.json with new keys keeps
// working on older binaries instead of erroring out.
func TestLoadForwardCompat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{"icons":"on","theme":"future-feature","unknown_block":{"a":1}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("forward-compat config should not error, got %v", err)
	}
	if cfg.Icons != IconsOn {
		t.Fatalf("recognised field still expected: got %q", cfg.Icons)
	}
}

// TestDefaultPathHonoursXDG verifies XDG_CONFIG_HOME wins over the
// fallback when set — important for nix-style setups that move every
// dotfile out of $HOME.
func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got := DefaultPath()
	want := filepath.Join("/tmp/xdg-test", "skiff", "config.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

// TestDefaultPathFallsBackToHome verifies the ~/.config fallback when
// XDG_CONFIG_HOME isn't set — the common path on macOS/Linux without
// XDG configured.
func TestDefaultPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/home-test")
	got := DefaultPath()
	want := filepath.Join("/tmp/home-test", ".config", "skiff", "config.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

// TestLoadTheme reads the theme key back out, defaulting to "" when
// absent — the app maps "" to its built-in default.
func TestLoadTheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"icons": "on", "theme": "dracula"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Theme != "dracula" || cfg.Icons != IconsOn {
		t.Fatalf("got %+v", cfg)
	}
}

// TestSetThemePreservesOtherKeys pins the round-trip rule: writing the
// theme must keep icons — and keys from future skiff versions — intact.
func TestSetThemePreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"icons": "on", "future-key": 7}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetTheme(path, "nord"); err != nil {
		t.Fatalf("set: %v", err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"theme": "nord"`, `"icons": "on"`, `"future-key": 7`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in:\n%s", want, data)
		}
	}
	// And a fresh file is created when none exists.
	fresh := filepath.Join(dir, "sub", "config.json")
	if err := SetTheme(fresh, "dracula"); err != nil {
		t.Fatalf("fresh set: %v", err)
	}
	cfg, err := Load(fresh)
	if err != nil || cfg.Theme != "dracula" {
		t.Fatalf("fresh load: %+v %v", cfg, err)
	}
}

// TestLoadWrap covers the wrap key's whole contract: absent defaults to
// on, "on"/"off" parse (case-insensitively), and a typo surfaces as an
// error with defaults — same tell-the-user rule as a bad icons value.
func TestLoadWrap(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		json    string
		want    bool
		wantErr bool
	}{
		{"absent defaults on", `{"icons": "on"}`, true, false},
		{"explicit on", `{"wrap": "on"}`, true, false},
		{"explicit off", `{"wrap": "off"}`, false, false},
		{"case insensitive", `{"wrap": "OFF"}`, false, false},
		{"typo errors", `{"wrap": "nope"}`, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(tc.json), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, err := Load(path)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if cfg.Wrap != tc.want {
				t.Fatalf("Wrap = %v, want %v", cfg.Wrap, tc.want)
			}
		})
	}
}

// TestSetWrapPreservesOtherKeys pins SetWrap's round-trip rule (keep
// unknown keys, create the file when missing) and that the value it
// writes reads back through Load.
func TestSetWrapPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"theme": "nord", "future-key": 7}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetWrap(path, false); err != nil {
		t.Fatalf("set: %v", err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"wrap": "off"`, `"theme": "nord"`, `"future-key": 7`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in:\n%s", want, data)
		}
	}
	cfg, err := Load(path)
	if err != nil || cfg.Wrap {
		t.Fatalf("load after set: %+v %v", cfg, err)
	}
	// Fresh-file creation, and flipping back to on round-trips too.
	fresh := filepath.Join(dir, "sub", "config.json")
	if err := SetWrap(fresh, true); err != nil {
		t.Fatalf("fresh set: %v", err)
	}
	cfg, err = Load(fresh)
	if err != nil || !cfg.Wrap {
		t.Fatalf("fresh load: %+v %v", cfg, err)
	}
}

// TestSetThemeWritesAtomically pins the durability contract added
// after the audit: config.json is read-modify-written on every theme
// or wrap toggle, so a torn write silently resets the user's whole
// config. Shrinking the file must leave no trailing bytes from the
// longer previous version, and no temp file may survive beside it.
func TestSetThemeWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	long := `{"theme": "` + strings.Repeat("x", 400) + `"}`
	if err := os.WriteFile(path, []byte(long), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetTheme(path, "nord"); err != nil {
		t.Fatalf("set: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), strings.Repeat("x", 10)) {
		t.Fatalf("stale bytes survived the rewrite:\n%s", data)
	}
	if !json.Valid(data) {
		t.Fatalf("config.json is not valid JSON after a shrinking write:\n%s", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only config.json in the config dir, got %v", names)
	}
}

// TestLoadGitignore covers the gitignore key's whole contract: absent
// defaults to on (the sidebar hides ignored files out of the box, so it
// agrees with the finder), "on"/"off" parse case-insensitively, and a
// typo is reported rather than silently swallowed. Same shape as the
// wrap key because both now share parseOnOff — this is what would catch
// that helper drifting for one key and not the other.
func TestLoadGitignore(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		json    string
		want    bool
		wantErr bool
	}{
		{"absent defaults on", `{"icons": "on"}`, true, false},
		{"explicit on", `{"gitignore": "on"}`, true, false},
		{"explicit off", `{"gitignore": "off"}`, false, false},
		{"case insensitive", `{"gitignore": "OFF"}`, false, false},
		{"typo errors", `{"gitignore": "nope"}`, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "gi-"+strings.ReplaceAll(tc.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(tc.json), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, err := Load(path)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if cfg.Gitignore != tc.want {
				t.Fatalf("Gitignore = %v, want %v", cfg.Gitignore, tc.want)
			}
		})
	}
}

// TestLoadGitignoreErrorNamesItsOwnKey guards the one thing sharing
// parseOnOff could plausibly break: a bad gitignore value must not be
// reported as a bad wrap value.
func TestLoadGitignoreErrorNamesItsOwnKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"gitignore": "yes"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for gitignore=yes")
	}
	if !strings.Contains(err.Error(), "gitignore must be") {
		t.Fatalf("error should name the gitignore key, got %v", err)
	}
}

// TestSetGitignorePreservesOtherKeys pins the round-trip: unknown keys
// and the neighbouring wrap key survive, the file is created when
// missing, and the value reads back through Load. A toggle that dropped
// the user's theme would be worse than not persisting at all.
func TestSetGitignorePreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	seed := `{"theme": "nord", "wrap": "off", "future-key": 7}`
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetGitignore(path, false); err != nil {
		t.Fatalf("set: %v", err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"gitignore": "off"`, `"wrap": "off"`, `"theme": "nord"`, `"future-key": 7`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in:\n%s", want, data)
		}
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load after set: %v", err)
	}
	if cfg.Gitignore {
		t.Fatal("Gitignore should read back false")
	}
	if cfg.Wrap {
		t.Fatal("SetGitignore must not disturb the wrap key")
	}

	// Fresh-file creation, and flipping back to on round-trips too.
	fresh := filepath.Join(dir, "sub", "config.json")
	if err := SetGitignore(fresh, true); err != nil {
		t.Fatalf("fresh set: %v", err)
	}
	cfg, err = Load(fresh)
	if err != nil || !cfg.Gitignore {
		t.Fatalf("fresh load: %+v %v", cfg, err)
	}
}

// TestLoadScrollCaret covers the scrollcaret key's contract: absent
// defaults to OFF (scrolling has never moved the caret, so opting in is
// explicit), "on"/"off" parse case-insensitively, and a typo surfaces
// as an error with the default — the same tell-the-user rule as wrap.
func TestLoadScrollCaret(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		json    string
		want    bool
		wantErr bool
	}{
		{"absent defaults off", `{"icons": "on"}`, false, false},
		{"explicit on", `{"scrollcaret": "on"}`, true, false},
		{"explicit off", `{"scrollcaret": "off"}`, false, false},
		{"case insensitive", `{"scrollcaret": "ON"}`, true, false},
		{"typo errors", `{"scrollcaret": "nope"}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(tc.json), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, err := Load(path)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if cfg.ScrollCaret != tc.want {
				t.Fatalf("ScrollCaret = %v, want %v", cfg.ScrollCaret, tc.want)
			}
		})
	}
}

// TestSetScrollCaretRoundTrip pins that SetScrollCaret writes through
// the shared preserve-unknown-keys machinery and reads back via Load —
// the same contract SetWrap/SetGitignore carry.
func TestSetScrollCaretRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"theme": "nord"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetScrollCaret(path, true); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, err := Load(path)
	if err != nil || !cfg.ScrollCaret || cfg.Theme != "nord" {
		t.Fatalf("load after set: %+v %v", cfg, err)
	}
}
