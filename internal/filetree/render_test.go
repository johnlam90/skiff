// =============================================================================
// File: internal/filetree/render_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/theme"
)

// TestRender_ProjectNameAndChevrons asserts that the explorer header shows
// the project (root) name on row 1 and that an expanded directory renders
// with a '▾' while a collapsed sibling renders with a '▸'.
func TestRender_ProjectNameAndChevrons(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// alpha will appear collapsed (default), Beta the same. Force alpha
	// expanded so we can see both chevrons in one render.
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand alpha

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Row 1 should contain the project (root) folder name.
	rootName := filepath.Base(root)
	if got := rowText(cells, w, 1); !containsRune(got, rootName) {
		t.Fatalf("row 1 missing project name %q: got %q", rootName, got)
	}

	// Find the row containing alpha; verify '▾' present.
	if !findRowWithBoth(cells, w, 20, "alpha", '▾') {
		t.Fatal("expected an expanded-row showing alpha with '▾'")
	}
	// Beta is collapsed — verify '▸' present.
	if !findRowWithBoth(cells, w, 20, "Beta", '▸') {
		t.Fatal("expected a collapsed-row showing Beta with '▸'")
	}
}

// TestRender_EmptyRootShowsPlaceholder pins the empty-project state. A
// bare root row with nothing under it reads as "the tree failed to
// load"; the muted placeholder says which of the two it is. This is the
// very first screen after `mkdir proj && skiff proj`.
func TestRender_EmptyRootShowsPlaceholder(t *testing.T) {
	root := t.TempDir()
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	if got := rowText(cells, w, 2); !containsRune(got, EmptyFolderLabel) {
		t.Fatalf("first list row = %q, want %q", got, EmptyFolderLabel)
	}
	// Muted, not Text: it's an explanation, not a file.
	fg, _, _ := cells[2*w+1].Style.Decompose()
	if fg != theme.Default().Muted {
		t.Fatalf("placeholder fg = %v, want Muted", fg)
	}
	// It is not a row you can click — HitTest must miss it, or the
	// placeholder would behave like a file and open nothing.
	if n, ok := tr.HitTest(0, 2); ok || n != nil {
		t.Fatalf("placeholder must not be a hit target, got %v", n)
	}
}

// TestRender_EmptyRootClipsToSidebarWidth keeps the placeholder inside
// the sidebar: like every other row it is drawn through drawString, so a
// sidebar narrower than the label truncates instead of painting over the
// splitter and the editor beyond it.
func TestRender_EmptyRootClipsToSidebarWidth(t *testing.T) {
	tr, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const narrow = 8 // narrower than "(folder is empty)"
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 20)
	tr.Render(scr, theme.Default(), 0, 0, narrow, 20)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// Cells the renderer never touched carry no runes at all; anything
	// with a visible glyph past the sidebar edge is a bleed.
	for x := narrow; x < w; x++ {
		if c := cells[2*w+x]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("placeholder painted past the sidebar at x=%d: %q", x, c.Runes[0])
		}
	}
	if got := rowText(cells, w, 2)[:narrow]; !strings.HasPrefix(got, " (folder") {
		t.Fatalf("clipped placeholder = %q", got)
	}
}

// TestRender_NonEmptyRootHasNoPlaceholder is the negative: a project
// with files must never show the empty-folder row.
func TestRender_NonEmptyRootHasNoPlaceholder(t *testing.T) {
	tr, err := New(mkTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)
	for y := range 20 {
		if containsRune(rowText(cells, w, y), EmptyFolderLabel) {
			t.Fatalf("row %d shows the empty placeholder in a populated tree", y)
		}
	}
}

// TestRender_ActiveFolderIsBold sets ActiveFolder to alpha's path and
// checks that alpha's row carries the AttrBold style — the visual cue
// the user uses to confirm where "New file" will land.
func TestRender_ActiveFolderIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path

	cells, w := renderAndCollect(t, tr, 40, 20)

	// Find any cell on the alpha row; assert the foreground style has Bold.
	rowY := -1
	for y := 2; y < 20; y++ {
		if containsRune(rowText(cells, w, y), "alpha") {
			rowY = y
			break
		}
	}
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	// Scan the row for any cell with AttrBold set.
	bold := false
	for x := 0; x < w; x++ {
		_, _, attr := cells[rowY*w+x].Style.Decompose()
		if attr&tcell.AttrBold != 0 {
			bold = true
			break
		}
	}
	if !bold {
		t.Fatal("expected alpha row to be rendered bold (active folder)")
	}
}

