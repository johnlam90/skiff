// =============================================================================
// File: internal/droppath/droppath.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package droppath decides whether a pasted text is a file drop.
//
// Rex, Herdr and Ghostty do not tell a program that a file was dropped
// on it: they paste the file's path, and that paste is byte-identical
// to the user pasting the same path as text. So the only evidence is
// the text itself — a paste that is nothing but paths, every one of
// which exists on this machine. Classify answers that question and
// nothing else: it never copies, never prompts, and is pure apart from
// the Stat function its caller hands it.
//
// The verdict is only ever an offer. The app shows the placement bar
// and lets the user decide, because a wrong "this is a drop" here would
// turn pasting /etc/hosts into a script into a file copy. See
// docs/adr/0003-a-pasted-path-is-a-drop-only-once-confirmed.md.
package droppath

import (
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
)

// MaxBytes caps the paste Classify will look at. Past it the text is
// text: a real drop is a handful of paths, and classification runs on
// the event loop.
const MaxBytes = 64 << 10

// MaxPaths caps how many entries one drop may name — the same ceiling
// Rex puts on a batch, and a bound on the stats one paste can cost.
const MaxPaths = 256

// Kind is what a pasted text turned out to be.
type Kind uint8

const (
	// Text is anything that is not provably a file drop. It is the
	// default and the safe answer: the paste is inserted as before.
	Text Kind = iota
	// Paths is a paste made only of absolute paths that all exist here.
	Paths
	// Foreign is a drop from another machine that the terminal pasted
	// without uploading: desktop-style paths, none of which exists
	// here. Only reported in a remote session.
	Foreign
)

// String names a Kind for test failures and logs.
func (k Kind) String() string {
	switch k {
	case Paths:
		return "Paths"
	case Foreign:
		return "Foreign"
	default:
		return "Text"
	}
}

// Options is what Classify cannot know from the text alone.
type Options struct {
	// Stat reports whether a path exists: nil, or an error wrapping
	// fs.ErrPermission, means it does. Injected so classification is
	// testable without a disk.
	Stat func(path string) error
	// Windows makes backslash a separator instead of an escape, and
	// "C:\x" an absolute path.
	Windows bool
	// Remote says this is an SSH session, which is the only place the
	// Foreign verdict can be true.
	Remote bool
}

// desktopRoots are the top-level directories a file dragged off a
// desktop lives under. The Foreign verdict requires one of them so a
// web route pasted over SSH ("/api/v1/users") stays text.
var desktopRoots = []string{
	"/Users/", "/Volumes/", "/home/", "/private/var/",
	"/var/folders/", "/mnt/", "/media/", "/run/media/",
}

// readingStatus is how one way of splitting the text into paths came
// out.
type readingStatus uint8

const (
	readingInvalid readingStatus = iota // not a list of absolute paths
	readingOK                           // a list of absolute paths
	readingTooMany                      // absolute paths, but past MaxPaths
)

// Classify reports whether text is a file drop and, when it is, the
// cleaned absolute paths it names.
//
// The text is read three ways, most specific first: as shell words
// (what Ghostty, Rex and Terminal.app paste), as one raw path per line
// (a text/uri-list, or a terminal that pastes unescaped), and as a
// single raw path. The first reading whose paths are all absolute and
// all exist wins. A reading that is a valid list but longer than
// MaxPaths ends the search: the later readings exist to rescue
// unescaped spaces, not to reinterpret a list that was understood.
func Classify(text string, o Options) (Kind, []string) {
	if len(text) == 0 || len(text) > MaxBytes || strings.IndexByte(text, 0) >= 0 {
		return Text, nil
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Text, nil
	}

	tokens, tokensOK := shellWords(trimmed, o.Windows)
	var tokenPaths []string
	tokenStatus := readingInvalid
	if tokensOK {
		tokenPaths, tokenStatus = resolve(tokens, o.Windows)
	}
	if tokenStatus == readingTooMany {
		return Text, nil
	}
	if tokenStatus == readingOK && allExist(tokenPaths, o.Stat) {
		return Paths, tokenPaths
	}

	for _, elems := range [][]string{pathLines(trimmed), {trimmed}} {
		paths, status := resolve(elems, o.Windows)
		if status == readingTooMany {
			return Text, nil
		}
		if status == readingOK && allExist(paths, o.Stat) {
			return Paths, paths
		}
	}

	if o.Remote && tokenStatus == readingOK && allForeign(tokenPaths, o.Stat) {
		return Foreign, tokenPaths
	}
	return Text, nil
}

