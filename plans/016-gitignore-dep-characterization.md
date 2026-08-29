# Plan 016: Characterize go-gitignore against real git before deciding its future

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index. This is an INVESTIGATE plan: its deliverable is a
> characterization test plus a findings report, not a dependency swap.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- go.mod internal/filetree/ internal/finder/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (the risk is in a *future* swap; this plan itself is LOW)
- **Depends on**: none
- **Category**: dependencies
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`github.com/sabhiram/go-gitignore` is pinned at a 2021 pseudo-version
(`go.mod:9` — `v0.0.0-20210923224102-525f6e181f06`; upstream has never tagged
a release and is inactive). It decides two user-visible things: which entries
the sidebar tree hides (`HideIgnored`) and which files the project index
walks for non-git projects. If its semantics diverge from git's — negation
ordering, `**` placement, directory-only patterns — the tree hides the wrong
files or the finder surfaces noise, and there is no upstream fix path except
a fork or a swap. Nobody currently knows whether it diverges. This plan
builds the ground-truth corpus that answers the question and records the
verdict, so a swap (if needed) happens against evidence rather than fear.

## Current state

The dependency has exactly two import sites:

1. `internal/filetree/filetree.go:41` — `gitignore "github.com/sabhiram/go-gitignore"`.
   Compilation happens in `cacheIgnore` (`filetree.go:586`):

   ```go
   gi: gitignore.CompileIgnoreLines(strings.Split(string(raw), "\n")...),
   ```

   The per-directory matcher cache is a documented design (`CLAUDE.md`
   architecture map: ".gitignore-aware filtering (HideIgnored + the
   per-directory matcher cache)"). Matching walks nested levels —
   `ignoreChain` (`filetree.go:617-638`) collects one compiled matcher per
   ancestor `.gitignore`, and `ignoredByChain` (`filetree.go:676-693`) probes
   each level with a level-relative path, appending `/` for directories:

   ```go
   rel := e.Name
   if e.IsDir {
       rel += "/"
   }
   for _, lv := range chain {
       if lv.gi.MatchesPath(lv.prefix + rel) {
           return true
       }
   }
   ```

   Two deliberate carve-outs live ABOVE the library (`ignoredByChain`'s doc
   comment): dotfiles are never filtered, and entries pinned by open tabs are
   exempt. These are skiff decisions, not library behavior — the
   characterization must test the library directly, not through them.

2. `internal/finder/index.go:39` — same import. `buildIndexWalk`
   (`index.go:150+`) compiles a single project-root ignorer via
   `loadProjectGitignore`; its doc comment (`index.go:144-149`) explicitly
   trades away nested-`.gitignore` subtree fidelity for simplicity. That
   tradeoff is documented and settled — do NOT report it as a divergence.
   Note the finder prefers `git ls-files` when git exists, so the walk (and
   this library) only governs non-git projects there.

Conventions (from `CLAUDE.md`): tests in the same package, one `_test.go`
per source file — this characterization goes in `internal/filetree/` as its
own concern; a new source-less test file is acceptable here because it pins
an external contract, name it `gitignore_characterization_test.go` and say so
in its file comment. Use `t.TempDir()`; real git in tests is established
practice (see `internal/app/gitops_test.go`). File header block (author, 2026
copyright) per existing files. Doc comment on every function and test.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Scoped run | `go test ./internal/filetree/ -run TestGitignoreCharacterization -v` | see per-step notes |
| Full suite | `make test` | exit 0 |
| Lint | `make lint` | exit 0 |
| Ground truth probe | `git -C <tmpdir> check-ignore -q -- <path>` | exit 0 = ignored, 1 = not ignored |

## Scope

**In scope**:
- `internal/filetree/gitignore_characterization_test.go` (create)
- The "Characterization results" section at the bottom of THIS plan file
  (fill it in)

**Out of scope** (do NOT touch):
- `go.mod` / `go.sum` — no dependency change in this plan.
- `internal/filetree/filetree.go`, `internal/finder/index.go` — no behavior
  change; if a divergence is found, the fix is a FUTURE plan.