// TestRender_ActiveFileIsBold verifies the open file itself is visible in the tree.
func TestRender_ActiveFileIsBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	tr.ActiveFile = inner.Path

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find active file row")
	}
	if !rowHasBold(cells, w, rowY) {
		t.Fatal("active file row should be bold")
	}
}

// TestRender_TinyHeightDoesNotPanic guards against an off-by-one when the
// caller hands Render a height smaller than the 2-row header — listH goes
// to zero and we shouldn't blow up dividing or indexing.
func TestRender_TinyHeightDoesNotPanic(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(20, 1)
	tr.Render(scr, theme.Default(), 0, 0, 20, 1) // listH would be -1 -> clamped to 0
	// no panic = pass; also visible must be empty.
	if len(tr.visible) != 0 {
		t.Fatalf("expected empty visible slice, got len=%d", len(tr.visible))
	}
}

// TestRender_DirtyFileUsesModifiedColor seeds the tree's DirtyFiles set
// with one path and asserts the renderer paints that row in
// theme.Modified — the colour the editor uses everywhere else (tab dot,
// future status indicators) for "uncommitted change".
func TestRender_DirtyFileUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("alpha/inner.go missing from fixture")
	}
	tr.DirtyFiles = map[string]GitChangeKind{inner.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatalf("expected inner.go row to be drawn in Modified color")
	}
}

// TestRender_DirtyFolderUsesModifiedColor proves that a folder appearing
// in DirtyFolders gets the Modified colour even when none of its visible
// children do — collapsed branches still need to signal "something
// changed inside".
func TestRender_DirtyFolderUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatal("expected alpha folder row to be drawn in Modified color")
	}
}

// TestRender_DirtyRootUsesModifiedColor ensures the project name itself
// reflects git changes when any descendant is dirty.
func TestRender_DirtyRootUsesModifiedColor(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFolders = map[string]GitChangeKind{tr.Root.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	if !rowHasColor(cells, w, 1, theme.Default().GitModified) {
		t.Fatal("expected root project row to be drawn in Modified color")
	}
}

// TestRender_DirtyAndActiveStaysBold confirms that the active-folder
// styling (bold) and the dirty-folder styling (Modified colour) compose
// cleanly — the user shouldn't lose the "current target" cue just
// because the folder also has uncommitted changes.
func TestRender_DirtyAndActiveStaysBold(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	tr.ActiveFolder = alpha.Path
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Error("expected alpha row to be Modified colour")
	}
	if !rowHasBold(cells, w, rowY) {
		t.Error("expected alpha row to remain bold")
	}
}

// TestRender_IconsDisabledByDefault pins down the default look — a tree
// whose IconsEnabled flag was never flipped should not embed any Nerd
// Font glyph in its output. Important so users on terminals without a
// Nerd Font don't see broken-glyph "tofu" boxes after upgrading.
func TestRender_IconsDisabledByDefault(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	// Walk every visible row and assert none of the file-default,
	// folder-open, or folder-closed glyphs appear.
	for y := 0; y < 20; y++ {
		row := rowText(cells, w, y)
		for _, g := range []string{icons.FileDefault, icons.FolderOpen, icons.FolderClosed} {
			if containsRune(row, g) {
				t.Fatalf("row %d unexpectedly contains glyph %q: %q", y, g, row)
			}
		}
	}
}

