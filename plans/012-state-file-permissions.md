# Plan 012: Tighten permissions on skiff's per-user state files

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/customactions/ internal/atomicfile/ internal/format/trust.go internal/session/session.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Skiff's README positions it as the editor you drop on a shared remote box.
Its per-user state is currently world-readable: the custom-actions log —
which records **the full combined stdout+stderr of every custom action the
user runs** (scp/ssh one-liners are the documented use case, so hostnames
and whatever a verbose tool prints land there) — is created `0644` in a
`0755` directory, and the trust store and session files (every project root
and open file path) are written `0644`. On a multi-user host any local
account can read all of it. Tightening to owner-only costs nothing and
matches what ssh, gh, and every credential-adjacent tool does.

## Current state

Files and their roles:

- `internal/customactions/customactions.go` — loads
  `~/.config/skiff/actions.json` and appends the run log. The permission
  sites, `:300-303`:

  ```go
  	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
  		return fmt.Errorf("mkdir log dir: %w", err)
  	}
  	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
  ```

  What lands in that log (`AppendLog`, `:313-332`): a header, the command
  string, the file vars, then `r.Output` — which
  `internal/app/actions.go:268-277` fills with
  `cmd.CombinedOutput()` unconditionally, success or failure.
- `internal/atomicfile/atomicfile.go` — the shared temp+fsync+rename
  writer. `:46`: `os.MkdirAll(dir, 0755)`. Its doc already promises "the
  temp file is created 0600 and chmod'd before the rename so the bytes are
  never briefly world readable" (`:41-43`) — the *final* perm is the
  caller's choice.
- Callers passing `0644` today (verify each):
  - `internal/format/trust.go:181` — `~/.config/skiff/format-trust.json`
    (which projects the user trusts; **tighten to 0600**)
  - `internal/session/session.go:173` — per-project session files under
    `~/.local/state/skiff/sessions/` (project roots, open paths, cursor
    positions; **tighten to 0600**)
  - `internal/userconfig/userconfig.go:248` — `~/.config/skiff/config.json`
    (theme/icons/wrap booleans; **leave 0644** — nothing sensitive, and
    users legitimately cat/share it)
  - `internal/format/defaults.go:151` — this one writes the **project's**
    `.skiff/format.json`, which is repo content teammates pull; it MUST
    stay `0644`. Do not change it.

Repo conventions: doc comment on every function; one `_test.go` per source
file in the same package; `t.TempDir()` for FS state; tests must never
touch real config — session paths honor `XDG_STATE_HOME`/`XDG_CONFIG_HOME`
and the format store honors `SKIFF_TRUST_FILE` (see
`internal/app/format_test.go:36-52` for the pattern).

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Focused tests | `go test ./internal/customactions/ ./internal/atomicfile/ ./internal/format/ ./internal/session/` | ok |
| Full gate | `make test` | all ok (race) |
| Lint gate | `make lint` | clean |

## Scope

**In scope** (the only files you should modify):
- `internal/customactions/customactions.go` + `customactions_test.go`
- `internal/atomicfile/atomicfile.go` + `atomicfile_test.go`
- `internal/format/trust.go` + `trust_test.go`
- `internal/session/session.go` + `session_test.go`

**Out of scope** (do NOT touch, even though they look related):
- `internal/format/defaults.go` — writes the project-shared
  `.skiff/format.json`; its `0644` is correct and load-bearing for teams.
- `internal/userconfig/userconfig.go` — non-sensitive config; leaving it
  `0644` is a decision this plan records, not an omission.
- `internal/app/actions.go` — what gets logged is a product decision
  (see Maintenance notes); this plan only tightens who can read it.

## Git workflow

- Branch: `advisor/012-state-file-permissions`
- Imperative commit subjects, repo style; no AI attribution trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Failing tests — assert owner-only modes

Write the tests first (TDD). Umask caveat: `OpenFile`/`Chmod` perms are
capped by the process umask, so assert **the absence of group/other bits**
(`mode.Perm()&0o077 == 0`), never exact equality with `0600`.

- `internal/customactions/customactions_test.go`:
  `TestAppendLog_OwnerOnlyPermissions` — `AppendLog` into a
  `t.TempDir()` path, then `os.Stat` both the log file and its parent dir
  and assert `Perm()&0o077 == 0` on each.
- `internal/format/trust_test.go`: `TestSaveTrust_OwnerOnlyPermissions` —
  `SaveTrust` to a temp path, assert the file has no group/other bits.
