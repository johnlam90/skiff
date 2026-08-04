// =============================================================================
// File: internal/editor/cluster_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"
)

// TestRuneCellWidth_Classes pins the per-rune width classes the whole
// layout rests on. Getting any one of these wrong moves the caret away
// from the glyph it is editing: a zero reported as one paints a phantom
// cell, a two reported as one lets the next glyph overwrite half a
// character.
func TestRuneCellWidth_Classes(t *testing.T) {
	cases := []struct {
		r    rune
		want int
		why  string
	}{
		{'a', 1, "ascii letter"},
		{' ', 1, "space"},
		{'\r', 0, "control characters take no cell"},
		{0x7f, 0, "DEL is a control character"},
		{'✓', 1, "neutral symbol"},
		{'☃', 1, "text-presentation pictograph"},
		{'你', 2, "han ideograph"},
		{'あ', 2, "hiragana"},
		{'\uff21', 2, "fullwidth latin A"},
		{'😀', 2, "emoji"},
		{'\u0301', 0, "combining acute"},
		{'\u200d', 0, "zero-width joiner"},
		{'\ufe0f', 0, "variation selector"},
		{'\U0001F1EF', 2, "regional indicator"},
	}
	for _, c := range cases {
		if got := runeCellWidth(c.r); got != c.want {
			t.Errorf("runeCellWidth(%q) = %d, want %d (%s)", c.r, got, c.want, c.why)
		}
	}
}

// TestClusterAt_Boundaries walks the shapes a "character" actually takes
// in the wild. The end index is what caret motion and Backspace step by;
// the width is what the renderer and soft wrap budget cells from. Both
// come from the same call on purpose — a disagreement between them is the
// bug class this file exists to prevent.
func TestClusterAt_Boundaries(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantEnd   int
		wantWidth int
	}{
		{"ascii", "ab", 1, 1},
		{"han", "你好", 1, 2},
		{"combining mark joins its base", "e\u0301x", 2, 1},
		{"two marks on one base", "a\u0301\u0302z", 3, 1},
		{"zwj family is one character", "👨\u200d👩\u200d👦!", 5, 2},
		{"regional indicator pair is one flag", "🇯🇵x", 2, 2},
		{"variation selector widens its base", "❤\ufe0fx", 2, 2},
		{"skin tone modifier joins", "👩🏻x", 2, 2},
		{"lone combining mark stands alone", "\u0301a", 1, 0},
	}
	for _, c := range cases {
		runes := []rune(c.text)
		end, width := ClusterAt(runes, 0, 0)
		if end != c.wantEnd || width != c.wantWidth {
			t.Errorf("%s: ClusterAt(%q) = (%d, %d), want (%d, %d)",
				c.name, c.text, end, width, c.wantEnd, c.wantWidth)
		}
	}
}

// TestClusterAt_TabKeepsTabStops guards the one width that is not a
// property of the rune: a tab fills the cells left to the next stop, and
// that arithmetic had to survive the move out of RuneVisualWidth.
func TestClusterAt_TabKeepsTabStops(t *testing.T) {
	runes := []rune("\t你")
	for col, want := range map[int]int{0: 4, 1: 3, 3: 1, 4: 4} {
		end, width := ClusterAt(runes, 0, col)
		if end != 1 || width != want {
			t.Errorf("tab at col %d = (%d, %d), want (1, %d)", col, end, width, want)
		}
	}
}

// TestClusterAt_GrowsPastScratchWindow feeds a base with more combining
// marks than the stack scratch buffer holds. The probe window doubles
// until uniseg stops consuming all of it, so the answer must be the whole
// cluster rather than "as much as fitted in 64 bytes" — otherwise a Zalgo
// line would quietly get a caret stop in the middle of a character.
func TestClusterAt_GrowsPastScratchWindow(t *testing.T) {
	marks := strings.Repeat("\u0301", 40) // 80 bytes of marks alone
	runes := []rune("a" + marks + "b")
	end, width := ClusterAt(runes, 0, 0)
	if end != 41 {
		t.Errorf("cluster end = %d, want 41 (base plus every mark)", end)
	}
	if width != 1 {
		t.Errorf("cluster width = %d, want 1", width)
	}
}