// TestRender_IconsEnabledShowsFolderGlyph verifies that flipping
// IconsEnabled actually emits the folder-closed glyph for an
// unexpanded directory — the most common visible case.
func TestRender_IconsEnabledShowsFolderGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "Beta") // collapsed
	if rowY < 0 {
		t.Fatal("could not find Beta row")
	}
	if !containsRune(rowText(cells, w, rowY), icons.FolderClosed) {
		t.Fatalf("expected FolderClosed glyph on Beta row, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledShowsFileGlyph picks the .go file inside
// alpha/, expands the parent so it's visible, and checks the
// language-specific glyph from icons.For lands on its row.
func TestRender_IconsEnabledShowsFileGlyph(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha) // expand so inner.go renders

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}
	want := icons.For("inner.go", false, false)
	if !containsRune(rowText(cells, w, rowY), want) {
		t.Fatalf("expected glyph %q on inner.go row, got %q",
			want, rowText(cells, w, rowY))
	}
}

// TestRender_DotFileRendersMuted verifies hidden / dotted entries
// fall back to the theme's Muted colour rather than FileColor — this
// is the visual cue users rely on to skim a tree full of metadata
// (.gitignore, .env, .github/) and find the source files at a glance.
func TestRender_DotFileRendersMuted(t *testing.T) {
	root := mkTree(t)
	// mkTree already creates .git but it's filtered by shouldHide. Add
	// a .env file that *will* show up so we can assert against its row.
	mustWrite(t, filepath.Join(root, ".env"), "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().Muted) {
		t.Fatalf(".env row should render in Muted; got %q", rowText(cells, w, rowY))
	}
	// Sanity check: a non-dot file on the same level should *not* be muted.
	zetaY := findRowY(cells, w, 20, "zeta.txt")
	if zetaY < 0 {
		t.Fatal("could not find zeta.txt row")
	}
	if rowHasColor(cells, w, zetaY, theme.Default().Muted) {
		t.Fatalf("non-dot file zeta.txt should not be muted")
	}
}

// TestRender_DirtyOverridesDotMute verifies the priority cascade
// documented in drawNodeRow: a modified .env should still flip to the
// Modified colour rather than staying muted, because "this file has
// uncommitted changes" is louder information than "this is metadata".
func TestRender_DirtyOverridesDotMute(t *testing.T) {
	root := mkTree(t)
	envPath := filepath.Join(root, ".env")
	mustWrite(t, envPath, "k=v")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]GitChangeKind{envPath: GitChangeModified}

	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, ".env")
	if rowY < 0 {
		t.Fatal("could not find .env row")
	}
	if !rowHasColor(cells, w, rowY, theme.Default().GitModified) {
		t.Fatalf("dirty .env should override Muted with Modified, got %q",
			rowText(cells, w, rowY))
	}
}

// TestRender_IconsEnabledColoursGlyphPerLanguage proves the glyph cell
// is drawn in icons.ColorFor's mapped colour rather than the row's
// regular file fg. Without this, every glyph would inherit the same
// FileColor and the visual cue (Go cyan / Markdown blue / etc.) would
// be lost.
func TestRender_IconsEnabledColoursGlyphPerLanguage(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")
	tr.Toggle(alpha)

	cells, w := renderAndCollect(t, tr, 40, 20)

	rowY := findRowY(cells, w, 20, "inner.go")
	if rowY < 0 {
		t.Fatal("could not find inner.go row")
	}

	// Locate the cell carrying the .go glyph and assert its fg is the
	// per-language colour, not the row's FileColor.
	wantGlyph := []rune(icons.For("inner.go", false, false))[0]
	wantColor := icons.ColorFor("inner.go", false, theme.Default().FileColor)
	found := false
	for x := 0; x < w; x++ {
		c := cells[rowY*w+x]
		if len(c.Runes) == 0 || c.Runes[0] != wantGlyph {
			continue
		}
		fg, _, _ := c.Style.Decompose()
		if fg != wantColor {
			t.Fatalf("glyph fg = %v, want %v (per-language)", fg, wantColor)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("no cell carried glyph %q on inner.go row", string(wantGlyph))
	}
}

// TestRender_IconsEnabledFolderOpenSwitches verifies the open/closed
// folder glyph pair flips correctly when the user expands a folder —
// the visual cue most users will rely on more than the chevron.
func TestRender_IconsEnabledFolderOpenSwitches(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = true
	alpha := findChild(tr.Root, "alpha")

	// Collapsed: should show closed-folder glyph, not the open one.
	cells, w := renderAndCollect(t, tr, 40, 20)
	rowY := findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (collapsed)")
	}
	collapsed := rowText(cells, w, rowY)
	if !containsRune(collapsed, icons.FolderClosed) {
		t.Fatalf("collapsed alpha row missing FolderClosed: %q", collapsed)
	}
	if containsRune(collapsed, icons.FolderOpen) {
		t.Fatalf("collapsed alpha row should not show FolderOpen: %q", collapsed)
	}

	// Expanded: should switch to open-folder glyph.
	tr.Toggle(alpha)
	cells, w = renderAndCollect(t, tr, 40, 20)
	rowY = findRowY(cells, w, 20, "alpha")
	if rowY < 0 {
		t.Fatal("could not find alpha row (expanded)")
	}
	expanded := rowText(cells, w, rowY)
	if !containsRune(expanded, icons.FolderOpen) {
		t.Fatalf("expanded alpha row missing FolderOpen: %q", expanded)
	}
}

