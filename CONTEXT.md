# Skiff

A mouse-first terminal code editor for SSH-into-tmux workflows: file tree,
tabs, syntax-highlighted editor, status bar — one static Go binary. This
glossary is the canonical language for its concepts; use these terms in
code, issues, and design discussion.

## Language

### Surfaces

**Overlay**:
A floating surface drawn over the editor that captures keyboard and mouse
until dismissed — the action menu, prompt, confirm, dirty-close, form,
context menu, list pick, finder, diff view, and git log.
_Avoid_: modal, dialog, popup

**Overlay stack**:
The single owner of which overlay is up. Opening an overlay replaces any
open one — at most one overlay is ever up.
_Avoid_: modal cascade, routing lists

**Strip**:
A bottom bar that reflows the editor and captures keys while letting mouse
actions pass through to the editor — the find bar, the project-find bar,
the leader strip. A strip is not an overlay and never sits on the stack.
_Avoid_: bar, bottom panel

**Shortcut reference**:
The `Esc ?` overlay: the whole leader table under its group headings plus
a note on the ≡ menu. Generated from `leaderBindings()`, so it cannot
advertise a gesture the dispatch dropped. Distinct from the **leader
strip**, which is a strip showing only what is armed right now.
_Avoid_: help screen, cheat sheet (that's the strip's job), keymap

**Prefab**:
A ready-made overlay kind (prompt, confirm, pick, form) that a feature
fills with text and callbacks instead of drawing its own surface.

**Drill-in**:
A single menu row that opens a pick of the rows it demoted — "Git…",
"File clipboard…". A drill-in keeps a cluster reachable from the action
menu without spending a top-level row per verb; every drill-in is
registered in `menuDrillIns()` so the reachability test can see it.
_Avoid_: submenu, nested menu

**Filter**:
The action menu's focused text field. Typing narrows rows across all
groups at once, so a row's group never has to be known to reach it. With
a filter typed the groups collapse into one flat match list.
_Avoid_: search box, palette query

**Action menu**:
The ≡ menu — the primary, always-reachable home of every editor and file
action. Right-click and shortcuts are redundant paths to it, never the
only path.
_Avoid_: hamburger menu, main menu

### Tabs

**Tab**:
An open file with its own cursor, selection, and scroll state. A tab is
identified by its identity — never by its position in the tab strip and
never by its path (paths change on rename).
_Avoid_: buffer (that's the text inside a tab), index

**Preview tab**:
A tab opened by a single click, shown in italics, occupying the single
preview slot — opening another preview replaces it in place.

**Pin**:
Promoting a preview tab to a permanent one (double-click, edit, or
explicit open).
_Avoid_: keep open

### Files

**Disk conflict**:
A dirty buffer whose file changed on disk underneath it. It is a decision
the user has to make — Keep mine / Reload / Diff — not a notification, so
it raises a prompt and keeps a status-bar marker until it is resolved.
Looking at the diff is not resolving; saving or reloading is.
_Avoid_: external change (that's the clean-buffer case, which just
reloads), merge conflict (that's git's)

**Line ending**:
The newline convention a file had on disk, remembered per tab. Buffers
hold unterminated lines and every write restores the file's own ending,
so editing one line of a CRLF file never rewrites the whole file.
_Avoid_: newline mode, EOL setting (there is no setting — it is detected)

### Git

**Repo**:
The project root's git repository as skiff sees it. "Not a repo" is an
explicit state, not an empty branch name. Its interface is a vocabulary
of typed verbs (Diff, Log, Branches, Push, Switch, …), each owning its
own argv and parse; a refusal on the write side is an OpError carrying
advice. No surface assembles a git command line.

**Snapshot**:
One consistent read of repo state — branch, ahead/behind counts, and the
set of changed paths — feeding every git-aware surface (tree badges,
gutter, status bar, git panel).
_Avoid_: git status (that's the command, not the model)

**Patch**:
One parsed diff — files, each with hunks, each with lines that know
their old and new line numbers. Both producers build the same shape:
`git diff` output parsed, or a dirty buffer measured against what is on
disk. Nothing renders a patch to text in order to read it back.
_Avoid_: diff output, diff lines (that is the display form, not the
model)

**Git panel**:
The sidebar's second mode: the list of changed files with their change
kinds, replacing the explorer tree while active. Mouse-first, but it can
take keyboard focus (`Esc g`) to walk rows, stage them, and reach the
action buttons without a mouse.

### Text

**Grapheme cluster**:
The unit the editor lays text out in: the runes a reader sees as one
character — a letter and its combining marks, a ZWJ emoji sequence, a
flag's two regional indicators. Runes are what a buffer stores and
cells are what the terminal paints; the cluster is what the caret
steps over, Backspace removes, wrap refuses to split, and a
double-click selects whole. Widths come from `uniseg`, the same
engine tcell measures a cell with.
_Avoid_: character, glyph, rune (a rune is a different unit — name
which one you mean)
