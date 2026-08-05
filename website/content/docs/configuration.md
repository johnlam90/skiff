---
title: "Configuration"
metaTitle: "Skiff Configuration Files Reference"
metaDescription: "Skiff reads a handful of small JSON files plus an optional per-project folder. The full reference, including XDG paths and what isn't configurable."
summary: "Editor config files, and what isn't configurable."
weight: 90
---

Skiff avoids a config file on purpose. The behaviors that *can* be configured live in a handful of small JSON files and one project-level folder. Everything else is opinionated.

## `~/.config/skiff/config.json`

Top-level editor preferences. Optional — without it, every field uses its default. The schema is intentionally tiny and forward-compatible: unknown fields are ignored, so old binaries won't break on a future config.

```json
{
  "icons": "auto",
  "theme": "tokyo-night",
  "wrap": "on",
  "gitignore": "on"
}
```

| Key     | Values                            | Default  | What it does |
| ------- | --------------------------------- | -------- | ------------ |
| `icons` | `"auto"` / `"on"` / `"off"`       | `"auto"` | Toggles the Nerd Font glyphs in the file tree. `auto` checks whether a Nerd Font is installed (via `fc-list` or by walking `~/Library/Fonts` / `~/.local/share/fonts`) and turns icons on iff one is found. Pick `on` if detection misses your install; pick `off` if the glyphs render as boxes in your terminal. |
| `theme` | any registry id (`"dracula"`, `"gruvbox-dark"`, `"nord"`, …) | `"tokyo-night"` | The active palette. Written by the `≡` → **Theme…** picker, which is the intended way to change it — pick a theme there and this key updates itself. An unknown id falls back to the default. |
| `wrap`  | `"on"` / `"off"`                  | `"on"`   | Soft wrap for long lines. Written by `≡` → **Wrap / Unwrap long lines** (`Esc z`). With wrap off, Shift+wheel scrolls sideways. |
| `gitignore` | `"on"` / `"off"`              | `"on"`   | Hides file-tree entries the project's `.gitignore` files exclude, so the sidebar and the finder agree on what's project noise. Written by `≡` → **Show ignored files** / **Hide ignored files**. No dot-prefixed name is ever filtered by it (a gitignored `.next/` or `.venv/` stays visible too — dotfile visibility is a separate axis), and a file open in a tab is never hidden: its ignored folder reappears holding just that file. |

Scope of `gitignore`, so you know when a rule won't apply: Skiff reads the `.gitignore` files from the project root down to the directory being listed, which matches `git ls-files --exclude-standard` for the ordinary case. It does **not** read `.git/info/exclude`, `core.excludesFile`, or any global excludes, and it consults nothing above the project root. A `!negation` in a deeper file cannot un-ignore what a shallower file already excluded — the walk stops at the first level that says "ignored". Turn the key off when a rule you need isn't one of the ones it honours.

The detector can only see whether the OS knows about the font — it can't tell whether your *terminal* is configured to render it. If icons turn on but show as "tofu" boxes, set `"icons": "off"` and either point your terminal at a Nerd Font or live without them.

### Installing a Nerd Font

