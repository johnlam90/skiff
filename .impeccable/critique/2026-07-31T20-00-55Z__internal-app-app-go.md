---
target: Skiff TUI (internal/app/app.go)
total_score: 28
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
timestamp: 2026-07-31T20-00-55Z
slug: internal-app-app-go
---
Method: dual-agent (A: design-review agent · B: detector/evidence agent)

Target: Skiff TUI (internal/app/app.go and rendered surface). Both agents built and drove the editor live in tmux (220×50, 200×50, 100×28-30, 80×24, sandbox copies for destructive flows). Integrity note: commit 16ca326 (leader armed-state indicator, slow-leader hint, selection fg-swap generalization, sidebar-header contrast) landed MID-ASSESSMENT; both agents flagged the concurrent modification. Findings below that cite those four items are confirmed-at-snapshot and already fixed at HEAD, verified by the test fence (TestDrawStatusBar_ArmedEscTag, TestHandleKey_ExpiredLeaderRuneHints, TestSelectionFg_SyntaxReadableOnSelection, TestDrawSidebarHeader_LabelsReadable) and the parent session's own live checks.

## Design Health Score (scored at assessment snapshot)

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Every action flashes; N-of-M counters, GIT badge, ‹›/▲▼ markers. Gaps: 3s flashes overwrite each other; armed-Esc had no indicator at snapshot (fixed at HEAD in 16ca326) |
| 2 | Match System / Real World | 3 | VS Code vocabulary lands. Contradiction: confirm says "Permanently delete" while delete is trash-recoverable and the flash says "≡ Undo delete"; three undo-ish words in one menu (Undo / Revert file / Undo delete) |
| 3 | User Control and Freedom | 3 | Undo/redo/revert + session trash + refuse-clobber restore; but trash silently purged at quit with no warning; no reopen-closed-tab; no keyboard tab switching |
| 4 | Consistency and Standards | 3 | Uniform modal chrome; one menu group mixes three implicit targets (active tab vs active folder), differentiated only by label suffixes |
| 5 | Error Prevention | 3 | Triple root-delete guard verified in both argv forms (SGR: disabled Muted at root, enabled Text on subfolder); bracketed paste inert; gap: folder-op target set by any historical click |
| 6 | Recognition Rather Than Recall | 3 | Whole surface visible with inline shortcuts and dynamic target labels; leader is recall-only in-app |
| 7 | Flexibility and Efficiency | 2 | 11 leader accelerators, finder, custom actions; but no next/prev-tab binding, SelectAll unreachable (dead), no go-to-line, 8-cell chevron steps |
| 8 | Aesthetic and Minimalist Design | 3 | Disciplined layout; the ≡ menu is a 26-row wall; tab names clip mid-word without ellipsis |
| 9 | Error Recovery | 3 | Actionable errors + stderr modal + log path; save failure is only a 3s flash with no persistent state |
| 10 | Help and Documentation | 2 | Great README/CLI help; zero in-app help; leader.go still promises "a future help screen" |
| **Total** | | **28/40** | **Good (lower edge) — solid foundation, address weak areas** |

Cognitive load: chunking (8-item File group), minimal choices (26 options at the one central decision point), and progressive disclosure (one flat layer) fail → moderate. The enabled set at startup is only 6 rows — the wall is mostly disabled rows being rendered.

## Design Specificity Verdict

High interaction-design specificity; deliberate visual borrowing. The input layer could not be transplanted: no-Ctrl policy, Esc-leader with tmux-coalescing fallback, widened menu-Esc window against ESC-munching, sticky-Shift wheel bridging, bracketed-paste stripping, OSC52 passthrough — every one authored for the SSH-into-tmux persona and all verified live. Visual language is intentionally category-derivative (VS Code layout, Tokyo Night) as a vocabulary-transfer choice, with the palette tuned and test-fenced. Authored, not interchangeable.

**Deterministic scan** (detect.mjs over website/, exit 2): same 3 warnings as prior runs — blockquote border-left (contextual false positive), Inter double-counted via noscript fallback. docs/ clean.

**Contrast** (at snapshot): all core pairs pass. The two failing groups B found — 7 kept syntax colors on Selection (3.44–4.47:1) and the Subtle inactive header label (3.39:1) — are fixed at HEAD (SelectionFg swap + Muted/Text-bold header), pinned by theme and render tests.

**Visual overlays**: skipped — terminal target; SGR readback served as ground truth (disabled/enabled folder rows, hover styles, chevron colors all verified by value).

## Overall Impression

The trajectory is real: the P0 is dead (both argv forms verified), the four original P1s stay dead, the session trash works flawlessly in live round-trips, and the tab strip finally serves narrow panes. What's left is a coherence layer, not a defect layer: the delete flow tells three different stories (permanent → undoable → silently purged), tab management works but is shallow (no cycling keys, pecky chevrons, mid-word clipping), and folder operations act on an invisible, history-dependent target. Biggest opportunity: make the delete promise consistent end-to-end — it's the difference between "safety net" and "support ticket."

