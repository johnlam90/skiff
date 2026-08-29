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
//
// The corpus found 6 known divergences from git — see gitignoreCorpus's
// knownDivergence entries below and plan 016's findings report for the
// full detail and the impact assessment. This test PINS CURRENT
// BEHAVIOR, it does not bless it: the divergent rows assert today's
// (buggy) library answer so the suite stays green, and fail loudly the
// moment either side's behavior changes. Whether to swap the dependency
// over it is a follow-up decision for a future plan, not settled here.
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

// fsIsCaseInsensitive reports whether t's fixture temp directory treats
// path casing as insignificant — true on the default macOS/APFS
// configuration, false on ext4 and most Linux setups. It writes a probe
// file and stats it back under an upper-cased name: a hit means the
// filesystem folded the case.
//
// This matters because git's ground truth for a case-differing pattern
// match is NOT a fixed, cross-platform fact: `git init` auto-detects
// the repository's filesystem case sensitivity and sets
// core.ignorecase accordingly, and check-ignore's pathspec matching
// honours it — so a pattern like "Foo.txt" DOES ignore "foo.txt" on a
// case-insensitive filesystem, even though the bytes differ, while it
// does not on a case-sensitive one. A corpus row built on this pattern
// shape can't pin a single expected git verdict; it has to ask this
// fixture what filesystem it is actually running on.
func fsIsCaseInsensitive(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	probe := filepath.Join(dir, "case-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("write case probe: %v", err)
	}
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err == nil
}

// knownDivergence documents one of the 6 corners where go-gitignore's
// verdict disagrees with git's, discovered by this corpus and written up
// in plan 016's findings report. It freezes BOTH sides' current answers
// — not just the library's — so the row still fails loudly if either
// one's behavior ever changes: a library fix that starts agreeing with
// git, or (far less likely) a change in git's own reading of the
// pattern. Either is worth a human re-triaging the row, possibly
// deleting the divergence so it rejoins the plain agreement check.
type knownDivergence struct {
	libraryVerdict bool   // go-gitignore's current (buggy) answer
	gitVerdict     bool   // git's answer — the one skiff actually wants
	cause          string // why the library disagrees, in one sentence
}

// gitignoreCase is one row of the characterization corpus: a pattern
// list, where the .gitignore holding it lives (giDir), the repo-relative
// path being probed, whether that path is a directory, and — for the 6
// rows where go-gitignore is simply wrong — the knownDivergence that
// keeps the suite green without hiding the bug. nil means "expected to
// agree with git," which is true for the other 35 rows.
type gitignoreCase struct {
	name     string
	lines    []string
	giDir    string
	probe    string
	isDir    bool
	diverges *knownDivergence
}

