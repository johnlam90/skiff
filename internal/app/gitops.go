// =============================================================================
// File: internal/app/gitops.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// gitops.go is the write side of skiff's git integration: commit, push,
// pull, fetch, branch switching/creation, stash, and undo-commit —
// druk's source-control verbs, skiff-shaped (≡ menu + Git panel
// buttons, no new key chords). Mutations run one at a time in a
// background goroutine and report back through a gitOpDoneEvent.
// Every verb is a typed method on the repo handle: this file decides
// WHICH verb runs and what to say when it lands; it never assembles an
// argument vector, and it never reads stderr — a refusal arrives as a
// *git.OpError already carrying the advice and the flags the follow-up
// offers (pull-then-push, force delete, force remove) branch on.

package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/overlay"
)

// gitOpDoneEvent carries a finished git mutation back onto the main
// event loop.
type gitOpDoneEvent struct {
	when        time.Time
	label       string // human verb for error titles ("Push", "Commit")
	okFlash     string // success flash text
	err         error  // nil, a *git.OpError, or a refusal such as git.ErrUnsafeRef
	touchesTree bool   // the op may have rewritten working-tree files
}

// When implements tcell.Event.
func (e *gitOpDoneEvent) When() time.Time { return e.when }

// gitOp is one write verb bound to a repo handle — a method value such
// as (*git.Repo).Push, or a closure over the verb's arguments.
type gitOp func(*git.Repo) error

// runGitOp launches op in the background, guarded by the one-at-a-time
// gate (mutations share a repository — racing them helps nobody).
// Returns false when another op is still running. The handle is opened
// here, on the main thread, through readRepo — so a test's git.Fake
// scripts the write side exactly as it scripts the reads — and every
// probe the verb needs to shape its own argv (Push asking for an
// upstream, Switch asking whether the tracking local exists) runs on
// this goroutine, never on the event loop.
func (a *App) runGitOp(label, okFlash string, touchesTree bool, op gitOp) bool {
	if a.gitOpBusy {
		a.flash("Another git operation is still running")
		return false
	}
	a.gitOpBusy = true
	repo := a.readRepo()
	scr := a.screen
	a.safeGo("git-op", func() {
		err := op(repo)
		_ = scr.PostEvent(&gitOpDoneEvent{
			when: time.Now(), label: label, okFlash: okFlash,
			err: err, touchesTree: touchesTree,
		})
	})
	return true
}

// handleGitOpDone lands a finished mutation: report, then refresh
// everything the op may have changed. A rejected push gets druk's
// one-gesture fix offered instead of a wall of stderr; the two
// force ladders (delete branch, remove worktree) get their second,
// explicit confirm. The routing reads the OpError's flags, never its
// text — the git package classified the refusal when it built the
// error.
func (a *App) handleGitOpDone(e *gitOpDoneEvent) {
	a.gitOpBusy = false
	var opErr *git.OpError
	errors.As(e.err, &opErr)
	switch {
	case e.err == nil:
		a.flash(e.okFlash)
	case opErr != nil && opErr.NonFastForward && e.label == "Push":
		a.openConfirm("Push rejected",
			"origin has commits you don't. Pull (merge), then push?",
			func(app *App) { app.doGitPullAndPush() })
	case opErr != nil && opErr.NotMerged && e.label == "Delete branch" && a.gitDeleteTarget != "":
		// `-d` refused an unmerged branch — losing work needs its own
		// explicit yes, so the force delete is a second confirm, never
		// the default.
		name := a.gitDeleteTarget
		a.gitDeleteTarget = ""
		a.openConfirm("Branch not merged",
			name+" has commits that aren't merged anywhere. Force delete?",
			func(app *App) {
				app.runGitOp("Force delete", "Deleted "+name, false,
					func(r *git.Repo) error { return r.DeleteBranch(name, true) })
			})
	case opErr != nil && opErr.WorktreeDirty && e.label == "Remove worktree" && a.gitWorktreeTarget != "":
		// A plain remove refused uncommitted work — force is a second
		// confirm, mirroring the branch-delete ladder.
		path := a.gitWorktreeTarget
		a.gitWorktreeTarget = ""
		a.openConfirm("Worktree not clean",
			path+" has uncommitted or untracked files. Force remove?",
			func(app *App) {
				app.runGitOp("Force remove worktree", "Removed worktree", false,
					func(r *git.Repo) error { return r.WorktreeRemove(path, true) })
			})
	case opErr != nil:
		lines := []string{opErr.Advice, ""}
		lines = append(lines, splitNonEmptyLines(opErr.Output)...)
		a.openInfo(e.label+" failed", lines)
	default:
		// Git never ran: the verb refused its own input (an unsafe ref
		// or path). One line says which.
		a.openInfo(e.label+" failed", []string{e.err.Error()})
	}
	a.refreshGitStatusAsync()
	if e.touchesTree {
		// Pull / stash / checkout rewrite files under open buffers —
		// refreshTreeNow reloads clean tabs, warns on dirty ones, and
		// re-tints everything in one pass. The finder is invalidated
		// here explicitly: these ops can create files in directories
		// the tree never loaded, which the sweep's membership gate
		// cannot see.
		a.refreshTreeNow()
		a.invalidateFinder()
	}
}

