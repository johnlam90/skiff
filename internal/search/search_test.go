// =============================================================================
// File: internal/search/search_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the project-search engine: literal matching, smart-case,
// binary/oversize skipping, and the result caps.

package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed writes name → content under root and returns the relative name.
func seed(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return name
}

// TestSearchLiteralHits pins the basics: hits carry the right file,
// 1-based line, rune column, and line text, in file order.
func TestSearchLiteralHits(t *testing.T) {
	root := t.TempDir()
	f1 := seed(t, root, "a.go", "package a\n\nfunc Hello() {}\n")
	f2 := seed(t, root, "b.go", "// Hello again\n")

	got, trunc := Search(root, []string{f1, f2}, "Hello", DefaultOptions())
	if trunc {
		t.Fatal("no caps should trip here")
	}
	if len(got) != 2 {
		t.Fatalf("matches: got %d, want 2", len(got))
	}
	if got[0].Path != "a.go" || got[0].Line != 3 || got[0].Col != 5 {
		t.Fatalf("first match wrong: %+v", got[0])
	}
	if got[1].Path != "b.go" || got[1].Line != 1 {
		t.Fatalf("second match wrong: %+v", got[1])
	}
	if got[0].Text != "func Hello() {}" {
		t.Fatalf("text: got %q", got[0].Text)
	}
}

// TestSearchSmartCase: a lowercase query matches any case; one capital
// letter makes the query exact.
func TestSearchSmartCase(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "x.txt", "Hello\nhello\nHELLO\n")

	got, _ := Search(root, []string{f}, "hello", DefaultOptions())
	if len(got) != 3 {
		t.Fatalf("lowercase query should hit all 3 casings, got %d", len(got))
	}
	got, _ = Search(root, []string{f}, "Hello", DefaultOptions())
	if len(got) != 1 || got[0].Line != 1 {
		t.Fatalf("cased query should hit only the exact line, got %+v", got)
	}
}

// TestSearchSkipsBinary: a NUL byte in the head marks a file binary and
// excludes it entirely.
func TestSearchSkipsBinary(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "bin.dat", "match\x00me\nmatch\n")

	got, _ := Search(root, []string{f}, "match", DefaultOptions())
	if len(got) != 0 {
		t.Fatalf("binary file should be skipped, got %d matches", len(got))
	}
}

// TestSearchCapsAndTruncatedFlag: the per-file and total caps stop the
// sweep and report truncation instead of flooding the UI.
func TestSearchCapsAndTruncatedFlag(t *testing.T) {
	root := t.TempDir()
	many := strings.Repeat("hit\n", 100)
	f1 := seed(t, root, "one.txt", many)
	f2 := seed(t, root, "two.txt", many)

	opts := Options{MaxTotal: 60, MaxPerFile: 50, MaxFileSize: 1 << 20}
	got, trunc := Search(root, []string{f1, f2}, "hit", opts)
	if !trunc {
		t.Fatal("caps were hit — truncated must be true")
	}
	if len(got) != 60 {
		t.Fatalf("total cap: got %d, want 60", len(got))
	}
	perFile := 0
	for _, m := range got {
		if m.Path == "one.txt" {
			perFile++
		}
	}
	if perFile != 50 {
		t.Fatalf("per-file cap: got %d from one.txt, want 50", perFile)
	}
}

// TestSearchMissingFileSkipped: a stale index entry (file deleted since
// the last build) is skipped without error.
func TestSearchMissingFileSkipped(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "real.txt", "needle\n")

	got, _ := Search(root, []string{"ghost.txt", f}, "needle", DefaultOptions())
	if len(got) != 1 || got[0].Path != "real.txt" {
		t.Fatalf("expected only the real file's hit, got %+v", got)
	}
}

// TestSearchEmptyQuery returns nothing rather than matching every line.
func TestSearchEmptyQuery(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "x.txt", "anything\n")
	if got, _ := Search(root, []string{f}, "   ", DefaultOptions()); len(got) != 0 {
		t.Fatalf("blank query must return nothing, got %d", len(got))
	}
}

