// =============================================================================
// File: internal/app/gitstatus_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for gitstatus.go. The byte-parsing helpers (parsePorcelain,
// unquotePath, dirtyFolderSet, pathInside) are exercised in isolation
// with synthetic input — no subprocess needed. The shell-out flow
// (loadGitStatus end-to-end) is exercised against a real `git init`'d
// repo in a t.TempDir, and skipped when git isn't on PATH so the test
// suite still runs in a stripped-down container.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
)

// TestLoadGitStatus_NotARepo verifies that pointing the loader at a
// directory that isn't tracked by git returns the zero-value gitStatus —
// the editor should silently skip its dirty highlight rather than
// erroring out when run inside a plain folder.
func TestLoadGitStatus_NotARepo(t *testing.T) {
	dir := t.TempDir()
	st := loadGitStatus(dir, "")
	if st.IsRepo {
		t.Fatalf("plain dir should not report as repo, got %+v", st)
	}
	if st.Files != nil {
		t.Fatalf("plain dir should have nil DirtyFiles, got %v", st.Files)
	}
}

// TestLoadGitStatus_EmptyRoot guards the "" early-return so a fresh App
// (rootDir not yet set) can call refreshGitStatus without spawning git.
func TestLoadGitStatus_EmptyRoot(t *testing.T) {
	if st := loadGitStatus("", ""); st.IsRepo {
		t.Fatalf("empty rootDir should not report as repo, got %+v", st)
	}
}

// TestLoadGitStatus_CleanRepo runs the full pipeline against a freshly
// initialised, fully committed repo and confirms IsRepo flips on but the
// dirty set comes back empty — the renderer should treat clean files
// like any other, no Modified-color highlight. Also pins down that the
// branch name comes through populated.
func TestLoadGitStatus_CleanRepo(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "hello")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")

	st := loadGitStatus(repo, "")
	if !st.IsRepo {
		t.Fatal("expected IsRepo=true on a real git repo")
	}
	if len(st.Files) != 0 {
		t.Fatalf("expected no dirty files, got %v", st.Files)
	}
	if st.Branch != "main" {
		t.Fatalf("expected Branch=main, got %q", st.Branch)
	}
}

// TestLoadGitLineChanges_IncludesStagedChanges compares the worktree with HEAD,
// so staging a file does not remove its gutter markers.
func TestLoadGitLineChanges_IncludesStagedChanges(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	path := filepath.Join(repo, "a.txt")
	writeFileT(t, path, "one\ntwo\nthree\n")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")
	writeFileT(t, path, "one\nchanged\nthree\nfour\n")
	gitRun(t, repo, "add", "a.txt")

	changes := loadGitLineChanges(repo, "", "a.txt")
	if len(changes) == 0 {
		t.Fatal("staged changes should produce gutter markers")
	}
	if got := changes[1]; got != editor.GitLineModified {
		t.Fatalf("line 2 marker = %v, want modified", got)
	}
}

// TestRefreshGitStatusAsync_PostsEventAndApplies drives the full async
// round trip: the kick collects on a goroutine and posts a
// gitStatusEvent; feeding that event through handleEvent applies the
// snapshot to the tree and clears the in-flight flag — exactly what
// the real event loop does, minus the loop.
func TestRefreshGitStatusAsync_PostsEventAndApplies(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	writeFileT(t, filepath.Join(dir, "f.txt"), "x\n")
	a := newTestApp(t, dir)
	a.tree.DirtyFiles = nil // prove the event repopulates it

	a.refreshGitStatusAsync()
	if !a.gitRefreshInFlight {
		t.Fatal("kick should mark a collection in flight")
	}

	evCh := make(chan tcell.Event, 1)
	go func() { evCh <- a.screen.PollEvent() }()
	select {
	case ev := <-evCh:
		gse, ok := ev.(*gitStatusEvent)
		if !ok {
			t.Fatalf("expected gitStatusEvent, got %T", ev)
		}
		a.handleEvent(gse)
	case <-time.After(5 * time.Second):
		t.Fatal("background collection never posted its event")
	}

	if a.gitRefreshInFlight {
		t.Fatal("event should clear the in-flight flag")
	}
	if len(a.tree.DirtyFiles) != 1 {
		t.Fatalf("event should stamp dirty files, got %v", a.tree.DirtyFiles)
	}
}

