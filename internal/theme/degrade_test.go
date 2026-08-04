// =============================================================================
// File: internal/theme/degrade_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the low-color fallback. The rule these pin down is
// "distinctions must survive the loss of hue": whatever the palette
// encoded in color at 24 bits has to still be visible at 8 colors or
// at none, and the truecolor path has to stay byte-identical so the
// common case pays nothing.

package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestDegradeTrueColorIsIdentity: the overwhelmingly common terminal
// reports 256 colors or more and must get exactly the palette the
// theme author wrote, with no attributes bolted on.
func TestDegradeTrueColorIsIdentity(t *testing.T) {
	src := Default()
	for _, colors := range []int{256, 16777216} {
		got := Degrade(src, colors)
		if got != src {
			t.Fatalf("Degrade(theme, %d) modified a truecolor palette", colors)
		}
		if got.LowColor {
			t.Fatalf("Degrade(theme, %d) flagged LowColor", colors)
		}
		if got.Attrs != (Attrs{}) {
			t.Fatalf("Degrade(theme, %d) set attributes: %+v", colors, got.Attrs)
		}
	}
}

// TestDegradeSurfacesFallBackToTerminalDefaults is the core of the
// fix: at low depth we stop guessing at background hues and hand the
// terminal back its own, which the user already configured to be
// readable. Painting a quantised "dark gray" over somebody's light
// scheme is the failure mode this prevents.
func TestDegradeSurfacesFallBackToTerminalDefaults(t *testing.T) {
	d := Degrade(Default(), 16)
	if !d.LowColor {
		t.Fatal("degraded palette must be flagged LowColor")
	}
	cases := []struct {
		name string
		got  tcell.Color
	}{
		{"BG", d.BG},
		{"SidebarBG", d.SidebarBG},
		{"StatusBG", d.StatusBG},
		{"StatusFg", d.StatusFg},
		{"LineHL", d.LineHL},
		{"Selection", d.Selection},
		{"Text", d.Text},
		{"Muted", d.Muted},
		{"Accent", d.Accent},
	}
	for _, c := range cases {
		if c.got != tcell.ColorDefault {
			t.Errorf("%s = %v, want ColorDefault at 16 colors", c.name, c.got)
		}
	}
}

// TestDegradeDropsSubtleBorders pins the "skip the low-contrast
// separators" half of the rule. Subtle exists to be one notch dimmer
// than Muted, and one notch of dimness is exactly what a 16-color
// terminal cannot render — kept as a distinct hue it would be either
// invisible or indistinguishable from body text.
func TestDegradeDropsSubtleBorders(t *testing.T) {
	src := Default()
	if src.Subtle == src.Muted {
		t.Fatal("precondition: the default palette should separate Subtle from Muted")
	}
	d := Degrade(src, 16)
	if d.Subtle != tcell.ColorDefault {
		t.Fatalf("Subtle = %v, want ColorDefault", d.Subtle)
	}
}

// TestDegradeMovesSemanticsIntoAttributes: with hue gone, selection,
// the status bar, the active tab and dirty markers must each still be
// distinguishable, and no two of them may collapse onto the same
// attribute mask in a way that makes them look identical where they
// meet (the current find match sits among plain matches; the active
// tab sits among inactive ones).
func TestDegradeMovesSemanticsIntoAttributes(t *testing.T) {
	a := Degrade(Default(), 16).Attrs

	if a.Selection == tcell.AttrNone {
		t.Error("selection has no color and no attribute: it would be invisible")
	}
	if a.StatusBar == tcell.AttrNone {
		t.Error("status bar has no color and no attribute: it would not read as a bar")
	}
	if a.ActiveTab == tcell.AttrNone {
		t.Error("active tab is indistinguishable from inactive ones")
	}
	if a.Modified == tcell.AttrNone {
		t.Error("dirty marker is indistinguishable from a saved buffer")
	}
	if a.FindCurrent == a.FindMatch {
		t.Error("current find match must differ from the other matches")
	}
	if a.Comment == tcell.AttrNone {
		t.Error("comments lose their only remaining distinction")
	}
	// Every mask must be built from attributes a VT100-era terminal
	// understands — anything else defeats the point of degrading.
	allowed := tcell.AttrBold | tcell.AttrDim | tcell.AttrUnderline | tcell.AttrReverse
	for name, mask := range map[string]tcell.AttrMask{
		"ActiveTab": a.ActiveTab, "Selection": a.Selection,
		"FindMatch": a.FindMatch, "FindCurrent": a.FindCurrent,
		"StatusBar": a.StatusBar, "Modified": a.Modified,
		"Error": a.Error, "Comment": a.Comment,
	} {
		if mask&^allowed != 0 {
			t.Errorf("%s uses an attribute outside bold/dim/underline/reverse: %v", name, mask)
		}
	}
}

