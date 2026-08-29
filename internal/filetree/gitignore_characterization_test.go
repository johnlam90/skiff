// =============================================================================
// File: internal/filetree/gitignore_characterization_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// This file has no corresponding source file on purpose: it characterizes
// github.com/sabhiram/go-gitignore against real git rather than testing
// skiff code. The dependency is pinned at a 2021 pseudo-version with no
// upstream releases, it decides two user-visible things (which rows
// HideIgnored hides in the sidebar and which files the finder's non-git
// walk fallback surfaces), and nobody had verified its semantics agree
// with git's on the corners that are easy to get wrong — negation
// ordering, ** placement, directory-only patterns, anchoring, nested
// .gitignore prefix-joining. This is that verification, run as a
// permanent fence against a real `git check-ignore` subprocess. See
// plans/016-gitignore-dep-characterization.md for the full brief.
package filetree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitignore "github.com/sabhiram/go-gitignore"
)

// hermeticGitEnv isolates a git subprocess from the operator's real
// ~/.gitconfig and global excludesFile. A corpus this specific to
// gitignore semantics should not silently pick up a global ignore rule
// that happens to collide with a probe path; pointing HOME and
// XDG_CONFIG_HOME at a fresh t.TempDir() means git resolves no global
// config at all.
func hermeticGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// buildIgnoreRepo creates a throwaway git repo in t.TempDir(), writes
// patterns to a .gitignore at giDir (repo-relative directory; "" for the
// repo root), and creates the probe entry (a file or an empty directory)
// at probe (repo-relative, forward-slash). It returns the repo's
// absolute path. Creating the entry on disk — not just naming it — is
// what lets git resolve the file-vs-directory ambiguity a bare path
// string can't settle on its own.
func buildIgnoreRepo(t *testing.T, patterns []string, giDir, probe string, isDir bool) string {
	t.Helper()
	repo := t.TempDir()
	initCmd := exec.Command("git", "-C", repo, "init", "-q")
	initCmd.Env = hermeticGitEnv(t)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	giAbsDir := filepath.Join(repo, filepath.FromSlash(giDir))
	if err := os.MkdirAll(giAbsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", giAbsDir, err)
	}
	body := strings.Join(patterns, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(giAbsDir, ".gitignore"), []byte(body), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	target := filepath.Join(repo, filepath.FromSlash(probe))
	if isDir {
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", target, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", target, err)
		}
		if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}
	return repo
}

// gitCheckIgnore runs `git check-ignore -q` for probe (repo-relative,
// forward-slash; a trailing slash is appended by the caller for
// directory probes) inside repoDir and reports git's verdict. Exit code
// 0 means ignored, 1 means not ignored; anything else is a fatal harness
// error — plan 016's STOP conditions call this out explicitly as a sign
// the harness's assumption about check-ignore no longer holds.
func gitCheckIgnore(t *testing.T, repoDir, probe string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repoDir, "check-ignore", "-q", "--", probe)
	cmd.Env = hermeticGitEnv(t)
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 1 {
			return false
		}
		t.Fatalf("git check-ignore -- %q exited %d (harness assumes only 0/1): %v", probe, exitErr.ExitCode(), err)
	}
	t.Fatalf("git check-ignore -- %q: %v", probe, err)
	return false
}

// gitGroundTruthAt reports whether real git ignores probe inside a
// throwaway repo whose .gitignore lives at giDir (repo-relative; "" is
// the repo root — the plain, single-level case most corpus rows use).
// It builds the repo, then asks git about probe with a trailing slash
// appended for directory cases, per the convention ignoredByChain uses.
// Generalizing over giDir (rather than only supporting a root
// .gitignore) is what lets the same helper drive the nested-level-
// relativity rows, where the .gitignore is written INTO a subdirectory.
func gitGroundTruthAt(t *testing.T, patterns []string, giDir, probe string, isDir bool) bool {
	t.Helper()
	repo := buildIgnoreRepo(t, patterns, giDir, probe, isDir)
	arg := probe
	if isDir {
		arg += "/"
	}
	return gitCheckIgnore(t, repo, arg)
}

// libraryVerdictAt compiles patterns exactly as (*Tree).cacheIgnore does
// and asks whether they ignore probe, applying the same prefix-joining
// (*Tree).ignoredByChain performs when the .gitignore that governs probe
// lives at giDir rather than in probe's own directory: strip giDir off
// the front of probe to get the path relative to the .gitignore's own
// directory, then (for directories) append the trailing slash
// ignoredByChain always adds. giDir == "" is the common single-level
// case and reduces to matching probe as-is.
func libraryVerdictAt(patterns []string, giDir, probe string, isDir bool) bool {
	gi := gitignore.CompileIgnoreLines(patterns...)
	rel := strings.TrimPrefix(probe, giDir)
	rel = strings.TrimPrefix(rel, "/")
	if isDir {
		rel += "/"
	}
	return gi.MatchesPath(rel)
}

// gitignoreCase is one row of the characterization corpus: a pattern
// list, where the .gitignore holding it lives (giDir), the repo-relative
// path being probed, and whether that path is a directory.
type gitignoreCase struct {
	name  string
	lines []string
	giDir string
	probe string
	isDir bool
}

