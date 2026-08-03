// =============================================================================
// File: internal/git/snapshot.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"bytes"
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
}

// Status collects a Snapshot: repo-ness and root via rev-parse, the
// changed set via status --porcelain (merged with a diff against base
// when comparing against a ref), the branch label, and the
// ahead/behind counts. Best-effort throughout — a non-repo or any
// failing command degrades to the zero value rather than erroring, so
// a transient git problem can never take the editor down with it.
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
		return Snapshot{}
	}
	toplevel := strings.TrimRight(string(topBytes), "\n\r")
	if toplevel == "" {
		return Snapshot{}
	}

	ahead, behind := r.aheadBehind()
	out, err := r.Output("status", "--porcelain")
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

// diffNameStatus builds the changed map versus an arbitrary ref via
// `git diff --name-status <ref>` — the loader behind the
// compare-against mode. Best-effort like every loader here.
func (r *Repo) diffNameStatus(base, toplevel string) map[string]ChangeKind {
	dirty := map[string]ChangeKind{}
	out, err := r.Output("diff", "--name-status", base)
	if err != nil {
		return dirty
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		rel := parts[len(parts)-1] // renames list old	new; keep the new
		var kind ChangeKind
		switch parts[0][0] {
		case 'A':
			kind = ChangeAdded
		case 'D':
			kind = ChangeDeleted
		case 'R':
			kind = ChangeRenamed
		default:
			kind = ChangeModified
		}
		dirty[filepath.Join(toplevel, rel)] = kind
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

// parsePorcelain converts the bytes returned by `git status --porcelain`
// into a map of absolute paths. Split out from Status so it can be
// exercised without spawning a subprocess.
//
// The porcelain v1 format (without -z) is:
//
//	XY <path>
//	XY <oldpath> -> <newpath>      (renames / copies)
//	XY "quoted path with spaces"   (when core.quotePath is on, the default)
//
// Any line counts as dirty regardless of the X/Y codes; renames mark
// both the old and new paths so the user sees both rows tinted.
func parsePorcelain(out []byte, toplevel string) map[string]ChangeKind {
	dirty := map[string]ChangeKind{}
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		line := string(raw)
		if len(line) < 4 {
			continue
		}
		kind := porcelainKind(line[:2])
		// Drop the two status chars + the separating space.
		body := line[3:]

		if idx := strings.Index(body, " -> "); idx >= 0 {
			oldPath := unquotePath(body[:idx])
			newPath := unquotePath(body[idx+len(" -> "):])
			if oldPath != "" {
				dirty[filepath.Join(toplevel, oldPath)] = ChangeDeleted
			}
			if newPath != "" {
				dirty[filepath.Join(toplevel, newPath)] = ChangeRenamed
			}
			continue
		}

		path := unquotePath(body)
		if path == "" {
			continue
		}
		dirty[filepath.Join(toplevel, path)] = kind
	}
	return dirty
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

// unquotePath undoes git's C-style quoting (enabled by default via
// core.quotePath) so paths with spaces, unicode, or control chars come
// back as a normal Go string. Falls back to the raw input on any parse
// error — that's safer than dropping a path the user might want flagged.
func unquotePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	if unq, err := strconv.Unquote(s); err == nil {
		return unq
	}
	return s
}
