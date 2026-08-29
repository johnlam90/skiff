# Plan 005: Stop format-on-save and the silent external reload from wiping undo history

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/editor/tab.go internal/editor/undo.go internal/app/format.go internal/app/refresh.go internal/app/conflict.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none (plan 006 touches `Tab.Reload` too — execute 005 BEFORE 006)
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`Tab.Reload` ends by clearing the entire undo/redo history. That is correct
for its original caller — the disk-conflict prompt's explicit "Reload" button,
where the user chose to take the disk version — but two other callers reuse it
where the justification is false: `handleFormatDone` reloads after every
successful format-on-save (so **every save with a formatter configured
destroys the user's undo stack**), and the 10-second reconcile tick silently
reloads a clean buffer whose file changed on disk (so a background
`git checkout` or `make` erases the history of a file the user never touched).
After this plan, those two paths keep history and the reload itself becomes an
undoable step.

## Current state

- `internal/editor/tab.go:422-464` — `Reload()`. The text branch reads the
  file, replaces `t.Buffer`, clamps cursor, clears `Dirty`/`DiskGone`,
  refreshes `Mtime`, sets `StyleStale`/`cursorMoved`, and ends:

  ```go
  // tab.go:458-463
  // Reload re-establishes "what's on disk" as the new baseline. Any
  // prior undo history is meaningless now (the line indices may have
  // shifted, and the user explicitly asked to take the disk version),
  // so reset both stacks and the revert anchor.
  t.initUndo()
  return nil
  ```

  There is also an image-tab branch at :426-440 that re-decodes and returns
  early (no history involved).

- The three call sites:
  - `internal/app/conflict.go:149` — `reloadConflictedTab`, the explicit
    "Reload" answer. KEEPS current behavior.
  - `internal/app/format.go:579` — `handleFormatDone`, after a formatter
    rewrote the file on disk. SWITCHES to history-preserving.
  - `internal/app/refresh.go:393` — `reconcileTab`'s clean-buffer silent
    reload. SWITCHES to history-preserving.

- `internal/editor/undo.go` — the machinery you will reuse:
  - `captureSnapshot()` (:99-106) — deep copy of lines + cursor + anchor.
  - `applySnapshot(s)` (:111-122) — restores one; re-copies lines.
  - `initUndo()` (:128-135) — seeds `undoOriginal`, `savedBaseline`, clears
    both stacks, zeroes `undoBytes`, resets `lastUndoGroup`.
  - `pushUndoSnapshot(s)` (:157+) — the ONLY sanctioned append to
    `undoStack` (keeps `undoBytes` honest — its doc comment says so).
  - `undoGroupStructural` (:72) — the "never coalesce" group tag.
  - `markSaved()` (~:246) — re-points the dirty baseline at the current
    buffer. Read its body before using it in Step 2.

- Conventions: doc comment on every function; tests in the same package;
  TDD (failing test first); the buffer's own undo tests live in
  `internal/editor/undo_test.go` — model new ones on them; app-level
  behavior tests live in `internal/app/format_test.go` / `refresh_test.go`
  (use `SKIFF_TRUST_FILE`/`SKIFF_DEFAULTS_FILE` + `t.TempDir()` — see
  `internal/app/format_test.go` for the pattern).

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite | `make test` | exit 0 |
| Lint gates | `make lint` | exit 0 |
| Targeted | `go test ./internal/editor/ -run Reload` | all pass |
| Targeted | `go test ./internal/app/ -run 'Format\|Reconcile'` | all pass |

## Scope

**In scope**:
- `internal/editor/tab.go` (Reload refactor + new method)
- `internal/editor/tab_test.go`
- `internal/app/format.go:579` (one call-site change)
- `internal/app/refresh.go:393` (one call-site change)
- `internal/app/format_test.go`, `internal/app/refresh_test.go` (new tests)

**Out of scope**:
- `internal/app/conflict.go` — keeps calling plain `Reload()`.
- Any change to `Reload`'s disk-reading logic (stat order, LineEnding
  detection, image branch) — that's plan 006's territory. Only the history
  handling moves here.
- `internal/editor/undo.go` internals (caps, coalescing).

## Git workflow

- Branch: `advisor/005-undo-preserving-reload`
- Imperative commit subject + explanatory body; NO Claude trailers
  (CLAUDE.md rule).
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Extract the shared reload body

In `tab.go`, factor the text-branch disk work of `Reload` (everything from
the `os.ReadFile` through `t.cursorMoved = true`, i.e. all of it EXCEPT
`t.initUndo()`) into an unexported `reloadFromDisk() error` with a doc
comment. `Reload()` becomes: image branch unchanged, then
`if err := t.reloadFromDisk(); err != nil { return err }; t.initUndo();
return nil`. Pure refactor — no behavior change.

