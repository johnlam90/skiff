// =============================================================================
// File: internal/filetree/ignore_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGitignore_HidesIgnoredEntriesUntilToggledOff is the headline
// contract: with filtering on the sidebar answers "is this project
// noise?" the way the finder's index already does, and with it off the
// same directory shows everything again. The toggle is only meaningful
// if both directions work, so both are asserted from one fixture.
func TestGitignore_HidesIgnoredEntriesUntilToggledOff(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.HideIgnored {
		t.Fatal("filtering must default on so the tree and the finder agree out of the box")
	}
	for _, name := range []string{"dist", "app.log"} {
		if findChild(tr.Root, name) != nil {
			t.Fatalf("%q is gitignored and must not be a row", name)
		}
	}
	if findChild(tr.Root, "main.go") == nil {
		t.Fatal("main.go is not ignored and must stay visible")
	}

	tr.HideIgnored = false
	tr.Refresh()
	for _, name := range []string{"dist", "app.log", "main.go"} {
		if findChild(tr.Root, name) == nil {
			t.Fatalf("%q must come back once filtering is off", name)
		}
	}
}

// TestGitignore_DotfilesStayVisibleInBothStates pins the deliberate
// split between the two axes. .env is the most commonly gitignored file
// there is and the least acceptable one to lose over SSH, so gitignore
// filtering never removes a dotfile — including .gitignore itself,
// which would otherwise hide the very rule set the user is debugging.
func TestGitignore_DotfilesStayVisibleInBothStates(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, on := range []bool{true, false} {
		tr.HideIgnored = on
		tr.Refresh()
		for _, name := range []string{".env", ".gitignore"} {
			if findChild(tr.Root, name) == nil {
				t.Fatalf("HideIgnored=%v hid the dotfile %q", on, name)
			}
		}
	}
}

// TestGitignore_NestedFileAppliesToItsSubtreeOnly is the nested-support
// claim stated as behaviour: a rule written in src/.gitignore hides the
// matching name below src/ and leaves the identically-named file at the
// root alone. Getting only one of those two right is the failure mode
// worth catching.
func TestGitignore_NestedFileAppliesToItsSubtreeOnly(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "*.tmp\n")
	mustWrite(t, filepath.Join(root, "generated.go"), "root copy")
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "src", ".gitignore"), "generated.go\n")
	mustWrite(t, filepath.Join(root, "src", "generated.go"), "built")
	mustWrite(t, filepath.Join(root, "src", "real.go"), "package src")
	mustWrite(t, filepath.Join(root, "src", "scratch.tmp"), "x")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "generated.go") == nil {
		t.Fatal("the root's generated.go is outside src/.gitignore's reach and must stay")
	}
	src := findChild(tr.Root, "src")
	if src == nil {
		t.Fatal("src/ missing")
	}
	tr.Toggle(src)

	if findChild(src, "generated.go") != nil {
		t.Fatal("src/.gitignore must hide src/generated.go")
	}
	if findChild(src, "scratch.tmp") != nil {
		t.Fatal("the root .gitignore's *.tmp must still reach into src/")
	}
	if findChild(src, "real.go") == nil {
		t.Fatal("src/real.go matches nothing and must stay visible")
	}
}

// TestGitignore_MatcherCacheFollowsTheChildren pins the cache's one
// invalidation rule: a directory's compiled matcher is replaced exactly
// when its listing is. Editing .gitignore and refreshing must move the
// filter, and an unchanged file must reuse the compiled matcher rather
// than rebuilding a regexp per pattern line on every tick.
func TestGitignore_MatcherCacheFollowsTheChildren(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "a.txt\n")
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "a.txt") != nil || findChild(tr.Root, "b.txt") == nil {
		t.Fatal("initial state should hide a.txt only")
	}

	compiled := tr.ignoreCache[tr.Root.Path].gi
	if compiled == nil {
		t.Fatal("the root .gitignore should be cached after the first read")
	}
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi != compiled {
		t.Fatal("unchanged .gitignore bytes must reuse the compiled matcher")
	}

	mustWrite(t, filepath.Join(root, ".gitignore"), "b.txt\n")
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi == compiled {
		t.Fatal("edited .gitignore must recompile")
	}
	if findChild(tr.Root, "a.txt") == nil {
		t.Fatal("a.txt is no longer ignored and must reappear")
	}
	if findChild(tr.Root, "b.txt") != nil {
		t.Fatal("b.txt is newly ignored and must disappear")
	}

	// Deleting the file drops the entry entirely rather than leaving a
	// stale matcher behind.
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tr.Refresh()
	if _, ok := tr.ignoreCache[tr.Root.Path]; ok {
		t.Fatal("a removed .gitignore must leave no cache entry")
	}
	if findChild(tr.Root, "b.txt") == nil {
		t.Fatal("b.txt must return once the rules are gone")
	}
}

// TestGitignore_OpenTabNeverVanishes is the safety rule: the user can
// legitimately be editing a file inside an ignored directory (a
// generated file, a vendored copy), and the sidebar must not pretend it
// does not exist. The ignored directory reappears carrying exactly the
// pinned file — the rest of the build output stays filtered.
func TestGitignore_OpenTabNeverVanishes(t *testing.T) {
	root := mkIgnoreTree(t)
	mustWrite(t, filepath.Join(root, "dist", "other.js"), "// also built")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("test setup: dist/ should start hidden")
	}

	open := filepath.Join(root, "dist", "bundle.js")
	tr.SetOpenFiles([]string{open})
	tr.Refresh()

	dist := findChild(tr.Root, "dist")
	if dist == nil {
		t.Fatal("the directory holding an open tab must be reachable")
	}
	tr.Toggle(dist)
	if findChild(dist, "bundle.js") == nil {
		t.Fatal("the open file must be a row")
	}
	if findChild(dist, "other.js") != nil {
		t.Fatal("pinning one file must not un-ignore its whole directory")
	}

	// Reveal takes the same path from cold: it pins the target itself and
	// re-reads the ancestors the filter had emptied.
	fresh, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fresh.Reveal(open, 20)
	revealed := findChild(fresh.Root, "dist")
	if revealed == nil || findChild(revealed, "bundle.js") == nil {
		t.Fatal("Reveal must surface a target inside an ignored directory")
	}

	// Closing the tab un-pins it again.
	tr.SetOpenFiles(nil)
	tr.Refresh()
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("dist/ should return to hidden once nothing inside it is open")
	}
}
