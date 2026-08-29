// =============================================================================
// File: internal/app/fileops_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the app side of file operations: the menu / popup wiring,
// the unsaved-changes gate in front of a delete, and applyChangeset —
// the one tail every operation runs. The disk primitives themselves
// (create, rename, trash, the copy ladder) are pinned in
// internal/filemanager; here the assertions are about what the editor
// does with a Changeset: tab paths, closed and reopened tabs, the git
// and finder refreshes, the flashes.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filemanager"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/finder"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/session"
)

// TestDoDeletePath_File removes an existing file from its original
// location (the content lives on in the session trash — see the
// trash/undo tests further down for that half of the contract).
func TestDoDeletePath_File(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "trash.txt")
	if err := os.WriteFile(target, []byte("nope"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.doDeletePath(target)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file still exists after delete: err=%v", err)
	}
}

// TestDoDeletePath_DirectoryRecursive pins folder-delete behaviour:
// the whole subtree leaves its original location in one action (moved
// to the trash as a unit), so the user never walks leaf-to-root.
func TestDoDeletePath_DirectoryRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	nested := filepath.Join(sub, "deeper", "leaf.txt")
	if err := os.MkdirAll(filepath.Dir(nested), 0755); err != nil {
		t.Fatalf("seed subdir: %v", err)
	}
	if err := os.WriteFile(nested, []byte("x"), 0644); err != nil {
		t.Fatalf("seed leaf: %v", err)
	}
	a := newTestApp(t, dir)

	a.doDeletePath(sub)
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: err=%v", err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("nested file survived parent delete: err=%v", err)
	}
}

// TestDoDeletePath_Missing surfaces a useful flash when the target is
// already gone, rather than silently claiming success on a typo/race.
func TestDoDeletePath_Missing(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	a.doDeletePath(filepath.Join(dir, "ghost"))
	if !strings.Contains(a.statusMsg, "Delete failed") {
		t.Fatalf("expected a Delete failed flash for a missing path, got %q", a.statusMsg)
	}
}

// TestTabPathRemoved_ExactMatch is the simplest case: a tab pointing
// at the deleted file is orphaned and must close.
func TestTabPathRemoved_ExactMatch(t *testing.T) {
	if !tabPathRemoved("/proj/main.go", "/proj/main.go") {
		t.Fatal("exact match should be flagged removed")
	}
}

// TestTabPathRemoved_InsideDeletedDir pins the folder-delete case:
// every tab living under the deleted directory is orphaned. Without
// this the editor would keep showing buffers backed by files that
// no longer exist, and the next save would silently re-create them
// at the deleted location.
func TestTabPathRemoved_InsideDeletedDir(t *testing.T) {
	if !tabPathRemoved("/proj/sub/leaf.go", "/proj/sub") {
		t.Fatal("descendant tab should be flagged removed")
	}
	if !tabPathRemoved("/proj/sub/deep/leaf.go", "/proj/sub") {
		t.Fatal("nested descendant should be flagged removed")
	}
}

// TestTabPathRemoved_PrefixCollisionSafe is the trap the +"/" check
// guards against: deleting /proj/foo must not also close a tab at
// /proj/foobar.go just because the strings share a prefix.
func TestTabPathRemoved_PrefixCollisionSafe(t *testing.T) {
	if tabPathRemoved("/proj/foobar.go", "/proj/foo") {
		t.Fatal("sibling with shared prefix should not be flagged removed")
	}
}

