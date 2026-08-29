// =============================================================================
// File: internal/app/gitworktree.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-23
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// gitworktree.go is the worktree side of skiff's git integration: create,
// list, and remove `git worktree` checkouts from the Git extras popup.
// Mutations ride the same one-at-a-time runGitOp / gitOpDoneEvent pattern
// as the rest of the write side; the list is a read, so it gets its own
// async event (worktreeListEvent) that keeps a slow `git worktree list`
// off the UI thread.
//
// A worktree is a second working tree of the same repository, so none of
// these operations rewrite the current one — every op runs with
// touchesTree=false, and the current checkout's buffers are never at
// stake. Removal follows the delete-branch ladder: a plain remove that
// git refuses because of uncommitted work gets one explicit force offer.

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/git"
)

// worktreeListEvent delivers an asynchronously-collected worktree list
// to the main loop, tagged with which flow asked for it — the same
// purpose-tagged pattern as branchListEvent.
type worktreeListEvent struct {
	when    time.Time
	purpose string // "list" | "remove"
	wts     []git.Worktree
}

// When implements tcell.Event.
func (e *worktreeListEvent) When() time.Time { return e.when }

// requestWorktreeList collects the list off the UI thread, then reopens
// the flow via handleWorktreeList. Best-effort like every read here: a
// failing git yields no rows, and the flow degrades to a flash rather
// than a hollow overlay.
func (a *App) requestWorktreeList(purpose string) {
	repo := a.readRepo()
	scr := a.screen
	a.safeGo("git-worktree-list", func() {
		wts, _ := repo.Worktrees()
		_ = scr.PostEvent(&worktreeListEvent{when: time.Now(), purpose: purpose, wts: wts})
	})
}

// handleWorktreeList routes a collected list to the flow that asked for
// it.
func (a *App) handleWorktreeList(e *worktreeListEvent) {
	switch e.purpose {
	case "list":
		a.openWorktreeList(e.wts)
	case "remove":
		a.openRemoveWorktreePick(e.wts)
	}
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

// menuGitNewWorktree opens the create flow: pick the branch the new
// worktree checks out (or a brand-new branch), then the path to create
// it at.
func (a *App) menuGitNewWorktree() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestBranchList("worktree")
}

// openWorktreeBranchPick opens the create-flow branch picker: a
// "New branch…" row on top, then every branch except the current one —
// the current branch is already checked out in the main worktree, and
// git refuses to check it out in a second one.
func (a *App) openWorktreeBranchPick(names []string) {
	candidates := make([]string, 0, len(names))
	for _, n := range names {
		if n != a.gitSnap.Branch {
			candidates = append(candidates, n)
		}
	}
	items := []listPickItem{{Label: "New branch…", Tag: "from HEAD"}}
	items = append(items, branchPickItems(candidates, "")...)
	a.openListPick("New worktree — branch", items,
		func(app *App, i int) {
			var newBranch string
			if i == 0 {
				newBranch = "new"
			} else {
				newBranch = candidates[i-1]
			}
			app.askWorktreePath(newBranch)
		}, nil, nil)
}

// askWorktreePath prompts for the worktree path, defaulting to a sibling
// directory next to the main checkout. A "new" branch gets one more
// prompt for its name before the command is built.
func (a *App) askWorktreePath(branch string) {
	a.openPrompt("New worktree — path", "directory to create", a.defaultWorktreePath(),
		func(app *App, path string) {
			path = strings.TrimSpace(path)
			if branch == "new" {
				app.openPrompt("New worktree — branch name", "created from "+app.gitSnap.Branch, "",
					func(app2 *App, name string) {
						app2.doGitNewWorktree(path, name, true)
					})
				return
			}
			app.doGitNewWorktree(path, branch, false)
		})
}

// defaultWorktreePath suggests a sibling directory next to the main
// checkout — the convention most worktree workflows settle on. The
// sibling takes the repo's own name plus a -wt suffix: the bare name is
// the main checkout's directory and can never be the target. A repo at
// the filesystem root has no usable parent, so the prompt starts empty.
func (a *App) defaultWorktreePath() string {
	parent := filepath.Dir(a.rootDir)
	if parent == a.rootDir || parent == string(filepath.Separator) {
		return ""
	}
	return filepath.Join(parent, filepath.Base(a.rootDir)+"-wt")
}

// doGitNewWorktree validates the inputs and runs the add. An existing
// branch is checked out as-is; a new branch is created from HEAD; a
// remote-tracking pick creates the tracking local — that rule, and the
// existence probe it needs, live in git.Repo.WorktreeAdd on the op
// goroutine. The early safeRef is the flash for a hostile name; the
// verb refuses it again regardless.
func (a *App) doGitNewWorktree(path, branch string, newBranch bool) {
	abs, err := filepath.Abs(path)
	if err != nil || abs == "" || strings.HasPrefix(abs, "-") {
		a.flash("Invalid worktree path")
		return
	}
	path = abs
	if _, ok := a.safeRef(branch); !ok {
		return
	}
	if !newBranch {
		if _, ok := a.safeRef(localBranchName(branch)); !ok {
			return
		}
	}
	label := "New worktree"
	if newBranch {
		label = "New worktree (new branch)"
	}
	a.runGitOp(label, "Created worktree at "+path, false,
		func(r *git.Repo) error { return r.WorktreeAdd(path, branch, newBranch) })
}

// -----------------------------------------------------------------------------
// List
// -----------------------------------------------------------------------------

// menuGitListWorktrees opens the read-only worktree list.
func (a *App) menuGitListWorktrees() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestWorktreeList("list")
}

// openWorktreeList renders the collected list as an info overlay: one
// line per worktree, the main one starred, flags trailing.
func (a *App) openWorktreeList(wts []git.Worktree) {
	if len(wts) == 0 {
		a.flash("No worktrees")
		return
	}
	lines := make([]string, 0, len(wts))
	for _, w := range wts {
		line := w.Path
		if w.Main {
			line = "* " + line
		}
		if w.Branch != "" {
			line += "  (" + w.Branch + ")"
		}
		if len(w.Flags) > 0 {
			line += "  [" + strings.Join(w.Flags, ", ") + "]"
		}
		lines = append(lines, line)
	}
	a.openInfo("Worktrees", lines)
}

// -----------------------------------------------------------------------------
// Remove
// -----------------------------------------------------------------------------

// menuGitRemoveWorktree opens the remove flow: pick a worktree (never
// the main one), confirm, remove.
func (a *App) menuGitRemoveWorktree() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestWorktreeList("remove")
}

// openRemoveWorktreePick opens the remove picker over every worktree
// except the main one — removing the main checkout is a shell job, not
// an editor one.
func (a *App) openRemoveWorktreePick(wts []git.Worktree) {
	var items []listPickItem
	var paths []string
	for _, w := range wts {
		if w.Main {
			continue
		}
		label := w.Path
		if w.Branch != "" {
			label += "  (" + w.Branch + ")"
		}
		items = append(items, listPickItem{Label: label})
		paths = append(paths, w.Path)
	}
	if len(items) == 0 {
		a.flash("No worktrees to remove")
		return
	}
	a.openListPick("Remove worktree", items,
		func(app *App, i int) {
			path := paths[i]
			app.openConfirm("Remove worktree",
				fmt.Sprintf("Remove %s? Uncommitted changes there will be lost.", path),
				func(app2 *App) {
					app2.gitWorktreeTarget = path
					app2.runGitOp("Remove worktree", "Removed worktree", false,
						func(r *git.Repo) error { return r.WorktreeRemove(path, false) })
				})
		}, nil, nil)
}
