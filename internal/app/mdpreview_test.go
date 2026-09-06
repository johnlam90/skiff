// =============================================================================
// File: internal/app/mdpreview_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the markdown preview mode: the ≡ View row's visibility
// gate, the toggle's render/teardown, the read-only key guard, wheel
// and arrow scrolling of the rendered view, the draw pass, and cache
// invalidation when the buffer reloads underneath the preview.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/textdraw"
	"github.com/johnlam90/skiff/internal/theme"
)

// seedMarkdownTab writes a small markdown file and opens it, returning
// the tab — the shared fixture for every preview test.
func seedMarkdownTab(t *testing.T, a *App, name, content string) *editor.Tab {
	t.Helper()
	path := filepath.Join(a.rootDir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return openTabAtPath(t, a, path)
}

// TestPreviewMarkdownRow_VisibleOnlyForMarkdownTabs pins the "visible
// when a .md file is selected" contract: the ≡ View row exists exactly
// when the active tab is a markdown file.
func TestPreviewMarkdownRow_VisibleOnlyForMarkdownTabs(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedMarkdownTab(t, a, "notes.md", "# Title\n\nhello\n")
	// labelFor rows carry no static label, so resolve dynamically the
	// way the draw pass does.
	var item menuItemDef
	found := false
	items, _, _ := a.menuLayout()
	for _, it := range items {
		if it.labelFor != nil && it.labelFor(a) == "Preview Markdown" {
			item, found = it, true
			break
		}
	}
	if !found {
		t.Fatal("Preview Markdown row not in the menu with a markdown tab active")
	}
	if item.visible == nil {
		t.Fatal("row must carry a visibility predicate")
	}
	if !item.visible(a) {
		t.Fatal("row hidden with a markdown tab active")
	}
	goPath := filepath.Join(a.rootDir, "x.go")
	if err := os.WriteFile(goPath, []byte("package x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, goPath)
	if item.visible(a) {
		t.Fatal("row visible with a Go tab active")
	}
}

// TestTogglePreviewMarkdown_RendersAndTearsDown pins the toggle's whole
// lifecycle: on renders the buffer into cached preview lines, the label
// flips, off drops the cache, and closing a previewing tab cleans its
// entry so the map can't leak across tab lifetimes.
func TestTogglePreviewMarkdown_RendersAndTearsDown(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "# Title\n\nhello world\n")

	a.menuTogglePreviewMarkdown()
	st := a.mdPreview[tab]
	if st == nil || len(st.lines) == 0 {
		t.Fatal("toggle on should render preview lines")
	}
	if got := a.previewMarkdownLabel(); got != "Edit Markdown" {
		t.Fatalf("label while previewing = %q", got)
	}
	a.menuTogglePreviewMarkdown()
	if a.mdPreview[tab] != nil {
		t.Fatal("toggle off should drop the preview state")
	}
	if got := a.previewMarkdownLabel(); got != "Preview Markdown" {
		t.Fatalf("label while editing = %q", got)
	}

	a.menuTogglePreviewMarkdown()
	a.closeTab(tab)
	if a.mdPreview[tab] != nil {
		t.Fatal("closing the tab must drop its preview state")
	}
}

// TestPreviewMarkdown_ReadOnlyKeysAndScroll pins the input contract
// while previewing: printable keys never reach the buffer, and the
// arrows scroll the rendered view instead of moving the caret.
func TestPreviewMarkdown_ReadOnlyKeysAndScroll(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	body := "# T\n\n"
	for range 100 {
		body += "para line words here\n\n"
	}
	tab := seedMarkdownTab(t, a, "notes.md", body)
	before := tab.Buffer.String()
	a.menuTogglePreviewMarkdown()

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if tab.Buffer.String() != before {
		t.Fatal("typing while previewing reached the buffer")
	}
	cursorBefore := tab.Cursor
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tab.Cursor != cursorBefore {
		t.Fatal("arrow moved the caret instead of the preview")
	}
	if a.mdPreview[tab].scroll == 0 {
		t.Fatal("Down should scroll the preview")
	}

	// The wheel scrolls the preview too, and never the buffer viewport.
	ex, _, _, _ := a.editorRect()
	sy := tab.ScrollY
	got := a.mdPreview[tab].scroll
	a.handleMouse(tcell.NewEventMouse(ex+3, 5, tcell.WheelDown, tcell.ModNone))
	if a.mdPreview[tab].scroll <= got {
		t.Fatal("wheel should scroll the preview")
	}
	if tab.ScrollY != sy {
		t.Fatal("wheel while previewing must not move the text viewport")
	}
}

// TestDrawPreviewMarkdown_PaintsRenderedText pins the draw pass: with
// the preview on, the screen shows the rendered heading text (no #
// marker) in the theme's accent, and the raw markdown is not painted.
func TestDrawPreviewMarkdown_PaintsRenderedText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedMarkdownTab(t, a, "notes.md", "# BigHeading\n\nplain body\n")
	a.menuTogglePreviewMarkdown()
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	found := false
	for y := 0; y < a.height; y++ {
		row := screenLine(scr, y)
		if strings.Contains(row, "# BigHeading") {
			t.Fatalf("raw markdown painted: %q", row)
		}
		if strings.Contains(row, "BigHeading") {
			found = true
			x := strings.Index(row, "BigHeading")
			cells, w, _ := scr.GetContents()
			fg, _, _ := cells[y*w+x].Style.Decompose()
			if fg != a.theme.Accent {
				t.Fatalf("heading fg = %v, want Accent", fg)
			}
		}
	}
	if !found {
		t.Fatal("rendered heading not on screen")
	}
}

// TestPreviewMarkdown_ReloadInvalidates pins the freshness contract: a
// silent external reload re-renders the preview, so the screen can
// never show stale content the buffer no longer holds.
func TestPreviewMarkdown_ReloadInvalidates(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "# Old\n")
	a.menuTogglePreviewMarkdown()
	if findPreviewLine(a, tab, "Old") < 0 {
		t.Fatal("fixture: preview should hold the old heading")
	}

	if err := os.WriteFile(tab.Path, []byte("# New\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	info, err := os.Stat(tab.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	a.reconcileTab(tab, tabProbe{path: tab.Path, mtime: info.ModTime().Add(1)})
	if findPreviewLine(a, tab, "New") < 0 || findPreviewLine(a, tab, "Old") >= 0 {
		t.Fatal("preview not re-rendered after the silent reload")
	}
}

// findPreviewLine returns the index of the first cached preview line
// containing sub, or -1 — including -1 when no preview is active.
func findPreviewLine(a *App, tab *editor.Tab, sub string) int {
	st := a.mdPreview[tab]
	if st == nil {
		return -1
	}
	for i, l := range st.lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// TestStatusBar_PreviewChip pins the subtle affordance: with a markdown
// tab in front the status bar shows a dim "Preview" chip, flipping to
// "Edit" while the preview is up — and no chip at all for other files.
func TestStatusBar_PreviewChip(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedMarkdownTab(t, a, "notes.md", "# T\n")
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	if !strings.Contains(screenLine(scr, a.height-1), "Preview") {
		t.Fatalf("status bar missing the Preview chip: %q", screenLine(scr, a.height-1))
	}

	a.menuTogglePreviewMarkdown()
	a.statusMsg = "" // the toggle's own flash also names the modes
	a.draw()
	scr.Show()
	// Judge only the right-hand group — the left side is the flash/path
	// text, which legitimately mentions the mode names.
	bar := screenLine(scr, a.height-1)
	right := bar[len(bar)-20:]
	if !strings.Contains(right, "Edit") || strings.Contains(right, "Preview") {
		t.Fatalf("chip should read Edit while previewing: %q", right)
	}

	goPath := filepath.Join(a.rootDir, "x.go")
	if err := os.WriteFile(goPath, []byte("package x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, goPath)
	a.draw()
	scr.Show()
	bar = screenLine(scr, a.height-1)
	if strings.Contains(bar, "Preview") || strings.Contains(bar, "Edit") {
		t.Fatalf("chip must vanish for non-markdown tabs: %q", bar)
	}
}

// TestStatusBarClick_TogglesPreview pins the chip's click target using
// the same segment geometry the draw pass uses — the whole point of
// deriving both from statusRightSegments is that they cannot disagree.
func TestStatusBarClick_TogglesPreview(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "# T\n")

	chipX := func() int {
		sx, _, sw, _ := a.statusRect()
		rightX := sx + sw
		for _, seg := range a.statusRightSegments(sw) {
			rightX -= runeLen(seg.text)
			if strings.Contains(seg.text, "Preview") || strings.Contains(seg.text, "Edit") {
				return rightX
			}
		}
		t.Fatal("chip segment not in statusRightSegments")
		return -1
	}

	a.statusBarClick(chipX())
	if a.mdPreview[tab] == nil {
		t.Fatal("clicking the chip should enable the preview")
	}
	a.statusBarClick(chipX())
	if a.mdPreview[tab] != nil {
		t.Fatal("clicking the Edit chip should disable the preview")
	}
}

// TestPreviewMarkdown_ThemeChangeRerenders pins the live-theme
// contract: the preview cache bakes theme colors into its style grid,
// so switching themes (the picker previews live) must re-render it —
// without this, a light theme paints its ground while the cached runes
// keep the old dark backgrounds, striping the page.
func TestPreviewMarkdown_ThemeChangeRerenders(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "# Title\n\nbody words\n")
	a.menuTogglePreviewMarkdown()
	oldBG := a.theme.BG

	light, ok := theme.ByID("github-light")
	if !ok {
		t.Fatal("github-light missing from the registry")
	}
	a.theme = light
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	st := a.mdPreview[tab]
	if st == nil {
		t.Fatal("preview vanished")
	}
	// Every cached rune style must now sit on the new theme's ground.
	for li, row := range st.styles {
		for ri, s := range row {
			_, bg, _ := s.Decompose()
			if bg == oldBG {
				t.Fatalf("line %d rune %d still carries the old theme bg", li, ri)
			}
		}
	}
	// And the painted body cell agrees.
	ex, ey, _, _ := a.editorRect()
	cells, w, _ := scr.GetContents()
	if _, bg, _ := cells[ey*w+ex+1].Style.Decompose(); bg == oldBG {
		t.Fatal("painted preview cell still on the old theme bg")
	}
}

// previewLineAt locates a rendered line containing sub and returns its
// index plus the screen y it draws at — the coordinate helper the
// selection tests share.
func previewLineAt(t *testing.T, a *App, tab *editor.Tab, sub string) (int, int) {
	t.Helper()
	st := a.mdPreview[tab]
	if st == nil {
		t.Fatal("no preview active")
	}
	li := findPreviewLine(a, tab, sub)
	if li < 0 {
		t.Fatalf("rendered line %q not found in %q", sub, st.lines)
	}
	_, ey, _, _ := a.editorRect()
	return li, ey + li - st.scroll
}

// TestPreviewMarkdown_DragSelectCopiesRenderedText pins select-to-copy
// inside the preview — skiff's identity gesture must work on rendered
// text too: press, drag, release copies the RENDERED characters (no
// markdown syntax) into the clipboard, exactly like the editor.
func TestPreviewMarkdown_DragSelectCopiesRenderedText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "# Title\n\nalpha **beta** gamma\n")
	a.menuTogglePreviewMarkdown()
	li, y := previewLineAt(t, a, tab, "alpha beta gamma")
	st := a.mdPreview[tab]
	ex, _ := a.mdPreviewGeom()
	x0 := ex + strings.Index(st.lines[li], "alpha")
	x1 := ex + strings.Index(st.lines[li], "beta") + len("beta")

	a.handleMouse(tcell.NewEventMouse(x0, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y, 0, tcell.ModNone))

	if a.clipBuf != "alpha beta" {
		t.Fatalf("clipBuf = %q, want the rendered %q", a.clipBuf, "alpha beta")
	}
	if tab.HasSelection() {
		t.Fatal("preview selection must not leak into the buffer's own selection")
	}
}

// TestPreviewMarkdown_MultiLineDragJoins pins the multi-line shape: a
// drag across rendered lines copies them newline-joined, partial ends
// respected.
func TestPreviewMarkdown_MultiLineDragJoins(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "first words here\n\nsecond words there\n")
	a.menuTogglePreviewMarkdown()
	l1, y1 := previewLineAt(t, a, tab, "first words")
	_, y2 := previewLineAt(t, a, tab, "second words")
	st := a.mdPreview[tab]
	ex, _ := a.mdPreviewGeom()
	x0 := ex + strings.Index(st.lines[l1], "words")
	x1 := ex + len("second")

	a.handleMouse(tcell.NewEventMouse(x0, y1, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y2, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y2, 0, tcell.ModNone))

	want := "words here\n\nsecond"
	if a.clipBuf != want {
		t.Fatalf("clipBuf = %q, want %q", a.clipBuf, want)
	}
}

