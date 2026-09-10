// =============================================================================
// File: internal/app/mdpreview.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Markdown preview mode: a per-tab, read-only, rendered view of a
// markdown buffer, toggled from the ≡ View menu — the glow idea,
// rendered through internal/mdrender so it's theme-native and
// Chroma-highlighted. App-level on purpose (like the git panel): the
// editor package stays untouched, the preview is ephemeral UI state
// keyed by tab, and it dies with the tab or the toggle. Scrolling is
// its own offset — the buffer's ScrollY, caret and selection are
// exactly where the user left them when the preview turns off.

package app

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/clipboard"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/mdrender"
	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
	"github.com/rivo/uniseg"
)

// mdPreviewState is one tab's rendered preview: the pre-wrapped styled
// lines, the width they were wrapped for (a resize re-renders), and the
// view's own scroll offset.
type mdPreviewState struct {
	lines  []string
	styles [][]tcell.Style
	width  int
	// wide is the block budget the grid was rendered with; a resize
	// that changes only the spare room past the measure still moves
	// table and code widths, so it invalidates like width does.
	wide   int
	scroll int
	// th is the theme the style grid was rendered with. The grid bakes
	// colors in, so a theme change (the picker previews live) must
	// re-render — drawMdPreview compares against the app's current
	// theme the same way it compares width.
	th theme.Theme
	// selA/selB are the drag-selection's anchor and moving end in
	// rendered-line coordinates (line index, rune column). Equal means
	// no selection. Ephemeral like the rest of the state: re-renders
	// (theme, width, reload) drop it, which is correct — the lines it
	// indexed no longer exist.
	selA, selB previewPos

	// findQuery / findCase / findMatches / findIndex are the preview's
	// own search, in the same rendered coordinates as the selection.
	// The find bar searches what the READER sees, never the markdown
	// underneath: the source spells the rendered word "beta" as
	// "**beta**", so a search of the buffer would miss what is on
	// screen and hit what is not. Unlike the selection these survive a
	// re-render — the query is re-run against the fresh lines (see
	// carryFind), because a search that goes dark on a resize is the
	// same "find does nothing" complaint from another angle.
	findQuery   string
	findCase    bool
	findMatches []editor.Match
	findIndex   int
	// findSuspended mirrors Tab.findSuspended: closing the bar takes
	// the highlights down but keeps the query, so the next Esc f seeds
	// itself and Esc ; relights instead of making the user retype.
	findSuspended bool
}

// previewSpan is one painted rune range on a rendered line — the
// half-open [from,to) of a find hit, plus whether it is the current
// one Enter jumps past.
type previewSpan struct {
	from, to int
	current  bool
}

// previewLineHL is everything painted on top of a rendered line's own
// styles: the selection range and the find hits. Bundled rather than
// passed as five loose ints, and ordered — find beats selection, the
// same precedence Tab.cellStyle applies in the editor body.
type previewLineHL struct {
	selFrom, selTo int
	matches        []previewSpan
}

// spanAt returns the find hit covering rune index i, if any.
func (h previewLineHL) spanAt(i int) (previewSpan, bool) {
	for _, sp := range h.matches {
		if i >= sp.from && i < sp.to {
			return sp, true
		}
	}
	return previewSpan{}, false
}

// previewPos is one position in the rendered document: a line index
// into mdPreviewState.lines and a rune column within that line.
type previewPos struct{ line, col int }

// less orders two preview positions document-wise.
func (p previewPos) less(q previewPos) bool {
	return p.line < q.line || (p.line == q.line && p.col < q.col)
}

// isMarkdownPath reports whether path names a markdown file — the one
// extension check the visibility gate and the toggle share.
func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// activeMarkdownTab returns the active tab when it is a markdown file
// (and not an image or an unnamed buffer), nil otherwise.
func (a *App) activeMarkdownTab() *editor.Tab {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || !isMarkdownPath(t.Path) {
		return nil
	}
	return t
}

// hasMarkdownTab is the ≡ row's visibility predicate: the row exists
// exactly when the active tab is a markdown file.
func (a *App) hasMarkdownTab() bool {
	return a.activeMarkdownTab() != nil
}

// mdPreviewState returns the active preview for tab, nil-safe against
// the lazily created map.
func (a *App) mdPreviewFor(t *editor.Tab) *mdPreviewState {
	if t == nil {
		return nil
	}
	return a.mdPreview[t]
}

