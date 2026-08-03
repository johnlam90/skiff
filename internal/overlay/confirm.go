// =============================================================================
// File: internal/overlay/confirm.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// Confirm geometry — button columns shared by draw and hit-test.
const (
	confirmWidth  = 54
	confirmHeight = 9

	confirmBtnNoX  = 14
	confirmBtnNoW  = 8 // "[  No  ]"
	confirmBtnYesX = 28
	confirmBtnYesW = 7 // "[ Yes ]"
)

// Confirm is the Yes/No overlay: a centered box with a title, a
// one-line message, and a No / Yes button row. Default focus is No so
// an accidental Enter is harmless — important for destructive actions
// like Delete. Esc, a No click, or a click outside runs OnCancel.
//
// Rows (relY): 0 border · 1 title · 2 divider · 3 blank · 4 message ·
// 5 buttons · 6–7 blank · 8 border.
type Confirm struct {
	Title   string
	Message string
	// Hover is the highlighted button: 0 = No (the safe default),
	// 1 = Yes.
	Hover int
	Theme theme.Theme

	// Size and Close are injected by the opener — see Prompt.
	Size  func() (w, h int)
	Close func()
	// OnYes runs after Close when the user picks Yes.
	OnYes func()
	// OnCancel runs after Close on every dismissal path (Esc, No,
	// outside click). Flows that must record a denial — the formatter
	// trust prompt — set it right after opening; it can never leak to
	// another confirm because it dies with this value.
	OnCancel func()
}

// rect computes the confirm's centered rectangle.
func (c *Confirm) rect() Rect {
	w, h := c.Size()
	return Centered(w, h, confirmWidth, confirmHeight)
}

// HandleKey: Left/Right/Tab move focus between No and Yes, Enter
// activates the focused button, Esc cancels.
func (c *Confirm) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		c.cancel()
	case tcell.KeyEnter:
		if c.Hover == 1 {
			c.yes()
		} else {
			c.cancel()
		}
	case tcell.KeyLeft, tcell.KeyTab:
		// Tab cycles between buttons; Left moves to No.
		if ev.Key() == tcell.KeyTab {
			c.Hover = 1 - c.Hover
		} else {
			c.Hover = 0
		}
	case tcell.KeyRight:
		c.Hover = 1
	}
}

// HandleMouse: hovering a button highlights it; clicking activates;
// clicks outside cancel.
func (c *Confirm) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := c.rect()
	if x >= r.X && x < r.X+r.W && y == r.Y+5 {
		relX := x - r.X
		switch {
		case relX >= confirmBtnNoX && relX < confirmBtnNoX+confirmBtnNoW:
			c.Hover = 0
		case relX >= confirmBtnYesX && relX < confirmBtnYesX+confirmBtnYesW:
			c.Hover = 1
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		c.cancel()
		return
	}
	if y == r.Y+5 {
		relX := x - r.X
		switch {
		case relX >= confirmBtnNoX && relX < confirmBtnNoX+confirmBtnNoW:
			c.cancel()
		case relX >= confirmBtnYesX && relX < confirmBtnYesX+confirmBtnYesW:
			c.yes()
		}
	}
}

// Draw renders the confirm: frame, centered rune-safe message, and the
// No / Yes buttons — Yes in the error color because every Yes in the
// editor is destructive.
func (c *Confirm) Draw(scr tcell.Screen) {
	r := c.rect()
	th := c.Theme
	DrawFrame(scr, r, c.Title, th)

	bg := th.LineHL
	bodyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	msg := trimRunes(c.Message, r.W-4)
	drawText(scr, r.X+(r.W-runeLen(msg))/2, r.Y+4, msg, bodyStyle)

	DrawButton(scr, r.X+confirmBtnNoX, r.Y+5, "[  No  ]", bg, th.Text, c.Hover == 0)
	DrawButton(scr, r.X+confirmBtnYesX, r.Y+5, "[ Yes ]", bg, th.Error, c.Hover == 1)
	scr.HideCursor()
}

// yes closes the overlay then runs OnYes — capture-then-close, so a
// callback that opens the next overlay is never popped by this
// teardown.
func (c *Confirm) yes() {
	cb := c.OnYes
	c.Close()
	if cb != nil {
		cb()
	}
}

// cancel closes the overlay then runs OnCancel, same ordering as yes.
func (c *Confirm) cancel() {
	hook := c.OnCancel
	c.Close()
	if hook != nil {
		hook()
	}
}
