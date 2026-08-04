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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
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
	return lineMatchFrom(line, matchHaystack(line, caseSensitive, re), 0, needle, re, wholeWord)
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

// lineMatchFrom is lineMatch with a starting byte offset — the scan
// loop ReplaceLine walks the line with. hay is the haystack literal
// probes search (see matchHaystack); it is byte-aligned with line, so an
// offset found in one names the same position in the other.
// Word-boundary checks always consult the FULL line, so a suffix scan
// can't mistake a mid-word position for a word start.
func lineMatchFrom(line, hay string, from int, needle string, re *regexp.Regexp, wholeWord bool) (int, int) {
	for from <= len(line) {
		var idx, length int
		if re != nil {
			loc := re.FindStringIndex(line[from:])
			if loc == nil {
				return -1, 0
			}
			idx, length = from+loc[0], loc[1]-loc[0]
		} else {
			i := strings.Index(hay[from:], needle)
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
	var b strings.Builder
	// Folded once for the whole scan below, not once per probe.
	hay := matchHaystack(line, caseSensitive, re)
	from, n := 0, 0
	for {
		idx, length := lineMatchFrom(line, hay, from, needle, re, opts.WholeWord)
		if idx < 0 {
			break
		}
		b.WriteString(line[from:idx])
		if re != nil {
			// Expand capture groups against this match. The submatch
			// scan on the suffix re-finds the same leftmost match
			// lineMatchFrom just located, so loc[0] is always 0.
			if loc := re.FindStringSubmatchIndex(line[idx:]); loc != nil && loc[0] == 0 {
				b.Write(re.Expand(nil, []byte(repl), []byte(line[idx:]), loc))
			} else {
				b.WriteString(repl)
			}
		} else {
			b.WriteString(repl)
		}
		n++
		next := idx + length
		if length == 0 {
			// Zero-width regex hit: emit one original byte to progress.
			if next < len(line) {
				b.WriteByte(line[next])
			}
			next++
		}
		from = next
		if from > len(line) {
			break
		}
	}
	if n == 0 {
		return line, 0
	}
	// A zero-width regex hit at end-of-line pushes `from` one past the
	// last byte (the +1 above forces progress with nothing left to copy),
	// so the tail write has to clamp or it slices out of range.
	if from > len(line) {
		from = len(line)
	}
	b.WriteString(line[from:])
	return b.String(), n
}

// VerifyLine reports whether a live line still matches what the sweep
// recorded — the guard that keeps replace from rewriting a line the
// user (or anything else) edited after the search. Compared through
// the same cap the sweep stored.
func VerifyLine(actual, recorded string) bool {
	return capRunes(actual, maxLineRunes) == recorded
}

// ApplyReplace rewrites matches on disk: per file, verify each matched
// line still reads as recorded, rewrite the survivors, and write the
// file back atomically (temp + rename). Matches whose file or line
// drifted are counted as skipped, never guessed at. The caller is
// expected to have routed open-buffer files elsewhere — this function
// only ever sees paths whose truth lives on disk.
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
		data, err := os.ReadFile(abs)
		if err != nil {
			rep.Skipped += len(group)
			continue
		}
		mode := os.FileMode(0644)
		if fi, statErr := os.Stat(abs); statErr == nil {
			mode = fi.Mode().Perm()
		}
		lines := strings.Split(string(data), "\n")
		changedLines, fileOcc := 0, 0
		for _, m := range group {
			i := m.Line - 1
			if i < 0 || i >= len(lines) || !VerifyLine(lines[i], m.Text) {
				rep.Skipped++
				continue
			}
			newLine, n := ReplaceLine(lines[i], query, repl, opts)
			if n == 0 {
				rep.Skipped++
				continue
			}
			lines[i] = newLine
			fileOcc += n
			changedLines++
		}
		if changedLines == 0 {
			continue
		}
		tmp := abs + ".skiff-replace"
		if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), mode); err != nil {
			rep.Skipped += changedLines
			continue
		}
		if err := os.Rename(tmp, abs); err != nil {
			_ = os.Remove(tmp)
			rep.Skipped += changedLines
			continue
		}
		rep.Replaced += fileOcc
		rep.Files++
	}
	return rep
}
