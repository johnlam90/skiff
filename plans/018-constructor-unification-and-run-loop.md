# Plan 018: Unify the three App constructors and drive Run() end to end in tests

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/app.go internal/app/app_test.go internal/app/mouse.go internal/app/mouse_test.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW (additive seam + new tests; constructors must stay behavior-identical)
- **Depends on**: plans/001-test-state-isolation.md (Run/quit paths save sessions; do not run these tests before 001 lands)
- **Category**: tests
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

The editor's main event loop, `App.Run`, has **zero test call sites** — the
burst-drain inner loop written specifically for SSH wheel bursts, the
`PollEvent() == nil` quit path, the pre-first-paint sidebar/mouse-mode
ordering, and the `emptyTrash()` teardown (a data path: it deletes the
delete-undo window) are all unverified. Meanwhile the app is constructed
three different ways: `New`, `NewSingleFile` (zero coverage, duplicates
~40 lines of `New`), and the test-only `newTestApp` struct literal that
omits several things production always does — so 38 test files validate an
App shape production never has (nil `finder`, zero `mouseFlags`, no user
config). A shared constructor plus one loop-driving test closes both gaps.

## Current state

Relevant files:

- `internal/app/app.go` — `New` (:486), `NewSingleFile` (:567), `Close` (:654), `Run` (:665).
- `internal/app/app_test.go` — `newTestApp` (:34).
- `internal/app/mouse.go` — `startAutoScroll` (:546) / `stopAutoScroll` (:570) and the `autoScrollEvent` type (:33).
- `internal/app/fileops.go` — `emptyTrash` (:189).

`app.go:665-704` — the loop under test (excerpt):

```go
func (a *App) Run() error {
	a.width, a.height = a.screen.Size()
	// ... applyResponsiveSidebar / syncMouseMode / draw / Show ...
	for !a.quit {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		a.handleEvent(ev)
		// Drain everything already queued before paying for a draw. ...
		for !a.quit && a.screen.HasPendingEvent() {
			ev = a.screen.PollEvent()
			if ev == nil {
				a.quit = true
				break
			}
			a.handleEvent(ev)
		}
		a.draw()
		a.screen.Show()
	}
	// The undo window for deletes is the session: discard the trash on
	// the way out so removed work doesn't pile up invisibly on disk.
	a.emptyTrash()
	return nil
}
```

`a.quit` is set in exactly two places (`grep -rn "a.quit = true" internal/app/*.go`,
excluding tests): `actions.go:542` (the Quit action) and `app.go:692` (the
nil-event drain guard above).

What each constructor does today, in order (read `app.go:486-618` in full
before starting):

- **`New(rootDir)`**: abs-canonicalize root → `tcell.NewScreen` + `Init` →
  `EnableMouse(mouseBaseFlags)` → `EnablePaste` → theme + `SetStyle` +
  `Clear` → `filetree.New` → struct literal (sets `mouseFlags:
  mouseBaseFlags`, `sidebarShown: true`, `wrapOn: true`, sentinel `-1`s) →
  `setActiveFolder` → `loadUserConfig` → `applyColorDepth` →
  `refreshGitStatus` → `loadCustomActions` → welcome `flash` →
  `startTreeRefresh` → build `a.finder` + background `Rebuild` →
  `restoreSession`.
- **`NewSingleFile(filePath)`**: same screen/mouse/paste/theme block →
  root = file's parent, abs → struct literal with `tree: nil`,
  `sidebarShown: false` → `setActiveFolder` → `loadUserConfig` →
  `applyColorDepth` → `loadCustomActions` → `openFile(filePath)`. No
  finder, no tree refresh, no session restore, no welcome flash, no
  `refreshGitStatus` (openFile loads its own gutter markers).
