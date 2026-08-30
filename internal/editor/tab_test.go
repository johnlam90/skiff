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
	"fmt"
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

// TestSavePreservesExecBitAndSymlink fences the two properties a
// rename-based atomic save is most likely to destroy: an executable
// file must keep its mode after a save, and a tab opened on a symlink
// must save through the link to its target instead of replacing the
// link with a regular file. os.WriteFile happened to give both for
// free; atomicfile.Replace has to provide them deliberately.
func TestSavePreservesExecBitAndSymlink(t *testing.T) {
	t.Run("exec bit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "run.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatalf("seed: %v", err)
		}
		tab, err := NewTab(path)
		if err != nil {
			t.Fatalf("NewTab: %v", err)
		}
		tab.InsertString("# edited\n")
		if err := tab.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0755 {
			t.Fatalf("mode after save = %v, want 0755", got)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		real := filepath.Join(dir, "real.txt")
		link := filepath.Join(dir, "link.txt")
		if err := os.WriteFile(real, []byte("old\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		tab, err := NewTab(link)
		if err != nil {
			t.Fatalf("NewTab: %v", err)
		}
		tab.InsertString("new ")
		if err := tab.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("lstat: %v", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("save replaced the symlink with a regular file")
		}
		got, err := os.ReadFile(real)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != "new old\n" {
			t.Fatalf("target = %q, want %q", got, "new old\n")
		}
	})
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

// TestReloadKeepHistory_UndoRestoresPreReloadContent pins the whole point
// of this method: a reload the user didn't ask for (format-on-save, the
// background reconcile) must not destroy their prior edits. The pre-reload
// buffer has to still be one Undo away, and Redo has to be able to move
// forward again to the disk content.
func TestReloadKeepHistory_UndoRestoresPreReloadContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("edited-")
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	preReload := tab.Buffer.String()

	if err := os.WriteFile(path, []byte("formatted-on-disk"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.ReloadKeepHistory(); err != nil {
		t.Fatalf("ReloadKeepHistory: %v", err)
	}
	if got := tab.Buffer.String(); got != "formatted-on-disk" {
		t.Fatalf("buffer after reload = %q, want formatted-on-disk", got)
	}
	if !tab.CanUndo() {
		t.Fatal("expected undo history to survive ReloadKeepHistory")
	}

	if !tab.Undo() {
		t.Fatal("Undo should succeed")
	}
	if got := tab.Buffer.String(); got != preReload {
		t.Fatalf("after undo = %q, want %q", got, preReload)
	}

	if !tab.Redo() {
		t.Fatal("Redo should succeed")
	}
	if got := tab.Buffer.String(); got != "formatted-on-disk" {
		t.Fatalf("after redo = %q, want formatted-on-disk", got)
	}
}

// TestReloadKeepHistory_ShorterFileClampsOnUndo reloads to a file much
// shorter than the pre-reload buffer, then rounds trip Undo/Redo. The
// cursor/anchor restored by applySnapshot must stay in range of whatever
// buffer they're paired with rather than panicking.
func TestReloadKeepHistory_ShorterFileClampsOnUndo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("line one\nline two\nline three\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Cursor = Position{Line: 2, Col: 5}
	tab.Anchor = tab.Cursor

	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.ReloadKeepHistory(); err != nil {
		t.Fatalf("ReloadKeepHistory: %v", err)
	}

	if !tab.Undo() {
		t.Fatal("Undo should succeed")
	}
	if tab.Cursor.Line >= len(tab.Buffer.Lines) {
		t.Fatalf("cursor line out of range after undo: %+v (lines=%d)", tab.Cursor, len(tab.Buffer.Lines))
	}

	if !tab.Redo() {
		t.Fatal("Redo should succeed")
	}
	if tab.Cursor.Line >= len(tab.Buffer.Lines) {
		t.Fatalf("cursor line out of range after redo: %+v (lines=%d)", tab.Cursor, len(tab.Buffer.Lines))
	}
}

// TestReload_StillClearsHistory pins that the plain Reload() contract used
// by the disk-conflict prompt's explicit "Reload" button is unchanged:
// the user chose to take the disk version, so undo history is wiped.
func TestReload_StillClearsHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("edited-")

	if err := os.WriteFile(path, []byte("disk-version"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if tab.CanUndo() {
		t.Fatal("plain Reload should still clear undo history")
	}
}

// TestReload_RefusesGrownFile pins the shared read gate: a file that grew
// past maxOpenBytes since it was opened must be refused by both reload
// variants, and — since a refusal must leave no partial state — the
// buffer has to still hold exactly what was there before the reload was
// attempted.
func TestReload_RefusesGrownFile(t *testing.T) {
	for _, variant := range []struct {
		name   string
		reload func(*Tab) error
	}{
		{"Reload", (*Tab).Reload},
		{"ReloadKeepHistory", (*Tab).ReloadKeepHistory},
	} {
		t.Run(variant.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "x.txt")
			if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			tab, err := NewTab(path)
			if err != nil {
				t.Fatalf("NewTab: %v", err)
			}

			// Grow the file past the cap externally — sparse, so the test
			// doesn't actually write 32 MiB to disk.
			f, err := os.OpenFile(path, os.O_WRONLY, 0644)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			if err := f.Truncate(maxOpenBytes + 1); err != nil {
				f.Close()
				t.Fatalf("truncate: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			if err := variant.reload(tab); !errors.Is(err, ErrFileTooLarge) {
				t.Fatalf("want ErrFileTooLarge, got %v", err)
			}
			if got := tab.Buffer.String(); got != "original" {
				t.Fatalf("buffer mutated by refused reload: %q", got)
			}
		})
	}
}

// TestReload_RefusesNowBinaryFile pins that a file which turned binary
// after it was opened — e.g. `git checkout` swapping in a build artifact
// at the same path — is refused by both reload variants, and that the
// refusal leaves the tab's existing buffer untouched rather than partly
// applying the new (rejected) content.
func TestReload_RefusesNowBinaryFile(t *testing.T) {
	for _, variant := range []struct {
		name   string
		reload func(*Tab) error
	}{
		{"Reload", (*Tab).Reload},
		{"ReloadKeepHistory", (*Tab).ReloadKeepHistory},
	} {
		t.Run(variant.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "x.txt")
			if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			tab, err := NewTab(path)
			if err != nil {
				t.Fatalf("NewTab: %v", err)
			}

			data := append([]byte("PK\x03\x04"), make([]byte, 64)...)
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			if err := variant.reload(tab); !errors.Is(err, ErrBinaryFile) {
				t.Fatalf("want ErrBinaryFile, got %v", err)
			}
			if got := tab.Buffer.String(); got != "original" {
				t.Fatalf("buffer mutated by refused reload: %q", got)
			}
		})
	}
}

// TestReload_RefusesNowInvalidUTF8 pins that a file which turned invalid
// UTF-8 after it was opened — e.g. someone re-saved it from an editor set
// to Latin-1 — is refused by both reload variants, with the tab's existing
// (valid) buffer left completely intact rather than partly applying the
// rejected content. Same shape as TestReload_RefusesNowBinaryFile and
// TestReload_RefusesGrownFile: the shared readTextFile gate must catch
// this on reload exactly as it does on first open.
func TestReload_RefusesNowInvalidUTF8(t *testing.T) {
	for _, variant := range []struct {
		name   string
		reload func(*Tab) error
	}{
		{"Reload", (*Tab).Reload},
		{"ReloadKeepHistory", (*Tab).ReloadKeepHistory},
	} {
		t.Run(variant.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "x.txt")
			if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			tab, err := NewTab(path)
			if err != nil {
				t.Fatalf("NewTab: %v", err)
			}

			// Raw 0xE9 is Latin-1 "é" — not valid UTF-8 on its own, and no
			// NUL byte, so looksBinary must not be the thing that catches it.
			if err := os.WriteFile(path, []byte("caf\xe9 au lait\n"), 0644); err != nil {
				t.Fatalf("rewrite: %v", err)
			}

			if err := variant.reload(tab); !errors.Is(err, ErrNotUTF8) {
				t.Fatalf("want ErrNotUTF8, got %v", err)
			}
			if got := tab.Buffer.String(); got != "original" {
				t.Fatalf("buffer mutated by refused reload: %q", got)
			}
		})
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

// TestTab_EveryMutatorGoesThroughEdit pins the trailer the edit seam
// owns, for every mutator in the package. After any text mutation the
// tab must be dirty, its highlight cache stale, its caret flagged as
// moved (and that flag consumed by the next Render), and one undo entry
// must roll the whole change back. Ten hand-typed copies of that trailer
// had already drifted apart — the find-match refresh only ever reached
// three of them — so this table is what stops the eleventh.
func TestTab_EveryMutatorGoesThroughEdit(t *testing.T) {
	const seed = "alpha\nbeta\ngamma"
	cases := []struct {
		name  string
		setup func(*Tab) // cursor / selection / find state the mutator needs
		apply func(*Tab)
		want  string
	}{
		{
			name:  "DeleteSelection",
			setup: func(tb *Tab) { tb.Anchor = Position{}; tb.Cursor = Position{Line: 0, Col: 2} },
			apply: func(tb *Tab) { tb.DeleteSelection() },
			want:  "pha\nbeta\ngamma",
		},
		{
			name:  "InsertString",
			apply: func(tb *Tab) { tb.InsertString("xy") },
			want:  "xyalpha\nbeta\ngamma",
		},
		{
			name:  "InsertString over a selection",
			setup: func(tb *Tab) { tb.Anchor = Position{}; tb.Cursor = Position{Line: 0, Col: 5} },
			apply: func(tb *Tab) { tb.InsertString("xy") },
			want:  "xy\nbeta\ngamma",
		},
		{
			name:  "InsertNewline",
			setup: func(tb *Tab) { tb.Cursor = Position{Line: 0, Col: 2}; tb.Anchor = tb.Cursor },
			apply: func(tb *Tab) { tb.InsertNewline() },
			want:  "al\npha\nbeta\ngamma",
		},
		{
			name:  "InsertRune",
			apply: func(tb *Tab) { tb.InsertRune('x') },
			want:  "xalpha\nbeta\ngamma",
		},
		{
			name:  "InsertRune over a selection",
			setup: func(tb *Tab) { tb.Anchor = Position{}; tb.Cursor = Position{Line: 0, Col: 5} },
			apply: func(tb *Tab) { tb.InsertRune('x') },
			want:  "x\nbeta\ngamma",
		},
		{
			name:  "Backspace",
			setup: func(tb *Tab) { tb.Cursor = Position{Line: 0, Col: 2}; tb.Anchor = tb.Cursor },
			apply: func(tb *Tab) { tb.Backspace() },
			want:  "apha\nbeta\ngamma",
		},
		{
			name:  "Delete",
			apply: func(tb *Tab) { tb.Delete() },
			want:  "lpha\nbeta\ngamma",
		},
		{
			name:  "ToggleLineComment",
			apply: func(tb *Tab) { tb.ToggleLineComment() },
			want:  "// alpha\nbeta\ngamma",
		},
		{
			name:  "ReplaceCurrentMatch",
			setup: func(tb *Tab) { tb.SetFindQuery("beta") },
			apply: func(tb *Tab) { tb.ReplaceCurrentMatch("x") },
			want:  "alpha\nx\ngamma",
		},
		{
			name:  "ReplaceAllMatches",
			setup: func(tb *Tab) { tb.SetFindQuery("a") },
			apply: func(tb *Tab) { tb.ReplaceAllMatches("") },
			want:  "lph\nbet\ngmm",
		},
		{
			name:  "ReplaceLines",
			apply: func(tb *Tab) { tb.ReplaceLines(map[int]string{1: "BETA"}) },
			want:  "alpha\nBETA\ngamma",
		},
		{
			name:  "MoveLinesUp",
			setup: func(tb *Tab) { tb.Cursor = Position{Line: 1}; tb.Anchor = tb.Cursor },
			apply: func(tb *Tab) { tb.MoveLinesUp() },
			want:  "beta\nalpha\ngamma",
		},
		{
			name:  "MoveLinesDown",
			apply: func(tb *Tab) { tb.MoveLinesDown() },
			want:  "beta\nalpha\ngamma",
		},
		{
			name:  "DuplicateLines",
			apply: func(tb *Tab) { tb.DuplicateLines() },
			want:  "alpha\nalpha\nbeta\ngamma",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := &Tab{Path: "main.go", Buffer: NewBuffer(seed), IndentUnit: "\t"}
			tab.initUndo()
			if tc.setup != nil {
				tc.setup(tab)
			}

			// Render first so the trailer's flags start from the state a
			// painted editor is actually in: it consumes cursorMoved and
			// clears StyleStale, which is what makes the assertions below
			// evidence that the MUTATION set them rather than the setup.
			scr := newSimScreen(t, 40, 10)
			defer scr.Fini()
			tab.Render(scr, theme.Default(), 0, 0, 40, 10)
			tab.Dirty, tab.StyleStale = false, false

			tc.apply(tab)

			if got := tab.Buffer.String(); got != tc.want {
				t.Fatalf("buffer:\n%q\nwant:\n%q", got, tc.want)
			}
			if !tab.Dirty {
				t.Error("Dirty not set — the tab would close without a save prompt")
			}
			if !tab.StyleStale {
				t.Error("StyleStale not set — the highlight cache would paint the old text")
			}
			if !tab.CanUndo() {
				t.Error("no undo entry pushed — the edit is unrecoverable")
			}
			if !tab.cursorMoved {
				t.Error("cursorMoved not set — the next Render would not scroll the caret into view")
			}
			tab.Render(scr, theme.Default(), 0, 0, 40, 10)
			if tab.cursorMoved {
				t.Error("Render must consume cursorMoved, or wheel scrolling fights every repaint")
			}
			if !tab.Undo() || tab.Buffer.String() != seed {
				t.Fatalf("one undo should restore the seed, got %q", tab.Buffer.String())
			}
		})
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

// TestTab_RestoreView covers the happy path of the seam the app puts a
// remembered place back through: the caret lands where it was, the
// selection collapses, the viewport takes the saved line, and both the
// cursorMoved flag and the undo-group break happen — the two things the
// old hand-written Cursor/Anchor/ScrollY assignments in the app kept
// forgetting.
func TestTab_RestoreView(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer(strings.Repeat("hello\n", 50))}
	tab.initUndo()
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 3} // a live selection to collapse
	tab.InsertRune('x')                    // opens a typing coalesce window
	tab.cursorMoved = false

	tab.RestoreView(Position{Line: 20, Col: 2}, 15)

	if tab.Cursor != (Position{Line: 20, Col: 2}) {
		t.Fatalf("cursor: %+v", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatalf("anchor should collapse onto the cursor: %+v", tab.Anchor)
	}
	if tab.ScrollY != 15 || tab.ScrollSeg != 0 {
		t.Fatalf("scroll: ScrollY=%d ScrollSeg=%d, want 15/0", tab.ScrollY, tab.ScrollSeg)
	}
	if !tab.cursorMoved {
		t.Fatal("RestoreView must flag the cursor as moved or Render won't scroll to it")
	}
	if tab.lastUndoGroup != undoGroupNone {
		t.Fatalf("undo group %v still open — typing would coalesce across the restore", tab.lastUndoGroup)
	}
}

// TestTab_RestoreView_ClampsGarbage is the failure mode: session files
// and reopen records are stale by nature (the file shrank, or the entry
// was hand-edited), so a place that no longer exists must land somewhere
// real instead of parking the caret past the end of the buffer.
func TestTab_RestoreView_ClampsGarbage(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("one\ntwo")}
	tab.initUndo()

	tab.RestoreView(Position{Line: 99, Col: 99}, -5)

	if tab.Cursor != (Position{Line: 1, Col: 3}) {
		t.Fatalf("cursor should clamp to the end of the buffer, got %+v", tab.Cursor)
	}
	if tab.ScrollY != 0 {
		t.Fatalf("negative scroll should clamp to 0, got %d", tab.ScrollY)
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

// TestTab_Render_HighlightsSelection pins that the selection is actually
// painted, not merely survived: every cell inside the selected span
// carries the theme's Selection background and the first cell outside it
// does not. The cursor's landing column is checked alongside because the
// two are computed from the same visual-column walk.
func TestTab_Render_HighlightsSelection(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()
	th := theme.Default()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}

	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()

	cells, w, _ := scr.GetContents()
	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	for off := range 5 {
		_, bg, _ := cells[0*w+contentX+off].Style.Decompose()
		if bg != th.Selection {
			t.Errorf("cell %d of the selection has bg %v, want Selection %v", off, bg, th.Selection)
		}
	}
	if _, bg, _ := cells[0*w+contentX+5].Style.Decompose(); bg == th.Selection {
		t.Error("the space after the selection was painted as selected")
	}

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

// TestTab_HitTest_WideGlyphs is the click half of wide-character support:
// a cell offset has to be converted through cell widths, not rune counts,
// or every click past a CJK run lands one rune left per ideograph. Both
// cells of a glyph belong to it, so clicking either half selects it.
func TestTab_HitTest_WideGlyphs(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("日本x")
	contentX := defaultGutterWidth + 1

	cases := []struct{ cell, want int }{
		{0, 0}, // left half of 日
		{1, 0}, // right half of 日 snaps back onto it
		{2, 1}, // left half of 本
		{3, 1},
		{4, 2}, // the 'x' sits at cell 4, not cell 2
		{5, 3}, // past the end
	}
	for _, c := range cases {
		pos, ok := tab.HitTest(contentX+c.cell, 0, 40, 10)
		if !ok {
			t.Fatalf("cell %d: expected ok", c.cell)
		}
		if pos.Col != c.want {
			t.Errorf("click at cell %d = col %d, want %d", c.cell, pos.Col, c.want)
		}
	}
}

// TestTab_Render_WideGlyphOwnsTwoCells checks the paint side. A width-2
// glyph goes in the first of its two cells and the second is left as a
// blank the screen owns — tcell reports it with no runes at all — so the
// next character starts two cells along and the hardware cursor lands on
// the same grid the text was painted into.
func TestTab_Render_WideGlyphOwnsTwoCells(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("日本x")
	tab.Cursor = Position{Line: 0, Col: 2} // after both ideographs
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 4)
	scr.Show()

	cells, _, _ := scr.GetContents()
	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	for _, want := range []struct {
		off int
		r   rune
	}{{0, '日'}, {2, '本'}, {4, 'x'}} {
		got := cells[contentX+want.off].Runes
		if len(got) == 0 || got[0] != want.r {
			t.Errorf("cell %d holds %q, want %q", want.off, string(got), string(want.r))
		}
	}
	for _, off := range []int{1, 3} {
		if got := cells[contentX+off].Runes; len(got) != 0 {
			t.Errorf("cell %d should be the second half of a wide glyph, got %q", off, string(got))
		}
	}

	cx, _, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor hidden")
	}
	if cx != contentX+4 {
		t.Errorf("cursor x = %d, want %d (two ideographs = four cells)", cx, contentX+4)
	}
}

