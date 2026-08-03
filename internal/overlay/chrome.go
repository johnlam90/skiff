// =============================================================================
// File: internal/overlay/chrome.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

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
	drawText(scr, r.X+1, r.Y+1, " "+title, titleStyle)
	hint := "esc "
	drawText(scr, r.X+r.W-1-runeLen(hint), r.Y+1, hint, mutedStyle)
}

// DrawButton renders a bracketed-label button at (x, y). The focused
// button inverts — label on a tinted block — matching the app's
// existing drawButton so converted modals are pixel-identical.
func DrawButton(scr tcell.Screen, x, y int, label string, modalBG, fg tcell.Color, focused bool) {
	st := tcell.StyleDefault.Background(modalBG).Foreground(fg).Bold(true)
	if focused {
		st = tcell.StyleDefault.Background(fg).Foreground(modalBG).Bold(true)
	}
	drawText(scr, x, y, label, st)
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

// drawText writes s left-to-right starting at (x, y), one cell per rune.
func drawText(scr tcell.Screen, x, y int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// runeLen returns the visible cell count of s (one cell per rune).
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
