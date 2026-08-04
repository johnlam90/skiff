// =============================================================================
// File: internal/editor/tab_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the Tab type — the per-file owner of buffer + view state. We
// cover the disk I/O (NewTab, Save, Reload), the editing primitives that
// wrap Buffer (InsertString, Backspace, Delete, DeleteSelection), the
// cursor/selection movement helpers, scroll clamping, and the Render and
// HitTest methods using a tcell SimulationScreen.

package editor

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/theme"
)

// newSimScreen builds a SimulationScreen of the given dimensions, ready to
// have a Tab rendered into it. The caller is responsible for Fini.
func newSimScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	scr.SetSize(w, h)
	return scr
}

// TestNewTab_ExistingFile loads a real file from disk and confirms the
// buffer matches its contents and Mtime is populated.
func TestNewTab_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello\nworld"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Buffer.String() != "hello\nworld" {
		t.Fatalf("buffer mismatch: %q", tab.Buffer.String())
	}
	if tab.Mtime.IsZero() {
		t.Fatal("expected mtime to be set")
	}
	if !tab.StyleStale {
		t.Fatal("new tab should mark styles stale")
	}
}

// TestTab_LineEndingRoundTrip is the CRLF regression. Opening a
// Windows-authored file and saving it back untouched must produce the
// same bytes: before the ending was tracked, every line kept its \r in
// the buffer and the save wrote it back inside the line, so a one-line
// edit turned the whole file into a diff.
func TestTab_LineEndingRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // bytes on disk after an untouched save
	}{
		{"crlf", "alpha\r\nbeta\r\n", "alpha\r\nbeta\r\n"},
		{"lf", "alpha\nbeta\n", "alpha\nbeta\n"},
		{"crlf without trailing newline", "alpha\r\nbeta", "alpha\r\nbeta"},
		{"single line no newline", "alpha", "alpha"},
		// A mixed file normalises to whichever ending dominates — the
		// same call every other editor makes.
		{"mostly crlf", "a\r\nb\r\nc\nd\r\n", "a\r\nb\r\nc\r\nd\r\n"},
		{"mostly lf", "a\nb\nc\r\nd\n", "a\nb\nc\nd\n"},
		{"tie favours lf", "a\r\nb\n", "a\nb\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(path, []byte(tc.src), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			tab, err := NewTab(path)
			if err != nil {
				t.Fatalf("NewTab: %v", err)
			}
			for i, ln := range tab.Buffer.Lines {
				if strings.Contains(ln, "\r") {
					t.Fatalf("line %d kept a carriage return: %q", i, ln)
				}
			}
			if err := tab.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("round trip = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTab_SaveKeepsCRLFAfterEdit proves the ending survives real editing
// and not just an untouched save — the point of tracking it is that
// changing one line of a CRLF file leaves every other line alone.
func TestTab_SaveKeepsCRLFAfterEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.MoveCursorTo(Position{Line: 1, Col: 3}, false)
	tab.InsertString("!")
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "one\r\ntwo!\r\n" {
		t.Fatalf("after edit = %q, want CRLF preserved", got)
	}
}

// TestTab_ReloadRedetectsLineEnding covers a file whose convention
// changed on disk under an open tab. Reload takes the disk version as
// the new truth, so the recorded ending has to move with it or the next
// save undoes whatever converted the file.
func TestTab_ReloadRedetectsLineEnding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.LineEnding != LineEndingLF {
		t.Fatalf("LF file detected as %v", tab.LineEnding)
	}
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if tab.LineEnding != LineEndingCRLF {
		t.Fatalf("after reload ending = %v, want CRLF", tab.LineEnding)
	}
}

// TestDetectLineEnding exercises the dominance counter directly,
// including the tie that has to fall to LF (what the editor writes for a
// brand-new file) and the no-newline case.
func TestDetectLineEnding(t *testing.T) {
	cases := []struct {
		src  string
		want LineEnding
	}{
		{"", LineEndingLF},
		{"no newline at all", LineEndingLF},
		{"a\nb\n", LineEndingLF},
		{"a\r\nb\r\n", LineEndingCRLF},
		{"a\r\nb\nc\r\n", LineEndingCRLF},
		{"a\r\nb\n", LineEndingLF},
		{"\n", LineEndingLF},
		{"\r\n", LineEndingCRLF},
		{"lone\rcarriage", LineEndingLF},
	}
	for _, tc := range cases {
		if got := detectLineEnding([]byte(tc.src)); got != tc.want {
			t.Fatalf("detectLineEnding(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
	if got := LineEndingLF.Newline(); got != "\n" {
		t.Fatalf("LF newline = %q", got)
	}
	if got := LineEndingCRLF.Newline(); got != "\r\n" {
		t.Fatalf("CRLF newline = %q", got)
	}
}

// TestNewTab_MissingFile creates a tab for a nonexistent path with an empty
// buffer — matches editor convention of "open" creating on first save.
func TestNewTab_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ghost.txt")

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Buffer.LineCount() != 1 || tab.Buffer.Lines[0] != "" {
		t.Fatalf("expected empty buffer, got %#v", tab.Buffer.Lines)
	}
	if !tab.Mtime.IsZero() {
		t.Fatal("missing-file tab should have zero mtime")
	}
}

// TestNewTab_EmptyPath produces a scratch tab with an empty buffer and no
// path — the "untitled" case.
func TestNewTab_EmptyPath(t *testing.T) {
	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Path != "" {
		t.Fatalf("expected empty path, got %q", tab.Path)
	}
	if tab.DisplayName() != "untitled" {
		t.Fatalf("expected 'untitled', got %q", tab.DisplayName())
	}
}

// TestNewTab_UnreadableFile surfaces non-NotExist errors. We make a file
// unreadable by making its parent directory unsearchable; on Windows the
// mode bits don't behave the same way so we skip there.
func TestNewTab_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions test not portable to Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file-mode checks")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(sub, "secret.txt")
	if err := os.WriteFile(target, []byte("nope"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(sub, 0000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0755) })

	if _, err := NewTab(target); err == nil {
		t.Fatal("expected error opening unreadable file")
	}
}

// TestTab_DisplayName returns the basename of Path for saved tabs.
func TestTab_DisplayName(t *testing.T) {
	tab := &Tab{Path: "/tmp/some/dir/code.go", Buffer: NewBuffer("")}
	if tab.DisplayName() != "code.go" {
		t.Fatalf("got %q", tab.DisplayName())
	}
}

// TestTab_Save_WritesAndClearsDirty round-trips a save to disk and confirms
// Dirty is cleared and Mtime refreshed.
func TestTab_Save_WritesAndClearsDirty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("payload")
	if !tab.Dirty {
		t.Fatal("expected Dirty after insert")
	}
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if tab.Dirty {
		t.Fatal("Dirty should be cleared after save")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("file contents = %q", got)
	}
	if tab.Mtime.IsZero() {
		t.Fatal("expected mtime after save")
	}
}

// TestTab_Save_NoPath rejects saving an untitled tab — caller must prompt.
func TestTab_Save_NoPath(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	if err := tab.Save(); err == nil {
		t.Fatal("expected error saving tab without a path")
	}
}

// TestTab_Save_WriteError surfaces write errors (e.g. unwritable directory).
func TestTab_Save_WriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file-mode checks")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	tab := &Tab{Path: filepath.Join(dir, "no.txt"), Buffer: NewBuffer("hi")}
	if err := tab.Save(); err == nil {
		t.Fatal("expected save error in unwritable directory")
	}
}

