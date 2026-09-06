// =============================================================================
// File: internal/textdraw/textdraw.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package textdraw is the chrome's single authority on how many terminal
// cells a string occupies and how to paint it clipped to a budget. It walks
// grapheme clusters with uniseg — the same engine tcell's CellBuffer uses —
// so measurement and the cell buffer agree by construction (see CLAUDE.md
// "Three units": a CJK ideograph is two cells, a combining mark none, a ZWJ
// family emoji is five runes in two cells).
//
// Like internal/scrollbar, this is deliberately a leaf package: it imports
// only tcell and uniseg — no theme, no editor — so every chrome surface
// (overlays, strips, git panel, file tree) can share it without dragging in
// higher layers. The editor keeps its own richer cluster machinery in
// internal/editor/cluster.go; do not unify the two.
package textdraw

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Width returns the terminal cell count of s, walking grapheme clusters
// with uniseg — the same engine tcell paints with, so measurement and
// the cell buffer agree by construction (see CLAUDE.md "Three units").
func Width(s string) int {
	w := 0
	state := -1
	for len(s) > 0 {
		var cw int
		_, s, cw, state = uniseg.FirstGraphemeClusterInString(s, state)
		w += cw
	}
	return w
}

// Clip returns the longest prefix of whole clusters fitting maxW cells,
// plus that prefix's width. A cluster is never split: a two-cell
// ideograph that would straddle the budget ends the prefix instead.
func Clip(s string, maxW int) (string, int) {
	if maxW <= 0 {
		return "", 0
	}
	w, end := 0, 0
	state := -1
	rest := s
	for len(rest) > 0 {
		cluster, tail, cw, next := uniseg.FirstGraphemeClusterInString(rest, state)
		if w+cw > maxW {
			break
		}
		w += cw
		end += len(cluster)
		rest, state = tail, next
	}
	return s[:end], w
}

// ClipEllipsis is Clip with a trailing … (1 cell) when anything was cut.
func ClipEllipsis(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if Width(s) <= maxW {
		return s
	}
	if maxW == 1 {
		return "…"
	}
	clipped, _ := Clip(s, maxW-1)
	return clipped + "…"
}

// DrawClipped paints s at (x, y) clipped to maxW cells and returns the
// x just past the last cell painted. Each cluster is emitted as one
// SetContent call — primary rune plus the cluster's remaining runes as
// combining content — then its width is skipped, which is how tcell
// expects wide/combined glyphs to be laid down. Drawing stops before a
// cluster that would cross the budget; a zero-width cluster with no base
// cell to attach to (a bare combining mark) is skipped rather than
// overdrawing the previous cell.
func DrawClipped(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) int {
	if maxW <= 0 {
		return x
	}
	budget := maxW
	state := -1
	for len(s) > 0 {
		var cluster string
		var cw int
		cluster, s, cw, state = uniseg.FirstGraphemeClusterInString(s, state)
		if cw > budget {
			break
		}
		if cw == 0 {
			continue
		}
		rs := []rune(cluster)
		scr.SetContent(x, y, rs[0], rs[1:], st)
		x += cw
		budget -= cw
	}
	return x
}

// WrapWords breaks s into lines of at most maxW cells, greedy at spaces
// so words stay whole and falling back to a hard break between clusters
// when a single run is wider than a line. Spacing INSIDE a line is kept
// exactly — a fixed key column or an indented stderr line survives —
// and only the spaces at a break are dropped: they are the break, not
// content, so a line never ends in one and a continuation never starts
// with one. The result always has at least one line — an empty s is one
// empty line, so a caller laying out rows keeps a blank row blank rather
// than losing it. A non-positive budget returns s untouched on one
// line: a wrap into zero cells is not a wrap, and the caller's clip
// decides.
//
// Widths are cluster-measured (Width), which is what makes this the
// one wrap the chrome shares: the flash strip, an Info body and the
// reference sheet all measure the same way the cell buffer paints.
func WrapWords(s string, maxW int) []string {
	if maxW <= 0 || s == "" {
		return []string{s}
	}
	var out []string
	line := ""
	lineW := 0
	flush := func() {
		out = append(out, trimTrailingSpace(line))
		line, lineW = "", 0
	}
	for _, tok := range splitWords(s) {
		if tok[0] == ' ' {
			if lineW == 0 && len(out) > 0 {
				continue // the air at a break belongs to the break
			}
			if room := maxW - lineW; len(tok) > room {
				tok = tok[:room] // trailing air past the edge is trimmed anyway
			}
			line += tok
			lineW += len(tok)
			continue
		}
		ww := Width(tok)
		if lineW > 0 && lineW+ww > maxW && Width(trimTrailingSpace(line)) > 0 {
			flush()
		}
		// A run wider than a whole line is broken between clusters —
		// a URL or a path must not force an overflow.
		for ww > maxW-lineW {
			head, hw := Clip(tok, maxW-lineW)
			if hw == 0 {
				if lineW > 0 {
					flush()
					continue
				}
				// The next cluster is wider than the whole budget (a
				// two-cell ideograph at maxW == 1). Nothing narrower can
				// ever fit, so it goes out on a line of its own — one
				// cell over, which the caller's clip decides — rather
				// than flushing an empty line and re-trying forever.
				_, tail, cw, _ := uniseg.FirstGraphemeClusterInString(tok, -1)
				head, hw = tok[:len(tok)-len(tail)], cw
			}
			line += head
			lineW += hw
			tok = tok[len(head):]
			ww -= hw
			flush()
		}
		line += tok
		lineW += ww
	}
	if trimTrailingSpace(line) != "" || len(out) == 0 {
		flush()
	}
	return out
}

// splitWords splits s into alternating word and space-run tokens, each
// run kept whole, so the wrapper can break between words while
// reproducing the spacing inside a line exactly.
func splitWords(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, r := range s {
		isSp := r == ' '
		if i == 0 {
			inSpace = isSp
			continue
		}
		if isSp != inSpace {
			out = append(out, s[start:i])
			start, inSpace = i, isSp
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// trimTrailingSpace drops the spaces a wrap point leaves at a line's end.
func trimTrailingSpace(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
