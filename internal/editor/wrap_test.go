// =============================================================================
// File: internal/editor/wrap_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for soft wrap: the pure segment math (WrapSegments, WrapRowOfCol),
// the anchor walks that replace line arithmetic in wrap mode, and the
// wrap-mode Render / HitTest / scroll behavior on a SimulationScreen.

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// wrapTestTab builds an in-memory tab with Wrap enabled around the given
// content, bypassing disk I/O.
func wrapTestTab(t *testing.T, content string) *Tab {
	t.Helper()
	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer(content)
	tab.Wrap = true
	return tab
}

// simScreenRows reads every row of a SimulationScreen back as strings,
// one rune per cell, for layout assertions.
func simScreenRows(scr tcell.SimulationScreen) []string {
	cells, w, h := scr.GetContents()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		rows[y] = b.String()
	}
	return rows
}

// TestWrapSegments_FitsInWidth pins the trivial cases: a line that fits,
// an empty line, and a non-positive width all produce a single segment,
// so wrap mode can always assume at least one row per line.
func TestWrapSegments_FitsInWidth(t *testing.T) {
	if got := WrapSegments([]rune("short"), 10); len(got) != 1 || got[0] != 0 {
		t.Fatalf("short line segs = %v, want [0]", got)
	}
	if got := WrapSegments(nil, 10); len(got) != 1 || got[0] != 0 {
		t.Fatalf("empty line segs = %v, want [0]", got)
	}
	if got := WrapSegments([]rune("anything at all"), 0); len(got) != 1 || got[0] != 0 {
		t.Fatalf("zero width segs = %v, want [0]", got)
	}
}

// TestWrapSegments_WordBoundary verifies the VS Code-style break rule: a
// word that would straddle the edge moves down whole, and the wrapping
// space stays at the end of the earlier row.
func TestWrapSegments_WordBoundary(t *testing.T) {
	runes := []rune("alpha beta gamma")
	got := WrapSegments(runes, 9)
	want := []int{0, 6, 11}
	if !equalInts(got, want) {
		t.Fatalf("segs = %v, want %v", got, want)
	}
	if s := string(runes[got[1]:got[2]]); s != "beta " {
		t.Fatalf("middle row = %q, want %q", s, "beta ")
	}
}

// TestWrapSegments_HardBreakLongWord pins the fallback: a single word
// wider than the pane hard-breaks at the width instead of looping or
// overflowing.
func TestWrapSegments_HardBreakLongWord(t *testing.T) {
	got := WrapSegments([]rune("abcdefghijkl"), 4)
	want := []int{0, 4, 8}
	if !equalInts(got, want) {
		t.Fatalf("segs = %v, want %v", got, want)
	}
}

// TestWrapSegments_WhitespaceHangs pins the rule that whitespace never
// starts a continuation row: the space that overflows the width hangs on
// the earlier row (painting clips it) and the next row starts at the word.
func TestWrapSegments_WhitespaceHangs(t *testing.T) {
	got := WrapSegments([]rune("abcd efgh"), 4)
	want := []int{0, 5}
	if !equalInts(got, want) {
		t.Fatalf("segs = %v, want %v", got, want)
	}
	// A run of spaces hangs entirely — continuation still starts non-blank.
	got = WrapSegments([]rune("ab   cdef"), 4)
	want = []int{0, 5}
	if !equalInts(got, want) {
		t.Fatalf("multi-space segs = %v, want %v", got, want)
	}
}

// TestWrapSegments_TabsBreak covers tabs at the edge: a tab is whitespace,
// so it hangs past the width and the break lands before the next word.
func TestWrapSegments_TabsBreak(t *testing.T) {
	got := WrapSegments([]rune("abcde\tfg"), 6)
	want := []int{0, 6}
	if !equalInts(got, want) {
		t.Fatalf("segs = %v, want %v", got, want)
	}
}

// TestWrapSegments_Progress guards the no-infinite-loop invariant: even at
// width 1 (narrower than a tab stop) every segment advances by at least
// one rune.
func TestWrapSegments_Progress(t *testing.T) {
	got := WrapSegments([]rune("abc"), 1)
	want := []int{0, 1, 2}
	if !equalInts(got, want) {
		t.Fatalf("width-1 segs = %v, want %v", got, want)
	}
	// Whitespace-only lines collapse to one hanging row.
	if got := WrapSegments([]rune("\t\t\t"), 1); len(got) != 1 {
		t.Fatalf("tab-only segs = %v, want single segment", got)
	}
}

