// =============================================================================
// File: internal/git/snapshot.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// ChangeKind describes the strongest git status a changed path carries.
type ChangeKind int

const (
	ChangeNone ChangeKind = iota
	ChangeModified
	ChangeAdded
	ChangeDeleted
	ChangeRenamed
	// ChangeMixed marks a folder whose descendants carry different
	// kinds — a rollup value, never produced for a file.
	ChangeMixed
)

// Snapshot is one consistent read of repo state — the model every
// git-aware surface consumes (tree badges, gutter, status bar, git
// panel). IsRepo distinguishes "not a git repo" (don't bother trying
// again) from "git error" (we tried and bailed); it is the explicit
// fact that replaces branch-name-isn't-empty guessing. Files holds
// absolute paths of changed entries; absence of a key means "clean",
// never "unknown". Branch is the current branch name, a short SHA when
// HEAD is detached, or "" outside a repo.
type Snapshot struct {
	IsRepo bool
	Root   string
	Branch string
	// Ahead/Behind count commits versus the branch's upstream — the
	// status bar's ↑ ↓ arrows. Both zero when there is no upstream.
	Ahead  int
	Behind int
	Files  map[string]ChangeKind
	// GitMissing separates "this machine has no git" from "this
	// directory is not a repo". Both render as no badges; only the
	// first is worth telling the user about, and only a caller can
	// decide whether to. Set when the very first command failed
	// because the binary isn't on PATH.
	GitMissing bool
}

// Status collects a Snapshot: repo-ness and root via rev-parse, the
// changed set via status --porcelain -z (merged with a diff against
// base when comparing against a ref), the branch label, and the
// ahead/behind counts. Best-effort throughout — a non-repo or any
// failing command degrades to the zero value rather than erroring, so
// a transient git problem can never take the editor down with it. The
// one failure worth distinguishing, "no git on this machine", comes
// back as GitMissing rather than as an error.
func (r *Repo) Status(base string) Snapshot {
	if r.root == "" {
		return Snapshot{}
	}

	// rev-parse --show-toplevel does double duty: it tells us whether
	// we're in a git work tree at all (non-zero exit otherwise) and
	// gives us the absolute path of the repo root, which is the prefix
	// every porcelain path is reported relative to.
	topBytes, err := r.Output("rev-parse", "--show-toplevel")
	if err != nil {
		return Snapshot{GitMissing: errors.Is(err, ErrGitMissing)}
	}
	toplevel := strings.TrimRight(string(topBytes), "\n\r")
	if toplevel == "" {
		return Snapshot{}
	}

	ahead, behind := r.aheadBehind()
	// -z is not an optimization: without it a path containing a newline
	// splits into two garbage records and shreds the rest of the parse.
	out, err := r.Output("status", "--porcelain", "-z")
	if err != nil {
		// We *are* in a repo (rev-parse succeeded) but couldn't read
		// status. Mark the result as a repo with no known dirty files
		// so the caller at least knows we tried.
		return Snapshot{IsRepo: true, Root: toplevel, Files: map[string]ChangeKind{}, Branch: r.branch(), Ahead: ahead, Behind: behind}
	}

	dirty := parsePorcelain(out, toplevel)
	if base != "" {
		// Compare-against-ref mode: the change set is everything
		// different from base, merged with the porcelain's untracked
		// entries (a diff can't see files git doesn't track).
		vsBase := r.diffNameStatus(base, toplevel)
		for abs, kind := range dirty {
			if kind == ChangeAdded {
				if _, exists := vsBase[abs]; !exists {
					vsBase[abs] = kind
				}
			}
		}
		dirty = vsBase
	}
	return Snapshot{IsRepo: true, Root: toplevel, Files: dirty, Branch: r.branch(), Ahead: ahead, Behind: behind}
}

// diffNameStatusArgs builds the argv for the compare-against-ref diff.
// base is repo-controlled — a clone can ship a branch named anything —
// so it goes through SafeRef and is followed by `--`. Without the
// separator, a branch called `--output=/tmp/x` is read as an option and
// the diff becomes an arbitrary file write.
func diffNameStatusArgs(base string) ([]string, error) {
	ref, err := SafeRef(base)
	if err != nil {
		return nil, err
	}
	return []string{"diff", "--name-status", "-z", ref, "--"}, nil
}

// diffNameStatus builds the changed map versus an arbitrary ref — the
// loader behind the compare-against mode. Best-effort like every loader
// here: a rejected ref or a failing git yields an empty map.
func (r *Repo) diffNameStatus(base, toplevel string) map[string]ChangeKind {
	args, err := diffNameStatusArgs(base)
	if err != nil {
		return map[string]ChangeKind{}
	}
	out, err := r.Output(args...)
	if err != nil {
		return map[string]ChangeKind{}
	}
	return parseNameStatus(out, toplevel)
}

