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

// TestWrite_CreatesOwnerOnlyDirectories pins the directory side of the
// state/config confidentiality story: every caller that lets Write
// create the parent directory (config.json, the trust store, per-project
// sessions) relies on that directory being owner-only, not the 0755 a
// share-friendly project directory would use. Asserted as "no
// group/other bits" rather than an exact mode because MkdirAll's perm is
// capped by the process umask.
func TestWrite_CreatesOwnerOnlyDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir")
	path := filepath.Join(dir, "f.json")
	if err := Write(path, []byte("x"), 0600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("dir mode = %v, want no group/other bits", perm)
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

// TestWriteSyncsDirectory pins the syncDir seam Write relies on for
// crash safety: userspace can't observe whether the fsync reached the
// disk, but it can pin that syncDir succeeds on a real directory and
// degrades to nil (rather than failing the save) when the directory
// can't be opened — a failed dir sync after a successful rename must
// never report the save as failed.
func TestWriteSyncsDirectory(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatalf("syncDir on a real directory: %v", err)
	}
	if err := syncDir(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("syncDir on a missing path must degrade to nil, got %v", err)
	}
}

// TestReplacePreservesMode is the executable-script case: a user's
// 0755 file must still be 0755 after a save, not reset to Write's
// fixed config-file perm.
func TestReplacePreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Replace(path, []byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Fatalf("mode = %v, want 0755", got)
	}
}

// TestReplaceWritesThroughSymlink pins the resolve step: saving through
// a symlink must update the target file, not replace the link itself
// with a regular file — that's how editors silently detach a user's
// dotfile from its dotfiles repo.
func TestReplaceWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(real, []byte("old"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := Replace(link, []byte("new")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link.txt was replaced by a regular file, want it to stay a symlink")
	}
	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("target = %q, want %q", got, "new")
	}
}

// TestReplaceCreatesMissingFile covers the first save of a brand-new
// file: nothing to resolve, nothing to preserve, so it lands 0644.
func TestReplaceCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.txt")
	if err := Replace(path, []byte("hello")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("mode = %v, want 0644", got)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
}

// TestReplaceFailureKeepsOriginal is the whole point of the package
// applied to user files: when the write can't complete, the original
// bytes must be untouched and no temp droppings may remain.
func TestReplaceFailureKeepsOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions; the failure can't be provoked")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if err := Replace(path, []byte("replacement")); err == nil {
		t.Fatal("expected an error writing into a read-only directory")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("original clobbered by a failed Replace: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file %q leaked after a failed Replace", e.Name())
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
