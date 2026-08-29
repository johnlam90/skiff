# Plan 022: Extract the projFind and gitPanel field clusters out of the App god struct

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/`
> internal/app WILL have drifted — this plan runs LAST. Re-read the App
> struct in `internal/app/app.go` and rebuild the two field inventories below
> from the live code before starting; the inventories here are the planning-
> time baseline, the live struct is the truth. STOP only if a listed field
> has been REMOVED (not merely moved/re-commented) or if another plan already
> extracted one of these clusters.

## Status

- **Priority**: P3
- **Effort**: L
- **Risk**: MED — mechanical renames across ~40 files including tests; the
  package's 1,300+ tests make each step verifiable, but the diff is wide
- **Depends on**: plans/021-*.md and every lower-numbered plan that modifies
  `internal/app/` (consult `plans/README.md`) — execute this plan LAST in the
  whole sequence
- **Category**: tech-debt
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`internal/app/app.go`'s `App` struct spans lines 151-478 (~98 fields at
planning time) and every one of the package's ~38 source files hangs methods
off this single receiver. It is the highest-churn file in the repo. The field
prefixes already name the missing structs — `proj*` (23 fields), `gitPanel*`
(7) — but the compiler can't see the boundaries, so any change to
project-find has 98 fields in scope and nothing stops an unrelated subsystem
poking a `projFindGen`. This plan extracts exactly the two largest clusters
as named sub-structs, one commit each, zero behavior change — the template
for later clusters without attempting them all.

## Current state

`internal/app/app.go:151` opens `type App struct {`. Two clusters, as of the
planning commit:

**Cluster 1 — project find/replace** (`app.go:343-367`):

```go
	// Project-wide content search (see projfind.go).
	projFindOpen      bool
	projFindValue     []rune
	projFindCursor    int
	projFindScroll    int
	projFindGen       int // generation counter; stale sweeps are dropped
	projFindBusy      bool
	projFindMatches   []search.Match
	projFindTruncated bool
	projFindSelected  int
	projFindScrollY   int
	projFindFolded    map[string]bool
	projFindLiveGen   atomic.Int64 // latest gen, readable from sweep goroutines
	projFindMatchCase bool
	projFindWholeWord bool
	projFindRegex     bool

	// Project-wide replace riding the panel (see projreplace.go). The
	// X ranges are stamped by drawProjFindBar for the mouse handler.
	projReplaceOpen                        bool
	projReplaceValue                       []rune
	projReplaceCursor                      int
	projFocusReplace                       bool
	projReplaceFieldX0, projReplaceFieldX1 int
	projReplaceAllX0, projReplaceAllX1     int
```

**Cluster 2 — git panel** (`app.go:444-455` plus one field at 463):

```go
	gitPanelActive bool
	gitPanelRows   []gitChangeRow
	gitPanelScroll int
	// … (keyboard-mode comment) …
	gitPanelKeys   bool
	gitPanelOnBtns bool
	gitPanelBtn    int
	// … and, inside the write-side block at :461-467:
	gitPanelSelected  int
```

**Deliberately NOT moving** (couplings documented in the struct itself):
`diffPanelRow` (`app.go:464`) — the comment at `app.go:469-472` says "the
panel row marker belongs to the Git panel", but it is read by the diff
overlay's walk (`diffview.go`); moving it would put a diffview dependency on
the new struct in the same breath as the extraction. Leave it; list as a
follow-up. Likewise the write-side gate (`gitOpBusy`, `gitCommitChecks`,
`gitDeleteTarget`, `gitWorktreeTarget`, `diffBase`) and the status-side
fields (`gitSnap`, `gitRefreshInFlight/Queued`, `gitMissingSeen`,
`gitRunner`) stay put.

Repo conventions that apply: every new type/func gets a doc comment
(project-wide rule); the state structs live in the file that owns the
subsystem (`projfind.go`, `gitchanges.go`), not in `app.go` — matching how
`clickRecord`/`tabRect` live near their users.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite (race) | `make test` | exit 0 |
| Lint | `make lint` | exit 0 |
| Fast loop | `go test ./internal/app/` | ok |
| Old names gone | `grep -rn "projFindOpen\|gitPanelActive" internal/` | no matches (after each step) |
| Usage inventory | `grep -rln "projFind\|projReplace\|projFocusReplace" internal/app/` | the file list you must touch |

## Scope

**In scope**: files under `internal/app/` only (sources and tests), and only
renames of the listed fields plus the two new type declarations.

**Out of scope** (do NOT touch):
- Any behavior: no logic, no signatures, no new methods beyond the struct
  declarations.
- `diffPanelRow` and the write-/status-side git fields (above).
- The other prefix clusters — `last*` (lastClick, lastTabRects, lastShiftAt,
  lastEscape, lastDragX/Y), `tree*` (treeRefreshStop, treeScanInFlight/
  Queued/Gen), `find*`/`replace*` (the in-file find bar), `sidebar*`,
  `menu*`, `fileClip*`, `trash*` — follow-up candidates, listed in
  Maintenance notes.
- Anything outside `internal/app/`.

## Git workflow

- Branch: `advisor/022-app-state-clusters`
- Exactly two code commits ("Extract projFindState from App",
  "Extract gitPanelState from App"), each leaving the suite green. No Claude
  trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Rebuild the live inventories

Read the current `App` struct. List every field starting `projFind`,
`projReplace`, `projFocus`, and every field starting `gitPanel`. Diff against
the inventories above; additions join the move (same mechanical rename rule),
removals are fine, and anything ambiguous is a STOP.

**Verify**: your two lists, posted in the final report.

### Step 2: Extract `projFindState`

1. In `internal/app/projfind.go`, declare (with a doc comment explaining it
   bundles the project-find panel + its riding replace state, moved out of
   App):

```go
type projFindState struct {
	findOpen      bool
	findValue     []rune
	findCursor    int
	findScroll    int
	findGen       int // generation counter; stale sweeps are dropped
	findBusy      bool
	findMatches   []search.Match
	findTruncated bool
	findSelected  int
	findScrollY   int
	findFolded    map[string]bool
	findLiveGen   atomic.Int64 // latest gen, readable from sweep goroutines
	findMatchCase bool
	findWholeWord bool
	findRegex     bool

	replaceOpen                    bool
	replaceValue                   []rune
	replaceCursor                  int
	focusReplace                   bool
	replaceFieldX0, replaceFieldX1 int
	replaceAllX0, replaceAllX1     int
}
```

   (Move each field's existing doc comment with it, verbatim.)
2. In `App`, replace the whole cluster with one named field —
   `projFind projFindState` — NAMED, not anonymously embedded: anonymous
   embedding would promote `findOpen` onto App and collide with the in-file
   find bar's existing `App.findOpen`.
3. Mechanical rename across `internal/app/` (sources AND tests), old → new:

   `a.projFindOpen`→`a.projFind.findOpen` · `projFindValue`→`projFind.findValue` ·
   `projFindCursor`→`projFind.findCursor` · `projFindScroll`→`projFind.findScroll` ·
   `projFindGen`→`projFind.findGen` · `projFindBusy`→`projFind.findBusy` ·
   `projFindMatches`→`projFind.findMatches` · `projFindTruncated`→`projFind.findTruncated` ·
   `projFindSelected`→`projFind.findSelected` · `projFindScrollY`→`projFind.findScrollY` ·
   `projFindFolded`→`projFind.findFolded` · `projFindLiveGen`→`projFind.findLiveGen` ·
   `projFindMatchCase`→`projFind.findMatchCase` · `projFindWholeWord`→`projFind.findWholeWord` ·
   `projFindRegex`→`projFind.findRegex` · `projReplaceOpen`→`projFind.replaceOpen` ·
   `projReplaceValue`→`projFind.replaceValue` · `projReplaceCursor`→`projFind.replaceCursor` ·
   `projFocusReplace`→`projFind.focusReplace` · `projReplaceFieldX0/X1`→`projFind.replaceFieldX0/X1` ·
   `projReplaceAllX0/X1`→`projFind.replaceAllX0/X1`

   Longest-name-first when using sed, or use `gopls rename` per field if
   available — either way the compiler is the net.
4. Any struct-literal construction of App that set these fields (search
   `newTestApp` in `app_test.go`) initializes the sub-struct instead.

**Verify**: `go test ./internal/app/` → ok; `grep -rn "projFindOpen\|projReplaceOpen\|projFocusReplace" internal/` → no matches; `make lint` → exit 0. Commit.

### Step 3: Extract `gitPanelState`

Same procedure. Declare in `internal/app/gitchanges.go`:

```go
type gitPanelState struct {
	active bool
	rows   []gitChangeRow
	scroll int

	keys   bool
	onBtns bool
	btn    int

	selected int
}
```

(with the three original doc comments — panel state, keyboard mode,
walk-selection — moved onto the groups verbatim). App gains
`gitPanel gitPanelState`; renames:

`gitPanelActive`→`gitPanel.active` · `gitPanelRows`→`gitPanel.rows` ·
`gitPanelScroll`→`gitPanel.scroll` · `gitPanelKeys`→`gitPanel.keys` ·
`gitPanelOnBtns`→`gitPanel.onBtns` · `gitPanelBtn`→`gitPanel.btn` ·
`gitPanelSelected`→`gitPanel.selected`

**Verify**: `go test ./internal/app/` → ok; `grep -rn "gitPanelActive\|gitPanelSelected\|gitPanelOnBtns" internal/` → no matches; `make lint` → exit 0. Commit.

### Step 4: Full gates + purity check

**Verify**: `make test` → exit 0 (whole repo, race). `make lint` → exit 0.
`git diff <branch-base>..HEAD --stat` touches only `internal/app/`.
Purity: `git diff <branch-base>..HEAD -- internal/app/ | grep "^+" | grep -v "^+++" | grep -vE "projFind\.|gitPanel\.|type projFindState|type gitPanelState|projFind projFindState|gitPanel gitPanelState|^\+\s*(//|$)|^\+\}|^\+\s*[a-zA-Z_]+\s+(bool|int|\[\]rune|\[\]search\.Match|map\[string\]bool|atomic\.Int64|\[\]gitChangeRow)"` should output (near) nothing — every added line is a rename, a struct declaration line, or a moved comment. Eyeball the residue.

## Test plan

No new tests: the contract is behavior-identical renames, enforced by the
existing suite passing untouched (the ~54 test references to these fields are
renamed, not rewritten — assertions keep their expected values). If any test
needs its LOGIC changed to pass, that is a STOP (you changed behavior).

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `grep -rn "projFindOpen\|projReplaceOpen\|gitPanelActive\|gitPanelSelected" internal/` → no matches
- [ ] `App` contains `projFind projFindState` and `gitPanel gitPanelState`; the 30 old fields are gone
- [ ] `diffPanelRow`, `gitOpBusy`, `gitSnap` (and the rest of the deliberately-kept fields) still live directly on `App`
- [ ] `make test` and `make lint` exit 0
- [ ] diff confined to `internal/app/`
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- Another plan already extracted either cluster (check the live struct first).
- A listed field turns out to be read by a file OUTSIDE `internal/app/`
  (nothing should be — all fields are unexported — but verify with the
  usage-inventory grep before renaming).
- Any code assigns or copies the whole cluster by value in a way that would
  copy `atomic.Int64` (e.g. `x := a.projFind`) — `go vet` flags this
  (copylocks); report rather than restructure.
- A test requires a logic change (not a rename) to pass.
- `plans/README.md` shows any earlier `internal/app/`-touching plan still
  TODO/IN PROGRESS.

## Maintenance notes

- This is the template for the remaining clusters; follow-up candidates in
  rough value order: the in-file find/replace bar (`find*`/`replace*`,
  8 fields), tree-refresh bookkeeping (`tree*`, 4), drag/auto-scroll
  (`autoScroll*` + `lastDragX/Y` + `dragMode`), sidebar (`sidebar*`, 4),
  menu (`menu*`, 4), file clipboard (`fileClip*`, 3), trash (`trash*`, 2).
- `diffPanelRow` is the known cross-subsystem coupling (git panel ↔ diff
  overlay); if a later refactor moves it into `gitPanelState`, the diff
  overlay should get it through a method, not a field poke.
- Reviewers of the next feature touching project-find should expect state on
  `a.projFind.*` — a new bare `projFoo` field on App is the regression smell.
