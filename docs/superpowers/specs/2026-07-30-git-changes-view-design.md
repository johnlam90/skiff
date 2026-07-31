# Git changes view — design

Date: 2026-07-30
Status: implemented (revised same-day: modal → sidebar panel, per
user feedback asking for a tab "right next to EXPLORER" plus diffs)

## Goal

Give Skiff the one VS Code / Cursor source-control affordance it is
still missing: a single place that answers "what have I changed?" with
one click, and a per-file diff view. PR #42 already delivered per-kind
tree tinting, editor gutter markers with click-to-preview hunks, and
the branch name in the status bar — this feature completes the picture
with a **changes panel and diff preview**, without turning the editor
into a git client.

## Constraints (from CLAUDE.md / AGENTS.md)

- Mouse-first; no `Ctrl+` shortcuts; `Esc` is the only leader.
- Every action reachable from the main `≡` menu.
- Best-effort git: never block the UI, never error at the user.
- One static binary; shell out to `git`, no libgit2/CGO.
- Every source file gets a same-package `_test.go`.

## Approaches considered

1. **Sidebar panel with EXPLORER / GIT header tabs** *(chosen)* — the
   changes list lives where VS Code users expect source control: in
   the sidebar, one tab over from the file tree. Click a row for the
   diff. Requested explicitly by the user after a first modal-based
   iteration.
2. **Centered modal list (finder-style)** — the first iteration.
   Worked, but a modal hides the list the moment you act on it; a
   panel keeps the worklist visible while you move through files.
3. **Full git porcelain (stage / commit / discard)** — rejected:
   Skiff is an editor, not a git client. Its audience lives in
   tmux; the shell is one pane away.

## Design

### 1. Sidebar header tabs

The sidebar's header row becomes two clickable tabs:

```
 EXPLORER   GIT
```

The active tab keeps the header's muted-bold look; the inactive one
drops to Subtle. The GIT tab only renders for git projects — plain
directories keep the single EXPLORER header. In explorer mode the tab
row is overdrawn on top of the tree's own header so filetree's
geometry (and its tests) stay untouched; `tree.HitTest` already treats
row 0 as inert, so the app intercepts header clicks in `sidebarClick`.

### 2. Git panel (`internal/app/gitchanges.go`)

Replaces the tree in the sidebar rect when active:

```
 EXPLORER   GIT
 main                        ← branch, where the tree shows the root
 M app.go  internal/app     ← letter colored, name first, dir dimmed
 A gitchanges.go  internal/app
 D old.go  internal/app
```

- Rows come from `tree.DirtyFiles` (refreshed on activation), sorted
  by relative path. Letters M / A / D / R use the matching `theme.Git*`
  colors — the same hue everywhere a file appears.
- Wheel scrolls when the list outgrows the panel; the 10-second
  refresh tick rebuilds rows live while the panel is up. A repo that
  vanishes (or `git checkout` cleaning everything) degrades gracefully
  — the panel falls back to the explorer / shows an empty state.
- The active file's row highlights in Accent, mirroring the tree.

### 3. Click a row → diff (the VS Code gesture)

Activating a change opens the **diff view modal**
(`internal/app/diffview.go`), a dedicated modal in the pattern of
finder.go / formmodal.go:

```
│ @@ -2,59 +2,59 @@ line 1                                    │
│   4  line 4              │   4  line 4                      │
│   5  line 5              │   5  LINE 5                      │
│                 [ Open file ]    [ Close ]                 │
```

- **Side-by-side on wide terminals**: the unified `git diff` output
  is parsed into aligned rows (context on both sides; each deletion
  run pairs line-for-line with the following addition run; leftovers
  go one-sided with a blank gap). Line numbers on both sides,
  deletions in GitDeleted, additions in GitAdded, hunk headers
  spanning the width.
- **Adapts to unified when narrow**: below ~92 body cells the modal
  falls back to the raw colored unified lines. The layout is chosen
  per draw, so resizing the terminal reflows an open diff live.
- **Open file** (focused by default — Enter is the fast path) opens
  the file with the cursor on its first changed line. Deleted paths
  get no open button; untracked directories flip to the explorer and
  reveal themselves. The editor's gutter-marker click routes through
  the same modal (no open button — the file is already open).
- Tracked files diff against `HEAD`; untracked files fall back to
  `git diff --no-index /dev/null <path>` so new files render as
  all-added. The fallback is gated on the porcelain status so a clean
  tracked file can never masquerade as brand new.