// TestRender_DirtyRowsShowStatusLetter pins the non-hue git channel:
// row color alone can't carry status for colorblind users (added-green
// vs deleted-red is the classic deuteranopia collision), so every dirty
// row must also show the GIT panel's one-cell letter, right-aligned.
func TestRender_DirtyRowsShowStatusLetter(t *testing.T) {
	root := mkTree(t)
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	alpha := findChild(tr.Root, "alpha")
	if err := tr.reload(alpha); err != nil {
		t.Fatalf("reload alpha: %v", err)
	}
	alpha.Expanded = true
	inner := findChild(alpha, "inner.go")
	if inner == nil {
		t.Fatal("alpha/inner.go missing from fixture")
	}
	tr.DirtyFiles = map[string]GitChangeKind{inner.Path: GitChangeModified}
	tr.DirtyFolders = map[string]GitChangeKind{alpha.Path: GitChangeAdded}

	cells, w := renderAndCollect(t, tr, 40, 20)

	fileY := findRowY(cells, w, 20, "inner.go")
	if fileY < 0 {
		t.Fatal("could not find inner.go row in render output")
	}
	c := cells[fileY*w+(w-2)]
	if len(c.Runes) == 0 || c.Runes[0] != 'M' {
		t.Fatalf("modified file row should end with an 'M' letter, got %q", c.Runes)
	}

	folderY := findRowY(cells, w, 20, "alpha")
	if folderY < 0 {
		t.Fatal("could not find alpha row in render output")
	}
	c = cells[folderY*w+(w-2)]
	if len(c.Runes) == 0 || c.Runes[0] != 'A' {
		t.Fatalf("added folder row should end with an 'A' letter, got %q", c.Runes)
	}
}

// TestRender_CJKFilenameKeepsStatusLetterAligned pins the tree's
// wide-glyph layout: each ideograph of a CJK filename occupies two cells
// (base cell plus an untouched continuation cell), the dirty-status
// letter still lands at the row's right edge, and nothing paints past
// the sidebar width. Before textdraw, drawString painted one rune per
// COLUMN, so consecutive ideographs landed in adjacent cells and
// rendered as overlapping garbage.
func TestRender_CJKFilenameKeepsStatusLetterAligned(t *testing.T) {
	root := t.TempDir()
	const name = "日本語ファイル.go"
	mustWrite(t, filepath.Join(root, name), "package x\n")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.DirtyFiles = map[string]GitChangeKind{filepath.Join(root, name): GitChangeModified}

	// Narrow tree inside a wider screen, so painting past the sidebar
	// edge would be visible — the same shape as the placeholder clip test.
	const treeW = 30
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(40, 20)
	tr.Render(scr, theme.Default(), 0, 0, treeW, 20)
	scr.Show()
	cells, w, _ := scr.GetContents()

	fileY := findRowY(cells, w, 20, "日")
	if fileY < 0 {
		t.Fatal("could not find the CJK file row in render output")
	}
	// Locate the first ideograph, then check the cluster layout: base
	// cell, untouched continuation cell (tcell paints the wide glyph
	// through it), and the next ideograph exactly two cells later.
	col := -1
	for x := 0; x < treeW; x++ {
		if c := cells[fileY*w+x]; len(c.Runes) > 0 && c.Runes[0] == '日' {
			col = x
			break
		}
	}
	if col < 0 {
		t.Fatal("日 not found on the CJK file row")
	}
	if c := cells[fileY*w+col+1]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
		t.Fatalf("continuation cell after 日 holds a glyph: %q", c.Runes)
	}
	if c := cells[fileY*w+col+2]; len(c.Runes) == 0 || c.Runes[0] != '本' {
		t.Fatalf("cell two after 日 = %q, want 本", c.Runes)
	}
	// The status letter keeps its right-edge home.
	if c := cells[fileY*w+(treeW-2)]; len(c.Runes) == 0 || c.Runes[0] != 'M' {
		t.Fatalf("status letter cell = %q, want M at column %d", c.Runes, treeW-2)
	}
	// And no glyph escapes the sidebar.
	for x := treeW; x < w; x++ {
		if c := cells[fileY*w+x]; len(c.Runes) > 0 && c.Runes[0] != ' ' {
			t.Fatalf("glyph painted past the sidebar at x=%d: %q", x, c.Runes[0])
		}
	}
}

