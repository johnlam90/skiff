---
target: Skiff TUI (internal/app/app.go)
total_score: 29
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
timestamp: 2026-07-31T20-52-37Z
slug: internal-app-app-go
---
Method: dual-agent (A: design-review agent · B: detector/evidence agent)

Target: Skiff TUI (internal/app/app.go and rendered surface) at HEAD 16ca326, stable tree. Both agents built and drove the editor live in tmux (220×50, 200×50, 120×32, 100×28, 80×24; disposable sandboxes for destructive flows).

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Flashes, counters, armed "Esc…" tag (verified live, self-clears), ▲▼/‹› markers. Gaps: quit-time trash discard invisible; single-slot 3s flash, no history |
| 2 | Match System / Real World | 3 | Fluent VS Code vocabulary; "Permanently delete X?" is false (trash-undoable); root tree row shows git letter "A" — a root can't be "added" |
| 3 | User Control and Freedom | 3 | Undo/redo/revert + session trash + refuse-clobber restore; quit silently voids the advertised undo (verified: instant exit, content survives nowhere); Undo delete doesn't reopen the closed tab |
| 4 | Consistency and Standards | 3 | Unified chrome; permanent-vs-undoable copy conflict; byte-slice truncation after rune-length checks in modals (multibyte mojibake risk) |
| 5 | Error Prevention | 3 | Paste inert, leader-timeout swallow (verified), O_EXCL, root triple-guard; the one hole is quit — the only destructive act with zero ceremony |
| 6 | Recognition Rather Than Recall | 3 | Menu doubles as cheat sheet with dynamic target labels; EXPLORER/GIT don't look like tabs; leader table invisible as a set |
| 7 | Flexibility and Efficiency | 2 | No keyboard tab switching anywhere; SelectAll dead (zero callers) — "select whole file + OSC-52 copy" has no path but a full-file drag; chevron steps small |
| 8 | Aesthetic and Minimalist Design | 3 | Restrained chrome; the flat menu is 26 rows / 37-row natural height — scrolls at 80×24 |
| 9 | Error Recovery | 3 | Best-in-class copy ("Create failed: X doesn't exist — create it first"; "Esc s timed out — tap Esc, then s right after" verified); undermined by the 3s single-slot flash |
| 10 | Help and Documentation | 3 | Welcome flash teaches both gestures; menu-as-help; thorough README; still no in-app cheat sheet (leader.go admits "a future help screen") |
| **Total** | | **29/40** | **Good — solid foundation, address weak areas** |

Cognitive load: 2 failures (8-item File group; 26-option menu) → moderate.

## Design Specificity Verdict

Authored, not interchangeable. The Esc gesture system is environment-specific engineering with visible state and best-in-class failure recovery — armed tag, coalesced-save, slow-leader coaching all verified live. VS Code vocabulary is deliberately borrowed familiarity; Tokyo Night chrome is the remaining generic residue.

**Deterministic scan** (website/, exit 2): same 3 warnings (blockquote false positive; Inter double-counted).

**Contrast: every measured pair passes** — first fully clean table across four rounds. SelectionFg verified at value level (keeps String/Variable/Operator/Text, swaps the rest to Text painted at 5.64:1); header Text-bold 11.14 / Muted 5.25; menu border 3.03 (glyph bar, barely).

**New deterministic bug (B, live-proven):** highlight.go:151-154 switches on tt.Category(), making `case chroma.LiteralString` / `LiteralNumber` unreachable — ALL literal tokens paint SynConstant orange ("hi" captured as 38;2;255;158;100, not SynString green). Numbers only look right because SynNumber == SynConstant. Side effect: strings get flattened to Text inside selections (Constant 4.47 < 4.5) when SynString (4.98) would have been kept. Undetected for four rounds because the wrong color is plausible.

**Visual overlays**: skipped — terminal target; SGR readback is ground truth.

## Overall Impression

The interaction layer has crossed from "fixed" to "engineered": all three Esc behaviors verified live, contrast fully clean, destructive flows guarded at three layers. What remains is the coherence debt already on the approved backlog — the delete promise still breaks at quit (reproduced by both agents; content unrecoverable), the copy still over-threatens, and the keyboard still can't switch tabs — plus one newly unearthed correctness bug in the syntax highlighter's category mapping.

## What's Working

