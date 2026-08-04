// =============================================================================
// File: internal/app/actions.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// actions.go holds the action-menu handlers — one function per menu row —
// plus the custom-action runner behind them. Each menu* function is the
// full user-facing action: do the work, close the menu, flash the result.
// The operations themselves live in tabops.go and the editor package; this
// file is the wiring from a menu row to a behavior.
//
// Custom actions shell out on a goroutine and report back through
// customActionDoneEvent, because a slow scp or ssh must never freeze the
// event loop.

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// customActionDoneEvent is posted by runCustomAction when its background
// shell-out finishes. Carries the label and any error so the main loop
// can flash a sensible status message — running scp / ssh inline would
// freeze the UI for the duration of the network round-trip.
type customActionDoneEvent struct {
	when   time.Time
	label  string
	err    error
	output []byte // combined stdout+stderr from the action's shell run
}

// When satisfies the tcell.Event interface.
func (e *customActionDoneEvent) When() time.Time { return e.when }

// handleCustomActionDone surfaces the result of an async custom-action
// run. Success flashes a brief confirmation and forces a sidebar
// refresh so a freshly-pulled file appears in the file tree without
// waiting for the 10-second auto-refresh tick. Failure opens an info
// modal with the captured stderr — the prior 1-line flash truncated
// scp's actual diagnostics, which is exactly the case where the user
// most needs to read them.
func (a *App) handleCustomActionDone(e *customActionDoneEvent) {
	if e.err != nil {
		title := "Action failed: " + e.label
		body := splitErrorOutput(e.err, e.output)
		a.openInfo(title, body)
		return
	}
	a.flash(e.label + " — done")
	a.refreshTreeNow()
}

// splitErrorOutput formats the action's captured output for the info
// modal: an opening line summarising the exit error, then up to a
// handful of lines of trimmed stderr, with the actions.log path as
// the closing line so the user knows where to find the full record.
// Pulled out so handleCustomActionDone reads as the routing decision
// it really is.
func splitErrorOutput(runErr error, out []byte) []string {
	const maxLines = 8
	const maxLineWidth = 78

	body := []string{strings.TrimSpace(runErr.Error())}
	captured := strings.TrimRight(string(out), "\n")
	if captured != "" {
		body = append(body, "")
		count := 0
		for _, ln := range strings.Split(captured, "\n") {
			ln = strings.TrimRight(ln, "\r")
			if runeLen(ln) > maxLineWidth {
				ln = string([]rune(ln)[:maxLineWidth-1]) + "…"
			}
			body = append(body, ln)
			count++
			if count >= maxLines {
				body = append(body, "… (truncated; see actions.log)")
				break
			}
		}
	}
	if logPath := customactions.LogPath(); logPath != "" {
		body = append(body, "", "Full output: "+logPath)
	}
	return body
}

// menuUndo rolls the active tab back one undo step.
func (a *App) menuUndo() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if !t.Undo() {
		a.flash("Nothing to undo")
	}
}

// menuRedo re-applies the most recently undone step.
func (a *App) menuRedo() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if !t.Redo() {
		a.flash("Nothing to redo")
	}
}

// menuRevert rewinds the active tab all the way back to the buffer
// state we captured the moment the file was opened (or last reloaded).
// The pre-revert state goes onto the undo stack so an accidental click
// is recoverable with one Undo.
func (a *App) menuRevert() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if !t.RevertFile() {
		a.flash("File matches its on-open state — nothing to revert")
		return
	}
	a.flash("Reverted to on-open state — Undo to recover")
}

// runCustomAction executes the custom action at idx. When the action
// declares prompts, the form modal opens first and the shell command
// runs only after the user submits — values are exported as KEY=VALUE
// env vars named after each prompt's Key. When prompts is empty the
// command runs immediately (the historical behaviour) and the action
// requires an open tab so $FILE / $FILENAME aren't blank.
//
// Either path runs in a goroutine so a slow scp / ssh can't freeze
// the UI; completion fires a customActionDoneEvent the main loop
// turns into a flash on success or an info modal on failure.
func (a *App) runCustomAction(idx int) {
	a.closeMenu()
	if idx < 0 || idx >= len(a.customActions) {
		return
	}
	act := a.customActions[idx]

	// No "is a file open?" guard: custom actions are user-defined
	// shell, and we don't second-guess what their command line
	// needs. A $FILE-dependent command run without a tab open will
	// fail with a real error and route through the info modal,
	// which is more honest than disabling actions like
	// "brew upgrade ..." that don't touch FILE at all.
	if len(act.Prompts) == 0 {
		a.execCustomAction(act, nil)
		return
	}

	a.openForm(act.Label, act.Prompts, func(app *App, values map[string]string) {
		app.execCustomAction(act, values)
	})
}

// execCustomAction is the actual shell-out. Pulled out of
// runCustomAction so both the prompt-less and prompted paths share
// the env-var, logging, and event-posting wiring without diverging.
func (a *App) execCustomAction(act customactions.Action, promptValues map[string]string) {
	vars := a.captureActionVars()
	env := append(os.Environ(), vars.envSlice()...)
	env = append(env, promptValuesEnv(act.Prompts, promptValues)...)

	a.flash(act.Label + "…")
	scr := a.screen
	go func() {
		started := time.Now()
		cmd := exec.Command("sh", "-c", act.Command)
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		duration := time.Since(started)

		// Always log — success or failure — so the user can scroll
		// back through actions.log when something goes sideways.
		// Best-effort: a log-write failure must not eat the action's
		// real error.
		_ = customactions.AppendLog(customactions.LogPath(), customactions.RunRecord{
			Time:     started,
			Duration: duration,
			Label:    act.Label,
			Command:  act.Command,
			File:     vars.File,
			Filename: vars.Filename,
			ExitErr:  runErr,
			Output:   out,
		})

		_ = scr.PostEvent(&customActionDoneEvent{
			when:   time.Now(),
			label:  act.Label,
			err:    runErr,
			output: out,
		})
	}()
}

