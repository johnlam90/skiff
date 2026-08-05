<!--
  File: CLAUDE.md
  Author: Spicer Matthews <spicer@cloudmanic.com>
  Created: 2026-04-29
  Copyright: 2026 Cloudmanic, LLC. All rights reserved.
-->

# CLAUDE.md — Skiff

Project-specific guidance for Claude Code. Read this first; it captures
conventions and design decisions that aren't obvious from the code alone.

## What this project is

Skiff is an opinionated, **mouse-first** terminal code editor aimed at
SSH-into-tmux workflows. It looks and behaves like a tiny VS Code: file
tree on the left, tabs across the top, syntax-highlighted editor in the
middle, status bar at the bottom. It ships as a single static Go binary
with no CGO.

Users open the action menu (Save, Quit, Show/Hide Sidebar, …) by clicking
the `≡` icon, right-clicking, or double-tapping `Esc`. There are
intentionally **no `Ctrl+` shortcuts** for editor actions — they conflict
with `tmux` and terminal emulators. Don't add them back.

**Every file action also lives in the main ≡ menu.** macOS Terminal +
tmux often swallows Button3 (right-click), so the editor cannot rely on
right-click as the only path to anything. Tree right-click is a redundant
shortcut, not a primary surface — when adding new file-management
features, make sure they're reachable from the main menu first.

## Module / repo

- Module: `github.com/johnlam90/skiff`
- Binary name: `skiff` (one word, lowercase — Makefile, goreleaser,
  brew formula all assume this)
- Brew: `johnlam90/homebrew-skiff` is the tap users install from
  (`brew install johnlam90/skiff/skiff`). The formula's source of truth
  is still `Formula/skiff.rb` in THIS repo (GoReleaser writes it each
  release with the default token); the tap repo pull-syncs it on a
  15-minute cron — no cross-repo secret exists or is needed.

## Architecture map

