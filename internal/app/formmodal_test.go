// =============================================================================
// File: internal/app/formmodal_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Wiring tests for openForm — the overlay.Form's own behavior (focus
// cycling, editing, select cycling, mouse, submit ordering) is pinned
// in internal/overlay's form tests; here we pin what the App adds:
// default expansion through the editor-state vars, fresh rows per open,
// and the callback bridge.

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/overlay"
)

// formPrefab returns the open form overlay, failing the test when none
// is up.
func formPrefab(t *testing.T, a *App) *overlay.Form {
	t.Helper()
	f, ok := a.overlays.Top().(*overlay.Form)
	if !ok {
		t.Fatalf("no form overlay open; top = %T", a.overlays.Top())
	}
	return f
}

// formIsOpen reports whether a form overlay is up.
func formIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*overlay.Form)
	return ok
}

// scpPrompts is the worked example from the docs / README — keeps
// the form tests grounded in the actual user-facing schema rather
// than a synthetic shape that drifts.
func scpPrompts() []customactions.Prompt {
	return []customactions.Prompt{
		{Key: "HOST", Label: "Host", Type: customactions.PromptSelect,
			Options: []string{"cascade", "rager"}},
		{Key: "DEST_DIR", Label: "Local destination", Type: customactions.PromptText,
			Default: "${ACTIVE_FOLDER}"},
		{Key: "REMOTE_SRC", Label: "Remote file", Type: customactions.PromptText},
	}
}

// TestOpenForm_ResolvesDefaults pins the open contract: every Default
// string is expanded through the editor-state vars before the overlay
// is rendered. Without this the user would see literal
// "${ACTIVE_FOLDER}" text in the input — a regression we'd notice only
// after running the action.
func TestOpenForm_ResolvesDefaults(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openForm("Copy from remote", scpPrompts(), func(*App, map[string]string) {})
	v := formPrefab(t, a).Values()
	if got := v["DEST_DIR"]; got == "" || got == "${ACTIVE_FOLDER}" {
		t.Fatalf("DEST_DIR default not expanded, got %q", got)
	}
	// Select prompts initialise to the matching option, or the first
	// option when no Default was provided. HOST has no Default so we
	// should land on "cascade".
	if v["HOST"] != "cascade" {
		t.Errorf("HOST initial value = %q, want %q", v["HOST"], "cascade")
	}
}

// TestOpenForm_RebuildsState ensures opening a second form doesn't
// inherit the first form's rows or focus — each open constructs a
// fresh overlay.Form, so state can't leak between instances.
func TestOpenForm_RebuildsState(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.openForm("First", []customactions.Prompt{
		{Key: "ALPHA", Label: "A", Type: customactions.PromptText, Default: "first"},
	}, nil)
	if v := formPrefab(t, a).Values(); v["ALPHA"] != "first" {
		t.Fatalf("first ALPHA = %q", v["ALPHA"])
	}

	a.openForm("Second", []customactions.Prompt{
		{Key: "BETA", Label: "B", Type: customactions.PromptText, Default: "second"},
	}, nil)
	f := formPrefab(t, a)
	v := f.Values()
	if _, ok := v["ALPHA"]; ok {
		t.Errorf("ALPHA leaked from previous form: %v", v)
	}
	if v["BETA"] != "second" {
		t.Errorf("BETA = %q, want %q", v["BETA"], "second")
	}
	if f.Focus != 0 {
		t.Errorf("focus should reset to 0, got %d", f.Focus)
	}
}

// TestOpenForm_SubmitBridgesCallback pins the callback bridge: the
// values collected by the overlay arrive at the App-side callback.
func TestOpenForm_SubmitBridgesCallback(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var got map[string]string
	a.openForm("T", []customactions.Prompt{
		{Key: "K", Label: "k", Type: customactions.PromptText, Default: "v"},
	}, func(_ *App, v map[string]string) { got = v })
	a.handleKey(keyEv(tcell.KeyEnter, 0)) // single row — Enter submits
	if got == nil || got["K"] != "v" {
		t.Fatalf("callback values = %v", got)
	}
	if formIsOpen(a) {
		t.Fatal("submit should close the form")
	}
}

// TestAnyModalOpen_IncludesForm keeps the form counted as an overlay so
// editor input stays short-circuited while it's up.
func TestAnyModalOpen_IncludesForm(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.anyModalOpen() {
		t.Fatal("none open")
	}
	a.openForm("T", scpPrompts(), nil)
	if !a.anyModalOpen() {
		t.Fatal("form should count as an open overlay")
	}
	a.closeAllModals()
	if a.anyModalOpen() {
		t.Fatal("closeAllModals should close the form")
	}
}