// TestTab_Render_CombiningMarkRidesItsBase pins the other half of the
// cluster paint: a mark is not given a cell of its own (tcell would render
// it as a blank, dropping the accent) but travels as a combining rune in
// its base's cell, and the character after it starts one cell along.
func TestTab_Render_CombiningMarkRidesItsBase(t *testing.T) {
	scr := newSimScreen(t, 40, 4)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("e\u0301x")

	tab.Render(scr, theme.Default(), 0, 0, 40, 4)
	scr.Show()

	cells, _, _ := scr.GetContents()
	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	if got := cells[contentX].Runes; len(got) != 2 || got[0] != 'e' || got[1] != '\u0301' {
		t.Errorf("first cell holds %q, want the e and its combining acute", string(got))
	}
	if got := cells[contentX+1].Runes; len(got) == 0 || got[0] != 'x' {
		t.Errorf("second cell holds %q, want the x one cell along", string(got))
	}
}

// TestTab_Render_PanKeepsWideGlyphsWhole pins horizontal panning over
// wide text: the pan starts at a cluster boundary, so the leftmost column
// can never show the tail half of a glyph whose head scrolled off. Cell 0
// carries the '‹' overflow hint (it always covers the first content cell,
// wide or not); everything after it must be the untouched remainder of the
// line at its true cell offsets.
func TestTab_Render_PanKeepsWideGlyphsWhole(t *testing.T) {
	scr := newSimScreen(t, 20, 3)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("日本語x")
	tab.ScrollX = 1 // pan 日 off the left edge
	tab.cursorMoved = false

	tab.Render(scr, theme.Default(), 0, 0, 20, 3)
	scr.Show()

	cells, _, _ := scr.GetContents()
	contentX := gutterWidthFor(tab.Buffer.LineCount()) + 1
	want := []string{"‹", " ", "語", "", "x"}
	for off, w := range want {
		got := string(cells[contentX+off].Runes)
		if got != w {
			t.Errorf("cell %d holds %q, want %q", off, got, w)
		}
	}
}

