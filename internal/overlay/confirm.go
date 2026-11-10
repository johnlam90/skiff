// =============================================================================
// File: internal/overlay/confirm.go
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

// Confirm geometry — button columns shared by draw and hit-test.
const (
	confirmWidth  = 54
	confirmHeight = 9

	confirmBtnNoX  = 14
	confirmBtnNoW  = 8 // "[  No  ]"
	confirmBtnYesX = 28
	confirmBtnYesW = 7 // "[ Yes ]"

	// confirmBtnGap is the space the pinned columns leave between No and
	// Yes. buttonRowCols starts from it when a narrow terminal forces the
	// pair to be re-placed, so the squeezed row keeps the spacing users
	// know for as long as the frame can afford it.
	confirmBtnGap = confirmBtnYesX - (confirmBtnNoX + confirmBtnNoW)
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

// confirmSqueezedChromeRows is the chrome a windowed body can fall back
// to on a short screen: the same rows minus one of the two blanks under
// the buttons. It only ever applies to a multi-row body that would
// otherwise cover the status row — the one-line form keeps
// confirmHeight, which is what sets minHeight.
const confirmSqueezedChromeRows = confirmChromeRows - 1

// confirmDefaultLabels are the stock captions. A caller that leaves
// Labels zero gets these plus the pinned columns, so every existing
// Yes/No confirm renders byte-identically.
var confirmDefaultLabels = [2]string{"[  No  ]", "[ Yes ]"}

// Confirm is the Yes/No overlay: a centered box with a title, a body,
// and a No / Yes button row. Default focus is No so an accidental Enter
// is harmless — important for destructive actions like Delete. Esc, a No
// click, or a click outside runs OnCancel.
//
// Rows (relY), one-line form: 0 border · 1 title · 2 divider · 3 blank ·
// 4 message · 5 buttons · 6–7 blank · 8 border. A multi-line Body — or a
// Message too long for one row, which is word-wrapped rather than cut
// (see rows) — grows rows 4..4+n-1 and pushes the button row down with
// it; on a screen too short for all of it the body windows and scrolls,
// so the chrome (confirmChromeRows) never grows.
type Confirm struct {
	Title   string
	Message string
	// Body, when non-empty, replaces the single centered Message with
	// left-aligned, scrollable rows in a wider frame. Reach for it when
	// one line cannot carry informed consent — the formatter-trust
	// prompt has to show every command it would run.
	Body []string
	// Labels optionally replaces the two button captions, left (the
	// dismissal) then right (the action), for confirms whose Yes is a
	// named destructive verb — "[ Cancel ] [ Delete branch ]" answers
	// "Delete x? Unmerged work would be lost." where a bare Yes makes
	// the user re-read the question. Leave it zero for No / Yes.
	// Captions carry their own brackets, exactly as Dirty's do, because
	// the bracket style is part of the label; keep them short — the
	// pair has to fit a 38-cell frame at minWidth (see buttonCols).
	Labels [2]string
	// Hover is the highlighted button: 0 = No (the safe default),
	// 1 = Yes.
	Hover int
	// press is the click latch: buttons answer a fresh press, never
	// the motion of a drag that started in the body. See Press.
	press Press
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

// BodyTextWidth returns the usable text width for Body rows at the
// confirm's current frame width — what callers should wrap consent
// text to, so a narrow terminal wraps instead of truncating.
func (c *Confirm) BodyTextWidth() int { return c.frameWidth() - 4 }

// frameWidth returns the frame width: the wide form for a multi-line
// Body, the classic 54-cell box otherwise — either way clamped to the
// terminal, because a frame wider than the screen puts its right border
// and the Yes button in columns that do not exist.
func (c *Confirm) frameWidth() int {
	natural := confirmWidth
	if len(c.Body) > 0 {
		natural = ConfirmBodyWidth
	}
	if c.Size == nil {
		return natural
	}
	w, _ := c.Size()
	return fit(natural, w)
}

// labels returns the captions to draw, falling back to the stock pair.
// Individual empty entries fall back too, so a caller can rename the
// action button without restating the dismissal.
func (c *Confirm) labels() [2]string {
	out := c.Labels
	for i := range out {
		if out[i] == "" {
			out[i] = confirmDefaultLabels[i]
		}
	}
	return out
}

// buttonWidths returns the cell width of each caption — the hit zone
// and the painted label are the same cells only if both read it here.
func (c *Confirm) buttonWidths() (noW, yesW int) {
	l := c.labels()
	return runeLen(l[0]), runeLen(l[1])
}

// buttonCols returns the No and Yes x offsets inside the frame. With
// the stock captions at or above the classic 54 cells they are the
// pinned columns (shifted by buttonOffset for the wide Body form), so
// ordinary terminals render byte-identically; custom Labels, or a
// frame the screen squeezed below 54, lay the pair out with
// buttonRowCols — the only rule that stays right for captions and
// widths this file has not seen.
func (c *Confirm) buttonCols() (noX, yesX int) {
	fw := c.frameWidth()
	if fw >= confirmWidth && c.labels() == confirmDefaultLabels {
		off := c.buttonOffset()
		return off + confirmBtnNoX, off + confirmBtnYesX
	}
	noW, yesW := c.buttonWidths()
	cols := buttonRowCols(fw, confirmBtnGap, []int{noW, yesW})
	return cols[0], cols[1]
}

// rows returns the scrollable body rows: the caller's Body, or — when
// there is none and the one-line Message is wider than the frame's text
// column — the Message word-wrapped to that column. A Message that fits
// returns nil, which is the classic one-line form with its pinned
// geometry. The wrap exists because the alternative was an ellipsis in
// the middle of the question a destructive confirm asks ("Remove the
// last commit? Its changes stay in your …"): the rest of the sentence
// is the consent. Everything that sizes, scrolls or paints the body
// reads it here, so a wrapped Message windows exactly like a Body and
// costs no chrome row.
func (c *Confirm) rows() []string {
	if len(c.Body) > 0 {
		return c.Body
	}
	w := c.BodyTextWidth()
	if w <= 0 || runeLen(c.Message) <= w {
		return nil
	}
	return textdraw.WrapWords(c.Message, w)
}

// bodyRows returns how many body rows are visible: one for the Message
// form, otherwise the row count (Body, or a wrapped Message) clamped by
// the row cap and by what the screen can fit above the button row.
func (c *Confirm) bodyRows() int {
	n := len(c.rows())
	if n == 0 {
		return 1
	}
	if n > confirmMaxBodyRows {
		n = confirmMaxBodyRows
	}
	// The window is what the screen leaves once the status row under
	// the modal is held back (that row is where the confirm's own
	// outcome is flashed — see CLAUDE.md, "minWidth / minHeight are
	// derived floors") and the second of the two blank rows under the
	// buttons is given up, which rect does when it has to. At minHeight
	// that is two rows of body in the same nine-row modal.
	if _, h := c.Size(); n > h-1-confirmSqueezedChromeRows {
		n = h - 1 - confirmSqueezedChromeRows
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
// len(rows()), which is zero in the classic Message form — so a bar is
// arithmetically impossible there and the pinned 54×9 geometry keeps
// every cell it had. Draw and HandleMouse both derive the bar here, so
// the painted column and the clickable column are the same column.
func (c *Confirm) bar(r Rect) Bar {
	return Bar{
		x:      BarColumn(r),
		top:    r.Y + 4,
		viewH:  c.bodyRows(),
		total:  len(c.rows()),
		scroll: c.scroll,
	}
}

// Scroll exposes the first visible body row for tests.
func (c *Confirm) Scroll() int { return c.scroll }

// ScrollBy moves the body window by delta rows, clamped to the content.
// A body that fits entirely pins to zero, so the one-line Message form
// can never scroll its text out of view.
func (c *Confirm) ScrollBy(delta int) {
	maxScroll := len(c.rows()) - c.bodyRows()
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
// body so the button row never lands on top of the content. When the
// full chrome plus the body would cover the status row, the frame sheds
// the spare blank row under the buttons instead (never below the
// squeezed chrome) — rows are positioned from the top, so the button
// row and the hit zones do not move.
func (c *Confirm) rect() Rect {
	w, h := c.Size()
	n := c.bodyRows()
	fh := confirmChromeRows + n
	if fh > h-1 {
		fh = max(h-1, confirmSqueezedChromeRows+n)
	}
	return Centered(w, h, c.frameWidth(), fh)
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
// Activation and dismissal answer a FRESH press only: a drag that
// started in the body and crossed the button row used to confirm.
func (c *Confirm) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := c.rect()
	fresh := c.press.Fresh(btn)
	if btn&tcell.WheelUp != 0 {
		c.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		c.ScrollBy(3)
		return
	}
	btnY := r.Y + c.buttonRow()
	noX, yesX := c.buttonCols()
	noW, yesW := c.buttonWidths()
	if x >= r.X && x < r.X+r.W && y == btnY {
		relX := x - r.X
		switch {
		case relX >= noX && relX < noX+noW:
			c.Hover = 0
		case relX >= yesX && relX < yesX+yesW:
			c.Hover = 1
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	// The indicator owns its column outright — claimed before the
	// button zones and the outside-click test so the one cell that
	// looks like a scrollbar can never read as a dismissal. It follows
	// a drag on purpose, so it reads the raw mask, not the latch.
	if b := c.bar(r); b.Hit(x, y) {
		c.scroll = b.Target(y)
		return
	}
	if !fresh {
		return
	}
	if !r.Contains(x, y) {
		c.cancel()
		return
	}
	if y == btnY {
		relX := x - r.X
		switch {
		case relX >= noX && relX < noX+noW:
			c.cancel()
		case relX >= yesX && relX < yesX+yesW:
			c.yes()
		}
	}
}

// Draw renders the confirm: frame, the body (a centered message —
// wrapped over several centered rows when one cannot hold it — or the
// visible slice of a left-aligned multi-line Body, because commands and
// paths read poorly centered), and the two buttons — the stock No / Yes
// or the caller's Labels — the action one in the error color because
// every Yes in the editor is destructive.
func (c *Confirm) Draw(scr tcell.Screen) {
	r := c.rect()
	th := c.Theme
	// The hint names what Enter would press right now — the focused
	// caption — so a confirm focused on its action says so before the
	// key is hit.
	DrawFrameHint(scr, r, c.Title, EnterHint(c.labels()[c.Hover]), th)

	bg := th.LineHL
	bodyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	body := c.rows()
	if len(body) == 0 {
		msg := trimRunes(c.Message, r.W-4)
		msgX := r.X + (r.W-runeLen(msg))/2
		drawText(scr, msgX, r.Y+4, r.X+r.W-1-msgX, msg, bodyStyle)
	} else {
		c.ScrollBy(0) // re-clamp: a resize can re-wrap the Message shorter
		centered := len(c.Body) == 0
		for i, rows := 0, c.bodyRows(); i < rows && c.scroll+i < len(body); i++ {
			line := trimRunes(body[c.scroll+i], r.W-4)
			x := r.X + 2
			if centered {
				// A wrapped Message is still a sentence, not a
				// listing: each row keeps the one-line form's
				// centring inside the same text column.
				x += (r.W - 4 - runeLen(line)) / 2
			}
			drawText(scr, x, r.Y+4+i, r.W-4, line, bodyStyle)
		}
		// Painted after the rows so the bar is never overwritten by a
		// body line — the rows clip to r.W-4 and stop two cells short
		// of it, but the paint order is the guarantee.
		c.bar(r).Draw(scr, th)
	}

	btnY := r.Y + c.buttonRow()
	noX, yesX := c.buttonCols()
	labels := c.labels()
	DrawButton(scr, r.X+noX, btnY, labels[0], bg, th.Text, c.Hover == 0)
	DrawButton(scr, r.X+yesX, btnY, labels[1], bg, th.Error, c.Hover == 1)
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

// WantsMotion is true: Confirm highlights the Yes/No button under the
// pointer, which needs motion with no button held.
func (c *Confirm) WantsMotion() bool { return true }

// Dismiss is a no-op — the OnCancel hook a trust prompt attaches fires
// from the user's own cancel, not from a teardown that replaced it.
func (c *Confirm) Dismiss() {}
