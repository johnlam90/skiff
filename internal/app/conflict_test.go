// =============================================================================
// File: internal/app/conflict_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for conflict.go — the dirty-buffer-versus-changed-file prompt and
// the local line differ behind its Diff button. The behavioural tests all
// drive the real reconcile tick, because "does the tick nag me again?" is
// the actual question the feature answers.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/overlay"
)

// conflictSetup opens a file, dirties the buffer, then rewrites the file
// on disk with a newer mtime — the exact "another tmux pane edited this"
// situation. Returns the app and the file path.
func conflictSetup(t *testing.T, buffer, disk string) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("file did not open")
	}
	// Split the in-memory side exactly the way a real open would, so an
	// identical buffer and file really are identical line-for-line.
	tab.Buffer.Lines = editor.NewBuffer(buffer).Lines
	tab.Dirty = true

	if err := os.WriteFile(path, []byte(disk), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	// Stat granularity is coarse enough on some filesystems that a write
	// microseconds after the open shares the open's mtime; push it
	// forward explicitly so the change is unambiguous.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return a, path
}

// dirtyOverlay returns the conflict prompt currently on the stack.
func dirtyOverlay(t *testing.T, a *App) *overlay.Dirty {
	t.Helper()
	d, ok := a.overlays.Top().(*overlay.Dirty)
	if !ok {
		t.Fatalf("no conflict overlay open; top = %T", a.overlays.Top())
	}
	return d
}

// TestReconcile_DirtyTabOpensConflictOverlay is the core of the fix: a
// dirty buffer whose file changed underneath it must stop and ask,
// not flash a warning and then silently overwrite on the next save.
func TestReconcile_DirtyTabOpensConflictOverlay(t *testing.T) {
	a, _ := conflictSetup(t, "mine\n", "theirs\n")

	a.reconcileOpenTabsWithDisk()

	d := dirtyOverlay(t, a)
	if d.Labels[0] != "[ Keep mine ]" || d.Labels[1] != "[ Reload ]" || d.Labels[2] != "[ Diff ]" {
		t.Fatalf("conflict prompt labels = %v", d.Labels)
	}
	if d.Hover != 0 {
		t.Fatalf("focus = %d, want the non-destructive Keep mine", d.Hover)
	}
	if !strings.Contains(d.Message, "shared.txt") {
		t.Fatalf("message should name the file, got %q", d.Message)
	}
	if a.activeTabPtr().Buffer.Lines[0] != "mine" {
		t.Fatal("opening the prompt must not touch the buffer")
	}
}

// TestReconcile_ReloadTakesDiskContents pins the Reload button: it
// throws away the in-memory edits and adopts what is on disk, and the
// conflict is then resolved.
func TestReconcile_ReloadTakesDiskContents(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.reconcileOpenTabsWithDisk()

	d := dirtyOverlay(t, a)
	d.Hover = 1 // Reload
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	tab := a.activeTabPtr()
	if got := tab.Buffer.Lines[0]; got != "theirs" {
		t.Fatalf("buffer after Reload = %q, want the disk copy", got)
	}
	if tab.Dirty {
		t.Fatal("Reload should leave a clean buffer")
	}
	if a.tabDiskConflict(tab) {
		t.Fatal("Reload resolves the conflict; the marker must go")
	}
	if _, still := a.diskConflicts[path]; still {
		t.Fatal("Reload should forget the conflict entry")
	}
	if a.overlays.IsOpen() {
		t.Fatal("Reload should close the prompt")
	}
}

// TestReconcile_KeepMineDoesNotReNag is the anti-nag guarantee: after
// the user picks Keep mine, the ten-second tick must stop re-opening the
// prompt for the same disk revision — while the status-bar marker keeps
// the conflict visible.
func TestReconcile_KeepMineDoesNotReNag(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.reconcileOpenTabsWithDisk()

	d := dirtyOverlay(t, a)
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) // Keep mine
	if a.overlays.IsOpen() {
		t.Fatal("Keep mine should dismiss the prompt")
	}

	a.reconcileOpenTabsWithDisk()
	if a.overlays.IsOpen() {
		t.Fatal("the next tick must not re-open the prompt for the same change")
	}
	tab := a.activeTabPtr()
	if tab.Buffer.Lines[0] != "mine" {
		t.Fatal("Keep mine must leave the buffer alone")
	}
	if !a.tabDiskConflict(tab) {
		t.Fatal("a dismissed prompt is not a resolved conflict — marker must stay")
	}

	// A genuinely new external write is new information and asks again.
	if err := os.WriteFile(path, []byte("theirs again\n"), 0o644); err != nil {
		t.Fatalf("second external write: %v", err)
	}
	later := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	a.reconcileOpenTabsWithDisk()
	if !a.overlays.IsOpen() {
		t.Fatal("a second, different disk change should prompt again")
	}
}