// TestWrapSegments_BreaksBeforeWideGlyph is the wide-character version of
// the width budget: an ideograph needs two cells, so one that would
// straddle the right edge moves down whole. Measuring it as one cell puts
// half a glyph on each row, which no terminal can draw and which shifts
// every later cell on the row by one.
func TestWrapSegments_BreaksBeforeWideGlyph(t *testing.T) {
	// Five cells of pane: "ab" then 日 fills it exactly, 本 must move down.
	got := WrapSegments([]rune("ab日本"), 5)
	want := []int{0, 3}
	if !equalInts(got, want) {
		t.Fatalf("segs = %v, want %v", got, want)
	}
	// An odd pane leaves one cell spare on each row rather than slicing.
	got = WrapSegments([]rune("日本語"), 3)
	want = []int{0, 1, 2}
	if !equalInts(got, want) {
		t.Fatalf("odd-width segs = %v, want %v", got, want)
	}
}

// TestWrapSegments_KeepsClustersWhole checks the other half of the same
// rule: a break index is always a cluster boundary, so a continuation row
// can never open with a combining mark orphaned from its base.
func TestWrapSegments_KeepsClustersWhole(t *testing.T) {
	runes := []rune("ae\u0301e\u0301e\u0301e\u0301") // 'a' then four é clusters
	for width := 1; width <= 6; width++ {
		for _, start := range WrapSegments(runes, width) {
			if got := ClusterStart(runes, start); got != start {
				t.Errorf("width %d: segment starts at %d, inside the cluster at %d", width, start, got)
			}
		}
	}
}

// TestWrapRowOfCol pins the boundary rule: a column at a segment start
// belongs to that (later) row, and end-of-line belongs to the last row.
func TestWrapRowOfCol(t *testing.T) {
	segs := []int{0, 6, 11}
	cases := []struct{ col, want int }{
		{0, 0}, {5, 0}, {6, 1}, {10, 1}, {11, 2}, {16, 2},
	}
	for _, c := range cases {
		if got := WrapRowOfCol(segs, c.col); got != c.want {
			t.Errorf("WrapRowOfCol(%d) = %d, want %d", c.col, got, c.want)
		}
	}
}

// TestAnchorWalks covers advanceAnchor / retreatAnchor / rowsBetween on a
// buffer whose first line wraps to three rows — the walks are the wrap
// replacement for ScrollY arithmetic, so their edge clamps (EOF, top of
// file) must hold.
func TestAnchorWalks(t *testing.T) {
	tab := wrapTestTab(t, "abcdefghijkl\nx\nyy")
	const w = 5 // line 0 wraps [0,5,10]

	if l, s := tab.advanceAnchor(0, 0, 1, w); l != 0 || s != 1 {
		t.Fatalf("advance 1 = (%d,%d), want (0,1)", l, s)
	}
	if l, s := tab.advanceAnchor(0, 0, 3, w); l != 1 || s != 0 {
		t.Fatalf("advance 3 = (%d,%d), want (1,0)", l, s)
	}
	if l, s := tab.advanceAnchor(0, 1, 3, w); l != 2 || s != 0 {
		t.Fatalf("advance from (0,1) by 3 = (%d,%d), want (2,0)", l, s)
	}
	if l, s := tab.advanceAnchor(0, 0, 99, w); l != 2 || s != 0 {
		t.Fatalf("advance past EOF = (%d,%d), want (2,0)", l, s)
	}
	if l, s := tab.retreatAnchor(2, 0, 1, w); l != 1 || s != 0 {
		t.Fatalf("retreat 1 = (%d,%d), want (1,0)", l, s)
	}
	if l, s := tab.retreatAnchor(1, 0, 1, w); l != 0 || s != 2 {
		t.Fatalf("retreat onto wrapped line = (%d,%d), want (0,2)", l, s)
	}
	if l, s := tab.retreatAnchor(0, 2, 5, w); l != 0 || s != 0 {
		t.Fatalf("retreat past top = (%d,%d), want (0,0)", l, s)
	}
	if got := tab.rowsBetween(0, 0, 2, 0, w, 99); got != 4 {
		t.Fatalf("rowsBetween top→last = %d, want 4", got)
	}
	if got := tab.rowsBetween(0, 1, 0, 2, w, 99); got != 1 {
		t.Fatalf("rowsBetween same line = %d, want 1", got)
	}
}

