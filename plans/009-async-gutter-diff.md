# Plan 009: Take the inline `git diff` off the tab-open and tab-switch path

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/preview.go internal/app/gitstatus.go internal/app/session_restore.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M (mostly verification; the mechanism already exists)
- **Risk**: LOW
- **Depends on**: none (coordinates with plan 011 — see Maintenance notes)
- **Category**: perf
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Every tab open AND every switch to an already-open tab runs
`loadGitLineChanges` — one `git diff --unified=0` subprocess — inline on
the event loop. `internal/git`'s read timeout is ten seconds, so on a
slow, huge, or network-mounted repo a plain click on a tab can freeze the
whole UI for up to that long, with no feedback. Session restore calls
`newTab` in a loop, so a 15-tab session serialises 15 subprocess waits
before the first paint. The repo already fixed this exact failure once:
`requestDiff` (`internal/app/diffview.go`) was built because "running the
diff inline meant one click on a gutter marker could freeze the editor for
that long" — but the far more frequent tab-open path never got the
treatment. Better still, the background machinery already computes and
merges per-tab gutter lines: `collectGitStatus` fetches them off-thread
and `applyGitStatus` merges them by path. The fix is to stop loading
inline and let that existing, coalesced pipeline fill the gutter.

## Current state

Relevant files:

- `internal/app/preview.go` — the shared open path; the two inline loads.
- `internal/app/gitstatus.go` — `loadGitLineChanges` (205),
  `collectGitStatus` (60), `refreshGitStatusAsync` (427),
  `handleGitStatusEvent` (444), `applyGitStatus` tab merge (491-498).
- `internal/app/session_restore.go` — restore loop calling `newTab`
  (line 87).

The two inline calls (`internal/app/preview.go`):

```go
// preview.go:69 — switching to an already-open tab:
		a.tabs.Activate(t)
		a.ensureActiveTabVisible()
		t.GitLines = loadGitLineChanges(a.rootDir, a.diffBase, t.Path)
		return
```

```go
// preview.go:92-101 — newTab, the ONE construction path:
func (a *App) newTab(path string) (*editor.Tab, error) {
	t, err := editor.NewTab(path)
	if err != nil {
		return nil, err
	}
	t.Wrap = a.wrapOn
	t.GitLines = loadGitLineChanges(a.rootDir, a.diffBase, path)
	return t, nil
}
```

The blocking callee (`internal/app/gitstatus.go:205-217`): builds
`git.Output(rootDir, "diff", "--unified=0", base, "--", path)` — a real
subprocess with the package's 10s read deadline.

The already-existing async pipeline:

```go
// gitstatus.go:427-439 — coalesced: at most one in flight, one queued.
func (a *App) refreshGitStatusAsync() {
	if a.gitRefreshInFlight {
		a.gitRefreshQueued = true
		return
	}
	a.gitRefreshInFlight = true
	rootDir, base, paths, skipStatus := a.rootDir, a.diffBase, a.openTabPaths(), a.tree == nil
	scr := a.screen
	go func() {
		res := collectGitStatus(rootDir, base, paths, skipStatus)
		_ = scr.PostEvent(&gitStatusEvent{when: time.Now(), res: res})
	}()
}
```

```go
// gitstatus.go:60-71 — collectGitStatus already loads gutter lines for
// every open tab, off-thread:
	for _, path := range tabPaths {
		res.tabLines[path] = loadGitLineChanges(rootDir, base, path)
	}
```

```go
// gitstatus.go:488-498 — applyGitStatus merges by path on the loop:
	// Tabs opened after the collection started aren't in the map and
	// keep the gutter lines they loaded on open; tabs closed since are
	// simply skipped by the path lookup.
	for _, tab := range a.tabs.Tabs() {
		...
		if lines, ok := res.tabLines[tab.Path]; ok {
			tab.GitLines = lines
		}
	}
```

Conventions: background work posts custom tcell events and never mutates
UI state off the loop (this plan only ever calls the existing async
entry point from the loop, so it inherits compliance); doc comment per
function; bug/perf fixes come with tests; `internal/app` git tests build
real repos in `t.TempDir()` — see `internal/app/gitstatus_test.go` for
the fixture pattern and for how `gitStatusEvent`s are synthesized and
applied in tests.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Tests | `make test` | exit 0 |
| Focused | `go test ./internal/app/ -run 'TestOpenFile|TestGitStatus|TestNewTab' -v` | PASS |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:
- `internal/app/preview.go` — remove both inline loads; trigger the async
  refresh.
- `internal/app/gitstatus.go` — only the stale comment at 488-490.
- `internal/app/session_restore.go` — one refresh after the restore loop
  (if not already triggered by the caller; verify first).
- Tests beside each.

**Out of scope**:
- `collectGitStatus`'s one-fork-per-tab shape — plan 011 batches it.
- `requestDiff`/diffview — already async.
- Any change to `loadGitLineChanges` parsing.