// TestTab_Backspace_RemovesWholeCluster is the deletion contract: one
// press removes one character as a person counts them, never a fragment
// that leaves a combining mark orphaned onto the letter in front of it.
func TestTab_Backspace_RemovesWholeCluster(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"emoji wearing a combining mark", "x😀\u0301", "x"},
		{"letter with a combining acute", "xe\u0301", "x"},
		{"zwj family", "x👨\u200d👩\u200d👦", "x"},
		{"regional indicator pair", "x🇯🇵", "x"},
		{"plain ascii still deletes one rune", "xy", "x"},
	}
	for _, c := range cases {
		tab := &Tab{Buffer: NewBuffer(c.text)}
		tab.Cursor = tab.Buffer.EndPos()
		tab.Anchor = tab.Cursor
		tab.Backspace()
		if got := tab.Buffer.String(); got != c.want {
			t.Errorf("%s: buffer %q, want %q", c.name, got, c.want)
		}
		if tab.Cursor.Col != len([]rune(c.want)) {
			t.Errorf("%s: cursor col %d, want %d", c.name, tab.Cursor.Col, len([]rune(c.want)))
		}
	}
}

// TestTab_Delete_RemovesWholeCluster mirrors Backspace forwards: a delete
// may not behead a character and leave its marks behind.
func TestTab_Delete_RemovesWholeCluster(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("e\u0301😀x")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Delete()
	if got := tab.Buffer.String(); got != "😀x" {
		t.Fatalf("after deleting the é cluster: %q", got)
	}
	tab.Delete()
	if got := tab.Buffer.String(); got != "x" {
		t.Fatalf("after deleting the emoji: %q", got)
	}
}

