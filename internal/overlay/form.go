// =============================================================================
// File: internal/overlay/form.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// Form geometry. The width matches the prompt so the editor's secondary
// surfaces keep one visual rhythm; height grows with the row count.
const (
	formWidth = 60
	// formRowHeight is two lines per row: label above input.
	formRowHeight = 2
	// formChromeRows is the cells spent on borders, title, divider, and
	// the button row — added on top of the input rows.
	formChromeRows = 7

	formBtnW = 10 // "[ Cancel ]" and "[ Submit ]"
)

// FormRow is one (label, input) pair. Options == nil makes it a text
// row backed by a Field; a non-nil Options slice makes it a select row
// cycling through the options with Sel as the current index. Key names
// the value in the submitted map.
type FormRow struct {
	Key     string
	Label   string
	Field   Field
	Options []string
	Sel     int
}

// Form is the multi-field overlay custom actions use to collect input
// before running: a vertical stack of rows, Tab/Shift+Tab focus
// cycling, and a Cancel / Submit button row. Enter on the last row
// submits; on any other row it advances focus, so a user racing
// through the form can hold Tab/Enter to fill it out.
type Form struct {
	Title string
	Rows  []FormRow
	// Focus is the focused row index.
	Focus int
	Theme theme.Theme

	Size  func() (w, h int)
	Close func()
	// OnSubmit receives the per-key value map. Empty fields pass
	// through — the action author chooses whether empty means "skip".
	OnSubmit func(map[string]string)
}

// rect computes the form's centered rectangle; height tracks the rows.
func (f *Form) rect() Rect {
	w, h := f.Size()
	rows := len(f.Rows)
	if rows < 1 {
		rows = 1
	}
	return Centered(w, h, formWidth, formChromeRows+formRowHeight*rows)
}

// Values assembles the submitted map from the rows' current state.
func (f *Form) Values() map[string]string {
	out := make(map[string]string, len(f.Rows))
	for i := range f.Rows {
		r := &f.Rows[i]
		if r.Options != nil {
			if r.Sel >= 0 && r.Sel < len(r.Options) {
				out[r.Key] = r.Options[r.Sel]
			}
			continue
		}
		out[r.Key] = r.Field.Text()
	}
	return out
}

// HandleKey routes keystrokes: Esc cancels, Tab/Shift+Tab cycle focus
// (wrapping, like every keyboard list in the editor), Enter advances or
// submits on the last row, and everything else goes to the focused row.
func (f *Form) HandleKey(ev *tcell.EventKey) {
	if len(f.Rows) == 0 {
		return
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		f.Close()
		return
	case tcell.KeyTab:
		f.moveFocus(+1)
		return
	case tcell.KeyBacktab:
		f.moveFocus(-1)
		return
	case tcell.KeyEnter:
		if f.Focus == len(f.Rows)-1 {
			f.submit()
			return
		}
		f.moveFocus(+1)
		return
	}
	row := &f.Rows[f.Focus]
	if row.Options != nil {
		switch ev.Key() {
		case tcell.KeyLeft, tcell.KeyUp:
			row.Sel--
			if row.Sel < 0 {
				row.Sel = len(row.Options) - 1
			}
		case tcell.KeyRight, tcell.KeyDown:
			row.Sel = (row.Sel + 1) % len(row.Options)
		}
		return
	}
	row.Field.HandleKey(ev)
}

// HandleMouse: clicks on a row focus it, select chevrons cycle, text
// clicks reposition the caret, the buttons resolve the form, and a
// click outside cancels.
func (f *Form) HandleMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.Button1 == 0 {
		return
	}
	r := f.rect()
	if !r.Contains(x, y) {
		f.Close()
		return
	}

	cancelX := r.X + 4
	submitX := r.X + r.W - formBtnW - 4
	btnY := r.Y + r.H - 3
	if y == btnY {
		switch {
		case x >= cancelX && x < cancelX+formBtnW:
			f.Close()
			return
		case x >= submitX && x < submitX+formBtnW:
			f.submit()
			return
		}
	}

	for i := range f.Rows {
		row := &f.Rows[i]
		rowY := r.Y + 3 + i*formRowHeight
		inputRow := rowY + 1
		if y != rowY && y != inputRow {
			continue
		}
		f.Focus = i
		fieldStart := r.X + 3
		fieldEnd := r.X + r.W - 3
		if y == inputRow && row.Options != nil {
			switch x {
			case fieldStart:
				row.Sel--
				if row.Sel < 0 {
					row.Sel = len(row.Options) - 1
				}
			case fieldEnd - 1:
				row.Sel = (row.Sel + 1) % len(row.Options)
			}
			return
		}
		if y == inputRow && row.Options == nil {
			if x >= fieldStart && x < fieldEnd {
				target := row.Field.Scroll() + (x - fieldStart)
				if target < 0 {
					target = 0
				}
				if target > len(row.Field.Value) {
					target = len(row.Field.Value)
				}
				row.Field.Cursor = target
			}
		}
		return
	}
}

// Draw renders the form: frame, one label/input pair per row — the
// focused row's label in the accent, its input on a lifted background,
// selects with < > chevron targets — and the button row.
func (f *Form) Draw(scr tcell.Screen) {
	r := f.rect()
	th := f.Theme
	DrawFrame(scr, r, f.Title, th)

	bg := th.LineHL
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)

	scr.HideCursor()
	for i := range f.Rows {
		row := &f.Rows[i]
		rowY := r.Y + 3 + i*formRowHeight
		inputRow := rowY + 1

		labelStyle := mutedStyle
		if i == f.Focus {
			labelStyle = titleStyle
		}
		drawText(scr, r.X+2, rowY, row.Label, labelStyle)

		fieldStart := r.X + 3
		fieldEnd := r.X + r.W - 3
		fieldWidth := fieldEnd - fieldStart

		inputBg := th.BG
		if i == f.Focus {
			inputBg = th.Subtle
		}
		inputStyle := tcell.StyleDefault.Background(inputBg).Foreground(th.Text)

		if row.Options != nil {
			for cx := fieldStart - 1; cx <= fieldEnd; cx++ {
				scr.SetContent(cx, inputRow, ' ', nil, inputStyle)
			}
			drawText(scr, fieldStart, inputRow, "<", inputStyle)
			drawText(scr, fieldEnd-1, inputRow, ">", inputStyle)
			opt := ""
			if row.Sel >= 0 && row.Sel < len(row.Options) {
				opt = row.Options[row.Sel]
			}
			drawText(scr, fieldStart+(fieldWidth-runeLen(opt))/2, inputRow, opt, inputStyle)
			continue
		}
		row.Field.Draw(scr, fieldStart, inputRow, fieldWidth, inputStyle, i == f.Focus)
	}

	DrawButton(scr, r.X+4, r.Y+r.H-3, "[ Cancel ]", bg, th.Text, false)
	DrawButton(scr, r.X+r.W-formBtnW-4, r.Y+r.H-3, "[ Submit ]", bg, th.Accent, true)
}

// moveFocus shifts the focused row by delta with wrap-around.
func (f *Form) moveFocus(delta int) {
	n := len(f.Rows)
	if n == 0 {
		return
	}
	i := (f.Focus + delta) % n
	if i < 0 {
		i += n
	}
	f.Focus = i
}

// submit closes the form and hands the collected values to OnSubmit —
// capture-then-close, like every overlay.
func (f *Form) submit() {
	values := f.Values()
	cb := f.OnSubmit
	f.Close()
	if cb != nil {
		cb(values)
	}
}