// TestSearchCancellation: a cancelled sweep abandons between files and
// returns nothing — its results would be dropped anyway, so the walk
// must not keep paying for disk reads.
func TestSearchCancellation(t *testing.T) {
	root := t.TempDir()
	f1 := seed(t, root, "a.txt", "hit\n")
	f2 := seed(t, root, "b.txt", "hit\n")
	opts := DefaultOptions()
	opts.Cancelled = func() bool { return true }
	if got, _ := Search(root, []string{f1, f2}, "hit", opts); got != nil {
		t.Fatalf("cancelled sweep should return nothing, got %v", got)
	}
}

// TestSearchToggles pins the three chips: MatchCase forces exactness
// on a lowercase query, WholeWord drops substring hits, Regex matches
// patterns (and a broken pattern matches nothing rather than erroring).
func TestSearchToggles(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "x.txt", "Foo food foo\nfootball\n")

	opts := DefaultOptions()
	opts.MatchCase = true
	got, _ := Search(root, []string{f}, "foo", opts)
	if len(got) != 2 {
		t.Fatalf("MatchCase: got %d lines, want 2", len(got))
	}

	opts = DefaultOptions()
	opts.WholeWord = true
	got, _ = Search(root, []string{f}, "foo", opts)
	if len(got) != 1 || got[0].Line != 1 {
		t.Fatalf("WholeWord: got %+v", got)
	}
	if got[0].Col != 0 {
		// "Foo" matches case-insensitively at col 0 and is word-bounded.
		t.Fatalf("WholeWord col: got %d", got[0].Col)
	}

	opts = DefaultOptions()
	opts.Regex = true
	got, _ = Search(root, []string{f}, "fo+tball", opts)
	if len(got) != 1 || got[0].Line != 2 {
		t.Fatalf("Regex: got %+v", got)
	}
	got, _ = Search(root, []string{f}, "fo(", opts)
	if len(got) != 0 {
		t.Fatalf("broken regex should match nothing, got %+v", got)
	}
}

// TestReplaceLine pins the rewrite rules: every qualifying occurrence
// swaps, scanning never re-matches its own output, and the mode flags
// (word, case, regex-with-literal-replacement) are honoured.
func TestReplaceLine(t *testing.T) {
	got, n := ReplaceLine("foo foo food", "foo", "foofoo", DefaultOptions())
	if got != "foofoo foofoo foofood" || n != 3 {
		t.Fatalf("basic: %q (%d)", got, n)
	}
	opts := DefaultOptions()
	opts.WholeWord = true
	got, n = ReplaceLine("foo food foo", "foo", "x", opts)
	if got != "x food x" || n != 2 {
		t.Fatalf("word: %q (%d)", got, n)
	}
	opts = DefaultOptions()
	opts.Regex = true
	got, n = ReplaceLine("a1 b22 c3", "[0-9]+", "#", opts)
	if got != "a# b# c#" || n != 3 {
		t.Fatalf("regex: %q (%d)", got, n)
	}
	if _, n = ReplaceLine("anything", "", "x", DefaultOptions()); n != 0 {
		t.Fatalf("empty query must be a no-op, replaced %d", n)
	}
}

// TestApplyReplaceVerifiesAndWrites drives the disk path: matched
// lines rewrite atomically, drifted lines are skipped and counted, and
// untouched files stay untouched.
func TestApplyReplaceVerifiesAndWrites(t *testing.T) {
	root := t.TempDir()
	f1 := seed(t, root, "a.txt", "keep\nold value\nkeep\n")
	f2 := seed(t, root, "b.txt", "old drifted\n")

	matches, _ := Search(root, []string{f1, f2}, "old", DefaultOptions())
	if len(matches) != 2 {
		t.Fatalf("seed matches: %d", len(matches))
	}
	// Drift b.txt's line after the sweep — its match must be skipped.
	seedOverwrite := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
	}
	seedOverwrite("b.txt", "edited since\n")

	rep := ApplyReplace(root, matches, "old", "new", DefaultOptions())
	if rep.Replaced != 1 || rep.Files != 1 || rep.Skipped != 1 {
		t.Fatalf("report: %+v", rep)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(data) != "keep\nnew value\nkeep\n" {
		t.Fatalf("a.txt: %q", data)
	}
	data, _ = os.ReadFile(filepath.Join(root, "b.txt"))
	if string(data) != "edited since\n" {
		t.Fatalf("b.txt must be untouched: %q", data)
	}
}