// TestDegradeKeepsConventionalAnsiHues: red-means-error and
// green-means-added are terminal conventions, not palette decoration,
// and an 8-color terminal renders them faithfully. Throwing them away
// with the rest of the palette would lose real information.
func TestDegradeKeepsConventionalAnsiHues(t *testing.T) {
	d := Degrade(Default(), 8)
	cases := []struct {
		name string
		got  tcell.Color
		want tcell.Color
	}{
		{"Error", d.Error, tcell.ColorRed},
		{"GitAdded", d.GitAdded, tcell.ColorGreen},
		{"GitDeleted", d.GitDeleted, tcell.ColorRed},
		{"GitModified", d.GitModified, tcell.ColorYellow},
		{"Modified", d.Modified, tcell.ColorYellow},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v at 8 colors", c.name, c.got, c.want)
		}
	}
}

// TestDegradeMonochromeUsesNoColorAtAll covers the serial-console /
// TERM=dumb end of the range: below eight colors even red is a lie,
// so every field has to be ColorDefault and the meaning has to be
// carried entirely by attributes.
func TestDegradeMonochromeUsesNoColorAtAll(t *testing.T) {
	d := Degrade(Default(), 2)
	cases := []struct {
		name string
		got  tcell.Color
	}{
		{"Error", d.Error},
		{"Modified", d.Modified},
		{"GitAdded", d.GitAdded},
		{"GitDeleted", d.GitDeleted},
		{"GitModified", d.GitModified},
		{"GitRenamed", d.GitRenamed},
		{"GitMixed", d.GitMixed},
		{"SynKeyword", d.SynKeyword},
		{"SynComment", d.SynComment},
	}
	for _, c := range cases {
		if c.got != tcell.ColorDefault {
			t.Errorf("%s = %v, want ColorDefault on a monochrome terminal", c.name, c.got)
		}
	}
	if d.Attrs.Error == tcell.AttrNone {
		t.Error("with no red available, errors need an attribute to stand out")
	}
}

// TestDegradeIsPure guards the "app can call this anywhere" promise:
// the input theme must not be mutated, and the same input must always
// give the same output.
func TestDegradeIsPure(t *testing.T) {
	src := Default()
	before := src
	first := Degrade(src, 16)
	second := Degrade(src, 16)
	if src != before {
		t.Fatal("Degrade mutated its argument")
	}
	if first != second {
		t.Fatal("Degrade is not deterministic")
	}
}

// TestDegradeAppliesToEveryRegistryPalette: the picker can hand the
// app any of the 26 themes, so the fallback has to hold for all of
// them, not just the default. A port that somehow kept a hue here
// would be unreadable on the exact terminals this path exists for.
func TestDegradeAppliesToEveryRegistryPalette(t *testing.T) {
	for _, e := range List() {
		d := Degrade(e.Build(), 16)
		if !d.LowColor {
			t.Errorf("%s: not flagged LowColor", e.ID)
		}
		if d.BG != tcell.ColorDefault || d.Text != tcell.ColorDefault {
			t.Errorf("%s: surfaces survived degradation (BG=%v Text=%v)", e.ID, d.BG, d.Text)
		}
	}
}
