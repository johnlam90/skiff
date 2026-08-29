# Plan 010: Make the project-find panel preview exactly what Replace-All will rewrite

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/search/ internal/app/projfind.go internal/app/projreplace.go internal/app/projfind_test.go internal/app/projreplace_test.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED
- **Depends on**: plans/002-atomic-save.md (for step 6 only — see STOP conditions)
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

The project-find panel (Esc-F) is the user's only preview of Replace-All — a
bulk, multi-file, irreversible disk rewrite. Today the sweep records **at most
one match per line** while `ReplaceLine` rewrites **every** occurrence on the
line, so on any line containing the query twice (`foo(foo)`, `import x from "x"`)
the panel under-reports what will change, and the final report ("Replaced 40")
legitimately exceeds the number of rows the user reviewed (25). The in-buffer
find bar already reports per-occurrence (`internal/editor/find.go:40
FindAll`, with `TestFindAll_NonOverlapping` pinning it), so project search is
the outlier. Separately, the replace write uses a fixed-name temp file with no
fsync — that half is fixed by routing through the atomic writer from plan 002.

## Current state

Files and roles:

- `internal/search/search.go` — the literal smart-case project search engine.
  Pure; tests can feed it a `t.TempDir()` corpus without a repo.
- `internal/app/projfind.go` — the Esc-F panel: rows, counter, jump-to-match.
- `internal/app/projreplace.go` — the replace flows: single-row apply,
  confirm-all, buffer-vs-disk routing.

The one-per-line sweep — `internal/search/search.go:147-165` (`searchFile`):

```go
for line := range strings.SplitSeq(string(data), "\n") {
    lineNo++
    byteCol, byteLen := lineMatch(line, needle, caseSensitive, re, opts.WholeWord)
    if byteCol < 0 {
        continue
    }
    _ = byteLen
    if opts.MaxPerFile > 0 && len(out) >= opts.MaxPerFile {
        truncated = true
        break
    }
    out = append(out, Match{
        Path: rel,
        Line: lineNo,
        Col:  len([]rune(line[:byteCol])),
        Text: capRunes(line, maxLineRunes),
    })
}
```

`lineMatch` (`search.go:171-173`) is already a thin wrapper over
`lineMatchFrom(line, matchHaystack(...), 0, ...)`, and `lineMatchFrom`
(`search.go:224-250`) is the resumable scanner `ReplaceLine` uses — including
the zero-width-regex progress rule (`next = idx + 2` on `length == 0`). Reuse
it for emission so the sweep and the rewrite agree **by construction**.

The all-occurrences rewrite — `internal/search/search.go:325-377`
(`ReplaceLine`) loops `lineMatchFrom` from offset 0 and replaces every hit,
returning the count `n`.

Consumers that currently assume one match per line (every one needs the
adjustment named in the steps):

1. `internal/search/search.go:417-431` (`ApplyReplace`): per-match loop does
   `VerifyLine(lines[i], m.Text)` then `ReplaceLine(lines[i], ...)` and
   assigns `lines[i] = newLine`. With per-occurrence matches, the **second**
   match on the same line would fail `VerifyLine` against the already-rewritten
   line and be mis-counted as `Skipped`.
2. `internal/app/projreplace.go:109-129` (`applyMatchesToTab`): same shape
   against `tab.Buffer.Lines[i]`, but it stages results in `newLines[i]`
   without mutating the buffer until the end — so a second same-line match
   passes `VerifyLine` and **double-counts**: two occurrences would report
   `occ == 4`.
3. `internal/app/projreplace.go:134-164` (`projReplaceRowApply`): applies a
   single panel row by passing `[]search.Match{m}` — which, via `ReplaceLine`,
   rewrites **every** occurrence on that line. With per-occurrence rows this
   must become occurrence-targeted, or applying one row silently consumes its
   siblings and later rows flash "Skipped — the line changed since the search".
4. `internal/app/projfind.go:175-184` (row building groups by `Path` into a
   header + one row per match; `MatchIdx` indexes `projFindMatches`),
   `projfind.go:221-223` (jump uses `m.Line, m.Col` — already per-occurrence
   ready), `projfind.go:683` (counter:
   `fmt.Sprintf("%d matches in %d files", len(a.projFindMatches), files)`).
   These need **no structural change** — more matches simply mean more rows —
   but their tests pin counts that will change.
5. `internal/search/search.go:39-40, 61` — `MaxTotal: 500, MaxPerFile: 50`
   currently cap *lines*; after this change they cap *occurrences*. That is
   the intended semantics (the caps bound UI rows and report size), but the
   plan pins it with a test so the change is deliberate, not accidental.