// TestTab_Reload_RereadsAndClampsCursor confirms that Reload picks up the
// fresh disk content and that the cursor is clamped into the new buffer.
func TestTab_Reload_RereadsAndClampsCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("aaaa\nbbbb\ncccc"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	// Park cursor far down; will be clamped after reload truncates.
	tab.Cursor = Position{Line: 2, Col: 4}
	tab.Anchor = Position{Line: 1, Col: 0}
	tab.Dirty = true

	if err := os.WriteFile(path, []byte("only one"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if tab.Buffer.String() != "only one" {
		t.Fatalf("buffer = %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("Dirty should be cleared")
	}
	if tab.Cursor.Line != 0 || tab.Cursor.Col > len([]rune("only one")) {
		t.Fatalf("cursor not clamped: %+v", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatal("Reload should drop selection")
	}
}

// TestTab_Reload_NoPath returns an error for untitled tabs.
func TestTab_Reload_NoPath(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("")}
	if err := tab.Reload(); err == nil {
		t.Fatal("expected error reloading untitled tab")
	}
}

// TestTab_Reload_VanishedFile returns an error when the file disappears
// between opens.
func TestTab_Reload_VanishedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := tab.Reload(); err == nil {
		t.Fatal("expected error reloading vanished file")
	}
}

// TestTab_HasSelection_AndSelectionText covers the selection accessors for
// (a) no selection, (b) anchor before cursor, (c) anchor after cursor.
func TestTab_HasSelection_AndSelectionText(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}

	// (a) Empty selection.
	if tab.HasSelection() {
		t.Fatal("fresh tab should have no selection")
	}
	if tab.SelectionText() != "" {
		t.Fatal("expected empty selection text")
	}

	// (b) Anchor before cursor.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}
	if !tab.HasSelection() {
		t.Fatal("expected selection")
	}
	if tab.SelectionText() != "hello" {
		t.Fatalf("got %q", tab.SelectionText())
	}

	// (c) Anchor after cursor — Substring returns document order.
	tab.Anchor = Position{Line: 0, Col: 11}
	tab.Cursor = Position{Line: 0, Col: 6}
	if tab.SelectionText() != "world" {
		t.Fatalf("got %q", tab.SelectionText())
	}
}

// TestTab_DeleteSelection removes the selected range and collapses both
// cursor and anchor to the start.
func TestTab_DeleteSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.Anchor = Position{Line: 0, Col: 5}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.DeleteSelection()
	if tab.Buffer.String() != "hello" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor != tab.Anchor || tab.Cursor.Col != 5 {
		t.Fatalf("cursor not collapsed: %+v / %+v", tab.Cursor, tab.Anchor)
	}
	if !tab.Dirty || !tab.StyleStale {
		t.Fatal("expected Dirty + StyleStale set")
	}
}