// TestRefreshGitStatusAsync_CoalescesKicks pins the burst behaviour: a
// second kick while one is in flight queues exactly one follow-up run
// rather than piling up goroutines, and the follow-up fires when the
// first result lands.
func TestRefreshGitStatusAsync_CoalescesKicks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitRefreshInFlight = true // simulate a collection mid-flight

	a.refreshGitStatusAsync()
	a.refreshGitStatusAsync()
	if !a.gitRefreshQueued {
		t.Fatal("kicks during flight should queue a follow-up")
	}

	a.handleGitStatusEvent(&gitStatusEvent{res: gitStatusResult{}})
	if !a.gitRefreshInFlight || a.gitRefreshQueued {
		t.Fatalf("landing should fire the queued follow-up, inFlight=%v queued=%v",
			a.gitRefreshInFlight, a.gitRefreshQueued)
	}
}

// TestApplyGitStatus_UpdatesOpenTabGutters verifies a collection's
// per-tab line changes reach the matching open tabs, and that tabs
// absent from the result keep their existing markers.
func TestApplyGitStatus_UpdatesOpenTabGutters(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "a.txt")
	other := filepath.Join(dir, "b.txt")
	writeFileT(t, tracked, "x\n")
	writeFileT(t, other, "y\n")
	a := newTestApp(t, dir)
	a.openFile(tracked)
	a.openFile(other)
	keep := map[int]editor.GitLineChange{3: editor.GitLineAdded}
	a.tabs.At(1).GitLines = keep

	a.applyGitStatus(gitStatusResult{
		st: gitStatus{IsRepo: true, Root: dir, Branch: "main", Files: map[string]filetree.GitChangeKind{}},
		tabLines: map[string]map[int]editor.GitLineChange{
			tracked: {0: editor.GitLineModified},
		},
	})

	if got := a.tabs.At(0).GitLines[0]; got != editor.GitLineModified {
		t.Fatalf("tab 0 gutter should update, got %v", got)
	}
	if got := a.tabs.At(1).GitLines[3]; got != editor.GitLineAdded {
		t.Fatalf("tab 1 (absent from result) should keep its markers, got %v", got)
	}
}

// TestLoadGitFileDiff_DeletedFile pins the loader the Git changes modal
// leans on for deleted rows: the full diff against HEAD, including the
// removed content, comes back as display lines.
func TestLoadGitFileDiff_DeletedFile(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	path := filepath.Join(repo, "gone.txt")
	writeFileT(t, path, "farewell\n")
	gitRun(t, repo, "add", "gone.txt")
	gitRun(t, repo, "commit", "-m", "init")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	lines := loadGitFileDiff(repo, "", path, false)
	if len(lines) == 0 {
		t.Fatal("deleted file should produce a diff")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "-farewell") {
		t.Fatalf("diff should contain the removed line, got:\n%s", joined)
	}
}

// TestLoadGitFileDiff_UntrackedFallsBackToNoIndex pins the untracked
// path: a file git has never seen still renders as an all-added diff
// via --no-index, so the Git panel's preview is never empty for new
// files.
func TestLoadGitFileDiff_UntrackedFallsBackToNoIndex(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	fresh := filepath.Join(repo, "fresh.txt")
	writeFileT(t, fresh, "hello\n")

	lines := loadGitFileDiff(repo, "", fresh, true)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "+hello") {
		t.Fatalf("untracked diff should show the file as added, got:\n%s", joined)
	}
}

// TestLoadGitFileDiff_Degrades confirms the best-effort contract: a
// non-repo, empty arguments, and a clean file all yield nil rather than
// an error the UI would have to route somewhere. The clean-file case
// doubly matters now that untracked=true has a fallback — a tracked,
// unchanged file must never be painted as brand new.
func TestLoadGitFileDiff_Degrades(t *testing.T) {
	if got := loadGitFileDiff(t.TempDir(), "", "x.txt", false); got != nil {
		t.Fatalf("non-repo diff = %v, want nil", got)
	}
	if got := loadGitFileDiff("", "", "x.txt", false); got != nil {
		t.Fatalf("empty root diff = %v, want nil", got)
	}
	if got := loadGitFileDiff(t.TempDir(), "", "", true); got != nil {
		t.Fatalf("empty path diff = %v, want nil", got)
	}
	requireGit(t)
	repo := initRepo(t)
	clean := filepath.Join(repo, "clean.txt")
	writeFileT(t, clean, "steady\n")
	gitRun(t, repo, "add", "clean.txt")
	gitRun(t, repo, "commit", "-m", "init")
	if got := loadGitFileDiff(repo, "", clean, false); got != nil {
		t.Fatalf("clean file diff = %v, want nil", got)
	}
}

