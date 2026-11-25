# Explorer drag-and-drop — design

Date: 2026-11-24
Status: approved (owner chose approaches B and C — "just do B and C";
design decisions delegated)

## Problem

Dragging a file from the desktop onto Skiff types its path into the open
buffer. The owner expects what VS Code and Zed do: drop a file or folder
on the explorer and it is copied into the project — whether Skiff runs on
the laptop or on a remote box, in Rex, Herdr, Ghostty and the rest.

## What a terminal actually delivers

A TUI never sees an OS drop. It sees whatever its terminal turns the
drop into, and there are exactly two shapes (checked 2026-11-24):

| Terminal | A drop arrives as | Position | Remote |
| --- | --- | --- | --- |
| Rex 0.1.0-preview.20 | bracketed paste of the path(s) | none | uploads first, pastes the remote path |
| Herdr app 0.1.16 | bracketed paste of quoted path(s) | none | uploads files; refuses folders |
| Ghostty 1.4.0 | bracketed paste of shell-escaped path(s) | none | pastes the laptop's path (unreadable remotely) |
| kitty ≥ 0.47 (iTerm2 has an implementation in its tree; not checked first-hand) | OSC 72 drag-and-drop protocol | cell x,y while hovering | file contents in-band, folders included |

Sources: Rex's own server API (`com.superlogical.terminal.upload_begin`:
"the terminal types their paths as if the user had pasted them") and a
probe run in a throwaway Rex session; Herdr's `defaults/AGENTS.md` and
changelog; Ghostty 1.4.0 release notes ("Parse (but do not implement) the
Kitty drag and drop protocol"); kitty's `docs/dnd-protocol.rst`.

Two facts shape everything below:

1. **A pasted path is byte-identical to a path the user pasted as text.**
   No marker, no pointer position.
2. **Rex answers the OSC 72 handshake (`t=q`) but cannot deliver a drop**
   — its client has no API to send hover or drop events to the server. A
   successful handshake is therefore not evidence of anything.

## Goal

Files and folders dropped on Skiff are copied into a project folder the
user can see and choose. Nothing is ever overwritten.

Non-goals: dragging out of Skiff; rearranging the tree by drag; dropping
text; opening a dropped file in a tab; cancelling a transfer in flight;
the protocol route on Windows or inside tmux/screen/zellij.

Anti-goal: **a text paste must never silently become a file copy.**
Pasting `/etc/hosts` — or a directory path — into a script and having
Skiff copy it into the repo is worse than not having the feature.

## Approaches considered

- **A. Copy straight away** — treat any pasted existing path as a drop
  and copy it into the active folder. Rejected: violates the anti-goal,
  and lands in the active folder rather than where the user pointed.
- **B. Drop, then one click (chosen)** — the same detection opens a
  placement bar; a click on a folder, or Enter, copies. Works in every
  terminal and inside tmux; costs one confirmation.
- **C. The OSC 72 protocol (chosen, alongside B)** — live hover
  highlight, exact-row drops, in-band transfer over SSH. None of the
  owner's three terminals delivers it today; kitty does, and libghostty
  has it in progress (ghostty PR #14536).

B and C are two inputs to one drop model, not two features.

## Design

### One drop model

A **drop** is "these sources, into this folder". Both routes resolve to
`filemanager.Import` on the existing `fileOp` job (Refuse), and both use
the same target model in the tree:

- `Tree.DropTarget` (absolute folder path, "" = none) paints that
  folder's row — or the root row — on `theme.Selection`.
- Target resolution from a cell: inside the tree list, the row's folder
  (a file row means its parent; the root row, the header and the blank
  area mean the project root). Anywhere else — or with the sidebar
  hidden — the active folder.
- **Spring-loading**: hovering a collapsed folder for `700ms` expands it.
- **Edge scroll**: hovering the first or last list row scrolls the tree
  one row every `120ms` while more rows exist that way.

When a drop starts and the sidebar is showing the Git panel, Skiff
switches it to the explorer. A hidden sidebar stays hidden.

### B — the paste route (`internal/droppath`, `internal/app/filedrop.go`)

`applyPaste` asks `droppath` to classify an editor-bound paste before
inserting it. A paste headed for an overlay or a strip is still text.

Classification (pure; the stat function is injected):

- Reject outright: empty, over `64 KiB`, or containing NUL.
- Readings, tried in order: shell-style tokens (backslash escapes,
  `'…'`, `"…"`, `file://` URIs); one raw path per line; the whole text
  as one raw path. On Windows backslash is a separator, not an escape.
- A reading qualifies when every element is an absolute path and
  **every** one exists (a permission error counts as existing — macOS
  privacy prompts must not turn a drop into typed text). At most `256`
  paths.
- **Foreign**: in an SSH session, when the token reading is all absolute
  paths under a desktop root (`/Users/`, `/Volumes/`, `/home/`,
  `/private/var/`, `/var/folders/`, `/mnt/`, `/media/`, `/run/media/`)
  and none exists, the paste is a drop from another machine that this
  terminal did not upload.

Anything else is text and is inserted exactly as before.

A qualifying paste opens the **placement bar** — an app-side overlay
(`WantsMotion` true), two rows above the status bar across the whole
window. (The buttons need 33 cells and the editor can be as narrow as 22
with the sidebar shown, so the bar cannot live in the editor's width;
the tree gives up the one row the bar covers.)

```
 Copy photo.png into internal/app/
 [ Cancel ] [ As text ] [ Copy ]   click a folder, or Tab then Enter
```

| Input | Effect |
| --- | --- |
| click a tree row | copy into that row's folder |
| click `[ Copy ]` | copy into the highlighted folder |
| pointer over a tree row | that folder becomes the highlighted target |
| Tab / Shift+Tab | move the focus between the buttons |
| Up / Down | step the target through the folder rows, and focus `[ Copy ]` |
| Right / Left | expand / collapse the target, and focus `[ Copy ]` |
| Enter | press the focused button — `[ As text ]` until the focus is moved |
| a typed character | "as text": insert the pasted text, then type the character |
| wheel or scrollbar over the sidebar | scroll the tree |
| Esc, `[ Cancel ]` | nothing happens |
| a second path paste | its paths join the pending drop |

> **Revised after review, 2026-11-24.** The first version copied on Enter
> and ignored every key but `t`. A reviewer showed "paste a path, press
> Enter for a new line" creating a file — the anti-goal, one reflex away —
> and a sentence typed after the paste losing its first dozen characters.
> The bar is now keyed for someone who is not looking at it: the default
> answer is "as text", typing means "as text", and a copy takes a click or
> a deliberate Tab / arrow first.

Keys delivered while a paste is in flight are buffered, not delivered, so
a second drop's own characters can never answer the bar. The foreign
state shows why the file cannot be read and offers only `[ Cancel ]` and
`[ As text ]`.

≡ → File gains **"Import path…"** — "Import path (in <folder>)…" below
the root, the New-file row's own form — visible with a tree: a prompt
whose value goes through the same classification, then straight to the
copy. It is the menu path the project rule requires, and the one that
works when dropping does not.

### C — the protocol route (`internal/dnd`, `internal/app/dnd.go`)

Skiff registers once at startup in project mode — `OSC 72;t=a;text/uri-list`
then `t=a:x=1;<machine id>` — and sends `t=A` from `Close`. **It never
sends the `t=q` query**: unknown OSCs are ignored by terminals, the paste
route is always on, and the protocol route simply activates if hover or
drop events ever arrive. Registration is skipped under `$TMUX`, `$STY`
and `$ZELLIJ`, on Windows, and in single-file mode.

A machine id is **always** sent — a random one when the platform has
none — and nothing is accepted before it has gone out: with no id a
terminal treats the editor as local, and a dropped path that also exists
on the remote machine would be copied from the wrong one.

Bytes in. `escTty` (the tty filter under tcell) gains a `dnd.Scanner`
that lifts `ESC ] 72 ; … ST` out of the input stream before tcell parses
it. **The scanner has no clock.** A message in progress is waited for
however long the stream stalls, because what an ordered stream delivers
after a stall is the rest of that message. It ends early only on
evidence in the stream — a C0 control byte, `ESC` not followed by `\`,
or a body past `16 KiB` (discarded up to its terminator, never released
as keystrokes) — and each of those fails the transfer in flight. The one
timed wait is the original lone-ESC window; `ESC ]` and longer prefixes
wait for their next byte, and an introducer that arrives in one piece
after a released ESC is still captured. Nothing is lifted between the
bracketed-paste markers.

> **Revised after review, 2026-11-24.** The first version abandoned a
> capture after `2s` of silence and released an undecided prefix after
> `60ms`. Over SSH both are ordinary network stalls: the remaining bytes
> then arrived as typed text in the open buffer, and the fetch finished
> "successfully" with a block missing.

Bytes out. `escTty.Write` takes a mutex, and tcell flushes a frame in a
single `Write`, so a request written from a worker can never land inside
a frame.

Routing (`dnd.Conn`). Chunk chains are tracked on the reader side. Hover
(`t=m`) and drop (`t=M`) are assembled and posted to the event loop; data
(`t=r`, `t=R`) is streamed to the worker over a bounded channel, which is
the backpressure.

On the loop: a hover is accepted when Skiff is in project mode, the
machine id has been sent, no overlay is up, no file op is running, and
the offer includes `text/uri-list`. Skiff replies `t=m:o=…` only when the
answer changes, moves the highlight, and flashes "Drop to copy into
<folder>". Leaving clears it. A drop is honoured only after a hover that
was accepted — the OS will not let a user drop anywhere else, so any
other drop event did not come from a drag.

On drop, one `fileOp` work function:

1. requests `text/uri-list`;
2. for each `file://` entry: if the terminal flagged the drop remote
   (`X=1`) or the path is not readable here, fetches it in-band —
   regular file, symlink (`X=1`), or directory (`X=handle`, breadth-first,
   one request outstanding at a time, handles closed as they finish) —
   into a staging directory; otherwise uses the local path;
3. `Import`s local paths (copy) and staged entries (move) into the
   target;
4. sends `t=r:o=<op>` **after** the copy, because on macOS dropped files
   are only guaranteed to exist while the drop is open: `0` when nothing
   landed, "copy" when only some did — a source told "move" deletes its
   originals — and the source's own operation when everything arrived.

Each message is one base64 unit (kitty encodes every block it reads by
itself, unpadded), so the decoder is flushed whenever a piece arrives
without the "more" flag.

Staging lives in `$XDG_STATE_HOME/skiff/drops/` (the session store's
state directory convention), not in the project and not in `/tmp`, which
is RAM on many Linux boxes.

Safety limits: entry names that are empty, `.`, `..`, or contain `/` or
NUL fail the drop; at most `100000` entries and `64` levels; `30s`
without a message fails it. Files land `0644`, directories `0755` — the
protocol carries no modes.

### `filemanager.Import`

`Import(srcs []string, dstDir string, move bool, progress func()) (Changeset, error)`

- `dstDir` must be a directory inside the root; sources may be anywhere.
- Each source lands under its own basename or the next free " copy" name.
- A folder is never imported into itself or a descendant — compared as
  files (`os.SameFile` over the resolved destination's ancestors), so
  the filesystem root and a differently-capitalised spelling on a
  case-insensitive volume are caught — and the project root is never a
  source.
- `move` is for sources the caller owns outside the project (staging);
  a source inside the root with `move` set is refused.
- One failed source does not stop the others: its partial destination is
  removed, the Changeset lists what did land, and the error joins the
  failures.

## Testing

- `droppath`, `dnd`, `filemanager.Import`: table tests, no screen.
- `dnd.Fetch` against a scripted terminal: local, remote file, remote
  tree, symlink, error replies, hostile names, silence.
- `escTty`: the scanner inside `Read`, with the clock seam.
- `app`: SimulationScreen tests for the bar, the tree highlight, both
  routes end to end, and the `…AtTheMinimumSize` / paints-every-cell
  fences.
- By hand: a paste simulated through Rex's `block.event` against the real
  binary; the protocol route against a scripted pty.

## Known gaps

- Ghostty over plain SSH cannot work until Ghostty ships the protocol:
  the file never leaves the laptop. Skiff says so instead of typing the
  path.
- The protocol route has not been exercised against a real terminal;
  none installed on the owner's machine delivers it.
- A drop while a prompt or the find bar has the keyboard is typed into
  it, as any paste is.