// TestTab_DeleteSelection_NoSelection is a no-op when nothing is selected.
func TestTab_DeleteSelection_NoSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.DeleteSelection()
	if tab.Buffer.String() != "hello" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("should not become dirty")
	}
}

// TestTab_InsertString_ReplacesSelection inserts text after first deleting
// any active selection.
func TestTab_InsertString_ReplacesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.InsertString("there")
	if tab.Buffer.String() != "hello there" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor.Col != 11 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_InsertRune is the one-rune wrapper around InsertString.
func TestTab_InsertRune(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab")}
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.InsertRune('X')
	if tab.Buffer.String() != "aXb" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Backspace_MidLine deletes the rune to the left of the cursor.
func TestTab_Backspace_MidLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "hell" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor.Col != 4 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_Backspace_StartOfBuffer is a no-op at line 0 col 0.
func TestTab_Backspace_StartOfBuffer(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "hi" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("should not be dirty")
	}
}

// TestTab_Backspace_JoinsLines deletes the implicit '\n' when at column 0
// of a non-first line.
func TestTab_Backspace_JoinsLines(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello\nworld")}
	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "helloworld" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor != (Position{Line: 0, Col: 5}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_Backspace_DeletesSelection prefers the selection over the rune.
func TestTab_Backspace_DeletesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Anchor = Position{Line: 0, Col: 1}
	tab.Cursor = Position{Line: 0, Col: 4}
	tab.Backspace()
	if tab.Buffer.String() != "ho" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_MidLine removes the rune to the right of the cursor.
func TestTab_Delete_MidLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "ello" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_EndOfBuffer is a no-op when the cursor is past the last
// rune of the document.
func TestTab_Delete_EndOfBuffer(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "hi" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
}

// TestTab_Delete_JoinsLines deletes the line break at end-of-line.
func TestTab_Delete_JoinsLines(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello\nworld")}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "helloworld" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_DeletesSelection prefers the selection.
func TestTab_Delete_DeletesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.Delete()
	if tab.Buffer.String() != "lo" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_MoveCursor_Basic walks the cursor across simple line/col deltas.
func TestTab_MoveCursor_Basic(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("aaa\nbbbb\nc")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.MoveCursor(0, 2, false)
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Fatalf("after right: %+v", tab.Cursor)
	}
	tab.MoveCursor(1, 0, false)
	if tab.Cursor.Line != 1 {
		t.Fatalf("after down: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_ClampsAtEdges keeps the cursor within bounds when the
// caller asks for a delta past the start or end of the buffer.
func TestTab_MoveCursor_ClampsAtEdges(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\nb\nc")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.MoveCursor(-99, 0, false)
	if tab.Cursor.Line != 0 {
		t.Fatalf("up clamp: %+v", tab.Cursor)
	}
	tab.MoveCursor(99, 0, false)
	if tab.Cursor.Line != tab.Buffer.LineCount()-1 {
		t.Fatalf("down clamp: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_WrapsAtLineEdges wraps to neighbouring lines when col
// goes below zero or past end.
func TestTab_MoveCursor_WrapsAtLineEdges(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab\ncd")}

	// Past end of line 0 wraps to start of line 1.
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 1, false)
	if tab.Cursor != (Position{Line: 1, Col: 0}) {
		t.Fatalf("forward wrap: %+v", tab.Cursor)
	}

	// Before start of line 1 wraps to end of line 0.
	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, -1, false)
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Fatalf("backward wrap: %+v", tab.Cursor)
	}

	// At document start, going left clamps to col 0.
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, -1, false)
	if tab.Cursor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("left at start: %+v", tab.Cursor)
	}

	// At document end, going right clamps at end of last line.
	tab.Cursor = Position{Line: 1, Col: 2}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 1, false)
	if tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("right at end: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_Extend leaves Anchor in place when extend=true.
func TestTab_MoveCursor_Extend(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("abcdef")}
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 3, true)
	if tab.Anchor != (Position{Line: 0, Col: 1}) {
		t.Fatalf("anchor moved: %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 0, Col: 4}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_DownAdjustsCol clamps Col when the new line is shorter.
func TestTab_MoveCursor_DownAdjustsCol(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("longer line\nshort")}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(1, 0, false)
	if tab.Cursor.Line != 1 || tab.Cursor.Col != 5 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursorTo clamps the target into the buffer.
func TestTab_MoveCursorTo(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("abc\nde")}
	tab.MoveCursorTo(Position{Line: 99, Col: 99}, false)
	if tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("not clamped: %+v", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatal("expected anchor moved with cursor")
	}

	// extend=true keeps anchor.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.MoveCursorTo(Position{Line: 0, Col: 2}, true)
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor moved: %+v", tab.Anchor)
	}
}

// TestTab_MoveLineHome_End covers both home and end movement.
func TestTab_MoveLineHome_End(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.Anchor = tab.Cursor

	tab.MoveLineHome(false)
	if tab.Cursor.Col != 0 {
		t.Fatalf("home: %+v", tab.Cursor)
	}
	tab.MoveLineEnd(false)
	if tab.Cursor.Col != 5 {
		t.Fatalf("end: %+v", tab.Cursor)
	}

	// extend=true preserves anchor.
	tab.Anchor = Position{Line: 0, Col: 2}
	tab.Cursor = Position{Line: 0, Col: 4}
	tab.MoveLineHome(true)
	if tab.Anchor != (Position{Line: 0, Col: 2}) {
		t.Fatal("anchor moved on extend home")
	}
	tab.MoveLineEnd(true)
	if tab.Anchor != (Position{Line: 0, Col: 2}) {
		t.Fatal("anchor moved on extend end")
	}
}

// TestTab_SelectAll spans the whole buffer.
func TestTab_SelectAll(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("foo\nbar")}
	tab.SelectAll()
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor wrong: %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 1, Col: 3}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_EnsureVisible_Scrolls walks the cursor off-screen in each
// direction and confirms ScrollY/ScrollX move to bring it back.
func TestTab_EnsureVisible_Scrolls(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer(strings.Repeat("xxxxxxxxxxxxxxxxxxxx\n", 50))}

	// Cursor below viewport.
	tab.Cursor = Position{Line: 30, Col: 0}
	tab.EnsureVisible(40, 10)
	if tab.ScrollY != 30-10+1 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}

	// Cursor above viewport.
	tab.ScrollY = 20
	tab.Cursor = Position{Line: 5, Col: 0}
	tab.EnsureVisible(40, 10)
	if tab.ScrollY != 5 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}

	// Cursor right of viewport.
	tab.ScrollX = 0
	tab.Cursor = Position{Line: 5, Col: 18}
	tab.EnsureVisible(20, 10) // contentW = 20-6-1 = 13
	if tab.ScrollX == 0 {
		t.Fatalf("expected ScrollX > 0, got %d", tab.ScrollX)
	}

	// Tiny viewport — contentW clamped to 1.
	tab.EnsureVisible(1, 1)
	if tab.ScrollX < 0 || tab.ScrollY < 0 {
		t.Fatalf("negative scroll: %d %d", tab.ScrollX, tab.ScrollY)
	}
}

