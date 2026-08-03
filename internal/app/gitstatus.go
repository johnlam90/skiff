// =============================================================================
// File: internal/app/gitstatus.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// gitstatus.go shells out to `git` to figure out which files inside the
// project root have uncommitted changes. The result feeds the file tree's
// "dirty" highlight: changed files render in the theme's Modified color,
// and any folder containing a dirty file picks up the same color so the
// signal isn't hidden behind a collapsed branch.
//
// Everything in here is best-effort — if the project isn't a git
// repo, or `git` isn't on PATH, or the command fails for any reason,
// loadGitStatus returns an empty result and the editor renders normally.
// We never block the UI on git, never spam errors at the user, and never
// retry on failure.

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/git"
)

// gitStatusResult bundles everything one status collection produces:
// the repo-level snapshot and the per-tab gutter line changes. Built by
// collectGitStatus — possibly on a background goroutine — and applied
// to the UI in one shot by App.applyGitStatus on the main thread.
type gitStatusResult struct {
	st       gitStatus
	tabLines map[string]map[int]editor.GitLineChange
}

// gitStatusEvent carries a finished background collection back to the
// main event loop, following the same custom-event pattern as
// treeRefreshEvent and finderRebuiltEvent.
type gitStatusEvent struct {
	when time.Time
	res  gitStatusResult
}

// When satisfies the tcell.Event interface.
func (e *gitStatusEvent) When() time.Time { return e.when }

// collectGitStatus runs the git side of a status refresh: the repo
// snapshot (skipped in single-file mode, which deliberately avoids the
// whole-repo walk) plus one `git diff` of gutter lines per open tab.
// It only shells out and builds data — no App state — so it is safe to
// run off the main thread.
func collectGitStatus(rootDir, base string, tabPaths []string, skipStatus bool) gitStatusResult {
	res := gitStatusResult{tabLines: map[string]map[int]editor.GitLineChange{}}
	if !skipStatus {
		res.st = loadGitStatus(rootDir, base)
	}
	for _, path := range tabPaths {
		res.tabLines[path] = loadGitLineChanges(rootDir, base, path)
	}
	return res
}

// gitStatus is the snapshot of a single git status run. IsRepo distinguishes
// "not a git repo" (don't bother trying again) from "git error" (we tried
// and bailed). DirtyFiles holds absolute paths to changed entries; callers
// should treat absence-of-key as "clean" rather than as "unknown". Branch
// is the human-readable current branch name, or a short SHA when HEAD is
// detached, or "" when we aren't in a repo.
type gitStatus struct {
	IsRepo     bool
	Root       string
	DirtyFiles map[string]filetree.GitChangeKind
	Branch     string
	// Ahead/Behind count commits versus the branch's upstream — the
	// status bar's ↑ ↓ arrows. Both zero when there is no upstream.
	Ahead  int
	Behind int
}

// loadGitStatus inspects rootDir and returns the set of dirty file paths
// reported by `git status --porcelain`. A non-git directory yields the
// zero value (IsRepo=false, no dirty paths). Any failure of the underlying
// commands degrades the same way — we'd rather lose the dirty highlight
// than crash the editor over a transient git issue.
func loadGitStatus(rootDir, base string) gitStatus {
	if rootDir == "" {
		return gitStatus{}
	}

	// rev-parse --show-toplevel does double duty: it tells us whether
	// we're in a git work tree at all (non-zero exit otherwise) and
	// gives us the absolute path of the repo root, which is the prefix
	// every porcelain path is reported relative to.
	topBytes, err := git.Output(rootDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitStatus{}
	}
	toplevel := strings.TrimRight(string(topBytes), "\n\r")
	if toplevel == "" {
		return gitStatus{}
	}

	ahead, behind := loadGitAheadBehind(rootDir)
	out, err := git.Output(rootDir, "status", "--porcelain")
	if err != nil {
		// We *are* in a repo (rev-parse succeeded) but couldn't read
		// status. Mark the result as a repo with no known dirty files
		// so the caller at least knows we tried.
		return gitStatus{IsRepo: true, Root: toplevel, DirtyFiles: map[string]filetree.GitChangeKind{}, Branch: loadGitBranch(rootDir), Ahead: ahead, Behind: behind}
	}

	dirty := parsePorcelain(out, toplevel)
	if base != "" {
		// Compare-against-ref mode (druk's diffBase): the change set is
		// everything different from base, merged with the porcelain's
		// untracked entries (a diff can't see files git doesn't track).
		vsBase := loadGitDiffNameStatus(rootDir, base, toplevel)
		for abs, kind := range dirty {
			if kind == filetree.GitChangeAdded {
				if _, exists := vsBase[abs]; !exists {
					vsBase[abs] = kind
				}
			}
		}
		dirty = vsBase
	}
	return gitStatus{IsRepo: true, Root: toplevel, DirtyFiles: dirty, Branch: loadGitBranch(rootDir), Ahead: ahead, Behind: behind}
}

