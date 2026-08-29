// =============================================================================
// File: internal/app/projfind.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// projfind.go owns the project-wide content search UI: a find-bar-style
// input row above the status bar plus a results overlay in the editor
// area, grouped by file with per-file folding (druk's search panel,
// skiff-shaped). The sweep itself lives in internal/search and runs on
// the panel's sweep job (internal/asyncjob, Supersede): every keystroke
// starts a run that debounces, then searches; the newer keystroke
// retires the older run, so stale sweeps are dropped, never rendered.

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/search"
)

// projFindDebounce is how long typing must pause before a sweep
// starts. Long enough to coalesce a burst of keystrokes, short enough
// to feel instant.
const projFindDebounce = 120 * time.Millisecond

// projFindResult is what a finished sweep lands: the matches and
// whether the engine's cap cut them short.
type projFindResult struct {
	matches   []search.Match
	truncated bool
}

// projFindRow is one rendered row of the results overlay: a file header
// (fold toggle) or a single match line.
type projFindRow struct {
	IsHeader bool
	Path     string
	MatchIdx int // index into projFind.findMatches; -1 for headers
	Count    int // header only: matches in this file
}

// projFindState bundles the project-wide content-search panel and the
// replace state riding it, moved out of App so the subsystem's fields
// have a compiler-visible boundary. Held on App as the named field
// projFind (named, not embedded: embedding would promote findOpen onto
// App and collide with the in-file find bar's field of the same name).
type projFindState struct {
	// Project-wide content search (see projfind.go).
	findOpen      bool
	findValue     []rune
	findCursor    int
	findScroll    int
	findMatches   []search.Match
	findTruncated bool
	findSelected  int
	findScrollY   int
	findFolded    map[string]bool
	findMatchCase bool
	findWholeWord bool
	findRegex     bool

	// Project-wide replace riding the panel (see projreplace.go). The
	// X ranges are stamped by drawProjFindBar for the mouse handler.
	replaceOpen                    bool
	replaceValue                   []rune
	replaceCursor                  int
	focusReplace                   bool
	replaceFieldX0, replaceFieldX1 int
	replaceAllX0, replaceAllX1     int

	// sweep is the background search: Supersede, so the newest
	// keystroke's run is the only one that can land, and Busy is the
	// bar's "searching…" state. Closing the panel invalidates it.
	sweep asyncjob.Job[projFindResult]
}

// menuFindInProject is the ≡ menu / Esc-F entry point.
func (a *App) menuFindInProject() {
	a.closeMenu()
	a.openProjFind()
}

// openProjFind opens the panel with a fresh query. Single-file mode has
// no project index, so the action degrades to a flash there.
func (a *App) openProjFind() {
	if a.finder == nil {
		a.flash("Project search needs a project (open a directory)")
		return
	}
	a.closeAllModals()
	a.projFind.findOpen = true
	a.projFind.findValue = nil
	a.projFind.findCursor = 0
	a.projFind.findScroll = 0
	a.projFind.findMatches = nil
	a.projFind.findTruncated = false
	a.projFind.findSelected = 0
	a.projFind.findScrollY = 0
	a.projFind.findFolded = map[string]bool{}
	a.resetProjReplace()
}

// closeProjFind dismisses the panel and forgets the query — same
// "Esc means done" contract as the in-file find bar.
func (a *App) closeProjFind() {
	a.projFind.findOpen = false
	a.projFind.findValue = nil
	a.projFind.findCursor = 0
	a.projFind.findScroll = 0
	a.projFind.findMatches = nil
	a.projFind.findTruncated = false
	a.projFind.findFolded = nil
	a.resetProjReplace()
	// Retire any in-flight sweep: its landing is dropped and its walk
	// told to stop.
	a.projFind.sweep.Invalidate()
}