// splitNonEmptyLines breaks command output into displayable lines,
// dropping blanks so the info modal stays tight.
func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// flashUnsafeRef reports a ref skiff refused to place on git's argv.
// Flash, not a modal: it is either a hostile clone or a typo, and
// neither deserves ceremony.
func (a *App) flashUnsafeRef(name string) {
	a.flash(fmt.Sprintf("Refusing unsafe branch name %q", name))
}

// safeRef validates a ref that arrived from a picker row, a prompt, or
// the refs a cloned repository shipped, before it reaches git's argv.
// The verbs re-check on their own goroutine; this early, pure-string
// check exists so a typo gets its flash while the prompt is still the
// thing the user is looking at.
func (a *App) safeRef(name string) (string, bool) {
	ref, err := git.SafeRef(name)
	if err != nil {
		a.flashUnsafeRef(name)
		return "", false
	}
	return ref, true
}

// -----------------------------------------------------------------------------
// Commit
// -----------------------------------------------------------------------------

// hasGitChanges gates the commit row: a repo with something to commit.
func (a *App) hasGitChanges() bool {
	return a.hasGitRepo() && a.tree != nil && len(a.tree.DirtyFiles) > 0
}

// checkedChangePaths returns the absolute paths of every change row
// still checked for commit. Rows absent from gitCommitChecks are
// checked — the default is "commit everything", unchecking is the
// deliberate act.
func (a *App) checkedChangePaths() []string {
	var out []string
	for _, row := range a.gitPanel.rows {
		if checked, explicit := a.gitCommitChecks[row.Abs]; explicit && !checked {
			continue
		}
		out = append(out, row.Abs)
	}
	return out
}

// menuGitCommit opens the commit-message prompt for the checked
// changes. The panel's row list is the source of truth — rebuild it
// first so a menu-only flow (panel never opened) still commits.
func (a *App) menuGitCommit() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	if a.diffBase != "" {
		// The change list is vs the compare base right now; the index is
		// always built against HEAD (druk's rule). Committing a base-
		// relative file list would surprise — make the mode explicit.
		a.flash("Comparing against " + a.diffBase + " — switch back to HEAD to commit")
		return
	}
	a.rebuildGitChangesRows()
	paths := a.checkedChangePaths()
	if len(paths) == 0 {
		a.flash("Nothing checked to commit")
		return
	}
	hint := fmt.Sprintf("%d file(s)", len(paths))
	a.openPrompt("Commit message", hint, "", func(app *App, msg string) {
		app.doGitCommit(paths, msg)
	})
}

// doGitCommit runs the path-scoped commit for paths.
func (a *App) doGitCommit(paths []string, message string) {
	ok := fmt.Sprintf("Committed %d file(s)", len(paths))
	a.runGitOp("Commit", ok, false, func(r *git.Repo) error { return r.Commit(paths, message) })
}

// -----------------------------------------------------------------------------
// Push / pull / fetch
// -----------------------------------------------------------------------------

