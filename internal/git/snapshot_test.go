// =============================================================================
// File: internal/git/snapshot_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"fmt"
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

// TestParsePorcelain pins the -z porcelain parsing: kinds per XY code,
// rename pairs marking both sides (current path first, origin second —
// the reverse of the human format), and paths verbatim with no
// C-quoting. Two fixtures are the whole reason for -z: a path holding a
// newline (line framing can't survive it) and a path holding the
// literal " -> " the human format uses as its rename delimiter.
func TestParsePorcelain(t *testing.T) {
	out := []byte(" M mod.go\x00?? new.go\x00D  gone.go\x00R  new2.go\x00old.go\x00" +
		" M sp ace.go\x00D  we\nird.go\x00 M arrow -> in name.go\x00")
	got := parsePorcelain(out, "/repo")
	want := map[string]ChangeKind{
		"/repo/mod.go":              ChangeModified,
		"/repo/new.go":              ChangeAdded,
		"/repo/gone.go":             ChangeDeleted,
		"/repo/old.go":              ChangeDeleted,
		"/repo/new2.go":             ChangeRenamed,
		"/repo/sp ace.go":           ChangeModified,
		"/repo/we\nird.go":          ChangeDeleted,
		"/repo/arrow -> in name.go": ChangeModified,
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

// TestParsePorcelain_RealGitOutput is the format contract test: the -z
// framing (especially the rename entry's second field) is asserted
// against what the installed git actually emits, not against our memory
// of the docs.
//
// core.quotePath=false is the configuration that makes this a
// regression test rather than a smoke test: with quoting on, git
// C-escapes the newline and a line-wise parser survives by accident.
// Users with non-ASCII filenames routinely turn it off, and that is
// when the old parser split one entry into two garbage records and lost
// the rest of the status.
func TestParsePorcelain_RealGitOutput(t *testing.T) {
	dir := initRepo(t)
	gitT(t, dir, "config", "core.quotePath", "false")
	weird := filepath.Join(dir, "we\nird.go")
	writeSeed(t, weird, "x\n")
	writeSeed(t, filepath.Join(dir, "old.go"), "y\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "second")

	if err := os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "new2.go")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	writeSeed(t, weird, "changed\n")
	gitT(t, dir, "add", "-A")

	snap := Open(dir).Status("")
	root, _ := filepath.EvalSymlinks(dir)
	if got := snap.Files[filepath.Join(root, "we\nird.go")]; got != ChangeModified {
		t.Fatalf("newline path = %v, want modified (files: %v)", got, snap.Files)
	}
	if got := snap.Files[filepath.Join(root, "new2.go")]; got != ChangeRenamed {
		t.Fatalf("rename target = %v, want renamed (files: %v)", got, snap.Files)
	}
	if got := snap.Files[filepath.Join(root, "old.go")]; got != ChangeDeleted {
		t.Fatalf("rename origin = %v, want deleted (files: %v)", got, snap.Files)
	}
}

// TestDiffNameStatusArgs pins the argv the compare-against-ref loader
// builds: -z framing, the validated ref, and the `--` separator that
// stops a repo-supplied branch name from being read as an option. The
// rejected case is the actual exploit — a clone shipping a branch named
// --output=/tmp/x must produce no command at all.
func TestDiffNameStatusArgs(t *testing.T) {
	args, err := diffNameStatusArgs("origin/main")
	if err != nil {
		t.Fatalf("safe ref rejected: %v", err)
	}
	want := []string{"diff", "--name-status", "-z", "origin/main", "--"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if args[len(args)-1] != "--" {
		t.Fatalf("the ref must be followed by --, got %v", args)
	}
	if _, err := diffNameStatusArgs("--output=/tmp/x"); !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("hostile base err = %v, want ErrUnsafeRef", err)
	}
}

// TestDiffNameStatus_RejectsHostileBase pins the loader's behavior on a
// rejected ref: an empty change set, and — the part that matters — no
// git process spawned at all.
func TestDiffNameStatus_RejectsHostileBase(t *testing.T) {
	fake := &Fake{}
	got := OpenWith("/repo", fake).diffNameStatus("--output=/tmp/x", "/repo")
	if len(got) != 0 {
		t.Fatalf("hostile base must yield no changes, got %v", got)
	}
	if fake.CallCount() != 0 {
		t.Fatalf("hostile base must not reach git, ran %d command(s): %v",
			fake.CallCount(), fake.Calls)
	}
}

// TestParseNameStatus pins the -z diff framing, which differs from
// porcelain's in two ways that are easy to get backwards: status and
// path are separate fields, and a rename lists old *then* new.
func TestParseNameStatus(t *testing.T) {
	out := []byte("M\x00mod.go\x00R100\x00old.go\x00new2.go\x00A\x00add.go\x00D\x00we\nird.go\x00")
	got := parseNameStatus(out, "/repo")
	want := map[string]ChangeKind{
		"/repo/mod.go":     ChangeModified,
		"/repo/new2.go":    ChangeRenamed,
		"/repo/add.go":     ChangeAdded,
		"/repo/we\nird.go": ChangeDeleted,
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

// TestStatus_CompareAgainstBase pins the compare-against mode against
// real git end to end: the change set is what differs from the base
// ref, not what differs from HEAD.
func TestStatus_CompareAgainstBase(t *testing.T) {
	dir := initRepo(t)
	writeSeed(t, filepath.Join(dir, "later.go"), "l\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "second")

	snap := Open(dir).Status("HEAD~1")
	root, _ := filepath.EvalSymlinks(dir)
	if got := snap.Files[filepath.Join(root, "later.go")]; got != ChangeAdded {
		t.Fatalf("vs HEAD~1: later.go = %v, want added (files: %v)", got, snap.Files)
	}
	if plain := Open(dir).Status(""); len(plain.Files) != 0 {
		t.Fatalf("vs HEAD the tree is clean, got %v", plain.Files)
	}
}

// TestStatus_GitMissingIsDistinguishable pins the sentinel: "no git on
// this machine" and "this directory isn't a repo" both render as no
// badges, but only the first is worth telling a user about, so the
// Snapshot has to carry the difference.
func TestStatus_GitMissingIsDistinguishable(t *testing.T) {
	fake := &Fake{}
	fake.Script("rev-parse --show-toplevel", "", fmt.Errorf("%w: exec: \"git\"", ErrGitMissing))
	if snap := OpenWith("/repo", fake).Status(""); !snap.GitMissing || snap.IsRepo {
		t.Fatalf("missing git must set GitMissing, got %+v", snap)
	}
	if snap := Open(t.TempDir()).Status(""); snap.GitMissing {
		t.Fatal("a plain directory with git installed is not GitMissing")
	}
}

// TestStatus_ViaFake pins the seam: a snapshot can be assembled from
// scripted responses with no git binary involved — the adapter tests
// need exactly this for unreachable states.
func TestStatus_ViaFake(t *testing.T) {
	fake := &Fake{}
	fake.Script("rev-parse --show-toplevel", "/repo\n", nil)
	fake.Script("rev-list --left-right --count @{upstream}...HEAD", "2\t3\n", nil)
	fake.Script("status --porcelain -z", " M a.go\x00", nil)
	fake.Script("symbolic-ref --short HEAD", "feature\n", nil)
	snap := OpenWith("/repo", fake).Status("")
	if !snap.IsRepo || snap.Branch != "feature" || snap.Behind != 2 || snap.Ahead != 3 {
		t.Fatalf("snapshot from fake wrong: %+v", snap)
	}
	if snap.Files["/repo/a.go"] != ChangeModified {
		t.Fatalf("files wrong: %v", snap.Files)
	}
}
