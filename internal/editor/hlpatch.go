// =============================================================================
// File: internal/editor/hlpatch.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// hlpatch.go is the editor half of off-loop syntax highlighting. An
// edit used to re-tokenise the whole highlight window — the viewport
// plus 256 lead lines each side — synchronously on the event loop, ~30ms
// per keystroke on an 8000-line Go file and several times that on a
// small remote box. Now the keystroke frame paints a PATCHED grid: the
// cached styles are rebased across the edit (rows shifted, the edited
// line spliced at the changed runes) so the frame still lines up with
// the text, and the real re-lex runs on a goroutine through the app's
// highlight job. HighlightRequest / ApplyHighlight are that seam: the
// request copies the window's text out on the loop (a Buffer is not
// safe to read from a goroutine, see CLAUDE.md), Run is the pure part
// the goroutine executes, and Apply installs the result only if no edit
// or synchronous re-lex has moved the tab on since.

package editor

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// HighlightRequest is one background re-tokenise: the window's text,
// already copied out of the buffer, plus the bookkeeping Apply needs to
// tell whether the result still describes the tab.
type HighlightRequest struct {
	Path             string
	Src              string
	WinStart, WinEnd int
	Height           int
	Gen              int
}

// HighlightResult is what Run hands back: the window's rows, indexed
// from WinStart, and the request's identity.
type HighlightResult struct {
	Rows             [][]tcell.Style
	WinStart, WinEnd int
	Height           int
	Gen              int
}

// Run tokenises the request. It touches nothing on the Tab, so it is
// safe to call from any goroutine; the theme is passed by value for the
// same reason.
func (r HighlightRequest) Run(th theme.Theme) HighlightResult {
	return HighlightResult{
		Rows:     highlightSource(r.Path, r.Src, th),
		WinStart: r.WinStart,
		WinEnd:   r.WinEnd,
		Height:   r.Height,
		Gen:      r.Gen,
	}
}

// HighlightPending reports whether the styles on screen are a patched
// approximation still waiting for a background re-lex to land.
func (t *Tab) HighlightPending() bool { return t.hlPending }

// HighlightRequest returns the background re-lex the tab wants for a
// viewport viewH rows tall, or false when the cached grid is exact or
// a request for this buffer generation is already out. The caller runs
// it off the loop and hands the result to ApplyHighlight.
func (t *Tab) HighlightRequest(viewH int) (HighlightRequest, bool) {
	if !t.hlPending || t.StyleStale || t.hlRequestedGen == t.hlGen || t.Buffer == nil {
		return HighlightRequest{}, false
	}
	_, _, winStart, winEnd := highlightBounds(t.Buffer.LineCount(), t.ScrollY, viewH)
	if winEnd <= winStart {
		return HighlightRequest{}, false
	}
	t.hlRequestedGen = t.hlGen
	return HighlightRequest{
		Path:     t.Path,
		Src:      strings.Join(capLongLines(t.Buffer.Lines[winStart:winEnd]), "\n"),
		WinStart: winStart,
		WinEnd:   winEnd,
		Height:   viewH,
		Gen:      t.hlGen,
	}, true
}

// ApplyHighlight installs a landed result and reports whether it was
// used. A result is dropped when the buffer has been edited since the
// request (its generation moved on) or when Render already re-lexed
// synchronously in the meantime — a scroll past the window's edge —
// because that grid is exact and newer.
func (t *Tab) ApplyHighlight(res HighlightResult) bool {
	if !t.hlPending || res.Gen != t.hlGen || t.Buffer == nil {
		return false
	}
	if res.WinEnd > t.Buffer.LineCount() {
		return false // defensive: the generation should have caught this
	}
	styles := make([][]tcell.Style, t.Buffer.LineCount())
	for i := res.WinStart; i < res.WinEnd; i++ {
		idx := i - res.WinStart
		if idx >= len(res.Rows) {
			break
		}
		styles[i] = res.Rows[idx]
	}
	t.Styles, t.hlWinStart, t.hlWinEnd = styles, res.WinStart, res.WinEnd
	t.lastHighlightHeight = res.Height
	t.hlPending = false
	t.StyleStale = false
	return true
}

// editLinesSnapshot copies the buffer's line headers into a scratch
// slice the Tab keeps, so rebaseStyles can diff the buffer before and
// after a mutation without allocating per keystroke. The strings
// themselves are shared, never copied.
func (t *Tab) editLinesSnapshot() []string {
	if t.Buffer == nil {
		return nil
	}
	t.editScratch = append(t.editScratch[:0], t.Buffer.Lines...)
	return t.editScratch
}

