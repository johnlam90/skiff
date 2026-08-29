# Plan 004: Restore the terminal on every exit path — signals, goroutine panics, and the trash

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/app.go internal/app/fileops.go internal/app/refresh.go internal/app/gitstatus.go internal/app/gitops.go internal/app/gitworktree.go internal/app/projfind.go internal/app/projreplace.go internal/app/diffview.go internal/app/format.go internal/app/fileclip.go internal/app/actions.go internal/app/mouse.go internal/finder/finder.go main.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Skiff's habitat is SSH-into-tmux on a remote box. Today the terminal is
restored (cooked mode, mouse reporting off, alt screen dropped) only by
`defer a.Close()` in `main` — which runs solely when `Run()` returns
normally. There is **no `signal.Notify` and no `recover()` anywhere in the
repo**, so three common events wreck the user's terminal and lose state:
`tmux kill-pane` / `pkill skiff` (SIGTERM/SIGHUP kill the process with raw
mode and `?1000h`/`?1002h` mouse tracking still on), a panic in any of the
14 background goroutines (which kills the process without unwinding main's
defers), and both of those also skip `emptyTrash()`, orphaning hidden
`.skiff-trash-*` files inside the user's project that the sidebar
deliberately hides but `git status` lists forever. A panic also scrolls its
trace into a wrecked screen, so there is nothing to paste into a bug
report. After this plan: signals quit cleanly through the normal path,
every goroutine panic restores the screen and writes a crash log the user
can report, and the trash is emptied on every close.

## Current state

Relevant files:

- `internal/app/app.go` — `Close()` (line 654) and `Run()` (line 665), the
  event loop and teardown.
- `internal/app/fileops.go` — `moveToTrash` (line 122), `emptyTrash`
  (line 189).
- `main.go` — constructs the app and runs it; `defer a.Close()` is the only
  teardown today.
- `internal/session/session.go` — `stateDir()` (line 86) is the XDG-state
  path helper pattern to mirror for the crash-log location.
- `internal/version/version.go` — `Version` constant for the crash log
  header.

`Close` and the tail of `Run` today (`internal/app/app.go:648-704`):

```go
// Close releases the terminal back to the user. Always call this before
// exit. Screen.Fini drops mouse reporting on its way out — its finalize
// path calls enableMouse(0), which emits
// `\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l` — so whichever mode
// syncMouseMode last put us in, the terminal is left with no tracking
// at all. Nothing here needs to unwind the mode by hand.
func (a *App) Close() {
	a.saveSession()
	a.stopTreeRefresh()
	a.stopAutoScroll()
	if a.screen != nil {
		a.screen.Fini()
	}
}
```

```go
	// (end of Run, app.go:700-704)
	// The undo window for deletes is the session: discard the trash on
	// the way out so removed work doesn't pile up invisibly on disk.
	a.emptyTrash()
	return nil
}
```

The quit flow (`internal/app/actions.go:538-542`): `menuQuit` sets
`a.quit = true` when no tab is dirty; the loop in `Run` checks `for
!a.quit`. There is no quit *event* type — quit is a plain bool consulted
after each drained batch.

The trash fallback that can orphan files in the project
(`internal/app/fileops.go:135-142`):

```go
	stored := filepath.Join(filepath.Dir(path),
		fmt.Sprintf("%s%d-%s", filetree.TrashPrefix, len(a.trashed), base))
	if err := os.Rename(path, stored); err != nil {
		return err
	}
```

The 14 goroutine launch sites (verified by
`grep -rn "go func()" internal/ --include="*.go" | grep -v _test`):

| File:line | What it does |
|---|---|
| `internal/app/refresh.go:151` | tree-refresh scan sweep |
| `internal/app/refresh.go:240` | (second refresh path) |
| `internal/app/gitstatus.go:435` | git status collection |
| `internal/app/gitops.go:58` | git write op runner |
| `internal/app/gitops.go:361` | branch list fetch |
| `internal/app/gitworktree.go:117` | worktree list fetch |
| `internal/app/projfind.go:147` | project search |
| `internal/app/projreplace.go:242` | project replace |
| `internal/app/diffview.go:193` | diff load |
| `internal/app/format.go:509` | formatter exec |
| `internal/app/fileclip.go:126` | file copy/paste worker |
| `internal/app/actions.go:257` | custom action runner |
| `internal/app/mouse.go:555` | drag auto-scroll ticker |
| `internal/finder/finder.go:94` | finder index rebuild |

