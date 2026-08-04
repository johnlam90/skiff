---
title: "Troubleshooting"
metaTitle: "Skiff Troubleshooting — Common Fixes"
metaDescription: "Right-click does nothing? Clipboard not working? Indexing slow? The fixes for the issues Skiff users hit most, with terminal-specific notes."
summary: "Fixes for the most common Skiff issues."
weight: 120
---

Common issues, with fixes.

## Right-click does nothing

You're probably in macOS Terminal + tmux. Apple's default Terminal eats Button3 events before tmux can forward them, so Skiff never sees the right-click.

**Fix.** Use the `≡` icon in the top-left or double-tap `Esc` to open the action menu. Every right-click action also lives there. Long-term: switch to iTerm2, WezTerm, or Ghostty, which all forward Button3 correctly.

## Clipboard isn't working over SSH

OSC 52 is the mechanism. Most terminal emulators ship with OSC 52 writes disabled by default for security reasons. You have to opt in.

**iTerm2.** Settings → General → Selection → "Applications in terminal may access clipboard."

**Kitty.** Add `clipboard_control write-clipboard write-primary` to `kitty.conf`.

**WezTerm.** OSC 52 is on by default; check your `.wezterm.lua` doesn't have `enable_csi_u_key_encoding` blocking it.

**Ghostty.** Settings → "OSC 52 clipboard write" → Allow.

**tmux.** Run `tmux show-options -g set-clipboard`. If it's `off`, run `tmux set-option -g set-clipboard on` (default since tmux 3.2). Skiff also wraps OSC 52 in tmux passthrough automatically when `$TMUX` is set, so this should mostly be a non-issue.

## OSC 52 isn't pasting into my local app

Even if Skiff copies successfully, your *local* terminal has to be allowed to write to the system clipboard. Check the same setting above. Some corporate Macs disable clipboard access entirely via MDM; if `pbpaste` doesn't return your last copy, the issue is upstream of Skiff.

## "Selection too large for the terminal clipboard"