// TestReplaceLineRegexGroups pins the expansion contract: $1/${name}
// insert captures with Go's ReplaceAllString semantics, $$ escapes a
// literal dollar, literal mode leaves $ alone, and expansion composes
// with whole-word.
func TestReplaceLineRegexGroups(t *testing.T) {
	opts := DefaultOptions()
	opts.Regex = true
	got, n := ReplaceLine("a=1 b=2", `(\w+)=(\w+)`, "$2:$1", opts)
	if got != "1:a 2:b" || n != 2 {
		t.Fatalf("swap: %q (%d)", got, n)
	}
	got, _ = ReplaceLine("price 5", `(?P<num>[0-9]+)`, "$$${num}", opts)
	if got != "price $5" {
		t.Fatalf("named+escape: %q", got)
	}
	// ${1}x disambiguates from the nonexistent group $1x.
	got, _ = ReplaceLine("ab", `(a)`, "${1}x", opts)
	if got != "axb" {
		t.Fatalf("braced: %q", got)
	}
	opts.WholeWord = true
	got, n = ReplaceLine("cat catalog cat", `(c)at`, "${1}ow", opts)
	if got != "cow catalog cow" || n != 2 {
		t.Fatalf("word+groups: %q (%d)", got, n)
	}
	// Bare $1ow munches the whole name (Go's documented rule): group
	// "1ow" doesn't exist, so it expands empty — ${1}ow is the fix.
	got, _ = ReplaceLine("cat", `(c)at`, "$1ow", opts)
	if got != "" {
		t.Fatalf("bare-name munch should match Go semantics: %q", got)
	}
	// Literal mode: $ stays a $.
	got, _ = ReplaceLine("x", "x", "$1", DefaultOptions())
	if got != "$1" {
		t.Fatalf("literal mode must not expand: %q", got)
	}
}

// TestApplyReplaceRegexGroups: expansion flows through the disk path.
func TestApplyReplaceRegexGroups(t *testing.T) {
	root := t.TempDir()
	f := seed(t, root, "kv.txt", "name=skiff\n")
	opts := DefaultOptions()
	opts.Regex = true
	matches, _ := Search(root, []string{f}, `(\w+)=(\w+)`, opts)
	rep := ApplyReplace(root, matches, `(\w+)=(\w+)`, `$2 is the $1`, opts)
	if rep.Replaced != 1 {
		t.Fatalf("report: %+v", rep)
	}
	data, _ := os.ReadFile(filepath.Join(root, "kv.txt"))
	if string(data) != "skiff is the name\n" {
		t.Fatalf("expanded write: %q", data)
	}
}

// TestReplaceLineZeroWidthMatchAtEnd is the regression for the crash
// FuzzReplaceLine found: a regex that can match empty (`a*`) fires once
// more at end-of-line after consuming the whole string, and the
// force-progress step pushed the tail offset one byte past the line. The
// unconditional tail write then sliced out of range and took the whole
// editor down mid-replace.
func TestReplaceLineZeroWidthMatchAtEnd(t *testing.T) {
	opts := DefaultOptions()
	opts.Regex = true

	got, n := ReplaceLine("aaa", "a*", "-", opts)
	if n < 1 {
		t.Fatalf("expected at least one replacement, got %d", n)
	}
	if strings.Contains(got, "a") {
		t.Fatalf("greedy a* should have consumed every 'a', got %q", got)
	}

	// The empty line is the same shape with nothing to consume at all.
	if _, n := ReplaceLine("", "a*", "-", opts); n < 0 {
		t.Fatalf("empty line replace returned %d", n)
	}
}

