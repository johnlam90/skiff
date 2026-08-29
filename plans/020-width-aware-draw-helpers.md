# Plan 020: One width-aware clipped-text helper — CJK/emoji stop misaligning the chrome

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/overlay/chrome.go internal/overlay/pick.go internal/app/leaderstrip.go internal/app/gitchanges.go internal/filetree/filetree.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P3
- **Effort**: M
- **Risk**: MED (corrects wrong pixel positions — some existing assertions legitimately change)
- **Depends on**: none
- **Category**: tech-debt
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

The repo has five near-duplicate "draw a string clipped to N columns"
helpers, all assuming **one cell per rune** — and one of them doesn't clip at
all. A CJK ideograph is two cells; a combining mark is zero; a ZWJ emoji is
five runes in two cells. So a Japanese filename misaligns the file tree's
right-edge git letter, the git panel's columns drift, and a long path in an
overlay **title** paints straight through the frame border into whatever is
behind it. Meanwhile `internal/editor/cluster.go` already measures text
correctly with `uniseg` — the editor body is right and all the chrome is
wrong. `CLAUDE.md`'s "Three units" section is the design law here:

> "a rune is not a character and not a cell: a CJK ideograph eats two
> cells, a combining mark none, and a ZWJ family emoji is five runes
> painted in two cells. Caret motion and layout therefore walk grapheme
> clusters … Widths come from `github.com/rivo/uniseg`, which is the same
> engine tcell's `CellBuffer.Put` uses, so our column math and tcell's
> cell buffer agree by construction."

This plan gives the chrome the same guarantee: one shared, cluster-aware
clip/draw helper, five call sites converted, and the unclipped title fixed.

## Current state

The five helpers (verified at `2616761`; all one-cell-per-rune):

1. `internal/overlay/chrome.go:81` — `drawText(scr, x, y, s, st)`:
   ```go
   // drawText writes s left-to-right starting at (x, y), one cell per rune.
   func drawText(scr tcell.Screen, x, y int, s string, st tcell.Style) {
       col := 0
       for _, r := range s {
           scr.SetContent(x+col, y, r, nil, st)
           col++
       }
   }
   ```
   **No clipping at all**, and `DrawFrame` (`chrome.go:29`) paints every
   overlay's TITLE with it: `drawText(scr, r.X+1, r.Y+1, " "+title,
   titleStyle)` — a title longer than the frame writes past the `┐` border.
   `runeLen` (`chrome.go:89-96`) is documented as "the visible cell count of
   s (one cell per rune)" — the false premise, used for right-aligning the
   `esc ` hint (`chrome.go:31`) and pick tags (`pick.go:399`). `trimRunes`
   (`chrome.go:101-113`) truncates by rune count with an `…`.
2. `internal/overlay/pick.go:404` — `drawClippedText(scr, x, y, maxW, s, st)`:
   per-rune loop, `if col >= maxW { break }`.
3. `internal/app/leaderstrip.go:104` — `drawStripSegment(scr, x, y, maxW, s,
   st) int`: same, clips against an absolute column, returns next x.
4. `internal/app/gitchanges.go:1078` — `drawClipped(scr, x, y, maxW, s, st)
   int`: `runes := []rune(s); n := min(len,maxW)`; returns `x + n`.
5. `internal/filetree/filetree.go:1335` — `drawString(scr, x, y, w, s, st)`:
   per-rune loop, `if col >= w { return }`. Paints tree rows
   (`drawNodeRow`), whose right edge carries the git status letter — the
   alignment casualty for wide names.

The correct machinery: `internal/editor/cluster.go` — `runeCellWidth`
(`:95-103`, uniseg-backed) and `ClusterAt` (`:112`, measures the cluster
starting at a rune index; "end always advances (end > i), so a walk over a
line can never stall"). `uniseg` is already a DIRECT dependency (`go.mod`).

Import graph (verified — this determines placement):
- `internal/overlay` imports only `theme`, `scrollbar`, tcell.
- `internal/editor` imports `theme`, `scrollbar`, `icons`, chroma, uniseg —
  and does NOT import `overlay`, so `overlay → editor` would be cycle-free
  BUT drags chroma into the overlay layer and violates the layering feel.
- Precedent to follow instead: `internal/scrollbar` — CLAUDE.md: "The one
  definition of a scrollbar … both the editor's bar and the tree's import it
  so they cannot drift." Create the text equivalent: a small leaf package.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Scoped tests | `go test ./internal/textdraw/ ./internal/overlay/ ./internal/filetree/ ./internal/app/` | ok |
| Full suite | `make test` | exit 0 |
| Lint | `make lint` | exit 0 |

## Scope

**In scope**:
- `internal/textdraw/textdraw.go`, `internal/textdraw/textdraw_test.go` (create)
- `internal/overlay/chrome.go` (delegate `drawText`/`runeLen`/`trimRunes`; clip the title)
- `internal/overlay/pick.go` (replace `drawClippedText` body or call sites)
- `internal/app/leaderstrip.go`, `internal/app/gitchanges.go`,
  `internal/filetree/filetree.go` (replace helper bodies with delegation)
- The paired `_test.go` files of each edited source file (new width cases;
  corrected assertions where a wide-glyph coordinate was previously wrong)

**Out of scope** (do NOT touch):
- `internal/editor/cluster.go` and every editor render path — already
  correct; this plan must not "unify" the editor onto the new package.
- `internal/editor/tab.go`, `wrap.go` — the caret/hit-test agreement there
  is protected by its own design (CLAUDE.md "Three units") and tests.
- Search/find highlighting, scrollbars, any behavior beyond text
  measurement + clipping in chrome drawing.

## Git workflow

- Branch: `advisor/020-width-aware-draw`
- Commit per step, imperative mood, no Claude trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Create `internal/textdraw` (TDD)

New leaf package importing only `tcell` + `uniseg` (NO theme, NO editor —
mirror `internal/scrollbar`'s dependency discipline and say so in the package
doc). File header block per repo convention (2026). API:

```go
// Width returns the terminal cell count of s, walking grapheme clusters
// with uniseg — the same engine tcell paints with, so measurement and
// the cell buffer agree by construction (see CLAUDE.md "Three units").
func Width(s string) int