// projFindQueryChanged starts a background sweep for the current input.
// The sweep job is Supersede, so every keystroke retires the run before
// it — the older run's walk stops at its next cancellation check and
// its landing is dropped — and fast typing never renders stale hits.
//
// The run debounces before it reads anything: the sweep proper starts
// only after the typing pauses, so a burst of keystrokes costs a burst
// of goroutines that exit at the debounce, not a full disk walk each.
// The file index is read after the debounce too, on the worker — the
// finder is safe from any goroutine, and copying a 50k-entry list per
// keystroke would be the wrong side of that trade.
func (a *App) projFindQueryChanged() {
	query := string(a.projFind.findValue)
	if strings.TrimSpace(query) == "" {
		a.projFind.sweep.Invalidate()
		a.projFind.findMatches = nil
		a.projFind.findTruncated = false
		a.projFind.findSelected = 0
		a.projFind.findScrollY = 0
		return
	}
	index := a.finder
	root := a.rootDir
	matchCase, wholeWord, regex := a.projFind.findMatchCase, a.projFind.findWholeWord, a.projFind.findRegex
	a.projFind.sweep.Start(func(ctx context.Context) (projFindResult, error) {
		if !projFindDebounceWait(ctx) {
			return projFindResult{}, ctx.Err()
		}
		opts := search.DefaultOptions()
		opts.Cancelled = func() bool { return ctx.Err() != nil }
		opts.MatchCase, opts.WholeWord, opts.Regex = matchCase, wholeWord, regex
		matches, truncated := search.Search(root, index.Files(), query, opts)
		return projFindResult{matches: matches, truncated: truncated}, ctx.Err()
	})
}

