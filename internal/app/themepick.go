// =============================================================================
// File: internal/app/themepick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// themepick.go wires the theme registry into the generic list picker
// (listpick.go) with the hooks that make themes special: moving the
// highlight previews the palette on the whole editor live, Enter
// persists to config.json, and cancel — Esc, outside click, or another
// modal opening on top — reverts to the theme the picker opened with.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// applyTheme switches the live palette to the registry entry id.
// Every surface reads a.theme at draw time, so the swap is: replace the
// struct, retint the terminal default style, and force every tab to
// re-tokenise (highlight styles bake the palette in). persist=false is
// the preview/startup path.
func (a *App) applyTheme(id string, persist bool) {
	th, ok := theme.ByID(id)
	if !ok {
		a.flash("Unknown theme " + id + " — using " + theme.DefaultID)
		id = theme.DefaultID
		th, _ = theme.ByID(id)
	}
	a.theme = th
	a.themeID = id
	// Re-run the colour-depth fallback: the registry hands back the
	// authored 24-bit palette every time, so without this a theme
	// switch on a 16-colour terminal would silently undo the degrade
	// applied at startup.
	a.applyColorDepth()
	a.screen.SetStyle(tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text))
	for _, t := range a.tabs.Tabs() {
		t.StyleStale = true
	}
	if !persist {
		return
	}
	if err := userconfig.SetTheme(userconfig.DefaultPath(), id); err != nil {
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

// currentThemeID returns the active theme's id, defaulting when the
// app predates the field (tests that build App literals directly).
func (a *App) currentThemeID() string {
	if a.themeID == "" {
		return theme.DefaultID
	}
	return a.themeID
}

// menuTheme is the ≡ → Theme… entry point.
func (a *App) menuTheme() {
	a.closeMenu()
	a.openThemePick()
}

// openThemePick opens the live-preview picker: every registered theme,
// the active one marked, light palettes tagged.
func (a *App) openThemePick() {
	entries := theme.List()
	current := a.currentThemeID()
	original := current
	items := make([]listPickItem, len(entries))
	for i, e := range entries {
		items[i] = listPickItem{Label: e.Name, Current: e.ID == current}
		if isLightTheme(e) {
			items[i].Tag = "light"
		}
	}
	a.openListPick("Theme — previews live", items,
		func(app *App, i int) { app.applyTheme(entries[i].ID, true) },
		func(app *App, i int) { app.applyTheme(entries[i].ID, false) },
		func(app *App) {
			// Cancel reverts however far the preview wandered.
			if app.currentThemeID() != original {
				app.applyTheme(original, false)
			}
		})
}

// isLightTheme classifies a palette by its background: contrast vs
// pure black above ~9:1 means a luminous background — a light theme.
func isLightTheme(e theme.Entry) bool {
	return theme.ContrastRatio(e.Build().BG, tcell.NewRGBColor(0, 0, 0)) > 9
}
