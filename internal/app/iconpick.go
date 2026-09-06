// =============================================================================
// File: internal/app/iconpick.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// iconpick.go is the ≡ → Icons… picker: Auto / On / Off for the Nerd
// Font glyphs in the tree and the tab strip, previewed live as the
// highlight moves, persisted to config.json on Enter, reverted on
// cancel — the theme picker's shape applied to the one preference that
// detection cannot settle. Auto-detection inspects the machine skiff
// runs on, which over SSH is the wrong machine, so until now the only
// way to turn icons on in the editor's primary habitat was to hand-edit
// config.json. The picker is that door, reachable from the menu the way
// CLAUDE.md requires of every action.

package app

import (
	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// iconsUndecidableHint is the one-time flash shown at startup when the
// config has never chosen an icons mode and detection cannot answer
// (an SSH session): the user who has a Nerd Font on their terminal
// would otherwise never learn why the tree is glyph-less.
const iconsUndecidableHint = "Icons: can't detect a Nerd Font over SSH — ≡ → Icons… to turn them on"

// iconsModes lists the picker's rows in display order.
func iconsModes() []userconfig.IconsMode {
	return []userconfig.IconsMode{userconfig.IconsAuto, userconfig.IconsOn, userconfig.IconsOff}
}

// iconsModeLabel names one mode for the picker. Auto says what it
// would actually do — "detected: on" on a local machine with the font,
// "can't detect over SSH" remotely — because a row that reads "Auto"
// alone hides exactly the information the picker exists to surface.
func iconsModeLabel(mode userconfig.IconsMode, autoOn bool) string {
	switch mode {
	case userconfig.IconsOn:
		return "On"
	case userconfig.IconsOff:
		return "Off"
	}
	if icons.Undecidable() {
		return "Auto (can't detect over SSH: off)"
	}
	if autoOn {
		return "Auto (detected: on)"
	}
	return "Auto (detected: off)"
}

// menuIcons is the ≡ → Icons… entry point.
func (a *App) menuIcons() {
	a.closeMenu()
	a.openIconsPick()
}

// openIconsPick opens the live-preview picker. The current mode comes
// from config.json rather than from App state: the app only ever keeps
// the resolved bool (on the tree), and the picker needs the choice
// behind it. Auto's answer is resolved once here — never per
// highlight move, because on a local machine Detect shells out to
// fc-list — and reused by every preview.
func (a *App) openIconsPick() {
	if a.tree == nil {
		return
	}
	cfg, _ := userconfig.Load(userconfig.DefaultPath())
	modes := iconsModes()
	autoOn := icons.Resolve(userconfig.IconsAuto)
	original := a.tree.IconsEnabled
	items := make([]listPickItem, len(modes))
	for i, m := range modes {
		items[i] = listPickItem{Label: iconsModeLabel(m, autoOn), Current: m == cfg.Icons}
	}
	a.openListPick("Icons — previews live", items,
		func(app *App, i int) { app.applyIconsMode(modes[i], autoOn, true) },
		func(app *App, i int) { app.applyIconsMode(modes[i], autoOn, false) },
		func(app *App) {
			// Cancel reverts however far the preview wandered.
			app.tree.IconsEnabled = original
		})
}

// applyIconsMode stamps the mode's resolved answer onto the tree — the
// single source every icon-drawing surface reads (iconsOn) — so the
// next draw shows or hides glyphs everywhere at once. persist=true also
// writes the mode to config.json; a failed write keeps the session's
// choice and says so.
func (a *App) applyIconsMode(mode userconfig.IconsMode, autoOn, persist bool) {
	if a.tree == nil {
		return
	}
	on := autoOn
	switch mode {
	case userconfig.IconsOn:
		on = true
	case userconfig.IconsOff:
		on = false
	}
	a.tree.IconsEnabled = on
	if !persist {
		return
	}
	if err := userconfig.SetIcons(userconfig.DefaultPath(), mode); err != nil {
		a.flashError("Icons set for this session — saving failed: " + err.Error())
		return
	}
	a.flash("Icons: " + iconsModeLabel(mode, autoOn))
}
