// =============================================================================
// File: internal/app/draw.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// draw.go is the render pass. draw paints the four panels in a fixed
// order — sidebar, tab bar, editor body, status bar — and lets the overlay
// stack paint last so modals sit on top of everything.
//
// The tab strip's geometry lives here as well: layoutTabs computes virtual
// rects, drawTabBar shifts them by tabScroll and stores the screen-space
// result, which is what mouse.go hit-tests against. Keeping the layout and
// the paint in one file is what keeps those two views in sync.

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
)

// tabRect remembers where each tab was drawn so click handling can hit-test
// against the actual rendered geometry rather than re-deriving it.
type tabRect struct {
	Index    int
	X, Width int
	CloseX   int // Cell column of the × close button.
}

// draw paints the entire screen. Called once per event in the main loop.
// The action modal — if open — is drawn last so it sits on top of everything.
func (a *App) draw() {
	a.screen.Clear()

	if a.width < minWidth || a.height < minHeight {
		a.drawTooSmall()
		return
	}

	if a.sidebarShown {
		sx, sy, sw, sh := a.sidebarRect()
		if a.gitPanelActive {
			a.drawGitPanel(sx, sy, sw, sh)
		} else {
			// Both scrollbar "I'm being dragged" flags are derived from
			// dragMode here rather than latched by the press/release
			// path, so a drag that ends through some other route (an
			// overlay opening, closeAllModals) can't strand a thumb in
			// its bright Accent state. Same rule drawSplitter follows.
			a.tree.ScrollbarActive = a.dragMode == "treescrollbar"
			a.tree.Render(a.screen, a.theme, sx, sy, sw, sh)
			// Overdraw the tree's plain header with the EXPLORER / GIT
			// tab row — the tree keeps rendering its own header so its
			// geometry (and tests) stay untouched.
			a.drawSidebarHeader(sx, sy, sw)
		}
		a.drawSplitter()
	}

	a.drawTabBar()

	if tab := a.activeTabPtr(); tab != nil {
		ex, ey, ew, eh := a.editorRect()
		tab.ScrollbarActive = a.dragMode == "scrollbar"
		tab.Render(a.screen, a.theme, ex, ey, ew, eh)
	} else {
		a.drawEmptyEditor()
	}

	if a.findOpen {
		a.drawFindBar()
	}
	a.drawStatusBar()
	if a.projFindOpen {
		a.drawProjFind()
	}
	a.drawLeaderStrip()

	// The overlay stack paints last so the open overlay sits above
	// everything — at most one is ever up (Open replaces), so there is
	// no layering order left to maintain.
	a.overlays.Draw(a.screen)
}

// iconsOn reports whether Nerd Font glyphs should render in places
// outside the file tree (e.g. the tab bar). The single source of
// truth is the file tree — App.loadUserConfig stamped the resolved
// auto/on/off decision onto t.IconsEnabled there, so consulting the
// tree keeps tabs and tree perfectly in sync (turning icons off via
// config.json hides them everywhere at once).
func (a *App) iconsOn() bool {
	return a.tree != nil && a.tree.IconsEnabled
}

// tabStripRegion returns the screen x and width of the area tabs may
// occupy: everything in the tab bar right of the ≡ button.
func (a *App) tabStripRegion() (x, w int) {
	tx, _, tw, _ := a.tabBarRect()
	x = a.sidebarW() + menuButtonWidth
	w = tx + tw - x
	if w < 0 {
		w = 0
	}
	return
}

// maxTabScroll returns how far the tab strip can scroll: the overflow
// between the laid-out tab widths and the strip. Zero when every tab
// fits.
func (a *App) maxTabScroll() int {
	rects := a.layoutTabs()
	if len(rects) == 0 {
		return 0
	}
	stripX, stripW := a.tabStripRegion()
	last := rects[len(rects)-1]
	over := (last.X + last.Width) - (stripX + stripW)
	if over < 0 {
		return 0
	}
	return over
}