// TestReconcile_CleanTabStillReloadsSilently guards the untouched half of
// the decision tree: a clean buffer takes the disk copy with no prompt.
func TestReconcile_CleanTabStillReloadsSilently(t *testing.T) {
	a, _ := conflictSetup(t, "mine\n", "theirs\n")
	a.activeTabPtr().Dirty = false

	a.reconcileOpenTabsWithDisk()

	if a.overlays.IsOpen() {
		t.Fatal("a clean tab must not prompt")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "theirs" {
		t.Fatalf("clean tab buffer = %q, want silent reload", got)
	}
}

// TestReconcile_DoesNotStealAnOpenOverlay: the tick fires on a timer, so
// it must never yank a modal the user is working in out from under them.
// The conflict is left unacknowledged so the next tick still asks.
func TestReconcile_DoesNotStealAnOpenOverlay(t *testing.T) {
	a, path := conflictSetup(t, "mine\n", "theirs\n")
	a.openPrompt("Rename", "", "shared.txt", nil)

	a.reconcileOpenTabsWithDisk()

	if _, ok := a.overlays.Top().(*overlay.Prompt); !ok {
		t.Fatalf("tick stole the open modal; top = %T", a.overlays.Top())
	}
	if _, noted := a.diskConflicts[path]; noted {
		t.Fatal("a deferred conflict must not be recorded as acknowledged")
	}

	a.closeAllModals()
	a.reconcileOpenTabsWithDisk()
	if _, ok := a.overlays.Top().(*overlay.Dirty); !ok {
		t.Fatalf("deferred conflict should prompt once the modal closes; top = %T", a.overlays.Top())
	}
}

// TestConflictDiff_OpensDiffViewAgainstDisk pins the Diff button: it
// hands the existing diff viewer a real buffer-versus-disk diff, and
// looking at the diff does not count as resolving the conflict.
func TestConflictDiff_OpensDiffViewAgainstDisk(t *testing.T) {
	a, path := conflictSetup(t, "one\nMINE\nthree\n", "one\nTHEIRS\nthree\n")
	a.reconcileOpenTabsWithDisk()

	d := dirtyOverlay(t, a)
	d.Hover = 2 // Diff
	d.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	dv, ok := a.overlays.Top().(*diffOverlay)
	if !ok {
		t.Fatalf("Diff should open the diff viewer; top = %T", a.overlays.Top())
	}
	joined := strings.Join(dv.raw, "\n")
	if !strings.Contains(joined, "-THEIRS") || !strings.Contains(joined, "+MINE") {
		t.Fatalf("diff should show disk as old and buffer as new:\n%s", joined)
	}
	if _, still := a.diskConflicts[path]; !still {
		t.Fatal("looking at a diff is not resolving the conflict")
	}
}

// TestConflictDiff_IdenticalContentsResolves covers the benign case: a
// `touch` bumps the mtime without changing a byte, so there is nothing
// to choose between and the conflict simply goes away.
func TestConflictDiff_IdenticalContentsResolves(t *testing.T) {
	a, path := conflictSetup(t, "same\n", "same\n")
	a.reconcileOpenTabsWithDisk()

	dirtyOverlay(t, a).HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	dirtyOverlay(t, a).HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	dirtyOverlay(t, a).HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.overlays.IsOpen() {
		t.Fatalf("identical contents should not open a diff; top = %T", a.overlays.Top())
	}
	if _, still := a.diskConflicts[path]; still {
		t.Fatal("identical contents resolve the conflict")
	}
}

// TestTabDiskConflict_RequiresDirtyBuffer pins the marker's own rule: a
// saved buffer cannot still be in conflict, whatever the map remembers,
// so the status bar never shows a stale warning.
func TestTabDiskConflict_RequiresDirtyBuffer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := &editor.Tab{Path: "/tmp/x.txt", Dirty: true}
	if a.tabDiskConflict(tab) {
		t.Fatal("no recorded conflict, no marker")
	}
	a.noteDiskConflict(tab.Path, time.Now())
	if !a.tabDiskConflict(tab) {
		t.Fatal("recorded conflict on a dirty tab should show")
	}
	tab.Dirty = false
	if a.tabDiskConflict(tab) {
		t.Fatal("clean buffer can't be in conflict")
	}
	if a.tabDiskConflict(nil) {
		t.Fatal("nil tab must be safe")
	}
}

