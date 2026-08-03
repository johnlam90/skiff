// =============================================================================
// File: internal/app/formmodal.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// formmodal.go opens the multi-field form overlay (overlay.Form) that
// custom actions use to collect input. Custom actions opt in by listing
// prompts in their config; before the action's shell command runs, the
// editor opens this overlay, lets the user fill in each field, and only
// on Submit does the action actually execute. The submitted values are
// exported to the shell as env vars named after the prompt keys.

package app

import (
	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/overlay"
)

// openForm displays the form overlay for a custom action — an
// overlay.Form. The caller passes the prompt list straight from
// customactions.Action; each Default is expanded through the
// editor-state vars here so the overlay sees only fully-resolved
// strings. callback receives the per-key value map only on Submit;
// Cancel discards everything. Rows are rebuilt from prompts on every
// open, so a previous form can never leak data into the next one.
func (a *App) openForm(title string, prompts []customactions.Prompt, callback func(*App, map[string]string)) {
	if len(prompts) == 0 {
		return
	}
	a.closeAllModals()

	vars := a.captureActionVars()
	f := &overlay.Form{Title: title, Theme: a.theme}
	f.Size = func() (int, int) { return a.width, a.height }
	f.Close = func() { a.closeAllModals() }
	if callback != nil {
		f.OnSubmit = func(v map[string]string) { callback(a, v) }
	}
	for _, p := range prompts {
		row := overlay.FormRow{Key: p.Key, Label: p.Label}
		switch p.Type {
		case customactions.PromptText:
			row.Field.SetText(vars.expand(p.Default))
		case customactions.PromptSelect:
			// Select state is the option index. Default matches by
			// equality against an option string when present; otherwise
			// the first option, so the user always sees a valid choice.
			row.Options = append([]string(nil), p.Options...)
			expanded := vars.expand(p.Default)
			for j, opt := range p.Options {
				if opt == expanded {
					row.Sel = j
					break
				}
			}
		}
		f.Rows = append(f.Rows, row)
	}
	a.overlays.Open(f)
}
