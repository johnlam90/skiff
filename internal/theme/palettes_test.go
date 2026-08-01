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
		// Status bar: ≥ 1.8 only. Two upstream palettes (ayu-light,
		// everforest-light) intentionally use white-on-color chips at
		// ~1.9-2.2:1 — bold bar text, ayu's own UI does the same — so
		// the fence just catches genuinely broken pairings.
		if r := ContrastRatio(th.StatusFg, th.StatusBG); r < 1.8 {
			t.Errorf("%s: status bar contrast %.2f < 1.8", e.ID, r)
		}
	}
}
