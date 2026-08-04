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

import "testing"

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