OSC 52 puts the whole selection inside one escape sequence, and that sequence has to survive every hop to your terminal. The tightest hop anyone can name is tmux, which caps an OSC string at 1 MiB — so after base64 inflation Skiff refuses anything over 512 KiB rather than writing half a sequence (a truncated OSC 52 leaves the terminal's parser swallowing everything Skiff draws next).

The copy is not lost: the editor's own clipboard always gets the text first, so `Esc v` still pastes it inside Skiff. Only the trip to your *local* machine is skipped. To get a big block onto your laptop's clipboard, use a custom action (`cat "$FILE" | ...`) or copy it in smaller pieces.

## Branch and change badges are missing

If the status bar shows no branch, the tree isn't tinted, and the GIT tab is absent, first check whether Skiff flashed "git was not found on PATH — branch and change badges are off" at startup. It says that once per session when `git` isn't on the `PATH` Skiff inherited — which is easy to hit on a minimal container or when your shell adds git via a profile that a non-login SSH command never sources. Run `git --version` in the same shell you launch Skiff from.

Every git surface repeats the real reason: with no binary the panel and its openers say "git was not found on PATH" rather than "Not a git repository", so the two failures are never confused. Install git (or fix `PATH`) and the branch and badges come back on the next 10-second tick, no restart needed — quietly, though: the warning is once per process, so there's no "resolved" message to wait for. Watch the status bar for the branch instead.

## A folder says "(unreadable)" or "… N more"

Both are deliberate labels, not glitches.

**`(unreadable)`** means the directory exists but Skiff got a permission (or I/O) error reading it. Previously it rendered exactly like an empty folder, which was the tree's most confident lie. It still expands, and it keeps whatever children it last saw rather than blanking them — a failed read is not evidence the directory emptied. Skiff retries on every refresh, so fixing the mode bits clears the label on the next tick.

**`… N more`** means *that directory* has more than 1000 entries the tree would have shown and it stopped listing there. The cap is per directory, not per project, and it counts what survives the hide list (`.git`, `node_modules`, `.vscode`, and friends are filtered out before the cap applies), so N is the number of rows you'd otherwise be scrolling past — not raw `readdir` output. The row is inert: clicking it does nothing. The sidebar is a navigation aid; use the finder (`Esc p`) for anything in the tail, since it indexes the whole project regardless of directory size.

## Indexing is slow

The fuzzy file finder uses `git ls-files` for git repos (~150 ms on 50,000 files) and falls back to a `filepath.Walk` on non-git projects.

**Slow on a git repo?** That's unusual. Run `git ls-files | wc -l` — if it's huge, see if you have committed `node_modules` or `vendor` directories that should be in `.gitignore`.

**Slow on a non-git project?** Check your `.gitignore`. Skiff reads it during the walk and skips anything that matches. Add `node_modules`, `dist`, `build`, `.cache`, and any other large generated directories. The hardcoded ignore list catches the most common offenders, but a `.gitignore` is the canonical signal.

## `Ctrl+S` froze my terminal

That's XOFF — a real terminal flow-control signal. Press `Ctrl+Q` to release it (XON). Skiff doesn't bind `Ctrl+S` for exactly this reason. Save with `Esc s` instead.

## Format on save isn't running

Three things to check:

1. Is `.skiff/format.json` in your project root? Skiff looks for it relative to the project root, not the file being saved.
2. Does the file's extension match a key in `commands`? Keys are extensions *without* the leading dot.
3. Did you trust the config? On the first save with a new or edited config, Skiff prompts. If you said No, every save in this project is a no-op until the config changes.

If the configured binary isn't installed, Skiff silently does nothing on save — that's deliberate, so you don't have to install everyone's formatter to clone a repo. To debug, run the formatter command manually and confirm it works.

## Files I just created don't show up in the finder

The finder index refreshes every 10 seconds and immediately after any create / rename / delete *inside* Skiff. If you create a file from another shell pane, wait up to 10 seconds, then reopen the finder.

## The file explorer vanished when I shrank the pane

That's deliberate. Below 58 columns — 18 for the narrowest useful tree plus 40 for the editor — Skiff hides the explorer rather than showing you a sliver of code next to a sliver of tree, and flashes "Narrow window — file explorer hidden (Esc t shows it)". Widen the pane past 58 and it returns with "File explorer restored".

`Esc t` overrides it either way, and the override sticks: reopening the explorer inside a narrow pane won't be undone by the next resize, and a panel you closed yourself is never reopened for you no matter how wide the window gets.

## CJK or emoji sits one column off

Skiff measures text in grapheme clusters and terminal cells with the same Unicode engine tcell uses to place a cell, so an ideograph takes two columns, a combining mark none, and a ZWJ emoji two — the caret, the selection, the find highlight and soft wrap all agree with what you see.

One class of character can still disagree with your *terminal*: the ambiguous-width set — `±`, `→`, `°`, `Ω`, the box-drawing glyphs. Unicode declines to say whether they're one cell or two, Skiff draws them as one, and a terminal (or tmux) configured to treat them as double-width drifts one column per character on that line. Terminals that offer the choice usually name it something like "treat ambiguous-width characters as double width"; turning it off makes the columns agree again. Skiff won't follow that setting — it's a global switch that would reflow everyone's box-drawn text.

## My terminal's color is off

Skiff's palettes are authored in 24-bit color. Most modern terminals support it; some older ones default to 256-color mode. Check `tput colors` — anything less than `16777216` means truecolor isn't on. Set `COLORTERM=truecolor` in your shell rc and reconnect, and if you run tmux, start it with `tmux -2`.

Below 256 colors Skiff stops relying on hue and switches to an attribute-based palette instead: reverse video for the selection and the status bar, bold for the active tab and dirty markers, underline for the current find match. That is deliberate, not a broken theme — on a 16-color `TERM` every gray in a theme collapses onto the same ANSI "bright black", so a color-only cue would be invisible. If the editor looks unexpectedly monochrome, `tput colors` is the thing to check.

## The image preview is blocky

Image preview uses the half-block technique — every cell is `▀` with a top color and a bottom color, giving you two vertical pixels per character cell. That's the trade for a renderer that works in *every* truecolor-capable terminal (including macOS Terminal) and passes through tmux without any passthrough config. For pixel-perfect previews, open the file on your laptop with a custom action.

## I want to see what `Esc t` does

Press `Esc ?` — the editor prints the whole leader table, grouped, with a short note on the ≡ menu. It's generated from the same table the keys dispatch through, so it can't be out of date. `≡` → **Keyboard shortcuts…** opens the same sheet, the action menu shows each row's combo next to it, and the [Hotkeys](/docs/hotkeys/) doc has the table plus the reasoning.
