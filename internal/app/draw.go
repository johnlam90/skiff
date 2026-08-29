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
			a.tree.ScrollbarActive = a.dragMode == dragTreeScrollbar
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
		tab.ScrollbarActive = a.dragMode == dragScrollbar
		tab.Render(a.screen, a.theme, ex, ey, ew, eh)
	} else {
		a.drawEmptyEditor()
	}

	if a.findOpen {
		a.drawFindBar()
	}
	a.drawStatusBar()
	a.drawFlashStrip()
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
			// Two markers, on purpose. Colour (BG/Text) is the primary
			// one; the underline is the one that survives everything
			// else. On a degraded palette the active tab's BG/Text have
			// collapsed onto the inactive ones and Attrs.ActiveTab is
			// all that is left — but even on a truecolor palette a
			// crowded strip carrying an italic preview tab makes
			// "which tab is focused?" a colour puzzle, so the rule
			// under the tab is drawn unconditionally. It reads as the
			// bottom edge of a raised tab, which is the thing the strip
			// is imitating.
			//
			// Attributes replaces the mask outright, so Underline comes
			// after it — and it must be Underline(true) rather than an
			// AttrUnderline bit: the terminal driver emits the escape
			// from the style's underline STYLE, and AttrUnderline alone
			// leaves that at None (i.e. paints nothing).
			st = st.Attributes(tcell.AttrBold | a.theme.Attrs.ActiveTab).
				Underline(true)
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
		if (tab.Dirty || tab.DiskGone) && col >= stripX && col < tx+tw {
			// The dot means "needs attention", not just "has edits" — a
			// DiskGone tab (its file deleted, not yet recreated or
			// re-saved) shows it too, same as Dirty. Modified is
			// ColorDefault once the palette degrades, so the marker
			// leans on Attrs.Modified to stay distinguishable from the
			// name. Decompose keeps the row's own attributes
			// (bold/italic) instead of clobbering them.
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
				gst = gst.Bold(true).Underline(true)
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

	// Overflow badges — the same ‹ › affordance the editor uses for
	// clipped lines, now carrying how many tabs are hidden on each side
	// and painted in reverse video so the marker is unmistakable on a
	// monochrome terminal too. Each badge is also the click target that
	// scrolls the strip (tabBarClick hit-tests the same geometry).
	chevStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).
		Foreground(a.theme.Accent).Attributes(tcell.AttrBold | tcell.AttrReverse)
	left, right := a.tabChevrons()
	for _, c := range [2]tabChevron{left, right} {
		if c.Label == "" {
			continue
		}
		drawAt(a.screen, c.X, ty, c.Label, chevStyle)
	}
}

// minVisibleTabCells is one minimal tab's worth of columns — pad, dirty
// slot, a single-rune name, the × and its spacing. tabChevrons keeps at
// least this much strip clear of its badges: a marker wide enough to bury
// the tabs it points at defeats itself.
const minVisibleTabCells = 8

// tabChevron is one overflow badge: where it starts and what it says. An
// empty Label means that side has nothing hidden. drawTabBar paints these
// and tabBarClick hit-tests them, so the whole badge — chevron and count
// together — is one click target.
type tabChevron struct {
	X     int
	Label string
}

// hit reports whether screen column x lands on the badge.
func (c tabChevron) hit(x int) bool {
	return c.Label != "" && x >= c.X && x < c.X+runeLen(c.Label)
}

// tabOverflow counts the tabs scrolled entirely out of the strip on each
// side. A partially visible tab is not counted: its name is on screen and
// clicking it works, so counting it would overstate what the badge is
// promising to reveal.
func (a *App) tabOverflow() (left, right int) {
	stripX, stripW := a.tabStripRegion()
	if stripW <= 0 {
		return 0, 0
	}
	for _, r := range a.layoutTabs() {
		x0 := r.X - a.tabScroll
		switch {
		case x0+r.Width <= stripX:
			left++
		case x0 >= stripX+stripW:
			right++
		}
	}
	return left, right
}

