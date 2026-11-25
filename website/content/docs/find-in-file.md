---
title: "Find in File"
metaTitle: "Find in File with Skiff (Esc f)"
metaDescription: "Esc f opens Skiff's in-file search bar. Smart-case substring, live highlight, Enter for next, Shift+Enter for previous, Esc to close."
summary: "Search the current file with Esc f."
weight: 50
---

`Esc f` (or **Find in file** from the `≡` menu) opens a single-row search bar pinned above the status bar.

```
 Find: foo█                       3 of 12   Enter: next · Shift+Enter: prev · Esc: close
```

## How matching works

- Smart-case substring: an all-lowercase query matches any case, a single uppercase letter in the query makes it exact — so `id` finds `ID` and `id`, while `ID` finds only `ID`. Same rule the project-wide search uses. `Alt+c` forces an exact-case match for an all-lowercase query; the counter shows `· Aa` while it is on. No regex, no whole-word.
- Results highlight live as you type — the editor shows every match in the visible buffer the moment you change the query.
- The active match paints in a brighter color than the rest, so you can pick out where you are in the result set.

## Navigation

| Key             | Action                                |
| --------------- | ------------------------------------- |
| Type            | Update the search query.              |
| `Enter`         | Jump to the next match (wraps).       |
| `Shift+Enter`   | Jump to the previous match (wraps).   |
| `Alt+c`         | Toggle match case.                    |
| `Tab`           | Open the replace field (see below).   |
| `Esc`           | Close the bar and clear highlights.   |

With the bar closed, `Esc ;` (**Find next**) jumps to the next match of the last query.

## Replace

`Tab` grows the bar a second field and moves the keyboard to it; `Tab` again goes back to the query.

| Key             | In the replace field                              |
| --------------- | ------------------------------------------------- |
| `Enter`         | Replace the current match and move to the next.   |
| `Shift+Enter`   | Replace every match, as one undo step.            |
| `Tab`           | Back to the query field.                          |

A replacement follows the case of what it replaces when the search is case-insensitive (an all-lowercase query, match case off): replacing `foo` with `bar` turns `FOO Foo foo` into `BAR Bar bar`. Replace is not offered over a rendered Markdown preview, which is read-only — switch the tab back to editing first.

## State across tabs

Each tab carries its own search state. Switch to another tab, search there, and switching back leaves the first tab's last query waiting if you reopen the bar — useful when you're hopping between two files looking for related symbols.

Closing the bar with `Esc` clears highlights. Opening it again starts fresh.

## Why so few toggles

Regex search is a pile of UI surface (toggles, error states, escape rules) for a feature most "find in file" users never reach for. Smart case answers the case question by watching how you type, and `Alt+c` covers the one thing it cannot: an exact match for a query that happens to be all lowercase. The 90% case is "I know roughly what I'm looking for, take me there." That's what `Esc f` does. For regex or whole-word search, use project find (`Esc F`), which has both.