// TestPreviewMarkdown_SelectionHighlightAndClickClears pins the visual
// half plus the collapse rule: a live drag paints the range on the
// Selection background, and a plain click (no drag) selects nothing
// and copies nothing.
func TestPreviewMarkdown_SelectionHighlightAndClickClears(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := seedMarkdownTab(t, a, "notes.md", "plain selectable words\n")
	a.menuTogglePreviewMarkdown()
	li, y := previewLineAt(t, a, tab, "selectable")
	st := a.mdPreview[tab]
	ex, _ := a.mdPreviewGeom()
	x0 := ex + strings.Index(st.lines[li], "selectable")

	a.handleMouse(tcell.NewEventMouse(x0, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x0+6, y, tcell.Button1, tcell.ModNone))
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	if _, bg, _ := cells[y*w+x0+2].Style.Decompose(); bg != a.theme.Selection {
		t.Fatalf("dragged range bg = %v, want Selection", bg)
	}
	a.handleMouse(tcell.NewEventMouse(x0+6, y, 0, tcell.ModNone))

	// Plain click elsewhere: collapses, copies nothing new.
	before := a.clipBuf
	a.handleMouse(tcell.NewEventMouse(x0, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x0, y, 0, tcell.ModNone))
	if a.clipBuf != before {
		t.Fatalf("plain click must not copy: %q -> %q", before, a.clipBuf)
	}
	a.draw()
	scr.Show()
	cells, w, _ = scr.GetContents()
	if _, bg, _ := cells[y*w+x0+2].Style.Decompose(); bg == a.theme.Selection {
		t.Fatal("click should clear the highlight")
	}
}