// TestTab_Scroll_NeverNegative bounds ScrollY at 0 even with a negative
// delta from cell zero.
func TestTab_Scroll_NeverNegative(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\nb\nc")}
	tab.Scroll(-10)
	if tab.ScrollY != 0 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}
	tab.Scroll(2)
	if tab.ScrollY != 2 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}
}

// TestTab_Render_DrawsLineNumbersAndContent renders into a SimulationScreen
// and reads cells back to confirm the gutter and the first line of content
// are visible. We don't pin colors — only the characters.
func TestTab_Render_DrawsLineNumbersAndContent(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer("hello\nworld")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	scr.Show()

	cells, w, _ := scr.GetContents()
	if w != 40 {
		t.Fatalf("width = %d", w)
	}
	// Reconstruct the first row.
	var row0 strings.Builder
	for i := 0; i < w; i++ {
		c := cells[i]
		if len(c.Runes) > 0 {
			row0.WriteRune(c.Runes[0])
		} else {
			row0.WriteRune(' ')
		}
	}
	got := row0.String()
	if !strings.Contains(got, "1") {
		t.Errorf("expected line number 1 in row 0, got %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' in row 0, got %q", got)
	}

	// Cursor should be visible somewhere in the rendered area.
	cx, cy, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor not visible")
	}
	if cy != 0 {
		t.Errorf("cursor row = %d, want 0", cy)
	}
	if cx < defaultGutterWidth {
		t.Errorf("cursor col %d should be past the gutter", cx)
	}
}

// TestTab_Render_HighlightsSelection draws a selection and confirms Render
// completes cleanly with mid-line cursor visibility — selection bg is theme
// dependent so we just ensure no panic and the cursor lands at col 5.
func TestTab_Render_HighlightsSelection(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	cx, _, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor hidden")
	}
	wantCx := defaultGutterWidth + 1 + 5
	if cx != wantCx {
		t.Errorf("cursor x = %d, want %d", cx, wantCx)
	}
}

// TestTab_Render_HidesCursorWhenOffscreen confirms the cursor is hidden
// when scroll has pushed the cursor's line out of view (cursorMoved=false
// so EnsureVisible doesn't drag it back).
func TestTab_Render_HidesCursorWhenOffscreen(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x\n", 50))
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.cursorMoved = false
	tab.ScrollY = 20 // far past line 0

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	if _, _, vis := scr.GetCursor(); vis {
		t.Fatal("expected cursor to be hidden")
	}
}

