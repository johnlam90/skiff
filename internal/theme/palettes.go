// =============================================================================
// File: internal/theme/palettes.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// palettes.go is the theme registry: every palette the ≡ → Theme picker
// offers. The non-default palettes were ported mechanically from druk's
// theme set (github.com/letstri/druk, MIT) — ui colors map onto skiff's
// surfaces, syntax groups onto the Syn* fields, and the two derived
// values (FindMatch tint, FileColor) are premixed at port time. Skiff's
// own hand-tuned Tokyo Night stays the default and is defined in
// theme.go; edits to a ported palette are fair game — this file is
// checked in, not regenerated.

package theme

import "github.com/gdamore/tcell/v2"

// Entry names one selectable theme: the stable ID persisted in
// config.json and the display name the picker shows.
type Entry struct {
	ID    string
	Name  string
	Build func() Theme
}

// registry lists every theme, default first. Order is what the picker
// shows, so keep the default on top and the rest alphabetical.
var registry = []Entry{
	{ID: DefaultID, Name: "Tokyo Night", Build: Default},
	{ID: "ayu-dark", Name: "Ayu Dark", Build: themeAyuDark},
	{ID: "ayu-light", Name: "Ayu Light", Build: themeAyuLight},
	{ID: "ayu-mirage", Name: "Ayu Mirage", Build: themeAyuMirage},
	{ID: "catppuccin-frappe", Name: "Catppuccin Frappé", Build: themeCatppuccinFrappe},
	{ID: "catppuccin-latte", Name: "Catppuccin Latte", Build: themeCatppuccinLatte},
	{ID: "catppuccin-macchiato", Name: "Catppuccin Macchiato", Build: themeCatppuccinMacchiato},
	{ID: "catppuccin-mocha", Name: "Catppuccin Mocha", Build: themeCatppuccinMocha},
	{ID: "dracula", Name: "Dracula", Build: themeDracula},
	{ID: "everforest-dark", Name: "Everforest Dark", Build: themeEverforestDark},
	{ID: "everforest-light", Name: "Everforest Light", Build: themeEverforestLight},
	{ID: "github-dark", Name: "GitHub Dark", Build: themeGithubDark},
	{ID: "github-light", Name: "GitHub Light", Build: themeGithubLight},
	{ID: "gruvbox-dark", Name: "Gruvbox Dark", Build: themeGruvboxDark},
	{ID: "gruvbox-light", Name: "Gruvbox Light", Build: themeGruvboxLight},
	{ID: "kanagawa-dragon", Name: "Kanagawa Dragon", Build: themeKanagawaDragon},
	{ID: "kanagawa-lotus", Name: "Kanagawa Lotus", Build: themeKanagawaLotus},
	{ID: "kanagawa-wave", Name: "Kanagawa Wave", Build: themeKanagawaWave},
	{ID: "nord", Name: "Nord", Build: themeNord},
	{ID: "offshore", Name: "Offshore", Build: themeOffshore},
	{ID: "one-dark", Name: "One Dark", Build: themeOneDark},
	{ID: "rose-pine-dawn", Name: "Rosé Pine Dawn", Build: themeRosePineDawn},
	{ID: "rose-pine-moon", Name: "Rosé Pine Moon", Build: themeRosePineMoon},
	{ID: "rose-pine", Name: "Rosé Pine", Build: themeRosePine},
	{ID: "solarized-dark", Name: "Solarized Dark", Build: themeSolarizedDark},
	{ID: "solarized-light", Name: "Solarized Light", Build: themeSolarizedLight},
	{ID: "vesper", Name: "Vesper", Build: themeVesper},
}

// DefaultID is the id of the built-in default theme.
const DefaultID = "tokyo-night"

// List returns the selectable themes in picker order. Each entry's
// Build is wrapped so the picker (and its live preview) hands out the
// same contrast-corrected palette ByID does — a theme must never look
// different depending on which door it came through.
func List() []Entry {
	out := make([]Entry, len(registry))
	for i, e := range registry {
		build := e.Build
		e.Build = func() Theme { return readable(build()) }
		out[i] = e
	}
	return out
}

// ByID returns the theme with the given id, or the default plus false
// when the id is unknown (a stale config.json must never break startup).
// The palette comes back contrast-corrected: ports keep their upstream
// character but can't ship a status bar the user can't read.
func ByID(id string) (Theme, bool) {
	for _, e := range registry {
		if e.ID == id {
			return readable(e.Build()), true
		}
	}
	return readable(Default()), false
}

// themeAyuDark is the Ayu Dark palette, ported from druk.
func themeAyuDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x10, 0x14, 0x1c),
		SidebarBG:   tcell.NewRGBColor(0x0d, 0x10, 0x17),
		StatusBG:    tcell.NewRGBColor(0xe6, 0xb4, 0x50),
		StatusFg:    tcell.NewRGBColor(0x10, 0x14, 0x1c),
		LineHL:      tcell.NewRGBColor(0x16, 0x1a, 0x24),
		Text:        tcell.NewRGBColor(0xbf, 0xbd, 0xb6),
		Muted:       tcell.NewRGBColor(0x5a, 0x63, 0x78),
		Subtle:      tcell.NewRGBColor(0x5a, 0x66, 0x73),
		Accent:      tcell.NewRGBColor(0xe6, 0xb4, 0x50),
		AccentSoft:  tcell.NewRGBColor(0xd8, 0xb7, 0x74),
		Selection:   tcell.NewRGBColor(0x1e, 0x23, 0x30),
		Modified:    tcell.NewRGBColor(0xe6, 0xb4, 0x50),
		Error:       tcell.NewRGBColor(0xd9, 0x57, 0x57),
		GitModified: tcell.NewRGBColor(0x73, 0xb8, 0xff),
		GitAdded:    tcell.NewRGBColor(0x7f, 0xd9, 0x62),
		GitDeleted:  tcell.NewRGBColor(0xf2, 0x6d, 0x78),
		GitRenamed:  tcell.NewRGBColor(0x59, 0xc2, 0xff),
		GitMixed:    tcell.NewRGBColor(0xe6, 0xb4, 0x50),
		FindMatch:   tcell.NewRGBColor(0x5b, 0x4c, 0x2e),
		FindCurrent: tcell.NewRGBColor(0xe6, 0xb4, 0x50),
		FolderColor: tcell.NewRGBColor(0xbf, 0xbd, 0xb6),
		FileColor:   tcell.NewRGBColor(0x9c, 0x9e, 0xa0),
		SynKeyword:  tcell.NewRGBColor(0xff, 0x8f, 0x40),
		SynString:   tcell.NewRGBColor(0xaa, 0xd9, 0x4c),
		SynNumber:   tcell.NewRGBColor(0xd2, 0xa6, 0xff),
		SynComment:  tcell.NewRGBColor(0x5a, 0x66, 0x73),
		SynFunction: tcell.NewRGBColor(0xff, 0xb4, 0x54),
		SynType:     tcell.NewRGBColor(0x59, 0xc2, 0xff),
		SynBuiltin:  tcell.NewRGBColor(0x39, 0xba, 0xe6),
		SynVariable: tcell.NewRGBColor(0xbf, 0xbd, 0xb6),
		SynOperator: tcell.NewRGBColor(0xf2, 0x96, 0x68),
		SynPunct:    tcell.NewRGBColor(0xbf, 0xbd, 0xb6),
		SynConstant: tcell.NewRGBColor(0xd2, 0xa6, 0xff),
	}
}

// themeAyuLight is the Ayu Light palette, ported from druk.
func themeAyuLight() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xfc, 0xfc, 0xfc),
		SidebarBG:   tcell.NewRGBColor(0xf8, 0xf9, 0xfa),
		StatusBG:    tcell.NewRGBColor(0xff, 0xaa, 0x33),
		StatusFg:    tcell.NewRGBColor(0xfc, 0xfc, 0xfc),
		LineHL:      tcell.NewRGBColor(0xf0, 0xf1, 0xf2),
		Text:        tcell.NewRGBColor(0x5c, 0x61, 0x66),
		Muted:       tcell.NewRGBColor(0x82, 0x8e, 0x9f),
		Subtle:      tcell.NewRGBColor(0xad, 0xae, 0xb1),
		Accent:      tcell.NewRGBColor(0xff, 0xaa, 0x33),
		AccentSoft:  tcell.NewRGBColor(0xc6, 0x90, 0x45),
		Selection:   tcell.NewRGBColor(0xe8, 0xec, 0xf0),
		Modified:    tcell.NewRGBColor(0xff, 0xaa, 0x33),
		Error:       tcell.NewRGBColor(0xe6, 0x50, 0x50),
		GitModified: tcell.NewRGBColor(0x47, 0x8a, 0xcc),
		GitAdded:    tcell.NewRGBColor(0x6c, 0xbf, 0x43),
		GitDeleted:  tcell.NewRGBColor(0xff, 0x73, 0x83),
		GitRenamed:  tcell.NewRGBColor(0x22, 0xa4, 0xe6),
		GitMixed:    tcell.NewRGBColor(0xff, 0xaa, 0x33),
		FindMatch:   tcell.NewRGBColor(0xfd, 0xdf, 0xb6),
		FindCurrent: tcell.NewRGBColor(0xff, 0xaa, 0x33),
		FolderColor: tcell.NewRGBColor(0x5c, 0x61, 0x66),
		FileColor:   tcell.NewRGBColor(0x69, 0x71, 0x7a),
		SynKeyword:  tcell.NewRGBColor(0xfa, 0x8d, 0x3e),
		SynString:   tcell.NewRGBColor(0x86, 0xb3, 0x00),
		SynNumber:   tcell.NewRGBColor(0xa3, 0x7a, 0xcc),
		SynComment:  tcell.NewRGBColor(0xad, 0xae, 0xb1),
		SynFunction: tcell.NewRGBColor(0xf2, 0xae, 0x49),
		SynType:     tcell.NewRGBColor(0x22, 0xa4, 0xe6),
		SynBuiltin:  tcell.NewRGBColor(0x55, 0xb4, 0xd4),
		SynVariable: tcell.NewRGBColor(0x5c, 0x61, 0x66),
		SynOperator: tcell.NewRGBColor(0xed, 0x93, 0x66),
		SynPunct:    tcell.NewRGBColor(0x5c, 0x61, 0x66),
		SynConstant: tcell.NewRGBColor(0xa3, 0x7a, 0xcc),
	}
}

