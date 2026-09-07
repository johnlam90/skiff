// =============================================================================
// File: internal/mdrender/mdrender.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package mdrender turns markdown source into pre-wrapped, theme-styled
// terminal lines for skiff's read-only preview mode — the glow idea,
// rendered skiff-natively: goldmark parses (pure Go, dependency-free),
// the theme supplies every color, fenced code blocks run through the
// same Chroma path the editor highlights with, and wrapping walks
// grapheme clusters so CJK and emoji stay inside the budget.
//
// The output contract mirrors the editor's own render idiom: a slice of
// line strings plus a parallel per-rune style grid, already wrapped to
// the requested width — the consumer draws them verbatim and never
// re-wraps. Rendering is pure (no screen, no app state), which is what
// keeps it testable and lets the app cache the result until the source
// or the width changes.
package mdrender

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/theme"
	"github.com/rivo/uniseg"
)

// boldAttr / underlineAttr name the tcell attributes the renderer
// hands out, so tests assert against the same constants the styles are
// built from.
const (
	boldAttr      = tcell.AttrBold
	underlineAttr = tcell.AttrUnderline
)

// Render converts markdown source into terminal lines wrapped to width
// (prose measure and block budget alike). See RenderWide for the two-width
// form the preview uses; this one-width form is what a caller with no
// spare room wants, and what every existing test pins.
func Render(src []byte, width int, th theme.Theme) ([]string, [][]tcell.Style) {
	return RenderWide(src, width, width, th)
}

// RenderWide converts markdown source into terminal lines: prose wraps
// at width (the measure — text past ~100 cells is hard to track from
// one line to the next), while tables, code blocks and their rules may
// run out to wide, the room the pane actually has. A table that fits
// the pane is never ellipsised to satisfy the prose measure. Styles are
// parallel to lines and carry all coloring; a width under 4 is clamped
// so degenerate panes still get output, and wide never goes under
// width.
func RenderWide(src []byte, width, wide int, th theme.Theme) ([]string, [][]tcell.Style) {
	if width < 4 {
		width = 4
	}
	if wide < width {
		wide = width
	}
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).
		Parser().Parse(gmtext.NewReader(src))
	r := &renderer{src: src, th: th, width: width, wide: wide}
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		r.block(c, "", r.base())
		r.blank()
	}
	r.flush()
	// Drop a trailing blank so the document doesn't end on padding.
	for len(r.lines) > 0 && r.lines[len(r.lines)-1] == "" {
		r.lines = r.lines[:len(r.lines)-1]
		r.styles = r.styles[:len(r.styles)-1]
	}
	return r.lines, r.styles
}

// renderer accumulates styled runes into width-bounded lines. indent is
// the prefix every wrapped continuation of the current block repeats
// (list hang, quote gutter), carried as pre-styled runes.
type renderer struct {
	src   []byte
	th    theme.Theme
	width int
	// wide is the block budget: what tables, code blocks and their
	// rules may use. At least width; larger when the pane has room the
	// prose measure deliberately leaves unused.
	wide int

	lines  []string
	styles [][]tcell.Style

	cur      []rune
	curSt    []tcell.Style
	curW     int
	indent   []rune
	indentSt []tcell.Style
}

// base returns the body-text style every block starts from.
func (r *renderer) base() tcell.Style {
	return tcell.StyleDefault.Background(r.th.BG).Foreground(r.th.Text)
}

// dim returns the secondary style used for URLs, placeholders, raw
// HTML and rules. Muted rather than Subtle on purpose: a link's
// destination and an image's alt text are read, not glanced at, so
// they sit on the palette's 4.5:1 text tier — Subtle only clears the
// 3:1 graphics floor and is for lines and borders.
func (r *renderer) dim() tcell.Style {
	return tcell.StyleDefault.Background(r.th.BG).Foreground(r.th.Muted)
}