// menuTogglePreviewMarkdown flips the active markdown tab between the
// editor and the rendered preview. Rendering happens here (and on
// invalidation), never per frame — the cache holds until the width or
// the buffer changes.
func (a *App) menuTogglePreviewMarkdown() {
	a.closeMenu()
	tab := a.activeMarkdownTab()
	if tab == nil {
		return
	}
	if a.mdPreview[tab] != nil {
		delete(a.mdPreview, tab)
		a.rebindFind(tab)
		a.flash("Editing Markdown")
		return
	}
	if a.mdPreview == nil {
		a.mdPreview = map[*editor.Tab]*mdPreviewState{}
	}
	a.mdPreview[tab] = a.renderMdPreview(tab)
	a.rebindFind(tab)
	a.flash("Previewing Markdown — ≡ → Edit Markdown to edit")
}

// rebindFind re-points an open find bar at whichever surface tab now
// shows. The bar searches the rendered page in preview mode and the
// buffer in edit mode, so a toggle underneath it would otherwise leave
// the query counting hits in a document nobody is looking at — the
// same bug as Esc f doing nothing in the preview, arrived at from the
// other side. The surface being left keeps no highlights, and the
// replace field cannot survive into a read-only page.
func (a *App) rebindFind(tab *editor.Tab) {
	s := a.findBar()
	if s == nil || s.boundTab() != tab {
		return
	}
	if !s.replaceAllowed() {
		s.replaceOpen, s.focusReplace = false, false
	}
	if a.mdPreviewFor(tab) != nil {
		tab.ClearFindHighlights()
	}
	s.applyQuery()
}

// previewMarkdownLabel names the toggle row for the current state.
func (a *App) previewMarkdownLabel() string {
	if t := a.activeMarkdownTab(); t != nil && a.mdPreview[t] != nil {
		return "Edit Markdown"
	}
	return "Preview Markdown"
}

// mdPreviewMaxWidth caps the preview's prose measure. Text past roughly
// a hundred cells is hard to track from one line to the next; a
// 200-column terminal used to wrap the document at 197. Tables and code
// blocks are not prose and may use the whole pane (mdPreviewGeom's
// wide) — a table that fits the terminal is never ellipsised to satisfy
// the measure.
const mdPreviewMaxWidth = 100

// mdPreviewGutter is the fixed left gutter between the editor's edge
// and the document. The column used to be centred in the pane, which
// on a wide terminal put the text a screen-width away from the sidebar
// it belongs with; a small fixed gutter reads like the editor's own
// line-number column.
const mdPreviewGutter = 2

// mdPreviewGeom returns the preview's content column: the x the text
// starts at, the width prose is wrapped to (capped at
// mdPreviewMaxWidth), and the wider budget tables and code blocks may
// run out to. The room is the editor rect minus one column of left
// padding, one for the scrollbar and one of right breathing space; the
// gutter is spent only when there is room to spare. This is the ONE
// origin the paint (drawMdPreview) and the hit-test (mdPreviewHit)
// share, so what is clicked is what was painted.
func (a *App) mdPreviewGeom() (contentX, contentW, wideW int) {
	ex, _, ew, _ := a.editorRect()
	room := ew - 3
	gutter := mdPreviewGutter
	if room-gutter < mdPreviewMaxWidth/2 {
		gutter = 0
	}
	wideW = room - gutter
	if wideW < 4 {
		wideW = 4
	}
	contentW = min(wideW, mdPreviewMaxWidth)
	contentX = ex + 1 + gutter
	return contentX, contentW, wideW
}

// renderMdPreview renders tab's buffer at the current width. The buffer
// is read on the event loop (the only place it may be read), and the
// document's own line ending is irrelevant to markdown, so the plain
// LF join is correct here — nothing is ever written back.
func (a *App) renderMdPreview(tab *editor.Tab) *mdPreviewState {
	_, w, wide := a.mdPreviewGeom()
	lines, styles := mdrender.RenderWide([]byte(tab.Buffer.String()), w, wide, a.theme)
	return &mdPreviewState{lines: lines, styles: styles, width: w, wide: wide, th: a.theme}
}

// invalidateMdPreview re-renders tab's preview if one is active —
// called by the silent-reload and format-on-save paths so the screen
// can never show content the buffer no longer holds. The scroll offset
// survives (clamped at draw) so an external touch doesn't yank the
// reader back to the top.
func (a *App) invalidateMdPreview(tab *editor.Tab) {
	st := a.mdPreviewFor(tab)
	if st == nil {
		return
	}
	fresh := a.renderMdPreview(tab)
	fresh.scroll = st.scroll
	fresh.carryFind(st)
	a.mdPreview[tab] = fresh
}