## What's Working

1. **The input layer absorbs terminal pathology instead of blaming it** — coalesced Esc-s saved live with no stray rune; pasted \x1bq stayed inert; the widened menu window and Alt-fallbacks all verified. Rare, expert-level empathy for the deployment environment.
2. **Destructive-action architecture** — session trash with refuse-clobber restore, triple root guard (verified disabled at root, enabled with named target on subfolders), No-default confirms, delete→undo round trip flawless in sandbox.
3. **Status-communication economy** — one flash channel plus per-surface passive signals; the GIT panel's side-by-side diff is unexpectedly polished for a "tiny" editor.

## Priority Issues

- **[P1] Quit silently destroys the delete safety net, and the copy contradicts itself.** Confirm says "Permanently delete X and everything inside?"; the flash then says "Deleted X — ≡ Undo delete"; quit runs emptyTrash() unannounced (app.go:895). Users get trained that delete is recoverable, then the promise breaks at the least visible moment. Fix: when the trash is non-empty, the quit flow adds one line ("N deleted items will be discarded permanently"), and the confirm copy becomes "Delete X? You can undo until you quit." Suggested command: /impeccable harden
- **[P1] Tab management is the mouse-first product's shallowest mouse surface.** No next/prev-tab binding anywhere; chevrons step 8 cells (~2 clicks per tab); names clip mid-word ("go ×" observed); after manual strip scrolling the active tab can rest off-strip with no marker. Fix: Esc ]/Esc [ cycling bindings, ellipsis on clipped names, and a mouse-first open-files list (click-and-jump). Suggested command: /impeccable polish
- **[P1] Folder rename/delete act on an invisible, history-dependent target.** activeFolder mutates on every tree click and every file open (including via the finder); the menu then offers "Delete folder (src/)" based on a click the user may not remember. Label + confirm basename are the only guards. Fix: show the relative path in the confirm body and reveal+highlight the target row in the tree while the confirm is open. Suggested command: /impeccable clarify
- **[P2] The 26-row menu wall.** One flat modal, disabled rows rendered, scrolls at 80×24; the enabled set at startup is 6 rows. Fix: hide disabled rows (or collapse the file/clipboard groups behind "More…"), keeping the flat list under ~12 visible rows. Suggested command: /impeccable distill
- **[P2] No in-app help; save failure under-signaled.** No shortcut cheat sheet (promised in leader.go:31); "Save failed: %v" lives 3 seconds in the same style as success. Fix: a "Help & shortcuts" menu row rendering leaderBindings(); persist save-failure in the status bar until the next successful save. Suggested command: /impeccable onboard

## Persona Red Flags

**Alex (tmux power user)**: no tab-cycling key — mouse or re-run the finder to switch files; SelectAll unreachable (dead code); no go-to-line despite Ln/Col in the status; find query resets on every reopen. Retained anyway — OSC52-over-SSH clipboard is the keeper feature.

**Jordan (nano refugee)**: survives on the teaching empty state and safe defaults. The 500ms leader window reads as a failed keystroke on slow pairs (the hint shipped at HEAD softens this); M/A/D/R letters and "GIT 10" never explained in-app; untitled-tab save is a dead end with no Save As.

**Sam (low vision / colorblind)**: better than TUI average — letter channel beside every git color, documented WCAG fences, hardware cursor. Remaining: editor gutter bars distinguish added-vs-modified by hue alone (both ▌); overflow markers are single glyphs, easy to miss at low vision; the hard 50×24 floor collides with font zoom.

## Minor Observations

Rebrand residue (.spiceedit/ dir, spiceconfig package, pre-fork file headers) · stale doc comments (find.go still says Esc-g = find-next) · README slightly stale on context-menu rows and fileops.go header on "New File lives only on right-click" · finder caps at 10 rows with no list scrolling ("50/145" implies more reachable than shown) · empty-state "≡" double-gap spacing reads as a typo · status-bar git segment is a click target with zero affordance · single-file mode silently lacks tree/finder/git until you try · drawAt's no-bounds contract leans on every caller trimming correctly · empty-directory tree state still missing · B hit one transient session death that a clean retry couldn't reproduce (third occurrence of the environment anomaly this cycle; nothing implicates skiff).

## Questions to Consider

1. The mouse is the thesis — what would a genuinely mouse-first open-files overview (click-and-jump list) do for the 80×24 tmux pane this editor claims as home turf?
2. At startup only 6 of 26 menu rows are enabled — what if the menu simply was those 6, growing as context does?
3. Delete tells three stories: "permanent" in the confirm, "undoable" in the flash, "gone forever" at quit. Which is the product's actual promise?
4. Should the product coach the leader ("Tip: Esc s saves") after repeated menu-clicks of the same action, or is silent duality the point?