- **`newTestApp` (app_test.go:34-79)**: SimulationScreen `Init` + SetSize →
  `filetree.New` → struct literal (note: **no** `mouseFlags` field, so it
  stays zero) → `setActiveFolder` → `a.width, a.height = scr.Size()` →
  `refreshGitStatus` → a git-refresh drain `t.Cleanup`. It never calls
  `loadUserConfig`, `applyColorDepth`, `loadCustomActions`, never builds
  `a.finder`, never flashes, never restores a session.

Single-file mode's `tree == nil` state is guarded at 16 production sites
(`grep -rn "tree == nil" internal/app/*.go | grep -v _test | wc -l` → 16),
e.g. `finder.go:76`, `actions.go:447`, `gitstatus.go:417,433` — but tests
only ever reach that state by hand-patching `a.tree = nil` onto a
tree-backed app, which is not the shape `NewSingleFile` actually produces
(it also has `sidebarShown: false` and no restored session).

`mouse.go:546-567` — auto-scroll posts events only the real loop consumes:

```go
func (a *App) startAutoScroll(dir int) {
	if a.autoScrollDir == dir {
		return
	}
	...
	go func() {
		ticker := time.NewTicker(autoScrollTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				_ = scr.PostEvent(&autoScrollEvent{when: t})
			}
		}
	}()
}
```

`grep -rn "handleAutoScroll\|startAutoScroll" internal/app/*_test.go` → 0
matches; `mouse_test.go:399-403` only asserts `autoScrollDir` is armed.

Conventions (CLAUDE.md): custom tcell events for goroutine→loop messaging;
doc comment on every function; tests use `tcell.NewSimulationScreen("UTF-8")`
and assert via `GetContents` (see `draw_test.go`); test doc comments explain
the pinned behavior.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Tests | `make test` | exit 0 |
| Package under work | `go test ./internal/app/ -run 'TestRun_|TestNewSingleFile' -v` | PASS lines for the new tests |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:

- `internal/app/app.go` (extract shared constructor; `New`/`NewSingleFile` become thin wrappers)
- `internal/app/app_test.go` (`newTestApp` rewired onto the seam; new `TestRun_*`, `TestNewSingleFile_*`)
- `internal/app/mouse_test.go` (auto-scroll drive test)
- `plans/README.md` (status row)

**Out of scope**:

- Any behavior change to what `New` / `NewSingleFile` produce — this is a
  seam extraction; byte-identical construction results are the bar.
