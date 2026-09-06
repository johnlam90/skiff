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

	"github.com/johnlam90/skiff/internal/textdraw"
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
//
// Lines are authored at whatever length they come in; the body
// soft-wraps them to BodyTextWidth at draw time, so a 200-cell stderr
// path on a 40-column phone is read on four rows rather than lost
// behind an ellipsis. Scrolling, the indicator and the row count all
// work in wrapped rows.
type Info struct {
	Title string
	Lines []string
	Theme theme.Theme

	Size  func() (w, h int)
	Close func()

	// press is the click latch: OK and the outside-click dismissal
	// answer a fresh press, never a held drag — see Press. A press in
	// the body followed by a drag past the frame used to dismiss a
	// 300-line stderr report mid-read; the scroll indicator still
	// follows the raw mask.
	press Press

	// scroll is the first visible body ROW (post-wrap); scrolling
	// clamps it.
	scroll int

	// wrapped is Lines soft-wrapped to wrappedW cells, rebuilt when the
	// width or the Lines slice changes (wrappedOf remembers which
	// slice it was built from). A resize re-wraps; a scroll does not.
	wrapped   []infoRow
	wrappedW  int
	wrappedOf []string
}

// infoRow is one wrapped body row and the index of the Line it came
// from, which is what its style is picked from — a continuation row of
// a "+" line has no marker of its own but is still an addition.
type infoRow struct {
	text string
	src  int
}

// frameWidth is the frame's column count: infoWidth, or the whole
// screen when the terminal is narrower than that. Without the clamp the
// frame's right border and every line's tail fall off the edge of an
// 80-column tmux pane — and the surfaces that use Info (a failed
// command's stderr, the shortcut reference) are needed most exactly
// there.
func (n *Info) frameWidth() int {
	w, _ := n.Size()
	return fit(infoWidth, w)
}

// BodyTextWidth returns the usable text width for body rows at the
// current size: the frame minus a border cell and a padding cell on
// each side — the width Lines are wrapped to. Exported for the same
// reason Confirm's is: a caller that lays out its own columns (the
// reference sheet's key column) can size them to the frame it will
// actually get.
func (n *Info) BodyTextWidth() int { return n.frameWidth() - 4 }

// rect computes the info rectangle: the frame width and the visible
// body rows plus chrome, centered.
func (n *Info) rect() Rect {
	w, h := n.Size()
	return Centered(w, h, n.frameWidth(), n.bodyRows()+infoChromeRows)
}

// body returns Lines wrapped to the current BodyTextWidth, rebuilding
// the cache only when the width or the Lines slice has changed since
// it was built. Identity is the slice header (length and first
// element's address): a caller that swaps in new content gets a
// re-wrap, a redraw at the same size gets the cache.
func (n *Info) body() []infoRow {
	w := n.BodyTextWidth()
	same := n.wrappedW == w && len(n.wrappedOf) == len(n.Lines) &&
		(len(n.Lines) == 0 || &n.wrappedOf[0] == &n.Lines[0])
	if same && n.wrapped != nil {
		return n.wrapped
	}
	rows := make([]infoRow, 0, len(n.Lines))
	for i, line := range n.Lines {
		for _, part := range textdraw.WrapWords(line, w) {
			rows = append(rows, infoRow{text: part, src: i})
		}
	}
	n.wrapped, n.wrappedW, n.wrappedOf = rows, w, n.Lines
	return rows
}

// bodyRows returns the visible body height: the screen minus chrome,
// clamped to the wrapped row count and never below one row.
func (n *Info) bodyRows() int {
	_, scrH := n.Size()
	rows := scrH - infoChromeRows
	if rows < 1 {
		return 1
	}
	if total := len(n.body()); total < rows {
		if total < 1 {
			return 1
		}
		return total
	}
	return rows
}

// Scroll exposes the first visible row index for tests.
func (n *Info) Scroll() int { return n.scroll }

// RowCount exposes the wrapped body's row count for tests, which is
// what the scroll clamp and the indicator's total are measured in.
func (n *Info) RowCount() int { return len(n.body()) }

