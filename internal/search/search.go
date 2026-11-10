// =============================================================================
// File: internal/search/search.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnlam90/skiff/internal/atomicfile"
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

// searchFile scans one file and records every qualifying occurrence of
// needle — not just the first hit per line. ReplaceLine rewrites every
// occurrence on a matched line, so the sweep has to enumerate them the
// same way or the panel under-reports what Replace-All will actually
// touch (a line with the query twice showed one row and "replaced" two).
// Missing, oversized, and binary files are silently skipped — the index
// can be slightly stale and the sweep must shrug that off.
func searchFile(path, rel, needle string, caseSensitive bool, re *regexp.Regexp, opts Options) ([]Match, bool) {
	// Lstat, not Stat: a tracked symlink is listed by `git ls-files`, and
	// following it would show (and let Replace rewrite) a file that can
	// live outside the project under an in-project name. The link's
	// target, when it is inside the project, is indexed on its own.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
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
lines:
	for raw := range strings.SplitSeq(string(data), "\n") {
		lineNo++
		// The terminator is not part of the line: a CRLF file's "\r" would
		// otherwise hide `;$` from a regex, and would make every recorded
		// Text differ from the same line in an open buffer (which never
		// carries one), so replace skipped every hit as "changed".
		line, _ := splitCR(raw)
		// lineMatches is the same enumeration ReplaceLine rewrites from,
		// so the sweep and the rewrite agree on where occurrences start
		// by construction.
		for _, loc := range lineMatches(line, matchHaystack(line, caseSensitive, re), needle, re, opts.WholeWord) {
			if opts.MaxPerFile > 0 && len(out) >= opts.MaxPerFile {
				truncated = true
				break lines
			}
			out = append(out, Match{
				Path: rel,
				Line: lineNo,
				Col:  len([]rune(line[:loc[0]])),
				Text: capRunes(line, maxLineRunes),
			})
		}
	}
	return out, truncated
}

// splitCR separates one trailing carriage return from a line that was
// split on "\n", returning the text and the terminator remainder ("\r" or
// ""). Matching and rewriting work on the text; the remainder is glued
// back on when a file is rejoined, so a CRLF file stays CRLF.
func splitCR(raw string) (line, cr string) {
	if strings.HasSuffix(raw, "\r") {
		return raw[:len(raw)-1], "\r"
	}
	return raw, ""
}

// matchHaystack returns the string literal probes run against: the line
// itself in case-sensitive or regex mode, its offset-preserving fold
// otherwise. Callers that probe one line repeatedly (ReplaceLine's scan)
// build it once and pass it down — folding inside the probe made a long
// line quadratic, since each probe lowercased the whole remaining tail.
func matchHaystack(line string, caseSensitive bool, re *regexp.Regexp) string {
	if caseSensitive || re != nil {
		return line
	}
	return foldLine(line)
}

// foldLine returns a lowercase copy of line whose byte offsets still name
// the same positions as the original. Runes whose lowercase form encodes
// to a different width (the Kelvin sign, dotted capital I, a handful of
// others) and invalid UTF-8 bytes are deliberately left alone: the
// offsets this feeds are used to slice the ORIGINAL line, so folding
// those would slide every later offset and cut a replacement mid-rune.
// Every line without one of them — which is very nearly all of them —
// folds exactly the way strings.ToLower would.
func foldLine(line string) string {
	var out []byte
	for i, r := range line {
		if r == utf8.RuneError {
			// Either a real U+FFFD, which folds to itself, or an invalid
			// byte, which strings.ToLower would widen to three.
			continue
		}
		lower := unicode.ToLower(r)
		if lower == r || utf8.RuneLen(lower) != utf8.RuneLen(r) {
			continue
		}
		if out == nil {
			out = []byte(line)
		}
		utf8.EncodeRune(out[i:], lower)
	}
	if out == nil {
		return line
	}
	return string(out)
}