// loadGitDiffNameStatus builds the dirty map versus an arbitrary ref
// via `git diff --name-status <ref>` — the loader behind the
// compare-against mode. Best-effort like every loader here.
func loadGitDiffNameStatus(rootDir, base, toplevel string) map[string]filetree.GitChangeKind {
	dirty := map[string]filetree.GitChangeKind{}
	out, err := git.Output(rootDir, "diff", "--name-status", base)
	if err != nil {
		return dirty
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		rel := parts[len(parts)-1] // renames list old	new; keep the new
		var kind filetree.GitChangeKind
		switch parts[0][0] {
		case 'A':
			kind = filetree.GitChangeAdded
		case 'D':
			kind = filetree.GitChangeDeleted
		case 'R':
			kind = filetree.GitChangeRenamed
		default:
			kind = filetree.GitChangeModified
		}
		dirty[filepath.Join(toplevel, rel)] = kind
	}
	return dirty
}

// loadGitAheadBehind counts commits versus the current branch's
// upstream: how many the user has that the upstream lacks (ahead — the
// "you haven't pushed" nudge) and vice versa (behind). A branch with no
// upstream, a detached HEAD, or any git failure yields 0/0 — the
// arrows simply don't render.
func loadGitAheadBehind(rootDir string) (ahead, behind int) {
	out, err := git.Output(rootDir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	// Left side of the symmetric difference is the upstream's commits
	// (we are behind by that many); the right side is ours (ahead).
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind
}

// rebaseGitPaths rewrites dirty paths to match the file tree root casing.
func rebaseGitPaths(paths map[string]filetree.GitChangeKind, treeRoot string) map[string]filetree.GitChangeKind {
	if len(paths) == 0 || treeRoot == "" {
		return paths
	}
	rebased := map[string]filetree.GitChangeKind{}
	for path, kind := range paths {
		rel, ok := relFromRoot(path, treeRoot)
		if !ok {
			rebased[path] = kind
			continue
		}
		rebased[filepath.Join(treeRoot, rel)] = kind
	}
	return rebased
}

// relFromRoot returns path relative to root, tolerating macOS path casing drift.
func relFromRoot(path, root string) (string, bool) {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel, true
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if strings.EqualFold(path, root) {
		return ".", true
	}
	prefix := root + string(filepath.Separator)
	if len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix) {
		return path[len(prefix):], true
	}
	return "", false
}

// loadGitBranch returns the current branch name for rootDir, or a short
// commit SHA when HEAD is detached (rebase / bisect / a manual checkout
// of a tag). Returns "" for non-repos and any other failure mode — the
// caller treats that as "no branch label to show" and the status bar
// just doesn't render one.
//
// We try `symbolic-ref --short HEAD` first because it's the cheapest way
// to distinguish "on a branch" from "detached"; the fallback to
// `rev-parse --short HEAD` only fires when symbolic-ref's non-zero exit
// tells us we're detached.
func loadGitBranch(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	if out, err := git.Output(rootDir, "symbolic-ref", "--short", "HEAD"); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	if out, err := git.Output(rootDir, "rev-parse", "--short", "HEAD"); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	return ""
}

