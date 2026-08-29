# Plan 019: Cover projfind's mouse/draw paths, remove time-window test flakes, make test-short real

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/projfind.go internal/app/projfind_test.go internal/finder/finder.go internal/finder/finder_test.go internal/app/format_test.go main_test.go Makefile`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P3
- **Effort**: S-M (three independent parts; each is small)
- **Risk**: LOW — tests and one Makefile comment only, except part C's `t.Skip` gates
- **Depends on**: plans/001-test-state-isolation.md
- **Category**: tests
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Three unrelated test-quality gaps, batched because each is small:
(A) the project-find panel — the front door to project-wide replace, the
editor's only N-file mutation — has 715 source lines against 173 test
lines, with its entire mouse hit-test and all five draw functions
untested; a hit-test off-by-one applies a replace from the wrong row.
(B) Two assertions sample wall-clock windows and can fail in both
directions: flake on a loaded CI runner, or silently pass when the bug
they exist to catch arrives late. CI gates merges on `go test -race` on
two platforms, so a flake here blocks unrelated PRs.
(C) `make test-short` promises "skip anything tagged slow" but only one
test in 1330 honors `-short`, so the documented fast loop is the slow
loop without the race detector.

## Current state

### Part A — projfind coverage

- `internal/app/projfind.go` — 715 lines. Untested functions (zero
  references from any `_test.go`): `projFindMove` (:228),
  `projFindClampView` (:245), `handleProjFindMouse` (:378, ~58 lines of
  row/fold-arrow/gutter hit-testing), `drawProjFind` (:437),
  `drawProjFindResults` (:446), `drawProjFindRow` (:476),
  `drawProjFindBar` (:556), `matchRuneSpans` (:692).
- `internal/app/projfind_test.go` — 173 lines, six tests:
  `TestProjFindRowsGroupAndFold`, `TestProjFindStaleGenerationDropped`,
  `TestProjFindEnterOpensAtLine`, `TestProjFindActivateHeaderFolds`,
  `TestProjFindEscCloses`, `TestProjFindDebounceAndStaleKick` — all
  keyboard/state level, none mouse, none draw.
- `internal/app/mouse.go:75-78` — the panel intercepts mouse before all
  non-overlay dispatch:

```go
	if a.projFindOpen {
		a.handleProjFindMouse(x, y, btn)
		return
	}