// tabChevrons returns the two overflow badges for the current scroll
// position. The chevron alone says "there is more"; the count says how
// much, which is the difference between a marker the eye skips and one
// that tells the user whether it is worth scrolling. The counts are the
// first thing dropped when the strip is too cramped to hold them and a
// readable tab at the same time — the chevrons themselves never are,
// because they are the click targets.
func (a *App) tabChevrons() (left, right tabChevron) {
	stripX, stripW := a.tabStripRegion()
	if stripW <= 0 {
		return
	}
	showLeft := a.tabScroll > 0
	showRight := a.tabScroll < a.maxTabScroll()
	if !showLeft && !showRight {
		return
	}
	nl, nr := a.tabOverflow()
	leftLabel, rightLabel := "", ""
	if showLeft {
		leftLabel = "‹"
		if nl > 0 {
			leftLabel += itoa(nl)
		}
	}
	if showRight {
		rightLabel = "›"
		if nr > 0 {
			rightLabel = itoa(nr) + "›"
		}
	}
	if runeLen(leftLabel)+runeLen(rightLabel)+minVisibleTabCells > stripW {
		if showLeft {
			leftLabel = "‹"
		}
		if showRight {
			rightLabel = "›"
		}
	}
	left = tabChevron{X: stripX, Label: leftLabel}
	right = tabChevron{X: stripX + stripW - runeLen(rightLabel), Label: rightLabel}
	return
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
	if a.dragMode == dragSidebar {
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

// drawStatusBar paints the bottom status bar: the right-hand group
// (branch, pending-Esc tag, disk-conflict marker) stacked leftward from
// the right edge, then the left-hand text clipped against whatever room
// the group left it.
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

	// Right-hand group first, so the left-side text can be clipped
	// against it and the two pieces never overlap on a narrow window.
	// The git segment doubles as the click target that opens the Git
	// changes panel — see statusBarClick.
	rightX := sx + sw
	for _, seg := range a.statusRightSegments(sw) {
		rightX -= runeLen(seg.text)
		st := style
		if seg.warn {
			st = style.Foreground(a.theme.Error).
				Attributes(tcell.AttrBold | a.theme.Attrs.StatusBar | a.theme.Attrs.Error)
		}
		drawAt(a.screen, rightX, sy, seg.text, st)
	}

	drawStatusText(a.screen, sx, sy, a.statusLeftMax(sw), a.statusLeftText(), style)
}

// statusRightSegment is one piece of the status bar's right-hand group.
type statusRightSegment struct {
	text string
	// warn paints the piece in Error instead of the bar's own
	// foreground: the disk-conflict marker is a warning, not a readout.
	warn bool
}

// statusRightSegments returns the right-hand pieces that fit in a status
// bar sw cells wide, rightmost first: the git branch segment, the
// pending-Esc tag, then the persistent disk-conflict marker. The order is
// load-bearing — the transient tag sits between the two stable pieces so
// the conflict marker never jumps around as the leader arms and expires.
//
// Pure, and the single source of the group's width: drawStatusBar paints
// from it and statusLeftMax measures from it, so the room the flash is
// clipped to and the room it is tested against cannot disagree.
func (a *App) statusRightSegments(sw int) []statusRightSegment {
	var segs []statusRightSegment
	used := 0
	add := func(text string, warn bool) {
		w := runeLen(text)
		// Pieces are dropped whole rather than clipped: half a branch
		// name is worse than none, and a clipped piece would silently
		// reclaim cells the left text was already measured against.
		if w == 0 || used+w >= sw {
			return
		}
		segs = append(segs, statusRightSegment{text: text, warn: warn})
		used += w
	}
	add(a.statusGitSegment(), false)
	// Pending-gesture tag: while an Esc is armed (leader or double-tap
	// window still open) show "Esc…" beside the git segment — vim's
	// showcmd idea sized for a status bar. The editor's only modifier
	// must not have invisible state: without this, a slow second
	// keystroke fails with no cue that the gesture died. A
	// leaderExpiryEvent posted at arming time repaints the bar so the
	// tag also clears when the user simply abandons the Esc.
	if !a.lastEscape.IsZero() && time.Since(a.lastEscape) < menuEscWindow {
		add("Esc… ", false)
	}
	// Persistent disk-conflict marker for the active tab: dismissing the
	// conflict overlay must not mean forgetting the conflict, so this
	// stays up until the tab is saved, reloaded or closed.
	if a.tabDiskConflict(a.activeTabPtr()) {
		add(statusConflictTag, true)
	}
	return segs
}

// statusRightWidth is how many cells the right-hand group occupies.
func (a *App) statusRightWidth(sw int) int {
	n := 0
	for _, seg := range a.statusRightSegments(sw) {
		n += runeLen(seg.text)
	}
	return n
}

// statusLeftMax returns how many cells of a status bar sw wide are left
// for the left-hand text, including the one cell of breathing room that
// keeps it from visually butting up against the right-hand group.
func (a *App) statusLeftMax(sw int) int {
	rw := a.statusRightWidth(sw)
	max := sw - rw
	if rw > 0 {
		max--
	}
	if max < 0 {
		max = 0
	}
	return max
}

// statusLeftText is the status bar's left-hand text: the live flash, else
// the active tab's readout, else the project root.
//
// A flash that moved onto its own strip is skipped here on purpose. The
// bar then falls back to the readout the flash would have covered, so a
// long message costs the user nothing — they read the whole sentence on
// the strip AND keep Ln/Col — instead of trading one for the other.
func (a *App) statusLeftText() string {
	if a.flashActive() && !a.flashStripVisible() {
		return " " + a.statusMsg
	}
	if tab := a.activeTabPtr(); tab != nil {
		if tab.IsImage() && tab.Image != nil {
			b := tab.Image.Bounds()
			return fmt.Sprintf(" %s · %d×%d · %s",
				strings.ToUpper(tab.ImageFmt), b.Dx(), b.Dy(), filepath.Base(tab.Path))
		}
		// Same "needs attention" gate as the tab-strip dot: DiskGone
		// alone (a deleted-but-not-yet-recreated file) shows the marker
		// too, not just Dirty.
		dirty := ""
		if tab.Dirty || tab.DiskGone {
			dirty = " · ●"
		}
		return fmt.Sprintf(" %s · Ln %d, Col %d · %d lines%s",
			detectLangLabel(tab.Path), tab.Cursor.Line+1, tab.Cursor.Col+1,
			tab.Buffer.LineCount(), dirty)
	}
	return " " + filepath.Base(a.rootDir)
}

// statusFlashRoom is how many cells the status bar can give the flash
// message itself: its left region minus the one cell of pad
// statusLeftText prefixes. flashStripVisible measures against this, so
// "would this be truncated?" is answered by the same arithmetic that does
// the truncating.
func (a *App) statusFlashRoom() int {
	_, _, sw, _ := a.statusRect()
	room := a.statusLeftMax(sw) - 1
	if room < 0 {
		room = 0
	}
	return room
}

// flashStripMaxRows caps how many rows a wrapped flash may take. Three is
// enough for the messages that motivated the strip — a format error
// quoting the formatter's stderr, a project-replace report naming the file
// whose save failed, a git command's first line of output — and few enough
// that something which expires in three seconds can never swallow the
// viewport.
const flashStripMaxRows = 3

// flashExpiryEvent is posted shortly after a flash that opened the strip
// is due to expire. It carries no action of its own — reaching the event
// loop is the point, because Run redraws after every event and that
// repaint hands the editor its row back. Without it the strip, and the
// reflow it caused, would linger until the user happened to press a key.
type flashExpiryEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *flashExpiryEvent) When() time.Time { return e.when }