// TestTab_MoveCursor_StepsByCluster walks an arrow key across text whose
// characters are several runes long. Each press must cross exactly one
// character in each direction, and the two directions must retrace the
// same stops.
func TestTab_MoveCursor_StepsByCluster(t *testing.T) {
	// é (runes 0-1), 日 (2), family (3-7), z (8) — nine runes, four
	// characters, five caret stops.
	tab := &Tab{Buffer: NewBuffer("e\u0301日👨\u200d👩\u200d👦z")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	for _, want := range []int{2, 3, 8, 9, 9} {
		tab.MoveCursor(0, 1, false)
		if tab.Cursor.Col != want {
			t.Fatalf("right landed on col %d, want %d", tab.Cursor.Col, want)
		}
	}
	for _, want := range []int{8, 3, 2, 0, 0} {
		tab.MoveCursor(0, -1, false)
		if tab.Cursor.Col != want {
			t.Fatalf("left landed on col %d, want %d", tab.Cursor.Col, want)
		}
	}
}

// TestTab_EnsureVisible_PansByCells pins the horizontal scroll fix: with
// wide text, "the caret is off the right edge" is a question about cells.
// Counting runes instead under-scrolls and hides the caret entirely — a
// cursor the renderer then refuses to show.
func TestTab_EnsureVisible_PansByCells(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer(strings.Repeat("日", 40))}
	tab.Cursor = Position{Line: 0, Col: 30}
	tab.Anchor = tab.Cursor

	const viewW = 26 // gutter 6 + 1 → 19 content cells
	tab.EnsureVisible(viewW, 10)

	runes := tab.Buffer.LineRunes(0)
	contentW := viewW - gutterWidthFor(tab.Buffer.LineCount()) - 1
	cursorVisual := LineVisualCol(runes, tab.Cursor.Col)
	scrollVisual := LineVisualCol(runes, tab.ScrollX)
	if cursorVisual < scrollVisual || cursorVisual >= scrollVisual+contentW {
		t.Fatalf("caret at cell %d is outside the panned window [%d,%d)",
			cursorVisual, scrollVisual, scrollVisual+contentW)
	}
	if got := ClusterStart(runes, tab.ScrollX); got != tab.ScrollX {
		t.Errorf("ScrollX %d is inside a cluster starting at %d", tab.ScrollX, got)
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

// TestNewTab_RefusesInvalidUTF8 pins the UTF-8 validity gate: a file with
// one raw non-UTF-8 byte (no NUL, so looksBinary must not be what catches
// this) is refused rather than silently loaded — Buffer.LineRunes would
// otherwise decode the invalid byte to U+FFFD, and editing that line would
// permanently rewrite it on save.
func TestNewTab_RefusesInvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latin1.txt")
	// Raw 0xE9 is Latin-1 "é" and is not valid UTF-8 on its own.
	data := []byte("caf\xe9 au lait\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := NewTab(path); !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("want ErrNotUTF8, got %v", err)
	}
}

// TestNewTab_AcceptsMultibyteUTF8 guards the guard: valid multibyte UTF-8
// — CJK, an emoji ZWJ sequence, and a combining mark — must still open
// normally rather than being caught by the new validity check.
func TestNewTab_AcceptsMultibyteUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multibyte.txt")
	// 高さ (CJK), a family emoji built from a ZWJ sequence, and "é" spelled
	// as e + combining acute (U+0301).
	data := []byte("高さ\n👨‍👩‍👧‍👦\ncafé\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("valid multibyte UTF-8 must open, got %v", err)
	}
	if len(tab.Buffer.Lines) < 3 {
		t.Fatalf("buffer looks wrong: %d lines", len(tab.Buffer.Lines))
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

// TestNewTab_RefusesOversizedFile pins the open size cap. The file is a
// sparse 33 MiB of NUL bytes, which means the assertion also pins the
// ORDER of the two gates: had NewTab read the file before checking its
// size, looksBinary would have won and the error would be
// ErrBinaryFile. Getting ErrFileTooLarge back is proof the Stat guard
// fired first and the 33 MiB never entered a buffer. The message must
// name both sizes — the app surfaces it verbatim as a status flash, and
// "too large" with no number leaves the user guessing.
func TestNewTab_RefusesOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sparse: no bytes are actually written, so the test costs an inode.
	if err := f.Truncate(maxOpenBytes + 1<<20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tab, err := NewTab(path)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("want ErrFileTooLarge, got %v", err)
	}
	if errors.Is(err, ErrBinaryFile) {
		t.Fatal("size gate must run before the binary probe, i.e. before the read")
	}
	if tab != nil {
		t.Fatal("a refused open must not hand back a tab")
	}
	for _, want := range []string{"huge.log", "33.0 MiB", "32.0 MiB"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should name %q", err, want)
		}
	}
}

