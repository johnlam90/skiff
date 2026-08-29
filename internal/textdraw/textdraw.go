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
