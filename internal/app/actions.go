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
// Custom actions shell out on the customAction job and land through
// handleCustomActionDone, because a slow scp or ssh must never freeze
// the event loop.

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// customActionResult is what a finished shell-out lands beside its exit
// error: the label, the captured output the report shows, and how long
// the run took so the main loop can pick a proportionate report —
// running scp / ssh inline would freeze the UI for the duration of the
// network round-trip.
type customActionResult struct {
	label    string
	output   []byte // combined stdout+stderr from the action's shell run
	duration time.Duration
}

// customActionQuietRun is how long a successful action may take before
// its completion is worth a modal rather than a flash. Under a second
// the user is still looking at the key they pressed and a status-bar
// line lands; past it their attention has moved and a flash that
// expires unseen is the same as no confirmation at all — which is
// exactly the "did my scp actually work?" complaint.
const customActionQuietRun = time.Second

// handleCustomActionDone surfaces the result of an async custom-action
// run and forces a sidebar refresh so a freshly-pulled file appears in
// the file tree without waiting for the 10-second auto-refresh tick.
//
// The report scales with how much there is to report. Failure always
// opens the info modal with the captured stderr — the prior 1-line
// flash truncated scp's actual diagnostics, which is exactly the case
// where the user most needs to read them. Success does too when the run
// printed something or took long enough that the user looked away.
// A fast, silent action still gets nothing but a flash, because
// stopping to dismiss a modal after every one-second formatter run is
// its own kind of hostile.
func (a *App) handleCustomActionDone(r customActionResult, err error) {
	if err != nil {
		a.openInfo("Action failed: "+r.label, splitErrorOutput(err, r.output))
		return
	}
	// Explicit finder invalidation, not the tick's conditional one: an
	// action (an scp, a generator) can drop files in directories the
	// tree never loaded, which the sweep's membership gate cannot see.
	a.refreshTreeNow()
	a.invalidateFinder()
	body := splitActionOutput(r.output, r.duration)
	if len(body) == 0 {
		a.flash(r.label + " — done")
		return
	}
	// refreshTreeNow ran first: openInfo takes the overlay slot, and a
	// tree tick that reconciles a changed tab must not try to open a
	// conflict prompt on top of the report we are about to show.
	a.openInfo("Action done: "+r.label, body)
}

// splitActionOutput formats a *successful* action's captured output for
// the info modal, or returns nil when a flash says everything there is
// to say. Silent runs that finished promptly are the nil case; anything
// that printed, and anything slow enough that the user's attention
// wandered, gets the modal.
func splitActionOutput(out []byte, duration time.Duration) []string {
	captured := strings.TrimRight(string(out), "\n")
	slow := duration >= customActionQuietRun
	if captured == "" && !slow {
		return nil
	}

	var body []string
	if slow {
		body = append(body, "Completed in "+duration.Round(100*time.Millisecond).String())
	}
	if captured != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, trimActionLines(captured)...)
	} else {
		body = append(body, "", "(no output)")
	}
	return append(body, actionLogFooter()...)
}

// actionOutputMaxLines caps how much captured output either report
// shows inline. Past it the info modal stops being readable and the
// actions.log pointer below is the better answer.
const actionOutputMaxLines = 8

// actionOutputMaxWidth is the widest inline output line, one cell
// narrower than the info overlay's body so nothing is clipped twice.
const actionOutputMaxWidth = 78

// trimActionLine clips one body line to the modal's width, rune-safe,
// spending the final cell on an ellipsis when anything was cut.
func trimActionLine(ln string) string {
	if runeLen(ln) <= actionOutputMaxWidth {
		return ln
	}
	return string([]rune(ln)[:actionOutputMaxWidth-1]) + "…"
}

// trimActionLines splits captured command output into modal-ready body
// lines: CR stripped, over-long lines ellipsised, and the whole thing
// capped with a pointer at the full log. Shared by the success and
// failure reports so the two can't drift apart.
func trimActionLines(captured string) []string {
	var body []string
	for _, ln := range strings.Split(captured, "\n") {
		body = append(body, trimActionLine(strings.TrimRight(ln, "\r")))
		if len(body) >= actionOutputMaxLines {
			body = append(body, "… (truncated; see actions.log)")
			break
		}
	}
	return body
}

// actionLogFooter is the closing "where's the full version?" pointer
// both reports end with, or nil when no log path is configured. It goes
// through the same width clip as the output above it — a deep
// XDG_STATE_HOME once pushed this line past the modal's body and it was
// the only line that escaped truncation.
func actionLogFooter() []string {
	logPath := customactions.LogPath()
	if logPath == "" {
		return nil
	}
	return []string{"", trimActionLine("Full output: " + logPath)}
}

