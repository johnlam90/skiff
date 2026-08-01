---
target: Skiff TUI (internal/app/app.go)
total_score: 26
max_score: 40
na_heuristics: 
p0_count: 1
p1_count: 2
timestamp: 2026-07-31T17-15-17Z
slug: internal-app-app-go
---
Method: dual-agent (A: design-review agent · B: detector/evidence agent)

Target: Skiff TUI (internal/app/app.go and rendered surface), working tree with the four P1 fixes from the 2026-07-31 round applied. Both agents built and drove the editor live in tmux (220×50, 80×24, 120×35, 100×30, 45×20).

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Rich flashes/counters, dirty ●, ▲▼ menu overflow markers — but tab-bar overflow has zero indicator, and the status bar shows a literal "." as project name on default launch (app.go:2865) |
| 2 | Match System / Real World | 3 | Mostly plain speech; '~' mixed-change glyph unexplained, untracked files badged "A" not "U", language label is the raw extension |
| 3 | User Control and Freedom | 2 | Undo/redo/revert and cancel-default modals; but delete = os.RemoveAll, permanent, no trash/undo; no reopen-closed-tab |
| 4 | Consistency and Standards | 3 | Modal chrome uniform; abs-vs-verbatim path comparison inconsistency (fileops.go:482 vs app.go setActiveFolder) drives the P0; two Esc windows (500ms leader / 1200ms menu) |
| 5 | Error Prevention | 1 | The flagship guard fails: "Delete folder (skiff/)" — the project root — is ENABLED on default launch; live sandbox repro recursively deleted the project root |
| 6 | Recognition Rather Than Recall | 3 | Menu-as-catalog with inline shortcuts and dynamic target labels; gesture layer (double-Esc, splitter, gutter-click-diff, shift+wheel) invisible |
| 7 | Flexibility and Efficiency | 3 | Leader survives tmux coalescing (verified live), fuzzy finder, bare-letter menu accelerators; missing go-to-line, word-jump, select-all, project-wide text search |
| 8 | Aesthetic and Minimalist Design | 3 | Disciplined Tokyo Night; the ≡ menu is 25 rows / 8 groups — a wall that needs scrolling at 80×24 |
| 9 | Error Recovery | 3 | stderr-bearing failure modals with log path; save-fail aborts close; disk-conflict warning still a 3-second flash |
| 10 | Help and Documentation | 2 | Good CLI help/README; no in-app help surface for the gesture layer; welcome flash lasts 3s |
| **Total** | | **26/40** | **Acceptable — held down by the P0/P1 mechanics, not the design language** |

All 10 heuristics applicable (Operate surface); max 40.

## Design Specificity Verdict

**Authored-for-this-product.** The input system is engineered against measured tmux/Zellij byte-mangling (Alt-rune coalescing, menuEscMs vs munched double-Esc, bracketed-paste guard, sticky-Shift wheel bridging, OSC52 passthrough) — verified live: coalesced Esc-s saved with no stray rune, pasted \x1bq inserted only 'q', the finder ranks tab.go first for "tab". Not a themed template; a thesis executed with unusual depth. Trust breaks at exactly the product's home terrain: tab overflow in 80-col panes hides the active tab, and the root-delete landmine sits enabled in the default menu.

**Deterministic scan** (detect.mjs over website/, exit 2): same 3 warnings as the prior run — blockquote border-left (false positive for the side-tab rule), Inter double-counted via its noscript fallback (one real usage). samples/ and docs/ scan clean. The TUI's deterministic evidence is the WCAG table: every previously failing pair now passes (Muted 4.99–5.25, Subtle glyphs 3.03–3.39, comments 4.75–5.05, Text-on-FindMatch 4.93). New precision findings: 7 of 10 syntax colors kept atop Selection sit at 3.44–4.47:1 (the tab.go comment overclaims "every other syntax color clears WCAG AA"), and the inactive sidebar header label ("GIT 12" in Subtle) is text at 3.39:1.

**Visual overlays**: skipped — terminal target; fallback evidence is SGR readback confirming painted values (Muted 0x8089b1, Subtle 0x606996 live in captures).

## Overall Impression

The four P1s from the last critique are verifiably gone — both agents independently exercised the exact failing gestures and they all pass now. The deeper look this earned surfaced one pre-existing showstopper the first round missed because both of its agents happened to launch with absolute paths: on a default `skiff` launch, the menu offers Rename/Delete of the project root as enabled rows, and the delete goes through. Error Prevention collapses from "excellent set of guards" to 1/4 on that single bug. The biggest opportunity is now trust in destructive flows: fix the root guard, add an undo story for deletes, and the score jumps a band.

## What's Working

