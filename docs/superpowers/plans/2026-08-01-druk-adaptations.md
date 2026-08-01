# Druk Adaptations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Port the druk features that fit skiff's philosophy: go-to-line (+ `file:42` CLI), move/duplicate line, reopen closed tab, leader cheat-strip, preview tabs, tree cut/copy/paste/duplicate, editor scrollbar with git change-map, project-wide content search, and per-project session restore.

**Architecture:** Everything follows existing skiff patterns: actions live in the ≡ menu (`builtinMenuGroups`) plus Esc-leader bindings (`leaderBindings`), text-input UIs reuse the prompt modal or the find-bar pattern, async work posts custom tcell events, and per-tab state lives on `editor.Tab`. Two new packages: `internal/search` (project-wide content search engine) and `internal/session` (per-project state persistence).

**Tech Stack:** Go, tcell v2, no new dependencies. Tests use `t.TempDir()` and `tcell.NewSimulationScreen("UTF-8")` per CLAUDE.md.

## Global Constraints

- No `Ctrl+` shortcuts, ever. Leader keys (Esc + rune) and menu entries only.
- Every file action must be reachable from the main ≡ menu (tree right-click is a redundant shortcut).
- Every new/modified source file keeps the standard header block; copyright year 2026.
- Doc comment above every function, public and private.
- One `_test.go` per source file, same package, each `Test*` has a doc comment.
- No CGO, no config system, no plugins.
- Free leader runes chosen: `l` (go to line), `j`/`k` (move line down/up), `d` (duplicate line), `o` (reopen closed tab), `F` (find in project). Taken already: s u r w q n t / f p g.
- `make test` must pass after every task.
- Do NOT commit or push — leave changes in the working tree (release workflow triggers on push to main; user will review and commit).

---

### Task 1: Go to line — Tab.JumpToLine + prompt modal + `skiff file.go:42` CLI

**Files:**
- Modify: `internal/editor/tab.go` (add JumpToLine, CenterOnCursor)
- Modify: `internal/editor/tab_test.go`
- Modify: `main.go` (`resolveArgs` parses `:N` suffix; `cliResult` gains `OpenLine int`)
- Modify: `main_test.go`
- Create: `internal/app/gotoline.go` + `internal/app/gotoline_test.go`
- Modify: `internal/app/app.go` (menu entry in Search group; `NewSingleFile`/`New` accept the initial line — add `OpenFileAtLine(path string, line int)` used from `main.go` after construction instead of changing signatures)
- Modify: `internal/app/leader.go` (bind `l`)

**Interfaces:**
- Produces: `(t *Tab) JumpToLine(line int)` — 1-based line, clamps, cursor to col 0, sets cursorMoved.
- Produces: `(t *Tab) CenterOnCursor(viewH int)` — sets ScrollY so cursor line sits at viewH/2, clamped ≥ 0.
- Produces: `(a *App) OpenFileAtLine(path string, line int)` — openFile then jump+center (line ≤ 0 = plain open).
- Produces: `(a *App) menuGoToLine()` — opens prompt "Go to line" → parse int → jump. Non-numeric input flashes.
- Produces: `cliResult.OpenLine int`; `resolveArgs("main.go:42")` → OpenFile "main.go", OpenLine 42 when `main.go` exists and `main.go:42` does not. Missing-prefix falls back to current create-file behavior (raw name). A `:0`/`:badnum` suffix is not a line spec.

**Steps:**
- [x] Tests: `TestJumpToLineClamps`, `TestCenterOnCursor` (tab_test.go); `TestResolveArgsFileLine`, `TestResolveArgsFileLineMissingPrefix` (main_test.go); `TestMenuGoToLineJumps`, `TestGoToLineRejectsGarbage` (gotoline_test.go). Run, watch fail.
- [x] Implement Tab methods, resolveArgs suffix parse, gotoline.go (prompt via existing `openPrompt`), menu row `{label: "Go to line", shortcut: "Esc l", action: (*App).menuGoToLine, enabled: (*App).hasFindable}` in Search group, leader `l`, main.go plumbs OpenLine via `a.OpenFileAtLine`.
- [x] `make test` green.

### Task 2: Move line up/down + duplicate — editor ops + menu + leader j/k/d