// TestLoadGitStatus_FindsModifiedAndUntracked seeds a repo with one
// committed file (later modified), one brand-new untracked file, and
// one staged-but-uncommitted file. All three should show up as dirty,
// indexed by absolute path so the file tree's path-keyed lookup hits.
func TestLoadGitStatus_FindsModifiedAndUntracked(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	writeFileT(t, filepath.Join(repo, "tracked.txt"), "v1")
	gitRun(t, repo, "add", "tracked.txt")
	gitRun(t, repo, "commit", "-m", "init")

	// Modify the tracked file (worktree change).
	writeFileT(t, filepath.Join(repo, "tracked.txt"), "v2")
	// Brand-new untracked file.
	writeFileT(t, filepath.Join(repo, "untracked.txt"), "fresh")
	// Staged-but-uncommitted.
	writeFileT(t, filepath.Join(repo, "staged.txt"), "added")
	gitRun(t, repo, "add", "staged.txt")

	st := loadGitStatus(repo, "")
	if !st.IsRepo {
		t.Fatal("expected IsRepo=true")
	}
	for _, want := range []string{"tracked.txt", "untracked.txt", "staged.txt"} {
		abs := filepath.Join(repo, want)
		if st.Files[abs] == filetree.GitChangeNone {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.Files))
		}
	}
}

// TestLoadGitStatus_FromSubdirectory makes sure the loader works when
// the editor was launched against a subdirectory of the repo, not the
// repo root. rev-parse --show-toplevel resolves the real top, and dirty
// paths still come back as absolute — even files outside the working
// rootDir but inside the repo.
func TestLoadGitStatus_FromSubdirectory(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	sub := filepath.Join(repo, "deep", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(sub, "inside.txt"), "x")
	writeFileT(t, filepath.Join(repo, "outside.txt"), "y")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")

	// Mutate both files so they appear dirty.
	writeFileT(t, filepath.Join(sub, "inside.txt"), "x2")
	writeFileT(t, filepath.Join(repo, "outside.txt"), "y2")

	st := loadGitStatus(sub, "")
	if !st.IsRepo {
		t.Fatal("subdirectory of a repo should still register as a repo")
	}
	for _, want := range []string{
		filepath.Join(sub, "inside.txt"),
		filepath.Join(repo, "outside.txt"),
	} {
		if st.Files[want] == filetree.GitChangeNone {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.Files))
		}
	}
}

// TestParseGitDiffLines maps unified hunk ranges to zero-based gutter rows.
func TestParseGitDiffLines(t *testing.T) {
	diff := []byte("@@ -2,0 +3,2 @@\n+a\n+b\n@@ -8,2 +10,2 @@\n-old\n+new\n@@ -20,2 +21,0 @@\n-old\n")
	got := parseGitDiffLines(diff)
	if got[2] != editor.GitLineAdded || got[3] != editor.GitLineAdded {
		t.Fatalf("added markers wrong: %v", got)
	}
	if got[9] != editor.GitLineModified || got[10] != editor.GitLineModified {
		t.Fatalf("modified markers wrong: %v", got)
	}
	if got[21] != editor.GitLineDeleted {
		t.Fatalf("deleted marker wrong: %v", got)
	}
}

// TestParseGitHunkPreview_ReturnsClickedHunk keeps gutter-click previews scoped
// to the hunk covering the clicked changed line.
func TestParseGitHunkPreview_ReturnsClickedHunk(t *testing.T) {
	diff := []byte("diff --git a/a.go b/a.go\n@@ -1,2 +1,2 @@\n old context\n-old\n+new\n@@ -20,1 +20,2 @@\n keep\n+added\n")
	got := parseGitHunkPreview(diff, 20)
	if len(got) == 0 {
		t.Fatal("expected hunk preview")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "+added") {
		t.Fatalf("expected clicked hunk, got %q", joined)
	}
	if strings.Contains(joined, "-old") {
		t.Fatalf("preview included wrong hunk: %q", joined)
	}
}

