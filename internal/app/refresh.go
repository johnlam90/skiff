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
	"github.com/johnlam90/skiff/internal/icons"
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
func (a *App) refreshTree() {
	if a.tree == nil {
		return
	}
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
	go func() {
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
	}()
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
// any open tabs with disk, refresh git status, and invalidate the
// finder index so a freshly-pulled file shows up everywhere at once.
// The session store piggybacks on the same tick, so a killed terminal
// loses at most ten seconds of tab state instead of the whole session.
// Called from the periodic event and from runCustomAction's success
// path so a Copy-from-remote action's output is visible immediately
// instead of after the next tick.
func (a *App) refreshTreeNow() {
	a.refreshTree()
	a.reconcileOpenTabsWithDisk()
	a.refreshGitStatusAsync()
	a.invalidateFinder()
	a.saveSession()
}

// reconcileOpenTabsWithDisk runs once per background tick. For every
// open tab with a real path it stats the file, compares the on-disk
// mtime to what the tab last knew, and reacts:
//
//   - File missing  → flash once, mark the tab dirty so the user knows.
//   - Disk newer, tab clean → reload the buffer silently, flash success.
//   - Disk newer, tab dirty → leave the buffer alone, flash a warning
//     that saving will overwrite.
//
// Untitled tabs (Path == "") are skipped because there's no disk file to
// reconcile against.
func (a *App) reconcileOpenTabsWithDisk() {
	for _, tab := range a.tabs.Tabs() {
		if tab.Path == "" {
			continue
		}
		info, err := os.Stat(tab.Path)
		if os.IsNotExist(err) {
			if !tab.DiskGone {
				tab.DiskGone = true
				tab.Dirty = true
				a.flash(fmt.Sprintf("%s deleted on disk", filepath.Base(tab.Path)))
			}
			continue
		}
		if err != nil {
			// Permission denied or some other transient stat error — leave
			// the tab as-is rather than spamming the user with a flash.
			continue
		}
		if tab.DiskGone {
			// File reappeared. Force the mtime check below to fire so we
			// either reload or warn about a dirty conflict.
			tab.DiskGone = false
			tab.Mtime = time.Time{}
		}
		if !info.ModTime().After(tab.Mtime) {
			continue // unchanged on disk.
		}
		if tab.Dirty {
			a.flash(fmt.Sprintf("%s changed on disk — your edits will overwrite on save",
				filepath.Base(tab.Path)))
			// Update Mtime so we don't re-flash every tick for the same change.
			tab.Mtime = info.ModTime()
			continue
		}
		if err := tab.Reload(); err != nil {
			a.flash(fmt.Sprintf("%s reload failed: %v", filepath.Base(tab.Path), err))
			continue
		}
		a.flash(fmt.Sprintf("%s reloaded from disk", filepath.Base(tab.Path)))
	}
}
