// =============================================================================
// File: internal/git/diff.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// diff.go is the working-tree diff vocabulary: what changed against a
// base, as internal/diff's model. Both verbs pin the header prefixes
// explicitly so a user's diff.noprefix / diff.mnemonicPrefix config
// cannot change the shape the parser attributes paths from. Paths in
// the headers still arrive C-quoted when they carry unusual bytes —
// `-z` does not unquote patch output, only the --name-* listings — and
// diff.Parse decodes them, so a caller sees repo-relative paths as
// they are on disk.

package git

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnlam90/skiff/internal/diff"
)

// diffPrefixArgs pins the a/ b/ header prefixes the parser attributes
// paths from.
var diffPrefixArgs = []string{"--src-prefix=a/", "--dst-prefix=b/"}

// Diff returns the working tree's changes against base ("" means HEAD)
// with context lines of surrounding context, narrowed to paths when any
// are given. base is repo-controlled — a clone can ship a branch named
// anything — so it goes through SafeRef and the paths sit behind `--`.
// A clean tree (or a pathspec that matches nothing) is an empty Patch
// and no error; the error is for git refusing to run, or a header the
// parser could not read, in which case the Patch still carries every
// file that did parse.
func (r *Repo) Diff(base string, context int, paths ...string) (diff.Patch, error) {
	if base == "" {
		base = "HEAD"
	}
	ref, err := SafeRef(base)
	if err != nil {
		return diff.Patch{}, err
	}
	args := []string{"diff", "--unified=" + strconv.Itoa(context)}
	args = append(args, diffPrefixArgs...)
	args = append(args, ref, "--")
	args = append(args, paths...)
	out, err := r.read(args...)
	if err != nil {
		return diff.Patch{}, err
	}
	return diff.Parse(out)
}

// DiffUntracked renders a file git has never seen as an all-added diff
// via `diff --no-index -- /dev/null <path>` — untracked files do not
// appear in `diff HEAD` at all. The path is handed over relative to the
// root when it lies inside it: git echoes the argument verbatim into
// the +++ header, and that is the path the File carries. --no-index
// exits 1 whenever the files differ, so a non-zero exit with output is
// the success case; an exit with no output is the real failure (an
// unreadable path). An empty file diffs as nothing and yields the zero
// File.
func (r *Repo) DiffUntracked(path string, context int) (diff.File, error) {
	if rel, err := filepath.Rel(r.root, path); err == nil && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	args := []string{"diff", "--no-index", "--unified=" + strconv.Itoa(context)}
	args = append(args, diffPrefixArgs...)
	args = append(args, "--", os.DevNull, path)
	out, err := r.read(args...)
	if err != nil && len(out) == 0 {
		return diff.File{}, err
	}
	p, perr := diff.Parse(out)
	if len(p.Files) == 0 {
		return diff.File{}, perr
	}
	return p.Files[0], perr
}