// gitignoreCorpus is the ~30-plus-row table plan 016 asks for, grouped by
// pattern class. Most rows carry no expectation of their own: the test
// below asks real git and the library the same question and requires
// they agree, which is the point of a characterization test — git is
// the ground truth, not a value baked into this table. The 6 rows with
// a non-nil diverges field are the exception: go-gitignore is provably
// wrong on them, so diverges freezes today's answer from both sides
// instead (see TestGitignoreCharacterization for how that's enforced).
func gitignoreCorpus() []gitignoreCase {
	return []gitignoreCase{
		// -- Basic --
		{"star extension matches a file", []string{"*.log"}, "", "build.log", false, nil},
		{"exact name matches a file", []string{"notes.txt"}, "", "notes.txt", false, nil},
		{"unqualified name matches a file", []string{"foo"}, "", "foo", false, nil},
		{"unqualified name matches a directory", []string{"foo"}, "", "foo", true, nil},

		// -- Negation & ordering --
		{"negation excludes a file from a later ignore", []string{"*.log", "!keep.log"}, "", "keep.log", false, nil},
		{"negation before the ignore rule does not survive", []string{"!keep.log", "*.log"}, "", "keep.log", false, nil},
		{"re-ignoring after a negation wins", []string{"*.log", "!keep.log", "keep.log"}, "", "keep.log", false, nil},

		// -- Directory-only (trailing slash) --
		{"trailing-slash pattern matches a directory", []string{"dist/"}, "", "dist", true, nil},
		{"trailing-slash pattern spares a same-named file", []string{"dist/"}, "", "dist", false, nil},
		{"no-slash pattern matches a directory too", []string{"dist"}, "", "dist", true, nil},
		{"no-slash pattern matches a file too", []string{"dist"}, "", "dist", false, nil},

		// -- Doublestar --
		{"leading **/ matches a nested file", []string{"**/foo"}, "", "a/b/foo", false, nil},
		{"leading **/ also matches at the root", []string{"**/foo"}, "", "foo", false, nil},
		{"trailing /** matches everything inside", []string{"foo/**"}, "", "foo/bar/baz", false, nil},
		{"a/**/b matches zero segments between", []string{"a/**/b"}, "", "a/b", false, nil},
		{"a/**/b matches multiple segments between", []string{"a/**/b"}, "", "a/x/y/b", false, nil},
		{"leading /**/x matches at the root", []string{"/**/x"}, "", "x", false, nil},

		// -- Anchoring --
		{"leading-slash pattern anchors to the root - matches there", []string{"/top.log"}, "", "top.log", false, nil},
		{"leading-slash pattern anchors to the root - spares a nested file", []string{"/top.log"}, "", "sub/top.log", false, nil},
		{"unanchored name matches at the root", []string{"top.log"}, "", "top.log", false, nil},
		{"unanchored name matches nested too", []string{"top.log"}, "", "sub/top.log", false, nil},
		{"embedded-slash pattern matches at its anchored location", []string{"sub/file"}, "", "sub/file", false, nil},
		{"embedded-slash pattern is root-relative, not matched deeper — KNOWN DIVERGENCE", []string{"sub/file"}, "", "x/sub/file", false, &knownDivergence{
			libraryVerdict: true,
			gitVerdict:     false,
			cause:          "go-gitignore only anchors a pattern to the .gitignore's directory when it literally begins with '/'; git anchors on any embedded slash, not just a leading one, so 'sub/file' should match only at that exact location — not at any depth",
		}},

		// -- Escapes & specials --
		{"escaped hash is a literal pattern, not a comment", []string{`\#literal`}, "", "#literal", false, nil},
		{"escaped bang is a literal pattern, not a negation", []string{`\!bang`}, "", "!bang", false, nil},
		{"unescaped trailing space in the pattern is stripped", []string{"trail.txt "}, "", "trail.txt", false, nil},
		{"escaped trailing space in the pattern is kept — KNOWN DIVERGENCE", []string{`trail.txt\ `}, "", "trail.txt ", false, &knownDivergence{
			libraryVerdict: false,
			gitVerdict:     true,
			cause:          "go-gitignore's own source marks the escaped-trailing-space rule (gitignore rule 3) as an unimplemented TODO; it strips the trailing space unconditionally, ignoring the backslash escape",
		}},

		// -- Globs --
		{"? matches exactly one char - hit — KNOWN DIVERGENCE", []string{"?at"}, "", "cat", false, &knownDivergence{
			libraryVerdict: false,
			gitVerdict:     true,
			cause:          "go-gitignore escapes '?' to a literal character instead of implementing git's single-char glob wildcard",
		}},
		{"? matches exactly one char - miss on extra char", []string{"?at"}, "", "scat", false, nil},
		{"bracket set matches a member", []string{"[abc].txt"}, "", "a.txt", false, nil},
		{"bracket range matches a member", []string{"[a-c].txt"}, "", "b.txt", false, nil},
		{"negated bracket matches a non-member — KNOWN DIVERGENCE", []string{"[!a]x"}, "", "bx", false, &knownDivergence{
			libraryVerdict: false,
			gitVerdict:     true,
			cause:          "'[!a]' is passed straight into Go's regexp engine, where '!' is not a negation operator — it becomes a positive class containing the literal characters '!' and 'a', which excludes 'b'",
		}},
		{"negated bracket spares the excluded member — KNOWN DIVERGENCE", []string{"[!a]x"}, "", "ax", false, &knownDivergence{
			libraryVerdict: true,
			gitVerdict:     false,
			cause:          "same broken '[!...]' translation as the non-member case; the positive class containing the literal 'a' matches 'a' where git's negation would exclude it",
		}},
		{"*.tx? matches with the required extra char — KNOWN DIVERGENCE", []string{"*.tx?"}, "", "file.txt", false, &knownDivergence{
			libraryVerdict: false,
			gitVerdict:     true,
			cause:          "same '?'-as-literal bug as the bare '?at' case",
		}},
		{"*.tx? does not match without the extra char", []string{"*.tx?"}, "", "file.tx", false, nil},

		// -- Case sensitivity --
		{"pattern case does not match a different-case file", []string{"Foo.txt"}, "", "foo.txt", false, nil},
		{"pattern case matches the exact case", []string{"Foo.txt"}, "", "Foo.txt", false, nil},

		// -- Nested-level relativity: .gitignore written INTO sub/, probed
		// through the same prefix convention ignoreChain builds --
		{"nested gitignore, unanchored, direct child", []string{"*.log"}, "sub", "sub/build.log", false, nil},
		{"nested gitignore, unanchored, grandchild still matches", []string{"*.log"}, "sub", "sub/deeper/build.log", false, nil},
		{"nested gitignore, anchored, matches its own directory", []string{"/anchored.log"}, "sub", "sub/anchored.log", false, nil},
		{"nested gitignore, anchored, spares a grandchild", []string{"/anchored.log"}, "sub", "sub/deeper/anchored.log", false, nil},
	}
}

