// =============================================================================
// File: internal/app/listpick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Wiring tests for openListPick — the pick overlay's own behavior
// (filtering, hooks, mouse, caret window) is pinned in internal/overlay;
// here we pin the App bridge and provide the pick helpers flow tests
// share.

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

// pickPrefab returns the open pick overlay, failing the test when none
// is up.
func pickPrefab(t *testing.T, a *App) *overlay.Pick {
	t.Helper()
	p, ok := a.overlays.Top().(*overlay.Pick)
	if !ok {
		t.Fatalf("no pick overlay open; top = %T", a.overlays.Top())
	}
	return p
}

// pickIsOpen reports whether a pick overlay is up.
func pickIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*overlay.Pick)
	return ok
}

// pickChoose highlights row idx (into the filtered view) and presses
// Enter through real routing.
func pickChoose(t *testing.T, a *App, idx int) {
	t.Helper()
	pickPrefab(t, a).Selected = idx
	a.handleKey(keyEv(tcell.KeyEnter, 0))
}

// TestOpenListPick_WiresHooksAndSnapsToCurrent pins the App bridge:
// items land on the prefab, the highlight starts on the Current row,
// and the OnPick wrapper delivers the original index to the App-side
// callback.
func TestOpenListPick_WiresHooksAndSnapsToCurrent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	picked := -1
	a.openListPick("T", []listPickItem{
		{Label: "one"}, {Label: "two", Current: true},
	}, func(_ *App, i int) { picked = i }, nil, nil)
	p := pickPrefab(t, a)
	if p.Selected != 1 {
		t.Fatalf("highlight should snap to Current, got %d", p.Selected)
	}
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if picked != 1 {
		t.Fatalf("OnPick wrapper should deliver the original index, got %d", picked)
	}
	if pickIsOpen(a) {
		t.Fatal("pick should close on confirm")
	}
}