// TestLineInHunk_IncludesDeletionAnchor pins deleted-line marker matching.
func TestLineInHunk_IncludesDeletionAnchor(t *testing.T) {
	if !lineInHunk(12, 12, 0) {
		t.Fatal("deleted-only hunk should match its anchor line")
	}
	if lineInHunk(13, 12, 0) {
		t.Fatal("deleted-only hunk should not match unrelated lines")
	}
}

// TestDirtyFolderSet_RollsUpToRoot verifies that each dirty file paints
// every ancestor folder up to (and including) the project root, so a
// collapsed branch still shows the user there's a change inside.
func TestDirtyFolderSet_RollsUpToRoot(t *testing.T) {
	root := "/proj"
	dirty := map[string]filetree.GitChangeKind{
		"/proj/a/b/c/leaf.txt": filetree.GitChangeModified,
		"/proj/x/y.txt":        filetree.GitChangeModified,
	}
	got := dirtyFolderSet(dirty, root)

	want := []string{
		"/proj",
		"/proj/a",
		"/proj/a/b",
		"/proj/a/b/c",
		"/proj/x",
	}
	for _, w := range want {
		if got[w] == filetree.GitChangeNone {
			t.Errorf("expected %q to be marked dirty; got %v", w, sortedKeys(got))
		}
	}
	// The leaf file path itself isn't a folder, must not appear here.
	if got["/proj/a/b/c/leaf.txt"] != filetree.GitChangeNone {
		t.Error("dirtyFolderSet should not contain file paths")
	}
}

// TestDirtyFolderSet_StopsAtRoot proves the walk stops at root rather
// than continuing all the way to "/", so a sibling project directory
// or the user's home directory can't be marked dirty by us.
func TestDirtyFolderSet_StopsAtRoot(t *testing.T) {
	root := "/proj/inner"
	dirty := map[string]filetree.GitChangeKind{
		"/proj/inner/a/b.txt": filetree.GitChangeModified,
	}
	got := dirtyFolderSet(dirty, root)
	for _, ancestor := range []string{"/proj", "/", "/home"} {
		if got[ancestor] != filetree.GitChangeNone {
			t.Errorf("walk escaped root: %q should not be marked", ancestor)
		}
	}
	if got["/proj/inner"] == filetree.GitChangeNone {
		t.Error("root itself should be marked when something inside is dirty")
	}
	if got["/proj/inner/a"] == filetree.GitChangeNone {
		t.Error("intermediate folder should be marked")
	}
}

