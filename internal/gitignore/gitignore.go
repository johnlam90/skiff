// =============================================================================
// File: internal/gitignore/gitignore.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package gitignore is the one definition of "does this .gitignore
// exclude this path": a parser for one ignore file and a matcher that
// answers the way git's own dir.c + wildmatch.c do. It has no tcell,
// theme, filetree or git-process imports — the sidebar's HideIgnored
// filter and the finder's non-git walk both compile through it, so the
// two cannot disagree with each other, and a differential test against
// `git check-ignore` keeps both from disagreeing with git.
//
// It replaced github.com/sabhiram/go-gitignore, which translated each
// pattern into a Go regexp and got the translation wrong in both
// directions: `src/gen` hid `a/src/gen` (only a LEADING slash anchored),
// `a+b` hid `aab` (regexp metacharacters were not escaped), a lone `/`
// hid the whole tree, while `a?c` and `[!ab].txt` did not match what git
// matches. A hand-port of git's matcher is smaller than the set of
// pre-translations that would have been needed to paper over those.
//
// Scope, stated plainly: a Matcher is ONE ignore file. Which files apply
// to a path, in what order, and the rule that nothing under an excluded
// directory can be re-included all belong to the caller (see
// filetree/ignore.go). Matching is byte-wise and case-sensitive, as git
// is on a case-sensitive filesystem; core.ignorecase is git
// configuration this package does not read.
package gitignore

import "strings"

// Verdict is what one ignore file says about one path. Three states, not
// a bool, because "no pattern here mentions it" and "a ! pattern here
// re-includes it" are different answers: the first lets a shallower
// ignore file decide, the second overrides it.
type Verdict uint8

const (
	// NoMatch means no pattern in the file matched the path.
	NoMatch Verdict = iota
	// Ignored means the last matching pattern excludes the path.
	Ignored
	// Included means the last matching pattern is a "!" negation.
	Included
)

// pattern is one parsed .gitignore line, in the shape git's
// parse_path_pattern leaves it: the glob with its "!" prefix, its one
// trailing "/" and (for an anchored pattern) its leading "/" removed,
// and the flags those carried.
type pattern struct {
	glob string

	// negate is the "!" prefix: a match re-includes instead of excluding.
	negate bool
	// dirOnly is the trailing "/": the pattern matches directories only.
	dirOnly bool
	// baseOnly marks a pattern with no "/" left in it. Git matches such
	// a pattern against the entry's basename at any depth; any other
	// pattern is anchored to the ignore file's directory and matched
	// against the whole relative path.
	baseOnly bool

	// literal and suffix are the two shapes that skip the glob engine —
	// together they cover almost every line of a real .gitignore
	// (`node_modules`, `/dist`, `*.log`), which keeps a tree sweep at a
	// string compare per pattern. literal: the glob has no wildcard or
	// escape, so matching is equality. suffix: a basename pattern that
	// is "*" followed by a literal, so matching is HasSuffix on tail.
	literal bool
	suffix  bool
	tail    string
}

// Matcher is one compiled ignore file. The zero value and nil both match
// nothing. It is immutable after Compile, so a Matcher may be shared
// across goroutines.
type Matcher struct {
	patterns []pattern
}

// Compile parses the bytes of one .gitignore file. It never fails: git
// has no notion of a malformed ignore file, only of lines that match
// nothing, and a pattern that cannot match (an unterminated "[", a bare
// "/") is kept out of the list or simply never hits.
func Compile(raw []byte) *Matcher {
	text := strings.TrimPrefix(string(raw), "\xEF\xBB\xBF") // git skips a UTF-8 BOM
	m := &Matcher{}
	for _, line := range strings.Split(text, "\n") {
		if p, ok := parseLine(line); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return m
}

// CompileLines is Compile for callers that already hold the file split
// into lines (tests, mostly).
func CompileLines(lines ...string) *Matcher {
	return Compile([]byte(strings.Join(lines, "\n")))
}

// parseLine turns one line into a pattern, reporting false for a line
// that contributes nothing: blank, a "#" comment, or a pattern that is
// empty once its markers are stripped (a bare "/" or "!"), which git
// keeps but can never match against a non-empty name.
//
// The order of the steps is git's and is load-bearing: the comment check
// sees the raw line (so "\#x" is a pattern and " #x" is too), trailing
// spaces go before "!" and "/" are read, and only ONE trailing slash is
// consumed.
func parseLine(line string) (pattern, bool) {
	line = strings.TrimSuffix(line, "\r") // git drops the CR of a CRLF file
	if line == "" || line[0] == '#' {
		return pattern{}, false
	}
	line = trimTrailingSpaces(line)

	var p pattern
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = line[:len(line)-1]
	}
	p.baseOnly = !strings.Contains(line, "/")
	if !p.baseOnly {
		// Every pattern with a slash is already anchored; the leading
		// one only exists to anchor a name that has no other.
		line = strings.TrimPrefix(line, "/")
	}
	if line == "" {
		return pattern{}, false
	}
	p.glob = line
	p.literal = !hasGlobSpecial(line)
	if p.baseOnly && line[0] == '*' && !hasGlobSpecial(line[1:]) {
		p.suffix = true
		p.tail = line[1:]
	}
	return p, true
}

