// =============================================================================
// File: internal/app/projreplace.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// projreplace.go is project-wide replace, riding the Esc-F panel: Tab
// grows a replace field in the bar, the results overlay is the preview,
// Enter rewrites the selected line, Shift+Enter (or [ All ]) rewrites
// everything behind one confirm. Open buffers route through the editor
// (one undo step per file, dirty tabs stay dirty); closed files go
// through search.ApplyReplace, which re-verifies every line before
// touching it and writes atomically. See the tranche-4 design in
// docs/superpowers/plans for the full safety model.

package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/search"
)

// projReplaceDoneEvent reports a finished background disk apply,
// carrying the buffer-side counts so the user gets ONE combined flash.
type projReplaceDoneEvent struct {
	when                      time.Time
	rep                       search.ReplaceReport
	bufOcc, bufFiles, bufSkip int
}

// When implements tcell.Event.
func (e *projReplaceDoneEvent) When() time.Time { return e.when }

// resetProjReplace clears the replace field state — panel open/close
// and closeAllModals all funnel through here.
func (a *App) resetProjReplace() {
	a.projReplaceOpen = false
	a.projReplaceValue = nil
	a.projReplaceCursor = 0
	a.projFocusReplace = false
}

// projReplaceToggleFocus is the Tab gesture: first press grows the
// replace field and focuses it, later presses hop between the fields.
func (a *App) projReplaceToggleFocus() {
	if !a.projReplaceOpen {
		a.projReplaceOpen = true
		a.projFocusReplace = true
		return
	}
	a.projFocusReplace = !a.projFocusReplace
}

// projReplaceOpts snapshots the panel's mode chips into engine options.
func (a *App) projReplaceOpts() search.Options {
	opts := search.DefaultOptions()
	opts.MatchCase = a.projFindMatchCase
	opts.WholeWord = a.projFindWholeWord
	opts.Regex = a.projFindRegex
	return opts
}

// projReplaceEnter routes Enter while the replace field is focused:
// plain Enter rewrites the selected match row, Shift+Enter opens the
// replace-everything confirm.
func (a *App) projReplaceEnter(all bool) {
	if all {
		a.projReplaceConfirmAll()
		return
	}
	rows := a.projFindRows()
	if a.projFindSelected < 0 || a.projFindSelected >= len(rows) {
		return
	}
	row := rows[a.projFindSelected]
	if row.IsHeader {
		a.projFindToggleFold(row.Path)
		return
	}
	a.projReplaceRowApply(row)
}

// findOpenTab returns the open tab backing abs (nil when the file
// isn't open) plus whether it was clean before we touch it.
func (a *App) findOpenTab(abs string) (*editor.Tab, bool) {
	for _, t := range a.tabs {
		if t.Path == abs && !t.IsImage() {
			return t, !t.Dirty
		}
	}
	return nil, false
}

// applyMatchesToTab rewrites a tab's matched lines in one undo step,
// verifying each line against what the sweep recorded first.
func applyMatchesToTab(tab *editor.Tab, group []search.Match, query, repl string, opts search.Options) (occ, skipped int) {
	newLines := map[int]string{}
	for _, m := range group {
		i := m.Line - 1
		if i < 0 || i >= tab.Buffer.LineCount() || !search.VerifyLine(tab.Buffer.Lines[i], m.Text) {
			skipped++
			continue
		}
		nl, n := search.ReplaceLine(tab.Buffer.Lines[i], query, repl, opts)
		if n == 0 {
			skipped++
			continue
		}
		newLines[i] = nl
		occ += n
	}
	if len(newLines) > 0 {
		tab.ReplaceLines(newLines)
	}
	return occ, skipped
}