// TestUnifiedDiff_HunkShape pins the differ's output format: real @@
// headers with the right line numbers, context around the change, and
// nothing emitted for identical inputs. parseSideBySideDiff consumes
// this, so the shape is a contract.
func TestUnifiedDiff_HunkShape(t *testing.T) {
	if got := unifiedDiff([]string{"a", "b"}, []string{"a", "b"}); got != nil {
		t.Fatalf("identical inputs should diff to nothing, got %v", got)
	}

	old := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	new := []string{"1", "2", "3", "4", "CHANGED", "6", "7", "8", "9"}
	got := unifiedDiff(old, new)
	want := []string{
		"@@ -2,7 +2,7 @@",
		" 2", " 3", " 4", "-5", "+CHANGED", " 6", " 7", " 8",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diff =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The diff viewer must be able to parse it back into aligned rows.
	rows := parseSideBySideDiff(got)
	var paired bool
	for _, r := range rows {
		if r.Kind == diffRowChange && r.Left == "5" && r.Right == "CHANGED" {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("diff viewer did not pair the modification: %+v", rows)
	}
}

// TestUnifiedDiff_SeparateHunks checks that distant changes get their
// own @@ header rather than dragging the whole file in as context.
func TestUnifiedDiff_SeparateHunks(t *testing.T) {
	old := make([]string, 40)
	for i := range old {
		old[i] = "line"
	}
	new := append([]string(nil), old...)
	new[2] = "top"
	new[37] = "bottom"

	got := unifiedDiff(old, new)
	headers := 0
	for _, l := range got {
		if strings.HasPrefix(l, "@@ ") {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("want two hunks for two distant changes, got %d:\n%s", headers, strings.Join(got, "\n"))
	}
	if len(got) >= len(old) {
		t.Fatalf("hunks should be far smaller than the file, got %d lines", len(got))
	}
}

// TestUnifiedDiff_PureInsertAndDelete covers the one-sided cases and the
// zero-count hunk header git emits for them.
func TestUnifiedDiff_PureInsertAndDelete(t *testing.T) {
	added := unifiedDiff([]string{"a"}, []string{"a", "b"})
	if strings.Join(added, "|") != "@@ -1,1 +1,2 @@| a|+b" {
		t.Fatalf("insert diff = %v", added)
	}
	removed := unifiedDiff([]string{"a", "b"}, []string{"a"})
	if strings.Join(removed, "|") != "@@ -1,2 +1,1 @@| a|-b" {
		t.Fatalf("delete diff = %v", removed)
	}
	emptied := unifiedDiff([]string{"a"}, nil)
	if strings.Join(emptied, "|") != "@@ -1,1 +0,0 @@|-a" {
		t.Fatalf("emptied diff = %v", emptied)
	}
}

// TestAlignLines_OversizedFallsBackToBlockReplace pins the memory guard:
// past diffPairCap the differ stops building an LCS table and emits a
// coarse but honest replace-everything alignment.
func TestAlignLines_OversizedFallsBackToBlockReplace(t *testing.T) {
	n := 1200 // 1200^2 > diffPairCap
	old := make([]string, n)
	new := make([]string, n)
	for i := range old {
		old[i] = "old"
		new[i] = "new"
	}
	ops := alignLines(old, new)
	if len(ops) != 2*n {
		t.Fatalf("block replace should emit every line twice, got %d", len(ops))
	}
	for _, op := range ops[:n] {
		if op.kind != '-' {
			t.Fatalf("first half should be deletions, saw %q", op.kind)
		}
	}
	for _, op := range ops[n:] {
		if op.kind != '+' {
			t.Fatalf("second half should be additions, saw %q", op.kind)
		}
	}
}

// TestMenuResolveDiskConflict_ReopensPrompt pins the way back. Once the
// prompt is dismissed the status-bar marker would otherwise be a dead
// end — there is no other route to the decision short of waiting for
// somebody else to write the file again.
func TestMenuResolveDiskConflict_ReopensPrompt(t *testing.T) {
	a, _ := conflictSetup(t, "mine\n", "theirs\n")
	a.reconcileOpenTabsWithDisk()
	dirtyOverlay(t, a).HandleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.overlays.IsOpen() {
		t.Fatal("Esc should dismiss the prompt")
	}

	if !a.hasDiskConflict() {
		t.Fatal("the menu row should be enabled while the conflict stands")
	}
	a.menuResolveDiskConflict()
	if _, ok := a.overlays.Top().(*overlay.Dirty); !ok {
		t.Fatalf("menu row should reopen the prompt; top = %T", a.overlays.Top())
	}
}

// TestMenuResolveDiskConflict_NoopWithoutConflict keeps the row honest:
// with nothing to resolve it must do nothing rather than open an empty
// prompt over whatever the user was doing.
func TestMenuResolveDiskConflict_NoopWithoutConflict(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasDiskConflict() {
		t.Fatal("a fresh app has no conflict")
	}
	a.menuResolveDiskConflict()
	if a.overlays.IsOpen() {
		t.Fatalf("no conflict, no overlay; got %T", a.overlays.Top())
	}
}