// themeAyuMirage is the Ayu Mirage palette, ported from druk.
func themeAyuMirage() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x24, 0x29, 0x36),
		SidebarBG:   tcell.NewRGBColor(0x1f, 0x24, 0x30),
		StatusBG:    tcell.NewRGBColor(0xff, 0xcc, 0x66),
		StatusFg:    tcell.NewRGBColor(0x24, 0x29, 0x36),
		LineHL:      tcell.NewRGBColor(0x1a, 0x1f, 0x29),
		Text:        tcell.NewRGBColor(0xcc, 0xca, 0xc2),
		Muted:       tcell.NewRGBColor(0x70, 0x7a, 0x8c),
		Subtle:      tcell.NewRGBColor(0x6e, 0x7c, 0x8f),
		Accent:      tcell.NewRGBColor(0xff, 0xcc, 0x66),
		AccentSoft:  tcell.NewRGBColor(0xed, 0xcb, 0x86),
		Selection:   tcell.NewRGBColor(0x2d, 0x34, 0x44),
		Modified:    tcell.NewRGBColor(0xff, 0xcc, 0x66),
		Error:       tcell.NewRGBColor(0xff, 0x66, 0x66),
		GitModified: tcell.NewRGBColor(0x80, 0xbf, 0xff),
		GitAdded:    tcell.NewRGBColor(0x87, 0xd9, 0x6c),
		GitDeleted:  tcell.NewRGBColor(0xf2, 0x79, 0x83),
		GitRenamed:  tcell.NewRGBColor(0x73, 0xd0, 0xff),
		GitMixed:    tcell.NewRGBColor(0xff, 0xcc, 0x66),
		FindMatch:   tcell.NewRGBColor(0x71, 0x62, 0x47),
		FindCurrent: tcell.NewRGBColor(0xff, 0xcc, 0x66),
		FolderColor: tcell.NewRGBColor(0xcc, 0xca, 0xc2),
		FileColor:   tcell.NewRGBColor(0xac, 0xae, 0xaf),
		SynKeyword:  tcell.NewRGBColor(0xff, 0xa6, 0x59),
		SynString:   tcell.NewRGBColor(0xd5, 0xff, 0x80),
		SynNumber:   tcell.NewRGBColor(0xdf, 0xbf, 0xff),
		SynComment:  tcell.NewRGBColor(0x6e, 0x7c, 0x8f),
		SynFunction: tcell.NewRGBColor(0xff, 0xd1, 0x73),
		SynType:     tcell.NewRGBColor(0x73, 0xd0, 0xff),
		SynBuiltin:  tcell.NewRGBColor(0x5c, 0xcf, 0xe6),
		SynVariable: tcell.NewRGBColor(0xcc, 0xca, 0xc2),
		SynOperator: tcell.NewRGBColor(0xf2, 0x9e, 0x74),
		SynPunct:    tcell.NewRGBColor(0xcc, 0xca, 0xc2),
		SynConstant: tcell.NewRGBColor(0xdf, 0xbf, 0xff),
	}
}

// themeCatppuccinFrappe is the Catppuccin Frappé palette, ported from druk.
func themeCatppuccinFrappe() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x30, 0x34, 0x46),
		SidebarBG:   tcell.NewRGBColor(0x29, 0x2c, 0x3c),
		StatusBG:    tcell.NewRGBColor(0x8c, 0xaa, 0xee),
		StatusFg:    tcell.NewRGBColor(0x23, 0x26, 0x34),
		LineHL:      tcell.NewRGBColor(0x29, 0x2c, 0x3c),
		Text:        tcell.NewRGBColor(0xc6, 0xd0, 0xf5),
		Muted:       tcell.NewRGBColor(0xa5, 0xad, 0xce),
		Subtle:      tcell.NewRGBColor(0x73, 0x79, 0x94),
		Accent:      tcell.NewRGBColor(0x8c, 0xaa, 0xee),
		AccentSoft:  tcell.NewRGBColor(0xa0, 0xb7, 0xf0),
		Selection:   tcell.NewRGBColor(0x41, 0x45, 0x59),
		Modified:    tcell.NewRGBColor(0xe5, 0xc8, 0x90),
		Error:       tcell.NewRGBColor(0xe7, 0x82, 0x84),
		GitModified: tcell.NewRGBColor(0xe5, 0xc8, 0x90),
		GitAdded:    tcell.NewRGBColor(0xa6, 0xd1, 0x89),
		GitDeleted:  tcell.NewRGBColor(0xe7, 0x82, 0x84),
		GitRenamed:  tcell.NewRGBColor(0xe5, 0xc8, 0x90),
		GitMixed:    tcell.NewRGBColor(0x8c, 0xaa, 0xee),
		FindMatch:   tcell.NewRGBColor(0x6f, 0x68, 0x60),
		FindCurrent: tcell.NewRGBColor(0xe5, 0xc8, 0x90),
		FolderColor: tcell.NewRGBColor(0xc6, 0xd0, 0xf5),
		FileColor:   tcell.NewRGBColor(0xba, 0xc4, 0xe7),
		SynKeyword:  tcell.NewRGBColor(0xca, 0x9e, 0xe6),
		SynString:   tcell.NewRGBColor(0xa6, 0xd1, 0x89),
		SynNumber:   tcell.NewRGBColor(0xef, 0x9f, 0x76),
		SynComment:  tcell.NewRGBColor(0x73, 0x79, 0x94),
		SynFunction: tcell.NewRGBColor(0x8c, 0xaa, 0xee),
		SynType:     tcell.NewRGBColor(0xe5, 0xc8, 0x90),
		SynBuiltin:  tcell.NewRGBColor(0xe7, 0x82, 0x84),
		SynVariable: tcell.NewRGBColor(0xc6, 0xd0, 0xf5),
		SynOperator: tcell.NewRGBColor(0x99, 0xd1, 0xdb),
		SynPunct:    tcell.NewRGBColor(0x94, 0x9c, 0xbb),
		SynConstant: tcell.NewRGBColor(0xef, 0x9f, 0x76),
	}
}

// themeCatppuccinLatte is the Catppuccin Latte palette, ported from druk.
func themeCatppuccinLatte() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xef, 0xf1, 0xf5),
		SidebarBG:   tcell.NewRGBColor(0xe6, 0xe9, 0xef),
		StatusBG:    tcell.NewRGBColor(0x1e, 0x66, 0xf5),
		StatusFg:    tcell.NewRGBColor(0xef, 0xf1, 0xf5),
		LineHL:      tcell.NewRGBColor(0xe6, 0xe9, 0xef),
		Text:        tcell.NewRGBColor(0x4c, 0x4f, 0x69),
		Muted:       tcell.NewRGBColor(0x6c, 0x6f, 0x85),
		Subtle:      tcell.NewRGBColor(0x9c, 0xa0, 0xb0),
		Accent:      tcell.NewRGBColor(0x1e, 0x66, 0xf5),
		AccentSoft:  tcell.NewRGBColor(0x2e, 0x5e, 0xc4),
		Selection:   tcell.NewRGBColor(0xcc, 0xd0, 0xda),
		Modified:    tcell.NewRGBColor(0xdf, 0x8e, 0x1d),
		Error:       tcell.NewRGBColor(0xd2, 0x0f, 0x39),
		GitModified: tcell.NewRGBColor(0xdf, 0x8e, 0x1d),
		GitAdded:    tcell.NewRGBColor(0x40, 0xa0, 0x2b),
		GitDeleted:  tcell.NewRGBColor(0xd2, 0x0f, 0x39),
		GitRenamed:  tcell.NewRGBColor(0xdf, 0x8e, 0x1d),
		GitMixed:    tcell.NewRGBColor(0x1e, 0x66, 0xf5),
		FindMatch:   tcell.NewRGBColor(0xe9, 0xce, 0xa9),
		FindCurrent: tcell.NewRGBColor(0xdf, 0x8e, 0x1d),
		FolderColor: tcell.NewRGBColor(0x4c, 0x4f, 0x69),
		FileColor:   tcell.NewRGBColor(0x57, 0x5a, 0x73),
		SynKeyword:  tcell.NewRGBColor(0x88, 0x39, 0xef),
		SynString:   tcell.NewRGBColor(0x40, 0xa0, 0x2b),
		SynNumber:   tcell.NewRGBColor(0xfe, 0x64, 0x0b),
		SynComment:  tcell.NewRGBColor(0x9c, 0xa0, 0xb0),
		SynFunction: tcell.NewRGBColor(0x1e, 0x66, 0xf5),
		SynType:     tcell.NewRGBColor(0xdf, 0x8e, 0x1d),
		SynBuiltin:  tcell.NewRGBColor(0xd2, 0x0f, 0x39),
		SynVariable: tcell.NewRGBColor(0x4c, 0x4f, 0x69),
		SynOperator: tcell.NewRGBColor(0x04, 0xa5, 0xe5),
		SynPunct:    tcell.NewRGBColor(0x7c, 0x7f, 0x93),
		SynConstant: tcell.NewRGBColor(0xfe, 0x64, 0x0b),
	}
}

