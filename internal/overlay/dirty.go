// =============================================================================
// File: internal/overlay/dirty.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// Dirty geometry. Wider than the confirm so the three buttons sit
// comfortably on one row; the columns center the trio in the modal.
const (
	dirtyWidth  = 60
	dirtyHeight = 9

	dirtyBtnCancelX  = 5
	dirtyBtnCancelW  = 10 // "[ Cancel ]"
	dirtyBtnDiscardX = 22
	dirtyBtnDiscardW = 11 // "[ Discard ]"
	dirtyBtnSaveX    = 42
	dirtyBtnSaveW    = 8 // "[ Save ]"
)

// Dirty is the unsaved-changes overlay: Cancel / Discard / Save on one
// row. Default focus is Cancel so a stray Enter is non-destructive —
// the same safety pattern the delete confirm uses.
//
// Rows (relY): 0 border · 1 title · 2 divider · 3 blank · 4 message ·
// 5 buttons · 6–7 blank · 8 border.
type Dirty struct {
	Title   string
	Message string
	// Hover is the focused button: 0 = Cancel, 1 = Discard, 2 = Save.
	Hover int
	Theme theme.Theme

	Size  func() (w, h int)
	Close func()
	// OnSave runs after Close when the user picks Save (typically:
	// save the tab(s), then proceed); OnDiscard when they pick Discard
	// (skip saving, proceed anyway). Cancel just dismisses.
	OnSave    func()
	OnDiscard func()
}

// rect computes the dirty modal's centered rectangle.
func (d *Dirty) rect() Rect {
	w, h := d.Size()
	return Centered(w, h, dirtyWidth, dirtyHeight)
}

// HandleKey: Left/Right and Tab cycle focus across the three buttons,
// Enter activates the focused one, Esc cancels.
func (d *Dirty) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		d.Close()
	case tcell.KeyEnter:
		d.activate(d.Hover)
	case tcell.KeyLeft:
		if d.Hover > 0 {
			d.Hover--
		}
	case tcell.KeyRight:
		if d.Hover < 2 {
			d.Hover++
		}
	case tcell.KeyTab:
		d.Hover = (d.Hover + 1) % 3
	}
}

// HandleMouse: hovering a button highlights it; clicking activates; a
// click outside cancels — same as the confirm modal.
func (d *Dirty) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := d.rect()
	if x >= r.X && x < r.X+r.W && y == r.Y+5 {
		if idx := dirtyButtonAtRelX(x - r.X); idx >= 0 {
			d.Hover = idx
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		d.Close()
		return
	}
	if y == r.Y+5 {
		if idx := dirtyButtonAtRelX(x - r.X); idx >= 0 {
			d.activate(idx)
		}
	}
}

// Draw renders the modal: frame, centered rune-safe message, and the
// button trio — Cancel neutral, Discard red (destructive), Save in the
// accent so it reads as the productive default.
func (d *Dirty) Draw(scr tcell.Screen) {
	r := d.rect()
	th := d.Theme
	DrawFrame(scr, r, d.Title, th)

	bg := th.LineHL
	bodyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	msg := trimRunes(d.Message, r.W-4)
	drawText(scr, r.X+(r.W-runeLen(msg))/2, r.Y+4, msg, bodyStyle)

	DrawButton(scr, r.X+dirtyBtnCancelX, r.Y+5, "[ Cancel ]", bg, th.Text, d.Hover == 0)
	DrawButton(scr, r.X+dirtyBtnDiscardX, r.Y+5, "[ Discard ]", bg, th.Error, d.Hover == 1)
	DrawButton(scr, r.X+dirtyBtnSaveX, r.Y+5, "[ Save ]", bg, th.Accent, d.Hover == 2)
	scr.HideCursor()
}

// activate runs one button: 0 Cancel, 1 Discard, 2 Save — always
// capture-then-close.
func (d *Dirty) activate(idx int) {
	switch idx {
	case 0:
		d.Close()
	case 1:
		cb := d.OnDiscard
		d.Close()
		if cb != nil {
			cb()
		}
	case 2:
		cb := d.OnSave
		d.Close()
		if cb != nil {
			cb()
		}
	}
}

// dirtyButtonAtRelX maps an x offset within the modal to a button index
// (0=Cancel, 1=Discard, 2=Save) or -1 when the offset misses every
// button — one geometry source for hover and click.
func dirtyButtonAtRelX(rx int) int {
	switch {
	case rx >= dirtyBtnCancelX && rx < dirtyBtnCancelX+dirtyBtnCancelW:
		return 0
	case rx >= dirtyBtnDiscardX && rx < dirtyBtnDiscardX+dirtyBtnDiscardW:
		return 1
	case rx >= dirtyBtnSaveX && rx < dirtyBtnSaveX+dirtyBtnSaveW:
		return 2
	}
	return -1
}