// Clip returns the longest prefix of whole clusters fitting maxW cells,
// plus that prefix's width. A cluster is never split.
func Clip(s string, maxW int) (string, int)

// ClipEllipsis is Clip with a trailing … (1 cell) when anything was cut.
func ClipEllipsis(s string, maxW int) string

// DrawClipped paints s at (x, y) clipped to maxW cells and returns the
// x just past the last cell painted. Wide clusters are emitted as one
// SetContent call — primary rune plus the cluster's remaining runes as
// combining content — then their width is skipped, which is how tcell
// expects wide/combined glyphs to be laid down.
func DrawClipped(scr tcell.Screen, x, y, maxW int, s string, st tcell.Style) int
```

Implement `Width`/`Clip` with `uniseg.FirstGraphemeClusterInString`
iteration. In `DrawClipped`, for each cluster call
`scr.SetContent(x, y, firstRune, restRunes, st)` then advance x by the
cluster's width; stop before a cluster that would cross `maxW`. Write the
tests FIRST (`textdraw_test.go`, `tcell.NewSimulationScreen("UTF-8")` +
`GetContents()` per repo convention) and watch them fail against stub
implementations:
- ASCII: width == len, clip at boundary, ellipsis.
- CJK: `"日本語"` → Width 6; Clip to 5 yields `"日本"` (width 4 — never
  splits a 2-cell cluster); DrawClipped skips cells correctly (assert the
  cell AFTER a wide glyph is untouched/continuation).
- Combining mark: `"é"` → Width 1.
- ZWJ family emoji → Width 2, one cluster.
- `maxW <= 0` → no-ops.

**Verify**: `go test ./internal/textdraw/ -v` → all new tests pass;
`make lint` → exit 0.

### Step 2: Convert `internal/overlay` and clip the title

- `runeLen` body → `return textdraw.Width(s)`; fix its doc comment (it no
  longer means "one cell per rune").
- `trimRunes` body → `return textdraw.ClipEllipsis(s, max)` (keep the
  function and its callers; doc comment updated).
- `drawText` body → `textdraw.DrawClipped(scr, x, y, <unbounded>, s, st)`
  is NOT acceptable — drawText's missing clip is the bug. Give it a width
  parameter is too invasive; instead in `DrawFrame` (`chrome.go:29`) clip
  the title explicitly: the title's budget is
  `r.W - 2 - textdraw.Width(" esc ") ` cells from `r.X+1`; draw
  `textdraw.DrawClipped(scr, r.X+1, r.Y+1, budget, " "+ClipEllipsis-treated
  title, titleStyle)`. Convert `drawText`'s body to cluster-aware emission
  via `textdraw.DrawClipped` with the remaining screen width... — simpler
  and safer: change `drawText`'s signature to take `maxW` and update its
  ~call sites within `internal/overlay` (enumerate them with
  `grep -n "drawText(" internal/overlay/*.go` first; they are all
  package-internal). Every call site must pass an honest budget derived
  from its frame rect, not a sentinel.
- `drawClippedText` (`pick.go:404`) body → delegate to
  `textdraw.DrawClipped`. The tag right-align at `pick.go:399` already uses
  `runeLen`, which is now width-correct for free.

**Verify**: `go test ./internal/overlay/` → pass (update any assertion whose
expected coordinates were wrong ONLY after re-deriving the correct position
by hand — see the rule in "Test plan"). Add
`TestDrawFrame_LongTitleClipsInsideBorder`: 30-wide frame, 60-cell title →
the `┐` corner cell still holds `┐` and row r.Y+1 ends with the `esc ` hint.

### Step 3: Convert the app + filetree helpers

Delegate bodies, keeping each package-local name and signature:
- `drawStripSegment` (`leaderstrip.go:104`): body becomes
  `return textdraw.DrawClipped(scr, x, y, maxW-x, s, st)` — note its `maxW`
  is an absolute column, so convert (`maxW-x` cells remain); keep returning
  the new x.
- `drawClipped` (`gitchanges.go:1078`): body → `textdraw.DrawClipped` (its
  `maxW` is already a cell budget; returns next x).
- `drawString` (`filetree.go:1335`): body → `textdraw.DrawClipped(scr, x,
  y, w, s, st)` discarding the return.
Then check the two right-edge compositions that DEPEND on width math:
`drawChangeLetter` (`filetree.go` — paints the git letter at `x+w-2`; it is
position-based and stays correct) and `drawNodeRow`'s icon path, which
advances by `len([]rune(prefix))` / `len([]rune(glyph))` — replace those two
advances with `textdraw.Width(prefix)` / `textdraw.Width(glyph)` so a wide
glyph or CJK chain label doesn't overdraw the name.

**Verify**: `go test ./internal/app/ ./internal/filetree/` → pass.

### Step 4: New wide-glyph regression tests

- `filetree_test.go`: `TestRender_CJKFilenameKeepsStatusLetterAligned` — a
  dirty file named `日本語ファイル.go` in a 30-wide tree: the status letter
  `M` still renders at column `w-2`, and no glyph paints past the sidebar
  width (model on `TestRender_DirtyRowsShowStatusLetter` and the clipping
  test `TestRender_EmptyRootClipsToSidebarWidth`).
- `gitchanges_test.go`: a CJK path row clips inside the panel width.
- `leaderstrip` (its test file): a segment with an emoji advances x by 2,
  not by rune count.

**Verify**: `make test` → exit 0; `make lint` → exit 0.

## Test plan

TDD throughout: every new test watched failing first. Structural exemplars:
`internal/filetree/filetree_test.go` (`renderAndCollect`/`rowText`),
`internal/overlay`'s existing SimulationScreen tests. **Rule for touched
assertions**: an existing assertion may be updated ONLY when you can state,
in the commit message, the hand-derived correct coordinate that replaces the
old wrong one (e.g. "glyph is 2 cells, so the name starts at x+…+2, not
+1"). Never adjust an expected value just to match observed output — with
ASCII-only fixtures, almost every existing assertion should pass unchanged;
widespread failures mean a helper conversion is wrong.

## Done criteria

- [ ] `internal/textdraw` exists, imports only tcell + uniseg (`grep -n "skiff/internal" internal/textdraw/textdraw.go` → no matches)
- [ ] All five helpers delegate: `grep -n "for _, r := range s" internal/overlay/chrome.go internal/overlay/pick.go internal/app/leaderstrip.go internal/app/gitchanges.go internal/filetree/filetree.go` → no clip-loop matches remain
- [ ] `TestDrawFrame_LongTitleClipsInsideBorder` and the CJK tree test exist and pass
- [ ] `make test` and `make lint` exit 0
- [ ] `git status` shows only in-scope files modified
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- The excerpts above don't match the live code (drift).
- Converting a helper makes MORE than ~5 existing tests fail in one package
  — that signals a semantic mistake (absolute-column vs budget confusion in
  Step 3's `maxW-x` conversion is the likely culprit), not stale assertions.
- You find an editor-package call site that seems to need the new helper —
  the editor is out of scope by design; report it instead.
- `SetContent`'s combining-rune emission renders differently on the
  SimulationScreen than expected (assert-first in Step 1 protects this;
  if the simulation disagrees with the design, report what it does).

## Maintenance notes

- `internal/textdraw` is now the chrome's single width authority, exactly as
  `internal/scrollbar` is for scrollbars — future drawing helpers must build
  on it, and a tcell/uniseg upgrade moves all chrome at once.
- The editor deliberately keeps its own richer cluster machinery
  (`cluster.go`) — do not unify them; the editor needs per-cluster caret
  semantics the chrome doesn't.
- Deferred: `overlay/info.go`, `diffview.go` and other draw paths that use
  `sliceRunes`/per-rune loops for BODY content still assume 1 cell/rune in
  places; this plan fixed the shared helpers and the title. A follow-up may
  sweep body-content call sites onto `textdraw` once this lands cleanly.
- Reviewer focus: the `maxW-x` vs cell-budget conversions in Step 3, and
  that `DrawFrame`'s title budget accounts for both border columns.
