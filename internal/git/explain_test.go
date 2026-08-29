// =============================================================================
// File: internal/git/explain_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"testing"
)

// TestExplain_Mappings pins the headline for each recognised failure
// shape — the sentence is the UX, so it's worth locking down. The
// worktree cases sit in the same table because the phrases and the
// verbs that emit them now live in one package.
func TestExplain_Mappings(t *testing.T) {
	cases := []struct{ out, want string }{
		{"! [rejected] main -> main (fetch first)", "origin has commits you don't — pull first, then push"},
		{"! [remote rejected] main -> main (pre-receive hook declined)", "the server refused the push — a hook or permission said no"},
		{"error: failed to push some refs to 'origin'", "push rejected — see git's reason below"},
		{"fatal: Not possible to fast-forward, aborting.", "local and origin have diverged — pull needs a merge"},
		{"CONFLICT (content): Merge conflict in a.go", "merge conflict — fix the marked files, then commit"},
		{"nothing to commit, working tree clean", "nothing to commit"},
		{"No stash entries found.", "no stash to pop"},
		{"fatal: The current branch x has no upstream branch.", "this branch has no upstream yet"},
		{"fatal: could not read from remote repository", "couldn't reach origin — check network / credentials"},
		{"error: Your local changes to the following files would be overwritten by checkout:", "uncommitted changes are in the way — commit or stash first"},
		{"fatal: '/r/side' is already checked out at '/r/other'", "that branch is checked out in another worktree — remove or switch it first"},
		{"fatal: refname 'side' already exists", "that path or branch already exists — pick another"},
		{"fatal: worktree '/r/side' is locked", "that worktree is locked — unlock it first (git worktree unlock)"},
		{"something novel", "git reported an error:"},
	}
	for _, c := range cases {
		if got := explain(c.out); got != c.want {
			t.Errorf("explain(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// TestNewOpError_ClassifiesRefusals pins the three flags a surface
// branches on. Each is the gate for a follow-up gesture that loses or
// rewrites work (pull-then-push, force delete, force remove), so a
// false positive would offer the wrong repair and a false negative
// would hide the right one.
func TestNewOpError_ClassifiesRefusals(t *testing.T) {
	cases := []struct {
		out                           string
		nonFF, notMerged, treeIsDirty bool
	}{
		{"! [rejected] main -> main (fetch first)", true, false, false},
		{"! [rejected] main -> main (non-fast-forward)", true, false, false},
		{"! [remote rejected] main -> main (pre-receive hook declined)", false, false, false},
		{"error: The branch 'orphan' is not fully merged.", false, true, false},
		{"fatal: '/r/wt' contains modified or untracked files, use --force to delete it", false, false, true},
		{"something novel", false, false, false},
	}
	for _, c := range cases {
		e := newOpError("Op", c.out, errors.New("exit status 1"))
		if e.NonFastForward != c.nonFF || e.NotMerged != c.notMerged || e.WorktreeDirty != c.treeIsDirty {
			t.Errorf("newOpError(%q) = nonFF %v notMerged %v dirty %v, want %v %v %v",
				c.out, e.NonFastForward, e.NotMerged, e.WorktreeDirty, c.nonFF, c.notMerged, c.treeIsDirty)
		}
		if e.Advice != explain(c.out) {
			t.Errorf("Advice for %q = %q, want the explainer's headline", c.out, e.Advice)
		}
	}
}

// TestOpError_ErrorAndUnwrap pins the error's two faces: Error names
// the verb and the advice for a log line, and Unwrap exposes the
// process error so errors.Is(err, ErrGitMissing) keeps answering
// through the typed wrapper.
func TestOpError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("exit status 128")
	var err error = newOpError("Push", "fatal: could not read from remote repository", inner)
	if got := err.Error(); got != "Push: couldn't reach origin — check network / credentials" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(err, inner) {
		t.Fatal("OpError must unwrap to the process error")
	}
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Output == "" {
		t.Fatal("errors.As must recover the typed error with its output")
	}
}