**Files:**
- Create: `internal/editor/lineops.go` + `internal/editor/lineops_test.go`
- Modify: `internal/app/app.go` (new "Line" menu group after Clipboard group; `menuMoveLineUp/Down`, `menuDuplicateLine`, predicate `hasEditableTab`)
- Modify: `internal/app/leader.go` (bind j/k/d)
- Modify: `internal/app/leader_test.go` if the table test enumerates counts

**Interfaces:**
- Produces: `(t *Tab) MoveLinesUp()`, `(t *Tab) MoveLinesDown()` — move the selection's line span (or cursor line) one line; no-op at buffer edge; pushUndo(undoGroupStructural); Dirty, StyleStale, cursorMoved; cursor+anchor travel with the block.
- Produces: `(t *Tab) DuplicateLines()` — insert a copy of the span below itself; cursor lands on the copy.
- All three no-op on image tabs.

**Steps:**
- [x] Tests in lineops_test.go: `TestMoveLinesUpSingle`, `TestMoveLinesDownAtBottomNoop`, `TestMoveLinesSelectionSpan`, `TestDuplicateLinesCursorOnCopy`, `TestLineOpsUndoOneStep`. Run, fail.
- [x] Implement lineops.go; wire menu group {"Move line up" Esc k, "Move line down" Esc j, "Duplicate line" Esc d} + leader binds.
- [x] `make test` green.

### Task 3: Reopen closed tab

**Files:**
- Create: `internal/app/reopen.go` + `internal/app/reopen_test.go`
- Modify: `internal/app/app.go` (App fields `closedTabs []closedTabRecord`; push in `closeTab`; menu row in Tab actions group)
- Modify: `internal/app/leader.go` (bind `o`)

**Interfaces:**
- Produces: `closedTabRecord{Path string; Cursor editor.Position; ScrollY int}`; cap stack at 20.
- Produces: `(a *App) menuReopenTab()` — pop; if file missing, flash and drop; else open + restore cursor/scroll. `(a *App) hasClosedTab() bool` predicate.
- closeTab pushes only when `tab.Path != ""`.

**Steps:**
- [x] Tests: `TestReopenRestoresCursor`, `TestReopenSkipsDeletedFile`, `TestReopenStackCap`, `TestCloseUntitledNotRecorded`. Run, fail.
- [x] Implement; menu row `{label: "Reopen closed tab", shortcut: "Esc o", action: (*App).menuReopenTab, enabled: (*App).hasClosedTab}`.
- [x] `make test` green.

### Task 4: Leader cheat-strip

**Files:**
- Modify: `internal/app/leader.go` (add `desc string` to leaderBinding; fill all entries)
- Modify: `internal/app/leader_test.go`
- Create: `internal/app/leaderstrip.go` + `internal/app/leaderstrip_test.go`
- Modify: `internal/app/app.go` (draw() calls drawLeaderStrip; editorRect unchanged — strip overlays the row above the status bar like the find bar)

**Interfaces:**
- Produces: `(a *App) leaderStripVisible() bool` — true while `lastEscape` is armed (within doubleEscMs) and no modal/menu is open.
- Produces: `(a *App) drawLeaderStrip()` — one row above status bar: ` Esc  s save · u undo · … ` built from leaderBindings() descs, clipped to width. The existing leaderExpiryEvent redraw clears it.

**Steps:**
- [x] Tests: `TestLeaderStripListsEveryBinding` (render on sim screen, assert a couple of descs present), `TestLeaderStripHiddenWhenNotArmed`. Run, fail.
- [x] Implement desc field + strip drawing.
- [x] `make test` green.

### Task 5: Preview tabs (single-click preview, pin on edit/second click)

**Files:**
- Modify: `internal/editor/tab.go` (field `Preview bool`; method `IsPreview() bool` = Preview && !Dirty; `Pin()`)
- Modify: `internal/editor/tab_test.go`
- Create: `internal/app/preview.go` + `internal/app/preview_test.go`
- Modify: `internal/app/app.go` (`sidebarClick` file branch → `openFilePreview`; `drawTabBar` italicizes preview labels; factor shared open logic)

**Interfaces:**
- Produces: `(a *App) openFilePreview(path string)`:
  - already open + active + IsPreview → `Pin()` (second click pins)
  - already open otherwise → activate (same as openFile)
  - an IsPreview tab exists → replace it in place (same slice slot, keeps tab order), new tab has Preview=true
  - else append new tab with Preview=true