// themeCatppuccinMacchiato is the Catppuccin Macchiato palette, ported from druk.
func themeCatppuccinMacchiato() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x24, 0x27, 0x3a),
		SidebarBG:   tcell.NewRGBColor(0x1e, 0x20, 0x30),
		StatusBG:    tcell.NewRGBColor(0x8a, 0xad, 0xf4),
		StatusFg:    tcell.NewRGBColor(0x18, 0x19, 0x26),
		LineHL:      tcell.NewRGBColor(0x1e, 0x20, 0x30),
		Text:        tcell.NewRGBColor(0xca, 0xd3, 0xf5),
		Muted:       tcell.NewRGBColor(0xa5, 0xad, 0xcb),
		Subtle:      tcell.NewRGBColor(0x6e, 0x73, 0x8d),
		Accent:      tcell.NewRGBColor(0x8a, 0xad, 0xf4),
		AccentSoft:  tcell.NewRGBColor(0xa0, 0xba, 0xf4),
		Selection:   tcell.NewRGBColor(0x36, 0x3a, 0x4f),
		Modified:    tcell.NewRGBColor(0xee, 0xd4, 0x9f),
		Error:       tcell.NewRGBColor(0xed, 0x87, 0x96),
		GitModified: tcell.NewRGBColor(0xee, 0xd4, 0x9f),
		GitAdded:    tcell.NewRGBColor(0xa6, 0xda, 0x95),
		GitDeleted:  tcell.NewRGBColor(0xed, 0x87, 0x96),
		GitRenamed:  tcell.NewRGBColor(0xee, 0xd4, 0x9f),
		GitMixed:    tcell.NewRGBColor(0x8a, 0xad, 0xf4),
		FindMatch:   tcell.NewRGBColor(0x6b, 0x64, 0x5d),
		FindCurrent: tcell.NewRGBColor(0xee, 0xd4, 0x9f),
		FolderColor: tcell.NewRGBColor(0xca, 0xd3, 0xf5),
		FileColor:   tcell.NewRGBColor(0xbd, 0xc6, 0xe6),
		SynKeyword:  tcell.NewRGBColor(0xc6, 0xa0, 0xf6),
		SynString:   tcell.NewRGBColor(0xa6, 0xda, 0x95),
		SynNumber:   tcell.NewRGBColor(0xf5, 0xa9, 0x7f),
		SynComment:  tcell.NewRGBColor(0x6e, 0x73, 0x8d),
		SynFunction: tcell.NewRGBColor(0x8a, 0xad, 0xf4),
		SynType:     tcell.NewRGBColor(0xee, 0xd4, 0x9f),
		SynBuiltin:  tcell.NewRGBColor(0xed, 0x87, 0x96),
		SynVariable: tcell.NewRGBColor(0xca, 0xd3, 0xf5),
		SynOperator: tcell.NewRGBColor(0x91, 0xd7, 0xe3),
		SynPunct:    tcell.NewRGBColor(0x93, 0x9a, 0xb7),
		SynConstant: tcell.NewRGBColor(0xf5, 0xa9, 0x7f),
	}
}

// themeCatppuccinMocha is the Catppuccin Mocha palette, ported from druk.
func themeCatppuccinMocha() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x1e, 0x1e, 0x2e),
		SidebarBG:   tcell.NewRGBColor(0x18, 0x18, 0x25),
		StatusBG:    tcell.NewRGBColor(0x89, 0xb4, 0xfa),
		StatusFg:    tcell.NewRGBColor(0x11, 0x11, 0x1b),
		LineHL:      tcell.NewRGBColor(0x18, 0x18, 0x25),
		Text:        tcell.NewRGBColor(0xcd, 0xd6, 0xf4),
		Muted:       tcell.NewRGBColor(0xa6, 0xad, 0xc8),
		Subtle:      tcell.NewRGBColor(0x6c, 0x70, 0x86),
		Accent:      tcell.NewRGBColor(0x89, 0xb4, 0xfa),
		AccentSoft:  tcell.NewRGBColor(0xa1, 0xc0, 0xf8),
		Selection:   tcell.NewRGBColor(0x31, 0x32, 0x44),
		Modified:    tcell.NewRGBColor(0xf9, 0xe2, 0xaf),
		Error:       tcell.NewRGBColor(0xf3, 0x8b, 0xa8),
		GitModified: tcell.NewRGBColor(0xf9, 0xe2, 0xaf),
		GitAdded:    tcell.NewRGBColor(0xa6, 0xe3, 0xa1),
		GitDeleted:  tcell.NewRGBColor(0xf3, 0x8b, 0xa8),
		GitRenamed:  tcell.NewRGBColor(0xf9, 0xe2, 0xaf),
		GitMixed:    tcell.NewRGBColor(0x89, 0xb4, 0xfa),
		FindMatch:   tcell.NewRGBColor(0x6b, 0x63, 0x5b),
		FindCurrent: tcell.NewRGBColor(0xf9, 0xe2, 0xaf),
		FolderColor: tcell.NewRGBColor(0xcd, 0xd6, 0xf4),
		FileColor:   tcell.NewRGBColor(0xbf, 0xc8, 0xe5),
		SynKeyword:  tcell.NewRGBColor(0xcb, 0xa6, 0xf7),
		SynString:   tcell.NewRGBColor(0xa6, 0xe3, 0xa1),
		SynNumber:   tcell.NewRGBColor(0xfa, 0xb3, 0x87),
		SynComment:  tcell.NewRGBColor(0x6c, 0x70, 0x86),
		SynFunction: tcell.NewRGBColor(0x89, 0xb4, 0xfa),
		SynType:     tcell.NewRGBColor(0xf9, 0xe2, 0xaf),
		SynBuiltin:  tcell.NewRGBColor(0xf3, 0x8b, 0xa8),
		SynVariable: tcell.NewRGBColor(0xcd, 0xd6, 0xf4),
		SynOperator: tcell.NewRGBColor(0x89, 0xdc, 0xeb),
		SynPunct:    tcell.NewRGBColor(0x93, 0x99, 0xb2),
		SynConstant: tcell.NewRGBColor(0xfa, 0xb3, 0x87),
	}
}

// themeDracula is the Dracula palette, ported from druk.
func themeDracula() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x28, 0x2a, 0x36),
		SidebarBG:   tcell.NewRGBColor(0x21, 0x22, 0x2c),
		StatusBG:    tcell.NewRGBColor(0xbd, 0x93, 0xf9),
		StatusFg:    tcell.NewRGBColor(0x28, 0x2a, 0x36),
		LineHL:      tcell.NewRGBColor(0x21, 0x22, 0x2c),
		Text:        tcell.NewRGBColor(0xf8, 0xf8, 0xf2),
		Muted:       tcell.NewRGBColor(0x62, 0x72, 0xa4),
		Subtle:      tcell.NewRGBColor(0x44, 0x47, 0x5a),
		Accent:      tcell.NewRGBColor(0xbd, 0x93, 0xf9),
		AccentSoft:  tcell.NewRGBColor(0xd2, 0xb6, 0xf7),
		Selection:   tcell.NewRGBColor(0x44, 0x47, 0x5a),
		Modified:    tcell.NewRGBColor(0xf1, 0xfa, 0x8c),
		Error:       tcell.NewRGBColor(0xff, 0x55, 0x55),
		GitModified: tcell.NewRGBColor(0xf1, 0xfa, 0x8c),
		GitAdded:    tcell.NewRGBColor(0x50, 0xfa, 0x7b),
		GitDeleted:  tcell.NewRGBColor(0xff, 0x55, 0x55),
		GitRenamed:  tcell.NewRGBColor(0x8b, 0xe9, 0xfd),
		GitMixed:    tcell.NewRGBColor(0xbd, 0x93, 0xf9),
		FindMatch:   tcell.NewRGBColor(0x6e, 0x73, 0x54),
		FindCurrent: tcell.NewRGBColor(0xf1, 0xfa, 0x8c),
		FolderColor: tcell.NewRGBColor(0xf8, 0xf8, 0xf2),
		FileColor:   tcell.NewRGBColor(0xc4, 0xc9, 0xd7),
		SynKeyword:  tcell.NewRGBColor(0xff, 0x79, 0xc6),
		SynString:   tcell.NewRGBColor(0xf1, 0xfa, 0x8c),
		SynNumber:   tcell.NewRGBColor(0xbd, 0x93, 0xf9),
		SynComment:  tcell.NewRGBColor(0x62, 0x72, 0xa4),
		SynFunction: tcell.NewRGBColor(0x50, 0xfa, 0x7b),
		SynType:     tcell.NewRGBColor(0x8b, 0xe9, 0xfd),
		SynBuiltin:  tcell.NewRGBColor(0xff, 0x79, 0xc6),
		SynVariable: tcell.NewRGBColor(0xf8, 0xf8, 0xf2),
		SynOperator: tcell.NewRGBColor(0xff, 0x79, 0xc6),
		SynPunct:    tcell.NewRGBColor(0xf8, 0xf8, 0xf2),
		SynConstant: tcell.NewRGBColor(0xbd, 0x93, 0xf9),
	}
}

// themeEverforestDark is the Everforest Dark palette, ported from druk.
func themeEverforestDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x2d, 0x35, 0x3b),
		SidebarBG:   tcell.NewRGBColor(0x23, 0x2a, 0x2e),
		StatusBG:    tcell.NewRGBColor(0xa7, 0xc0, 0x80),
		StatusFg:    tcell.NewRGBColor(0x2d, 0x35, 0x3b),
		LineHL:      tcell.NewRGBColor(0x34, 0x3f, 0x44),
		Text:        tcell.NewRGBColor(0xd3, 0xc6, 0xaa),
		Muted:       tcell.NewRGBColor(0x9d, 0xa9, 0xa0),
		Subtle:      tcell.NewRGBColor(0x7a, 0x84, 0x78),
		Accent:      tcell.NewRGBColor(0xa7, 0xc0, 0x80),
		AccentSoft:  tcell.NewRGBColor(0xb6, 0xc2, 0x8f),
		Selection:   tcell.NewRGBColor(0x47, 0x52, 0x58),
		Modified:    tcell.NewRGBColor(0xdb, 0xbc, 0x7f),
		Error:       tcell.NewRGBColor(0xe6, 0x7e, 0x80),
		GitModified: tcell.NewRGBColor(0x7f, 0xbb, 0xb3),
		GitAdded:    tcell.NewRGBColor(0xa7, 0xc0, 0x80),
		GitDeleted:  tcell.NewRGBColor(0xe6, 0x7e, 0x80),
		GitRenamed:  tcell.NewRGBColor(0xdb, 0xbc, 0x7f),
		GitMixed:    tcell.NewRGBColor(0xa7, 0xc0, 0x80),
		FindMatch:   tcell.NewRGBColor(0x6a, 0x64, 0x53),
		FindCurrent: tcell.NewRGBColor(0xdb, 0xbc, 0x7f),
		FolderColor: tcell.NewRGBColor(0xd3, 0xc6, 0xaa),
		FileColor:   tcell.NewRGBColor(0xc0, 0xbc, 0xa6),
		SynKeyword:  tcell.NewRGBColor(0xe6, 0x7e, 0x80),
		SynString:   tcell.NewRGBColor(0x83, 0xc0, 0x92),
		SynNumber:   tcell.NewRGBColor(0xd6, 0x99, 0xb6),
		SynComment:  tcell.NewRGBColor(0x85, 0x92, 0x89),
		SynFunction: tcell.NewRGBColor(0xa7, 0xc0, 0x80),
		SynType:     tcell.NewRGBColor(0xdb, 0xbc, 0x7f),
		SynBuiltin:  tcell.NewRGBColor(0xe6, 0x98, 0x75),
		SynVariable: tcell.NewRGBColor(0xd3, 0xc6, 0xaa),
		SynOperator: tcell.NewRGBColor(0xe6, 0x98, 0x75),
		SynPunct:    tcell.NewRGBColor(0x85, 0x92, 0x89),
		SynConstant: tcell.NewRGBColor(0xd3, 0xc6, 0xaa),
	}
}