// TestPreviewMarkdown_ClampsMeasureAndCentres pins the wide-terminal
// rule: on a 200-column editor the document is wrapped at
// mdPreviewMaxWidth rather than 197 cells, painted centred in the room
// the cap leaves, and the drag hit-test uses the same origin as the
// paint — so select-to-copy still lands on the words under the pointer
// when the text is not flush left.
func TestPreviewMarkdown_ClampsMeasureAndCentres(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 200, 40)
	a.sidebarShown = false
	tab := seedMarkdownTab(t, a, "notes.md", strings.Repeat("word ", 60)+"\n")
	a.menuTogglePreviewMarkdown()
	st := a.mdPreview[tab]
	if st.width != mdPreviewMaxWidth {
		t.Fatalf("wrap width = %d, want the %d cap", st.width, mdPreviewMaxWidth)
	}
	for _, l := range st.lines {
		if w := textdraw.Width(l); w > mdPreviewMaxWidth {
			t.Fatalf("line %q is %d cells, over the cap", l, w)
		}
	}
	ex, _, ew, _ := a.editorRect()
	contentX, contentW := a.mdPreviewGeom()
	if contentW != mdPreviewMaxWidth || contentX <= ex+1 || contentX+contentW >= ex+ew-1 {
		t.Fatalf("content column x=%d w=%d is not centred inside the editor [%d,%d)", contentX, contentW, ex, ex+ew)
	}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	li, y := previewLineAt(t, a, tab, "word")
	row := screenLine(scr, y)
	if strings.TrimSpace(row[:contentX]) != "" {
		t.Fatalf("text painted left of the content column: %q", row)
	}
	if strings.Index(row, "word") != contentX {
		t.Fatalf("first word at column %d, want the content origin %d", strings.Index(row, "word"), contentX)
	}
	// A drag over the third and fourth words, addressed from the shared
	// origin, copies exactly those words.
	x0 := contentX + strings.Index(st.lines[li], "word word word") + len("word word ")
	x1 := x0 + len("word word")
	a.handleMouse(tcell.NewEventMouse(x0, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(x1, y, 0, tcell.ModNone))
	if a.clipBuf != "word word" {
		t.Fatalf("clipBuf = %q, want the two words under the drag", a.clipBuf)
	}
}