// TestSetWrap pins the mode-switch bookkeeping: enabling wrap clears the
// horizontal pan, disabling clears the segment anchor, and both mark the
// cursor moved so the next render re-ensures visibility.
func TestSetWrap(t *testing.T) {
	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.ScrollX = 7
	tab.SetWrap(true)
	if !tab.Wrap || tab.ScrollX != 0 {
		t.Fatalf("after SetWrap(true): Wrap=%v ScrollX=%d", tab.Wrap, tab.ScrollX)
	}
	if !tab.cursorMoved {
		t.Fatal("SetWrap(true) should set cursorMoved")
	}
	tab.cursorMoved = false
	tab.ScrollSeg = 3
	tab.SetWrap(false)
	if tab.Wrap || tab.ScrollSeg != 0 {
		t.Fatalf("after SetWrap(false): Wrap=%v ScrollSeg=%d", tab.Wrap, tab.ScrollSeg)
	}
	// Same-value calls are no-ops and must not disturb view state.
	tab.cursorMoved = false
	tab.SetWrap(false)
	if tab.cursorMoved {
		t.Fatal("no-op SetWrap should not set cursorMoved")
	}
}

// TestRenderWrapped_ContinuationRows renders one wrapping line plus a
// short second line and pins the wrap-mode body layout: content flows
// onto continuation rows, the gutter number appears only on a line's
// first row, the next line's number lands after the wrapped rows, and no
// horizontal-overflow chevron is drawn.
func TestRenderWrapped_ContinuationRows(t *testing.T) {
	scr := newSimScreen(t, 16, 6) // gutter 6 → content width 9
	defer scr.Fini()

	tab := wrapTestTab(t, "alpha beta gamma\nsecond")
	tab.Render(scr, theme.Default(), 0, 0, 16, 6)
	scr.Show()

	rows := simScreenRows(scr)
	if !strings.Contains(rows[0], "1") || !strings.Contains(rows[0], "alpha") {
		t.Errorf("row 0 = %q, want line number 1 and 'alpha'", rows[0])
	}
	if !strings.Contains(rows[1], "beta") {
		t.Errorf("row 1 = %q, want continuation 'beta'", rows[1])
	}
	if got := strings.TrimSpace(rows[1][:defaultGutterWidth]); got != "" {
		t.Errorf("continuation gutter = %q, want blank", got)
	}
	if !strings.Contains(rows[2], "gamma") {
		t.Errorf("row 2 = %q, want continuation 'gamma'", rows[2])
	}
	if !strings.Contains(rows[3], "2") || !strings.Contains(rows[3], "second") {
		t.Errorf("row 3 = %q, want line number 2 and 'second'", rows[3])
	}
	for i, row := range rows {
		if strings.ContainsRune(row, '›') || strings.ContainsRune(row, '‹') {
			t.Errorf("row %d = %q: wrap mode must not draw overflow chevrons", i, row)
		}
	}
}