// menuGitPush pushes the current branch. Whether this is the branch's
// first push (and so needs --set-upstream) is the verb's own question,
// answered on the op goroutine — nothing here asks git anything.
func (a *App) menuGitPush() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.runGitOp("Push", "Pushed", false, (*git.Repo).Push)
}

// doGitPullAndPush is the accepted "Push rejected" offer: merge-pull,
// then push — one operation, stopping at the pull if it conflicts.
func (a *App) doGitPullAndPush() {
	a.runGitOp("Pull & push", "Pulled and pushed", true, (*git.Repo).PullAndPush)
}

// menuGitPull fast-forwards from origin. A real merge wants an editor
// and a conflict UI — --ff-only fails fast instead, and the OpError's
// advice tells the user why.
func (a *App) menuGitPull() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.runGitOp("Pull", "Pulled", true, func(r *git.Repo) error { return r.Pull(true) })
}

// menuGitFetch refreshes the remote-tracking refs (and with them the
// status bar's ↑↓ counts) without touching the working tree.
func (a *App) menuGitFetch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.runGitOp("Fetch", "Fetched", false, (*git.Repo).Fetch)
}

// -----------------------------------------------------------------------------
// Branches
// -----------------------------------------------------------------------------

// branchListEvent delivers an asynchronously-collected branch list to
// the main loop, tagged with which picker asked for it.
type branchListEvent struct {
	when    time.Time
	purpose string // "switch" | "merge" | "delete"
	names   []string
}

// When implements tcell.Event.
func (e *branchListEvent) When() time.Time { return e.when }

// requestBranchList collects the branch list off the UI thread — on a
// network-mounted repo listing refs can stall, and a menu click must
// never freeze the event loop — then reopens the flow via
// handleBranchList. Best-effort like every read here: a failing git
// yields no names, and the picker explains the empty list.
func (a *App) requestBranchList(purpose string) {
	repo := a.readRepo()
	scr := a.screen
	a.safeGo("git-branch-list", func() {
		names, _ := repo.Branches()
		_ = scr.PostEvent(&branchListEvent{when: time.Now(), purpose: purpose, names: names})
	})
}

// handleBranchList routes a collected branch list to the picker that
// asked for it.
func (a *App) handleBranchList(e *branchListEvent) {
	switch e.purpose {
	case "switch":
		a.openSwitchBranchPick(e.names)
	case "merge":
		a.openMergeBranchPick(e.names)
	case "delete":
		a.openDeleteBranchPick(e.names)
	case "base":
		a.openComparePick(e.names)
	case "worktree":
		a.openWorktreeBranchPick(e.names)
	}
}

// openComparePick sets the editor-wide compare base (druk's diffBase):
// every mark — tree tint, gutter, panel list, diffs — follows the
// picked ref until HEAD is picked again. Committing stays HEAD-scoped,
// so it is gated off while a base is active (see menuGitCommit).
func (a *App) openComparePick(names []string) {
	items := make([]listPickItem, 0, len(names)+1)
	items = append(items, listPickItem{Label: "HEAD (working default)", Current: a.diffBase == ""})
	for _, n := range names {
		it := listPickItem{Label: n, Current: n == a.diffBase}
		if strings.ContainsRune(n, '/') {
			it.Tag = "remote"
		}
		items = append(items, it)
	}
	a.openListPick("Compare against", items,
		func(app *App, i int) {
			if i == 0 {
				app.setDiffBase("")
				return
			}
			app.setDiffBase(names[i-1])
		}, nil, nil)
}

// setDiffBase applies a new compare base and re-points every mark.
func (a *App) setDiffBase(base string) {
	if base == a.gitSnap.Branch {
		base = "" // comparing a branch against itself is just HEAD
	}
	if base != "" {
		// The base is pasted onto the argv of every diff the editor
		// runs — tree tint, gutter, panel list, diff view — and stays
		// there until it's cleared. Validate once, here, instead of at
		// four call sites that would each have to remember.
		if _, ok := a.safeRef(base); !ok {
			return
		}
	}
	a.diffBase = base
	if base == "" {
		a.flash("Comparing against HEAD")
	} else {
		a.flash("Comparing against " + base)
	}
	a.refreshGitStatusAsync()
}