- Wheel/trackpad scrolling uses tcell's WheelUp/WheelDown masks (a
  Button4/5 bug in the info modal was fixed along the way; the info
  modal itself stays single-button for report-only output).

### 4. Entry points

- Sidebar `GIT` tab (primary, mouse-first).
- `≡` menu: **Git changes** (Search group, `Esc g` shortcut label);
  hidden in single-file mode, greyed out outside a repo.
- `Esc g` — toggles between the two sidebar views, showing the
  sidebar first if hidden (mirrors `Esc t`).
- Status bar: the right-hand segment reads ` branch · N ` when dirty
  and clicking it activates the panel. Geometry comes from a pure
  helper shared by draw and hit-test.

## Error handling

All git failures degrade to "no rows / no count / flash message",
matching gitstatus.go's best-effort contract. Single-file mode flashes
"Git changes isn't available in single-file mode"; a non-repo flashes
"Not a git repository".

## Testing

- `gitchanges_test.go`: row building/sorting/letters, toggle guards
  and round-trip, header tab clicks, row-click → diff modal (M, A,
  D, dir cases), Open file → cursor jump, scroll clamps, panel and
  header draw smoke tests on a `SimulationScreen`, status-segment
  formatting and click hit-test, live refresh.
- `modals_test.go`: `openInfoAction` arming/focus, keyboard decline
  path, single-OK geometry preservation, mouse click paths.
- `leader_test.go`: `Esc g` activates the panel.
- `gitstatus_test.go`: `loadGitFileDiff` deleted/untracked/degrade
  paths.
- Menu-table test pinning the row's label, shortcut, and predicates.

## Docs

README (feature bullet, hotkey row, "Git changes" section), website
`hotkeys.md` (Esc g row) and `mouse-controls.md` (sidebar tabs +
status-bar click).

## Round 3 additions (same day)

Six improvements layered on after the panel + diff view landed:

1. **Async git status** — `collectGitStatus` (pure worker: repo
   snapshot + per-tab gutter diffs) runs on a goroutine and posts a
   `gitStatusEvent`; `applyGitStatus` stamps results on the main
   thread. In-flight kicks coalesce into one queued follow-up. The
   synchronous flavour remains for startup and tests. Panel toggles
   gate on the *cached* branch so they never block on git.
2. **Word-level diff highlighting** — `diffSpan` finds the common
   prefix/suffix of each paired modification; the differing span
   renders in reverse video over the git color.
3. **Horizontal diff scrolling** — Shift+wheel / `←`/`→` slide the
   body sideways in both layouts; line-number gutters stay put;
   clamped to the longest line.
4. **Diff this file** — ≡ menu row showing the active tab's diff
   (no Open button; the file is already open). Enabled via gutter
   markers or the dirty-file map, so it works in single-file mode.
5. **GIT tab count badge** — `GIT 4`, part of the tab's click zone,
   clipped safely on narrow sidebars.
6. **Chroma syntax highlighting** — context rows are lexed once at
   open (the whole diff as one source, so multi-line tokens survive)
   and re-backgrounded onto the modal; changed rows keep git colors;
   capped at 4000 rows; narrow/unified layout keeps plain coloring.

## Round 4: history (borrowed from lazygit, same day)

Three read-only additions — the lazygit ideas that fit an editor:

1. **Ahead/behind arrows** — `git rev-list --left-right --count
   @{upstream}...HEAD` in the (async) status collection; the status
   bar segment becomes ` main ↑2 ↓1 · 4 `. No upstream → no arrows.
2. **Commit history** (`internal/app/gitlog.go`) — a finder-family
   modal listing recent commits (short SHA / subject / relative age,
   capped at 200); Enter or click opens the commit's diff. Reached
   from ≡ → Commit history or a click on the Git panel's branch row.
3. **History of this file** — the same modal scoped by
   `git log --follow -- <path>`; activating an entry shows that
   commit's diff of just that file, with syntax highlighting (single
   language, so the lexer is safe).

Enabling work: the side-by-side parser now understands multi-file
diffs — `diff --git` boundaries become full-width file rows, hunk
numbering resets per file, and single-file diffs drop their lone
boundary row since the title already names the file. Whole-commit
diffs skip Chroma (one lexer can't serve many languages); file-scoped
ones keep it.

Considered and deferred: a branch switcher (first mutation — decide
after living with the read-only set), stage/commit (a philosophy
change; the minimal loop is sketched in the improvement notes).

## Out of scope

Stage/unstage/commit/discard, diff *editing*, side-by-side diff
rendering, tree letter-badges (color-only was a deliberate #42
decision), and `-uall` porcelain expansion of untracked directories.
