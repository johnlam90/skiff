// =============================================================================
// File: internal/app/gotoline.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// gotoline.go owns the "Go to line" flow: the ≡ menu / Esc-l entry that
// opens a prompt modal, the input parsing, and the OpenFileAtLine helper
// that the CLI (`skiff file:42`) and project search funnel through. The
// jump itself lives on Tab (JumpToLine / CenterOnCursor) so it stays
// testable without an App.

package app

import (
	"fmt"
	"strconv"
	"strings"
)

// menuGoToLine opens the go-to-line prompt for the active tab. The hint
// shows the valid range so the user doesn't have to guess how long the
// file is.
func (a *App) menuGoToLine() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	hint := fmt.Sprintf("1 – %d", tab.Buffer.LineCount())
	a.openPrompt("Go to line", hint, "", func(app *App, value string) {
		app.doGoToLine(value)
	})
}

// doGoToLine parses the prompt's value and jumps the active tab.
// Garbage input flashes instead of guessing — a goto that lands
// somewhere unexpected is worse than one that visibly refuses.
func (a *App) doGoToLine(value string) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		a.flash("Go to line needs a positive number")
		return
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	tab.JumpToLine(n)
	_, h := a.editorSize()
	tab.CenterOnCursor(h)
}

// OpenFileAtLine opens path (or activates its existing tab) and jumps to
// the 1-based line when line > 0. Centering is best-effort: before the
// first draw the editor height is unknown and CenterOnCursor no-ops, in
// which case the first Render's EnsureVisible still shows the line.
func (a *App) OpenFileAtLine(path string, line int) {
	a.openFile(path)
	if line <= 0 {
		return
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	tab.JumpToLine(line)
	_, h := a.editorSize()
	tab.CenterOnCursor(h)
}