// clampTabScroll bounds tabScroll to [0, maxTabScroll] so the strip can
// never scroll past its first or last tab.
func (a *App) clampTabScroll() {
	if maxS := a.maxTabScroll(); a.tabScroll > maxS {
		a.tabScroll = maxS
	}
	if a.tabScroll < 0 {
		a.tabScroll = 0
	}
}

// ensureActiveTabVisible scrolls the tab strip so the active tab's full
// rect sits inside the visible window — the tab-bar analogue of the
// editor's EnsureVisible. Called from activation sites (open, click,
// close, resize), never from the draw path, so manual strip scrolling
// isn't fought by the renderer.
func (a *App) ensureActiveTabVisible() {
	rects := a.layoutTabs()
	if a.tabs.ActiveIndex() < 0 || a.tabs.ActiveIndex() >= len(rects) {
		a.tabScroll = 0
		return
	}
	stripX, stripW := a.tabStripRegion()
	if stripW <= 0 {
		return
	}
	r := rects[a.tabs.ActiveIndex()]
	left := stripX + a.tabScroll
	if r.X < left {
		a.tabScroll = r.X - stripX
	} else if r.X+r.Width > left+stripW {
		a.tabScroll = r.X + r.Width - stripX - stripW
	}
	a.clampTabScroll()
}

// scrollTabStrip moves the strip by delta cells (negative = toward the
// first tab), clamped. Fired by the ‹ › chevron clicks and by wheel
// events over the tab bar.
func (a *App) scrollTabStrip(delta int) {
	a.tabScroll += delta
	a.clampTabScroll()
}

// tabScrollStep is how many cells one chevron click or wheel tick moves
// the tab strip — enough to reveal most of a typical tab without
// disorienting jumps.
const tabScrollStep = 8

// layoutTabs computes the tabRect geometry for every tab. Tabs are rendered
// to the right of the menu button, in the format:
//
//	" <dirty><icon? ><name> × " — a single space pad, two-cell dirty slot
//	(dot+space, or two spaces), an optional Nerd Font glyph + 1-space
//	separator (only when icons are enabled), the file name, a separator
//	space, the close ×, and a trailing space.
//
// The X coordinates are virtual (as if the strip never scrolled);
// drawTabBar subtracts tabScroll before painting and stores the
// shifted rects, so click hit-testing always works in screen space.
func (a *App) layoutTabs() []tabRect {
	out := make([]tabRect, 0, a.tabs.Len())
	cursor := a.sidebarW() + menuButtonWidth
	iconW := 0
	if a.iconsOn() {
		iconW = 2 // glyph + space
	}
	for i, t := range a.tabs.Tabs() {
		nameLen := len([]rune(t.DisplayName()))
		w := 1 + 2 + iconW + nameLen + 1 + 1 + 1 // pad+dirty+icon?+name+space+×+pad
		out = append(out, tabRect{
			Index:  i,
			X:      cursor,
			Width:  w,
			CloseX: cursor + 1 + 2 + iconW + nameLen + 1,
		})
		cursor += w
	}
	return out
}