// trimTrailingSpaces removes the unescaped spaces at the end of a line,
// exactly as git's function of the same name does: a backslash protects
// the character after it, so "trail\ " keeps its space (the glob engine
// later reads "\ " as a literal space) while "trail  " loses both. Only
// spaces are trimmed — a trailing tab is part of the pattern — and
// leading whitespace is never touched.
func trimTrailingSpaces(s string) string {
	lastSpace := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			if lastSpace < 0 {
				lastSpace = i
			}
		case '\\':
			i++
			if i >= len(s) {
				return s
			}
			lastSpace = -1
		default:
			lastSpace = -1
		}
	}
	if lastSpace >= 0 {
		return s[:lastSpace]
	}
	return s
}

// hasGlobSpecial reports whether s contains a byte the glob engine
// treats specially. A backslash counts: "a\b" must go through the
// engine to have its escape removed.
func hasGlobSpecial(s string) bool {
	return strings.ContainsAny(s, `*?[\`)
}

// Match reports what this ignore file says about rel, a slash-separated
// path relative to the directory the file lives in, with no leading or
// trailing slash. isDir says whether the entry is a directory, which a
// path string alone cannot settle and a trailing-slash pattern needs.
//
// Patterns are consulted last to first and the first hit decides — git's
// "last matching pattern wins". Match looks at rel itself only: whether
// one of rel's parent directories is excluded is the caller's question
// to ask, one Match per ancestor.
func (m *Matcher) Match(rel string, isDir bool) Verdict {
	if m == nil || len(m.patterns) == 0 {
		return NoMatch
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	for i := len(m.patterns) - 1; i >= 0; i-- {
		p := &m.patterns[i]
		if p.dirOnly && !isDir {
			continue
		}
		name := rel
		if p.baseOnly {
			name = base
		}
		if !p.matches(name) {
			continue
		}
		if p.negate {
			return Included
		}
		return Ignored
	}
	return NoMatch
}

// matches tests one name against the pattern's glob, through whichever
// of the three paths parseLine picked for it.
func (p *pattern) matches(name string) bool {
	switch {
	case p.literal:
		return name == p.glob
	case p.suffix:
		return strings.HasSuffix(name, p.tail)
	default:
		return wildmatch(p.glob, name) == wmMatch
	}
}

// The glob engine's results. The two abort codes are git's pruning
// signals, kept because they bound the backtracking on patterns with
// several stars: abortAll means no amount of retrying further along the
// text can help, abortToStarStar means only a "**" (which may cross a
// slash) could still rescue the match.
const (
	wmMatch = iota
	wmNoMatch
	wmAbortAll
	wmAbortToStarStar
)

// wildmatch is a port of git's dowild (wildmatch.c) with WM_PATHNAME
// always set: "*" and "?" and a bracket class never match "/", and "**"
// crosses directories only where it stands as a whole path segment
// ("**/x", "a/**", "a/**/b"); anywhere else it is two ordinary stars.
// It walks bytes, not runes, because git does — "?" is one byte.
//
// Running a slash-free basename pattern through the pathname rules is
// safe: with no "/" on either side the two modes cannot differ.
func wildmatch(p, t string) int {
	pi, ti := 0, 0
	for ; pi < len(p); pi, ti = pi+1, ti+1 {
		pc := p[pi]
		var tc byte
		if ti < len(t) {
			tc = t[ti]
		} else if pc != '*' {
			return wmAbortAll
		}
		switch pc {
		case '\\':
			// The next pattern byte is literal. A trailing lone
			// backslash has nothing to escape and matches nothing.
			pi++
			if pi >= len(p) || tc != p[pi] {
				return wmNoMatch
			}
		case '?':
			if tc == '/' {
				return wmNoMatch
			}
		case '*':
			pi++
			matchSlash := false
			if pi < len(p) && p[pi] == '*' {
				prev := pi - 2 // the byte before the first star
				for pi++; pi < len(p) && p[pi] == '*'; pi++ {
				}
				leftEdge := prev < 0 || p[prev] == '/'
				rightEdge := pi == len(p) || p[pi] == '/' ||
					(p[pi] == '\\' && pi+1 < len(p) && p[pi+1] == '/')
				if leftEdge && rightEdge {
					// "**/" may match nothing at all, which is what
					// lets "a/**/b" match "a/b".
					if pi < len(p) && p[pi] == '/' && wildmatch(p[pi+1:], t[ti:]) == wmMatch {
						return wmMatch
					}
					matchSlash = true
				}
			}
			if pi == len(p) {
				// A trailing "**" swallows everything; a trailing "*"
				// only the rest of this path segment.
				if !matchSlash && strings.IndexByte(t[ti:], '/') >= 0 {
					return wmAbortToStarStar
				}
				return wmMatch
			}
			if !matchSlash && p[pi] == '/' {
				// One star then a slash: skip to the text's next
				// slash; the loop's own step consumes both slashes.
				s := strings.IndexByte(t[ti:], '/')
				if s < 0 {
					return wmAbortAll
				}
				ti += s
				continue
			}
			for ; ti < len(t); ti++ {
				r := wildmatch(p[pi:], t[ti:])
				if r != wmNoMatch {
					if !matchSlash || r != wmAbortToStarStar {
						return r
					}
				} else if !matchSlash && t[ti] == '/' {
					return wmAbortToStarStar
				}
			}
			return wmAbortAll
		case '[':
			matched, next, ok := matchClass(p, pi+1, tc)
			if !ok {
				return wmAbortAll
			}
			if !matched || tc == '/' {
				return wmNoMatch
			}
			pi = next
		default:
			if tc != pc {
				return wmNoMatch
			}
		}
	}
	if ti < len(t) {
		return wmNoMatch
	}
	return wmMatch
}

// matchClass evaluates the bracket expression whose first byte after
// the "[" is p[pi] against the text byte tc. It returns whether tc is
// accepted (negation already applied), the index of the closing "]",
// and ok=false when the expression is malformed — unterminated, or
// naming an unknown [:class:] — which git treats as "the whole pattern
// matches nothing".
//
// The quirks are git's: "!" and "^" both negate; the first member is
// taken literally even when it is "]" (so "[]a]" is a two-member set);
// "-" is a range only between two members; a backslash escapes the next
// byte, including as a range's upper bound.
func matchClass(p string, pi int, tc byte) (matched bool, closeAt int, ok bool) {
	if pi >= len(p) {
		return false, 0, false
	}
	pc := p[pi]
	negated := pc == '!' || pc == '^'
	if negated {
		pi++
		if pi >= len(p) {
			return false, 0, false
		}
		pc = p[pi]
	}
	var prev byte
	for {
		switch {
		case pc == '\\':
			pi++
			if pi >= len(p) {
				return false, 0, false
			}
			pc = p[pi]
			if tc == pc {
				matched = true
			}
		case pc == '-' && prev != 0 && pi+1 < len(p) && p[pi+1] != ']':
			pi++
			pc = p[pi]
			if pc == '\\' {
				pi++
				if pi >= len(p) {
					return false, 0, false
				}
				pc = p[pi]
			}
			if tc >= prev && tc <= pc {
				matched = true
			}
			pc = 0 // a range's end cannot start another range
		case pc == '[' && pi+1 < len(p) && p[pi+1] == ':':
			start := pi + 2
			end := start
			for end < len(p) && p[end] != ']' {
				end++
			}
			if end >= len(p) {
				return false, 0, false
			}
			if end-start-1 < 0 || p[end-1] != ':' {
				// No ":]" — this "[" is an ordinary set member.
				if tc == '[' {
					matched = true
				}
				break
			}
			in, known := posixClass(p[start:end-1], tc)
			if !known {
				return false, 0, false
			}
			if in {
				matched = true
			}
			pi = end
			pc = 0
		case tc == pc:
			matched = true
		}
		prev = pc
		pi++
		if pi >= len(p) {
			return false, 0, false
		}
		pc = p[pi]
		if pc == ']' {
			break
		}
	}
	return matched != negated, pi, true
}

// posixClass reports whether c belongs to the named POSIX character
// class, and whether the name is one git knows. ASCII only, like git's
// own table.
func posixClass(name string, c byte) (in, known bool) {
	lower := c >= 'a' && c <= 'z'
	upper := c >= 'A' && c <= 'Z'
	digit := c >= '0' && c <= '9'
	switch name {
	case "alnum":
		return lower || upper || digit, true
	case "alpha":
		return lower || upper, true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < 32 || c == 127, true
	case "digit":
		return digit, true
	case "graph":
		return c > 32 && c < 127, true
	case "lower":
		return lower, true
	case "print":
		return c >= 32 && c < 127, true
	case "punct":
		return c > 32 && c < 127 && !lower && !upper && !digit, true
	case "space":
		return c == ' ' || (c >= '\t' && c <= '\r'), true
	case "upper":
		return upper, true
	case "xdigit":
		return digit || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'), true
	}
	return false, false
}
