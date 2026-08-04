// =============================================================================
// File: internal/editor/indent_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"
)

// TestRuneVisualWidth_Tab pins down the tab-stop math: a tab at col 0
// fills the whole 4-cell stop; a tab at col 3 fills just one cell to
// reach col 4; a tab at col 4 fills another whole stop. This is the
// math every viewer (cat, less, GitHub) uses, and getting it wrong is
// the kind of off-by-one nobody will notice until it's everywhere.
func TestRuneVisualWidth_Tab(t *testing.T) {
	cases := []struct {
		col, want int
	}{
		{0, 4},
		{1, 3},
		{2, 2},
		{3, 1},
		{4, 4},
		{7, 1},
		{8, 4},
	}
	for _, c := range cases {
		if got := RuneVisualWidth('\t', c.col); got != c.want {
			t.Errorf("RuneVisualWidth(\\t, %d) = %d, want %d", c.col, got, c.want)
		}
	}
}

// TestRuneVisualWidth_Other pins the non-tab widths: a rune reports the
// cells a terminal gives it, which is 2 for east-asian wide glyphs and
// emoji, 0 for combining marks and joiners that ride inside another
// character's cell, and 1 for everything else. Text-presentation
// pictographs like ✓ and ☃ stay at 1 — they are drawn from the text font,
// and calling them wide would shift every character after them by a cell.
func TestRuneVisualWidth_Other(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{'✓', 1},
		{'☃', 1},
		{'你', 2},
		{'😀', 2},
		{'\u0301', 0},
		{'\u200d', 0},
	}
	for _, c := range cases {
		if got := RuneVisualWidth(c.r, 0); got != c.want {
			t.Errorf("RuneVisualWidth(%q) = %d, want %d", c.r, got, c.want)
		}
	}
}

// TestLineVisualCol_WideGlyphs is the CJK version of the tab-stop math:
// every ideograph advances the visual column by two, so the caret after
// three of them sits at cell 6 even though it is at rune 3. A one-cell
// answer here is the bug where the caret drifts left of the glyph it is
// editing, one cell per wide character on the line.
func TestLineVisualCol_WideGlyphs(t *testing.T) {
	runes := []rune("日本語x")
	cases := []struct {
		runeCol, want int
	}{
		{0, 0},
		{1, 2},
		{2, 4},
		{3, 6},
		{4, 7}, // past the trailing ASCII 'x'
	}
	for _, c := range cases {
		if got := LineVisualCol(runes, c.runeCol); got != c.want {
			t.Errorf("LineVisualCol(runeCol=%d) = %d, want %d", c.runeCol, got, c.want)
		}
	}
}

// TestLineVisualCol_ClusterInteriorReportsItsStart keeps the caret out of
// the middle of a character. "e" plus a combining acute is two runes in
// one cell, so the interior column has no cell of its own to report and
// must answer with the cell the cluster starts in.
func TestLineVisualCol_ClusterInteriorReportsItsStart(t *testing.T) {
	runes := []rune("xe\u0301y") // x, e, combining acute, y
	if got := LineVisualCol(runes, 2); got != 1 {
		t.Errorf("interior of the é cluster = %d, want 1", got)
	}
	if got := LineVisualCol(runes, 3); got != 2 {
		t.Errorf("after the é cluster = %d, want 2", got)
	}
}

// TestRuneColAtVisual_WideGlyphs is the click side of the same math: both
// cells of an ideograph belong to it, and a click past a run of them has
// to count cells rather than runes or it lands on the wrong character.
func TestRuneColAtVisual_WideGlyphs(t *testing.T) {
	runes := []rune("日本語x")
	cases := []struct {
		visCol, want int
	}{
		{0, 0}, // left half of 日
		{1, 0}, // right half of 日 snaps back to it
		{2, 1}, // left half of 本
		{3, 1},
		{4, 2},
		{6, 3}, // the 'x' after three wide glyphs
		{7, 4}, // end-of-line
	}
	for _, c := range cases {
		if got := RuneColAtVisual(runes, c.visCol); got != c.want {
			t.Errorf("RuneColAtVisual(visCol=%d) = %d, want %d", c.visCol, got, c.want)
		}
	}
}

// TestRuneColAtVisual_NeverLandsInsideACluster checks the click contract
// for combining marks: whatever cell is hit, the answer is a rune index a
// caret may legally occupy.
func TestRuneColAtVisual_NeverLandsInsideACluster(t *testing.T) {
	runes := []rune("e\u0301😀\u0301") // é then an emoji wearing a mark
	for visCol := range 8 {
		got := RuneColAtVisual(runes, visCol)
		if got != ClusterStart(runes, got) {
			t.Errorf("visCol %d resolved to %d, which is inside a cluster", visCol, got)
		}
	}
}