// shellWords splits s the way a POSIX shell would split a command line
// into words: blanks separate, backslash escapes the next byte, single
// quotes are literal, and double quotes group with backslash escaping
// only `"`, `\`, `$` and the backtick. ok is false on an unterminated
// quote. With windows set, backslash and the single quote are ordinary
// bytes — both are legal in a Windows path — and only double quotes
// group.
func shellWords(s string, windows bool) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\\' && !windows:
			inWord = true
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte('\\')
			}
		case c == '\'' && !windows:
			inWord = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			inWord = true
			closed := false
			for i++; i < len(s); i++ {
				if s[i] == '"' {
					closed = true
					break
				}
				if s[i] == '\\' && !windows && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if !closed {
				return nil, false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

// pathLines splits s into its non-blank lines, dropping the "#" comment
// lines a text/uri-list may carry. Each line is one raw, unescaped
// path or URI.
func pathLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// resolve turns the elements of one reading into cleaned, de-duplicated
// absolute paths. The reading is invalid as soon as one element is not
// an absolute path (or a file:// URI for this machine), and too-many
// when it is a valid list longer than MaxPaths.
func resolve(elems []string, windows bool) ([]string, readingStatus) {
	if len(elems) == 0 {
		return nil, readingInvalid
	}
	seen := make(map[string]bool, len(elems))
	var out []string
	for _, e := range elems {
		p := e
		if strings.HasPrefix(p, "file://") {
			var ok bool
			if p, ok = fromFileURI(p, windows); !ok {
				return nil, readingInvalid
			}
		}
		if !isAbs(p, windows) {
			return nil, readingInvalid
		}
		p = filepath.Clean(p)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) > MaxPaths {
		return nil, readingTooMany
	}
	return out, readingOK
}

// fromFileURI returns the local path a file:// URI names. A URI for
// another host is refused: its path means nothing on this machine.
func fromFileURI(s string, windows bool) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", false
	}
	p := u.Path
	// file:///C:/x parses to the path "/C:/x".
	if windows && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return p, true
}

// isAbs reports whether p is an absolute path. filepath.IsAbs answers
// for the host OS only, and the Windows rule has to be testable on the
// platforms CI runs, so both rules are spelled out here.
func isAbs(p string, windows bool) bool {
	if !windows {
		return strings.HasPrefix(p, "/")
	}
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// exists reads a Stat result: present, or present-but-unreadable. A
// permission error counts because macOS answers it for files in
// privacy-protected folders — exactly the ones people drag out of
// Downloads — and calling those "missing" would type the path into the
// buffer.
func exists(stat func(string) error, p string) bool {
	if stat == nil {
		return false
	}
	err := stat(p)
	return err == nil || errors.Is(err, fs.ErrPermission)
}

// allExist reports whether every path exists.
func allExist(paths []string, stat func(string) error) bool {
	for _, p := range paths {
		if !exists(stat, p) {
			return false
		}
	}
	return len(paths) > 0
}

// allForeign reports whether paths look like a drop from another
// machine: every one under a desktop root and none of them here. One
// path that does exist means the paste is about this machine, and the
// plain "not all exist" answer — text — already covers it.
func allForeign(paths []string, stat func(string) error) bool {
	for _, p := range paths {
		if !underDesktopRoot(p) || exists(stat, p) {
			return false
		}
	}
	return len(paths) > 0
}

// underDesktopRoot reports whether p sits under one of desktopRoots.
func underDesktopRoot(p string) bool {
	for _, root := range desktopRoots {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}