// TestRenderWrapped_WideGlyphsRowLayout is the wrap-mode version of the
// cell budget. A nine-cell pane holds four ideographs with one cell to
// spare, so the fifth moves to the next row rather than being sliced, each
// glyph owns two cells, and the caret on the continuation row lands at the
// segment-local cell — not the segment-local rune.
func TestRenderWrapped_WideGlyphsRowLayout(t *testing.T) {
	scr := newSimScreen(t, 16, 6) // gutter 6 → content width 9
	defer scr.Fini()

	tab := wrapTestTab(t, "日本語です。ました")
	tab.Cursor = Position{Line: 0, Col: 5} // second row, one glyph in
	tab.Anchor = tab.Cursor
	tab.Render(scr, theme.Default(), 0, 0, 16, 6)
	scr.Show()

	cells, w, _ := scr.GetContents()
	contentX := defaultGutterWidth + 1
	row := func(y int, offs ...int) []string {
		out := make([]string, 0, len(offs))
		for _, off := range offs {
			out = append(out, string(cells[y*w+contentX+off].Runes))
		}
		return out
	}
	// Four glyphs at even cells, their second halves owned by the screen.
	first := row(0, 0, 1, 2, 3, 4, 5, 6, 7, 8)
	want := []string{"日", "", "本", "", "語", "", "で", "", " "}
	for i := range want {
		if first[i] != want[i] {
			t.Errorf("row 0 cell %d = %q, want %q", i, first[i], want[i])
		}
	}
	if got := row(1, 0, 2)[0]; got != "す" {
		t.Errorf("row 1 should continue with す, got %q", got)
	}

	cx, cy, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor hidden")
	}
	// Col 5 is the sixth glyph: second row (segment starts at rune 4), one
	// glyph in, so two cells from the content edge.
	if cy != 1 || cx != contentX+2 {
		t.Errorf("cursor at (%d,%d), want (%d,1)", cx, cy, contentX+2)
	}
}

// TestRenderWrapped_CursorOnContinuationRow pins hardware-cursor
// placement on a wrapped row: a cursor in the third segment lands on
// screen row 2 at the segment-local column.
func TestRenderWrapped_CursorOnContinuationRow(t *testing.T) {
	scr := newSimScreen(t, 16, 6)
	defer scr.Fini()

	tab := wrapTestTab(t, "alpha beta gamma\nsecond")
	tab.Cursor = Position{Line: 0, Col: 11} // start of "gamma"
	tab.Anchor = tab.Cursor
	tab.Render(scr, theme.Default(), 0, 0, 16, 6)
	scr.Show()

	cx, cy, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor not visible")
	}
	if cy != 2 {
		t.Errorf("cursor row = %d, want 2", cy)
	}
	if want := defaultGutterWidth + 1; cx != want {
		t.Errorf("cursor col = %d, want %d (segment-local col 0)", cx, want)
	}
}

// TestRenderWrapped_CursorClampAtFullWidthRow covers the end-of-line
// cursor on a row that fills the pane exactly: it clamps onto the last
// cell instead of landing in the scrollbar/border column.
func TestRenderWrapped_CursorClampAtFullWidthRow(t *testing.T) {
	scr := newSimScreen(t, 16, 4)
	defer scr.Fini()

	tab := wrapTestTab(t, "abcdefghi") // exactly content width (9)
	tab.Cursor = Position{Line: 0, Col: 9}
	tab.Anchor = tab.Cursor
	tab.Render(scr, theme.Default(), 0, 0, 16, 4)
	scr.Show()

	cx, cy, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor not visible")
	}
	if cy != 0 {
		t.Errorf("cursor row = %d, want 0", cy)
	}
	if want := defaultGutterWidth + 1 + 8; cx != want {
		t.Errorf("cursor col = %d, want clamp at %d", cx, want)
	}
}

// TestHitTestWrapped maps clicks on wrapped rows back to buffer
// positions: continuation rows resolve to the same buffer line at the
// segment's rune offsets, gutter clicks land at the row's first rune,
// and clicks below the last row miss.
func TestHitTestWrapped(t *testing.T) {
	scr := newSimScreen(t, 16, 6)
	defer scr.Fini()

	tab := wrapTestTab(t, "alpha beta gamma\nsecond")
	tab.Render(scr, theme.Default(), 0, 0, 16, 6)

	contentX := defaultGutterWidth + 1
	if pos, ok := tab.HitTest(contentX+2, 1, 16, 6); !ok || pos != (Position{Line: 0, Col: 8}) {
		t.Errorf("continuation click = %+v ok=%v, want line 0 col 8", pos, ok)
	}
	if pos, ok := tab.HitTest(contentX, 2, 16, 6); !ok || pos != (Position{Line: 0, Col: 11}) {
		t.Errorf("third-row click = %+v ok=%v, want line 0 col 11", pos, ok)
	}
	if pos, ok := tab.HitTest(2, 1, 16, 6); !ok || pos != (Position{Line: 0, Col: 6}) {
		t.Errorf("gutter click = %+v ok=%v, want segment start col 6", pos, ok)
	}
	if pos, ok := tab.HitTest(contentX+1, 3, 16, 6); !ok || pos != (Position{Line: 1, Col: 1}) {
		t.Errorf("second-line click = %+v ok=%v, want line 1 col 1", pos, ok)
	}
	if _, ok := tab.HitTest(contentX, 5, 16, 6); ok {
		t.Error("click below the last visual row should miss")
	}
}

