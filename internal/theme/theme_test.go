// =============================================================================
// File: internal/theme/theme_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the theme package. The package is pure data, but we still want
// to pin down a few invariants: every color is set, and the few pairs that
// must visually contrast (BG vs Text, BG vs SidebarBG, BG vs Selection) are
// not accidentally equal. A future palette tweak that breaks one of these
// would render the editor unusable, so the tests act as a tripwire.

package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestDefault_AllColorsSet walks every documented field on the Theme struct
// and asserts the value is not the zero tcell.Color. A missing assignment
// in Default() would otherwise silently render that UI element invisible.
func TestDefault_AllColorsSet(t *testing.T) {
	th := Default()

	// Each entry is the human-readable field name and its color value. We
	// list explicitly rather than reflecting so a new field forces us to
	// decide whether it belongs in the contrast invariants too.
	cases := []struct {
		name  string
		color tcell.Color
	}{
		{"BG", th.BG},
		{"SidebarBG", th.SidebarBG},
		{"StatusBG", th.StatusBG},
		{"LineHL", th.LineHL},
		{"Text", th.Text},
		{"Muted", th.Muted},
		{"Subtle", th.Subtle},
		{"Accent", th.Accent},
		{"AccentSoft", th.AccentSoft},
		{"Selection", th.Selection},
		{"Modified", th.Modified},
		{"Error", th.Error},
		{"FolderColor", th.FolderColor},
		{"FileColor", th.FileColor},
		{"SynKeyword", th.SynKeyword},
		{"SynString", th.SynString},
		{"SynNumber", th.SynNumber},
		{"SynComment", th.SynComment},
		{"SynFunction", th.SynFunction},
		{"SynType", th.SynType},
		{"SynBuiltin", th.SynBuiltin},
		{"SynVariable", th.SynVariable},
		{"SynOperator", th.SynOperator},
		{"SynPunct", th.SynPunct},
		{"SynConstant", th.SynConstant},
	}

	for _, c := range cases {
		// tcell.ColorDefault is the zero/sentinel; an unset RGB color from
		// Default() would also be zero, so we treat "0" as missing.
		if c.color == 0 {
			t.Errorf("Default(): field %s is unset (zero color)", c.name)
		}
	}
}

// TestDefault_ContrastInvariants asserts the small handful of color pairs
// that must differ for the editor to be readable. If any of these collapse
// to the same color the user sees a blank panel or invisible selection.
func TestDefault_ContrastInvariants(t *testing.T) {
	th := Default()

	cases := []struct {
		name string
		a, b tcell.Color
	}{
		// Text on background — without contrast there's nothing to read.
		{"BG vs Text", th.BG, th.Text},
		// Sidebar must read as a separate panel from the editor surface.
		{"BG vs SidebarBG", th.BG, th.SidebarBG},
		// Selection block must stand out against the unselected background.
		{"Selection vs BG", th.Selection, th.BG},
	}

	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("Default(): %s collide (%v == %v)", c.name, c.a, c.b)
		}
	}
}

// contrastRatio delegates to the package's exported ContrastRatio —
// production code needs the math now (Theme.SelectionFg), so the tests
// exercise the same implementation the renderer uses.
func contrastRatio(a, b tcell.Color) float64 {
	return ContrastRatio(a, b)
}

// TestSelectionFg_SyntaxReadableOnSelection pins the selection-swap
// contract: for every syntax color, whatever SelectionFg returns must
// clear WCAG AA on the Selection background — colors that already pass
// keep their identity, the rest trade it for Text. Without the swap,
// keywords/functions/builtins sat at 3.4–4.5:1 inside a selection.
func TestSelectionFg_SyntaxReadableOnSelection(t *testing.T) {
	th := Default()
	cases := []struct {
		name string
		fg   tcell.Color
	}{
		{"SynKeyword", th.SynKeyword},
		{"SynString", th.SynString},
		{"SynNumber", th.SynNumber},
		{"SynComment", th.SynComment},
		{"SynFunction", th.SynFunction},
		{"SynType", th.SynType},
		{"SynBuiltin", th.SynBuiltin},
		{"SynVariable", th.SynVariable},
		{"SynOperator", th.SynOperator},
		{"SynPunct", th.SynPunct},
		{"SynConstant", th.SynConstant},
		{"Text", th.Text},
	}
	for _, c := range cases {
		got := th.SelectionFg(c.fg)
		if ratio := ContrastRatio(got, th.Selection); ratio < 4.5 {
			t.Errorf("SelectionFg(%s) = %v at %.2f:1 on Selection, need >= 4.5", c.name, got, ratio)
		}
	}
	// A color that already passes must keep its identity.
	if got := th.SelectionFg(th.SynString); got != th.SynString {
		t.Errorf("SynString passes on Selection and should be kept, got %v", got)
	}
}