- `main.go` — callers of `New`/`NewSingleFile` keep their signatures.
- The signal/panic robustness work (a separate plan touches `Run`'s
  shutdown path; keep this plan's `Run` edits to zero — tests only).

## Git workflow

- Branch: `advisor/018-constructor-unification`
- Imperative commit subjects, no AI trailers; one commit per step is fine.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Extract `newApp(scr tcell.Screen, rootDir string) *App`

In `app.go`, factor the struct literal + common post-literal calls into an
unexported constructor taking the screen as a parameter. Target shape:

```go
// newApp builds the App core every construction path shares: the struct
// literal with its sentinels, the active folder, user config, color-depth
// degrade, and custom actions. Callers layer on what their mode needs —
// New adds the tree/finder/session machinery, NewSingleFile opens the one
// file, tests inject a SimulationScreen.
func newApp(scr tcell.Screen, rootDir string, tree *filetree.Tree, sidebarShown bool) *App {
	a := &App{
		screen:         scr,
		mouseFlags:     mouseBaseFlags,
		theme:          theme.Default(),
		rootDir:        rootDir,
		tree:           tree,
		hoveredMenuRow: -1,
		diffPanelRow:   -1,
		sidebarShown:   sidebarShown,
		sidebarWidth:   defaultSidebarWidth,
		wrapOn:         true,
	}
	if tree != nil {
		a.setActiveFolder(tree.Root.Path)
	} else {
		a.setActiveFolder(rootDir)
	}
	a.loadUserConfig()
	a.applyColorDepth()
	a.loadCustomActions()
	return a
}
```

Rewrite `New` and `NewSingleFile` to: do their screen setup (NewScreen,
Init, EnableMouse, EnablePaste, SetStyle, Clear — this block stays
duplicated or gets its own tiny `initScreen` helper, executor's choice),
build the tree (or not), call `newApp`, then layer on their unique tail
(`New`: refreshGitStatus, flash, startTreeRefresh, finder, restoreSession;
`NewSingleFile`: openFile). Preserve the exact ordering each has today —
`applyColorDepth` must run after `loadUserConfig` (the comment at
app.go:531-534 explains why; keep that comment on the shared path).

**Verify**: `make test` → exit 0 (pure refactor; every existing test still
passes). `git diff app.go` shows `New`'s field-for-field literal gone.

### Step 2: Rewire `newTestApp` onto the seam

`newTestApp` becomes: build SimulationScreen (Init, SetSize as today) →
`filetree.New` → `a := newApp(scr, tree.Root.Path, tree, true)` → the
existing `a.width/a.height`, `refreshGitStatus`, and drain-cleanup tail.
Delete its hand-rolled struct literal. Note in its doc comment which
production steps it now genuinely shares and which it still skips
(flash, startTreeRefresh, finder, restoreSession) and why (no tickers,
no background goroutines in tests).

This makes `mouseFlags` non-zero and user config/custom actions loaded in
tests for the first time. Plan 001's `TestMain` XDG redirect guarantees
`loadUserConfig`/`loadCustomActions` read the throwaway config dir, not the
developer's.

**Verify**: `make test` → exit 0. If any test now fails because it
implicitly depended on `mouseFlags == 0` or an unloaded config, fix the
*test's assumption* and record it in the commit message — that is exactly
the divergence this plan exists to remove. More than 5 such failures →
STOP condition.

### Step 3: First real single-file-mode test

`NewSingleFile` still calls `tcell.NewScreen()` and cannot run headless.
Add a sibling seam: extract its post-screen body into
`newSingleFileApp(scr tcell.Screen, filePath string) *App` (calls `newApp`
with `tree=nil, sidebarShown=false`, then `openFile`), have `NewSingleFile`
wrap it, and test the seam:

```go
// TestNewSingleFileApp_ShapeInvariants pins the single-file mode contract:
// no tree, no sidebar, no finder, and the file open in a tab — the state
// 16 production guard sites (finder.go:76, actions.go:447, ...) exist to
// handle, previously reachable in tests only by hand-patching tree=nil.
```

Assert: `a.tree == nil`, `a.sidebarShown == false`, `a.finder == nil`,
exactly one tab whose path is the file, `a.rootDir` = the file's parent
(absolute). Also assert one guarded behavior end to end, e.g. the finder
action is a no-op / the menu row is hidden (find the `hasTree` predicate
referenced in `NewSingleFile`'s doc comment at app.go:560-562 and assert
through it).

**Verify**: `go test ./internal/app/ -run TestNewSingleFileApp -v` → PASS.

### Step 4: Drive `Run` end to end

New test in `app_test.go` (needs a `newTestApp`-style app whose screen is
the SimulationScreen — `Run` uses `a.screen` only, so no extra seam):

```go
// TestRun_DrainsBurstAndExits drives the real event loop: a queued wheel
// burst must be fully drained (scroll moved by the whole burst, not one
// tick per draw), the Quit action must terminate the loop, and the
// trash-empty teardown must run on the way out.
```

Mechanics: open a multi-line file into a tab; use
`scr.InjectMouse(x, y, tcell.WheelDown, tcell.ModNone)` N times to queue a
burst BEFORE calling `Run` (SimulationScreen queues injected events);
finally inject the quit gesture — the cheapest deterministic quit is
posting the action directly: `scr.PostEvent` a key sequence is fragile, so
prefer injecting the two keys of the Esc-leader quit (`Esc`, `q`) via
`scr.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)` then
`scr.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)`. Read
`internal/app/keys.go` / `leader.go` first to confirm `Esc q` dispatches
the Quit action with no intervening state; if the leader window is
time-gated, read how existing tests simulate it and copy that. Run
`a.Run()` on the test goroutine with a watchdog: wrap in a goroutine +
`select` on a done channel and a 10s `time.After` that calls `t.Fatal`
naming the hang point (the watchdog only fires on regression).

Assert after `Run` returns: the active tab's scroll advanced by the full
burst (compute expected from the wheel-scroll step in `mouse.go` — read
`wheelLines`/equivalent constant), `a.quit` is true, and a file moved to
trash before `Run` (use the app's delete path on a fixture file) is gone
from disk (`emptyTrash` ran).

**Verify**: `go test ./internal/app/ -run TestRun_DrainsBurstAndExits -v` → PASS,
and it completes in well under a second (no watchdog trip).

### Step 5: Drive auto-scroll through the loop

New test in `mouse_test.go`: arm auto-scroll the way the existing
`mouse_test.go:389+` test does (drag past the editor's bottom edge), then
instead of only asserting `autoScrollDir`, pump the loop: poll
`scr.HasPendingEvent()` / `a.handleEvent(scr.PollEvent())` in a bounded
loop (deadline 2s) until the tab's `ScrollY` has advanced at least one
line, then release the button and assert the goroutine stops posting
(drain until quiet, `autoScrollDir == 0`). This exercises
`autoScrollEvent` delivery + `handleAutoScroll` for the first time without
running full `Run` (the ticker goroutine posts to the SimulationScreen's
queue regardless).

**Verify**: `go test ./internal/app/ -run TestHandleMouse -v` → all PASS
including the new test; run it 20× (`-count=20`) → no flakes.

### Step 6: Full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

New tests (all with doc comments per convention):
- `TestNewSingleFileApp_ShapeInvariants` — single-file construction shape.
- `TestRun_DrainsBurstAndExits` — burst drain, quit, emptyTrash teardown.
- The auto-scroll loop-drive test in `mouse_test.go`.
Pattern exemplars: `TestHandleMouse_Wheel` (mouse_test.go:413) for
injection style, `newTestApp` for app setup, `draw_test.go` for
SimulationScreen assertions.

## Done criteria

- [ ] `grep -n "mouseFlags" internal/app/app_test.go` shows `newTestApp` no longer builds a bare literal missing it (field comes via `newApp`)
- [ ] `go test ./internal/app/ -run 'TestRun_|TestNewSingleFileApp' -v` → PASS
- [ ] `go test ./internal/app/ -run TestHandleMouse -count=20` → PASS
- [ ] `make test` and `make lint` exit 0
- [ ] `git status` clean outside in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

- `Run` never returns under SimulationScreen (hang in `PollEvent` after
  quit): report the exact blocking call and the screen's event-queue state
  — do NOT add sleeps or timeouts to force it; the finding is the deliverable.
- Step 2 breaks more than 5 existing tests — the divergence is larger than
  mapped; report the failing list.
- `Esc q` turns out not to be a two-keystroke quit path in `leader.go`
  (e.g. the leader window needs wall-clock arming you cannot inject) and no
  existing test demonstrates a working key-driven quit — report rather
  than reaching into `a.quit` directly, which would bypass the loop's
  dispatch and defeat the test's purpose.
- The excerpts above no longer match `app.go` (drift).

## Maintenance notes

- Plan 003 (crash/signal robustness), if executed later, edits `Run`'s
  shutdown path — `TestRun_DrainsBurstAndExits` is the fence that keeps
  that refactor honest; its author should extend it with the signal path.
- Once `newApp` exists, any new construction-time step (a new config
  loader, a new background job) belongs in `newApp` or must justify in a
  comment why it is mode-specific — reviewers should reject additions
  hand-copied into two constructors.
- Deferred deliberately: injecting a fake screen into `NewSingleFile`'s
  public signature (would churn `main.go` for no user value); the
  unexported seam gets the coverage without the API change.