```
main.go                       Entry — one optional rootDir / file[:line] arg;
                              extra arguments are refused, never dropped
internal/app/app.go           App struct + New/NewSingleFile, Run, handleEvent, flash
internal/app/layout.go        All screen geometry: panel rects + splitter clamping
internal/app/draw.go          The render pass + tab-strip layout
internal/app/refresh.go       10s tick: off-thread scan, on-loop apply, tab reconcile
internal/app/keys.go          Keyboard entry point: Esc-leader windows, arrows, typing
internal/app/leader.go        Esc-leader table (key/action/desc/group) — the single
                              source for dispatch, cheat-strip and Esc ? sheet
internal/app/cheatsheet.go    Esc ? shortcut reference, generated from leaderBindings()
internal/app/mouse.go         Mouse dispatcher + drag/auto-scroll state
internal/app/menudef.go       Menu data model: rows, groups, drill-ins, filter matcher
internal/app/menu.go          Menu behavior: filter field, nav, hit-test, drawing
internal/app/actions.go       One handler per menu row + the custom-action runner
internal/app/tabops.go        Tab lifecycle, save/close guards, clipboard, has* gates
internal/app/conflict.go      Dirty-buffer-vs-changed-file prompt + buffer/disk diff
internal/app/gitchanges.go    Git panel: rows, buttons, keyboard mode, hint
                              strip, and the change list's own scrollbar
internal/app/gitstatus.go     Best-effort `git status` read behind the tree tint
internal/app/overlays.go      Overlay stack wiring: menu adapter + dropOverlay
internal/app/modals.go        Openers for the prefab overlays + closeAllModals
internal/app/projfind.go      Project-wide content search panel (Esc-F)
internal/app/preview.go       Shared file-open path + preview-tab rules
internal/app/fileclip.go      File clipboard: cut/copy/paste/duplicate tree entries
internal/app/session_restore.go App ↔ session store bridge (capture/restore)
internal/app/gitops.go        Git write side: commit/push/pull/branch/stash runner
internal/editor/buffer.go     Position + Buffer ([]string lines), edit primitives
internal/editor/tab.go        Tab: path, buffer, cursor, anchor, scroll, dirty state
internal/editor/lineops.go    Move / duplicate line-block gestures
internal/editor/wrap.go       Soft wrap: segment math + wrap-mode render/scroll/hit-test
internal/editor/scrollbar.go  Right-edge scrollbar + git change map
internal/editor/highlight.go  Chroma → []tcell.Style per line
internal/editor/indent.go     Visual-column math, indent detection, Enter auto-indent
internal/editor/word.go       The single definition of "a word" + word-wise motion
internal/editor/cluster.go    Grapheme clusters + terminal cell widths (uniseg)
internal/editor/bracket.go    Bracket match under the caret (+ the render decision)
internal/filetree/filetree.go Lazy tree, identity-preserving refresh, hit-test,
                              render — plus the per-directory child cap
                              (MaxDirChildren + "… N more" sentinel), the
                              ReadErr "(unreadable)" mark, .gitignore-aware
                              filtering (HideIgnored + the per-directory
                              matcher cache), symlink resolution with
                              ancestor-loop refusal, and the sidebar's
                              own scrollbar column
internal/scrollbar/           The one definition of a scrollbar: thumb
                              geometry, its click inverse, and the Track/
                              Thumb glyphs. No tcell, no theme — both the
                              editor's bar and the tree's import it so they
                              cannot drift
internal/search/search.go     Literal smart-case project search engine
internal/finder/              Project file index (git ls-files, gitignore-aware
                              walk fallback) + the fzy-style fuzzy matcher
internal/format/              Format-on-save: project + defaults config and the
                              (path, config-hash) trust store
internal/customactions/       Loader for ~/.config/skiff/actions.json, incl. the
                              optional per-action prompts
internal/session/session.go   Session store — one file per project under
                              ~/.local/state/skiff/sessions/ (legacy
                              sessions.json is migrated + renamed aside)
internal/atomicfile/atomicfile.go Temp-file + fsync + rename write, shared by
                              every config/state file (session, trust,
                              config.json, .skiff/format.json)
internal/overlay/             Floating surfaces: the Stack (routing truth),
                              Field/chrome primitives, and the prefab
                              overlays (Prompt/Confirm/Info/Dirty/Form/
                              Popup/Pick). scrollbar.go is the shared
                              scroll indicator every windowing prefab
                              paints in its frame's right padding
                              column — Confirm's trust body, Info's
                              stderr/diff, Pick's list
internal/git/                 Git process boundary: Repo over a Runner
                              seam (real exec + in-memory Fake), hardened
                              env + read timeouts on every call, and the
                              Snapshot model (IsRepo/Branch/Ahead/Behind/
                              Files) every git-aware surface consumes
internal/git/ref.go           SafeRef: refuses refs git would read as an option
internal/clipboard/clipboard.go OSC 52 to /dev/tty with tmux passthrough wrap,
                              capped at MaxPayloadBytes (ErrTooLarge above it)
internal/userconfig/userconfig.go ~/.config/skiff/config.json (icons, theme,
                              wrap, gitignore)
internal/icons/icons.go       Nerd Font detection (deadline-bounded) + glyphs
internal/theme/theme.go       Default Tokyo Night palette + contrast helpers
internal/theme/palettes.go    Theme registry — 25 druk-ported palettes + ByID
internal/theme/degrade.go     Low-color fallback: hue → bold/underline/reverse
internal/version/version.go   const Version = "x.y.z" — single line, CI bumps it
```

## Conventions

### File headers
Every new source file gets the header block (file name, author, created
date, copyright year). See existing files for the exact format. Keep
copyright year matching the **current year** (2026 right now).

### Comments
- A short doc comment above every function (public **and** private)
  explaining intent. This is a project-wide convention — don't skip it.
- Skip throwaway "what" comments inside functions; favor "why" notes
  for non-obvious decisions.