// bar describes the body's scroll indicator inside frame r: the frame's
// right-hand padding column, spanning exactly the rows Draw fills with
// wrapped text. Body text is wrapped to r.W-4 cells and so never reaches
// that column, which is what keeps DiffLineStyle off the bar — a diff
// preview colors its own lines, not the scrollbar beside them.
func (n *Info) bar(r Rect) Bar {
	return Bar{
		x:      BarColumn(r),
		top:    r.Y + 3,
		viewH:  n.bodyRows(),
		total:  len(n.body()),
		scroll: n.scroll,
	}
}

// ScrollBy moves the body window by delta rows, clamped to the content.
func (n *Info) ScrollBy(delta int) {
	maxScroll := len(n.body()) - n.bodyRows()
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
// tcell actually emits for wheels and trackpads); a press on the scroll
// indicator jumps the thumb there; a FRESH click on OK or outside the
// modal dismisses — the motion of a held button never does.
func (n *Info) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := n.rect()
	fresh := n.press.Fresh(btn)
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
	// The indicator's column is claimed before the dismissal paths: a
	// 300-line stderr dump is exactly where a user reaches for the bar,
	// and losing the report to a stray dismiss would be worse than not
	// having one.
	if b := n.bar(r); b.Hit(x, y) {
		n.scroll = b.Target(y)
		return
	}
	if !fresh {
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

// Draw renders the info overlay: frame, the visible slice of the
// wrapped body left-aligned (stderr usually starts with file paths that
// read poorly centered), diff-aware line colors, and the centered OK
// button.
func (n *Info) Draw(scr tcell.Screen) {
	r := n.rect()
	th := n.Theme
	DrawFrameHint(scr, r, n.Title, "⏎ ok · "+FrameHintEsc, th)

	bg := th.LineHL
	n.ScrollBy(0) // re-clamp after any resize
	body := n.body()
	rows := n.bodyRows()
	end := n.scroll + rows
	if end > len(body) {
		end = len(body)
	}
	for i, row := range body[n.scroll:end] {
		// Style is picked from the SOURCE line: a diff marker lives in
		// column 0 of the line as authored, and a wrapped continuation
		// row of an addition is still an addition.
		st := DiffLineStyle(th, bg, n.Lines[row.src])
		drawText(scr, r.X+2, r.Y+3+i, r.W-4, row.text, st)
	}
	n.bar(r).Draw(scr, th)
	DrawButton(scr, r.X+(r.W-10)/2, r.Y+r.H-3, "[  OK  ]", bg, th.Accent, true)
	scr.HideCursor()
}

// DiffLineStyle colors one line of a git diff preview: additions,
// deletions, hunk headers, and file headers each get their own color so
// the preview reads like a real diff. Additions and deletions also
// carry the palette's derived row tint as background — the same wash
// the side-by-side view paints — except on low-color palettes, where
// DiffTints opts out and the passed surface stays. Shared with the
// diff view's unified fallback.
func DiffLineStyle(th theme.Theme, bg tcell.Color, line string) tcell.Style {
	style := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
		return style.Foreground(th.Muted)
	}
	tints, tinted := th.DiffTints()
	if strings.HasPrefix(line, "+") {
		if tinted {
			style = style.Background(tints.AddRow)
		}
		return style.Foreground(th.GitAdded)
	}
	if strings.HasPrefix(line, "-") {
		if tinted {
			style = style.Background(tints.DelRow)
		}
		return style.Foreground(th.GitDeleted)
	}
	if strings.HasPrefix(line, "@@") {
		return style.Foreground(th.AccentSoft).Bold(true)
	}
	return style
}

// WantsMotion is false. Info is the long-lived surface — a 300-line
// stderr dump or diff preview the user reads and scrolls — and it
// ignores everything but the wheel and Button1, so all-motion tracking
// would buy a continuous uplink flood and nothing else.
func (n *Info) WantsMotion() bool { return false }

// Dismiss is a no-op: Info reports, it does not decide anything.
func (n *Info) Dismiss() {}
