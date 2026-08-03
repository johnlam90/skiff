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

**Prefab**:
A ready-made overlay kind (prompt, confirm, pick, form) that a feature
fills with text and callbacks instead of drawing its own surface.

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

### Git

**Repo**:
The project root's git repository as skiff sees it. "Not a repo" is an
explicit state, not an empty branch name.

**Snapshot**:
One consistent read of repo state — branch, ahead/behind counts, and the
set of changed paths — feeding every git-aware surface (tree badges,
gutter, status bar, git panel).
_Avoid_: git status (that's the command, not the model)

**Git panel**:
The sidebar's second mode: the list of changed files with their change
kinds, replacing the explorer tree while active.
