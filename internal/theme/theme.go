// =============================================================================
// File: internal/theme/theme.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package theme defines the editor's curated color palette. The editor
// intentionally ships one opinionated dark theme — there is no runtime
// configuration, no theme file, no JSON. To restyle the editor, edit this
// file and recompile. The palette is inspired by Tokyo Night and tuned so
// the syntax colors stay legible against the chrome.
package theme

import (
	"math"

	"github.com/gdamore/tcell/v2"
)

// Theme bundles every color the editor renders. UI surfaces, accents, and
// syntax-highlight colors all live in one struct so that adjusting one
// element of the palette can be balanced against the others.
type Theme struct {
	// --- Surfaces ---
	BG        tcell.Color // Editor background.
	SidebarBG tcell.Color // File tree / inactive tab background, slightly darker than BG.
	StatusBG  tcell.Color // Status bar background.
	LineHL    tcell.Color // Active line highlight.

	// --- Foregrounds & accents ---
	Text        tcell.Color // Primary editor text.
	Muted       tcell.Color // Line numbers, inactive tabs, secondary UI text.
	Subtle      tcell.Color // Even more subtle (separators, hints).
	Accent      tcell.Color // Active tab accent, root label, important UI.
	AccentSoft  tcell.Color // Softer accent (active line number).
	Selection   tcell.Color // Selection background.
	Modified    tcell.Color // Dirty indicator (unsaved changes).
	Error       tcell.Color // Error messages.
	GitModified tcell.Color
	GitAdded    tcell.Color
	GitDeleted  tcell.Color
	GitRenamed  tcell.Color
	GitMixed    tcell.Color

	// FindMatch / FindCurrent paint search hits in the editor body.
	// FindMatch is a soft tint applied to every match in the viewport;
	// FindCurrent is the louder color drawn under the "active" match
	// (the one Enter/Esc-g will jump past) so the user can find their
	// place at a glance.
	FindMatch   tcell.Color
	FindCurrent tcell.Color

	// --- File tree ---
	FolderColor tcell.Color
	FileColor   tcell.Color

	// --- Syntax highlighting ---
	SynKeyword  tcell.Color
	SynString   tcell.Color
	SynNumber   tcell.Color
	SynComment  tcell.Color
	SynFunction tcell.Color
	SynType     tcell.Color
	SynBuiltin  tcell.Color
	SynVariable tcell.Color
	SynOperator tcell.Color
	SynPunct    tcell.Color
	SynConstant tcell.Color
}

// relativeLuminance implements the WCAG 2.x luminance formula for a
// tcell RGB color.
func relativeLuminance(c tcell.Color) float64 {
	ri, gi, bi := c.RGB()
	lin := func(v int32) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(ri) + 0.7152*lin(gi) + 0.0722*lin(bi)
}

// ContrastRatio returns the WCAG contrast ratio (>=1) between two
// colors. Exported because the renderer makes live readability
// decisions with it (SelectionFg) and the theme tests fence the
// palette with it — one implementation for both.
func ContrastRatio(a, b tcell.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// SelectionFg returns the foreground to paint fg with when its cell
// sits on the Selection background: fg itself while it stays readable
// (>=4.5:1), Text otherwise. Several syntax colors (keyword, function,
// builtin, type, punct, number) fall to ~3.4-4.5:1 on the selection
// blue; swapping exactly the failing ones keeps selected code legible
// without flattening the colors that do pass.
func (t Theme) SelectionFg(fg tcell.Color) tcell.Color {
	if ContrastRatio(fg, t.Selection) >= 4.5 {
		return fg
	}
	return t.Text
}

// Default returns the editor's curated dark theme. It is the only theme the
// editor ships with — calling code can tweak fields on the returned value if
// it really needs to, but there is no theme-loading machinery on purpose.
func Default() Theme {
	return Theme{
		// Surfaces.
		BG:        tcell.NewRGBColor(0x1a, 0x1b, 0x26),
		SidebarBG: tcell.NewRGBColor(0x16, 0x16, 0x1e),
		StatusBG:  tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		LineHL:    tcell.NewRGBColor(0x1f, 0x20, 0x2e),

		// Foregrounds & accents. Muted and Subtle are brighter than
		// stock Tokyo Night on purpose: they paint real text (line
		// numbers, hints, inactive tabs) and real controls (splitter,
		// close-×, modal borders), so Muted must clear WCAG AA 4.5:1
		// and Subtle the 3:1 graphics bar on every surface they touch.
		// theme_test.go's WCAG table is the fence — check it before
		// darkening either.
		Text:        tcell.NewRGBColor(0xc0, 0xca, 0xf5),
		Muted:       tcell.NewRGBColor(0x80, 0x89, 0xb1),
		Subtle:      tcell.NewRGBColor(0x60, 0x69, 0x96),
		Accent:      tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		AccentSoft:  tcell.NewRGBColor(0xbb, 0x9a, 0xf7),
		Selection:   tcell.NewRGBColor(0x33, 0x46, 0x7c),
		Modified:    tcell.NewRGBColor(0xe0, 0xaf, 0x68),
		Error:       tcell.NewRGBColor(0xf7, 0x76, 0x8e),
		GitModified: tcell.NewRGBColor(0xff, 0x9e, 0x64),
		GitAdded:    tcell.NewRGBColor(0x9e, 0xce, 0x6a),
		GitDeleted:  tcell.NewRGBColor(0xf7, 0x76, 0x8e),
		GitRenamed:  tcell.NewRGBColor(0x7d, 0xcf, 0xf7),
		GitMixed:    tcell.NewRGBColor(0xbb, 0x9a, 0xf7),

		// Find. FindMatch is a desaturated amber so it reads as "all
		// hits" without competing with the syntax palette — dark
		// enough that Text stays readable (4.5:1) on top of it, since
		// the tint keeps the editor's foreground. FindCurrent is full
		// amber — the same shade the dirty indicator uses — so the
		// active match jumps off the page.
		FindMatch:   tcell.NewRGBColor(0x68, 0x4c, 0x1c),
		FindCurrent: tcell.NewRGBColor(0xe0, 0xaf, 0x68),

		// Tree.
		FolderColor: tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		FileColor:   tcell.NewRGBColor(0xa9, 0xb1, 0xd6),

		// Syntax — Tokyo Night-ish.
		SynKeyword: tcell.NewRGBColor(0xbb, 0x9a, 0xf7), // purple
		SynString:  tcell.NewRGBColor(0x9e, 0xce, 0x6a), // green
		SynNumber:  tcell.NewRGBColor(0xff, 0x9e, 0x64), // orange
		// Comments are content, not chrome — they get their own value
		// (not Muted) so dimming UI hints can never drag code comments
		// below the 4.5:1 readability bar, and vice versa.
		SynComment:  tcell.NewRGBColor(0x7d, 0x8a, 0xb8), // readable slate-blue
		SynFunction: tcell.NewRGBColor(0x7a, 0xa2, 0xf7), // blue
		SynType:     tcell.NewRGBColor(0x2a, 0xc3, 0xde), // cyan
		SynBuiltin:  tcell.NewRGBColor(0xf7, 0x76, 0x8e), // red
		SynVariable: tcell.NewRGBColor(0xc0, 0xca, 0xf5), // text-like
		SynOperator: tcell.NewRGBColor(0x89, 0xdd, 0xff), // light cyan
		SynPunct:    tcell.NewRGBColor(0xa9, 0xb1, 0xd6), // soft text
		SynConstant: tcell.NewRGBColor(0xff, 0x9e, 0x64), // orange
	}
}