- `ignoredByChain`'s dotfile/pin carve-outs — skiff policy, not under test.

## Git workflow

- Branch: `advisor/016-gitignore-characterization`
- One or two commits, imperative mood, no Claude trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Build the harness

In `gitignore_characterization_test.go`, write a helper:

```go
// gitGroundTruth reports whether real git ignores rel inside a repo
// whose root .gitignore holds patterns. It builds a throwaway repo in
// t.TempDir(), writes the pattern file, creates rel's parent dirs, and
// asks `git check-ignore`.
func gitGroundTruth(t *testing.T, patterns []string, rel string, isDir bool) bool
```

Use `exec.Command("git", "-C", dir, ...)` with `init` then `check-ignore -q --
<rel>`; treat exit code 0 as ignored, 1 as not, anything else as a fatal
harness error. Skip the whole test with `t.Skip` if `exec.LookPath("git")`
fails — the CLAUDE.md rule allows skips only for hard environment
requirements, which this is. For directory cases pass `rel` with a trailing
slash to check-ignore AND create the directory so git evaluates it as one.

The library side probes the same inputs the tree uses:
`gitignore.CompileIgnoreLines(patterns...).MatchesPath(rel)` with the same
trailing-slash convention `ignoredByChain` applies.

**Verify**: `go test ./internal/filetree/ -run TestGitignoreCharacterization -v`
with a single smoke case (`*.log` vs `build.log`) → PASS.

### Step 2: The corpus — ~30 pattern classes

One table test, each row: name, pattern lines, probe path, isDir. Cover at
minimum:

- Basic: `*.log`; exact name; `foo` matching file AND dir.
- Negation & ordering: `[ "*.log", "!keep.log" ]`; the reverse order
  (`!keep.log` before `*.log` — the negation must NOT survive);
  re-ignoring after negation (`*.log`, `!keep.log`, `keep.log`).
- Directory-only: `dist/` vs a FILE named `dist` (must not match) and a dir
  `dist` (must match); `dist` (no slash) against both.
- Doublestar: `**/foo`, `foo/**`, `a/**/b`, leading `/**/x`; `**` matching
  zero path segments (`a/**/b` vs `a/b`).
- Anchoring: `/top.log` vs `top.log` at root and in a subdir; `sub/file`
  (pattern with a slash is root-relative — probe `x/sub/file`, which git
  does NOT ignore).
- Escapes & specials: `\#literal`, `\!bang`, trailing-space pattern vs
  escaped trailing space (`a\ ` — git keeps the space).
- Globs: `?at`, `[abc].txt`, `[a-c].txt`, `[!a]x`, `*.tx?`.
- Case sensitivity: `Foo.txt` vs probe `foo.txt` (git is case-sensitive
  here by default on Linux).
- Nested-level relativity: pattern list applied at a subdir level with the
  `prefix` convention `ignoreChain` uses (`sub/` + name) vs git with the
  `.gitignore` written INTO `sub/` — this validates the tree's
  prefix-joining against git's nested-file semantics, the one place skiff
  composes the library beyond a single probe.

For each row assert `libraryResult == gitResult` — but do NOT make
divergences fail the build permanently: collect mismatches into a slice and
report them all via ONE `t.Errorf` per divergent row, so a red run enumerates
the full divergence set in a single pass.

**Verify**: `go test ./internal/filetree/ -run TestGitignoreCharacterization -v`
→ either PASS (no divergence) or a complete list of divergent rows. Both are
successful investigation outcomes.

### Step 3: Record the verdict

Fill in the "Characterization results" section at the bottom of this file:

- If **zero divergences**: state the negative result, the git version used
  (`git --version`), and the date. Recommendation to record: keep the
  dependency, add a `// verified against git X.Y` note to the corpus test,
  and revisit only if a user reports a mismatch. The test stays in the suite
  as a permanent fence (it runs real git; it is cheap).
