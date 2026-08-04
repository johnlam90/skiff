// =============================================================================
// File: internal/finder/score_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package finder

import (
	"strings"
	"testing"
	"unicode"
)

// TestScore_NoMatchReturnsZero pins the "every query rune must
// appear in order" contract. Without it, a query like "xyz" could
// silently match every file with a non-zero score and the results
// list would be useless.
func TestScore_NoMatchReturnsZero(t *testing.T) {
	if s, _ := Score("xyz", "internal/app/app.go"); s != 0 {
		t.Fatalf("expected 0 for no-match query, got %d", s)
	}
	// Out-of-order query characters also fail.
	if s, _ := Score("og", "go"); s != 0 {
		t.Fatalf("expected 0 for out-of-order query, got %d", s)
	}
}

// TestScore_EmptyQueryMatches lets callers feed the empty string
// without writing a special case at every use site. Returns a tiny
// positive score (treated as "any file") with no highlight indexes.
func TestScore_EmptyQueryMatches(t *testing.T) {
	s, idx := Score("", "anywhere")
	if s == 0 {
		t.Fatal("empty query should match")
	}
	if len(idx) != 0 {
		t.Fatalf("empty query should not produce highlights, got %v", idx)
	}
}

// TestScore_BasenameBeatsDirname is the headline ranking rule: a
// query that hits inside the file's basename should outrank one
// that only hits inside the directory part. Without this users
// type "tab" and get "internal/tabs/foo.go" above "tab.go", which
// is the opposite of what every other fuzzy finder does.
func TestScore_BasenameBeatsDirname(t *testing.T) {
	basenameHit, _ := Score("tab", "internal/app/tab.go")
	dirHit, _ := Score("tab", "internal/tabs/foo.go")
	if basenameHit <= dirHit {
		t.Fatalf("basename hit (%d) should outrank dir hit (%d)",
			basenameHit, dirHit)
	}
}

// TestScore_ConsecutiveBeatsScattered guards the second-most-
// important rule: "abc" matching "abc.go" should outrank the
// same query matching "a_b_c.go", because consecutive characters
// signal stronger user intent.
func TestScore_ConsecutiveBeatsScattered(t *testing.T) {
	tight, _ := Score("abc", "abc.go")
	scattered, _ := Score("abc", "a_b_c.go")
	if tight <= scattered {
		t.Fatalf("tight (%d) should outrank scattered (%d)", tight, scattered)
	}
}

// TestScore_CaseInsensitive pins the "query case doesn't matter"
// rule. Users will type "tab" and expect to match "Tab.go"; the
// inverse (typing exact casing) is rare enough that flexibility
// wins. Lowercasing inside the matcher is the simplest path.
func TestScore_CaseInsensitive(t *testing.T) {
	if s, _ := Score("tab", "Tab.go"); s == 0 {
		t.Fatal("expected case-insensitive match")
	}
	if s, _ := Score("TAB", "tab.go"); s == 0 {
		t.Fatal("uppercase query should match lowercase path")
	}
}

// TestScore_MatchedIndexesAlignWithRunes checks that the indexes
// returned for highlighting actually point at the right runes. A
// regression here would let the UI underline the wrong characters.
// The basename here ("main.txt") can't fit the query, so the
// leftmost walk is the winning alignment and its indexes must be
// the ones returned.
func TestScore_MatchedIndexesAlignWithRunes(t *testing.T) {
	_, idx := Score("ago", "app/go/main.txt")
	// Greedy walk: 'a' at 0, 'g' at 4, 'o' at 5.
	want := []int{0, 4, 5}
	if len(idx) != len(want) {
		t.Fatalf("idx length: got %v, want %v", idx, want)
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Fatalf("idx[%d]: got %d, want %d", i, idx[i], want[i])
		}
	}
}

// TestScore_WordBoundaryBonus confirms the word-boundary heuristic
// fires: "go" matching "x_go.txt" (after underscore) should beat
// "go" matching "ago" (mid-word) by a meaningful margin.
func TestScore_WordBoundaryBonus(t *testing.T) {
	boundary, _ := Score("go", "x_go.txt")
	midword, _ := Score("go", "ago.txt")
	if boundary <= midword {
		t.Fatalf("word-boundary hit (%d) should outrank mid-word (%d)",
			boundary, midword)
	}
}

// TestScore_FirstCharBonus locks in the rank order people expect
// when they type a prefix: "tab" should rank "tab.go" above
// "stable.go" simply because the match starts at position 0.
func TestScore_FirstCharBonus(t *testing.T) {
	prefix, _ := Score("tab", "tab.go")
	mid, _ := Score("tab", "stable.go")
	if prefix <= mid {
		t.Fatalf("prefix match (%d) should outrank mid-string (%d)",
			prefix, mid)
	}
}

// TestScore_MinimumOne floors the score at 1 for any actual match,
// even one with lots of gaps. Without the floor a sufficiently
// scattered match could go to 0 and disappear from results — the
// correct behaviour is "a bad match still appears, just at the
// bottom of the list."
func TestScore_MinimumOne(t *testing.T) {
	// Query forces every char to be far apart.
	s, _ := Score("ze", "abcdefghijklmnopqrstuvwxyzfffe")
	if s < 1 {
		t.Fatalf("expected score >= 1 for any real match, got %d", s)
	}
}

