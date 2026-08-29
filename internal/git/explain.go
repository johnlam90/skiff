// =============================================================================
// File: internal/git/explain.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import "strings"

// OpError is what a write verb returns when git ran and said no. It is
// the package's error vocabulary: Output holds everything git printed,
// Advice is the one plain-language sentence a surface leads with, and
// the three flags name the refusals the editor offers a follow-up
// gesture for — pull-then-push, force delete, force remove. A surface
// branches on the flags and renders Advice; it never reads Output for
// meaning, only to show it.
type OpError struct {
	// Op is the verb that failed, in the package's own words ("Push").
	Op string
	// Output is git's combined stdout+stderr, accumulated across every
	// command the verb ran so a report shows the whole story.
	Output string
	// Advice is the headline explain derived from Output.
	Advice string
	// NonFastForward marks a push origin rejected because it has commits
	// we don't — the one refusal pull-then-push repairs. A hook or
	// permission rejection is NOT this: pulling would not help.
	NonFastForward bool
	// NotMerged marks `branch -d` refusing an unmerged branch; the fix
	// (`-D`) loses work, so it wants its own explicit yes.
	NotMerged bool
	// WorktreeDirty marks `worktree remove` refusing a tree with
	// uncommitted or untracked files, the mirror of NotMerged.
	WorktreeDirty bool
	// Err is the underlying process error (exit status, deadline,
	// ErrGitMissing) for errors.Is callers.
	Err error
}

// Error renders the verb and the headline — enough for a log line; the
// surfaces render Output themselves.
func (e *OpError) Error() string {
	return e.Op + ": " + e.Advice
}

// Unwrap exposes the process error so errors.Is(err, ErrGitMissing)
// still answers through the typed wrapper.
func (e *OpError) Unwrap() error { return e.Err }

// newOpError classifies one failure: the flags are read once here so
// no surface has to know which words git uses for which refusal.
func newOpError(op, output string, err error) *OpError {
	return &OpError{
		Op:             op,
		Output:         output,
		Advice:         explain(output),
		NonFastForward: isNonFastForward(output),
		NotMerged:      strings.Contains(output, "not fully merged"),
		WorktreeDirty:  strings.Contains(output, "contains modified or untracked files"),
		Err:            err,
	}
}

// isPushRejected recognises any push refusal (for the explainer);
// isNonFastForward recognises the specific refusal whose fix is "pull,
// then push". A hook / permission rejection must NOT get that offer —
// pulling won't help, and the merge it creates just muddies the water.
func isPushRejected(output string) bool {
	return strings.Contains(output, "[rejected]") ||
		strings.Contains(output, "[remote rejected]") ||
		strings.Contains(output, "failed to push some refs")
}

// isNonFastForward reports whether a rejected push failed because
// origin has commits we don't — the one case pull-then-push repairs.
func isNonFastForward(output string) bool {
	return strings.Contains(output, "fetch first") ||
		strings.Contains(output, "non-fast-forward")
}

// explain turns git's output into one plain-language headline. The raw
// output still follows in the modal — this is the sentence that tells
// the user what to *do*. It lives here, next to the verbs that produce
// the output, so the phrases it probes and the commands that emit them
// change in the same package.
func explain(output string) string {
	low := strings.ToLower(output)
	switch {
	case isNonFastForward(output):
		return "origin has commits you don't — pull first, then push"
	case strings.Contains(output, "[remote rejected]") ||
		strings.Contains(low, "hook declined"):
		return "the server refused the push — a hook or permission said no"
	case isPushRejected(output):
		return "push rejected — see git's reason below"
	case strings.Contains(low, "not possible to fast-forward") ||
		strings.Contains(low, "divergent branches"):
		return "local and origin have diverged — pull needs a merge"
	case strings.Contains(low, "conflict"):
		return "merge conflict — fix the marked files, then commit"
	case strings.Contains(low, "nothing to commit"):
		return "nothing to commit"
	case strings.Contains(low, "no stash entries"):
		return "no stash to pop"
	case strings.Contains(low, "no upstream branch"):
		return "this branch has no upstream yet"
	case strings.Contains(low, "could not read from remote") ||
		strings.Contains(low, "could not resolve host") ||
		strings.Contains(low, "authentication failed") ||
		strings.Contains(low, "terminal prompts disabled"):
		return "couldn't reach origin — check network / credentials"
	case strings.Contains(low, "would be overwritten by checkout"):
		return "uncommitted changes are in the way — commit or stash first"
	case strings.Contains(low, "already checked out at"):
		return "that branch is checked out in another worktree — remove or switch it first"
	case strings.Contains(low, "already exists"):
		return "that path or branch already exists — pick another"
	case strings.Contains(low, "is locked"):
		return "that worktree is locked — unlock it first (git worktree unlock)"
	default:
		return "git reported an error:"
	}
}