// TestDoRenameFolder_RewritesDescendantTabPaths is the most
// important invariant of folder rename: an open tab pointing at a
// file inside the renamed directory must follow the rename, or the
// next save would write to the old (now nonexistent) path and
// silently re-create the folder under the wrong name.
func TestDoRenameFolder_RewritesDescendantTabPaths(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	if err := os.MkdirAll(filepath.Join(oldDir, "deep"), 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	leaf := filepath.Join(oldDir, "deep", "leaf.go")
	if err := os.WriteFile(leaf, []byte("package x\n"), 0644); err != nil {
		t.Fatalf("seed leaf: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(leaf)
	a.setActiveFolder(oldDir)

	a.doRename(oldDir, "renamed")

	newLeaf := filepath.Join(root, "renamed", "deep", "leaf.go")
	if _, err := os.Stat(newLeaf); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if got := a.tabs.At(0).Path; got != newLeaf {
		t.Fatalf("descendant tab path: got %q, want %q", got, newLeaf)
	}
	if want := filepath.Join(root, "renamed"); a.activeFolder != want {
		t.Fatalf("activeFolder: got %q, want %q", a.activeFolder, want)
	}
}

// TestDoRenameFolder_RefusesPathSeparator pins the input-validation
// rule shared with file rename: typing a slash should be rejected
// rather than silently moving the folder somewhere unexpected. The
// flash gives the user something actionable.
func TestDoRenameFolder_RefusesPathSeparator(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	a.doRename(sub, "nested/inside")

	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("source folder vanished despite refusal: %v", err)
	}
	if !strings.Contains(a.statusMsg, "path separator") {
		t.Fatalf("expected separator flash, got %q", a.statusMsg)
	}
}

// TestDoRenameFolder_RefusesClobber confirms the rename helper
// won't overwrite a sibling that already exists. Same safety rail
// the manager gives file rename, just exercised through the folder
// path so we don't accidentally regress it for directories.
func TestDoRenameFolder_RefusesClobber(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "lib")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	if err := os.Mkdir(dst, 0755); err != nil {
		t.Fatalf("seed dst: %v", err)
	}
	a := newTestApp(t, root)

	a.doRename(src, "lib")

	if _, err := os.Stat(src); err != nil {
		t.Fatalf("src disappeared despite refusal: %v", err)
	}
}

// TestMenuRenameFolder_OpensPrompt walks the menu wiring: clicking
// Rename folder must open the prompt with the folder's basename
// already filled in (so the user only edits, not retypes).
func TestMenuRenameFolder_OpensPrompt(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "victim")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.setActiveFolder(sub)

	a.menuRenameFolder()
	if !promptIsOpen(a) {
		t.Fatal("expected prompt to open")
	}
	if got := promptPrefab(t, a).Field.Text(); got != "victim" {
		t.Fatalf("prompt value: got %q, want %q", got, "victim")
	}
}

// TestMenuRenameFolder_RefusesRoot mirrors menuDeleteFolder's
// guard. Renaming the project root would invalidate the editor's
// own working directory and confuse every open tab — must be a
// no-op even if some future caller sets activeFolder to root.
func TestMenuRenameFolder_RefusesRoot(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.setActiveFolder(root)

	a.menuRenameFolder()
	if promptIsOpen(a) {
		t.Fatal("root should not open the rename prompt")
	}
}

// TestRenameFolderLabel_DynamicSuffix matches the delete-folder
// label test — bare label at root, "(subdir/)" suffix elsewhere
// so the user sees what's about to be renamed before clicking.
func TestRenameFolderLabel_DynamicSuffix(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	a.setActiveFolder(root)
	if got := a.renameFolderLabel(); got != "Rename folder" {
		t.Fatalf("root label = %q", got)
	}

	a.setActiveFolder(sub)
	got := a.renameFolderLabel()
	if !strings.Contains(got, "src") {
		t.Fatalf("subdir label should mention folder, got %q", got)
	}
}

// TestMenuDeleteFolder_Confirms walks the happy path: with a real
// active folder, menuDeleteFolder opens the confirm modal and the
// Yes branch removes the folder from disk plus resets activeFolder
// back to root so a follow-up New File doesn't try to write into a
// deleted directory.
func TestMenuDeleteFolder_Confirms(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "victim")
	if err := os.MkdirAll(filepath.Join(sub, "deep"), 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.setActiveFolder(sub)

	a.menuDeleteFolder()
	if !confirmIsOpen(a) {
		t.Fatal("expected confirm modal to open")
	}
	confirmYes(a)

	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("folder still exists: err=%v", err)
	}
	if a.activeFolder != root {
		t.Fatalf("activeFolder = %q, want project root", a.activeFolder)
	}
}

// TestMenuDeleteFolder_RefusesRoot guards the most destructive
// possibility: the project root must never be deletable from the
// menu, even if some future caller manages to set activeFolder to
// it. The early return in menuDeleteFolder is the only thing
// preventing the editor from rm -rf-ing its own working dir.
func TestMenuDeleteFolder_RefusesRoot(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.setActiveFolder(root)

	a.menuDeleteFolder()
	if confirmIsOpen(a) {
		t.Fatal("root folder should not open a confirm modal")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root vanished: %v", err)
	}
}