// block renders one block node. indent is the continuation prefix the
// caller's structure demands (already applied to r.indent by the time
// text flows); st is the inherited text style.
func (r *renderer) block(n ast.Node, hang string, st tcell.Style) {
	switch b := n.(type) {
	case *ast.Heading:
		r.heading(b)
	case *ast.Paragraph, *ast.TextBlock:
		r.inlines(n, st)
		r.flush()
	case *ast.Blockquote:
		r.pushIndent("│ ", r.dim())
		for c := b.FirstChild(); c != nil; c = c.NextSibling() {
			r.block(c, hang, st)
		}
		r.popIndent("│ ")
	case *ast.List:
		i := 1
		for c := b.FirstChild(); c != nil; c = c.NextSibling() {
			marker := "• "
			if b.IsOrdered() {
				marker = fmt.Sprintf("%d. ", b.Start+i-1)
			}
			r.item(c, marker, st)
			i++
		}
	case *ast.FencedCodeBlock:
		r.codeBlock(b)
	case *extast.Table:
		r.table(b, st)
	case *ast.CodeBlock:
		r.codeLines(rawLines(r.src, n), nil)
	case *ast.ThematicBreak:
		r.emitLine(strings.Repeat("─", r.contentWidth()), r.dim())
	case *ast.HTMLBlock:
		// Raw HTML has no terminal rendering; show it dimmed verbatim
		// rather than silently dropping content.
		for _, l := range rawLines(r.src, n) {
			r.emitLine(l, r.dim())
		}
	default:
		if n.Type() == ast.TypeBlock {
			r.inlines(n, st)
			r.flush()
		}
	}
}

// heading renders one heading with a level cue that survives a
// monochrome terminal — six levels used to collapse onto two colours,
// Accent for 1-2 and AccentSoft for 3-6. The scheme, top to bottom:
//
//	H1  Accent bold, then a heavy full-width rule (━) in Accent
//	H2  Accent bold, then a light full-width rule (─) in Muted
//	H3+ AccentSoft bold, prefixed by one › per level past two
//	    ("› " for H3, "›› " for H4, …)
//
// Rules are rows of their own, the full content width, so an H1 reads
// as a title bar and an H2 as a section break even when every colour
// has degraded to the terminal default; the chevron prefix keeps H3–H6
// apart from body text and from each other the same way.
//
// The opening flush is flushBlock, not flush: `- # Title` arrives with
// the list marker already on the accumulating row, and flushing that
// painted a lone • above the heading (a bare │ for `> # Title`).
func (r *renderer) heading(b *ast.Heading) {
	r.flushBlock()
	fg := r.th.Accent
	if b.Level > 2 {
		fg = r.th.AccentSoft
	}
	hst := tcell.StyleDefault.Background(r.th.BG).Foreground(fg).Bold(true)
	if b.Level > 2 {
		r.append(strings.Repeat("›", b.Level-2)+" ", hst)
	}
	r.inlines(b, hst)
	r.flush()
	switch b.Level {
	case 1:
		r.emitLine(strings.Repeat("━", r.contentWidth()), tcell.StyleDefault.Background(r.th.BG).Foreground(r.th.Accent))
	case 2:
		r.emitLine(strings.Repeat("─", r.contentWidth()), tcell.StyleDefault.Background(r.th.BG).Foreground(r.th.Muted))
	}
}

// item renders one list item: the marker on its first line, hanging
// indent for everything after, and a blank-line-free join between the
// item's own blocks (tight lists read as single lines).
func (r *renderer) item(it ast.Node, marker string, st tcell.Style) {
	r.flush()
	hang := strings.Repeat(" ", uniseg.StringWidth(marker))
	r.pushIndent(hang, st)
	// The first line carries the marker where continuations carry the
	// hang spaces — same width by construction, so wrap math is shared.
	// Only this item's hang is swapped for the marker: the outer levels'
	// prefix pushIndent seeded stays, which is what indents a nested
	// item under its parent instead of flattening every level onto
	// column 0.
	mst := st.Foreground(r.th.AccentSoft)
	keep := len(r.cur) - len([]rune(hang))
	r.cur = r.cur[:keep]
	r.curSt = r.curSt[:keep]
	for _, ru := range marker {
		r.cur = append(r.cur, ru)
		r.curSt = append(r.curSt, mst)
	}
	r.curW = uniseg.StringWidth(string(r.cur))
	first := true
	for c := it.FirstChild(); c != nil; c = c.NextSibling() {
		if !first {
			r.flush()
		}
		r.block(c, hang, st)
		first = false
	}
	r.popIndent(hang)
	r.flush()
}