// TestFoldLineIsByteAligned pins the invariant every literal probe leans
// on: the folded haystack is the same length as the line and shares its
// byte offsets, so an index found in one names the same position in the
// other. That is what lets the fold be computed once per line instead of
// once per probe. Runes whose lowercase encodes to a different width are
// deliberately left unfolded — folding them would slide every later
// offset and let a replacement cut the original line mid-rune.
func TestFoldLineIsByteAligned(t *testing.T) {
	cases := []string{
		"",
		"Plain ASCII Line",
		"ÄÖÜ mixed Ünicode",
		"\u212Aelvin",   // KELVIN SIGN: its lowercase 'k' is narrower
		"\u0130stanbul", // dotted capital I
		"invalid \xff byte",
		strings.Repeat("AbC", 100),
	}
	for _, line := range cases {
		folded := foldLine(line)
		if len(folded) != len(line) {
			t.Fatalf("foldLine(%q) changed length %d -> %d", line, len(line), len(folded))
		}
	}

	// Where the fold is width-safe it must agree with strings.ToLower,
	// or case-insensitive matching would quietly stop finding things.
	for _, line := range []string{"Plain ASCII Line", "ÄÖÜ mixed Ünicode", ""} {
		if got, want := foldLine(line), strings.ToLower(line); got != want {
			t.Fatalf("foldLine(%q) = %q, want %q", line, got, want)
		}
	}

	// Case-sensitive and regex modes want the untouched line.
	if got := matchHaystack("MiXeD", true, nil); got != "MiXeD" {
		t.Fatalf("case-sensitive haystack = %q", got)
	}
	if got := matchHaystack("MiXeD", false, nil); got != "mixed" {
		t.Fatalf("case-insensitive haystack = %q", got)
	}
}

// TestReplaceLineCaseInsensitiveScan walks one line through the matcher
// many times — the loop that used to re-lowercase the whole remaining
// tail on every probe, making a long line quadratic. Folding once up
// front must not change which occurrences are found or where they sit.
func TestReplaceLineCaseInsensitiveScan(t *testing.T) {
	got, n := ReplaceLine("Foo mid FOO tail foO", "foo", "bar", DefaultOptions())
	if n != 3 || got != "bar mid bar tail bar" {
		t.Fatalf("case-insensitive scan = %q (%d)", got, n)
	}

	// Multi-byte folds keep their offsets aligned with the original.
	got, n = ReplaceLine("ÄÖÜ and äöü", "äöü", "x", DefaultOptions())
	if n != 2 || got != "x and x" {
		t.Fatalf("unicode fold = %q (%d)", got, n)
	}

	// Whole-word rejects candidates and re-probes from the next byte, so
	// it hits the shared haystack at several offsets per line.
	opts := DefaultOptions()
	opts.WholeWord = true
	got, n = ReplaceLine("Food FOO foodie foo", "foo", "bar", opts)
	if n != 2 || got != "Food bar foodie bar" {
		t.Fatalf("whole-word scan = %q (%d)", got, n)
	}
}

