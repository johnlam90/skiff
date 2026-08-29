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

	// scroll is the first row rendered. Non-zero only when the terminal
	// is too short to show every row at once — a three-prompt custom
	// action wants 13 rows and a phone in landscape has ten.
	scroll int
}

// frameWidth returns the form's frame width: 60 cells, or the whole
// terminal when it is narrower, so the Submit button stays on screen.
func (f *Form) frameWidth() int {
	if f.Size == nil {
		return formWidth
	}
	w, _ := f.Size()
	return fit(formWidth, w)
}

// rect computes the form's centered rectangle; height tracks the rows
// and Centered clamps it to the terminal, so a form with more rows than
// the screen can hold windows them instead of painting its buttons into
// cells that do not exist.
func (f *Form) rect() Rect {
	w, h := f.Size()
	rows := len(f.Rows)
	if rows < 1 {
		rows = 1
	}
	return Centered(w, h, f.frameWidth(), formChromeRows+formRowHeight*rows)
}

// visibleRows is how many (label, input) pairs the frame can show. At
// least one: a form the user cannot see a single field of is worse than
// a cramped one.
func (f *Form) visibleRows() int {
	n := (f.rect().H - formChromeRows) / formRowHeight
	if n < 1 {
		n = 1
	}
	if n > len(f.Rows) {
		n = len(f.Rows)
	}
	return n
}

// maxScroll is the largest first-visible-row index — zero whenever the
// whole form fits, which is every ordinary terminal.
func (f *Form) maxScroll() int {
	if n := len(f.Rows) - f.visibleRows(); n > 0 {
		return n
	}
	return 0
}

// ensureFocusVisible scrolls the window so the focused row is on screen.
// Called from every focus change, which is the only thing that moves the
// window: the form has no wheel or scroll keys of its own because Tab
// already walks it end to end.
func (f *Form) ensureFocusVisible() {
	if f.Focus < f.scroll {
		f.scroll = f.Focus
	}
	if n := f.visibleRows(); f.Focus >= f.scroll+n {
		f.scroll = f.Focus - n + 1
	}
	if f.scroll > f.maxScroll() {
		f.scroll = f.maxScroll()
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
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

	// Only the rows the window is showing are clickable — a row scrolled
	// out of the frame occupies no cells, so hit-testing it would fire on
	// whatever is painted where it used to be.
	for vi, n := 0, f.visibleRows(); vi < n; vi++ {
		i := f.scroll + vi
		if i >= len(f.Rows) {
			break
		}
		row := &f.Rows[i]
		rowY := r.Y + 3 + vi*formRowHeight
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

// Draw renders the form: frame, one label/input pair per visible row —
// the focused row's label in the accent, its input on a lifted
// background, selects with < > chevron targets — and the button row.
// When the terminal is too short for every row, ▲/▼ markers go into the
// divider and the bottom border (the menu's vocabulary) so the hidden
// fields announce themselves without costing a content row.
func (f *Form) Draw(scr tcell.Screen) {
	r := f.rect()
	th := f.Theme
	DrawFrame(scr, r, f.Title, th)

	bg := th.LineHL
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)

	scr.HideCursor()
	for vi, n := 0, f.visibleRows(); vi < n; vi++ {
		i := f.scroll + vi
		if i >= len(f.Rows) {
			break
		}
		row := &f.Rows[i]
		rowY := r.Y + 3 + vi*formRowHeight
		inputRow := rowY + 1

		labelStyle := mutedStyle
		if i == f.Focus {
			labelStyle = titleStyle
		}
		drawText(scr, r.X+2, rowY, r.W-4, trimRunes(row.Label, r.W-4), labelStyle)

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
			drawText(scr, fieldStart, inputRow, fieldWidth, "<", inputStyle)
			drawText(scr, fieldEnd-1, inputRow, 1, ">", inputStyle)
			opt := ""
			if row.Sel >= 0 && row.Sel < len(row.Options) {
				opt = row.Options[row.Sel]
			}
			optX := fieldStart + (fieldWidth-runeLen(opt))/2
			drawText(scr, optX, inputRow, fieldEnd-optX, opt, inputStyle)
			continue
		}
		row.Field.Draw(scr, fieldStart, inputRow, fieldWidth, inputStyle, i == f.Focus)
	}

	moreStyle := tcell.StyleDefault.Background(bg).Foreground(th.Accent)
	if f.scroll > 0 {
		drawText(scr, r.X+2, r.Y+2, r.W-3, " ▲ ", moreStyle)
	}
	if f.scroll < f.maxScroll() {
		drawText(scr, r.X+2, r.Y+r.H-1, r.W-3, " ▼ ", moreStyle)
	}

	DrawButton(scr, r.X+4, r.Y+r.H-3, "[ Cancel ]", bg, th.Text, false)
	DrawButton(scr, r.X+r.W-formBtnW-4, r.Y+r.H-3, "[ Submit ]", bg, th.Accent, true)
}

// moveFocus shifts the focused row by delta with wrap-around, keeping
// the new row inside the visible window.
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
	f.ensureFocusVisible()
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
