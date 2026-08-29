# Plan 001: Stop the test suite from destroying the developer's real session store

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/session internal/app internal/userconfig internal/customactions internal/format`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1 — execute this before every other plan; until it lands, running the suite is destructive to the developer's own state.
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: tests
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Running `make test` today writes session files for every temporary test
project into the developer's **real** state directory,
`~/.local/state/skiff/sessions/`. That directory is capped at 50 files and
pruned oldest-first on every save, so each test run evicts the developer's
genuine project sessions (open tabs, cursor positions, expanded folders)
and replaces them with dead entries pointing at deleted temp dirs. This was
verified live at plan time: the directory held exactly 50 files, 46 of which
contained `"root": "/tmp/Test..."`. Running the suite is currently a
data-loss operation against the tool's own users — starting with whoever
develops it.

## Current state

Relevant files:

- `internal/session/session.go` — the session store. `stateDir()` resolves the base directory; `Save` writes one file per project then prunes.
- `internal/app/tabops.go` — `closeTab` calls `defer a.saveSession()` (line ~156), one of many app paths that persist sessions during tests.
- `internal/app/session_restore.go:119-125` — `(*App).saveSession` → `session.Save(a.rootDir, p)`.
- 38 test files exist in `internal/app/`; only **4** redirect the state dir (`actions_test.go`, `refresh_test.go`, `session_restore_test.go`, `mousemode_test.go` — found via `grep -rln "XDG_STATE_HOME" internal/app/*_test.go`). Every other app test that closes a tab or quits writes to the real store.
- There is **no `TestMain` anywhere in the repo**: `grep -rn "func TestMain(" --include="*_test.go" .` returns nothing (note: `TestMainExitCodes` in `main_test.go` is a regular test, not a `TestMain`).

`internal/session/session.go:84-96` — the fallback that reaches the real home:

```go
// stateDir resolves skiff's state directory, honouring $XDG_STATE_HOME
// so tests (and unusual setups) can redirect it.
func stateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skiff"), nil
}
```

`internal/session/session.go:190-201` — every save prunes:

```go
func Save(rootAbs string, p Project) error {
	migrateLegacyStore()
	path, err := projectPath(rootAbs)
	...
	pruneSessions(path)
	return nil
}
```

`pruneSessions` (session.go:207-253) removes files beyond `maxProjects = 50`
(session.go:67), oldest `SavedAt` first. That is the eviction mechanism.

Config-side paths have the same shape: `internal/userconfig/userconfig.go:94`,
`internal/customactions/customactions.go:130` (config) and `:244-245`
(actions.log under XDG_STATE_HOME), `internal/format/trust.go:134`,
`internal/format/defaults.go:55` all honour `XDG_CONFIG_HOME` /
`XDG_STATE_HOME` then fall back to the real home. The `format` package
additionally honours `SKIFF_TRUST_FILE` / `SKIFF_DEFAULTS_FILE` overrides
(trust.go:102, defaults.go:50). The session/userconfig/customactions/format
packages' own tests already use `t.Setenv` per-test; `internal/app` is the
confirmed leaker, and the others get the same belt-and-braces guard.

Repo conventions that apply (from `CLAUDE.md`):

- Tests never read or write the user's real config/state — this plan makes
  that documented rule true.
- One `_test.go` per source file, same package. **`TestMain` therefore goes
  into the package's existing primary test file** (e.g. `app_test.go`,
  `session_test.go`), not a new pairless file.
- Every `Test*` function gets a short doc comment explaining the behavior
  it pins down (see `internal/app/fileops_test.go` for style).

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Tests | `make test` | exit 0, all pass |
| One package | `go test ./internal/app/` | exit 0 |
| Lint | `make lint` | exit 0, no output from gofmt/vet/staticcheck |

## Scope

**In scope** (the only files you should modify):

- `internal/app/app_test.go` (add `TestMain`)
- `internal/session/session_test.go` (add `TestMain` + guard test)
- `internal/userconfig/userconfig_test.go` (add `TestMain`)
- `internal/customactions/customactions_test.go` (add `TestMain`)
- `internal/format/format_test.go` or the package's primary test file (add `TestMain`)
- `plans/README.md` (status row only)

**Out of scope** (do NOT touch):

- `internal/session/session.go` and any production source — the fix is
  test-side only; `stateDir()`'s env contract is correct as designed.
- The 4 app test files that already call `t.Setenv("XDG_STATE_HOME", ...)` —
  their per-test overrides compose with `TestMain` (the inner `t.Setenv`
  wins for that test and restores afterwards). Leave them alone.
- `main_test.go` — `TestMainExitCodes` only exercises argument-error exits
  of the built binary (`skiff a.go b.go` fails before an App is built), so
  it does not write sessions. Do not add a `TestMain` there.

## Git workflow

- Branch: `advisor/001-test-state-isolation`
- Commit style: imperative subject + wrapped body explaining why (match
  `git log`, e.g. "Fold single-child dir chains into compact tree rows").
  No AI/Co-Authored-By trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Record the pre-fix leak baseline

Count the real store before touching anything, so Step 4 can prove the fix:

```sh
ls ~/.local/state/skiff/sessions/ 2>/dev/null | wc -l
grep -l '"root": "/tmp/Test' ~/.local/state/skiff/sessions/* 2>/dev/null | wc -l
```

Record both numbers. (At plan time: 50 and 46.)

**Verify**: both commands print a number (0 is fine on a clean machine).

### Step 2: Add `TestMain` to `internal/app/app_test.go`

At the top level of `app_test.go` (which already hosts the shared
`newTestApp` helper), add:

```go
// TestMain redirects every XDG base directory to a throwaway root before
// any test runs. App tests exercise paths that persist sessions and config
// (closeTab's deferred saveSession, quit flows); without this, each run
// wrote temp-project sessions into the developer's real
// ~/.local/state/skiff/sessions/ and the 50-file prune evicted their real
// project sessions. Per-test t.Setenv overrides still compose on top.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skiff-test-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
```

Add `os`/`filepath` imports if missing.

**Verify**: `go test ./internal/app/` → exit 0.

### Step 3: Add the same `TestMain` to the other four packages

Same function (adjust the doc comment to the package: these packages'
tests mostly redirect per-test already; `TestMain` is the backstop that
makes a forgotten `t.Setenv` in a future test harmless rather than a leak)
in:

- `internal/session/session_test.go`
- `internal/userconfig/userconfig_test.go`
- `internal/customactions/customactions_test.go`
- `internal/format`'s primary test file (whichever exists — check with
  `ls internal/format/*_test.go`; use the file matching the package's main
  source file)

**Verify**: `go test ./internal/session/ ./internal/userconfig/ ./internal/customactions/ ./internal/format/` → exit 0.

### Step 4: Add the guard test that pins the redirect

In `internal/session/session_test.go` (same package, so `stateDir` is
reachable):

```go
// TestStateDirIsRedirectedDuringTests pins the isolation contract this
// suite relies on: with TestMain's XDG redirect in place, the session
// store must never resolve under the developer's real home. If this
// fails, some test cleared XDG_STATE_HOME or TestMain was removed —
// either way the suite is about to write into ~/.local/state/skiff.
func TestStateDirIsRedirectedDuringTests(t *testing.T) {
	dir, err := stateDir()
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if strings.HasPrefix(dir, filepath.Join(home, ".local")) {
		t.Fatalf("stateDir resolved under the real home during tests: %s", dir)
	}
}
```

**Verify**: `go test ./internal/session/ -run TestStateDirIsRedirected` → PASS.
Then prove it guards: temporarily run
`XDG_STATE_HOME= go test ./internal/session/ -run TestStateDirIsRedirected`
— with the var forced empty at the process level TestMain re-sets it, so
this should still PASS (TestMain wins). Now temporarily comment out the
`os.Setenv("XDG_STATE_HOME", ...)` line in that package's TestMain, run
again with `XDG_STATE_HOME=`, confirm FAIL, restore the line, re-run,
confirm PASS. (This is the watch-it-fail step for a guard test.)

### Step 5: Prove the leak is closed

```sh
BEFORE=$(ls ~/.local/state/skiff/sessions/ 2>/dev/null | wc -l)
make test
AFTER=$(ls ~/.local/state/skiff/sessions/ 2>/dev/null | wc -l)
echo "before=$BEFORE after=$AFTER"
grep -l '"root": "/tmp/Test' ~/.local/state/skiff/sessions/* 2>/dev/null | wc -l
```

**Verify**: `make test` exits 0; `AFTER` ≤ `BEFORE` (no new files); the
tempdir-rooted count did not increase from Step 1's number. Also confirm
the *mtimes* did not change: `ls -lt ~/.local/state/skiff/sessions/ | head -3`
shows no timestamps from the run you just did.

### Step 6: Lint gate

**Verify**: `make lint` → exit 0, no findings.

## Test plan

- New: `TestStateDirIsRedirectedDuringTests` (Step 4) — the regression
  fence for this whole plan.
- The five `TestMain`s are infrastructure, not tests; their behavior is
  proven by Step 5's before/after file count.
- Verification: `make test` → all pass; Step 5's counts hold.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `grep -rln "func TestMain(" internal/app internal/session internal/userconfig internal/customactions internal/format` lists exactly five files
- [ ] `make test` exits 0
- [ ] `make lint` exits 0
- [ ] Step 5 shows zero new files and zero new `/tmp/Test` roots in `~/.local/state/skiff/sessions/`
- [ ] `git status` shows no modified files outside the in-scope list
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- Any existing test FAILS after the redirect — that test was silently
  depending on real home-dir state, which is itself a bug worth reporting
  with the test's name, not papering over with a per-test exemption.
- `stateDir` in `internal/session/session.go` no longer matches the
  excerpt above (env contract changed).
- Adding `TestMain` to a package causes a compile error about a duplicate
  `TestMain` — the repo grew one since plan time; reconcile instead of
  renaming yours.

## Maintenance notes

- **The developer's store is already polluted.** After this plan lands,
  the dead entries remain. To clean them, first dry-run:
  `grep -l '"root": "/tmp/Test' ~/.local/state/skiff/sessions/*` and review
  the list, then delete exactly those files. Do not script deletion inside
  this plan's execution — it touches state outside the repo; surface the
  dry-run list in your report instead.
- Any **new package** that grows state/config writes needs the same
  `TestMain`; the guard test only covers `internal/session`. A reviewer
  should ask "does this package write outside the repo?" on new packages.
- `CLAUDE.md`'s testing section already states the rule ("Never let a test
  read or write the user's real config / state"); this plan is the
  mechanism. If CLAUDE.md's guidance is updated, point it at `TestMain`
  rather than per-test `t.Setenv` as the primary pattern.