// projFindDebounceWait holds the run for the debounce interval and
// reports whether it survived it — false when a newer keystroke (or the
// panel closing) retired the run first, which is the common case while
// the user is still typing.
func projFindDebounceWait(ctx context.Context) bool {
	timer := time.NewTimer(projFindDebounce)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// handleProjFindDone lands a finished sweep: a run that was cancelled
// mid-walk carries a partial answer and is dropped, as is one landing
// after the panel closed; a current one replaces the result set and
// resets the cursor. Superseded runs never reach here — the job drops
// them.
func (a *App) handleProjFindDone(r projFindResult, err error) {
	if err != nil || !a.projFind.findOpen {
		return
	}
	a.projFind.findMatches = r.matches
	a.projFind.findTruncated = r.truncated
	a.projFind.findSelected = 0
	a.projFind.findScrollY = 0
}

// projFindRows flattens the match list into renderable rows: one header
// per file (in first-hit order) and, unless that file is folded, one
// row per match.
func (a *App) projFindRows() []projFindRow {
	var rows []projFindRow
	i := 0
	for i < len(a.projFind.findMatches) {
		path := a.projFind.findMatches[i].Path
		j := i
		for j < len(a.projFind.findMatches) && a.projFind.findMatches[j].Path == path {
			j++
		}
		rows = append(rows, projFindRow{IsHeader: true, Path: path, MatchIdx: -1, Count: j - i})
		if !a.projFind.findFolded[path] {
			for k := i; k < j; k++ {
				rows = append(rows, projFindRow{Path: path, MatchIdx: k})
			}
		}
		i = j
	}
	return rows
}

// projFindToggleFold flips a file group's fold state, keeping the
// selection on the header so fold/unfold round-trips in place.
func (a *App) projFindToggleFold(path string) {
	if a.projFind.findFolded == nil {
		a.projFind.findFolded = map[string]bool{}
	}
	a.projFind.findFolded[path] = !a.projFind.findFolded[path]
	rows := a.projFindRows()
	for idx, r := range rows {
		if r.IsHeader && r.Path == path {
			a.projFind.findSelected = idx
			break
		}
	}
	a.projFindClampView(rows)
}

// projFindActivate acts on the selected row: headers fold, matches open
// the file at the hit line and dismiss the panel.
func (a *App) projFindActivate() {
	rows := a.projFindRows()
	if a.projFind.findSelected < 0 || a.projFind.findSelected >= len(rows) {
		return
	}
	row := rows[a.projFind.findSelected]
	if row.IsHeader {
		a.projFindToggleFold(row.Path)
		return
	}
	m := a.projFind.findMatches[row.MatchIdx]
	a.closeProjFind()
	a.OpenFileAtLineCol(filepath.Join(a.rootDir, m.Path), m.Line, m.Col)
}

// projFindMove shifts the selection by delta rows, clamped, and keeps
// it on screen.
func (a *App) projFindMove(delta int) {
	rows := a.projFindRows()
	if len(rows) == 0 {
		return
	}
	a.projFind.findSelected += delta
	if a.projFind.findSelected < 0 {
		a.projFind.findSelected = 0
	}
	if a.projFind.findSelected >= len(rows) {
		a.projFind.findSelected = len(rows) - 1
	}
	a.projFindClampView(rows)
}

// projFindClampView keeps the selected row inside the results viewport,
// and the viewport inside the row list.
func (a *App) projFindClampView(rows []projFindRow) {
	_, _, _, eh := a.editorRect()
	if eh < 1 {
		eh = 1
	}
	if a.projFind.findSelected < a.projFind.findScrollY {
		a.projFind.findScrollY = a.projFind.findSelected
	}
	if a.projFind.findSelected >= a.projFind.findScrollY+eh {
		a.projFind.findScrollY = a.projFind.findSelected - eh + 1
	}
	maxScroll := len(rows) - eh
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.projFind.findScrollY > maxScroll {
		a.projFind.findScrollY = maxScroll
	}
	if a.projFind.findScrollY < 0 {
		a.projFind.findScrollY = 0
	}
}

// handleProjFindKey owns the keyboard while the panel is open: Esc
// closes, Enter activates, Tab folds the selected row's file, arrows
// move the selection, everything text-ish edits the query.
func (a *App) handleProjFindKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeProjFind()
	case tcell.KeyEnter:
		if a.projFind.focusReplace {
			a.projReplaceEnter(ev.Modifiers()&tcell.ModShift != 0)
			return
		}
		a.projFindActivate()
	case tcell.KeyTab:
		// Tab grows/hops the replace field (folding lives on Enter-on-
		// header and header clicks).
		a.projReplaceToggleFocus()
	case tcell.KeyUp:
		a.projFindMove(-1)
	case tcell.KeyDown:
		a.projFindMove(1)
	case tcell.KeyPgUp:
		_, _, _, eh := a.editorRect()
		a.projFindMove(-eh)
	case tcell.KeyPgDn:
		_, _, _, eh := a.editorRect()
		a.projFindMove(eh)
	case tcell.KeyLeft:
		if a.projFind.focusReplace {
			if a.projFind.replaceCursor > 0 {
				a.projFind.replaceCursor--
			}
			return
		}
		if a.projFind.findCursor > 0 {
			a.projFind.findCursor--
		}
	case tcell.KeyRight:
		if a.projFind.focusReplace {
			if a.projFind.replaceCursor < len(a.projFind.replaceValue) {
				a.projFind.replaceCursor++
			}
			return
		}
		if a.projFind.findCursor < len(a.projFind.findValue) {
			a.projFind.findCursor++
		}
	case tcell.KeyHome:
		if a.projFind.focusReplace {
			a.projFind.replaceCursor = 0
			return
		}
		a.projFind.findCursor = 0
	case tcell.KeyEnd:
		if a.projFind.focusReplace {
			a.projFind.replaceCursor = len(a.projFind.replaceValue)
			return
		}
		a.projFind.findCursor = len(a.projFind.findValue)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.projFind.focusReplace {
			if a.projFind.replaceCursor > 0 {
				a.projFind.replaceValue = append(a.projFind.replaceValue[:a.projFind.replaceCursor-1], a.projFind.replaceValue[a.projFind.replaceCursor:]...)
				a.projFind.replaceCursor--
			}
			return
		}
		if a.projFind.findCursor > 0 {
			a.projFind.findValue = append(a.projFind.findValue[:a.projFind.findCursor-1], a.projFind.findValue[a.projFind.findCursor:]...)
			a.projFind.findCursor--
			a.projFindQueryChanged()
		}
	case tcell.KeyDelete:
		if a.projFind.focusReplace {
			if a.projFind.replaceCursor < len(a.projFind.replaceValue) {
				a.projFind.replaceValue = append(a.projFind.replaceValue[:a.projFind.replaceCursor], a.projFind.replaceValue[a.projFind.replaceCursor+1:]...)
			}
			return
		}
		if a.projFind.findCursor < len(a.projFind.findValue) {
			a.projFind.findValue = append(a.projFind.findValue[:a.projFind.findCursor], a.projFind.findValue[a.projFind.findCursor+1:]...)
			a.projFindQueryChanged()
		}
	case tcell.KeyRune:
		r := ev.Rune()
		if r < 0x20 {
			return
		}
		if a.projFind.focusReplace {
			next := make([]rune, 0, len(a.projFind.replaceValue)+1)
			next = append(next, a.projFind.replaceValue[:a.projFind.replaceCursor]...)
			next = append(next, r)
			next = append(next, a.projFind.replaceValue[a.projFind.replaceCursor:]...)
			a.projFind.replaceValue = next
			a.projFind.replaceCursor++
			return
		}
		next := make([]rune, 0, len(a.projFind.findValue)+1)
		next = append(next, a.projFind.findValue[:a.projFind.findCursor]...)
		next = append(next, r)
		next = append(next, a.projFind.findValue[a.projFind.findCursor:]...)
		a.projFind.findValue = next
		a.projFind.findCursor++
		a.projFindQueryChanged()
	}
}

