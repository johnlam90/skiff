// =============================================================================
// File: internal/editor/indent.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// indent.go owns two related concerns:
//
//   1. Visual-column math. Tabs render as multi-cell tab stops, CJK and
//      emoji glyphs claim two cells, and combining marks claim none, so a
//      rune's "screen position" is no longer equal to its index in the
//      line. The renderer, cursor placement, and mouse hit-test all consult
//      these helpers when converting between rune indices and visual cells.
//      The cell widths themselves come from cluster.go.
//
//   2. Indent style detection. When a tab is opened, we sample its existing
//      indentation and pick the unit (tab character vs N spaces) the user
//      gets when they press Tab. This avoids the "spaces in a tab-indented
//      file" bug that has historically been the #1 complaint about new
//      editors.

package editor

import (
	"path/filepath"
	"strings"
)

// TabStop is the visual cell width of a hard tab. Four matches what most
// modern editors and viewers default to (cat, less, GitHub) and matches the
// editor's typical insert width when the user presses Tab.
const TabStop = 4

// defaultSpaceIndent is the fallback indent unit when detection has nothing
// to go on (empty file, no leading whitespace anywhere). Four spaces is
// the common convention across the languages the editor most often opens.
const defaultSpaceIndent = "    "

// RuneVisualWidth returns the cell count occupied by r when it lands at the
// given visualCol (0-based, measured from the start of the line). Hard tabs
// expand to fill enough cells to reach the next TabStop boundary; every
// other rune reports the width Unicode gives it in a monospace grid — 0 for
// combining marks, ZWJ, and control characters, 2 for east-asian wide and
// fullwidth glyphs and emoji, 1 for the rest.
//
// This is a per-RUNE answer, and a rune is not always a character: U+FE0F
// reports 0 here but widens the emoji in front of it, and the five runes of
// a family emoji report 2+0+2+0+2 while the family itself paints in two
// cells. Layout code walks clusters through ClusterAt for exactly that
// reason. Reach for this only with a rune that has no neighbours.
func RuneVisualWidth(r rune, visualCol int) int {
	if r == '\t' {
		return tabCells(visualCol)
	}
	return runeCellWidth(r)
}

// LineVisualCol returns the visual column (0-based) at the rune position
// runeCol within runes. Tabs in the prefix expand to tab stops and wide
// glyphs count double. Used for cursor placement and selection / find
// highlighting.
//
// runeCol is clamped to [0, len(runes)] so callers can pass an end-of-line
// position without bounds-checking, and a runeCol that lands *inside* a
// grapheme cluster reports the column the cluster starts at: a caret can
// sit before or after "é" but there is no cell between the e and its
// accent to report.
func LineVisualCol(runes []rune, runeCol int) int {
	if runeCol > len(runes) {
		runeCol = len(runes)
	}
	visualCol := 0
	for i := 0; i < runeCol; {
		end, w := ClusterAt(runes, i, visualCol)
		if end > runeCol {
			break // runeCol sits inside this cluster
		}
		visualCol += w
		i = end
	}
	return visualCol
}

// RuneColAtVisual returns the rune index of the cluster whose cells cover
// targetVisualCol, snapping clicks inside a multi-cell glyph back to its
// first rune. Used by mouse hit-testing — clicking the right half of a CJK
// ideograph or anywhere in a 4-cell tab places the cursor on that
// character, not somewhere "inside" it, and never between a base rune and
// its combining marks.
//
// When targetVisualCol is past the line's end, the rune count is returned
// (cursor lands at the end-of-line virtual position). Zero-width clusters
// are skipped rather than returned: there is no cell to click on them
// with.
func RuneColAtVisual(runes []rune, targetVisualCol int) int {
	if targetVisualCol <= 0 {
		return 0
	}
	visualCol := 0
	for i := 0; i < len(runes); {
		end, w := ClusterAt(runes, i, visualCol)
		if targetVisualCol < visualCol+w {
			return i
		}
		visualCol += w
		i = end
	}
	return len(runes)
}

