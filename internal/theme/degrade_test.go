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
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if a.CursorLine == tcell.AttrNone {
		t.Error("the caret's line has no tint and no attribute: it would be invisible")
	}
	if a.SelectedRow == tcell.AttrNone {
		t.Error("a list's highlighted row has no hue and no attribute")
	}
	if a.CursorLine == a.Selection {
		t.Error("the cursor line must not look like a selection on that line")
	}
	// Every mask must be built from attributes a VT100-era terminal
	// understands — anything else defeats the point of degrading.
	allowed := tcell.AttrBold | tcell.AttrDim | tcell.AttrUnderline | tcell.AttrReverse
	for name, mask := range map[string]tcell.AttrMask{
		"ActiveTab": a.ActiveTab, "Selection": a.Selection,
		"FindMatch": a.FindMatch, "FindCurrent": a.FindCurrent,
		"StatusBar": a.StatusBar, "Modified": a.Modified,
		"Error": a.Error, "Comment": a.Comment,
		"CursorLine": a.CursorLine, "SelectedRow": a.SelectedRow,
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

// TestWithAttrs pins the one way a renderer applies an Attrs field: the
// mask is OR'd into the style's existing attributes rather than
// replacing them, an underline bit also turns the underline STYLE on
// (which is what the terminal driver actually emits from), and a zero
// mask — every truecolor palette — hands the style back untouched.
func TestWithAttrs(t *testing.T) {
	base := tcell.StyleDefault.Bold(true)
	if got := WithAttrs(base, tcell.AttrNone); got != base {
		t.Fatal("an empty mask must not change the style")
	}
	got := WithAttrs(base, tcell.AttrReverse)
	if _, _, attrs := got.Decompose(); attrs&tcell.AttrBold == 0 || attrs&tcell.AttrReverse == 0 {
		t.Fatalf("attrs = %v, want bold kept and reverse added", attrs)
	}
	ul := WithAttrs(base, tcell.AttrUnderline)
	if ul.GetUnderlineStyle() == tcell.UnderlineStyleNone {
		t.Fatal("an underline mask must set the underline style, or the driver paints nothing")
	}
	if _, _, attrs := ul.Decompose(); attrs&tcell.AttrUnderline == 0 {
		t.Fatal("the underline attribute bit must be set too")
	}
}

// TestEveryAttrHasAConsumer is the fence against a half-wired channel:
// every field of Attrs must be read by at least one renderer outside
// this package. Four of the eight original fields — Selection,
// FindMatch, FindCurrent, Comment — were declared and degraded but
// never applied, so on a 16-colour terminal a selection gave no
// feedback and find highlights vanished. A grep over the source tree
// is a blunt instrument, but a field nobody names is a field nobody
// uses, and that is exactly the bug.
func TestEveryAttrHasAConsumer(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	var sources []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Base(path) == "theme" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sources = append(sources, string(b))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	all := strings.Join(sources, "\n")
	rt := reflect.TypeOf(Attrs{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !strings.Contains(all, "Attrs."+name) {
			t.Errorf("Attrs.%s is degraded but no renderer outside internal/theme reads it", name)
		}
	}
}