// setFindQuery installs a query on the rendered document and points
// findIndex at the first hit at or after the top of the view — the
// preview's answer to "the nearest match", where the editor uses the
// caret and the preview has none. An empty query clears the search,
// the same contract Tab.SetFindQuery keeps.
func (st *mdPreviewState) setFindQuery(query string, matchCase bool) {
	st.findQuery, st.findCase = query, matchCase
	st.findSuspended = false
	if query == "" {
		st.findMatches, st.findIndex = nil, -1
		return
	}
	st.findMatches = editor.FindAllInLines(st.lines, query, editor.FindOptions{MatchCase: matchCase})
	st.findIndex = editor.FirstMatchAtOrAfter(st.findMatches, editor.Position{Line: st.scroll})
}

// clearFindHighlights takes the tints down but keeps the query, so
// closing the bar leaves the page clean and the next Esc f still seeds
// itself. Mirrors Tab.ClearFindHighlights exactly.
func (st *mdPreviewState) clearFindHighlights() {
	st.findMatches, st.findIndex = nil, -1
	st.findSuspended = st.findQuery != ""
}

// carryFind re-runs old's search against this state's freshly rendered
// lines. A re-render (resize, theme change, reload) replaces every
// line, so the old match list indexes text that no longer exists;
// dropping it instead would make a live search vanish on a resize.
func (st *mdPreviewState) carryFind(old *mdPreviewState) {
	if old.findQuery == "" {
		return
	}
	if old.findSuspended {
		st.findQuery, st.findCase, st.findSuspended = old.findQuery, old.findCase, true
		st.findIndex = -1
		return
	}
	st.setFindQuery(old.findQuery, old.findCase)
}

// findStep advances the current hit by delta with wrap-around, and
// reports whether there was anything to step to.
func (st *mdPreviewState) findStep(delta int) bool {
	n := len(st.findMatches)
	if n == 0 {
		return false
	}
	st.findIndex = ((st.findIndex+delta)%n + n) % n
	return true
}

// currentFindMatch returns the hit Enter jumps past, ok=false when the
// search found nothing.
func (st *mdPreviewState) currentFindMatch() (editor.Match, bool) {
	if st.findIndex < 0 || st.findIndex >= len(st.findMatches) {
		return editor.Match{}, false
	}
	return st.findMatches[st.findIndex], true
}

// findSpansOn returns the hits painted on rendered line i. The match
// list is in document order, so a line's hits are a contiguous run of
// it — found by binary search rather than by scanning every match on
// every painted row.
func (st *mdPreviewState) findSpansOn(i int) []previewSpan {
	if len(st.findMatches) == 0 {
		return nil
	}
	lo := sort.Search(len(st.findMatches), func(k int) bool {
		return st.findMatches[k].Line >= i
	})
	var out []previewSpan
	for k := lo; k < len(st.findMatches) && st.findMatches[k].Line == i; k++ {
		m := st.findMatches[k]
		out = append(out, previewSpan{from: m.Col, to: m.Col + m.Width, current: k == st.findIndex})
	}
	return out
}

// ensurePreviewMatchVisible scrolls the preview the least it can to
// bring the current hit onto the screen — the preview's EnsureVisible.
// Every query change and every next/prev runs it, because a highlight
// the reader cannot see is exactly the bug this path exists to fix.
func (a *App) ensurePreviewMatchVisible(st *mdPreviewState) {
	m, ok := st.currentFindMatch()
	if !ok {
		return
	}
	_, _, _, eh := a.editorRect()
	if eh < 1 {
		return
	}
	if m.Line < st.scroll {
		st.scroll = m.Line
		return
	}
	if m.Line >= st.scroll+eh {
		st.scroll = m.Line - eh + 1
	}
}

// previewFindAgain is Esc ; over the rendered page: relight a search
// the bar suspended when it closed, or step to the next hit. Reports
// false when the remembered query has nothing to land on.
func (a *App) previewFindAgain(st *mdPreviewState) bool {
	if st.findSuspended {
		st.setFindQuery(st.findQuery, st.findCase)
	} else if !st.findStep(1) {
		return false
	}
	if _, ok := st.currentFindMatch(); !ok {
		return false
	}
	a.ensurePreviewMatchVisible(st)
	return true
}

// scrollMdPreview moves the preview by delta rows, clamped at the top;
// the bottom clamp lives in drawMdPreview where the height is known.
func (st *mdPreviewState) scrollBy(delta int) {
	st.scroll += delta
	if st.scroll < 0 {
		st.scroll = 0
	}
}