// parsePorcelain converts the bytes returned by `git status --porcelain`
// into a set of absolute file paths. Split out from loadGitStatus so it
// can be exercised by tests without spawning a subprocess.
//
// The porcelain v1 format (without -z) is:
//
//	XY <path>
//	XY <oldpath> -> <newpath>      (renames / copies)
//	XY "quoted path with spaces"   (when core.quotePath is on, the default)
//
// We treat any line as dirty regardless of the X/Y status codes; for renames
// we mark both the old and new paths so the user sees both rows tinted.
func parsePorcelain(out []byte, toplevel string) map[string]filetree.GitChangeKind {
	dirty := map[string]filetree.GitChangeKind{}
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		line := string(raw)
		if len(line) < 4 {
			continue
		}
		kind := porcelainKind(line[:2])
		// Drop the two status chars + the separating space.
		body := line[3:]

		if idx := strings.Index(body, " -> "); idx >= 0 {
			oldPath := unquotePath(body[:idx])
			newPath := unquotePath(body[idx+len(" -> "):])
			if oldPath != "" {
				dirty[filepath.Join(toplevel, oldPath)] = filetree.GitChangeDeleted
			}
			if newPath != "" {
				dirty[filepath.Join(toplevel, newPath)] = filetree.GitChangeRenamed
			}
			continue
		}

		path := unquotePath(body)
		if path == "" {
			continue
		}
		dirty[filepath.Join(toplevel, path)] = kind
	}
	return dirty
}

// porcelainKind maps git porcelain's XY status pair to the tree status kind.
func porcelainKind(code string) filetree.GitChangeKind {
	if strings.Contains(code, "?") || strings.Contains(code, "A") {
		return filetree.GitChangeAdded
	}
	if strings.Contains(code, "D") {
		return filetree.GitChangeDeleted
	}
	if strings.Contains(code, "R") || strings.Contains(code, "C") {
		return filetree.GitChangeRenamed
	}
	return filetree.GitChangeModified
}

// unquotePath undoes git's C-style quoting (enabled by default via
// core.quotePath) so paths with spaces, unicode, or control chars come
// back as a normal Go string. Falls back to the raw input on any parse
// error — that's safer than dropping a path the user might want flagged.
func unquotePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	if unq, err := strconv.Unquote(s); err == nil {
		return unq
	}
	return s
}

// dirtyFolderSet rolls a set of dirty file paths up to every ancestor
// folder under root. A folder is "dirty" if any of its descendants are
// dirty, so collapsed branches still signal that there's something
// changed inside.
func dirtyFolderSet(dirtyFiles map[string]filetree.GitChangeKind, root string) map[string]filetree.GitChangeKind {
	folders := map[string]filetree.GitChangeKind{}
	if len(dirtyFiles) == 0 {
		return folders
	}
	root = filepath.Clean(root)
	for path, kind := range dirtyFiles {
		// Walk up from each dirty file's parent toward the root,
		// marking every ancestor inside the project. The walk halts
		// the moment we step outside root so a file outside the
		// editor's scope can't paint folders we don't render.
		for p := filepath.Dir(path); p != "" && p != "."; p = filepath.Dir(p) {
			if !pathInside(p, root) {
				break
			}
			if folders[p] == kind || folders[p] == filetree.GitChangeMixed {
				break // already marked by a sibling — skip the rest.
			}
			if folders[p] != filetree.GitChangeNone && folders[p] != kind {
				folders[p] = filetree.GitChangeMixed
			} else {
				folders[p] = kind
			}
			if p == root {
				break
			}
		}
	}
	return folders
}

// loadGitLineChanges returns line-level changes for path versus base
// ("" = HEAD, the everyday worktree gutter).
func loadGitLineChanges(rootDir, base, path string) map[int]editor.GitLineChange {
	if rootDir == "" || path == "" {
		return nil
	}
	if base == "" {
		base = "HEAD"
	}
	out, err := git.Output(rootDir, "diff", "--unified=0", base, "--", path)
	if err != nil || len(out) == 0 {
		return nil
	}
	return parseGitDiffLines(out)
}