// scheduleFlashStripExpiry wakes the event loop just past the current
// flash's window, but only when the flash actually opened the strip. A
// message living entirely inside the status bar costs no layout, so
// letting its text go stale until the next event is free — the same
// reasoning behind the Esc tag only getting a wake-up when an Esc was
// armed.
func (a *App) scheduleFlashStripExpiry() {
	if a.screen == nil || !a.flashStripVisible() {
		return
	}
	scr := a.screen
	time.AfterFunc(statusFlashFor+50*time.Millisecond, func() {
		_ = scr.PostEvent(&flashExpiryEvent{when: time.Now()})
	})
}

// flashActive reports whether a transient flash message is showing right
// now. The status bar's left text and the flash strip both gate on it, so
// there is exactly one definition of "a flash is up".
func (a *App) flashActive() bool {
	return a.statusMsg != "" && time.Now().Before(a.statusUntil)
}

// flashStripVisible reports whether the live flash needs rows of its own.
// Two conditions: the status bar's left region is too narrow to hold the
// message whole, and the strip can show more of it than that region can.
//
// The second is not a formality. The strip's advantage is not width — with
// the sidebar hidden it is the same width as the bar — it is that it may
// wrap onto flashStripMaxRows. So the comparison is against capacity, and
// it genuinely fails in one shape: a very wide window whose sidebar has
// been dragged out to its legal maximum leaves the strip about forty
// columns while the bar keeps nearly all of them. Reflowing the editor to
// show LESS of the message would be strictly worse.
//
// The too-small window is excluded because drawTooSmall owns the whole
// screen there; that also covers the zero-size state New() flashes into
// before Run has asked the terminal how big it is.
func (a *App) flashStripVisible() bool {
	if !a.flashActive() || a.width < minWidth || a.height < minHeight {
		return false
	}
	room := a.statusFlashRoom()
	return runeLen(a.statusMsg) > room && a.flashStripCapacity() > room
}

