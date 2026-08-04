// =============================================================================
// File: internal/editor/cluster.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// cluster.go is the editor's bridge to Unicode's idea of "one character".
//
// Three different units are in play and all three are load-bearing:
//
//   - A rune is what Position.Col indexes and what Buffer edits splice.
//   - A terminal cell is what the renderer paints into; a CJK ideograph
//     eats two of them, a combining mark none.
//   - A grapheme cluster is what a *user* calls a character: "é" may be
//     one rune or two, a keycap is three, a family emoji is five.
//
// The caret walks clusters, the renderer paints clusters, and the buffer
// still stores runes. This file is the only place that knows how to get
// from one to the other.
//
// The rules come from github.com/rivo/uniseg, which is also what tcell
// itself uses to decide how many cells a SetContent call consumes (see
// tcell's CellBuffer.Put — it runs the primary rune plus its combining
// runes through uniseg and takes that cluster's width). Sharing the engine
// is the entire point: if this package's column math disagreed with
// tcell's, the caret would drift away from the glyph under it on exactly
// the text that is hardest to reason about. That is why widths come from a
// cluster and not from summing runes — uniseg calls a ZWJ family two cells
// wide, not six, and an emoji with a VS16 selector two cells, not one.
//
// Nothing here caches. Every walk is left-to-right over the runes a caller
// already holds, so the soft-wrap design's O(viewport) walks stay
// O(viewport); ASCII text short-circuits before uniseg is ever consulted.

package editor

import (
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// clusterScratchBytes is the stack buffer ClusterAt encodes into before
// handing bytes to uniseg. 64 bytes holds 16 ASCII runes, 21 three-byte
// CJK runes, or a 16-codepoint emoji sequence — past anything real. A
// cluster that overflows it falls back to a heap encode of the rest of
// the line, which keeps the answer exact for Zalgo-style input at the
// cost of an allocation nobody sane will trigger.
const clusterScratchBytes = 64

// clusterProbeRunes is how many runes the first uniseg probe encodes. A
// cluster is one rune in essentially all text, and one rune of lookahead
// is what it takes to prove that, so two is the size that resolves the
// common case in a single pass. Longer clusters double the window until
// uniseg stops consuming all of it.
const clusterProbeRunes = 2

// tabCells returns the cells a hard tab occupies when it starts at
// visualCol: enough to reach the next TabStop boundary. Split out of
// RuneVisualWidth so the cluster walk can reuse the arithmetic without
// re-testing for a tab.
func tabCells(visualCol int) int {
	w := TabStop - visualCol%TabStop
	if w <= 0 || w > TabStop {
		// Only reachable for a negative visualCol, which no caller should
		// produce; a full stop is the least surprising answer.
		w = TabStop
	}
	return w
}

// asciiCellWidth returns the cell width of a single-byte rune. Control
// characters (and DEL) are zero-width, matching uniseg and therefore
// matching what tcell will do with them; printable ASCII is one cell.
// Tabs are the caller's problem — their width depends on the column.
func asciiCellWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	return 1
}