// themeEverforestLight is the Everforest Light palette, ported from druk.
func themeEverforestLight() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xfd, 0xf6, 0xe3),
		SidebarBG:   tcell.NewRGBColor(0xef, 0xeb, 0xd4),
		StatusBG:    tcell.NewRGBColor(0x93, 0xb2, 0x59),
		StatusFg:    tcell.NewRGBColor(0xfd, 0xf6, 0xe3),
		LineHL:      tcell.NewRGBColor(0xf4, 0xf0, 0xd9),
		Text:        tcell.NewRGBColor(0x5c, 0x6a, 0x72),
		Muted:       tcell.NewRGBColor(0x82, 0x91, 0x81),
		Subtle:      tcell.NewRGBColor(0xa6, 0xb0, 0xa0),
		Accent:      tcell.NewRGBColor(0x8d, 0xa1, 0x01),
		AccentSoft:  tcell.NewRGBColor(0x7c, 0x8e, 0x29),
		Selection:   tcell.NewRGBColor(0xe6, 0xe2, 0xcc),
		Modified:    tcell.NewRGBColor(0xdf, 0xa0, 0x00),
		Error:       tcell.NewRGBColor(0xf8, 0x55, 0x52),
		GitModified: tcell.NewRGBColor(0x3a, 0x94, 0xc5),
		GitAdded:    tcell.NewRGBColor(0x8d, 0xa1, 0x01),
		GitDeleted:  tcell.NewRGBColor(0xf8, 0x55, 0x52),
		GitRenamed:  tcell.NewRGBColor(0xdf, 0xa0, 0x00),
		GitMixed:    tcell.NewRGBColor(0x8d, 0xa1, 0x01),
		FindMatch:   tcell.NewRGBColor(0xf2, 0xd8, 0x94),
		FindCurrent: tcell.NewRGBColor(0xdf, 0xa0, 0x00),
		FolderColor: tcell.NewRGBColor(0x5c, 0x6a, 0x72),
		FileColor:   tcell.NewRGBColor(0x69, 0x78, 0x77),
		SynKeyword:  tcell.NewRGBColor(0xf8, 0x55, 0x52),
		SynString:   tcell.NewRGBColor(0x35, 0xa7, 0x7c),
		SynNumber:   tcell.NewRGBColor(0xdf, 0x69, 0xba),
		SynComment:  tcell.NewRGBColor(0x93, 0x9f, 0x91),
		SynFunction: tcell.NewRGBColor(0x8d, 0xa1, 0x01),
		SynType:     tcell.NewRGBColor(0xdf, 0xa0, 0x00),
		SynBuiltin:  tcell.NewRGBColor(0xf5, 0x7d, 0x26),
		SynVariable: tcell.NewRGBColor(0x5c, 0x6a, 0x72),
		SynOperator: tcell.NewRGBColor(0xf5, 0x7d, 0x26),
		SynPunct:    tcell.NewRGBColor(0x93, 0x9f, 0x91),
		SynConstant: tcell.NewRGBColor(0x5c, 0x6a, 0x72),
	}
}

// themeGithubDark is the GitHub Dark palette, ported from druk.
func themeGithubDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x0d, 0x11, 0x17),
		SidebarBG:   tcell.NewRGBColor(0x16, 0x1b, 0x22),
		StatusBG:    tcell.NewRGBColor(0x1f, 0x6f, 0xeb),
		StatusFg:    tcell.NewRGBColor(0xff, 0xff, 0xff),
		LineHL:      tcell.NewRGBColor(0x16, 0x1b, 0x22),
		Text:        tcell.NewRGBColor(0xe6, 0xed, 0xf3),
		Muted:       tcell.NewRGBColor(0x8b, 0x94, 0x9e),
		Subtle:      tcell.NewRGBColor(0x6e, 0x76, 0x81),
		Accent:      tcell.NewRGBColor(0x58, 0xa6, 0xff),
		AccentSoft:  tcell.NewRGBColor(0x8a, 0xbf, 0xfb),
		Selection:   tcell.NewRGBColor(0x19, 0x33, 0x56),
		Modified:    tcell.NewRGBColor(0xd2, 0x99, 0x22),
		Error:       tcell.NewRGBColor(0xf8, 0x51, 0x49),
		GitModified: tcell.NewRGBColor(0xd2, 0x99, 0x22),
		GitAdded:    tcell.NewRGBColor(0x3f, 0xb9, 0x50),
		GitDeleted:  tcell.NewRGBColor(0xf8, 0x51, 0x49),
		GitRenamed:  tcell.NewRGBColor(0xff, 0x7b, 0x72),
		GitMixed:    tcell.NewRGBColor(0x58, 0xa6, 0xff),
		FindMatch:   tcell.NewRGBColor(0x52, 0x41, 0x1b),
		FindCurrent: tcell.NewRGBColor(0xd2, 0x99, 0x22),
		FolderColor: tcell.NewRGBColor(0xe6, 0xed, 0xf3),
		FileColor:   tcell.NewRGBColor(0xc6, 0xce, 0xd5),
		SynKeyword:  tcell.NewRGBColor(0xff, 0x7b, 0x72),
		SynString:   tcell.NewRGBColor(0xa5, 0xd6, 0xff),
		SynNumber:   tcell.NewRGBColor(0x79, 0xc0, 0xff),
		SynComment:  tcell.NewRGBColor(0x8b, 0x94, 0x9e),
		SynFunction: tcell.NewRGBColor(0xd2, 0xa8, 0xff),
		SynType:     tcell.NewRGBColor(0xff, 0x7b, 0x72),
		SynBuiltin:  tcell.NewRGBColor(0x7e, 0xe7, 0x87),
		SynVariable: tcell.NewRGBColor(0xff, 0xa6, 0x57),
		SynOperator: tcell.NewRGBColor(0xff, 0x7b, 0x72),
		SynPunct:    tcell.NewRGBColor(0x8b, 0x94, 0x9e),
		SynConstant: tcell.NewRGBColor(0x79, 0xc0, 0xff),
	}
}

// themeGithubLight is the GitHub Light palette, ported from druk.
func themeGithubLight() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xff, 0xff, 0xff),
		SidebarBG:   tcell.NewRGBColor(0xf6, 0xf8, 0xfa),
		StatusBG:    tcell.NewRGBColor(0x09, 0x69, 0xda),
		StatusFg:    tcell.NewRGBColor(0xff, 0xff, 0xff),
		LineHL:      tcell.NewRGBColor(0xf6, 0xf8, 0xfa),
		Text:        tcell.NewRGBColor(0x1f, 0x23, 0x28),
		Muted:       tcell.NewRGBColor(0x65, 0x6d, 0x76),
		Subtle:      tcell.NewRGBColor(0x8c, 0x95, 0x9f),
		Accent:      tcell.NewRGBColor(0x09, 0x69, 0xda),
		AccentSoft:  tcell.NewRGBColor(0x11, 0x50, 0x9c),
		Selection:   tcell.NewRGBColor(0xdd, 0xf4, 0xff),
		Modified:    tcell.NewRGBColor(0x9a, 0x67, 0x00),
		Error:       tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		GitModified: tcell.NewRGBColor(0x9a, 0x67, 0x00),
		GitAdded:    tcell.NewRGBColor(0x1a, 0x7f, 0x37),
		GitDeleted:  tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		GitRenamed:  tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		GitMixed:    tcell.NewRGBColor(0x09, 0x69, 0xda),
		FindMatch:   tcell.NewRGBColor(0xdc, 0xca, 0xa6),
		FindCurrent: tcell.NewRGBColor(0x9a, 0x67, 0x00),
		FolderColor: tcell.NewRGBColor(0x1f, 0x23, 0x28),
		FileColor:   tcell.NewRGBColor(0x38, 0x3d, 0x43),
		SynKeyword:  tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		SynString:   tcell.NewRGBColor(0x0a, 0x30, 0x69),
		SynNumber:   tcell.NewRGBColor(0x05, 0x50, 0xae),
		SynComment:  tcell.NewRGBColor(0x6e, 0x77, 0x81),
		SynFunction: tcell.NewRGBColor(0x82, 0x50, 0xdf),
		SynType:     tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		SynBuiltin:  tcell.NewRGBColor(0x11, 0x63, 0x29),
		SynVariable: tcell.NewRGBColor(0x95, 0x38, 0x00),
		SynOperator: tcell.NewRGBColor(0xcf, 0x22, 0x2e),
		SynPunct:    tcell.NewRGBColor(0x6e, 0x77, 0x81),
		SynConstant: tcell.NewRGBColor(0x05, 0x50, 0xae),
	}
}

