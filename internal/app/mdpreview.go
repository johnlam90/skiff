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
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/mdrender"
	"github.com/johnlam90/skiff/internal/scrollbar"
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
	return &mdPreviewState{lines: lines, styles: styles, width: w}
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
// preview is up: a click on the scrollbar column jumps, everything else
// is inert (no caret to place, no drag to arm). Returns true when the
// press was inside the preview's domain.
func (a *App) mdPreviewPress(st *mdPreviewState, x, y int) bool {
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return false
	}
	if x == ex+ew-1 {
		if _, _, ok := scrollbar.Geom(len(st.lines), eh, st.scroll); ok {
			st.scroll = scrollbar.TargetForThumb(len(st.lines), eh, y-ey)
		}
	}
	return true
}

// drawMdPreview paints the rendered document into the editor rect: the
// cached lines from the scroll offset down, a scrollbar when the
// document is taller than the view, re-rendering first if the width
// changed since the cache was built.
func (a *App) drawMdPreview(tab *editor.Tab, st *mdPreviewState, x, y, w, h int) {
	if st.width != a.mdPreviewContentWidth() {
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
		drawStyledRunes(a.screen, x+1, y+row, w-2, st.lines[i], st.styles[i])
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
// guards a narrower-than-cached frame mid-resize).
func drawStyledRunes(scr tcell.Screen, x, y, maxW int, s string, sts []tcell.Style) {
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
		scr.SetContent(x+col, y, ru, nil, st)
		col += w
	}
}
