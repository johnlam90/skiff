// =============================================================================
// File: internal/gitignore/gitignore_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package gitignore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ignoreCase is one row of the matcher's corpus: the lines of a
// .gitignore, where that file lives (giDir, repo-relative, "" for the
// root), the repo-relative path being asked about, whether that path is
// a directory, and the verdict git gives.
//
// want is used twice, which is the point of carrying it: the pure unit
// test asserts the matcher produces it with no git anywhere, and the
// differential test asserts real git produces it too — so a wrong
// expectation in this table cannot survive on a machine with git, and
// the matcher is still tested on a machine without.
type ignoreCase struct {
	name  string
	lines []string
	giDir string
	probe string
	isDir bool
	want  bool
}

// ignoreCorpus is the table both tests walk. It began as the 41-row
// characterization of the go-gitignore library this package replaced
// (six of whose rows were pinned as known divergences from git) and
// grew every pattern class the tree-filter audit found the library
// reading wrongly, in both directions: paths git tracks that were
// hidden, and paths git ignores that were shown.
func ignoreCorpus() []ignoreCase {
	return []ignoreCase{
		// -- Basic --
		{"star extension matches a file", []string{"*.log"}, "", "build.log", false, true},
		{"star extension spares another extension", []string{"*.log"}, "", "build.txt", false, false},
		{"exact name matches a file", []string{"notes.txt"}, "", "notes.txt", false, true},
		{"unqualified name matches a file", []string{"foo"}, "", "foo", false, true},
		{"unqualified name matches a directory", []string{"foo"}, "", "foo", true, true},
		{"unqualified name is not a prefix match", []string{"foo"}, "", "foobar", false, false},
		{"lone star matches everything", []string{"*"}, "", "anything", false, true},

		// -- Blank lines, comments, line endings --
		{"comment line is not a pattern", []string{"#note"}, "", "#note", false, false},
		{"blank and space-only lines are skipped", []string{"", "   ", "x.txt"}, "", "x.txt", false, true},
		{"CRLF line ending is not part of the pattern", []string{"foo\r", "bar\r"}, "", "foo", false, true},

		// -- Negation & ordering --
		{"negation excludes a file from an earlier ignore", []string{"*.log", "!keep.log"}, "", "keep.log", false, false},
		{"negation before the ignore rule does not survive", []string{"!keep.log", "*.log"}, "", "keep.log", false, true},
		{"re-ignoring after a negation wins", []string{"*.log", "!keep.log", "keep.log"}, "", "keep.log", false, true},
		{"negation spares only what it names", []string{"*", "!*.go"}, "", "a.txt", false, true},
		{"negation re-includes what it names", []string{"*", "!*.go"}, "", "a.go", false, false},
		{"a bare bang negates nothing", []string{"*.log", "!"}, "", "a.log", false, true},
		{"negation cannot reach inside an excluded directory", []string{"build/", "!build/keep.txt"}, "", "build/keep.txt", false, true},
		{"negation works when only the contents were excluded", []string{"build/*", "!build/keep.txt"}, "", "build/keep.txt", false, false},

		// -- Directory-only (trailing slash) --
		{"trailing-slash pattern matches a directory", []string{"dist/"}, "", "dist", true, true},
		{"trailing-slash pattern spares a same-named file", []string{"dist/"}, "", "dist", false, false},
		{"trailing-slash pattern matches a nested directory", []string{"dist/"}, "", "sub/dist", true, true},
		{"trailing-slash pattern takes the directory's contents", []string{"dist/"}, "", "dist/out.o", false, true},
		{"no-slash pattern matches a directory too", []string{"dist"}, "", "dist", true, true},
		{"no-slash pattern matches a file too", []string{"dist"}, "", "dist", false, true},
		{"only one trailing slash is the directory marker", []string{"dist//"}, "", "dist", true, false},

		// -- Doublestar --
		{"leading **/ matches a nested file", []string{"**/foo"}, "", "a/b/foo", false, true},
		{"leading **/ also matches at the root", []string{"**/foo"}, "", "foo", false, true},
		{"**/tmp matches a nested directory", []string{"**/tmp"}, "", "a/b/tmp", true, true},
		{"trailing /** matches everything inside", []string{"foo/**"}, "", "foo/bar/baz", false, true},
		{"trailing /** does not match the directory itself", []string{"foo/**"}, "", "foo", true, false},
		{"a/**/b matches zero segments between", []string{"a/**/b"}, "", "a/b", false, true},
		{"a/**/b matches multiple segments between", []string{"a/**/b"}, "", "a/x/y/b", false, true},
		{"a/**/b needs the trailing segment", []string{"a/**/b"}, "", "a/x/c", false, false},
		{"leading /**/x matches at the root", []string{"/**/x"}, "", "x", false, true},
		{"doc/**/*.pdf matches directly inside", []string{"doc/**/*.pdf"}, "", "doc/x.pdf", false, true},
		{"doc/**/*.pdf matches deep inside", []string{"doc/**/*.pdf"}, "", "doc/a/b/x.pdf", false, true},
		{"doc/**/*.pdf is anchored at doc", []string{"doc/**/*.pdf"}, "", "other/doc/x.pdf", false, false},
		{"doc/**/*.pdf spares a pdf outside doc", []string{"doc/**/*.pdf"}, "", "x.pdf", false, false},
		{"** inside a word is two plain stars", []string{"a**b"}, "", "axxb", false, true},
		{"** inside a word does not cross a slash", []string{"x/a**b"}, "", "x/a/b", false, false},

		// -- Anchoring --
		{"leading-slash pattern anchors to the root - matches there", []string{"/top.log"}, "", "top.log", false, true},
		{"leading-slash pattern anchors to the root - spares a nested file", []string{"/top.log"}, "", "sub/top.log", false, false},
		{"unanchored name matches at the root", []string{"top.log"}, "", "top.log", false, true},
		{"unanchored name matches nested too", []string{"top.log"}, "", "sub/top.log", false, true},
		{"embedded-slash pattern matches at its anchored location", []string{"sub/file"}, "", "sub/file", false, true},
		{"embedded-slash pattern is root-relative, not matched deeper", []string{"sub/file"}, "", "x/sub/file", false, false},
		{"src/gen matches the directory at the root", []string{"src/gen"}, "", "src/gen", true, true},
		{"src/gen does not match a/src/gen", []string{"src/gen"}, "", "a/src/gen", true, false},
		{"*/b matches one level down", []string{"*/b"}, "", "a/b", false, true},
		{"*/b does not match two levels down", []string{"*/b"}, "", "x/a/b", false, false},
		{"a/* takes a directory child and so its contents", []string{"a/*"}, "", "a/b/c", false, true},
		{"a/*/c does not span two directories", []string{"a/*/c"}, "", "a/x/y/c", false, false},
		{"a bare slash matches nothing - file", []string{"/"}, "", "anything", false, false},
		{"a bare slash matches nothing - directory", []string{"/"}, "", "anydir", true, false},

		// -- Whitespace --
		{"unescaped trailing space in the pattern is stripped", []string{"trail.txt "}, "", "trail.txt", false, true},
		{"escaped trailing space in the pattern is kept", []string{`trail\ `}, "", "trail ", false, true},
		{"escaped trailing space does not match the bare name", []string{`trail\ `}, "", "trail", false, false},
		{"leading spaces are part of the pattern - miss", []string{"  lead"}, "", "lead", false, false},
		{"leading spaces are part of the pattern - hit", []string{"  lead"}, "", "  lead", false, true},

		// -- Escapes & regexp metacharacters, which are all literals --
		{"escaped hash is a literal pattern, not a comment", []string{`\#literal`}, "", "#literal", false, true},
		{"escaped bang is a literal pattern, not a negation", []string{`\!bang`}, "", "!bang", false, true},
		{"escaped star is a literal star - hit", []string{`foo\*`}, "", "foo*", false, true},
		{"escaped star is a literal star - miss", []string{`foo\*`}, "", "foox", false, false},
		{"backslash before a plain letter is that letter", []string{`a\b`}, "", "ab", false, true},
		{"plus is literal - hit", []string{"a+b"}, "", "a+b", false, true},
		{"plus is not a repetition", []string{"a+b"}, "", "aab", false, false},
		{"pipe is literal - hit", []string{"a|b"}, "", "a|b", false, true},
		{"pipe is not an alternation", []string{"a|b"}, "", "a", false, false},
		{"parentheses are literal", []string{"(x)"}, "", "(x)", false, true},
		{"parentheses are not a group", []string{"(x)"}, "", "x", false, false},
		{"dollar is literal", []string{"x$y"}, "", "x$y", false, true},
		{"caret is literal", []string{"x^"}, "", "x^", false, true},
		{"dot is literal", []string{"a.c"}, "", "abc", false, false},
		{"braces are literal", []string{"a{1}"}, "", "a{1}", false, true},

		// -- Globs --
		{"? matches exactly one char - hit", []string{"?at"}, "", "cat", false, true},
		{"? matches exactly one char - miss on extra char", []string{"?at"}, "", "scat", false, false},
		{"a?c matches abc", []string{"a?c"}, "", "abc", false, true},
		{"a?c does not match ac", []string{"a?c"}, "", "ac", false, false},
		{"? does not match a slash", []string{"a/b?d"}, "", "a/b/d", false, false},
		{"bracket set matches a member", []string{"[abc].txt"}, "", "a.txt", false, true},
		{"bracket range matches a member", []string{"[a-c].txt"}, "", "b.txt", false, true},
		{"bracket range spares a non-member", []string{"[a-c].txt"}, "", "d.txt", false, false},
		{"negated bracket matches a non-member", []string{"[!ab].txt"}, "", "c.txt", false, true},
		{"negated bracket spares the excluded member", []string{"[!ab].txt"}, "", "a.txt", false, false},
		{"caret negates a bracket too - hit", []string{"[^ab].txt"}, "", "c.txt", false, true},
		{"caret negates a bracket too - miss", []string{"[^ab].txt"}, "", "b.txt", false, false},
		{"a leading ] is a set member", []string{"[]a]x"}, "", "]x", false, true},
		{"posix class matches a member", []string{"[[:digit:]]x"}, "", "1x", false, true},
		{"posix class spares a non-member", []string{"[[:digit:]]x"}, "", "ax", false, false},
		{"unterminated bracket matches nothing", []string{"[ab"}, "", "[ab", false, false},
		{"*.tx? matches with the required extra char", []string{"*.tx?"}, "", "file.txt", false, true},
		{"*.tx? does not match without the extra char", []string{"*.tx?"}, "", "file.tx", false, false},
		{"star in the middle of a name", []string{"a*z"}, "", "a-to-z", false, true},

		// -- Case sensitivity (the differing-case half is platform
		// dependent in git; see TestMatch_IsCaseSensitive) --
		{"pattern case matches the exact case", []string{"Foo.txt"}, "", "Foo.txt", false, true},

		// -- Nested-level relativity: the .gitignore lives in sub/ --
		{"nested gitignore, unanchored, direct child", []string{"*.log"}, "sub", "sub/build.log", false, true},
		{"nested gitignore, unanchored, grandchild still matches", []string{"*.log"}, "sub", "sub/deeper/build.log", false, true},
		{"nested gitignore, anchored, matches its own directory", []string{"/anchored.log"}, "sub", "sub/anchored.log", false, true},
		{"nested gitignore, anchored, spares a grandchild", []string{"/anchored.log"}, "sub", "sub/deeper/anchored.log", false, false},
		{"nested gitignore, embedded slash is relative to it", []string{"gen/out"}, "sub", "sub/gen/out", false, true},
	}
}