// TestHasActiveSubfolder_Predicate pins the menu enable rule: true
// when activeFolder points at a real subdirectory, false for the
// root, an empty active folder, or a folder that's been deleted
// externally. The menu row uses this to dim itself when the action
// would no-op, so a regression here would let the user click into
// a flash they can't act on.
func TestHasActiveSubfolder_Predicate(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "live")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	a.setActiveFolder(root)
	if a.hasActiveSubfolder() {
		t.Fatal("root should not be deletable")
	}

	a.activeFolder = ""
	if a.hasActiveSubfolder() {
		t.Fatal("empty active folder should not be deletable")
	}

	a.setActiveFolder(sub)
	if !a.hasActiveSubfolder() {
		t.Fatal("real subfolder should be deletable")
	}

	if err := os.Remove(sub); err != nil {
		t.Fatalf("remove for stale test: %v", err)
	}
	if a.hasActiveSubfolder() {
		t.Fatal("stale (externally-removed) folder should not be deletable")
	}
}

// TestDeleteFolderLabel_DynamicSuffix mirrors the New File label
// pattern: bare label at root, "(subdir/)" suffix when the active
// folder is somewhere we'd actually act on. Without this, the menu
// row would just say "Delete folder" with no hint of which folder —
// the user could click it not realising what was about to vanish.
func TestDeleteFolderLabel_DynamicSuffix(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	a.setActiveFolder(root)
	if got := a.deleteFolderLabel(); got != "Delete folder" {
		t.Fatalf("root label = %q, want bare 'Delete folder'", got)
	}

	a.setActiveFolder(sub)
	got := a.deleteFolderLabel()
	if !strings.Contains(got, "src") {
		t.Fatalf("subdir label should include folder name, got %q", got)
	}
}

// TestDynamicLabels_FitInModal pins down the regression that motivated
// the modalWidth bump: a realistic long folder name (e.g. a domain
// like "spicermatthews.com") used to leak past the right edge of the
// action menu and bleed onto the editor underneath. Every dynamic
// label hook must produce a string that fits inside the modal's
// interior cell budget — otherwise the visual overflow returns.
//
// The interior budget is modalWidth minus the leading "▸ " indent
// (drawn at mx+4) and one cell of right padding, so the constraint
// is runeLen(label) <= modalWidth - 5.
func TestDynamicLabels_FitInModal(t *testing.T) {
	root := t.TempDir()
	// Deliberately picks a folder name longer than what fit in the
	// pre-fix modalWidth=38 — this is the exact case from the bug
	// report where "spicermatthews.com" overflowed the right edge.
	sub := filepath.Join(root, "spicermatthews.com")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.setActiveFolder(sub)

	maxFit := modalWidth - 5
	for _, c := range []struct {
		name  string
		label string
	}{
		{"newFileLabel", a.newFileLabel()},
		{"renameFolderLabel", a.renameFolderLabel()},
		{"deleteFolderLabel", a.deleteFolderLabel()},
	} {
		if runeLen(c.label) > maxFit {
			t.Errorf("%s = %q (%d runes) overflows modal interior (%d cells)",
				c.name, c.label, runeLen(c.label), maxFit)
		}
		// Sanity: label still mentions the folder so the user can tell
		// what's about to be acted on. Truncation must not erase the
		// trailing folder name — that's the most informative part.
		if !strings.Contains(c.label, "spicermatthews.com") &&
			!strings.Contains(c.label, "thews.com") {
			t.Errorf("%s = %q dropped the folder name entirely", c.name, c.label)
		}
	}
}

// TestTabPathRemoved_UnrelatedSafe sanity-checks the negative case:
// a tab outside the deleted path stays open. This is the everyday
// path during a regular file delete.
func TestTabPathRemoved_UnrelatedSafe(t *testing.T) {
	if tabPathRemoved("/proj/other.go", "/proj/sub") {
		t.Fatal("unrelated tab should not be flagged removed")
	}
	if tabPathRemoved("", "/proj/sub") {
		t.Fatal("empty tab path should not be flagged removed")
	}
}

// TestRelativePathFor_InsideRoot returns a path relative to the project
// root. This is what the user expects on the clipboard when the editor's
// "root" is their repo and they want to paste the path into a commit
// message or another tool inside that same repo.
func TestRelativePathFor_InsideRoot(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	a.rootDir = dir

	target := filepath.Join(dir, "sub", "thing.go")
	got := a.relativePathFor(target)
	want := filepath.Join("sub", "thing.go")
	if got != want {
		t.Fatalf("relativePathFor = %q, want %q", got, want)
	}
}

// TestRelativePathFor_RelativeRootDir is the regression test for the bug
// where `skiff` with no argument leaves App.rootDir = "." while tree
// and tab paths are absolute — filepath.Rel refuses to mix the two and
// the helper used to silently fall back to the absolute path. Now we
// base the relativisation on tree.Root.Path which is always absolute.
func TestRelativePathFor_RelativeRootDir(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	a.rootDir = "." // simulate `skiff` invoked with no argument

	target := filepath.Join(a.tree.Root.Path, "sub", "thing.go")
	got := a.relativePathFor(target)
	want := filepath.Join("sub", "thing.go")
	if got != want {
		t.Fatalf("relativePathFor with rootDir=\".\": got %q, want %q", got, want)
	}
}