// drawTabBar paints the tab bar across the top of the editor area: first
// the menu button (≡), then any open tabs.
func (a *App) drawTabBar() {
	tx, ty, tw, _ := a.tabBarRect()
	barStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)
	for cx := tx; cx < tx+tw; cx++ {
		a.screen.SetContent(cx, ty, ' ', nil, barStyle)
	}

	a.drawMenuButton()

	// Shift the virtual layout by the strip scroll and remember the
	// shifted rects — hit-testing then stays in screen coordinates.
	stripX, _ := a.tabStripRegion()
	rects := a.layoutTabs()
	for i := range rects {
		rects[i].X -= a.tabScroll
		rects[i].CloseX -= a.tabScroll
	}
	a.lastTabRects = rects
	for _, r := range rects {
		active := r.Index == a.tabs.ActiveIndex()
		bg := a.theme.SidebarBG
		fg := a.theme.Muted
		if active {
			bg = a.theme.BG
			fg = a.theme.Text
		}
		st := tcell.StyleDefault.Background(bg).Foreground(fg)
		if active {
			// On a degraded palette the active tab's BG/Text have
			// collapsed onto the inactive ones, so Attrs.ActiveTab is
			// the only thing left separating "focused" from "open".
			// It is AttrNone on a truecolor palette.
			st = st.Attributes(tcell.AttrBold | a.theme.Attrs.ActiveTab)
		}
		// Preview tabs render in italics — the visual promise that the
		// next tree click will replace this tab rather than add one.
		if a.tabs.At(r.Index).IsPreview() {
			st = st.Italic(true)
		}
		// Background. Cells scrolled off either edge of the strip are
		// skipped; the chevrons painted below mark what's hidden.
		for cx := r.X; cx < r.X+r.Width; cx++ {
			if cx < stripX {
				continue
			}
			if cx >= tx+tw {
				break
			}
			a.screen.SetContent(cx, ty, ' ', nil, st)
		}
		tab := a.tabs.At(r.Index)
		col := r.X + 1
		if tab.Dirty && col >= stripX && col < tx+tw {
			// Same story for the dirty dot: Modified is ColorDefault
			// once the palette degrades, so the marker leans on
			// Attrs.Modified to stay distinguishable from the name.
			// Decompose keeps the row's own attributes (bold/italic)
			// instead of clobbering them.
			_, _, rowAttrs := st.Decompose()
			dot := st.Foreground(a.theme.Modified).
				Attributes(rowAttrs | a.theme.Attrs.Modified)
			a.screen.SetContent(col, ty, '●', nil, dot)
		}
		col += 2 // skip dirty slot.
		// Per-language Nerd Font glyph between the dirty dot and the
		// filename — only when icons are enabled. Coloured the same
		// way the file tree glyphs are (icons.ColorFor) so the eye
		// connects "this tab" to "that row in the tree" instantly.
		if a.iconsOn() {
			name := tab.DisplayName()
			glyph := icons.For(name, false, false)
			gfg := icons.ColorFor(name, false, fg)
			gst := tcell.StyleDefault.Background(bg).Foreground(gfg)
			if active {
				gst = gst.Bold(true)
			}
			for _, gr := range glyph {
				if col >= tx+tw {
					break
				}
				if col >= stripX {
					a.screen.SetContent(col, ty, gr, nil, gst)
				}
				col++
			}
			col++ // separator space after glyph
		}
		for _, ru := range tab.DisplayName() {
			if col >= tx+tw {
				break
			}
			if col >= stripX {
				a.screen.SetContent(col, ty, ru, nil, st)
			}
			col++
		}
		col++ // separator space before ×
		if col >= stripX && col < tx+tw {
			// Emphasis tracks likelihood of use: the active tab's × is
			// the likeliest close target, so it gets the brighter Muted;
			// inactive tabs recede to Subtle so their × can't outshine
			// the active tab's controls.
			closeStyle := st.Foreground(a.theme.Subtle)
			if active {
				closeStyle = st.Foreground(a.theme.Muted)
			}
			a.screen.SetContent(col, ty, '×', nil, closeStyle)
		}
	}

	// Overflow chevrons — the same ‹ › affordance the editor uses for
	// clipped lines, painted over the strip's extreme cells in Accent.
	// Each is also a click target that scrolls the strip (tabBarClick).
	chevStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Accent)
	if a.tabScroll > 0 {
		a.screen.SetContent(stripX, ty, '‹', nil, chevStyle)
	}
	if a.tabScroll < a.maxTabScroll() {
		a.screen.SetContent(tx+tw-1, ty, '›', nil, chevStyle)
	}
}

// drawSplitter paints a 1-column vertical line at the right edge of the
// sidebar. Idle it sits in Subtle grey; while the user is dragging it
// brightens to Accent so the active grab handle is unmistakable.
func (a *App) drawSplitter() {
	x := a.splitterX()
	if x < 0 {
		return
	}
	fg := a.theme.Subtle
	if a.dragMode == "sidebar" {
		fg = a.theme.Accent
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg)
	for y := 0; y < a.height-1; y++ {
		a.screen.SetContent(x, y, '│', nil, style)
	}
}

