// =============================================================================
// File: internal/app/gitlog.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

// Commit history modal — lazygit's commits panel shrunk to a Skiff
// list. Two flavours share everything: the branch log (≡ → Commit
// history, or a click on the branch row of the Git panel) and a single
// file's history (≡ → History of this file, `git log --follow` under
// the hood). Rows show short SHA, subject, and relative age; Enter or
// a click opens that commit's diff in the diff view — multi-file for
// branch commits, just the one file for file history.
//
// Read-only like the rest of the git integration: no checkout, no
// rebase, no cherry-pick. It answers "what happened here?" and shows
// you the change; rewriting history stays in the shell.

import (
	"path/filepath"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/overlay"
)

const (
	// gitLogModalMaxWidth mirrors the finder — same modal family, same
	// reading width.
	gitLogModalMaxWidth = 80
	// gitLogVisible caps how many commit rows render at once; longer
	// histories scroll.
	gitLogVisible = 14
	// gitLogLimit bounds how many commits we ask git for. 200 is
	// several screens of scrolling — past that, the answer the user
	// wants lives in `git log` proper.
	gitLogLimit = 200
)

// gitLogOverlay is the commit-history overlay: the loaded commits and
// the highlight/scroll window. Bespoke — it needs the App for the diff
// view hand-off and theme — and its state dies with it.
type gitLogOverlay struct {
	app      *App
	title    string
	path     string
	entries  []git.Commit
	selected int
	scroll   int
}

// openGitLog shows the history overlay. path narrows it to one file
// (file history); empty path shows the branch log. An empty result —
// or a git that refused to answer, which outside a repo it will —
// flashes instead of opening a hollow modal. The read is synchronous
// (the log is bounded to gitLogLimit rows); it goes through readRepo
// so a test scripts it like every other surface.
func (a *App) openGitLog(title, path string) {
	entries, _ := a.readRepo().Log(gitLogLimit, path)
	if len(entries) == 0 {
		a.flash("No commit history found")
		return
	}
	a.closeAllModals()
	a.overlays.Open(&gitLogOverlay{app: a, title: title, path: path, entries: entries})
}

// menuCommitHistory is the ≡ menu entry for the branch log.
func (a *App) menuCommitHistory() {
	a.closeMenu()
	a.openGitLog("History · "+a.gitSnap.Branch, "")
}

// menuFileHistory is the ≡ menu entry for the active file's history.
func (a *App) menuFileHistory() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" || tab.IsImage() {
		return
	}
	a.openGitLog("History · "+filepath.Base(tab.Path), tab.Path)
}

// hasFileHistoryTab is the menu enabled-predicate for History of this
// file: a file-backed tab inside a repo. The file itself may be clean —
// history is about the past, not the working tree.
func (a *App) hasFileHistoryTab() bool {
	tab := a.activeTabPtr()
	return tab != nil && tab.Path != "" && !tab.IsImage() && a.gitSnap.Branch != ""
}

// activate opens the selected commit's diff in the diff view. For file
// history the diff is limited to that file and keeps its syntax
// highlighting; branch commits show every file with boundary rows and
// skip syntax colors (one lexer can't serve many languages).
func (g *gitLogOverlay) activate() {
	if g.selected < 0 || g.selected >= len(g.entries) {
		return
	}
	a := g.app
	entry := g.entries[g.selected]
	patch, err := a.readRepo().Show(entry.Hash, g.path)
	if err != nil && patch.Empty() {
		// Git said no (or the parser could read none of it): the
		// reason beats a silent empty answer.
		a.flash("Couldn't load commit " + entry.Hash + ": " + err.Error())
		return
	}
	if patch.Empty() {
		// Merge commits and rename-only commits can produce no diff
		// for this path — say so rather than opening an empty modal.
		a.flash("No diff for commit " + entry.Hash)
		return
	}
	title := "Commit " + entry.Hash
	langPath := ""
	if g.path != "" {
		title += " · " + filepath.Base(g.path)
		langPath = g.path
	}
	// openDiffView closes this overlay via closeAllModals.
	a.openDiffView(title, patch, "", langPath)
}

// HandleKey routes keyboard input while the overlay is open.
func (g *gitLogOverlay) HandleKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		g.app.closeAllModals()
	case tcell.KeyEnter:
		g.activate()
	case tcell.KeyUp:
		if g.selected > 0 {
			g.selected--
		}
	case tcell.KeyDown:
		if g.selected < len(g.entries)-1 {
			g.selected++
		}
	case tcell.KeyPgUp:
		g.selected -= gitLogVisible
		if g.selected < 0 {
			g.selected = 0
		}
	case tcell.KeyPgDn:
		g.selected += gitLogVisible
		if g.selected > len(g.entries)-1 {
			g.selected = len(g.entries) - 1
		}
	}
}