// TestLineVisualCol walks through a line containing tabs and confirms
// the visual column at each rune position. "\t\tfoo" should put 'f' at
// visual col 8 — two full tab stops in.
func TestLineVisualCol(t *testing.T) {
	runes := []rune("\t\tfoo")
	cases := []struct {
		runeCol, want int
	}{
		{0, 0}, // before first tab
		{1, 4}, // after first tab
		{2, 8}, // after second tab
		{3, 9}, // after 'f'
		{5, 11},
	}
	for _, c := range cases {
		if got := LineVisualCol(runes, c.runeCol); got != c.want {
			t.Errorf("LineVisualCol(runeCol=%d) = %d, want %d", c.runeCol, got, c.want)
		}
	}
}

// TestLineVisualCol_ClampsBeyondEnd lets callers pass a past-the-end
// column without worrying about bounds — the function returns the
// total visual width.
func TestLineVisualCol_ClampsBeyondEnd(t *testing.T) {
	if got := LineVisualCol([]rune("ab"), 99); got != 2 {
		t.Fatalf("expected clamp to length, got %d", got)
	}
}

// TestRuneColAtVisual_HitInsideTab snaps clicks anywhere in a tab's
// visual span back to the tab's rune index — clicking on cell 2 of a
// 4-cell tab should select rune 0 (the tab itself), not "between" it.
func TestRuneColAtVisual_HitInsideTab(t *testing.T) {
	runes := []rune("\tfoo")
	for visCol := 0; visCol < 4; visCol++ {
		if got := RuneColAtVisual(runes, visCol); got != 0 {
			t.Errorf("visCol=%d should snap to rune 0, got %d", visCol, got)
		}
	}
	if got := RuneColAtVisual(runes, 4); got != 1 {
		t.Errorf("visCol=4 should be rune 1 (the 'f'), got %d", got)
	}
}

// TestRuneColAtVisual_BeyondEnd returns the rune count so callers can
// place the cursor at end-of-line.
func TestRuneColAtVisual_BeyondEnd(t *testing.T) {
	runes := []rune("ab")
	if got := RuneColAtVisual(runes, 99); got != len(runes) {
		t.Fatalf("expected end-of-line index, got %d", got)
	}
}

// TestRuneColAtVisual_NegativeIsZero clamps left-of-line to col 0 — a
// guard for tests and any caller that subtracts a scroll offset.
func TestRuneColAtVisual_NegativeIsZero(t *testing.T) {
	if got := RuneColAtVisual([]rune("ab"), -3); got != 0 {
		t.Fatalf("expected 0 for negative input, got %d", got)
	}
}

// TestDetectIndent_TabsWin returns "\t" when the file is dominated by
// tab-indented lines.
func TestDetectIndent_TabsWin(t *testing.T) {
	lines := []string{
		"package x",
		"",
		"\tfunc x() {",
		"\t\treturn 1",
		"\t}",
	}
	if got := DetectIndent(lines, "x.go"); got != "\t" {
		t.Fatalf("expected tab indent, got %q", got)
	}
}

// TestDetectIndent_SpacesWin_FourWide infers an indent width from the
// smallest non-zero leading-space count. Four spaces here is the most
// common indented prefix, so that's the unit.
func TestDetectIndent_SpacesWin_FourWide(t *testing.T) {
	lines := []string{
		"def foo():",
		"    if x:",
		"        return 1",
		"    return 0",
	}
	if got := DetectIndent(lines, "foo.py"); got != "    " {
		t.Fatalf("expected 4-space indent, got %q", got)
	}
}

// TestDetectIndent_SpacesWin_TwoWide proves the smallest-leading-spaces
// heuristic: a file with 2/4/6 indents should infer width 2.
func TestDetectIndent_SpacesWin_TwoWide(t *testing.T) {
	lines := []string{
		"function foo() {",
		"  if (x) {",
		"    return 1;",
		"  }",
		"}",
	}
	if got := DetectIndent(lines, "foo.js"); got != "  " {
		t.Fatalf("expected 2-space indent, got %q", got)
	}
}

// TestDetectIndent_NoSignal_GoDefaultsToTab covers the empty-file path
// for Go, where the convention is so strong (gofmt enforces tabs) that
// guessing spaces would just annoy the user.
func TestDetectIndent_NoSignal_GoDefaultsToTab(t *testing.T) {
	if got := DetectIndent([]string{""}, "new.go"); got != "\t" {
		t.Fatalf("empty .go should default to tab, got %q", got)
	}
}