// TestClusterAt_InvalidRunesDoNotPanic feeds the walk runes that cannot be
// encoded — a surrogate half and values outside the Unicode range. Buffer
// text never contains them (decoding a string turns them into RuneError
// first), but these helpers are exported and a panic in the render path
// would take the whole editor down. The long-cluster case is the one that
// actually bites: an unencodable rune reports length -1, so a probe window
// sized from those lengths would be short of the bytes the encode writes.
func TestClusterAt_InvalidRunesDoNotPanic(t *testing.T) {
	lines := [][]rune{
		{'a', 0xD800, 0x110000, -1, '你'},
		append([]rune("a"+strings.Repeat("\u0301", 40)), 0xD800, 0xD800, -1, 0x110000, 0xDFFF),
	}
	for n, runes := range lines {
		visualCol := 0
		for i := 0; i < len(runes); {
			end, w := ClusterAt(runes, i, visualCol)
			if end <= i {
				t.Fatalf("line %d: walk stalled at %d", n, i)
			}
			if w < 0 {
				t.Fatalf("line %d: negative width %d at %d", n, w, i)
			}
			visualCol += w
			i = end
		}
		if got := LineVisualCol(runes, len(runes)); got < 1 {
			t.Errorf("line %d measured %d cells, want a sane count", n, got)
		}
	}
}

// TestClusterStart_SnapsIntoCluster is the promise motion code relies on:
// any rune index resolves back to the first rune of the character it sits
// in, and an index already on a boundary — including the end-of-line caret
// slot — is left alone.
func TestClusterStart_SnapsIntoCluster(t *testing.T) {
	runes := []rune("a👨\u200d👩\u200d👦b") // a, 5-rune family, b
	want := []int{0, 1, 1, 1, 1, 1, 6, 7}
	for i, w := range want {
		if got := ClusterStart(runes, i); got != w {
			t.Errorf("ClusterStart(%d) = %d, want %d", i, got, w)
		}
	}
	if got := ClusterStart(runes, 99); got != len(runes) {
		t.Errorf("past-the-end index = %d, want %d", got, len(runes))
	}
	if got := ClusterStart(runes, -3); got != 0 {
		t.Errorf("negative index = %d, want 0", got)
	}
}

// TestClusterWalk_RoundTrips walks a mixed line forward with NextCluster
// and back with PrevCluster and requires the two to visit exactly the same
// boundaries. Left-then-right caret motion round-tripping is the visible
// version of this property; Backspace deleting what Left just stepped over
// is the other.
func TestClusterWalk_RoundTrips(t *testing.T) {
	runes := []rune("x你e\u0301🇯🇵\t👩🏻z")

	var forward []int
	for i := 0; i < len(runes); {
		forward = append(forward, i)
		next := NextCluster(runes, i)
		if next <= i {
			t.Fatalf("NextCluster stalled at %d", i)
		}
		i = next
	}

	var backward []int
	for i := len(runes); i > 0; {
		prev := PrevCluster(runes, i)
		if prev >= i {
			t.Fatalf("PrevCluster stalled at %d", i)
		}
		backward = append([]int{prev}, backward...)
		i = prev
	}

	if len(forward) != len(backward) {
		t.Fatalf("forward %v and backward %v disagree on cluster count", forward, backward)
	}
	for i := range forward {
		if forward[i] != backward[i] {
			t.Fatalf("forward %v != backward %v", forward, backward)
		}
	}
	// Every boundary the walk produced must be a real cluster start.
	for _, i := range forward {
		if got := ClusterStart(runes, i); got != i {
			t.Errorf("walk stopped at %d, which snaps to %d", i, got)
		}
	}
}

// TestClusterWalk_DoesNotAllocate keeps the render path honest. Every
// visible cell of every frame goes through ClusterAt, so a stray
// allocation here is an allocation per cell per repaint — the exact cost
// Buffer.LineRunes's cache exists to avoid. The scratch buffer is sized to
// hold any real cluster on the stack; only Zalgo-length clusters may fall
// back to the heap.
func TestClusterWalk_DoesNotAllocate(t *testing.T) {
	runes := []rune("func 名前() { return \"日本語 é 😀\" } // 折り返し")
	got := testing.AllocsPerRun(100, func() {
		visualCol := 0
		for i := 0; i < len(runes); {
			end, w := ClusterAt(runes, i, visualCol)
			visualCol += w
			i = end
		}
	})
	if got != 0 {
		t.Errorf("cluster walk allocated %.1f times per run, want 0", got)
	}
}
