// =============================================================================
// File: internal/app/themepick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// themepick.go owns theme selection: applying a palette from the
// registry (startup and runtime), the ≡ → Theme… picker, and persisting
// the choice into config.json's "theme" key — the same file the icons
// preference already lives in, so this stays a key, not a new config
// mechanism.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/spiceconfig"
	"github.com/johnlam90/skiff/internal/theme"
)

// applyTheme switches the live palette to the registry entry id.
// Every surface reads a.theme at draw time, so the swap is: replace the
// struct, retint the terminal default style, and force every tab to
// re-tokenise (highlight styles bake the palette in). persist=false is
// the startup path — re-writing the config with its own value on every
// launch would be noise.
func (a *App) applyTheme(id string, persist bool) {
	th, ok := theme.ByID(id)
	if !ok {
		a.flash("Unknown theme " + id + " — using " + theme.DefaultID)
		id = theme.DefaultID
		th, _ = theme.ByID(id)
	}
	a.theme = th
	a.themeID = id
	a.screen.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	for _, t := range a.tabs {
		t.StyleStale = true
	}
	if !persist {
		return
	}
	if err := spiceconfig.SetTheme(spiceconfig.DefaultPath(), id); err != nil {
		a.flash("Theme set for this session — saving failed: " + err.Error())
		return
	}
	a.flash("Theme: " + themeDisplayName(id))
}

// themeDisplayName maps a registry id to its picker label.
func themeDisplayName(id string) string {
	for _, e := range theme.List() {
		if e.ID == id {
			return e.Name
		}
	}
	return id
}

// menuTheme is the ≡ → Theme… entry: a select of every registered
// palette, current one pre-selected; submitting applies and persists.
func (a *App) menuTheme() {
	a.closeMenu()
	entries := theme.List()
	names := make([]string, len(entries))
	current := themeDisplayName(a.currentThemeID())
	for i, e := range entries {
		names[i] = e.Name
	}
	prompts := []customactions.Prompt{{
		Key: "THEME", Label: "Theme", Type: customactions.PromptSelect,
		Options: names, Default: current,
	}}
	a.openForm("Theme", prompts, func(app *App, values map[string]string) {
		picked := values["THEME"]
		for _, e := range entries {
			if e.Name == picked {
				app.applyTheme(e.ID, true)
				return
			}
		}
	})
}

// currentThemeID returns the active theme's id, defaulting when the
// app predates the field (tests that build App literals directly).
func (a *App) currentThemeID() string {
	if a.themeID == "" {
		return theme.DefaultID
	}
	return a.themeID
}
