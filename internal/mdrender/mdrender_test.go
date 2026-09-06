// =============================================================================
// File: internal/mdrender/mdrender_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the markdown preview renderer: markdown source in,
// pre-wrapped theme-styled terminal lines out. Assertions read the
// returned line strings plus spot-check the per-rune style grid, since
// the styles are the whole point of rendering instead of showing raw
// markdown.

package mdrender

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/textdraw"
	"github.com/johnlam90/skiff/internal/theme"
)

// findLine returns the index of the first rendered line containing sub,
// or -1 — the shared lookup every assertion here starts from.
func findLine(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// TestRender_HeadingStyledAccentBold pins the heading treatment: the
// text survives without its # markers and takes the theme's accent in
// bold — the "this is a rendered document now" signal.
func TestRender_HeadingStyledAccentBold(t *testing.T) {
	th := theme.Default()
	lines, styles := Render([]byte("# Title\n\nbody text\n"), 60, th)
	i := findLine(lines, "Title")
	if i < 0 {
		t.Fatalf("no heading line in %q", lines)
	}
	if strings.Contains(lines[i], "#") {
		t.Fatalf("heading kept its marker: %q", lines[i])
	}
	j := strings.IndexRune(lines[i], 'T')
	fg, _, attrs := styles[i][j].Decompose()
	if fg != th.Accent {
		t.Fatalf("heading fg = %v, want Accent", fg)
	}
	if attrs&boldAttr == 0 {
		t.Fatal("heading should be bold")
	}
}

// TestRender_ParagraphWrapsToWidth pins the reflow contract: no
// rendered line may exceed the requested cell width, measured with the
// same cluster-aware Width the chrome draws with.
func TestRender_ParagraphWrapsToWidth(t *testing.T) {
	src := []byte("one two three four five six seven eight nine ten eleven twelve\n")
	lines, _ := Render(src, 20, theme.Default())
	if len(lines) < 3 {
		t.Fatalf("expected the paragraph to wrap, got %q", lines)
	}
	for _, l := range lines {
		if w := textdraw.Width(l); w > 20 {
			t.Fatalf("line %q is %d cells wide, budget 20", l, w)
		}
	}
}

// TestRender_ListAndQuote pins the two block decorations: bullets
// render as • with their text, and blockquote lines carry a │ gutter.
func TestRender_ListAndQuote(t *testing.T) {
	lines, _ := Render([]byte("- alpha\n- beta\n\n> quoted words\n"), 60, theme.Default())
	if i := findLine(lines, "alpha"); i < 0 || !strings.Contains(lines[i], "•") {
		t.Fatalf("bullet line missing: %q", lines)
	}
	if i := findLine(lines, "quoted words"); i < 0 || !strings.Contains(lines[i], "│") {
		t.Fatalf("quote gutter missing: %q", lines)
	}
}

// TestRender_FencedCodeGetsChromaColors pins the reuse contract: a
// fenced block with a language runs through the same highlighter the
// editor uses, so a Go keyword inside markdown gets the theme's keyword
// color, not plain text.
func TestRender_FencedCodeGetsChromaColors(t *testing.T) {
	th := theme.Default()
	src := []byte("intro\n\n```go\nfunc main() {}\n```\n")
	lines, styles := Render(src, 60, th)
	i := findLine(lines, "func main")
	if i < 0 {
		t.Fatalf("code line missing in %q", lines)
	}
	j := strings.Index(lines[i], "func")
	fg, _, _ := styles[i][j].Decompose()
	if fg == th.Text || fg == 0 {
		t.Fatalf("code keyword fg = %v, want a syntax color", fg)
	}
}

// TestRender_LinkAndImage pins the inline treatments: link text is
// underlined with its URL appended dimly, and an image collapses to a
// labelled placeholder instead of raw ![...](...) syntax.
func TestRender_LinkAndImage(t *testing.T) {
	th := theme.Default()
	lines, styles := Render([]byte("see [the docs](https://x.dev) here\n\n![diagram](a.png)\n"), 60, th)
	i := findLine(lines, "the docs")
	if i < 0 {
		t.Fatalf("link text missing in %q", lines)
	}
	if !strings.Contains(lines[i], "x.dev") {
		t.Fatalf("link URL not shown: %q", lines[i])
	}
	j := strings.Index(lines[i], "the docs")
	if _, _, attrs := styles[i][j].Decompose(); attrs&underlineAttr == 0 {
		t.Fatal("link text should be underlined")
	}
	if findLine(lines, "[image: diagram]") < 0 {
		t.Fatalf("image placeholder missing in %q", lines)
	}
	// The URL and the placeholder are read, not glanced at: they sit on
	// Muted (the 4.5:1 text tier), never on Subtle, which only clears
	// the 3:1 graphics floor and is reserved for rules and borders.
	u := strings.Index(lines[i], "x.dev")
	if fg, _, _ := styles[i][u].Decompose(); fg != th.Muted {
		t.Fatalf("link URL fg = %v, want Muted", fg)
	}
	k := findLine(lines, "[image: diagram]")
	if fg, _, _ := styles[k][0].Decompose(); fg != th.Muted {
		t.Fatalf("image placeholder fg = %v, want Muted", fg)
	}
}

// TestRender_CJKWrapStaysInBudget pins cluster safety: wide glyphs
// count as their real cell width when wrapping, so a CJK paragraph
// never overflows the budget or splits a glyph.
func TestRender_CJKWrapStaysInBudget(t *testing.T) {
	src := []byte(strings.Repeat("日本語テキスト ", 8) + "\n")
	lines, _ := Render(src, 12, theme.Default())
	for _, l := range lines {
		if w := textdraw.Width(l); w > 12 {
			t.Fatalf("CJK line %q is %d cells, budget 12", l, w)
		}
	}
}

// TestRender_TableRendersAlignedColumns pins the GFM table treatment —
// the bug that motivated it: without the extension, pipe rows collapsed
// into a mangled paragraph. A table must come out as aligned columns
// with a bold header, a separator rule, and one output line per row.
func TestRender_TableRendersAlignedColumns(t *testing.T) {
	th := theme.Default()
	src := []byte("| What | Address |\n|---|---|\n| Bastion | 100.82.16.4 |\n| DNS | 100.82.0.42 |\n")
	lines, styles := Render(src, 60, th)

	hi := findLine(lines, "What")
	if hi < 0 || !strings.Contains(lines[hi], "Address") {
		t.Fatalf("header row missing: %q", lines)
	}
	j := strings.Index(lines[hi], "What")
	if _, _, attrs := styles[hi][j].Decompose(); attrs&boldAttr == 0 {
		t.Fatal("header cells should be bold")
	}
	bi := findLine(lines, "Bastion")
	di := findLine(lines, "DNS")
	if bi < 0 || di < 0 {
		t.Fatalf("body rows missing: %q", lines)
	}
	// Columns align: the second column starts at the same x in each row.
	if strings.Index(lines[bi], "100.82.16.4") != strings.Index(lines[di], "100.82.0.42") {
		t.Fatalf("columns not aligned:\n%q\n%q", lines[bi], lines[di])
	}
	// A separator rule sits between header and body.
	if !strings.Contains(lines[hi+1], "─") {
		t.Fatalf("no separator under the header: %q", lines[hi+1])
	}
	// And no raw pipe-syntax leakage.
	if findLine(lines, "|---|") >= 0 {
		t.Fatalf("raw table syntax leaked: %q", lines)
	}
}

// TestRender_WideTableStaysInBudget pins the overflow rule: a table
// wider than the viewport shrinks its widest columns and truncates
// cells with an ellipsis instead of overflowing the width.
func TestRender_WideTableStaysInBudget(t *testing.T) {
	src := []byte("| A | B |\n|---|---|\n| " + strings.Repeat("longcell ", 12) + " | " + strings.Repeat("wide ", 10) + " |\n")
	lines, _ := Render(src, 40, theme.Default())
	for _, l := range lines {
		if w := textdraw.Width(l); w > 40 {
			t.Fatalf("table line %q is %d cells, budget 40", l, w)
		}
	}
	if findLine(lines, "…") < 0 {
		t.Fatalf("no ellipsis on truncated cells: %q", lines)
	}
}

// TestRender_TaskListAndStrikethrough pins the remaining GFM inlines a
// README actually uses: checkboxes render as glyphs and ~~struck~~
// text carries the strikethrough attribute.
func TestRender_TaskListAndStrikethrough(t *testing.T) {
	th := theme.Default()
	lines, styles := Render([]byte("- [x] done\n- [ ] todo\n\nthis is ~~gone~~ now\n"), 60, th)
	if findLine(lines, "☑") < 0 || findLine(lines, "☐") < 0 {
		t.Fatalf("task checkboxes missing: %q", lines)
	}
	i := findLine(lines, "gone")
	if i < 0 {
		t.Fatalf("strikethrough text missing: %q", lines)
	}
	j := strings.Index(lines[i], "gone")
	if _, _, attrs := styles[i][j].Decompose(); attrs&tcell.AttrStrikeThrough == 0 {
		t.Fatal("~~text~~ should carry StrikeThrough")
	}
}

// TestRender_HeadingLevelsAreToldApart pins the level scheme: an H1 is
// followed by a heavy full-width rule, an H2 by a light one, and H3+
// carry one › per level past two — so six levels stay distinguishable
// on a monochrome terminal where Accent and AccentSoft are the same
// colour. The old scheme collapsed all six onto two colours.
func TestRender_HeadingLevelsAreToldApart(t *testing.T) {
	th := theme.Default()
	src := "# One\n\n## Two\n\n### Three\n\n#### Four\n\nbody\n"
	lines, styles := Render([]byte(src), 40, th)
	i1 := findLine(lines, "One")
	if i1 < 0 || lines[i1+1] != strings.Repeat("━", 40) {
		t.Fatalf("H1 should be followed by a heavy full-width rule, got %q", lines[i1+1])
	}
	if fg, _, _ := styles[i1+1][0].Decompose(); fg != th.Accent {
		t.Fatalf("H1 rule fg = %v, want Accent", fg)
	}
	i2 := findLine(lines, "Two")
	if i2 < 0 || lines[i2+1] != strings.Repeat("─", 40) {
		t.Fatalf("H2 should be followed by a light full-width rule, got %q", lines[i2+1])
	}
	if fg, _, _ := styles[i2+1][0].Decompose(); fg != th.Muted {
		t.Fatalf("H2 rule fg = %v, want Muted", fg)
	}
	if i3 := findLine(lines, "Three"); i3 < 0 || lines[i3] != "› Three" {
		t.Fatalf("H3 = %q, want a single-chevron prefix", lines[findLine(lines, "Three")])
	}
	i4 := findLine(lines, "Four")
	if i4 < 0 || lines[i4] != "›› Four" {
		t.Fatalf("H4 = %q, want a double-chevron prefix", lines[i4])
	}
	if _, _, attrs := styles[i4][3].Decompose(); attrs&boldAttr == 0 {
		t.Fatal("H4 text should be bold")
	}
	if fg, _, _ := styles[i4][3].Decompose(); fg != th.AccentSoft {
		t.Fatalf("H4 fg = %v, want AccentSoft", fg)
	}
}

// TestRender_CodeBlockIsARectangle pins the fenced block's shape: a
// blank line inside the fence is a blank ROW (it used to be dropped),
// every row starts with the rail, every row is padded to the block's
// widest line so the LineHL surface forms a rectangle, and the padding
// carries that surface too.
func TestRender_CodeBlockIsARectangle(t *testing.T) {
	th := theme.Default()
	src := "```go\nfunc main() {\n\n\tx := 1\n}\n```\n"
	lines, styles := Render([]byte(src), 60, th)
	first := findLine(lines, "func main")
	if first < 0 {
		t.Fatalf("code missing in %q", lines)
	}
	rows := lines[first : first+4]
	if !strings.HasPrefix(rows[1], string(codeRail)) || strings.TrimSpace(rows[1][len(string(codeRail)):]) != "" {
		t.Fatalf("the blank line inside the fence should be a blank rail row, got %q", rows[1])
	}
	want := textdraw.Width(rows[0])
	for i, row := range rows {
		if !strings.HasPrefix(row, string(codeRail)) {
			t.Fatalf("row %d %q lacks the rail", i, row)
		}
		if w := textdraw.Width(row); w != want {
			t.Fatalf("row %d is %d cells, want the block width %d: %q", i, w, want, row)
		}
		last := styles[first+i][len(styles[first+i])-1]
		if _, bg, _ := last.Decompose(); bg != th.LineHL {
			t.Fatalf("row %d padding bg = %v, want LineHL", i, bg)
		}
	}
	if fg, _, _ := styles[first][0].Decompose(); fg != th.Subtle {
		t.Fatalf("rail fg = %v, want Subtle", fg)
	}
}

// TestRender_CodeBlockHardWrapsInsideTheBudget pins the wrap rule for
// code: a line wider than the budget breaks between clusters (never
// reflowing at spaces), and every resulting row — rail included — fits
// the width.
func TestRender_CodeBlockHardWrapsInsideTheBudget(t *testing.T) {
	th := theme.Default()
	src := "```\n" + strings.Repeat("abcdefghij", 5) + "\n```\n"
	lines, _ := Render([]byte(src), 20, th)
	rows := 0
	for _, l := range lines {
		if strings.HasPrefix(l, string(codeRail)) {
			rows++
			if w := textdraw.Width(l); w > 20 {
				t.Fatalf("code row %q is %d cells, budget 20", l, w)
			}
		}
	}
	if rows != 3 {
		t.Fatalf("50 cells over a 19-cell text column should take 3 rows, got %d: %q", rows, lines)
	}
}

// TestRender_CodeBlockInsideListKeepsIndent pins the block prefix on
// code rows: a fenced block inside a list item starts under the item's
// hang, rail included, instead of at column 0. The rows used to be
// appended straight to the output, bypassing the indent every other
// block row carries, so the block fell out of its list.
func TestRender_CodeBlockInsideListKeepsIndent(t *testing.T) {
	th := theme.Default()
	src := "- item\n\n  ```\n  code\n  ```\n\n- next\n"
	lines, styles := Render([]byte(src), 40, th)
	i := findLine(lines, "code")
	if i < 0 || lines[i] != "  "+string(codeRail)+"code" {
		t.Fatalf("code row = %q, want it under the item's hang", lines[i])
	}
	if fg, _, _ := styles[i][2].Decompose(); fg != th.Subtle {
		t.Fatalf("rail after the hang fg = %v, want Subtle", fg)
	}
	if j := findLine(lines, "next"); j < 0 || lines[j] != "• next" {
		t.Fatalf("the list should resume after the block, got %q", lines)
	}
}

// TestRender_CodeBlockInsideQuoteKeepsGutter pins the same prefix rule
// for blockquotes: every code row keeps the │ gutter, and hard-wrapped
// rows share the gutter with the first.
func TestRender_CodeBlockInsideQuoteKeepsGutter(t *testing.T) {
	th := theme.Default()
	src := "> quote\n>\n> ```\n> " + strings.Repeat("abcdefghij", 3) + "\n> ```\n"
	lines, _ := Render([]byte(src), 20, th)
	rows := 0
	for _, l := range lines {
		if strings.Contains(l, string(codeRail)) {
			rows++
			if !strings.HasPrefix(l, "│ "+string(codeRail)) {
				t.Fatalf("code row %q lost the quote gutter", l)
			}
			if w := textdraw.Width(l); w > 20 {
				t.Fatalf("code row %q is %d cells, budget 20", l, w)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("30 cells over a 17-cell text column should take 2 rows, got %d: %q", rows, lines)
	}
}

// TestRender_HeadingSharesTheMarkerRow pins `- # Title` and `> # Title`:
// the heading joins the row that holds the list marker or quote gutter
// instead of flushing that prefix out as a row of its own — which
// painted a lone • above the heading and a bare │ above a quoted one.
// The rules under H1/H2 carry the prefix too and fit inside it.
func TestRender_HeadingSharesTheMarkerRow(t *testing.T) {
	th := theme.Default()
	lines, _ := Render([]byte("- # Title\n"), 30, th)
	if i := findLine(lines, "Title"); i < 0 || lines[i] != "• Title" {
		t.Fatalf("list heading = %q, want the bullet on the same row", lines)
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "•" {
			t.Fatalf("a bare bullet row leaked out: %q", lines)
		}
	}
	lines, _ = Render([]byte("> # Title\n> body\n"), 30, th)
	i := findLine(lines, "Title")
	if i < 0 || lines[i] != "│ Title" {
		t.Fatalf("quoted heading = %q, want it on the gutter row", lines)
	}
	if lines[i+1] != "│ "+strings.Repeat("━", 28) {
		t.Fatalf("the H1 rule should sit inside the gutter, got %q", lines[i+1])
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "│" {
			t.Fatalf("a bare gutter row leaked out: %q", lines)
		}
	}
}

// TestRender_NestedListKeepsOuterIndent pins the marker row of a
// nested item: it starts under the parent's hang, so the levels read
// as a tree. The marker used to replace the whole seeded prefix, which
// flattened every level onto column 0.
func TestRender_NestedListKeepsOuterIndent(t *testing.T) {
	lines, _ := Render([]byte("- a\n  - b\n    - c\n"), 30, theme.Default())
	want := []string{"• a", "  • b", "    • c"}
	for _, w := range want {
		if findLine(lines, w) < 0 || lines[findLine(lines, w)] != w {
			t.Fatalf("nested list = %q, want %q", lines, want)
		}
	}
}

// TestRender_CodeTabsExpandToTabStops pins tab handling inside code:
// a tab is painted as spaces to the next 4-cell stop, so a tab-indented
// Go block keeps its indentation. uniseg measures a tab at 0 cells, so
// left alone the indent vanished and the rectangle's padding miscounted.
func TestRender_CodeTabsExpandToTabStops(t *testing.T) {
	lines, styles := Render([]byte("```\n\tx\na\tb\n```\n"), 30, theme.Default())
	i := findLine(lines, "x")
	if i < 0 || lines[i] != string(codeRail)+"    x" {
		t.Fatalf("tab-indented row = %q, want four cells of indent", lines[i])
	}
	if len(styles[i]) != len([]rune(lines[i])) {
		t.Fatalf("style grid %d runes vs line %d", len(styles[i]), len([]rune(lines[i])))
	}
	if j := findLine(lines, "a"); j < 0 || lines[j] != string(codeRail)+"a   b" {
		t.Fatalf("mid-line tab row = %q, want the next stop", lines[j])
	}
}

// TestRender_TableInsideQuoteKeepsGutter pins the prefix rule for the
// last block that wrote rows directly: a table inside a blockquote
// keeps the │ gutter on every row, header rule included.
func TestRender_TableInsideQuoteKeepsGutter(t *testing.T) {
	src := "> | a | b |\n> |---|---|\n> | 1 | 2 |\n"
	lines, _ := Render([]byte(src), 30, theme.Default())
	for _, l := range lines {
		if !strings.HasPrefix(l, "│ ") {
			t.Fatalf("table row %q lost the quote gutter: %q", l, lines)
		}
	}
	if findLine(lines, "a │ b") < 0 || findLine(lines, "1 │ 2") < 0 {
		t.Fatalf("table content missing: %q", lines)
	}
}