// lineMatches enumerates every qualifying occurrence on line, left to
// right and non-overlapping, as index slices: loc[0]:loc[1] is the match
// and, in regex mode, the rest of loc are the capture groups (the shape
// regexp.Expand wants). hay is the haystack literal probes search (see
// matchHaystack); it is byte-aligned with line.
//
// The regex is always run against the WHOLE line, once. Probing suffixes
// (line[from:]) instead made every resume point look like a line start
// and a word boundary: `^\t` stripped all of a line's indentation, and
// `\bfoo` matched the second half of "foofoo".
func lineMatches(line, hay, needle string, re *regexp.Regexp, wholeWord bool) [][]int {
	if re != nil {
		all := re.FindAllStringSubmatchIndex(line, -1)
		if !wholeWord {
			return all
		}
		kept := all[:0]
		for _, loc := range all {
			if isWordBounded(line, loc[0], loc[1]-loc[0]) {
				kept = append(kept, loc)
			}
		}
		return kept
	}
	if needle == "" {
		return nil
	}
	var out [][]int
	for from := 0; from <= len(line); {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		idx := from + i
		if wholeWord && !isWordBounded(line, idx, len(needle)) {
			from = idx + 1
			continue
		}
		out = append(out, []int{idx, idx + len(needle)})
		from = idx + len(needle)
	}
	return out
}

// writeReplacement appends what one match is replaced with: repl
// verbatim in literal mode, repl expanded against the match's capture
// groups in regex mode.
func writeReplacement(b *strings.Builder, line, repl string, re *regexp.Regexp, loc []int) {
	if re == nil {
		b.WriteString(repl)
		return
	}
	b.Write(re.ExpandString(nil, repl, line, loc))
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

// ReplaceReport summarises an ApplyReplace run.
type ReplaceReport struct {
	Replaced int // occurrences rewritten
	Files    int // files touched
	Skipped  int // matches skipped (file or line changed since the sweep)
}

// CompileQuery resolves the query into the matcher pieces ReplaceLine
// needs, honouring the same smart-case / regex rules as Search. ok is
// false for an invalid regex.
func CompileQuery(query string, opts Options) (needle string, caseSensitive bool, re *regexp.Regexp, ok bool) {
	caseSensitive = opts.MatchCase || hasUpper(query)
	needle = query
	if !caseSensitive {
		needle = strings.ToLower(query)
	}
	if opts.Regex {
		pat := query
		if !caseSensitive {
			pat = "(?i)" + pat
		}
		var err error
		re, err = regexp.Compile(pat)
		if err != nil {
			return "", false, nil, false
		}
	}
	return needle, caseSensitive, re, true
}

// ReplaceLine rewrites every qualifying occurrence of query on line
// with repl and returns the new line plus the occurrence count. In
// regex mode repl is an expansion template with Go's ReplaceAllString
// semantics — $1 / ${name} insert capture groups, $$ is a literal
// dollar. Literal mode writes repl verbatim. Scanning walks left to
// right over the ORIGINAL text, so a replacement can never re-match
// its own output.
func ReplaceLine(line, query, repl string, opts Options) (string, int) {
	needle, caseSensitive, re, ok := CompileQuery(query, opts)
	if !ok || query == "" {
		return line, 0
	}
	locs := lineMatches(line, matchHaystack(line, caseSensitive, re), needle, re, opts.WholeWord)
	if len(locs) == 0 {
		return line, 0
	}
	var b strings.Builder
	from := 0
	for _, loc := range locs {
		b.WriteString(line[from:loc[0]])
		writeReplacement(&b, line, repl, re, loc)
		from = loc[1]
	}
	b.WriteString(line[from:])
	return b.String(), len(locs)
}

// ReplaceLineAt rewrites only the occurrence that starts at rune column
// col on line, returning the new line and 1, or the line unchanged and 0
// when no qualifying occurrence starts exactly there. The panel's
// row-apply uses it so one row rewrites one occurrence — its sibling
// rows on the same line stay valid.
func ReplaceLineAt(line string, col int, query, repl string, opts Options) (string, int) {
	needle, caseSensitive, re, ok := CompileQuery(query, opts)
	if !ok || query == "" {
		return line, 0
	}
	for _, loc := range lineMatches(line, matchHaystack(line, caseSensitive, re), needle, re, opts.WholeWord) {
		runeCol := len([]rune(line[:loc[0]]))
		if runeCol > col {
			// Occurrences come left to right, so once past the target
			// column nothing further can start on it.
			break
		}
		if runeCol == col {
			var b strings.Builder
			b.WriteString(line[:loc[0]])
			writeReplacement(&b, line, repl, re, loc)
			b.WriteString(line[loc[1]:])
			return b.String(), 1
		}
	}
	return line, 0
}

// VerifyLine reports whether a live line still matches what the sweep
// recorded — the guard that keeps replace from rewriting a line the
// user (or anything else) edited after the search. Compared through
// the same cap the sweep stored.
func VerifyLine(actual, recorded string) bool {
	return capRunes(actual, maxLineRunes) == recorded
}

// readRegular reads path only when it is a regular file. A symlink is
// refused rather than followed: the sweep never reports matches through
// one (see searchFile), and a rewrite through one could land outside the
// project.
func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	return os.ReadFile(path)
}