### Tests — required, not optional
**Every source file gets a corresponding `_test.go` file in the same
package.** New code without tests should not be merged. The bar:

- New exported functions: cover happy path + the obvious failure mode.
- New unexported helpers with non-trivial logic: same.
- Bug fixes: add a test that fails before the fix and passes after.
- Pure data / glue (theme palettes, single-constant files): a smoke
  test that the value is sensible is enough.

Conventions:
- One `_test.go` per source file, in the same package (NOT `_test`),
  so tests can poke unexported helpers directly. Don't split tests
  for one source file across multiple test files.
- Each `Test*` function gets a short doc comment above it explaining
  the behavior it pins down — the same "why over what" rule as
  production code. See `internal/app/fileops_test.go` for the style.
- Use `t.TempDir()` for filesystem state; never write into the repo
  or `/tmp` directly.
- Never let a test read or write the user's real config / state. The
  format store honours `SKIFF_TRUST_FILE` and `SKIFF_DEFAULTS_FILE`
  overrides for exactly this — point them at `t.TempDir()` (see
  `internal/app/format_test.go`). The pre-rename `SPICEEDIT_*` names
  are still honoured so an old harness doesn't silently fall through to
  `~/.config/skiff`, but write the `SKIFF_` ones in new code. Session
  and config paths follow `$XDG_STATE_HOME` / `$XDG_CONFIG_HOME`.
- For UI / drawing code that takes a `tcell.Screen`, build one with
  `tcell.NewSimulationScreen("UTF-8")` and assert against
  `scr.GetContents()`.
- Skip a test (`t.Skip`) only when the environment can't satisfy a
  hard requirement (e.g. `/dev/tty` in CI). Don't skip to dodge a
  flaky test — fix it.

Run them locally:
```sh
make test          # go test ./... with race detector
make lint          # gofmt + go vet + pinned staticcheck (the CI gates)
make coverage      # generates coverage.out + an HTML report
```

CI (`.github/workflows/test.yml`) runs on every PR and enforces more
than the tests: `go mod tidy` cleanliness, `go build`, `gofmt -l`,
`go vet`, a pinned `staticcheck`, then `go test -race ./...` on Linux +
macOS. All of them block merges via the PR's required-checks, so an
unformatted file or a staticcheck finding is as red as a failing test.
`make lint` runs the three static gates with the same staticcheck
version — keep the pin in the Makefile and the workflow in lockstep.
There is deliberately no `push` trigger: `release.yml` calls this
workflow instead (see Releases).

### Commits
- No "Generated with Claude Code" trailers, no Co-Authored-By Claude.
- Don't ask for commit-message approval — commit directly with a good
  message when the user asks you to commit.

## Design patterns to preserve

### `cursorMoved` flag (tab.go)
The cursor only triggers `EnsureVisible` when something actually moved
the cursor. Every cursor mutator sets `t.cursorMoved = true`; `Render`
consumes the flag and clears it. **Do not** call `EnsureVisible`
unconditionally — that re-introduces the "scroll yanks back to cursor
on every tick" bug.

### Scroll clamping with overscroll
`tab.clampScroll(viewH)` allows the last line to scroll roughly to the
middle (`overscroll = max(viewH/2, 3)`). This is intentional — without
it, you can't comfortably read the bottom of a file.

### Soft wrap is anchor-based — no whole-file layout (wrap.go)
In wrap mode the scroll position is `(ScrollY, ScrollSeg)` and every
computation (render, EnsureVisible, clamp, hit-test) walks at most a
viewport's worth of lines from that anchor. Don't "optimize" this into
a cached file-wide visual-row map — the O(viewport) walks are the
design, and they keep huge files snappy with zero invalidation logic.
Tab stops reset at each segment, so a segment's rune subslice behaves
exactly like an independent line under the existing visual-column
helpers. Segments break only on grapheme boundaries, so a wide glyph is
never split across rows — that is what keeps the subslice property true
for CJK and emoji. The scrollbar stays buffer-line proportional on
purpose.

