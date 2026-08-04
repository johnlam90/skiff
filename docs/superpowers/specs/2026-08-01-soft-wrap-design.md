# Soft wrap — design

Date: 2026-08-01
Status: approved (owner requested the feature; design decisions delegated)

## Problem

Skiff renders every buffer line on exactly one screen row and pans
horizontally (`Tab.ScrollX`). Long code and prose lines run off the right
edge and force sideways scrolling — the #1 readability complaint for an
editor aimed at reading files over SSH.

## Goal

Soft wrap in the editor pane: long lines flow onto continuation rows, no
horizontal scrolling while enabled. Toggleable from the ≡ menu, persisted
in `~/.config/skiff/config.json`, **default on**.

Out of scope (documented, not accidental): wrap in the diff viewer,
visual-row Up/Down cursor movement (arrows keep moving buffer lines),
visual-row Home/End, wide-rune (CJK) width support (the editor treats one
rune as one cell everywhere; unchanged).

> **Note, 2026-08-03:** the last of those is superseded. Wide-rune width
> support shipped — `internal/editor/cluster.go` derives cell widths and
> grapheme-cluster boundaries from `github.com/rivo/uniseg`, and wrap
> segments now break only on cluster boundaries so a wide glyph is never
> split across rows. The rest of this record stands as written; it is
> kept unedited as the design rationale for the anchor-based wrap, which
> the change did not alter.

## Approaches considered

1. **Whole-file visual-row layout cache** — map every buffer line to its
   visual rows up front; scroll/scrollbar in exact visual-row space.
   Precise, but O(file) relayout on every edit/resize and a new cache
   invalidation surface. Rejected: cost lands on huge files, exactly
   where a TUI must stay snappy.
2. **Anchor-based viewport-local wrapping (chosen)** — the scroll anchor
   becomes `(ScrollY line, ScrollSeg segment)`; all math walks at most a
   viewport's worth of lines from the anchor or the cursor. O(viewport)
   per frame, same as today's render. The scrollbar stays buffer-line
   proportional (an approximation; see below).
3. **Display-buffer transform** — pre-split the buffer into wrapped lines
   and reuse the existing renderer. Rejected: breaks Position mapping,
   undo, git gutter, find highlighting — everything keyed on real line
   numbers.

## Design

### Segment model (`internal/editor/wrap.go`, pure math)

- `WrapSegments(runes, width) []int` returns the rune start index of each
  visual row of one line. Always at least `[0]`; every segment advances
  by ≥1 rune (progress guarantee, no infinite loops on pathological
  widths).
- Break rule: fill cells left to right; when a rune won't fit, break
  before it, unless backtracking to just-after the last whitespace rune
  in the segment yields a word-boundary break (VS Code-style word wrap
  with hard-break fallback for words wider than the pane).
- **Tab stops reset per segment.** Each visual row is its own tab-stop
  coordinate system, so a segment behaves exactly like an independent
  line and every existing helper (`LineVisualCol`, `RuneColAtVisual`)
  works on the segment's rune subslice unchanged.

### Tab state (`internal/editor/tab.go`)

- `Wrap bool` — stamped by the app on tab creation and on toggle.
- `ScrollSeg int` — segment index within `ScrollY`'s line of the first
  visible row. Always 0 when wrap is off. Not persisted in sessions
  (restores to 0; `ScrollY` still restores).
- `lastContentW int` — content width of the last render, so `Scroll`
  (wheel, auto-scroll drag) can do visual-row math between renders.

### Render (wrap mode)

- Iterate visual rows from the anchor: for each line compute segments
  once, paint from `ScrollSeg` (first line only) until `h` rows fill.
- Line number and git marker only on a line's first segment row;
  continuation rows get a blank gutter.
- Cursor-line background highlights **all** segments of the cursor line.
- Per-rune styling (syntax, selection, find matches) is extracted into a
  shared helper used by both render paths, so wrap mode can't drift from
  the proven non-wrap styling rules.
- No overflow chevrons (nothing overflows); `ScrollX` forced to 0;
  `ScrollH` is a no-op while wrap is on.

### Scrolling & visibility (wrap mode)

- `Scroll(delta)` moves the anchor by visual rows (walks segments,
  bounded by |delta|).
- `EnsureVisible`: cursor above anchor → anchor jumps to the cursor's
  (line, seg); cursor below → walk forward at most a viewport to find its
  row, else place the cursor's row at the bottom by walking backward
  `viewH-1` rows from it. All walks are O(viewH · line length).
- `clampScroll`: the anchor may not pass the point where the file's last
  visual row sits mid-viewport (same overscroll feel as today), computed
  by walking backward from EOF.

### Hit-testing (wrap mode)

`HitTest` walks rows from the anchor to find the clicked (line, segment),
then maps the x offset through `RuneColAtVisual` on the segment subslice.
Gutter clicks land at the segment's first rune. Clicks past a segment's
end clamp to the segment end. Drag-selection and double-click word select
sit on top of HitTest and need no changes.

### Scrollbar

Stays buffer-line proportional (`LineCount` vs `viewH`), and a click
still maps to a `ScrollY` line (with `ScrollSeg` reset to 0). With wrap
on this is an approximation — a short file of very long lines shows no
bar even though it scrolls. Accepted for v1: precision requires the
rejected whole-file layout.

### App & config

- `App.wrapOn`, loaded from config (`"wrap": "on" | "off"`, absent = on),
  stamped onto tabs at both creation sites (open path, session restore).
- ≡ menu View group gains a toggle row (`labelFor`: "Unwrap long lines" /
  "Wrap long lines") that flips all open tabs, persists via
  `userconfig.SetWrap`, and flashes confirmation. Menu-first per
  CLAUDE.md; no new Esc-leader binding for now.
- Default **on**: the editor's audience reads code/prose over SSH, and
  the toggle persists for anyone who prefers panning.

## Testing

- `wrap_test.go`: segment math — empty/short lines, exact-width fit,
  word-boundary break, long-word hard break, whitespace at the edge,
  tabs, width ≤ 0, progress guarantee; anchor advance/retreat walks.
- `tab_test.go` additions via `tcell.NewSimulationScreen`: wrapped
  continuation rows painted, blank continuation gutter, cursor position
  on a wrapped row, HitTest on continuation rows, EnsureVisible/Scroll/
  clampScroll in wrap mode, ScrollH no-op.
- `userconfig_test.go`: wrap key parsing (on/off/absent/invalid),
  SetWrap round-trip preserving unknown keys.
- `app_test.go`: pinned `TestMenuLayout_*` numbers updated for the new
  row; toggle flips tabs + persists.
