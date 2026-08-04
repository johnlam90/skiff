// =============================================================================
// File: internal/atomicfile/atomicfile_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteCreatesParentDir pins the "first write on a fresh machine"
// path: ~/.config/skiff doesn't exist yet, and callers shouldn't have
// to MkdirAll before every save.
func TestWriteCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "config.json")
	if err := Write(path, []byte(`{"theme":"nord"}`), 0644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != `{"theme":"nord"}` {
		t.Fatalf("content = %q", got)
	}
}

// TestWriteReplacesExistingFile covers the common case — the file is
// already there and must end up holding only the new bytes, with no
// trailing remains of a longer previous version.
func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Write(path, []byte("a much longer original body"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Write(path, []byte("short"), 0644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "short" {
		t.Fatalf("stale bytes survived the replace: %q", got)
	}
}

// TestWriteAppliesPerm verifies the final file carries the requested
// mode, not the 0600 the temp file is created with — config files are
// expected to be readable by the user's other tools.
func TestWriteAppliesPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Write(path, []byte("x"), 0644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("mode = %v, want 0644", got)
	}
}

// TestWriteLeavesNoTempOnSuccess guards the housekeeping half of the
// contract: a stray ".config.json.tmp-123" next to the real file would
// confuse anyone poking at ~/.config/skiff by hand.
func TestWriteLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "config.json"), []byte("x"), 0644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only config.json, got %v", names)
	}
}

// TestWriteCleansUpTempOnFailure: when the rename can't happen (here
// the target path is a directory), the temp file must not survive.
// Without the cleanup, a persistently failing save would litter the
// config dir on every attempt.
func TestWriteCleansUpTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if err := Write(target, []byte("x"), 0644); err == nil {
		t.Fatal("expected an error renaming over a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file %q leaked after a failed write", e.Name())
		}
	}
}

// TestWriteConcurrentWritersNeverTear is the reason the temp name is
// randomised: two skiff instances (or two goroutines) saving the same
// file must each land a complete document. A shared "path + .tmp"
// name lets one writer's partial bytes get renamed by the other.
func TestWriteConcurrentWritersNeverTear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	bodies := []string{
		strings.Repeat("a", 4096),
		strings.Repeat("b", 4096),
		strings.Repeat("c", 4096),
	}
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Write(path, []byte(bodies[i%len(bodies)]), 0644); err != nil {
				t.Errorf("Write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range bodies {
		if string(got) == want {
			return
		}
	}
	t.Fatalf("torn write: got %d bytes that match no single writer", len(got))
}
