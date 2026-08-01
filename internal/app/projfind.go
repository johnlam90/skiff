// =============================================================================
// File: internal/app/projfind.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// projfind.go owns the project-wide content search UI: a find-bar-style
// input row above the status bar plus a results overlay in the editor
// area, grouped by file with per-file folding (druk's search panel,
// skiff-shaped). The sweep itself lives in internal/search and runs in
// a goroutine; results come back through a projFindDoneEvent carrying a
// generation number so stale sweeps are dropped, never rendered.

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/search"
)

// projFindKickEvent fires after the debounce interval: the sweep for
// gen starts only if no newer keystroke has arrived meanwhile.
type projFindKickEvent struct {
	when time.Time
	gen  int
}

// When implements tcell.Event.
func (e *projFindKickEvent) When() time.Time { return e.when }

// projFindDebounce is how long typing must pause before a sweep
// starts. Long enough to coalesce a burst of keystrokes, short enough
// to feel instant.
const projFindDebounce = 120 * time.Millisecond

// projFindDoneEvent carries a finished background sweep onto the main
// event loop. gen pins it to the query generation that started it.
type projFindDoneEvent struct {
	when      time.Time
	gen       int
	matches   []search.Match
	truncated bool
}

// When implements tcell.Event.
func (e *projFindDoneEvent) When() time.Time { return e.when }

// projFindRow is one rendered row of the results overlay: a file header
// (fold toggle) or a single match line.
type projFindRow struct {
	IsHeader bool
	Path     string
	MatchIdx int // index into projFindMatches; -1 for headers
	Count    int // header only: matches in this file
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
	a.projFindOpen = true
	a.projFindValue = nil
	a.projFindCursor = 0
	a.projFindScroll = 0
	a.projFindMatches = nil
	a.projFindTruncated = false
	a.projFindBusy = false
	a.projFindSelected = 0
	a.projFindScrollY = 0
	a.projFindFolded = map[string]bool{}
	a.resetProjReplace()
}

// closeProjFind dismisses the panel and forgets the query — same
// "Esc means done" contract as the in-file find bar.
func (a *App) closeProjFind() {
	a.projFindOpen = false
	a.projFindValue = nil
	a.projFindCursor = 0
	a.projFindScroll = 0
	a.projFindMatches = nil
	a.projFindTruncated = false
	a.projFindBusy = false
	a.projFindFolded = nil
	a.resetProjReplace()
	// Invalidate any in-flight sweep.
	a.projFindGen++
}

// projFindQueryChanged kicks a background sweep for the current input.
// Every call bumps the generation; the done-handler drops results from
// older generations, so fast typing never renders stale hits.
func (a *App) projFindQueryChanged() {
	a.projFindGen++
	gen := a.projFindGen
	query := string(a.projFindValue)
	if strings.TrimSpace(query) == "" {
		a.projFindMatches = nil
		a.projFindTruncated = false
		a.projFindBusy = false
		a.projFindSelected = 0
		a.projFindScrollY = 0
		return
	}
	a.projFindBusy = true
	a.projFindLiveGen.Store(int64(gen))
	// Debounce: the sweep starts only after the typing pauses — every
	// keystroke on a big repo used to launch (and then discard) a full
	// disk walk.
	scr := a.screen
	time.AfterFunc(projFindDebounce, func() {
		_ = scr.PostEvent(&projFindKickEvent{when: time.Now(), gen: gen})
	})
}

// handleProjFindKick starts the debounced sweep, unless a newer
// keystroke superseded it. The sweep itself also polls the live
// generation so an in-flight walk abandons as soon as it is stale.
func (a *App) handleProjFindKick(e *projFindKickEvent) {
	if !a.projFindOpen || e.gen != a.projFindGen {
		return
	}
	gen := e.gen
	query := string(a.projFindValue)
	files := a.finder.Files()
	root := a.rootDir
	scr := a.screen
	live := &a.projFindLiveGen
	matchCase, wholeWord, regex := a.projFindMatchCase, a.projFindWholeWord, a.projFindRegex
	go func() {
		opts := search.DefaultOptions()
		opts.Cancelled = func() bool { return live.Load() != int64(gen) }
		opts.MatchCase, opts.WholeWord, opts.Regex = matchCase, wholeWord, regex
		matches, truncated := search.Search(root, files, query, opts)
		_ = scr.PostEvent(&projFindDoneEvent{when: time.Now(), gen: gen, matches: matches, truncated: truncated})
	}()
}

// handleProjFindDone lands a finished sweep: stale generations are
// dropped, current ones replace the result set and reset the cursor.
func (a *App) handleProjFindDone(e *projFindDoneEvent) {
	if !a.projFindOpen || e.gen != a.projFindGen {
		return
	}
	a.projFindMatches = e.matches
	a.projFindTruncated = e.truncated
	a.projFindBusy = false
	a.projFindSelected = 0
	a.projFindScrollY = 0
}

