// =============================================================================
// File: internal/git/diff_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/diff"
)

// TestDiff_Argv pins the argv Diff builds: HEAD for an empty base, the
// context width, the pinned header prefixes, the ref, then `--` and the
// paths — so a scripted Fake in an app test and real git see the same
// command.
func TestDiff_Argv(t *testing.T) {
	f := &Fake{}
	f.Script("diff --unified=0 --src-prefix=a/ --dst-prefix=b/ HEAD -- /r/a.go /r/b.go", "", nil)
	if _, err := OpenWith("/r", f).Diff("", 0, "/r/a.go", "/r/b.go"); err != nil {
		t.Fatalf("diff: %v", err)
	}
	f.Script("diff --unified=3 --src-prefix=a/ --dst-prefix=b/ HEAD~1 --", "", nil)
	if _, err := OpenWith("/r", f).Diff("HEAD~1", 3); err != nil {
		t.Fatalf("whole-tree diff: %v", err)
	}
	if got := joinedCalls(f); len(got) != 2 || !f.Called("diff --unified=3 --src-prefix=a/ --dst-prefix=b/ HEAD~1 --") {
		t.Fatalf("argv = %v", got)
	}
}

// TestDiff_RejectsUnsafeBase pins the loader's behavior on a hostile
// compare base: ErrUnsafeRef, and — the part that matters — no git
// process spawned at all.
func TestDiff_RejectsUnsafeBase(t *testing.T) {
	f := &Fake{}
	_, err := OpenWith("/r", f).Diff("--output=/tmp/x", 0, "/r/a.go")
	if !errors.Is(err, ErrUnsafeRef) {
		t.Fatalf("err = %v, want ErrUnsafeRef", err)
	}
	if f.CallCount() != 0 {
		t.Fatalf("hostile base must not reach git, ran %v", joinedCalls(f))
	}
}

// TestDiff_RealGit pins the model against real git: a modified file
// comes back as one File with a hunk whose new-side range names the
// changed line, and a clean tree is an empty Patch with no error.
func TestDiff_RealGit(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	clean, err := r.Diff("", 0)
	if err != nil || !clean.Empty() {
		t.Fatalf("clean tree: %+v %v", clean, err)
	}
	f := filepath.Join(dir, "f.txt")
	writeSeed(t, f, "hi\nthere\n")
	p, err := r.Diff("", 0, f)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(p.Files) != 1 || p.Files[0].Path() != "f.txt" {
		t.Fatalf("files = %+v", p.Files)
	}
	h := p.Files[0].Hunks
	if len(h) != 1 || h[0].NewStart != 2 || h[0].NewLen != 1 || h[0].OldLen != 0 {
		t.Fatalf("hunks = %+v", h)
	}
	if _, err := Open(t.TempDir()).Diff("", 0); err == nil {
		t.Fatal("a non-repo must error rather than look clean")
	}
}

// TestDiff_QuotedPathsDecode pins why Diff carries no -z: git still
// C-quotes a patch header's path when it holds a space or non-ASCII
// bytes, and diff.Parse is what turns that back into the path on disk.
func TestDiff_QuotedPathsDecode(t *testing.T) {
	dir := initRepo(t)
	sub := filepath.Join(dir, "sp ace")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := filepath.Join(sub, "ü.txt")
	writeSeed(t, f, "a\nb\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "odd path")
	writeSeed(t, f, "a\nB\n")
	p, err := Open(dir).Diff("", 0, f)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(p.Files) != 1 || p.Files[0].Path() != "sp ace/ü.txt" {
		t.Fatalf("quoted path must decode to the on-disk name, got %+v", p.Files)
	}
}

// TestDiffUntracked_RealGit pins the --no-index rendering of a file git
// has never seen: an all-added File whose path is repo-relative, the
// exit-1 "files differ" status treated as success, and a missing path
// as the real error.
func TestDiffUntracked_RealGit(t *testing.T) {
	dir := initRepo(t)
	fresh := filepath.Join(dir, "fresh.txt")
	writeSeed(t, fresh, "one\ntwo\n")
	file, err := Open(dir).DiffUntracked(fresh, 3)
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	if !file.IsNew || file.Path() != "fresh.txt" {
		t.Fatalf("file = %+v, want a new file at the repo-relative path", file)
	}
	var added int
	for _, h := range file.Hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.Add {
				added++
			}
		}
	}
	if added != 2 {
		t.Fatalf("want 2 added lines, got %d in %+v", added, file.Hunks)
	}
	if _, err := Open(dir).DiffUntracked(filepath.Join(dir, "missing.txt"), 3); err == nil {
		t.Fatal("a missing path must error")
	}
	empty := filepath.Join(dir, "empty.txt")
	writeSeed(t, empty, "")
	if file, err := Open(dir).DiffUntracked(empty, 3); err != nil || len(file.Hunks) != 0 {
		t.Fatalf("an empty file has nothing to add: %+v %v", file, err)
	}
}

// TestDiffUntracked_Argv pins the argv, /dev/null first behind `--`,
// so a scripted app test and real git agree on the command.
func TestDiffUntracked_Argv(t *testing.T) {
	f := &Fake{}
	want := "diff --no-index --unified=3 --src-prefix=a/ --dst-prefix=b/ -- " + os.DevNull + " new.txt"
	f.Script(want, strings.Join([]string{
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1 @@",
		"+x",
		"",
	}, "\n"), errors.New("exit status 1"))
	file, err := OpenWith("/r", f).DiffUntracked("/r/new.txt", 3)
	if err != nil {
		t.Fatalf("exit 1 with output is the success case, got %v", err)
	}
	if !file.IsNew || len(file.Hunks) != 1 {
		t.Fatalf("file = %+v", file)
	}
	if !f.Called(want) {
		t.Fatalf("argv = %v", joinedCalls(f))
	}
}
