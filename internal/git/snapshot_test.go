// =============================================================================
// File: internal/git/snapshot_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStatus_NonRepoIsExplicit pins the fact the Snapshot exists for:
// "not a repo" is IsRepo=false on the value, not an empty branch
// string a caller has to interpret.
func TestStatus_NonRepoIsExplicit(t *testing.T) {
	snap := Open(t.TempDir()).Status("")
	if snap.IsRepo {
		t.Fatal("a plain directory must report IsRepo=false")
	}
	if snap.Branch != "" || len(snap.Files) != 0 {
		t.Fatalf("non-repo snapshot must be zero, got %+v", snap)
	}
}

// TestStatus_RealRepoSnapshot is the contract test against real git:
// branch name, a modified file, and an untracked file all land in one
// consistent snapshot.
func TestStatus_RealRepoSnapshot(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("create: %v", err)
	}

	snap := Open(dir).Status("")
	if !snap.IsRepo || snap.Branch != "main" {
		t.Fatalf("snapshot header wrong: %+v", snap)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if kind := snap.Files[filepath.Join(resolved, "f.txt")]; kind != ChangeModified {
		t.Fatalf("f.txt kind = %v, want modified (files: %v)", kind, snap.Files)
	}
	if kind := snap.Files[filepath.Join(resolved, "new.txt")]; kind != ChangeAdded {
		t.Fatalf("new.txt kind = %v, want added", kind)
	}
	if snap.Ahead != 0 || snap.Behind != 0 {
		t.Fatalf("no upstream: ahead/behind must be 0/0, got %d/%d", snap.Ahead, snap.Behind)
	}
}

// TestParsePorcelain pins the porcelain v1 parsing: kinds per XY code,
// rename pairs marking both sides, and C-style quoted paths.
func TestParsePorcelain(t *testing.T) {
	out := []byte(" M mod.go\n?? new.go\nD  gone.go\nR  old.go -> new2.go\n M \"sp ace.go\"\n")
	got := parsePorcelain(out, "/repo")
	want := map[string]ChangeKind{
		"/repo/mod.go":    ChangeModified,
		"/repo/new.go":    ChangeAdded,
		"/repo/gone.go":   ChangeDeleted,
		"/repo/old.go":    ChangeDeleted,
		"/repo/new2.go":   ChangeRenamed,
		"/repo/sp ace.go": ChangeModified,
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Fatalf("%s = %v, want %v", path, got[path], kind)
		}
	}
}

// TestStatus_ViaFake pins the seam: a snapshot can be assembled from
// scripted responses with no git binary involved — the adapter tests
// need exactly this for unreachable states.
func TestStatus_ViaFake(t *testing.T) {
	fake := &Fake{}
	fake.Script("rev-parse --show-toplevel", "/repo\n", nil)
	fake.Script("rev-list --left-right --count @{upstream}...HEAD", "2\t3\n", nil)
	fake.Script("status --porcelain", " M a.go\n", nil)
	fake.Script("symbolic-ref --short HEAD", "feature\n", nil)
	snap := OpenWith("/repo", fake).Status("")
	if !snap.IsRepo || snap.Branch != "feature" || snap.Behind != 2 || snap.Ahead != 3 {
		t.Fatalf("snapshot from fake wrong: %+v", snap)
	}
	if snap.Files["/repo/a.go"] != ChangeModified {
		t.Fatalf("files wrong: %v", snap.Files)
	}
}