- openFile (finder / menu / CLI path) stays permanent AND pins an existing preview tab it lands on.
- Dirty edit auto-pins via IsPreview definition; `Pin()` clears the flag so italics drop.

**Steps:**
- [x] Tests: `TestPreviewReplacedByNextPreview`, `TestSecondClickPins`, `TestEditPinsPreview`, `TestOpenFilePinsExistingPreview`, `TestPreviewReplaceKeepsSlot`. Run, fail.
- [x] Implement; italic style in drawTabBar via `style.Italic(true)` when `t.IsPreview()`.
- [x] `make test` green.

### Task 6: Tree cut / copy / paste / duplicate

**Files:**
- Create: `internal/app/fileclip.go` + `internal/app/fileclip_test.go`
- Modify: `internal/app/fileops.go` (helpers `copyTree(src, dst)`, `uniqueDestPath(dir, base) string`)
- Modify: `internal/app/fileops_test.go`
- Modify: `internal/app/modals.go` (context menu rows: Cut, Copy, Paste here, Duplicate)
- Modify: `internal/app/app.go` (App fields `fileClipPath string; fileClipCut bool`; main-menu rows in File actions group)

**Interfaces:**
- Produces: `uniqueDestPath("/d", "app.ts")` → first free of `app.ts`, `app copy.ts`, `app copy 2.ts`… (suffix before extension; dirs get plain ` copy`).
- Produces: `(a *App) clipCutPath(p string)`, `clipCopyPath(p string)`, `(a *App) pasteInto(dir string)` — cut→move (os.Rename + copy/delete fallback), copy→recursive copy; never overwrites (uniqueDestPath); cut clears the clip, copy keeps it; refuses pasting a dir into itself/descendant; open tabs follow moves (exact path + children, like doRenameFolder); refreshTree + invalidateFinder + git refresh after.
- Produces: `(a *App) duplicatePath(p string)` = copy+paste beside the source.
- Context menu: paste target = node itself when dir, else its parent. Main menu rows: "Cut file", "Copy file" (active tab), "Paste into folder" (activeFolder, labelled via relativeFolderLabel), "Duplicate file"; predicates `hasFileTab` / `hasFileClip`.

**Steps:**
- [x] Tests: `TestUniqueDestPathSuffixes`, `TestPasteCopyKeepsClip`, `TestPasteCutMovesAndClearsClip`, `TestPasteDirIntoItselfRefused`, `TestPasteOntoFileLandsBeside`, `TestMoveUpdatesOpenTabs`, `TestDuplicateInPlace`. Run, fail.
- [x] Implement helpers + wiring (context + main menu).
- [x] `make test` green.

### Task 7: Editor scrollbar with git change-map

**Files:**
- Create: `internal/editor/scrollbar.go` + `internal/editor/scrollbar_test.go`
- Modify: `internal/editor/tab.go` (Render reserves last column when needed and draws the bar)
- Modify: `internal/app/app.go` (mouse: press/drag on the bar column scrolls; new dragMode "scrollbar")

**Interfaces:**
- Produces: `scrollbarGeom(total, viewH, scrollY int) (thumbStart, thumbLen int, ok bool)` — pure; ok=false when total ≤ viewH (no bar).
- Produces: `scrollTargetForThumb(total, viewH, clickY int) int` — maps a bar-local y to a ScrollY (thumb centered on grab point), clamped.
- Produces: `(t *Tab) ScrollbarVisible(viewH int) bool`; Render draws track (theme.Muted dim), thumb (theme.Muted), and per-cell git marks (added=green/modified=yellow/deleted=red from theme) at `line*viewH/total` positions; marks paint on track cells, thumb wins.
- App: press with `x == editorRight-1` and bar visible → set ScrollY via scrollTargetForThumb, dragMode "scrollbar"; drag continues; release ends. Editor text area narrows by one column when the bar shows (Render handles internally; editorPress position math must ignore presses on the bar column).

**Steps:**
- [x] Tests: `TestScrollbarGeomProportions`, `TestScrollbarGeomHiddenWhenFits`, `TestScrollTargetClamps`, `TestRenderDrawsThumbAndMarks` (sim screen + GitLines fixture). Run, fail.
- [x] Implement scrollbar.go + Render integration + app mouse routing.
- [x] `make test` green.

