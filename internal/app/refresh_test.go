// =============================================================================
// File: internal/app/refresh_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for refresh.go — the startup config reads, the background tree
// tick's lifecycle, and the three-way disk reconciliation. The reconcile
// cases are the ones that matter: they are the only place the editor
// touches a buffer the user did not ask it to touch, so each branch
// (missing / newer-and-clean / newer-and-dirty) gets pinned separately.
//
// Disk mtimes are forced with os.Chtimes rather than a sleep — filesystem
// timestamp granularity is coarse enough that a same-instant rewrite would
// otherwise flake between "unchanged" and "newer".

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/icons"
)

// TestRefreshTree_PicksUpFileCreatedBehindTheEditor is the whole point of
// the background tick: a file written by git or another pane has to appear
// in the sidebar without the user doing anything.
func TestRefreshTree_PicksUpFileCreatedBehindTheEditor(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)

	before := len(a.tree.Root.Children)
	if err := os.WriteFile(filepath.Join(dir, "appeared.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.refreshTree()

	if got := len(a.tree.Root.Children); got != before+1 {
		t.Fatalf("child count: got %d, want %d", got, before+1)
	}
	var found bool
	for _, c := range a.tree.Root.Children {
		if c.Name == "appeared.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("refreshTree should surface a file created behind the editor")
	}
}

// TestRefreshTree_SingleFileModeIsNoop guards the nil-tree contract:
// single-file mode deliberately never builds a tree, and every file
// operation calls refreshTree unconditionally, so it must not panic.
func TestRefreshTree_SingleFileModeIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tree = nil
	a.refreshTree() // must not panic
}

// TestStartTreeRefresh_StopIsIdempotent pins the goroutine lifecycle:
// starting arms a stop channel, stopping clears it, and a second stop is
// safe. Close runs stopTreeRefresh on every exit path, including ones that
// never started the ticker, so a double close would panic on quit.
func TestStartTreeRefresh_StopIsIdempotent(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.startTreeRefresh()
	if a.treeRefreshStop == nil {
		t.Fatal("startTreeRefresh should arm the stop channel")
	}
	a.stopTreeRefresh()
	if a.treeRefreshStop != nil {
		t.Fatal("stopTreeRefresh should clear the stop channel")
	}
	a.stopTreeRefresh() // must not panic on a second call
}

// TestLoadCustomActions_ReadsConfiguredActions covers the happy path: a
// well-formed actions.json lands on the App so menuLayout can splice the
// rows into the action menu.
func TestLoadCustomActions_ReadsConfiguredActions(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	path := filepath.Join(cfgDir, "skiff", "actions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"actions":[{"label":"Copy to remote","command":"true"}]}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, t.TempDir())
	a.loadCustomActions()

	if len(a.customActions) != 1 || a.customActions[0].Label != "Copy to remote" {
		t.Fatalf("custom actions: got %+v", a.customActions)
	}
}

// TestLoadCustomActions_MalformedFileFlashesInsteadOfBlocking is the
// "don't swallow, don't block" contract: a typo in actions.json has to be
// visible in the status bar, but the editor still opens with no custom
// actions rather than refusing to start.
func TestLoadCustomActions_MalformedFileFlashesInsteadOfBlocking(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	path := filepath.Join(cfgDir, "skiff", "actions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"actions":[`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, t.TempDir())
	a.loadCustomActions()

	if !strings.HasPrefix(a.statusMsg, "custom actions:") {
		t.Fatalf("expected a custom-actions flash, got %q", a.statusMsg)
	}
	if len(a.customActions) != 0 {
		t.Fatalf("malformed config should leave no actions, got %+v", a.customActions)
	}
}

// TestLoadUserConfig_StampsIconsDecisionOnTree pins the one-source-of-truth
// rule for glyphs: loadUserConfig resolves auto/on/off once and stamps the
// answer on the tree, which is what iconsOn (and therefore the tab bar)
// reads. An explicit on/off must bypass Nerd Font detection entirely.
func TestLoadUserConfig_StampsIconsDecisionOnTree(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"off", false},
		{"on", true},
	} {
		cfgDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", cfgDir)
		path := filepath.Join(cfgDir, "skiff", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(`{"icons":"`+tc.mode+`"}`), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}

		a := newTestApp(t, t.TempDir())
		a.loadUserConfig()

		if a.tree.IconsEnabled != tc.want {
			t.Fatalf("icons=%q: tree.IconsEnabled got %v, want %v",
				tc.mode, a.tree.IconsEnabled, tc.want)
		}
		if a.iconsOn() != tc.want {
			t.Fatalf("icons=%q: iconsOn got %v, want %v", tc.mode, a.iconsOn(), tc.want)
		}
	}
}