// TestDetectIndent_NoSignal_MakefileDefaultsToTab covers the Makefile
// case, where tabs aren't a preference — they're literally required by
// make for recipe lines.
func TestDetectIndent_NoSignal_MakefileDefaultsToTab(t *testing.T) {
	if got := DetectIndent([]string{""}, "Makefile"); got != "\t" {
		t.Fatalf("empty Makefile should default to tab, got %q", got)
	}
}

// TestDetectIndent_NoSignal_DefaultsToFourSpaces is the catch-all path
// for a brand-new buffer with no extension hint.
func TestDetectIndent_NoSignal_DefaultsToFourSpaces(t *testing.T) {
	if got := DetectIndent([]string{""}, "untitled.txt"); got != strings.Repeat(" ", 4) {
		t.Fatalf("unknown extension should default to 4 spaces, got %q", got)
	}
}

// TestDetectIndent_MixedFavorsMajority decides ties on dominant count.
// A file with 5 tab lines and 1 space line is conceptually a tab file;
// the user opening it shouldn't have spaces inserted that would mix
// into the rest.
func TestDetectIndent_MixedFavorsMajority(t *testing.T) {
	lines := []string{
		"\tone",
		"\ttwo",
		"\tthree",
		"\tfour",
		"\tfive",
		"  oddball",
	}
	if got := DetectIndent(lines, "x.txt"); got != "\t" {
		t.Fatalf("expected tab from majority, got %q", got)
	}
}

// TestAutoIndentFor_CopiesLeadingWhitespace covers the base rule: the new
// line opens with whatever whitespace the old one opened with, in the same
// characters. Mixing tabs into a space-indented file is the bug this whole
// mechanism exists to avoid, so the copy is verbatim rather than
// re-derived from the indent unit.
func TestAutoIndentFor_CopiesLeadingWhitespace(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		unit   string
		path   string
		want   string
	}{
		{"no indent", "foo", "    ", "a.txt", ""},
		{"spaces", "    foo", "    ", "a.txt", "    "},
		{"tabs", "\t\tfoo", "\t", "a.go", "\t\t"},
		{"odd width is preserved", "   foo", "  ", "a.txt", "   "},
		{"mixed leading run", "\t  foo", "\t", "a.go", "\t  "},
		{"whitespace-only line", "      ", "    ", "a.txt", "      "},
		{"empty line", "", "    ", "a.txt", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoIndentFor([]rune(tc.prefix), tc.unit, tc.path); got != tc.want {
				t.Errorf("autoIndentFor(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}
}

// TestAutoIndentFor_AddsLevelAfterOpener checks the one piece of cleverness
// the rule allows: a line that opens a block gets its successor pushed one
// unit deeper, in the file's own unit rather than a hardcoded width.
func TestAutoIndentFor_AddsLevelAfterOpener(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		unit   string
		path   string
		want   string
	}{
		{"brace with tabs", "\tif x {", "\t", "a.go", "\t\t"},
		{"brace with spaces", "  if x {", "  ", "a.js", "    "},
		{"bracket", "list = [", "    ", "a.txt", "    "},
		{"paren", "call(", "    ", "a.txt", "    "},
		{"trailing space after opener", "\tif x {  ", "\t", "a.go", "\t\t"},
		{"closer does not add", "\t}", "\t", "a.go", "\t"},
		{"plain statement", "\tx := 1", "\t", "a.go", "\t"},
		{"python colon", "  if x:", "  ", "a.py", "    "},
		{"yaml colon", "  key:", "  ", "conf.yaml", "    "},
		{"go label colon does not add", "loop:", "\t", "a.go", ""},
		{"empty unit falls back", "if x {", "", "a.txt", defaultSpaceIndent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoIndentFor([]rune(tc.prefix), tc.unit, tc.path); got != tc.want {
				t.Errorf("autoIndentFor(%q, unit=%q, %s) = %q, want %q",
					tc.prefix, tc.unit, tc.path, got, tc.want)
			}
		})
	}
}

// TestColonOpensBlock keeps the colon rule scoped to the languages where a
// trailing colon really does introduce a block. Everywhere else it is a
// label or a map value and indenting after it would be actively wrong.
func TestColonOpensBlock(t *testing.T) {
	for _, path := range []string{"a.py", "a.pyi", "stubs.PYW", "c.yml", "c.yaml"} {
		if !colonOpensBlock(path) {
			t.Errorf("%s: colon should open a block", path)
		}
	}
	for _, path := range []string{"a.go", "a.c", "a.js", "a.txt", "Makefile", ""} {
		if colonOpensBlock(path) {
			t.Errorf("%s: colon should not open a block", path)
		}
	}
}