// flashStripTextWidth is how many cells of one strip row carry text: the
// editor's own width minus the one-cell left pad that keeps the message
// off the splitter, mirroring the pad statusLeftText prefixes.
func (a *App) flashStripTextWidth() int {
	w := a.width - a.sidebarW() - 1
	if w < 0 {
		w = 0
	}
	return w
}

// flashStripCapacity is the most message the strip could show: every row
// it is allowed to take, at full width.
func (a *App) flashStripCapacity() int {
	return a.flashStripTextWidth() * flashStripMaxRows
}

// flashStripRows is how many rows the strip needs right now: zero when it
// isn't showing, else the wrapped message's row count, capped by what the
// editor can spare. editorRect subtracts it, and that subtraction is what
// keeps the editor's own scrollbar, caret and hit-testing inside the
// region actually painted — so the cap is what makes the subtraction safe
// rather than something editorRect has to clean up afterwards.
func (a *App) flashStripRows() int {
	if !a.flashStripVisible() {
		return 0
	}
	n := len(wrapFlashLines(a.statusMsg, a.flashStripTextWidth()))
	if budget := a.stripRowBudget(); n > budget {
		n = budget
	}
	return n
}

// flashStripRect returns the strip's screen rectangle: the rows directly
// above the status bar, above the find bar when that is open too, and
// aligned with the editor exactly the way findBarRect is.
func (a *App) flashStripRect() (x, y, w, h int) {
	sw := a.sidebarW()
	h = a.flashStripRows()
	y = a.height - 1 - h
	if a.findOpen || a.projFindOpen {
		y -= findBarHeight
	}
	return sw, y, a.width - sw, h
}

