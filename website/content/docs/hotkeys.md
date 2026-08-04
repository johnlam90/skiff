---
title: "Hotkeys"
metaTitle: "Skiff Hotkeys — The Esc-Leader Table"
metaDescription: "Skiff avoids Ctrl+ shortcuts that fight tmux. Esc is the leader. Tap Esc, then a letter inside half a second. The complete table, with rationale."
summary: "The complete Esc-leader table, plus rationale."
weight: 30
---

Skiff avoids `Ctrl+` shortcuts on purpose. They fight tmux. They fight Zellij. They fight your terminal — `Ctrl+S` is XOFF flow control on a real serial line, and modern emulators still honor it. They fight remote sessions where keystrokes hop through three layers of software.

So `Esc` is the leader. Tap `Esc`, then within half a second tap a bound letter. A lone `Esc` with no follow-up is a no-op — your next keystroke reaches the editor as normal, so accidental Escs never swallow a real character.

## The full table

| Combo       | Action                 |
| ----------- | ---------------------- |
| `Esc Esc`   | Open ≡ menu            |
| `Esc s`     | Save                   |
| `Esc u`     | Undo                   |
| `Esc r`     | Redo                   |
| `Esc w`     | Close tab              |
| `Esc o`     | Reopen closed tab      |
| `Esc q`     | Quit                   |
| `Esc n`     | New file               |
| `Esc t`     | Toggle sidebar         |
| `Esc z`     | Toggle line wrap       |
| `Esc /`     | Toggle line comment    |
| `Esc k`     | Move line up           |
| `Esc j`     | Move line down         |
| `Esc d`     | Duplicate line         |
| `Esc c`     | Copy selection         |
| `Esc x`     | Cut selection          |
| `Esc v`     | Paste                  |
| `Esc f`     | Find in file           |
| `Esc F`     | Find in project        |
| `Esc l`     | Go to line             |
| `Esc p`     | Find file in project   |
| `Esc b`     | Move to previous word  |
| `Esc e`     | Move to next word      |
| `Esc %`     | Go to matching bracket |
| `Esc g`     | Focus the Git panel    |

While the leader window is armed, a one-row cheat strip above the status bar lists every key that works right now, so there is nothing to memorize.

## Alt is the same gesture

Under tmux (and most terminals) a fast `Esc s` is coalesced into a single `Alt+s` event before Skiff ever sees it. Skiff treats the two as the same gesture, so every row in the table above also works as `Alt+<key>`. That matters most with the ≡ menu open: the menu focuses a type-to-filter field, so a bare `s` types into the filter while `Alt+s` still saves. Every menu row keeps printing its `Esc` combo either way.

## Editor keys (no Esc needed)

Standard movement and editing keys behave the way every editor since the Macintosh has trained you to expect:

| Key                  | Action                                                    |
| -------------------- | --------------------------------------------------------- |
| Arrow keys           | Move the cursor.                                          |
| Shift + arrow        | Extend the selection.                                     |
| `Alt+←` / `Alt+→`    | Move a word at a time. Shift extends.                     |
| Home / End           | Jump to the line start / end.                             |
| PgUp / PgDn          | Scroll a viewport.                                        |
| Tab / Shift+Tab      | Indent / dedent.                                          |
| Enter                | New line, indented to match the line it split.            |
| Backspace / Delete   | Remove a character or selection.                          |
| Mouse drag           | Select.                                                   |
| Double-click         | Select the word under the cursor.                         |
| Scroll wheel         | Scroll the panel under the mouse.                         |

`Enter` copies the current line's leading whitespace onto the new line, and adds one more indent level when the line ends with an opening `{`, `[`, `(` — or a `:` in a Python or YAML file. The indent unit is whatever the file already uses (tab, or N spaces), detected on open. One `Enter` is one undo step.

A word is `[A-Za-z0-9_]` — the same definition double-click selection uses, so the two never disagree about where a token starts.

## Git panel keys

`Esc g` hands the keyboard to the Git panel (the ≡ menu's **Git changes** row does the same). While it has focus:

| Key           | Action                                                    |
| ------------- | --------------------------------------------------------- |
| `↑` / `↓`     | Move the row selection.                                   |
| `Space`       | Toggle the selected row's commit checkbox.                |
| `Enter`       | Open the selected row's diff.                             |
| `Tab` / `←→`  | Move between the row list and the action buttons.          |
| `Enter`       | Run the focused action button.                            |
| `Esc`         | Hand the keyboard back to the editor.                     |

A hint strip docks at the bottom of the panel while it has focus, naming the bindings and the focused button's verb — which is how the compact `[✓][↑][↓][⋯]` button ladder stays decodable on a narrow sidebar. There are deliberately no `Ctrl+` bindings here either: this mode exists precisely for the SSH-into-tmux setups where Button3 and mouse reporting never arrive.

## Why clipboard *is* bound

`c`, `x`, and `v` look like they break the "your terminal already does that" rule, and they would — except Skiff runs with mouse reporting on at all times, so your terminal and any multiplexer never build a selection of their own. Cmd+C at the terminal level has nothing to grab. `Esc c` / `Esc x` / `Esc v` are the keyboard path; mouse users get select-to-copy on drag release, the tmux convention. Copy and Paste also live in the ≡ menu.

## Why not bind destructive ops

Rename, Delete, and Revert are deliberately unbound. They're destructive enough that a confirm modal is the right gate, and the menu's confirm flow makes the action a deliberate gesture instead of muscle memory.

## Double-tap Esc

The leader window for a bound letter is 500 ms. The window for the double-tap that opens the menu is wider — 1.2 s — because tmux's default escape-time munches a fast `Esc Esc` into one event and delivers a slow pair more than 500 ms apart, which made the gesture nearly impossible to land. A lone armed `Esc` is inert, so the wider window costs nothing: mash `Esc` and the menu appears.