// TestTab_HitTest_ContentClick converts a click on a content cell back to
// the matching buffer Position.
func TestTab_HitTest_ContentClick(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello\nworld")

	pos, ok := tab.HitTest(defaultGutterWidth+1+2, 1, 40, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos != (Position{Line: 1, Col: 2}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestTab_HitTest_GutterClick treats clicks in the gutter as col 0 of that
// line — convenient for click-to-select-line.
func TestTab_HitTest_GutterClick(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello\nworld")

	pos, ok := tab.HitTest(0, 1, 40, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos != (Position{Line: 1, Col: 0}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestTab_HitTest_OutOfBounds returns ok=false when the click is below the
// last line or above the area.
func TestTab_HitTest_OutOfBounds(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hi")

	if _, ok := tab.HitTest(10, -1, 40, 10); ok {
		t.Fatal("expected !ok for negative y")
	}
	if _, ok := tab.HitTest(10, 99, 40, 10); ok {
		t.Fatal("expected !ok for huge y")
	}
	// Click on a row that is past the buffer's last line (still within h).
	tab.ScrollY = 0
	if _, ok := tab.HitTest(10, 5, 40, 10); ok {
		t.Fatal("expected !ok past last line")
	}
}

// TestTab_HitTest_ClampsColumnAtLineEnd never returns a Col past the line's
// rune length.
func TestTab_HitTest_ClampsColumnAtLineEnd(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("ab")

	pos, ok := tab.HitTest(defaultGutterWidth+1+50, 0, 80, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos.Col != 2 {
		t.Fatalf("col = %d, want 2", pos.Col)
	}
}

// TestTab_Render_ExpandsTabsToTabStops confirms that a real \t in the
// buffer paints across multiple cells until the next 4-cell tab stop.
// Without this, the cell directly after a tab character would read as
// ' ' (not 'a'), and indented lines wouldn't line up with each other.
func TestTab_Render_ExpandsTabsToTabStops(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("\tabc")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	cellRune := func(col int) rune {
		c := cells[col]
		if len(c.Runes) == 0 {
			return ' '
		}
		return c.Runes[0]
	}
	// Content starts at col defaultGutterWidth+1. The tab fills 4 cells, so
	// 'a' lands at content+4, 'b' at +5, 'c' at +6.
	contentCol := defaultGutterWidth + 1
	if w < contentCol+7 {
		t.Fatalf("simulated screen too narrow: w=%d", w)
	}
	if got := cellRune(contentCol + 4); got != 'a' {
		t.Errorf("expected 'a' at content+4, got %q", got)
	}
	if got := cellRune(contentCol + 5); got != 'b' {
		t.Errorf("expected 'b' at content+5, got %q", got)
	}
	if got := cellRune(contentCol + 6); got != 'c' {
		t.Errorf("expected 'c' at content+6, got %q", got)
	}
}

// TestTab_HitTest_InsideTabSnapsToTab proves a click anywhere inside a
// tab's 4-cell visual span returns the tab's rune column. Without this,
// clicks would silently land on phantom positions where there's nothing
// to edit.
func TestTab_HitTest_InsideTabSnapsToTab(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("\tx")

	contentX := defaultGutterWidth + 1
	for offset := 0; offset < 4; offset++ {
		pos, ok := tab.HitTest(contentX+offset, 0, 40, 10)
		if !ok {
			t.Fatalf("HitTest offset %d returned !ok", offset)
		}
		if pos.Col != 0 {
			t.Errorf("offset %d: col = %d, want 0 (the tab)", offset, pos.Col)
		}
	}
	// Cell 4 is the first cell of 'x' — should land on rune 1.
	pos, _ := tab.HitTest(contentX+4, 0, 40, 10)
	if pos.Col != 1 {
		t.Errorf("cell after tab: col = %d, want 1", pos.Col)
	}
}

// TestTab_NewTab_DetectsIndent ties the editor.Tab type to the
// indent-detection step so opening a tab-indented file makes Tab key
// inserts use a real tab. Pinned at this layer so a future refactor
// can't accidentally drop the call without a test failing.
func TestTab_NewTab_DetectsIndent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.go")
	if err := os.WriteFile(target, []byte("package x\n\nfunc x() {\n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(target)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.IndentUnit != "\t" {
		t.Fatalf("expected tab IndentUnit, got %q", tab.IndentUnit)
	}
}

// TestTab_clampScroll_BoundsScroll exercises clampScroll via Render: when
// ScrollY is set absurdly high, clampScroll caps it so the file stays
// visible.
func TestTab_clampScroll_BoundsScroll(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x\n", 5))
	tab.ScrollY = 9999
	tab.cursorMoved = false

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if tab.ScrollY > tab.Buffer.LineCount() {
		t.Fatalf("ScrollY not clamped: %d", tab.ScrollY)
	}
}

// TestTab_ScrollH_AdjustsAndClamps confirms that ScrollH adds the delta
// and never lets ScrollX go negative — mirroring how Scroll behaves for
// the vertical axis.
func TestTab_ScrollH_AdjustsAndClamps(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.ScrollH(5)
	if tab.ScrollX != 5 {
		t.Fatalf("ScrollX = %d, want 5", tab.ScrollX)
	}
	tab.ScrollH(-100)
	if tab.ScrollX != 0 {
		t.Fatalf("ScrollX after negative delta = %d, want 0", tab.ScrollX)
	}
}

// TestTab_Render_OverflowIndicator_Right paints a long line into a narrow
// viewport and confirms a '›' glyph appears at the rightmost content cell,
// signaling that more content exists off-screen. Without this affordance
// the user has no way to discover horizontal scroll is available.
func TestTab_Render_OverflowIndicator_Right(t *testing.T) {
	scr := newSimScreen(t, 20, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	// 30 chars on one line; viewport content width = 20 - defaultGutterWidth - 1 = 13.
	tab.Buffer = NewBuffer(strings.Repeat("x", 30))
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 20, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// Last cell of row 0 should be the right-overflow glyph.
	last := cells[w-1]
	if len(last.Runes) == 0 || last.Runes[0] != '›' {
		t.Fatalf("expected '›' at row 0 col %d, got %q", w-1, string(last.Runes))
	}
}

// TestTab_Render_OverflowIndicator_Left scrolls a long line right and
// confirms a '‹' glyph appears at the leftmost content cell to signal
// off-screen content to the left.
func TestTab_Render_OverflowIndicator_Left(t *testing.T) {
	scr := newSimScreen(t, 20, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x", 30))
	tab.ScrollX = 10
	tab.Cursor = Position{Line: 0, Col: 10}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 20, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// First content cell is at column defaultGutterWidth + 1.
	left := cells[defaultGutterWidth+1]
	if len(left.Runes) == 0 || left.Runes[0] != '‹' {
		t.Fatalf("expected '‹' at row 0 col %d, got %q", defaultGutterWidth+1, string(left.Runes))
	}
	_ = w
}

// TestTab_Render_NoOverflowIndicator_WhenLineFits is the negative control:
// a line that fits within contentW should NOT get a '›' glyph painted over
// its trailing real content.
func TestTab_Render_NoOverflowIndicator_WhenLineFits(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("short")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	for i := 0; i < w; i++ {
		if len(cells[i].Runes) > 0 && (cells[i].Runes[0] == '›' || cells[i].Runes[0] == '‹') {
			t.Fatalf("unexpected overflow glyph at col %d", i)
		}
	}
}

// TestGutterWidthFor pins the dynamic gutter width: files up to 9999 lines
// keep the default six-cell gutter, and each extra digit grows it by one so
// the git change-bar always has a blank leading cell to sit in.
func TestGutterWidthFor(t *testing.T) {
	cases := []struct {
		lines int
		want  int
	}{
		{0, 6}, {1, 6}, {999, 6}, {9999, 6},
		{10000, 7}, {99999, 7}, {100000, 8},
	}
	for _, c := range cases {
		if got := gutterWidthFor(c.lines); got != c.want {
			t.Errorf("gutterWidthFor(%d) = %d, want %d", c.lines, got, c.want)
		}
	}
}

// TestTab_RenderSkipsHighlightWhenNotStale confirms Render only re-tokenises
// the visible rows when something actually changed. Without this gate every
// redraw, including mouse moves, would re-tokenise for nothing, and the
// StyleStale flag set by every edit would be written but never read. We
// detect recompute by comparing the styles grid's backing array: a fresh
// make per recompute vs. reuse on skip.
func TestTab_RenderSkipsHighlightWhenNotStale(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("package main\nfunc main() {}\n")
	tab.StyleStale = true

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if tab.Styles == nil {
		t.Fatal("expected first render to highlight")
	}
	firstPtr := reflect.ValueOf(tab.Styles).Pointer()

	// Second render with no content, scroll, or height change: should reuse
	// the existing styles grid instead of re-tokenising.
	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if reflect.ValueOf(tab.Styles).Pointer() != firstPtr {
		t.Fatal("expected Render to skip re-highlight when nothing changed")
	}

	// An edit marks styles stale, so the next render must recompute.
	tab.StyleStale = true
	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if reflect.ValueOf(tab.Styles).Pointer() == firstPtr {
		t.Fatal("expected Render to re-highlight after StyleStale set")
	}
}

// TestTab_RenderScrollKeepsRowsStyled confirms the windowed highlight
// cache serves scrolled-to rows without a re-tokenise: the grid pointer
// survives a small scroll (no recompute) and the newly visible rows are
// already styled, because the window carries a lead beyond the viewport.
func TestTab_RenderScrollKeepsRowsStyled(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("package main\n", 50))
	tab.StyleStale = true

	tab.Render(scr, theme.Default(), 0, 0, 40, 10) // ScrollY clamps to 0
	if tab.Styles == nil {
		t.Fatal("expected first render to highlight")
	}
	firstPtr := reflect.ValueOf(tab.Styles).Pointer()

	// Scroll without moving the cursor (cursorMoved=false so EnsureVisible
	// doesn't snap ScrollY back). The 51-line file fits one window, so the
	// cache must be reused as-is — and row 20 must already carry styles.
	tab.ScrollY = 20
	tab.cursorMoved = false
	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if reflect.ValueOf(tab.Styles).Pointer() != firstPtr {
		t.Fatal("small scroll inside the window must not re-tokenise")
	}
	if tab.Styles[20] == nil {
		t.Fatal("scrolled-to rows must already be styled by the window")
	}
}

// TestTab_Render_GitMarkerDoesNotOverlapLineNumber is the regression test for
// the change-bar covering a line-number digit on files past 10000 lines.
// Before the dynamic gutter, "10000" rendered as "▌0000" with the bar
// overwriting the first digit. The gutter now grows by one cell per extra
// digit so the marker always sits in a blank leading cell.
func TestTab_Render_GitMarkerDoesNotOverlapLineNumber(t *testing.T) {
	const w = 60
	scr := newSimScreen(t, w, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x\n", 9999) + "x") // exactly 10000 lines
	tab.GitLines = map[int]GitLineChange{9999: GitLineModified}
	tab.ScrollY = 9990 // line 9999 (display 10000) lands on the last visible row
	tab.cursorMoved = false

	tab.Render(scr, theme.Default(), 0, 0, w, 10)
	scr.Show()

	cells, _, _ := scr.GetContents()
	const row = 9 // last visible row -> line 9999
	cellRune := func(col int) rune {
		c := cells[row*w+col]
		if len(c.Runes) == 0 {
			return ' '
		}
		return c.Runes[0]
	}
	if got := cellRune(0); got != '▌' {
		t.Errorf("expected git marker at col 0, got %q", got)
	}
	want := "10000"
	for i, r := range want {
		if got := cellRune(1 + i); got != r {
			t.Errorf("col %d: got %q, want %q", 1+i, got, string(r))
		}
	}
}

// TestTab_Render_SelectedCommentReadable pins the contrast fix for
// comments inside a selection: the comment foreground sat at 1.47:1
// against the Selection background, making selected comments unreadable.
// Render must swap comment-colored runes to the Text foreground when the
// selection covers them, and leave unselected comment runes alone.
func TestTab_Render_SelectedCommentReadable(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer("// hi")
	commentStyle := tcell.StyleDefault.Foreground(th.SynComment)
	tab.Styles = [][]tcell.Style{
		{commentStyle, commentStyle, commentStyle, commentStyle, commentStyle},
	}
	// Freeze the highlight cache so Render keeps the hand-set styles
	// instead of re-tokenising the (extension-less) buffer.
	tab.StyleStale = false
	tab.hlWinEnd = tab.Buffer.LineCount()
	tab.lastHighlightHeight = 4
	// Select the first three runes ("// ") and leave "hi" unselected.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 3}

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()
	selCell := cells[0*w+contentX] // first content rune, selected
	fg, bg, _ := selCell.Style.Decompose()
	if bg != th.Selection {
		t.Fatalf("expected Selection bg under selected rune, got %v", bg)
	}
	if fg == th.SynComment {
		t.Fatal("selected comment rune kept the 1.47:1 comment fg; want Text")
	}
	if fg != th.Text {
		t.Fatalf("selected comment fg: got %v, want Text %v", fg, th.Text)
	}

	unselCell := cells[0*w+contentX+4] // 'i' of "hi", unselected
	fg, _, _ = unselCell.Style.Decompose()
	if fg != th.SynComment {
		t.Fatalf("unselected comment rune should keep SynComment, got %v", fg)
	}
}

// TestTab_Render_FindMatchForcesTextFg pins the companion fix for the
// find tint: FindMatch kept each rune's syntax foreground, which put
// comments at 1.17:1 against the amber. Every non-current match must
// render with the Text foreground instead.
func TestTab_Render_FindMatchForcesTextFg(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer("// hi")
	commentStyle := tcell.StyleDefault.Foreground(th.SynComment)
	tab.Styles = [][]tcell.Style{
		{commentStyle, commentStyle, commentStyle, commentStyle, commentStyle},
	}
	// Freeze the highlight cache so Render keeps the hand-set styles.
	tab.StyleStale = false
	tab.hlWinEnd = tab.Buffer.LineCount()
	tab.lastHighlightHeight = 4
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	// One match over "hi"; FindIndex points elsewhere so this renders as
	// a non-current match (the FindMatch tint, not FindCurrent).
	tab.FindMatches = []Match{{Line: 0, Col: 3, Width: 2}}
	tab.FindIndex = -1

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()
	matchCell := cells[0*w+contentX+3] // 'h' of "hi"
	fg, bg, _ := matchCell.Style.Decompose()
	if bg != th.FindMatch {
		t.Fatalf("expected FindMatch bg under match, got %v", bg)
	}
	if fg != th.Text {
		t.Fatalf("find-match fg: got %v, want Text %v", fg, th.Text)
	}
}

// TestTab_Render_SelectedLowContrastSyntaxSwaps generalizes the
// selected-comment fix to every syntax color: a keyword-colored rune
// (3.9:1 on the selection blue) must swap to Text inside a selection,
// while a string-colored rune (5.0:1) keeps its identity.
func TestTab_Render_SelectedLowContrastSyntaxSwaps(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()
	th := theme.Default()

	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer("kw str")
	kwStyle := tcell.StyleDefault.Foreground(th.SynKeyword)
	strStyle := tcell.StyleDefault.Foreground(th.SynString)
	tab.Styles = [][]tcell.Style{
		{kwStyle, kwStyle, kwStyle, strStyle, strStyle, strStyle},
	}
	tab.StyleStale = false
	tab.hlWinEnd = tab.Buffer.LineCount()
	tab.lastHighlightHeight = 4
	// Select the whole line.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 6}

	tab.Render(scr, th, 0, 0, 40, 4)
	scr.Show()

	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	cells, w, _ := scr.GetContents()

	fg, _, _ := cells[0*w+contentX].Style.Decompose() // 'k' of "kw"
	if fg != th.Text {
		t.Fatalf("selected keyword rune fg = %v, want Text (keyword is 3.9:1 on Selection)", fg)
	}
	fg, _, _ = cells[0*w+contentX+3].Style.Decompose() // 's' of "str"
	if fg != th.SynString {
		t.Fatalf("selected string rune fg = %v, want SynString kept (it passes AA)", fg)
	}
}

// TestJumpToLineClamps pins JumpToLine's contract: 1-based input, cursor
// lands at column 0 of the requested line, the selection collapses, and
// out-of-range lines clamp to the buffer instead of panicking.
func TestJumpToLineClamps(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\nb\nc\nd\ne")}
	tab.JumpToLine(3)
	if tab.Cursor != (Position{Line: 2, Col: 0}) {
		t.Fatalf("cursor: got %+v, want line 2 col 0", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatalf("anchor should collapse to cursor, got %+v", tab.Anchor)
	}
	tab.JumpToLine(999)
	if tab.Cursor.Line != 4 {
		t.Fatalf("overshoot should clamp to last line, got %d", tab.Cursor.Line)
	}
	tab.JumpToLine(-5)
	if tab.Cursor.Line != 0 {
		t.Fatalf("undershoot should clamp to first line, got %d", tab.Cursor.Line)
	}
}

// TestCenterOnCursor verifies the goto-line scroll rule: the cursor's
// line ends up mid-viewport rather than pinned to an edge, and a cursor
// near the top never produces a negative scroll.
func TestCenterOnCursor(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "x"
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}}
	tab.Cursor = Position{Line: 50}
	tab.CenterOnCursor(20)
	if tab.ScrollY != 40 {
		t.Fatalf("ScrollY: got %d, want 40 (50 - 20/2)", tab.ScrollY)
	}
	tab.Cursor = Position{Line: 2}
	tab.CenterOnCursor(20)
	if tab.ScrollY != 0 {
		t.Fatalf("near-top center should clamp to 0, got %d", tab.ScrollY)
	}
}

// TestRenderScrollReusesHighlightCache pins the remote-performance fix:
// scrolling within the cached highlight window must NOT re-tokenise (a
// poisoned cache row survives the redraw), while scrolling far outside
// the window rebuilds it.
func TestRenderScrollReusesHighlightCache(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)

	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = "x := 1"
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}, StyleStale: true}
	tab.initUndo()
	th := theme.Default()

	tab.Render(scr, th, 0, 0, 40, 10)
	if tab.hlWinEnd <= tab.hlWinStart {
		t.Fatalf("first render should populate the window, got [%d,%d)", tab.hlWinStart, tab.hlWinEnd)
	}
	// Poison a cached row, scroll a little (still inside the window),
	// and check the poison survives — proof no re-tokenise happened.
	poison := []tcell.Style{tcell.StyleDefault.Foreground(tcell.ColorRed)}
	tab.Styles[8] = poison
	tab.ScrollY = 5
	tab.Render(scr, th, 0, 0, 40, 10)
	if len(tab.Styles[8]) != 1 {
		t.Fatal("small scroll re-tokenised — the cache window was ignored")
	}

	// A jump far past the window must rebuild it.
	tab.ScrollY = 600
	tab.Render(scr, th, 0, 0, 40, 10)
	if tab.Styles[600] == nil {
		t.Fatal("jump outside the window should re-tokenise around the new viewport")
	}
	if len(tab.Styles[8]) == 1 && tab.hlWinStart <= 8 {
		t.Fatal("window did not move with the viewport")
	}
}

// TestNewTab_RefusesBinaryFiles pins the open gate: a binary file (a
// zip, a misnamed executable) must not load into a text buffer — every
// downstream stage (Chroma lexing, per-rune style grids, wrap math)
// scales with line length, and binary content has pathological lines.
// On a real machine this manifested as a full editor freeze: multi-
// second highlights snowballing behind tcell's mouse-motion events
// until even Esc-q could not get through.
func TestNewTab_RefusesBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.zip")
	// PK\x03\x04 header followed by NUL-laden compressed bytes.
	data := append([]byte("PK\x03\x04"), make([]byte, 4096)...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := NewTab(path); !errors.Is(err, ErrBinaryFile) {
		t.Fatalf("want ErrBinaryFile, got %v", err)
	}
}

// TestNewTab_KeepsMultibyteTextOpenable guards the heuristic's other
// side: UTF-8 text (no NUL bytes, whatever the language) must still
// open normally.
func TestNewTab_KeepsMultibyteTextOpenable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "höhe.txt")
	if err := os.WriteFile(path, []byte("höhe — 高さ\nplain line\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("multibyte text must open, got %v", err)
	}
	if len(tab.Buffer.Lines) < 2 {
		t.Fatalf("buffer looks wrong: %d lines", len(tab.Buffer.Lines))
	}
}