1. **Terminal-reality engineering as product identity** — all four hostile-input tests (coalesced save, munched double-Esc, ESC-byte paste, clamped-menu keyboard nav) passed live in tmux. No category peer at this size does this.
2. **Feedback vocabulary that names its targets** — "New file (in skiff/)", red no-results counter, n/total finder tail, stderr-bearing failure modals with a log path.
3. **Measured accessibility floor with a test fence** — contrast bars computed, cited in theme.go comments, and pinned by theme_test.go's WCAG table; git letters (M/A/D/R/~) ride alongside hue in the tree.

## Priority Issues

- **[P0] Folder rename/delete targets the project root on default launch.** hasActiveSubfolder (fileops.go:480-487) string-compares the abs-resolved activeFolder to the verbatim rootDir ("." for `skiff` or `skiff .`), so the guard never fires; "Delete folder (skiff/)" renders enabled (SGR-verified) and a live sandbox repro deleted the entire project root while the editor kept running. Fix: filepath.Abs the rootDir once in New()/NewSingleFile, compare canonical-to-canonical, and hard-refuse tree.Root.Path in doDeletePath as defense-in-depth; regression test both launch forms. Suggested command: /impeccable harden
- **[P1] Tab-bar overflow hides the active tab with no indicator or recovery.** layoutTabs (app.go:2662-2681) lays rects unbounded; verified at 80×24 with five tabs: the active file's tab is entirely absent, hidden tabs are unclickable. Mouse-first product, core loop, home terrain. Fix: scroll the strip to keep the active tab visible plus ‹ › overflow chevrons (the editor already has this vocabulary), or an MRU dropdown at the right edge. Suggested command: /impeccable adapt
- **[P1] Destructive ops are permanent with one soft confirm.** os.RemoveAll, no trash, no undo; the same Yes/No modal gates a single-file delete and a whole-tree delete. Fix: session trash dir with an "Undo" window in the status bar, or type-the-name confirmation for recursive deletes. Suggested command: /impeccable harden
- **[P2] Esc-leader timing cliff still inserts a literal rune on slow links.** Coalescing is fixed, but an armed Esc that expires (>500ms — real over laggy SSH) lets the next 's' insert silently; the user believes they saved. Fix: 1-cell armed-gesture indicator in the status bar (vim showcmd); on expiry, swallow the first leader-bound rune inside the 1.2s menu window and hint instead of inserting. Suggested command: /impeccable harden
- **[P2] Selection and sidebar-header contrast residuals.** Seven syntax colors kept on Selection measure 3.44-4.47:1 (tab.go:656 comment overclaims AA); the inactive sidebar header label is Subtle text at 3.39:1. Fix: extend the selection fg-swap to any token below 4.5 (precompute the set in theme_test-verified code) and lift the header label to Muted. Suggested command: /impeccable colorize

## Persona Red Flags

**Alex (tmux SSH veteran)**: full loop in under 60s and it survives tmux coalescing now (verified). Breaks: >500ms latency turns Esc-s into a typed 's' silently; no go-to-line/word-jump/select-all/project-grep, so Alex greps in another pane; >3 files in an 80-col split hides buffers with no recovery except closing visible tabs.

**Jordan (nano refugee)**: empty-state text and clickable everything carry the first session. Breaks: single Esc and Ctrl+S both do nothing silently; "GIT 12", '~', M/A badges unexplained; the finder's no-match state is blank rows with a "0" tail — reads as broken; and the enabled "Delete folder (skiff/)" row is a day-one landmine. No in-app help.

**Sam (low vision / colorblind)**: text contrast genuinely handled now — every text pair measured ≥4.5 except the two residuals above; tree pairs letters with hue. Breaks: enabled-vs-disabled menu rows differ only by fg brightness; git gutter bars are color-only (same ▌ glyph); terminal-font zoom collides with the hard 50×24 floor — "Window too small" is an accessibility wall.

## Minor Observations

Bare-letter menu accelerators exist but are unlabeled (hints say "Esc s") · Shift+Enter find-prev unreliable in most terminals — needs a second binding · "Find in file" vs "Find file in project" adjacent and confusable; no project-wide text search · status bar shows "." as workspace name on default launch · raw extension as language label · untitled-tab save is a dead end ("not supported yet", no Save As) · right-click in the editor body opens the main menu, not a caret context menu · disk-conflict warning remains a 3s flash (deferred from last round) · success and error flashes still share one style (deferred) · empty-directory tree state still missing (deferred) · Tab.SelectAll still dead in production code · '›' clip markers, clamped-menu auto-scroll, and the version-stamped border all verified working.

## Questions to Consider

1. If mouse-first is the identity, why does the most-clicked surface (the tab bar) fail silently at exactly the widths tmux splits produce? What would tabs designed for 80 columns look like?
2. The ≡ menu is 25 rows and grows with custom actions — at what point does it stop being a menu and become a typed palette? You already ship a fuzzy scorer.
3. What is the product's answer to "I deleted the wrong thing"? A session trash dir is ~30 lines and converts the scariest moment into a shrug.
4. Esc is the only modifier you trust — so why is its armed state invisible? What would a 1-cell pending-gesture indicator buy in user confidence?