// handleMdPreviewKey consumes every key while the preview is up:
// arrows and paging scroll, everything else reminds the user the view
// is read-only. Returning true means the caller must not let the key
// reach the buffer. Esc is NOT consumed — it belongs to the leader so
// the menu (and the toggle back) stays reachable.
func (a *App) handleMdPreviewKey(st *mdPreviewState, ev *tcell.EventKey) bool {
	_, _, _, eh := a.editorRect()
	switch ev.Key() {
	case tcell.KeyUp:
		st.scrollBy(-1)
	case tcell.KeyDown:
		st.scrollBy(1)
	case tcell.KeyPgUp:
		st.scrollBy(-eh)
	case tcell.KeyPgDn:
		st.scrollBy(eh)
	case tcell.KeyHome:
		st.scroll = 0
	case tcell.KeyEnd:
		st.scroll = len(st.lines)
	default:
		a.flash("Preview is read-only — ≡ → Edit Markdown to edit")
	}
	return true
}

// mdPreviewPress handles a mouse press inside the editor rect while the
// preview is up: a press on the content anchors a drag-selection over
// the RENDERED text — skiff's select-to-copy works in the preview
// exactly as in the editor, it just selects what the reader sees
// instead of markdown syntax. Returns true when the press should arm
// the preview drag. The scrollbar column is not content: the
// dispatcher claims it first (mdPreviewScrollbarHit, mouse.go) so the
// thumb gets the same press-and-drag contract as the editor's bar,
// and a press there never starts a selection.
func (a *App) mdPreviewPress(st *mdPreviewState, x, y int) bool {
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew-1 || y < ey || y >= ey+eh {
		return false
	}
	pos := a.mdPreviewHit(st, x, y)
	st.selA, st.selB = pos, pos
	return true
}

// mdPreviewDragTo extends the selection's moving end to the pointer.
// Coordinates are clamped into the content rect, so dragging past an
// edge selects to the first/last visible column rather than escaping.
func (a *App) mdPreviewDragTo(st *mdPreviewState, x, y int) {
	st.selB = a.mdPreviewHit(st, x, y)
}

// mdPreviewHit maps a screen cell onto rendered-document coordinates,
// walking real cluster widths — the inverse of drawStyledRunes' cell
// advance, so what you click is what you select even through CJK.
func (a *App) mdPreviewHit(st *mdPreviewState, x, y int) previewPos {
	_, ey, ew, eh := a.editorRect()
	y = min(max(y, ey), ey+eh-1)
	line := st.scroll + (y - ey)
	if line >= len(st.lines) {
		line = len(st.lines) - 1
	}
	if line < 0 {
		return previewPos{}
	}
	contentX, _, _ := a.mdPreviewGeom()
	off := min(max(x-contentX, 0), ew)
	col, acc := 0, 0
	for _, ru := range st.lines[line] {
		w := uniseg.StringWidth(string(ru))
		if acc+w > off {
			break
		}
		acc += w
		col++
	}
	return previewPos{line: line, col: col}
}

// mdPreviewSelectionText extracts the selected rendered text, partial
// end lines respected, interior lines whole, newline-joined.
func mdPreviewSelectionText(st *mdPreviewState) string {
	a, b := st.selA, st.selB
	if b.less(a) {
		a, b = b, a
	}
	if a == b {
		return ""
	}
	if a.line == b.line {
		runes := []rune(st.lines[a.line])
		return string(runes[min(a.col, len(runes)):min(b.col, len(runes))])
	}
	var parts []string
	first := []rune(st.lines[a.line])
	parts = append(parts, string(first[min(a.col, len(first)):]))
	for i := a.line + 1; i < b.line; i++ {
		parts = append(parts, st.lines[i])
	}
	last := []rune(st.lines[b.line])
	parts = append(parts, string(last[:min(b.col, len(last))]))
	return strings.Join(parts, "\n")
}

// copyMdPreviewSelection is the release half of select-to-copy in the
// preview: same clipboard path, same flash vocabulary as the editor's
// copySelection, and the selection stays highlighted afterwards like a
// terminal's own select-to-copy would.
func (a *App) copyMdPreviewSelection(st *mdPreviewState) {
	txt := mdPreviewSelectionText(st)
	if txt == "" {
		return
	}
	a.clipBuf = txt
	if err := clipboard.CopyToSystem(txt); err != nil {
		if errors.Is(err, clipboard.ErrTooLarge) {
			a.flash("Selection too large for the terminal clipboard — copied inside skiff only")
			return
		}
		a.flash("Copied (system clipboard unavailable)")
		return
	}
	a.flash("Copied")
}