// TestAbsolutePathFor_Resolves turns a relative path into a fully-qualified
// absolute path so the clipboard contents work even if the user pastes
// into a shell whose cwd doesn't match the editor's root.
func TestAbsolutePathFor_Resolves(t *testing.T) {
	got := absolutePathFor("relative/thing.go")
	if !filepath.IsAbs(got) {
		t.Fatalf("absolutePathFor returned non-absolute: %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join("relative", "thing.go")) {
		t.Fatalf("absolutePathFor = %q, want suffix relative/thing.go", got)
	}
}

// TestMenuCopyPath_NoTabSilent guards against a nil-deref when the user
// somehow triggers the action without a tab open. The menu disables the
// row in that case but keyboard activation can still race; the action
// must be a no-op.
func TestMenuCopyPath_NoTabSilent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuCopyRelativePath()
	a.menuOpen = true
	a.menuCopyAbsolutePath()
	// Reaching here without a panic is the whole assertion.
}

// TestCopyPathToSystemClipboard_FlashMessage exercises the shared helper
// and confirms it sets a status flash so the user gets feedback —
// silent OSC 52 leaves the user wondering if the copy worked.
func TestCopyPathToSystemClipboard_FlashMessage(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copyPathToSystemClipboard("/tmp/sample.go", "relative path")
	if a.statusMsg == "" {
		t.Fatal("expected a status flash after copy")
	}
	// Either success ("Copied …") or failure ("Copy failed: …") is
	// acceptable here — the test environment may not have a usable
	// /dev/tty. The contract is just "user gets feedback."
	if !strings.Contains(a.statusMsg, "/tmp/sample.go") &&
		!strings.Contains(a.statusMsg, "Copy failed") {
		t.Fatalf("status flash didn't mention the path or an error: %q", a.statusMsg)
	}
}

// TestHasActiveSubfolder_RelativeRootNeverSubfolder is the regression
// test for the root-delete P0: launched as `skiff` or `skiff .`, the
// verbatim rootDir "." never string-matched the abs-resolved active
// folder, so the project root registered as a deletable subfolder and
// ≡ → Delete folder offered — and performed — recursive deletion of
// the whole workspace. The root must be recognized in any spelling.
func TestHasActiveSubfolder_RelativeRootNeverSubfolder(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	t.Chdir(dir)
	a.rootDir = "." // what New() received on a default launch
	a.setActiveFolder(a.tree.Root.Path)

	if a.hasActiveSubfolder() {
		t.Fatal("project root must never register as an active subfolder (rootDir given as \".\")")
	}
}

// TestDoDeletePath_RefusesProjectRoot pins the defense-in-depth layer:
// even if every menu guard fails, doDeletePath itself must refuse to
// remove the project root and say so, instead of deleting the tree out
// from under the running session.
func TestDoDeletePath_RefusesProjectRoot(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "keep.txt"), "x")
	a := newTestApp(t, dir)

	a.doDeletePath(a.tree.Root.Path)

	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("doDeletePath deleted the project root's contents")
	}
	if !strings.Contains(a.statusMsg, "project root") {
		t.Fatalf("expected a refusal flash naming the project root, got %q", a.statusMsg)
	}
}

// TestDoDeletePath_MovesToTrashAndUndoRestores pins the session-trash
// contract: Delete relocates instead of destroying, and ≡ → Undo
// delete puts the item back with its content intact.
func TestDoDeletePath_MovesToTrashAndUndoRestores(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doomed.txt")
	writeFileT(t, target, "precious content")
	a := newTestApp(t, dir)

	a.doDeletePath(target)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("deleted file should be gone from its original path")
	}
	if !a.hasTrashedEntry() {
		t.Fatal("delete should leave an entry in the session trash")
	}

	a.menuUndoDelete()

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("undo should restore the file: %v", err)
	}
	if string(got) != "precious content" {
		t.Fatalf("restored content = %q", got)
	}
	if a.hasTrashedEntry() {
		t.Fatal("successful undo should pop the trash entry")
	}
}