### Three units: runes are stored, clusters are walked, cells are painted
`Position.Col` indexes RUNES and `Buffer` splices runes — that never
changes. But a rune is not a character and not a cell: a CJK ideograph
eats two cells, a combining mark none, and a ZWJ family emoji is five
runes painted in two cells. Caret motion and layout therefore walk
grapheme clusters via `ClusterAt` (cluster.go), never by summing
`RuneVisualWidth` over runes — summing gives a family emoji 6 cells
instead of 2 and drifts the caret off the glyph under it. Widths come
from `github.com/rivo/uniseg`, which is the same engine tcell's
`CellBuffer.Put` uses, so our column math and tcell's cell buffer agree
by construction; swapping in a hand-rolled width table reintroduces the
drift on exactly the text that is hardest to debug. That is also why
`github.com/rivo/uniseg` is a DIRECT dependency in `go.mod` rather than
the indirect one it used to be under tcell: the editor now measures
text itself, so it has to pin the same segmenter tcell resolves cells
with — inheriting it transitively lets a tcell upgrade move our column
math without a line of our code changing. `RuneVisualWidth` is still
correct for a rune with no neighbours (tabs, plain ASCII) and wrong for
anything else.

Two scope limits, both deliberate. **Matching is still sub-cluster**:
`FindAll` compares decoded rune slices and `internal/search` works on
strings and regexps, so neither is cluster-aware — a query for a bare
combining mark or the second regional indicator of a flag can match a
position no caret can occupy. The highlight still lines
up in cells — the cluster takes its base rune's style — so alignment
holds while cluster-granular *matching* does not. Don't "fix" that
without deciding what a partial-cluster hit should select. And
**`uniseg.EastAsianAmbiguousWidth` is left at its default 1**, so `±`,
`→` and box-drawing characters stay one cell. Setting it to 2 would
reflow every existing user's terminal-drawn text; it is a global in
uniseg, so a single assignment anywhere changes the whole editor.

### `Buffer.LineRunes` memoises — so a buffer is not thread-safe to READ
`LineRunes` caches its decode in `Buffer.runeCache` and hands back a
slice it still owns. Two consequences, both easy to break:
**(1)** the returned `[]rune` is read-only for callers — writing through
it corrupts every later reader of that line. **(2)** reading a buffer
from a goroutine is a data race, not a safe read, because the read
mutates the cache. Anything off the main loop that wants buffer text
must copy the strings out on the event loop first (see how
`probeOpenTabs` deliberately only stats paths).

### Writing a buffer to disk goes through `TextWith`
Tabs remember the line ending the file arrived with
(`Tab.LineEnding`, detected in `NewTab`) and lines never carry a
terminator. Any new buffer-to-disk write MUST use
`buf.TextWith(tab.LineEnding.Newline())`, never `Buffer.String()` —
`String()` is LF-joined and silently rewrites every line of a CRLF
file. `Tab.Save` is the reference. Inserted newlines stay `"\n"` in the
buffer on purpose; only the write joins with the file's own ending.

### Custom tcell events for goroutine → main-loop messaging
Background work (auto-scroll during drag, 10s tree refresh) posts custom
events (`autoScrollEvent`, `treeRefreshEvent`) onto the tcell event queue
and the main loop handles them. Don't mutate UI state from goroutines
directly.

### Identity-preserving tree refresh (filetree.go)
`merge` walks the existing children, matches survivors by name, and
keeps their `*Node` pointers (and their `Expanded` state). New entries
get fresh nodes; gone entries are dropped. This is what makes the
10-second auto-refresh feel non-jarring — open folders stay open.

