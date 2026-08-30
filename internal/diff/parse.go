// =============================================================================
// File: internal/diff/parse.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// parse.go is the git-text constructor: unified diff bytes in, Patch
// out. It is the only place in skiff that knows git's framing — the
// `diff --git` boundary, the `---`/`+++` operands with their /dev/null
// and C-quoted forms, `\ No newline at end of file`, binary notes, and
// hunk headers with the length elided. Everything downstream reads the
// model instead, which is why the gutter and the diff view can no longer
// disagree about what a hunk is.

package diff

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Parse reads a unified diff into the model. It is best-effort by
// design: every git-aware surface in skiff would rather show a partial
// diff than nothing, so a header it cannot make sense of ends the hunk
// and the walk continues. The returned error names the first such
// header — the Patch still carries everything that did parse, so a
// caller may ignore it.
func Parse(unified []byte) (Patch, error) {
	var (
		p        Patch
		cur      File
		hunk     Hunk
		open     bool // cur holds a file worth emitting
		inHunk   bool
		seenHunk bool // this file's header block is over
		oldNo    int
		newNo    int
		firstEr  error
	)

	flushHunk := func() {
		if inHunk {
			cur.Hunks = append(cur.Hunks, hunk)
			hunk, inHunk = Hunk{}, false
		}
	}
	flushFile := func() {
		flushHunk()
		if open {
			p.Files = append(p.Files, cur)
		}
		cur, open, seenHunk = File{}, false, false
	}

	for _, text := range splitLines(unified) {
		switch {
		case strings.HasPrefix(text, "diff --git "):
			flushFile()
			cur.OldPath, cur.NewPath = gitHeaderPaths(text)
			open = true

		case strings.HasPrefix(text, "@@ "):
			// Body lines always carry a +/-/space/backslash prefix, so
			// an unprefixed "@@ " at the start of a line can only be a
			// header — even in the middle of a hunk, where it opens the
			// next one.
			flushHunk()
			h, ok := parseHunkHeader(text)
			if !ok {
				if firstEr == nil {
					firstEr = fmt.Errorf("diff: unparsable hunk header %q", text)
				}
				continue
			}
			hunk, inHunk, open, seenHunk = h, true, true, true
			oldNo, newNo = h.OldStart, h.NewStart

		case inHunk:
			switch {
			case strings.HasPrefix(text, "-"):
				hunk.Lines = append(hunk.Lines, Line{Kind: Del, OldNo: oldNo, Text: text[1:]})
				oldNo++
			case strings.HasPrefix(text, "+"):
				hunk.Lines = append(hunk.Lines, Line{Kind: Add, NewNo: newNo, Text: text[1:]})
				newNo++
			case strings.HasPrefix(text, `\`):
				// "\ No newline at end of file" describes the line above
				// it, so it is a flag rather than a line of its own.
				if n := len(hunk.Lines); n > 0 {
					hunk.Lines[n-1].NoNewline = true
				}
			default:
				hunk.Lines = append(hunk.Lines, Line{
					Kind:  Context,
					OldNo: oldNo,
					NewNo: newNo,
					Text:  strings.TrimPrefix(text, " "),
				})
				oldNo++
				newNo++
			}

		case !seenHunk:
			// The header block above the first hunk. Only these lines may
			// be trusted as headers: inside a hunk an added line reading
			// "++ x" renders as "+++ x" and would otherwise re-point the
			// whole section at a file that does not exist.
			applyFileHeader(&cur, &open, text)
		}
	}
	flushFile()
	return p, firstEr
}

// applyFileHeader folds one pre-hunk header line into the file being
// built. It is split out so the walk above stays a single switch on
// "where am I", and so the /dev/null and quoting rules live next to each
// other rather than being rediscovered per caller.
func applyFileHeader(cur *File, open *bool, text string) {
	switch {
	case strings.HasPrefix(text, "--- "):
		path, devNull := headerPath(text[4:], "a/")
		switch {
		case devNull:
			cur.OldPath, cur.IsNew = "", true
		case path != "":
			cur.OldPath = path
		}
		*open = true
	case strings.HasPrefix(text, "+++ "):
		path, devNull := headerPath(text[4:], "b/")
		switch {
		case devNull:
			cur.NewPath, cur.IsDeleted = "", true
		case path != "":
			cur.NewPath = path
		}
		*open = true
	case strings.HasPrefix(text, "new file mode "):
		cur.IsNew = true
	case strings.HasPrefix(text, "deleted file mode "):
		cur.IsDeleted = true
	case strings.HasPrefix(text, "Binary files ") || strings.HasPrefix(text, "GIT binary patch"):
		cur.Binary = true
		*open = true
	}
}

// splitLines cuts diff bytes into lines without inventing a trailing
// empty one. Git always ends its output with a newline, and a phantom
// final line would land in the last hunk as an empty context row.
func splitLines(out []byte) []string {
	out = bytes.TrimSuffix(out, []byte{'\n'})
	if len(out) == 0 {
		return nil
	}
	parts := bytes.Split(out, []byte{'\n'})
	lines := make([]string, len(parts))
	for i, p := range parts {
		lines[i] = string(p)
	}
	return lines
}

// gitHeaderPaths splits a `diff --git a/X b/Y` boundary into its two
// operands. It is only a first guess — the ---/+++ lines below it are
// authoritative and overwrite both — so a shape that surprises us falls
// back to handing the whole remainder over as the new path rather than
// leaving the file nameless.
func gitHeaderPaths(line string) (old, new string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	idx := strings.LastIndex(rest, " b/")
	if idx < 0 {
		return "", rest
	}
	return strings.TrimPrefix(rest[:idx], "a/"), rest[idx+len(" b/"):]
}

// headerPath turns one `---`/`+++` operand into a repo-relative path:
// unquote git's C-style form when present, report /dev/null separately
// (it is a fact about the file, not a failure), and strip the expected
// a// b/ prefix — anything else fails closed to "" so a section keeps
// whatever name it already had rather than being mis-attributed. An
// UNQUOTED name containing blanks arrives with one trailing TAB (git's
// GNU-patch compatibility marker for "the name ends here"); the quoted
// form never carries it, and a real tab inside a name forces quoting, so
// stripping one from the unquoted form is unambiguous.
func headerPath(raw, prefix string) (path string, devNull bool) {
	if strings.HasPrefix(raw, `"`) {
		raw = unquoteGitPath(raw)
		if raw == "" {
			return "", false
		}
	} else {
		raw = strings.TrimSuffix(raw, "\t")
	}
	if raw == os.DevNull || raw == "/dev/null" {
		return "", true
	}
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	return raw[len(prefix):], false
}

// unquoteGitPath decodes the C-style quoting git applies to header
// paths containing control or non-ASCII bytes (quote.c: \a \b \f \n
// \r \t \v, \\, \", and three-digit octal). Returns "" for anything
// malformed — failing closed beats guessing at a path. Content after
// the closing quote is ignored; git writes none.
func unquoteGitPath(q string) string {
	if len(q) < 2 || q[0] != '"' {
		return ""
	}
	var b strings.Builder
	for i := 1; i < len(q); i++ {
		c := q[i]
		if c == '"' {
			return b.String()
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(q) {
			return ""
		}
		switch e := q[i]; e {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '\\', '"':
			b.WriteByte(e)
		default:
			if e < '0' || e > '7' || i+2 >= len(q) ||
				q[i+1] < '0' || q[i+1] > '7' || q[i+2] < '0' || q[i+2] > '7' {
				return ""
			}
			b.WriteByte((e-'0')<<6 | (q[i+1]-'0')<<3 | (q[i+2] - '0'))
			i += 2
		}
	}
	return "" // unterminated quote
}

// parseHunkHeader reads an `@@ -a,b +c,d @@ section` line into an empty
// hunk. The section suffix is kept because git puts the enclosing
// function there, which is often the fastest way to see where in a file
// a hunk landed.
func parseHunkHeader(line string) (Hunk, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return Hunk{}, false
	}
	oldStart, oldLen, ok := parseDiffRange(fields[1])
	if !ok {
		return Hunk{}, false
	}
	newStart, newLen, ok := parseDiffRange(fields[2])
	if !ok {
		return Hunk{}, false
	}
	return Hunk{
		OldStart: oldStart,
		OldLen:   oldLen,
		NewStart: newStart,
		NewLen:   newLen,
		Section:  hunkSection(line),
	}, true
}

// hunkSection extracts whatever git wrote after the closing `@@`. The
// scan starts past the opening marker so the closing one is the first
// hit, and a header without one simply has no section.
func hunkSection(line string) string {
	rest := line[2:]
	j := strings.Index(rest, "@@")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[j+2:])
}

// parseDiffRange parses one side of a hunk header, such as -1,2 or +7.
// A missing length means 1 — git elides it, and a reader that defaulted
// to 0 would silently drop every single-line hunk.
func parseDiffRange(s string) (start, count int, ok bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	parts := strings.SplitN(s[1:], ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}