// wrapFlashLines breaks msg into at most flashStripMaxRows lines of w
// cells, preferring a space break so words stay whole and falling back to
// a hard break when a single run is wider than the strip. The last line
// is ellipsised if the message still doesn't fit, because a strip that
// silently drops its tail is the bug it exists to fix.
func wrapFlashLines(msg string, w int) []string {
	if w <= 0 || msg == "" {
		return nil
	}
	rs := []rune(msg)
	out := make([]string, 0, flashStripMaxRows)
	for len(rs) > 0 {
		if len(rs) <= w {
			return append(out, string(rs))
		}
		if len(out) == flashStripMaxRows-1 {
			return append(out, trimRunes(string(rs), w))
		}
		// rs[w] is the rune that would start the next row; when it is
		// already a space the first w runes are whole words and the full
		// row is usable. Only when it isn't do we hunt backwards for the
		// last space that fits — and a run with none breaks hard.
		cut := w
		if rs[w] != ' ' {
			for i := w; i > 0; i-- {
				if rs[i-1] == ' ' {
					cut = i
					break
				}
			}
		}
		out = append(out, strings.TrimRight(string(rs[:cut]), " "))
		rs = rs[cut:]
		// Spaces at a wrap point are the break, not content.
		for len(rs) > 0 && rs[0] == ' ' {
			rs = rs[1:]
		}
	}
	return out
}

// drawFlashStrip paints the transient flash rows above the status bar,
// styled like the find bar and the leader strip so the three read as one
// family of transient chrome.
//
// It is a strip, not an overlay (docs/adr/0001): it captures no keys, it
// never goes on a.overlays, and the editor reflowed for it rather than
// being covered by it.
func (a *App) drawFlashStrip() {
	x, y, w, h := a.flashStripRect()
	if h <= 0 || w <= 0 || y < 1 {
		return
	}
	style := tcell.StyleDefault.Background(a.theme.LineHL).
		Foreground(a.theme.Text).Bold(true)
	lines := wrapFlashLines(a.statusMsg, a.flashStripTextWidth())
	for i := 0; i < h; i++ {
		for cx := x; cx < x+w; cx++ {
			a.screen.SetContent(cx, y+i, ' ', nil, style)
		}
		if i < len(lines) {
			drawStatusText(a.screen, x+1, y+i, w-1, lines[i], style)
		}
	}
}

// tooSmallLines is the refusal for the current size: a label, then the
// measurement that says what to do about it. The measurement is the
// useful half on the device this floor exists for — a phone user cannot
// resize a terminal, only shrink its font — so it names both what the
// terminal is and what skiff needs.
//
// The label degrades to a shorter wording and is clipped by drawTooSmall
// if even that overruns; a blank screen is the one outcome worse than a
// truncated word. The measurement is all-or-nothing: half a size is not
// a size, so it is dropped rather than cut.
func (a *App) tooSmallLines() []string {
	label := "Window too small — please resize"
	if runeLen(label) > a.width {
		label = "Too small"
	}
	out := []string{label}
	size := fmt.Sprintf("%d×%d — needs %d×%d", a.width, a.height, minWidth, minHeight)
	if runeLen(size) > a.width {
		size = fmt.Sprintf("needs %d×%d", minWidth, minHeight)
	}
	if runeLen(size) <= a.width {
		out = append(out, size)
	}
	if len(out) > a.height {
		out = out[:a.height]
	}
	return out
}

// drawTooSmall paints the centred refusal when the terminal window is
// smaller than the editor's minimum supported size.
func (a *App) drawTooSmall() {
	style := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Error).Bold(true)
	for cy := range a.height {
		for cx := range a.width {
			a.screen.SetContent(cx, cy, ' ', nil,
				tcell.StyleDefault.Background(a.theme.BG))
		}
	}
	lines := a.tooSmallLines()
	top := (a.height - len(lines)) / 2
	if top < 0 {
		top = 0
	}
	for i, msg := range lines {
		// Ranging []rune, not the string: an em dash is three bytes, and
		// a byte index used as a column pushed everything after it two
		// cells right — which on the widths this screen appears at is
		// the difference between centred and clipped.
		rs := []rune(msg)
		cx := (a.width - len(rs)) / 2
		if cx < 0 {
			cx = 0
		}
		for j, r := range rs {
			if cx+j >= a.width {
				break
			}
			a.screen.SetContent(cx+j, top+i, r, nil, style)
		}
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
