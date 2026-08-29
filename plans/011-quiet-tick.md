# Plan 011: Make the 10-second refresh tick quiesce when nothing changed

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/refresh.go internal/app/finder.go internal/app/gitstatus.go internal/app/gitchanges.go internal/filetree/filetree.go internal/finder/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (staleness regressions if a gate is too eager — the STOP
  conditions below exist for exactly that)
- **Depends on**: none (coordinates with plans 004 and 009 — see
  Maintenance notes)
- **Category**: perf
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Every 10 seconds, forever, whether or not anything changed, skiff: (a)
rebuilds the entire finder index (`git ls-files` fork, or a full
`filepath.Walk` in non-git projects) — and while that rebuild runs,
`Finder.Search` returns nil, so `Esc p` shows "Indexing…" for a slice of
every ten-second window; (b) forks one `git diff --unified=0` per open tab
on top of the status/branch reads — ten tabs is ~14 git processes per
tick; (c) re-filters (gitignore regexes per entry per ancestor level),
re-sorts (two `strings.ToLower` allocations per comparison), and
re-allocates the children of **every loaded directory on the event loop**
via `merge`; and (d) when the Git panel is open, `os.Stat`s every changed
path on the event loop. On the stated deployment target — big trees over
NFS/SSHFS from a laptop on cellular — this is a continuous background load
that never quiesces, and (c)/(d) are main-thread work that stutters the
UI. The prior fix round moved the *ReadDir* off-thread (see
`refreshTreeNow`'s own comment); this plan finishes the job: unchanged
scans become near-free, the finder only reindexes when names actually
changed, and the per-tab git forks collapse into one.

## Current state

Relevant files:

- `internal/app/refresh.go` — the tick: `refreshTreeNow` (188),
  `refreshTreeAsync` (211), `handleTreeScan` (263), `probeOpenTabs` (295).
- `internal/app/finder.go` — `invalidateFinder` (129-142).
- `internal/finder/finder.go` — `Search` returns nil while
  `StateIdle`/`StateBuilding` (lines 171-180); `Rebuild` at 94.
- `internal/filetree/filetree.go` — `merge` (469-525), `ApplyScan` (~803),
  `ScanEntry` (358), `DirScan` (376, carries `Ignore []byte`),
  `cacheIgnore`, `filterIgnored`.
- `internal/app/gitstatus.go` — `collectGitStatus` (60-71: one
  `loadGitLineChanges` fork per tab), `loadGitLineChanges` (205: one
  `git diff --unified=0 <base> -- <path>` each).
- `internal/app/gitchanges.go` — `buildGitChangesRows` (389-405:
  `os.Stat` per changed path, on the loop).

The tick (`internal/app/refresh.go:188-192`):

```go
func (a *App) refreshTreeNow() {
	a.refreshTreeAsync()
	a.refreshGitStatusAsync()
	a.invalidateFinder()
}
```

The unconditional finder rebuild (`internal/app/finder.go:129-142`):

```go
func (a *App) invalidateFinder() {
	if a.finder == nil {
		return
	}
	a.finder.Invalidate()
	scr := a.screen
	a.finder.Rebuild(func() {
		_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
}
```

`merge`'s unconditional work (`internal/filetree/filetree.go:469-479`,
head of the function):

```go
func (t *Tree) merge(n *Node, ds DirScan) {
	t.cacheIgnore(n.Path, ds.Ignore)
	entries := t.filterIgnored(n.Path, ds.Entries)

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
```