// loadGitFileDiff returns the full unified diff for path, one display
// line per entry — the Git panel's per-file diff preview. Tracked files
// diff against HEAD. untracked=true enables a fallback for files git
// has never seen (they don't appear in `git diff HEAD` at all):
// `git diff --no-index /dev/null <path>` renders the whole file as an
// all-added diff. The flag comes from the caller's porcelain status —
// gating on it keeps the fallback from painting a *clean* tracked file
// as brand new just because its HEAD diff is empty. Same best-effort
// contract as every other loader here: nil on any failure and the
// caller shows a friendly placeholder instead.
func loadGitFileDiff(rootDir, base, path string, untracked bool) []string {
	if rootDir == "" || path == "" {
		return nil
	}
	if base == "" {
		base = "HEAD"
	}
	out, err := git.Output(rootDir, "diff", base, "--", path)
	if err != nil || len(out) == 0 {
		if !untracked {
			return nil
		}
		// Hand --no-index a path relative to rootDir when we can — git
		// echoes the argument verbatim into the +++ header, and a full
		// absolute path there is noise the user has to scan past.
		if rel, relErr := filepath.Rel(rootDir, path); relErr == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		// --no-index exits 1 whenever the files differ, so the error is
		// expected; the output being non-empty is the success signal.
		out, _ = git.Output(rootDir, "diff", "--no-index", "--", os.DevNull, path)
		if len(out) == 0 {
			return nil
		}
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// loadGitHunkPreview returns the unified diff hunk covering zero-based line.
func loadGitHunkPreview(rootDir, path string, line int) []string {
	if rootDir == "" || path == "" || line < 0 {
		return nil
	}
	out, err := git.Output(rootDir, "diff", "--unified=3", "HEAD", "--", path)
	if err != nil || len(out) == 0 {
		return nil
	}
	return parseGitHunkPreview(out, line)
}

// parseGitHunkPreview extracts the diff hunk covering zero-based line.
func parseGitHunkPreview(out []byte, line int) []string {
	target := line + 1
	var current []string
	match := false
	flush := func() []string {
		if match && len(current) > 0 {
			return current
		}
		return nil
	}
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		text := string(raw)
		if strings.HasPrefix(text, "@@ ") {
			if hunk := flush(); hunk != nil {
				return hunk
			}
			_, _, newStart, newCount, ok := parseHunkHeader(text)
			current = []string{text}
			match = ok && lineInHunk(target, newStart, newCount)
			continue
		}
		if len(current) == 0 {
			continue
		}
		current = append(current, text)
	}
	return flush()
}

// lineInHunk reports whether target one-based line belongs to a new-file range.
func lineInHunk(target, start, count int) bool {
	if count == 0 {
		return target == start
	}
	return target >= start && target < start+count
}

// parseGitDiffLines converts unified diff hunks into editor gutter markers.
func parseGitDiffLines(out []byte) map[int]editor.GitLineChange {
	changes := map[int]editor.GitLineChange{}
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		line := string(raw)
		if !strings.HasPrefix(line, "@@ ") {
			continue
		}
		oldStart, oldCount, newStart, newCount, ok := parseHunkHeader(line)
		if !ok {
			continue
		}
		if newCount == 0 {
			mark := newStart
			if mark < 0 {
				mark = 0
			}
			changes[mark] = editor.GitLineDeleted
			_ = oldStart
			_ = oldCount
			continue
		}
		kind := editor.GitLineAdded
		if oldCount > 0 {
			kind = editor.GitLineModified
		}
		for lineNo := newStart; lineNo < newStart+newCount; lineNo++ {
			changes[lineNo-1] = kind
		}
	}
	return changes
}

// parseHunkHeader extracts old/new ranges from a unified diff header.
func parseHunkHeader(line string) (int, int, int, int, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, 0, 0, false
	}
	oldStart, oldCount, ok := parseDiffRange(fields[1])
	if !ok {
		return 0, 0, 0, 0, false
	}
	newStart, newCount, ok := parseDiffRange(fields[2])
	if !ok {
		return 0, 0, 0, 0, false
	}
	return oldStart, oldCount, newStart, newCount, true
}

// parseDiffRange parses a hunk range such as -1,2 or +7.
func parseDiffRange(s string) (int, int, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	parts := strings.SplitN(s[1:], ",", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	count := 1
	if len(parts) == 2 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// pathInside reports whether candidate is root or a descendant of root.
// Uses filepath.Rel rather than string-prefix matching so '/foo/bar'
// isn't considered inside '/foo/ba'.
func pathInside(candidate, root string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}