// inlines walks a block's inline children, mapping emphasis, code
// spans, links and images onto styles.
func (r *renderer) inlines(n ast.Node, st tcell.Style) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch in := c.(type) {
		case *ast.Text:
			r.append(string(in.Segment.Value(r.src)), st)
			if in.SoftLineBreak() {
				r.append(" ", st)
			}
			if in.HardLineBreak() {
				r.flush()
			}
		case *ast.String:
			r.append(string(in.Value), st)
		case *ast.Emphasis:
			est := st.Italic(true)
			if in.Level >= 2 {
				est = st.Bold(true)
			}
			r.inlines(in, est)
		case *ast.CodeSpan:
			cst := tcell.StyleDefault.Background(r.th.LineHL).Foreground(r.th.SynString)
			r.inlines(in, cst)
		case *ast.Link:
			lst := st.Foreground(r.th.Accent).Underline(true)
			r.inlines(in, lst)
			if dest := string(in.Destination); dest != "" {
				r.append(" ("+dest+")", r.dim())
			}
		case *ast.AutoLink:
			r.append(string(in.URL(r.src)), st.Foreground(r.th.Accent).Underline(true))
		case *ast.Image:
			alt := string(nodeText(r.src, in))
			r.append("[image: "+alt+"]", r.dim())
		case *extast.Strikethrough:
			r.inlines(in, st.StrikeThrough(true))
		case *extast.TaskCheckBox:
			box := "☐ "
			if in.IsChecked {
				box = "☑ "
			}
			r.append(box, st.Foreground(r.th.AccentSoft))
		case *ast.RawHTML:
			// Inline HTML is shown dimmed rather than dropped.
			for i := 0; i < in.Segments.Len(); i++ {
				seg := in.Segments.At(i)
				r.append(string(seg.Value(r.src)), r.dim())
			}
		default:
			r.inlines(c, st)
		}
	}
}

// codeBlock highlights a fenced block through the editor's own Chroma
// path — a synthetic filename carries the fence's language, so the
// token→theme mapping is byte-identical to the editor's — and emits
// the lines on the LineHL surface the editor also uses for "this is a
// distinct region".
func (r *renderer) codeBlock(b *ast.FencedCodeBlock) {
	lines := rawLines(r.src, b)
	lang := string(b.Language(r.src))
	var grid [][]tcell.Style
	if lang != "" {
		grid = editor.Highlight("f."+lang, strings.Join(lines, "\n"), r.th)
	}
	r.codeLines(lines, grid)
}

// codeRail is the one-cell left edge every code row starts with — the
// block's margin, painted in Subtle on the code surface, so a block
// reads as a block even where its first line is blank.
const codeRail = '▏'

// codeTabStop is the cell width a tab expands to inside a code block.
// Four, matching the editor's own default for Go's gofmt-tabbed source
// — the block is read, not edited, so the exact stop only has to look
// like indentation.
const codeTabStop = 4