// menuGitCompareAgainst is the extras-popup entry point.
func (a *App) menuGitCompareAgainst() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestBranchList("base")
}

// menuGitSwitchBranch opens a select of every branch. Picking a
// remote-tracking name creates (or reuses) the local branch that
// tracks it — checking out origin/x directly would only detach HEAD.
func (a *App) menuGitSwitchBranch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestBranchList("switch")
}

// openSwitchBranchPick opens the switch picker for a collected list.
func (a *App) openSwitchBranchPick(names []string) {
	if len(names) < 2 {
		a.flash("No other branches")
		return
	}
	a.openListPick("Switch branch", branchPickItems(names, a.gitSnap.Branch),
		func(app *App, i int) { app.doGitSwitchBranch(names[i]) }, nil, nil)
}

// branchPickItems shapes branch names for the list picker: the current
// branch wears the ● marker and remote-tracking spellings a dimmed tag.
func branchPickItems(names []string, current string) []listPickItem {
	items := make([]listPickItem, len(names))
	for i, n := range names {
		items[i] = listPickItem{Label: n, Current: n == current}
		if strings.ContainsRune(n, '/') {
			items[i].Tag = "remote"
		}
	}
	return items
}

// doGitSwitchBranch checks out name. The tracking rule for remote picks
// (first switch creates local x tracking origin/x, later ones just move
// to x) and the existence probe it needs live in git.Repo.Switch, on
// the op goroutine. The early safeRef is the flash for a hostile name;
// the verb refuses it again regardless.
func (a *App) doGitSwitchBranch(name string) {
	if name == "" || name == a.gitSnap.Branch {
		return
	}
	if _, ok := a.safeRef(name); !ok {
		return
	}
	if _, ok := a.safeRef(localBranchName(name)); !ok {
		return
	}
	a.runGitOp("Switch branch", "On "+localBranchName(name), true,
		func(r *git.Repo) error { return r.Switch(name) })
}

// localBranchName strips the remote prefix from a remote-tracking
// spelling ("origin/fix" → "fix"); local names pass through.
func localBranchName(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// menuGitNewBranch prompts for a name and creates + switches to it.
func (a *App) menuGitNewBranch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.openPrompt("New branch", "created from "+a.gitSnap.Branch, "", func(app *App, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := app.safeRef(name); !ok {
			return
		}
		app.runGitOp("New branch", "On "+name, false,
			func(r *git.Repo) error { return r.NewBranch(name, "") })
	})
}

// -----------------------------------------------------------------------------
// Stash / undo commit / extras popup
// -----------------------------------------------------------------------------

// menuGitStash stashes the working tree, new files included (-u —
// "stash my changes" from an editor means the file just created too).
func (a *App) menuGitStash() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.runGitOp("Stash", "Stashed working tree", true, (*git.Repo).StashPush)
}

// menuGitStashPop pops the most recent stash back onto the tree.
func (a *App) menuGitStashPop() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.runGitOp("Pop stash", "Stash popped", true, (*git.Repo).StashPop)
}

// menuGitUndoCommit soft-resets HEAD~1 behind a confirm: the commit
// disappears, its changes stay staged in the working tree.
func (a *App) menuGitUndoCommit() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.openConfirm("Undo last commit",
		"Remove the last commit? Its changes stay in your working tree. "+
			"If it was already pushed, the next push will need a merge.",
		func(app *App) {
			app.runGitOp("Undo commit", "Last commit undone", false, (*git.Repo).UndoCommit)
		})
}

// menuGitMergeBranch picks any other branch and merges it into the
// current one. --no-edit lives in the runner's environment contract; a
// conflicted merge stops with git's own reason and the tree left
// mid-merge, same as druk.
func (a *App) menuGitMergeBranch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestBranchList("merge")
}

// openMergeBranchPick opens the merge picker for a collected list.
func (a *App) openMergeBranchPick(all []string) {
	names := otherNames(all, a.gitSnap.Branch, false)
	if len(names) == 0 {
		a.flash("No other branches to merge")
		return
	}
	a.openListPick("Merge into "+a.gitSnap.Branch, branchPickItems(names, ""),
		func(app *App, i int) {
			name := names[i]
			if _, ok := app.safeRef(name); !ok {
				return
			}
			app.runGitOp("Merge", "Merged "+name, true,
				func(r *git.Repo) error { return r.Merge(name) })
		}, nil, nil)
}

