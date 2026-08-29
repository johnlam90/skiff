// =============================================================================
// File: internal/overlay/popup.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// PopupItem is one row of a Popup: a label and the action a pick runs.
// The action is a plain closure — a popup needs no knowledge of what
// its items operate on, so the git-extras menu no longer has to
// fabricate a tree node just to borrow the right-click menu.
type PopupItem struct {
	Label  string
	OnPick func()
	// Divider marks a non-interactive rule between groups: skipped by
	// keyboard navigation, ignored by hover, never activatable.
	Divider bool
}

// Popup is the small anchored action menu (the tree right-click menu,
// the git extras menu): a bordered list of rows, one action each.
// Unlike the centered prefabs its rectangle is fixed at open — anchored
// to the click point via PlacePopup — so it needs no Size hook.
type Popup struct {
	Items []PopupItem
	// Hover is the highlighted row index.
	Hover int
	// At is the popup's rectangle, computed once at open by PlacePopup.
	At    Rect
	Theme theme.Theme
	Close func()

	// scroll is the first row rendered. Non-zero only when PlacePopup had
	// to clamp the frame — the git-extras menu is thirteen rows plus two
	// of border, and a phone in landscape has ten.
	scroll int
}

// visibleRows is how many item rows the frame holds: its height less the
// two border rows.
func (p *Popup) visibleRows() int {
	if n := p.At.H - 2; n > 0 {
		return n
	}
	return 1
}

// maxScroll is the largest first-visible-row index — zero whenever every
// item fits, which is every popup on an ordinary terminal.
func (p *Popup) maxScroll() int {
	if n := len(p.Items) - p.visibleRows(); n > 0 {
		return n
	}
	return 0
}

// ensureHoverVisible scrolls the window so the highlighted row is on
// screen. Keyboard navigation is the only thing that moves the window: a
// popup has no wheel handler, and an item the arrows can reach but the
// frame never shows is an action the user cannot run.
func (p *Popup) ensureHoverVisible() {
	if p.Hover < p.scroll {
		p.scroll = p.Hover
	}
	if n := p.visibleRows(); p.Hover >= p.scroll+n {
		p.scroll = p.Hover - n + 1
	}
	if p.scroll > p.maxScroll() {
		p.scroll = p.maxScroll()
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

// PopupWidth returns the width a popup needs to show every label in
// full — the chevron indent on the left, a padding cell and the border
// on the right — never narrower than min. Sizing to content is what
// keeps a long label ("Compare against…") from painting past the
// border into whatever sits behind the popup.
func PopupWidth(items []PopupItem, min int) int {
	w := min
	for _, it := range items {
		if n := 4 + runeLen(it.Label) + 2; n > w {
			w = n
		}
	}
	return w
}

// PlacePopup anchors a w-wide, count-row popup at click point (x, y) on
// a scrW×scrH screen. It flips left or up when the popup would fall off
// the right or bottom edge, clamps the frame to the screen, and clamps
// the origin. The height clamp is what a thirteen-row git-extras menu
// needs on a ten-row terminal: the frame keeps its borders and windows
// the rows instead of painting four of them into cells that do not
// exist.
func PlacePopup(scrW, scrH, x, y, w, count int) Rect {
	w = fit(w, scrW)
	h := fit(count+2, scrH)
	cx, cy := x, y
	if cx+w > scrW {
		cx = x - w + 1
	}
	if cy+h > scrH {
		cy = y - h + 1
	}
	if cx < 0 {
		cx = 0
	}
	if cy < 0 {
		cy = 0
	}
	return Rect{X: cx, Y: cy, W: w, H: h}
}

// HandleKey: Up/Down move the highlight (clamped, not wrapped — the
// popup sits at an anchor, so wrapping past the end feels like a jump),
// skipping divider rows; Enter activates, Esc dismisses.
func (p *Popup) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		p.Close()
	case tcell.KeyDown:
		p.Hover = p.nextSelectable(p.Hover, +1)
		p.ensureHoverVisible()
	case tcell.KeyUp:
		p.Hover = p.nextSelectable(p.Hover, -1)
		p.ensureHoverVisible()
	case tcell.KeyEnter:
		p.activate()
	}
}