// codeLines emits pre-split code lines with an optional per-rune syntax
// grid as one rectangular block: every row starts with codeRail, is
// hard-wrapped at the width (code must not reflow at word boundaries —
// indentation is meaning), and is right-padded to the block's widest
// row so the LineHL surface forms a rectangle rather than a ragged
// right edge. A blank line inside the block is a blank ROW, rail and
// padding included — the accumulate/flush path dropped it, because
// flush treats an empty line as nothing to emit and trimmed trailing
// spaces off the rest. Rows go out through emitRow so a block inside a
// list item or a blockquote sits under the item's hang / behind the
// quote gutter like every other block row. Tabs expand to codeTabStop
// on the way in: uniseg measures a tab at zero cells, so left alone a
// tab-indented Go block painted flush against the rail.
func (r *renderer) codeLines(lines []string, grid [][]tcell.Style) {
	r.flushBlock()
	surface := r.th.LineHL
	plain := tcell.StyleDefault.Background(surface).Foreground(r.th.Text)
	rail := tcell.StyleDefault.Background(surface).Foreground(r.th.Subtle)
	textW := r.blockWidth() - 1 // the rail's cell
	if textW < 1 {
		textW = 1
	}
	// Split every line into rows of at most textW cells, whole clusters
	// only, carrying each rune's grid index for its style.
	type codeRow struct {
		runes []rune
		sts   []tcell.Style
		width int
	}
	var rows []codeRow
	blockW := 0
	for i, l := range lines {
		var sts []tcell.Style
		if grid != nil && i < len(grid) {
			sts = grid[i]
		}
		row := codeRow{}
		j := 0 // rune index into l, for the grid lookup
		for rest := l; rest != ""; {
			cluster, tail, cw, _ := uniseg.FirstGraphemeClusterInString(rest, -1)
			// One source rune per grid entry, however many cells the
			// cluster (or the expanded tab) paints.
			consumed := len([]rune(cluster))
			if cluster == "\t" {
				cw = codeTabStop - row.width%codeTabStop
				cluster = strings.Repeat(" ", cw)
			}
			if row.width+cw > textW && row.width > 0 {
				rows = append(rows, row)
				blockW = max(blockW, row.width)
				row = codeRow{}
			}
			st := plain
			if sts != nil && j < len(sts) {
				st = sts[j].Background(surface)
			}
			for _, ru := range cluster {
				row.runes = append(row.runes, ru)
				row.sts = append(row.sts, st)
			}
			row.width += cw
			j += consumed
			rest = tail
		}
		rows = append(rows, row)
		blockW = max(blockW, row.width)
	}
	if blockW > textW {
		blockW = textW
	}
	for _, row := range rows {
		runes := append([]rune{codeRail}, row.runes...)
		sts := append([]tcell.Style{rail}, row.sts...)
		for w := row.width; w < blockW; w++ {
			runes = append(runes, ' ')
			sts = append(sts, plain)
		}
		r.emitRow(runes, sts)
	}
}

// styledCell is one captured table cell: its runes with their styles,
// plus the cell's cluster width, measured once.
type styledCell struct {
	runes  []rune
	styles []tcell.Style
	width  int
}

// captureInlines renders a node's inline children into a detached
// rune/style pair instead of the flowing document — how table cells are
// measured before the column layout decides where they land. The
// renderer's accumulation state is swapped out and restored around the
// walk; the huge temporary width means nothing flushes mid-capture.
func (r *renderer) captureInlines(n ast.Node, st tcell.Style) styledCell {
	savedCur, savedSt, savedW := r.cur, r.curSt, r.curW
	savedWidth, savedIndent, savedIndentSt := r.width, r.indent, r.indentSt
	savedLines, savedStyles := r.lines, r.styles
	r.cur, r.curSt, r.curW = nil, nil, 0
	r.width, r.indent, r.indentSt = 1<<30, nil, nil
	r.inlines(n, st)
	cell := styledCell{runes: r.cur, styles: r.curSt, width: r.curW}
	r.cur, r.curSt, r.curW = savedCur, savedSt, savedW
	r.width, r.indent, r.indentSt = savedWidth, savedIndent, savedIndentSt
	r.lines, r.styles = savedLines, savedStyles
	return cell
}

// table lays a GFM table out as aligned columns: header bold, a rule
// under it, one line per row, columns separated by a dim │. Columns
// size to their widest cell; when the sum overflows the budget the
// widest columns shrink first and their cells truncate with an
// ellipsis — a readable narrow table beats a correct overflowing one.
func (r *renderer) table(t *extast.Table, st tcell.Style) {
	r.flushBlock()
	var rows [][]styledCell
	header := -1
	for tr := t.FirstChild(); tr != nil; tr = tr.NextSibling() {
		var row []styledCell
		for c := tr.FirstChild(); c != nil; c = c.NextSibling() {
			cst := st
			if _, ok := tr.(*extast.TableHeader); ok {
				cst = st.Bold(true)
			}
			row = append(row, r.captureInlines(c, cst))
		}
		if _, ok := tr.(*extast.TableHeader); ok {
			header = len(rows)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	widths := make([]int, cols)
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], c.width)
		}
	}
	// Shrink the widest column one cell at a time until the row fits;
	// the floor keeps every column identifiable.
	const sepW, floor = 3, 5
	total := func() int {
		s := (cols - 1) * sepW
		for _, w := range widths {
			s += w
		}
		return s
	}
	for total() > r.blockWidth() {
		wi, ww := -1, floor
		for i, w := range widths {
			if w > ww {
				wi, ww = i, w
			}
		}
		if wi < 0 {
			break // every column at the floor: emit anyway, clipped by draw
		}
		widths[wi]--
	}
	sepSt := r.dim()
	for ri, row := range rows {
		var runes []rune
		var sts []tcell.Style
		for i := 0; i < cols; i++ {
			var c styledCell
			if i < len(row) {
				c = row[i]
			}
			cr, cs := fitCell(c, widths[i], st)
			runes = append(runes, cr...)
			sts = append(sts, cs...)
			if i < cols-1 {
				for _, sr := range " │ " {
					runes = append(runes, sr)
					sts = append(sts, sepSt)
				}
			}
		}
		r.emitRow(runes, sts)
		if ri == header {
			r.emitLine(strings.Repeat("─", min(total(), r.blockWidth())), r.dim())
		}
	}
}

