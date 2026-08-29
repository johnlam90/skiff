# Plan 007: Stop the false disk-conflict prompt when a clean file is deleted and recreated

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/refresh.go internal/app/tabops.go internal/app/actions.go internal/app/draw.go internal/editor/tab.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

When a file open in a **clean** tab is deleted on disk, the reconcile tick
sets `tab.Dirty = true` synthetically — the buffer has no user edits, but
`Dirty` is the only flag the tab-strip dot, status bar, and close guard
consult, so the overload was the cheap way to make "deleted on disk" feel
urgent. The bug: when the file **reappears** (a `git checkout`, `git
stash pop`, `make`, or any unlink-then-recreate tool spanning two ticks),
the reconcile flow still sees `Dirty == true` and opens the "Keep mine /
Reload / Diff" disk-conflict prompt — a modal claiming the user has
unsaved edits they don't have, offering to preserve nothing. The right
outcome for a clean buffer is the silent reload the code already does for
plain external edits. The fix is to stop overloading `Dirty`: the delete
branch sets only `DiskGone`, and every consumer that meant "needs
attention" (not "has unsaved edits") gates on `Dirty || DiskGone`.

## Current state

Relevant files:

- `internal/app/refresh.go` — `reconcileTab` (lines 344-394), the
  three-way external-change reconciliation. Contains the bug.
- `internal/app/tabops.go` — `saveAllDirty` (89), `dirtyTabCount` (104),
  `requestCloseTab` (121) — `Dirty` consumers.
- `internal/app/actions.go` — `menuQuit` (538) — quit-time dirty check.
- `internal/app/draw.go` — tab-strip dirty dot (281), status-bar `· ●`
  (648).
- `internal/editor/tab.go` — `DiskGone` field (139), cleared by
  `Save` (401) and `Reload` (454).
- `internal/app/conflict.go` — `openDiskConflict` (91), the prompt this
  plan stops firing falsely; `diskConflicts` map bookkeeping (55-64).

The buggy flow (`internal/app/refresh.go:345-394`, abridged to the
load-bearing lines — read the whole function before editing):

```go
func (a *App) reconcileTab(tab *editor.Tab, p tabProbe) {
	if os.IsNotExist(p.err) {
		if !tab.DiskGone {
			tab.DiskGone = true
			tab.Dirty = true                              // ← the overload
			a.flash(fmt.Sprintf("%s deleted on disk", filepath.Base(tab.Path)))
		}
		return
	}
	...
	if tab.DiskGone {
		// File reappeared. Force the mtime check below to fire so we
		// either reload or warn about a dirty conflict.
		tab.DiskGone = false
		tab.Mtime = time.Time{}
	}
	// A tab that stopped being dirty resolved its conflict by
	// being saved; drop the marker so the status bar stops warning.
	if !tab.Dirty {
		a.clearDiskConflict(tab.Path)
	}
	if !p.mtime.After(tab.Mtime) {
		return // unchanged on disk.
	}
	if tab.Dirty {                                        // ← still the synthetic true
		...
		a.openDiskConflict(tab, p.mtime)                  // ← false prompt
		tab.Mtime = p.mtime
		return
	}
	if err := tab.Reload(); err != nil {                  // ← where a clean tab should land
```

The `Dirty` consumers that today rely on the synthetic write (verified by
grep; enumerate again before editing):

| Site | Meaning it wants |
|---|---|
| `tabops.go:91` `saveAllDirty` skip | true dirtiness (don't save clean buffers) |
| `tabops.go:106` `dirtyTabCount` | quit modal trigger — needs attention |
| `tabops.go:125` `requestCloseTab` guard | close warning — needs attention |
| `actions.go:549` `menuQuit` loop | same as dirtyTabCount |
| `draw.go:281` tab-strip `●` | needs attention |
| `draw.go:648` status `· ●` | needs attention |

`DiskGone`'s other writers (`fileops.go:261`, `fileclip.go:231`,
`editor/tab.go:401,438,454`) clear it on save/restore/reload — they are
correct as-is.

Conventions: doc comment on every function; bug fixes add a test that
fails before and passes after (CLAUDE.md); `internal/app` tests build
apps with `newTestApp` (`app_test.go`) and drive reconcile via
`reconcileTab`/`handleTreeScan` — `internal/app/refresh_test.go` is the
exemplar for probe-shaped tests (`tabProbe` values are constructed
directly).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Tests | `make test` | exit 0 |
| Focused | `go test ./internal/app/ -run TestReconcile -v` | PASS |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:
- `internal/app/refresh.go` (the delete branch)
- `internal/app/tabops.go`, `internal/app/actions.go`,
  `internal/app/draw.go` (gate the "needs attention" consumers)