// TestScrollWrapped verifies wheel scrolling moves the anchor by visual
// rows — one tick inside a wrapped line advances the segment, not the
// whole line — and that the pre-first-render fallback still moves by
// buffer lines.
func TestScrollWrapped(t *testing.T) {
	tab := wrapTestTab(t, "abcdefghijkl\nx\nyy")
	tab.lastWrapW = 5 // line 0 wraps [0,5,10]

	tab.Scroll(1)
	if tab.ScrollY != 0 || tab.ScrollSeg != 1 {
		t.Fatalf("after 1 tick: (%d,%d), want (0,1)", tab.ScrollY, tab.ScrollSeg)
	}
	tab.Scroll(2)
	if tab.ScrollY != 1 || tab.ScrollSeg != 0 {
		t.Fatalf("after 3 ticks: (%d,%d), want (1,0)", tab.ScrollY, tab.ScrollSeg)
	}
	tab.Scroll(-3)
	if tab.ScrollY != 0 || tab.ScrollSeg != 0 {
		t.Fatalf("after scrolling back: (%d,%d), want (0,0)", tab.ScrollY, tab.ScrollSeg)
	}
	tab.Scroll(99)
	if tab.ScrollY != 2 || tab.ScrollSeg != 0 {
		t.Fatalf("past EOF: (%d,%d), want cap at (2,0)", tab.ScrollY, tab.ScrollSeg)
	}

	// Never-rendered fallback: no cached width, so scroll by lines.
	fresh := wrapTestTab(t, "abcdefghijkl\nx\nyy")
	fresh.Scroll(2)
	if fresh.ScrollY != 2 || fresh.ScrollSeg != 0 {
		t.Fatalf("fallback scroll: (%d,%d), want (2,0)", fresh.ScrollY, fresh.ScrollSeg)
	}
}

// TestEnsureVisibleWrapped pins both directions of cursor chasing: a
// cursor below the viewport pulls the anchor down just far enough to
// show its row at the bottom, and a cursor above snaps the anchor to the
// cursor's own row.
func TestEnsureVisibleWrapped(t *testing.T) {
	tab := wrapTestTab(t, "abcdefghijklmnopqrstuvwxy\nend") // 25 runes → 5 rows at width 5

	tab.Cursor = Position{Line: 0, Col: 22} // segment 4
	tab.ensureVisibleWrapped(5, 3)
	if tab.ScrollY != 0 || tab.ScrollSeg != 2 {
		t.Fatalf("cursor below: anchor (%d,%d), want (0,2)", tab.ScrollY, tab.ScrollSeg)
	}

	tab.Cursor = Position{Line: 0, Col: 0}
	tab.ensureVisibleWrapped(5, 3)
	if tab.ScrollY != 0 || tab.ScrollSeg != 0 {
		t.Fatalf("cursor above: anchor (%d,%d), want (0,0)", tab.ScrollY, tab.ScrollSeg)
	}
}

// TestClampScrollWrapped verifies the overscroll bound: the anchor may
// not pass the point where the file's last row sits mid-viewport, mirroring
// line mode's clampScroll feel.
func TestClampScrollWrapped(t *testing.T) {
	tab := wrapTestTab(t, strings.Repeat("x", 60)) // 12 rows at width 5
	tab.ScrollSeg = 11
	tab.clampScrollWrapped(5, 6) // overscroll 3 → max anchor (0,9)
	if tab.ScrollY != 0 || tab.ScrollSeg != 9 {
		t.Fatalf("clamped anchor (%d,%d), want (0,9)", tab.ScrollY, tab.ScrollSeg)
	}
}