All of them already follow the project rule "background work posts custom
tcell events, never mutates UI state" (e.g. `gitstatus.go:437`
`scr.PostEvent(&gitStatusEvent{...})`). The finder one lives in a package
that must not import `internal/app`, so the helper must be injectable
there (see Step 3).

The XDG state-dir pattern to mirror (`internal/session/session.go:86-97`):

```go
func stateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skiff"), nil
}
```

Repo conventions that apply: every new source file gets the standard
header block (see the top of any file in `internal/app/`; copyright year
2026); every function — exported or not — gets a doc comment explaining
intent; every new source file gets a same-package `_test.go`; tests use
`t.TempDir()` and env overrides, never the real home; TDD (write the
failing test first). CLAUDE.md's rule: **never mutate UI state from
goroutines — post custom tcell events.**

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Tests | `make test` | exit 0, all packages `ok` |
| Lint | `make lint` | exit 0, no output from gofmt/vet/staticcheck |
| One package | `go test ./internal/app/ -run TestSafeGo -v` | PASS |

## Scope

**In scope** (the only files you should modify):
- `internal/app/app.go` (Close, Run, new safeGo + crash helpers — or a new
  `internal/app/crash.go` + `crash_test.go`, preferred so the helpers get
  their own file/test pair)
- `main.go` (only if signal wiring belongs there; prefer inside `Run`)
- The 13 `internal/app` goroutine sites listed above (mechanical wrap)
- `internal/finder/finder.go` (accept an injectable panic-guard hook)
- New tests beside each change

**Out of scope** (do NOT touch):
- `internal/app/refresh.go` beyond wrapping its two `go func()` bodies —
  plan 011 restructures the tick.
- Any change to what the goroutines *do* or the events they post.
- tcell itself; no forking or vendoring.

## Git workflow

- Branch: `advisor/004-crash-signal-robustness`
- Imperative commit subjects, no Claude trailers (repo rule), commit per
  step.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Signal handling → clean quit

In `Run` (before the loop), register `signal.Notify(ch, syscall.SIGTERM,
syscall.SIGHUP, os.Interrupt)` and start a goroutine that, on receipt,
posts a new custom event `quitRequestEvent` via `a.screen.PostEvent`
(model the event type on `flashExpiryEvent` in `internal/app/draw.go:704`
— a struct with a `when time.Time` and the standard `When()` method).
Handle it in `handleEvent`'s switch by setting `a.quit = true` directly —
signals must not be blocked by the dirty-tab modal; the session save in
`Close()` preserves state and dirty buffers stay dirty on next open's
session restore. `signal.Stop` the channel when `Run` returns. Note in the
doc comment why SIGINT is included: tcell puts the terminal in raw mode so
Ctrl-C arrives as a key event, not SIGINT — the handler is for `kill -INT`
from outside.

Note: `PollEvent` blocks, and `PostEvent` is tcell's documented
thread-safe wake-up — this is the same mechanism every background
goroutine in the repo already uses.

**Verify**: `go test ./internal/app/ -run TestSignal -v` → PASS (test from
the Test plan below), then `make lint` → exit 0.

### Step 2: `safeGo` + crash log for the 13 app goroutines

Create `internal/app/crash.go` with:

- `crashLogPath() (string, error)` — mirrors `session.stateDir()`'s XDG
  logic (it is unexported in `internal/session`; duplicate the ~10 lines
  rather than exporting — match the comment style) and returns
  `<state>/skiff/crash-<unix-timestamp>.log`.
- `writeCrashLog(name string, r any, stack []byte) string` — best-effort
  write of: `skiff <version.Version>`, `TERM=` + `os.Getenv("TERM")`,
  `runtime.GOOS/GOARCH`, the goroutine name, the recovered value, and the
  stack from `runtime/debug.Stack()`. Returns the path ("" on failure).
  Never panics.
- `func (a *App) safeGo(name string, fn func())` — runs `go func()` with
  `defer func() { if r := recover(); ... }()`. On panic: call
  `a.screen.Fini()` (see safety note below), write the crash log, print
  `fmt.Fprintf(os.Stderr, "skiff: background task %q crashed — log at %s\n", name, path)`,
  then `os.Exit(2)`.

Safety note for the doc comment: tcell's `Fini` is documented as the
teardown call and is guarded internally (`fini` runs once via
`sync.Once`-style checks in tcell 2.13.x); calling it from the panicking
goroutine while the main loop blocks in `PollEvent` makes `PollEvent`
return nil, which `Run` already treats as quit (`app.go:680-682` and
`691-694`). Re-verify this against the vendored tcell version before
relying on it — if `Fini` proves unsafe off the event loop in practice
(deadlock or double-close panic in a test), switch to: post a
`fatalErrorEvent` carrying the log path, have `handleEvent` set `a.quit`,
and do the Fini + stderr print from `Run`'s exit path. Document whichever
holds.