// TestNewTab_OpensFileUnderCap guards the other side of the gate: a
// normal file is nowhere near the cap and must still open with its
// content and mtime intact — the Stat hoisted above the read is now the
// only source of Mtime, so a regression there would go unnoticed by the
// size assertions alone.
func TestNewTab_OpensFileUnderCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if got := tab.Buffer.Lines[0]; got != "one" {
		t.Fatalf("first line = %q, want \"one\"", got)
	}
	if !tab.Mtime.Equal(info.ModTime()) {
		t.Fatalf("mtime = %v, want %v", tab.Mtime, info.ModTime())
	}
}

// TestNewTab_ImageOverSizeCapRefused pins that NewTab's image branch is
// covered by the same size cap as the text path, even though the branch
// still runs first — decodeImageFile carries its own Stat guard (see
// image.go), so a sparse oversize file named like an image is refused
// without ever attempting a decode. Naming it x.png rather than x.txt is
// the point: the image extension routes into newImageTab before any of
// NewTab's own text-path guards would see the file at all.
func TestNewTab_ImageOverSizeCapRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(maxOpenBytes + 1); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	tab, err := NewTab(path)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("want ErrFileTooLarge, got %v", err)
	}
	if tab != nil {
		t.Fatal("a refused open must not hand back a tab")
	}
}