// fitCell truncates or pads one captured cell to exactly w cells,
// ellipsising a cut so a shrunken column still says it lost something.
func fitCell(c styledCell, w int, pad tcell.Style) ([]rune, []tcell.Style) {
	var runes []rune
	var sts []tcell.Style
	used := 0
	if c.width <= w {
		runes = append(runes, c.runes...)
		sts = append(sts, c.styles...)
		used = c.width
	} else {
		for i, ru := range c.runes {
			rw := uniseg.StringWidth(string(ru))
			if used+rw > w-1 {
				break
			}
			runes = append(runes, ru)
			sts = append(sts, c.styles[i])
			used += rw
		}
		runes = append(runes, '…')
		sts = append(sts, pad.Foreground(tcell.ColorDefault).Dim(true))
		used++
	}
	for used < w {
		runes = append(runes, ' ')
		sts = append(sts, pad)
		used++
	}
	return runes, sts
}

// blank emits one empty separator line unless the document is already
// at one — block spacing without double gaps.
func (r *renderer) blank() {
	r.flush()
	if len(r.lines) > 0 && r.lines[len(r.lines)-1] != "" {
		r.lines = append(r.lines, "")
		r.styles = append(r.styles, nil)
	}
}

// pushIndent appends a styled continuation prefix and starts the
// current line with it; popIndent removes it again.
func (r *renderer) pushIndent(s string, st tcell.Style) {
	r.flush()
	for _, ru := range s {
		r.indent = append(r.indent, ru)
		r.indentSt = append(r.indentSt, st)
	}
	r.startLine()
}

// popIndent trims the most recent pushIndent's runes off the prefix.
func (r *renderer) popIndent(s string) {
	r.flush()
	n := len([]rune(s))
	r.indent = r.indent[:len(r.indent)-n]
	r.indentSt = r.indentSt[:len(r.indentSt)-n]
}

// startLine seeds the accumulating line with the active indent prefix.
func (r *renderer) startLine() {
	if len(r.cur) == 0 && len(r.indent) > 0 {
		r.cur = append(r.cur, r.indent...)
		r.curSt = append(r.curSt, r.indentSt...)
		r.curW = uniseg.StringWidth(string(r.indent))
	}
}

// append adds text in one style, greedy-word-wrapping at the width.
// Words wider than a whole line fall back to per-cluster hard wrap so a
// long URL can't force an overflow.
func (r *renderer) append(s string, st tcell.Style) {
	r.startLine()
	for _, word := range splitKeepSpaces(s) {
		w := uniseg.StringWidth(word)
		if r.curW+w > r.width && r.curW > len(r.indent) {
			r.flush()
			r.startLine()
			if strings.TrimSpace(word) == "" {
				continue // a wrap swallows the breaking space
			}
		}
		if w > r.width-len(r.indent) {
			for _, ru := range word {
				r.appendRune(ru, st, true)
			}
			continue
		}
		for _, ru := range word {
			r.appendRune(ru, st, false)
		}
	}
}

// appendRune adds one rune; hardWrap flushes mid-word when the next
// cluster would overflow (code lines, oversized words).
func (r *renderer) appendRune(ru rune, st tcell.Style, hardWrap bool) {
	r.startLine()
	w := uniseg.StringWidth(string(ru))
	if r.curW+w > r.width {
		if !hardWrap && len(r.cur) >= r.width {
			// Soft content past the budget without a wrap opportunity:
			// flush anyway rather than overflow.
			r.flush()
			r.startLine()
		} else {
			r.flush()
			r.startLine()
		}
	}
	r.cur = append(r.cur, ru)
	r.curSt = append(r.curSt, st)
	r.curW += w
}

