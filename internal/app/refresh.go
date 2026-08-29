// =============================================================================
// File: internal/app/refresh.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// refresh.go is the editor's "resync with the outside world" layer: the
// user config and custom actions read at startup, the background tick that
// re-reads the file tree, and the three-way reconciliation that decides
// what to do when a file changed on disk under an open tab.
//
// The tick runs on a goroutine but only ever posts a treeRefreshEvent —
// the actual rescan happens on the main loop, because everything it
// touches is UI state.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/session"
	"github.com/johnlam90/skiff/internal/userconfig"
)

// leaderExpiryEvent is posted shortly after an armed Esc's window
// closes. It carries no action of its own — reaching the event loop is
// the point, because Run redraws after every event and that repaint
// removes the status bar's "Esc…" tag.
type leaderExpiryEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *leaderExpiryEvent) When() time.Time { return e.when }

// treeRefreshEvent is the custom tcell event the background tree-refresh
// goroutine posts every treeRefreshInterval. The main loop reacts by
// asking the file tree to re-read every loaded directory.
type treeRefreshEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *treeRefreshEvent) When() time.Time { return e.when }

// treeScanEvent carries a finished background disk sweep onto the main
// event loop: the directory listings the file tree should adopt and the
// Stat results the open tabs should be reconciled against. Same
// goroutine → custom event pattern as gitStatusEvent — the sweep reads
// disk, the main loop mutates the node graph and the tabs. gen pins the
// sweep to the tree generation it started from; see handleTreeScan.
type treeScanEvent struct {
	when time.Time
	gen  int
	dirs []filetree.DirScan
	tabs []tabProbe
}

// When satisfies the tcell.Event interface.
func (e *treeScanEvent) When() time.Time { return e.when }

// tabProbe is one open tab's on-disk state as observed by a background
// sweep. err is carried verbatim rather than pre-interpreted so the main
// thread runs exactly the same os.IsNotExist / other-error branches the
// synchronous reconcile always did.
type tabProbe struct {
	path  string
	mtime time.Time
	err   error
}

// loadCustomActions reads the user's actions.json (if any) and stores
// the parsed list on the App. Failures are surfaced as a status flash
// so a typo in the config file isn't silently swallowed, but they
// don't block startup — the editor still opens with no custom actions
// in the menu.
func (a *App) loadCustomActions() {
	path := customactions.DefaultPath()
	actions, err := customactions.Load(path)
	if err != nil {
		a.flash("custom actions: " + err.Error())
		return
	}
	a.customActions = actions
}

// loadUserConfig reads ~/.config/skiff/config.json (if any),
// resolves the Nerd Fonts auto/on/off mode to a concrete bool via
// icons.Resolve, and stamps the result onto the file tree so the
// next render starts drawing glyphs (or doesn't). A malformed
// config flashes a status message but never blocks startup — the
// editor falls back to Defaults() and keeps going.
func (a *App) loadUserConfig() {
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil {
		a.flash("config: " + err.Error())
	}
	if a.tree != nil {
		a.tree.IconsEnabled = icons.Resolve(cfg.Icons)
		// filetree.New already read the root with filtering on (the
		// config default), so only a config that disagrees costs a
		// re-read.
		if a.tree.HideIgnored != cfg.Gitignore {
			a.tree.HideIgnored = cfg.Gitignore
			a.tree.Refresh()
		}
	}
	if cfg.Theme != "" {
		a.applyTheme(cfg.Theme, false)
	}
	// Both constructors call this before any tab exists, so stamping
	// the preference on the App is enough — tab-open paths copy it.
	a.wrapOn = cfg.Wrap
}

// refreshTree calls tree.Refresh when the file tree exists, and is a
// no-op in single-file mode. File operations (create / rename / delete)
// call this after touching the disk so callers don't have to nil-check
// every site. The git-status and finder refreshes that usually
// accompany it already guard themselves internally.
//
// The generation bump retires any background sweep already in flight:
// its listing was read before this mutation, so applying it afterwards
// would put the file the user just deleted back on screen for a whole
// tick. See handleTreeScan.
func (a *App) refreshTree() {
	a.treeScanGen++
	if a.tree == nil {
		return
	}
	a.tree.SetOpenFiles(a.openTabDiskPaths())
	a.tree.Refresh()
}

// startTreeRefresh launches a goroutine that posts a treeRefreshEvent every
// treeRefreshInterval. The main event loop reacts by calling tree.Refresh,
// which keeps the sidebar in sync with on-disk changes from outside the
// editor (git, mv, another tmux pane, etc.).
func (a *App) startTreeRefresh() {
	a.treeRefreshStop = make(chan struct{})
	stop := a.treeRefreshStop
	scr := a.screen
	a.safeGo("tree-refresh-tick", func() {
		ticker := time.NewTicker(treeRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				_ = scr.PostEvent(&treeRefreshEvent{when: t})
			}
		}
	})
}

