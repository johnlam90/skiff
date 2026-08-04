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

// ConfirmBodyWidth is the frame width a Confirm uses when it carries a
// multi-line Body. Wider than the one-line form on purpose: the flows
// that need several rows are exactly the ones whose content must not be
// abbreviated. The formatter-trust prompt lists the argv it is asking
// permission to execute, and an elided argv is worthless consent.
const ConfirmBodyWidth = 84

// ConfirmBodyTextWidth is the usable text width inside a Body-mode
// confirm: the frame minus a border cell and a padding cell on each
// side. Exported so callers can wrap their own lines to fit instead of
// discovering the truncation at draw time.
const ConfirmBodyTextWidth = ConfirmBodyWidth - 4

// confirmMaxBodyRows caps the visible body so a config declaring dozens
// of commands cannot push the buttons off a small screen. Rows past the
// cap stay reachable by scrolling — hiding them outright would be the
// same consent hole as truncating an argv.
const confirmMaxBodyRows = 14

// confirmChromeRows is the non-body height: border, title, divider,
// blank, the button row, two blanks, and the bottom border. Body rows
// are added on top, so a one-row body reproduces confirmHeight exactly.
const confirmChromeRows = confirmHeight - 1

// Confirm is the Yes/No overlay: a centered box with a title, a body,
// and a No / Yes button row. Default focus is No so an accidental Enter
// is harmless — important for destructive actions like Delete. Esc, a No
// click, or a click outside runs OnCancel.
//
// Rows (relY), one-line form: 0 border · 1 title · 2 divider · 3 blank ·
// 4 message · 5 buttons · 6–7 blank · 8 border. A multi-line Body grows
// rows 4..4+n-1 and pushes the button row down with it.
type Confirm struct {
	Title   string
	Message string
	// Body, when non-empty, replaces the single centered Message with
	// left-aligned, scrollable rows in a wider frame. Reach for it when
	// one line cannot carry informed consent — the formatter-trust
	// prompt has to show every command it would run.
	Body []string
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

	// scroll is the index of the first visible Body row.
	scroll int
}

// frameWidth returns the frame width: the wide form for a multi-line
// Body, the classic 54-cell box otherwise.
func (c *Confirm) frameWidth() int {
	if len(c.Body) == 0 {
		return confirmWidth
	}
	return ConfirmBodyWidth
}

// bodyRows returns how many body rows are visible: one for the Message
// form, otherwise the Body length clamped by the row cap and by what
// the screen can fit above the button row.
func (c *Confirm) bodyRows() int {
	n := len(c.Body)
	if n == 0 {
		return 1
	}
	if n > confirmMaxBodyRows {
		n = confirmMaxBodyRows
	}
	if _, h := c.Size(); n > h-confirmChromeRows {
		n = h - confirmChromeRows
	}
	if n < 1 {
		n = 1
	}
	return n
}

// buttonRow returns the body-relative row the No / Yes pair is painted
// on. Draw and hit-test both derive it here so they cannot drift.
func (c *Confirm) buttonRow() int { return 4 + c.bodyRows() }

// buttonOffset shifts the button columns when the frame is wider than
// the classic box, keeping the pair in the same relative position
// instead of hugging the left edge. Zero for the 54-cell form, so the
// one-line confirm's geometry is byte-identical to before Body existed.
func (c *Confirm) buttonOffset() int { return (c.frameWidth() - confirmWidth) / 2 }

// bar describes the body's scroll indicator inside frame r. total is
// len(Body), which is zero in the classic Message form — so a bar is
// arithmetically impossible there and the pinned 54×9 geometry keeps
// every cell it had. Draw and HandleMouse both derive the bar here, so
// the painted column and the clickable column are the same column.
func (c *Confirm) bar(r Rect) bodyBar {
	return bodyBar{
		x:      barColumn(r),
		top:    r.Y + 4,
		viewH:  c.bodyRows(),
		total:  len(c.Body),
		scroll: c.scroll,
	}
}

// Scroll exposes the first visible body row for tests.
func (c *Confirm) Scroll() int { return c.scroll }