// TestLoadUserConfig_MalformedFileFlashesAndFallsBack: a broken
// config.json must not leave the editor on zero values — soft wrap in
// particular defaults to on, and silently flipping it off would look like
// a bug in the editor rather than a typo in the file.
func TestLoadUserConfig_MalformedFileFlashesAndFallsBack(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	path := filepath.Join(cfgDir, "skiff", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"icons":"sometimes"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := newTestApp(t, t.TempDir())
	a.wrapOn = false
	a.loadUserConfig()

	if !strings.HasPrefix(a.statusMsg, "config:") {
		t.Fatalf("expected a config flash, got %q", a.statusMsg)
	}
	if !a.wrapOn {
		t.Fatal("malformed config should fall back to Defaults(), which wraps")
	}
	if a.tree.IconsEnabled != icons.Resolve("auto") {
		t.Fatal("malformed config should fall back to the auto icons decision")
	}
}

// TestReconcileOpenTabsWithDisk_ReloadsCleanTab is the silent-reload
// branch: nothing of the user's is at risk, so an external change (git
// checkout, another pane) should just appear.
func TestReconcileOpenTabsWithDisk_ReloadsCleanTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	writeNewer(t, target, "two", tab.Mtime)
	a.reconcileOpenTabsWithDisk()

	if got := tab.Buffer.String(); got != "two" {
		t.Fatalf("buffer: got %q, want %q", got, "two")
	}
	if !strings.Contains(a.statusMsg, "reloaded from disk") {
		t.Fatalf("expected a reload flash, got %q", a.statusMsg)
	}
}

// TestReconcileOpenTabsWithDisk_DirtyTabKeepsEdits is the branch that
// protects unsaved work: a dirty buffer is never overwritten by the disk,
// the user just gets warned that saving will clobber the other change.
func TestReconcileOpenTabsWithDisk_DirtyTabKeepsEdits(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.Buffer.Lines = []string{"mine"}
	tab.Dirty = true

	writeNewer(t, target, "theirs", tab.Mtime)
	a.reconcileOpenTabsWithDisk()

	if got := tab.Buffer.String(); got != "mine" {
		t.Fatalf("dirty buffer was overwritten: got %q", got)
	}
	if !strings.Contains(a.statusMsg, "will overwrite on save") {
		t.Fatalf("expected an overwrite warning, got %q", a.statusMsg)
	}

	// The warning must not repeat every tick for the same disk change.
	a.statusMsg = ""
	a.reconcileOpenTabsWithDisk()
	if a.statusMsg != "" {
		t.Fatalf("re-flashed for an already-reported change: %q", a.statusMsg)
	}
}

// TestReconcileOpenTabsWithDisk_DeletedFileMarksTab covers the missing
// branch: the tab is marked gone and dirty so the buffer reads as the only
// surviving copy, and the flash fires exactly once.
func TestReconcileOpenTabsWithDisk_DeletedFileMarksTab(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove: %v", err)
	}
	a.reconcileOpenTabsWithDisk()

	if !tab.DiskGone || !tab.Dirty {
		t.Fatalf("deleted file should mark the tab gone+dirty: gone=%v dirty=%v",
			tab.DiskGone, tab.Dirty)
	}
	if !strings.Contains(a.statusMsg, "deleted on disk") {
		t.Fatalf("expected a deletion flash, got %q", a.statusMsg)
	}

	a.statusMsg = ""
	a.reconcileOpenTabsWithDisk()
	if a.statusMsg != "" {
		t.Fatalf("deletion flash repeated on a later tick: %q", a.statusMsg)
	}
}

// TestReconcileOpenTabsWithDisk_SkipsUntitledTab: an untitled buffer has
// no disk file to stat, so the sweep must pass over it rather than
// stat("") its way into a flash on every tick.
func TestReconcileOpenTabsWithDisk_SkipsUntitledTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.tabs.Append(&editor.Tab{Buffer: editor.NewBuffer("scratch")})

	a.reconcileOpenTabsWithDisk()

	if a.statusMsg != "" {
		t.Fatalf("untitled tab should be skipped silently, got %q", a.statusMsg)
	}
}

// writeNewer rewrites path with body and forces its mtime past base, so
// the reconcile sweep reliably sees "disk is newer" regardless of the
// filesystem's timestamp resolution.
func writeNewer(t *testing.T, path, body string, base time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := base.Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}
