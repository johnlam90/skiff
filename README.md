<!--
  File: README.md
  Author: John Lam <johnlam90@gmail.com>
  Created: 2026-07-30
  Copyright: 2026 John Lam. All rights reserved.
-->

# Skiff

> An opinionated, **mouse-first** terminal code editor for SSH workflows.

Skiff is a single-binary code editor that runs inside your terminal but
behaves like a tiny VS Code: a file tree on the left, tabs across the top,
syntax highlighting in the middle, a status bar at the bottom — and it's
all driven by the **mouse**, not arcane keystrokes.

Skiff started life as a fork of
[SpiceEdit](https://github.com/cloudmanic/spice-edit) by Spicer
Matthews / Cloudmanic, LLC (MIT) and has since grown into its own
editor: project-wide search & replace, a full git workflow (commit,
push, pull, branches, stash, compare-against-ref), 26 live-preview
themes, per-project sessions, preview tabs, and a remote-first
performance pass. Original copyright is retained in
[LICENSE](LICENSE); the theme palettes are ported from
[druk](https://github.com/letstri/druk) (MIT).

It's built for the workflow most "modern" terminal editors ignore: SSHing
into a remote box from inside `tmux` / `zellij`, opening a project, clicking
around files like a normal human, copying and pasting through your local
clipboard, and getting back to work.

<img width="2510" height="1712" alt="CleanShot 2026-04-29 at 23 30 21@2x" src="https://github.com/user-attachments/assets/a42ff082-406c-48cf-b5ca-9ca978ada217" />

## Why does this exist?

Vim and friends are wonderful if you've spent years memorizing them. Most
terminal editors assume you have. Skiff doesn't.

The goals, in order:

1. **Mouse-first.** Click a file to open it. Click a tab to switch.
   Click-and-drag to select text. Scroll wheel actually scrolls.
   Drag the splitter to resize the sidebar. Right-click (or click the
   `≡` icon, or double-tap `Esc`) for the action menu.
2. **No hot-key archaeology.** Save, save & close, quit — they all live
   in a centered modal you open with one gesture. No `Ctrl+` shortcuts
   that fight `tmux`, your shell, or your terminal emulator.
3. **SSH-friendly.** Copy uses OSC 52 escape sequences with a tmux
   passthrough wrapper, so highlighting text on a remote box still
   ends up in your local Mac clipboard.
4. **One static binary.** No runtime, no plugin manager, no config
   directory full of YAML. Drop it on a server and run it.
5. **Looks reasonable.** A hand-tuned Tokyo Night palette out of the
   box, a couple dozen more themes one menu away (`≡` → **Theme…** — Catppuccin,
   Dracula, Gruvbox, Nord, Rosé Pine, Solarized, and friends, ported
   from [druk](https://github.com/letstri/druk)), and syntax
   highlighting via [chroma](https://github.com/alecthomas/chroma)
   (no CGO, no tree-sitter setup).

## Features

- **VS Code-shaped layout** — file tree on the left, tab bar across the
  top, editor in the middle, status bar at the bottom.
- **Mouse-driven everything** — click to place cursor, drag to select,
  scroll wheel scrolls, double-click selects a word, drag past the edge
  to auto-scroll a selection.
- **Syntax highlighting** for dozens of languages via Chroma.
- **Action menu, filterable** — open it with the `≡` icon, right-click,
  or a double-tap of `Esc`, then just type: the filter narrows every
  top-level group at once ("comment" finds **Toggle line comment**, and
  so does "tlc"). Seven groups — File, Edit, Go, Git, View, Custom,
  Quit — with the git verbs and the file-clipboard actions one
  keystroke deeper behind **Git…** and **File clipboard…** (each pick
  has a filter field of its own). Arrows + `Enter` still walk the
  list; `Esc` clears the filter, a second `Esc` closes. Rows that
  can't apply right now (git verbs with no repo, edit verbs with no
  tab) are hidden rather than greyed out.
- **Live file tree** — auto-refreshes every 10 seconds so files added
  or removed from disk show up without you doing anything. A directory
  it can't read is labelled `(unreadable)` rather than drawn as empty,
  and a directory past 1000 entries is truncated with an inert
  `… N more` row — the sidebar is for navigating, the finder (`Esc p`)
  indexes the rest.
- **External change detection** — if a file on disk changes underneath
  an open clean buffer, the editor reloads it; if your buffer is dirty,
  you get the [disk-conflict prompt](#when-a-file-changes-under-you) —
  Keep mine / Reload / Diff — instead of quietly overwriting the other
  writer on your next save; if the file is deleted, the tab is flagged
  once.
- **Refuses what it can't edit** — a file with NUL bytes in its first
  8KB (a zip, a stray binary) or one larger than 32 MiB never reaches a
  buffer. The status bar says which, and names the size, instead of the
  editor going away for a while: a several-hundred-megabyte log used to
  load synchronously on a single click.
- **Git awareness** — changed files tint the file tree, gutter bars mark
  added/modified/deleted lines (click one for a diff popup), the status
  bar shows the branch plus a change count, and the sidebar's
  [GIT tab](#git-changes) lists every uncommitted change with
  click-to-diff — VS Code's Source Control view, one click away. On a
  machine with no `git` on `PATH` the editor says so once ("git was not
  found on PATH — branch and change badges are off") and every git
  surface repeats that reason instead of claiming "not a git
  repository".
- **Project-wide search & replace** — `Esc F` sweeps every file in the
  project (smart-case, with match-case / whole-word / regex chips),
  groups the hits by file, and opens any hit at its line. `Tab` grows
  a replace field: `Enter` rewrites the selected line, `[ All ]`
  (or `Shift+Enter`) rewrites everything behind one confirm. Every
  line re-verifies against what the search saw before it's touched
  (regex replacements expand `$1` / `${name}` groups; `$$` is a
  literal dollar) —
  files edited since are skipped and reported, never guessed at. Open
  buffers apply through the editor (per-file undo, dirty tabs stay
  dirty); closed files rewrite atomically on disk.
- **Soft wrap, on by default** — long lines flow onto continuation
  rows (breaking at word boundaries, like VS Code) instead of running
  off the right edge, so code and prose read without sideways
  scrolling. Prefer panning? `≡` → **Unwrap long lines** (or `Esc z`)
  flips every open tab and persists (`{"wrap": "off"}` in the same
  config file the theme lives in); with wrap off, Shift+wheel scrolls
  sideways and `‹`/`›` chevrons mark clipped lines.
- **Preview tabs** — a single tree click opens a file in one reusable
  *italic* tab, so browsing ten files doesn't leave ten tabs behind.
  Click the file again, or just start typing, to pin it. The first
  preview of each session says so once — "Preview tab — edit it or
  click again to keep it open" — because a tab that replaces itself
  silently reads as tabs going missing.
- **Scrollbars you can actually see** — long files get a clickable,
  draggable scrollbar on the editor's right edge: a solid block thumb
  on a shaded track, brightening while you drag it, with the file's git
  changes marked along it, so "where did I change this file" is one
  glance (and one click) away. The file tree grows the same bar on the
  sidebar's edge whenever the listing outgrows the panel.
- **Editing that keeps up** — `Enter` opens the new line with the same
  indentation the old one had, plus one level after an opening `{`,
  `[`, `(` (or a trailing `:` in Python / YAML). `Alt+←` / `Alt+→`
  move the caret a word at a time (`Shift` extends the selection), and
  the bracket under the caret is highlighted together with its
  partner — `Esc %` jumps between them. An unmatched bracket is marked
  in the error color rather than passed over in silence.
- **Respects the file's conventions** — the indent unit is read off the
  file itself (tab vs N spaces), and a file that arrived with CRLF
  line endings is written back with CRLF, so editing one line of a
  Windows-authored file doesn't turn the whole file into a diff.
- **CJK, emoji and combining marks line up** — column math is done in
  grapheme clusters and terminal cells, not runes: an ideograph takes
  two columns, a combining mark none, a ZWJ family emoji two rather
  than six. One arrow press steps over a whole cluster (so does one
  `Backspace`), soft wrap never breaks a wide glyph across rows, and
  selections, the find highlight and the gutter stay aligned with what
  you see. Widths come from the same Unicode engine `tcell` uses to
  size a cell, so the caret can't drift away from the glyph under it.
- **File clipboard** — cut, copy, paste, and duplicate files or folders
  from the tree's right-click menu or the main `≡` menu. Nothing is
  ever overwritten: a taken name becomes `name copy.ext`.
- **Session restore** — reopening a project brings back your open tabs
  (cursor and scroll included), expanded folders, and sidebar exactly
  as you left them. State lives in one file per project under
  `~/.local/state/skiff/sessions/` (`$XDG_STATE_HOME` when set), so a
  corrupt or half-written file can only ever cost you that one
  project's tab list.
- **Themes with live preview** — `≡` → **Theme…** opens a picker
  that restyles the whole editor as you arrow (or hover) through the
  list; type to filter ("cat" → the Catppuccins), `Enter` keeps,
  `Esc` puts your old theme back. The choice persists to
  `~/.config/skiff/config.json` (`{"theme": "dracula"}`), the same
  tiny file the Nerd-Font icons preference lives in. Tokyo Night
  stays the default.
- **Toggleable, draggable sidebar** — show/hide the file tree from the
  menu (or `Esc t`), or drag the splitter to resize it. Below 58
  columns — too narrow to hold an 18-column tree and 40 columns of
  code — the explorer hides itself and says so ("Narrow window — file
  explorer hidden (Esc t shows it)"), then comes back when the window
  grows. It only restores what *it* hid: a panel you closed on purpose
  stays closed, and reopening it inside a narrow pane sticks.
- **The tree hides what the project ignores** — entries excluded by the
  project's `.gitignore` files are filtered out of the sidebar, so it
  and the finder agree on what counts as noise. `≡` → **Show ignored
  files** / **Hide ignored files** flips it and persists
  (`{"gitignore": "off"}` in the same config file). Two exemptions, both
  on purpose: **no** dot-prefixed name is ever filtered by it, so an
  ignored `.next/` or `.venv/` stays visible right alongside `.env` and
  `.github` — dotfile visibility is a separate axis, not something
  `.gitignore` gets a vote on — and a file you have open in a tab is
  never hidden, so its ignored folder reappears holding just the file
  you're editing. Symlinked directories expand like real ones and are
  marked `→`; a link that points back onto its own ancestors shows
  `→ (link loop)` and refuses to open.
- **Clipboard over SSH** — OSC 52, including a `tmux` passthrough so
  copy works from inside a tmux session on a remote host. One escape
  sequence can only carry so much: past 512 KiB (tmux's 1 MiB ceiling
  minus base64 inflation) the copy is refused with "Selection too large
  for the terminal clipboard — copied inside skiff only" rather than
  half-written, and pasting inside the editor still works.
- **Survives a low-color terminal** — under 256 colors the palettes
  stop being distinguishable, so skiff spends attributes instead of
  hue: reverse video for the selection and the status bar, bold for the
  active tab and dirty markers, underline for the current find match.
  Plain `TERM=xterm`, a serial console, and a `tmux` started without
  `-2` all stay readable.
- **Format on save** — opt-in per-project via `.skiff/format.json`
  with a first-run trust prompt so cloning a repo never silently
  executes its commands. See [Format on save](#format-on-save).
- **Single binary, no CGO** — cross-compiled for macOS and Linux
  (amd64 + arm64) and Windows (amd64).

<img width="2504" height="1726" alt="CleanShot 2026-04-29 at 23 32 22@2x" src="https://github.com/user-attachments/assets/d0dca3da-5ba7-474d-852e-832acde90ca4" />

## Install

### macOS / Linux (Homebrew)

```sh
brew install johnlam90/skiff/skiff
```

That's it — brew resolves the
[johnlam90/homebrew-skiff](https://github.com/johnlam90/homebrew-skiff)
tap automatically. (If you tried an older install command and it left a
broken tap behind, `brew untap johnlam90/skiff` first.)

### Updating

When a new release ships:

```sh
brew update
brew upgrade johnlam90/skiff/skiff
```

### Uninstalling

```sh
brew uninstall johnlam90/skiff/skiff
brew untap johnlam90/skiff
```

### Linux (one-line install script)

The simplest way to drop Skiff onto a Linux box (or any macOS that
isn't using Homebrew) is the install script:

```sh
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | sh
```

It detects your OS / arch, downloads the matching archive from the
latest [GitHub Release](https://github.com/johnlam90/skiff/releases),
verifies it against that release's published `checksums.txt`, and drops
the `skiff` binary into `~/.local/bin` (or `/usr/local/bin` when
`~/.local/bin` isn't writable). **Re-run the same command to
upgrade** — it always fetches the latest tagged release. A missing
checksum entry, a mismatch, or a host with no sha256 tool aborts the
install; there is deliberately no way to skip verification, because
this is remote code about to land on your `$PATH`. The checksum proves
the download matches what the release published — integrity, not
authenticity. When `cosign` is installed, the script also verifies the
release's keyless signature over `checksums.txt`, confirming it was
published by this repo's own release workflow.

Override behaviour with environment variables:

```sh
# Pin to a specific release.
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh \
  | VERSION=v0.0.18 sh

# Install to a custom directory.
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh \
  | INSTALL_DIR=/opt/bin sh
```

The script is plain POSIX `sh` — it works on Alpine / BusyBox / any
SSH target where you don't want to depend on bash. It needs `tar`, one
of `curl` or `wget`, and one of `sha256sum` or `shasum`.

### Other platforms (manual binary install)

Pre-built binaries for Linux and macOS (amd64 + arm64) and Windows
(amd64) are attached to every
[GitHub Release](https://github.com/johnlam90/skiff/releases).
Download the archive for your OS/arch, extract it, and drop the
`skiff` binary somewhere on your `$PATH`.

### From source

```sh
git clone https://github.com/johnlam90/skiff.git
cd skiff
make install        # builds and installs to /usr/local/bin (may need sudo)
```

## Usage

```sh
skiff              # opens the current directory
skiff ~/code/app   # opens a specific project root
skiff main.go      # opens one file — single-file mode, no sidebar
skiff main.go:42   # …opened at line 42
skiff new-file.go  # creates the file on first save (vim-style)
skiff --version    # print version and exit
skiff --help       # print short usage
```

One directory or one file, and a flag has to come first. A second path
is refused by name (`skiff: unexpected argument: "b.go"`) rather than
quietly dropped — open the rest from the tree or the finder (`Esc p`)
once the editor is up. `skiff main.go` deliberately skips the file tree
and the project index entirely: you asked for one file, so Skiff never
walks the directory around it. Point it at a directory when you want the
sidebar.

Then:

- Click a file in the tree to open it.
- Click a tab to switch, click the `×` to close it.
- Click `≡` (top-left), right-click anywhere, or double-tap `Esc`
  for the action menu — including New file, Rename, Delete — then type
  to filter it down.
- If your terminal forwards Button3, right-click on a file or folder
  in the tree opens a per-item context menu (New File on folders,
  Rename, Delete). macOS Terminal + tmux often swallows right-click,
  and [herdr](https://herdr.dev) reserves plain right-click for its own
  pane menu (its `right_click_passthrough_modifier` setting can forward
  modified right-clicks) — so all of those actions also live in the
  main `≡` menu.
- Drag the splitter between the sidebar and editor to resize.
- Click and drag in the editor to select; drag past the top or bottom
  edge to auto-scroll the selection. Releasing the drag copies the
  selection (the tmux convention) — with mouse reporting on, your
  terminal never has a selection of its own, so `Cmd+C` at the
  terminal level would grab nothing. `Esc c` copies too.

### Terminal size

Skiff needs **40 columns × 10 rows**. Below either, the whole screen
becomes `Window too small — please resize`, with the size you have and
the size it needs on the line underneath, and nothing else is painted
until the window grows back. No state is lost — the editor just refuses
to draw a layout it can't fit.

Neither number is round. Ten rows is the shortest the tallest dialogs
get: the unsaved-changes, confirm and single-line prompt modals bottom
out at 9 rows each — unlike the menu or a file picker, none of them has
anything left to window away — plus the one status-bar row underneath,
which is where the dialog's own outcome gets reported.
Forty columns is the widest fixed button row (`[ Cancel ]`
`[ Discard ]` `[ Save ]` is 29 cells of label and can't squeeze under
33) plus enough label column for the `≡` menu's rows to stay
identifiable. 40×10 is also a phone in landscape with the soft keyboard
up, which is the smallest real terminal Skiff targets.

### Hotkeys

Skiff deliberately avoids `Ctrl+`-style shortcuts (they fight `tmux`,
`zellij`, and the terminal itself — `Ctrl+S` is XOFF flow control on a
real terminal). Instead, **`Esc` is the leader key**: tap `Esc`, then
within half a second tap one of the keys below.

| Combo       | Action                 | Group |
| ----------- | ---------------------- | ----- |
| `Esc Esc`   | Open ≡ menu            | —     |
| `Esc s`     | Save                   | File  |
| `Esc n`     | New file               | File  |
| `Esc w`     | Close tab              | File  |
| `Esc o`     | Reopen closed tab      | File  |
| `Esc u`     | Undo                   | Edit  |
| `Esc r`     | Redo                   | Edit  |
| `Esc c`     | Copy selection         | Edit  |
| `Esc x`     | Cut selection          | Edit  |
| `Esc v`     | Paste                  | Edit  |
| `Esc /`     | Toggle line comment    | Edit  |
| `Esc k`     | Move line up           | Edit  |
| `Esc j`     | Move line down         | Edit  |
| `Esc d`     | Duplicate line         | Edit  |
| `Esc f`     | Find in file           | Go    |
| `Esc F`     | Find in project        | Go    |
| `Esc l`     | Go to line             | Go    |
| `Esc p`     | Find file in project   | Go    |
| `Esc b`     | Move to previous word  | Go    |
| `Esc e`     | Move to next word      | Go    |
| `Esc %`     | Go to matching bracket | Go    |
| `Esc g`     | Focus the Git panel    | Git   |
| `Esc t`     | Toggle sidebar         | View  |
| `Esc z`     | Toggle line wrap       | View  |
| `Esc ?`     | Keyboard shortcuts     | View  |
| `Esc q`     | Quit                   | Quit  |

The rows are in `leaderBindings()`' own order, and the Group column is
its `group` field — the same six headings the ≡ menu uses, so "where
does this live?" has one answer.

**Forgot one? `Esc ?`** opens the whole table as a scrollable overlay,
grouped exactly as above, plus a short note on the ≡ menu and its
filter. It's *generated* from the dispatch table (`≡` →
**Keyboard shortcuts…** is the same row), so it can't drift into
advertising a gesture that no longer fires.

A lone `Esc` is harmless — if you don't follow it with a bound key
within the window, your next keystroke goes to the editor as normal,
so accidental `Esc` taps never swallow a real character. And while the
window is armed, a one-row cheat-strip above the status bar lists every
key that works right now — no memorizing required.

Under `tmux`, a fast `Esc s` often reaches the editor as `Alt+s`;
skiff treats the two as the same gesture, so the table works either
way — including while the ≡ menu is up, where a bare `s` types into
the filter instead and `Alt+s` still saves.

Two motions don't need the leader at all: `Alt+←` / `Alt+→` move by
word (`Shift` extends the selection), and `Enter` indents the new line
to match the one it split.

Everything reachable by hotkey is also reachable from the `≡` menu —
the hotkeys are just a faster path for the actions you reach for most.

### Find in file (and replace)

`Esc f` (or **Find in file** from the `≡` menu) opens a search bar
above the status bar:

```
 Find: foo█                       3 of 12   Enter: next · Shift+Enter: prev · Esc: close
```

- Type to search — matching is **smart-case substring**: an
  all-lowercase query matches any case, a single uppercase letter makes
  it exact (`id` finds `ID` and `id`; `ID` finds only `ID`). Results
  highlight live as you type.
- `Enter` jumps to the next match (wraps at the end), `Shift+Enter`
  jumps to the previous one.
- `Tab` grows the bar a **replace** field (`Find: foo ⇒ bar`): `Enter`
  replaces the current match and walks forward, `Shift+Enter` replaces
  every match in the file as one undo step, `Tab` hops back to the
  query.
- `Esc` closes the bar and clears the highlights — each `Esc f` opens
  a fresh search.
- The active match is painted a brighter color than the rest, so you
  can pick out where you are in the result set.

There's no regex or whole-word toggle in v1, and case needs no toggle —
smart case reads it off how you type. The common case is "I know roughly
what I'm looking for, take me there."

### Find file in project

`Esc p` (or **Find file in project** from the `≡` menu) opens a
fuzzy file finder over every non-ignored file in the project:

```
┌ Find file                                                    esc ┐
│  app.go                                              50/12345    │
│  internal/app/app.go                                             │
│  internal/app/app_test.go                                        │
│  internal/finder/score.go                                        │
│  ...                                                             │
└──────────────────────────────────────────────────────────────────┘
```

- Type to fuzzy-match. The matcher prefers basename hits, consecutive
  matches, and word boundaries — typing `tab` finds `tab.go` before
  `tabs/foo.go` before `notable.go`.
- `↑` / `↓` to move, `Enter` to open, `Esc` to dismiss. Mouse hover
  highlights, click opens.
- Honours `.gitignore` automatically. The fast path uses
  `git ls-files --cached --others --exclude-standard` (so a 50k-file
  repo indexes in ~150ms); non-git projects fall back to a Go
  walker that still respects the project root's `.gitignore`.
- Indexed in the background at startup so the modal opens with
  results already in hand. Refreshes on the same 10-second cadence
  as the file tree, plus immediately after any create/rename/delete
  inside the editor.
- Only files are listed — no directories, no symlinked duplicates.

### Git changes

The sidebar has two tabs: **EXPLORER** and **GIT**. Click `GIT` (or
press `Esc g`, pick **Git changes** from `≡` → **Git…**, or click the
branch segment in the status bar) and the sidebar flips to the
uncommitted-changes list — VS Code's Source Control view, shrunk to a
Skiff panel:

```
 EXPLORER   GIT
 ⎇ main ↑1
 [ Commit ] [ Push ] [ Pull ] [ ⋯ ]
 ● M app.go  internal/app
 ● A gitchanges.go  internal/app
 ○ D old.go  internal/app
```

- Every changed path in the project, sorted, with a colored status
  letter: **M** modified, **A** added/untracked, **D** deleted,
  **R** renamed — the same colors the file tree uses. The file name
  leads and the directory trails dimmed, so the narrow sidebar stays
  scannable.
- **Drive it from the keyboard.** `Esc g` (and the menu route) hands
  the keyboard to the panel: `↑`/`↓` walk the rows, `Space` toggles the
  selected row's commit checkbox, `Enter` opens its diff, `Tab` (or
  `←`/`→`) moves between the row list and the action buttons, `Enter`
  runs the focused one, and `Esc` gives the keys back to the editor.
  While it's armed, a hint strip docks at the bottom of the panel
  naming the bindings and the focused button's verb — which is what
  makes the compact `[✓][↑][↓][⋯]` ladder decodable on a narrow
  sidebar. No `Ctrl+` anything, so it works over SSH inside tmux where
  Button3 never arrives.
- **Click a row to see its diff** — side-by-side on a wide terminal
  (old text left, new text right, line numbers on both, changes
  aligned row-for-row like VS Code's diff editor), automatically
  adapting to the classic unified view when the window is too narrow
  for two readable columns. Resizing the terminal reflows the open
  diff live. On paired modifications the exact characters that
  changed are highlighted within the line, and context lines get full
  syntax highlighting. Scroll with the trackpad/wheel or
  `↑`/`↓`/`PgUp`/`PgDn`; long lines scroll sideways with
  Shift+wheel or `←`/`→`. The `[ Open file ]` button (focused by
  default, so `Enter` works too) opens the file with the cursor
  parked on its first changed line; from there the gutter bars mark
  each hunk — and clicking a gutter bar opens this same diff view for
  that hunk. **Diff this file** in the `≡` menu shows the active
  tab's own diff without a trip through the panel. Both of those read
  git on a background goroutine — the click flashes "Loading diff…"
  and the view appears when git answers, so a slow or network-mounted
  repo can't freeze the editor on the way there.
- The `GIT` tab wears a change-count badge (`GIT 4`), and git status
  collection runs on a background goroutine — a slow `git status` on
  a huge or network-mounted repo can never stall typing.
- The status bar shows ` main ↑2 ↓1 · 4 ` when the branch has
  diverged from its upstream — the "you haven't pushed" nudge, before
  you quit the editor.
- **Commit straight from the panel.** Every row carries a checkbox
  (`●` in, `○` out — click to toggle; everything starts checked).
  `[ Commit ]` asks for a message and commits exactly the checked
  files; anything already staged in a shell stays out of it. The
  same flow lives at `≡` → **Commit changes…**.
- **Push / Pull / Fetch.** `[ Push ]` pushes the branch — a branch's
  first push sets the upstream automatically. A rejected push gets a
  one-click fix offered ("origin has commits you don't — pull, then
  push?") instead of a wall of stderr. `[ Pull ]` fast-forwards;
  anything needing a real merge fails fast with a plain-language
  explanation. Fetch lives under `[ ⋯ ]`.
- **Branches.** Click the branch line (or `≡` → **Switch branch…**)
  to pick any local or remote branch — picking `origin/x` creates the
  local tracking branch the way you'd expect. **New branch…** is
  under `[ ⋯ ]`, next to **Stash changes**, **Pop stash**, and
  **Undo last commit** (a soft reset: the commit disappears, its
  changes stay in your tree).
- **Compare against any ref.** `[ ⋯ ]` → **Compare against…** points
  the *whole editor* at another branch: tree tint, gutter bars, the
  panel's list and every diff show what changed versus that ref —
  "review this branch against main" as a mode. The status bar shows
  `⇆ main` while it's on; pick **HEAD** to come back. Committing is
  deliberately disabled in this mode (the index is always HEAD's).
- **Walk a review with the arrows.** With a panel-opened diff up,
  `↓`/`↑` jump straight to the next / previous changed file — read
  the whole change-set end to end without touching the mouse. Every
  git mutation runs on a background goroutine (never blocking typing),
  one at a time, and refreshes the panel, tree tint, and gutter marks
  when it lands.
- **Commit history** (`≡` menu, or under the panel's `[ ⋯ ]` button)
  lists recent commits — SHA, subject, relative age — and a
  click opens that commit's full diff, with per-file boundary rows
  for multi-file commits. **History of this file** does the same
  scoped to the active tab (with `--follow`, so renames don't
  truncate the story). Both are read-only — rewriting old history
  stays in your shell.
- An **untracked** file shows its whole content as an added diff. A
  **deleted** file shows what was removed, with no open button — there
  is nothing left to open. An untracked **directory** flips back to
  the explorer and reveals itself.
- The status bar shows ` branch · N ` while anything is uncommitted;
  clicking it is the mouse-first way in from anywhere.
- The list refreshes live on the same 10-second cadence as the file
  tree, so a `git checkout` in the next tmux pane empties it on its
  own. Click `EXPLORER` in the sidebar header to switch the sidebar
  back to the tree.

Staging is per-commit and lives in the panel's checkboxes rather than
a persistent index: skiff never runs `git add`, so anything you staged
in a shell stays exactly as you left it. Rewriting history stays in
your shell too — the log views are read-only.

### When a file changes under you

The pane next door running `git pull` or `sed -i` is the normal case
this editor was built for, so a file changing on disk is handled
explicitly rather than flashed at you:

- **Clean buffer** — the tab silently reloads on the next 10-second
  tick. Nothing to decide.
- **Dirty buffer** — a three-way prompt: **Keep mine** (your buffer
  wins on the next save), **Reload** (take what's on disk and drop
  your edits), or **Diff** (your buffer against the bytes on disk, in
  the ordinary diff viewer, so you can answer "whose change matters?"
  before choosing). Focus starts on Keep mine, so a reflex `Enter`
  can't discard work.
- The status bar keeps a `⚠ disk conflict` marker until the conflict is
  actually resolved — dismissing the prompt doesn't make it go away —
  and `≡` → **Resolve disk conflict…** reopens the prompt. Looking at
  the diff is not resolving; saving or reloading is.
- **Deleted file** — the tab is flagged once and left alone.

Deleting a file that has unsaved changes in an open tab prompts first
(naming the files, or how many there are for a folder delete) instead
of discarding the buffer with the file.

## Custom actions (open remote files on your laptop)

[![Watch the walkthrough](https://img.youtube.com/vi/vDWZWEmIiZ8/maxresdefault.jpg)](https://www.youtube.com/watch?v=vDWZWEmIiZ8)

> 📺 [Custom actions walkthrough on YouTube](https://www.youtube.com/watch?v=vDWZWEmIiZ8)

Skiff can read user-defined shell-out actions from
`~/.config/skiff/actions.json` and prepend them to the action menu.
Each action runs against the **currently open file** when you click it.

The use case this was built for: you SSH from your laptop into a remote
box, edit a file there, and want to *open it on your laptop* — but
neither Sixel nor the Kitty graphics protocol survive the trip through
zellij/tmux. The trick is to bypass the terminal entirely and pipe the
file back over a second SSH connection.

### File location

`~/.config/skiff/actions.json` (or `$XDG_CONFIG_HOME/skiff/actions.json`
when set). The file is optional — without it, the menu just shows the
built-in actions.

### Schema

```json
{
  "actions": [
    {
      "label": "Open on Rager",
      "command": "scp \"$FILE\" rager:~/Downloads/ && ssh rager open \"~/Downloads/$FILENAME\""
    },
    {
      "label": "Open on Cascade",
      "command": "scp \"$FILE\" cascade:~/Downloads/ && ssh cascade open \"~/Downloads/$FILENAME\""
    }
  ]
}
```

Each entry needs:

- **`label`** — the menu text (kept under ~30 chars; long labels clip
  inside the modal).
- **`command`** — handed to `sh -c` with the editor-state env
  variables exported:
  - `FILE` — absolute path of the active tab's file (empty with no tab)
  - `FILENAME` — basename of the same file
  - `PROJECT_ROOT` — absolute path of the project root
  - `ACTIVE_FOLDER` — absolute path of the sidebar's active folder
  - `ACTIVE_FOLDER_REL` — that folder relative to `PROJECT_ROOT`
  - `CURRENT_FILE` / `CURRENT_FILE_REL` — `FILE`, absolute and relative
- **`prompts`** *(optional)* — input fields collected in a form modal
  before the command runs; each value is exported under its own `key`.
  See [the docs page](https://johnlam90.github.io/skiff/docs/custom-actions/).

> **`$HOME` and `~` gotcha for two-hop SSH:** the command runs in a
> shell on the *Skiff host* (the remote box you SSH'd into). So
> `$HOME` and `~` outside of `ssh "..."` quotes expand to *that* box's
> home directory, not your laptop's. To run something on your laptop,
> wrap the remote command in quotes: `ssh rager "open ~/Downloads/$FILENAME"` —
> `$FILENAME` is expanded locally (you want that — it's a filename),
> but `~` is sent literally and rager's shell expands it on arrival.

Actions are always enabled, with or without a file open — Skiff doesn't
guess from the command string which ones need `$FILE`. Commands run in a
background goroutine, so a slow `scp` or hanging `ssh` won't freeze
the editor. A quick, silent run just flashes in the status bar; a
failure always opens a modal with the captured stderr (a one-line
flash truncated exactly the diagnostics you needed), and so does a
success that printed something or took longer than a second — with a
pointer to the full log below.

### Quote every variable

`command` goes to `sh -c` with those variables in the environment, so
**the shell** expands them, not Skiff. An unquoted `$FILE` is word-split
on spaces and then glob-expanded before your program sees one argument.
That shell power is the point — `actions.json` is your own file, read
only from `$XDG_CONFIG_HOME/skiff/actions.json`, so a cloned repo can't
plant one — but it bites on the most ordinary input there is: a path
with a space.

- Write `"$FILE"`, never bare `$FILE`. Same for every variable above,
  and for every prompt value (`"$DEST_DIR"`).
- Unquoted, a value splits on spaces/tabs/newlines and each piece is
  then matched as a glob, so `draft [2].md` breaks too.
- Quote the variable, not the tilde: `cp "$FILE" ~/backup/` works;
  `"~/backup/"` looks for a directory literally named `~`.
- Quote command substitutions as well: `cd "$(dirname "$FILE")"`.

With `/home/spicer/My Notes/todo.md` open:

```sh
cp $FILE ~/backup/     # WRONG — cp gets "/home/spicer/My" and "Notes/todo.md"
cp "$FILE" ~/backup/   # RIGHT — one argument, spaces and all
```

### Debugging — every run is logged

Every custom-action invocation appends a record to
`~/.local/state/skiff/actions.log` (or
`$XDG_STATE_HOME/skiff/actions.log` when set). One entry per run,
human-readable, with the exact command, the env vars that were
exported, the duration, and the combined stdout / stderr:

```
[2026-04-30T13:26:32-07:00] Open on Rager (1.234s) → ok
  command: scp "$FILE" rager:~/Downloads/ && ssh rager open "$HOME/Downloads/$FILENAME"
  FILE:     /Users/spicer/dev/foo/bar.txt
  FILENAME: bar.txt
  --- output ---
  --- end ---

[2026-04-30T13:27:01-07:00] Open on Cascade (0.521s) → exit status 1
  command: scp "$FILE" cascade:~/Downloads/ && ssh cascade open "$HOME/Downloads/$FILENAME"
  FILE:     /Users/spicer/dev/foo/bar.txt
  FILENAME: bar.txt
  --- output ---
  ssh: connect to host cascade port 22: Connection refused
  lost connection
  --- end ---
```

`tail -f ~/.local/state/skiff/actions.log` while you click around
to watch entries roll in. There's no rotation — the file is one-line
per run plus a few lines of output, so it grows slowly. Delete it
whenever you want to start fresh.

### The "open on my laptop" workflow

Both example actions assume `rager` and `cascade` are SSH host aliases
in the **remote** machine's `~/.ssh/config` that resolve back to your
laptop. The simplest way to set that up:

1. **On your laptop**, generate (or pick) an SSH key pair you'll
   dedicate to inbound connections from your remote work box.
2. **On your laptop**, make sure Remote Login is enabled (System
   Settings → General → Sharing → Remote Login on macOS) and add the
   public key to `~/.ssh/authorized_keys`.
3. **On the remote box**, drop the matching private key into
   `~/.ssh/id_<name>` and add a host alias:

   ```sshconfig
   Host rager
     HostName your-laptop.example.com   # or a Tailscale / mesh hostname
     User your-mac-username
     IdentityFile ~/.ssh/id_rager
   ```

4. Test it by hand from the remote: `ssh rager echo hi`. Once that
   works, Skiff can drive it the same way.

If your laptop sits behind NAT, point `HostName` at a Tailscale /
WireGuard / Cloudflare-tunnel address — anywhere the remote can reach
the laptop directly. The action itself is just `scp` + `ssh`; it
doesn't care how the network gets there.

### Anything else `sh` can do

The schema is deliberately small. If you can write it on one shell
line, you can put it in `actions.json`:

```json
{ "label": "Send to ChatGPT", "command": "cat \"$FILE\" | pbcopy && open https://chat.openai.com/" }
{ "label": "Lint with eslint", "command": "cd \"$(dirname \"$FILE\")\" && eslint \"$FILENAME\"" }
{ "label": "Run formatter",    "command": "gofmt -w \"$FILE\"" }
```

## Format on save

Skiff can run a formatter on every save — `gofmt`, `php-cs-fixer`,
`prettier`, anything you like — but the feature is **off by default**
and only kicks in for projects that opt in by checking in a config
file. Quick edits to a stranger's repo will never silently rewrite
their files.

### Setup

Create `.skiff/format.json` in your project root:

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "php": ["php-cs-fixer", "fix", "$FILE", "--quiet"],
    "py":  ["ruff", "format", "$FILE"],
    "js":  ["prettier", "--write", "$FILE"],
    "ts":  ["prettier", "--write", "$FILE"]
  }
}
```

- Keys are file extensions, **without** the leading dot.
- Values are argv arrays — passed straight to `execve`, no shell, so
  there's no injection surface. (Use `["sh", "-c", "..."]` if you
  genuinely need a shell.)
- `$FILE` in any argument is replaced with the absolute path of the
  file being saved.

### First save: trust prompt

The first time Skiff would run a formatter from a new (or edited)
`.skiff/format.json`, you get a Yes / No prompt that spells out
exactly what it is asking for:

> **Trust this project's formatter?**
> Allow .skiff/format.json to run these commands on save?
>
> `  .go  gofmt -w $FILE`
> `  .ts  prettier --write $FILE`

Every declared extension and its full argv is listed, written the way
`format.json` writes it (`$FILE` unexpanded — that's the text the
trust hash covers). Without the list, a Yes would be consent to
something you were never shown.

Pick **Yes** once and Skiff will run the configured formatters
silently from then on. Pick **No** (or `Esc`) and it will never run
them in this project — until the config file changes, at which point
you'll be prompted again. The remembered answer (and the SHA-256 hash
of the config it applies to) lives in
`~/.config/skiff/format-trust.json`.

The hash is the security trick: a teammate can't push a "v2" of the
config that runs `rm -rf` — your editor will re-prompt the next time
you save, because the file has changed since you trusted it.

One shape is refused outright, trusted or not: `argv[0]` must be a
bare program name resolved on `PATH` (`gofmt`, `prettier`), never a
path. `["./tools/fmt", "$FILE"]` or `["/opt/x/fmt", "$FILE"]` turns
"trust the commands you just read" into "trust every byte in this
clone", and the prompt can only show you the path, not the code behind
it. Skiff reports the offending entry and skips the whole config.

### What happens on save

1. Save writes the file to disk first. A broken formatter never
   blocks the save.
2. Skiff looks up the file's extension in `format.json`. No
   match → done.
3. The configured command runs in a goroutine. Slow formatters don't
   freeze the UI; you can keep typing.
4. When the formatter finishes, Skiff reloads the buffer — but
   only if you haven't typed anything since saving. If you did, your
   in-flight edits win and a status flash tells you the on-disk file
   was reformatted.
5. If the configured binary isn't installed, it's a silent no-op.
   You don't have to install everyone's formatter to clone the repo.

### Sharing vs. ignoring

Two reasonable patterns:

- **Commit `.skiff/format.json`** so everyone on the team gets
  the same format-on-save behavior automatically.
- **Add `.skiff/` to `.gitignore`** if developers prefer their
  own setups — each person's local copy can configure whatever
  formatters they like.

Both work. Skiff doesn't care which you pick.

### Personal defaults — the install prompt

You can list your favorite formatters once globally in
`~/.config/skiff/format-defaults.json` (same shape as the
project file):

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "php": ["php-cs-fixer", "fix", "$FILE", "--quiet"],
    "py":  ["ruff", "format", "$FILE"]
  }
}
```

These never run on their own. Instead, when you save a file in a
project where:

1. The project's `.skiff/format.json` is missing or has no
   entry for that file's extension, **and**
2. Your global defaults *do* have an entry for that extension,

…Skiff asks once: **"Add `gofmt` for `.go` to `.skiff/format.json`?"**

- **Yes** — merges the entry into the project's config (creating
  `.skiff/format.json` if it didn't exist), auto-trusts the
  resulting file, and runs the formatter on the save you just made.
- **No / Esc** — remembered per-extension in the trust file. You
  won't be re-asked about that file type in this project until you
  manually edit the project config.

This keeps your personal preferences out of repos that don't want
them while still making it one click to opt a project in.

## Project layout

```
.
├── main.go                   # Entry point — parses optional rootDir / file[:line]
├── internal/
│   ├── app/                  # Event loop, layout, menu, keys, mouse, drawing
│   ├── editor/               # Buffer, tab, cursor, wrap, indent, brackets, highlighting
│   ├── filetree/             # Lazy directory tree with identity-preserving refresh
│   ├── overlay/              # Overlay stack + the prefab floating surfaces
│   ├── git/                  # Git process boundary + the Snapshot model
│   ├── search/               # Literal smart-case project search engine
│   ├── finder/               # Project file index + fuzzy matcher
│   ├── session/              # Per-project session store
│   ├── atomicfile/           # Temp-file + fsync + rename writes for config/state
│   ├── clipboard/            # OSC 52 clipboard with tmux passthrough
│   ├── customactions/        # Loader for ~/.config/skiff/actions.json
│   ├── format/               # Format-on-save config + trust store
│   ├── userconfig/           # ~/.config/skiff/config.json (icons, theme, wrap, gitignore)
│   ├── icons/                # Nerd Font detection + per-file glyphs
│   ├── theme/                # the theme registry + the low-color fallback
│   └── version/              # Single-line version constant
├── .github/workflows/        # Test, auto-release, and Pages-deploy pipelines
├── .goreleaser.yml           # Cross-compile + brew formula config
├── Formula/                  # Homebrew formula (written by CI)
├── website/                  # Hugo + Tailwind site (johnlam90.github.io/skiff)
└── Makefile
```

## Development

```sh
make run          # build and run against the current directory
make build        # build to ./bin/skiff
make build-linux  # cross-compile a linux/amd64 binary
make test         # full suite with -race (same as CI)
make test-short   # quick iteration loop (-short, no race)
make coverage     # writes coverage.out + a browsable coverage.html
make lint         # gofmt + go vet + staticcheck (the same three CI gates)
make tidy         # go mod tidy
make clean        # rm -rf bin + coverage artifacts
```

Every PR runs the pipeline in
[`.github/workflows/test.yml`](.github/workflows/test.yml) on Linux +
macOS: `go mod tidy` verification, `go build`, `gofmt -l`, `go vet`,
a pinned `staticcheck`, then `go test -race ./...`. `make lint` runs
the three static gates locally. New code needs a corresponding
`_test.go` — see CLAUDE.md for the bar.

## Releases

Releases are fully automated. Every push to `main` runs the test
workflow first — a red suite, an unformatted file, or a staticcheck
finding blocks the tag — and then:

1. Reads `internal/version/version.go`.
2. If that file was hand-edited in the pushed commit, the version is
   used as-is (this is how you bump major or minor: edit the constant
   manually). Otherwise the patch number is auto-bumped and committed
   back to `main` with `[skip ci]`.
3. Tags `v<x.y.z>` and pushes the tag.
4. [GoReleaser](https://goreleaser.com/) cross-compiles for
   linux/darwin (amd64 + arm64) and windows/amd64, attaches archives
   to a GitHub Release, and pushes an updated formula into
   `Formula/skiff.rb` on this same repo.

No PAT, no separate tap repo — the default workflow `GITHUB_TOKEN` is
enough since the formula lives in the source repo.

## Contributing

Bug reports and feature requests go through the
[issue templates](https://github.com/johnlam90/skiff/issues/new/choose) —
they ask for the terminal/multiplexer details a TUI bug always needs. Code
contributions: see [CONTRIBUTING.md](CONTRIBUTING.md); security reports: see
[SECURITY.md](SECURITY.md).

## License

MIT — see [LICENSE](LICENSE).

Copyright © 2026 Cloudmanic, LLC.