// TestDirtyFolderSet_EmptyInput returns an empty (non-nil) map so
// callers can safely range over the result without nil-checking.
func TestDirtyFolderSet_EmptyInput(t *testing.T) {
	got := dirtyFolderSet(nil, "/anywhere")
	if got == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

// TestRebaseGitPaths_NormalizesTreeRootCasing keeps git and filetree path keys
// aligned on case-insensitive filesystems where cwd casing may drift.
func TestRebaseGitPaths_NormalizesTreeRootCasing(t *testing.T) {
	dirty := map[string]filetree.GitChangeKind{
		"/Users/fatih/Documents/Projeler/skiff/internal/app/app.go": filetree.GitChangeModified,
	}
	rebased := rebaseGitPaths(dirty, "/Users/fatih/documents/projeler/skiff")
	want := "/Users/fatih/documents/projeler/skiff/internal/app/app.go"
	if rebased[want] != filetree.GitChangeModified {
		t.Fatalf("rebased path missing: got %v want key %q", rebased, want)
	}
}

// TestRebaseGitPaths_DoesNotMoveRepoPathsUnderSubdirRoot protects launches
// rooted at a subdirectory: only descendants of that tree root are rebased.
func TestRebaseGitPaths_DoesNotMoveRepoPathsUnderSubdirRoot(t *testing.T) {
	dirty := map[string]filetree.GitChangeKind{
		"/repo/internal/app/app.go":    filetree.GitChangeModified,
		"/repo/internal/editor/tab.go": filetree.GitChangeModified,
	}
	rebased := rebaseGitPaths(dirty, "/repo/internal/app")
	if rebased["/repo/internal/app/app.go"] != filetree.GitChangeModified {
		t.Fatalf("descendant path should stay under subdir root, got %v", rebased)
	}
	if rebased["/repo/internal/editor/tab.go"] != filetree.GitChangeModified {
		t.Fatalf("outside path should remain unchanged, got %v", rebased)
	}
}

// TestPathInside covers the core ancestry check used by dirtyFolderSet.
// Beyond the obvious matches, the prefix-trick trap ("/foo/bar" is NOT
// inside "/foo/ba") is the regression we care most about.
func TestPathInside(t *testing.T) {
	cases := []struct {
		candidate, root string
		want            bool
	}{
		{"/foo", "/foo", true},
		{"/foo/bar", "/foo", true},
		{"/foo/bar/baz", "/foo", true},
		{"/foo/ba", "/foo/bar", false},
		{"/foo/bar", "/foo/ba", false}, // string-prefix would lie here
		{"/sibling", "/foo", false},
		{"/", "/foo", false},
	}
	for _, tc := range cases {
		if got := pathInside(tc.candidate, tc.root); got != tc.want {
			t.Errorf("pathInside(%q, %q) = %v, want %v", tc.candidate, tc.root, got, tc.want)
		}
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// requireGit skips the calling test when git isn't on PATH. The encoding
// helpers don't need it; only the end-to-end flow does.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// initRepo creates a fresh git repo in t.TempDir and configures a local
// committer identity so commits in the test don't depend on the host's
// global git config.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test User")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	// macOS 'git init' may print a default-branch hint; force a stable name
	// so the tests work the same on every host.
	gitRun(t, dir, "checkout", "-q", "-b", "main")
	// On macOS the temp dir lives under /var, which is a symlink to
	// /private/var. git resolves the real path; rev-parse --show-toplevel
	// will report /private/var/... — tests use the same dir variable so
	// they compare the *resolved* path to itself. Force resolution here.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	return resolved
}

// gitRun invokes git in cwd. Fails the test on non-zero exit so a broken
// fixture doesn't masquerade as a code bug.
func gitRun(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
	}
}

// writeFileT writes content to path with sensible perms, failing the test
// on any IO error. (Named writeFileT to avoid colliding with the helper
// of the same name in modals_test.go.)
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// sortedKeys returns the keys of m in lexicographic order — handy when
// printing diff context inside test failures. The value type is
// unconstrained because only the keys are read.
func sortedKeys[K any](m map[string]K) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestRefreshGitStatus_RefreshesGutterInSingleFileMode pins the
// single-file-mode fix: with no file tree, refreshGitStatus must still
// reload the open tab's per-line gutter markers (a file-scoped git diff
// that doesn't need the tree). Without this, saving a file in
// single-file mode — which routes through refreshGitStatus — would
// leave the gutter markers frozen at their open-time state.
func TestRefreshGitStatus_RefreshesGutterInSingleFileMode(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	target := filepath.Join(repo, "f.go")
	writeFileT(t, target, "package main\n\nfunc main() {}\n")
	gitRun(t, repo, "add", "f.go")
	gitRun(t, repo, "commit", "-m", "init")

	a := newTestApp(t, repo)
	a.tree = nil // simulate single-file mode
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected an open tab")
	}

	// Clean file → no markers yet. Now dirty the worktree and clear the
	// tab's cached markers so we can prove refreshGitStatus repopulates
	// them despite tree == nil.
	writeFileT(t, target, "package main\n\nfunc main() { println(1) }\n")
	tab.GitLines = nil
	a.refreshGitStatus()

	if len(tab.GitLines) == 0 {
		t.Fatal("expected gutter markers to be refreshed in single-file mode, got none")
	}
}

// missingGitResult is a collection result that looks like what
// git.Repo.Status returns on a machine with no git binary: not a repo,
// nothing dirty, and the one bit that separates that from a plain
// directory.
func missingGitResult() gitStatusResult {
	return gitStatusResult{
		st:       gitStatus{GitMissing: true},
		tabLines: map[string]map[int]editor.GitLineChange{},
	}
}

