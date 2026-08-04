// =============================================================================
// File: internal/overlay/info.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// infoWidth is wider than the confirm so a command's stderr lines fit
// without aggressive truncation; the height tracks the line count.
const infoWidth = 84

// infoChromeRows is the non-body height: border, title, divider, and
// the OK button row with its padding.
const infoChromeRows = 7

// Info is the single-button report overlay: a scrollable, left-aligned
// body (command stderr, a git diff preview) and one centered OK button.
// Any "I'm done" key — Esc, Enter, Tab — dismisses it.
type Info struct {
	Title string
	Lines []string
	Theme theme.Theme

	Size  func() (w, h int)
	Close func()

	// scroll is the first visible body line; scrolling clamps it.
	scroll int
}

// rect computes the info rectangle: infoWidth, or the whole screen when
// the terminal is narrower than that. Without the clamp the frame's
// right border and every line's tail fall off the edge of an 80-column
// tmux pane — and the surfaces that use Info (a failed command's
// stderr, the shortcut reference) are needed most exactly there.
// Height tracks the visible body rows.
func (n *Info) rect() Rect {
	w, h := n.Size()
	fw := infoWidth
	if w < fw {
		fw = w
	}
	return Centered(w, h, fw, n.bodyRows()+infoChromeRows)
}

// bodyRows returns the visible body height: the screen minus chrome,
// clamped to the line count and never below one row.
func (n *Info) bodyRows() int {
	_, scrH := n.Size()
	rows := scrH - infoChromeRows
	if rows < 1 {
		return 1
	}
	if len(n.Lines) < rows {
		if len(n.Lines) < 1 {
			return 1
		}
		return len(n.Lines)
	}
	return rows
}

// Scroll exposes the first visible line index for tests.
func (n *Info) Scroll() int { return n.scroll }

// ScrollBy moves the body window by delta lines, clamped to the content.
func (n *Info) ScrollBy(delta int) {
	maxScroll := len(n.Lines) - n.bodyRows()
	if maxScroll < 0 {
		maxScroll = 0
	}
	n.scroll += delta
	if n.scroll < 0 {
		n.scroll = 0
	}
	if n.scroll > maxScroll {
		n.scroll = maxScroll
	}
}

// HandleKey: Esc/Enter/Tab dismiss; arrows and PgUp/PgDn scroll.
func (n *Info) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc, tcell.KeyEnter, tcell.KeyTab:
		n.Close()
	case tcell.KeyUp:
		n.ScrollBy(-1)
	case tcell.KeyDown:
		n.ScrollBy(1)
	case tcell.KeyPgUp:
		n.ScrollBy(-n.bodyRows())
	case tcell.KeyPgDn:
		n.ScrollBy(n.bodyRows())
	}
}

// HandleMouse: wheel scrolls the body (WheelUp/WheelDown — the masks
// tcell actually emits for wheels and trackpads); a click on OK or
// outside the modal dismisses.
func (n *Info) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := n.rect()
	if btn&tcell.WheelUp != 0 {
		n.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		n.ScrollBy(3)
		return
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		n.Close()
		return
	}
	btnY := r.Y + r.H - 3
	btnX := r.X + (r.W-10)/2
	if y == btnY && x >= btnX && x < btnX+10 {
		n.Close()
	}
}

// Draw renders the info overlay: frame, the visible slice of the body
// left-aligned (stderr usually starts with file paths that read poorly
// centered), diff-aware line colors, and the centered OK button.
func (n *Info) Draw(scr tcell.Screen) {
	r := n.rect()
	th := n.Theme
	DrawFrame(scr, r, n.Title, th)

	bg := th.LineHL
	n.ScrollBy(0) // re-clamp after any resize
	rows := n.bodyRows()
	end := n.scroll + rows
	if end > len(n.Lines) {
		end = len(n.Lines)
	}
	for i, line := range n.Lines[n.scroll:end] {
		// trimRunes appends the ellipsis a hard rune-slice cut used to
		// drop, so a clipped stderr path no longer looks like a
		// complete-but-wrong path. Style is picked from the untruncated
		// line: a diff marker lives in column 0 either way, and the
		// ellipsis must not recolor the row.
		st := DiffLineStyle(th, bg, line)
		drawText(scr, r.X+2, r.Y+3+i, trimRunes(line, r.W-4), st)
	}
	DrawButton(scr, r.X+(r.W-10)/2, r.Y+r.H-3, "[  OK  ]", bg, th.Accent, true)
	scr.HideCursor()
}

// DiffLineStyle colors one line of a git diff preview: additions,
// deletions, hunk headers, and file headers each get their own color so
// the preview reads like a real diff. Shared with the diff view's
// raw-preview path.
func DiffLineStyle(th theme.Theme, bg tcell.Color, line string) tcell.Style {
	style := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
		return style.Foreground(th.Muted)
	}
	if strings.HasPrefix(line, "+") {
		return style.Foreground(th.GitAdded)
	}
	if strings.HasPrefix(line, "-") {
		return style.Foreground(th.GitDeleted)
	}
	if strings.HasPrefix(line, "@@") {
		return style.Foreground(th.AccentSoft).Bold(true)
	}
	return style
}