// TestDefault_WCAGContrast pins the real accessibility bar for every
// fg/bg pair the UI actually paints (verified against the draw sites in
// app, editor, and filetree). Text pairs must hit WCAG AA 4.5:1;
// non-text UI glyphs (borders, splitter, close-×, hover chevron) get
// the 3:1 graphics bar. This is the regression fence for the "line
// numbers and comments at 2.76:1" audit finding — a future palette
// tweak that dips below these bars should fail loudly here.
func TestDefault_WCAGContrast(t *testing.T) {
	th := Default()

	cases := []struct {
		name string
		fg   tcell.Color
		bg   tcell.Color
		min  float64
	}{
		// Primary text surfaces.
		{"Text on BG (editor body)", th.Text, th.BG, 4.5},
		{"Text on LineHL (cursor line)", th.Text, th.LineHL, 4.5},
		{"Text on Selection", th.Text, th.Selection, 4.5},
		{"Text on FindMatch (find tint keeps Text fg)", th.Text, th.FindMatch, 4.5},
		{"BG on FindCurrent (active match)", th.BG, th.FindCurrent, 4.5},
		{"BG on StatusBG (status bar)", th.BG, th.StatusBG, 4.5},

		// Secondary text: line numbers, hints, inactive tabs, tree
		// header, dotfiles, menu shortcuts — everywhere Muted is painted.
		{"Muted on BG (line numbers, hints)", th.Muted, th.BG, 4.5},
		{"Muted on SidebarBG (inactive tabs, tree header)", th.Muted, th.SidebarBG, 4.5},
		{"Muted on LineHL (menu shortcuts, disabled rows)", th.Muted, th.LineHL, 4.5},

		// Comments are content, not chrome — full AA on both line bgs.
		{"SynComment on BG", th.SynComment, th.BG, 4.5},
		{"SynComment on LineHL (comment on cursor line)", th.SynComment, th.LineHL, 4.5},

		// Accents and states that carry text.
		{"Error on BG", th.Error, th.BG, 4.5},
		{"Modified on SidebarBG (dirty tab dot label)", th.Modified, th.SidebarBG, 4.5},
		{"Accent on SidebarBG (active tree row)", th.Accent, th.SidebarBG, 4.5},
		{"Accent on LineHL (menu title)", th.Accent, th.LineHL, 4.5},
		{"AccentSoft on LineHL (active line number)", th.AccentSoft, th.LineHL, 4.5},
		{"FolderColor on SidebarBG", th.FolderColor, th.SidebarBG, 4.5},
		{"FileColor on SidebarBG", th.FileColor, th.SidebarBG, 4.5},

		// Git status colors label file names in the tree.
		{"GitModified on SidebarBG", th.GitModified, th.SidebarBG, 4.5},
		{"GitAdded on SidebarBG", th.GitAdded, th.SidebarBG, 4.5},
		{"GitDeleted on SidebarBG", th.GitDeleted, th.SidebarBG, 4.5},
		{"GitRenamed on SidebarBG", th.GitRenamed, th.SidebarBG, 4.5},
		{"GitMixed on SidebarBG", th.GitMixed, th.SidebarBG, 4.5},

		// Non-text UI glyphs: modal border, splitter, close-×, hover
		// chevron. WCAG 1.4.11 graphics bar.
		{"Subtle on BG (close-× on active tab)", th.Subtle, th.BG, 3.0},
		{"Subtle on SidebarBG (idle splitter)", th.Subtle, th.SidebarBG, 3.0},
		{"Subtle on LineHL (modal border)", th.Subtle, th.LineHL, 3.0},
		{"AccentSoft on Selection (hover chevron)", th.AccentSoft, th.Selection, 3.0},
	}

	for _, c := range cases {
		if got := contrastRatio(c.fg, c.bg); got < c.min {
			t.Errorf("%s: contrast %.2f:1, need >= %.1f:1", c.name, got, c.min)
		}
	}
}