### Task 8: Project-wide content search

**Files:**
- Create: `internal/search/search.go` + `internal/search/search_test.go`
- Create: `internal/app/projfind.go` + `internal/app/projfind_test.go`
- Modify: `internal/app/app.go` (App state block; handleKey routing before findOpen; draw hook; custom event `projFindDoneEvent`; menu row in Search group)
- Modify: `internal/app/leader.go` (bind `F`)
- Modify: `internal/finder/finder.go` (add `Files() []string` snapshot accessor) + `finder_test.go`

**Interfaces:**
- Produces (search pkg): `type Match {Path string; Line int; Col int; Text string}`, `type Options {MaxTotal, MaxPerFile, MaxFileSize int}` (defaults 500/50/1MB via `DefaultOptions()`), `Search(rootDir string, files []string, query string, opts Options) ([]Match, bool)` — literal substring, smart-case (all-lowercase query = case-insensitive), skips binaries (NUL in first 8KB), second return = truncated flag. Pure, synchronous, testable.
- Produces (app): find-bar-style input row ("Search project:") + editor-area results overlay:
  - App fields: `projFindOpen bool`, `projFindValue []rune`, `projFindCursor, projFindScroll int`, `projFindGen int`, `projFindMatches []search.Match`, `projFindTruncated bool`, `projFindSelected, projFindScrollY int`, `projFindFolded map[string]bool`, `projFindBusy bool`
  - typing bumps projFindGen and spawns a goroutine → `screen.PostEvent(&projFindDoneEvent{gen, matches, truncated})`; stale gens dropped in the handler (custom-event pattern, no UI mutation from goroutine)
  - rows rebuilt by `projFindRows()`: file header `▾ rel/path (N)` then `  123: text`; header click or Enter toggles fold; match click/Enter → `OpenFileAtLine` + close; Up/Down moves selection skipping folded matches; Esc closes; wheel scrolls
  - `(a *App) menuFindInProject()`; leader `F`; menu row `{label: "Find in project", shortcut: "Esc F", enabled: (*App).hasFinder}`
- Produces (finder): `(f *Finder) Files() []string` — copy of the indexed list (empty until index ready).

**Steps:**
- [x] Engine tests: `TestSearchLiteralHits`, `TestSearchSmartCase`, `TestSearchSkipsBinary`, `TestSearchCapsAndTruncatedFlag`, `TestSearchMissingFileSkipped`. Run, fail. Implement search.go.
- [x] App tests: `TestProjFindRowsGroupAndFold`, `TestProjFindStaleGenerationDropped`, `TestProjFindEnterOpensAtLine`, `TestProjFindEscCloses`. Run, fail. Implement projfind.go + wiring.
- [x] `make test` green.

### Task 9: Per-project session restore

**Files:**
- Create: `internal/session/session.go` + `internal/session/session_test.go`
- Modify: `internal/filetree/filetree.go` (add `ExpandedDirs() []string`, `ExpandDirs(rels []string)`) + `filetree_test.go`
- Modify: `internal/app/app.go` (New: restore after tree build when no CLI file; Close: save; helpers `captureSession`/`restoreSession` in new file)
- Create: `internal/app/session_restore.go` + `internal/app/session_restore_test.go`

**Interfaces:**
- Produces (session pkg): `type TabState {Path string; Line, Col, ScrollY int}`, `type Project {Tabs []TabState; Active int; Expanded []string; SidebarWidth int; SidebarShown bool; SavedAt time.Time}`, `Load(rootAbs string) (Project, bool)`, `Save(rootAbs string, p Project) error`. Storage: `$XDG_STATE_HOME/skiff/sessions.json` (fallback `~/.local/state/skiff/`), one JSON map keyed by abs root, pruned to the 50 newest by SavedAt. Corrupt file = fresh start, never an error the app surfaces. Tab paths stored relative to root.
- Produces (filetree): `ExpandedDirs()` — rel paths of expanded dirs, root excluded; `ExpandDirs` — expands each rel path component-by-component, lazily loading children; unknown paths skipped.
- App: restore skips missing files, clamps Active, applies sidebar width/shown; single-file mode (`NewSingleFile`) neither restores nor saves.