// TestScore_NestedBasenameBeatsScatteredElsewhere is the regression
// test for the ranking bug the README example promises against:
// "tab" must rank internal/editor/tab.go above paths that merely
// scatter t-a-b across directories and unrelated basenames. The pure
// left-to-right greedy walk bound 't','a' inside "internal" and
// scored the real basename hit a 1, sinking it below files like
// copy-button.js (39) in this repo's own index.
func TestScore_NestedBasenameBeatsScatteredElsewhere(t *testing.T) {
	nested, _ := Score("tab", "internal/editor/tab.go")
	scattered, _ := Score("tab", "website/assets/js/copy-button.js")
	if nested <= scattered {
		t.Fatalf("nested basename hit (%d) should outrank scattered match (%d)",
			nested, scattered)
	}
}

// TestScore_BasenameRetryHighlightsBasename pins that when the
// basename-anchored walk wins, the returned MatchedIndexes point at
// the basename runes — the highlight must follow the alignment that
// produced the score, or the UI underlines the wrong characters.
func TestScore_BasenameRetryHighlightsBasename(t *testing.T) {
	_, idx := Score("tab", "internal/editor/tab.go")
	// Basename "tab.go" starts at rune 16; t=16, a=17, b=18.
	want := []int{16, 17, 18}
	if len(idx) != len(want) {
		t.Fatalf("expected %d matched indexes, got %v", len(want), idx)
	}
	for i, w := range want {
		if idx[i] != w {
			t.Fatalf("matched indexes = %v, want %v", idx, want)
		}
	}
}

// FuzzScore pins the matcher's alignment contract. The finder UI paints
// MatchedIndexes as highlights over the path, so a returned index that is
// out of range, out of order, or points at a rune the query never asked
// for would either underline the wrong characters or index-panic the
// renderer. The other half is the subsequence rule itself: a positive
// score must be backed by a real in-order, case-insensitive match, and a
// zero score must carry no highlight at all.
func FuzzScore(f *testing.F) {
	seeds := []struct {
		query string
		path  string
	}{
		{"", ""},
		{"", "internal/app/app.go"},
		{"tab", "internal/editor/tab.go"},
		{"xyz", "internal/app/app.go"},
		{"go", "a_b-c.d/go"},
		{"日本", "docs/日本語.md"},
		{"AB", "ab/AB.go"},
		{"i", "İstanbul.txt"},
		{"////", "////"},
		{"a", strings.Repeat("a", 8192)},
		{strings.Repeat("a", 512), strings.Repeat("a", 512)},
		{"e\u0301", "cafe\u0301.go"},
		{"\\", "windows\\style\\path.go"},
	}
	for _, s := range seeds {
		f.Add(s.query, s.path)
	}

	f.Fuzz(func(t *testing.T, query, path string) {
		score, idx := Score(query, path)

		if score < 0 {
			t.Fatalf("score must never go negative, got %d", score)
		}
		if query == "" {
			if score != 1 || idx != nil {
				t.Fatalf("empty query must score 1 with no highlight, got %d / %v", score, idx)
			}
			return
		}
		if score == 0 {
			if idx != nil {
				t.Fatalf("no-match score carried highlight indexes %v", idx)
			}
			// A zero score claims the query is not a subsequence of the
			// path; verify that independently so the matcher can't quietly
			// start dropping real hits.
			if isSubsequenceFold(query, path) {
				t.Fatalf("scored 0 but %q IS an in-order match inside %q", query, path)
			}
			return
		}

		pathRunes := []rune(path)
		queryRunes := []rune(query)
		if len(idx) != len(queryRunes) {
			t.Fatalf("got %d highlight indexes for a %d-rune query", len(idx), len(queryRunes))
		}
		prev := -1
		for i, at := range idx {
			if at <= prev {
				t.Fatalf("highlight indexes not strictly ascending: %v", idx)
			}
			if at < 0 || at >= len(pathRunes) {
				t.Fatalf("highlight index %d escapes the %d-rune path", at, len(pathRunes))
			}
			if unicode.ToLower(pathRunes[at]) != unicode.ToLower(queryRunes[i]) {
				t.Fatalf("highlight %d points at %q, query rune %d is %q",
					at, string(pathRunes[at]), i, string(queryRunes[i]))
			}
			prev = at
		}
	})
}

// isSubsequenceFold reports whether query's runes appear inside path in
// order under simple case folding — the same rule Score's greedy walk
// enforces, reimplemented here so the fuzz target checks the matcher
// against an independent definition rather than against itself.
func isSubsequenceFold(query, path string) bool {
	q := []rune(query)
	qi := 0
	for _, r := range path {
		if qi < len(q) && unicode.ToLower(r) == unicode.ToLower(q[qi]) {
			qi++
		}
	}
	return qi == len(q)
}
