// =============================================================================
// File: internal/git/branches.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// branches.go is the read side of branch knowledge: the picker list,
// and the two facts the write verbs probe before shaping their argv —
// whether the current branch has an upstream (Push) and whether a local
// branch exists yet (Switch, WorktreeAdd).

package git

import "strings"

// branchesFormat asks for-each-ref for three NUL-separated fields per
// line: the HEAD marker ("*" on the checked-out branch), the full
// refname, and the symref target. The symref field is how origin/HEAD is
// dropped structurally — it is the one remote ref that is an alias
// rather than a branch — instead of by matching "HEAD" in a name, which
// would also swallow a branch called fix-HEADER. for-each-ref rather
// than `branch --all` because the latter invents a "(HEAD detached at …)"
// pseudo-row that is not a branch at all.
const branchesFormat = "--format=%(HEAD)%00%(refname)%00%(symref)"

// Branches lists local then remote-tracking branch names, current
// first, remote spellings of local branches dropped (origin/x duplicates
// a local x — switching to it via the local is what the user means).
func (r *Repo) Branches() ([]string, error) {
	out, err := r.read("for-each-ref", branchesFormat, "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	return parseBranches(string(out)), nil
}

// parseBranches orders for-each-ref's records into the picker list.
// Split out so the ordering rules are pinned against captured output
// without a repository.
func parseBranches(out string) []string {
	var current string
	var locals, remotes []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) < 3 || fields[2] != "" {
			continue // blank tail, or a symref alias such as origin/HEAD
		}
		marker, ref := fields[0], fields[1]
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			name := strings.TrimPrefix(ref, "refs/heads/")
			seen[name] = true
			if marker == "*" {
				current = name
				continue
			}
			locals = append(locals, name)
		case strings.HasPrefix(ref, "refs/remotes/"):
			remotes = append(remotes, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	names := make([]string, 0, 1+len(locals)+len(remotes))
	if current != "" {
		names = append(names, current)
	}
	names = append(names, locals...)
	for _, rem := range remotes {
		if i := strings.IndexByte(rem, '/'); i >= 0 && seen[rem[i+1:]] {
			continue
		}
		names = append(names, rem)
	}
	return names
}

// HasUpstream reports whether the current branch tracks an upstream —
// the fact Push needs to choose between a plain push and its first-push
// `--set-upstream` form.
func (r *Repo) HasUpstream() bool {
	_, err := r.read("rev-parse", "--abbrev-ref", "@{upstream}")
	return err == nil
}

// BranchExists reports whether a local branch named name exists. A name
// SafeRef refuses answers false: it cannot be a branch we would act on.
func (r *Repo) BranchExists(name string) bool {
	name, err := SafeRef(name)
	if err != nil {
		return false
	}
	_, err = r.read("rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// currentBranch returns the checked-out branch name, or ok=false when
// HEAD is detached. Unlike Snapshot's branch label it does not fall back
// to a SHA: a verb that would paste the answer into `push --set-upstream
// origin <name>` must not be handed a commit hash.
func (r *Repo) currentBranch() (name string, ok bool) {
	out, err := r.read("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", false
	}
	name = strings.TrimRight(string(out), "\n\r")
	return name, name != ""
}
