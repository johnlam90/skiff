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
	if st.DirtyFiles != nil {
		t.Fatalf("plain dir should have nil DirtyFiles, got %v", st.DirtyFiles)
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
	if len(st.DirtyFiles) != 0 {
		t.Fatalf("expected no dirty files, got %v", st.DirtyFiles)
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

// TestLoadGitAheadBehind_CountsBothDirections builds a repo with a
// bare upstream, pushes, then creates one unpushed commit and rewinds
// past one pushed commit — landing 1 ahead and 1 behind at once. Pins
// the left/right order of rev-list's symmetric difference, the mistake
// this helper exists to encapsulate.
func TestLoadGitAheadBehind_CountsBothDirections(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	bare := t.TempDir()
	gitRun(t, bare, "init", "-q", "--bare")
	writeFileT(t, filepath.Join(repo, "a.txt"), "one\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "c1")
	writeFileT(t, filepath.Join(repo, "a.txt"), "two\n")
	gitRun(t, repo, "commit", "-q", "-am", "c2")
	gitRun(t, repo, "remote", "add", "origin", bare)
	gitRun(t, repo, "push", "-q", "-u", "origin", "main")

	// Rewind one pushed commit (now behind 1), then commit something
	// new on top (now also ahead 1).
	gitRun(t, repo, "reset", "-q", "--hard", "HEAD~1")
	writeFileT(t, filepath.Join(repo, "b.txt"), "local\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "local-only")

	ahead, behind := loadGitAheadBehind(repo)
	if ahead != 1 || behind != 1 {
		t.Fatalf("ahead=%d behind=%d, want 1/1", ahead, behind)
	}
}

// TestLoadGitAheadBehind_NoUpstreamIsZero verifies branches without an
// upstream (and non-repos) degrade to 0/0 so the arrows just don't
// render.
func TestLoadGitAheadBehind_NoUpstreamIsZero(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "c1")
	if a, b := loadGitAheadBehind(repo); a != 0 || b != 0 {
		t.Fatalf("no-upstream: got %d/%d, want 0/0", a, b)
	}
	if a, b := loadGitAheadBehind(t.TempDir()); a != 0 || b != 0 {
		t.Fatalf("non-repo: got %d/%d, want 0/0", a, b)
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
	a.tabs[1].GitLines = keep

	a.applyGitStatus(gitStatusResult{
		st: gitStatus{IsRepo: true, Root: dir, Branch: "main", DirtyFiles: map[string]filetree.GitChangeKind{}},
		tabLines: map[string]map[int]editor.GitLineChange{
			tracked: {0: editor.GitLineModified},
		},
	})

	if got := a.tabs[0].GitLines[0]; got != editor.GitLineModified {
		t.Fatalf("tab 0 gutter should update, got %v", got)
	}
	if got := a.tabs[1].GitLines[3]; got != editor.GitLineAdded {
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

// TestLoadGitBranch_NotARepo confirms the helper degrades quietly when
// the directory isn't a git work tree — empty string, no panic, no
// stderr noise reaching the editor.
func TestLoadGitBranch_NotARepo(t *testing.T) {
	if got := loadGitBranch(t.TempDir()); got != "" {
		t.Fatalf("non-repo branch = %q, want empty", got)
	}
	if got := loadGitBranch(""); got != "" {
		t.Fatalf("empty rootDir branch = %q, want empty", got)
	}
}

// TestLoadGitBranch_OnBranch checks the happy path — a fresh repo
// checked out on `main` returns "main".
func TestLoadGitBranch_OnBranch(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	if got := loadGitBranch(repo); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
}

// TestLoadGitBranch_TracksRename confirms a rename of the current
// branch is reflected on the next call — this is the whole point of
// the 10s tick: the user's checkout state is allowed to change behind
// the editor's back.
func TestLoadGitBranch_TracksRename(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")
	gitRun(t, repo, "branch", "-m", "main", "feat/something")
	if got := loadGitBranch(repo); got != "feat/something" {
		t.Fatalf("after rename branch = %q, want feat/something", got)
	}
}

// TestLoadGitBranch_DetachedHEAD asserts the symbolic-ref fallback
// kicks in: when HEAD is detached at a commit, the helper returns a
// short SHA instead of an empty string, so the status bar still shows
// *something* useful instead of vanishing mid-rebase.
func TestLoadGitBranch_DetachedHEAD(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")
	gitRun(t, repo, "checkout", "-q", "--detach", "HEAD")

	got := loadGitBranch(repo)
	if got == "" {
		t.Fatal("detached HEAD branch came back empty; expected a short SHA")
	}
	if got == "main" {
		t.Fatalf("detached HEAD reported branch name %q; expected SHA", got)
	}
	if len(got) > 12 || len(got) < 4 {
		t.Fatalf("detached HEAD output %q doesn't look like a short SHA", got)
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
		if st.DirtyFiles[abs] == filetree.GitChangeNone {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.DirtyFiles))
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
		if st.DirtyFiles[want] == filetree.GitChangeNone {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.DirtyFiles))
		}
	}
}

// TestParsePorcelain_BasicCases pins down the byte-level porcelain v1
// parser. Each case mirrors something `git status --porcelain` actually
// produces — we want regression coverage on the format itself, not just
// the happy path through real git.
func TestParsePorcelain_BasicCases(t *testing.T) {
	top := "/tmp/repo"
	cases := []struct {
		name     string
		input    string
		wantKeys []string
	}{
		{
			name:     "single modified",
			input:    " M file.txt\n",
			wantKeys: []string{"/tmp/repo/file.txt"},
		},
		{
			name:     "untracked",
			input:    "?? new.go\n",
			wantKeys: []string{"/tmp/repo/new.go"},
		},
		{
			name:     "staged plus modified",
			input:    "MM file.go\n",
			wantKeys: []string{"/tmp/repo/file.go"},
		},
		{
			name:     "multiple lines",
			input:    " M a.txt\n?? b.txt\nA  c.txt\n",
			wantKeys: []string{"/tmp/repo/a.txt", "/tmp/repo/b.txt", "/tmp/repo/c.txt"},
		},
		{
			name:     "rename marks both old and new",
			input:    "R  oldname.txt -> newname.txt\n",
			wantKeys: []string{"/tmp/repo/oldname.txt", "/tmp/repo/newname.txt"},
		},
		{
			name:     "quoted path with spaces",
			input:    " M \"weird name.txt\"\n",
			wantKeys: []string{"/tmp/repo/weird name.txt"},
		},
		{
			name:     "blank input",
			input:    "",
			wantKeys: nil,
		},
		{
			name:     "junk too short to parse is dropped",
			input:    "M\n",
			wantKeys: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePorcelain([]byte(tc.input), top)
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("count mismatch: want %d (%v), got %d (%v)",
					len(tc.wantKeys), tc.wantKeys,
					len(got), sortedKeys(got))
			}
			for _, k := range tc.wantKeys {
				if got[k] == filetree.GitChangeNone {
					t.Errorf("missing %q in %v", k, sortedKeys(got))
				}
			}
		})
	}
}

// TestParsePorcelain_StatusKinds confirms the tree can color different git
// states distinctly instead of collapsing everything to one dirty color.
func TestParsePorcelain_StatusKinds(t *testing.T) {
	top := "/tmp/repo"
	got := parsePorcelain([]byte(" M mod.go\n?? new.go\n D gone.go\nR  old.go -> moved.go\n"), top)
	want := map[string]filetree.GitChangeKind{
		"/tmp/repo/mod.go":   filetree.GitChangeModified,
		"/tmp/repo/new.go":   filetree.GitChangeAdded,
		"/tmp/repo/gone.go":  filetree.GitChangeDeleted,
		"/tmp/repo/old.go":   filetree.GitChangeDeleted,
		"/tmp/repo/moved.go": filetree.GitChangeRenamed,
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Fatalf("%s kind = %v, want %v; got %v", path, got[path], kind, got)
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

// TestUnquotePath_Variants verifies the C-style unquoter handles git's
// default quoting — quoted paths come back clean, unquoted paths pass
// through, and a malformed quoted string falls back to the raw input
// rather than dropping the path entirely.
func TestUnquotePath_Variants(t *testing.T) {
	cases := map[string]string{
		`plain.txt`:          `plain.txt`,
		`"quoted.txt"`:       `quoted.txt`,
		`"with space.txt"`:   `with space.txt`,
		`"escaped\nnewline"`: "escaped\nnewline",
		`""`:                 ``,
		`   spaced.txt   `:   `spaced.txt`,
		``:                   ``,
		`"unterminated`:      `"unterminated`, // malformed → raw fallback
	}
	for in, want := range cases {
		if got := unquotePath(in); got != want {
			t.Errorf("unquotePath(%q) = %q, want %q", in, got, want)
		}
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
// printing diff context inside test failures.
func sortedKeys[K comparable](m map[string]K) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