### Refresh scans off-thread, merges on the main loop (refresh.go)
The 10-second tick is split in two. `Tree.LoadedDirs` (a pure in-memory
walk) names the work, `filetree.ScanDirs` does the ReadDir sweep and
`probeOpenTabs` the per-tab Stat on a goroutine, the session write rides
along, and `handleTreeScan` merges the result via `Tree.ApplyScan` on the
event loop — the node graph the renderer walks is only ever mutated
there. `Tree.Refresh` stays synchronous for file operations, which need
the tree correct before the next draw; its `treeScanGen` bump retires any
in-flight sweep so a stale listing can't resurrect a just-deleted file.
Overlapping ticks coalesce into one follow-up, as in
`refreshGitStatusAsync`.

### Three-way external-change reconciliation (refresh.go)
On each tree-refresh tick, `reconcileTab` compares each open tab's mtime
against what the background sweep found: clean buffer + changed file →
silent reload; dirty buffer + changed file → conflict prompt; file
deleted → set `DiskGone` once.

### The overlay stack is the routing truth
Every floating surface (menu, prompt, confirm, pickers, diff, …) lives
on `a.overlays` (`internal/overlay.Stack`): `handleKey`, `handleMouse`,
`draw`, and `anyModalOpen` all consult the stack, never per-surface
booleans. Opening replaces (at most one overlay is ever up), openers run
`closeAllModals()` first, and activate paths are capture-then-close so a
callback that opens the next overlay is never popped by the previous
one's teardown. Strips (find bar, project-find bar, leader strip) are
NOT overlays — see `docs/adr/0001-strips-are-not-overlays.md` before
"fixing" the find bar's mouse pass-through.

### Modal layout via `relY` and dynamic `labelFor`
The action menu uses named struct literals with an optional `labelFor`
hook so labels like "Show Sidebar" / "Hide Sidebar" toggle in place.
`menuLayout` recomputes every `relY`, the divider rows, and the modal
height on each call — adding a menu item is just adding the struct
literal (plus updating the pinned numbers in `TestMenuLayout_*`).

### The menu's filter beats bare leader runes (menu.go)
With the action menu up, a bare rune goes to the type-to-filter field —
it never fires its Esc-leader action. 21 of 26 letters are bound in
`leaderBindings`, so honouring bare runes would make almost every
filter untypeable ("switch branch" saving the file on its first
keystroke). The cheat-sheet role survives three ways, and all three are
load-bearing: every row still renders its `Esc s` hint; `Alt+<rune>`
still fires the action (which is how tmux actually delivers a fast
`Esc s`); and an already-armed leader window still wins via
`leaderWindowIntercept`, the same precedence `handleKey` applies over
typing into the buffer. `Esc` is the menu's own key — clear a non-empty
filter, then close — and deliberately does NOT re-arm the leader.
See `docs/adr/0002-menu-filter-beats-bare-leader-runes.md`.

### Menu rows hide, not dim, when they can't apply (menudef.go)
`menuItemDef` carries both `enabled` (dim) and `visible` (drop). A row
that cannot light up in this session shape — git verbs with no repo,
edit verbs with no tab — sets `visible` and disappears; `enabled` is
for rows the user can act on *right now* by moving the caret or making
a selection. Demotion into an `overlay.Pick` drill-in is the other
half: register any new drill-in in `menuDrillIns()` or the
reachability test cannot see it, and CLAUDE.md's "every action is
reachable from the ≡ menu" rule quietly stops holding.

### Sidebar splitter drag
A drag is detected when a press lands at exactly `x == splitterX()`.
Min widths: `minSidebarWidth = 18`, `minEditorAfterDrag = 40`. Don't
let the editor shrink below that.