// TestGitMissing_FlashesExactlyOnce is the reason ErrGitMissing exists.
// Without the binary the whole git surface is inert forever, so the user
// has to be told — but the fact is static for the process lifetime and
// the status tick fires every ten seconds, so telling them repeatedly
// would bury every other flash under it.
func TestGitMissing_FlashesExactlyOnce(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.statusMsg = ""
	a.handleGitStatusEvent(&gitStatusEvent{when: time.Now(), res: missingGitResult()})
	if !strings.Contains(a.statusMsg, "git was not found") {
		t.Fatalf("first snapshot must explain the missing binary, got %q", a.statusMsg)
	}

	for tick := 2; tick <= 5; tick++ {
		a.statusMsg = ""
		a.handleGitStatusEvent(&gitStatusEvent{when: time.Now(), res: missingGitResult()})
		if a.statusMsg != "" {
			t.Fatalf("tick %d re-flashed a static fact: %q", tick, a.statusMsg)
		}
	}
}

// TestGitMissing_QuietWhenGitExists guards the other direction: a plain
// directory on a machine that has git is the overwhelmingly common case
// and must stay silent.
func TestGitMissing_QuietWhenGitExists(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.statusMsg = ""
	a.handleGitStatusEvent(&gitStatusEvent{
		when: time.Now(),
		res:  gitStatusResult{tabLines: map[string]map[int]editor.GitLineChange{}},
	})
	if a.statusMsg != "" {
		t.Fatalf("a non-repo with git installed must not flash, got %q", a.statusMsg)
	}
}

// TestGitUnavailableMsg_SeparatesMissingBinaryFromNonRepo pins the copy
// the panel openers use. "Not a git repository" sends the user looking
// for a .git directory that was never the problem — on a machine with no
// git, every directory would say it, forever.
func TestGitUnavailableMsg_SeparatesMissingBinaryFromNonRepo(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.gitSnap = gitStatus{}
	a.statusMsg = ""
	a.toggleGitPanel()
	if a.statusMsg != "Not a git repository" {
		t.Fatalf("plain directory: got %q", a.statusMsg)
	}
	if a.gitPanel.active {
		t.Fatal("the panel must not open without a repo")
	}

	a.gitSnap = gitStatus{GitMissing: true}
	a.statusMsg = ""
	a.toggleGitPanel()
	if !strings.Contains(a.statusMsg, "git was not found on PATH") {
		t.Fatalf("missing binary: got %q", a.statusMsg)
	}
}

// TestGitPanelEmptyLabel_NamesMissingGit covers the panel's empty state.
// An empty change list means "clean tree" only when git actually ran;
// with no binary the same blank list would be an invented fact, and the
// user would have no way to learn why the panel is dead.
func TestGitPanelEmptyLabel_NamesMissingGit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	a.gitPanel.active = true
	a.gitPanel.rows = nil

	a.gitSnap = gitStatus{IsRepo: true, Branch: "main"}
	a.draw()
	scr.Show()
	if body := screenLine(scr, gitPanelListTop); !strings.Contains(body, "No uncommitted changes") {
		t.Fatalf("clean repo empty state: %q", body)
	}

	a.gitSnap = gitStatus{GitMissing: true}
	a.draw()
	scr.Show()
	body := screenLine(scr, gitPanelListTop)
	if !strings.Contains(body, "git was not found on PATH") {
		t.Fatalf("missing-git empty state must say so, got %q", body)
	}
	if strings.Contains(body, "No uncommitted changes") {
		t.Fatalf("missing-git empty state must not claim a clean tree, got %q", body)
	}
}

// TestLoadGitStatus_NoGitOnPath closes the loop between internal/git's
// sentinel and the flash above it: with nothing executable on PATH the
// real exec path has to come back GitMissing rather than as an ordinary
// "not a repo", or noteGitMissing never fires on the machines it exists
// for.
func TestLoadGitStatus_NoGitOnPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Join(dir, "definitely-empty"))

	st := loadGitStatus(dir, "")
	if !st.GitMissing {
		t.Fatalf("no git on PATH must set GitMissing, got %+v", st)
	}
	if st.IsRepo {
		t.Fatalf("no git on PATH cannot report a repo, got %+v", st)
	}
}