The icons in the file tree come from [Nerd Fonts](https://www.nerdfonts.com/) — they're not part of stock system fonts. Skiff needs **two** things in place:

1. A Nerd Font installed at the OS level (so Skiff's detector sees it).
2. Your terminal emulator configured to render that font.

Without step 2, glyphs render as boxes ("tofu") even though detection says yes.

**macOS (Homebrew)** — pick any patched font and install it:

```sh
brew install --cask font-jetbrains-mono-nerd-font
# or: font-hack-nerd-font, font-fira-code-nerd-font, font-meslo-lg-nerd-font, etc.
```

Then point your terminal at it: in iTerm2, **Settings → Profiles → Text → Font** and pick the `Nerd Font` variant. In Terminal.app, **Settings → Profiles → Text → Font**. In Ghostty, set `font-family = "JetBrainsMono Nerd Font"` in `~/.config/ghostty/config`.

**Linux (Debian / Ubuntu)** — Nerd Fonts aren't in apt yet, so download a patched font and drop it in `~/.local/share/fonts`:

```sh
mkdir -p ~/.local/share/fonts
cd ~/.local/share/fonts
curl -fLo "JetBrainsMono.zip" \
  https://github.com/ryanoasis/nerd-fonts/releases/latest/download/JetBrainsMono.zip
unzip -o JetBrainsMono.zip
fc-cache -fv
```

Then set the font in your terminal — for GNOME Terminal: **Preferences → Profiles → Text → Custom font → JetBrainsMono Nerd Font**. For Alacritty / Kitty / Wezterm / Ghostty, edit the config file's `font` / `font-family` entry.

**Linux (Arch)** — patched fonts are in the official repos:

```sh
sudo pacman -S ttf-jetbrains-mono-nerd
# or any of: ttf-hack-nerd, ttf-firacode-nerd, ttf-meslo-nerd, etc.
```

**Verifying** — `fc-list | grep -i nerd` should print at least one line. If it does and Skiff *still* shows boxes, the font is installed but your terminal isn't using it; fix the terminal's font setting.

## `~/.config/skiff/actions.json`

User-defined shell-out actions for the action menu. See [Custom actions](/docs/custom-actions/). Optional — without it, the menu shows only built-in actions.

```json
{
  "actions": [
    { "label": "Open on Laptop", "command": "scp \"$FILE\" laptop:~/Downloads/" }
  ]
}
```

## `~/.config/skiff/format-defaults.json`

Personal default formatters. Same schema as the project file. Never runs on its own — only used when Skiff prompts you to "install" an entry into a project's `.skiff/format.json`. See [Format on save](/docs/format-on-save/).

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "py":  ["ruff", "format", "$FILE"]
  }
}
```

## `~/.config/skiff/format-trust.json`

Stores per-project answers to the format-on-save trust prompt and the install prompt. Managed by Skiff — you don't edit this directly. Each entry records the project path, a SHA-256 hash of the project's `.skiff/format.json`, and the user's answer (or per-extension declines).

If a teammate pushes a new `.skiff/format.json`, the hash changes and Skiff re-prompts on the next save. That's the security model in one sentence.

## `<project>/.skiff/format.json`

Per-project format-on-save config. Keys are file extensions (no leading dot); values are argv arrays. See [Format on save](/docs/format-on-save/).

```json
{
  "commands": {
    "go":  ["gofmt", "-w", "$FILE"],
    "ts":  ["prettier", "--write", "$FILE"]
  }
}
```

Commit it to share with your team, or add `.skiff/` to `.gitignore` to keep it personal. Both work.

## `~/.local/state/skiff/actions.log`

State, not config. Append-only log of every custom-action invocation. See [Custom actions](/docs/custom-actions/).

## `~/.local/state/skiff/sessions/`

State, not config. One JSON file per project — open tabs with cursor and scroll, expanded folders, sidebar width — keyed by a hash of the project root. Managed by Skiff; delete a file to forget one project, delete the directory to forget them all. An older single `sessions.json` is migrated on first run and renamed to `sessions.json.migrated` rather than deleted.

## XDG awareness

All paths above respect the XDG environment variables when set:

- `$XDG_CONFIG_HOME` — defaults to `~/.config`
- `$XDG_STATE_HOME` — defaults to `~/.local/state`

## What you can't configure

This is intentional. Don't ask for it.

- **Themes beyond the registry.** 26 palettes ship in the binary and the `≡` → **Theme…** picker switches between them live; there's no way to define a 27th. One colorway per theme, no per-token overrides.
- **Keymap.** Esc-leader is the keymap. Adding a config file for it would defeat the entire point.
- **Plugins.** None. Skiff is opinionated — that's the product.
- **Tab width / line endings.** Detected from the file's own contents on open.

If a behavior matters enough to configure, it should be obvious enough to be the default.
