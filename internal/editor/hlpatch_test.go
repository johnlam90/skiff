// =============================================================================
// File: internal/editor/hlpatch_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// styleRow builds a row where every rune carries the same style, so a
// test can tell "kept" from "filled" by colour alone.
func styleRow(n int, st tcell.Style) []tcell.Style {
	row := make([]tcell.Style, n)
	for i := range row {
		row[i] = st
	}
	return row
}

var (
	stA = tcell.StyleDefault.Foreground(tcell.ColorRed)
	stB = tcell.StyleDefault.Foreground(tcell.ColorBlue)
	stC = tcell.StyleDefault.Foreground(tcell.ColorGreen)
)

// TestPatchStyles_InsertRuneKeepsNeighbours pins the common keystroke:
// a rune typed mid-line takes the style of the rune before it, and the
// text either side keeps its own colours.
func TestPatchStyles_InsertRuneKeepsNeighbours(t *testing.T) {
	old := []string{"aa", "bbcc", "dd"}
	styles := [][]tcell.Style{styleRow(2, stA), append(styleRow(2, stB), styleRow(2, stC)...), styleRow(2, stA)}
	cur := []string{"aa", "bbXcc", "dd"}
	got, winEnd, ok := patchStyles(styles, 0, 3, old, cur)
	if !ok || winEnd != 3 {
		t.Fatalf("ok=%v winEnd=%d", ok, winEnd)
	}
	want := []tcell.Style{stB, stB, stB, stC, stC}
	for j, st := range want {
		if got[1][j] != st {
			t.Fatalf("rune %d: got %v want %v", j, got[1][j], st)
		}
	}
	if len(got[0]) != 2 || len(got[2]) != 2 {
		t.Fatal("untouched rows changed length")
	}
}

// TestPatchStyles_DeleteRunes pins that removing runes drops exactly
// their styles and the remainder stays aligned to the text.
func TestPatchStyles_DeleteRunes(t *testing.T) {
	old := []string{"bbcc"}
	styles := [][]tcell.Style{append(styleRow(2, stB), styleRow(2, stC)...)}
	got, _, ok := patchStyles(styles, 0, 1, old, []string{"bc"})
	if !ok {
		t.Fatal("refused")
	}
	if len(got[0]) != 2 || got[0][0] != stB || got[0][1] != stC {
		t.Fatalf("got %v", got[0])
	}
}

// TestPatchStyles_EnterSplitsRow pins a newline: the head keeps its
// styles, the new tail row is a fresh row of the head's last style,
// every row below shifts down by one, and the window's end grows.
func TestPatchStyles_EnterSplitsRow(t *testing.T) {
	old := []string{"aa", "bbcc", "dd"}
	styles := [][]tcell.Style{styleRow(2, stA), append(styleRow(2, stB), styleRow(2, stC)...), styleRow(2, stA)}
	cur := []string{"aa", "bb", "cc", "dd"}
	got, winEnd, ok := patchStyles(styles, 0, 3, old, cur)
	if !ok || winEnd != 4 || len(got) != 4 {
		t.Fatalf("ok=%v winEnd=%d rows=%d", ok, winEnd, len(got))
	}
	if got[1][0] != stB || got[1][1] != stB || len(got[1]) != 2 {
		t.Fatalf("head row %v", got[1])
	}
	if len(got[2]) != 2 || got[2][0] != stB {
		t.Fatalf("tail row should be filled with the head's last style, got %v", got[2])
	}
	if got[3][0] != stA {
		t.Fatal("row below the split did not shift")
	}
}

// TestPatchStyles_DeleteLineShiftsRows pins a whole-line delete: rows
// below move up and the window shrinks by one.
func TestPatchStyles_DeleteLineShiftsRows(t *testing.T) {
	old := []string{"aa", "bb", "cc"}
	styles := [][]tcell.Style{styleRow(2, stA), styleRow(2, stB), styleRow(2, stC)}
	got, winEnd, ok := patchStyles(styles, 0, 3, old, []string{"aa", "cc"})
	if !ok || winEnd != 2 || len(got) != 2 {
		t.Fatalf("ok=%v winEnd=%d rows=%d", ok, winEnd, len(got))
	}
	if got[1][0] != stC {
		t.Fatal("row below the deleted line did not shift up")
	}
}

