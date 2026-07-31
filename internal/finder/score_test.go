// =============================================================================
// File: internal/finder/score_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package finder

import "testing"

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
