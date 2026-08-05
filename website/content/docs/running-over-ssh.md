---
title: "Running over SSH"
metaTitle: "Skiff over SSH — tmux, Zellij, OSC 52"
metaDescription: "Skiff was built for SSH workflows. tmux, Zellij, OSC 52 clipboard, install on a remote box, leave it running, reconnect tomorrow. Real workflow."
summary: "The workflow Skiff was built for."
weight: 100
---

This is the workflow Skiff was built for. Most terminal editors treat SSH as a degraded mode of the local case. Skiff is the other way around — every design decision optimizes for editing on a remote box from inside tmux or Zellij.

## The setup

On your laptop:

```sh
ssh remote-box                # or whatever host alias you use
tmux attach || tmux           # attach to or start a tmux session
skiff ~/code/some-project
```

That's it. Skiff is now running on the remote box, sharing the SSH channel with the rest of your terminal traffic.

## Install on the remote box

The one-liner installer works on every Linux you'll ever SSH into — Alpine, BusyBox, Debian, Ubuntu, RHEL, Arch:

```sh
curl -fsSL https://raw.githubusercontent.com/johnlam90/skiff/main/install.sh | sh
```

Plain POSIX `sh`. No bash. No GLIBC version pin — the binary is fully static, CGO-off. Drops `skiff` into `~/.local/bin` (or `/usr/local/bin` if `~/.local/bin` isn't writable).

## Why tmux / Zellij is the killer combo

You SSH into a box. You attach to tmux. Half your panes have agents working. One pane has Skiff. You disconnect, get on a plane, reconnect six hours later from a different machine, and everything is exactly where you left it. The agents are still there. Skiff is still there. The cursor is in the line you were last editing.

That workflow is impossible with a desktop IDE. It's annoying with VS Code Remote SSH (which spawns a node process on the host). It's the default with Skiff, because there's no state to lose — it's a TUI inside tmux.

## Terminal size

Skiff needs at least **40 columns × 10 rows**. Below either, the whole screen becomes `Window too small — please resize`, with the size you have and the size it needs on the line under it, and nothing else is painted until the window grows back. Nothing is lost — the editor just won't draw a layout it can't fit.

Both numbers are floors of what Skiff can actually paint, not preferences. Ten rows is the shortest the tallest dialogs get: the unsaved-changes, confirm and single-line prompt modals bottom out at 9 rows each — unlike the menu or a file picker, none of them has anything left to window away — plus the one status-bar row underneath, which is where the dialog's outcome is reported. Forty columns is the widest fixed button row (`[ Cancel ] [ Discard ] [ Save ]` is 29 cells of label and cannot squeeze under 33) plus enough label column for the `≡` menu's rows to stay identifiable.

## Skiff on a phone

A phone terminal app over SSH is a client Skiff supports on purpose, and 40×10 is chosen so it fits one.

**Portrait usually beats landscape**, which surprises people. Rotating to landscape buys columns, and columns are the thing Skiff has plenty of — the soft keyboard then eats the *rows*, which are what it's short of. An iPhone SE in portrait at the default font is about 40×22 and runs. The same phone in landscape with the keyboard up is closer to 92×13, and an Android one 80×10; both run now, and both were refused outright under the old 50×24 floor.

**The file explorer gets out of the way by itself.** Below 58 columns — 18 for the narrowest useful tree plus 40 for the editor — Skiff hides it and flashes "Narrow window — file explorer hidden (Esc t shows it)". A phone in portrait at any font you'd want to read code in lands well under that, so the default there is a full-width editor; `Esc t` brings the tree back, and that override sticks through the next resize instead of being undone by it.

**Type to filter the menu instead of thumb-scrolling it.** At 40×10 the action modal clamps to the screen and gets eight rows: its border, a title row, the filter field, a divider, three rows of actions, and a `▼` sitting in the bottom border to say more exist below (`▲` appears in the divider once you have scrolled past the top). The status bar stays visible underneath. Three rows at a time is a miserable way to walk a menu; typing is not. The filter runs across every group at once — File, Edit, Go, Git, View, your custom actions and Quit become one flat match list while a query is typed — and matches are ranked: whole-label prefix first, then word prefix ("comment" → **Toggle line comment**), then a substring anywhere, then a loose subsequence ("tlc" finds the same row). `Enter` runs the best-ranked match, not whichever row happens to sit highest in the table, so two or three letters is usually the whole gesture. When the frame has to narrow far enough to clip a label, moving onto the row flashes it in full on the status bar. The git verbs and the file-clipboard actions live one level down, behind **Git…** and **File clipboard…** — those picks have a type-to-filter field of their own (plain substring), so the pattern holds after the drill-in.

**There are no `Ctrl+` bindings anywhere in Skiff**, which matters more here than on a laptop: a soft keyboard often has no Ctrl key at all, and the ones that do put it behind a modifier row you tap first. `Esc` is the only modifier — tap it, then a letter. `Esc ?` prints the whole table, generated from the same dispatch table the keys run through.

**Two `Esc` taps open the menu, and the window is a deliberate 1.2 seconds.** Under tmux's default escape-time a *fast* pair is munched into a single `Esc` event before Skiff ever sees two, and a pair slow enough to survive that arrives more than half a second apart — so the obvious 500 ms window made the gesture nearly unlandable. 1.2 s costs nothing, because a lone armed `Esc` is already inert: don't follow it with a bound key and your next keystroke goes to the editor as normal.

## Mouse reporting on a metered link

Skiff asks the terminal for clicks, drags and the wheel (`?1000h` + `?1002h`) and nothing more. Full all-motion tracking (`?1003h`), where the terminal reports every pointer movement whether a button is down or not, is switched on only while a hover-sensitive overlay — the action menu, a picker, a confirm — is actually up, and switched back off when it closes.

That's the difference between a steady uplink trickle for the whole session and a burst while a modal is open, which is worth having on cellular or a high-latency link. Nothing you can do with the mouse depends on the difference: caret placement, drag-select, the scrollbars, the splitter and wheel scrolling are all reported under the baseline.

## Clipboard over SSH (OSC 52)

Highlight text in Skiff. The text ends up in your laptop's clipboard. Even though Skiff is running three layers deep — your laptop, SSH, tmux, the editor.

The mechanism is OSC 52, a terminal escape sequence every modern emulator honors:

- iTerm2
- WezTerm
- Kitty
- Alacritty
- Ghostty
- gnome-terminal
- macOS Terminal (the default)
- Windows Terminal

Skiff writes OSC 52 to `/dev/tty` directly (not stdout — that would race tcell's renderer). When `$TMUX` is set, the sequence is wrapped in tmux's passthrough escape so it reaches the outer terminal even with `set-clipboard off`.

One escape sequence can only carry so much, so a selection over 512 KiB is refused rather than half-written — tmux discards an OSC string past 1 MiB without a word, and a truncated sequence leaves your terminal's parser eating whatever Skiff draws next. The editor's own clipboard still takes the text, so `Esc v` pastes it inside Skiff; only the hop to your laptop is skipped.

If your laptop's clipboard isn't picking up copies, check your terminal's "Allow apps to read/write clipboard" setting — most terminals require an opt-in for OSC 52 writes. iTerm2 calls it "Applications in terminal may access clipboard." kitty has a `clipboard_control` directive. Check [troubleshooting](/docs/troubleshooting/) for specifics.

## Paste

Paste *into* Skiff doesn't use OSC 52 — most terminals refuse to expose the system clipboard to a TUI for security reasons, and Skiff deliberately doesn't try.

Instead, paste from your laptop's clipboard the way you'd paste anywhere else: Cmd+V (macOS), Ctrl+Shift+V (Linux terminals), or right-click → Paste. The terminal delivers the text as keypresses, and Skiff handles them as normal input.

For paste *within* Skiff — copy here, paste here — use the action menu's Copy / Paste entries. Skiff keeps an internal clipboard alongside the system one.

## A practical workflow

1. SSH to a dev box every Monday morning.
2. `tmux attach` — your sessions are still there.
3. In one pane: `skiff ~/code/api`.
4. In other panes: agents, build watchers, log tails.
5. Click around files in Skiff. Make small edits. `Esc s` to save.
6. Highlight an error message in the log pane. Cmd+V it into ChatGPT on your laptop, get a fix.
7. Highlight the fix in your terminal. Paste it into Skiff. Save. The build watcher reloads.
8. Disconnect when you're done. Tomorrow, reconnect, attach, keep going.

That's the whole product.
