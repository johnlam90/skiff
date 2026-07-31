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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
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
	args := []string{"-C", rootDir, "log", "--format=%h%x09%s%x09%cr", "-n", itoa(limit)}
	if path != "" {
		args = append(args, "--follow", "--", path)
	}
	out, err := exec.Command("git", args...).Output()
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
func loadGitCommitDiff(rootDir, sha, path string) []string {
	if rootDir == "" || sha == "" {
		return nil
	}
	args := []string{"-C", rootDir, "show", "--format=", sha}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// openGitLog shows the history modal. path narrows it to one file
// (file history); empty path shows the branch log. An empty result
// flashes instead of opening a hollow modal.
func (a *App) openGitLog(title, path string) {
	entries := loadGitLog(a.rootDir, path, gitLogLimit)
	if len(entries) == 0 {
		a.flash("No commit history found")
		return
	}
	a.closeAllModals()
	a.gitLogOpen = true
	a.gitLogTitle = title
	a.gitLogPath = path
	a.gitLogEntries = entries
	a.gitLogSelected = 0
	a.gitLogScroll = 0
}

// closeGitLog dismisses the modal and drops its transient state.
func (a *App) closeGitLog() {
	a.gitLogOpen = false
	a.gitLogEntries = nil
	a.gitLogTitle = ""
	a.gitLogPath = ""
	a.gitLogSelected = 0
	a.gitLogScroll = 0
}

// menuCommitHistory is the ≡ menu entry for the branch log.
func (a *App) menuCommitHistory() {
	a.closeMenu()
	a.openGitLog("History · "+a.gitBranch, "")
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
	return tab != nil && tab.Path != "" && !tab.IsImage() && a.gitBranch != ""
}

// activateGitLogRow opens the selected commit's diff in the diff view.
// For file history the diff is limited to that file and keeps its
// syntax highlighting; branch commits show every file with boundary
// rows and skip syntax colors (one lexer can't serve many languages).
func (a *App) activateGitLogRow() {
	if a.gitLogSelected < 0 || a.gitLogSelected >= len(a.gitLogEntries) {
		return
	}
	entry := a.gitLogEntries[a.gitLogSelected]
	lines := loadGitCommitDiff(a.rootDir, entry.SHA, a.gitLogPath)
	if len(lines) == 0 {
		// Merge commits and rename-only commits can produce no diff
		// for this path — say so rather than opening an empty modal.
		a.flash("No diff for commit " + entry.SHA)
		return
	}
	title := "Commit " + entry.SHA
	langPath := ""
	if a.gitLogPath != "" {
		title += " · " + filepath.Base(a.gitLogPath)
		langPath = a.gitLogPath
	}
	// openDiffView closes this modal via closeAllModals.
	a.openDiffView(title, lines, "", langPath)
}

// handleGitLogKey routes keyboard input while the modal is open.
func (a *App) handleGitLogKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeGitLog()
	case tcell.KeyEnter:
		a.activateGitLogRow()
	case tcell.KeyUp:
		if a.gitLogSelected > 0 {
			a.gitLogSelected--
		}
	case tcell.KeyDown:
		if a.gitLogSelected < len(a.gitLogEntries)-1 {
			a.gitLogSelected++
		}
	case tcell.KeyPgUp:
		a.gitLogSelected -= gitLogVisible
		if a.gitLogSelected < 0 {
			a.gitLogSelected = 0
		}
	case tcell.KeyPgDn:
		a.gitLogSelected += gitLogVisible
		if a.gitLogSelected > len(a.gitLogEntries)-1 {
			a.gitLogSelected = len(a.gitLogEntries) - 1
		}
	}
}

// handleGitLogMouse mirrors the finder's mouse contract: hover
// highlights, wheel scrolls, click activates, click outside dismisses.
func (a *App) handleGitLogMouse(x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		a.scrollGitLog(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		a.scrollGitLog(3)
		return
	}
	mx, my, mw, mh := a.gitLogModalRect()
	rowsStart := my + 3
	row := a.gitLogScroll + (y - rowsStart)
	inRows := y >= rowsStart && y < my+mh-1 && x >= mx && x < mx+mw
	if inRows && row >= 0 && row < len(a.gitLogEntries) {
		a.gitLogSelected = row
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeGitLog()
		return
	}
	if inRows && row >= 0 && row < len(a.gitLogEntries) {
		a.gitLogSelected = row
		a.activateGitLogRow()
	}
}

