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

// Worktree is one parsed row of `git worktree list --porcelain`.
type Worktree struct {
	Path   string // absolute worktree path
	Branch string // short branch name; "" when detached or bare
	Main   bool   // the main worktree — never removable from here
	Flags  []string
}

// parseWorktreeList turns `git worktree list --porcelain` output into
// rows. Blocks are separated by blank lines and start with `worktree
// <path>`; `branch refs/heads/x` carries the short name, `bare`,
// `detached`, `locked` and `prunable` are flags. Anything unrecognised
// is ignored — porcelain is stable, but a future field must not cost a
// row. The first row is marked Main: porcelain has no explicit marker,
// and the main worktree is always listed first.
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
	// Porcelain carries no explicit "main" marker — the main worktree is
	// simply listed first. That is the only reliable signal, so the first
	// row is the one the remove picker must skip.
	if len(wts) > 0 {
		wts[0].Main = true
	}
	return wts
}

// gitWorktreeList reads the worktree list from real git. Best-effort like
// every other loader: nil on any failure, and the caller degrades to a
// flash rather than a hollow overlay.
func gitWorktreeList(root string) []Worktree {
	out, err := git.Output(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	return parseWorktreeList(string(out))
}

// worktreeListEvent delivers an asynchronously-collected worktree list
// to the main loop, tagged with which flow asked for it — the same
// purpose-tagged pattern as branchListEvent.
type worktreeListEvent struct {
	when    time.Time
	purpose string // "list" | "remove"
	wts     []Worktree
}

// When implements tcell.Event.
func (e *worktreeListEvent) When() time.Time { return e.when }

// requestWorktreeList collects the list off the UI thread, then reopens
// the flow via handleWorktreeList.
func (a *App) requestWorktreeList(purpose string) {
	root := a.rootDir
	scr := a.screen
	go func() {
		wts := gitWorktreeList(root)
		_ = scr.PostEvent(&worktreeListEvent{when: time.Now(), purpose: purpose, wts: wts})
	}()
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
// "New branch…" row on top, then every branch the switch picker sees.
func (a *App) openWorktreeBranchPick(names []string) {
	items := []listPickItem{{Label: "New branch…", Tag: "from HEAD"}}
	items = append(items, branchPickItems(names, a.gitSnap.Branch)...)
	a.openListPick("New worktree — branch", items,
		func(app *App, i int) {
			var newBranch string
			if i == 0 {
				newBranch = "new"
			} else {
				newBranch = names[i-1]
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

// defaultWorktreePath suggests ../<repo name> — the sibling-directory
// convention most worktree workflows settle on. A repo at the
// filesystem root has no usable parent, so the prompt starts empty.
func (a *App) defaultWorktreePath() string {
	parent := filepath.Dir(a.rootDir)
	if parent == a.rootDir || parent == string(filepath.Separator) {
		return ""
	}
	return filepath.Join(parent, filepath.Base(a.rootDir))
}

// doGitNewWorktree validates the inputs, builds the command, and runs it.
// An existing branch is checked out as-is; a new branch is created from
// HEAD; a remote-tracking pick creates the tracking local, mirroring
func (a *App) doGitNewWorktree(path, branch string, newBranch bool) {
	abs, err := filepath.Abs(path)
	if err != nil || abs == "" || strings.HasPrefix(abs, "-") {
		a.flash("Invalid worktree path")
		return
	}
	path = abs
	cmds := gitWorktreeAddCmds(a.rootDir, path, branch, newBranch)
	if cmds == nil {
		if newBranch {
			a.flashUnsafeRef(branch)
		} else {
			a.flashUnsafeRef(localBranchName(branch))
		}
		return
	}
	label := "New worktree"
	if newBranch {
		label = "New worktree (new branch)"
	}
	a.runGitOp(label, "Created worktree at "+path, false, cmds...)
}

// gitWorktreeAddCmds builds the `worktree add` invocation for path, or
// nil when a name can't safely reach git's argv. The path is a user
// prompt, not a repo ref — it is guarded against the option position
// (a leading "-") and never needs SafeRef. Branches do: the `-b` value
// sits in a position no `--` separator protects, and the checkout
// positional is separated anyway.
func gitWorktreeAddCmds(root, path, branch string, newBranch bool) [][]string {
	if strings.HasPrefix(path, "-") {
		return nil
	}
	if newBranch {
		if _, err := git.SafeRef(branch); err != nil {
			return nil
		}
		return [][]string{{"worktree", "add", path, "-b", branch, "--"}}
	}
	if _, err := git.SafeRef(branch); err != nil {
		return nil
	}
	i := strings.IndexByte(branch, '/')
	if i < 0 {
		return [][]string{{"worktree", "add", path, branch, "--"}}
	}
	local := branch[i+1:]
	if _, err := git.SafeRef(local); err != nil {
		return nil
	}
	_, err := git.Output(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+local)
	if err == nil {
		return [][]string{{"worktree", "add", path, local, "--"}}
	}
	return [][]string{{"worktree", "add", path, "-b", local, "--track", branch, "--"}}
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
func (a *App) openWorktreeList(wts []Worktree) {
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
func (a *App) openRemoveWorktreePick(wts []Worktree) {
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
						[]string{"worktree", "remove", "--", path})
				})
		}, nil, nil)
}