**Verify**: `go test ./internal/editor/` → all pass (existing Reload tests
still green).

### Step 2: Add `ReloadKeepHistory` (test first)

Write `TestReloadKeepHistory_UndoRestoresPreReloadContent` in `tab_test.go`
first: create a file, open a Tab, type an edit + save (so history exists),
rewrite the file externally, call `ReloadKeepHistory()`, assert buffer holds
the disk content and `CanUndo()` is true; `Undo()` restores the pre-reload
content; a subsequent `Redo()` returns to the disk content without panic.
Watch it fail (method missing), then implement:

```go
// ReloadKeepHistory re-reads the file from disk like Reload but keeps the
// tab's undo history, pushing the pre-reload buffer as its own undoable
// step. This is the right shape for reloads the user did NOT explicitly
// ask for — format-on-save and the background external-change reconcile —
// where wiping history would destroy work the user never chose to give up.
func (t *Tab) ReloadKeepHistory() error {
    if t.IsImage() {
        return t.Reload() // image tabs have no text history to keep.
    }
    pre := t.captureSnapshot()
    if err := t.reloadFromDisk(); err != nil {
        return err
    }
    t.pushUndoSnapshot(pre)
    t.redoStack = nil
    t.lastUndoGroup = undoGroupNone // the reload never coalesces with typing
    t.markSaved() // disk == buffer now; dirty is measured against this state
    return nil
}
```

Before using `markSaved()`, read its body (undo.go ~:246): it must
re-baseline the dirty comparison against the *current* buffer. If it does
something narrower, assign the baseline the way `initUndo` does
(`t.savedBaseline = t.captureSnapshot()`) instead — and say which you did in
the commit body.

Also add `TestReloadKeepHistory_ShorterFileClampsOnUndo`: reload to a much
shorter file, `Undo()`, `Redo()` — no panic, cursor in range (undo.go's
`applySnapshot` + `Buffer.Clamp` handle this; the test pins it).

And `TestReload_StillClearsHistory`: plain `Reload()` after edits leaves
`CanUndo()` false — the conflict-prompt contract is unchanged.

**Verify**: `go test ./internal/editor/ -run Reload` → all pass, including
the three new tests.

### Step 3: Switch the two call sites (tests first)

In `internal/app/format_test.go`, add
`TestFormatOnSave_PreservesUndoHistory`: configure a trusted formatter the
way the existing format tests do, save a tab with edits, deliver the
`formatDoneEvent`, then assert the tab's `CanUndo()` is still true and one
`Undo()` restores the pre-format text. In `refresh_test.go`, add the mirror
`TestReconcileTab_SilentReloadKeepsUndo` using the existing reconcile test
scaffolding (external write + tick). Watch both fail, then change:

- `internal/app/format.go:579` → `tab.ReloadKeepHistory()`
- `internal/app/refresh.go:393` → `tab.ReloadKeepHistory()`

Leave the surrounding flash messages exactly as they are.

**Verify**: `go test ./internal/app/ -run 'Format\|Reconcile'` → all pass.

### Step 4: Full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

New tests (all named above): three in `internal/editor/tab_test.go`, one in
`internal/app/format_test.go`, one in `internal/app/refresh_test.go`. Model
app-level setup on the existing tests in those files (they already build
apps with SimulationScreen and temp roots).

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -n "tab.Reload()" internal/app/format.go internal/app/refresh.go`
      returns no matches (both now `ReloadKeepHistory`)
- [ ] `grep -n "tab.Reload()" internal/app/conflict.go` still returns
      exactly one match (line ~149)
- [ ] The five new tests exist and pass
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back if:

- `Reload` no longer matches the excerpt (plan 006 may have landed first —
  it was ordered after this plan precisely to avoid that; if it did land,
  re-read `Reload`/`reloadFromDisk` and reconcile before editing).
- `markSaved()` turns out to have side effects beyond re-baselining the
  dirty comparison (e.g. touching mtime or disk) — report what you found.
- The format-test scaffolding cannot deliver a `formatDoneEvent`
  synchronously — do not invent sleeps; report.

## Maintenance notes

- Plan 006 (open/reload guards) moves `reloadFromDisk`'s raw `os.ReadFile`
  behind a size/binary gate; it builds on this refactor.
- The pre-reload snapshot can be large (whole buffer); the undo caps in
  `undo.go` (`maxUndoBytes`) already bound total memory — a reviewer should
  confirm `pushUndoSnapshot`'s trimming applies (it does: every append goes
  through it).
- Future: if a "reload" toast ever gains an Undo button, `ReloadKeepHistory`
  is the primitive it calls.