// parseNameStatus converts `diff --name-status -z` output into the
// changed map. Under -z the status and the path are separate
// NUL-terminated fields and paths arrive verbatim (no C-quoting):
//
//	M NUL path NUL
//	R100 NUL oldpath NUL newpath NUL
//
// Renames and copies therefore carry two paths, old first — the
// opposite order from `status --porcelain -z`. We keep the new one:
// it's the path that exists in the tree the user is looking at.
func parseNameStatus(out []byte, toplevel string) map[string]ChangeKind {
	dirty := map[string]ChangeKind{}
	fields := splitNUL(out)
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		kind := ChangeModified
		switch status[0] {
		case 'A':
			kind = ChangeAdded
		case 'D':
			kind = ChangeDeleted
		case 'R', 'C':
			kind = ChangeRenamed
			if i+2 < len(fields) {
				i++
				path = fields[i+1]
			}
		}
		dirty[filepath.Join(toplevel, path)] = kind
	}
	return dirty
}

// aheadBehind counts commits versus the current branch's upstream: how
// many the user has that the upstream lacks (ahead — the "you haven't
// pushed" nudge) and vice versa (behind). A branch with no upstream, a
// detached HEAD, or any git failure yields 0/0 — the arrows simply
// don't render.
func (r *Repo) aheadBehind() (ahead, behind int) {
	out, err := r.Output("rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	// Left side of the symmetric difference is the upstream's commits
	// (we are behind by that many); the right side is ours (ahead).
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind
}

// branch returns the current branch name, or a short commit SHA when
// HEAD is detached (rebase / bisect / a manual checkout of a tag).
// Returns "" for non-repos and any other failure mode — the caller
// treats that as "no branch label to show".
//
// symbolic-ref --short HEAD goes first because it's the cheapest way
// to distinguish "on a branch" from "detached"; the rev-parse fallback
// only fires when symbolic-ref's non-zero exit says we're detached.
func (r *Repo) branch() string {
	if out, err := r.Output("symbolic-ref", "--short", "HEAD"); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	if out, err := r.Output("rev-parse", "--short", "HEAD"); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	return ""
}

// parsePorcelain converts the bytes returned by `git status --porcelain
// -z` into a map of absolute paths. Split out from Status so it can be
// exercised without spawning a subprocess.
//
// The -z format is NUL-terminated records, paths verbatim — no C-style
// quoting, and no line framing to be broken by a path containing a
// newline (which is exactly why we ask for it):
//
//	XY <path> NUL                    ordinary entry
//	XY <path> NUL <origPath> NUL     rename / copy
//
// Note the rename order is the reverse of the human format's
// "<orig> -> <path>": under -z the current path comes first and the
// origin follows in its own field.
//
// Any record counts as dirty regardless of the X/Y codes; renames mark
// both the old and new paths so the user sees both rows tinted.
func parsePorcelain(out []byte, toplevel string) map[string]ChangeKind {
	dirty := map[string]ChangeKind{}
	fields := splitNUL(out)
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		code := rec[:2]
		// Drop the two status chars + the separating space.
		path := rec[3:]

		if isRenamePair(code) {
			if i+1 < len(fields) {
				i++
				dirty[filepath.Join(toplevel, fields[i])] = ChangeDeleted
			}
			dirty[filepath.Join(toplevel, path)] = ChangeRenamed
			continue
		}
		dirty[filepath.Join(toplevel, path)] = porcelainKind(code)
	}
	return dirty
}

// isRenamePair reports whether a porcelain XY pair announces a rename
// or copy — the two entry kinds that carry a second, origin-path field
// under -z. Getting this wrong doesn't just mislabel one row, it
// desynchronises every record after it.
func isRenamePair(code string) bool {
	return strings.ContainsAny(code, "RC")
}

// splitNUL breaks NUL-terminated git output into records, dropping the
// empty tail the trailing NUL leaves behind. Git never emits an empty
// field, so dropping empties can't desynchronise a paired record.
func splitNUL(out []byte) []string {
	raw := strings.Split(string(out), "\x00")
	fields := make([]string, 0, len(raw))
	for _, f := range raw {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// porcelainKind maps git porcelain's XY status pair to a ChangeKind.
func porcelainKind(code string) ChangeKind {
	if strings.Contains(code, "?") || strings.Contains(code, "A") {
		return ChangeAdded
	}
	if strings.Contains(code, "D") {
		return ChangeDeleted
	}
	if strings.Contains(code, "R") || strings.Contains(code, "C") {
		return ChangeRenamed
	}
	return ChangeModified
}