// projFindRows flattens the match list into renderable rows: one header
// per file (in first-hit order) and, unless that file is folded, one
// row per match.
func (a *App) projFindRows() []projFindRow {
	var rows []projFindRow
	i := 0
	for i < len(a.projFindMatches) {
		path := a.projFindMatches[i].Path
		j := i
		for j < len(a.projFindMatches) && a.projFindMatches[j].Path == path {
			j++
		}
		rows = append(rows, projFindRow{IsHeader: true, Path: path, MatchIdx: -1, Count: j - i})
		if !a.projFindFolded[path] {
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
	if a.projFindFolded == nil {
		a.projFindFolded = map[string]bool{}
	}
	a.projFindFolded[path] = !a.projFindFolded[path]
	rows := a.projFindRows()
	for idx, r := range rows {
		if r.IsHeader && r.Path == path {
			a.projFindSelected = idx
			break
		}
	}
	a.projFindClampView(rows)
}

// projFindActivate acts on the selected row: headers fold, matches open
// the file at the hit line and dismiss the panel.
func (a *App) projFindActivate() {
	rows := a.projFindRows()
	if a.projFindSelected < 0 || a.projFindSelected >= len(rows) {
		return
	}
	row := rows[a.projFindSelected]
	if row.IsHeader {
		a.projFindToggleFold(row.Path)
		return
	}
	m := a.projFindMatches[row.MatchIdx]
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
	a.projFindSelected += delta
	if a.projFindSelected < 0 {
		a.projFindSelected = 0
	}
	if a.projFindSelected >= len(rows) {
		a.projFindSelected = len(rows) - 1
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
	if a.projFindSelected < a.projFindScrollY {
		a.projFindScrollY = a.projFindSelected
	}
	if a.projFindSelected >= a.projFindScrollY+eh {
		a.projFindScrollY = a.projFindSelected - eh + 1
	}
	maxScroll := len(rows) - eh
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.projFindScrollY > maxScroll {
		a.projFindScrollY = maxScroll
	}
	if a.projFindScrollY < 0 {
		a.projFindScrollY = 0
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
		if a.projFocusReplace {
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
		if a.projFocusReplace {
			if a.projReplaceCursor > 0 {
				a.projReplaceCursor--
			}
			return
		}
		if a.projFindCursor > 0 {
			a.projFindCursor--
		}
	case tcell.KeyRight:
		if a.projFocusReplace {
			if a.projReplaceCursor < len(a.projReplaceValue) {
				a.projReplaceCursor++
			}
			return
		}
		if a.projFindCursor < len(a.projFindValue) {
			a.projFindCursor++
		}
	case tcell.KeyHome:
		if a.projFocusReplace {
			a.projReplaceCursor = 0
			return
		}
		a.projFindCursor = 0
	case tcell.KeyEnd:
		if a.projFocusReplace {
			a.projReplaceCursor = len(a.projReplaceValue)
			return
		}
		a.projFindCursor = len(a.projFindValue)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.projFocusReplace {
			if a.projReplaceCursor > 0 {
				a.projReplaceValue = append(a.projReplaceValue[:a.projReplaceCursor-1], a.projReplaceValue[a.projReplaceCursor:]...)
				a.projReplaceCursor--
			}
			return
		}
		if a.projFindCursor > 0 {
			a.projFindValue = append(a.projFindValue[:a.projFindCursor-1], a.projFindValue[a.projFindCursor:]...)
			a.projFindCursor--
			a.projFindQueryChanged()
		}
	case tcell.KeyDelete:
		if a.projFocusReplace {
			if a.projReplaceCursor < len(a.projReplaceValue) {
				a.projReplaceValue = append(a.projReplaceValue[:a.projReplaceCursor], a.projReplaceValue[a.projReplaceCursor+1:]...)
			}
			return
		}
		if a.projFindCursor < len(a.projFindValue) {
			a.projFindValue = append(a.projFindValue[:a.projFindCursor], a.projFindValue[a.projFindCursor+1:]...)
			a.projFindQueryChanged()
		}
	case tcell.KeyRune:
		r := ev.Rune()
		if r < 0x20 {
			return
		}
		if a.projFocusReplace {
			next := make([]rune, 0, len(a.projReplaceValue)+1)
			next = append(next, a.projReplaceValue[:a.projReplaceCursor]...)
			next = append(next, r)
			next = append(next, a.projReplaceValue[a.projReplaceCursor:]...)
			a.projReplaceValue = next
			a.projReplaceCursor++
			return
		}
		next := make([]rune, 0, len(a.projFindValue)+1)
		next = append(next, a.projFindValue[:a.projFindCursor]...)
		next = append(next, r)
		next = append(next, a.projFindValue[a.projFindCursor:]...)
		a.projFindValue = next
		a.projFindCursor++
		a.projFindQueryChanged()
	}
}

// handleProjFindMouse routes mouse input while the panel is open:
// wheel scrolls the results, clicking a header folds, clicking a match
// opens it, clicking anywhere outside the panel dismisses it.
func (a *App) handleProjFindMouse(x, y int, btn tcell.ButtonMask) {
	ex, ey, ew, eh := a.editorRect()
	if btn&tcell.WheelUp != 0 {
		a.projFindScrollY -= wheelLines
		a.projFindClampView(a.projFindRows())
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.projFindScrollY += wheelLines
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
		if btn&tcell.Button1 != 0 && a.projReplaceOpen {
			if x >= a.projReplaceAllX0 && x < a.projReplaceAllX1 && a.projReplaceAllX1 > 0 {
				a.projReplaceConfirmAll()
				return
			}
			if x >= a.projReplaceFieldX0 && x < a.projReplaceFieldX1 {
				a.projFocusReplace = true
				return
			}
			a.projFocusReplace = false
		}
		return
	}
	if x >= ex && x < ex+ew && y >= ey && y < ey+eh {
		rows := a.projFindRows()
		idx := a.projFindScrollY + (y - ey)
		if idx < 0 || idx >= len(rows) {
			return
		}
		a.projFindSelected = idx
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
	if !a.projFindOpen {
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
		if a.projFindBusy {
			msg = "Searching…"
		} else if len(a.projFindValue) > 0 {
			msg = "No matches"
		}
		drawAt(a.screen, ex+2, ey+1, msg, bgStyle.Foreground(a.theme.Muted))
		return
	}
	query := string(a.projFindValue)
	for i := 0; i < eh; i++ {
		idx := a.projFindScrollY + i
		if idx >= len(rows) {
			break
		}
		a.drawProjFindRow(ex, ey+i, ew, rows[idx], idx == a.projFindSelected, query)
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
		if a.projFindFolded[row.Path] {
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
	m := a.projFindMatches[row.MatchIdx]
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
		{"Aa", &a.projFindMatchCase},
		{"⌇w", &a.projFindWholeWord},
		{".*", &a.projFindRegex},
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
	if a.projReplaceOpen && a.projFocusReplace {
		hint = " Enter: replace line · Shift+Enter: all · Tab: query · Esc: close "
	} else if a.projReplaceOpen {
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
		if !a.projFindBusy && len(a.projFindValue) > 0 && len(a.projFindMatches) == 0 {
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
	a.projReplaceFieldX0, a.projReplaceFieldX1 = 0, 0
	a.projReplaceAllX0, a.projReplaceAllX1 = 0, 0
	if a.projReplaceOpen {
		half := inputStart + (inputEnd-inputStart)/2
		rlabel := " ⇒ "
		drawAt(a.screen, half, by, rlabel, labelStyle)
		fieldX0 := half + runeLen(rlabel)
		fieldX1 := inputEnd
		if len(a.projReplaceValue) > 0 {
			allBtn := "[ All ]"
			bx0 := inputEnd - runeLen(allBtn)
			if bx0 > fieldX0+2 {
				drawAt(a.screen, bx0, by, allBtn, labelStyle)
				a.projReplaceAllX0, a.projReplaceAllX1 = bx0, bx0+runeLen(allBtn)
				fieldX1 = bx0 - 1
			}
		}
		for i, r := range a.projReplaceValue {
			if fieldX0+i >= fieldX1 {
				break
			}
			a.screen.SetContent(fieldX0+i, by, r, nil, barStyle)
		}
		a.projReplaceFieldX0, a.projReplaceFieldX1 = fieldX0, fieldX1
		inputEnd = half - 1
	}
	inputWidth := inputEnd - inputStart
	if inputWidth < 1 {
		inputWidth = 1
	}
	if a.projFindCursor < a.projFindScroll {
		a.projFindScroll = a.projFindCursor
	}
	if a.projFindCursor >= a.projFindScroll+inputWidth {
		a.projFindScroll = a.projFindCursor - inputWidth + 1
	}
	for i := 0; i < inputWidth; i++ {
		idx := a.projFindScroll + i
		if idx >= len(a.projFindValue) {
			break
		}
		a.screen.SetContent(inputStart+i, by, a.projFindValue[idx], nil, barStyle)
	}
	if a.projReplaceOpen && a.projFocusReplace {
		caret := a.projReplaceFieldX0 + a.projReplaceCursor
		if caret >= a.projReplaceFieldX0 && caret <= a.projReplaceFieldX1 {
			a.screen.ShowCursor(caret, by)
		}
		return
	}
	caret := inputStart + (a.projFindCursor - a.projFindScroll)
	if caret >= inputStart && caret <= inputEnd {
		a.screen.ShowCursor(caret, by)
	}
}

// projFindCounterText summarises the sweep for the bar's right side.
func (a *App) projFindCounterText() string {
	if len(a.projFindValue) == 0 {
		return ""
	}
	if a.projFindBusy {
		return "searching…"
	}
	if len(a.projFindMatches) == 0 {
		return "no results"
	}
	files := 0
	last := ""
	for _, m := range a.projFindMatches {
		if m.Path != last {
			files++
			last = m.Path
		}
	}
	s := fmt.Sprintf("%d matches in %d files", len(a.projFindMatches), files)
	if a.projFindTruncated {
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