// stopTreeRefresh signals the background tree-refresh goroutine to exit.
// Safe to call multiple times.
func (a *App) stopTreeRefresh() {
	if a.treeRefreshStop != nil {
		close(a.treeRefreshStop)
		a.treeRefreshStop = nil
	}
}

// refreshTreeNow re-runs the same refresh pipeline the 10s timer
// fires: rescan the file tree (preserving expansion state), reconcile
// any open tabs with disk, and refresh git status. The session store
// piggybacks on the same tick, so a killed terminal loses at most ten
// seconds of tab state instead of the whole session. Called from the
// periodic event and from the git-op / custom-action / project-replace
// landing paths so their output is visible immediately instead of
// after the next tick.
//
// The finder is deliberately NOT invalidated here any more: the sweep
// itself reports whether any directory's membership changed, and
// handleTreeScan reindexes only then — so a quiet tick stops re-running
// `git ls-files` (or a full walk) on a project nobody touched. Callers
// whose changes can land in directories the tree never loaded (git
// ops, custom actions) invalidate explicitly at their own call sites,
// because the scan gate cannot see those.
//
// Every stage is off-thread now. The tick used to run a recursive
// ReadDir of the whole loaded tree, a Stat per open tab, and a session
// write inside the event handler, which stuttered visibly every ten
// seconds on a large tree over NFS.
func (a *App) refreshTreeNow() {
	a.refreshTreeAsync()
	a.refreshGitStatusAsync()
}

// refreshTreeAsync starts one background disk sweep: the session write,
// the ReadDir walk of every loaded directory, and one Stat per open tab.
// The goroutine gets nothing but path strings and an unaliased session
// payload captured here on the main thread — it never reads or writes
// App, Tab, or Node state, and handleTreeScan does all the mutating.
// Overlapping ticks coalesce into a single follow-up sweep exactly the
// way refreshGitStatusAsync does, so a burst of triggers costs at most
// two sweeps rather than one each.
//
// The session write rides along rather than getting its own goroutine so
// that treeScanInFlight covers the whole sweep: one flag, and a caller
// that has seen the treeScanEvent knows nothing is still touching disk
// on this tick's behalf. It goes first because it is small and bounded
// while the walk is neither, and delaying a durability write behind a
// slow NFS walk widens the window where a killed terminal loses tab
// state.
func (a *App) refreshTreeAsync() {
	if a.treeScanInFlight {
		a.treeScanQueued = true
		return
	}
	a.treeScanInFlight = true
	paths := a.openTabDiskPaths()
	var dirs []string
	if a.tree != nil {
		// The sweep's listings are filtered against this set when they
		// land, so it has to be current before the work list is taken —
		// a tab opened inside an ignored directory must not blink out of
		// the sidebar on the next tick.
		a.tree.SetOpenFiles(paths)
		dirs = a.tree.LoadedDirs()
	}
	gen := a.treeScanGen
	scr := a.screen
	// The session payload is captured here for the same reason the path
	// lists are: it reads tabs, cursors, and the tree's expanded set.
	// captureSession builds fresh slices nothing else aliases, so only
	// the write itself crosses over. Single-file mode has no project to
	// remember, hence the nil.
	var snap *session.Project
	if a.tree != nil {
		p := a.captureSession()
		p.SavedAt = time.Now()
		snap = &p
	}
	root := a.rootDir
	a.safeGo("tree-scan", func() {
		if snap != nil {
			_ = session.Save(root, *snap)
		}
		_ = scr.PostEvent(&treeScanEvent{
			when: time.Now(),
			gen:  gen,
			dirs: filetree.ScanDirs(dirs),
			tabs: probeOpenTabs(paths),
		})
	})
}

// handleTreeScan applies a finished background sweep on the main thread
// and starts the follow-up sweep if more triggers arrived while this one
// was in flight.
//
// A generation mismatch means a main-thread tree mutation — a create,
// rename, delete, or paste, each of which refreshes the tree
// synchronously — landed after the sweep read the disk. Its listing
// predates that change, so applying it would resurrect the file the user
// just deleted until the next tick. Drop it; the mutation already
// refreshed the tree correctly.
//
// The finder reindexes only when ApplyScan reports a membership change
// — names actually came or went, or a .gitignore moved. Tab
// reconciliation is deliberately outside the gate: it consumes the
// per-tab Stat probes, never the directory listings, so external-edit
// detection on open tabs cannot be starved by a quiet tree.
func (a *App) handleTreeScan(e *treeScanEvent) {
	a.treeScanInFlight = false
	if e.gen == a.treeScanGen {
		if a.tree != nil && a.tree.ApplyScan(e.dirs) {
			a.invalidateFinder()
		}
		a.applyTabProbes(e.tabs)
	}
	if a.treeScanQueued {
		a.treeScanQueued = false
		a.refreshTreeAsync()
	}
}

// openTabDiskPaths returns the path of every open tab backed by a real
// file — the Stat list for a disk sweep. Untitled tabs (Path == "") have
// no disk file to reconcile against; image tabs stay in, because they
// reload from disk too.
func (a *App) openTabDiskPaths() []string {
	paths := make([]string, 0, a.tabs.Len())
	for _, tab := range a.tabs.Tabs() {
		if tab == nil || tab.Path == "" {
			continue
		}
		paths = append(paths, tab.Path)
	}
	return paths
}

