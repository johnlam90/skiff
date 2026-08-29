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
	"bytes"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/diff"
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

// gitLogEntry is one commit row: abbreviated SHA, subject line, and
// git's human-readable relative age ("2 days ago").
type gitLogEntry struct {
	SHA     string
	Subject string
	Age     string
}

// loadGitLog returns up to limit commits, newest first. path narrows
// the log to one file (with --follow so renames don't truncate the
// story); empty path logs the whole branch. Best-effort like every git
// loader here: nil on any failure.
func loadGitLog(rootDir, path string, limit int) []gitLogEntry {
	if rootDir == "" || limit <= 0 {
		return nil
	}
	args := []string{"log", "--format=%h%x09%s%x09%cr", "-n", itoa(limit)}
	if path != "" {
		args = append(args, "--follow", "--", path)
	}
	out, err := git.Output(rootDir, args...)
	if err != nil || len(out) == 0 {
		return nil
	}
	var entries []gitLogEntry
	for _, raw := range bytes.Split(bytes.TrimRight(out, "\n"), []byte{'\n'}) {
		parts := strings.SplitN(string(raw), "\t", 3)
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		entries = append(entries, gitLogEntry{SHA: parts[0], Subject: parts[1], Age: parts[2]})
	}
	return entries
}

// loadGitCommitDiff returns the unified diff a commit introduced —
// the whole commit, or just path's part of it when path is non-empty.
// --format= suppresses the message header so the output starts at the
// first `diff --git`, which is what the diff view's parser expects.
//
// sha is routed through git.SafeRef and the arg list always carries a
// trailing "--" even with no path, unlike every other loader here that
// only bothers when path is non-empty. This is defense-in-depth: today
// sha only ever holds loadGitLog's %h output (hex, single-line), so no
// current input reaches the refused class, but the gate is what stands
// between a future caller — or a format-string change upstream — and a
// repo-supplied string landing on git's argv as an option.
func loadGitCommitDiff(rootDir, sha, path string) diff.Patch {
	if rootDir == "" {
		return diff.Patch{}
	}
	safe, err := git.SafeRef(sha)
	if err != nil {
		return diff.Patch{}
	}
	args := []string{"show", "--format=", safe, "--"}
	if path != "" {
		args = append(args, path)
	}
	out, err := git.Output(rootDir, args...)
	if err != nil || len(out) == 0 {
		return diff.Patch{}
	}
	p, _ := diff.Parse(out)
	return p
}

// gitLogOverlay is the commit-history overlay: the loaded entries and
// the window over them. Bespoke — it needs the App for the diff view
// hand-off and theme — and its state dies with it. The highlight and
// the scroll offset are an overlay.List, the same window Pick, the
// menu, the finder and the git panel scroll.
type gitLogOverlay struct {
	overlay.List
	app     *App
	title   string
	path    string
	entries []gitLogEntry
}

// sync derives the window height from the entry count and the terminal,
// pushes both into the embedded List, and hands back the frame they
// size. rect reads the window back out, so every entry point calls this
// first — including the opener, so a caller can measure the frame
// before the first event arrives.
func (g *gitLogOverlay) sync() overlay.Rect {
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
	g.SetLen(len(g.entries))
	g.SetVisible(rows)
	return g.rect()
}

// openGitLog shows the history overlay. path narrows it to one file
// (file history); empty path shows the branch log. An empty result
// flashes instead of opening a hollow modal.
func (a *App) openGitLog(title, path string) {
	entries := loadGitLog(a.rootDir, path, gitLogLimit)
	if len(entries) == 0 {
		a.flash("No commit history found")
		return
	}
	a.closeAllModals()
	g := &gitLogOverlay{app: a, title: title, path: path, entries: entries}
	g.sync()
	a.overlays.Open(g)
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
	if g.Sel() < 0 || g.Sel() >= len(g.entries) {
		return
	}
	a := g.app
	entry := g.entries[g.Sel()]
	patch := loadGitCommitDiff(a.rootDir, entry.SHA, g.path)
	if patch.Empty() {
		// Merge commits and rename-only commits can produce no diff
		// for this path — say so rather than opening an empty modal.
		a.flash("No diff for commit " + entry.SHA)
		return
	}
	title := "Commit " + entry.SHA
	langPath := ""
	if g.path != "" {
		title += " · " + filepath.Base(g.path)
		langPath = g.path
	}
	// openDiffView closes this overlay via closeAllModals.
	a.openDiffView(title, patch, "", langPath)
}

