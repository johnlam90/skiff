// =============================================================================
// File: internal/overlay/chrome.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
	"github.com/johnlam90/skiff/internal/theme"
)

// DrawFrame paints the chrome every overlay shares: a filled body, a
// single-line border, the title row ("<title>   esc") and the divider
// under it. Overlays draw their content inside, starting at r.Y+3.
func DrawFrame(scr tcell.Screen, r Rect, title string, th theme.Theme) {
	bg := th.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(th.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)

	fillRect(scr, r.X, r.Y, r.W, r.H, bgStyle)
	drawBorder(scr, r.X, r.Y, r.W, r.H, borderStyle)
	drawHDivider(scr, r.X, r.Y+2, r.W, borderStyle)
	// The title's budget is the interior (r.W-2) minus the right-aligned
	// hint and one gap cell before it — a long path used to paint
	// straight through the ┐ border onto whatever sat behind the frame.
	hint := "esc "
	hintW := runeLen(hint)
	titleBudget := r.W - 2 - hintW - 1
	drawText(scr, r.X+1, r.Y+1, titleBudget, trimRunes(" "+title, titleBudget), titleStyle)
	drawText(scr, r.X+r.W-1-hintW, r.Y+1, hintW, hint, mutedStyle)
}

// DrawButton renders a bracketed-label button at (x, y). The focused
// button inverts — label on a tinted block — matching the app's
// existing drawButton so converted modals are pixel-identical.
func DrawButton(scr tcell.Screen, x, y int, label string, modalBG, fg tcell.Color, focused bool) {
	st := tcell.StyleDefault.Background(modalBG).Foreground(fg).Bold(true)
	if focused {
		st = tcell.StyleDefault.Background(fg).Foreground(modalBG).Bold(true)
	}
	// The label is its own budget: buttons are fixed chrome whose fit is
	// guaranteed by the app's minWidth floor (see CLAUDE.md), and
	// DrawButton has no rect to derive a tighter bound from.
	drawText(scr, x, y, runeLen(label), label, st)
}

// fillRect paints a w×h cell rectangle with st.
func fillRect(scr tcell.Screen, x, y, w, h int, st tcell.Style) {
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, st)
		}
	}
}

// drawBorder draws a single-line box border around the rectangle.
func drawBorder(scr tcell.Screen, x, y, w, h int, st tcell.Style) {
	scr.SetContent(x, y, '┌', nil, st)
	scr.SetContent(x+w-1, y, '┐', nil, st)
	scr.SetContent(x, y+h-1, '└', nil, st)
	scr.SetContent(x+w-1, y+h-1, '┘', nil, st)
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, '─', nil, st)
		scr.SetContent(cx, y+h-1, '─', nil, st)
	}
	for cy := y + 1; cy < y+h-1; cy++ {
		scr.SetContent(x, cy, '│', nil, st)
		scr.SetContent(x+w-1, cy, '│', nil, st)
	}
}

// drawHDivider draws a horizontal divider with ├ ┤ end caps inside an
// existing border.
func drawHDivider(scr tcell.Screen, x, y, w int, st tcell.Style) {
	scr.SetContent(x, y, '├', nil, st)
	scr.SetContent(x+w-1, y, '┤', nil, st)
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, '─', nil, st)
	}
}

// drawText writes s left-to-right starting at (x, y), clipped to maxW
// cells and cluster-aware — CJK, combining marks and ZWJ emoji advance
// by their real cell widths. There is deliberately no unbounded variant:
// every caller passes a budget derived from its frame rect, because the
// old clip-free drawText let long titles paint through the border.
func drawText(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) {
	textdraw.DrawClipped(scr, x, y, maxW, s, st)
}

// runeLen returns the visible cell count of s, measured cluster-aware
// via textdraw — a CJK ideograph counts 2, a combining mark 0 — so
// right-aligned hints, tags and centered messages line up for non-ASCII
// text too.
func runeLen(s string) int {
	return textdraw.Width(s)
}

// trimRunes truncates s to max visible cells, cluster-safe, appending an
// ellipsis when anything was cut — a byte-slice truncation once split
// multibyte filenames into replacement garbage, and a rune-count cut
// still overflowed the frame on wide glyphs.
func trimRunes(s string, max int) string {
	return textdraw.ClipEllipsis(s, max)
}
