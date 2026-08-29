// =============================================================================
// File: internal/theme/theme.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package theme defines the editor's color palettes. The default is a
// hand-tuned Tokyo Night (this file) with WCAG fences in theme_test.go;
// palettes.go carries the registry of selectable themes ported from
// druk. Themes are compiled in — there is no theme *file* format; the
// only runtime knob is config.json's "theme" key naming a registry id.
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
	StatusFg  tcell.Color // Status bar text — paired with StatusBG per palette.
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

	// --- Low-color degradation (see degrade.go) ---
	//
	// Both are zero on every compiled-in palette: a truecolor terminal
	// gets pure hue and no attributes. Degrade fills them in when the
	// terminal can't render the palette, so the renderer keeps its
	// semantic distinctions in bold/underline/reverse instead of hues
	// the terminal would round into mush.
	LowColor bool
	Attrs    Attrs
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

// minStatusContrast is the readability floor for status-bar text.
// Below WCAG AA (4.5) on purpose — the bar is short, bold, and
// upstream palettes ship deliberately tinted chips — but 3.0 is where
// a colored chip stops being legible over SSH on a dim laptop screen,
// which is skiff's whole habitat. Several ported palettes (ayu-light,
// everforest-light) land under it, so ports are corrected at load
// rather than hand-edited away from their upstream character.
const minStatusContrast = 3.0

// readable returns t with the pairings that fall under the editor's
// floor nudged back over it. Applied by the registry accessors, so
// every palette the picker can hand the app has already been
// corrected — no draw site has to think about it.
//
// Only StatusFg is corrected today: it's the one pairing where the
// ports genuinely fail, and it's the surface a user cannot avoid
// looking at. Body text is fenced by the tests instead, because
// silently repainting someone's Solarized would be worse than the
// 4.13:1 it ships with.
func readable(t Theme) Theme {
	t.StatusFg = ensureContrast(t.StatusFg, t.StatusBG, minStatusContrast)
	return t
}

// ensureContrast returns fg unchanged when it already clears want
// against bg, otherwise the closest color along the line from fg to
// whichever pole (black or white) contrasts more with bg that does.
//
// Walking toward a pole instead of snapping straight to it keeps as
// much of the palette's hue as the floor allows — ayu-light's status
// text stays recognisably ayu, just dark enough to read. The pole is
// picked by contrast rather than by "is bg light?" so a mid-luminance
// chip lands on the side that actually gains. A pole always clears
// 3.0: the worst possible bg still reaches 4.58 against its better
// pole, so the loop can't fall through without improving.
func ensureContrast(fg, bg tcell.Color, want float64) tcell.Color {
	if ContrastRatio(fg, bg) >= want {
		return fg
	}
	pole := tcell.NewRGBColor(0, 0, 0)
	if ContrastRatio(tcell.ColorWhite, bg) > ContrastRatio(pole, bg) {
		pole = tcell.NewRGBColor(255, 255, 255)
	}
	fr, fg8, fb := fg.RGB()
	pr, pg, pb := pole.RGB()
	// 20 fixed steps: fine enough that the hue shift is invisible,
	// cheap enough to run on every theme load.
	for i := 1; i <= 20; i++ {
		mix := float64(i) / 20
		c := tcell.NewRGBColor(
			lerpChannel(fr, pr, mix),
			lerpChannel(fg8, pg, mix),
			lerpChannel(fb, pb, mix),
		)
		if ContrastRatio(c, bg) >= want {
			return c
		}
	}
	return pole
}

// lerpChannel blends one 0-255 channel from a toward b by mix,
// rounding to the nearest value so a full mix lands exactly on b.
func lerpChannel(a, b int32, mix float64) int32 {
	return int32(math.Round(float64(a) + (float64(b)-float64(a))*mix))
}

// Blend linearly mixes a toward b by mix (0 = a, 1 = b) per RGB
// channel. Exported because the diff surfaces derive their changed-row
// tints with it and the tests pin that derivation.
func Blend(a, b tcell.Color, mix float64) tcell.Color {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	return tcell.NewRGBColor(
		lerpChannel(ar, br, mix),
		lerpChannel(ag, bg, mix),
		lerpChannel(ab, bb, mix),
	)
}