// probeOpenTabs stats each path and records what it found. One syscall
// per open tab — cheap on a local disk, not cheap over NFS, and it used
// to run on the event thread every ten seconds. Touches no Tab, so it is
// safe on a background goroutine.
func probeOpenTabs(paths []string) []tabProbe {
	probes := make([]tabProbe, 0, len(paths))
	for _, p := range paths {
		pr := tabProbe{path: p}
		info, err := os.Stat(p)
		if err != nil {
			pr.err = err
		} else {
			pr.mtime = info.ModTime()
		}
		probes = append(probes, pr)
	}
	return probes
}

// applyTabProbes runs the three-way external-change reconciliation over
// a sweep's Stat results. Tabs opened after the sweep started aren't in
// the map and are skipped — the next tick covers them.
func (a *App) applyTabProbes(probes []tabProbe) {
	if len(probes) == 0 {
		return
	}
	byPath := make(map[string]tabProbe, len(probes))
	for _, p := range probes {
		byPath[p.path] = p
	}
	for _, tab := range a.tabs.Tabs() {
		if tab == nil || tab.Path == "" {
			continue
		}
		if p, ok := byPath[tab.Path]; ok {
			a.reconcileTab(tab, p)
		}
	}
}

// reconcileTab decides what one open tab should do about what a Stat
// found:
//
//   - File missing  → flash once, mark the tab DiskGone so the user
//     knows. Dirty is untouched: the buffer has no edits of its own, so
//     a later delete+recreate must still be able to reload silently
//     instead of reading as a conflict with edits that don't exist.
//   - Disk newer, tab clean → reload the buffer silently, flash success.
//     This is also how a DiskGone tab resolves once the file reappears.
//   - Disk newer, tab dirty → a real conflict: prompt (Keep mine /
//     Reload / Diff) and leave a marker on the tab until it is resolved.
//
// The clean-tab reload uses ReloadKeepHistory, not plain Reload: the user
// never asked for this reload (a background git checkout or `make` wrote
// the file), so it must not erase the undo history of edits the user made
// before those bytes were last saved.
//
// The reload's ReadFile stays synchronous on purpose: it only fires when
// a file actually changed under a clean tab, which is an event rather
// than a per-tick cost, and reading the bytes off-thread would need a
// second "load from memory" entry point into editor.Tab that could drift
// from Tab.Reload.
func (a *App) reconcileTab(tab *editor.Tab, p tabProbe) {
	if os.IsNotExist(p.err) {
		if !tab.DiskGone {
			// Dirty is deliberately NOT set here: the buffer has no user
			// edits, so marking it dirty would make a later delete+
			// recreate (a `git checkout`, `git stash pop`, or a build
			// tool spanning two ticks) look like a real unsaved-edits
			// conflict when the file reappears. DiskGone alone carries
			// "needs attention" for every consumer that means that
			// rather than "has edits" — see the Dirty||DiskGone gates in
			// tabops.go, actions.go and draw.go.
			tab.DiskGone = true
			a.flash(fmt.Sprintf("%s deleted on disk", filepath.Base(tab.Path)))
		}
		return
	}
	if p.err != nil {
		// Permission denied or some other transient stat error — leave
		// the tab as-is rather than spamming the user with a flash.
		return
	}
	if tab.DiskGone {
		// File reappeared. Force the mtime check below to fire so we
		// either reload or warn about a dirty conflict.
		tab.DiskGone = false
		tab.Mtime = time.Time{}
	}
	// A tab that stopped being dirty resolved its conflict by
	// being saved; drop the marker so the status bar stops warning.
	if !tab.Dirty {
		a.clearDiskConflict(tab.Path)
	}
	if !p.mtime.After(tab.Mtime) {
		return // unchanged on disk.
	}
	if tab.Dirty {
		// Two writers, one file: the user has to choose, because
		// every automatic answer here loses somebody's work. Only
		// prompt once per disk revision — openDiskConflict records
		// the mtime it warned about, and Mtime moves with it so a
		// later external write asks again.
		if a.diskConflicts[tab.Path].Equal(p.mtime) {
			tab.Mtime = p.mtime
			return
		}
		// Don't yank an overlay the user is already working in out
		// from under them; the next tick will prompt instead. Tab
		// Mtime is left alone so that tick still sees the change.
		if a.anyModalOpen() {
			return
		}
		a.openDiskConflict(tab, p.mtime)
		tab.Mtime = p.mtime
		return
	}
	if err := tab.ReloadKeepHistory(); err != nil {
		a.flash(fmt.Sprintf("%s reload failed: %v", filepath.Base(tab.Path), err))
		return
	}
	a.clearDiskConflict(tab.Path)
	a.flash(fmt.Sprintf("%s reloaded from disk", filepath.Base(tab.Path)))
}