// TestPreviewMarkdown_HitAtCentredOriginIsColumnZero pins the hit-test
// against the paint on a centred document, row by row: a press on the
// first content cell maps to column 0 of the rendered line, whether
// that cell is prose, a code row's rail or an H1 rule — the rows the
// heading scheme and the code rectangle added — and the glyph painted
// there is that column's rune. A press in the centring margin left of
// the origin clamps to column 0 rather than going negative, and one
// three cells in lands on column 3, so select-to-copy addresses the
// text under the pointer and not the text shifted by the margin.
func TestPreviewMarkdown_HitAtCentredOriginIsColumnZero(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 200, 40)
	a.sidebarShown = false
	tab := seedMarkdownTab(t, a, "notes.md", "# Title\n\nprose line here\n\n```go\nx := 1\n```\n")
	a.menuTogglePreviewMarkdown()
	st := a.mdPreview[tab]
	ex, _, _, _ := a.editorRect()
	contentX, _ := a.mdPreviewGeom()
	if contentX <= ex+1 {
		t.Fatalf("content origin %d is not centred past the editor edge %d", contentX, ex)
	}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	for _, sub := range []string{"Title", "━━━", "prose line", "▏x := 1"} {
		li, y := previewLineAt(t, a, tab, sub)
		first := []rune(st.lines[li])[0]
		if got := cells[y*w+contentX].Runes[0]; got != first {
			t.Fatalf("%q: cell at the origin paints %q, want the line's first rune %q", sub, got, first)
		}
		if hit := a.mdPreviewHit(st, contentX, y); hit.line != li || hit.col != 0 {
			t.Fatalf("%q: hit at the origin = %+v, want line %d col 0", sub, hit, li)
		}
		if hit := a.mdPreviewHit(st, ex, y); hit.line != li || hit.col != 0 {
			t.Fatalf("%q: hit in the centring margin = %+v, want it clamped to col 0", sub, hit)
		}
		if hit := a.mdPreviewHit(st, contentX+3, y); hit.col != 3 {
			t.Fatalf("%q: hit three cells in = col %d, want 3", sub, hit.col)
		}
	}
}