// TestMibString pins the size rendering the refusal message depends on:
// one decimal place, MiB unit, and no rounding surprise at the cap
// itself (a "32.0 MiB (limit 32.0 MiB)" message would read as a bug).
func TestMibString(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.0 MiB"},
		{maxOpenBytes, "32.0 MiB"},
		{maxOpenBytes + 1<<19, "32.5 MiB"},
		{512 << 20, "512.0 MiB"},
	}
	for _, c := range cases {
		if got := mibString(c.in); got != c.want {
			t.Fatalf("mibString(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// newIndentTab writes content to a real file and opens it, so IndentUnit
// comes from DetectIndent rather than a hand-set field — the point of the
// auto-indent tests is that Enter follows the FILE's convention.
func newIndentTab(t *testing.T, name, content string) *Tab {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	return tab
}

// TestTab_InsertNewline_FollowsDetectedIndentStyle is the headline
// behavior: Enter carries the current line's indentation onto the new
// line, in the characters the file itself uses. A tab-indented Go file
// must never gain spaces, and a space-indented file must never gain tabs.
func TestTab_InsertNewline_FollowsDetectedIndentStyle(t *testing.T) {
	tabbed := newIndentTab(t, "main.go", "func f() {\n\tx := 1\n}\n")
	if tabbed.IndentUnit != "\t" {
		t.Fatalf("precondition: IndentUnit = %q, want a tab", tabbed.IndentUnit)
	}
	tabbed.Cursor = Position{Line: 1, Col: 7} // end of "\tx := 1"
	tabbed.Anchor = tabbed.Cursor
	tabbed.InsertNewline()
	if got := tabbed.Buffer.Lines[2]; got != "\t" {
		t.Fatalf("tab file: new line = %q, want %q", got, "\t")
	}
	if want := (Position{Line: 2, Col: 1}); tabbed.Cursor != want {
		t.Fatalf("tab file: cursor = %v, want %v", tabbed.Cursor, want)
	}

	spaced := newIndentTab(t, "app.js", "function f() {\n  let x = 1;\n}\n")
	if spaced.IndentUnit != "  " {
		t.Fatalf("precondition: IndentUnit = %q, want two spaces", spaced.IndentUnit)
	}
	spaced.Cursor = Position{Line: 1, Col: 12} // end of "  let x = 1;"
	spaced.Anchor = spaced.Cursor
	spaced.InsertNewline()
	if got := spaced.Buffer.Lines[2]; got != "  " {
		t.Fatalf("space file: new line = %q, want two spaces", got)
	}
}

// TestTab_InsertNewline_AddsLevelAfterOpener checks the extra level a
// block opener earns, end to end and in the file's own unit.
func TestTab_InsertNewline_AddsLevelAfterOpener(t *testing.T) {
	tab := newIndentTab(t, "main.go", "func f() {\n\tif x {\n\t}\n}\n")
	tab.Cursor = Position{Line: 1, Col: 7} // just after "\tif x {"
	tab.Anchor = tab.Cursor
	tab.InsertNewline()
	if got := tab.Buffer.Lines[2]; got != "\t\t" {
		t.Fatalf("after '{': new line = %q, want two tabs", got)
	}

	py := newIndentTab(t, "s.py", "def f():\n    if x:\n        pass\n")
	py.Cursor = Position{Line: 1, Col: 9} // just after "    if x:"
	py.Anchor = py.Cursor
	py.InsertNewline()
	if got := py.Buffer.Lines[2]; got != "        " {
		t.Fatalf("after ':': new line = %q, want eight spaces", got)
	}
}

// TestTab_InsertNewline_IsOneUndoStep pins the undo contract: one Enter is
// one step, whether or not it carried an indent, and whether or not it
// replaced a selection. Two entries would strand the user on a
// half-indented line after a single Undo.
func TestTab_InsertNewline_IsOneUndoStep(t *testing.T) {
	tab := newIndentTab(t, "main.go", "func f() {\n\tx := 1\n}\n")
	before := tab.Buffer.String()
	depth := len(tab.undoStack)

	tab.Cursor = Position{Line: 1, Col: 7}
	tab.Anchor = tab.Cursor
	tab.InsertNewline()
	if got := len(tab.undoStack) - depth; got != 1 {
		t.Fatalf("plain Enter pushed %d undo entries, want 1", got)
	}
	if !tab.Undo() || tab.Buffer.String() != before {
		t.Fatalf("one Undo did not restore the buffer:\n%q", tab.Buffer.String())
	}

	// With a selection: DeleteSelection's step is the only one recorded.
	depth = len(tab.undoStack)
	tab.Anchor = Position{Line: 1, Col: 1}
	tab.Cursor = Position{Line: 1, Col: 6}
	tab.InsertNewline()
	if got := len(tab.undoStack) - depth; got != 1 {
		t.Fatalf("Enter over a selection pushed %d undo entries, want 1", got)
	}
	if !tab.Undo() || tab.Buffer.String() != before {
		t.Fatalf("one Undo after a selection-replacing Enter left:\n%q", tab.Buffer.String())
	}
}

// TestTab_InsertNewline_UsesSelectionStartIndent covers the case where the
// caret is not where the split lands: with a selection, the new line
// inherits the indentation of the line the selection STARTS on, which is
// the line that survives the delete.
func TestTab_InsertNewline_UsesSelectionStartIndent(t *testing.T) {
	tab := newIndentTab(t, "main.go", "\t\talpha\nbeta\n")
	tab.Anchor = Position{Line: 0, Col: 6} // inside "\t\talpha"
	tab.Cursor = Position{Line: 1, Col: 2} // inside "beta"
	tab.InsertNewline()

	if got := tab.Buffer.Lines[0]; got != "\t\talph" {
		t.Fatalf("line 0 = %q", got)
	}
	if got := tab.Buffer.Lines[1]; got != "\t\tta" {
		t.Fatalf("line 1 = %q, want the selection-start indent plus the tail", got)
	}
}

// TestTab_InsertNewline_SplitInsideIndent keeps a mid-indentation Enter
// from shifting the code: the text that moves down keeps its own leading
// whitespace, and the new line only inherits what was actually behind the
// caret, so the visible column of the code is unchanged.
func TestTab_InsertNewline_SplitInsideIndent(t *testing.T) {
	tab := newIndentTab(t, "app.js", "        code();\n")
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.InsertNewline()

	if got := tab.Buffer.Lines[0]; got != "  " {
		t.Fatalf("line 0 = %q", got)
	}
	if got := tab.Buffer.Lines[1]; got != "        code();" {
		t.Fatalf("line 1 = %q — the code moved column", got)
	}
}

// TestTab_InsertNewline_NeverInsertsCR guards the CRLF round trip: buffer
// lines carry no terminator, so an Enter in a CRLF file must add "\n" only
// and let Save re-join with the recorded ending.
func TestTab_InsertNewline_NeverInsertsCR(t *testing.T) {
	tab := newIndentTab(t, "win.txt", "  alpha\r\n  beta\r\n")
	if tab.LineEnding != LineEndingCRLF {
		t.Fatalf("precondition: LineEnding = %v, want CRLF", tab.LineEnding)
	}
	tab.Cursor = Position{Line: 0, Col: 7}
	tab.Anchor = tab.Cursor
	tab.InsertNewline()
	for i, line := range tab.Buffer.Lines {
		if strings.ContainsRune(line, '\r') {
			t.Fatalf("line %d picked up a CR: %q", i, line)
		}
	}
}

// TestTab_InsertNewline_IgnoresImageTabs keeps Enter from mutating a
// read-only image preview.
func TestTab_InsertNewline_IgnoresImageTabs(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("x"), Mode: imageMode}
	tab.InsertNewline()
	if tab.Buffer.LineCount() != 1 {
		t.Fatalf("image tab gained a line: %q", tab.Buffer.Lines)
	}
}

// TestTab_InsertNewline_UnderSoftWrap checks the interaction the anchor-
// based wrap design makes easy to get wrong: splitting a line that is
// currently wrapped must leave the caret on screen and the indent intact.
// Wrap keeps no cached layout, so there is nothing to invalidate here —
// this test is what keeps that true.
func TestTab_InsertNewline_UnderSoftWrap(t *testing.T) {
	scr := newSimScreen(t, 40, 6)
	defer scr.Fini()

	long := "\tif x {" + strings.Repeat(" // padding", 12)
	tab := newIndentTab(t, "main.go", long+"\n\t}\n")
	tab.Wrap = true
	tab.Render(scr, theme.Default(), 0, 0, 40, 6)

	// Split right after the opening brace, several visual rows up from
	// the end of the wrapped line.
	tab.Cursor = Position{Line: 0, Col: 7}
	tab.Anchor = tab.Cursor
	tab.InsertNewline()
	tab.Render(scr, theme.Default(), 0, 0, 40, 6)

	if got := tab.Buffer.Lines[0]; got != "\tif x {" {
		t.Fatalf("line 0 = %q", got)
	}
	if !strings.HasPrefix(tab.Buffer.Lines[1], "\t\t") {
		t.Fatalf("line 1 lost its extra indent level: %q", tab.Buffer.Lines[1])
	}
	if want := (Position{Line: 1, Col: 2}); tab.Cursor != want {
		t.Fatalf("cursor = %v, want %v", tab.Cursor, want)
	}
	if _, _, vis := scr.GetCursor(); !vis {
		t.Fatal("caret left the viewport after a wrapped-line split")
	}
}

// TestClampCursorToView_LineMode pins the caret-follows-scroll clamp in
// line mode: a caret stranded above the viewport by a viewport-only
// scroll lands on the first visible line, one stranded below lands on
// the last, the column carries over, and — the invariant that keeps the
// old yank-back bug dead — the clamped position is already visible, so
// EnsureVisible afterwards must not move the viewport.
func TestClampCursorToView_LineMode(t *testing.T) {
	var sb strings.Builder
	for i := range 100 {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.SetWrap(false)
	tab.MoveCursorTo(Position{Line: 50, Col: 3}, false)

	// Scrolled below the caret: clamp to the first visible line.
	tab.ScrollY = 60
	tab.ClampCursorToView(10)
	if tab.Cursor != (Position{Line: 60, Col: 3}) {
		t.Fatalf("above-view clamp = %+v, want {60 3}", tab.Cursor)
	}
	tab.EnsureVisible(80, 10)
	if tab.ScrollY != 60 {
		t.Fatalf("EnsureVisible moved the viewport to %d — yank-back is back", tab.ScrollY)
	}

	// Scrolled above the caret: clamp to the last visible line.
	tab.ScrollY = 10
	tab.ClampCursorToView(10)
	if tab.Cursor != (Position{Line: 19, Col: 3}) {
		t.Fatalf("below-view clamp = %+v, want {19 3}", tab.Cursor)
	}
	tab.EnsureVisible(80, 10)
	if tab.ScrollY != 10 {
		t.Fatalf("EnsureVisible moved the viewport to %d — yank-back is back", tab.ScrollY)
	}
}

// TestClampCursorToView_SelectionAndNoopGuards pins the two refusals: an
// active selection is never clobbered by the follow-scroll clamp, and a
// caret already inside the viewport is left byte-for-byte alone (no
// cursorMoved, so the render pass runs no EnsureVisible either).
func TestClampCursorToView_SelectionAndNoopGuards(t *testing.T) {
	var sb strings.Builder
	for i := range 50 {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	path := filepath.Join(t.TempDir(), "sel.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.SetWrap(false)

	// Selection guard.
	tab.MoveCursorTo(Position{Line: 5, Col: 0}, false)
	tab.MoveCursorTo(Position{Line: 6, Col: 2}, true) // extend
	tab.cursorMoved = false
	tab.ScrollY = 30
	tab.ClampCursorToView(10)
	if tab.Cursor != (Position{Line: 6, Col: 2}) || tab.Anchor != (Position{Line: 5, Col: 0}) {
		t.Fatalf("clamp clobbered the selection: cursor=%+v anchor=%+v", tab.Cursor, tab.Anchor)
	}
	if tab.cursorMoved {
		t.Fatal("selection guard must not mark the cursor moved")
	}

	// Noop guard.
	tab.MoveCursorTo(Position{Line: 32, Col: 1}, false)
	tab.cursorMoved = false
	tab.ClampCursorToView(10) // visible range [30,39] contains the caret
	if tab.Cursor != (Position{Line: 32, Col: 1}) {
		t.Fatalf("visible caret moved to %+v", tab.Cursor)
	}
	if tab.cursorMoved {
		t.Fatal("noop clamp must not mark the cursor moved")
	}
}