// TestParseGitDiffBatch_AttributesHunksToPaths drives the batched
// diff's splitter over one synthetic multi-file `--unified=0` stream
// covering every attribution shape the single-file loader handled by
// construction: a path with spaces (git prints those verbatim), a
// rename (the tab holds the NEW name, which is what the +++ line
// carries), a deletion (+++ is /dev/null, so the --- line must answer),
// and a C-quoted path (git quotes control characters).
func TestParseGitDiffBatch_AttributesHunksToPaths(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a dir/spaced name.txt b/a dir/spaced name.txt",
		"index 0000000..1111111 100644",
		// Spaced names carry git's trailing TAB in the ---/+++ lines
		// (GNU-patch compatibility) — real output, not an artifact.
		"--- a/a dir/spaced name.txt\t",
		"+++ b/a dir/spaced name.txt\t",
		"@@ -2,0 +3 @@ ctx",
		"+added line",
		"diff --git a/old.go b/renamed_new.go",
		"similarity index 90%",
		"rename from old.go",
		"rename to renamed_new.go",
		"index 2222222..3333333 100644",
		"--- a/old.go",
		"+++ b/renamed_new.go",
		"@@ -5 +5 @@ ctx",
		"-x",
		"+y",
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"index 4444444..0000000",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
		"-a",
		"-b",
		"-c",
		`diff --git "a/qu\totes.txt" "b/qu\totes.txt"`,
		"index 5555555..6666666 100644",
		`--- "a/qu\totes.txt"`,
		`+++ "b/qu\totes.txt"`,
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"",
	}, "\n")

	got := parseGitDiffBatch([]byte(diff))
	if len(got) != 4 {
		t.Fatalf("split found %d sections, want 4: %v", len(got), sortedKeys(got))
	}
	if m := got["a dir/spaced name.txt"]; m[2] != editor.GitLineAdded {
		t.Fatalf("spaced path: line 3 marker = %v, want added (%v)", m, editor.GitLineAdded)
	}
	if m := got["renamed_new.go"]; m[4] != editor.GitLineModified {
		t.Fatalf("renamed path: line 5 marker = %v, want modified", m)
	}
	if m := got["gone.txt"]; m[0] != editor.GitLineDeleted {
		t.Fatalf("deleted path: marker = %v, want deleted anchor at 0", m)
	}
	if m := got["qu\totes.txt"]; m[0] != editor.GitLineModified {
		t.Fatalf("quoted path: line 1 marker = %v, want modified", m)
	}
}

// TestParseGitDiffBatch_BodyLinesNeverStartASection guards the split
// against patch content that mimics headers: an added line whose text
// begins "++ " renders as "+++ ", and only lines before the first hunk
// may be read as headers.
func TestParseGitDiffBatch_BodyLinesNeverStartASection(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 0000000..1111111 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1 +1,2 @@",
		"-old",
		"+++ b/decoy.txt",
		"+--- a/decoy2.txt",
		"",
	}, "\n")

	got := parseGitDiffBatch([]byte(diff))
	if len(got) != 1 {
		t.Fatalf("split found %d sections, want 1: %v", len(got), sortedKeys(got))
	}
	m := got["f.txt"]
	if m == nil || m[0] != editor.GitLineModified || m[1] != editor.GitLineModified {
		t.Fatalf("f.txt markers = %v, want lines 1-2 modified", m)
	}
}

// TestCollectGitStatus_BatchMatchesSingleFileLoads is the batcher's
// equivalence contract against a real repo: with several tabs open —
// two modified (one with a space in its name), one clean — the single
// collection must hand every tab exactly what a per-file
// loadGitLineChanges would have, and the clean tab's key must still be
// PRESENT (applyGitStatus clears stale gutters only for keys it finds).
func TestCollectGitStatus_BatchMatchesSingleFileLoads(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	names := []string{"one.txt", "spaced name.txt", "clean.txt"}
	for _, n := range names {
		writeFileT(t, filepath.Join(repo, n), "l1\nl2\nl3\n")
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	writeFileT(t, filepath.Join(repo, "one.txt"), "l1\nchanged\nl3\n")
	writeFileT(t, filepath.Join(repo, "spaced name.txt"), "l1\nl2\nl3\nl4\n")

	paths := []string{
		filepath.Join(repo, "one.txt"),
		filepath.Join(repo, "spaced name.txt"),
		filepath.Join(repo, "clean.txt"),
	}
	res := collectGitStatus(repo, "", paths, false)

	for _, p := range paths {
		got, ok := res.tabLines[p]
		if !ok {
			t.Fatalf("%s: key missing from the batch result", p)
		}
		want := loadGitLineChanges(repo, "", p)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: batch %v != single %v", p, got, want)
		}
	}
	if len(res.tabLines[paths[2]]) != 0 {
		t.Fatalf("clean tab should carry no markers, got %v", res.tabLines[paths[2]])
	}
}