// rebaseStyles is edit's highlight trailer: bump the generation (any
// result in flight is now stale), then either rebase the cached grid
// across the mutation and ask for a background re-lex, or — when there
// is no usable grid, or the edit reached outside the cached window —
// fall back to the synchronous re-lex Render has always done.
func (t *Tab) rebaseStyles(before []string) {
	t.hlGen++
	if before == nil || t.StyleStale || t.Styles == nil || t.Buffer == nil {
		t.StyleStale = true
		return
	}
	styles, winEnd, ok := patchStyles(t.Styles, t.hlWinStart, t.hlWinEnd, before, t.Buffer.Lines)
	if !ok {
		t.StyleStale = true
		return
	}
	t.Styles, t.hlWinEnd = styles, winEnd
	t.hlPending = true
}

// patchStyles rebases a highlight grid across one buffer mutation so
// the next frame can paint it against cur. The changed span is found
// by common prefix and suffix over the line strings (unchanged lines
// share their string with the old slice, so the compare is a pointer
// test); rows before it are kept, rows after it shift, and each
// changed row is spliced at the rune level so text either side of the
// edit keeps its colour. A row that gained lines takes the style of
// the rune before the insertion — a new line typed inside a comment
// stays comment-coloured until the real lex lands.
//
// It refuses (ok=false) when the changed span reaches outside the
// cached window [winStart, winEnd): a paste or undo that lands past the
// window needs a full re-lex anyway, and shifting rows the grid never
// styled would only move nils around. The returned winEnd is the
// window's new end after the line-count delta.
func patchStyles(styles [][]tcell.Style, winStart, winEnd int, old, cur []string) ([][]tcell.Style, int, bool) {
	if len(styles) != len(old) {
		return nil, 0, false
	}
	p := 0
	for p < len(old) && p < len(cur) && old[p] == cur[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(cur)-p && old[len(old)-1-s] == cur[len(cur)-1-s] {
		s++
	}
	oldMid := old[p : len(old)-s]
	curMid := cur[p : len(cur)-s]
	if p < winStart || p+len(oldMid) > winEnd {
		return nil, 0, false
	}
	delta := len(cur) - len(old)
	out := make([][]tcell.Style, len(cur))
	copy(out, styles[:p])
	copy(out[p+len(curMid):], styles[p+len(oldMid):])
	for i, line := range curMid {
		var fill tcell.Style
		if i > 0 {
			fill = lastStyle(out[p+i-1])
		} else if p > 0 {
			fill = lastStyle(out[p-1])
		}
		if i < len(oldMid) {
			out[p+i] = spliceRow(styles[p+i], []rune(oldMid[i]), []rune(line), fill)
			continue
		}
		row := make([]tcell.Style, len([]rune(line)))
		for j := range row {
			row[j] = fill
		}
		out[p+i] = row
	}
	return out, winEnd + delta, true
}

// lastStyle is the style of a row's final rune, or the zero style for
// an empty or unstyled row.
func lastStyle(row []tcell.Style) tcell.Style {
	if len(row) == 0 {
		return tcell.StyleDefault
	}
	return row[len(row)-1]
}

// spliceRow rebases one row's styles from oldRunes to newRunes: the
// common prefix and suffix keep their styles and the runes between take
// fill — or, when there is a rune before the change, that rune's style,
// so typing inside a string keeps the string colour. A row whose style
// count does not match its old rune count (a line capLongLines emptied)
// comes back nil and paints plain.
func spliceRow(row []tcell.Style, oldRunes, newRunes []rune, fill tcell.Style) []tcell.Style {
	if len(row) != len(oldRunes) {
		return nil
	}
	p := 0
	for p < len(oldRunes) && p < len(newRunes) && oldRunes[p] == newRunes[p] {
		p++
	}
	s := 0
	for s < len(oldRunes)-p && s < len(newRunes)-p && oldRunes[len(oldRunes)-1-s] == newRunes[len(newRunes)-1-s] {
		s++
	}
	if p > 0 {
		fill = row[p-1]
	}
	out := make([]tcell.Style, len(newRunes))
	copy(out, row[:p])
	for j := p; j < len(newRunes)-s; j++ {
		out[j] = fill
	}
	copy(out[len(newRunes)-s:], row[len(oldRunes)-s:])
	return out
}