// themeGruvboxDark is the Gruvbox Dark palette, ported from druk.
func themeGruvboxDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x28, 0x28, 0x28),
		SidebarBG:   tcell.NewRGBColor(0x32, 0x30, 0x2f),
		StatusBG:    tcell.NewRGBColor(0x45, 0x85, 0x88),
		StatusFg:    tcell.NewRGBColor(0xfb, 0xf1, 0xc7),
		LineHL:      tcell.NewRGBColor(0x32, 0x30, 0x2f),
		Text:        tcell.NewRGBColor(0xeb, 0xdb, 0xb2),
		Muted:       tcell.NewRGBColor(0xa8, 0x99, 0x84),
		Subtle:      tcell.NewRGBColor(0x7c, 0x6f, 0x64),
		Accent:      tcell.NewRGBColor(0x83, 0xa5, 0x98),
		AccentSoft:  tcell.NewRGBColor(0xa7, 0xb8, 0xa1),
		Selection:   tcell.NewRGBColor(0x50, 0x49, 0x45),
		Modified:    tcell.NewRGBColor(0xfa, 0xbd, 0x2f),
		Error:       tcell.NewRGBColor(0xfb, 0x49, 0x34),
		GitModified: tcell.NewRGBColor(0xfa, 0xbd, 0x2f),
		GitAdded:    tcell.NewRGBColor(0xb8, 0xbb, 0x26),
		GitDeleted:  tcell.NewRGBColor(0xfb, 0x49, 0x34),
		GitRenamed:  tcell.NewRGBColor(0xfa, 0xbd, 0x2f),
		GitMixed:    tcell.NewRGBColor(0x83, 0xa5, 0x98),
		FindMatch:   tcell.NewRGBColor(0x72, 0x5c, 0x2a),
		FindCurrent: tcell.NewRGBColor(0xfa, 0xbd, 0x2f),
		FolderColor: tcell.NewRGBColor(0xeb, 0xdb, 0xb2),
		FileColor:   tcell.NewRGBColor(0xd4, 0xc4, 0xa2),
		SynKeyword:  tcell.NewRGBColor(0xfb, 0x49, 0x34),
		SynString:   tcell.NewRGBColor(0xb8, 0xbb, 0x26),
		SynNumber:   tcell.NewRGBColor(0xd3, 0x86, 0x9b),
		SynComment:  tcell.NewRGBColor(0x92, 0x83, 0x74),
		SynFunction: tcell.NewRGBColor(0xb8, 0xbb, 0x26),
		SynType:     tcell.NewRGBColor(0xfa, 0xbd, 0x2f),
		SynBuiltin:  tcell.NewRGBColor(0x8e, 0xc0, 0x7c),
		SynVariable: tcell.NewRGBColor(0xeb, 0xdb, 0xb2),
		SynOperator: tcell.NewRGBColor(0x8e, 0xc0, 0x7c),
		SynPunct:    tcell.NewRGBColor(0xa8, 0x99, 0x84),
		SynConstant: tcell.NewRGBColor(0xd3, 0x86, 0x9b),
	}
}

// themeGruvboxLight is the Gruvbox Light palette, ported from druk.
func themeGruvboxLight() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xfb, 0xf1, 0xc7),
		SidebarBG:   tcell.NewRGBColor(0xf2, 0xe5, 0xbc),
		StatusBG:    tcell.NewRGBColor(0x07, 0x66, 0x78),
		StatusFg:    tcell.NewRGBColor(0xfb, 0xf1, 0xc7),
		LineHL:      tcell.NewRGBColor(0xf2, 0xe5, 0xbc),
		Text:        tcell.NewRGBColor(0x3c, 0x38, 0x36),
		Muted:       tcell.NewRGBColor(0x7c, 0x6f, 0x64),
		Subtle:      tcell.NewRGBColor(0xa8, 0x99, 0x84),
		Accent:      tcell.NewRGBColor(0x07, 0x66, 0x78),
		AccentSoft:  tcell.NewRGBColor(0x1a, 0x56, 0x61),
		Selection:   tcell.NewRGBColor(0xd5, 0xc4, 0xa1),
		Modified:    tcell.NewRGBColor(0xb5, 0x76, 0x14),
		Error:       tcell.NewRGBColor(0x9d, 0x00, 0x06),
		GitModified: tcell.NewRGBColor(0xb5, 0x76, 0x14),
		GitAdded:    tcell.NewRGBColor(0x79, 0x74, 0x0e),
		GitDeleted:  tcell.NewRGBColor(0x9d, 0x00, 0x06),
		GitRenamed:  tcell.NewRGBColor(0xb5, 0x76, 0x14),
		GitMixed:    tcell.NewRGBColor(0x07, 0x66, 0x78),
		FindMatch:   tcell.NewRGBColor(0xe2, 0xc6, 0x88),
		FindCurrent: tcell.NewRGBColor(0xb5, 0x76, 0x14),
		FolderColor: tcell.NewRGBColor(0x3c, 0x38, 0x36),
		FileColor:   tcell.NewRGBColor(0x52, 0x4b, 0x46),
		SynKeyword:  tcell.NewRGBColor(0x9d, 0x00, 0x06),
		SynString:   tcell.NewRGBColor(0x79, 0x74, 0x0e),
		SynNumber:   tcell.NewRGBColor(0x8f, 0x3f, 0x71),
		SynComment:  tcell.NewRGBColor(0x92, 0x83, 0x74),
		SynFunction: tcell.NewRGBColor(0x79, 0x74, 0x0e),
		SynType:     tcell.NewRGBColor(0xb5, 0x76, 0x14),
		SynBuiltin:  tcell.NewRGBColor(0x42, 0x7b, 0x58),
		SynVariable: tcell.NewRGBColor(0x3c, 0x38, 0x36),
		SynOperator: tcell.NewRGBColor(0x42, 0x7b, 0x58),
		SynPunct:    tcell.NewRGBColor(0x7c, 0x6f, 0x64),
		SynConstant: tcell.NewRGBColor(0x8f, 0x3f, 0x71),
	}
}

// themeKanagawaDragon is the Kanagawa Dragon palette, ported from druk.
func themeKanagawaDragon() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x18, 0x16, 0x16),
		SidebarBG:   tcell.NewRGBColor(0x0d, 0x0c, 0x0c),
		StatusBG:    tcell.NewRGBColor(0x8b, 0xa4, 0xb0),
		StatusFg:    tcell.NewRGBColor(0x18, 0x16, 0x16),
		LineHL:      tcell.NewRGBColor(0x22, 0x1f, 0x1f),
		Text:        tcell.NewRGBColor(0xc5, 0xc9, 0xc5),
		Muted:       tcell.NewRGBColor(0xc8, 0xc0, 0x93),
		Subtle:      tcell.NewRGBColor(0x73, 0x7c, 0x73),
		Accent:      tcell.NewRGBColor(0x8b, 0xa4, 0xb0),
		AccentSoft:  tcell.NewRGBColor(0x9f, 0xb1, 0xb7),
		Selection:   tcell.NewRGBColor(0x28, 0x27, 0x27),
		Modified:    tcell.NewRGBColor(0xff, 0x9e, 0x3b),
		Error:       tcell.NewRGBColor(0xe8, 0x24, 0x24),
		GitModified: tcell.NewRGBColor(0xdc, 0xa5, 0x61),
		GitAdded:    tcell.NewRGBColor(0x76, 0x94, 0x6a),
		GitDeleted:  tcell.NewRGBColor(0xc3, 0x40, 0x43),
		GitRenamed:  tcell.NewRGBColor(0x8e, 0xa4, 0xa2),
		GitMixed:    tcell.NewRGBColor(0x8b, 0xa4, 0xb0),
		FindMatch:   tcell.NewRGBColor(0x69, 0x46, 0x23),
		FindCurrent: tcell.NewRGBColor(0xff, 0x9e, 0x3b),
		FolderColor: tcell.NewRGBColor(0xc5, 0xc9, 0xc5),
		FileColor:   tcell.NewRGBColor(0xc6, 0xc6, 0xb4),
		SynKeyword:  tcell.NewRGBColor(0x89, 0x92, 0xa7),
		SynString:   tcell.NewRGBColor(0x8a, 0x9a, 0x7b),
		SynNumber:   tcell.NewRGBColor(0xa2, 0x92, 0xa3),
		SynComment:  tcell.NewRGBColor(0x73, 0x7c, 0x73),
		SynFunction: tcell.NewRGBColor(0x8b, 0xa4, 0xb0),
		SynType:     tcell.NewRGBColor(0x8e, 0xa4, 0xa2),
		SynBuiltin:  tcell.NewRGBColor(0xc4, 0x74, 0x6e),
		SynVariable: tcell.NewRGBColor(0xc5, 0xc9, 0xc5),
		SynOperator: tcell.NewRGBColor(0xc4, 0x74, 0x6e),
		SynPunct:    tcell.NewRGBColor(0x9e, 0x9b, 0x93),
		SynConstant: tcell.NewRGBColor(0xb6, 0x92, 0x7b),
	}
}

