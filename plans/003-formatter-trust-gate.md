# Plan 003: Close the formatter install prompt's auto-trust of unseen config, and stop consent text truncating on narrow terminals

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/format.go internal/app/format_test.go internal/overlay/confirm.go internal/overlay/confirm_test.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW-MED (UX change: one extra prompt in a legitimate flow)
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Skiff runs project-configured formatter commands on save, gated by a trust
store whose whole promise (per `internal/format/trust.go:10-18`) is:
"Without it, cloning a malicious repo and saving a file would silently exec
whatever the dotfile said." That promise has a hole. When a user with a
personal `format-defaults.json` saves a file in a freshly cloned repo, the
"Install formatter for this project?" prompt fires, and answering Yes
**merges** the user's one entry into the repo's pre-existing
`.skiff/format.json` and then trusts the hash of the *entire merged file* —
silently approving every command the hostile repo already declared, none of
which were shown in the prompt. The next save of a matching file executes
them. Separately, the trust prompt that *does* show commands wraps them to a
constant 80-cell width but is drawn truncated to the real terminal width, so
on terminals narrower than ~88 columns the tail of each command is replaced
with `…` — and the code's own comment says a hidden command tail "is
precisely how a hostile format.json would smuggle a payload past the
prompt."

## Current state

Files and their roles:

- `internal/app/format.go` — the format-on-save flow: routing
  (`runFormatOnSave` :70), trust check (`runWithTrust` :96), the install
  offer (`maybeOfferInstall` :132), the trust prompt
  (`openFormatTrustPrompt` :194), body rendering (`formatTrustPromptBody`
  :229, `wrapIndented` :290), the install prompt
  (`openFormatInstallPrompt` :370).
- `internal/format/defaults.go` — `InstallCommandIntoProject` (:113)
  merges the new entry into the existing project config, deliberately
  preserving prior entries (comment at :126-128), and returns the merged
  file's hash.
- `internal/overlay/confirm.go` — the Confirm prefab.
  `ConfirmBodyTextWidth = ConfirmBodyWidth - 4` = 80 (:44), `frameWidth()`
  clamps the 84-cell frame to the terminal (:93-108), and the body draw
  truncates each row: `trimRunes(c.Body[c.scroll+i], r.W-4)` (:296 area).
- `internal/app/format_test.go` — the existing suite; every test starts
  with `useTestTrustFile(t)` (:45) which redirects `SKIFF_TRUST_FILE` and
  `SKIFF_DEFAULTS_FILE` to temp paths, so no test ever touches real user
  config. Model all new tests on this file.

The auto-trust hole, `internal/app/format.go:385-397`:

```go
	c := a.openConfirm(title, msg, func(app *App) {
		// Yes — merge into project config, trust the new hash, run.
		hash, err := format.InstallCommandIntoProject(root, ext, argvTemplate)
		if err != nil {
			app.flash("install failed: " + err.Error())
			return
		}
		// Auto-trust: the user just consented to the exact contents
		// they wrote. Re-prompting would be busywork. ...
		app.persistTrust(root, hash, true)
```

The comment's claim is true only when no project config existed. With a
pre-existing config, `InstallCommandIntoProject` merges
(`defaults.go:126-139`) and `persistTrust(root, hash, true)` approves
entries never displayed: the prompt message is only
`"Add %s for .%s to %s?"` (`format.go:382`) and no `c.Body` is set.

The gate that lets the prompt fire on an untrusted repo,
`internal/app/format.go:174-178` (only `TrustDenied` bails; a fresh clone
is `TrustUnknown` and falls through):

```go
	if cfg, _ := format.Load(a.rootDir); cfg != nil {
		if tf.CheckTrust(a.rootDir, cfg.Hash()) == format.TrustDenied {
			return
		}
	}

	a.openFormatInstallPrompt(tab, ext, template)
```

The truncation half. Body lines are wrapped to the constant
(`format.go:239-242`):

```go
		body = append(body, wrapIndented(
			fmt.Sprintf("  .%s  ", ext),
			renderArgv(cfg.Commands[ext]),
			overlay.ConfirmBodyTextWidth)...)
```

but drawn clipped to the *runtime* frame (`overlay/confirm.go`, Draw):

```go
		for i, rows := 0, c.bodyRows(); i < rows && c.scroll+i < len(c.Body); i++ {
			drawText(scr, r.X+2, r.Y+4+i, trimRunes(c.Body[c.scroll+i], r.W-4), bodyStyle)
		}
```

On an 80-column terminal `frameWidth()` clamps to 80, so `r.W-4 = 76` and
every 80-cell wrapped line loses its tail to `trimRunes`
(`overlay/chrome.go:101-113` appends `…`).