Conventions to honor (from `CLAUDE.md`): every new function gets a doc
comment; one `_test.go` per source file, same package; each `Test*` gets a
doc comment explaining the behavior it pins (see
`internal/app/fileops_test.go` for style); use `t.TempDir()`; TDD — write the
failing test first. `CONTEXT.md` vocabulary: the panel is "project-wide
content search panel"; rows/headers are the established terms in
`projfind.go`.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Tests (fast, scoped) | `go test ./internal/search/ ./internal/app/` | ok, all pass |
| Full suite | `make test` | exit 0 (race-enabled) |
| Lint gates | `make lint` | exit 0, no output from gofmt/vet/staticcheck |

## Scope

**In scope** (the only files you should modify):
- `internal/search/search.go`, `internal/search/search_test.go`
- `internal/app/projreplace.go`, `internal/app/projreplace_test.go`
- `internal/app/projfind_test.go` (count assertions only)

**Out of scope** (do NOT touch):
- `internal/editor/find.go` — the in-buffer find engine is already
  per-occurrence and cluster-adjacent; nothing here changes it.
- `internal/app/projfind.go` — row building already handles N rows per line.
  If you believe it needs a change, that is a STOP condition, not an edit.
- Overlap/zero-width *semantics* — keep exactly `lineMatchFrom`'s existing
  progress rules; do not redesign matching.

## Git workflow

- Branch: `advisor/010-replace-preview-parity`
- Commit per step, imperative mood ("Emit one search match per occurrence"),
  no Claude trailers, no Co-Authored-By.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Pin the current mismatch with a failing test

In `internal/search/search_test.go`, add `TestSearchAndReplace_CountsAgreeOnMultiOccurrenceLine`:
write a `t.TempDir()` file containing the line `foo(foo)`, run `Search` for
`foo` (literal, default options), and assert **2** matches with distinct
`Col` values (0 and 4); then run `ApplyReplace` over those matches replacing
`foo`→`bar` and assert `Replaced == 2`, `Skipped == 0`, and the file reads
`bar(bar)`. Model the file layout on the existing search tests in the same
file.

**Verify**: `go test ./internal/search/ -run TestSearchAndReplace_CountsAgree` → FAILS
(1 match found where 2 expected). Do not proceed until you have seen this
failure — it proves the test detects the bug.

### Step 2: Emit one Match per occurrence in `searchFile`

Replace the single `lineMatch` probe with a per-line loop over
`lineMatchFrom`, building the haystack once per line via
`matchHaystack(line, caseSensitive, re)` (see `ReplaceLine:330-333` for the
exact pattern, including the fold-once rationale in `matchHaystack`'s doc
comment at `search.go:175-179`). Preserve: the `MaxPerFile` check before each
append (now capping occurrences), the `Col` computation
`len([]rune(line[:byteCol]))` per hit, the shared `Text: capRunes(line,
maxLineRunes)` per hit, and `lineMatchFrom`'s zero-width progress rule
(advance `from` past each hit by `length`, or the same `+2` rule for
`length == 0` — mirror `ReplaceLine:353-360`). Update `searchFile`'s doc
comment: it now records every qualifying occurrence.

**Verify**: `go test ./internal/search/` → the Step-1 test's *search half*
passes; expect `ApplyReplace` assertions may still fail (fixed in Step 3).
Existing tests that assumed one-per-line will fail here — fix only their
expected counts, never their inputs.

### Step 3: Make `ApplyReplace` line-idempotent

In `ApplyReplace` (`search.go:417-431`), group each file's matches by
`m.Line` first; for each distinct line, verify once against the first
recorded `Text`, call `ReplaceLine` once, and count its returned `n` into
`fileOcc`. Extra same-line matches must be neither re-verified against the
rewritten line nor re-counted. A line whose verification fails counts
`Skipped` once per recorded match on that line (the user saw that many rows
go stale).

**Verify**: `go test ./internal/search/ -run TestSearchAndReplace_CountsAgree` → PASS.

### Step 4: Same idempotence in `applyMatchesToTab`

Apply the identical group-by-line treatment in
`internal/app/projreplace.go:109-129`, reading from `tab.Buffer.Lines[i]`
and staging into `newLines[i]` exactly once per line. Add
`TestApplyMatchesToTab_MultiOccurrenceLineCountsOnce` in
`projreplace_test.go` (TDD: write it first, watch the double-count `occ == 4`
failure): a buffer line `foo(foo)` with two per-occurrence matches must yield
`occ == 2`, one `ReplaceLines` staging, buffer text `bar(bar)`.

**Verify**: `go test ./internal/app/ -run TestApplyMatchesToTab` → PASS.

### Step 5: Make single-row apply occurrence-targeted

Add to `internal/search/search.go`:

```go
// ReplaceLineAt rewrites only the occurrence that starts at rune column
// col on line, returning the new line and 1, or the line unchanged and 0
// when no qualifying occurrence starts exactly there. The panel's
// row-apply uses it so one row rewrites one occurrence — its sibling
// rows on the same line stay valid.
func ReplaceLineAt(line string, col int, query, repl string, opts Options) (string, int)
```

Implement by iterating `lineMatchFrom` (same haystack pattern) until a hit's
rune column equals `col`; splice `repl` (with the same regex `Expand`
treatment as `ReplaceLine:340-351`) over just that hit. Then in
`projReplaceRowApply` (`projreplace.go:134-164`) and the disk branch's
single-match `ApplyReplace` call, route the single-row path through it: for
the open-tab branch replace via `ReplaceLineAt` + `tab.ReplaceLines`; for the
disk branch add a small helper in `search.go` (`ApplyReplaceAt`) that
verifies the line then uses `ReplaceLineAt` and the same write path as
`ApplyReplace`. Tests: applying the first of two same-line rows leaves the
second occurrence intact and its row still verifiable; the flash count says
`Replaced 1`.

**Verify**: `go test ./internal/search/ ./internal/app/ -run 'ReplaceLineAt|ProjReplaceRow'` → PASS.

### Step 6: Route the replace write through the atomic writer

`ApplyReplace`'s write (`search.go:435-444`) currently does
`os.WriteFile(abs+".skiff-replace", ...)` + `os.Rename` — a fixed temp name
(two skiff instances collide; a crash strands droppings), no fsync, and it
replaces a symlinked target with a regular file. Plan 002 exports an atomic,
symlink-resolving, mode-preserving writer in `internal/atomicfile` (check
`plans/002-atomic-save.md`'s Done criteria for its exact name — at planning
time, `WriteFileAtomic`). Replace the hand-rolled dance with one call to it,
passing the `mode` already stat'ed at `search.go:411-414`. Delete the
`.skiff-replace` constant/logic entirely.

**Verify**: `grep -n "skiff-replace" internal/search/search.go` → no matches;
`go test ./internal/search/` → PASS, including a new
`TestApplyReplace_WriteFailureLeavesOriginal` (make the target's directory
read-only via `os.Chmod(dir, 0o555)` + `t.Cleanup` restore; assert the
original content survives and the report counts the file's matches as
skipped).

### Step 7: Pin cap semantics and update stale counts

Add `TestSearchFile_MaxPerFileCapsOccurrences`: a file with one line
containing three occurrences and `MaxPerFile: 2` yields 2 matches +
`truncated == true`. Sweep `projfind_test.go` / `projreplace_test.go` for
assertions whose expected match counts changed; update numbers only, with a
one-line comment where the count grew because of per-occurrence emission.

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

New tests (all TDD — watch each fail first):
- `search_test.go`: counts-agree on `foo(foo)` (Step 1); `MaxPerFile`
  occurrence semantics (Step 7); `ReplaceLineAt` targets one occurrence,
  returns 0 on a column with no hit, regex `$1` expansion works (Step 5);
  write-failure leaves original (Step 6).
- `projreplace_test.go`: `applyMatchesToTab` multi-occurrence idempotence
  (Step 4); single-row apply preserves sibling rows (Step 5).
- Pattern exemplars: existing tests in `internal/search/search_test.go` and
  `internal/app/projreplace_test.go` — same package, doc comment per test,
  `t.TempDir()`.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `go test ./internal/search/ -run TestSearchAndReplace_CountsAgree -v` passes
- [ ] `grep -n "skiff-replace" internal/ -r` → no matches
- [ ] Panel row count equals `ReplaceReport.Replaced` on the multi-occurrence corpus (asserted by Step 1's test)
- [ ] `git status` shows modifications only to in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `plans/002-atomic-save.md` is not marked DONE in `plans/README.md` when you
  reach Step 6 — complete Steps 1-5 + 7, mark this plan BLOCKED on 002 for
  the write step, and report.
- The excerpts above don't match the live code (drift).
- Making `searchFile` per-occurrence requires touching
  `internal/app/projfind.go`'s row-building logic — the analysis says it
  doesn't; if it does, the analysis is wrong somewhere else too.
- Any existing test failure cannot be explained as "expected count grew
  because per-line became per-occurrence".

## Maintenance notes

- `MaxTotal`/`MaxPerFile` now bound occurrences; if a future change wants
  "lines shown" caps for the panel, cap at row-build time, not in the engine.
- `ReplaceLineAt` and `ReplaceLine` share `lineMatchFrom`; any change to
  match progression rules must keep sweep emission, whole-line replace, and
  targeted replace iterating identically — that shared walk IS the
  preview-parity guarantee.
- Reviewer focus: the group-by-line skip accounting in Steps 3-4 (per-match
  vs per-line counts), and that zero-width regex emission can't loop.