// gitCallLog puts a logging shim ahead of the real git on PATH and
// returns the log file its every invocation appends argv to. The
// production runner resolves "git" through PATH with the inherited
// environment, so this counts real forks without any seam in the code
// under test. Call it after all fixture setup so the log holds only
// the invocations the test means to count.
func gitCallLog(t *testing.T) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available on PATH")
	}
	shim := t.TempDir()
	log := filepath.Join(shim, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// countLoggedDiffs returns how many logged git invocations were gutter
// diffs — the fork the batcher exists to collapse.
func countLoggedDiffs(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "diff --unified=0") {
			n++
		}
	}
	return n
}

// TestCollectGitStatus_OneDiffForkForManyTabs pins the collapse itself:
// three dirty tabs used to cost three `git diff` forks per collection
// (every ten seconds, forever); now they cost one, and every tab still
// gets its markers.
func TestCollectGitStatus_OneDiffForkForManyTabs(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	names := []string{"a.txt", "b.txt", "c.txt"}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(repo, n)
		writeFileT(t, paths[i], "l1\nl2\n")
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	for _, p := range paths {
		writeFileT(t, p, "l1\nedited\n")
	}

	log := gitCallLog(t)
	res := collectGitStatus(repo, "", paths, false)

	if got := countLoggedDiffs(t, log); got != 1 {
		t.Fatalf("collection forked %d gutter diffs for 3 tabs, want 1", got)
	}
	for _, p := range paths {
		if res.tabLines[p][1] != editor.GitLineModified {
			t.Fatalf("%s: markers = %v, want line 2 modified", p, res.tabLines[p])
		}
	}
}

// TestCollectGitStatus_NoTabsSkipsDiffFork: the other end of the
// collapse — a collection with nothing open must not fork a diff at
// all.
func TestCollectGitStatus_NoTabsSkipsDiffFork(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")

	log := gitCallLog(t)
	res := collectGitStatus(repo, "", nil, false)

	if got := countLoggedDiffs(t, log); got != 0 {
		t.Fatalf("collection with no tabs forked %d gutter diffs, want 0", got)
	}
	if len(res.tabLines) != 0 {
		t.Fatalf("no tabs should yield no gutter entries, got %v", res.tabLines)
	}
}

// TestCollectGitStatus_CarriesIsDirForUntrackedDirs pins step 4 of the
// quiet tick: the collection itself (already off the event loop) stats
// the dirty paths and carries which ones are directories, so the Git
// panel's row builder needs no filesystem at all. A deleted path — the
// stat that fails — must come back unmarked, preserving the old
// stat-on-the-loop semantics exactly.
func TestCollectGitStatus_CarriesIsDirForUntrackedDirs(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "doomed.txt"), "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	if err := os.Mkdir(filepath.Join(repo, "newdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(repo, "newdir", "inside.txt"), "y\n")
	if err := os.Remove(filepath.Join(repo, "doomed.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	res := collectGitStatus(repo, "", nil, false)
	if !res.isDir[filepath.Join(repo, "newdir")] {
		t.Fatalf("untracked dir should be flagged, got %v (dirty: %v)",
			res.isDir, sortedKeys(res.st.Files))
	}
	if res.isDir[filepath.Join(repo, "doomed.txt")] {
		t.Fatal("a deleted path must never be flagged as a directory")
	}
}

// TestUnquoteGitPath covers the C-style quoting git applies to header
// paths with control or non-ASCII bytes — the escapes quote.c emits,
// octal included — plus the malformed shapes that must fail closed
// (empty string) rather than mis-attribute a section.
func TestUnquoteGitPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"b/plain.txt"`, "b/plain.txt"},
		{`"b/qu\totes.txt"`, "b/qu\totes.txt"},
		{`"b/back\\slash"`, `b/back\slash`},
		{`"b/dq\".txt"`, `b/dq".txt`},
		{`"b/na\303\257ve.txt"`, "b/naïve.txt"},
		{`b/notquoted`, ""},
		{`"unterminated`, ""},
		{`"bad\q"`, ""},
		{`"bad\30"`, ""},
	}
	for _, c := range cases {
		if got := unquoteGitPath(c.in); got != c.want {
			t.Fatalf("unquoteGitPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