```

- Pattern exemplars to model on (read them first):
  `TestHandleMouse_Wheel` (`internal/app/mouse_test.go:413`) for
  injecting mouse events at computed coordinates, and `TestDraw_AllPanels`
  (`internal/app/draw_test.go:76`) for SimulationScreen cell assertions.
  `newTestApp` (`app_test.go:34`) builds the app.

### Part B — time-window assertions

`internal/finder/finder_test.go:95-100` (inside the rebuild-callback test):

```go
	// Give any spurious extra callbacks a moment to fire.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := doneCount
	mu.Unlock()
	if got != 1 {
		t.Fatalf("onDone fired %d times, want 1", got)
```

The deterministic quiescence point already exists in the code under test —
`internal/finder/finder.go:94-110`: the rebuild goroutine calls
`onDone()` in its body and `defer f.running.Store(0)` runs **after** it
(deferred calls run last), so once `f.running` reads 0 no further callback
from that rebuild can arrive:

```go
	go func() {
		defer f.running.Store(0)
		paths, viaGit, err := BuildIndex(root)
		...
		if onDone != nil {
			onDone()
		}
	}()
```

`internal/app/format_test.go:690-705` — `expectNoFormatEvent` polls a
150ms wall-clock window (callers at :205, :737, :759) and fails if a
`formatDoneEvent` shows up. The event is only ever posted from the exec
goroutine (`internal/app/format.go:509` `go func` → `:537 PostEvent`);
the refusal paths these tests exercise return **before** that goroutine
is spawned, i.e. synchronously — so a sentinel event posted after the
triggering call is a strict happens-after marker.

### Part C — test-short

`Makefile:72-75`:

```make
# test-short is the quick local iteration loop: skip anything tagged
# slow with -short, no race detector. Use this while writing tests.
test-short:
	go test -short ./...
```

The only `-short` awareness in the repo is
`internal/finder/finder_test.go:238` (`TestFinder_SearchMatchesFullSortOrdering`
shrinks its corpus 20000→2000 — a reduction, not a skip). The genuinely
slow ungated tests: `main_test.go:293` `TestMainExitCodes` shells out to a
full `go build -o <tmp> .` of the whole binary; the `internal/app` package
alone takes ~6.5s; the git suites (`internal/app/gitops_test.go`,
`gitworktree_test.go`, `internal/git`) fork real `git` processes
per test.

Conventions (CLAUDE.md): doc comment on every `Test*` func; simulation
screen for draw code; skip (`t.Skip`) only for hard environment
requirements — **and `-short` gating is an explicit contract, not a
dodge, so document each gate's reason in its skip message**.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite | `make test` | exit 0 |
| Short loop | `make test-short` | exit 0, measurably faster than `make test` |
| Flake check | `go test ./internal/finder/ -run TestFinder -count=30` | PASS every run |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:

- `internal/app/projfind_test.go` (new tests)
- `internal/finder/finder_test.go` (rewrite the sleep-based assertion; may add a tiny same-package quiesce helper)
- `internal/app/format_test.go` (rewrite `expectNoFormatEvent`; may define a test-local sentinel event type)
- `main_test.go`, `internal/app/gitops_test.go`, `internal/app/gitworktree_test.go`, `internal/git/*_test.go` (add `testing.Short` gates ONLY — no other edits)
- `Makefile` (the `test-short` comment, if its contract wording needs correcting)
- `plans/README.md` (status row)

**Out of scope**:

- `internal/app/projfind.go` and all other production source — if a new
  test finds a real bug, report it (STOP condition), don't fix it here.
- `internal/finder/finder.go` — the quiescence point exists; do not add
  test hooks to production code unless the same-package test genuinely
  cannot observe `f.running` (it can — same package).
- Deleting the `test-short` target (gating is the chosen approach).

## Git workflow

- Branch: `advisor/019-test-quality`
- One commit per part (A, B, C); imperative subjects, no AI trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step A1: projfind mouse hit-test coverage

In `projfind_test.go`, following `TestHandleMouse_Wheel`'s coordinate
style: build a `newTestApp`, create 2–3 files with known matches, open the
panel the way `TestProjFindEnterOpensAtLine` does (read it; reuse its
seeding helper), then drive `a.handleMouse`-level events (or call
`a.handleProjFindMouse` directly with computed coordinates — prefer the
public `handleMouse` path since `mouse.go:75-78` routing is part of what
needs pinning):

- click on a match row → the file opens at that match's line (assert the
  active tab path + cursor line — same assertions
  `TestProjFindEnterOpensAtLine` makes for Enter);
- click on a file-header row's fold arrow → the group folds (assert via
  the same state `TestProjFindActivateHeaderFolds` checks);
- click below the last row → no state change, no panic.

To compute row coordinates, read `drawProjFindResults`/`projFindClampView`
for the panel's top offset and derive Y from the row index; if the
geometry helpers are unexported values, mirror how `mouse_test.go`
computes editor coordinates rather than hardcoding magic numbers.

**Verify**: `go test ./internal/app/ -run TestProjFind -v` → all PASS,
including ≥3 new tests.

### Step A2: projfind draw + span coverage

- One SimulationScreen draw test modeled on `TestDraw_AllPanels`: open the
  panel with results, `a.draw()`, assert a known match line's text and its
  file header appear in the expected rows, and the counter text
  (`projFindCounterText`) appears in the bar.
- Table test for `matchRuneSpans` (:692): ASCII match, repeated query on
  one line, multibyte text before the match (spans are rune-indexed —
  assert alignment holds), no-match → empty.

**Verify**: `go test ./internal/app/ -run 'TestProjFind|TestMatchRuneSpans' -v` → PASS.

### Step B1: deterministic finder callback assertion

Replace the `time.Sleep(50ms)` block in `finder_test.go:~95`: poll
`f.running.Load() == 0` with a deadline (reuse the file's existing
poll-until-deadline style at :88-93), then assert `doneCount == 1` once.
Add a comment stating the invariant it leans on: *onDone runs in the
rebuild goroutine body; `defer f.running.Store(0)` runs after it, so
running==0 ⇒ all callbacks delivered.*

**Verify**: `go test ./internal/finder/ -count=30` → PASS every run; and
watch-it-catch: temporarily double-call `onDone()` in `finder.go`'s
goroutine, run the test, confirm FAIL, revert.

### Step B2: deterministic no-format-event assertion

Rewrite `expectNoFormatEvent` (format_test.go:690): define a test-local
sentinel event (`type formatSentinelEvent struct{ when time.Time }` with a
`When()` method, mirroring how production events in `format.go` are
declared), and change the helper to: post the sentinel via
`a.screen.PostEvent`, then drain events until the sentinel arrives
(bounded by a generous 5s deadline that only trips on a genuinely stuck
queue), failing if any `*formatDoneEvent` precedes it. Update the three
callers (:205, :737, :759) — the `within` duration parameter goes away.
First, confirm each caller's refusal path really is synchronous: read the
code path each test triggers and check it returns before `format.go:509`'s
`go func`; if any is asynchronous, STOP (the sentinel would race).

**Verify**: `go test ./internal/app/ -run TestRunFormatOnSave -count=10` →
PASS every run.

### Step C1: gate the slow tests

Add at the top of each (with the existing skip-style message):

- `main_test.go` `TestMainExitCodes`: `if testing.Short() { t.Skip("builds the full binary — slow; run without -short") }`
- Each `Test*` in `internal/app/gitops_test.go`, `gitworktree_test.go`
  and `internal/git`'s process-forking tests that shells out to real git:
  prefer gating the shared setup helper if one exists (read the files;
  if every test funnels through one repo-fixture helper, one gate there
  covers all — otherwise gate per-test).

Then time both loops and correct the Makefile comment if the wording
overpromises:

```sh
time make test        # baseline
time make test-short  # must be materially faster
```

**Verify**: `make test-short` exit 0 and wall-clock at least ~40% faster
than `make test`; `go test ./... | tail -1` still exit 0 (nothing gated
out of the full run).

### Step C2: full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

- Part A: ≥3 mouse hit-test tests + 1 draw test + `TestMatchRuneSpans`
  table test, all in `projfind_test.go`, doc-commented.
- Part B: no new tests — two existing assertions made deterministic, each
  proven by `-count` runs and a watch-it-catch mutation.
- Part C: no new tests — gates only; proven by the timing comparison.

## Done criteria

- [ ] `grep -c "func Test" internal/app/projfind_test.go` ≥ 11 (was 6)
- [ ] `grep -n "time.Sleep(50" internal/finder/finder_test.go` → no matches
- [ ] `grep -n "within time.Duration" internal/app/format_test.go` → no matches (helper signature changed)
- [ ] `go test ./internal/finder/ -count=30` and `go test ./internal/app/ -run TestRunFormatOnSave -count=10` → PASS
- [ ] `make test-short` measurably faster than `make test` (report both times)
- [ ] `make test` and `make lint` exit 0
- [ ] `plans/README.md` status row updated

## STOP conditions

- A new projfind mouse test exposes a real hit-test bug (click lands on
  the wrong row / panics): report the failing test and coordinates; the
  fix belongs in a bug PR against `projfind.go`, not in this test-only plan.
- A caller of `expectNoFormatEvent` turns out to trigger an asynchronous
  refusal (the `go func` at format.go:509 is reached): the sentinel
  rewrite would race — report which caller and stop part B2.
- `f.running` is not observable from `finder_test.go` (package layout
  changed): report rather than adding a production accessor unilaterally.
- Gating git suites under `-short` removes more than ~30% of `internal/app`'s
  test count: the gate is too broad — report the count delta first.

## Maintenance notes

- New projfind features (chips, filters) now have draw/mouse exemplars to
  extend — reviewers should expect a mouse test alongside any new
  clickable region in the panel.
- The sentinel-drain helper generalizes: any future "X never happened"
  assertion should use it, never a wall-clock window. Worth a mention if
  CLAUDE.md's test section is next edited.
- Deferred: `t.Parallel()` adoption (blocked broadly by `t.Setenv` usage
  and shared SimulationScreen patterns) — not worth the churn now.