// ignoredInFile answers "does this one ignore file exclude rel" the way
// git does for a single .gitignore: an excluded parent directory settles
// it for everything beneath, then the entry's own verdict. This is the
// ancestor walk Match's doc comment hands to the caller, written out
// once here so the corpus can probe paths several levels deep.
func ignoredInFile(m *Matcher, rel string, isDir bool) bool {
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && m.Match(rel[:i], true) == Ignored {
			return true
		}
	}
	return m.Match(rel, isDir) == Ignored
}

// matcherVerdict runs one corpus row through the matcher, relativizing
// the probe to the ignore file's own directory first — the form every
// caller hands Match.
func matcherVerdict(tc ignoreCase) bool {
	rel := strings.TrimPrefix(strings.TrimPrefix(tc.probe, tc.giDir), "/")
	return ignoredInFile(CompileLines(tc.lines...), rel, tc.isDir)
}

// TestMatch_Corpus pins the matcher against the table with no git
// involved, so the semantics are fenced on any machine: anchoring of
// every pattern with a non-trailing slash, regexp metacharacters as
// plain bytes, "?" and "[!...]", escaped trailing spaces, and a bare
// "/" matching nothing are the rows the replaced library failed.
func TestMatch_Corpus(t *testing.T) {
	for _, tc := range ignoreCorpus() {
		if got := matcherVerdict(tc); got != tc.want {
			t.Errorf("%s: patterns=%q giDir=%q probe=%q isDir=%v — got ignored=%v, want %v",
				tc.name, tc.lines, tc.giDir, tc.probe, tc.isDir, got, tc.want)
		}
	}
}

