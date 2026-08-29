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

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/finder"
	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/session"
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

// TestReconcileTab_SilentReloadKeepsUndo pins the fix for the bug where
// a background git checkout or `make` erased the undo history of a file
// the user never touched: the clean-buffer silent reload branch of
// reconcileTab was never something the user asked for, so — like
// format-on-save — it must keep history instead of wiping it.
func TestReconcileTab_SilentReloadKeepsUndo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.InsertString("edited-")
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	preReload := tab.Buffer.String()

	writeNewer(t, target, "two", tab.Mtime)
	a.reconcileOpenTabsWithDisk()

	if got := tab.Buffer.String(); got != "two" {
		t.Fatalf("buffer: got %q, want %q", got, "two")
	}
	if !tab.CanUndo() {
		t.Fatal("expected undo history to survive the silent background reload")
	}
	if !tab.Undo() {
		t.Fatal("Undo should succeed")
	}
	if got := tab.Buffer.String(); got != preReload {
		t.Fatalf("after undo = %q, want %q", got, preReload)
	}
}

// TestReconcileOpenTabsWithDisk_DirtyTabKeepsEdits is the branch that
// protects unsaved work: a dirty buffer is never overwritten by the
// disk. The one-line "will overwrite on save" flash this used to assert
// was replaced by the conflict prompt (see conflict.go) — a flash is the
// wrong instrument for a decision — so what's pinned here is the
// invariant that survived the change: reconciliation itself never
// touches the bytes.
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
	if !a.tabDiskConflict(tab) {
		t.Fatal("a dirty tab whose file changed should be marked in conflict")
	}
}

