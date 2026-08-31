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