// ApplyReplace rewrites matches on disk: per file, verify each matched
// line still reads as recorded, rewrite the survivors, and write the
// file back via atomicfile.Replace — mode-preserving, so a 0755 script
// stays executable. Symlinks are never rewritten through (readRegular),
// and a CRLF line keeps its "\r". Matches whose file or line drifted are
// counted as skipped, never guessed at. The caller is expected to have
// routed open-buffer files elsewhere — this function only ever sees
// paths whose truth lives on disk.
func ApplyReplace(rootDir string, matches []Match, query, repl string, opts Options) ReplaceReport {
	var rep ReplaceReport
	byFile := map[string][]Match{}
	order := []string{}
	for _, m := range matches {
		if _, seen := byFile[m.Path]; !seen {
			order = append(order, m.Path)
		}
		byFile[m.Path] = append(byFile[m.Path], m)
	}
	for _, rel := range order {
		group := byFile[rel]
		abs := filepath.Join(rootDir, rel)
		data, err := readRegular(abs)
		if err != nil {
			rep.Skipped += len(group)
			continue
		}
		lines := strings.Split(string(data), "\n")

		// Matches are per-occurrence now, but ReplaceLine rewrites a
		// WHOLE line in one pass — group by line and touch each one
		// once, or a second same-line match would verify against the
		// line ReplaceLine already rewrote (an always-false compare
		// against stale Text) and get double-counted as skipped.
		byLine := map[int][]Match{}
		var lineOrder []int
		for _, m := range group {
			if _, seen := byLine[m.Line]; !seen {
				lineOrder = append(lineOrder, m.Line)
			}
			byLine[m.Line] = append(byLine[m.Line], m)
		}

		changedLines, fileOcc := 0, 0
		for _, ln := range lineOrder {
			ms := byLine[ln]
			i := ln - 1
			// All matches recorded for one line share the same recorded
			// Text (the sweep captured the whole line once per hit), so
			// verifying against the first is verifying them all.
			if i < 0 || i >= len(lines) {
				rep.Skipped += len(ms)
				continue
			}
			body, cr := splitCR(lines[i])
			if !VerifyLine(body, ms[0].Text) {
				rep.Skipped += len(ms)
				continue
			}
			newLine, n := ReplaceLine(body, query, repl, opts)
			if n == 0 {
				rep.Skipped += len(ms)
				continue
			}
			lines[i] = newLine + cr
			fileOcc += n
			changedLines++
		}
		if changedLines == 0 {
			continue
		}
		if err := atomicfile.Replace(abs, []byte(strings.Join(lines, "\n"))); err != nil {
			rep.Skipped += fileOcc
			continue
		}
		rep.Replaced += fileOcc
		rep.Files++
	}
	return rep
}

// ApplyReplaceAt is ApplyReplace narrowed to exactly one occurrence: it
// verifies the recorded line once, rewrites only the occurrence at
// m.Col via ReplaceLineAt, and writes the file back atomically. The
// panel's single-row apply on a closed file goes through this instead of
// ApplyReplace, so applying one row can never sweep up a sibling
// occurrence recorded on the same line.
func ApplyReplaceAt(rootDir string, m Match, query, repl string, opts Options) ReplaceReport {
	var rep ReplaceReport
	abs := filepath.Join(rootDir, m.Path)
	data, err := readRegular(abs)
	if err != nil {
		rep.Skipped++
		return rep
	}
	lines := strings.Split(string(data), "\n")
	i := m.Line - 1
	if i < 0 || i >= len(lines) {
		rep.Skipped++
		return rep
	}
	body, cr := splitCR(lines[i])
	if !VerifyLine(body, m.Text) {
		rep.Skipped++
		return rep
	}
	newLine, n := ReplaceLineAt(body, m.Col, query, repl, opts)
	if n == 0 {
		rep.Skipped++
		return rep
	}
	lines[i] = newLine + cr
	if err := atomicfile.Replace(abs, []byte(strings.Join(lines, "\n"))); err != nil {
		rep.Skipped++
		return rep
	}
	rep.Replaced = n
	rep.Files = 1
	return rep
}