// menuSave runs the Save action and dismisses the menu.
func (a *App) menuSave() {
	a.closeMenu()
	a.saveActiveTab()
}

// menuSaveAndClose saves the active tab and then closes it. If the save
// fails the close is aborted so we don't lose the user's edits.
func (a *App) menuSaveAndClose() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	if err := tab.Save(); err != nil {
		a.flash(fmt.Sprintf("Save failed: %v", err))
		return
	}
	a.refreshGitStatusAsync()
	a.flash(fmt.Sprintf("Saved %s — closed", filepath.Base(tab.Path)))
	a.closeTab(a.tabs.Active())
}

// menuClose closes the active tab via the same dirty-tab confirmation flow
// used by clicking the × on the tab.
func (a *App) menuClose() {
	a.closeMenu()
	a.requestCloseTab(a.tabs.Active())
}

// menuCopy copies the current selection.
func (a *App) menuCopy() {
	a.closeMenu()
	a.copySelection()
}

// menuCut cuts the current selection.
func (a *App) menuCut() {
	a.closeMenu()
	a.cutSelection()
}

// menuPaste pastes the editor's internal clipboard at the cursor.
func (a *App) menuPaste() {
	a.closeMenu()
	a.pasteClipboard()
}

// menuToggleLineComment comments or uncomments the active line selection.
func (a *App) menuToggleLineComment() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	changed, ok := tab.ToggleLineComment()
	if !ok {
		a.flash("No line comment syntax for this file")
		return
	}
	if !changed {
		a.flash("No non-blank lines to comment")
		return
	}
	a.flash("Toggled line comment")
}

// menuMoveLineUp nudges the cursor line / selected block up one line.
func (a *App) menuMoveLineUp() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveLinesUp()
	}
}

// menuMoveLineDown nudges the cursor line / selected block down one line.
func (a *App) menuMoveLineDown() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveLinesDown()
	}
}

// menuDuplicateLine copies the cursor line / selected block below itself.
func (a *App) menuDuplicateLine() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.DuplicateLines()
	}
}

// menuRefreshTree forces an immediate sidebar reload. Currently unwired
// from the menu — the 10s background poller covers the common case — but
// the method is kept so re-adding the menu row (see menuItems) only
// requires uncommenting one line.
func (a *App) menuRefreshTree() {
	a.closeMenu()
	a.refreshTree()
	a.flash("File tree refreshed")
}

// menuToggleSidebar shows or hides the file explorer panel. The editor and
// tab bar reflow to fill the freed cells when the panel is hidden, and
// snap back when it returns.
func (a *App) menuToggleSidebar() {
	a.closeMenu()
	// Single-file mode has no file tree, so there's nothing to show or
	// hide. The menu row is hidden (hasTree), but the Esc-t leader reaches
	// here directly — guard it so the toggle can't flip sidebarShown true
	// and send draw() into a.tree.Render on a nil tree.
	if a.tree == nil {
		a.flash("No file explorer in single-file mode")
		return
	}
	a.sidebarShown = !a.sidebarShown
}

// sidebarToggleLabel returns the label the toggle row should display given
// the current sidebar state. Drawn dynamically by drawMenu.
func (a *App) sidebarToggleLabel() string {
	if a.sidebarShown {
		return "Hide file explorer"
	}
	return "Show file explorer"
}

// menuToggleWrap flips soft wrap for every open tab and persists the
// preference, so it holds for future tabs and future sessions alike.
// A persistence failure still applies the toggle in-memory — the user
// asked for a view change first, a config write second.
func (a *App) menuToggleWrap() {
	a.closeMenu()
	a.wrapOn = !a.wrapOn
	for _, t := range a.tabs.Tabs() {
		t.SetWrap(a.wrapOn)
	}
	if err := userconfig.SetWrap(userconfig.DefaultPath(), a.wrapOn); err != nil {
		a.flash("config: " + err.Error())
		return
	}
	if a.wrapOn {
		a.flash("Long lines now wrap")
	} else {
		a.flash("Long lines now scroll sideways")
	}
}

// wrapToggleLabel returns the wrap row's label for the current state.
// Drawn dynamically by drawMenu, same pattern as the sidebar toggle.
func (a *App) wrapToggleLabel() string {
	if a.wrapOn {
		return "Unwrap long lines"
	}
	return "Wrap long lines"
}

// menuQuit exits the editor. When any tab has unsaved changes, opens the
// dirty-close modal so the user can pick Save (save all then quit),
// Discard (quit anyway), or Cancel. With no dirty tabs we exit straight
// away.
func (a *App) menuQuit() {
	a.closeMenu()
	dirty := a.dirtyTabCount()
	if dirty == 0 {
		a.quit = true
		return
	}
	var message string
	if dirty == 1 {
		// Find the one dirty tab so we can name it in the modal.
		for _, tab := range a.tabs.Tabs() {
			if tab.Dirty {
				name := filepath.Base(tab.Path)
				if name == "" || name == "." {
					name = "untitled"
				}
				message = name + " has unsaved changes. Save before quitting?"
				break
			}
		}
	} else {
		message = fmt.Sprintf("%d files have unsaved changes. Save all before quitting?", dirty)
	}
	a.openDirtyClose(
		"Unsaved changes",
		message,
		func(app *App) {
			// Only quit if every save succeeded — a half-saved exit
			// would silently lose work on whichever tab failed.
			if app.saveAllDirty() {
				app.quit = true
			}
		},
		func(app *App) { app.quit = true },
	)
}