// splitErrorOutput formats the action's captured output for the info
// modal: an opening line summarising the exit error, then a handful of
// lines of trimmed stderr, with the actions.log path as the closing
// line so the user knows where to find the full record. Pulled out so
// handleCustomActionDone reads as the routing decision it really is.
func splitErrorOutput(runErr error, out []byte) []string {
	body := []string{strings.TrimSpace(runErr.Error())}
	if captured := strings.TrimRight(string(out), "\n"); captured != "" {
		body = append(body, "")
		body = append(body, trimActionLines(captured)...)
	}
	return append(body, actionLogFooter()...)
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
// Either path runs on the customAction job so a slow scp / ssh can't
// freeze the UI; the landing turns into a flash on success or an info
// modal on failure.
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
// the env-var, logging, and landing wiring without diverging. The job
// is Refuse: a second action while one is still running is refused
// with a flash rather than run concurrently — of the three policies it
// is the only one that cannot silently lose the first action's report,
// which is the "did my scp actually work?" complaint the report exists
// to answer.
func (a *App) execCustomAction(act customactions.Action, promptValues map[string]string) {
	vars := a.captureActionVars()
	env := append(os.Environ(), vars.envSlice()...)
	env = append(env, promptValuesEnv(act.Prompts, promptValues)...)

	accepted := a.customAction.Start(func(context.Context) (customActionResult, error) {
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

		return customActionResult{label: act.Label, output: out, duration: duration}, runErr
	})
	if !accepted {
		a.flash("Another action is still running")
		return
	}
	a.flash(act.Label + "…")
}

// menuSave runs the Save action and dismisses the menu.
func (a *App) menuSave() {
	a.closeMenu()
	a.saveActiveTab()
}

// menuSaveAndClose saves the active tab and then closes it. The write
// goes through saveTab — the one shared save path — so Save & close
// behaves exactly like Save followed by Close: format-on-save runs, the
// git status refreshes, and a failure flashes the same message. Calling
// tab.Save() directly here is what once let this row write an
// unformatted file while the Save row above it formatted. A failed save
// aborts the close so we never drop the user's edits.
func (a *App) menuSaveAndClose() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	if a.saveTab(tab) {
		a.closeTab(tab)
	}
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

// menuMoveWordLeft walks the caret to the start of the word on its left.
// Also on Esc-b and Alt+Left; the menu row exists because that is where a
// user discovers the gesture in the first place. Boundaries come from
// editor.IsWordChar — the same predicate double-click selection uses.
func (a *App) menuMoveWordLeft() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveWordLeft(false)
	}
}

// menuMoveWordRight walks the caret to the end of the word on its right.
// Also on Esc-e and Alt+Right.
func (a *App) menuMoveWordRight() {
	a.closeMenu()
	if t := a.activeTabPtr(); t != nil {
		t.MoveWordRight(false)
	}
}

// menuGoToMatchingBracket jumps the caret to the partner of the bracket it
// is touching. Flashing on failure matters here: the row is reachable with
// the caret nowhere near a bracket, and a silent no-op would read as a
// broken menu item.
func (a *App) menuGoToMatchingBracket() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if !t.GoToMatchingBracket() {
		a.flash("No matching bracket at the caret")
	}
}

// hasMatchingBracket gates the "Go to matching bracket" row: enabled only
// when the caret is on (or just after) a bracket whose partner we found,
// so the row dims rather than promising a jump that won't happen.
func (a *App) hasMatchingBracket() bool {
	t := a.activeTabPtr()
	return t != nil && t.HasMatchingBracket()
}

// menuRefreshTree forces an immediate sidebar reload. The 10s background
// poller covers the common case; this is the ≡ → View row for the case
// it can't win — a network mount where the walk is slow enough that "I
// know it changed, look again" beats waiting for the next tick.
func (a *App) menuRefreshTree() {
	a.closeMenu()
	a.refreshTree()
	a.flash("File tree refreshed")
}

// menuToggleSidebar shows or hides the file explorer panel. The editor and
// tab bar reflow to fill the freed cells when the panel is hidden, and
// snap back when it returns.
//
// This is the explicit choice, so it also retires any pending automatic
// restore: whichever way the user just flipped the panel, widening the
// terminal later must not override it. See applyResponsiveSidebar.
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
	a.sidebarAutoHidden = false
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

// menuToggleGitignore flips whether the file tree hides entries the
// project's .gitignore files exclude, then persists the choice. The
// refresh is what puts it on screen: the filter runs as each directory
// is read, so the tree only changes shape on the next read.
//
// A persistence failure still applies the toggle in-memory — same
// ordering as the wrap row, the view change first and the config write
// second.
func (a *App) menuToggleGitignore() {
	a.closeMenu()
	// The Esc leader can't reach this row today, but menuToggleSidebar's
	// guard is here for the same reason: nothing in single-file mode
	// should be able to dereference a nil tree.
	if a.tree == nil {
		a.flash("No file explorer in single-file mode")
		return
	}
	a.tree.HideIgnored = !a.tree.HideIgnored
	a.refreshTree()
	a.invalidateFinder()
	if err := userconfig.SetGitignore(userconfig.DefaultPath(), a.tree.HideIgnored); err != nil {
		a.flash("config: " + err.Error())
		return
	}
	if a.tree.HideIgnored {
		a.flash("Hiding files the project ignores")
	} else {
		a.flash("Showing files the project ignores")
	}
}

// gitignoreToggleLabel returns the row's label for the current state —
// the action it will perform, matching the wrap and sidebar rows.
func (a *App) gitignoreToggleLabel() string {
	if a.tree != nil && !a.tree.HideIgnored {
		return "Hide ignored files"
	}
	return "Show ignored files"
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
		// Find the one tab needing attention so we can name it in the
		// modal — dirtyTabCount counts Dirty||DiskGone, so this loop
		// must use the same gate or a DiskGone-only tab leaves message
		// empty.
		for _, tab := range a.tabs.Tabs() {
			if tab.Dirty || tab.DiskGone {
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