Conventions that apply (from CLAUDE.md): every function gets a doc comment;
tests live in the same package, one `_test.go` per source file, TDD (write
the failing test first); never let a test read real config — the
`SKIFF_TRUST_FILE`/`SKIFF_DEFAULTS_FILE` overrides exist for exactly this.
Vocabulary (CONTEXT.md): these surfaces are "overlays"/"prefabs", not
modals/dialogs — use those words in comments.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Package tests | `go test ./internal/app/ ./internal/overlay/` | ok, all pass |
| Focused | `go test ./internal/app/ -run 'Format\|Install\|Trust'` | ok |
| Full gate | `make test` | all packages ok (race) |
| Lint gate | `make lint` | no output after staticcheck line |

## Scope

**In scope** (the only files you should modify):
- `internal/app/format.go`
- `internal/app/format_test.go`
- `internal/overlay/confirm.go` (one small exported method)
- `internal/overlay/confirm_test.go`

**Out of scope** (do NOT touch, even though they look related):
- `internal/format/defaults.go` — the merge behavior of
  `InstallCommandIntoProject` is correct and relied on; the fix is about
  *consent*, not about the merge.
- `internal/format/trust.go` — the hash/trust mechanics are sound
  (verified in audit); do not change key shapes or file format.
- `internal/app/modals.go` `openConfirm` — keep the prefab wiring as is.

## Git workflow

