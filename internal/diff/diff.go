// =============================================================================
// File: internal/diff/diff.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package diff is skiff's one model of a unified diff.
//
// Two producers build it and every consumer reads the same shape: Parse
// turns `git diff` output into it, and Lines turns two in-memory line
// slices (a buffer against what is on disk) into it without ever
// rendering text. Before this package existed the two lived apart —
// unified hunks were parsed twice under different names, and the
// buffer-versus-disk differ serialised its result as git-diff TEXT purely
// so the other parser could read it back.
//
// The package is deliberately pure: no tcell, no theme, no git, no app.
// It knows line numbers, kinds and paths; it does not know colors,
// rectangles or subprocesses. Rows is the one concession to presentation
// and it stops at pairing — which old line sits beside which new one —
// because that answer is identical for both producers and is worth
// testing once.
package diff

import "strconv"

// Kind classifies one line of a diff by which side of the change owns
// it. It is the smallest fact a consumer needs: gutter markers, row
// pairing and the unified rendering all branch on nothing else.
type Kind int

const (
	// Context is a line present unchanged on both sides.
	Context Kind = iota
	// Add is a line only the new side has.
	Add
	// Del is a line only the old side has.
	Del
)

// Marker returns the unified-diff prefix byte for the kind — the
// character a reader (and skiff's own line styling) recognises the side
// by. It lives on Kind so no consumer has to keep its own table.
func (k Kind) Marker() byte {
	switch k {
	case Add:
		return '+'
	case Del:
		return '-'
	default:
		return ' '
	}
}

// Line is one line inside a hunk. OldNo and NewNo are 1-based file line
// numbers, and a zero means the line does not exist on that side — the
// same convention the diff view paints as a blank column. NoNewline
// records git's "\ No newline at end of file" note, which belongs to the
// line it follows rather than being a line of its own.
type Line struct {
	Kind      Kind
	OldNo     int
	NewNo     int
	Text      string
	NoNewline bool
}

// Hunk is one changed region with its surrounding context. The four
// range fields carry the values a unified diff puts on the wire: for a
// side with no lines at all, git reports the line BEFORE the change, so
// OldStart can legitimately be one less than the first line listed.
// Section is git's function-context suffix (`@@ … @@ func main() {`),
// kept so the header can be shown the way it arrived.
type Hunk struct {
	OldStart int
	OldLen   int
	NewStart int
	NewLen   int
	Section  string
	Lines    []Line
}

// Header renders the hunk's `@@` line. Both producers share this one
// renderer so a diff reads the same however it was built; the length is
// always written, even when it is 1, because a header that sometimes
// elides it is one more shape every reader has to know. Parse still
// accepts git's elided form.
func (h Hunk) Header() string {
	side := func(sign string, start, count int) string {
		return sign + strconv.Itoa(start) + "," + strconv.Itoa(count)
	}
	out := "@@ " + side("-", h.OldStart, h.OldLen) + " " + side("+", h.NewStart, h.NewLen) + " @@"
	if h.Section != "" {
		out += " " + h.Section
	}
	return out
}

// File is one file's worth of change. OldPath and NewPath are
// repo-relative and differ on a rename; either is empty when that side
// is /dev/null. IsNew, IsDeleted and Binary are recorded from the header
// rather than re-derived from the paths, so a consumer never has to
// guess what an empty path meant.
type File struct {
	OldPath   string
	NewPath   string
	Hunks     []Hunk
	Binary    bool
	IsNew     bool
	IsDeleted bool
}

// Path names the file the way a reader thinks of it: the new name, since
// that is what a rename lands on and what an open tab holds, falling back
// to the old name for a deletion, where there is no new side.
func (f File) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Patch is a whole diff — one file for a working-tree diff, several for
// a commit. It is the unit the diff view opens, so callers hand one
// around rather than a slice of files plus a flag.
type Patch struct {
	Files []File
}

// Empty reports whether the patch has nothing worth showing. A file
// carrying no hunks is header noise (a mode change, a rename with no
// content edit); a binary file is not, because "these bytes differ" is
// the whole answer for it.
func (p Patch) Empty() bool {
	for _, f := range p.Files {
		if len(f.Hunks) > 0 || f.Binary {
			return false
		}
	}
	return true
}
