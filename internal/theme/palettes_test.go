// =============================================================================
// File: internal/theme/palettes_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Sanity fences for the theme registry: identity invariants plus a
// readability floor every ported palette must clear. Deliberately
// looser than theme_test.go's WCAG table for the hand-tuned default —
// ports keep their upstream character, but text must stay text.

package theme

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestRegistryIdentity pins the registry shape: the default leads,
// ids are unique, and ByID round-trips every entry.
func TestRegistryIdentity(t *testing.T) {
	list := List()
	if len(list) < 20 {
		t.Fatalf("registry suspiciously small: %d", len(list))
	}
	if list[0].ID != DefaultID {
		t.Fatalf("default must lead the picker, got %q", list[0].ID)
	}
	seen := map[string]bool{}
	for _, e := range list {
		if e.ID == "" || e.Name == "" || e.Build == nil {
			t.Fatalf("incomplete entry: %+v", e)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		if _, ok := ByID(e.ID); !ok {
			t.Fatalf("ByID(%q) missed", e.ID)
		}
	}
}

// TestByIDUnknownFallsBackToDefault: a stale config id must yield the
// default theme and ok=false, never a zero-valued (all-black) Theme.
func TestByIDUnknownFallsBackToDefault(t *testing.T) {
	th, ok := ByID("no-such-theme")
	if ok {
		t.Fatal("unknown id should report !ok")
	}
	if th.BG != Default().BG {
		t.Fatal("unknown id should fall back to the default palette")
	}
}

// TestEveryThemeReadable is the floor for ported palettes. It is
// deliberately looser than the default theme's WCAG fence: upstream
// palettes keep their upstream values (Solarized's body text is 4.13:1
// by design; Nord's comments are famously dim), so this only catches
// genuine port breakage — text you can't read at all.
func TestEveryThemeReadable(t *testing.T) {
	for _, e := range List() {
		th := e.Build()
		if r := ContrastRatio(th.Text, th.BG); r < 4.0 {
			t.Errorf("%s: Text/BG contrast %.2f < 4.0", e.ID, r)
		}
		if r := ContrastRatio(th.SynComment, th.BG); r < 2.0 {
			t.Errorf("%s: comment contrast %.2f < 2.0", e.ID, r)
		}
		if th.Text == th.BG || th.Text == th.Selection {
			t.Errorf("%s: text would vanish on its surfaces", e.ID)
		}
		// Status bar: >= minStatusContrast. Ayu Light and Everforest
		// Light ship white-on-color chips at ~1.9-2.2:1 upstream —
		// fine on a calibrated laptop panel, unreadable over SSH on a
		// dim screen, which is skiff's habitat. The registry
		// accessors run readable() over every palette, so what the
		// picker hands the app has already been corrected; this
		// asserts the post-correction floor rather than the ports'
		// raw values.
		if r := ContrastRatio(th.StatusFg, th.StatusBG); r < minStatusContrast {
			t.Errorf("%s: status bar contrast %.2f < %.1f", e.ID, r, minStatusContrast)
		}
	}
}

// TestEverySubtleClearsGraphicsFloor is the chrome counterpart of
// TestEveryThemeReadable: Subtle paints every overlay border, scrollbar
// track, the splitter and an inactive tab's ×, over three different
// surfaces, and the registry accessors must hand back a palette on
// which none of those disappears. As shipped, Offshore's borders
// measured 2.58:1 on the editor and 2.31:1 on an overlay, Dracula's
// 1.56:1 — under the 3:1 graphics bar on 21 of 27 palettes.
func TestEverySubtleClearsGraphicsFloor(t *testing.T) {
	for _, e := range List() {
		th := e.Build()
		if r := minContrastOver(th.Subtle, subtleSurfaces(th)); r < minGraphicsContrast {
			t.Errorf("%s: Subtle contrast %.2f < %.1f on its worst surface", e.ID, r, minGraphicsContrast)
		}
	}
}

// TestSubtleIsCorrectedNotHandEdited pins the mechanism behind the
// floor: at least one raw palette must still fail it before readable()
// runs (otherwise the correction is dead code and the next dim port
// ships unnoticed), and the correction must stay minimal — Subtle is
// meant to recede, so a walk that snapped it to white would turn every
// border into a text-weight line.
func TestSubtleIsCorrectedNotHandEdited(t *testing.T) {
	corrected := 0
	for _, e := range registry { // raw registry: pre-correction values
		raw := e.Build()
		before := minContrastOver(raw.Subtle, subtleSurfaces(raw))
		if before >= minGraphicsContrast {
			continue
		}
		corrected++
		fixed := readable(raw)
		after := minContrastOver(fixed.Subtle, subtleSurfaces(fixed))
		if after < minGraphicsContrast {
			t.Errorf("%s: Subtle correction landed at %.2f, still under the floor", e.ID, after)
		}
		if after > minGraphicsContrast+1.5 {
			t.Errorf("%s: Subtle correction overshot to %.2f — no longer subtle", e.ID, after)
		}
	}
	if corrected == 0 {
		t.Fatal("no palette needed a Subtle correction; the floor is no longer load-bearing")
	}
}

// TestPortedPalettesAreCorrectedNotHandEdited pins the mechanism, not
// just the outcome: at least one registry palette must actually be
// failing the floor before correction, otherwise readable() has
// quietly become dead code and the next low-contrast port would ship
// unnoticed. It also checks the correction is minimal — a corrected
// StatusFg must land near the floor, not snap to black or white and
// throw the palette's character away.
func TestPortedPalettesAreCorrectedNotHandEdited(t *testing.T) {
	corrected := 0
	for _, e := range registry { // raw registry: pre-correction values
		raw := e.Build()
		before := ContrastRatio(raw.StatusFg, raw.StatusBG)
		if before >= minStatusContrast {
			continue
		}
		corrected++
		fixed := readable(raw)
		if fixed.StatusFg == raw.StatusFg {
			t.Errorf("%s: status contrast %.2f left uncorrected", e.ID, before)
			continue
		}
		after := ContrastRatio(fixed.StatusFg, fixed.StatusBG)
		if after < minStatusContrast {
			t.Errorf("%s: correction landed at %.2f, still under the floor", e.ID, after)
		}
		// Generous ceiling: the walk steps in twentieths, so one step
		// past the floor is expected, a jump to 21:1 is not.
		if after > minStatusContrast+2.0 {
			t.Errorf("%s: correction overshot to %.2f — hue thrown away", e.ID, after)
		}
	}
	if corrected == 0 {
		t.Fatal("no palette needed correcting; readable() is no longer load-bearing")
	}
}

// TestOffshoreCharacter pins what makes the hand-tuned Offshore palette
// itself rather than one more dark port: a deep navy background (blue
// leads, everything dark), a status bar that sits in the background's
// own family instead of being a bright accent chip, and Muted text held
// to the default theme's 4.5:1 bar — Offshore is hand-tuned like Tokyo
// Night, so it doesn't get the looser ported-palette floor.
func TestOffshoreCharacter(t *testing.T) {
	th, ok := ByID("offshore")
	if !ok {
		t.Fatal("offshore missing from the registry")
	}
	r, g, b := th.BG.RGB()
	if !(b > r && b > g) {
		t.Errorf("BG %02x%02x%02x is not navy: blue must lead", r, g, b)
	}
	if r > 0x40 || g > 0x40 || b > 0x40 {
		t.Errorf("BG %02x%02x%02x is not dark enough for the deep-navy look", r, g, b)
	}
	if ratio := ContrastRatio(th.StatusBG, th.BG); ratio > 1.5 {
		t.Errorf("status bar contrasts %.2f with BG — an accent chip, not the understated bar", ratio)
	}
	for _, surface := range []struct {
		name string
		bg   tcell.Color
	}{{"BG", th.BG}, {"SidebarBG", th.SidebarBG}} {
		if ratio := ContrastRatio(th.Muted, surface.bg); ratio < 4.5 {
			t.Errorf("Muted on %s: %.2f < 4.5", surface.name, ratio)
		}
	}
}

// TestRegistryAccessorsAgree: List() and ByID() must hand out the same
// palette for the same id. The picker previews through List and the
// app applies through ByID, so a divergence would make the theme
// change the moment you pressed Enter on it.
func TestRegistryAccessorsAgree(t *testing.T) {
	for _, e := range List() {
		viaID, ok := ByID(e.ID)
		if !ok {
			t.Fatalf("ByID(%q) missed", e.ID)
		}
		if viaID != e.Build() {
			t.Errorf("%s: List() and ByID() disagree", e.ID)
		}
	}
}

// TestEveryThemeDiffTintsReadable fences the derived diff tints across
// the whole registry, stating the derivation's contract: a ROW tint may
// spend at most 8% of the contrast its surface already affords, and
// never dips under 4.0 when the palette can afford 4.0 (Solarized
// can't — its body text starts at 3.6:1, and the tint must not be
// blamed for that). An EMPHASIS tint buys loudness with a bounded
// contrast cost — it spans only the few glyphs that differ — so it gets
// an absolute 2.5 floor plus the guarantee that it actually differs
// from its row tint, which is its entire job.
func TestEveryThemeDiffTintsReadable(t *testing.T) {
	for _, e := range List() {
		th := e.Build()
		tints, ok := th.DiffTints()
		if !ok {
			t.Errorf("%s: truecolor palette must yield diff tints", e.ID)
			continue
		}
		affordable := ContrastRatio(th.Text, th.LineHL) * 0.92
		if affordable > 4.0 {
			affordable = 4.0
		}
		for _, bg := range []struct {
			name string
			c    tcell.Color
		}{{"DelRow", tints.DelRow}, {"AddRow", tints.AddRow}} {
			if r := ContrastRatio(th.Text, bg.c); r < affordable-0.01 {
				t.Errorf("%s: Text on %s contrast %.2f < affordable %.2f", e.ID, bg.name, r, affordable)
			}
		}
		for _, bg := range []struct {
			name string
			c    tcell.Color
		}{{"DelEmph", tints.DelEmph}, {"AddEmph", tints.AddEmph}} {
			if r := ContrastRatio(th.Text, bg.c); r < 2.5 {
				t.Errorf("%s: Text on %s contrast %.2f < 2.5", e.ID, bg.name, r)
			}
		}
		if tints.DelEmph == tints.DelRow || tints.AddEmph == tints.AddRow {
			t.Errorf("%s: emphasis tint must differ from its row tint", e.ID)
		}
	}
}

// TestEveryThemeFieldReadable is the registry fence for the one
// surface the user types on: the custom-action form's focused field
// paints Text over FieldBG. Through readable(), Subtle — the surface
// it used to use — measured 3.55:1 on Offshore, 2.34 on Nord, 1.81 on
// One Dark and 1.11 on Solarized Light under Text. Every palette must
// now clear WCAG AA there, or its own body contrast when the palette
// ships under 4.5 (Solarized Light's Text/BG is 4.13 by design and
// readable() never repaints Text), and the field must stay at least
// as readable as the raw Selection it is derived from.
func TestEveryThemeFieldReadable(t *testing.T) {
	for _, e := range List() {
		th := e.Build()
		floor := minFieldContrast
		if body := ContrastRatio(th.Text, th.BG); body < floor {
			floor = body
		}
		field := th.FieldBG()
		if r := ContrastRatio(th.Text, field); r < floor {
			t.Errorf("%s: Text on FieldBG %.2f < %.2f", e.ID, r, floor)
		}
		if ContrastRatio(th.Text, field) < ContrastRatio(th.Text, th.Selection) {
			t.Errorf("%s: FieldBG is less readable than the Selection it derives from", e.ID)
		}
	}
}