// TestReconcileOpenTabsWithDisk_DeletedFileMarksTab covers the missing
// branch: the tab is marked DiskGone so the buffer reads as the only
// surviving copy, and the flash fires exactly once. Dirty stays false —
// the buffer has no user edits, and leaving it false is what lets a later
// delete+recreate (see
// TestReconcileTab_DeleteThenRecreateCleanTabReloadsSilently) fall through
// to a silent reload instead of a false conflict prompt. Every consumer
// that needs to treat this tab as "needs attention" anyway gates on
// Dirty||DiskGone, not Dirty alone.
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

	if !tab.DiskGone {
		t.Fatalf("deleted file should mark the tab gone: gone=%v", tab.DiskGone)
	}
	if tab.Dirty {
		t.Fatal("a deleted-but-unedited tab must not be marked Dirty")
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

// TestReconcileTab_DeleteThenRecreateCleanTabReloadsSilently pins the fix
// for the false disk-conflict prompt: a clean tab whose file is deleted
// and then recreated (the shape of `git checkout`, `git stash pop`, or a
// build tool rewriting its output — one unlink-then-recreate spanning two
// reconcile ticks) must reload silently, exactly like any other external
// edit. Before the fix, the delete branch overloaded Dirty to make the
// deletion feel urgent, so the reappearance was indistinguishable from a
// real unsaved-edits conflict and popped the Keep-mine/Reload/Diff modal
// over a buffer that had nothing of the user's to lose.
func TestReconcileTab_DeleteThenRecreateCleanTabReloadsSilently(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	// Tick 1: the file vanishes.
	a.reconcileTab(tab, tabProbe{path: target, err: os.ErrNotExist})
	if !tab.DiskGone {
		t.Fatal("expected DiskGone after the file disappeared")
	}

	// Tick 2: the file reappears with new content and a later mtime —
	// the shape a `git checkout` or `make` leaves behind.
	future := tab.Mtime.Add(2 * time.Second)
	if err := os.WriteFile(target, []byte("two"), 0644); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	a.reconcileTab(tab, tabProbe{path: target, mtime: future})

	if a.anyModalOpen() {
		t.Fatal("delete+recreate of a clean tab must not open the disk-conflict modal")
	}
	if got := tab.Buffer.String(); got != "two" {
		t.Fatalf("buffer: got %q, want %q", got, "two")
	}
	if tab.Dirty {
		t.Fatal("a silently reloaded tab must not read as dirty")
	}
}

// TestReconcileTab_DeleteWhileDirtyStillConflicts guards against
// overcorrecting the fix above: dropping the synthetic Dirty must not
// weaken the real-conflict path. A tab with genuine unsaved edits whose
// file is deleted and then recreated still has something of the user's
// to lose, so the reappearance must still open the disk-conflict prompt.
func TestReconcileTab_DeleteWhileDirtyStillConflicts(t *testing.T) {
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

	// Tick 1: the file vanishes while the buffer has real edits.
	a.reconcileTab(tab, tabProbe{path: target, err: os.ErrNotExist})
	if !tab.DiskGone {
		t.Fatal("expected DiskGone after the file disappeared")
	}
	if !tab.Dirty {
		t.Fatal("genuine edits must survive the delete branch")
	}

	// Tick 2: the file reappears with different content.
	future := tab.Mtime.Add(2 * time.Second)
	if err := os.WriteFile(target, []byte("theirs"), 0644); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	a.reconcileTab(tab, tabProbe{path: target, mtime: future})

	d := dirtyOverlay(t, a)
	if d.Labels[0] != "[ Keep mine ]" || d.Labels[1] != "[ Reload ]" || d.Labels[2] != "[ Diff ]" {
		t.Fatalf("conflict prompt labels = %v", d.Labels)
	}
	if got := tab.Buffer.String(); got != "mine" {
		t.Fatalf("opening the prompt must not touch the buffer: got %q", got)
	}
}

// TestRequestCloseTab_DiskGoneOnlyWarns guards the close path: a clean
// tab whose file is DiskGone still has the only surviving copy of that
// content in its buffer, so closing it must warn exactly like a dirty
// tab would rather than discarding it silently.
func TestRequestCloseTab_DiskGoneOnlyWarns(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	tab.DiskGone = true

	a.requestCloseTab(tab)

	if !a.anyModalOpen() {
		t.Fatal("a DiskGone-only tab should open the unsaved-changes modal, not close silently")
	}
	if a.tabs.IndexOf(tab) < 0 {
		t.Fatal("requestCloseTab must not have closed the tab before the modal is answered")
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

// pumpUntil feeds events off the simulation screen through handleEvent —
// what the real loop does, minus the loop — until match recognises the
// one the test is waiting for. Other background events (a git-status
// collection riding the same tick, a resize from screen setup) are
// handled on the way past instead of tripping the test, because the
// order two goroutines post in is not something a test may assume.
func pumpUntil(t *testing.T, a *App, what string, match func(tcell.Event) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !a.screen.HasPendingEvent() {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		a.handleEvent(ev)
		if match(ev) {
			return
		}
	}
	t.Fatalf("%s never arrived", what)
}

// pumpTreeScan applies the treeScanEvent a background sweep posted.
func pumpTreeScan(t *testing.T, a *App) {
	t.Helper()
	pumpUntil(t, a, "treeScanEvent", func(ev tcell.Event) bool {
		_, ok := ev.(*treeScanEvent)
		return ok
	})
}

// reconcileOpenTabsWithDisk runs the open-tab disk sweep synchronously:
// stat inline, then apply, which is exactly how the tick's two halves
// compose. It lives in the test file because nothing in production wants
// it any more — refreshTreeAsync collects the probes on a goroutine and
// handleTreeScan lands them — but a test that only cares about one
// reconciliation branch shouldn't have to pump an event to reach it.
func (a *App) reconcileOpenTabsWithDisk() {
	a.applyTabProbes(probeOpenTabs(a.openTabDiskPaths()))
}

// TestRefreshTreeAsync_ScansOffThreadAndAppliesOnEvent is the whole
// point of the split tick: the ReadDir walk and the per-tab Stat happen
// on a goroutine, and nothing about the tree or the tab changes until the
// posted event is handled on the main loop. Before the split, both ran
// inside the event handler and stuttered every ten seconds on a large
// tree over NFS.
func TestRefreshTreeAsync_ScansOffThreadAndAppliesOnEvent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "watched.txt")
	if err := os.WriteFile(target, []byte("one"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()

	// Two external changes at once: a new file for the tree to find and a
	// newer revision of the open file for the reconcile to reload.
	if err := os.WriteFile(filepath.Join(dir, "appeared.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	writeNewer(t, target, "two", tab.Mtime)

	a.refreshTreeAsync()
	if !a.treeScanInFlight {
		t.Fatal("the kick should mark a sweep in flight")
	}
	if findTreeChild(a, "appeared.txt") != nil {
		t.Fatal("the kick must not mutate the tree — that is the event's job")
	}

	pumpTreeScan(t, a)

	if a.treeScanInFlight {
		t.Fatal("the event should clear the in-flight flag")
	}
	if findTreeChild(a, "appeared.txt") == nil {
		t.Fatal("the applied sweep should surface the new file")
	}
	if got := tab.Buffer.String(); got != "two" {
		t.Fatalf("the applied sweep should reload the clean tab: got %q", got)
	}
}

// TestRefreshTreeAsync_CoalescesTicks pins the burst behaviour, matching
// refreshGitStatusAsync: kicks arriving while a sweep is in flight queue
// exactly one follow-up rather than piling up goroutines, and that
// follow-up starts when the first result lands.
func TestRefreshTreeAsync_CoalescesTicks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.treeScanInFlight = true // simulate a sweep mid-flight

	a.refreshTreeAsync()
	a.refreshTreeAsync()
	if !a.treeScanQueued {
		t.Fatal("kicks during flight should queue a follow-up")
	}

	a.handleTreeScan(&treeScanEvent{when: time.Now(), gen: a.treeScanGen})
	if a.treeScanQueued {
		t.Fatal("landing should consume the queued flag")
	}
	if !a.treeScanInFlight {
		t.Fatal("landing with a queued kick should start exactly one follow-up sweep")
	}
	pumpTreeScan(t, a)
}

// TestHandleTreeScan_DropsScanOlderThanATreeMutation is the correctness
// half of moving the walk off-thread. A sweep reads the disk, then the
// user deletes a file — which refreshes the tree synchronously. Applying
// the older listing afterwards would put the deleted file back on screen
// until the next tick, so the generation bump retires it.
func TestHandleTreeScan_DropsScanOlderThanATreeMutation(t *testing.T) {
	dir := t.TempDir()
	doomed := filepath.Join(dir, "doomed.txt")
	if err := os.WriteFile(doomed, []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	if findTreeChild(a, "doomed.txt") == nil {
		t.Fatal("seed file should be in the tree")
	}

	// The sweep sees doomed.txt...
	stale := &treeScanEvent{
		when: time.Now(),
		gen:  a.treeScanGen,
		dirs: filetree.ScanDirs(a.tree.LoadedDirs()),
	}
	// ...and only then does the user delete it, refreshing the tree.
	if err := os.Remove(doomed); err != nil {
		t.Fatalf("remove: %v", err)
	}
	a.refreshTree()
	if findTreeChild(a, "doomed.txt") != nil {
		t.Fatal("the synchronous refresh should have dropped the file")
	}

	a.handleTreeScan(stale)
	if findTreeChild(a, "doomed.txt") != nil {
		t.Fatal("a scan older than the deletion must not resurrect the file")
	}
}

// TestRefreshTreeAsync_WritesSessionOffThread pins the third piece of the
// tick. The capture stays on the main thread (it reads tabs, cursors and
// the tree's expanded set); only the temp-file-plus-fsync-plus-rename
// write crosses over, and it rides the same sweep, so the treeScanEvent
// arriving means the write is done — nothing is left touching the state
// directory after the tick lands. The round trip through restoreSession
// proves the payload survived the hop.
func TestRefreshTreeAsync_WritesSessionOffThread(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	target := filepath.Join(root, "s.txt")
	if err := os.WriteFile(target, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(target)
	a.activeTabPtr().Cursor = editor.Position{Line: 2}

	a.refreshTreeAsync()
	if _, ok := session.Load(root); ok {
		t.Fatal("the kick must not write inline — that is the fsync on the event thread")
	}
	pumpTreeScan(t, a)

	p, ok := session.Load(root)
	if !ok || len(p.Tabs) != 1 || p.Tabs[0].Line != 2 {
		t.Fatalf("session did not land with the sweep: found=%v %+v", ok, p.Tabs)
	}

	b := newTestApp(t, root)
	b.restoreSession()
	if b.tabs.Len() != 1 {
		t.Fatalf("restored %d tabs, want 1", b.tabs.Len())
	}
	if got := b.activeTabPtr().Cursor.Line; got != 2 {
		t.Fatalf("restored cursor line %d, want 2", got)
	}
}

// findTreeChild returns the root-level tree node named name, or nil.
func findTreeChild(a *App, name string) *filetree.Node {
	for _, c := range a.tree.Root.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// installReadyFinder wires a Ready finder onto the app and returns a
// counter of rebuild kicks. The PanicGuard seam intercepts the build
// goroutine after the first (real) index lands, so a test can assert
// "no rebuild started" without racing a goroutine that would flip the
// state back to Ready before the assert runs.
func installReadyFinder(t *testing.T, a *App) *int {
	t.Helper()
	f := finder.New(a.rootDir)
	f.Rebuild(nil)
	deadline := time.Now().Add(5 * time.Second)
	for f.State() != finder.StateReady && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if f.State() != finder.StateReady {
		t.Fatalf("finder never reached Ready: state=%v", f.State())
	}
	kicks := 0
	f.PanicGuard = func(name string, fn func()) { kicks++ }
	a.finder = f
	return &kicks
}

// TestHandleTreeScan_QuietScanSkipsFinderRebuild pins the quiet tick:
// a background sweep that reproduces the tree's current membership
// must not invalidate the finder index. Before the gate, every tick
// re-ran `git ls-files` (or a full walk) and left Esc-p showing
// "Indexing…" for a slice of every ten-second window, forever, on a
// project nobody was touching.
func TestHandleTreeScan_QuietScanSkipsFinderRebuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	kicks := installReadyFinder(t, a)

	a.handleTreeScan(&treeScanEvent{
		when: time.Now(),
		gen:  a.treeScanGen,
		dirs: filetree.ScanDirs(a.tree.LoadedDirs()),
	})

	if *kicks != 0 {
		t.Fatalf("an unchanged scan kicked %d finder rebuild(s), want 0", *kicks)
	}
	if a.finder.State() != finder.StateReady {
		t.Fatalf("an unchanged scan moved the finder off Ready: %v", a.finder.State())
	}
}

// TestHandleTreeScan_NewNameTriggersFinderRebuild is the other
// direction of the gate: a sweep that lands an actual membership
// change must reindex, or a file pulled in behind the editor would
// stay invisible to Esc-p until some file op happened to invalidate.
func TestHandleTreeScan_NewNameTriggersFinderRebuild(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	kicks := installReadyFinder(t, a)

	if err := os.WriteFile(filepath.Join(dir, "appeared.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.handleTreeScan(&treeScanEvent{
		when: time.Now(),
		gen:  a.treeScanGen,
		dirs: filetree.ScanDirs(a.tree.LoadedDirs()),
	})

	if *kicks != 1 {
		t.Fatalf("a scan with a new name kicked %d finder rebuild(s), want 1", *kicks)
	}
	if findTreeChild(a, "appeared.txt") == nil {
		t.Fatal("the scan should still land the new file in the tree")
	}
}

// TestHandleGitOpDone_TreeTouchingOpStillReindexesFinder guards the
// call refreshTreeNow used to make for everyone: a pull/checkout/stash
// can create files in directories the tree never loaded, which the
// scan-change gate cannot see, so the git-op landing must invalidate
// the finder explicitly.
func TestHandleGitOpDone_TreeTouchingOpStillReindexesFinder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	kicks := installReadyFinder(t, a)

	a.handleGitOpDone(&gitOpDoneEvent{
		when: time.Now(), label: "Pull", okFlash: "Pulled", touchesTree: true,
	})

	if *kicks != 1 {
		t.Fatalf("a tree-touching git op kicked %d finder rebuild(s), want 1", *kicks)
	}
	pumpTreeScan(t, a)
}

// TestHandleCustomActionDone_StillReindexesFinder: same reasoning as
// the git-op case — a custom action (an scp, a generator) can drop
// files anywhere, so its success path keeps an explicit invalidation
// rather than riding the tick's now-conditional one.
func TestHandleCustomActionDone_StillReindexesFinder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	kicks := installReadyFinder(t, a)

	a.handleCustomActionDone(&customActionDoneEvent{when: time.Now(), label: "Sync"})

	if *kicks != 1 {
		t.Fatalf("a finished custom action kicked %d finder rebuild(s), want 1", *kicks)
	}
	pumpTreeScan(t, a)
}
