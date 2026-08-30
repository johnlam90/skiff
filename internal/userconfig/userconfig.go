// =============================================================================
// File: internal/userconfig/userconfig.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package userconfig loads the editor's small user-level config from
// ~/.config/skiff/config.json. It's separate from customactions on
// purpose: actions.json is a list of shell-out menu entries, config.json
// is editor preferences. Keeping them apart means a malformed actions
// file can't break editor settings and vice-versa.
//
// Schema today is intentionally tiny — a handful of flat keys — but the
// loader is already wrapped in a struct so we can grow new top-level
// fields without breaking older configs:
//
//	{"icons": "auto"}          // default; auto-detect Nerd Fonts on startup
//	{"icons": "on"}            // force-on, even if detection would say no
//	{"icons": "off"}           // force-off, even if a Nerd Font is installed
//	{"theme": "tokyo-night"}   // any id from internal/theme's registry
//	{"wrap": "off"}            // long lines pan sideways; "on" (default) wraps
//	{"gitignore": "off"}       // file tree shows ignored files; "on" (default) hides them
//
// The loader is best-effort the same way customactions is: missing
// file → defaults, malformed file → error returned for the app to
// flash, but the editor still starts cleanly.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnlam90/skiff/internal/atomicfile"
)

// IconsMode is the user's preference for Nerd Font icons in the file
// tree. "auto" means "use them iff a Nerd Font is installed"; the
// other two values bypass detection entirely.
type IconsMode string

const (
	IconsAuto IconsMode = "auto"
	IconsOn   IconsMode = "on"
	IconsOff  IconsMode = "off"
)

// Config is the resolved, validated form of config.json. Callers get a
// fully-populated Config back from Load — defaults are filled in for
// any field the file omitted, so consumers never need to nil-check.
type Config struct {
	Icons IconsMode
	// Theme is the theme id to start with ("" = default). Validated by
	// the app against the theme registry, not here — the config loader
	// shouldn't need to import the palette table.
	Theme string
	// Wrap is whether the editor soft-wraps long lines. Defaults to on —
	// the editor's audience reads code over SSH, where sideways panning
	// hurts the most; the menu toggle persists an "off" here.
	Wrap bool
	// Gitignore is whether the file tree hides entries the project's
	// .gitignore files exclude. Defaults to on so the sidebar and the
	// finder agree about what counts as project noise; the ≡ View row
	// persists an "off" here for the times you need to see build output.
	Gitignore bool
	// ScrollCaret is whether a viewport-only scroll (wheel, scrollbar)
	// pulls the caret along so it stays on a visible line. Defaults to
	// off — scrolling has never moved the caret here (the VS Code
	// behavior), so following it is an explicit opt-in via the ≡ View
	// row.
	ScrollCaret bool
}

// Defaults returns a Config populated with the values used when no
// config file is present (or every field in it is blank). Centralised
// so tests and the loader can't drift from each other.
func Defaults() Config {
	return Config{Icons: IconsAuto, Wrap: true, Gitignore: true}
}

// fileFormat mirrors the on-disk JSON shape. We decode into this and
// then promote into Config so the public type doesn't have to carry
// JSON tags or pointer fields just for "field was absent" detection.
type fileFormat struct {
	Icons       string `json:"icons,omitempty"`
	Theme       string `json:"theme,omitempty"`
	Wrap        string `json:"wrap,omitempty"`
	Gitignore   string `json:"gitignore,omitempty"`
	ScrollCaret string `json:"scrollcaret,omitempty"`
}

// DefaultPath returns the canonical config-file location:
// $XDG_CONFIG_HOME/skiff/config.json, falling back to
// ~/.config/skiff/config.json. Returns "" when neither resolves
// — callers should treat that as "use defaults".
func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "skiff", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "skiff", "config.json")
}

