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
		a.flash("Editing Markdown")
		return
	}
	if a.mdPreview == nil {
		a.mdPreview = map[*editor.Tab]*mdPreviewState{}
	}
	a.mdPreview[tab] = a.renderMdPreview(tab)
	a.flash("Previewing Markdown — ≡ → Edit Markdown to edit")
}

// previewMarkdownLabel names the toggle row for the current state.
func (a *App) previewMarkdownLabel() string {
	if t := a.activeMarkdownTab(); t != nil && a.mdPreview[t] != nil {
		return "Edit Markdown"
	}
	return "Preview Markdown"
}

// mdPreviewContentWidth is the wrap budget for the current editor rect:
// one column of left padding, one for the scrollbar, one of right
// breathing room.
func (a *App) mdPreviewContentWidth() int {
	_, _, ew, _ := a.editorRect()
	w := ew - 3
	if w < 4 {
		w = 4
	}
	return w
}

// renderMdPreview renders tab's buffer at the current width. The buffer
// is read on the event loop (the only place it may be read), and the
// document's own line ending is irrelevant to markdown, so the plain
// LF join is correct here — nothing is ever written back.
func (a *App) renderMdPreview(tab *editor.Tab) *mdPreviewState {
	w := a.mdPreviewContentWidth()
	lines, styles := mdrender.Render([]byte(tab.Buffer.String()), w, a.theme)
	return &mdPreviewState{lines: lines, styles: styles, width: w, th: a.theme}
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
	a.mdPreview[tab] = fresh
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
// preview is up: a click on the scrollbar column jumps; a press on the
// content anchors a drag-selection over the RENDERED text — skiff's
// select-to-copy works in the preview exactly as in the editor, it
// just selects what the reader sees instead of markdown syntax.
// Returns true when the press should arm the preview drag.
func (a *App) mdPreviewPress(st *mdPreviewState, x, y int) bool {
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return false
	}
	if x == ex+ew-1 {
		if _, _, ok := scrollbar.Geom(len(st.lines), eh, st.scroll); ok {
			st.scroll = scrollbar.TargetForThumb(len(st.lines), eh, y-ey)
		}
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
	ex, ey, ew, eh := a.editorRect()
	y = min(max(y, ey), ey+eh-1)
	line := st.scroll + (y - ey)
	if line >= len(st.lines) {
		line = len(st.lines) - 1
	}
	if line < 0 {
		return previewPos{}
	}
	off := min(max(x-(ex+1), 0), ew)
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
	if st.width != a.mdPreviewContentWidth() || st.th != a.theme {
		fresh := a.renderMdPreview(tab)
		fresh.scroll = st.scroll
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
	for row := 0; row < h; row++ {
		i := st.scroll + row
		if i >= len(st.lines) {
			break
		}
		selFrom, selTo := st.selRange(i)
		drawStyledRunes(a.screen, x+1, y+row, w-2, st.lines[i], st.styles[i],
			selFrom, selTo, a.theme)
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
// half-open [selFrom, selTo) selection range repaint on the theme's
// Selection background, with SelectionFg keeping their syntax color
// only while it stays readable — the editor's own selection rule.
func drawStyledRunes(scr tcell.Screen, x, y, maxW int, s string, sts []tcell.Style,
	selFrom, selTo int, th theme.Theme) {
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
		if i >= selFrom && i < selTo {
			fg, _, _ := st.Decompose()
			st = st.Background(th.Selection).Foreground(th.SelectionFg(fg))
		}
		scr.SetContent(x+col, y, ru, nil, st)
		col += w
	}
}