// handleProjFindMouse routes mouse input while the panel is open:
// wheel scrolls the results, clicking a header folds, clicking a match
// opens it, clicking anywhere outside the panel dismisses it.
func (a *App) handleProjFindMouse(x, y int, btn tcell.ButtonMask) {
	ex, ey, ew, eh := a.editorRect()
	if btn&tcell.WheelUp != 0 {
		a.projFind.findScrollY -= wheelLines
		a.projFindClampView(a.projFindRows())
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.projFind.findScrollY += wheelLines
		a.projFindClampView(a.projFindRows())
		return
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	// Bar row: chip clicks toggle a mode and re-run the query; other
	// spots keep focus and do nothing.
	if y == a.height-2 && x >= ex {
		label := " Search project: "
		for _, c := range a.projFindChips(runeLen(label)) {
			sw := a.sidebarW()
			if x >= sw+c.x0 && x < sw+c.x1 {
				*c.on = !*c.on
				a.projFindQueryChanged()
				return
			}
		}
		if btn&tcell.Button1 != 0 && a.projFind.replaceOpen {
			if x >= a.projFind.replaceAllX0 && x < a.projFind.replaceAllX1 && a.projFind.replaceAllX1 > 0 {
				a.projReplaceConfirmAll()
				return
			}
			if x >= a.projFind.replaceFieldX0 && x < a.projFind.replaceFieldX1 {
				a.projFind.focusReplace = true
				return
			}
			a.projFind.focusReplace = false
		}
		return
	}
	if x >= ex && x < ex+ew && y >= ey && y < ey+eh {
		rows := a.projFindRows()
		idx := a.projFind.findScrollY + (y - ey)
		if idx < 0 || idx >= len(rows) {
			return
		}
		a.projFind.findSelected = idx
		a.projFindActivate()
		return
	}
	// Anywhere else — the user has moved on.
	a.closeProjFind()
}

// drawProjFind renders the results overlay plus the query bar. Layout:
//
//	<editor area>   ▾ internal/app/app.go (12)
//	                  123: the matched line text
//	<bar row>        Search project: query     37 matches in 4 files
func (a *App) drawProjFind() {
	if !a.projFind.findOpen {
		return
	}
	a.drawProjFindResults()
	a.drawProjFindBar()
}

// drawProjFindResults paints the grouped match list over the editor area.
func (a *App) drawProjFindResults() {
	ex, ey, ew, eh := a.editorRect()
	bgStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	for cy := ey; cy < ey+eh; cy++ {
		for cx := ex; cx < ex+ew; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}
	rows := a.projFindRows()
	if len(rows) == 0 {
		msg := "Type to search the project"
		if a.projFind.sweep.Busy() {
			msg = "Searching…"
		} else if len(a.projFind.findValue) > 0 {
			msg = "No matches"
		}
		drawAt(a.screen, ex+2, ey+1, msg, bgStyle.Foreground(a.theme.Muted))
		return
	}
	query := string(a.projFind.findValue)
	for i := 0; i < eh; i++ {
		idx := a.projFind.findScrollY + i
		if idx >= len(rows) {
			break
		}
		a.drawProjFindRow(ex, ey+i, ew, rows[idx], idx == a.projFind.findSelected, query)
	}
}

// drawProjFindRow paints one row of the overlay.
func (a *App) drawProjFindRow(x, y, w int, row projFindRow, selected bool, query string) {
	bg := a.theme.BG
	if selected {
		bg = a.theme.Selection
	}
	base := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	for cx := x; cx < x+w; cx++ {
		a.screen.SetContent(cx, y, ' ', nil, base)
	}
	if row.IsHeader {
		arrow := "▾ "
		if a.projFind.findFolded[row.Path] {
			arrow = "▸ "
		}
		label := fmt.Sprintf("%s%s (%d)", arrow, row.Path, row.Count)
		st := base.Foreground(a.theme.Accent).Bold(true)
		if selected {
			st = base.Bold(true)
		}
		drawClipped(a.screen, x+1, y, w-2, label, st)
		return
	}
	m := a.projFind.findMatches[row.MatchIdx]
	numStr := fmt.Sprintf("%6d: ", m.Line)
	numEnd := drawClipped(a.screen, x+1, y, w-2, numStr, base.Foreground(a.theme.Muted))
	textW := x + w - 1 - numEnd
	if textW < 1 {
		return
	}
	// Base text first, then re-paint the query hits so they pop. On a
	// selected row the row highlight already carries the eye — skip the
	// match tint there to keep the text legible.
	drawClipped(a.screen, numEnd, y, textW, m.Text, base)
	if selected || query == "" {
		return
	}
	matchStyle := tcell.StyleDefault.Background(a.theme.FindMatch).Foreground(a.theme.Text)
	for _, span := range matchRuneSpans(m.Text, query) {
		start, end := span[0], span[1]
		runes := []rune(m.Text)
		for ri := start; ri < end && ri < len(runes); ri++ {
			cx := numEnd + ri
			if cx >= x+w-1 {
				break
			}
			a.screen.SetContent(cx, y, runes[ri], nil, matchStyle)
		}
	}
}

// projFindChip is one mode toggle on the search bar with its x-range
// in bar-local cells.
type projFindChip struct {
	label  string
	x0, x1 int
	on     *bool
}

// projFindChips lays out the three mode chips right of the label —
// computed on the fly so the renderer and the click handler agree.
func (a *App) projFindChips(labelEnd int) []projFindChip {
	defs := []struct {
		label string
		on    *bool
	}{
		{"Aa", &a.projFind.findMatchCase},
		{"⌇w", &a.projFind.findWholeWord},
		{".*", &a.projFind.findRegex},
	}
	out := make([]projFindChip, 0, 3)
	x := labelEnd
	for _, d := range defs {
		w := runeLen(d.label) + 2
		out = append(out, projFindChip{label: d.label, x0: x, x1: x + w, on: d.on})
		x += w
	}
	return out
}

// drawProjFindBar paints the query input row above the status bar.
func (a *App) drawProjFindBar() {
	sw := a.sidebarW()
	bx, by, bw := sw, a.height-2, a.width-sw
	bg := a.theme.LineHL
	barStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	labelStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	for cx := bx; cx < bx+bw; cx++ {
		a.screen.SetContent(cx, by, ' ', nil, barStyle)
	}
	label := " Search project: "
	drawAt(a.screen, bx, by, label, labelStyle)
	// Mode chips: clickable [Aa] [⌇w] [.*] toggles, lit when active.
	chips := a.projFindChips(runeLen(label))
	for _, c := range chips {
		st := mutedStyle
		if *c.on {
			st = labelStyle
		}
		drawAt(a.screen, bx+c.x0, by, " "+c.label+" ", st)
	}
	inputStart := bx + chips[len(chips)-1].x1 + 1

	hint := " Enter: open · Tab: replace · Esc: close "
	if a.projFind.replaceOpen && a.projFind.focusReplace {
		hint = " Enter: replace line · Shift+Enter: all · Tab: query · Esc: close "
	} else if a.projFind.replaceOpen {
		hint = " Enter: open · Tab: replace field · Esc: close "
	}
	counter := a.projFindCounterText()
	rightTextStart := bx + bw
	if bw > runeLen(label)+runeLen(hint)+10 {
		rightTextStart -= runeLen(hint) + 1
		drawAt(a.screen, rightTextStart, by, hint, mutedStyle)
	}
	if counter != "" && bw > runeLen(label)+runeLen(counter)+4 {
		rightTextStart -= runeLen(counter) + 2
		style := mutedStyle
		if !a.projFind.sweep.Busy() && len(a.projFind.findValue) > 0 && len(a.projFind.findMatches) == 0 {
			style = tcell.StyleDefault.Background(bg).Foreground(a.theme.Error).Bold(true)
		}
		drawAt(a.screen, rightTextStart, by, counter, style)
	}

	inputEnd := rightTextStart - 1
	if inputEnd <= inputStart {
		inputEnd = bx + bw - 1
	}
	// With the replace field open, the query keeps the left half; the
	// right half carries " ⇒ <replacement> [ All ]". The x ranges are
	// stamped for the mouse handler.
	a.projFind.replaceFieldX0, a.projFind.replaceFieldX1 = 0, 0
	a.projFind.replaceAllX0, a.projFind.replaceAllX1 = 0, 0
	if a.projFind.replaceOpen {
		half := inputStart + (inputEnd-inputStart)/2
		rlabel := " ⇒ "
		drawAt(a.screen, half, by, rlabel, labelStyle)
		fieldX0 := half + runeLen(rlabel)
		fieldX1 := inputEnd
		if len(a.projFind.replaceValue) > 0 {
			allBtn := "[ All ]"
			bx0 := inputEnd - runeLen(allBtn)
			if bx0 > fieldX0+2 {
				drawAt(a.screen, bx0, by, allBtn, labelStyle)
				a.projFind.replaceAllX0, a.projFind.replaceAllX1 = bx0, bx0+runeLen(allBtn)
				fieldX1 = bx0 - 1
			}
		}
		for i, r := range a.projFind.replaceValue {
			if fieldX0+i >= fieldX1 {
				break
			}
			a.screen.SetContent(fieldX0+i, by, r, nil, barStyle)
		}
		a.projFind.replaceFieldX0, a.projFind.replaceFieldX1 = fieldX0, fieldX1
		inputEnd = half - 1
	}
	inputWidth := inputEnd - inputStart
	if inputWidth < 1 {
		inputWidth = 1
	}
	if a.projFind.findCursor < a.projFind.findScroll {
		a.projFind.findScroll = a.projFind.findCursor
	}
	if a.projFind.findCursor >= a.projFind.findScroll+inputWidth {
		a.projFind.findScroll = a.projFind.findCursor - inputWidth + 1
	}
	for i := 0; i < inputWidth; i++ {
		idx := a.projFind.findScroll + i
		if idx >= len(a.projFind.findValue) {
			break
		}
		a.screen.SetContent(inputStart+i, by, a.projFind.findValue[idx], nil, barStyle)
	}
	if a.projFind.replaceOpen && a.projFind.focusReplace {
		caret := a.projFind.replaceFieldX0 + a.projFind.replaceCursor
		if caret >= a.projFind.replaceFieldX0 && caret <= a.projFind.replaceFieldX1 {
			a.screen.ShowCursor(caret, by)
		}
		return
	}
	caret := inputStart + (a.projFind.findCursor - a.projFind.findScroll)
	if caret >= inputStart && caret <= inputEnd {
		a.screen.ShowCursor(caret, by)
	}
}

// projFindCounterText summarises the sweep for the bar's right side.
func (a *App) projFindCounterText() string {
	if len(a.projFind.findValue) == 0 {
		return ""
	}
	if a.projFind.sweep.Busy() {
		return "searching…"
	}
	if len(a.projFind.findMatches) == 0 {
		return "no results"
	}
	files := 0
	last := ""
	for _, m := range a.projFind.findMatches {
		if m.Path != last {
			files++
			last = m.Path
		}
	}
	s := fmt.Sprintf("%d matches in %d files", len(a.projFind.findMatches), files)
	if a.projFind.findTruncated {
		s += " (capped)"
	}
	return s
}

// matchRuneSpans returns the [start, end) rune ranges of every
// smart-case occurrence of query inside text.
func matchRuneSpans(text, query string) [][2]int {
	if query == "" {
		return nil
	}
	hay, needle := text, query
	if !strings.ContainsFunc(query, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		hay = strings.ToLower(text)
		needle = strings.ToLower(query)
	}
	var spans [][2]int
	qRunes := len([]rune(needle))
	byteOff := 0
	for {
		i := strings.Index(hay[byteOff:], needle)
		if i < 0 {
			break
		}
		abs := byteOff + i
		start := len([]rune(hay[:abs]))
		spans = append(spans, [2]int{start, start + qRunes})
		byteOff = abs + len(needle)
	}
	return spans
}