// runeCellWidth returns the terminal cell width of a lone rune: 0 for
// combining marks, ZWJ, and control characters, 2 for east-asian wide and
// fullwidth glyphs and emoji that default to emoji presentation, 1 for
// everything else.
//
// "Lone" is the operative word. A rune's width in isolation is not always
// its contribution to the cluster it lives in — U+FE0F reports 0 here but
// promotes the emoji in front of it from one cell to two. Layout code
// therefore asks ClusterAt, not this; this is the primitive behind
// RuneVisualWidth, which callers reach for when they genuinely have a
// single rune and no context.
func runeCellWidth(r rune) int {
	if r < utf8.RuneSelf {
		return asciiCellWidth(r)
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	_, _, w, _ := uniseg.FirstGraphemeCluster(buf[:n], -1)
	return w
}

// ClusterAt measures the grapheme cluster that starts at rune index i:
// it returns the index one past the cluster's last rune and the number of
// terminal cells the cluster occupies when it starts at visualCol. i must
// be a valid index into runes and should be a cluster boundary — pass
// ClusterStart's answer if that is not already guaranteed.
//
// end always advances (end > i), so a walk over a line can never stall.
func ClusterAt(runes []rune, i, visualCol int) (end, width int) {
	r := runes[i]
	// An ASCII rune followed by another ASCII rune is always its own
	// cluster: nothing in the ASCII range extends, joins, or prepends, and
	// the one exception in the standard — CR followed by LF — cannot occur
	// because buffer lines never carry their terminator. Short-circuiting
	// here keeps source code, which is what this editor mostly opens, off
	// the uniseg path entirely.
	if r < utf8.RuneSelf && (i+1 >= len(runes) || runes[i+1] < utf8.RuneSelf) {
		// A cluster of one rune is exactly what RuneVisualWidth measures,
		// so the two can't drift apart on tab stops or control characters.
		return i + 1, RuneVisualWidth(r, visualCol)
	}
	return clusterAtSlow(runes, i, visualCol)
}

// clusterAtSlow is ClusterAt's non-ASCII path: encode a window of runes
// starting at i and let uniseg find the first cluster boundary in it. The
// window starts at two runes and doubles whenever uniseg consumes all of
// it, because a cluster that fills the window may well continue past it.
// Growing instead of always encoding the maximum is what keeps a line of
// CJK O(runes) rather than O(runes x window).
func clusterAtSlow(runes []rune, i, visualCol int) (end, width int) {
	if runes[i] == '\t' {
		return i + 1, RuneVisualWidth(runes[i], visualCol)
	}
	var scratch [clusterScratchBytes]byte
	buf := scratch[:]
	avail := len(runes) - i
	for want := clusterProbeRunes; ; want *= 2 {
		if want > avail {
			want = avail
		}
		need := 0
		for j := range want {
			// A surrogate or out-of-range rune reports length -1 and
			// encodes as RuneError; budget the maximum so the encode
			// below can never run past the buffer.
			if l := utf8.RuneLen(runes[i+j]); l > 0 {
				need += l
			} else {
				need += utf8.UTFMax
			}
		}
		if need > len(buf) {
			buf = make([]byte, need)
		}
		n := 0
		for j := range want {
			n += utf8.EncodeRune(buf[n:], runes[i+j])
		}
		cluster, _, w, _ := uniseg.FirstGraphemeCluster(buf[:n], -1)
		got := utf8.RuneCount(cluster)
		if got <= 0 {
			// Defensive: uniseg never returns an empty cluster for a
			// non-empty input, but a stalled walk would hang the render.
			return i + 1, runeCellWidth(runes[i])
		}
		if got < want || want == avail {
			return i + got, w
		}
	}
}

// ClusterStart snaps a rune index back to the first rune of the grapheme
// cluster containing it, so no caller can leave the caret sitting between
// a base character and its combining mark. An index already on a boundary
// (including len(runes), the end-of-line caret position) is returned
// unchanged.
//
// The scan starts from the nearest index a boundary is guaranteed at
// rather than from column 0, which makes the ASCII case O(1) and the
// worst case no worse than the O(line) visual-column walks the renderer
// already runs on every frame.
func ClusterStart(runes []rune, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(runes) {
		return len(runes)
	}
	j := clusterAnchor(runes, i)
	for j < i {
		end, _ := ClusterAt(runes, j, 0)
		if end > i {
			return j
		}
		j = end
	}
	return i
}

// clusterAnchor returns an index at or before i that is certain to be a
// cluster boundary. A boundary always exists between two ASCII runes, so
// the scan walks back only as far as the current run of non-ASCII text —
// which is zero steps in source code and the length of the CJK run in
// prose.
func clusterAnchor(runes []rune, i int) int {
	for j := i; j > 0; j-- {
		if runes[j-1] < utf8.RuneSelf && runes[j] < utf8.RuneSelf {
			return j
		}
	}
	return 0
}

// NextCluster returns the rune index of the cluster boundary after the
// cluster containing i — where a rightward caret step or a forward Delete
// lands. Past the last rune it returns the rune count, the end-of-line
// caret position.
func NextCluster(runes []rune, i int) int {
	if i < 0 {
		i = 0
	}
	if i >= len(runes) {
		return len(runes)
	}
	end, _ := ClusterAt(runes, ClusterStart(runes, i), 0)
	if end <= i {
		end = i + 1
	}
	return end
}

// PrevCluster returns the rune index of the cluster boundary before i —
// where a leftward caret step or a Backspace lands. An i sitting inside a
// cluster snaps to that cluster's start rather than skipping a whole
// character, so a caret that got there by some other route is repaired
// instead of stepping over text the user can see.
func PrevCluster(runes []rune, i int) int {
	if i <= 0 {
		return 0
	}
	if i > len(runes) {
		i = len(runes)
	}
	if start := ClusterStart(runes, i); start < i {
		return start
	}
	return ClusterStart(runes, i-1)
}