// themeKanagawaLotus is the Kanagawa Lotus palette, ported from druk.
func themeKanagawaLotus() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xf2, 0xec, 0xbc),
		SidebarBG:   tcell.NewRGBColor(0xd5, 0xce, 0xa3),
		StatusBG:    tcell.NewRGBColor(0x4d, 0x69, 0x9b),
		StatusFg:    tcell.NewRGBColor(0xf2, 0xec, 0xbc),
		LineHL:      tcell.NewRGBColor(0xec, 0xe6, 0xb6),
		Text:        tcell.NewRGBColor(0x54, 0x54, 0x64),
		Muted:       tcell.NewRGBColor(0x43, 0x43, 0x6c),
		Subtle:      tcell.NewRGBColor(0x8a, 0x89, 0x80),
		Accent:      tcell.NewRGBColor(0x4d, 0x69, 0x9b),
		AccentSoft:  tcell.NewRGBColor(0x4f, 0x62, 0x88),
		Selection:   tcell.NewRGBColor(0xe4, 0xd7, 0x94),
		Modified:    tcell.NewRGBColor(0xcc, 0x6d, 0x00),
		Error:       tcell.NewRGBColor(0xe8, 0x24, 0x24),
		GitModified: tcell.NewRGBColor(0xde, 0x98, 0x00),
		GitAdded:    tcell.NewRGBColor(0x6e, 0x91, 0x5f),
		GitDeleted:  tcell.NewRGBColor(0xd7, 0x47, 0x4b),
		GitRenamed:  tcell.NewRGBColor(0x59, 0x7b, 0x75),
		GitMixed:    tcell.NewRGBColor(0x4d, 0x69, 0x9b),
		FindMatch:   tcell.NewRGBColor(0xe5, 0xc0, 0x7a),
		FindCurrent: tcell.NewRGBColor(0xcc, 0x6d, 0x00),
		FolderColor: tcell.NewRGBColor(0x54, 0x54, 0x64),
		FileColor:   tcell.NewRGBColor(0x4e, 0x4e, 0x67),
		SynKeyword:  tcell.NewRGBColor(0x62, 0x4c, 0x83),
		SynString:   tcell.NewRGBColor(0x6f, 0x89, 0x4e),
		SynNumber:   tcell.NewRGBColor(0xb3, 0x5b, 0x79),
		SynComment:  tcell.NewRGBColor(0x8a, 0x89, 0x80),
		SynFunction: tcell.NewRGBColor(0x4d, 0x69, 0x9b),
		SynType:     tcell.NewRGBColor(0x59, 0x7b, 0x75),
		SynBuiltin:  tcell.NewRGBColor(0xc8, 0x40, 0x53),
		SynVariable: tcell.NewRGBColor(0x54, 0x54, 0x64),
		SynOperator: tcell.NewRGBColor(0x83, 0x6f, 0x4a),
		SynPunct:    tcell.NewRGBColor(0x4e, 0x8c, 0xa2),
		SynConstant: tcell.NewRGBColor(0xcc, 0x6d, 0x00),
	}
}

// themeKanagawaWave is the Kanagawa Wave palette, ported from druk.
func themeKanagawaWave() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x1f, 0x1f, 0x28),
		SidebarBG:   tcell.NewRGBColor(0x16, 0x16, 0x1d),
		StatusBG:    tcell.NewRGBColor(0x7e, 0x9c, 0xd8),
		StatusFg:    tcell.NewRGBColor(0x1f, 0x1f, 0x28),
		LineHL:      tcell.NewRGBColor(0x29, 0x29, 0x32),
		Text:        tcell.NewRGBColor(0xdc, 0xd7, 0xba),
		Muted:       tcell.NewRGBColor(0xc8, 0xc0, 0x93),
		Subtle:      tcell.NewRGBColor(0x72, 0x71, 0x69),
		Accent:      tcell.NewRGBColor(0x7e, 0x9c, 0xd8),
		AccentSoft:  tcell.NewRGBColor(0x9f, 0xb1, 0xce),
		Selection:   tcell.NewRGBColor(0x2a, 0x2a, 0x37),
		Modified:    tcell.NewRGBColor(0xff, 0x9e, 0x3b),
		Error:       tcell.NewRGBColor(0xe8, 0x24, 0x24),
		GitModified: tcell.NewRGBColor(0xdc, 0xa5, 0x61),
		GitAdded:    tcell.NewRGBColor(0x76, 0x94, 0x6a),
		GitDeleted:  tcell.NewRGBColor(0xc3, 0x40, 0x43),
		GitRenamed:  tcell.NewRGBColor(0x7a, 0xa8, 0x9f),
		GitMixed:    tcell.NewRGBColor(0x7e, 0x9c, 0xd8),
		FindMatch:   tcell.NewRGBColor(0x6d, 0x4b, 0x2f),
		FindCurrent: tcell.NewRGBColor(0xff, 0x9e, 0x3b),
		FolderColor: tcell.NewRGBColor(0xdc, 0xd7, 0xba),
		FileColor:   tcell.NewRGBColor(0xd5, 0xcf, 0xac),
		SynKeyword:  tcell.NewRGBColor(0x95, 0x7f, 0xb8),
		SynString:   tcell.NewRGBColor(0x98, 0xbb, 0x6c),
		SynNumber:   tcell.NewRGBColor(0xd2, 0x7e, 0x99),
		SynComment:  tcell.NewRGBColor(0x72, 0x71, 0x69),
		SynFunction: tcell.NewRGBColor(0x7e, 0x9c, 0xd8),
		SynType:     tcell.NewRGBColor(0x7a, 0xa8, 0x9f),
		SynBuiltin:  tcell.NewRGBColor(0xe4, 0x68, 0x76),
		SynVariable: tcell.NewRGBColor(0xdc, 0xd7, 0xba),
		SynOperator: tcell.NewRGBColor(0xc0, 0xa3, 0x6e),
		SynPunct:    tcell.NewRGBColor(0x9c, 0xab, 0xca),
		SynConstant: tcell.NewRGBColor(0xff, 0xa0, 0x66),
	}
}

// themeNord is the Nord palette, ported from druk.
func themeNord() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x2e, 0x34, 0x40),
		SidebarBG:   tcell.NewRGBColor(0x29, 0x2e, 0x39),
		StatusBG:    tcell.NewRGBColor(0x88, 0xc0, 0xd0),
		StatusFg:    tcell.NewRGBColor(0x2e, 0x34, 0x40),
		LineHL:      tcell.NewRGBColor(0x3b, 0x42, 0x52),
		Text:        tcell.NewRGBColor(0xd8, 0xde, 0xe9),
		Muted:       tcell.NewRGBColor(0x7b, 0x88, 0xa1),
		Subtle:      tcell.NewRGBColor(0x4c, 0x56, 0x6a),
		Accent:      tcell.NewRGBColor(0x88, 0xc0, 0xd0),
		AccentSoft:  tcell.NewRGBColor(0xa4, 0xca, 0xd9),
		Selection:   tcell.NewRGBColor(0x43, 0x4c, 0x5e),
		Modified:    tcell.NewRGBColor(0xeb, 0xcb, 0x8b),
		Error:       tcell.NewRGBColor(0xbf, 0x61, 0x6a),
		GitModified: tcell.NewRGBColor(0xeb, 0xcb, 0x8b),
		GitAdded:    tcell.NewRGBColor(0xa3, 0xbe, 0x8c),
		GitDeleted:  tcell.NewRGBColor(0xbf, 0x61, 0x6a),
		GitRenamed:  tcell.NewRGBColor(0x8f, 0xbc, 0xbb),
		GitMixed:    tcell.NewRGBColor(0x88, 0xc0, 0xd0),
		FindMatch:   tcell.NewRGBColor(0x70, 0x69, 0x5a),
		FindCurrent: tcell.NewRGBColor(0xeb, 0xcb, 0x8b),
		FolderColor: tcell.NewRGBColor(0xd8, 0xde, 0xe9),
		FileColor:   tcell.NewRGBColor(0xb7, 0xc0, 0xd0),
		SynKeyword:  tcell.NewRGBColor(0x81, 0xa1, 0xc1),
		SynString:   tcell.NewRGBColor(0xa3, 0xbe, 0x8c),
		SynNumber:   tcell.NewRGBColor(0xb4, 0x8e, 0xad),
		SynComment:  tcell.NewRGBColor(0x61, 0x6e, 0x88),
		SynFunction: tcell.NewRGBColor(0x88, 0xc0, 0xd0),
		SynType:     tcell.NewRGBColor(0x8f, 0xbc, 0xbb),
		SynBuiltin:  tcell.NewRGBColor(0x81, 0xa1, 0xc1),
		SynVariable: tcell.NewRGBColor(0xd8, 0xde, 0xe9),
		SynOperator: tcell.NewRGBColor(0x81, 0xa1, 0xc1),
		SynPunct:    tcell.NewRGBColor(0xec, 0xef, 0xf4),
		SynConstant: tcell.NewRGBColor(0xb4, 0x8e, 0xad),
	}
}

// themeOffshore is skiff's second hand-tuned palette (not a druk
// port): a deep-navy review-tool look — near-black blue surfaces,
// slate-indigo chrome, violet/teal/peach syntax. Unlike every other
// dark theme here its status bar is NOT an accent chip: it stays in
// the background's own family so the frame recedes and the code
// carries all the color. Muted is held to the default theme's 4.5:1
// bar (TestOffshoreCharacter), not the looser ported floor.
func themeOffshore() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x0e, 0x15, 0x21),
		SidebarBG:   tcell.NewRGBColor(0x0b, 0x11, 0x1c),
		StatusBG:    tcell.NewRGBColor(0x0b, 0x11, 0x1c),
		StatusFg:    tcell.NewRGBColor(0x8b, 0x9b, 0xc4),
		LineHL:      tcell.NewRGBColor(0x16, 0x20, 0x2f),
		Text:        tcell.NewRGBColor(0xd0, 0xdc, 0xf5),
		Muted:       tcell.NewRGBColor(0x7c, 0x8c, 0xb4),
		Subtle:      tcell.NewRGBColor(0x4a, 0x58, 0x78),
		Accent:      tcell.NewRGBColor(0x82, 0xaa, 0xff),
		AccentSoft:  tcell.NewRGBColor(0xc0, 0x99, 0xff),
		Selection:   tcell.NewRGBColor(0x1f, 0x2d, 0x4d),
		Modified:    tcell.NewRGBColor(0xec, 0xc4, 0x8d),
		Error:       tcell.NewRGBColor(0xee, 0x6d, 0x85),
		GitModified: tcell.NewRGBColor(0x82, 0xaa, 0xff),
		GitAdded:    tcell.NewRGBColor(0x7f, 0xd8, 0x8f),
		GitDeleted:  tcell.NewRGBColor(0xee, 0x6d, 0x85),
		GitRenamed:  tcell.NewRGBColor(0x7f, 0xdb, 0xca),
		GitMixed:    tcell.NewRGBColor(0xc0, 0x99, 0xff),
		FindMatch:   tcell.NewRGBColor(0x53, 0x43, 0x22),
		FindCurrent: tcell.NewRGBColor(0xec, 0xc4, 0x8d),
		FolderColor: tcell.NewRGBColor(0x82, 0xaa, 0xff),
		FileColor:   tcell.NewRGBColor(0xb6, 0xc2, 0xe0),
		SynKeyword:  tcell.NewRGBColor(0xc0, 0x99, 0xff),
		SynString:   tcell.NewRGBColor(0xec, 0xc4, 0x8d),
		SynNumber:   tcell.NewRGBColor(0xf7, 0x8c, 0x6c),
		SynComment:  tcell.NewRGBColor(0x62, 0x79, 0xa3),
		SynFunction: tcell.NewRGBColor(0x82, 0xaa, 0xff),
		SynType:     tcell.NewRGBColor(0x7f, 0xdb, 0xca),
		SynBuiltin:  tcell.NewRGBColor(0xee, 0x6d, 0x85),
		SynVariable: tcell.NewRGBColor(0xd0, 0xdc, 0xf5),
		SynOperator: tcell.NewRGBColor(0x89, 0xdd, 0xff),
		SynPunct:    tcell.NewRGBColor(0xa8, 0xb6, 0xd8),
		SynConstant: tcell.NewRGBColor(0xf7, 0x8c, 0x6c),
	}
}

