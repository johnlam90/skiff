// =============================================================================
// File: internal/search/search.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package search implements project-wide literal content search over a
// pre-built file list (the fuzzy finder's git-aware index). It is pure
// and synchronous — the app layer runs it in a goroutine and posts the
// results back onto the event queue. Matching is smart-case: an
// all-lowercase query matches case-insensitively, any uppercase letter
// makes it exact. Binary files and oversized files are skipped.
package search

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Match is one hit: a project-relative path, a 1-based line number, the
// rune column of the first hit on that line, and the line's text
// (capped — see maxLineRunes).
type Match struct {
	Path string
	Line int
	Col  int
	Text string
}

// Options caps the sweep so a short query over a big repo can't stall
// the app or flood the UI.
type Options struct {
	MaxTotal    int // stop after this many matches overall
	MaxPerFile  int // stop after this many matches in one file
	MaxFileSize int // skip files larger than this many bytes
	// Cancelled, when set, is polled between files: a true return
	// abandons the sweep immediately. The app wires it to "a newer
	// query generation exists", so keystrokes stop paying for their
	// predecessors' disk walks.
	Cancelled func() bool

	// MatchCase forces exact-case matching (default is smart-case:
	// exact only when the query carries an uppercase letter).
	// WholeWord requires non-word characters (or line edges) around
	// the hit. Regex treats the query as a Go regexp instead of a
	// literal; a bad pattern simply matches nothing.
	MatchCase bool
	WholeWord bool
	Regex     bool
}

// DefaultOptions returns the caps the app uses: 500 total, 50 per file,
// 1 MiB per file.
func DefaultOptions() Options {
	return Options{MaxTotal: 500, MaxPerFile: 50, MaxFileSize: 1 << 20}
}

// maxLineRunes caps stored line text so a minified one-liner doesn't
// drag a megabyte into the results UI.
const maxLineRunes = 400

// binarySniffLen is how many leading bytes are checked for a NUL to
// classify a file as binary.
const binarySniffLen = 8192

// Search scans files (project-relative, resolved against rootDir) for
// query and returns matches in file order plus a truncated flag that is
// true when any cap cut the sweep short. An empty or whitespace-only
// query returns nothing.
func Search(rootDir string, files []string, query string, opts Options) ([]Match, bool) {
	if strings.TrimSpace(query) == "" {
		return nil, false
	}
	caseSensitive := opts.MatchCase || hasUpper(query)
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(query)
	}
	var re *regexp.Regexp
	if opts.Regex {
		pat := query
		if !caseSensitive {
			pat = "(?i)" + pat
		}
		var err error
		re, err = regexp.Compile(pat)
		if err != nil {
			return nil, false
		}
	}

	var out []Match
	truncated := false
	for _, rel := range files {
		if opts.Cancelled != nil && opts.Cancelled() {
			return nil, false
		}
		if opts.MaxTotal > 0 && len(out) >= opts.MaxTotal {
			truncated = true
			break
		}
		matches, fileTrunc := searchFile(filepath.Join(rootDir, rel), rel, needle, caseSensitive, re, opts)
		if fileTrunc {
			truncated = true
		}
		if opts.MaxTotal > 0 && len(out)+len(matches) > opts.MaxTotal {
			matches = matches[:opts.MaxTotal-len(out)]
			truncated = true
		}
		out = append(out, matches...)
	}
	return out, truncated
}

// searchFile scans one file for needle. Missing, oversized, and binary
// files are silently skipped — the index can be slightly stale and the
// sweep must shrug that off.
func searchFile(path, rel, needle string, caseSensitive bool, re *regexp.Regexp, opts Options) ([]Match, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, false
	}
	if opts.MaxFileSize > 0 && info.Size() > int64(opts.MaxFileSize) {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	sniff := data
	if len(sniff) > binarySniffLen {
		sniff = sniff[:binarySniffLen]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return nil, false
	}

	var out []Match
	truncated := false
	lineNo := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		lineNo++
		byteCol, byteLen := lineMatch(line, needle, caseSensitive, re, opts.WholeWord)
		if byteCol < 0 {
			continue
		}
		_ = byteLen
		if opts.MaxPerFile > 0 && len(out) >= opts.MaxPerFile {
			truncated = true
			break
		}
		out = append(out, Match{
			Path: rel,
			Line: lineNo,
			Col:  len([]rune(line[:byteCol])),
			Text: capRunes(line, maxLineRunes),
		})
	}
	return out, truncated
}

// lineMatch finds the first qualifying hit on line, returning its byte
// offset and length (-1, 0 when none). Regex and whole-word are layered
// here so the sweep loop stays a single call.
func lineMatch(line, needle string, caseSensitive bool, re *regexp.Regexp, wholeWord bool) (int, int) {
	from := 0
	for from <= len(line) {
		var idx, length int
		if re != nil {
			loc := re.FindStringIndex(line[from:])
			if loc == nil {
				return -1, 0
			}
			idx, length = from+loc[0], loc[1]-loc[0]
		} else {
			hay := line[from:]
			if !caseSensitive {
				hay = strings.ToLower(hay)
			}
			i := strings.Index(hay, needle)
			if i < 0 {
				return -1, 0
			}
			idx, length = from+i, len(needle)
		}
		if !wholeWord || isWordBounded(line, idx, length) {
			return idx, length
		}
		next := idx + 1
		if length == 0 {
			next = idx + 2 // zero-width regex hit — force progress
		}
		from = next
	}
	return -1, 0
}

// isWordBounded reports whether line[idx:idx+length] has non-word
// characters (or the line's edges) on both sides.
func isWordBounded(line string, idx, length int) bool {
	wordish := func(b byte) bool {
		return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	if idx > 0 && wordish(line[idx-1]) {
		return false
	}
	end := idx + length
	if end < len(line) && wordish(line[end]) {
		return false
	}
	return true
}

// hasUpper reports whether s contains an uppercase letter — the
// smart-case trigger.
func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// capRunes trims s to at most n runes.
func capRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