// HandleMouse mirrors the finder's mouse contract: hover highlights,
// wheel scrolls, click activates, click outside dismisses.
func (g *gitLogOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		g.scrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		g.scrollBy(3)
		return
	}
	r := g.rect()
	rowsStart := r.Y + 3
	row := g.scroll + (y - rowsStart)
	inRows := y >= rowsStart && y < r.Y+r.H-1 && x >= r.X && x < r.X+r.W
	if inRows && row >= 0 && row < len(g.entries) {
		g.selected = row
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		g.app.closeAllModals()
		return
	}
	if inRows && row >= 0 && row < len(g.entries) {
		g.selected = row
		g.activate()
	}
}

// scrollBy nudges the visible window by delta rows, clamped to the
// list's ends.
func (g *gitLogOverlay) scrollBy(delta int) {
	max := len(g.entries) - g.visibleRows()
	if max < 0 {
		max = 0
	}
	g.scroll += delta
	if g.scroll > max {
		g.scroll = max
	}
	if g.scroll < 0 {
		g.scroll = 0
	}
}

// visibleRows returns how many commit rows the overlay shows at once,
// shrunk on tiny terminals.
func (g *gitLogOverlay) visibleRows() int {
	rows := len(g.entries)
	if rows > gitLogVisible {
		rows = gitLogVisible
	}
	if rows > g.app.height-6 {
		rows = g.app.height - 6
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// rect returns the overlay's on-screen rectangle — the same width cap
// and upper-third anchor as the finder.
func (g *gitLogOverlay) rect() overlay.Rect {
	a := g.app
	w := gitLogModalMaxWidth
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 30 {
		w = 30
	}
	// 1 border + 1 title + 1 divider + N rows + 1 border.
	h := g.visibleRows() + 4
	x := (a.width - w) / 2
	y := (a.height - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return overlay.Rect{X: x, Y: y, W: w, H: h}
}

// Draw paints the overlay: shared frame chrome, then one row per
// commit — SHA in the soft accent, subject in the text color, age
// right-aligned and muted, selection flipping the row background.
//
// Layout (relY): 0 border · 1 title · 2 divider · 3..N commit rows ·
// N+1 border.
func (g *gitLogOverlay) Draw(scr tcell.Screen) {
	r := g.rect()
	overlay.DrawFrame(scr, r, g.title, g.app.theme)

	visCap := g.visibleRows()
	g.ensureSelectedVisible(visCap)
	rowsStart := r.Y + 3
	for i := 0; i < visCap; i++ {
		idx := g.scroll + i
		if idx >= len(g.entries) {
			break
		}
		g.drawRow(scr, r, rowsStart+i, g.entries[idx], idx == g.selected)
	}
	scr.HideCursor()
}

// drawRow paints one commit row inside the overlay.
func (g *gitLogOverlay) drawRow(scr tcell.Screen, r overlay.Rect, ry int, entry git.Commit, selected bool) {
	th := g.app.theme
	rowBG := th.LineHL
	if selected {
		rowBG = th.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(th.Text)
	shaStyle := tcell.StyleDefault.Background(rowBG).Foreground(th.AccentSoft).Bold(true)
	ageStyle := tcell.StyleDefault.Background(rowBG).Foreground(th.Muted)

	for cx := r.X + 1; cx < r.X+r.W-1; cx++ {
		scr.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	x := r.X + 2
	x = drawClipped(scr, x, ry, r.W-4, entry.Hash, shaStyle)
	// Age goes on the right; the subject gets whatever is left between.
	age := entry.When
	ageX := r.X + r.W - 2 - runeLen(age)
	drawClipped(scr, ageX, ry, runeLen(age), age, ageStyle)
	subjectW := ageX - x - 3
	drawClipped(scr, x+2, ry, subjectW, entry.Subject, rowStyle)
}

// ensureSelectedVisible keeps the selected row inside the visible
// window, sliding the window when arrow keys walk past its edges.
func (g *gitLogOverlay) ensureSelectedVisible(visCap int) {
	if g.selected < g.scroll {
		g.scroll = g.selected
	}
	if g.selected-g.scroll >= visCap {
		g.scroll = g.selected - visCap + 1
	}
	if g.scroll < 0 {
		g.scroll = 0
	}
}
