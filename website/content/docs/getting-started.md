---
title: "Getting Started"
metaTitle: "Getting Started with Skiff in 3 Minutes"
metaDescription: "Open a project, click a file, edit a line, save. The first three minutes with Skiff — the mouse-first terminal code editor for SSH workflows."
summary: "Open a project, click a file, edit a line, save."
weight: 20
---

This page walks the first three minutes. Open a project, click around, save a file, get back to your terminal.

## Open a project

From any directory:

```sh
skiff              # opens the current directory as the project root
skiff ~/code/app   # opens a specific project root
skiff main.go      # opens that one file — single-file mode, no sidebar
skiff new-file.go  # creates the file on first save (vim-style)
```

One directory or one file per invocation, with any flag first. A second
path is refused by name rather than silently dropped — once the editor
is up, the tree and the finder (`Esc p`) are how you open the rest. And
`skiff main.go` means *just* that file: no sidebar, no project index, no
walk of the surrounding directory. Pass a directory when you want the
full project view.

<figure class="screenshot-figure">
  <img
    src="/img/screenshots/opening-a-project.png"
    srcset="/img/screenshots/opening-a-project-1200.png 1200w, /img/screenshots/opening-a-project.png 2000w"
    sizes="(min-width: 1024px) 800px, 100vw"
    width="2000" height="1414"
    alt="Skiff just after opening the skiff project — sidebar shows the file tree, the editor area shows 'No file open' with a hint to click a file or press the menu icon."
    loading="lazy" decoding="async"
  />
</figure>

The layout is what you'd expect: a file tree on the left, a tab bar across the top, the editor in the middle, a status bar at the bottom.

## Click a file

Single-click any file in the tree to open it. Single-click a folder to expand it. The active folder — the one New File and Rename Folder will target — bolds in the sidebar so you always know where the next file lands.

That first click opens a *preview* tab: its label is italic, and the next single click reuses the same slot instead of stacking another tab, so reading your way through a directory doesn't leave a dozen tabs behind. Click the same file again — or just start editing it — and the tab is pinned for good. Opening a file from the finder, the menu, or the command line always opens a permanent tab. Skiff explains this once, the first time a session makes a preview: "Preview tab — edit it or click again to keep it open".

<figure class="screenshot-figure">
  <img
    src="/img/screenshots/clicking-a-file.png"
    srcset="/img/screenshots/clicking-a-file-1200.png 1200w, /img/screenshots/clicking-a-file.png 2000w"
    sizes="(min-width: 1024px) 800px, 100vw"
    width="2000" height="1409"
    alt="main.go open in the editor with Tokyo Night syntax highlighting after clicking it in the sidebar."
    loading="lazy" decoding="async"
  />
</figure>

## Switch tabs

Each open file is a tab. Click a tab body to switch. Click the `×` to close. Re-opening a file that's already open just switches to its existing tab.

## Open the menu

Every action lives in the action menu. Open it three ways:

1. Click the `≡` icon in the top-left corner.
2. Right-click anywhere outside the file tree (works in most terminals; macOS Terminal + tmux often eats Button3).
3. Double-tap `Esc`.

<figure class="screenshot-figure">
  <img
    src="/img/screenshots/clicking-the-menu.png"
    srcset="/img/screenshots/clicking-the-menu-1200.png 1200w, /img/screenshots/clicking-the-menu.png 2000w"
    sizes="(min-width: 1024px) 800px, 100vw"
    width="2000" height="1412"
    alt="Action menu modal expanded over the editor — Save, Close tab, Find file, Open on Repo, Quit editor, and other actions visible."
    loading="lazy" decoding="async"
  />
</figure>

The menu opens with a filter field focused, so the fastest way to find an action is to type part of its name — "branch" finds **Switch branch…**, "thm" finds **Theme…**. Arrow keys move, `Enter` runs the highlighted row, `Esc` clears the filter and a second `Esc` dismisses the menu. Rows are grouped File / Edit / Go / Git / View / Custom / Quit, and the git verbs and file-clipboard actions sit one row deeper behind **Git…** and **File clipboard…**.

Every action worth a shortcut has one, and you don't have to memorize them: `Esc ?` (or `≡` → **Keyboard shortcuts…**) opens the whole list, grouped the same way the menu is.

## Edit and save

Click in the editor body to place the cursor. Type. Drag to select. The standard editor keys all work: arrow keys move, Shift+arrow extends selection, Home/End jump to line ends, PgUp/PgDn scroll a viewport.

To save: open the menu and pick **Save**, or press `Esc s`.

<figure class="screenshot-figure">
  <img
    src="/img/screenshots/saving.png"
    srcset="/img/screenshots/saving-1200.png 1200w, /img/screenshots/saving.png 2000w"
    sizes="(min-width: 1024px) 800px, 100vw"
    width="2000" height="1411"
    alt="Editor with a line selected mid-edit, status bar at the bottom showing the cursor position and dirty marker."
    loading="lazy" decoding="async"
  />
</figure>

## Resize the sidebar

Drag the column between the tree and the editor. Minimum sidebar width is 18 columns; the editor won't shrink below 40.

Below 58 columns total there isn't room for both, so Skiff hides the explorer itself and tells you ("Narrow window — file explorer hidden (Esc t shows it)"). Widen the window and it comes back. `Esc t` toggles it by hand at any width, and a panel you closed on purpose stays closed — the auto-hide only puts back what it took away.

## Quit

Open the menu and pick **Quit editor**, or press `Esc q`. If any tabs have unsaved changes, you'll see a Save / Discard / Cancel modal — Save & Close blocks the quit if a save fails, so no work is lost.

That's it. You now know enough to use Skiff. The rest of these docs are reference.
