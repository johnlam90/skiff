// =============================================================================
// File: internal/git/ops.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// ops.go is the write side of the vocabulary: commit, push, pull,
// fetch, branch verbs, stash and undo. Every verb runs under the write
// deadline, accumulates git's combined output across the commands it
// issues, and reports a refusal as *OpError. Verbs that need a fact
// about the repository to decide their own argv (Push: is there an
// upstream? Switch: does the tracking local exist yet?) ask git for it
// here — the caller hands the verb to a goroutine, so the probe never
// runs on the event loop.

package git

import "strings"

// opRun accumulates one verb's output across the commands it issues,
// so an OpError shows everything git said, not just the last command's
// words. A verb creates one, runs its commands through it, and stops at
// the first failure.
type opRun struct {
	r   *Repo
	op  string
	out strings.Builder
}

// op starts a run for the named verb.
func (r *Repo) op(name string) *opRun {
	return &opRun{r: r, op: name}
}

// run issues one command, appends its output, and wraps a failure as
// *OpError carrying everything accumulated so far.
func (o *opRun) run(args ...string) error {
	b, err := o.r.write(args...)
	o.out.Write(b)
	if err != nil {
		return newOpError(o.op, o.out.String(), err)
	}
	return nil
}

// Commit stages and commits exactly paths: `add -A` scoped to the paths
// is what stages a deletion or an untracked file, and the commit is
// path-scoped so anything else already in the index stays out. An empty
// paths list is refused rather than passed on — an unscoped `add -A`
// would stage the whole tree, the opposite of what a checkbox column
// means.
func (r *Repo) Commit(paths []string, message string) error {
	if len(paths) == 0 {
		return newOpError("Commit", "nothing to commit: no paths selected", nil)
	}
	o := r.op("Commit")
	if err := o.run(append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	return o.run(append([]string{"commit", "-m", message, "--"}, paths...)...)
}

// pushArgs decides the push's shape: plain when an upstream exists,
// `--set-upstream origin <branch>` for a branch's first push (the
// second push shouldn't need a shell visit either). push takes no `--`
// separator, so a detached HEAD or a branch name git would read as an
// option loses the positional entirely: plain `push` then fails with
// git's own "no upstream" message instead of handing git an option we
// didn't write. The upstream probe is the reason this is a method on
// the verb and not a decision the caller makes: it is a git round trip.
func (r *Repo) pushArgs() []string {
	if r.HasUpstream() {
		return []string{"push"}
	}
	branch, ok := r.currentBranch()
	if !ok {
		return []string{"push"}
	}
	if _, err := SafeRef(branch); err != nil {
		return []string{"push"}
	}
	return []string{"push", "--set-upstream", "origin", branch}
}

// Push pushes the current branch, setting the upstream on its first
// push. A non-fast-forward refusal comes back with
// OpError.NonFastForward set so the caller can offer PullAndPush.
func (r *Repo) Push() error {
	return r.op("Push").run(r.pushArgs()...)
}

// PullAndPush is the accepted "push rejected" offer: a merge-pull, then
// the push — one verb, stopping at the pull if it conflicts, with both
// commands' output in the report.
func (r *Repo) PullAndPush() error {
	o := r.op("Pull & push")
	if err := o.run("pull", "--no-rebase", "--no-edit"); err != nil {
		return err
	}
	return o.run(r.pushArgs()...)
}

// Pull integrates origin. ffOnly fails fast when the branches have
// diverged — a real merge wants an editor and a conflict UI the editor
// doesn't host, and OpError.Advice tells the user why. Without it the
// pull is a no-edit merge, the shape PullAndPush uses.
func (r *Repo) Pull(ffOnly bool) error {
	if ffOnly {
		return r.op("Pull").run("pull", "--ff-only")
	}
	return r.op("Pull").run("pull", "--no-rebase", "--no-edit")
}

// Fetch refreshes the remote-tracking refs (and with them a Snapshot's
// ahead/behind counts) without touching the working tree.
func (r *Repo) Fetch() error {
	return r.op("Fetch").run("fetch")
}

// Switch checks out ref. A remote-tracking spelling ("origin/fix")
// follows the tracking rule: the first switch creates local "fix"
// tracking origin/fix, later ones just move to "fix" — checking out
// origin/fix directly would only detach HEAD. The existence probe runs
// here, inside the verb. Every ref is followed by `--`; the `-b` value
// sits in a position no separator protects, so SafeRef covers it.
func (r *Repo) Switch(ref string) error {
	ref, err := SafeRef(ref)
	if err != nil {
		return err
	}
	o := r.op("Switch branch")
	i := strings.IndexByte(ref, '/')
	if i < 0 {
		return o.run("checkout", ref, "--")
	}
	local := ref[i+1:]
	if _, err := SafeRef(local); err != nil {
		return err
	}
	if r.BranchExists(local) {
		return o.run("checkout", local, "--")
	}
	return o.run("checkout", "-b", local, "--track", ref, "--")
}

// NewBranch creates name and switches to it. from is the start point;
// empty means HEAD.
func (r *Repo) NewBranch(name, from string) error {
	name, err := SafeRef(name)
	if err != nil {
		return err
	}
	args := []string{"checkout", "-b", name}
	if from != "" {
		start, err := SafeRef(from)
		if err != nil {
			return err
		}
		args = append(args, start)
	}
	return r.op("New branch").run(append(args, "--")...)
}

// DeleteBranch deletes a local branch. Without force, `-d` refuses an
// unmerged branch and the error carries OpError.NotMerged so the caller
// can ask again with force — losing work needs its own explicit yes.
func (r *Repo) DeleteBranch(name string, force bool) error {
	name, err := SafeRef(name)
	if err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	return r.op("Delete branch").run("branch", flag, "--", name)
}

// RenameBranch renames old to name in place.
func (r *Repo) RenameBranch(old, name string) error {
	old, err := SafeRef(old)
	if err != nil {
		return err
	}
	name, err = SafeRef(name)
	if err != nil {
		return err
	}
	return r.op("Rename branch").run("branch", "-m", "--", old, name)
}

// Merge merges ref into the current branch without opening an editor.
// A conflicted merge stops with git's own reason and the tree left
// mid-merge, which is the honest state to leave it in.
func (r *Repo) Merge(ref string) error {
	ref, err := SafeRef(ref)
	if err != nil {
		return err
	}
	return r.op("Merge").run("merge", "--no-edit", ref, "--")
}

// StashPush stashes the working tree, new files included — "stash my
// changes" from an editor means the file just created too.
func (r *Repo) StashPush() error {
	return r.op("Stash").run("stash", "push", "-u")
}

// StashPop pops the most recent stash back onto the tree.
func (r *Repo) StashPop() error {
	return r.op("Pop stash").run("stash", "pop")
}

// UndoCommit soft-resets HEAD~1: the commit disappears, its changes stay
// staged in the working tree.
func (r *Repo) UndoCommit() error {
	return r.op("Undo commit").run("reset", "--soft", "HEAD~1")
}