// emptyEditorHints are the ways out of the "no file open" state, in the
// order a new user is likely to want them. Both a mouse line and a
// keyboard line are shown on purpose: skiff is mouse-first, but its
// natural habitat is an SSH session where the mouse may not be wired
// through at all, and a hint that only names the mouse is a dead end
// there. The Esc leader is the only shortcut mechanism the editor has
// (no Ctrl+ bindings), so naming its three most useful gestures is the
// whole keyboard onboarding story.
var emptyEditorHints = []string{
	"Click a file in the tree, or  ≡  for the menu",
	"Esc p  find file  ·  Esc n  new file  ·  Esc Esc  menu",
}

// drawEmptyEditor paints the placeholder shown when no tabs are open.
// Every line is trimmed to the editor's width before centering — on a
// narrow pane the untrimmed 45-rune hint used to start left of the
// editor rect and overwrite file-tree rows and the splitter.
func (a *App) drawEmptyEditor() {
	ex, ey, ew, eh := a.editorRect()
	bg := a.theme.BG
	muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	bold := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text).Bold(true)
	for cy := ey; cy < ey+eh; cy++ {
		for cx := ex; cx < ex+ew; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil, muted)
		}
	}
	cy := ey + eh/2
	title := trimRunes("No file open", ew)
	cx1 := ex + (ew-runeLen(title))/2
	for i, r := range title {
		a.screen.SetContent(cx1+i, cy-1, r, nil, bold)
	}
	// Hints stack downward from one row below the title. Rows that
	// would fall outside the editor rect are dropped rather than
	// clipped, so a two-row pane still shows the title cleanly.
	for h, hint := range emptyEditorHints {
		hy := cy + 1 + h
		if hy >= ey+eh {
			break
		}
		msg := trimRunes(hint, ew)
		hx := ex + (ew-runeLen(msg))/2
		for i, r := range msg {
			a.screen.SetContent(hx+i, hy, r, nil, muted)
		}
	}
	a.screen.HideCursor()
}

// statusConflictTag is the persistent "this buffer and the file on disk
// have diverged" marker. It lives in the status bar rather than in a
// flash because a dismissed conflict overlay must not mean a forgotten
// conflict — the warning has to survive until the user saves, reloads,
// or closes the tab.
const statusConflictTag = "⚠ disk conflict "