// themeOneDark is the One Dark palette, ported from druk.
func themeOneDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x28, 0x2c, 0x34),
		SidebarBG:   tcell.NewRGBColor(0x21, 0x25, 0x2b),
		StatusBG:    tcell.NewRGBColor(0x52, 0x8b, 0xff),
		StatusFg:    tcell.NewRGBColor(0xff, 0xff, 0xff),
		LineHL:      tcell.NewRGBColor(0x2d, 0x32, 0x3c),
		Text:        tcell.NewRGBColor(0xab, 0xb2, 0xbf),
		Muted:       tcell.NewRGBColor(0x82, 0x89, 0x97),
		Subtle:      tcell.NewRGBColor(0x5c, 0x63, 0x70),
		Accent:      tcell.NewRGBColor(0x52, 0x8b, 0xff),
		AccentSoft:  tcell.NewRGBColor(0x71, 0x99, 0xe9),
		Selection:   tcell.NewRGBColor(0x3a, 0x3f, 0x4b),
		Modified:    tcell.NewRGBColor(0xe2, 0xc0, 0x8d),
		Error:       tcell.NewRGBColor(0xff, 0x63, 0x47),
		GitModified: tcell.NewRGBColor(0xe0, 0xc2, 0x85),
		GitAdded:    tcell.NewRGBColor(0x43, 0xd0, 0x8a),
		GitDeleted:  tcell.NewRGBColor(0xe0, 0x52, 0x52),
		GitRenamed:  tcell.NewRGBColor(0xe5, 0xc0, 0x7b),
		GitMixed:    tcell.NewRGBColor(0x52, 0x8b, 0xff),
		FindMatch:   tcell.NewRGBColor(0x69, 0x60, 0x53),
		FindCurrent: tcell.NewRGBColor(0xe2, 0xc0, 0x8d),
		FolderColor: tcell.NewRGBColor(0xab, 0xb2, 0xbf),
		FileColor:   tcell.NewRGBColor(0x9d, 0xa4, 0xb1),
		SynKeyword:  tcell.NewRGBColor(0xc6, 0x78, 0xdd),
		SynString:   tcell.NewRGBColor(0x98, 0xc3, 0x79),
		SynNumber:   tcell.NewRGBColor(0xd1, 0x9a, 0x66),
		SynComment:  tcell.NewRGBColor(0x5c, 0x63, 0x70),
		SynFunction: tcell.NewRGBColor(0x61, 0xaf, 0xef),
		SynType:     tcell.NewRGBColor(0xe5, 0xc0, 0x7b),
		SynBuiltin:  tcell.NewRGBColor(0xe0, 0x6c, 0x75),
		SynVariable: tcell.NewRGBColor(0xab, 0xb2, 0xbf),
		SynOperator: tcell.NewRGBColor(0xc6, 0x78, 0xdd),
		SynPunct:    tcell.NewRGBColor(0xab, 0xb2, 0xbf),
		SynConstant: tcell.NewRGBColor(0xd1, 0x9a, 0x66),
	}
}

// themeRosePineDawn is the Rosé Pine Dawn palette, ported from druk.
func themeRosePineDawn() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xfa, 0xf4, 0xed),
		SidebarBG:   tcell.NewRGBColor(0xff, 0xfa, 0xf3),
		StatusBG:    tcell.NewRGBColor(0x90, 0x7a, 0xa9),
		StatusFg:    tcell.NewRGBColor(0xfa, 0xf4, 0xed),
		LineHL:      tcell.NewRGBColor(0xf4, 0xed, 0xe8),
		Text:        tcell.NewRGBColor(0x57, 0x52, 0x79),
		Muted:       tcell.NewRGBColor(0x79, 0x75, 0x93),
		Subtle:      tcell.NewRGBColor(0x98, 0x93, 0xa5),
		Accent:      tcell.NewRGBColor(0x90, 0x7a, 0xa9),
		AccentSoft:  tcell.NewRGBColor(0x7c, 0x6c, 0x98),
		Selection:   tcell.NewRGBColor(0xdf, 0xda, 0xd9),
		Modified:    tcell.NewRGBColor(0xea, 0x9d, 0x34),
		Error:       tcell.NewRGBColor(0xb4, 0x63, 0x7a),
		GitModified: tcell.NewRGBColor(0xd7, 0x82, 0x7e),
		GitAdded:    tcell.NewRGBColor(0x56, 0x94, 0x9f),
		GitDeleted:  tcell.NewRGBColor(0xb4, 0x63, 0x7a),
		GitRenamed:  tcell.NewRGBColor(0x56, 0x94, 0x9f),
		GitMixed:    tcell.NewRGBColor(0x90, 0x7a, 0xa9),
		FindMatch:   tcell.NewRGBColor(0xf4, 0xd6, 0xac),
		FindCurrent: tcell.NewRGBColor(0xea, 0x9d, 0x34),
		FolderColor: tcell.NewRGBColor(0x57, 0x52, 0x79),
		FileColor:   tcell.NewRGBColor(0x63, 0x5e, 0x82),
		SynKeyword:  tcell.NewRGBColor(0x28, 0x69, 0x83),
		SynString:   tcell.NewRGBColor(0xea, 0x9d, 0x34),
		SynNumber:   tcell.NewRGBColor(0xd7, 0x82, 0x7e),
		SynComment:  tcell.NewRGBColor(0x98, 0x93, 0xa5),
		SynFunction: tcell.NewRGBColor(0xd7, 0x82, 0x7e),
		SynType:     tcell.NewRGBColor(0x56, 0x94, 0x9f),
		SynBuiltin:  tcell.NewRGBColor(0x56, 0x94, 0x9f),
		SynVariable: tcell.NewRGBColor(0x57, 0x52, 0x79),
		SynOperator: tcell.NewRGBColor(0x28, 0x69, 0x83),
		SynPunct:    tcell.NewRGBColor(0x79, 0x75, 0x93),
		SynConstant: tcell.NewRGBColor(0xd7, 0x82, 0x7e),
	}
}

// themeRosePineMoon is the Rosé Pine Moon palette, ported from druk.
func themeRosePineMoon() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x23, 0x21, 0x36),
		SidebarBG:   tcell.NewRGBColor(0x2a, 0x27, 0x3f),
		StatusBG:    tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		StatusFg:    tcell.NewRGBColor(0x23, 0x21, 0x36),
		LineHL:      tcell.NewRGBColor(0x2a, 0x28, 0x3e),
		Text:        tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		Muted:       tcell.NewRGBColor(0x90, 0x8c, 0xaa),
		Subtle:      tcell.NewRGBColor(0x6e, 0x6a, 0x86),
		Accent:      tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		AccentSoft:  tcell.NewRGBColor(0xce, 0xba, 0xec),
		Selection:   tcell.NewRGBColor(0x44, 0x41, 0x5a),
		Modified:    tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		Error:       tcell.NewRGBColor(0xeb, 0x6f, 0x92),
		GitModified: tcell.NewRGBColor(0xea, 0x9a, 0x97),
		GitAdded:    tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		GitDeleted:  tcell.NewRGBColor(0xeb, 0x6f, 0x92),
		GitRenamed:  tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		GitMixed:    tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		FindMatch:   tcell.NewRGBColor(0x6d, 0x59, 0x4d),
		FindCurrent: tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		FolderColor: tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		FileColor:   tcell.NewRGBColor(0xc4, 0xc1, 0xda),
		SynKeyword:  tcell.NewRGBColor(0x3e, 0x8f, 0xb0),
		SynString:   tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		SynNumber:   tcell.NewRGBColor(0xea, 0x9a, 0x97),
		SynComment:  tcell.NewRGBColor(0x6e, 0x6a, 0x86),
		SynFunction: tcell.NewRGBColor(0xea, 0x9a, 0x97),
		SynType:     tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		SynBuiltin:  tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		SynVariable: tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		SynOperator: tcell.NewRGBColor(0x3e, 0x8f, 0xb0),
		SynPunct:    tcell.NewRGBColor(0x90, 0x8c, 0xaa),
		SynConstant: tcell.NewRGBColor(0xea, 0x9a, 0x97),
	}
}