// DetectIndent picks the indent unit a freshly-opened buffer should use
// when the user presses Tab. The algorithm in priority order:
//
//  1. Walk every line; classify lines that start with whitespace as either
//     tab-indented or space-indented. Count both.
//  2. If tab-indented lines outnumber space-indented ones, return "\t".
//  3. If space-indented wins, return that many spaces (using the smallest
//     non-zero leading-space count as the indent width — that matches what
//     "infer" tools in other editors do).
//  4. With no signal, fall back to the path's extension: ".go" / Makefiles /
//     ".tsv" default to tabs (those file types either require or strongly
//     prefer tabs); everything else defaults to four spaces.
//
// The result is what the *Tab* key inserts; existing characters in the file
// are not rewritten.
func DetectIndent(lines []string, path string) string {
	tabLines := 0
	spaceLines := 0
	smallestSpaceWidth := 0 // 0 = "no space-indented line seen yet"

	for _, line := range lines {
		if line == "" {
			continue
		}
		switch line[0] {
		case '\t':
			tabLines++
		case ' ':
			spaceLines++
			n := leadingSpaces(line)
			if n > 0 && (smallestSpaceWidth == 0 || n < smallestSpaceWidth) {
				smallestSpaceWidth = n
			}
		}
	}

	if tabLines > spaceLines && tabLines > 0 {
		return "\t"
	}
	if spaceLines > 0 && smallestSpaceWidth > 0 {
		return strings.Repeat(" ", smallestSpaceWidth)
	}

	// No signal — pick a sensible default by file extension.
	return defaultIndentForPath(path)
}

// leadingSpaces counts the number of leading ' ' bytes in s, stopping at
// the first non-space byte.
func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// defaultIndentForPath returns "\t" for languages whose conventions strongly
// prefer or require tabs (Go's gofmt rewrites to tabs; Makefiles literally
// require tabs at the start of recipe lines; TSV is tab-separated by name).
// Everything else defaults to four spaces. This is consulted only when the
// file has no existing indentation to learn from — a real Python source
// with 4-space indents will be detected as "    " regardless of the path.
func defaultIndentForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if base == "makefile" || base == "gnumakefile" {
		return "\t"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".tsv":
		return "\t"
	}
	return defaultSpaceIndent
}

// autoIndentFor returns the whitespace a freshly-created line should open
// with, given the text that will sit behind the caret on the line being
// split (prefix), the tab's detected indent unit, and the file path.
//
// The rule is deliberately shallow — copy the current line's leading
// whitespace, plus one unit when the line opens a block. No reflow, no
// de-denting of closing braces, no language model. An auto-indent that
// occasionally guesses wrong is only forgivable if the user can predict
// exactly when; anything cleverer here would be a formatter, and this
// project already has one behind format-on-save.
//
// Taking the prefix rather than the whole line is what makes splitting a
// line mid-indent behave: the new line inherits only the whitespace that
// was actually before the caret, so the text that moves down keeps its own
// leading whitespace and lands back at the column it started at.
func autoIndentFor(prefix []rune, unit, path string) string {
	n := 0
	for n < len(prefix) && (prefix[n] == ' ' || prefix[n] == '\t') {
		n++
	}
	lead := string(prefix[:n])
	if !opensIndentBlock(prefix, path) {
		return lead
	}
	if unit == "" {
		// Only hand-built tabs get here — NewTab always runs
		// DetectIndent — but an empty unit would silently turn the
		// block rule off, which is worse than picking the default.
		unit = defaultSpaceIndent
	}
	return lead + unit
}

// opensIndentBlock reports whether prefix ends with something that should
// push the next line one level deeper: an opening brace / bracket / paren
// in any language, or a colon in the languages where a trailing colon
// introduces a block. Trailing whitespace is ignored so "if x {   " counts.
func opensIndentBlock(prefix []rune, path string) bool {
	i := len(prefix) - 1
	for i >= 0 && (prefix[i] == ' ' || prefix[i] == '\t') {
		i--
	}
	if i < 0 {
		return false
	}
	switch prefix[i] {
	case '{', '[', '(':
		return true
	case ':':
		return colonOpensBlock(path)
	}
	return false
}

// colonOpensBlock reports whether a trailing ':' introduces an indented
// block in this file type. Python and YAML are the two the editor opens
// often enough to matter; everywhere else a trailing colon is a label
// (Go, C), a ternary, or a map value, and indenting after it would be
// wrong. Detection is by extension only — a shebang script with no
// extension gets the plain copy-the-indent behavior, which is never
// actively wrong, just less helpful.
func colonOpensBlock(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".pyi", ".pyw", ".yml", ".yaml":
		return true
	}
	return false
}
