// =============================================================================
// File: internal/git/worktrees.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// worktrees.go is the worktree vocabulary: the list, its porcelain
// parser, and the add/remove verbs. A worktree is a second working tree
// of the same repository, so none of these touch the tree the handle is
// bound to.

package git

import "strings"

// Worktree is one row of `git worktree list --porcelain`.
type Worktree struct {
	Path   string   // absolute worktree path
	Branch string   // short branch name; "" when detached or bare
	Main   bool     // the main worktree — never removable from the editor
	Flags  []string // bare, detached, locked, prunable
}

// Worktrees lists every worktree of the repository, the main one first
// and marked.
func (r *Repo) Worktrees() ([]Worktree, error) {
	out, err := r.read("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(string(out)), nil
}

// parseWorktreeList turns `git worktree list --porcelain` output into
// rows. Blocks are separated by blank lines and start with `worktree
// <path>`; `branch refs/heads/x` carries the short name, `bare`,
// `detached`, `locked` and `prunable` are flags. Anything unrecognised
// is ignored — porcelain is stable, but a future field must not cost a
// row. Paths arrive verbatim (no C-quoting) in this format, which is
// why it needs no -z. The first row is marked Main: porcelain has no
// explicit marker, and the main worktree is always listed first.
func parseWorktreeList(out string) []Worktree {
	var (
		wts   []Worktree
		cur   *Worktree
		block bool
	)
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			block = false
			continue
		}
		if strings.HasPrefix(l, "worktree ") {
			if cur != nil {
				wts = append(wts, *cur)
			}
			cur = &Worktree{Path: strings.TrimSpace(l[len("worktree "):])}
			block = true
			continue
		}
		if !block || cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(l, "branch "):
			cur.Branch = strings.TrimPrefix(l[len("branch "):], "refs/heads/")
		case l == "bare", l == "detached", l == "locked", l == "prunable":
			cur.Flags = append(cur.Flags, l)
		}
	}
	if cur != nil {
		wts = append(wts, *cur)
	}
	if len(wts) > 0 {
		wts[0].Main = true
	}
	return wts
}

// WorktreeAdd creates a worktree at path. With create the branch is
// made fresh from HEAD; otherwise an existing local is checked out
// as-is, and a remote-tracking spelling creates the tracking local on
// first use and reuses it after, mirroring Switch. The existence probe
// runs here, inside the verb. The path is a user prompt, not a ref: it
// is guarded against the option position and never needs SafeRef.
// Branches do — the `-b` value sits in a position no `--` protects.
func (r *Repo) WorktreeAdd(path, branch string, create bool) error {
	if err := safePath(path); err != nil {
		return err
	}
	branch, err := SafeRef(branch)
	if err != nil {
		return err
	}
	o := r.op("New worktree")
	if create {
		return o.run("worktree", "add", path, "-b", branch, "--")
	}
	i := strings.IndexByte(branch, '/')
	if i < 0 {
		return o.run("worktree", "add", path, branch, "--")
	}
	local := branch[i+1:]
	if _, err := SafeRef(local); err != nil {
		return err
	}
	if r.BranchExists(local) {
		return o.run("worktree", "add", path, local, "--")
	}
	return o.run("worktree", "add", path, "-b", local, "--track", branch, "--")
}

// WorktreeRemove removes the worktree at path. Without force git
// refuses a tree with uncommitted work and the error carries
// OpError.WorktreeDirty so the caller can ask again with force.
func (r *Repo) WorktreeRemove(path string, force bool) error {
	if err := safePath(path); err != nil {
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return r.op("Remove worktree").run(append(args, "--", path)...)
}