// TestMenuUndoDelete_RefusesClobber covers the collision case: if
// something new occupies the original path, Undo must not overwrite
// it — the entry stays in the trash and the flash explains why.
func TestMenuUndoDelete_RefusesClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "old")
	a := newTestApp(t, dir)

	a.doDeletePath(target)
	writeFileT(t, target, "new occupant")

	a.menuUndoDelete()

	got, _ := os.ReadFile(target)
	if string(got) != "new occupant" {
		t.Fatalf("undo must not clobber, file now = %q", got)
	}
	if !a.hasTrashedEntry() {
		t.Fatal("failed undo should keep the trash entry for later")
	}
	if !strings.Contains(a.statusMsg, "already exists") {
		t.Fatalf("expected an already-exists flash, got %q", a.statusMsg)
	}
}

// TestClose_DiscardsTrashAndUndoRow pins the "session trash" half of
// the name from the app's side: Close empties the manager's trash, so
// after it the Undo delete row has nothing to offer. The on-disk
// discard itself is the manager's contract (internal/filemanager's
// TestEmptyTrash_DiscardsStored); this is the wiring.
func TestClose_DiscardsTrashAndUndoRow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "x")
	a := newTestApp(t, dir)

	a.doDeletePath(target)
	if !a.hasTrashedEntry() {
		t.Fatal("setup: expected a trash entry")
	}

	a.Close()

	if a.hasTrashedEntry() {
		t.Fatal("Close should empty the session trash")
	}
	if a.undoDeleteLabel() != "Undo delete" {
		t.Fatalf("label after Close = %q, want the bare label", a.undoDeleteLabel())
	}
}

// seedDirtyDeleteApp opens path in a tab and dirties it with an edit that
// is visible in the buffer text, so a test can tell "the unsaved work
// survived" from "the last saved bytes came back".
func seedDirtyDeleteApp(t *testing.T, a *App, path string) *editor.Tab {
	t.Helper()
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatalf("openFile(%s) produced no tab", path)
	}
	tab.InsertString("EDITED ")
	if !tab.Dirty {
		t.Fatal("setup: tab should be dirty after an edit")
	}
	return tab
}

// TestDoDeletePath_DirtyTabPromptsInsteadOfDiscarding is the data-loss
// regression. Deleting a file whose buffer had unsaved edits used to trash
// the file and close the tab through closeTab, which has no dirty check —
// so the edits were simply gone. Undo delete can't help: the reopen stack
// stores path and cursor, not buffer text, and the trashed copy holds the
// last SAVED bytes. Nothing may leave the disk before the user answers.
func TestDoDeletePath_DirtyTabPromptsInsteadOfDiscarding(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "unsaved.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	seedDirtyDeleteApp(t, a, target)

	a.doDeletePath(target)

	if !dirtyIsOpen(a) {
		t.Fatalf("expected the unsaved-changes overlay; top = %T", a.overlays.Top())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the file must stay put until the user answers: %v", err)
	}
	if a.tabs.Len() != 1 {
		t.Fatalf("the tab must stay open until the user answers; got %d tabs", a.tabs.Len())
	}
	if a.hasTrashedEntry() {
		t.Fatal("nothing should have entered the trash yet")
	}
}

// TestDoDeletePath_DirtyCancelAbortsTheDelete pins Cancel as a true abort:
// the file stays, the tab stays, and the unsaved edits stay unsaved.
func TestDoDeletePath_DirtyCancelAbortsTheDelete(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "unsaved.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	tab := seedDirtyDeleteApp(t, a, target)

	a.doDeletePath(target)
	dirtyChoose(t, a, 0) // Cancel

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("Cancel must leave the file alone: %v", err)
	}
	if a.tabs.Len() != 1 || !tab.Dirty {
		t.Fatalf("Cancel must leave the dirty tab open: tabs=%d dirty=%v", a.tabs.Len(), tab.Dirty)
	}
	if !strings.Contains(tab.Buffer.String(), "EDITED") {
		t.Fatalf("Cancel must leave the buffer untouched: %q", tab.Buffer.String())
	}
}

// TestDoDeletePath_DirtySaveKeepsTheEditsRecoverable is the whole point of
// the prompt: Save writes the buffer before the file goes to the trash, so
// Undo delete brings back the work the user actually did rather than the
// stale bytes that happened to be on disk.
func TestDoDeletePath_DirtySaveKeepsTheEditsRecoverable(t *testing.T) {
	useTestTrustFile(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "unsaved.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	seedDirtyDeleteApp(t, a, target)

	a.doDeletePath(target)
	dirtyChoose(t, a, 2) // Save

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("Save then delete must still delete: err=%v", err)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("the orphaned tab should be closed; got %d tabs", a.tabs.Len())
	}
	a.menuUndoDelete()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("undo delete should restore the file: %v", err)
	}
	if !strings.Contains(string(got), "EDITED") {
		t.Fatalf("the restored file must hold the saved edits, got %q", got)
	}
}