// TestScrollH_NoopWhenWrapped pins that horizontal wheel input does
// nothing in wrap mode — there is never content past the right edge.
func TestScrollH_NoopWhenWrapped(t *testing.T) {
	tab := wrapTestTab(t, "abcdefghijkl")
	tab.ScrollH(4)
	if tab.ScrollX != 0 {
		t.Fatalf("ScrollX = %d, want 0 in wrap mode", tab.ScrollX)
	}
}

// equalInts reports whether two int slices are element-wise equal —
// small local helper so segment assertions read cleanly.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// singleCluster reports whether runes is exactly one grapheme cluster —
// the one shape a wrap segment is allowed to overflow its pane with,
// because there is nothing smaller to break it into.
func singleCluster(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	end, _ := ClusterAt(runes, 0, 0)
	return end == len(runes)
}

// FuzzWrapSegments hammers the segment splitter with adversarial lines and
// pane widths. Every wrap walk in the package (render, hit-test, anchor
// advance/retreat) trusts three properties, so those are what we assert
// rather than "it didn't panic": segments start at 0 and strictly ascend,
// the segment slices concatenate back into the original line byte-for-byte
// (no rune dropped, duplicated, or reordered), and no segment paints wider
// than the pane once the trailing whitespace that is deliberately allowed
// to hang past the right edge is discounted.
func FuzzWrapSegments(f *testing.F) {
	seeds := []struct {
		line  string
		width int
	}{
		{"", 10},
		{"hello world", 5},
		{"日本語のテキストが折り返される", 6},
		{"\t\t\tdeeply indented block", 4},
		{"                        ", 3},
		{"a\rb\rc\r", 2},
		{"\r", 1},
		{strings.Repeat("supercalifragilistic ", 40), 17},
		{"word word word", 0},
		{"one-really-long-unbreakable-token", 7},
		{"e\u0301 combining acute", 4},
		{"tab\tafter\ttab", 5},
		{"日本語のテキスト", 1}, // a pane narrower than one glyph
		{"ab日本語", 5},    // a wide glyph straddling the right edge
		{"🇯🇵 flag then text", 3},
		{"👨\u200d👩\u200d👦 family", 4},
		{"a\u0301\u0302\u0303 stacked marks", 2},
		{"❤\ufe0f emoji presentation", 3},
		{"混ざったmixed幅width", 7},
		{"a \u0303b cd", 2}, // whitespace carrying a combining mark
	}
	for _, s := range seeds {
		f.Add(s.line, s.width)
	}

	f.Fuzz(func(t *testing.T, line string, width int) {
		// Clamp into the range a real pane can occupy. Absurd widths add
		// no coverage (everything collapses to a single segment) and a
		// negative width only has one interesting case, which the
		// non-positive branch below already pins.
		width %= 512
		if width < -1 {
			width = -1
		}

		runes := []rune(line)
		segs := WrapSegments(runes, width)

		if len(segs) == 0 || segs[0] != 0 {
			t.Fatalf("segments must start at 0, got %v", segs)
		}
		if width <= 0 && len(segs) != 1 {
			t.Fatalf("non-positive width must collapse to one segment, got %v", segs)
		}
		for i := 1; i < len(segs); i++ {
			if segs[i] <= segs[i-1] {
				t.Fatalf("segments not strictly ascending: %v", segs)
			}
		}
		if last := segs[len(segs)-1]; last > len(runes) {
			t.Fatalf("segment start %d is past the %d-rune line", last, len(runes))
		}

		rebuilt := make([]rune, 0, len(runes))
		for i := range segs {
			start, end := wrapSegBounds(segs, i, len(runes))
			if start > end || end > len(runes) {
				t.Fatalf("segment %d has bad bounds [%d,%d) over %d runes", i, start, end, len(runes))
			}
			seg := runes[start:end]
			rebuilt = append(rebuilt, seg...)

			if width <= 0 {
				continue
			}
			// A run of spaces or tabs is allowed to hang past the right
			// edge (painting clips it) so continuation rows never open on
			// the wrapping whitespace. Everything the user can actually
			// see must fit — unless the segment is a single grapheme
			// cluster, which is the smallest thing a row can hold: a
			// two-cell ideograph in a one-cell pane has to overflow
			// somewhere, and splitting it is not an option.
			// Trim by cluster, not by rune: whitespace can carry a
			// combining mark or a joiner, and the whole cluster is what
			// hangs off the edge.
			trimmed := seg
			for len(trimmed) > 0 {
				last := ClusterStart(trimmed, len(trimmed)-1)
				if !isWrapSpace(trimmed[last]) {
					break
				}
				trimmed = trimmed[:last]
			}
			if w := LineVisualCol(trimmed, len(trimmed)); w > width && !singleCluster(trimmed) {
				t.Fatalf("segment %d paints %d cells, wider than the %d-cell pane (segment %q, line %q)",
					i, w, width, string(seg), line)
			}
		}
		// Compare rune slices, not the raw string: WrapSegments is only
		// ever handed Buffer.LineRunes output, so invalid UTF-8 has
		// already collapsed to RuneError before it gets here and the
		// original bytes are not the contract.
		if string(rebuilt) != string(runes) {
			t.Fatalf("segments do not reconstruct the line:\n got %q\nwant %q", string(rebuilt), string(runes))
		}

		// A break may never land inside a character: every segment starts
		// on a cluster boundary, which is what lets the render and
		// hit-test paths treat a segment's rune subslice as a line of its
		// own. Collected in one forward walk — asking per segment would be
		// quadratic on a line with no ASCII in it.
		boundary := make([]bool, len(runes)+1)
		boundary[len(runes)] = true
		for i := 0; i < len(runes); {
			boundary[i] = true
			next, _ := ClusterAt(runes, i, 0)
			i = next
		}
		for i, start := range segs {
			if !boundary[start] {
				t.Fatalf("segment %d starts at %d, inside a cluster (line %q)", i, start, line)
			}
		}

		// Every rune column must resolve to the segment that actually
		// contains it — hit-test and cursor placement both depend on this.
		for col := 0; col <= len(runes); col++ {
			row := WrapRowOfCol(segs, col)
			start, end := wrapSegBounds(segs, row, len(runes))
			if col < start || (col > end) {
				t.Fatalf("col %d resolved to segment %d with bounds [%d,%d)", col, row, start, end)
			}
		}
	})
}