1. **The Esc gesture system**: armed "Esc…" tag self-clearing via posted expiry event; tmux-munched Esc+s saves; slow Esc…s swallowed with corrective coaching instead of buffer corruption — all verified live.
2. **Contrast as invariant, not vibes**: SelectionFg swaps only failing colors; palette comments cite the fences; theme_test pins them; git status always carries a letter channel.
3. **Feedback copy quality**: nearly every flash names the object and the next step; dynamic labels disclose targets pre-click.

## Priority Issues

- **[P1] Quit silently destroys the session trash.** Verified by both agents: delete → "≡ Undo delete" advertised → Esc-q → instant exit, no prompt, content survives nowhere (Run's emptyTrash, app.go:906). Fix (user-approved direction): gate quit with a one-line confirm when the trash is non-empty; fold into the existing dirty-quit modal machinery. Suggested command: /impeccable harden
- **[P1] Delete confirm copy contradicts the model.** "Permanently delete X?" while delete is undoable-until-quit. Fix (user-approved): "Delete X? You can undo until you quit"; reserve "permanently" for the quit-time discard prompt. Suggested command: /impeccable harden
- **[P1] No keyboard tab switching; SelectAll dead.** No nextTab/prevTab anywhere; Tab.SelectAll has zero callers — the OSC-52 headline use case ("select whole file, copy over SSH") requires a full-file mouse drag. Fix: Esc ]/Esc [ leader bindings + menu rows; wire SelectAll to a menu row + Esc a. Suggested command: /impeccable polish
- **[P2] String literals highlight as Constant orange (highlight.go category-mapping bug).** tt.Category() never yields LiteralString/LiteralNumber; the sub-category cases are unreachable. Strings paint the wrong color in every file, and selections flatten them unnecessarily. Fix: switch on the token type with SubCategory fallback (check tt itself, then tt.SubCategory(), then tt.Category()); regression test with a real Chroma token stream. Suggested command: /impeccable polish
- **[P2] Menu overflow at 80×24 hides Quit behind a 3-cell ▼.** Fix: stronger affordance ("▼ 12 more") or the user-approved hide-disabled-rows direction (backlog). Suggested command: /impeccable distill

## Persona Red Flags

**Alex**: cannot cycle tabs from the keyboard — six tabs means mouse or re-typing finder queries; no select-all; chevron steps need 3+ clicks to cross the strip. Keeps using it anyway: coalesced Esc-s and OSC-52 clipboard are the retention features.

**Jordan**: "How do I quit?" is below the menu fold at 80×24 behind an unlabeled ▼; right-click means two different things (tree row vs elsewhere); "Permanently delete?" terrifies precisely when the product is actually safe.

**Sam**: strongest TUI baseline the reviewers have seen — WCAG-fenced palette, letter channels, red "no results" as text. Remaining: active-vs-inactive tab bg differ 1.06:1 (label weight carries it); the armed tag is small and corner-positioned; single-slot 3s flashes have no reviewable log.

## Minor Observations

47 orphaned skiff-trash-* dirs in /tmp — emptyTrash runs only on clean exit, killed sessions leak (startup sweep or PID-tagged dirs would fix) · root tree row shows "A" for an untracked child (semantically wrong; '~' or nothing) · byte-slice truncation after rune-length checks in drawConfirm/drawDirtyClose (multibyte filenames can split a UTF-8 sequence) · Undo delete restores the file but not its tab · finder has no empty-results message (blank rows + "0" tail) · stale find.go comments still reference Esc-g as find-next · single-file mode is a trapdoor (no tree/finder/git, no upgrade path; --help implies otherwise) · status-bar git segment is a click target with zero affordance · prompt-modal buttons hit-tested at hard-coded offsets · "Saving untitled tabs is not supported yet" dead end.

## Questions to Consider

1. If the trash makes deletes safe, why does the dialog still say "Permanently" — and if quit makes them permanent, why is quit the only destructive action with zero ceremony?
2. The flat menu already scrolls at 80×24 before custom actions are added — at what row count does the "one opinionated menu" opinion stop scaling?
3. skiff file.go is the most muscle-memory-compatible invocation and delivers the least product. Should single-file mode be a lazy state instead of a constitution?
4. Would one menu row — "Esc shortcuts…" rendering leaderBindings() — convert the hidden second interface from tribal knowledge to product?