- `internal/session/session_test.go`: `TestSave_OwnerOnlyPermissions` —
  with `XDG_STATE_HOME` pointed at `t.TempDir()`, `Save` a session and
  assert the session file and the `sessions` dir have no group/other bits.
- `internal/atomicfile/atomicfile_test.go`:
  `TestWrite_CreatesOwnerOnlyDirectories` — `Write` to
  `<tmp>/newdir/f.json` with perm `0600`; assert `newdir` has no
  group/other bits.

Each `Test` function gets a doc comment naming the behavior it pins (repo
convention; see any existing test in these files for the style).

**Verify**: `go test ./internal/customactions/ ./internal/atomicfile/ ./internal/format/ ./internal/session/ -run OwnerOnly` → the new tests FAIL.

### Step 2: Tighten the actions log

In `customactions.go` `AppendLog`: `MkdirAll(..., 0o700)` and
`OpenFile(..., 0o600)`. Then add a best-effort tighten for pre-existing
logs created by older skiffs — after a successful `OpenFile`:

```go
	// Best-effort: logs created by older releases were 0644; tighten
	// on the next append rather than leaving history world-readable.
	_ = f.Chmod(0o600)
```

Update the function's doc comment to say the log is owner-only and why
(command output lands in it).

**Verify**: `go test ./internal/customactions/` → all pass.

### Step 3: Tighten the atomic-writer callers and its MkdirAll

1. `internal/atomicfile/atomicfile.go:46` → `os.MkdirAll(dir, 0o700)`.
   Note in the doc comment that this only affects directories the writer
   itself creates — `MkdirAll` never chmods an existing directory, so the
   project-level `.skiff/` dir (pre-created `0755` by
   `format.InstallCommandIntoProject`) is untouched.
2. `internal/format/trust.go:181` → pass `0o600`.
3. `internal/session/session.go:173` → pass `0o600`.

Leave `userconfig.go:248` and `defaults.go:151` at `0644` (out of scope,
deliberate).

**Verify**: `go test ./internal/format/ ./internal/session/ ./internal/atomicfile/` → all pass, including Step 1's tests.

### Step 4: Confirm the project config is untouched

Add (or extend an existing) test in `internal/format/defaults_test.go`? —
NO: `defaults.go` is out of scope and already has tests. Instead run the
existing suite and one targeted check:

**Verify**: `go test ./internal/format/ -run Install` → passes unchanged,
and `grep -n "0644" internal/format/defaults.go` still shows the project
config write (proof it wasn't swept up in a blanket replace).

### Step 5: Full gates

**Verify**: `make test` → ok. `make lint` → clean.

## Test plan

Covered in Step 1 (four new tests, one per touched writer) plus the
Step 4 grep. Model structure on the existing tests in each `_test.go`
(all use `t.TempDir()`; session tests set `XDG_STATE_HOME`).

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `go test ./... -run OwnerOnly` → 4 new tests pass
- [ ] `grep -n "0o755\|0755" internal/customactions/customactions.go internal/atomicfile/atomicfile.go` → no matches
- [ ] `grep -n "0644" internal/format/trust.go internal/session/session.go` → no matches
- [ ] `grep -n "0644" internal/format/defaults.go internal/userconfig/userconfig.go` → both still present
- [ ] `git status --short` shows only in-scope files modified

## STOP conditions

Stop and report back (do not improvise) if:

- The excerpts don't match the live code (drift since `2616761`).
- You find an additional `atomicfile.Write` caller not listed above —
  report it with its path and what the file contains; do not guess its
  sensitivity.
- Any existing test asserts a `0644` mode on the trust store, session
  files, or the log — report before changing the assertion, in case it
  pins a documented contract this audit missed.
- Tightening a directory breaks a test that shares state across users or
  processes (none known; if one exists, that's a finding in itself).

## Maintenance notes

- Deliberately NOT done here: capping or redacting the output captured
  into `actions.log` (a product decision about debuggability vs exposure —
  the log's grep-friendliness is documented as intentional in
  `customactions.go:285-295`). If that's wanted later it belongs in
  `internal/app/actions.go` where `Output` is populated.
- `userconfig.go` staying `0644` is recorded here as a decision; if
  config.json ever grows anything sensitive, revisit.
- Reviewer focus: the umask-safe assertions (`&0o077`), and that
  `defaults.go`'s project-file write was not touched.