// nextSelectable walks from the current row in dir, skipping dividers;
// hitting either end leaves the highlight where it was.
func (p *Popup) nextSelectable(from, dir int) int {
	i := from
	for {
		i += dir
		if i < 0 || i >= len(p.Items) {
			return from
		}
		if !p.Items[i].Divider {
			return i
		}
	}
}

// HandleMouse: hovering a row highlights it; clicking activates; any
// click outside dismisses.
func (p *Popup) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := p.At
	row := -1
	// Screen row → item index through the scroll offset: a row the frame
	// windowed out occupies no cells, so hit-testing it would fire on
	// whatever is painted where it used to be.
	if x >= r.X && x < r.X+r.W && y > r.Y && y < r.Y+r.H-1 {
		row = y - r.Y - 1 + p.scroll
	}
	if row >= 0 && row < len(p.Items) && !p.Items[row].Divider {
		p.Hover = row
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		p.Close()
		return
	}
	if row >= 0 && row < len(p.Items) && !p.Items[row].Divider {
		p.Hover = row
		p.activate()
	}
}

// Draw renders the popup: bordered list, chevron on every row, hovered
// row on the selection color.
func (p *Popup) Draw(scr tcell.Screen) {
	r := p.At
	th := p.Theme
	bg := th.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(th.Subtle)
	hoverBg := th.Selection
	hoverStyle := tcell.StyleDefault.Background(hoverBg).Foreground(th.Text).Bold(true)
	hoverChevStyle := tcell.StyleDefault.Background(hoverBg).Foreground(th.AccentSoft).Bold(true)
	chevStyle := tcell.StyleDefault.Background(bg).Foreground(th.AccentSoft)

	fillRect(scr, r.X, r.Y, r.W, r.H, bgStyle)
	drawBorder(scr, r.X, r.Y, r.W, r.H, borderStyle)

	for vi, n := 0, p.visibleRows(); vi < n; vi++ {
		i := p.scroll + vi
		if i >= len(p.Items) {
			break
		}
		item := p.Items[i]
		cy := r.Y + 1 + vi
		if item.Divider {
			drawHDivider(scr, r.X, cy, r.W, borderStyle)
			continue
		}
		// Labels clip at the border: a clamped-width popup on a tiny
		// screen must never leak text over what sits behind it.
		labelW := r.W - 4 - 1
		if i == p.Hover {
			for cx := r.X + 1; cx < r.X+r.W-1; cx++ {
				scr.SetContent(cx, cy, ' ', nil, hoverStyle)
			}
			drawText(scr, r.X+2, cy, r.W-3, "▸", hoverChevStyle)
			drawClippedText(scr, r.X+4, cy, labelW, item.Label, hoverStyle)
		} else {
			drawText(scr, r.X+2, cy, r.W-3, "▸", chevStyle)
			drawClippedText(scr, r.X+4, cy, labelW, item.Label, bgStyle)
		}
	}

	// ▲/▼ in the border rows, the same vocabulary the action menu and the
	// form use: rows exist off-frame and the arrow keys will reach them.
	// Painted into the chrome, so they cost the list nothing.
	moreStyle := tcell.StyleDefault.Background(bg).Foreground(th.Accent)
	if p.scroll > 0 {
		drawText(scr, r.X+2, r.Y, r.W-3, " ▲ ", moreStyle)
	}
	if p.scroll < p.maxScroll() {
		drawText(scr, r.X+2, r.Y+r.H-1, r.W-3, " ▼ ", moreStyle)
	}
	scr.HideCursor()
}

// activate runs the highlighted item — capture-then-close, like every
// other overlay.
func (p *Popup) activate() {
	if p.Hover < 0 || p.Hover >= len(p.Items) || p.Items[p.Hover].Divider {
		return
	}
	pick := p.Items[p.Hover].OnPick
	p.Close()
	if pick != nil {
		pick()
	}
}

// WantsMotion is true: the popup's rows highlight on hover, which is
// the whole affordance telling the user they are clickable.
func (p *Popup) WantsMotion() bool { return true }

// Dismiss is a no-op — a popup that is replaced simply picks nothing.
func (p *Popup) Dismiss() {}