// projReplaceRowApply rewrites one match row: through the open buffer
// when the file is up (clean tabs re-save, dirty tabs stay dirty with
// the change applied), through the verified disk path otherwise.
func (a *App) projReplaceRowApply(row projFindRow) {
	if row.MatchIdx < 0 || row.MatchIdx >= len(a.projFindMatches) {
		return
	}
	m := a.projFindMatches[row.MatchIdx]
	query, repl := string(a.projFindValue), string(a.projReplaceValue)
	opts := a.projReplaceOpts()
	abs := filepath.Join(a.rootDir, filepath.FromSlash(m.Path))
	if tab, wasClean := a.findOpenTab(abs); tab != nil {
		occ, skipped := applyMatchesToTab(tab, []search.Match{m}, query, repl, opts)
		if occ > 0 && wasClean {
			if err := tab.Save(); err != nil {
				a.flash(fmt.Sprintf("Replaced, but save failed: %v", err))
			}
		}
		if skipped > 0 {
			a.flash("Skipped — the line changed since the search")
		} else if occ > 0 {
			a.flash(fmt.Sprintf("Replaced %d on %s:%d", occ, m.Path, m.Line))
		}
	} else {
		rep := search.ApplyReplace(a.rootDir, []search.Match{m}, query, repl, opts)
		if rep.Skipped > 0 {
			a.flash("Skipped — the file changed since the search")
		} else {
			a.flash(fmt.Sprintf("Replaced %d on %s:%d", rep.Replaced, m.Path, m.Line))
		}
	}
	a.refreshGitStatusAsync()
	a.projFindQueryChanged()
}

// projReplaceConfirmAll states the blast radius and arms the apply.
func (a *App) projReplaceConfirmAll() {
	if len(a.projFindMatches) == 0 {
		a.flash("Nothing to replace")
		return
	}
	files := map[string]bool{}
	for _, m := range a.projFindMatches {
		files[m.Path] = true
	}
	msg := fmt.Sprintf(
		"Replace %d match(es) in %d file(s)? Files change on disk — commit or stash first if unsure.",
		len(a.projFindMatches), len(files))
	if a.projFindRegex {
		msg += " Regex mode replaces with the literal text (no $1 groups yet)."
	}
	// Snapshot everything the apply needs — the confirm modal closes
	// the panel (closeAllModals), taking the live state with it.
	matches := append([]search.Match(nil), a.projFindMatches...)
	query, repl := string(a.projFindValue), string(a.projReplaceValue)
	opts := a.projReplaceOpts()
	a.openConfirm("Replace in project", msg, func(app *App) {
		app.doProjReplaceAll(matches, query, repl, opts)
	})
}

// doProjReplaceAll applies everywhere: open buffers synchronously (the
// editor owns them), closed files in the background behind the shared
// file-op gate. One combined report lands via projReplaceDoneEvent.
func (a *App) doProjReplaceAll(matches []search.Match, query, repl string, opts search.Options) {
	if a.fileOpBusy {
		a.flash("Another file operation is still running")
		return
	}
	var diskMatches []search.Match
	byTab := map[*editor.Tab][]search.Match{}
	tabClean := map[*editor.Tab]bool{}
	for _, m := range matches {
		abs := filepath.Join(a.rootDir, filepath.FromSlash(m.Path))
		if tab, wasClean := a.findOpenTab(abs); tab != nil {
			if _, seen := byTab[tab]; !seen {
				tabClean[tab] = wasClean
			}
			byTab[tab] = append(byTab[tab], m)
			continue
		}
		diskMatches = append(diskMatches, m)
	}
	var bufOcc, bufFiles, bufSkip int
	for tab, group := range byTab {
		occ, skipped := applyMatchesToTab(tab, group, query, repl, opts)
		bufOcc += occ
		bufSkip += skipped
		if occ > 0 {
			bufFiles++
			if tabClean[tab] {
				_ = tab.Save()
			}
		}
	}
	root := a.rootDir
	scr := a.screen
	a.fileOpBusy = true
	go func() {
		rep := search.ApplyReplace(root, diskMatches, query, repl, opts)
		_ = scr.PostEvent(&projReplaceDoneEvent{
			when: time.Now(), rep: rep,
			bufOcc: bufOcc, bufFiles: bufFiles, bufSkip: bufSkip,
		})
	}()
}

// handleProjReplaceDone lands the combined report and refreshes
// everything the rewrite touched.
func (a *App) handleProjReplaceDone(e *projReplaceDoneEvent) {
	a.fileOpBusy = false
	occ := e.rep.Replaced + e.bufOcc
	files := e.rep.Files + e.bufFiles
	skipped := e.rep.Skipped + e.bufSkip
	msg := fmt.Sprintf("Replaced %d in %d file(s)", occ, files)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d skipped — changed since search)", skipped)
	}
	a.flash(msg)
	a.refreshTreeNow()
	if a.projFindOpen {
		a.projFindQueryChanged()
	}
}