Then mechanically convert the 13 `internal/app` sites from
`go func() { ... }()` to `a.safeGo("<name>", func() { ... })` with a
short stable name each (`"tree-scan"`, `"git-status"`, …).

**Verify**: `grep -rn "go func()" internal/app/ | grep -v _test | wc -l` →
`0`; `go test ./internal/app/ -run TestSafeGo -v` → PASS.

### Step 3: The finder goroutine

`internal/finder` cannot import `internal/app`. Add an optional hook field
to the finder (e.g. `PanicGuard func(name string, fn func())` on the
`Finder` struct, defaulting to plain `go fn()` semantics when nil) and
call it at `finder.go:94`'s launch site; wire `a.safeGo` into it where the
app constructs the finder (find the construction site with
`grep -rn "finder.New" internal/app/`). Keep the zero-value behaviour
identical so finder tests without a guard still pass.

**Verify**: `go test ./internal/finder/ ./internal/app/` → ok.

### Step 4: Main-goroutine recover + trash into Close

- In `Run`, add a top `defer` that recovers, calls `a.Close()` (Fini is
  idempotent), writes the crash log, prints the same stderr line, and
  re-panics so the exit code and trace still signal failure.
- Move `a.emptyTrash()` from the tail of `Run` into `Close()` (after
  `saveSession`, before `Fini`), and delete the call at the end of `Run`.
  Keep the explanatory comment with it.

**Verify**: `make test` → exit 0.

## Test plan

New file `internal/app/crash_test.go` (same package), modelled
structurally on `internal/app/fileops_test.go`. Each test gets a doc
comment (repo rule). Point `XDG_STATE_HOME` at `t.TempDir()` via
`t.Setenv` in every test that can write a crash log.

- `TestSafeGo_RecoversAndWritesCrashLog` — cannot use the real `os.Exit`;
  factor the panic-handling body into an unexported
  `handleGoroutinePanic(name string, r any) (logPath string)` that
  `safeGo` calls, and test THAT: fires it with a synthetic value, asserts
  the log file exists under the env-overridden state dir and contains the
  version string and goroutine name.
- `TestSafeGo_RunsFnNormally` — no panic → fn runs, no log written.
- `TestSignalRequestsQuit` — build a test app (`newTestApp` in
  `app_test.go` is the exemplar), deliver a `quitRequestEvent` through
  `a.handleEvent`, assert `a.quit` is true. Do not send real signals in
  tests.
- `TestClose_EmptiesTrash` — create a file in `t.TempDir()`, route it
  through `a.moveToTrash`, call `a.Close()`, assert the stored path is
  gone and `a.trashed` is nil. (Simulation screen's Fini is safe in
  tests — `newTestApp` already owns one.)
- Update any test that relied on `emptyTrash` running only in `Run` (grep
  `emptyTrash` in `_test.go` files first).

Verification: `make test` → exit 0; `make lint` → exit 0.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -rn "go func()" internal/app/ --include="*.go" | grep -v _test` → no output
- [ ] `grep -rn "signal.Notify" internal/app/ main.go` → exactly one site
- [ ] `grep -n "emptyTrash" internal/app/app.go` shows it inside `Close`, not `Run`
- [ ] New tests listed above exist and pass
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- The excerpts above don't match the live code (drift).
- `Screen.Fini` from a non-main goroutine deadlocks or double-panics in
  the test run **and** the fallback (post a fatal event) also fails to
  unblock `PollEvent` — report what you observed instead of shipping
  either.
- Wrapping a goroutine site changes observable event ordering in an
  existing test (a wrapped body must behave identically when it doesn't
  panic).
- You find a goroutine site not in the table above — add it to the wrap
  set, but STOP if it lives outside `internal/app`/`internal/finder`.

## Maintenance notes

- Every future `go func()` in `internal/app` must go through `safeGo`; a
  reviewer should reject bare `go func()`. Consider (deferred, not this
  plan) a lint fence: a test that greps for bare `go func()` in the
  package.
- If plan 011 (quiet tick) restructures `refresh.go`, its new goroutines
  must keep the `safeGo` wrap.
- The crash log has no rotation; if that ever matters, cap to the newest
  N files in `writeCrashLog` (deferred — crash logs should be rare).