// mdPreviewSelRange returns the selected rune range on rendered line i
// as a half-open [from,to), or (0,0) when the line has no selection.
func (st *mdPreviewState) selRange(i int) (int, int) {
	a, b := st.selA, st.selB
	if b.less(a) {
		a, b = b, a
	}
	if a == b || i < a.line || i > b.line {
		return 0, 0
	}
	from, to := 0, len([]rune(st.lines[i]))
	if i == a.line {
		from = a.col
	}
	if i == b.line {
		to = b.col
	}
	if from >= to {
		return 0, 0
	}
	return from, to
}

// drawMdPreview paints the rendered document into the editor rect: the
// cached lines from the scroll offset down, a scrollbar when the
// document is taller than the view, re-rendering first if the width
// changed since the cache was built.
func (a *App) drawMdPreview(tab *editor.Tab, st *mdPreviewState, x, y, w, h int) {
	if _, w, wide := a.mdPreviewGeom(); st.width != w || st.wide != wide || st.th != a.theme {
		fresh := a.renderMdPreview(tab)
		fresh.scroll = st.scroll
		fresh.carryFind(st)
		*st = *fresh
	}
	bg := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	fillRect(a.screen, x, y, w, h, bg)

	if max := len(st.lines) - h; st.scroll > max {
		st.scroll = max
	}
	if st.scroll < 0 {
		st.scroll = 0
	}
	// Text starts at the shared origin and may paint up to the
	// scrollbar column; the wrapper already fit the budget, the clip
	// only guards a narrower-than-cached frame mid-resize.
	contentX, _, _ := a.mdPreviewGeom()
	maxW := x + w - 1 - contentX
	for row := 0; row < h; row++ {
		i := st.scroll + row
		if i >= len(st.lines) {
			break
		}
		selFrom, selTo := st.selRange(i)
		hl := previewLineHL{selFrom: selFrom, selTo: selTo, matches: st.findSpansOn(i)}
		drawStyledRunes(a.screen, contentX, y+row, maxW, st.lines[i], st.styles[i],
			hl, a.theme)
	}
	if thumb, size, ok := scrollbar.Geom(len(st.lines), h, st.scroll); ok {
		barX := x + w - 1
		track := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Subtle)
		thumbSt := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Muted)
		for row := 0; row < h; row++ {
			glyph, stl := scrollbar.Track, track
			if row >= thumb && row < thumb+size {
				glyph, stl = scrollbar.Thumb, thumbSt
			}
			a.screen.SetContent(barX, y+row, glyph, nil, stl)
		}
	}
	a.screen.HideCursor()
}

// drawStyledRunes paints one pre-styled line, advancing by real cell
// widths so CJK and emoji land where the wrapper measured them; content
// past maxW is clipped (the wrapper already fit the budget — this only
// guards a narrower-than-cached frame mid-resize). Runes inside the
// half-open [hl.selFrom, hl.selTo) selection range repaint on the
// theme's Selection background, with SelectionFg keeping their syntax
// color only while it stays readable — the editor's own selection
// rule. Find hits repaint last and so win over the selection, exactly
// as Tab.cellStyle orders the two in the editor body.
func drawStyledRunes(scr tcell.Screen, x, y, maxW int, s string, sts []tcell.Style,
	hl previewLineHL, th theme.Theme) {
	col := 0
	for i, ru := range []rune(s) {
		w := uniseg.StringWidth(string(ru))
		if w == 0 {
			continue
		}
		if col+w > maxW {
			return
		}
		st := tcell.StyleDefault
		if i < len(sts) {
			st = sts[i]
		}
		if i >= hl.selFrom && i < hl.selTo {
			fg, _, _ := st.Decompose()
			st = st.Background(th.Selection).Foreground(th.SelectionFg(fg))
		}
		if sp, ok := hl.spanAt(i); ok {
			// Same story as the editor: on a degraded palette the amber
			// tints are gone and Attrs carries the hit — reverse for
			// every match, reverse+bold+underline for the current one.
			if sp.current {
				st = theme.WithAttrs(st.Background(th.FindCurrent).Foreground(th.BG), th.Attrs.FindCurrent)
			} else {
				st = theme.WithAttrs(st.Background(th.FindMatch).Foreground(th.Text), th.Attrs.FindMatch)
			}
		}
		scr.SetContent(x+col, y, ru, nil, st)
		col += w
	}
}
