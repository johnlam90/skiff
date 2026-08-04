---
title: "Mouse Controls"
metaTitle: "Mouse Controls in Skiff — Click, Drag, Scroll"
metaDescription: "Every mouse gesture Skiff responds to — clicks, drags, double-clicks, right-clicks, scroll wheel — across the editor, tree, tab bar, and modals."
summary: "Every mouse gesture Skiff responds to."
weight: 40
---

Skiff is mouse-first. Every UI surface is clickable, draggable, or scrollable. This page is the complete reference.

## Editor body

| Gesture                                | Effect                                                                 |
| -------------------------------------- | ---------------------------------------------------------------------- |
| Left-click                             | Place the cursor at the click point.                                   |
| Left-click + drag                      | Extend a selection from the press point.                               |
| Drag past the top or bottom edge       | Auto-scroll while extending the selection at your last column.         |
| Double-click                           | Select the word under the cursor (letters, digits, underscore — Unicode aware). |
| Scroll wheel                           | Scroll three lines per tick.                                           |
| Right-click                            | Open the action menu (in terminals that forward Button3).              |

## File tree

| Gesture                                | Effect                                                                 |
| -------------------------------------- | ---------------------------------------------------------------------- |
| Left-click on a folder                 | Toggle expand / collapse. Sets the folder as the active folder.        |
| Left-click on a file                   | Open it in the preview tab (italic, reused by the next click), or switch to it if already open. |
| Right-click on a folder                | Per-folder context menu: New File, Rename, Delete, Copy paths.         |
| Right-click on a file                  | Per-file context menu: Rename, Delete, Copy relative path, Copy absolute. |
| Scroll wheel                           | Scroll the tree.                                                       |

The active folder — the one shown bold in the sidebar — is the default target for New File. The label in the action menu reflects this: "New file in `cmd/`" when a subfolder is active, plain "New file" at the project root.

Clicking a file is a *preview* open, so browsing costs one tab rather than one per file: the label goes italic and the next single click replaces it in place. A second click on the same file, or the first edit to it, pins the tab; the finder (`Esc p`) and the menu always open a permanent one. The first preview of each session flashes the rule once, since nothing on screen says it.

Two rows behave unlike the rest. The `… N more` row that ends a directory over 1000 entries is inert — it has no path behind it, so a click lands on nothing; use the finder (`Esc p`) to reach files past the cap. A directory marked `(unreadable)` still toggles when clicked, it just has nothing to show: Skiff got a permission or I/O error listing it, and the label is there so it doesn't read as an empty folder.

Note: macOS Terminal + tmux often swallows Button3. Every right-click action also lives in the main `≡` menu, so you're never stuck.

## Tab bar

| Gesture                                | Effect                                                                 |
| -------------------------------------- | ---------------------------------------------------------------------- |
| Left-click a tab body                  | Switch to that tab.                                                    |
| Left-click the `×` on a tab            | Close that tab. Dirty tabs prompt Save / Discard / Cancel.             |
| Left-click the `≡` icon                | Open the action menu.                                                  |

## Splitter

The single column between the sidebar and the editor is the splitter.

| Gesture                                | Effect                                                                 |
| -------------------------------------- | ---------------------------------------------------------------------- |
| Press on the splitter column, drag     | Resize the sidebar. Min sidebar width: 18. Min editor width: 40.       |

## Modals (action menu, find, finder, confirms)

| Gesture                                | Effect                                                                 |
| -------------------------------------- | ---------------------------------------------------------------------- |
| Left-click a row                       | Activate the row's action.                                             |
| Hover                                  | Highlight the row under the cursor.                                    |
| Click outside the modal                | Dismiss.                                                               |

The action menu's type-to-filter field changes none of this: a click still runs the row it landed on, and clicking outside still dismisses. Filtering only changes which rows are on screen. The `Git…` and `File clipboard…` rows open a second pick, which behaves the same way.

## Status bar

The status bar shows the active file's path, language, cursor position, dirty marker, git branch and change count (when applicable), a `⚠ disk conflict` marker while an open dirty buffer has diverged from the file on disk, and any flash messages from background work. One region is clickable: the branch segment on the right flips the sidebar to the GIT panel — the mouse-first sibling of `Esc g`. The sidebar's own header is clickable too: the `EXPLORER` and `GIT` tabs switch between the file tree and the uncommitted-changes list, and clicking a change row opens its diff — side-by-side on wide terminals, unified on narrow ones — with an `[ Open file ]` button that jumps to the first changed line.