// HandleKey routes keyboard input while the overlay is open. The
// vertical keys walk the entries and drag the window with them once
// they step past its edge.
func (g *gitLogOverlay) HandleKey(ev *tcell.EventKey) {
	g.sync()
	switch ev.Key() {
	case tcell.KeyEsc:
		g.app.closeAllModals()
	case tcell.KeyEnter:
		g.activate()
	case tcell.KeyUp:
		g.Move(-1)
		g.EnsureVisible()
	case tcell.KeyDown:
		g.Move(1)
		g.EnsureVisible()
	case tcell.KeyPgUp:
		g.Move(-gitLogVisible)
		g.EnsureVisible()
	case tcell.KeyPgDn:
		g.Move(gitLogVisible)
		g.EnsureVisible()
	}
}

// HandleMouse mirrors the finder's mouse contract: hover highlights,
// wheel scrolls, click activates, click outside dismisses — except on
// the scroll indicator's column, which the bar claims for itself.
func (g *gitLogOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := g.sync()
	if btn&tcell.WheelUp != 0 {
		g.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		g.ScrollBy(3)
		return
	}
	rowsStart := r.Y + 3
	// Claimed before the row hit-test, the same ordering Pick uses: a
	// press on the thumb must scroll, never open the diff of whichever
	// commit happens to sit behind it. The hit is false whenever the
	// history fits, so the column stays ordinary row surface then.
	if b := g.bar(r); b.Hit(x, y) {
		if btn&tcell.Button1 != 0 {
			g.ScrollToBar(rowsStart, y)
		}
		return
	}
	row := -1
	if x >= r.X && x < r.X+r.W {
		if i, ok := g.RowAt(rowsStart, y); ok {
			row = i
		}
	}
	if row >= 0 {
		g.Select(row)
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		g.app.closeAllModals()
		return
	}
	if row >= 0 {
		g.activate()
	}
}

// bar describes the history list's scroll indicator inside frame r —
// the frame's right padding column, over the commit rows only.
func (g *gitLogOverlay) bar(r overlay.Rect) overlay.Bar {
	return g.List.Bar(overlay.BarColumn(r), r.Y+3)
}

// WantsMotion is true: hovering a commit row moves the highlight, the
// same contract the finder's rows follow.
func (g *gitLogOverlay) WantsMotion() bool { return true }

// Dismiss is a no-op — the history overlay is read-only, so a teardown
// leaves nothing to revert.
func (g *gitLogOverlay) Dismiss() {}

// rect returns the overlay's on-screen rectangle — the same width cap
// and upper-third anchor as the finder. The height comes from the
// List's window, so sync has to have run first; every entry point and
// the opener do.
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
	h := g.Visible() + 4
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
	r := g.sync()
	overlay.DrawFrame(scr, r, g.title, g.app.theme)

	g.EnsureVisible()
	rowsStart := r.Y + 3
	for i := range g.Visible() {
		idx := g.Scroll() + i
		if idx >= len(g.entries) {
			break
		}
		g.drawRow(scr, r, rowsStart+i, g.entries[idx], idx == g.Sel())
	}
	// After the rows: drawRow fills the frame's inner width, padding
	// column included, so the bar has to land on top of it.
	g.bar(r).Draw(scr, g.app.theme)
	scr.HideCursor()
}

// drawRow paints one commit row inside the overlay.
func (g *gitLogOverlay) drawRow(scr tcell.Screen, r overlay.Rect, ry int, entry gitLogEntry, selected bool) {
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
	x = drawClipped(scr, x, ry, r.W-4, entry.SHA, shaStyle)
	// Age goes on the right; the subject gets whatever is left between.
	age := entry.Age
	ageX := r.X + r.W - 2 - runeLen(age)
	drawClipped(scr, ageX, ry, runeLen(age), age, ageStyle)
	subjectW := ageX - x - 3
	drawClipped(scr, x+2, ry, subjectW, entry.Subject, rowStyle)
}