### Responsive sidebar: edge-triggered, and it restores only what it hid
`applyResponsiveSidebar` hides the explorer below
`autoHideSidebarWidth` (`minSidebarWidth + minEditorAfterDrag` = 58).
Two properties are load-bearing. It is **edge-triggered** — only a
crossing of the threshold acts (`a.sidebarNarrow` is the memo), so
`Esc t` inside a still-narrow window sticks instead of being re-hidden
by the next resize event. And it **restores only what it hid** —
`sidebarAutoHidden` is set here and cleared by `menuToggleSidebar`, so
a panel the user closed on purpose stays closed however wide the
terminal gets. Don't "simplify" either one into `sidebarShown =
width >= autoHideSidebarWidth`: that form is level-triggered, so it
re-hides the panel on every resize event inside a narrow episode and
stomps an `Esc t` the user just pressed.
`TestApplyResponsiveSidebar_ExplicitShowSurvivesNarrowResizes` is the
test that catches it.

### Every shortcut surface is generated from `leaderBindings()`
`leader.go` is the single source: `leaderActionFor` dispatches from it,
`leaderstrip.go` renders the armed-window strip from it, and
`cheatsheet.go` builds the `Esc ?` overlay from it via
`leaderDisplayGroups` (bucketed by the binding's `group` field, whose
names deliberately match the ≡ menu's top-level groups). Never
hand-write a second list of keys — a reference that can disagree with
the dispatch teaches a gesture that does nothing.
The drop-nothing guarantee lives in `groupLeaderBindings`, the pure
helper: an unknown group name appends its own heading rather than
dropping the binding, so a new `group` string can't make a shortcut
vanish from the sheet. `leaderDisplayGroups` is just the one-line
wrapper that feeds it `leaderBindings()` — the split exists so the
guarantee can be tested against a synthetic table carrying a
deliberately unregistered group, not only against today's real one.
Adding a binding is one struct literal; the menu row, the strip and the
overlay follow.

## Build / run

```sh
make run          # go run . in current dir
make build        # build to ./bin/skiff
make build-linux  # cross-compile linux/amd64
make install      # go install to $GOPATH/bin
make tidy         # go mod tidy
make clean        # rm -rf bin
```

There's no `dev server` to run for this project — it's a TUI. To test
UI behavior, build and run it against a real directory.

## Releases (don't break this)

Pushes to `main` trigger `.github/workflows/release.yml`, whose first
job calls `test.yml` (`workflow_call`) and gates everything below on
it — a red suite can never publish:

1. Reads `internal/version/version.go`.
2. **If that file was edited in the pushed commit**, the version is used
   as-is (manual major/minor bump). **Otherwise** the patch is
   auto-bumped, committed back to main with `[skip ci]`, and pushed.
3. Tags `v<x.y.z>`.
4. GoReleaser cross-compiles, attaches archives to a GitHub Release,
   and writes `Formula/skiff.rb` back into this repo (using the
   default `GITHUB_TOKEN` — no PAT). The formula commit also carries
   `[skip ci]` to break the loop.

If you're touching the workflow or `.goreleaser.yml`, make sure both
auto-commits keep their `[skip ci]` markers — without them the workflow
loops forever.

## What NOT to add

- `Ctrl+` editor shortcuts (they fight tmux/terminals — that's the
  whole reason the action menu exists).
- A config *system*. Skiff is opinionated: `~/.config/skiff/config.json`
  exists but stays a flat file of tiny keys (`icons`, `theme`, `wrap`,
  `gitignore`) — no plugin manifests, no per-key UI beyond a picker, no
  dotfile sprawl.
- CGO dependencies. The whole point is one static binary.
- Tree-sitter. We use Chroma intentionally — pure Go, no setup.
- A multi-file command line. `skiff a.go b.go` is an error naming
  `b.go`, not two tabs: a file argument means single-file mode (no
  sidebar, no index), and N files would force a project root out of the
  arguments' common ancestor — `/` or `$HOME` for `skiff pkg/a.go
  ../other/b.go`. The tree and the finder (`Esc p`) are the designed way
  to open the second file. See the doc comment on `resolveArgs`.
- Cross-repo release tokens. The `homebrew-skiff` tap repo mirrors
  `Formula/skiff.rb` from here via its own pull-sync cron — don't
  "simplify" that into a PAT-based push from this repo's CI.

## Agent skills

### Issue tracker

Issues live in this repo's GitHub Issues, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary, label strings equal to their names. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