// drawStatusBar paints the bottom status bar.
func (a *App) drawStatusBar() {
	sx, sy, sw, _ := a.statusRect()
	bg := a.theme.StatusBG
	// StatusFg is paired with StatusBG per palette — hardcoding BG here
	// broke ported themes whose status bar isn't an accent color.
	fg := a.theme.StatusFg
	// Attrs.StatusBar is empty (AttrNone) on a truecolor palette, so
	// this is a no-op there. On a degraded palette StatusBG/StatusFg
	// have collapsed onto the terminal default and reverse video is
	// the only thing left that still says "this row is a bar".
	// Attributes replaces the mask outright, so AttrBold is carried
	// explicitly rather than chained through .Bold(true).
	style := tcell.StyleDefault.Background(bg).Foreground(fg).
		Attributes(tcell.AttrBold | a.theme.Attrs.StatusBar)
	for cx := sx; cx < sx+sw; cx++ {
		a.screen.SetContent(cx, sy, ' ', nil, style)
	}

	// Right-side text: current git branch (plus a changed-path count when
	// there is one) when we're inside a repo. Drawn first so the left-side
	// text can be clipped against it and the two pieces never overlap on a
	// narrow window. The segment doubles as the click target that opens
	// the Git changes modal — see statusBarClick.
	var rightWidth int
	if right := a.statusGitSegment(); right != "" {
		rw := len([]rune(right))
		if rw < sw {
			drawAt(a.screen, sx+sw-rw, sy, right, style)
			rightWidth = rw
		}
	}

	// Pending-gesture tag: while an Esc is armed (leader or double-tap
	// window still open) show "Esc…" beside the git segment — vim's
	// showcmd idea sized for a status bar. The editor's only modifier
	// must not have invisible state: without this, a slow second
	// keystroke fails with no cue that the gesture died. A
	// leaderExpiryEvent posted at arming time repaints the bar so the
	// tag also clears when the user simply abandons the Esc.
	if !a.lastEscape.IsZero() && time.Since(a.lastEscape) < menuEscWindow {
		tag := "Esc… "
		tw := len([]rune(tag))
		if tw+rightWidth < sw {
			drawAt(a.screen, sx+sw-rightWidth-tw, sy, tag, style)
			rightWidth += tw
		}
	}

	// Persistent disk-conflict marker for the active tab. Drawn after
	// the transient Esc tag so it sits furthest left of the right-hand
	// group and never jumps around as the leader arms and expires. It
	// stays put until the conflict is actually resolved — see
	// tabDiskConflict.
	if a.tabDiskConflict(a.activeTabPtr()) {
		cw := runeLen(statusConflictTag)
		if cw+rightWidth < sw {
			warn := style.Foreground(a.theme.Error).
				Attributes(tcell.AttrBold | a.theme.Attrs.StatusBar | a.theme.Attrs.Error)
			drawAt(a.screen, sx+sw-rightWidth-cw, sy, statusConflictTag, warn)
			rightWidth += cw
		}
	}

	// Left-side text: status flash, file info, or root dir.
	var left string
	if time.Now().Before(a.statusUntil) && a.statusMsg != "" {
		left = " " + a.statusMsg
	} else if tab := a.activeTabPtr(); tab != nil {
		if tab.IsImage() && tab.Image != nil {
			b := tab.Image.Bounds()
			left = fmt.Sprintf(" %s · %d×%d · %s",
				strings.ToUpper(tab.ImageFmt), b.Dx(), b.Dy(), filepath.Base(tab.Path))
		} else {
			lang := detectLangLabel(tab.Path)
			dirty := ""
			if tab.Dirty {
				dirty = " · ●"
			}
			left = fmt.Sprintf(" %s · Ln %d, Col %d · %d lines%s",
				lang, tab.Cursor.Line+1, tab.Cursor.Col+1, tab.Buffer.LineCount(), dirty)
		}
	} else {
		left = " " + filepath.Base(a.rootDir)
	}
	// One cell of breathing room between left and right text so they
	// don't visually butt up against each other on a tight terminal.
	leftMax := sw - rightWidth
	if rightWidth > 0 {
		leftMax--
	}
	if leftMax < 0 {
		leftMax = 0
	}
	drawStatusText(a.screen, sx, sy, leftMax, left, style)
}

// drawTooSmall paints a centred error message when the terminal window is
// smaller than the editor's minimum supported size.
func (a *App) drawTooSmall() {
	style := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Error).Bold(true)
	for cy := 0; cy < a.height; cy++ {
		for cx := 0; cx < a.width; cx++ {
			a.screen.SetContent(cx, cy, ' ', nil,
				tcell.StyleDefault.Background(a.theme.BG))
		}
	}
	msg := "Window too small — please resize"
	cy := a.height / 2
	cx := (a.width - len([]rune(msg))) / 2
	if cx < 0 {
		cx = 0
	}
	for i, r := range msg {
		if cx+i >= a.width {
			break
		}
		a.screen.SetContent(cx+i, cy, r, nil, style)
	}
	a.screen.HideCursor()
}

// drawStatusText writes s left-aligned into the status bar at (x, y) with a
// max width of maxW cells. Truncates rather than wraps.
func drawStatusText(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		if col >= maxW {
			return
		}
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// drawAt writes s starting at (x, y) without bounds checking. Callers are
// expected to keep the string within the rectangle they're drawing into.
func drawAt(scr tcell.Screen, x, y int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// trimRunes shortens s to max visible cells, reserving the final cell for an
// ellipsis when truncation is needed.
func trimRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if runeLen(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	rs := []rune(s)
	return string(rs[:max-1]) + "…"
}

// detectLangLabel returns a short label for the active file's language —
// just the file extension, or "text" when there is no path or extension.
func detectLangLabel(path string) string {
	if path == "" {
		return "text"
	}
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return "text"
	}
	return ext
}