// TestDoDeletePath_DirtyDiscardDeletesAnyway keeps the escape hatch: a
// user who says Discard gets the delete they asked for, unsaved buffer
// and all.
func TestDoDeletePath_DirtyDiscardDeletesAnyway(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "unsaved.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	seedDirtyDeleteApp(t, a, target)

	a.doDeletePath(target)
	dirtyChoose(t, a, 1) // Discard

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("Discard must complete the delete: err=%v", err)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("the orphaned tab should be closed; got %d tabs", a.tabs.Len())
	}
	a.menuUndoDelete()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("undo delete should restore the file: %v", err)
	}
	if string(got) != "saved\n" {
		t.Fatalf("Discard means the unsaved edits are gone, got %q", got)
	}
}

// TestDoDeletePath_DirtyFolderNamesEveryUnsavedFile covers the folder
// case, where one delete can orphan several dirty buffers at once. The
// prompt has to name them all — "some file somewhere has unsaved changes"
// is not something a user can act on.
func TestDoDeletePath_DirtyFolderNamesEveryUnsavedFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkg")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := filepath.Join(sub, "one.txt")
	second := filepath.Join(sub, "two.txt")
	writeFileT(t, first, "1\n")
	writeFileT(t, second, "2\n")
	a := newTestApp(t, dir)
	seedDirtyDeleteApp(t, a, first)
	seedDirtyDeleteApp(t, a, second)

	a.doDeletePath(sub)

	d, ok := a.overlays.Top().(*overlay.Dirty)
	if !ok {
		t.Fatalf("expected the unsaved-changes overlay; top = %T", a.overlays.Top())
	}
	if !strings.Contains(d.Message, "one.txt") || !strings.Contains(d.Message, "two.txt") {
		t.Fatalf("the prompt must name every unsaved file, got %q", d.Message)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("the folder must stay put until the user answers: %v", err)
	}
}

// TestDoDeletePath_CleanTabDeletesWithoutPrompting keeps the common case
// one click: the guard only fires for buffers with unsaved work.
func TestDoDeletePath_CleanTabDeletesWithoutPrompting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clean.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	a.openFile(target)

	a.doDeletePath(target)

	if dirtyIsOpen(a) {
		t.Fatal("a clean tab must not raise the unsaved-changes prompt")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("clean delete should go straight through: err=%v", err)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("the orphaned tab should be closed; got %d tabs", a.tabs.Len())
	}
}

// TestMenuDelete_DirtyTabChainsToTheUnsavedPrompt drives the real user
// path — ≡ Delete file, Yes, then the unsaved-changes prompt — because
// chaining one overlay off another's callback is where this could still
// break: the confirm has to capture its Yes callback before tearing
// itself down, or the prompt the callback opens is popped by the
// confirm's own teardown and the delete quietly proceeds anyway.
func TestMenuDelete_DirtyTabChainsToTheUnsavedPrompt(t *testing.T) {
	useTestTrustFile(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "unsaved.txt")
	writeFileT(t, target, "saved\n")
	a := newTestApp(t, dir)
	seedDirtyDeleteApp(t, a, target)

	a.menuDelete()
	confirmYes(a)

	if !dirtyIsOpen(a) {
		t.Fatalf("expected the unsaved-changes prompt after Yes; top = %T", a.overlays.Top())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the file must survive until the second answer: %v", err)
	}

	dirtyChoose(t, a, 2) // Save

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("Save should let the delete finish: err=%v", err)
	}
	if a.tabs.Len() != 0 {
		t.Fatalf("the orphaned tab should be closed; got %d tabs", a.tabs.Len())
	}
}

// TestDoCreateFile_RefusesEscapingName drives the deliberate UX
// narrowing recorded on doCreateFile's doc comment: a name that climbs
// out of parent via "../" is refused with a flash instead of quietly
// creating a file outside the tree the user is looking at.
func TestDoCreateFile_RefusesEscapingName(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "sub")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	a.doCreateFile(parent, "../evil.txt")

	if _, err := os.Stat(filepath.Join(root, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("escaping name must not create a file outside parent: err=%v", err)
	}
	if !strings.Contains(a.statusMsg, "escapes") {
		t.Fatalf("expected a refusal flash mentioning the escape, got %q", a.statusMsg)
	}
}

