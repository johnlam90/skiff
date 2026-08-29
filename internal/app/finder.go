// =============================================================================
// File: internal/app/finder.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

// Project-wide file finder UI — a centered overlay with a search input
// on top and ~10 result rows below. Type to filter, ↑/↓ to navigate,
// Enter to open, Esc to dismiss. Mouse hover highlights; click opens.
//
// All the index/scoring lifting lives in internal/finder; finderOverlay
// is a bespoke overlay (it needs the App for the index, file opening,
// and theme) implementing the overlay contract directly, with its own
// state and the shared Field/chrome primitives.

import (
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/finder"
	"github.com/johnlam90/skiff/internal/overlay"
)

const (
	// finderModalMaxWidth caps the modal so very wide terminals
	// don't get a full-width strip that's awkward to scan. 80 is
	// the comfortable reading width every IDE settles on.
	finderModalMaxWidth = 80
	// finderResultsVisible is how many result rows we render at
	// once. 10 is the floor for "feels useful" without dominating
	// the screen on small terminals.
	finderResultsVisible = 10
	// finderSearchLimit is how deep we look — we ask for one full
	// "page" extra so users can scroll past the initial chunk.
	finderSearchLimit = 50
	// finderChromeRows is the non-list height of the modal: the two
	// borders, the title, the divider and the query input. Subtracting
	// it from the frame is what turns a terminal height into a window
	// height, and it lives next to finderResultsVisible so the cap and
	// the derivation cannot drift.
	finderChromeRows = 5
)

// finderRebuiltEvent is posted by the background indexer goroutine
// when it finishes a rebuild. The main loop reacts by re-running
// the current query so the visible results refresh — the user
// gets to see "Indexing…" replaced with real matches without
// having to type or wait for the next keystroke.
type finderRebuiltEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *finderRebuiltEvent) When() time.Time { return e.when }

// finderOverlay is the file finder's overlay: the query field, the
// current results, and the window over them. It dies with the overlay —
// only the index cache (a.finder) outlives a session.
//
// The window is an overlay.List, which is what makes every one of the
// finderSearchLimit matches reachable. It used to have no scroll state
// at all: fifty results were fetched, ten were painted, and the other
// forty could be selected with ↓ and never once appear on screen.
type finderOverlay struct {
	overlay.List
	app     *App
	query   overlay.Field
	results []finder.Result
}

// sync pushes the live result count and the window the frame can paint
// into the embedded List. Both move underneath the overlay — the count
// on every keystroke, the window on every resize — so every entry point
// calls this before reading the List, and hands the frame on.
func (fo *finderOverlay) sync() overlay.Rect {
	r := fo.rect()
	rows := r.H - finderChromeRows
	if rows > finderResultsVisible {
		rows = finderResultsVisible
	}
	if rows < 1 {
		rows = 1
	}
	fo.SetLen(len(fo.results))
	fo.SetVisible(rows)
	return r
}

// openFinder shows the project-wide file finder. Triggers a
// background rebuild on every open so external file changes are
// reflected even if the periodic invalidation tick hasn't fired.
// (Building when the index is already StateReady is a no-op
// inside the orchestrator's coalesce gate.)
func (a *App) openFinder() {
	// Single-file mode has no project index — the menu row is already
	// hidden (hasTree), but the Esc-p leader reaches here directly, so
	// guard it too rather than pop an always-empty modal. tree == nil is
	// the single-file signal (see NewSingleFile); the finder is a
	// project-scoped feature that's omitted alongside the tree.
	if a.tree == nil {
		a.flash("Find file isn't available in single-file mode")
		return
	}
	a.closeAllModals()
	fo := &finderOverlay{app: a}
	scr := a.screen
	if a.finder != nil {
		// Rebuild only when the cache is genuinely stale. A re-open
		// during a still-warm session shouldn't pay for a refresh.
		if a.finder.State() != finder.StateReady {
			a.finder.Rebuild(func() {
				_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
			})
		}
	}
	fo.refreshResults()
	a.overlays.Open(fo)
}

// menuFindFile is the ≡ menu entry that opens the finder. Lives
// alongside menuFind (which is the in-file find bar) — they share
// vocabulary but search different scopes.
func (a *App) menuFindFile() {
	a.closeMenu()
	a.openFinder()
}

// hasFinder is the menu predicate. Always true once the finder
// has been wired in App.New — the row stays enabled even before
// the first index lands so the user can pop the modal and watch
// "Indexing…" tick over.
func (a *App) hasFinder() bool {
	return a.finder != nil
}

