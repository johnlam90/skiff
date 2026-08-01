// =============================================================================
// File: internal/app/gotoline_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the Go-to-line prompt flow and the OpenFileAtLine helper the
// CLI (`skiff file:42`) and project search funnel through.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedBigFile writes a 100-line file and returns its path — enough lines
// that a jump has somewhere real to land.
func seedBigFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "big.txt")
	content := strings.Repeat("line\n", 100)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return path
}

// TestMenuGoToLineJumps drives the full prompt flow: menuGoToLine opens
// the prompt modal, and submitting a number moves the active tab's
// cursor to that (1-based) line and closes the modal.
func TestMenuGoToLineJumps(t *testing.T) {
	dir := t.TempDir()
	path := seedBigFile(t, dir)
	a := newTestApp(t, dir)
	a.openFile(path)

	a.menuGoToLine()
	if !a.promptOpen {
		t.Fatal("menuGoToLine should open the prompt modal")
	}
	a.promptValue = []rune("42")
	a.promptSubmit()

	tab := a.activeTabPtr()
	if tab.Cursor.Line != 41 {
		t.Fatalf("cursor line: got %d, want 41", tab.Cursor.Line)
	}
	if a.promptOpen {
		t.Fatal("prompt should close after submit")
	}
}

// TestGoToLineRejectsGarbage pins the failure mode: non-numeric input
// must not move the cursor — the user gets a flash, not a surprise jump.
func TestGoToLineRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := seedBigFile(t, dir)
	a := newTestApp(t, dir)
	a.openFile(path)

	a.menuGoToLine()
	a.promptValue = []rune("abc")
	a.promptSubmit()

	tab := a.activeTabPtr()
	if tab.Cursor.Line != 0 {
		t.Fatalf("garbage input moved the cursor to line %d", tab.Cursor.Line)
	}
}

// TestOpenFileAtLine covers the CLI/`file:42` entry point: the file
// opens (or its tab activates) and the cursor lands on the requested
// line; line <= 0 degrades to a plain open.
func TestOpenFileAtLine(t *testing.T) {
	dir := t.TempDir()
	path := seedBigFile(t, dir)
	a := newTestApp(t, dir)

	a.OpenFileAtLine(path, 7)
	tab := a.activeTabPtr()
	if tab == nil || tab.Cursor.Line != 6 {
		t.Fatalf("cursor: got %+v, want line 6", tab)
	}

	a.OpenFileAtLine(path, 0)
	if tab.Cursor.Line != 6 {
		t.Fatalf("line 0 should leave the cursor alone, got %d", tab.Cursor.Line)
	}
}