// treeChild returns the root-level tree node called name, failing the
// test when the tree does not show it — the popup rows take a node,
// so a popup test has to start from one.
func treeChild(t *testing.T, a *App, name string) *filetree.Node {
	t.Helper()
	for _, c := range a.tree.Root.Children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%s not in tree", name)
	return nil
}

// TestCtxRename_FolderRepointsDescendantTabs pins the tree popup's
// Rename row to the same contract the ≡ Rename folder row already
// keeps: renaming a folder carries every open tab under it to the new
// path. The popup used to route folders through the file-only rename,
// which rewrote exact-path tabs and nothing beneath a directory — so a
// buffer stayed attached to a path that no longer existed and its next
// save quietly re-created the old folder.
func TestCtxRename_FolderRepointsDescendantTabs(t *testing.T) {
	root := t.TempDir()
	leaf := mkFile(t, root, "pkg/leaf.go", "package pkg\n")
	a := newTestApp(t, root)
	a.openFile(leaf)
	node := treeChild(t, a, "pkg")

	ctxRename(a, node)
	promptPrefab(t, a).Field.SetText("renamed")
	submitPrompt(a)

	want := filepath.Join(root, "renamed", "leaf.go")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("renamed leaf missing on disk: %v", err)
	}
	if got := a.tabs.At(0).Path; got != want {
		t.Fatalf("tab path after popup rename: got %q, want %q", got, want)
	}
}

// TestMenuUndoDelete_ReopensTheTabsTheDeleteClosed pins the half of
// Undo delete that the file alone cannot give back: the delete closed
// the tabs living under the folder, so the restore reopens them where
// the user left them — same file, same caret, same scroll. Bringing
// the bytes back and leaving the editor empty made "undo" a half-undo.
func TestMenuUndoDelete_ReopensTheTabsTheDeleteClosed(t *testing.T) {
	root := t.TempDir()
	leaf := mkFile(t, root, "pkg/leaf.go", "one\ntwo\nthree\nfour\n")
	a := newTestApp(t, root)
	a.openFile(leaf)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 2, Col: 1}, false)
	a.activeTabPtr().ScrollY = 1

	a.doDeletePath(filepath.Join(root, "pkg"))
	if a.tabs.Len() != 0 {
		t.Fatalf("setup: the delete should have closed the tab; got %d tabs", a.tabs.Len())
	}

	a.menuUndoDelete()

	if a.tabs.Len() != 1 {
		t.Fatalf("undo delete should reopen the tab the delete closed; got %d tabs", a.tabs.Len())
	}
	tab := a.activeTabPtr()
	if tab.Path != leaf {
		t.Fatalf("reopened tab path: got %q, want %q", tab.Path, leaf)
	}
	if tab.Cursor != (editor.Position{Line: 2, Col: 1}) {
		t.Fatalf("reopened tab cursor: got %+v, want line 2 col 1", tab.Cursor)
	}
	if tab.ScrollY != 1 {
		t.Fatalf("reopened tab scroll: got %d, want 1", tab.ScrollY)
	}
}

// countingFinder gives a test app a real finder whose index builds run
// inline and are counted, so "the finder was invalidated" is an integer
// the test can read rather than a goroutine it has to drain.
func countingFinder(t *testing.T, a *App) *int {
	t.Helper()
	n := 0
	a.finder = finder.New(a.rootDir)
	a.finder.PanicGuard = func(_ string, fn func()) { n++; fn() }
	return &n
}

// TestApplyChangeset_EmptyStillRefreshesEverything pins the rule every
// error path relies on: an empty Changeset is not a no-op. The tree,
// the git tint, the finder index and the session are refreshed
// regardless, because the op that produced it may have changed the
// disk before failing.
func TestApplyChangeset_EmptyStillRefreshesEverything(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	a := newTestApp(t, root)
	rebuilds := countingFinder(t, a)
	if _, ok := session.Load(a.rootDir); ok {
		t.Fatal("setup: no session should exist yet")
	}

	a.applyChangeset(filemanager.Changeset{})

	if !a.gitStatus.Busy() && !a.gitStatus.Queued() {
		t.Fatal("an empty changeset must still request a git refresh")
	}
	if *rebuilds != 1 {
		t.Fatalf("finder rebuilds = %d, want 1", *rebuilds)
	}
	if _, ok := session.Load(a.rootDir); !ok {
		t.Fatal("an empty changeset must still save the session")
	}
}

