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
// the repo-level snapshot, the per-tab gutter line changes, and which
// dirty paths are directories on disk (the Git panel's trailing-slash
// answer, stat'd here so the row builder never touches the filesystem
// on the event loop). Built by collectGitStatus — possibly on a
// background goroutine — and applied to the UI in one shot by
// App.applyGitStatus on the main thread.
type gitStatusResult struct {
	st       gitStatus
	tabLines map[string]map[int]editor.GitLineChange

	// isDir holds true for each snapshot path that stats as a
	// directory — untracked dirs, which porcelain reports as one
	// collapsed entry. Paths that fail to stat (deleted) are simply
	// absent, so a lookup answers false for them, exactly like the
	// on-loop stat used to.
	isDir map[string]bool
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
// whole-repo walk) plus ONE `git diff` of gutter lines covering every
// open tab — ten tabs used to mean ten diff forks per collection,
// every ten seconds, forever. It only shells out and builds data — no
// App state — so it is safe to run off the main thread.
//
// A single tab keeps the single-path loader: one fork either way, and
// the per-path form needs no header attribution at all. No tabs forks
// nothing.
func collectGitStatus(rootDir, base string, tabPaths []string, skipStatus bool) gitStatusResult {
	res := gitStatusResult{tabLines: map[string]map[int]editor.GitLineChange{}}
	if !skipStatus {
		res.st = loadGitStatus(rootDir, base)
		res.isDir = statDirtyDirs(res.st.Files)
	}
	switch len(tabPaths) {
	case 0:
	case 1:
		res.tabLines[tabPaths[0]] = loadGitLineChanges(rootDir, base, tabPaths[0])
	default:
		batchGitLineChanges(res.tabLines, rootDir, base, res.st.Root, tabPaths)
	}
	return res
}

// batchGitLineChanges fills dst with gutter line changes for every path
// using one `git diff --unified=0 <ref> -- p1 … pN` invocation, split
// on its per-file headers. Every requested path gets an entry — nil
// for a clean (or unattributable) tab — because applyGitStatus clears
// stale gutters only for keys it finds.
//
// toplevel is the repo root the diff's header paths are relative to,
// normally free from the snapshot the same collection just took; when
// the caller has none (multi-tab single-file mode, a failed status) one
// rev-parse resolves it — still a win over one diff per tab. base is
// repo-controlled (a clone can ship a branch named anything), so it
// goes through SafeRef and the paths sit behind `--`, same as
// diffNameStatus. Any failure degrades to nil markers, the package's
// standing best-effort contract.
func batchGitLineChanges(dst map[string]map[int]editor.GitLineChange, rootDir, base, toplevel string, paths []string) {
	for _, p := range paths {
		dst[p] = nil
	}
	if rootDir == "" {
		return
	}
	if toplevel == "" {
		out, err := git.Output(rootDir, "rev-parse", "--show-toplevel")
		if err != nil {
			return
		}
		toplevel = strings.TrimRight(string(out), "\n\r")
		if toplevel == "" {
			return
		}
	}
	ref := "HEAD"
	if base != "" {
		safe, err := git.SafeRef(base)
		if err != nil {
			return
		}
		ref = safe
	}
	// The explicit prefixes pin the header shape the attribution parses
	// against a user's diff.noprefix/diff.mnemonicPrefix config.
	args := append([]string{"diff", "--unified=0", "--src-prefix=a/", "--dst-prefix=b/", ref, "--"}, paths...)
	out, err := git.Output(rootDir, args...)
	if err != nil || len(out) == 0 {
		return
	}
	byRel := make(map[string]string, len(paths))
	for _, p := range paths {
		// relFromRoot rather than a plain Rel: tab paths can disagree
		// with the repo root on casing (macOS), and an unmatched tab
		// must simply keep nil markers, exactly like a tab outside the
		// repo.
		if rel, ok := relFromRoot(p, toplevel); ok {
			byRel[filepath.ToSlash(rel)] = p
		}
	}
	for rel, lines := range parseGitDiffBatch(out) {
		if abs, ok := byRel[rel]; ok {
			dst[abs] = lines
		}
	}
}

// parseGitDiffBatch splits a multi-file unified diff into per-file
// sections and runs the existing hunk parser over each, keyed by the
// section's repo-relative path. Sections that name no usable path (a
// binary file note, a mode-only change) are dropped — they carry no
// hunks anyway.
func parseGitDiffBatch(out []byte) map[string]map[int]editor.GitLineChange {
	res := map[string]map[int]editor.GitLineChange{}
	for _, section := range splitGitDiffSections(out) {
		if rel := sectionTargetRel(section); rel != "" {
			res[rel] = parseGitDiffLines(section)
		}
	}
	return res
}

// splitGitDiffSections cuts a combined diff at each `diff --git ` file
// header, returning subslices of out (no copying). Only a line START
// can open a section: inside hunk bodies every line carries a +/-/space
// prefix, so patch content can never fake the header.
func splitGitDiffSections(out []byte) [][]byte {
	header := []byte("diff --git ")
	var starts []int
	for i := 0; i >= 0 && i < len(out); {
		if bytes.HasPrefix(out[i:], header) {
			starts = append(starts, i)
		}
		j := bytes.IndexByte(out[i:], '\n')
		if j < 0 {
			break
		}
		i += j + 1
	}
	sections := make([][]byte, 0, len(starts))
	for k, s := range starts {
		end := len(out)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		sections = append(sections, out[s:end])
	}
	return sections
}

// sectionTargetRel extracts the repo-relative path a diff section
// belongs to. The `+++ b/<new>` line answers for everything a tab can
// hold — modifications, renames (the tab has the new name) — and the
// `--- a/<old>` line covers deletions, where +++ is /dev/null. The
// scan stops at the first hunk: body lines may legitimately start with
// "+++ " or "--- " (an added line whose text begins "++ "), and only
// the header block above the hunks may be trusted.
func sectionTargetRel(section []byte) string {
	oldRel := ""
	for _, line := range bytes.Split(section, []byte{'\n'}) {
		text := string(line)
		switch {
		case strings.HasPrefix(text, "@@ "):
			return oldRel
		case strings.HasPrefix(text, "+++ "):
			if rel := headerPath(text[4:], "b/"); rel != "" {
				return rel
			}
		case strings.HasPrefix(text, "--- "):
			oldRel = headerPath(text[4:], "a/")
		}
	}
	return oldRel
}

// headerPath turns one `---`/`+++` header operand into a repo-relative
// path: unquote git's C-style form when present, reject /dev/null, and
// strip the expected a// b/ prefix — anything else fails closed to ""
// so a section is dropped rather than mis-attributed. An UNQUOTED name
// containing blanks arrives with one trailing TAB (git's GNU-patch
// compatibility marker for "the name ends here"); the quoted form
// never carries it, and a real tab inside a name forces quoting, so
// stripping one from the unquoted form is unambiguous.
func headerPath(raw, prefix string) string {
	if strings.HasPrefix(raw, `"`) {
		raw = unquoteGitPath(raw)
		if raw == "" {
			return ""
		}
	} else {
		raw = strings.TrimSuffix(raw, "\t")
	}
	if raw == os.DevNull || raw == "/dev/null" {
		return ""
	}
	if !strings.HasPrefix(raw, prefix) {
		return ""
	}
	return raw[len(prefix):]
}

// unquoteGitPath decodes the C-style quoting git applies to header
// paths containing control or non-ASCII bytes (quote.c: \a \b \f \n
// \r \t \v, \\, \", and three-digit octal). Returns "" for anything
// malformed — failing closed beats guessing at a path. Content after
// the closing quote is ignored; git writes none.
func unquoteGitPath(q string) string {
	if len(q) < 2 || q[0] != '"' {
		return ""
	}
	var b strings.Builder
	for i := 1; i < len(q); i++ {
		c := q[i]
		if c == '"' {
			return b.String()
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(q) {
			return ""
		}
		switch e := q[i]; e {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '\\', '"':
			b.WriteByte(e)
		default:
			if e < '0' || e > '7' || i+2 >= len(q) ||
				q[i+1] < '0' || q[i+1] > '7' || q[i+2] < '0' || q[i+2] > '7' {
				return ""
			}
			b.WriteByte((e-'0')<<6 | (q[i+1]-'0')<<3 | (q[i+2] - '0'))
			i += 2
		}
	}
	return "" // unterminated quote
}

// gitStatus is the git package's Snapshot under its historical
// app-side name.
type gitStatus = git.Snapshot

// loadGitStatus inspects rootDir and returns the set of dirty file paths
// reported by `git status --porcelain`. A non-git directory yields the
// zero value (IsRepo=false, no dirty paths). Any failure of the underlying
// commands degrades the same way — we'd rather lose the dirty highlight
// than crash the editor over a transient git issue.
func loadGitStatus(rootDir, base string) gitStatus {
	return git.Open(rootDir).Status(base)
}

// gitUnavailableMsg explains why there is nothing git-shaped to show.
// "Not a git repository" is a lie on a machine with no git binary:
// every directory would say it, forever, and the user would go looking
// for a .git that was never the problem. internal/git already draws the
// distinction (ErrGitMissing → Snapshot.GitMissing); this is the caller
// the sentinel was added for.
func (a *App) gitUnavailableMsg() string {
	if a.gitSnap.GitMissing {
		return "git was not found on PATH"
	}
	return "Not a git repository"
}

// gitPanelEmptyLabel is the line the Git panel draws when it has no
// rows. An empty list means "clean tree" only when git actually ran —
// claiming it on a machine without the binary invents a fact, and
// leaves the user with no way to learn why the whole git surface is
// inert.
func (a *App) gitPanelEmptyLabel() string {
	if a.gitSnap.GitMissing {
		return a.gitUnavailableMsg()
	}
	return "No uncommitted changes"
}

// noteGitMissing tells the user once — and only once — that this
// machine has no git binary. The fact is static for the process
// lifetime, so the 10-second status tick would otherwise reprint it
// forever and bury every other flash under it.
func (a *App) noteGitMissing(st gitStatus) {
	if !st.GitMissing || a.gitMissingSeen {
		return
	}
	a.gitMissingSeen = true
	a.flash("git was not found on PATH — branch and change badges are off")
}

// readRepo returns the handle the app's asynchronous git reads go
// through: real git in production, or the injected Runner when a test
// installed one. Call it on the main thread and hand the result to the
// goroutine — a *git.Repo is immutable after construction, so passing
// it across is the safe alternative to letting background work reach
// back into App for a.rootDir.
func (a *App) readRepo() *git.Repo {
	if a.gitRunner != nil {
		return git.OpenWith(a.rootDir, a.gitRunner)
	}
	return git.Open(a.rootDir)
}

// statDirtyDirs stats each dirty path and returns the set that are
// directories on disk. Runs inside the collection (off the event loop
// when async) so buildGitChangesRows can stay pure — this used to be a
// stat per changed path on the main thread every time the Git panel
// rebuilt. Only true entries are stored: a deleted path's failed stat
// is the absence a lookup reads as false.
func statDirtyDirs(files map[string]git.ChangeKind) map[string]bool {
	if len(files) == 0 {
		return nil
	}
	var isDir map[string]bool
	for abs := range files {
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			if isDir == nil {
				isDir = map[string]bool{}
			}
			isDir[abs] = true
		}
	}
	return isDir
}

// rebaseGitPaths rewrites dirty paths to match the file tree root casing.
func rebaseGitPaths(paths map[string]filetree.GitChangeKind, treeRoot string) map[string]filetree.GitChangeKind {
	return rebaseByRoot(paths, treeRoot)
}

// rebaseByRoot is rebaseGitPaths' shape-generic core: every key that
// resolves under treeRoot (case drift tolerated) is rewritten onto the
// tree root's own casing so lookups keyed by tree paths hit. Shared by
// the dirty-kind map and the isDir map, which must be rebased the same
// way or the panel's directory flags would miss on macOS.
func rebaseByRoot[V any](paths map[string]V, treeRoot string) map[string]V {
	if len(paths) == 0 || treeRoot == "" {
		return paths
	}
	rebased := make(map[string]V, len(paths))
	for path, v := range paths {
		rel, ok := relFromRoot(path, treeRoot)
		if !ok {
			rebased[path] = v
			continue
		}
		rebased[filepath.Join(treeRoot, rel)] = v
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
	return repoFileDiff(git.Open(rootDir), base, path, untracked)
}

// repoFileDiff is loadGitFileDiff's body over an explicit Repo handle.
// The handle is the seam the async diff path needs: a *git.Repo is
// immutable once opened, so the main loop can capture one and hand it to
// a goroutine without sharing App state — and a test can hand over one
// backed by git.Fake instead of paying for a subprocess and a repo in
// exactly the right state.
func repoFileDiff(repo *git.Repo, base, path string, untracked bool) []string {
	rootDir := repo.Root()
	if rootDir == "" || path == "" {
		return nil
	}
	if base == "" {
		base = "HEAD"
	}
	out, err := repo.Output("diff", base, "--", path)
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
		out, _ = repo.Output("diff", "--no-index", "--", os.DevNull, path)
		if len(out) == 0 {
			return nil
		}
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// repoHunkPreview returns the unified diff hunk covering zero-based line.
// It takes a Repo handle rather than a root path because its only caller
// is the asynchronous gutter-click path: a *git.Repo is immutable once
// opened, so the main loop can capture one and hand it to a goroutine
// without sharing App state — and a test can hand over one backed by
// git.Fake instead of paying for a subprocess and a repo in exactly the
// right state. See repoFileDiff, whose rootDir-taking wrapper survives
// for the Git panel's synchronous callers.
func repoHunkPreview(repo *git.Repo, path string, line int) []string {
	if repo.Root() == "" || path == "" || line < 0 {
		return nil
	}
	out, err := repo.Output("diff", "--unified=3", "HEAD", "--", path)
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

// refreshGitStatus re-runs `git status --porcelain` against the project
// root and stamps the resulting dirty-paths sets onto the file tree, so
// changed files render in the Modified color on the next draw. This is
// the synchronous flavour — it blocks until git answers — used at
// startup (nothing is interactive yet) and by tests. Interactive code
// paths use refreshGitStatusAsync so a slow `git status` on a huge or
// network-mounted repo can never stall typing.
func (a *App) refreshGitStatus() {
	a.applyGitStatus(collectGitStatus(a.rootDir, a.diffBase, a.openTabPaths(), a.tree == nil))
}

// refreshGitStatusAsync collects git status on a background goroutine
// and posts the result back to the main loop as a gitStatusEvent —
// the same goroutine → custom event pattern the auto-scroller and the
// finder indexer use, so no UI state is ever touched off-thread.
// Kicks while a collection is already in flight coalesce into a single
// follow-up run, so a burst of file operations costs at most two
// status calls rather than one each.
func (a *App) refreshGitStatusAsync() {
	if a.gitRefreshInFlight {
		a.gitRefreshQueued = true
		return
	}
	a.gitRefreshInFlight = true
	rootDir, base, paths, skipStatus := a.rootDir, a.diffBase, a.openTabPaths(), a.tree == nil
	scr := a.screen
	a.safeGo("git-status", func() {
		res := collectGitStatus(rootDir, base, paths, skipStatus)
		_ = scr.PostEvent(&gitStatusEvent{when: time.Now(), res: res})
	})
}

// handleGitStatusEvent applies a finished background collection and
// launches the follow-up run if more refresh requests arrived while
// this one was in flight.
func (a *App) handleGitStatusEvent(e *gitStatusEvent) {
	a.gitRefreshInFlight = false
	a.applyGitStatus(e.res)
	if a.gitRefreshQueued {
		a.gitRefreshQueued = false
		a.refreshGitStatusAsync()
	}
}

// openTabPaths returns the file paths of every open text tab — the set
// whose gutter markers a status collection should refresh. Image tabs
// and untitled buffers have no diff to compute.
func (a *App) openTabPaths() []string {
	paths := make([]string, 0, a.tabs.Len())
	for _, tab := range a.tabs.Tabs() {
		if tab == nil || tab.Path == "" || tab.IsImage() {
			continue
		}
		paths = append(paths, tab.Path)
	}
	return paths
}

// applyGitStatus stamps a collection result onto the UI: tree tint
// maps, branch, per-tab gutter markers, and the Git panel rows when the
// panel is up. Main-thread only — the async path hands results here
// via gitStatusEvent.
func (a *App) applyGitStatus(res gitStatusResult) {
	a.noteGitMissing(res.st)
	if a.tree != nil {
		a.gitSnap = res.st
		if !res.st.IsRepo {
			a.tree.DirtyFiles = nil
			a.tree.DirtyFolders = nil
			a.gitDirtyDirs = nil
			// No repo, no Git panel — fall back to the explorer rather
			// than strand the user on a view with nothing behind it.
			a.gitPanelActive = false
			a.gitPanelRows = nil
		} else {
			dirtyFiles := rebaseGitPaths(res.st.Files, a.tree.Root.Path)
			a.tree.DirtyFiles = dirtyFiles
			a.tree.DirtyFolders = dirtyFolderSet(dirtyFiles, a.tree.Root.Path)
			// Same rebase as the kinds, or the flags would miss the
			// tree-cased keys buildGitChangesRows looks up.
			a.gitDirtyDirs = rebaseByRoot(res.isDir, a.tree.Root.Path)
		}
	}
	// Tabs opened after the collection started aren't in the map — they
	// render without gutter marks until the queued follow-up collection
	// lands (the coalescer's gitRefreshQueued guarantees one; see
	// refreshGitStatusAsync and newTab). Tabs closed since are simply
	// skipped by the path lookup.
	for _, tab := range a.tabs.Tabs() {
		if tab == nil || tab.Path == "" || tab.IsImage() {
			continue
		}
		if lines, ok := res.tabLines[tab.Path]; ok {
			tab.GitLines = lines
		}
	}
	// Keep the Git panel live: whatever refreshed the status (the 10s
	// tick, a save, a file op) also refreshes the visible list.
	if a.gitPanelActive {
		a.rebuildGitChangesRows()
	}
}