// TestMatch_CorpusAgreesWithGit is the differential half: every row's
// expected verdict is put to a real `git check-ignore` in a throwaway
// repo. A failure here means the TABLE is wrong about git, which is the
// one mistake TestMatch_Corpus cannot catch on its own. git is a hard
// external requirement for that question, so the test skips without it.
func TestMatch_CorpusAgreesWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; cannot compare against ground truth")
	}
	for _, tc := range ignoreCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitVerdict(t, tc); got != tc.want {
				t.Errorf("git disagrees with the corpus: patterns=%q giDir=%q probe=%q isDir=%v — git ignored=%v, table says %v",
					tc.lines, tc.giDir, tc.probe, tc.isDir, got, tc.want)
			}
		})
	}
}

// TestMatch_Verdicts pins the three-state answer the tree's nested-file
// chain depends on: a "!" hit must be distinguishable from no hit at
// all, because the first lets a deeper file re-include what a shallower
// one excluded and the second defers to the shallower file.
func TestMatch_Verdicts(t *testing.T) {
	m := CompileLines("*.log", "!keep.log")
	for _, tc := range []struct {
		rel  string
		want Verdict
	}{
		{"a.log", Ignored},
		{"keep.log", Included},
		{"main.go", NoMatch},
		{"deep/down/keep.log", Included},
	} {
		if got := m.Match(tc.rel, false); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// TestMatch_NilAndEmptyMatchNothing pins the zero cases callers lean on
// instead of nil checks: a missing .gitignore (nil Matcher) and one
// holding only comments both answer NoMatch for everything.
func TestMatch_NilAndEmptyMatchNothing(t *testing.T) {
	var none *Matcher
	if got := none.Match("a.log", false); got != NoMatch {
		t.Errorf("nil Matcher: got %v, want NoMatch", got)
	}
	if got := Compile([]byte("# only a comment\n\n")).Match("a.log", false); got != NoMatch {
		t.Errorf("comment-only file: got %v, want NoMatch", got)
	}
}

// TestMatch_IsCaseSensitive pins the documented limit: matching is
// byte-exact. Git agrees on a case-sensitive filesystem and folds case
// (core.ignorecase) on a case-insensitive one, which is why this row is
// asserted here alone and kept out of the git differential.
func TestMatch_IsCaseSensitive(t *testing.T) {
	if got := CompileLines("Foo.txt").Match("foo.txt", false); got != NoMatch {
		t.Errorf("got %v, want NoMatch: pattern case must not fold", got)
	}
}

// TestCompile_SkipsBOM pins that a UTF-8 byte-order mark at the top of
// the file is not glued onto the first pattern, as git also ensures —
// without it the first line of an editor-saved .gitignore never matches.
func TestCompile_SkipsBOM(t *testing.T) {
	m := Compile([]byte("\xEF\xBB\xBFfirst.txt\n"))
	if got := m.Match("first.txt", false); got != Ignored {
		t.Errorf("got %v, want Ignored", got)
	}
}

// TestTrimTrailingSpaces pins the line-level whitespace rule on its own:
// only unescaped trailing spaces go, a backslash protects the character
// after it, and tabs and leading spaces are pattern text.
func TestTrimTrailingSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "plain",
		"two  ":      "two",
		`kept\ `:     `kept\ `,
		`kept\  `:    `kept\ `,
		"in side  ":  "in side",
		"  lead":     "  lead",
		"tab\t":      "tab\t",
		`slash\\  `:  `slash\\`,
		"   ":        "",
		`dangling\`:  `dangling\`,
		`a\ b\ \ `:   `a\ b\ \ `,
		"mid  x   ":  "mid  x",
		"":           "",
		`\ `:         `\ `,
		" x":         " x",
		`esc\x  `:    `esc\x`,
		"sp tab \t":  "sp tab \t",
		"sp tab\t  ": "sp tab\t",
	} {
		if got := trimTrailingSpaces(in); got != want {
			t.Errorf("trimTrailingSpaces(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWildmatch_PathologicalPatternTerminates is the reason the abort
// codes were ported rather than dropped: a run of stars against a long
// non-matching name is exponential in a naive backtracker, and a
// .gitignore is a file anyone in the repo can write. With the pruning
// this returns immediately.
func TestWildmatch_PathologicalPatternTerminates(t *testing.T) {
	pat := strings.Repeat("*a", 24) + "b"
	text := strings.Repeat("a", 60)
	if got := wildmatch(pat, text); got == wmMatch {
		t.Fatalf("pattern %q must not match %q", pat, text)
	}
}

// BenchmarkMatch measures one entry against a realistic ignore file —
// the per-row cost a tree sweep pays in every directory an ignore file
// reaches. The entry matches nothing, so every pattern is consulted.
func BenchmarkMatch(b *testing.B) {
	m := CompileLines(
		"# build output", "bin/", "/dist", "*.log", "*.tmp", "!keep.log",
		"node_modules", "coverage.out", "doc/**/*.pdf", "**/tmp",
		"*.py[co]", "/vendor/*/testdata", ".env", "*.sw?",
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.Match("internal/app/handler.go", false)
	}
}

// hermeticGitEnv isolates a git subprocess from the operator's real
// ~/.gitconfig and global excludesFile. A corpus this specific to
// gitignore semantics must not pick up a global ignore rule that
// happens to collide with a probe path; pointing HOME and
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

// gitVerdict builds a throwaway repo for one corpus row — the
// .gitignore at giDir, the probe created on disk as a real file or
// directory so git can settle the file-vs-directory question a path
// string cannot — and reports `git check-ignore -q`'s answer. Exit 0 is
// ignored and 1 is not; anything else means the harness's assumption
// about check-ignore no longer holds and is fatal.
func gitVerdict(t *testing.T, tc ignoreCase) bool {
	t.Helper()
	repo := t.TempDir()
	env := hermeticGitEnv(t)
	initCmd := exec.Command("git", "-C", repo, "init", "-q")
	initCmd.Env = env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	giAbsDir := filepath.Join(repo, filepath.FromSlash(tc.giDir))
	if err := os.MkdirAll(giAbsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", giAbsDir, err)
	}
	body := strings.Join(tc.lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(giAbsDir, ".gitignore"), []byte(body), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	target := filepath.Join(repo, filepath.FromSlash(tc.probe))
	if tc.isDir {
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

	// The probe is named WITHOUT a trailing slash even when it is a
	// directory: git reads the type off the disk, and a trailing slash
	// in the query leaves check-ignore matching an empty basename —
	// which makes a bare "/" pattern appear to ignore every directory.
	arg := tc.probe
	cmd := exec.Command("git", "-C", repo, "check-ignore", "-q", "--", arg)
	cmd.Env = env
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore -- %q: %v (harness assumes exit 0 or 1)", arg, err)
	return false
}
