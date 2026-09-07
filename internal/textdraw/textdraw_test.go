// =============================================================================
// File: internal/textdraw/textdraw_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package textdraw

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// famEmoji is a ZWJ family: five runes (man, ZWJ, woman, ZWJ, boy) that
// paint as ONE two-cell cluster — the canonical "runes are not cells" case.
const famEmoji = "\U0001F468\u200D\U0001F469\u200D\U0001F466"

// simScreen builds an initialized 40×5 simulation screen, the same harness
// every drawing test in the repo uses.
func simScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init sim screen: %v", err)
	}
	scr.SetSize(40, 5)
	t.Cleanup(scr.Fini)
	return scr
}

// cellRunes returns the full rune slice at (x, y) after a Show — nil for a
// cell the drawing never touched (which is how tcell represents the
// continuation cell behind a wide glyph).
func cellRunes(scr tcell.SimulationScreen, x, y int) []rune {
	cells, w, _ := scr.GetContents()
	return cells[y*w+x].Runes
}

// TestWidth pins the cluster-aware cell counts the whole chrome relies on:
// ASCII is one cell per rune, CJK two, a combining sequence one, and a ZWJ
// family emoji two — never the five its rune count suggests.
func TestWidth(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"cjk", "日本語", 6},
		{"combining precomposed", "é", 1},
		{"combining decomposed", "é", 1},
		{"zwj family emoji", famEmoji, 2},
		{"mixed", "a日b", 4},
	}
	for _, c := range cases {
		if got := Width(c.s); got != c.want {
			t.Errorf("Width(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestClip pins the whole-cluster prefix rule: the clip never splits a
// two-cell cluster, so a budget that lands mid-ideograph comes back short.
func TestClip(t *testing.T) {
	cases := []struct {
		name  string
		s     string
		maxW  int
		want  string
		wantW int
	}{
		{"ascii fits", "hello", 5, "hello", 5},
		{"ascii clipped", "hello", 3, "hel", 3},
		{"cjk exact", "日本語", 6, "日本語", 6},
		{"cjk mid-cluster budget", "日本語", 5, "日本", 4},
		{"cjk clipped", "日本語", 4, "日本", 4},
		{"family emoji unsplit", famEmoji, 1, "", 0},
		{"zero budget", "hello", 0, "", 0},
		{"negative budget", "hello", -1, "", 0},
		{"empty", "", 10, "", 0},
	}
	for _, c := range cases {
		got, gotW := Clip(c.s, c.maxW)
		if got != c.want || gotW != c.wantW {
			t.Errorf("Clip(%s, %d) = (%q, %d), want (%q, %d)",
				c.name, c.maxW, got, gotW, c.want, c.wantW)
		}
	}
}

// TestClipEllipsis pins the truncation marker contract: untouched when the
// string fits, a one-cell … when anything was cut, and the marker itself is
// the whole answer at a one-cell budget.
func TestClipEllipsis(t *testing.T) {
	cases := []struct {
		name string
		s    string
		maxW int
		want string
	}{
		{"fits", "hello", 5, "hello"},
		{"cut", "hello", 4, "hel…"},
		{"cjk cut keeps whole clusters", "日本語", 5, "日本…"},
		{"one cell", "hello", 1, "…"},
		{"zero budget", "hello", 0, ""},
		{"empty", "", 3, ""},
	}
	for _, c := range cases {
		if got := ClipEllipsis(c.s, c.maxW); got != c.want {
			t.Errorf("ClipEllipsis(%s, %d) = %q, want %q", c.name, c.maxW, got, c.want)
		}
	}
}

// TestDrawClipped_ASCII pins the base contract on the simulation screen:
// each rune lands in its own cell, drawing stops at the budget, and the
// returned x is one past the last painted cell.
func TestDrawClipped_ASCII(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	next := DrawClipped(scr, 2, 1, 3, "hello", st)
	scr.Show()

	if next != 5 {
		t.Fatalf("DrawClipped returned %d, want 5", next)
	}
	for i, want := range "hel" {
		if got := cellRunes(scr, 2+i, 1); len(got) == 0 || got[0] != want {
			t.Errorf("cell (%d,1) = %q, want %q", 2+i, got, want)
		}
	}
	if got := cellRunes(scr, 5, 1); len(got) != 0 {
		t.Errorf("cell past the budget painted: %q", got)
	}
}

// TestDrawClipped_CJKSkipsContinuationCells pins the wide-glyph layout:
// each ideograph is one SetContent at its base cell, the continuation cell
// stays untouched (tcell paints through it), and the next cluster starts
// two cells later.
func TestDrawClipped_CJKSkipsContinuationCells(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	next := DrawClipped(scr, 0, 0, 10, "日本", st)
	scr.Show()

	if next != 4 {
		t.Fatalf("DrawClipped returned %d, want 4", next)
	}
	if got := cellRunes(scr, 0, 0); len(got) == 0 || got[0] != '日' {
		t.Fatalf("cell (0,0) = %q, want 日", got)
	}
	if got := cellRunes(scr, 1, 0); len(got) != 0 {
		t.Errorf("continuation cell (1,0) painted: %q", got)
	}
	if got := cellRunes(scr, 2, 0); len(got) == 0 || got[0] != '本' {
		t.Fatalf("cell (2,0) = %q, want 本", got)
	}
	if got := cellRunes(scr, 3, 0); len(got) != 0 {
		t.Errorf("continuation cell (3,0) painted: %q", got)
	}
}

// TestDrawClipped_WideClusterNeverStraddlesBudget pins the clip rule at a
// wide boundary: with one cell of budget left, a two-cell ideograph is
// dropped entirely rather than half-painted past the edge.
func TestDrawClipped_WideClusterNeverStraddlesBudget(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	next := DrawClipped(scr, 0, 0, 3, "日本", st)
	scr.Show()

	if next != 2 {
		t.Fatalf("DrawClipped returned %d, want 2 (本 must not straddle)", next)
	}
	if got := cellRunes(scr, 2, 0); len(got) != 0 {
		t.Errorf("cell (2,0) painted despite 本 not fitting: %q", got)
	}
}

// TestDrawClipped_CombiningEmittedAsOneCell pins the cluster emission: a
// decomposed é goes down as ONE SetContent (base rune + combining mark) in
// one cell, and the following rune lands in the very next cell.
func TestDrawClipped_CombiningEmittedAsOneCell(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	next := DrawClipped(scr, 0, 0, 10, "éx", st)
	scr.Show()

	if next != 2 {
		t.Fatalf("DrawClipped returned %d, want 2", next)
	}
	got := cellRunes(scr, 0, 0)
	if len(got) != 2 || got[0] != 'e' || got[1] != '́' {
		t.Fatalf("cell (0,0) = %q, want e+combining acute in one cell", got)
	}
	if got := cellRunes(scr, 1, 0); len(got) == 0 || got[0] != 'x' {
		t.Fatalf("cell (1,0) = %q, want x", got)
	}
}

// TestDrawClipped_FamilyEmojiIsTwoCells pins the ZWJ case: five runes, one
// cluster, two cells — the x advance must be 2, not the rune count.
func TestDrawClipped_FamilyEmojiIsTwoCells(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	next := DrawClipped(scr, 0, 0, 10, famEmoji+"x", st)
	scr.Show()

	if next != 3 {
		t.Fatalf("DrawClipped returned %d, want 3 (2-cell emoji + x)", next)
	}
	got := cellRunes(scr, 0, 0)
	if len(got) != 5 {
		t.Fatalf("cell (0,0) holds %d runes, want the whole 5-rune cluster", len(got))
	}
	if got := cellRunes(scr, 2, 0); len(got) == 0 || got[0] != 'x' {
		t.Fatalf("cell (2,0) = %q, want x right after the 2-cell emoji", got)
	}
}

// TestDrawClipped_NonPositiveBudgetIsNoop pins the guard: a zero or
// negative budget paints nothing and returns x unchanged.
func TestDrawClipped_NonPositiveBudgetIsNoop(t *testing.T) {
	scr := simScreen(t)
	st := tcell.StyleDefault
	for _, maxW := range []int{0, -3} {
		if next := DrawClipped(scr, 4, 0, maxW, "hi", st); next != 4 {
			t.Fatalf("DrawClipped(maxW=%d) returned %d, want 4", maxW, next)
		}
	}
	scr.Show()
	if got := cellRunes(scr, 4, 0); len(got) != 0 {
		t.Errorf("cell painted despite non-positive budget: %q", got)
	}
}

// TestWrapWords_ClusterWiderThanTheBudget pins the one case a wrap
// cannot honour: a two-cell cluster at maxW == 1. Clip hands back
// nothing, and the loop used to flush an empty line and retry the same
// cluster forever. Each cluster now goes out on its own line, one cell
// over budget, and the call returns.
func TestWrapWords_ClusterWiderThanTheBudget(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- WrapWords("日本 x語", 1) }()
	select {
	case got := <-done:
		want := []string{"日", "本", "x", "語"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("WrapWords = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WrapWords spun on a cluster wider than the budget")
	}
}

// TestWrapWords pins the shared wrap: a fit stays one line, breaks land
// on spaces with the space dropped, a run wider than a line breaks hard
// between clusters (never inside a two-cell glyph), an empty string is
// one empty line, and a non-positive budget hands the input back.
func TestWrapWords(t *testing.T) {
	cases := []struct {
		name string
		s    string
		maxW int
		want []string
	}{
		{"fits", "Saved", 20, []string{"Saved"}},
		{"exact", "Saved", 5, []string{"Saved"}},
		{"word break", "Saved main.go to disk", 10, []string{"Saved", "main.go to", "disk"}},
		{"hard break", "aaaaaaaaaa", 4, []string{"aaaa", "aaaa", "aa"}},
		{"hard break mid line", "ab cdefghij", 5, []string{"ab", "cdefg", "hij"}},
		{"cjk never split", "日本語です", 5, []string{"日本", "語で", "す"}},
		{"spaces at a break dropped", "one   two", 4, []string{"one", "two"}},
		{"spacing inside a line kept", "  Esc s   save", 40, []string{"  Esc s   save"}},
		{"indent kept on the first line", "    at main.go:12 in something long", 20, []string{"    at main.go:12 in", "something long"}},
		{"only spaces", "     ", 3, []string{""}},
		{"empty", "", 10, []string{""}},
		{"zero budget", "Saved", 0, []string{"Saved"}},
	}
	for _, c := range cases {
		got := WrapWords(c.s, c.maxW)
		if len(got) != len(c.want) {
			t.Errorf("WrapWords(%s) = %q, want %q", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("WrapWords(%s) line %d = %q, want %q (all %q)", c.name, i, got[i], c.want[i], got)
			}
			if c.maxW > 0 && Width(got[i]) > c.maxW {
				t.Errorf("WrapWords(%s) line %d = %q is %d cells, budget %d", c.name, i, got[i], Width(got[i]), c.maxW)
			}
		}
	}
}

// TestCell_MatchesSetContent pins that Cell is a drop-in for
// scr.SetContent on the three shapes the editor paints — plain ASCII, a
// two-cell ideograph, and a base rune with a combining mark — so the
// alloc-free path cannot drift from tcell's own cell semantics.
func TestCell_MatchesSetContent(t *testing.T) {
	cases := []struct {
		r    rune
		comb []rune
	}{
		{'a', nil},
		{' ', nil},
		{'世', nil},
		{'e', []rune{0x301}},
	}
	st := tcell.StyleDefault.Foreground(tcell.ColorRed)
	for _, c := range cases {
		want := tcell.NewSimulationScreen("UTF-8")
		got := tcell.NewSimulationScreen("UTF-8")
		for _, s := range []tcell.SimulationScreen{want, got} {
			if err := s.Init(); err != nil {
				t.Fatal(err)
			}
			s.SetSize(4, 1)
		}
		want.SetContent(1, 0, c.r, c.comb, st)
		Cell(got, 1, 0, c.r, c.comb, st)
		want.Show()
		got.Show()
		wc, _, _ := want.GetContents()
		gc, _, _ := got.GetContents()
		for i := range wc {
			if string(wc[i].Bytes) != string(gc[i].Bytes) || wc[i].Style != gc[i].Style {
				t.Fatalf("rune %q cell %d: Cell=%q/%v SetContent=%q/%v", c.r, i, gc[i].Bytes, gc[i].Style, wc[i].Bytes, wc[i].Style)
			}
		}
		want.Fini()
		got.Fini()
	}
}

// TestCell_ASCIIDoesNotAllocate is the reason Cell exists: a frame
// paints ten thousand cells, and SetContent's two allocations per cell
// were the idle frame's single largest cost.
func TestCell_ASCIIDoesNotAllocate(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(8, 2)
	st := tcell.StyleDefault
	if n := testing.AllocsPerRun(100, func() { Cell(scr, 1, 0, 'x', nil, st) }); n != 0 {
		t.Fatalf("Cell allocated %v times per ASCII call, want 0", n)
	}
	if n := testing.AllocsPerRun(100, func() { Fill(scr, 0, 0, 8, 2, st) }); n != 0 {
		t.Fatalf("Fill allocated %v times per call, want 0", n)
	}
	if n := testing.AllocsPerRun(100, func() { DrawClipped(scr, 0, 1, 8, "abc", st) }); n != 0 {
		t.Fatalf("DrawClipped allocated %v times per call, want 0", n)
	}
}

// TestFill_PaintsExactlyTheRect pins Fill's bounds: every cell inside
// gets the style, the cells around it are untouched, and a degenerate
// rect paints nothing.
func TestFill_PaintsExactlyTheRect(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(6, 4)
	st := tcell.StyleDefault.Background(tcell.ColorBlue)
	Fill(scr, 1, 1, 3, 2, st)
	Fill(scr, 0, 0, 0, 4, st)
	Fill(scr, 0, 0, 6, -1, st)
	scr.Show()
	cells, w, _ := scr.GetContents()
	for i, c := range cells {
		x, y := i%w, i/w
		inside := x >= 1 && x < 4 && y >= 1 && y < 3
		if (c.Style == st) != inside {
			t.Fatalf("cell (%d,%d) style %v, inside=%v", x, y, c.Style, inside)
		}
	}
}
