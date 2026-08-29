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
	"sort"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/search"
)

// projReplaceDoneEvent reports a finished background disk apply,
// carrying the buffer-side counts so the user gets ONE combined flash.
// bufSaveFailed names the open buffers we rewrote but could not write
// back — those tabs are still dirty and the report has to say so, or
// the user reads a success count that covers files nothing reached.
type projReplaceDoneEvent struct {
	when                      time.Time
	rep                       search.ReplaceReport
	bufOcc, bufFiles, bufSkip int
	bufSaveFailed             []string
}

// When implements tcell.Event.
func (e *projReplaceDoneEvent) When() time.Time { return e.when }

// resetProjReplace clears the replace field state. Every teardown
// funnels through closeProjFind, which calls this — including
// closeAllModals, so an overlay opening over the panel can't leave the
// replace field armed underneath it.
func (a *App) resetProjReplace() {
	a.projFind.replaceOpen = false
	a.projFind.replace = overlay.Field{}
	a.projFind.focusReplace = false
}

// projReplaceToggleFocus is the Tab gesture: first press grows the
// replace field and focuses it, later presses hop between the fields.
func (a *App) projReplaceToggleFocus() {
	if !a.projFind.replaceOpen {
		a.projFind.replaceOpen = true
		a.projFind.focusReplace = true
		return
	}
	a.projFind.focusReplace = !a.projFind.focusReplace
}

// projReplaceOpts snapshots the panel's mode chips into engine options.
func (a *App) projReplaceOpts() search.Options {
	opts := search.DefaultOptions()
	opts.MatchCase = a.projFind.findMatchCase
	opts.WholeWord = a.projFind.findWholeWord
	opts.Regex = a.projFind.findRegex
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
	if a.projFind.findSelected < 0 || a.projFind.findSelected >= len(rows) {
		return
	}
	row := rows[a.projFind.findSelected]
	if row.IsHeader {
		a.projFindToggleFold(row.Path)
		return
	}
	a.projReplaceRowApply(row)
}

// findOpenTab returns the open tab backing abs (nil when the file
// isn't open) plus whether it was clean before we touch it.
func (a *App) findOpenTab(abs string) (*editor.Tab, bool) {
	for _, t := range a.tabs.Tabs() {
		if t.Path == abs && !t.IsImage() {
			return t, !t.Dirty
		}
	}
	return nil, false
}