// TestUnreadableDirIsMarkedNotEmpty is the whole point of Node.ReadErr:
// before it, a directory we lacked permission to read rendered exactly
// like a directory with nothing in it, so the tree confidently reported
// "nothing here" about a place it had never managed to look.
func TestUnreadableDirIsMarkedNotEmpty(t *testing.T) {
	skipIfRoot(t)
	root := t.TempDir()
	mkUnreadable(t, root, "locked")
	mustMkdir(t, filepath.Join(root, "vacant"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	locked, vacant := findChild(tr.Root, "locked"), findChild(tr.Root, "vacant")
	if locked == nil || vacant == nil {
		t.Fatalf("expected both dirs as children, got %v", tr.Root.Children)
	}
	tr.Toggle(locked)
	tr.Toggle(vacant)

	if locked.ReadErr == nil {
		t.Fatal("an unreadable directory must carry ReadErr")
	}
	if vacant.ReadErr != nil {
		t.Fatalf("a readable empty directory must not be marked: %v", vacant.ReadErr)
	}

	cells, w := renderAndCollect(t, tr, 40, 20)
	lockedRow, vacantRow := rowText(cells, w, 2), rowText(cells, w, 3)
	if !strings.Contains(lockedRow, "locked/") || !strings.Contains(lockedRow, UnreadableLabel) {
		t.Fatalf("locked row must carry the marker, got %q", lockedRow)
	}
	if strings.Contains(vacantRow, UnreadableLabel) {
		t.Fatalf("empty row must stay unmarked, got %q", vacantRow)
	}
	if lockedRow == vacantRow {
		t.Fatal("unreadable and empty directories must not render identically")
	}
}

// TestUnreadableRootLabel: the root has no row of its own under the
// project name, so an unreadable root falls through to the placeholder
// line — which said "(folder is empty)" and was the most confident lie
// in the tree.
func TestUnreadableRootLabel(t *testing.T) {
	tr := &Tree{Root: &Node{Path: "/nope", Name: "nope", IsDir: true, Expanded: true, Loaded: true}}
	cells, w := renderAndCollect(t, tr, 40, 10)
	if got := rowText(cells, w, 2); !strings.Contains(got, EmptyFolderLabel) {
		t.Fatalf("a readable empty root says so, got %q", got)
	}

	tr.Root.ReadErr = os.ErrPermission
	cells, w = renderAndCollect(t, tr, 40, 10)
	got := rowText(cells, w, 2)
	if !strings.Contains(got, UnreadableLabel) || strings.Contains(got, EmptyFolderLabel) {
		t.Fatalf("an unreadable root must say so, got %q", got)
	}
}

// TestRender_SymlinkRowsAreMarked checks the user-visible half: a link
// is not silently drawn as an ordinary row, and a refused link says why
// instead of offering a chevron onto nothing.
func TestRender_SymlinkRowsAreMarked(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	mustMkdir(t, target)
	mustSymlink(t, target, filepath.Join(root, "linked"))
	mustSymlink(t, root, filepath.Join(root, "self"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.IconsEnabled = false
	cells, w := renderAndCollect(t, tr, 44, 12)

	linkedY := findRowY(cells, w, 12, "linked/")
	if linkedY < 0 {
		t.Fatal("linked/ row not rendered")
	}
	if !strings.Contains(rowText(cells, w, linkedY), SymlinkLabel) {
		t.Fatalf("link row should carry %q: %q", SymlinkLabel, rowText(cells, w, linkedY))
	}
	if !strings.Contains(rowText(cells, w, linkedY), "▸") {
		t.Fatalf("an openable link keeps its chevron: %q", rowText(cells, w, linkedY))
	}

	selfY := findRowY(cells, w, 12, LoopLabel)
	if selfY < 0 {
		t.Fatalf("the refused link must be labelled %q", LoopLabel)
	}
	if strings.Contains(rowText(cells, w, selfY), "▸") {
		t.Fatalf("a link that never opens must not draw a chevron: %q", rowText(cells, w, selfY))
	}
	// The real directory next to it is unaffected — no stray markers.
	targetY := findRowY(cells, w, 12, "target/")
	if targetY < 0 {
		t.Fatal("target/ row not rendered")
	}
	if strings.Contains(rowText(cells, w, targetY), SymlinkLabel) {
		t.Fatalf("an ordinary directory must not be marked as a link: %q", rowText(cells, w, targetY))
	}
}

// TestRender_CompactChainShowsJoinedPath: the screen shows one
// "a/b/c/" row with the expanded chevron, and no standalone "b/" row —
// the visual half of the compact-folders contract.
func TestRender_CompactChainShowsJoinedPath(t *testing.T) {
	tr, err := New(mkChain(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.Toggle(findChild(tr.Root, "a"))

	cells, w := renderAndCollect(t, tr, 40, 20)
	if !findRowWithBoth(cells, w, 20, "a/b/c/", '▾') {
		t.Fatal("expected an expanded chain row showing a/b/c/")
	}
	for y := 0; y < 20; y++ {
		row := rowText(cells, w, y)
		if strings.Contains(row, "b/") && !strings.Contains(row, "a/b/c/") {
			t.Fatalf("mid-chain dir must not get its own row: %q", row)
		}
	}
}

// TestRender_ActiveFolderInsideChainHighlights: making a folded-away
// mid-chain dir the active folder must light up the chain row that
// contains it — the row is that dir's only representation on screen.
func TestRender_ActiveFolderInsideChainHighlights(t *testing.T) {
	tr, err := New(mkChain(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := findChild(tr.Root, "a")
	tr.Toggle(a)
	b := findChild(a, "b")
	tr.ActiveFolder = b.Path

	cells, w := renderAndCollect(t, tr, 40, 20)
	y := findRowY(cells, w, 20, "a/b/c/")
	if y < 0 {
		t.Fatal("chain row not rendered")
	}
	fg, _, attrs := cells[y*w+2].Style.Decompose()
	if fg != theme.Default().Accent || attrs&tcell.AttrBold == 0 {
		t.Fatalf("chain row must show the active-folder highlight: fg=%v attrs=%v", fg, attrs)
	}
}

// TestRender_DotDirChainRendersMuted: a chain headed by a dot-dir reads
// as metadata exactly like a standalone dot-dir — the mute keys off the
// label the user sees, not the deepest segment's name.
func TestRender_DotDirChainRendersMuted(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".config"))
	mustMkdir(t, filepath.Join(root, ".config", "app"))
	mustWrite(t, filepath.Join(root, ".config", "app", "cfg.txt"), "x")
	mustWrite(t, filepath.Join(root, "main.go"), "package m")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.Toggle(findChild(tr.Root, ".config"))

	cells, w := renderAndCollect(t, tr, 40, 20)
	y := findRowY(cells, w, 20, ".config/app/")
	if y < 0 {
		t.Fatal("dot chain row not rendered")
	}
	fg, _, _ := cells[y*w+2].Style.Decompose()
	if fg != theme.Default().Muted {
		t.Fatalf("dot chain row fg = %v, want Muted", fg)
	}
}