// ScrollBy moves the Body window by delta rows, clamped to the content.
// A body that fits entirely pins to zero, so the one-line Message form
// can never scroll its text out of view.
func (c *Confirm) ScrollBy(delta int) {
	maxScroll := len(c.Body) - c.bodyRows()
	if maxScroll <= 0 {
		c.scroll = 0
		return
	}
	c.scroll += delta
	if c.scroll < 0 {
		c.scroll = 0
	}
	if c.scroll > maxScroll {
		c.scroll = maxScroll
	}
}

// rect computes the confirm's centered rectangle. Height tracks the
// body so the button row never lands on top of the content.
func (c *Confirm) rect() Rect {
	w, h := c.Size()
	return Centered(w, h, c.frameWidth(), confirmChromeRows+c.bodyRows())
}

// HandleKey: Left/Right/Tab move focus between No and Yes, Enter
// activates the focused button, Esc cancels. Up/Down and PgUp/PgDn
// scroll a multi-line Body — those keys are unused by the button row,
// so a long body stays readable without stealing focus movement.
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
	case tcell.KeyUp:
		c.ScrollBy(-1)
	case tcell.KeyDown:
		c.ScrollBy(1)
	case tcell.KeyPgUp:
		c.ScrollBy(-c.bodyRows())
	case tcell.KeyPgDn:
		c.ScrollBy(c.bodyRows())
	}
}

// HandleMouse: the wheel scrolls a multi-line body, the scroll
// indicator's column jumps the thumb to the pressed row, hovering a
// button highlights it, clicking activates, and clicks outside cancel.
func (c *Confirm) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := c.rect()
	if btn&tcell.WheelUp != 0 {
		c.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		c.ScrollBy(3)
		return
	}
	btnY := r.Y + c.buttonRow()
	noX := c.buttonOffset() + confirmBtnNoX
	yesX := c.buttonOffset() + confirmBtnYesX
	if x >= r.X && x < r.X+r.W && y == btnY {
		relX := x - r.X
		switch {
		case relX >= noX && relX < noX+confirmBtnNoW:
			c.Hover = 0
		case relX >= yesX && relX < yesX+confirmBtnYesW:
			c.Hover = 1
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	// The indicator owns its column outright — claimed before the
	// button zones and the outside-click test so the one cell that
	// looks like a scrollbar can never read as a dismissal.
	if b := c.bar(r); b.hit(x, y) {
		c.scroll = b.target(y)
		return
	}
	if !r.Contains(x, y) {
		c.cancel()
		return
	}
	if y == btnY {
		relX := x - r.X
		switch {
		case relX >= noX && relX < noX+confirmBtnNoW:
			c.cancel()
		case relX >= yesX && relX < yesX+confirmBtnYesW:
			c.yes()
		}
	}
}

// Draw renders the confirm: frame, the body (a centered rune-safe
// message, or the visible slice of a left-aligned multi-line Body —
// commands and paths read poorly centered), and the No / Yes buttons,
// Yes in the error color because every Yes in the editor is destructive.
func (c *Confirm) Draw(scr tcell.Screen) {
	r := c.rect()
	th := c.Theme
	DrawFrame(scr, r, c.Title, th)

	bg := th.LineHL
	bodyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	if len(c.Body) == 0 {
		msg := trimRunes(c.Message, r.W-4)
		drawText(scr, r.X+(r.W-runeLen(msg))/2, r.Y+4, msg, bodyStyle)
	} else {
		for i, rows := 0, c.bodyRows(); i < rows && c.scroll+i < len(c.Body); i++ {
			drawText(scr, r.X+2, r.Y+4+i, trimRunes(c.Body[c.scroll+i], r.W-4), bodyStyle)
		}
		// Painted after the rows so the bar is never overwritten by a
		// body line — the rows clip to r.W-4 and stop two cells short
		// of it, but the paint order is the guarantee.
		c.bar(r).draw(scr, th)
	}

	btnY := r.Y + c.buttonRow()
	DrawButton(scr, r.X+c.buttonOffset()+confirmBtnNoX, btnY, "[  No  ]", bg, th.Text, c.Hover == 0)
	DrawButton(scr, r.X+c.buttonOffset()+confirmBtnYesX, btnY, "[ Yes ]", bg, th.Error, c.Hover == 1)
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