// applyMatchesToTab rewrites a tab's matched lines in one undo step,
// verifying each line against what the sweep recorded first. Matches are
// per-occurrence, but staging never mutates tab.Buffer.Lines until the
// end — so unlike the disk path, a naive per-match loop wouldn't even
// see its own edits, it would DOUBLE-count: a second same-line match
// would still verify clean against the untouched buffer and add its own
// ReplaceLine pass on top of the first. Group by line and touch each one
// once, exactly as ApplyReplace does on disk.
func applyMatchesToTab(tab *editor.Tab, group []search.Match, query, repl string, opts search.Options) (occ, skipped int) {
	byLine := map[int][]search.Match{}
	var lineOrder []int
	for _, m := range group {
		if _, seen := byLine[m.Line]; !seen {
			lineOrder = append(lineOrder, m.Line)
		}
		byLine[m.Line] = append(byLine[m.Line], m)
	}

	newLines := map[int]string{}
	for _, ln := range lineOrder {
		ms := byLine[ln]
		i := ln - 1
		if i < 0 || i >= tab.Buffer.LineCount() || !search.VerifyLine(tab.Buffer.Lines[i], ms[0].Text) {
			skipped += len(ms)
			continue
		}
		nl, n := search.ReplaceLine(tab.Buffer.Lines[i], query, repl, opts)
		if n == 0 {
			skipped += len(ms)
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

// applyMatchAtTab rewrites exactly the occurrence m names in tab's
// buffer via ReplaceLineAt, verifying the recorded line first. Unlike
// applyMatchesToTab (whole-line ReplaceLine, used for the group/confirm-
// all path where "replace" means every occurrence on the line) this
// targets one occurrence, so applying a single panel row can't consume
// a sibling occurrence recorded on the same line.
func applyMatchAtTab(tab *editor.Tab, m search.Match, query, repl string, opts search.Options) (occ, skipped int) {
	i := m.Line - 1
	if i < 0 || i >= tab.Buffer.LineCount() || !search.VerifyLine(tab.Buffer.Lines[i], m.Text) {
		return 0, 1
	}
	nl, n := search.ReplaceLineAt(tab.Buffer.Lines[i], m.Col, query, repl, opts)
	if n == 0 {
		return 0, 1
	}
	tab.ReplaceLines(map[int]string{i: nl})
	return n, 0
}

// projReplaceRowApply rewrites one match row: through the open buffer
// when the file is up (clean tabs re-save, dirty tabs stay dirty with
// the change applied), through the verified disk path otherwise. Both
// paths target only the row's own occurrence — a sibling row recorded on
// the same line is left untouched.
func (a *App) projReplaceRowApply(row projFindRow) {
	if row.MatchIdx < 0 || row.MatchIdx >= len(a.projFind.findMatches) {
		return
	}
	m := a.projFind.findMatches[row.MatchIdx]
	query, repl := a.projFind.query.Text(), a.projFind.replace.Text()
	opts := a.projReplaceOpts()
	abs := filepath.Join(a.rootDir, filepath.FromSlash(m.Path))
	if tab, wasClean := a.findOpenTab(abs); tab != nil {
		occ, skipped := applyMatchAtTab(tab, m, query, repl, opts)
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
		rep := search.ApplyReplaceAt(a.rootDir, m, query, repl, opts)
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
	if len(a.projFind.findMatches) == 0 {
		a.flash("Nothing to replace")
		return
	}
	files := map[string]bool{}
	for _, m := range a.projFind.findMatches {
		files[m.Path] = true
	}
	msg := fmt.Sprintf(
		"Replace %d match(es) in %d file(s)? Files change on disk — commit or stash first if unsure.",
		len(a.projFind.findMatches), len(files))
	if a.projFind.findRegex {
		msg += " Regex replacements expand $1 / ${name} groups ($$ for a literal $)."
	}
	// Snapshot everything the apply needs — the confirm modal closes
	// the panel (closeAllModals), taking the live state with it.
	matches := append([]search.Match(nil), a.projFind.findMatches...)
	query, repl := a.projFind.query.Text(), a.projFind.replace.Text()
	opts := a.projReplaceOpts()
	a.openConfirm("Replace in project", msg, func(app *App) {
		app.doProjReplaceAll(matches, query, repl, opts)
	})
}

// doProjReplaceAll applies everywhere: open buffers synchronously (the
// editor owns them), closed files in the background behind the
// replace's own gate (projReplaceBusy — not the file clipboard's, the
// two are unrelated). One combined report lands via projReplaceDoneEvent.
//
// Re-saving a clean tab can fail (the file turned into a directory, the
// disk filled, permissions changed under us). Those failures are
// collected rather than dropped: the buffer keeps the replacement and
// stays dirty, and the report names the file. Reporting a bare success
// count for a write that never landed is the one outcome worse than the
// failure itself.
func (a *App) doProjReplaceAll(matches []search.Match, query, repl string, opts search.Options) {
	if a.projReplaceBusy {
		a.flash("Another replace is still running")
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
	var saveFailed []string
	for tab, group := range byTab {
		occ, skipped := applyMatchesToTab(tab, group, query, repl, opts)
		bufOcc += occ
		bufSkip += skipped
		if occ > 0 {
			bufFiles++
			if tabClean[tab] {
				if err := tab.Save(); err != nil {
					saveFailed = append(saveFailed, tab.DisplayName())
				}
			}
		}
	}
	// Map iteration order is random, so sort the names — the same
	// failure must not produce a different message on each run.
	sort.Strings(saveFailed)
	root := a.rootDir
	scr := a.screen
	a.projReplaceBusy = true
	a.safeGo("project-replace", func() {
		rep := search.ApplyReplace(root, diskMatches, query, repl, opts)
		_ = scr.PostEvent(&projReplaceDoneEvent{
			when: time.Now(), rep: rep,
			bufOcc: bufOcc, bufFiles: bufFiles, bufSkip: bufSkip,
			bufSaveFailed: saveFailed,
		})
	})
}

// handleProjReplaceDone lands the combined report and refreshes
// everything the rewrite touched. The flash is deliberately the LAST
// thing this does: refreshTreeNow reconciles open tabs against disk and
// flashes its own warning for any tab that now disagrees with the file
// — which is exactly the tab whose save just failed. Flashing first
// would let that generic warning bury the specific report.
func (a *App) handleProjReplaceDone(e *projReplaceDoneEvent) {
	a.projReplaceBusy = false
	occ := e.rep.Replaced + e.bufOcc
	files := e.rep.Files + e.bufFiles
	skipped := e.rep.Skipped + e.bufSkip
	msg := fmt.Sprintf("Replaced %d in %d file(s)", occ, files)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d skipped — changed since search)", skipped)
	}
	if len(e.bufSaveFailed) > 0 {
		msg += fmt.Sprintf(" — save failed for %s, still unsaved",
			strings.Join(e.bufSaveFailed, ", "))
	}
	a.refreshTreeNow()
	if a.projFindOpen() {
		a.projFindQueryChanged()
	}
	a.flash(msg)
}
