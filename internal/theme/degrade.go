// =============================================================================
// File: internal/theme/degrade.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// degrade.go handles the terminals skiff's palettes weren't written
// for. Every theme in the registry is 24-bit RGB, and tcell maps RGB
// onto whatever the terminal advertises: on a 16-color TERM that means
// Tokyo Night's five carefully separated grays all collapse onto the
// same ANSI "bright black", and the status bar, the selection, the
// active tab, and the find highlight stop being distinguishable at
// all. That happens on plain `TERM=xterm`, on a serial console, and
// inside a tmux somebody launched without -2 — all normal habitats for
// an SSH editor.
//
// The answer is to stop paying for hue we won't get and spend
// attributes instead: reverse video for selections and the status bar,
// bold for the active tab and dirty markers, underline for the current
// find match. Those survive on every terminal back to a VT100.
//
// Degrade is a pure function on purpose. The app decides once, at
// startup and on resize/theme change, by asking tcell how many colors
// it has; this package just answers "what should the palette look like
// at that depth?" so the rule is testable without a screen.

package theme

import "github.com/gdamore/tcell/v2"

// Attrs carries the attribute-based distinctions a degraded palette
// leans on. Every field is an AttrMask the renderer ORs into the style
// it was already going to use, so a truecolor palette (all zero,
// AttrNone) renders exactly as before and no draw site needs a branch.
type Attrs struct {
	ActiveTab   tcell.AttrMask // Which tab has focus, when the accent hue is gone.
	Selection   tcell.AttrMask // Selected text, when the selection tint is gone.
	FindMatch   tcell.AttrMask // Every match in the viewport.
	FindCurrent tcell.AttrMask // The one match Enter jumps past.
	StatusBar   tcell.AttrMask // The bar itself, so it reads as a bar.
	Modified    tcell.AttrMask // Dirty-buffer markers in the tab strip and tree.
	Error       tcell.AttrMask // Flash messages that report a failure.
	Comment     tcell.AttrMask // Comments, the one syntax class worth keeping apart.
}

// MinTrueColorPalette is the color count at or above which a palette
// is rendered as authored. 256 rather than 2^24 because tcell's
// 256-color quantiser preserves the palettes' relative luminance well
// enough to stay readable; below it, colors are being rounded to eight
// hues and the distinctions the UI encodes in color simply vanish.
const MinTrueColorPalette = 256

// MinAnsiPalette is the color count at or above which the basic eight
// ANSI hues are still worth using for semantics (red = error, green =
// added). Below it the terminal is monochrome and everything has to
// live in attributes.
const MinAnsiPalette = 8

// Degrade returns the palette to actually render with on a terminal
// that reports `colors` colors, as tcell.Screen.Colors() does.
//
// At or above MinTrueColorPalette the theme is returned untouched —
// this is the overwhelmingly common case and must cost nothing.
//
// Below it, surfaces and text drop to tcell.ColorDefault so the
// terminal's own (user-configured, and therefore already readable)
// foreground and background show through instead of a guess quantised
// into the wrong bucket. Semantics that a user would actually
// misread if they collapsed move into Attrs. On an 8-color-or-better
// terminal the handful of colors that are *conventional* rather than
// decorative — red for errors and deletions, green for additions,
// yellow for modification — keep an ANSI hue, because those the
// terminal renders faithfully.
//
// Subtle is deliberately flattened onto ColorDefault: it exists to
// paint separators one notch dimmer than Muted, and one notch of
// dimness is precisely what a low-color terminal cannot express. A
// renderer that wants a cleaner look can check LowColor and skip
// decorative separators entirely.
func Degrade(t Theme, colors int) Theme {
	if colors >= MinTrueColorPalette {
		return t
	}

	d := t
	d.LowColor = true
	d.Attrs = degradedAttrs()

	// Surfaces: hand the terminal back its own defaults. Painting a
	// "dark gray" background on a terminal whose user chose a light
	// scheme is how a degraded palette becomes unreadable, not more
	// readable.
	d.BG = tcell.ColorDefault
	d.SidebarBG = tcell.ColorDefault
	d.StatusBG = tcell.ColorDefault
	d.StatusFg = tcell.ColorDefault
	d.LineHL = tcell.ColorDefault
	d.Selection = tcell.ColorDefault

	// Text tiers: three shades of gray become one shade plus dim.
	d.Text = tcell.ColorDefault
	d.Muted = tcell.ColorDefault
	d.Subtle = tcell.ColorDefault
	d.Accent = tcell.ColorDefault
	d.AccentSoft = tcell.ColorDefault
	d.FolderColor = tcell.ColorDefault
	d.FileColor = tcell.ColorDefault
	d.FindMatch = tcell.ColorDefault
	d.FindCurrent = tcell.ColorDefault

	// Syntax highlighting is a luxury at this depth: eleven token
	// classes cannot survive eight hues, and half-applied highlighting
	// reads worse than none. Comments keep their distinction via
	// Attrs.Comment.
	d.SynKeyword = tcell.ColorDefault
	d.SynString = tcell.ColorDefault
	d.SynNumber = tcell.ColorDefault
	d.SynComment = tcell.ColorDefault
	d.SynFunction = tcell.ColorDefault
	d.SynType = tcell.ColorDefault
	d.SynBuiltin = tcell.ColorDefault
	d.SynVariable = tcell.ColorDefault
	d.SynOperator = tcell.ColorDefault
	d.SynPunct = tcell.ColorDefault
	d.SynConstant = tcell.ColorDefault

	if colors >= MinAnsiPalette {
		d.Error = tcell.ColorRed
		d.Modified = tcell.ColorYellow
		d.GitModified = tcell.ColorYellow
		d.GitAdded = tcell.ColorGreen
		d.GitDeleted = tcell.ColorRed
		d.GitRenamed = tcell.ColorBlue
		d.GitMixed = tcell.ColorPurple
		return d
	}

	// Monochrome: even red is a lie here. Attrs.Error and
	// Attrs.Modified carry the whole signal.
	d.Error = tcell.ColorDefault
	d.Modified = tcell.ColorDefault
	d.GitModified = tcell.ColorDefault
	d.GitAdded = tcell.ColorDefault
	d.GitDeleted = tcell.ColorDefault
	d.GitRenamed = tcell.ColorDefault
	d.GitMixed = tcell.ColorDefault
	return d
}

// degradedAttrs is the attribute vocabulary a low-color palette
// speaks. Chosen so no two adjacent surfaces wear the same mask:
// reverse marks "this region is special" (selection, status bar,
// current match), bold marks "this item is active or changed", and
// underline is reserved for the single current find match so it stands
// out from the other reversed matches around it.
func degradedAttrs() Attrs {
	return Attrs{
		ActiveTab:   tcell.AttrBold | tcell.AttrUnderline,
		Selection:   tcell.AttrReverse,
		FindMatch:   tcell.AttrReverse,
		FindCurrent: tcell.AttrReverse | tcell.AttrBold | tcell.AttrUnderline,
		StatusBar:   tcell.AttrReverse,
		Modified:    tcell.AttrBold,
		Error:       tcell.AttrBold,
		Comment:     tcell.AttrDim,
	}
}