**Steps:**
- [x] Session pkg tests: `TestSaveLoadRoundTrip`, `TestLoadCorruptFileFreshStart`, `TestPruneKeepsNewest50` (t.Setenv XDG_STATE_HOME). Run, fail. Implement.
- [x] Filetree tests: `TestExpandedDirsRoundTrip`, `TestExpandDirsUnknownSkipped`. Run, fail. Implement.
- [x] App tests: `TestRestoreSkipsMissingFiles`, `TestCaptureSessionContents`. Run, fail. Implement wiring.
- [x] `make test` green; `make build` sanity.

### Task 10: Docs touch-up

- [x] Update README feature list + printHelp() in main.go (mentions `skiff file:line`). Update CLAUDE.md architecture map with the two new packages. `make test` green.

---

## Addendum: druk git workflow (second tranche)

Skiff already has the read side (panel, adaptive side-by-side diff, log,
ahead/behind). This tranche adds druk's write side and panel layout.

### Task G1: git op plumbing — async runner, error explainer, push args

**Files:** Create `internal/app/gitops.go` + `gitops_test.go`; modify `app.go` (event case, `gitBusy` field), `modals.go` (closeAllModals resets `diffPanelRow`).

- `gitOpDoneEvent{when, label, okFlash, output string, err, touchesTree bool}` + `handleGitOpDone`: clear busy, error → `openInfo` with `explainGit` headline + raw output (push-rejected error instead offers a "Pull, then push" confirm that runs `pull --no-rebase --no-edit` + push), success → flash; always `refreshGitStatusAsync`, `touchesTree` → `refreshTreeNow`.
- `runGitOp(label, okFlash string, touchesTree bool, cmds ...[]string) bool` — refuses while busy; goroutine runs `git -C root` sequence with `GIT_TERMINAL_PROMPT=0`, `GIT_EDITOR=true`, stops at first error, posts event. Pure helper `execGitSequence(root, cmds)` shared with tests.
- `explainGit(output) string` mapping table (rejected push, non-ff pull, conflict, nothing to commit, no stash).
- `gitPushCmds(root, branch) [][]string` — `push` or `push --set-upstream origin <branch>` when `@{upstream}` is absent; `gitCommitCmds(paths, msg)` — `add -A -- <paths>` + `commit -m <msg> -- <paths>` (druk semantics).
- [x] Tests (real repos via `initRepo`, bare-dir origin): explain mappings, push cmds ± upstream, commit cmds, end-to-end commit/undo/stash via `execGitSequence`, handleGitOpDone busy/flash/error routing.

### Task G2: menu actions — commit, push, pull, branch, extras

**Files:** `gitops.go` (+tests); `app.go` menu rows.

- Git menu group grows: "Commit changes…" (`hasGitChanges`), "Push", "Pull", "Switch branch…", "More git actions…" (context-popup with Fetch / New branch… / Stash changes / Pop stash / Undo last commit — reuses contextItems anchored via placeContext, node = tree root).
- Commit: checked-set from G3 (default all) → `openPrompt("Commit message")` → `runGitOp`. Pull: `--ff-only`. Undo commit: `openConfirm` first, `reset --soft HEAD~1`. Stash: `stash push -u` / `stash pop`. New branch: prompt → `checkout -b`.
- Switch branch: `openForm` select of `branch --all` names (current first, HEAD filtered); remote pick uses druk's tracking logic (`checkout -b x --track origin/x` unless local exists).
- [x] Tests: branch list builder, tracking switch end-to-end, menu layout constants bump (+5 rows).

### Task G3: panel redesign — branch row, buttons, commit checkboxes

**Files:** `gitchanges.go` (+ test updates), `app.go` (fields `gitCommitChecks map[string]bool`, `gitPanelSelected`, `diffPanelRow`).

- Layout: row 0 header tabs; row 1 `⎇ branch ↑n ↓n` (click → branch picker); row 2 buttons `[ Commit ] [ Push ] [ Pull ] [ ⋯ ]` (⋯ anchors the extras popup); rows 3+ changes with a leading `●`/`○` commit checkbox (click on the first two cells toggles; dirs included). Busy → muted "working…" after the buttons.
- `gitCommitChecks` absent-means-checked, pruned in `rebuildGitChangesRows`; `checkedChangePaths()` feeds the commit.
- History moves from the old row-1 click into the extras popup ("Commit history" stays in the main menu too).
- [x] Tests: click mapping (branch/buttons/checkbox/row), checked-set pruning, draw assertions for the new rows.

