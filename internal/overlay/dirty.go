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
// The per-button columns are hand-tuned for the default captions and
// are used verbatim whenever Labels is empty, so the stock
// unsaved-changes modal renders exactly as it always has.
const (
	dirtyWidth  = 60
	dirtyHeight = 9

	dirtyBtnCancelX  = 5
	dirtyBtnCancelW  = 10 // "[ Cancel ]"
	dirtyBtnDiscardX = 22
	dirtyBtnDiscardW = 11 // "[ Discard ]"
	dirtyBtnSaveX    = 42
	dirtyBtnSaveW    = 8 // "[ Save ]"

	// dirtyBtnMargin is the smallest gap left at each end of the button
	// row when custom Labels force a computed layout.
	dirtyBtnMargin = 3
)

// dirtyDefaultLabels are the captions the unsaved-changes flow uses.
// A caller that leaves Labels zero gets these plus the pinned columns.
var dirtyDefaultLabels = [3]string{"[ Cancel ]", "[ Discard ]", "[ Save ]"}

// Dirty is the unsaved-changes overlay: Cancel / Discard / Save on one
// row. Default focus is Cancel so a stray Enter is non-destructive —
// the same safety pattern the delete confirm uses.
//
// Rows (relY): 0 border · 1 title · 2 divider · 3 blank · 4 message ·
// 5 buttons · 6–7 blank · 8 border.
type Dirty struct {
	Title   string
	Message string
	// Labels optionally replaces the three button captions, left to
	// right, for flows that reuse this three-way shape with a
	// different vocabulary — the disk-conflict prompt's Keep mine /
	// Reload / Diff, say. Leave it zero for Cancel / Discard / Save.
	// Callers pass captions complete with their brackets, exactly as
	// the defaults do, because the bracket style is part of the label.
	Labels [3]string
	// Hover is the focused button: 0 = Cancel, 1 = Discard, 2 = Save.
	Hover int
	Theme theme.Theme

	Size  func() (w, h int)
	Close func()
	// OnSave runs after Close when the user picks Save (typically:
	// save the tab(s), then proceed); OnDiscard when they pick Discard
	// (skip saving, proceed anyway). Cancel just dismisses — pass
	// OnCancel when the dismissal itself has to be recorded.
	OnSave    func()
	OnDiscard func()
	OnCancel  func()
}

// labels returns the captions to draw, falling back to the stock trio.
// Individual empty entries fall back too, so a caller can rename one
// button without restating the other two.
func (d *Dirty) labels() [3]string {
	out := d.Labels
	for i := range out {
		if out[i] == "" {
			out[i] = dirtyDefaultLabels[i]
		}
	}
	return out
}

// frameWidth returns the modal's frame width: the natural 60 cells, or
// the whole terminal when it is narrower. A frame wider than the screen
// would put the Save button — the productive default — in columns that
// do not exist, which is the worst possible thing for this particular
// modal to lose.
//
// Size is nil for a bare Dirty measured outside a live screen
// (dirtyButtonAtRelX does exactly that to expose the stock hit test);
// with no screen to clamp against, the natural width is the answer.
func (d *Dirty) frameWidth() int {
	if d.Size == nil {
		return dirtyWidth
	}
	w, _ := d.Size()
	return fit(dirtyWidth, w)
}

// columns returns each button's x offset within the modal and its
// width. The default captions keep their pinned, hand-tuned columns as
// long as the frame is its natural width; anything else — a relabelled
// trio, or a terminal too narrow for 60 cells — is laid out by
// centering the trio with even gaps inside the frame we actually got,
// which is the only rule that stays correct for captions and widths we
// have not seen.
func (d *Dirty) columns() (xs, ws [3]int) {
	labels := d.labels()
	for i, l := range labels {
		ws[i] = runeLen(l)
	}
	fw := d.frameWidth()
	if labels == dirtyDefaultLabels && fw >= dirtyWidth {
		return [3]int{dirtyBtnCancelX, dirtyBtnDiscardX, dirtyBtnSaveX},
			[3]int{dirtyBtnCancelW, dirtyBtnDiscardW, dirtyBtnSaveW}
	}
	total := ws[0] + ws[1] + ws[2]
	gap := (fw - 2*dirtyBtnMargin - total) / 2
	if gap < 1 {
		gap = 1
	}
	x := (fw - (total + 2*gap)) / 2
	// One cell in, never on the border: a button painted over the frame's
	// left edge reads as a rendering fault rather than a control.
	if x < 1 {
		x = 1
	}
	for i := range xs {
		xs[i] = x
		x += ws[i] + gap
	}
	return xs, ws
}

// rect computes the dirty modal's centered rectangle.
func (d *Dirty) rect() Rect {
	w, h := d.Size()
	return Centered(w, h, d.frameWidth(), dirtyHeight)
}

// HandleKey: Left/Right and Tab cycle focus across the three buttons,
// Enter activates the focused one, Esc cancels.
func (d *Dirty) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		d.cancel()
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
		if idx := d.buttonAtRelX(x - r.X); idx >= 0 {
			d.Hover = idx
		}
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		d.cancel()
		return
	}
	if y == r.Y+5 {
		if idx := d.buttonAtRelX(x - r.X); idx >= 0 {
			d.activate(idx)
		}
	}
}

// Draw renders the modal: frame, centered rune-safe message, and the
// button trio. The colors encode intent rather than position — the
// left button is neutral, the middle one destructive (red), the right
// one the productive default (accent) — so a relabelled trio still
// reads correctly.
func (d *Dirty) Draw(scr tcell.Screen) {
	r := d.rect()
	th := d.Theme
	DrawFrame(scr, r, d.Title, th)

	bg := th.LineHL
	bodyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	msg := trimRunes(d.Message, r.W-4)
	msgX := r.X + (r.W-runeLen(msg))/2
	drawText(scr, msgX, r.Y+4, r.X+r.W-1-msgX, msg, bodyStyle)

	labels := d.labels()
	xs, _ := d.columns()
	fgs := [3]tcell.Color{th.Text, th.Error, th.Accent}
	for i, l := range labels {
		DrawButton(scr, r.X+xs[i], r.Y+5, l, bg, fgs[i], d.Hover == i)
	}
	scr.HideCursor()
}

// cancel dismisses without choosing either action, firing OnCancel for
// flows where "the user backed out" is itself a decision worth
// recording. Capture-then-close, like activate.
func (d *Dirty) cancel() {
	cb := d.OnCancel
	d.Close()
	if cb != nil {
		cb()
	}
}

// activate runs one button: 0 Cancel, 1 Discard, 2 Save — always
// capture-then-close.
func (d *Dirty) activate(idx int) {
	switch idx {
	case 0:
		d.cancel()
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

// buttonAtRelX maps an x offset within the modal to a button index
// (0/1/2) or -1 when the offset misses every button — one geometry
// source for hover, click, and draw, whatever the captions are.
func (d *Dirty) buttonAtRelX(rx int) int {
	xs, ws := d.columns()
	for i := range xs {
		if rx >= xs[i] && rx < xs[i]+ws[i] {
			return i
		}
	}
	return -1
}

// dirtyButtonAtRelX is the hit test for the stock Cancel / Discard /
// Save captions and their pinned columns.
func dirtyButtonAtRelX(rx int) int {
	return (&Dirty{}).buttonAtRelX(rx)
}

// WantsMotion is true: Dirty highlights the Cancel / Discard / Save
// button under the pointer.
func (d *Dirty) WantsMotion() bool { return true }

// Dismiss is a no-op — a torn-down dirty prompt neither saves nor
// discards, which is the same as cancelling.
func (d *Dirty) Dismiss() {}