// TestPatchStyles_RefusesOutsideWindow pins the fallback: an edit that
// touches rows the window never styled cannot be rebased and must be
// left to the synchronous re-lex.
func TestPatchStyles_RefusesOutsideWindow(t *testing.T) {
	old := []string{"aa", "bb", "cc", "dd"}
	styles := [][]tcell.Style{nil, styleRow(2, stA), styleRow(2, stB), nil}
	if _, _, ok := patchStyles(styles, 1, 3, old, []string{"aa", "bb", "cc", "dX"}); ok {
		t.Fatal("an edit below the window was rebased")
	}
	if _, _, ok := patchStyles(styles, 1, 3, old, []string{"aX", "bb", "cc", "dd"}); ok {
		t.Fatal("an edit above the window was rebased")
	}
	if _, _, ok := patchStyles(styles[:3], 1, 3, old, old); ok {
		t.Fatal("a grid shorter than the buffer was rebased")
	}
}

// TestPatchStyles_CappedRowGoesPlain pins the long-line guard: a row
// whose style count does not match its runes (capLongLines emptied it)
// comes back nil rather than misaligned.
func TestPatchStyles_CappedRowGoesPlain(t *testing.T) {
	old := []string{"abcd"}
	got, _, ok := patchStyles([][]tcell.Style{{}}, 0, 1, old, []string{"abXcd"})
	if !ok || got[0] != nil {
		t.Fatalf("ok=%v row=%v", ok, got[0])
	}
}

// TestEdit_RebasesGridAndRequestsRelex walks the whole seam on a real
// tab: after a synchronous first lex, an edit leaves the grid pending,
// exactly one request goes out per generation, a stale landing is
// refused, and the current one installs an exact grid.
func TestEdit_RebasesGridAndRequestsRelex(t *testing.T) {
	th := theme.Default()
	tab, _ := NewTab("")
	tab.Path = "main.go"
	tab.Buffer = NewBuffer("package main\n\n// a comment\nfunc main() {\n}\n")
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()
	tab.Render(scr, th, 0, 0, 40, 10)
	if tab.HighlightPending() {
		t.Fatal("pending after a synchronous lex")
	}
	if _, ok := tab.HighlightRequest(10); ok {
		t.Fatal("an exact grid asked for a re-lex")
	}

	tab.Cursor = Position{Line: 2, Col: 3}
	tab.Anchor = tab.Cursor
	tab.InsertRune('X')
	if !tab.HighlightPending() {
		t.Fatal("edit inside the window should rebase, not invalidate")
	}
	if tab.Styles[2][3] != tab.Styles[2][2] {
		t.Fatal("the typed rune should borrow the comment style beside it")
	}
	req, ok := tab.HighlightRequest(10)
	if !ok {
		t.Fatal("no request after the edit")
	}
	if _, again := tab.HighlightRequest(10); again {
		t.Fatal("the same generation was requested twice")
	}
	if !strings.Contains(req.Src, "// Xa comment") {
		t.Fatalf("request text is not the edited buffer: %q", req.Src)
	}

	stale := req.Run(th)
	tab.InsertRune('Y')
	if tab.ApplyHighlight(stale) {
		t.Fatal("a landing from before the second edit was applied")
	}
	req2, ok := tab.HighlightRequest(10)
	if !ok {
		t.Fatal("the second edit should request again")
	}
	if !tab.ApplyHighlight(req2.Run(th)) {
		t.Fatal("the current generation's landing was refused")
	}
	if tab.HighlightPending() {
		t.Fatal("still pending after the landing")
	}
	want, _, _ := HighlightWindow("main.go", tab.Buffer.Lines, 0, 10, th)
	for i := range want {
		for j := range want[i] {
			if want[i][j] != tab.Styles[i][j] {
				t.Fatalf("line %d rune %d differs from a synchronous lex", i, j)
			}
		}
	}
}

// TestRender_SyncLexRetiresInFlightRequest pins the race the generation
// alone cannot settle: a scroll past the window's edge re-lexes
// synchronously while a request is out, and that exact grid must win
// over the older landing.
func TestRender_SyncLexRetiresInFlightRequest(t *testing.T) {
	th := theme.Default()
	tab, _ := NewTab("")
	tab.Path = "main.go"
	tab.Buffer = NewBuffer(strings.Repeat("var x = 1\n", 2000))
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()
	tab.Render(scr, th, 0, 0, 40, 10)
	tab.InsertRune('/')
	req, ok := tab.HighlightRequest(10)
	if !ok {
		t.Fatal("no request")
	}
	// One frame with the patched grid consumes the edit's cursorMoved,
	// so the wheel-style scroll below is not yanked back to the caret.
	tab.Render(scr, th, 0, 0, 40, 10)
	if !tab.HighlightPending() {
		t.Fatal("a frame inside the window should keep the patched grid")
	}
	tab.ScrollY = 1500
	tab.Render(scr, th, 0, 0, 40, 10)
	if tab.HighlightPending() {
		t.Fatal("a synchronous lex should leave nothing pending")
	}
	if tab.ApplyHighlight(req.Run(th)) {
		t.Fatal("the older landing replaced the exact grid")
	}
}