// FuzzCompileQuery pins the contract ReplaceLine and the sweep both read
// off CompileQuery: the smart-case decision is exactly "MatchCase or the
// query carries an uppercase rune", the returned needle is pre-lowered
// precisely when matching is case-insensitive (matchHaystack lowers the
// line and the probe compares raw, so a needle that isn't lowered would
// silently never match), and a failed compile hands back zero values
// rather than a half-built matcher a caller could mistake for a working
// one.
func FuzzCompileQuery(f *testing.F) {
	seeds := []struct {
		query   string
		regex   bool
		matchCS bool
	}{
		{"", false, false},
		{"skiff", false, false},
		{"Skiff", false, false},
		{"日本語", false, false},
		{"a(b", true, false},
		{"(a+)+", true, false},
		{"^$", true, false},
		{"[", true, true},
		{"\r", false, false},
		{"ünïcödé", false, false},
		{"İ", false, false},
		{strings.Repeat("x", 4096), false, false},
	}
	for _, s := range seeds {
		f.Add(s.query, s.regex, s.matchCS)
	}

	f.Fuzz(func(t *testing.T, query string, regex, matchCase bool) {
		opts := DefaultOptions()
		opts.Regex = regex
		opts.MatchCase = matchCase

		needle, caseSensitive, re, ok := CompileQuery(query, opts)

		if !ok {
			if !regex {
				t.Fatalf("literal query %q must always compile", query)
			}
			if needle != "" || re != nil || caseSensitive {
				t.Fatalf("failed compile leaked state: needle=%q re=%v cs=%v", needle, re, caseSensitive)
			}
			return
		}

		if want := matchCase || hasUpper(query); caseSensitive != want {
			t.Fatalf("caseSensitive=%v, want %v for query %q (MatchCase=%v)", caseSensitive, want, query, matchCase)
		}
		if caseSensitive {
			if needle != query {
				t.Fatalf("case-sensitive needle %q, want the query verbatim %q", needle, query)
			}
		} else if needle != strings.ToLower(query) {
			t.Fatalf("case-insensitive needle %q, want it pre-lowered to %q", needle, strings.ToLower(query))
		}
		if regex != (re != nil) {
			t.Fatalf("Regex=%v but re==nil is %v", regex, re == nil)
		}
		if re != nil {
			// The compiled pattern must agree with the case decision, or
			// the sweep and the highlighter disagree about what matched.
			if !caseSensitive && !strings.HasPrefix(re.String(), "(?i)") {
				t.Fatalf("case-insensitive regex %q lacks the (?i) flag", re.String())
			}
			re.MatchString("probe")
		}
	})
}

// FuzzReplaceLine pins project replace against adversarial lines. In
// literal, case-sensitive mode the function must be indistinguishable from
// strings.ReplaceAll — that is the whole promise the replace preview makes
// to the user, and getting it wrong rewrites source files. The regex and
// smart-case arms get the weaker but still real invariants: a zero count
// must leave the line untouched, and a non-zero count must correspond to
// a matcher that actually fires.
func FuzzReplaceLine(f *testing.F) {
	seeds := []struct {
		line  string
		query string
		repl  string
		regex bool
	}{
		{"", "", "", false},
		{"skiff is skiff", "skiff", "boat", false},
		{"aaaa", "aa", "b", false},
		{"日本語のテキスト", "テキスト", "text", false},
		{"MiXeD", "mixed", "x", false},
		{"trailing\r", "\r", "", false},
		{"          ", " ", "_", false},
		{strings.Repeat("ab", 4096), "aba", "z", false},
		{"key = value", `(\w+) = (\w+)`, "$2 = $1", true},
		{"aaa", "a*", "-", true},
		{"abc", "", "x", true},
		{"e\u0301e\u0301", "e\u0301", "e", false},
	}
	for _, s := range seeds {
		f.Add(s.line, s.query, s.repl, s.regex)
	}

	f.Fuzz(func(t *testing.T, line, query, repl string, regex bool) {
		if regex {
			opts := DefaultOptions()
			opts.Regex = true
			got, n := ReplaceLine(line, query, repl, opts)
			if n < 0 {
				t.Fatalf("negative replacement count %d", n)
			}
			if n == 0 && got != line {
				t.Fatalf("zero replacements but line changed:\n got %q\nwant %q", got, line)
			}
			if _, _, re, ok := CompileQuery(query, opts); n > 0 && (!ok || re == nil || !re.MatchString(line)) {
				t.Fatalf("reported %d replacements for a pattern that does not match %q", n, line)
			}
			return
		}

		// Literal + forced exact case: ReplaceLine must be ReplaceAll.
		opts := DefaultOptions()
		opts.MatchCase = true
		got, n := ReplaceLine(line, query, repl, opts)

		if query == "" {
			if got != line || n != 0 {
				t.Fatalf("empty query must be a no-op, got %q / %d", got, n)
			}
			return
		}
		if want := strings.Count(line, query); n != want {
			t.Fatalf("replaced %d occurrences of %q in %q, want %d", n, query, line, want)
		}
		if want := strings.ReplaceAll(line, query, repl); got != want {
			t.Fatalf("literal replace diverged from ReplaceAll:\n got %q\nwant %q", got, want)
		}
	})
}