…followed by cap, map rebuild, node allocation, and per-child
`Real`/`Loop` recompute ("Recomputed every merge because an ancestor's own
link target can change under us").

Per-tab git forks (`internal/app/gitstatus.go:66-68`):

```go
	for _, path := range tabPaths {
		res.tabLines[path] = loadGitLineChanges(rootDir, base, path)
	}
```

The panel's stat loop (`internal/app/gitchanges.go:396-400`):

```go
		row := gitChangeRow{Rel: filepath.ToSlash(rel), Abs: abs, Kind: kind}
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			row.IsDir = true
			row.Rel += "/"
		}
```

Two documented invariants this plan MUST preserve (CLAUDE.md, quoted):

> "Identity-preserving tree refresh: `merge` walks the existing children,
> matches survivors by name, and keeps their `*Node` pointers (and their
> `Expanded` state)."

> "Refresh scans off-thread, merges on the main loop: … the node graph the
> renderer walks is only ever mutated there. `Tree.Refresh` stays
> synchronous for file operations … its `treeScanGen` bump retires any
> in-flight sweep."

Also load-bearing: tab reconciliation (`reconcileTab`) consumes
`probeOpenTabs` results (per-tab `os.Stat`), NOT the directory listings —
so a fast-path that skips `merge` work cannot affect external-edit
detection on open tabs. Verify this yourself before relying on it: read
`handleTreeScan` → `applyTabProbes`.

Conventions: doc comment per function; every behavioural change lands with
a test that fails first; benchmarks are ordinary `testing.B` in the same
`_test.go`; `make test` / `make lint` are the gates.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Tests | `make test` | exit 0 |
| Filetree only | `go test ./internal/filetree/ -run TestMerge -v` | PASS |
| Benchmark | `go test ./internal/filetree/ -bench BenchmarkMerge -benchmem -run NONE` | fast-path ≥10× fewer allocs on unchanged input |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:
- `internal/filetree/filetree.go` (merge fast-path, `ScanEntry` sort-key
  cache) + `filetree_test.go`
- `internal/app/refresh.go` (conditional finder invalidation, change
  detection from landed scans)
- `internal/app/finder.go` (no structural change expected; call-site
  discipline)
- `internal/app/gitstatus.go` (batched gutter diff; `IsDir` carried on
  the result) + tests
- `internal/app/gitchanges.go` (consume the carried `IsDir`) + tests

**Out of scope**:
- `treeRefreshInterval` (10s) stays 10s — the point is a cheap tick, not
  a rarer one.
- The session write riding on `refreshTreeAsync` — durability cadence is
  deliberate (its comment explains why); do not gate it.
- `probeOpenTabs` — per-tab stats feed conflict detection; do not gate.
- `internal/finder`'s index format or matcher.

## Git workflow

- Branch: `advisor/011-quiet-tick`
- One commit per step below; imperative subjects, no Claude trailers.

## Steps

### Step 1: `merge` fast-path for unchanged directories

In `internal/filetree/filetree.go`:

1. Add an unexported helper `scanUnchanged(n *Node, ds DirScan,
   cachedIgnore []byte) bool` that returns true only when ALL hold:
   - `n.Loaded && n.ReadErr == nil`
   - `ds.Err == nil`
   - `bytes.Equal(ds.Ignore, cachedIgnore)` (read how `cacheIgnore`
     stores bytes — compare against the cached raw bytes, not the
     compiled matcher)
   - the entry list matches the CURRENT children exactly: same length
     ignoring a trailing sentinel, and pairwise — but the children are
     post-filter/post-sort/post-cap while `ds.Entries` are raw. Cheapest
     correct comparison: remember the raw scan on the node. Add a field
     `lastScan []ScanEntry` (or a compact fingerprint — names+flags
     joined) set at the bottom of `merge`, and compare `ds.Entries`
     against it here. Choose the fingerprint representation and document
     it; byte-level equality of a `strings.Builder` join
     (`name\x00dirFlag\x00linkFlag\x00real\x1e...`) is fine and
     allocation-light.
   - **symlink safety rule**: return false whenever `n.IsLink`, `n.Real
     != n.Path`, or any entry has `IsLink` — `Real`/`Loop` are recomputed
     each merge precisely because an ancestor's link target can move, and
     the fast-path must not cache through that. Plain directories (the
     overwhelming majority) still win.
2. At the top of `merge`, if `scanUnchanged(...)` → return immediately
   (before `cacheIgnore`/`filterIgnored`/sort/alloc).
3. Cache the sort key: add `lowerName string` to `ScanEntry`, filled in
   `ScanDirs` (off-thread), and sort on it; fall back to computing it in
   `merge` when empty so hand-built test entries keep working.

Tests in `internal/filetree/filetree_test.go` (doc comment each, follow
the existing style):
- `TestMerge_FastPathPreservesNodes` — two identical scans; second one
  must keep the same `*Node` pointers AND skip reallocation (assert via
  the fingerprint being set + pointers equal; pointers-equal is the
  user-visible contract).
- `TestMerge_FastPathDisabledForSymlinks` — an entry with `IsLink` forces
  the full merge.
- `TestMerge_ChangeInIgnoreBytesForcesFullMerge`.
- `TestMerge_EntryAddedForcesFullMerge` (and removed).
- `BenchmarkMergeUnchanged` — 1000-entry dir, unchanged scan; record
  before/after `-benchmem` numbers in the commit message.

**Verify**: `go test ./internal/filetree/` → ok; benchmark shows the
fast-path doing ~zero allocs.

### Step 2: Finder reindexes only when names changed

1. Make `Tree.ApplyScan` (or `handleTreeScan`) report whether ANY
   directory took the full-merge path with an actual membership change.
   Cleanest seam: have `merge` return `bool` ("children changed":
   fast-path → false; full merge → compare fingerprint before/after) and
   bubble it up through `applyScanNode`/`ApplyScan` → `handleTreeScan`.
2. In `handleTreeScan`, call `a.invalidateFinder()` only when that bool
   is true. Remove `a.invalidateFinder()` from `refreshTreeNow`.
3. Audit every OTHER `invalidateFinder` call site
   (`grep -rn "invalidateFinder" internal/app/ | grep -v _test`) — file
   ops (create/rename/delete/paste/undo-delete) and custom-action success
   must KEEP their direct calls; list them in the commit message. The
   tick is the only caller that becomes conditional.

Tests: extend `internal/app/refresh_test.go` — a `treeScanEvent` whose
dirs match the current tree does NOT post a finder rebuild (assert via
the finder's state or a stubbed rebuild hook — read how
`finder_test.go` in `internal/app` observes rebuilds); a scan with a new
name does.

**Verify**: `go test ./internal/app/ -run 'TestHandleTreeScan|TestFinder' -v`
→ PASS.

### Step 3: Batch the per-tab gutter diffs into one git call

In `internal/app/gitstatus.go`:

1. Replace the per-path loop in `collectGitStatus` with one invocation:
   `git diff --unified=0 <base> -- p1 p2 … pN` (paths after `--`, matching
   the repo's argv discipline — see the existing call in
   `loadGitLineChanges`: `git.Output(rootDir, "diff", "--unified=0",
   base, "--", path)`).
2. Split the combined output on `diff --git ` file headers, attribute
   each section to its path (parse the `+++ b/<rel>` line; paths with
   spaces/renames — read `parseGitDiffLines` first to see what it already
   handles and keep parity), and run the existing `parseGitDiffLines` per
   section.
3. Keep `loadGitLineChanges` for single-path callers; the batcher can
   call the same parser.
4. Empty tab list → skip the fork entirely.

Tests: table test with a captured two-file `--unified=0` diff asserting
the split assigns hunks to the right paths; empty-list short-circuit.
Fixture pattern: `internal/app/gitstatus_test.go` builds real repos in
`t.TempDir()` — add one integration case with two modified tabs open.

**Verify**: `go test ./internal/app/ -run TestCollectGitStatus -v` → PASS;
`grep -c "loadGitLineChanges" internal/app/gitstatus.go` shows the loop
gone.

### Step 4: Carry `IsDir` instead of stat'ing on the loop

`buildGitChangesRows` stats every dirty path only to add a trailing `/`
for untracked directories. Move that answer off-thread: in
`collectGitStatus` (already a goroutine), after `loadGitStatus`, stat the
dirty paths there and carry `isDir map[string]bool` on `gitStatusResult`;
`applyGitStatus` hands it to `rebuildGitChangesRows`. `buildGitChangesRows`
takes the map as a parameter and does zero filesystem work — its doc
comment currently says "Pure so tests can drive it without a repo; the
only filesystem touch is a stat" — make it actually pure and update the
comment. Preserve today's semantics: paths that fail to stat (deleted)
get `IsDir == false`.

**Verify**: `go test ./internal/app/ -run TestBuildGitChanges -v` → PASS;
`grep -n "os.Stat" internal/app/gitchanges.go` → no output.

### Step 5: Full-suite + convergence check

Run everything; then re-read the three tick stages and confirm in the
commit message: the tick still (a) applies changed scans, (b) reconciles
tabs via probes, (c) refreshes git status — nothing became conditional
except finder invalidation and merge's alloc work.

**Verify**: `make test` → exit 0; `make lint` → exit 0.

## Test plan

Enumerated per step above. Every new gate has both directions tested:
unchanged input takes the cheap path, changed input takes the full path.
The identity-preservation contract
(`TestRefresh_PreservesExpandedState` in `filetree_test.go`) must stay
green untouched.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `BenchmarkMergeUnchanged` allocs/op ≈ 0 (record numbers)
- [ ] `refreshTreeNow` no longer calls `invalidateFinder` unconditionally
- [ ] One `git diff` per status collection regardless of tab count (test proves it)
- [ ] `grep -n "os.Stat" internal/app/gitchanges.go` → no output
- [ ] No files outside scope modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

- Any excerpt above doesn't match the live code.
- You cannot make the fingerprint comparison sound for the sentinel/cap
  case (a dir at exactly `MaxDirChildren` boundary) — report rather than
  special-case blindly.
- `reconcileTab` turns out to consume anything from `DirScan`s (it must
  only consume `tabProbe`s) — the finder gate is then unsafe as designed;
  report.
- The batched diff's per-file attribution cannot round-trip a rename or a
  path with spaces that the single-file version handled — report with the
  failing fixture.
- Any existing filetree identity test fails.

## Maintenance notes

- Plan 004 wraps this file's goroutines in `safeGo`; land order is
  irrelevant, but whichever is second keeps both shapes.
- Plan 009 makes `collectGitStatus` the only gutter-line producer — after
  both land, Step 3's batcher is the single git-diff path for gutters.
- The `lastScan` fingerprint on `Node` is per-directory memory
  (~bytes×entries); if profiling ever shows it matters on 50k-file trees,
  hash it instead of storing the join.
- Future "watchman/fsnotify instead of polling" work supersedes parts of
  this plan; the merge fast-path stays valuable regardless (file ops call
  `Tree.Refresh` synchronously through the same merge).