// Load reads and parses the config file at path, returning a Config
// with defaults filled in for any missing or blank fields.
//
// Contract:
//   - path == ""              → (Defaults(), nil). Treated as "no
//     config configured".
//   - file doesn't exist      → (Defaults(), nil). Same as above.
//   - file unreadable         → (Defaults(), err). Caller can flash a
//     message; editor keeps running on defaults.
//   - file empty / all-blank  → (Defaults(), nil).
//   - unknown icons value     → (Defaults(), err). We'd rather tell
//     the user their config has a typo than silently fall back to
//     defaults and hide the bug.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return cfg, nil
	}

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}

	switch IconsMode(strings.ToLower(strings.TrimSpace(ff.Icons))) {
	case "":
		// field omitted — keep default
	case IconsAuto:
		cfg.Icons = IconsAuto
	case IconsOn:
		cfg.Icons = IconsOn
	case IconsOff:
		cfg.Icons = IconsOff
	default:
		return Defaults(), fmt.Errorf(
			"%s: icons must be %q, %q, or %q (got %q)",
			path, IconsAuto, IconsOn, IconsOff, ff.Icons,
		)
	}
	cfg.Theme = strings.TrimSpace(ff.Theme)

	wrap, err := parseOnOff(path, "wrap", ff.Wrap, cfg.Wrap)
	if err != nil {
		return Defaults(), err
	}
	cfg.Wrap = wrap

	gi, err := parseOnOff(path, "gitignore", ff.Gitignore, cfg.Gitignore)
	if err != nil {
		return Defaults(), err
	}
	cfg.Gitignore = gi

	sc, err := parseOnOff(path, "scrollcaret", ff.ScrollCaret, cfg.ScrollCaret)
	if err != nil {
		return Defaults(), err
	}
	cfg.ScrollCaret = sc
	return cfg, nil
}

// parseOnOff resolves one "on"/"off" key, leaving def in place when the
// field was omitted or blank. Every boolean key goes through it so a new
// one can't quietly invent a second spelling of the same switch.
func parseOnOff(path, key, value string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return def, nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return def, fmt.Errorf("%s: %s must be \"on\" or \"off\" (got %q)", path, key, value)
}

// SetTheme persists the theme id into the config file at path,
// creating it if needed and preserving every other key (including ones
// this version of skiff doesn't know about — a newer config must
// survive a round-trip through an older binary).
func SetTheme(path, id string) error {
	if path == "" {
		return errors.New("no config path available")
	}
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		// A malformed file is overwritten rather than failed on — the
		// user just picked a theme; keeping it beats preserving JSON
		// that was already broken.
		_ = json.Unmarshal(data, &raw)
	}
	raw["theme"] = id
	return writeRaw(path, raw)
}

// SetWrap persists the soft-wrap preference into the config file at
// path, with the same create-if-needed / preserve-unknown-keys contract
// as SetTheme.
func SetWrap(path string, on bool) error {
	return setOnOff(path, "wrap", on)
}

// SetGitignore persists the file tree's "hide ignored entries"
// preference, with the same contract as SetWrap.
func SetGitignore(path string, on bool) error {
	return setOnOff(path, "gitignore", on)
}

// SetScrollCaret persists the "caret follows scroll" preference, with
// the same contract as SetWrap.
func SetScrollCaret(path string, on bool) error {
	return setOnOff(path, "scrollcaret", on)
}

// setOnOff writes one "on"/"off" key into the config file at path,
// creating it if needed and preserving every other key — including ones
// this version of skiff doesn't know about, so a newer config survives a
// round-trip through an older binary.
func setOnOff(path, key string, on bool) error {
	if path == "" {
		return errors.New("no config path available")
	}
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		// Same tradeoff as SetTheme: a malformed file is overwritten —
		// keeping the user's fresh choice beats preserving broken JSON.
		_ = json.Unmarshal(data, &raw)
	}
	if on {
		raw[key] = "on"
	} else {
		raw[key] = "off"
	}
	return writeRaw(path, raw)
}

// writeRaw marshals the merged key map back to disk. The write goes
// through atomicfile because config.json is read-modify-written on
// every theme/wrap toggle: a truncated file would silently reset the
// user's preferences (and drop any key a newer skiff had written)
// rather than fail loudly.
func writeRaw(path string, raw map[string]any) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0644)
}