// flush commits the accumulating line, if any, to the output.
func (r *renderer) flush() {
	if len(r.cur) == 0 {
		return
	}
	// Trailing spaces carry no information and can push Width past the
	// budget assertion without painting anything.
	for len(r.cur) > 0 && r.cur[len(r.cur)-1] == ' ' {
		r.cur = r.cur[:len(r.cur)-1]
		r.curSt = r.curSt[:len(r.curSt)-1]
	}
	if len(r.cur) == 0 {
		r.curW = 0
		return
	}
	r.lines = append(r.lines, string(r.cur))
	r.styles = append(r.styles, append([]tcell.Style(nil), r.curSt...))
	r.cur, r.curSt, r.curW = nil, nil, 0
}

// emitLine appends one whole pre-built line in a single style, behind
// the active block prefix.
func (r *renderer) emitLine(s string, st tcell.Style) {
	sts := make([]tcell.Style, len([]rune(s)))
	for i := range sts {
		sts[i] = st
	}
	r.emitRow([]rune(s), sts)
}

// emitRow is the one door for a pre-built block row (code, table,
// rule): it lands behind the block prefix the flowing text would have
// carried — the accumulating row when that holds only a list marker
// or the indent, so `- ` followed by a fence keeps its bullet, the
// indent alone otherwise. Appending straight to r.lines is what
// dropped a fenced block inside a list item onto column 0.
func (r *renderer) emitRow(runes []rune, sts []tcell.Style) {
	r.flushBlock()
	prefix, prefixSt := r.indent, r.indentSt
	if len(r.cur) > 0 {
		prefix, prefixSt = r.cur, r.curSt
		r.cur, r.curSt, r.curW = nil, nil, 0
	}
	line := append(append([]rune(nil), prefix...), runes...)
	styles := append(append([]tcell.Style(nil), prefixSt...), sts...)
	r.lines = append(r.lines, string(line))
	r.styles = append(r.styles, styles)
}

// prefixOnly reports whether the accumulating row holds nothing but
// the block prefix — the indent startLine seeded, or a list marker in
// its place — so the next block should join it rather than flush it
// out as a row of its own.
func (r *renderer) prefixOnly() bool {
	return len(r.cur) > 0 && r.curW <= uniseg.StringWidth(string(r.indent))
}

// flushBlock is the flush a block start wants: commit any real text on
// the accumulating row, but leave a prefix-only row open for the block
// to land on.
func (r *renderer) flushBlock() {
	if !r.prefixOnly() {
		r.flush()
	}
}

// contentWidth is the cells left of the budget after the active block
// prefix — what a rule or a code row may span so it ends where the
// wrapped text does.
func (r *renderer) contentWidth() int {
	w := r.width - uniseg.StringWidth(string(r.indent))
	if w < 1 {
		w = 1
	}
	return w
}

// blockWidth is the budget for a table or code block inside the current
// indent: wide minus the indent, never under one cell.
func (r *renderer) blockWidth() int {
	w := r.wide - uniseg.StringWidth(string(r.indent))
	if w < 1 {
		w = 1
	}
	return w
}

// rawLines returns a block node's source lines without trailing
// newlines — the shape codeLines and the HTML fallback consume.
func rawLines(src []byte, n ast.Node) []string {
	var out []string
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		out = append(out, strings.TrimRight(string(seg.Value(src)), "\n"))
	}
	return out
}

// nodeText flattens a node's text content — how image alt text is
// recovered from the inline children goldmark parses it into.
func nodeText(src []byte, n ast.Node) []byte {
	var out []byte
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			out = append(out, t.Segment.Value(src)...)
		case *ast.String:
			out = append(out, t.Value...)
		default:
			out = append(out, nodeText(src, c)...)
		}
	}
	return out
}

// splitKeepSpaces splits s into alternating word / whitespace tokens so
// the wrapper can break between words while preserving intra-text
// spacing exactly.
func splitKeepSpaces(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, ru := range s {
		isSp := ru == ' ' || ru == '\t'
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