// DiffTints are the derived backgrounds the diff surfaces paint changed
// rows with: a whisper of the Git change hue over the modal surface for
// the whole row, and a louder mix over the intra-line span that
// actually differs. Derived rather than hand-tuned per palette, so
// every registry theme gets a correct set for free and a tweak to
// GitAdded/GitDeleted moves the tints with it.
type DiffTints struct {
	DelRow, AddRow   tcell.Color
	DelEmph, AddEmph tcell.Color
}

// diffRowMix and diffEmphMix are how far the row and emphasis tints
// lean from the modal surface (LineHL) toward the Git change hue. 0.18
// reads as a wash under syntax-colored text; 0.35 is loud enough that
// the changed span pops without drowning the glyphs. Both are fenced
// registry-wide by TestEveryThemeDiffTintsReadable — raise them only
// with that test's contrast floor in view.
const (
	diffRowMix  = 0.18
	diffEmphMix = 0.35
)

// diffTintFloor, diffTintSpend, diffMixStep, diffEmphMinGap parametrise
// the tint correction: a tint aims for 4.5:1-adjacent readability
// (diffTintFloor), may spend at most 8% of the contrast its surface
// already affords (diffTintSpend caps the floor for palettes like
// Solarized whose body text starts under 4.0), walks toward the surface
// in diffMixStep steps until it clears, and an emphasis tint always
// stays at least diffEmphMinGap of mix louder than its row tint —
// loudness bought with a bounded contrast cost, because over the few
// glyphs that differ, being distinguishable IS the readability.
const (
	diffTintFloor  = 4.0
	diffTintSpend  = 0.92
	diffMixStep    = 0.02
	diffEmphMinGap = 0.08
)

// DiffTints returns the palette's derived diff-row backgrounds, or
// ok=false on a low-color palette — blending hues the terminal will
// round anyway produces mush, so callers keep their attribute-based
// painting (colored foregrounds, reverse-video emphasis) there.
// Each tint is derived at its target mix, then minimally corrected the
// way readable() corrects StatusFg: walked toward the surface until
// body text clears the floor the palette can afford.
func (t Theme) DiffTints() (DiffTints, bool) {
	if t.LowColor {
		return DiffTints{}, false
	}
	del := diffTintPair(t.LineHL, t.GitDeleted, t.Text)
	add := diffTintPair(t.LineHL, t.GitAdded, t.Text)
	return DiffTints{
		DelRow: del[0], AddRow: add[0],
		DelEmph: del[1], AddEmph: add[1],
	}, true
}

// diffTintPair derives the row and emphasis tints for one change hue.
// The floor is what the palette affords: diffTintFloor when the surface
// itself clears it, a diffTintSpend fraction of the surface's own
// contrast otherwise — so a mix of zero always satisfies the floor and
// the walk always terminates with some tint. The emphasis mix is
// clamped to at least the row's plus diffEmphMinGap, the one place
// loudness deliberately outranks the floor.
func diffTintPair(surface, hue, text tcell.Color) [2]tcell.Color {
	floor := math.Min(diffTintFloor, ContrastRatio(text, surface)*diffTintSpend)
	fit := func(target, minMix float64) float64 {
		mix := target
		for mix > minMix && ContrastRatio(text, Blend(surface, hue, mix)) < floor {
			mix -= diffMixStep
		}
		return math.Max(mix, minMix)
	}
	row := fit(diffRowMix, 0)
	emph := fit(diffEmphMix, row+diffEmphMinGap)
	return [2]tcell.Color{Blend(surface, hue, row), Blend(surface, hue, emph)}
}

// Default returns the editor's hand-tuned Tokyo Night theme — the
// registry's first entry and the fallback for unknown ids.
func Default() Theme {
	return Theme{
		// Surfaces.
		BG:        tcell.NewRGBColor(0x1a, 0x1b, 0x26),
		SidebarBG: tcell.NewRGBColor(0x16, 0x16, 0x1e),
		StatusBG:  tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		StatusFg:  tcell.NewRGBColor(0x1a, 0x1b, 0x26), // == BG: dark text on the accent bar
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
