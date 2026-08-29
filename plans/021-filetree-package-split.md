# Plan 021: Split internal/filetree into files that match its responsibilities

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/filetree/`
> Function line numbers below WILL have drifted if any earlier plan touched
> this package — that is expected and fine (this plan moves whole
> declarations, matched by NAME, not by line). Treat drift as a STOP only if
> a named function no longer exists or a NEW top-level declaration has
> appeared that this plan's mapping does not cover — in that case add it to
> the correct destination by the section rules below and note it in your
> report.

## Status

- **Priority**: P3
- **Effort**: L (mechanical, but large)
- **Risk**: LOW — same package, no API change, no logic edits; the compiler
  and the full suite catch any slip immediately
- **Depends on**: plans/011-*.md and plans/020-*.md (and any other plan that
  edits `internal/filetree/` — check `plans/README.md`; run this split only
  when no earlier plan still has filetree edits pending, so their diffs stay
  reviewable against the file layout they were written for)
- **Category**: tech-debt
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`internal/filetree/filetree.go` is 1,619 lines / ~51 top-level functions in a
single-file package — ~6× the repo's non-test median — and its paired
`filetree_test.go` is 2,769 lines, the largest file in the repo. Six distinct
responsibilities live in one file (model+merge, background scan, gitignore
chain, flatten/compaction, rendering, scrollbar, navigation). The package is
also among the highest-churn in the repo, so the cost of "everything in one
file" recurs on every feature. Contrast: `internal/editor` is 15 files,
`internal/overlay` 12, `internal/app` 38. This plan is a pure re-shelving:
move declarations, change no bodies.

## Current state

- `internal/filetree/filetree.go` — everything (1,619 lines at planning).
- `internal/filetree/filetree_test.go` — everything's tests (2,769 lines).
- Convention (CLAUDE.md "Tests — required, not optional"): one `_test.go` per
  source file, in the same package; don't split one source file's tests
  across multiple test files. A split package therefore splits its test file
  alongside. Because everything stays in package `filetree`, test placement
  is purely organizational — any assignment compiles; the mapping below is
  the reviewable contract, not a compile constraint.
- Convention (CLAUDE.md "File headers"): every NEW source file gets the
  header block (file name, author, created date, copyright 2026). Copy the
  shape from the top of `filetree.go`, updating the `File:` line; keep
  `Author: John Lam <johnlam90@gmail.com>` and `Created: <today>`.
- CLAUDE.md's architecture map lists `internal/filetree/filetree.go` with a
  long description — update that entry to name the new files (keep the
  described behaviors; they don't change).

Top-of-file spot excerpt to confirm you're in the right file
(`filetree.go:52`):

```go
type Node struct {
	Path     string
	Name     string
	IsDir    bool
```

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite (race) | `make test` | exit 0, all `ok` |
| Lint gates | `make lint` | exit 0 |
| Package only (fast loop) | `go test ./internal/filetree/` | ok |
| Size check | `wc -l internal/filetree/*.go` | every non-test file < 700 |
| No body edits | `git diff --ignore-all-space <base>..HEAD -- internal/filetree/ \| grep -c "^[+-]func "` | equals moved-declaration count ×2 (pure add/remove pairs) |

## Scope

**In scope**:
- `internal/filetree/` — new files `scan.go`, `ignore.go`, `flatten.go`,
  `render.go`, `scrollbar.go`, `nav.go` (+ their `_test.go` partners and a
  shared `helpers_test.go`), shrinking `filetree.go`/`filetree_test.go`.
- `CLAUDE.md` — the one architecture-map entry for filetree.

**Out of scope** (do NOT touch):
- Any function BODY, signature, type shape, or doc-comment content (moving a
  declaration keeps its doc comment attached, verbatim).
- Any file outside `internal/filetree/` except the CLAUDE.md map entry.
- Exported API — nothing is renamed, nothing changes visibility.

## Git workflow

- Branch: `advisor/021-filetree-package-split`
- One commit per destination file ("Move scan pipeline into
  internal/filetree/scan.go", …), then one commit for the test split, then
  one for the CLAUDE.md map entry — 8-9 small commits, each leaving the
  suite green. No Claude trailers.
- Do NOT push or open a PR unless the operator instructed it.

## The mapping — source declarations

Move each declaration (with its doc comment) to its destination. Line numbers
are from commit `2616761` and are locators, not anchors.

**stays in `filetree.go`** (model: types, construction, identity-preserving
merge, symlink resolution, synchronous refresh):
`Node` (52), `TrashPrefix` (103), `gitignoreName` (131) → moves to ignore.go,
`MaxDirChildren` (147), `GitChangeKind` alias + kind consts (156-168),
`Tree` (170), `ignoreEntry` (247) → moves to ignore.go, `New` (260),
`SetOpenFiles` (291), `pin` (309), `loadChildren` (319), `reload` (340),
`merge` (469), `resolvedPath` (530), `(*Node).loops` (547), `Refresh` (730),
`refreshNode` (740), `childByName` (1609).

**`scan.go`** (the off-thread disk sweep + on-loop apply):
`ScanEntry` (358), `DirScan` (376), `readDir` (394), `scanEntries` (422),
`shouldHide` (834), `LoadedDirs` (755), `collectLoadedDirs` (767),
`ScanDirs` (784), `ApplyScan` (803), `applyScanNode` (814).

**`ignore.go`** (.gitignore chain + caching + pinned exemptions):
`gitignoreName` (131), `ignoreEntry` (247), `cacheIgnore` (573),
`ignoreLevel` (594), `ignoreChain` (617), `filterIgnored` (644),
`ignoredByChain` (675), `pinnedNames` (697).

**`flatten.go`** (visible-row list + compact folder chains):
`flatNode` (855), `flattenInto` (876), `compactChild` (909),
`(flatNode).containsPath` (924), `(*Tree).flatten` (945).

**`render.go`** (painting + row styling + display labels):
`EmptyFolderLabel` (109), `UnreadableLabel` (115), `SymlinkLabel` (121),
`LoopLabel` (126), `moreRowFormat` (151), `listHeaderRows` (957),
`listArea` (962), `Render` (988), `changeKind` (1145), `drawNodeRow` (1169),
`drawChangeLetter` (1287), `gitChangeLetter` (1300), `gitChangeColor` (1317),
`drawString` (1335).

**`scrollbar.go`** (the sidebar's bar column):
`minScrollbarWidth` (976), `scrollbarVisible` (1078),
`ScrollbarVisible` (1089), `ScrollbarHit` (1099), `ScrollToBarRow` (1111),
`renderScrollbar` (1124).

**`nav.go`** (hit-testing, toggling, scrolling, reveal, expanded-dir
persistence):
`clampScroll` (1347), `HitTest` (1373), `Toggle` (1396),
`maxChainProbe` (1412), `expandChain` (1419), `Scroll` (1434),
`Reveal` (1463), `flatIndexOf` (1527), `ExpandedDirs` (1540),
`ExpandDirs` (1564), `descend` (1593).

Judgment rule for anything unlisted (drift): a declaration goes with the
subsystem whose functions call it most; when tied, leave it in `filetree.go`
and say so in your report.

## The mapping — tests

First create `helpers_test.go` (with a file header) holding the shared
fixtures/assert helpers used across areas: `mkTree` (35), `mustMkdir` (50),
`mustWrite` (58), `findChild` (67), `renderAndCollect` (531), `rowText` (547),
`findRowY` (851), `rowHasColor` (863), `rowHasBold` (878),
`containsRune` (890), `findRowWithBoth` (905), `mkNested` (1147),
`mkUnreadable` (1532), `mkFlatTree` (1835), `mkIgnoreTree` (2084),
`mustSymlink` (2099), `mkChain` (2516).

Then move each Test func to the `_test.go` partner of the file its subject
landed in:

**`filetree_test.go`** (model/merge/symlinks/cap): TestNew_NonExistentRoot,
TestNew_RootIsFile, TestNew_LoadsAndHides, TestLoadChildren_SortOrder,
TestRefresh_PreservesExpandedState, TestReload_HidesTrashEntries,
TestSymlinkDir_ExpandsThroughTheLink, TestSymlinkLoop_TerminatesInsteadOfHanging,
TestSymlinkLoop_CatchesMutualLinks, TestMaxDirChildren_SentinelRow,
TestMaxDirChildren_SentinelSurvivesRefresh, TestMaxDirChildren_MergeStaysBounded,
TestUnreadableMarkClearsOnRefresh.

**`scan_test.go`**: TestLoadedDirs_OnlyLoadedDirectories,
TestScanDirsApplyScan_MatchesRefresh,
TestApplyScan_LeavesUnscannedAndFailedDirsAlone, TestShouldHide,
TestApplyScanMarksUnreadable, TestGitignore_BackgroundScanFiltersLikeRefresh.

**`ignore_test.go`**: TestGitignore_HidesIgnoredEntriesUntilToggledOff,
TestGitignore_DotfilesStayVisibleInBothStates,
TestGitignore_NestedFileAppliesToItsSubtreeOnly,
TestGitignore_MatcherCacheFollowsTheChildren, TestGitignore_OpenTabNeverVanishes.

**`flatten_test.go`**: TestFlattenInto_Collapsed, TestFlattenInto_Expanded,
TestFlattenInto_NilSafe, TestFlattenInto_CompactsSingleDirChain,
TestFlattenInto_ChainStopsAtUnloadedDir, TestFlattenInto_SiblingBreaksChain,
TestFlattenInto_FileChildDoesNotCompact, TestFlattenInto_LinksNeverJoinChains.

**`render_test.go`**: every `TestRender_*` (ProjectNameAndChevrons,
EmptyRootShowsPlaceholder, EmptyRootClipsToSidebarWidth,
NonEmptyRootHasNoPlaceholder, ActiveFolderIsBold, ActiveFileIsBold,
TinyHeightDoesNotPanic, DirtyFileUsesModifiedColor,
DirtyFolderUsesModifiedColor, DirtyRootUsesModifiedColor,
DirtyAndActiveStaysBold, IconsDisabledByDefault, IconsEnabledShowsFolderGlyph,
IconsEnabledShowsFileGlyph, DotFileRendersMuted, DirtyOverridesDotMute,
IconsEnabledColoursGlyphPerLanguage, IconsEnabledFolderOpenSwitches,
SymlinkRowsAreMarked, DirtyRowsShowStatusLetter, CompactChainShowsJoinedPath,
ActiveFolderInsideChainHighlights, DotDirChainRendersMuted) plus
TestUnreadableDirIsMarkedNotEmpty and TestUnreadableRootLabel (they assert
rendered labels).

**`scrollbar_test.go`**: TestTreeScrollbar_HiddenWhenListingFits,
TestTreeScrollbar_ThumbTracksScroll, TestTreeScrollbar_ReservesTheLabelColumn,
TestTreeScrollbar_HitTestSeparatesColumns, TestTreeScrollbar_ClickToJump,
TestTreeScrollbar_WidthFloor, TestTreeScrollbar_ThumbBrightensWhileDragging.

**`nav_test.go`**: TestScroll_ClampsAtZero, TestClampScroll_AllCases,
TestHitTest_ExplorerHeaderMisses, TestHitTest_ProjectRootRowReturnsRoot,
TestHitTest_ValidRow, TestHitTest_OutOfRange, TestToggle_LoadsThenFlips,
TestToggle_FileIsNoop, TestToggle_ExpandsChainToDeepest,
TestReveal_ExpandsAncestorsAndScrolls, TestReveal_NoScrollWhenAlreadyVisible,
TestReveal_ScrollsWhenTargetBelowViewport, TestReveal_DirectChildOfRoot,
TestReveal_ViewHZeroExpandsButDoesNotScroll, TestReveal_HiddenDirIsNoop,
TestReveal_OutsideRootIsNoop, TestReveal_RootItselfIsNoop,
TestFlatIndexOf_MatchesRenderOrder, TestFlatIndexOf_FindsDirFoldedIntoChain,
TestReveal_FileInsideCompactChain, TestExpandedDirsRoundTrip,
TestExpandDirsUnknownSkipped.

Any Test func present in the live file but absent above (drift): file it with
its subject's destination and note it.

## Steps

### Step 1: Record the base and the invariant

`BASE=$(git rev-parse HEAD)` — every later "no body edits" check diffs
against this. Run `make test` once to confirm green before touching anything.

**Verify**: `make test` → exit 0.

### Step 2-7: One destination file per commit

For each of `scan.go`, `ignore.go`, `flatten.go`, `render.go`,
`scrollbar.go`, `nav.go` (in that order):

1. Create the file with the standard header block + `package filetree` + the
   imports its declarations need (run `goimports` mentally or let the
   compiler list them).
2. Cut the mapped declarations (doc comments included, verbatim) from
   `filetree.go` and paste them unmodified.
3. `go test ./internal/filetree/` → ok. Commit.

**Verify (each)**: `go test ./internal/filetree/` → ok; `gofmt -l internal/filetree/` → empty.

### Step 8: Split the test file

Create `helpers_test.go` + the six `_test.go` partners per the mapping (each
with a file header; the package doc comment describing the test suite stays
at the top of `filetree_test.go`). One commit.

**Verify**: `go test ./internal/filetree/` → ok; every `*_test.go` corresponds to a source file or is `helpers_test.go`.

### Step 9: Update the CLAUDE.md map entry

Replace the single `internal/filetree/filetree.go` entry in CLAUDE.md's
architecture map with entries for the new files, redistributing the existing
description's clauses (child cap → filetree.go, `.gitignore`-aware filtering →
ignore.go, sidebar scrollbar → scrollbar.go, compact chains → flatten.go,
etc.). Keep the map's terse one-line-per-file style.

**Verify**: `grep -n "filetree" CLAUDE.md` shows the new file names.

### Step 10: Final gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.
`wc -l internal/filetree/*.go | awk '$2 != "total" && $1 >= 700 && $2 !~ /_test/ {print; exit 1}'` → no output.
No-body-edits spot check: `git diff $BASE..HEAD -- internal/filetree/ | grep "^[+-]" | grep -v "^[+-][+-]" | grep -v "^[+-]$" | sort | uniq -c | sort -rn | head` — every content line should appear once as `-` and once as `+` (moved), except file headers and `package`/`import` lines. For one non-trivial function (e.g. `merge`), run `git log -L :merge:internal/filetree/filetree.go $BASE` vs the new location and confirm the body is byte-identical.

## Test plan

No new tests — this plan's contract is that the existing 100+ tests pass
unchanged at every commit. The per-step `go test ./internal/filetree/` runs
are the test plan.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `internal/filetree/` contains `filetree.go, scan.go, ignore.go, flatten.go, render.go, scrollbar.go, nav.go` + matching `_test.go` files + `helpers_test.go`
- [ ] every non-test file < 700 lines (`wc -l`)
- [ ] `make test` and `make lint` exit 0
- [ ] no function body changed (Step 10's checks)
- [ ] CLAUDE.md's map names the new files
- [ ] `git status --short` shows nothing outside `internal/filetree/` + CLAUDE.md
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- A mapped function name no longer exists AND you cannot identify its renamed
  successor from `git log` — report the gap.
- Moving a declaration requires ANY edit to its body or signature to compile
  (it shouldn't — same package): report what forced it.
- `plans/README.md` shows an earlier filetree-touching plan still TODO/IN
  PROGRESS — run this split only after those land.

## Maintenance notes

- Future filetree features should land in the responsibility file they belong
  to; the 700-line ceiling in Done criteria is the tripwire for the next
  split.
- The test mapping is organizational, not enforced by the compiler — a
  reviewer should reject new tests added to the wrong partner file, or the
  split decays.
- `helpers_test.go` deliberately bends "one test file per source file" for
  shared fixtures; CLAUDE.md's rule is about not splitting ONE source file's
  tests across files, which still holds.