// caseSensitivityRowName identifies the one corpus row ("pattern case
// does not match a different-case file", under -- Case sensitivity --
// above) whose correct git verdict is platform-dependent rather than a
// fixed fact this table can pin — see fsIsCaseInsensitive and
// assertCaseSensitivityRow. CI caught this the hard way: the row passed
// on ubuntu-latest and failed on macos-latest, because APFS folds case
// and ext4 doesn't.
const caseSensitivityRowName = "pattern case does not match a different-case file"

// assertCaseSensitivityRow is TestGitignoreCharacterization's special
// case for caseSensitivityRowName. go-gitignore's own match is asserted
// UNCONDITIONALLY: its regex is always case-sensitive, so it must say
// "not ignored" regardless of platform. git's answer is not asserted
// against a fixed expectation — it is compared against a live probe of
// this fixture's filesystem, because that probe is exactly what git's
// own core.ignorecase auto-detection is answering under the hood. On a
// case-insensitive filesystem this makes the row a self-documenting,
// platform-conditional known divergence (logged, not failed); on a
// case-sensitive one the two sides simply agree, as they did before CI
// found the gap.
func assertCaseSensitivityRow(t *testing.T, libResult, gitResult bool) {
	t.Helper()
	if libResult {
		t.Errorf("go-gitignore's case handling changed: expected case-sensitive (no match) unconditionally, got a match — re-triage this row")
	}
	wantGit := fsIsCaseInsensitive(t)
	if gitResult != wantGit {
		t.Errorf("git's verdict didn't track this fixture's own case sensitivity: fsIsCaseInsensitive=%v git=%v", wantGit, gitResult)
	}
	if wantGit {
		t.Logf("known divergence (platform-conditional): fixture filesystem is case-insensitive, so git ignores %q under pattern %q via core.ignorecase, while go-gitignore's regex match stays case-sensitive and says no",
			"foo.txt", "Foo.txt")
	}
}

// TestGitignoreCharacterization is plan 016's characterization corpus: for
// every row it asks a throwaway git repo and the compiled go-gitignore
// matcher the same question — does this pattern list ignore this path?
// It is a permanent fence, not a one-off investigation.
//
// 35 rows have no diverges field and must agree with git outright; a
// disagreement there is a NEW divergence plan 016 didn't find, and fails
// loudly. The other 6 rows carry a knownDivergence — go-gitignore is
// provably wrong on them today (see plan 016's findings report for the
// full detail) — and instead of failing on a bug this project can't fix
// upstream, they assert BOTH sides still match what knownDivergence
// recorded. That keeps the suite green today while still catching any
// future change: a library update that starts agreeing with git, or git
// itself changing how it reads the pattern, either fails this row and
// sends someone back to re-triage it. This test pins current behavior;
// it does not bless it.
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

			if tc.name == caseSensitivityRowName {
				assertCaseSensitivityRow(t, libResult, gitResult)
				return
			}

			if tc.diverges == nil {
				if libResult != gitResult {
					t.Errorf("new divergence from git (not previously known): patterns=%v giDir=%q probe=%q isDir=%v — library=%v git=%v; see plan 016's findings before documenting it as known",
						tc.lines, tc.giDir, tc.probe, tc.isDir, libResult, gitResult)
				}
				return
			}

			d := tc.diverges
			t.Logf("known divergence: library=%v git=%v — %s", d.libraryVerdict, d.gitVerdict, d.cause)
			if gitResult != d.gitVerdict {
				t.Errorf("git's own answer changed: patterns=%v probe=%q — was %v, now %v; re-triage this row (plan 016)",
					tc.lines, tc.probe, d.gitVerdict, gitResult)
			}
			if libResult != d.libraryVerdict {
				t.Errorf("go-gitignore's behavior changed: patterns=%v probe=%q — was %v, now %v (documented cause: %q); re-triage this row, it may no longer diverge from git",
					tc.lines, tc.probe, d.libraryVerdict, libResult, d.cause)
			}
		})
	}
}