// TestApplyChangeset_MovedFolderRepointsPrefixAware pins the Move half:
// one Move of a folder pair carries the exact-path tab, every tab
// beneath it, and the active folder — and leaves a sibling that merely
// shares the prefix (foo vs foobar) alone.
func TestApplyChangeset_MovedFolderRepointsPrefixAware(t *testing.T) {
	root := t.TempDir()
	inside := mkFile(t, root, "foo/deep/a.go", "a")
	lookalike := mkFile(t, root, "foobar/b.go", "b")
	a := newTestApp(t, root)
	a.openFile(inside)
	a.openFile(lookalike)
	a.setActiveFolder(filepath.Join(root, "foo", "deep"))
	// Move on disk first: applyChangeset reacts to what the manager did.
	if err := os.Rename(filepath.Join(root, "foo"), filepath.Join(root, "bar")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	a.applyChangeset(filemanager.Changeset{Moved: []filemanager.Move{
		{Old: filepath.Join(root, "foo"), New: filepath.Join(root, "bar")},
	}})

	if got, want := a.tabs.At(0).Path, filepath.Join(root, "bar", "deep", "a.go"); got != want {
		t.Fatalf("descendant tab: got %q, want %q", got, want)
	}
	if got := a.tabs.At(1).Path; got != lookalike {
		t.Fatalf("prefix lookalike must not move: got %q", got)
	}
	if want := filepath.Join(root, "bar", "deep"); a.activeFolder != want {
		t.Fatalf("activeFolder: got %q, want %q", a.activeFolder, want)
	}
}

// TestApplyChangeset_RemovedThenAddedRoundTripsTabs pins the memory
// between the two halves of a delete/undo: Removed closes every tab
// under the path and remembers them against it; the matching Added
// reopens exactly those, with their view, and forgets the record so
// a second Added of the same path opens nothing.
func TestApplyChangeset_RemovedThenAddedRoundTripsTabs(t *testing.T) {
	root := t.TempDir()
	first := mkFile(t, root, "pkg/a.txt", "one\ntwo\nthree\n")
	second := mkFile(t, root, "pkg/b.txt", "x\n")
	other := mkFile(t, root, "other.txt", "o\n")
	a := newTestApp(t, root)
	a.openFile(first)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)
	a.openFile(second)
	a.openFile(other)
	pkg := filepath.Join(root, "pkg")

	a.applyChangeset(filemanager.Changeset{Removed: []string{pkg}})

	if a.tabs.Len() != 1 || a.tabs.At(0).Path != other {
		t.Fatalf("only the unrelated tab should survive; tabs = %d", a.tabs.Len())
	}
	if len(a.removedTabs[pkg]) != 2 {
		t.Fatalf("both closed tabs should be remembered under %s, got %v", pkg, a.removedTabs)
	}

	a.applyChangeset(filemanager.Changeset{Added: []string{pkg}})

	if a.tabs.Len() != 3 {
		t.Fatalf("both remembered tabs should reopen; tabs = %d", a.tabs.Len())
	}
	reopened := a.tabs.Lookup(first)
	if reopened == nil || reopened.Cursor != (editor.Position{Line: 2, Col: 0}) {
		t.Fatalf("reopened tab should land on its old caret; got %+v", reopened)
	}
	if a.tabs.Lookup(second) == nil {
		t.Fatal("the second remembered tab should reopen too")
	}
	if _, still := a.removedTabs[pkg]; still {
		t.Fatal("a consumed record must be forgotten")
	}
	a.applyChangeset(filemanager.Changeset{Added: []string{pkg}})
	if a.tabs.Len() != 3 {
		t.Fatalf("a second Added of the same path must open nothing; tabs = %d", a.tabs.Len())
	}
}

// TestDoRename_FailureRunsTheTail pins the "on error too" rule on a
// synchronous site: a rename the manager refuses still flashes AND runs
// applyChangeset, so the tint and the index are refreshed exactly as
// after a success.
func TestDoRename_FailureRunsTheTail(t *testing.T) {
	root := t.TempDir()
	src := mkFile(t, root, "src.txt", "s")
	mkFile(t, root, "taken.txt", "t")
	a := newTestApp(t, root)
	rebuilds := countingFinder(t, a)

	a.doRename(src, "taken.txt")

	if !strings.Contains(a.statusMsg, "Rename failed") || !strings.Contains(a.statusMsg, "already exists") {
		t.Fatalf("expected the clobber refusal flash, got %q", a.statusMsg)
	}
	if *rebuilds != 1 {
		t.Fatalf("a failed rename must still invalidate the finder; rebuilds = %d", *rebuilds)
	}
	if !a.gitStatus.Busy() && !a.gitStatus.Queued() {
		t.Fatal("a failed rename must still request a git refresh")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("refused rename must leave the source: %v", err)
	}
}
