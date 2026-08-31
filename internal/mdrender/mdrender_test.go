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