// gitignoreCorpus is the ~30-plus-row table plan 016 asks for, grouped by
// pattern class. Each row's expectation is NOT hand-computed here — the
// test below asks both real git and the library the same question and
// compares their answers, which is the whole point of a characterization
// test: git is the ground truth, not a value baked into this table.
func gitignoreCorpus() []gitignoreCase {
	return []gitignoreCase{
		// -- Basic --
		{"star extension matches a file", []string{"*.log"}, "", "build.log", false},
		{"exact name matches a file", []string{"notes.txt"}, "", "notes.txt", false},
		{"unqualified name matches a file", []string{"foo"}, "", "foo", false},
		{"unqualified name matches a directory", []string{"foo"}, "", "foo", true},

		// -- Negation & ordering --
		{"negation excludes a file from a later ignore", []string{"*.log", "!keep.log"}, "", "keep.log", false},
		{"negation before the ignore rule does not survive", []string{"!keep.log", "*.log"}, "", "keep.log", false},
		{"re-ignoring after a negation wins", []string{"*.log", "!keep.log", "keep.log"}, "", "keep.log", false},

		// -- Directory-only (trailing slash) --
		{"trailing-slash pattern matches a directory", []string{"dist/"}, "", "dist", true},
		{"trailing-slash pattern spares a same-named file", []string{"dist/"}, "", "dist", false},
		{"no-slash pattern matches a directory too", []string{"dist"}, "", "dist", true},
		{"no-slash pattern matches a file too", []string{"dist"}, "", "dist", false},

		// -- Doublestar --
		{"leading **/ matches a nested file", []string{"**/foo"}, "", "a/b/foo", false},
		{"leading **/ also matches at the root", []string{"**/foo"}, "", "foo", false},
		{"trailing /** matches everything inside", []string{"foo/**"}, "", "foo/bar/baz", false},
		{"a/**/b matches zero segments between", []string{"a/**/b"}, "", "a/b", false},
		{"a/**/b matches multiple segments between", []string{"a/**/b"}, "", "a/x/y/b", false},
		{"leading /**/x matches at the root", []string{"/**/x"}, "", "x", false},

		// -- Anchoring --
		{"leading-slash pattern anchors to the root - matches there", []string{"/top.log"}, "", "top.log", false},
		{"leading-slash pattern anchors to the root - spares a nested file", []string{"/top.log"}, "", "sub/top.log", false},
		{"unanchored name matches at the root", []string{"top.log"}, "", "top.log", false},
		{"unanchored name matches nested too", []string{"top.log"}, "", "sub/top.log", false},
		{"embedded-slash pattern matches at its anchored location", []string{"sub/file"}, "", "sub/file", false},
		{"embedded-slash pattern is root-relative, not matched deeper", []string{"sub/file"}, "", "x/sub/file", false},

		// -- Escapes & specials --
		{"escaped hash is a literal pattern, not a comment", []string{`\#literal`}, "", "#literal", false},
		{"escaped bang is a literal pattern, not a negation", []string{`\!bang`}, "", "!bang", false},
		{"unescaped trailing space in the pattern is stripped", []string{"trail.txt "}, "", "trail.txt", false},
		{"escaped trailing space in the pattern is kept", []string{`trail.txt\ `}, "", "trail.txt ", false},

		// -- Globs --
		{"? matches exactly one char - hit", []string{"?at"}, "", "cat", false},
		{"? matches exactly one char - miss on extra char", []string{"?at"}, "", "scat", false},
		{"bracket set matches a member", []string{"[abc].txt"}, "", "a.txt", false},
		{"bracket range matches a member", []string{"[a-c].txt"}, "", "b.txt", false},
		{"negated bracket matches a non-member", []string{"[!a]x"}, "", "bx", false},
		{"negated bracket spares the excluded member", []string{"[!a]x"}, "", "ax", false},
		{"*.tx? matches with the required extra char", []string{"*.tx?"}, "", "file.txt", false},
		{"*.tx? does not match without the extra char", []string{"*.tx?"}, "", "file.tx", false},

		// -- Case sensitivity --
		{"pattern case does not match a different-case file", []string{"Foo.txt"}, "", "foo.txt", false},
		{"pattern case matches the exact case", []string{"Foo.txt"}, "", "Foo.txt", false},

		// -- Nested-level relativity: .gitignore written INTO sub/, probed
		// through the same prefix convention ignoreChain builds --
		{"nested gitignore, unanchored, direct child", []string{"*.log"}, "sub", "sub/build.log", false},
		{"nested gitignore, unanchored, grandchild still matches", []string{"*.log"}, "sub", "sub/deeper/build.log", false},
		{"nested gitignore, anchored, matches its own directory", []string{"/anchored.log"}, "sub", "sub/anchored.log", false},
		{"nested gitignore, anchored, spares a grandchild", []string{"/anchored.log"}, "sub", "sub/deeper/anchored.log", false},
	}
}

// TestGitignoreCharacterization is plan 016's characterization corpus: for
// every row it asks a throwaway git repo and the compiled go-gitignore
// matcher the same question — does this pattern list ignore this path? —
// and reports every row where they disagree. It is a permanent fence, not
// a one-off investigation: green means the library's semantics still
// agree with git's on this corpus, and a future change to either side
// that regresses one of these rows should show up here first.
//
// git is a hard external requirement (the whole point is comparing
// against it), so the test skips — rather than fails — when no git
// binary is on PATH.
func TestGitignoreCharacterization(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; cannot compare against ground truth")
	}

	for _, tc := range gitignoreCorpus() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gitResult := gitGroundTruthAt(t, tc.lines, tc.giDir, tc.probe, tc.isDir)
			libResult := libraryVerdictAt(tc.lines, tc.giDir, tc.probe, tc.isDir)
			if libResult != gitResult {
				t.Errorf("divergence: patterns=%v giDir=%q probe=%q isDir=%v — library=%v git=%v",
					tc.lines, tc.giDir, tc.probe, tc.isDir, libResult, gitResult)
			}
		})
	}
}
