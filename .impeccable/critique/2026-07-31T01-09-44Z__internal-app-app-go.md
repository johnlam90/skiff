---
target: Skiff TUI (internal/app/app.go)
total_score: 27
p0_count: 0
p1_count: 4
timestamp: 2026-07-31T01-09-44Z
slug: internal-app-app-go
---
Method: dual-agent (A: design-review agent · B: detector/evidence agent)

Target: Skiff TUI (internal/app/app.go and rendered surface), v0.1.0, built from main and driven live in tmux at 200×50, 80×24, and below-minimum sizes.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Strong flashes, dirty ●, find "N of M", branch, ‹› overflow markers — but success and error flashes share one identical style (app.go:2696) |
| 2 | Match System / Real World | 3 | VS Code vocabulary lands; raw Go errors leak into flashes ("Rename failed: %v") |
| 3 | User Control and Freedom | 3 | Esc/click-outside everywhere, undoable revert, Cancel-default dirty modal; no reopen-closed-tab, no select-all (Tab.SelectAll is dead code, tab.go:505) |
| 4 | Consistency and Standards | 3 | One modal chrome system throughout; "Copy rel path" (context menu) vs "Copy relative path" (main menu); Shift+Enter find-prev doesn't survive most terminals |
| 5 | Error Prevention | 3 | Excellent safe defaults (O_EXCL create, no-clobber rename, save-fail aborts close) — but the Esc-leader design *creates* an error path under tmux (see P1 below), and no bracketed-paste handling means pasted ESC bytes can fire leader actions |
| 6 | Recognition Rather Than Recall | 3 | Everything visible in one menu with inline shortcuts, dynamic labels name their target; the cost is a 25-item wall |
| 7 | Flexibility and Efficiency | 2 | Leader unreliable under default tmux escape-time; no keyboard tab-switch or tree nav; no select-all; finder is the only keyboard route to files |
| 8 | Aesthetic and Minimalist Design | 3 | Restrained chrome, coherent palette; loudest element on screen is the status bar (full-accent #7aa2f7), and the 38-row menu modal |
| 9 | Error Recovery | 2 | Custom-action failure modal (stderr + log path) is exemplary; editor errors are 3-second single-style toasts with no persistent trace |
| 10 | Help and Documentation | 2 | Good --help and README; zero in-app help surface (leader.go:29 admits "a future help screen") |
| **Total** | | **27/40** | **Acceptable — solid foundation, significant improvements needed before category-fluent users are happy** |

## Anti-Patterns Verdict

**LLM assessment (product register)**: Not slop. Skiff reads as a designed object with a coherent point of view — mouse-first, no Ctrl chords, one opinionated theme, uniform modal chrome, consistent copy. The product-register trust test ("would a VS Code/vim-fluent user pause at a subtly-off component?") fails at exactly three points: the Esc-leader misfiring under default tmux, the action menu physically cut off at the declared minimum terminal size, and fuzzy-finder ranking that buries `tab.go` for the query "tab". Each is strangeness-without-purpose — the category where product trust erodes.

**Deterministic scan** (detect.mjs over `website/`, the repo's only markup; exit 2): 3 warnings. `side-tab` at website/assets/css/tailwind.css:591 is a **false positive** — it's conventional blockquote `border-left` styling inside a prose class, not a card accent. Two `overused-font` hits (Inter via Google Fonts, head.html:43 and :50) are **one logical usage double-counted** (stylesheet + noscript fallback), though Inter-as-default is a real, mild tell on the marketing site. The detector cannot scan Go TUI rendering; the deterministic evidence for the editor itself is the WCAG contrast math and live tmux captures below.

**Visual overlays**: skipped — the target renders in a terminal, not a browser, so overlay injection doesn't apply. Fallback signal: live tmux captures with SGR color readback confirmed the failing contrast values are actually painted on screen (e.g. disabled menu rows at 38;2;86;95;137 on 48;2;31;32;46).

## Overall Impression

Skiff's safety engineering and modal consistency are better than most shipping TUIs — the dirty-quit flow alone (named file, Cancel default, red Discard, save-failure aborts quit) is the best moment in the product. But the flagship gesture (Esc-leader) breaks in the flagship environment (tmux), the single most important menu is taller than the minimum supported terminal, and secondary text fails WCAG across ten distinct color pairs. The single biggest opportunity: make the Esc-leader reliable under tmux — it's the most-repeated interaction and currently inserts stray characters into users' files.

## What's Working

1. **Destructive-flow safety engineering** — safe-default focus on every dangerous modal, O_EXCL create, no-clobber rename, save-then-close and save-all-then-quit that abort on first failure, format-on-save trust prompt keyed to a config hash. Thought through, not boilerplate.
2. **One consistent modal system** — every overlay (menu, prompt, confirm, dirty, finder, diff, log) shares chrome, esc hint, click-outside dismiss, hover highlight, and mutual exclusion. The interface teaches itself once.
3. **Terminal-honest affordances** — ‹/› line-overflow markers, red "no results" find counter, dynamic menu labels naming their target ("Delete folder (subdir/)"), version stamp tucked into the modal border, splitter that brightens while dragged.

## Priority Issues

- **[P1] Esc-leader collides with tmux escape-time and corrupts the buffer.** Under default tmux (escape-time ~500ms ≈ Skiff's doubleEscMs 500ms), fast Esc+s arrives coalesced as Alt+s: the leader never fires and the raw rune is inserted into the file (app.go:1251 KeyRune fall-through). Reproduced live: coalesced ESC sequences never opened the menu; only spaced literal ESC bytes did. Why it matters: the save gesture types an `s` into the code of the exact audience the product targets. Fix: treat KeyRune+ModAlt as leader-equivalent (Alt+s ≡ Esc,s), treat Alt+Esc as menu-open, never insert Alt-modified runes; handle bracketed paste (EventPaste) while there. Suggested command: /impeccable harden
- **[P1] Action menu is taller than the minimum supported terminal.** The modal renders 38 rows (25 items + 7 dividers + chrome); minHeight is 24 (app.go:47-48). Verified at 80×24: everything after "Delete folder" — clipboard group, sidebar toggle, and **Quit editor** — is clipped, unreachable, unscrollable. Why it matters: mouse-first product loses its only exit affordance to geometry at its own declared minimum; first-timers can't quit. Fix: cap modal height at screen height−2 with scrolling (diff view already has the machinery), or two-column layout at short heights. Suggested command: /impeccable adapt
- **[P1] Finder ranking violates its own documented contract.** Greedy first-occurrence alignment (finder score.go:88-105) scores "tab" against `internal/editor/tab.go` at 1 while `website/assets/js/copy-button.js` scores 39; live capture confirms tab.go absent from the top 10 of a 143-file repo. README promises "typing `tab` finds tab.go first." Fix: retry the greedy walk anchored at the basename (or at each occurrence of the first query rune) and keep the best score. Suggested command: /impeccable polish
- **[P1] Secondary text fails WCAG contrast in ten distinct painted pairs.** Computed from theme.go and code-verified pairings: Muted/SynComment on BG 2.76:1 (line numbers, all code comments), on LineHL 2.60:1, on SidebarBG 2.91:1 (inactive tabs, tree header); selected comments 1.47:1 (tab.go:652); comment under find-match tint 1.17:1; Subtle close-×/splitter/modal border 1.32–1.48:1. Git state in the tree is hue-only (red/green deuteranopia collision) — the GIT panel's M/A/D letters do it right, the tree doesn't. Fix: lift Muted toward ~#7a83ab, split SynComment from Muted, brighten Subtle for interactive glyphs, recompute selection-fg for low-contrast tokens, add a one-cell status letter in the tree. Suggested command: /impeccable colorize
- **[P2] External-change conflict is a 3-second toast, then silent overwrite.** "%s changed on disk — your edits will overwrite on save" (app.go:978) flashes once; miss it and the next save destroys the external change with no re-confirmation, no persistent conflict state. Compounded by success and error flashes sharing one style. Fix: persistent status segment for conflicted tabs + confirm on next save; add an error style to flashes. Suggested command: /impeccable harden

## Persona Red Flags

**Alex (tmux SSH power user)**: Esc-s misfires under default escape-time and types a literal `s` into the diff he ships — he will either set escape-time 0 or leave. No select-all (dead code), no keyboard tab-switch, no keyboard tree nav; short tmux splits hit the 50×24 refusal wall or the truncated menu. Shift+Enter find-prev doesn't reach the app in most terminals.

**Jordan (nano-grade first-timer)**: Best-case launch — persistent empty-state hint and an all-text menu mean the first click succeeds. But "Quit editor" is the last menu row, i.e. the first casualty of the 24-row clip — on a small terminal a first-timer cannot see how to exit. In an empty directory the welcome flash still says "click a file to open" though the tree contains no files, and there's no empty-folder hint. Menu "Paste" only serves the internal clipboard; the rescue flash arrives only after a confused attempt.

**Sam (low vision / colorblind)**: Line numbers, comments, and all secondary text at 2.60–2.91:1 — below AA. Git tree state is hue-only; modified-vs-added indistinguishable for deutans. One-cell hit targets (tab ×, splitter column at exactly x == splitterX(), gutter bars) demand motor precision. Keyboard-only path exists for files (Esc-p) and the menu, but not tabs or the tree; state changes are announced only via the bottom-row flash — at least a stable location.

## Minor Observations

- Tab.SelectAll (tab.go:505) is dead code — no menu row, no binding.
- No bracketed-paste handling: pasted content containing ESC bytes can arm the leader (pasted `\033q` on a clean buffer quits).
- Tab bar has no overflow handling — enough tabs run off the right edge silently.
- Nerd-Font detection inspects the remote host's fonts over SSH; the rendering font is the local terminal's (config override exists).
- Context-menu labels abbreviated ("Copy rel path") vs main menu's full words — one vocabulary, please.
- Menu title row says just "Menu" — could carry context (file name / root).
- "Window too small" message renders with doubled spaces around the em dash.
- Status-flash catalogue (34 distinct strings) is mostly plain-language and good; raw %v error leaks are the exception.
- One-off anomaly observed during evidence capture: a first launched instance rendered fully but ignored all input (ESC bytes confirmed read by the process); relaunches behaved normally. Cause undetermined — possibly interference from a second instance sharing the tmux server during parallel testing. Worth keeping in mind if users ever report a "frozen on launch" editor.

## Questions to Consider

1. If tmux is the home environment, why is the flagship gesture tuned to a 500ms window that exactly mirrors tmux's default escape-time? Should Alt+letter simply *be* the leader alias — an invisible fix with zero new surface?
2. The ≡ menu is trying to be a menu bar *and* a command palette, and at 25 rows it is neither. The finder modal already proves the search-list pattern — what would "8 common verbs + searchable everything else" look like?
3. "Mouse-first" — yet the most polished features (finder, git panel) are leader-key-first in practice, and the tree/tab bar are mouse-only. Is the real design "two complete input paths," and if so, where's the keyboard path for tabs and the tree?
4. What is the smallest honest Skiff? A 3-way tmux split gives ~80×16; today that's a refusal screen. Which chrome would you shed to be useful there?
