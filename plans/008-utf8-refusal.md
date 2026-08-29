# Plan 008: Refuse files that are not valid UTF-8 instead of silently corrupting them on edit

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/editor/tab.go internal/editor/buffer.go`
> Plan 006 is EXPECTED to have landed first (it creates the `readTextFile`
> gate this plan extends). If `readTextFile` does not exist in
> `internal/editor/tab.go`, see Step 1's fallback. Any other mismatch with
> the "Current state" excerpts is a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plans/006-open-reload-guards.md (preferred; has a fallback if 006 was skipped)
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

The buffer stores lines as Go strings and edits them through rune slices.
Go's `[]rune(s)` conversion maps every invalid UTF-8 byte to U+FFFD, and
re-encoding produces the three bytes `EF BF BD`. Consequence: open a Latin-1
`.properties` file or a `.po` with one mis-encoded byte (no NUL, so the
binary check passes), type a single character on that line, save — every
invalid byte **on that line** is silently rewritten to U+FFFD. Unedited lines
round-trip byte-exact, which makes the corruption invisible until someone
reads the file back in its original encoding. There is no warning and no
undo after save+close. The repo already refuses binary files on open as a
recorded tradeoff; refusing invalid UTF-8 the same way is the cheapest
correct fix and matches that precedent.

## Current state

- The corruption mechanism (all in `internal/editor/buffer.go`):
  - `LineRunes` (~:120-135) decodes a line with `runes := []rune(src)` —
    invalid bytes become U+FFFD, one per byte.
  - `InsertString` (~:160-168) rebuilds the line from those runes:

    ```go
    line := b.LineRunes(p.Line)
    before := string(line[:p.Col])
    after := string(line[p.Col:])
    ```

  - `DeleteRange` (~:200-211) does the same with `head`/`tail`.

  These are correct for valid UTF-8 and must NOT change — the fix is at the
  door, not in the splice.

- The door, post-plan-006: `readTextFile(path)` in `internal/editor/tab.go`
  — the single gate `NewTab` and `Reload` read through, which already
  refuses `> maxOpenBytes` (wrapping `ErrFileTooLarge`, tab.go:231) and
  `looksBinary` content (wrapping `ErrBinaryFile`, tab.go:215). Error
  sentinels live at tab.go:212-231; follow their comment style.

- App-side: callers of `NewTab`/`Reload` flash wrapped errors verbatim
  (e.g. `internal/app/refresh.go:393-395` flashes "… reload failed: …"), so
  a well-worded sentinel needs no app changes.

- Conventions: doc comment on every function; same-package tests; TDD;
  `t.TempDir()` fixtures.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite | `make test` | exit 0 |
| Lint gates | `make lint` | exit 0 |
| Targeted | `go test ./internal/editor/ -run 'UTF8\|NewTab\|Reload'` | all pass |

## Scope

**In scope**:
- `internal/editor/tab.go` (the sentinel + one check inside `readTextFile`)
- `internal/editor/tab_test.go`

**Out of scope**:
- `internal/editor/buffer.go` — no changes; the splice code is correct for
  the input domain this plan enforces.
- Encoding conversion/transcoding features (no charset detection, no
  iconv) — recorded as rejected below.
- `internal/search/` — project search opens files read-only and its
  sub-cluster matching is a documented tradeoff.

## Git workflow

- Branch: `advisor/008-utf8-refusal`
- Imperative subject + body; NO Claude trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add the sentinel and the gate check (tests first)

Tests in `tab_test.go`, watch each fail first:

- `TestNewTab_RefusesInvalidUTF8` — fixture: ASCII text with one raw 0xE9
  byte (Latin-1 "é"), e.g. `[]byte("caf\xe9 au lait\n")`. `NewTab` must
  return an error wrapping the new sentinel; `errors.Is` asserted.
- `TestNewTab_AcceptsMultibyteUTF8` — a file containing CJK, an emoji ZWJ
  sequence, and a combining mark opens fine (guards the guard: valid
  multibyte must not be caught).
- `TestReload_RefusesNowInvalidUTF8` — open a valid file, externally
  rewrite it with the Latin-1 bytes, reload is refused and the buffer still
  holds the original content (same shape as plan 006's
  `TestReload_RefusesGrownFile`).

Implement in `tab.go`:

```go
// ErrNotUTF8 marks a refusal to open content that is not valid UTF-8.
// The buffer edits text through rune slices, and Go maps invalid bytes
// to U+FFFD on decode — so editing any line holding such bytes would
// silently rewrite them on save. Refusing at the door is the same
// tradeoff as ErrBinaryFile: skiff edits UTF-8 text, and says so
// instead of corrupting quietly.
var ErrNotUTF8 = errors.New("not valid UTF-8 — convert the file to UTF-8 to edit it")
```

In `readTextFile`, after the `looksBinary` refusal:

```go
if !utf8.Valid(data) {
    return nil, time.Time{}, fmt.Errorf("%s: %w", filepath.Base(path), ErrNotUTF8)
}
```

(`unicode/utf8` import.) Order matters: binary first — a zip should still
say "binary", not "not UTF-8".

**Fallback if plan 006 was skipped and `readTextFile` doesn't exist**: put
the same check in `NewTab` directly after the `looksBinary` block
(tab.go:305-312) AND in `Reload`'s text branch immediately after its
`os.ReadFile` (tab.go:441-443), before any field mutation. Note in your
report that 006 should still land and absorb both call sites.

**Verify**: `go test ./internal/editor/ -run UTF8` → 3 new tests pass.

### Step 2: Full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

The three tests in Step 1. Fixture bytes are inlined in the test (no
testdata files needed). Model assertion style on the existing
`ErrBinaryFile` tests in `tab_test.go` (grep `ErrBinaryFile` there and
mirror the `errors.Is` pattern).

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `go doc ./internal/editor ErrNotUTF8` prints the sentinel
- [ ] `go test ./internal/editor/ -run UTF8 -v` shows the 3 new tests passing
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back if:

- An existing test opens an invalid-UTF-8 fixture on purpose (i.e. some
  feature *depends* on U+FFFD tolerance) — report which test before
  changing anything.
- `looksBinary` already rejects your Latin-1 fixture (it should not — the
  fixture has no NUL byte) — the guard landscape has drifted; report.

## Maintenance notes

- **Rejected alternative, recorded so nobody re-litigates blind**: lossless
  round-tripping (having `LineRunes` carry a byte-offset table and splicing
  on bytes) would let skiff edit mixed-encoding files without corruption.
  It touches every rune-position consumer (cursor math, wrap, search
  highlighting) for a file class the editor's habitat rarely needs, and the
  CLAUDE.md "three units" section makes rune indexing load-bearing. If
  users hit the refusal often, revisit with that section in hand.
- If a "convert to UTF-8?" affordance is ever wanted, it belongs in the
  app-side open error handler (offer + `golang.org/x/text` transcode), not
  in the editor package.
- Reviewer: confirm the check ordering (size → binary → utf8) and that the
  error message names the file, matching the other two refusals.