### Task G4: keyboard diff-walk + docs

- `activateGitChangeRow` records `diffPanelRow`; while that diff is open, `↑`/`↓` in `handleDiffKey` walk to the prev/next file row (dirs skipped), reopening the diff and keeping the panel selection visible; wheel/PgUp/PgDn still scroll. `closeAllModals` resets the index. Menu "Diff this file" leaves it -1 (old scroll behavior).
- README git section + hotkey note; CLAUDE.md map line for gitops.go.
- [x] Tests: walk skips dirs and clamps; `make test` race-green.

---

## Tranche 3: corner-cut fixes + remote performance

Priority order; check off as they land, one commit each.

- [x] P1 Remote scroll performance: (a) coalesce pending tcell events into one draw (Run loop drains queue before drawing); (b) stop re-tokenizing on scroll — cache highlight styles per content-generation, not per viewport (read highlight.go first).
- [x] P2 Generic filter-list picker modal (generalize themepick: title, entries, current, live-preview hook optional) → branch picker uses it (filter + hover + Enter, no blind ←→ select).
- [x] P3 Themes: port druk's statusFg as Theme.StatusFg (generator mapping + regenerate palettes.go), audit drawStatusBar to use it; sanity-fence status bar contrast for all 26.
- [x] P4 Background bulk file ops: paste/duplicate/delete of large trees run in a goroutine posting progress → done events (custom-event pattern); cross-device os.Rename fallback (copy+delete); status-bar progress count.
- [x] P5 Branch verbs: Merge branch…, Rename branch…, Delete branch… in the ⋯ extras popup via the generic picker + confirm; gitops runners (merge --no-edit; branch -m; branch -d with -D confirm on failure).
- [x] P6 Search hardening CORE done: 120ms debounce (projFindKickEvent), per-file cancellation (Options.Cancelled vs projFindLiveGen), match-column jump (OpenFileAtLineCol). Toggle chips DONE too (Aa / ⌇w / .* on the bar, engine flags in search.Options).
- [x] P7 Replace DONE: App fields replaceOpen/replaceValue/replaceCursor/findFocusReplace. handleFindKey: Tab toggles/switches to the replace field; in replace focus Enter = ReplaceCurrentMatch+FindNext, Shift+Enter = ReplaceAllMatches (hint in bar), Esc closes bar. Editor side in internal/editor/find.go: (t *Tab) ReplaceCurrentMatch(repl string) bool and ReplaceAllMatches(repl string) int — pushUndo(undoGroupStructural) once, replace via DeleteRange+InsertString using the Match span, re-run SetFindQuery after, set Dirty/StyleStale. drawFindBar renders " Replace: <v>" second field on the same row, caret follows focus. closeFind clears replace state. Tests in editor/find_test.go (replace current advances + one undo step; replace-all count + single undo) and app find_test-style (Tab focus toggle, Enter routes to replace when focused). Project-wide replace stays deferred — note in README.
- [x] P8 Sessions (atomic rename write; flock skipped — Windows target — race narrowed via save-on-change): save on every tab open/close + 30s tick, atomic temp+rename write, flock to close the two-instance lost-update race; persist Preview flag.
- [x] P9 Git polish: branch-list/upstream checks off the UI thread; isPushRejected only for non-fast-forward ("fetch first"/"non-fast-forward"), hook rejections get plain error; drop "Opened" flash on preview clicks.
- [x] P10 diffBase DONE (Compare against… in ⋯ popup; loaders take base; commit gated while base active; ⇆ marker in status bar + panel): compare-against-ref mode — picker sets a.diffBase; gitstatus/diff/gutter/panel honour it; status bar shows "⎇ main ⇆ base"; Esc path back to HEAD.
- [x] P11 Small (menu height left as-is: scrolls fine, extras popups keep it bounded): cheat-strip two-row wrap when clipped; CLAUDE.md stale relY paragraph; menu height note → move Line-ops group into ⋯-style "Edit extras" if still unwieldy.