// scrollGitLog nudges the visible window by delta rows, clamped to the
// list's ends.
func (a *App) scrollGitLog(delta int) {
	max := len(a.gitLogEntries) - a.gitLogVisibleRows()
	if max < 0 {
		max = 0
	}
	a.gitLogScroll += delta
	if a.gitLogScroll > max {
		a.gitLogScroll = max
	}
	if a.gitLogScroll < 0 {
		a.gitLogScroll = 0
	}
}

// gitLogVisibleRows returns how many commit rows the modal shows at
// once, shrunk on tiny terminals.
func (a *App) gitLogVisibleRows() int {
	rows := len(a.gitLogEntries)
	if rows > gitLogVisible {
		rows = gitLogVisible
	}
	if rows > a.height-6 {
		rows = a.height - 6
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// gitLogModalRect returns the modal's on-screen rectangle — the same
// width cap and upper-third anchor as the finder.
func (a *App) gitLogModalRect() (x, y, w, h int) {
	w = gitLogModalMaxWidth
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 30 {
		w = 30
	}
	// 1 border + 1 title + 1 divider + N rows + 1 border.
	h = a.gitLogVisibleRows() + 4
	x = (a.width - w) / 2
	y = (a.height - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// drawGitLog paints the modal: title + esc hint, then one row per
// commit — SHA in the soft accent, subject in the text color, age
// right-aligned and muted, selection flipping the row background.
//
// Layout (relY):
//
//	0     top border
//	1     title — " History · main                  esc"
//	2     divider
//	3..N  commit rows
//	N+1   bottom border
func (a *App) drawGitLog() {
	mx, my, mw, mh := a.gitLogModalRect()
	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)

	fillRect(a.screen, mx, my, mw, mh, bgStyle)
	drawBorder(a.screen, mx, my, mw, mh, borderStyle)
	drawHDivider(a.screen, mx, my+2, mw, borderStyle)

	drawAt(a.screen, mx+1, my+1, " "+a.gitLogTitle, titleStyle)
	hint := "esc "
	drawAt(a.screen, mx+mw-1-runeLen(hint), my+1, hint, mutedStyle)

	visCap := a.gitLogVisibleRows()
	a.adjustGitLogScroll(visCap)
	rowsStart := my + 3
	for i := 0; i < visCap; i++ {
		idx := a.gitLogScroll + i
		if idx >= len(a.gitLogEntries) {
			break
		}
		a.drawGitLogRow(mx, rowsStart+i, mw, a.gitLogEntries[idx], idx == a.gitLogSelected, bg)
	}
	a.screen.HideCursor()
}

// drawGitLogRow paints one commit row inside the modal.
func (a *App) drawGitLogRow(mx, ry, mw int, entry gitLogEntry, selected bool, modalBG tcell.Color) {
	rowBG := modalBG
	if selected {
		rowBG = a.theme.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Text)
	shaStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.AccentSoft).Bold(true)
	ageStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Muted)

	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	x := mx + 2
	x = drawClipped(a.screen, x, ry, mw-4, entry.SHA, shaStyle)
	// Age goes on the right; the subject gets whatever is left between.
	age := entry.Age
	ageX := mx + mw - 2 - runeLen(age)
	drawClipped(a.screen, ageX, ry, runeLen(age), age, ageStyle)
	subjectW := ageX - x - 3
	drawClipped(a.screen, x+2, ry, subjectW, entry.Subject, rowStyle)
}

// adjustGitLogScroll keeps the selected row inside the visible window,
// sliding the window when arrow keys walk past its edges.
func (a *App) adjustGitLogScroll(visCap int) {
	if a.gitLogSelected < a.gitLogScroll {
		a.gitLogScroll = a.gitLogSelected
	}
	if a.gitLogSelected-a.gitLogScroll >= visCap {
		a.gitLogScroll = a.gitLogSelected - visCap + 1
	}
	if a.gitLogScroll < 0 {
		a.gitLogScroll = 0
	}
}
