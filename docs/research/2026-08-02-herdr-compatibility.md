# Skiff inside herdr — compatibility research

Date: 2026-08-02
Question: what does Skiff (tcell-based, mouse-first TUI) need for full
compatibility inside the herdr terminal multiplexer?

## Sources

- **herdr 0.7.5 source checkout** at `/root/.claude/skills/herdr/`
  (version matches the installed binary; `Cargo.toml` names the canonical
  repo `https://github.com/ogulcancelik/herdr`, homepage `https://herdr.dev`).
  Cited below as `herdr:<path>:<line>`.
- **herdr CHANGELOG.md** (same checkout) for shipped-fix provenance, cited
  by issue number.
- **Observed with herdr 0.7.5**: live environment checks run inside a herdr
  pane (`HERDR_ENV=1`, `HERDR_PANE_ID` set).
- Skiff code cited as `skiff:<path>:<line>`.

## Verdict up front

**No Skiff code changes are required for correctness.** Herdr embeds the
Ghostty terminal engine per pane and implements real mode-tracked terminal
emulation, so Skiff's existing tmux-hardened behavior carries over. The
items worth acting on are documentation-level (right-click, README), plus
two optional polish notes.

| Topic | herdr behavior | Skiff impact | Skiff code touched |
| --- | --- | --- | --- |
| Mouse button/drag/motion (1000/1002/1003) | Fully mode-tracked per pane; forwarded when the app requests them | ✅ works as-is | `skiff:internal/app/app.go:697` (`EnableMouse`) |
| SGR mouse (1006) | Native; split reads reassembled (#1334) | ✅ works as-is | — |
| Wheel + Shift+wheel | Forwarded with modifier bits intact; no stripping, no split modifier events | ✅ works as-is; sticky-Shift workaround harmless | `skiff:internal/app/app.go:74` |
| Right-click (Button3) | **Never forwarded by default** — always opens herdr's pane menu; opt-in via `right_click_passthrough_modifier` (Shift explicitly unsupported) | ✅ already designed for (≡ menu is primary); document it | README / docs only |
| OSC 52 clipboard | Intercepted from the pane PTY and bridged to the host clipboard by herdr itself, incl. over `herdr --remote`; **no wrapping needed** | ✅ works as-is (`$TMUX` unset → Skiff writes plain OSC 52) | `skiff:internal/clipboard/clipboard.go:43` |
| Bracketed paste (2004) | Mode-tracked; paste wrapped iff the pane enabled it | ✅ works as-is | `skiff:internal/app/app.go:702` (`EnablePaste`) |
| Esc / escape-time | No tmux-style escape-time (~10 ms idle flush); same-read `ESC`+key still becomes an Alt chord, re-encoded as a single ESC-prefixed write | ✅ Skiff's Alt+rune / Alt+Esc handlers cover it | `skiff:internal/app/app.go:1436` |
| TERM / colors | `TERM=xterm-256color`, `COLORTERM=truecolor` set for every pane | ✅ tcell picks up truecolor | — |
| tcell-specific issues | None found; several generic TUI fixes already shipped | — | — |

## Findings by topic

### Mouse protocol

Herdr's pane terminal (the embedded Ghostty engine) tracks the pane's
negotiated mouse protocol: `None / Press (1000) / PressRelease /
ButtonMotion (1002) / AnyMotion (1003)` and encodings
`Default / Utf8 / Sgr (1006)` — `herdr:src/pane/terminal.rs:44-45,118-119,1331-1356`,
`herdr:src/ghostty/mod.rs:160` (`MODE_MOUSE_SGR: u16 = 1006`).

- Left press/drag/release inside a pane are offered to the pane app first;
  herdr only starts its own text selection when the app has **no** mouse
  reporting (`herdr:src/app/input/mouse.rs:627-641`). Encoding returns
  `None` when the pane didn't request the event class, so gating is
  automatic (`herdr:src/pane/terminal.rs:1643-1699`).
- Motion-without-button is forwarded only under mode 1003
  (`herdr:src/pane/terminal.rs:1672-1675`), which tcell requests via
  `MouseMotionEvents`. Shipped fix: hover/move events for any-motion apps
  (CHANGELOG #419).
- Modifier bits ride the SGR `cb` value: +4 Shift, +8 Alt, +16 Ctrl
  (`herdr:src/input/encode.rs:127-135`).
- Split SGR reports across input reads are reassembled, and a standalone
  Esc preceding mouse bytes is preserved (CHANGELOG #1334, #1382);
  SGR reports no longer leak into pane input after host-side handling
  (CHANGELOG #939).
- Wheel: routed per pane as `MouseReport` (app enabled mouse reporting →
  forwarded with modifiers), `AlternateScroll`, or `HostScroll` (herdr's
  own scrollback) — `herdr:src/app/input/mouse.rs:1698-1739`. Horizontal
  wheel/trackpad events reach mouse-reporting apps since #1349
  (`herdr:src/app/input/mouse.rs:931-936`). `ui.mouse_scroll_lines = 3`
  only affects herdr's own scrollback panes, not forwarded events
  (observed in `herdr --default-config`).
- Double-click token copy exists in herdr but "mouse passthrough
  preserved for terminal apps that request mouse input" (CHANGELOG #142,
  #296) — Skiff's own double-click word-select receives the events.
- Click-to-focus an unfocused pane **also** delivers that click to the
  app (`herdr:src/app/input/mouse.rs:622-631`) — Skiff will move the
  caret on the focusing click, same as in a plain terminal.

### Right-click (Button3) — the one real behavioral difference

Plain right-click in a pane **always opens herdr's pane context menu**,
even when the inner app has mouse reporting enabled — a deliberate design
(CHANGELOG #25/#701: "right-click now opens the pane context menu even
when the inner TUI has mouse reporting enabled"), implemented at
`herdr:src/app/input/mouse.rs:1059`. Forwarding right-click requires the
user to configure `right_click_passthrough_modifier` (e.g. `ctrl`,
`alt`); empty/off is the default, and Shift is intentionally rejected
"because terminals commonly reserve Shift+mouse"
(`herdr:src/config/model.rs:141-151,796`; `herdr --default-config`
lines 174-176; CHANGELOG #148).

Skiff already treats tree right-click as a redundant shortcut, never a
primary surface (CLAUDE.md: "macOS Terminal + tmux often swallows
Button3"). Herdr is a second major environment where that rule pays off.
Action: mention in README/docs that under herdr, right-click opens
herdr's menu, and users who want Skiff's tree context menu can set
`right_click_passthrough_modifier = "ctrl"` (then Ctrl+right-click
forwards; herdr strips the configured modifier before forwarding —
`herdr:src/app/input/mouse.rs:1500-1516`).

### OSC 52 clipboard

Pane PTY output is parsed by the Ghostty engine; a valid OSC 52 write
triggers a clipboard callback that herdr turns into
`AppEvent::ClipboardWrite` (`herdr:src/pane.rs:1815-1823`,
`herdr:src/events.rs:127-129`). Herdr then writes the host clipboard
itself: native platform tools first (`pbcopy`/`wl-copy`/`xclip`/`xsel`),
falling back to emitting its **own** plain OSC 52 (BEL-terminated) to
the outer terminal — preferred automatically over SSH
(`herdr:src/selection.rs:326-334`, CHANGELOG "#702", "prefer native
platform clipboard tools before falling back to OSC 52"). Remote
attach bridges it server→client (`herdr:src/client/mod.rs:1570-1571,1963-1969`).

Consequences for Skiff (`skiff:internal/clipboard/clipboard.go`):

- Writing plain OSC 52 to `/dev/tty` is exactly right: inside a herdr
  pane, `/dev/tty` **is** the pane PTY, so herdr's parser sees it.
- `$TMUX` is unset inside herdr (observed with herdr 0.7.5), so Skiff's
  tmux-passthrough wrap correctly does not engage. No `HERDR_ENV` branch
  is needed — herdr wants the *unwrapped* sequence.
- Nested case (Skiff → tmux → herdr): `$TMUX` is set, Skiff wraps for
  tmux, tmux unwraps and re-emits toward its client tty — which is the
  herdr pane — so herdr still sees plain OSC 52. Works without changes.
- Skiff already treats OSC 52 as fire-and-forget with a graceful flash
  fallback, which matches herdr's best-effort model.

### Bracketed paste

Herdr tracks the pane's mode 2004 via the Ghostty engine
(`herdr:src/pane/terminal.rs:1311-1312,1522-1524`) and wraps delivered
pastes in `\x1b[200~ … \x1b[201~` **iff** the pane enabled it
(`herdr:src/pane.rs:2638-2649`). tcell's `EnablePaste()` therefore works
unchanged, and Skiff's paste guard (strip raw ESC, disarm leader —
`skiff:internal/app/app.go:1375-1380`) stays effective.

### Esc coalescing / escape-time

Herdr has **no tmux-style `escape-time`** setting. Its raw-input reader
flushes a lone ESC after a ~10 ms idle window
(`RAW_INPUT_IDLE_FLUSH_TIMEOUT_MS = 10`, 150 ms only while a mouse
escape sequence is mid-flight — `herdr:src/raw_input.rs:104-105,366-382`).
On Kitty-keyboard-capable outer terminals herdr pushes keyboard
enhancement flags on the host (`herdr:src/main.rs:26`,
`herdr:src/client/mod.rs:610`), so Esc arrives as an unambiguous event.

The tmux-like munching Skiff already handles still exists in the legacy
path: if `ESC` + `s` land in the same host read, herdr parses a legacy
Alt chord and re-encodes it to the pane as a **single write** of
ESC-prefixed bytes (`herdr:src/input/encode.rs:265-275`) — tcell then
reports Alt+s, and fast Esc,Esc becomes `\x1b\x1b` → Alt+Esc. Skiff's
Alt-modifier branch (`skiff:internal/app/app.go:1436-1452`) covers both,
and the widened `menuEscMs` window (`skiff:internal/app/app.go:62`)
remains harmless. **No change needed.**

### Shift+wheel

Wheel events are forwarded to mouse-reporting panes with the modifier
bits preserved in the SGR encoding (+4 for Shift —
`herdr:src/input/encode.rs:127-129`; forwarding passes
`mouse.modifiers` through unmodified —
`herdr:src/app/input/mouse.rs:1698-1725`). Herdr neither strips Shift
nor emits Zellij-style separate modifier-state events; the crossterm
host parser folds the modifier into the wheel event itself. Skiff's
`modifierStickyWindow` workaround (`skiff:internal/app/app.go:74`) is
unnecessary under herdr but harmless. Caveat: whether Shift+wheel
reaches herdr at all still depends on the outer terminal (same as
today outside herdr).

### TERM, terminfo, truecolor

Every pane gets `TERM=xterm-256color` and `COLORTERM=truecolor`,
deliberately not inheriting the host TERM
(`herdr:src/pane.rs:53-63` incl. rationale comment). Observed with
herdr 0.7.5: matches. tcell's truecolor detection keys off
`COLORTERM=truecolor`, so Skiff's RGB themes render in direct color.
Herdr also answers XTGETTCAP capability queries (CHANGELOG #393) and
forwards focus in/out (mode 1004) to apps that enable focus reporting
(CHANGELOG #1337; `herdr:src/pane.rs:2651-2664`) — Skiff uses neither
today.

### Known issues with tcell apps

No tcell-specific issues found in the 0.7.5 CHANGELOG or source
comments. The historically relevant generic bugs (SGR reports leaking
into pane input #939, motion events not forwarded #419, horizontal
wheel dropped #1349, resize responses for full-screen TUIs #471) are
all marked fixed by 0.7.5.

## Recommended actions in Skiff (all optional)

1. **README / docs note** (documentation only): Skiff works inside herdr
   out of the box; right-click opens herdr's pane menu by default — use
   the ≡ menu, or set `right_click_passthrough_modifier = "ctrl"` in
   herdr's `config.toml` to forward Ctrl+right-click to Skiff's tree
   context menu.
2. **Comment refresh** in `internal/clipboard/clipboard.go`: note that
   herdr intercepts pane OSC 52 and bridges it natively (no wrap, no
   env check needed) so future readers don't add a `HERDR_ENV` branch.

## Open questions

- **OSC 52 payload size cap inside herdr**: the Ghostty engine's
  clipboard-write callback path presumably bounds payload size, but no
  explicit limit for text OSC 52 was located in the 0.7.5 source
  (`MAX_CLIPBOARD_IMAGE_PAYLOAD` applies to image bridging only). Very
  large copies are untested; not verifiable from source alone.
- **Host clipboard end-to-end test**: this environment is a headless
  herdr session, so an empirical write-then-read clipboard verification
  wasn't possible; the claim rests on the source path plus CHANGELOG
  #702.