- Branch: `advisor/003-formatter-trust-gate`
- Imperative commit subjects, matching repo style (e.g. "Fold single-child
  dir chains into compact tree rows"). No AI attribution trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Failing test — merged config must not be silently trusted

In `internal/app/format_test.go`, add
`TestMaybeOfferInstall_MergedConfigNotSilentlyTrusted`, modeled on
`TestMaybeOfferInstall_AcceptWritesProjectConfig` (:406) for the accept
mechanics:

- `useTestTrustFile(t)`; `useTestDefaultsFile(t,
  `{"commands":{"go":["gofmt","-w","$FILE"]}}`)`.
- `writeFormatConfig(t, root, `{"commands":{"py":["evil-cmd","$FILE"]}}`)` —
  a pre-existing project entry the user has never been shown.
- Open a `.go` tab, `a.runFormatOnSave(...)` → install prompt opens.
- Accept it the way the existing accept test does.
- Assert: `format.Load(root)` now has BOTH `go` and `py` entries (merge
  preserved), AND `tf.CheckTrust(root, mergedCfg.Hash())` is
  `format.TrustUnknown` — not trusted — AND a confirm is (still) open whose
  `Body` mentions `py` (the trust prompt for the merged file).

**Verify**: `go test ./internal/app/ -run MergedConfigNotSilentlyTrusted`
→ FAILS (today the merged hash is trusted and no second prompt opens).

### Step 2: Show the post-merge commands and gate the auto-trust

In `internal/app/format.go`, rework `openFormatInstallPrompt`:

1. Before building the confirm, `preCfg, _ := format.Load(root)` and
   compute the pre-merge trust state (load the trust file the way
   `maybeOfferInstall` does).
2. Build an in-memory preview of the merged commands (copy
   `preCfg.Commands` into a fresh map, set `ext` → template, wrap in a
   `*format.Config`), and set the confirm's `Body` from it via
   `formatTrustPromptBody` so the user sees every command the merged file
   will contain. Keep the one-line `msg` as the summary.
3. In the Yes callback, after `InstallCommandIntoProject` succeeds:
   - If `preCfg == nil` (no prior project config) **or** the pre-merge
     hash was `TrustAllowed`: keep today's behavior — `persistTrust(root,
     hash, true)` and run the formatter. The "you consented to what you
     saw" claim is now actually true, because the Body showed the full
     list.
   - Otherwise (pre-merge state `TrustUnknown`): do **not** persistTrust.
     Reload the fresh config (`format.Load(root)`) and route to
     `a.openFormatTrustPrompt(tab, freshCfg, substituteFile(argvTemplate,
     tabPath))` so the standard trust prompt decides the merged file's
     fate. Keep the tree refresh + `persistInstallDecline(root, ext,
     false)` on both branches.
4. Update the now-false comment ("the user just consented to the exact
   contents they wrote") to state the new rule and why.

`maybeOfferInstall`'s TrustDenied bail (:174-178) stays exactly as is.

**Verify**: `go test ./internal/app/ -run 'MaybeOfferInstall\|FormatTrust'`
→ all pass, including Step 1's test and the existing
`TestMaybeOfferInstall_AcceptWritesProjectConfig` (fresh-project auto-trust
must keep passing — if it fails, your gate is too strict; see STOP).

### Step 3: Failing test — no consent row may end in an ellipsis at 60 cols

Add `TestFormatTrustPrompt_NarrowTerminalShowsFullCommands` in
`internal/app/format_test.go`:

- Build `newTestApp`, then resize: `a.screen.(tcell.SimulationScreen)`,
  `SetSize(60, 40)`, and set `a.width, a.height = 60, 40` (the confirm's
  `Size` hook closes over these — see `modals.go:105`).
- Open the trust prompt with a config whose argv renders well past 56
  cells (e.g. `{"commands":{"go":["gofmt","-w","--some-quite-long-flag=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","$FILE"]}}`).
- Draw the app (`a.draw()`), then scan the simulation screen's cells and
  fail if any row contains the rune `…` (model the cell scan on the
  `renderAndCollect`/`rowText` helpers in `internal/filetree/filetree_test.go:531-558`;
  `internal/app/draw_test.go` has equivalents in-package — prefer those).

**Verify**: `go test ./internal/app/ -run NarrowTerminalShowsFullCommands`
→ FAILS (tails currently truncate to `…`).

### Step 4: Wrap consent text to the runtime width

1. In `internal/overlay/confirm.go`, add an exported method with a doc
   comment:

   ```go
   // BodyTextWidth returns the usable text width for Body rows at the
   // confirm's current frame width — what callers should wrap consent
   // text to, so a narrow terminal wraps instead of truncating.
   func (c *Confirm) BodyTextWidth() int { return c.frameWidth() - 4 }
   ```

2. In `internal/app/format.go`, give `formatTrustPromptBody` a `width int`
   parameter (and pass it through to the `wrapIndented` call), then update
   both call sites to pass `c.BodyTextWidth()` **after** the confirm has
   been created by `openConfirm` (its `Size` hook is set there):
   - `openFormatTrustPrompt` (:214 today) — move the `c.Body = ...`
     assignment after `openConfirm` returns, passing the runtime width.
   - the new Body assignment in `openFormatInstallPrompt` from Step 2.
   Guard the width: never wrap wider than
   `overlay.ConfirmBodyTextWidth`, never narrower than ~20 (wrapIndented
   already floors `avail` at 8; the guard keeps prefixes sane).

**Verify**: `go test ./internal/app/ ./internal/overlay/` → all pass,
including Step 3's test. Also add a one-case test for `BodyTextWidth` in
`internal/overlay/confirm_test.go` (narrow Size → smaller width; wide
Size → 80).

### Step 5: Full gates

**Verify**: `make test` → ok everywhere. `make lint` → clean.

## Test plan

- `TestMaybeOfferInstall_MergedConfigNotSilentlyTrusted` (Step 1) — the
  regression this plan exists for.
- `TestOpenFormatInstallPrompt_BodyListsMergedCommands` — install prompt's
  `Body` contains both the new entry and each pre-existing extension.
- `TestMaybeOfferInstall_FreshProjectStillAutoTrusts` — no pre-existing
  config → Yes trusts and runs with no second prompt (pin the preserved
  behavior; may already be covered by `..._AcceptWritesProjectConfig`).
- `TestFormatTrustPrompt_NarrowTerminalShowsFullCommands` (Step 3).
- `BodyTextWidth` unit case in `internal/overlay/confirm_test.go`.
- Pattern to follow: `internal/app/format_test.go` throughout — note every
  test's doc comment explains the behavior it pins (repo convention).

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `make test` exits 0
- [ ] `make lint` exits 0
- [ ] `go test ./internal/app/ -run 'MergedConfigNotSilentlyTrusted|NarrowTerminalShowsFullCommands'` passes
- [ ] `grep -n "the user just consented to the exact contents" internal/app/format.go` returns no matches (comment rewritten)
- [ ] `git status --short` shows only in-scope files modified

## STOP conditions

Stop and report back (do not improvise) if:

- The excerpts above don't match the live code (drift since `2616761`).
- `TestMaybeOfferInstall_AcceptWritesProjectConfig` fails after Step 2 —
  the fresh-project auto-trust path regressed; report rather than loosening
  the gate ad hoc.
- Making the Body width dynamic requires changing `Confirm.Draw`'s
  truncation itself or the overlay `Rect`/`Centered` geometry — that is a
  larger overlay change this plan deliberately avoids.
- You find another call site of `formatTrustPromptBody` beyond the two
  named here.

## Maintenance notes

- Terminal resize *while the prompt is open* still shows lines wrapped for
  the old width (re-wrapped only on next open). Deliberately accepted —
  re-wrapping at draw time would put allocation in the per-frame path.
  Revisit only if users report it.
- Any future prompt that asks consent for executable content must set a
  `Body` wrapped via `BodyTextWidth()` — never rely on `trimRunes`.
- Reviewer focus: the trust-state decision table in Step 2 (nil / Allowed
  / Unknown), and that `TrustDenied` still never reaches the install
  prompt.