// TestClampCursorWrapped pins the caret-follows-scroll clamp in wrap
// mode, where "visible" is a (line, segment) question: a caret above
// the anchor lands inside the anchor row's own segment, one below lands
// inside the last visible row's segment, and the result is a position
// ensureVisibleWrapped already considers visible — so the anchor does
// not move afterwards (the yank-back fence, wrap edition).
func TestClampCursorWrapped(t *testing.T) {
	tab := wrapTestTab(t, "abcdefghijkl\nx\nyy")
	tab.lastWrapW = 5 // line 0 wraps at cols [0,5,10]

	// Anchor on line 0's third segment; caret in its first: above.
	tab.MoveCursorTo(Position{Line: 0, Col: 1}, false)
	tab.ScrollY, tab.ScrollSeg = 0, 2
	tab.ClampCursorToView(2)
	if tab.Cursor.Line != 0 || tab.Cursor.Col < 10 {
		t.Fatalf("above-view clamp = %+v, want line 0 inside segment [10,12]", tab.Cursor)
	}
	y, s := tab.ScrollY, tab.ScrollSeg
	tab.ensureVisibleWrapped(5, 2)
	if tab.ScrollY != y || tab.ScrollSeg != s {
		t.Fatalf("anchor moved to (%d,%d) — yank-back is back", tab.ScrollY, tab.ScrollSeg)
	}

	// Anchor at the top; caret on line 2: below a 2-row viewport whose
	// last visible row is line 0's second segment [5,10).
	tab.MoveCursorTo(Position{Line: 2, Col: 0}, false)
	tab.ScrollY, tab.ScrollSeg = 0, 0
	tab.ClampCursorToView(2)
	if tab.Cursor.Line != 0 || tab.Cursor.Col < 5 || tab.Cursor.Col >= 10 {
		t.Fatalf("below-view clamp = %+v, want line 0 inside segment [5,10)", tab.Cursor)
	}
	y, s = tab.ScrollY, tab.ScrollSeg
	tab.ensureVisibleWrapped(5, 2)
	if tab.ScrollY != y || tab.ScrollSeg != s {
		t.Fatalf("anchor moved to (%d,%d) — yank-back is back", tab.ScrollY, tab.ScrollSeg)
	}
}
