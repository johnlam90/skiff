// =============================================================================
// File: internal/overlay/prompt.go
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

// Prompt geometry. The button columns are offsets into the modal so the
// draw and hit-test sides can never drift apart.
const (
	promptWidth  = 54
	promptHeight = 9

	promptBtnCancelX = 14
	promptBtnCancelW = 10 // "[ Cancel ]"
	promptBtnOKX     = 30
	promptBtnOKW     = 8 // "[  OK  ]"
)

// Prompt is the single-line text-input overlay: a centered box with a
// title, an optional hint line, one Field, and a Cancel / OK button row.
// Enter always submits (OK is the focused default); Esc or a click
// outside cancels. An empty submit is ignored so the user can still
// cancel deliberately.
//
// Rows (relY): 0 border · 1 title · 2 divider · 3 hint · 4 input ·
// 5 blank · 6 buttons · 7 blank · 8 border.
type Prompt struct {
	Title string
	Hint  string
	Field Field
	// Hover tracks the highlighted button: 0 = Cancel, 1 = OK. OK is
	// the default — it is what Enter does.
	Hover int
	Theme theme.Theme

	// Size reports the screen dimensions — injected by the opener so
	// the prompt owns its geometry without a screen handle.
	Size func() (w, h int)
	// Close dismisses this overlay. Injected by the opener; runs before
	// either callback fires (capture-then-close, so a callback that
	// opens the next overlay is never popped by this one's teardown).
	Close func()
	// OnSubmit receives the trimmed, non-empty value.
	OnSubmit func(string)
}

// rect computes the prompt's on-screen rectangle for the current
// screen size.
func (p *Prompt) rect() Rect {
	w, h := p.Size()
	return Centered(w, h, promptWidth, promptHeight)
}

// HandleKey routes one key event: Esc cancels, Enter submits, and
// everything else belongs to the input field. The prompt owns the whole
// keyboard while it is up, so unhandled keys are simply swallowed.
func (p *Prompt) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		p.cancel()
	case tcell.KeyEnter:
		p.submit()
	default:
		p.Field.HandleKey(ev)
	}
}

// HandleMouse processes mouse input: hovering a button highlights it,
// clicks on the buttons activate them, a click outside cancels, and a
// click in the input field repositions the cursor.
func (p *Prompt) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := p.rect()
	// Hover tracking — runs on every event, button held or not.
	if x >= r.X && x < r.X+r.W && y == r.Y+6 {
		relX := x - r.X
		switch {
		case relX >= promptBtnCancelX && relX < promptBtnCancelX+promptBtnCancelW:
			p.Hover = 0
		case relX >= promptBtnOKX && relX < promptBtnOKX+promptBtnOKW:
			p.Hover = 1
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		p.cancel()
		return
	}
	if y == r.Y+6 {
		relX := x - r.X
		switch {
		case relX >= promptBtnCancelX && relX < promptBtnCancelX+promptBtnCancelW:
			p.cancel()
			return
		case relX >= promptBtnOKX && relX < promptBtnOKX+promptBtnOKW:
			p.submit()
			return
		}
	}
	// Click in the input field — move the cursor to the clicked rune.
	if y == r.Y+4 {
		fieldStart := r.X + 3
		fieldEnd := r.X + r.W - 3
		if x >= fieldStart && x < fieldEnd {
			target := p.Field.Scroll() + (x - fieldStart)
			if target < 0 {
				target = 0
			}
			if target > len(p.Field.Value) {
				target = len(p.Field.Value)
			}
			p.Field.Cursor = target
		}
	}
}

// Draw renders the prompt: shared frame chrome, the greyed hint, the
// input field with a live caret, and the button row.
func (p *Prompt) Draw(scr tcell.Screen) {
	r := p.rect()
	th := p.Theme
	DrawFrame(scr, r, p.Title, th)

	bg := th.LineHL
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)
	if p.Hint != "" {
		drawText(scr, r.X+2, r.Y+3, p.Hint, mutedStyle)
	}

	inputStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	fieldStart := r.X + 3
	fieldWidth := (r.X + r.W - 3) - fieldStart
	p.Field.Draw(scr, fieldStart, r.Y+4, fieldWidth, inputStyle, true)

	DrawButton(scr, r.X+promptBtnCancelX, r.Y+6, "[ Cancel ]", bg, th.Text, p.Hover == 0)
	DrawButton(scr, r.X+promptBtnOKX, r.Y+6, "[  OK  ]", bg, th.Accent, p.Hover == 1)
}

// submit closes the prompt and hands the trimmed value to OnSubmit. An
// empty value is rejected silently — the prompt stays open.
func (p *Prompt) submit() {
	v := strings.TrimSpace(p.Field.Text())
	if v == "" {
		return
	}
	cb := p.OnSubmit
	p.Close()
	if cb != nil {
		cb(v)
	}
}

// cancel dismisses the prompt without running OnSubmit.
func (p *Prompt) cancel() {
	p.Close()
}