## Git workflow

- Branch: `advisor/009-async-gutter-diff`
- Imperative commits, no Claude trailers.

## Steps

### Step 1: Pin current behaviour, then remove the inline load from `newTab`

Write `TestNewTab_DoesNotBlockOnGit` in `internal/app/preview_test.go`
(create the file if it doesn't exist — check for an existing test home for
preview.go first; the repo convention is one `_test.go` per source file):
in a real git repo fixture with a modified tracked file (copy the fixture
setup from `internal/app/gitstatus_test.go`), call `a.newTab(path)` and
assert `t.GitLines == nil` — the gutter is filled later by the event, not
inline. This fails before the change (GitLines non-nil) and passes after.

Then delete the `t.GitLines = loadGitLineChanges(...)` line from `newTab`
and update its doc comment: gutter lines arrive via the git-status
pipeline; a fresh tab renders without gutter marks for one round trip.

**Verify**: the new test passes; `go test ./internal/app/` → ok.

### Step 2: Same for the switch-to-open-tab path

Remove `t.GitLines = loadGitLineChanges(...)` at `preview.go:69` and
replace with `a.refreshGitStatusAsync()` (the coalescing entry — cheap to
call; if a refresh is in flight it just queues one). Also call
`a.refreshGitStatusAsync()` at the end of the new-tab branch of the open
path (after `finishOpen`), so a newly opened tab's gutter arrives without
waiting for the 10s tick.

Update the stale comment in `applyGitStatus` (gitstatus.go:488-490): tabs
opened after the collection started are no longer "keeping the lines they
loaded on open" — they render without gutter marks until the queued
follow-up collection lands (the coalescer's `gitRefreshQueued` guarantees
one).

**Verify**: `go test ./internal/app/ -run TestGitStatus -v` → PASS.

### Step 3: Session restore

Read `restoreSession` (`internal/app/session_restore.go`, loop at :87) and
its caller. If no `refreshGitStatusAsync()` already runs after restore
(grep the call sites), add exactly one after the loop. The N inline forks
during restore disappear with Step 1; this step ensures the gutters still
converge shortly after startup.

**Verify**: `grep -n "loadGitLineChanges" internal/app/*.go | grep -v _test | grep -v gitstatus.go`
→ no output (the only remaining callers live in gitstatus.go's collector).

### Step 4: End-to-end convergence test

`TestOpenFile_GutterArrivesViaStatusEvent`: git fixture with a modified
file; `a.openFileMode(path, ...)` (read the real entry-point name in
preview.go); assert `GitLines == nil`; then synthesize the event the
pipeline would post — build `gitStatusResult` with
`tabLines[path] = <expected map>` and run it through
`a.handleGitStatusEvent(&gitStatusEvent{res: ...})` (match how
`gitstatus_test.go` constructs these); assert the tab's `GitLines` now
carries the expected change map and `gitRefreshInFlight` bookkeeping is
consistent.

**Verify**: `make test` → exit 0; `make lint` → exit 0.

## Test plan

- `TestNewTab_DoesNotBlockOnGit` (step 1) — regression pin.
- `TestOpenFile_GutterArrivesViaStatusEvent` (step 4) — convergence.
- Existing coalescing tests (`handleGitStatusEvent` requeue) must stay
  green — they already cover the in-flight/queued logic this plan leans
  on.
- Pattern exemplar: `internal/app/gitstatus_test.go`.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -rn "loadGitLineChanges" internal/app/ --include='*.go' | grep -v _test` shows call sites only inside `gitstatus.go`
- [ ] New tests exist and pass
- [ ] The `applyGitStatus` comment no longer claims tabs load lines on open
- [ ] No files outside scope modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

- The excerpts don't match the live code (drift — plan 011 may have
  landed first and reshaped `collectGitStatus`; if so, re-read and adapt
  only if the merge-by-path contract in `applyGitStatus` is intact,
  otherwise report).
- A test depends on gutter lines being present synchronously right after
  open (grep `GitLines` in `_test.go` first) and its intent is unclear —
  report rather than rewrite it.
- Calling `refreshGitStatusAsync` from the switch path causes visible
  status-bar churn in the simulation tests (it also reloads branch state;
  if that breaks an assertion, report — the alternative design is a
  narrow per-path request modelled on `requestDiff`, but do not build it
  without confirmation).

## Maintenance notes

- With this plan, `collectGitStatus` is the single place gutter lines are
  produced — plan 011's batching of the per-tab forks into one
  `git diff --unified=0 <base> -- p1 p2 …` call becomes strictly easier;
  land whichever first, the other adapts.
- A tab now has a gutter-less window after open (one status round trip).
  If that reads as a regression to users, the follow-up is a subtle
  "loading" treatment in the scrollbar change-map — deferred; do not
  build it here.
