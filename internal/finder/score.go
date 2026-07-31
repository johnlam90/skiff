// =============================================================================
// File: internal/finder/score.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package finder implements project-wide file search: an in-memory
// index of every non-ignored file under the project root, plus a
// fuzzy matcher that ranks paths against a typed query.
//
// The match algorithm is a small fzy-style fuzzy matcher: every
// query character must appear in the path in order (case-insensitive),
// then a positive integer score is computed from a handful of
// heuristics that VS Code's Cmd+P and similar finders use:
//
//   - Matches in the basename outrank matches in the directory.
//   - Consecutive runs of matched characters outrank scattered ones.
//   - Matches at word boundaries (`/`, `_`, `-`, `.`) are favoured
//     so "tab" matches "tab.go" before it matches "stable.go".
//   - Earlier match positions break ties.
//
// The matcher returns both the score and the matched rune indexes so
// the UI can highlight which characters of each result line up with
// the query — the same trick `fzf` uses to make results scannable.
package finder

import "unicode"

// Match is the result of scoring one path against a query. Score
// is positive for any hit (higher is better); MatchedIndexes lists
// the rune indexes inside the path that line up with the query —
// the renderer uses those to highlight matched characters.
type Match struct {
	Score          int
	MatchedIndexes []int
}

// scoring constants. Pulled out so a future tweak (e.g. weighting
// basename hits even harder) is one line, and so the comments below
// can refer to each by name. Magnitudes were tuned to make the
// "basename match wins" rule visible in test ranking — the smallest
// gap between two reasonable matches should still differ by at
// least one full bonus, not a 1-point fudge.
const (
	bonusBasename     = 15 // match falls inside the file's basename
	bonusWordBoundary = 20 // match starts a word (after / _ - .)
	bonusConsecutive  = 30 // match continues an unbroken run
	bonusFirstChar    = 10 // first matched char is at position 0
	penaltyGap        = 1  // each unmatched char between matches
)

// Why bonusConsecutive (30) > bonusWordBoundary (20): a tight run
// of three matched chars in a row (e.g. "abc" hitting "abc.go")
// must outrank the same query scattered across word boundaries
// (e.g. "abc" hitting "a_b_c.go"). Without this, "type the start
// of the basename" — the most common finder pattern — would lose
// to anything with underscores.

// Score returns (score, matchedIndexes) for query against path.
// score == 0 means "no match" — every query rune must appear in
// path in order (case-insensitive) to score above zero.
//
// The matcher is greedy left-to-right: for each query rune we
// take the first matching path rune we haven't consumed yet. Pure
// greedy is faster than a full DP, but its leftmost alignment can
// bind early query runes to directory characters and sink the real
// basename hit ("tab" against internal/editor/tab.go used to match
// t,a inside "internal" and score a 1). So the walk runs twice —
// once from the path start, once anchored at the basename — and the
// better alignment wins. That keeps the common "type the file's
// name" pattern ranked the way every other fuzzy finder ranks it,
// at the cost of one extra bounded walk per candidate.
func Score(query, path string) (int, []int) {
	if query == "" {
		// Empty query matches everything, with no highlighting and
		// a tiny score so the caller can keep results sorted by
		// some external order (typically alphabetical).
		return 1, nil
	}
	if path == "" {
		return 0, nil
	}

	pathRunes := []rune(path)
	queryRunes := []rune(query)

	score, matchedIdx := scoreFrom(pathRunes, queryRunes, 0)
	if score == 0 {
		// No subsequence anywhere in the path; the anchored walk
		// searches a subrange and cannot succeed either.
		return 0, nil
	}
	if basenameStart := lastSeparatorIndex(pathRunes) + 1; basenameStart > 0 {
		if s, idx := scoreFrom(pathRunes, queryRunes, basenameStart); s > score {
			return s, idx
		}
	}
	return score, matchedIdx
}

// scoreFrom runs the greedy match walk over pathRunes starting at rune
// offset start, then scores the alignment it found. Returns (0, nil)
// when the query doesn't fit in path[start:]. The scoring rules are
// identical for both walks — only the anchor differs — so a basename
// alignment wins exactly when its bonuses genuinely beat the leftmost
// alignment, not because it plays by different rules.
func scoreFrom(pathRunes, queryRunes []rune, start int) (int, []int) {
	matchedIdx := make([]int, 0, len(queryRunes))

	// First pass: find the matched indexes via greedy walk. Bail as
	// soon as we know the query can't fit — saves us scoring paths
	// that already lost.
	pi := start
	for qi := 0; qi < len(queryRunes); qi++ {
		want := unicode.ToLower(queryRunes[qi])
		found := false
		for pi < len(pathRunes) {
			got := unicode.ToLower(pathRunes[pi])
			if got == want {
				matchedIdx = append(matchedIdx, pi)
				pi++
				found = true
				break
			}
			pi++
		}
		if !found {
			return 0, nil
		}
	}

	// Second pass: score the matches we found. Cheaper than scoring
	// during the walk because it lets the matching loop stay tight.
	score := 0
	basenameStart := lastSeparatorIndex(pathRunes) + 1
	prevIdx := -2 // -2 so the first match never counts as consecutive
	for i, idx := range matchedIdx {
		if i == 0 && idx == 0 {
			score += bonusFirstChar
		}
		if idx == prevIdx+1 {
			score += bonusConsecutive
		}
		if idx >= basenameStart {
			score += bonusBasename
		}
		// Position 0 is the start of a word by definition — without
		// this, "tab" hitting tab.go (10-point first-char bonus) would
		// lose to the same query hitting a *longer* basename like
		// tabbar.go, whose match sits after a '/' and collects the
		// 20-point boundary bonus instead.
		if idx == 0 || isWordBoundary(pathRunes[idx-1]) {
			score += bonusWordBoundary
		}
		gap := idx - (prevIdx + 1)
		if gap > 0 && i > 0 {
			score -= gap * penaltyGap
		}
		prevIdx = idx
	}

	// Floor at 1 — even a "bad" match (lots of gaps) should outrank
	// a non-match (zero) so the result still appears.
	if score < 1 {
		score = 1
	}
	return score, matchedIdx
}

// lastSeparatorIndex returns the index of the last `/` (or `\\` on
// Windows-y paths) in r, or -1 when none. Pulled into a helper so
// the score loop reads as the rule it's enforcing rather than path
// arithmetic.
func lastSeparatorIndex(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == '/' || r[i] == '\\' {
			return i
		}
	}
	return -1
}

// isWordBoundary reports whether r is the kind of separator after
// which the *next* character is the start of a "word" — the cue we
// use to upweight `tab` matching `cmd/tab.go` over `command-table.go`.
func isWordBoundary(r rune) bool {
	switch r {
	case '/', '\\', '_', '-', '.', ' ':
		return true
	}
	return false
}