// themeRosePine is the Rosé Pine palette, ported from druk.
func themeRosePine() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x19, 0x17, 0x24),
		SidebarBG:   tcell.NewRGBColor(0x1f, 0x1d, 0x2e),
		StatusBG:    tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		StatusFg:    tcell.NewRGBColor(0x19, 0x17, 0x24),
		LineHL:      tcell.NewRGBColor(0x21, 0x20, 0x2e),
		Text:        tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		Muted:       tcell.NewRGBColor(0x90, 0x8c, 0xaa),
		Subtle:      tcell.NewRGBColor(0x6e, 0x6a, 0x86),
		Accent:      tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		AccentSoft:  tcell.NewRGBColor(0xce, 0xba, 0xec),
		Selection:   tcell.NewRGBColor(0x40, 0x3d, 0x52),
		Modified:    tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		Error:       tcell.NewRGBColor(0xeb, 0x6f, 0x92),
		GitModified: tcell.NewRGBColor(0xeb, 0xbc, 0xba),
		GitAdded:    tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		GitDeleted:  tcell.NewRGBColor(0xeb, 0x6f, 0x92),
		GitRenamed:  tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		GitMixed:    tcell.NewRGBColor(0xc4, 0xa7, 0xe7),
		FindMatch:   tcell.NewRGBColor(0x66, 0x52, 0x41),
		FindCurrent: tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		FolderColor: tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		FileColor:   tcell.NewRGBColor(0xc4, 0xc1, 0xda),
		SynKeyword:  tcell.NewRGBColor(0x31, 0x74, 0x8f),
		SynString:   tcell.NewRGBColor(0xf6, 0xc1, 0x77),
		SynNumber:   tcell.NewRGBColor(0xeb, 0xbc, 0xba),
		SynComment:  tcell.NewRGBColor(0x6e, 0x6a, 0x86),
		SynFunction: tcell.NewRGBColor(0xeb, 0xbc, 0xba),
		SynType:     tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		SynBuiltin:  tcell.NewRGBColor(0x9c, 0xcf, 0xd8),
		SynVariable: tcell.NewRGBColor(0xe0, 0xde, 0xf4),
		SynOperator: tcell.NewRGBColor(0x31, 0x74, 0x8f),
		SynPunct:    tcell.NewRGBColor(0x90, 0x8c, 0xaa),
		SynConstant: tcell.NewRGBColor(0xeb, 0xbc, 0xba),
	}
}

// themeSolarizedDark is the Solarized Dark palette, ported from druk.
func themeSolarizedDark() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x00, 0x2b, 0x36),
		SidebarBG:   tcell.NewRGBColor(0x00, 0x2b, 0x36),
		StatusBG:    tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		StatusFg:    tcell.NewRGBColor(0x00, 0x2b, 0x36),
		LineHL:      tcell.NewRGBColor(0x07, 0x36, 0x42),
		Text:        tcell.NewRGBColor(0x83, 0x94, 0x96),
		Muted:       tcell.NewRGBColor(0x65, 0x7b, 0x83),
		Subtle:      tcell.NewRGBColor(0x58, 0x6e, 0x75),
		Accent:      tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		AccentSoft:  tcell.NewRGBColor(0x47, 0x8e, 0xbd),
		Selection:   tcell.NewRGBColor(0x07, 0x36, 0x42),
		Modified:    tcell.NewRGBColor(0xb5, 0x89, 0x00),
		Error:       tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		GitModified: tcell.NewRGBColor(0xb5, 0x89, 0x00),
		GitAdded:    tcell.NewRGBColor(0x85, 0x99, 0x00),
		GitDeleted:  tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		GitRenamed:  tcell.NewRGBColor(0xb5, 0x89, 0x00),
		GitMixed:    tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		FindMatch:   tcell.NewRGBColor(0x3f, 0x4c, 0x23),
		FindCurrent: tcell.NewRGBColor(0xb5, 0x89, 0x00),
		FolderColor: tcell.NewRGBColor(0x83, 0x94, 0x96),
		FileColor:   tcell.NewRGBColor(0x78, 0x8b, 0x8f),
		SynKeyword:  tcell.NewRGBColor(0x85, 0x99, 0x00),
		SynString:   tcell.NewRGBColor(0x2a, 0xa1, 0x98),
		SynNumber:   tcell.NewRGBColor(0x2a, 0xa1, 0x98),
		SynComment:  tcell.NewRGBColor(0x58, 0x6e, 0x75),
		SynFunction: tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		SynType:     tcell.NewRGBColor(0xb5, 0x89, 0x00),
		SynBuiltin:  tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		SynVariable: tcell.NewRGBColor(0x83, 0x94, 0x96),
		SynOperator: tcell.NewRGBColor(0x85, 0x99, 0x00),
		SynPunct:    tcell.NewRGBColor(0x83, 0x94, 0x96),
		SynConstant: tcell.NewRGBColor(0x2a, 0xa1, 0x98),
	}
}

// themeSolarizedLight is the Solarized Light palette, ported from druk.
func themeSolarizedLight() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0xfd, 0xf6, 0xe3),
		SidebarBG:   tcell.NewRGBColor(0xfd, 0xf6, 0xe3),
		StatusBG:    tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		StatusFg:    tcell.NewRGBColor(0xfd, 0xf6, 0xe3),
		LineHL:      tcell.NewRGBColor(0xee, 0xe8, 0xd5),
		Text:        tcell.NewRGBColor(0x65, 0x7b, 0x83),
		Muted:       tcell.NewRGBColor(0x83, 0x94, 0x96),
		Subtle:      tcell.NewRGBColor(0x93, 0xa1, 0xa1),
		Accent:      tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		AccentSoft:  tcell.NewRGBColor(0x3c, 0x85, 0xb6),
		Selection:   tcell.NewRGBColor(0xee, 0xe8, 0xd5),
		Modified:    tcell.NewRGBColor(0xb5, 0x89, 0x00),
		Error:       tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		GitModified: tcell.NewRGBColor(0xb5, 0x89, 0x00),
		GitAdded:    tcell.NewRGBColor(0x85, 0x99, 0x00),
		GitDeleted:  tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		GitRenamed:  tcell.NewRGBColor(0xb5, 0x89, 0x00),
		GitMixed:    tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		FindMatch:   tcell.NewRGBColor(0xe4, 0xd0, 0x94),
		FindCurrent: tcell.NewRGBColor(0xb5, 0x89, 0x00),
		FolderColor: tcell.NewRGBColor(0x65, 0x7b, 0x83),
		FileColor:   tcell.NewRGBColor(0x70, 0x84, 0x8a),
		SynKeyword:  tcell.NewRGBColor(0x85, 0x99, 0x00),
		SynString:   tcell.NewRGBColor(0x2a, 0xa1, 0x98),
		SynNumber:   tcell.NewRGBColor(0x2a, 0xa1, 0x98),
		SynComment:  tcell.NewRGBColor(0x93, 0xa1, 0xa1),
		SynFunction: tcell.NewRGBColor(0x26, 0x8b, 0xd2),
		SynType:     tcell.NewRGBColor(0xb5, 0x89, 0x00),
		SynBuiltin:  tcell.NewRGBColor(0xdc, 0x32, 0x2f),
		SynVariable: tcell.NewRGBColor(0x65, 0x7b, 0x83),
		SynOperator: tcell.NewRGBColor(0x85, 0x99, 0x00),
		SynPunct:    tcell.NewRGBColor(0x65, 0x7b, 0x83),
		SynConstant: tcell.NewRGBColor(0x2a, 0xa1, 0x98),
	}
}

// themeVesper is the Vesper palette, ported from druk.
func themeVesper() Theme {
	return Theme{
		BG:          tcell.NewRGBColor(0x10, 0x10, 0x10),
		SidebarBG:   tcell.NewRGBColor(0x16, 0x16, 0x16),
		StatusBG:    tcell.NewRGBColor(0xff, 0xc7, 0x99),
		StatusFg:    tcell.NewRGBColor(0x00, 0x00, 0x00),
		LineHL:      tcell.NewRGBColor(0x16, 0x16, 0x16),
		Text:        tcell.NewRGBColor(0xff, 0xff, 0xff),
		Muted:       tcell.NewRGBColor(0xa0, 0xa0, 0xa0),
		Subtle:      tcell.NewRGBColor(0x50, 0x50, 0x50),
		Accent:      tcell.NewRGBColor(0xff, 0xc7, 0x99),
		AccentSoft:  tcell.NewRGBColor(0xff, 0xdb, 0xbd),
		Selection:   tcell.NewRGBColor(0x23, 0x23, 0x23),
		Modified:    tcell.NewRGBColor(0xff, 0xc7, 0x99),
		Error:       tcell.NewRGBColor(0xff, 0x80, 0x80),
		GitModified: tcell.NewRGBColor(0xff, 0xc7, 0x99),
		GitAdded:    tcell.NewRGBColor(0x99, 0xff, 0xe4),
		GitDeleted:  tcell.NewRGBColor(0xff, 0x80, 0x80),
		GitRenamed:  tcell.NewRGBColor(0xff, 0xc7, 0x99),
		GitMixed:    tcell.NewRGBColor(0xff, 0xc7, 0x99),
		FindMatch:   tcell.NewRGBColor(0x64, 0x50, 0x40),
		FindCurrent: tcell.NewRGBColor(0xff, 0xc7, 0x99),
		FolderColor: tcell.NewRGBColor(0xff, 0xff, 0xff),
		FileColor:   tcell.NewRGBColor(0xde, 0xde, 0xde),
		SynKeyword:  tcell.NewRGBColor(0xa0, 0xa0, 0xa0),
		SynString:   tcell.NewRGBColor(0x99, 0xff, 0xe4),
		SynNumber:   tcell.NewRGBColor(0xff, 0xc7, 0x99),
		SynComment:  tcell.NewRGBColor(0x8b, 0x8b, 0x8b),
		SynFunction: tcell.NewRGBColor(0xff, 0xc7, 0x99),
		SynType:     tcell.NewRGBColor(0xff, 0xc7, 0x99),
		SynBuiltin:  tcell.NewRGBColor(0xff, 0xc7, 0x99),
		SynVariable: tcell.NewRGBColor(0xff, 0xff, 0xff),
		SynOperator: tcell.NewRGBColor(0xa0, 0xa0, 0xa0),
		SynPunct:    tcell.NewRGBColor(0xa0, 0xa0, 0xa0),
		SynConstant: tcell.NewRGBColor(0xff, 0xc7, 0x99),
	}
}