- `internal/app/refresh_test.go`, plus touched files' tests

**Out of scope**:
- `internal/editor/tab.go` — `Dirty`'s meaning inside the editor package
  stays "buffer differs from disk baseline"; do not add editor-side flags.
- `internal/app/conflict.go` — the prompt itself is correct; only its
  false trigger changes.
- The dirty-close modal wording (`requestCloseTab`) — a DiskGone-only tab
  may show the same modal; copy changes are deferred.

## Git workflow

- Branch: `advisor/007-false-conflict-delete-recreate`
- Imperative commits, no Claude trailers. TDD: commit the failing test
  first only if the repo's history shows that pattern; otherwise one
  commit with test + fix is fine.

## Steps

### Step 1: Pin the bug with a failing test

In `internal/app/refresh_test.go` add
`TestReconcileTab_DeleteThenRecreateCleanTabReloadsSilently` (doc comment
explaining the git-checkout shape): open a clean tab via `newTestApp` +
a real file in `t.TempDir()`; feed `reconcileTab` a probe with
`os.ErrNotExist`; assert `DiskGone` true; recreate the file with new
content and a later mtime; feed a second probe with that mtime; assert:
no overlay is open (`a.overlays.Top() == nil` or the package's
`anyModalOpen()` accessor — read how existing tests assert it), buffer
content equals the new disk content, `tab.Dirty` is false.

**Verify**: `go test ./internal/app/ -run TestReconcileTab_DeleteThenRecreate -v`
→ FAIL (a conflict modal opened / Dirty stayed true).

### Step 2: Remove the overload, gate the consumers

- In `reconcileTab`'s missing-file branch: delete `tab.Dirty = true`; keep
  `DiskGone` and the flash. Update the branch comment to say why Dirty is
  NOT set (the buffer has no user edits; DiskGone alone carries the
  state).
- Gate the "needs attention" consumers on `Dirty || DiskGone`:
  - `dirtyTabCount` (`tabops.go:104`) — count `tab.Dirty || tab.DiskGone`;
    rename NOT required (callers read "tabs needing attention"), but
    update its doc comment.
  - `requestCloseTab` (`tabops.go:125`) — `if !tab.Dirty && !tab.DiskGone`.
  - `menuQuit`'s loop (`actions.go:549`) — same gate.
  - `draw.go:281` and `draw.go:648` — show the dot for
    `tab.Dirty || tab.DiskGone`.
- Leave `saveAllDirty` on plain `Dirty` — saving a clean DiskGone buffer
  IS desirable on quit-with-save (it recreates the file), so change its
  skip to `!tab.Dirty && !tab.DiskGone` **only if** `Tab.Save` succeeds on
  a missing file — read `editor/tab.go` `Save` (it writes with
  `os.WriteFile`, which creates; it does succeed). So: gate it too, and
  note in the comment that saving a DiskGone tab resurrects the file.

**Verify**: Step 1's test now PASSES.

### Step 3: Protect the behaviours that must survive

Confirm existing tests still pass, and add two more:

- `TestReconcileTab_DeleteWhileDirtyStillConflicts` — genuinely dirty tab
  (edit the buffer first), delete + recreate probes → the conflict prompt
  DOES open.
- `TestRequestCloseTab_DiskGoneOnlyWarns` — clean tab with
  `DiskGone = true` → `requestCloseTab` opens the unsaved-changes modal
  rather than closing silently (the file's content exists only in the
  buffer now; closing without asking would lose it).

**Verify**: `make test` → exit 0; `make lint` → exit 0.

## Test plan

Covered in steps: one regression test (fails before, passes after), one
preserved-behaviour test for the true-conflict path, one for the close
guard. Model all three on the existing probe-driven tests in
`internal/app/refresh_test.go`.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -n "Dirty = true" internal/app/refresh.go` → no output
- [ ] The three new tests exist and pass
- [ ] Delete-while-dirty still prompts (test proves it)
- [ ] No files outside scope modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

- The `reconcileTab` excerpt doesn't match the live code.
- Removing the synthetic `Dirty` breaks a test that asserts the *old*
  behaviour for a reason you can't determine from its doc comment —
  report the test name and its stated intent instead of changing it.
- You find another consumer of the synthetic `Dirty` beyond the table
  above whose correct gate is ambiguous (report it).

## Maintenance notes

- `Dirty` now means exactly "buffer differs from its saved baseline" and
  `DiskGone` "no file behind this tab"; future status-bar work should
  render distinct markers (e.g. `· ●` vs `· deleted on disk`) — deferred
  here to keep the fix minimal.
- Plan 005 (undo-preserving reload) touches the same `reconcileTab` tail;
  whichever lands second must re-run this plan's three tests.