- If **divergences exist**: list each (pattern, path, library says, git
  says), assess user impact (tree hides wrongly vs shows noise), and record
  the follow-up recommendation: evaluate `go-git`'s
  `plumbing/format/gitignore` behind the two existing call sites as its own
  plan — do NOT perform the swap here. If divergences make the suite red,
  convert exactly those rows to a documented known-divergence list the test
  tolerates (`t.Logf` not `t.Errorf`), so the fence stays green while the
  report stays honest.

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

The corpus IS the test (Step 2). Structural pattern: table tests as in
`internal/filetree/filetree_test.go` (e.g. `TestShouldHide`); real-git usage
as in `internal/app/gitops_test.go`. Every helper and the test get doc
comments explaining what they pin.

## Done criteria

- [ ] `go test ./internal/filetree/ -run TestGitignoreCharacterization -v` runs ≥30 rows (count them in `-v` output)
- [ ] `make test` and `make lint` exit 0
- [ ] The "Characterization results" section below is filled in with a verdict and recommendation
- [ ] `go.mod` untouched (`git diff go.mod` empty)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `git` is unavailable in the execution environment (the ground-truth half
  cannot run; a library-only corpus proves nothing).
- `check-ignore` behaves differently than described (exit codes other than
  0/1 on clean input) — the harness assumption is wrong.
- You find yourself editing `filetree.go` or `index.go` — that is the
  follow-up plan, not this one.
- More than ~5 divergences appear: pause and report the partial list before
  finishing the corpus — that magnitude changes the follow-up's priority.

## Maintenance notes

- If a swap plan is written later, this corpus becomes its acceptance
  gate: the replacement must pass every row the old library passed, plus the
  divergent ones.
- The corpus deliberately tests the LIBRARY + the tree's prefix convention,
  not `ignoredByChain`'s carve-outs; if someone later changes the
  trailing-slash or prefix-joining in `ignoredByChain`, this test is the one
  that must be re-run against git.
- The finder's single-root-ignorer fidelity tradeoff (`index.go:144-149`) is
  documented and out of scope; don't let a future reader mistake this plan's
  verdict for a claim about nested-ignore fidelity in the finder.

## Characterization results (filled by the executor)

_Not yet run. The executor replaces this line with:_

- **Date / git version**: …
- **Rows run / divergences**: …
- **Divergence table** (omit if none): pattern | probe | library | git | user impact
- **Verdict & recommended follow-up**: …

---

## Characterization results (executed 2026-08-29, commit 36732c1)

Corpus: 41 pattern rows verified against real `git check-ignore`
(hermetic git env). **35 agree; 6 diverge.** The divergent rows are
pinned as `knownDivergence` entries in
`internal/filetree/gitignore_characterization_test.go` — the suite is
green today and fails loudly if either side's behavior changes.

| # | pattern | probe | library | git | root cause | impact |
|---|---------|-------|---------|-----|-----------|--------|
| 1 | `?at` | `cat` | no match | match | `?` escaped to a literal instead of single-char wildcard | Low |
| 2 | `*.tx?` | `file.txt` | no match | match | same `?`-literal bug | Low |
| 3 | `[!a]x` | `bx` | no match | match | `[!...]` passed raw into RE2, where `!` isn't negation | Low |
| 4 | `[!a]x` | `ax` | match | no match | same, inverted | Low |
| 5 | `sub/file` | `x/sub/file` | match | no match | embedded-slash patterns not anchored to their .gitignore dir | **Medium** — common real-world shape; skiff over-hides same-named nested paths |
| 6 | `trail.txt\ ` | `trail.txt ` | no match | match | escaped-trailing-space rule is an unimplemented TODO upstream | Very low |

**Recommendation (follow-up, operator decision):** divergence #5 alone
justifies swapping to a maintained implementation (go-git's
`plumbing/format/gitignore` is the candidate) behind the existing
two-call-site seam — weighed against the dependency footprint it adds.
Until then this corpus is the fence: any upstream or replacement change
shows up as a red row naming the exact pattern.