// menuGitRenameBranch renames the current branch in place.
func (a *App) menuGitRenameBranch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	old := a.gitSnap.Branch
	a.openPrompt("Rename branch", "renames "+old, old, func(app *App, name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == old {
			return
		}
		if _, ok := app.safeRef(name); !ok {
			return
		}
		app.runGitOp("Rename branch", "Renamed to "+name, false,
			func(r *git.Repo) error { return r.RenameBranch(old, name) })
	})
}

// menuGitDeleteBranch picks a local branch (never the current one) and
// deletes it behind a confirm. An unmerged branch fails `-d`; the done
// handler then offers the force delete explicitly — see handleGitOpDone.
func (a *App) menuGitDeleteBranch() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.requestBranchList("delete")
}

// openDeleteBranchPick opens the delete picker for a collected list.
func (a *App) openDeleteBranchPick(all []string) {
	names := otherNames(all, a.gitSnap.Branch, true)
	if len(names) == 0 {
		a.flash("No other local branches")
		return
	}
	a.openListPick("Delete branch", branchPickItems(names, ""),
		func(app *App, i int) {
			name := names[i]
			if _, ok := app.safeRef(name); !ok {
				return
			}
			app.openConfirm("Delete branch",
				"Delete "+name+"? Unmerged work on it would be lost.",
				func(app2 *App) {
					app2.gitDeleteTarget = name
					app2.runGitOp("Delete branch", "Deleted "+name, false,
						func(r *git.Repo) error { return r.DeleteBranch(name, false) })
				})
		}, nil, nil)
}

// otherNames filters a collected branch list: drop the current branch,
// and with localOnly the remote-tracking spellings too (you can't
// `branch -d` those).
func otherNames(all []string, current string, localOnly bool) []string {
	var out []string
	for _, n := range all {
		if n == current {
			continue
		}
		if localOnly && strings.ContainsRune(n, '/') {
			continue
		}
		out = append(out, n)
	}
	return out
}

// menuGitExtras opens the less-used git verbs as a small popup so the
// main menu doesn't grow five more rows. Reuses the context-menu modal
// with the tree root as a harmless anchor node.
func (a *App) menuGitExtras() {
	a.closeMenu()
	if !a.hasGitRepo() {
		return
	}
	a.openGitExtras(a.width/2-contextMenuWidth/2, a.height/2-4)
}

// openGitExtras opens the extras popup anchored near (x, y) — shared by
// the ≡ menu row (centered) and the Git panel's ⋯ button (anchored).
func (a *App) openGitExtras(x, y int) {
	a.closeAllModals()
	a.openPopup([]overlay.PopupItem{
		{Label: "Fetch", OnPick: func() { a.menuGitFetch() }},
		{Label: "Compare against…", OnPick: func() { a.menuGitCompareAgainst() }},
		{Divider: true},
		{Label: "New branch…", OnPick: func() { a.menuGitNewBranch() }},
		{Label: "Merge branch…", OnPick: func() { a.menuGitMergeBranch() }},
		{Label: "Rename branch…", OnPick: func() { a.menuGitRenameBranch() }},
		{Label: "Delete branch…", OnPick: func() { a.menuGitDeleteBranch() }},
		{Divider: true},
		{Label: "New worktree…", OnPick: func() { a.menuGitNewWorktree() }},
		{Label: "List worktrees", OnPick: func() { a.menuGitListWorktrees() }},
		{Label: "Remove worktree…", OnPick: func() { a.menuGitRemoveWorktree() }},
		{Divider: true},
		{Label: "Stash changes", OnPick: func() { a.menuGitStash() }},
		{Label: "Pop stash", OnPick: func() { a.menuGitStashPop() }},
		{Divider: true},
		{Label: "Undo last commit", OnPick: func() { a.menuGitUndoCommit() }},
		{Label: "Commit history", OnPick: func() { a.menuCommitHistory() }},
	}, x, y)
}