// refreshResults re-runs the cached query against the current index.
// Called on every keystroke and on the rebuilt event so a slow first
// index doesn't leave the modal showing stale "Indexing…" forever.
func (fo *finderOverlay) refreshResults() {
	if fo.app.finder == nil {
		fo.results = nil
		fo.SetLen(0)
		fo.Clamp()
		return
	}
	fo.results = fo.app.finder.Search(fo.query.Text(), finderSearchLimit)
	fo.sync()
	fo.Clamp()
	fo.EnsureVisible()
}

// invalidateFinder marks the index stale and kicks off a rebuild.
// Called by every fileops mutation (create / rename / delete / paste /
// undo-delete), by the git-op and custom-action landing paths (their
// changes can land in directories the tree never loaded), and by the
// refresh tick — but the tick only when its sweep actually changed the
// tree's membership; see handleTreeScan. Keeping the callers explicit
// is what lets the quiet tick skip the reindex without any of them
// going stale.
func (a *App) invalidateFinder() {
	if a.finder == nil {
		return
	}
	a.finder.Invalidate()
	scr := a.screen
	a.finder.Rebuild(func() {
		_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
}

// HandleKey routes keyboard input while the finder is open: Esc
// dismisses, Enter opens the highlighted result, ↑/↓ move the
// highlight — dragging the window with them once they walk past its
// edge — and everything else edits the query field, an edit that
// re-runs the search with the highlight reset.
func (fo *finderOverlay) HandleKey(ev *tcell.EventKey) {
	a := fo.app
	fo.sync()
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeAllModals()
		return
	case tcell.KeyEnter:
		fo.openSelected()
		return
	case tcell.KeyUp:
		fo.Move(-1)
		fo.EnsureVisible()
		return
	case tcell.KeyDown:
		fo.Move(1)
		fo.EnsureVisible()
		return
	}
	before := len(fo.query.Value)
	fo.query.HandleKey(ev)
	if len(fo.query.Value) != before {
		fo.Select(0)
		fo.refreshResults()
	}
}

// HandleMouse handles mouse input while the overlay is open. The wheel
// scrolls the result window, hover highlights the row under the cursor,
// click opens it, and a click outside the modal dismisses.
func (fo *finderOverlay) HandleMouse(x, y int, btn tcell.ButtonMask) {
	r := fo.sync()
	if btn&tcell.WheelUp != 0 {
		fo.ScrollBy(-3)
		return
	}
	if btn&tcell.WheelDown != 0 {
		fo.ScrollBy(3)
		return
	}
	idx := -1
	if x >= r.X && x < r.X+r.W {
		if i, ok := fo.RowAt(r.Y+4, y); ok {
			idx = i
		}
	}
	if idx >= 0 {
		// Hover highlight always tracks the mouse — same behaviour
		// the action menu uses, so users can scrub through results
		// without clicking.
		fo.Select(idx)
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if !r.Contains(x, y) {
		fo.app.closeAllModals()
		return
	}
	if idx >= 0 {
		fo.openSelected()
	}
}

// openSelected opens the highlighted result: closes the overlay, then
// resolves the relative path against the project root and opens the
// file. Silent no-op when the result list is empty (e.g. the user
// mashed Enter on a no-match query).
func (fo *finderOverlay) openSelected() {
	if fo.Sel() < 0 || fo.Sel() >= len(fo.results) {
		return
	}
	a := fo.app
	rel := fo.results[fo.Sel()].Path
	a.closeAllModals()
	a.openFile(filepath.Join(a.rootDir, rel))
}

// WantsMotion is true: the finder highlights the result row under the
// pointer so a mouse user can scrub the list without clicking.
func (fo *finderOverlay) WantsMotion() bool { return true }

// Dismiss is a no-op — a finder torn down by the stack opens nothing,
// and it holds no state the rest of the app can see.
func (fo *finderOverlay) Dismiss() {}

// rect returns the finder's on-screen rectangle. Sized to fit a healthy
// result list while leaving margins on small terminals; anchored in the
// upper third — matches VS Code's feel.
func (fo *finderOverlay) rect() overlay.Rect {
	a := fo.app
	w := finderModalMaxWidth
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 30 {
		w = 30
	}
	// Layout: 1 border + 1 title + 1 divider + 1 input + N results
	// + 1 status + 1 border = N+6 rows.
	h := finderResultsVisible + 6
	if h > a.height-2 {
		h = a.height - 2
	}
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

// Draw paints the overlay: shared frame chrome, the query field with a
// match-count tail, then either an "indexing…" tail or the result rows
// with highlighted match characters.
//
// Layout (relY): 0 border · 1 title · 2 divider · 3 input+count ·
// 4..N result rows · N+1 border.
func (fo *finderOverlay) Draw(scr tcell.Screen) {
	a := fo.app
	r := fo.sync()
	th := a.theme
	overlay.DrawFrame(scr, r, "Find file", th)

	bg := th.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted)
	hitStyle := tcell.StyleDefault.Background(bg).Foreground(th.FindCurrent).Bold(true)

	// Input row — the field stops short of the right edge to leave room
	// for the count tail.
	inputStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	fieldStart := r.X + 3
	fieldWidth := (r.X + r.W - 12) - fieldStart
	fo.query.Draw(scr, fieldStart, r.Y+3, fieldWidth, inputStyle, true)

	// Match count tail.
	state, total, viaGit := finder.StateIdle, 0, false
	if a.finder != nil {
		state, total, viaGit = a.finder.Stats()
	}
	_ = viaGit // reserved for a future "via git" badge — keeping the
	// triple-return so callers don't have to plumb a new method later.
	tail := ""
	switch state {
	case finder.StateBuilding, finder.StateIdle:
		tail = "indexing… "
	case finder.StateErrored:
		tail = "index err "
	case finder.StateReady:
		tail = countLabel(len(fo.results), total) + " "
	}
	drawAt(scr, r.X+r.W-1-runeLen(tail), r.Y+3, tail, mutedStyle)

	// Result rows: the List's window, not the head of the list. Rows
	// past the end are blanked so a previous query's tail doesn't
	// linger when the results shrink.
	rowsStart := r.Y + 4
	for i := range fo.Visible() {
		ry := rowsStart + i
		idx := fo.Scroll() + i
		if idx >= len(fo.results) {
			for cx := r.X + 1; cx < r.X+r.W-1; cx++ {
				scr.SetContent(cx, ry, ' ', nil, bgStyle)
			}
			continue
		}
		fo.drawRow(scr, r, ry, fo.results[idx], idx == fo.Sel(), hitStyle, mutedStyle, bg)
	}
}

// drawRow paints one result line: the path, with matched runes
// highlighted via hitStyle, and the row background flipped when it's
// the selected row. The dirname is dimmed so the basename pops — same
// trick the editor's tab bar uses to make the file name the visual
// anchor.
func (fo *finderOverlay) drawRow(scr tcell.Screen, r overlay.Rect, ry int, res finder.Result, selected bool, hitStyle, mutedStyle tcell.Style, modalBG tcell.Color) {
	th := fo.app.theme
	rowBG := modalBG
	if selected {
		rowBG = th.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(th.Text)
	hitOnRow := hitStyle.Background(rowBG)
	mutedOnRow := mutedStyle.Background(rowBG)

	// Background fill so the selected row reads as a single block.
	for cx := r.X + 1; cx < r.X+r.W-1; cx++ {
		scr.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	pathRunes := []rune(res.Path)
	// Find the basename split so we can dim the directory part.
	sepIdx := -1
	for i := len(pathRunes) - 1; i >= 0; i-- {
		if pathRunes[i] == '/' {
			sepIdx = i
			break
		}
	}
	matchSet := map[int]bool{}
	for _, m := range res.MatchedIndexes {
		matchSet[m] = true
	}

	startCol := r.X + 2
	maxCols := r.W - 4
	for i, ch := range pathRunes {
		if i >= maxCols {
			break
		}
		st := rowStyle
		if i <= sepIdx {
			st = mutedOnRow
		}
		if matchSet[i] {
			st = hitOnRow
		}
		scr.SetContent(startCol+i, ry, ch, nil, st)
	}
}

// countLabel formats the "shown/total" tail. Pulled into its own
// helper so future tweaks (commas, abbreviation past 1k) live in
// one spot. We don't bother showing "shown" when it equals total
// — the count would be redundant and the eye should focus on the
// total.
func countLabel(shown, total int) string {
	if total == 0 {
		return "0"
	}
	if shown == total {
		return itoa(total)
	}
	return itoa(shown) + "/" + itoa(total)
}

// itoa is a tiny non-allocating-ish int→string for the count
// tail. strconv would work fine; keeping a local helper avoids
// pulling strconv into a UI file that otherwise doesn't need it.
func itoa(n int) string {
	if n < 0 {
		return "0"
	}
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
