# Plan 017: Small hardening cluster — typed drag modes, gitignored build output, SafeRef on git show, path-containment guards

> **Executor instructions**: Follow this plan step by step. Each step is an
> independent unit — commit each one separately. Run every verification
> command and confirm the expected result before moving on. If anything in
> the "STOP conditions" section occurs, stop and report — do not improvise.
> When done, update the status row for this plan in `plans/README.md` —
> unless a reviewer dispatched you and told you they maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/app/app.go internal/app/mouse.go internal/app/draw.go internal/app/gitchanges.go internal/app/modals.go internal/app/gitlog.go internal/app/session_restore.go internal/app/fileops.go internal/app/fileclip.go .gitignore`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none (but coordinate with any in-flight plan touching
  `internal/app/mouse.go` — the dragKind rename is wide)
- **Category**: tech-debt / security-hardening
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Four small, independently verified items, none urgent, all cheap, each one
a latent papercut: (a) the mouse drag state is five magic strings compared
in ~68 places — a typo compiles and silently kills a drag mode; (b) a bare
`go build` drops a 10 MB `skiff` binary at the repo root that `.gitignore`
doesn't cover, one `git add -A` away from permanent history; (c) one git
invocation takes a repo-supplied ref without the `SafeRef` gate every other
call site uses; (d) three path joins accept input that can escape their
base directory, contrary to the containment rule the codebase already
established in `relOrEmpty`.

## Current state

- `internal/app/app.go:239` — the field and its stale comment (names one
  value, code uses five):

  ```go
  	dragMode     string // "editor" while a drag-select is active.
  ```

  Values assigned/compared: `"editor"`, `"sidebar"`, `"scrollbar"`,
  `"treescrollbar"`, `"gitpanelscrollbar"`, `""`. Sites:
  `internal/app/mouse.go:147,154,161,170,178,184,195,204,209,220,231,243,248,584`,
  `internal/app/draw.go:58,72,449`, `internal/app/gitchanges.go:524`,
  `internal/app/modals.go:56`, plus **54** occurrences across
  `internal/app/*_test.go` (`grep -rn dragMode internal/app/*_test.go | wc -l` → 54).
- `.gitignore` — has `/bin/` (and coverage artifacts) but no `/skiff`;
  `git status --short` currently shows `?? skiff` (a ~10 MB binary).
- `internal/app/gitlog.go:85-97` — the one un-gated ref position:

  ```go
  func loadGitCommitDiff(rootDir, sha, path string) []string {
  	if rootDir == "" || sha == "" {
  		return nil
  	}
  	args := []string{"show", "--format=", sha}
  	if path != "" {
  		args = append(args, "--", path)
  	}
  ```

  `sha` comes from `loadGitLog`'s `--format=%h%x09%s%x09%cr` parse
  (`gitlog.go:63-76`, field 0). Not currently exploitable — `%h` is hex
  and git folds a subject's newlines, so field 0 can't carry attacker
  text — but it is the only repo-derived ref that skips
  `git.SafeRef` (`internal/git/ref.go:40-51`, which rejects empty,
  leading-`-`, and NUL) and, when `path == ""`, has no `--` at all.
  Every other site is gated: `gitops.go:206,224,532,540`,
  `gitworktree.go:243,248,256`, `snapshot.go:117`.
- `internal/app/actionvars.go:125-141` — the containment rule this repo
  already chose, to mirror (returns "" for anything whose
  `filepath.Rel` escapes base). Three sites don't follow it:
  1. `internal/app/session_restore.go:83` —
     `abs := filepath.Join(a.rootDir, filepath.FromSlash(ts.Path))`
     with no check that `abs` stays under `a.rootDir` (a session file with
     a `../..` path opens a file outside the project).
  2. `internal/app/fileops.go:210-216` — `doCreateFile` joins a
     user-typed `name` that "may contain path separators" (its own doc
     comment) with no `..` rejection: `target := filepath.Join(parent, name)`.
  3. `internal/app/fileclip.go:73-79` — the paste-into-self guard
     compares **unresolved** paths:

     ```go
     	if info.IsDir() {
     		sep := string(filepath.Separator)
     		if dir == src || strings.HasPrefix(dir+sep, src+sep) {
     			a.flash("Can't paste a folder into itself")
     ```

     so a symlinked destination that resolves inside `src` slips past and
     `copyTree` would walk into its own growing output.

Conventions: doc comment on every function (and every `Test` func); one
`_test.go` per source file, same package; refusals surface as a `flash`,
never a silent no-op; TDD.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| App tests | `go test ./internal/app/` | ok |
| Full gate | `make test` | all ok (race) |
| Lint gate | `make lint` | clean |

## Scope

**In scope**:
- `internal/app/app.go`, `mouse.go`, `draw.go`, `gitchanges.go`,
  `modals.go` (dragKind), and every `internal/app/*_test.go` that
  mentions `dragMode`
- `.gitignore`
- `internal/app/gitlog.go` + `gitlog_test.go`
- `internal/app/fileops.go` + `fileops_test.go` (withinRoot helper + create guard)
- `internal/app/session_restore.go` + `session_restore_test.go`
- `internal/app/fileclip.go` + `fileclip_test.go`

**Out of scope**:
- `internal/git/ref.go` — `SafeRef` itself is correct; don't extend it.
- `internal/app/actionvars.go` — `relOrEmpty` stays as is (it returns a
  *relative path or empty*, a different contract from the boolean guard).
- Symlinked directories expanding in the file tree — intentional behavior
  (loop-guarded browsing), not a traversal bug.

## Git workflow

- Branch: `advisor/017-small-hardening`
- One commit per step (a/b/c/d), imperative subjects, no AI trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1 (a): Typed drag modes

In `internal/app/app.go`, replace the field and add the type next to it:

```go
// dragKind names which surface owns the current mouse drag. Typed so a
// misspelled mode is a compile error instead of a silently dead drag.
type dragKind uint8

const (
	dragNone dragKind = iota
	dragEditor
	dragSidebar
	dragScrollbar
	dragTreeScrollbar
	dragGitPanelScrollbar
)
```

Field becomes `dragMode dragKind // dragEditor while a drag-select is active, etc.`
Mechanically replace every comparison/assignment listed in Current state
(`"editor"` → `dragEditor`, `""` → `dragNone`, …) in source AND the 54
test occurrences. No behavior change.

**Verify**:
`go test ./internal/app/` → ok;
`grep -rn '"editor"\|"treescrollbar"\|"gitpanelscrollbar"' internal/app/ | grep -i drag` → no matches.

### Step 2 (b): Gitignore the default build output

Add `/skiff` to `.gitignore` under the "Compiled binaries." comment, next
to `/bin/`.

**Verify**: `git check-ignore -v skiff` → prints the `.gitignore:/skiff`
rule. (`git status --short` no longer lists `?? skiff`.)

### Step 3 (c): SafeRef + `--` on the commit diff loader

In `internal/app/gitlog.go` `loadGitCommitDiff`, route `sha` through the
gate and always terminate the ref list:

```go
	safe, err := git.SafeRef(sha)
	if err != nil {
		return nil
	}
	args := []string{"show", "--format=", safe, "--"}
	if path != "" {
		args = append(args, path)
	}
```

Extend the doc comment: note this is defense-in-depth — today's callers
only pass `%h` output (hex, single-line), so no current input reaches the
refused class; the gate exists so a future caller or format change can't
turn a repo-supplied string into a `git show` option.

Add to `internal/app/gitlog_test.go` a table test
`TestLoadGitCommitDiff_RefusesOptionLookalikeSHA`: shas `"--output=x"`,
`""`, `"-p"` → all return nil without invoking git (use a `t.TempDir()`
non-repo root — refusal must happen before any subprocess). Keep one
positive case in a real `t.TempDir()` git repo (model repo setup on the
existing `gitlog_test.go` fixtures) proving a valid sha still yields a
diff with the trailing `--` present.

**Verify**: `go test ./internal/app/ -run LoadGitCommitDiff` → pass.

### Step 4 (d): `withinRoot` containment guard at three sites

1. In `internal/app/fileops.go`, add (with doc comment):

```go
// withinRoot reports whether candidate, made absolute and cleaned, still
// lives under root. Same escape rule relOrEmpty applies to shell
// variables, expressed as the boolean the file-op guards need.
func withinRoot(root, candidate string) bool
```

Implement via `filepath.Rel(root, candidate)`: false on error, on `..`,
or on any `..`+separator prefix (mirror `actionvars.go:129-136`).

2. `session_restore.go:83` — after the join, skip escaping entries:
   `if !withinRoot(a.rootDir, abs) { continue }` (same silent-skip shape
   as the existing stat/IsDir `continue` right below it — a stale or
   hand-edited session entry is not worth a flash per tab).
3. `doCreateFile` — after `target := filepath.Join(parent, name)`:
   refuse with a flash (`a.flash("Create refused: name escapes " +
   filepath.Base(parent))` — match the existing "Create failed: …" tone)
   when `!withinRoot(parent, target)`. Record in the doc comment the
   deliberate UX change: typing `../sibling/file.go` in the new-file
   prompt is now a refusal, not a file outside the tree.
4. `pasteInto` — resolve both sides before the self-paste check:
   `filepath.EvalSymlinks` on `src` and `dir` (fall back to the original
   on error, best-effort), then run the existing `dir == src ||
   strings.HasPrefix(...)` comparison on the resolved values.

Tests (TDD each):
- `fileops_test.go`: `TestWithinRoot` table (inside, equal-to-root →
  Rel is `.` — decide: equal counts as inside for session restore; state
  it in the doc comment), `../escape`, absolute elsewhere;
  `TestDoCreateFile_RefusesEscapingName` — name `"../evil.txt"` →
  no file created outside parent, flash set (assert via `a.statusMsg`
  the way existing fileops tests do).
- `session_restore_test.go`: `TestRestoreSession_SkipsEscapingPaths` —
  hand-write a session file whose tab path is `"../outside.txt"` (create
  the real file one level above root so only the guard, not the stat,
  can skip it) → no tab opened for it.
- `fileclip_test.go`: `TestPasteInto_SymlinkedDescendantRefused` —
  `dir/sub` symlinked as `elsewhere/link` … simplest honest fixture:
  copy a folder `src`, symlink `src/inner` to some path, and paste `src`
  into a symlink that resolves under `src`; assert the "into itself"
  flash and no copy started. If constructing this fixture takes more
  than ~20 lines, cover `withinRoot`-style resolution with a direct unit
  on the resolved-comparison helper instead and note it.

**Verify**: `go test ./internal/app/ -run 'WithinRoot|DoCreateFile|RestoreSession|PasteInto'` → pass.

### Step 5: Full gates

**Verify**: `make test` → ok. `make lint` → clean.

## Test plan

Listed per step above. Doc-comment every new `Test` func with the behavior
it pins (see `internal/app/fileops_test.go` for the house style, which
CLAUDE.md names as the reference).

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -rn '"treescrollbar"' internal/app/` → no matches
- [ ] `git check-ignore skiff` → exit 0
- [ ] `grep -n "SafeRef" internal/app/gitlog.go` → present
- [ ] `grep -n "withinRoot" internal/app/session_restore.go internal/app/fileops.go` → present at both
- [ ] `git status --short` shows only in-scope files modified

## STOP conditions

Stop and report back (do not improvise) if:

- Excerpts don't match live code (drift since `2616761`).
- The dragKind replacement finds a site comparing `dragMode` against a
  runtime-built string (none known — all literals); that would mean the
  type change alters behavior.
- The symlink-paste test reveals `startFileOp` has its own containment
  logic that conflicts with the resolved-path guard — report, don't
  duplicate guards.
- Session-restore's guard breaks a legitimate flow (e.g. a project root
  that is itself a symlink makes every entry "escape") — if
  `TestRestoreSession` existing cases fail, resolve `a.rootDir` via
  `EvalSymlinks` before comparing, and if that doesn't settle it, STOP.

## Maintenance notes

- The dragKind constants must grow in lockstep with any new draggable
  surface; the compiler now enforces the comparisons, but a new mode
  still needs its `handleMouse` arm — grep `dragKind` when adding one.
- `loadGitCommitDiff`'s gate is belt-and-braces; if commit subjects ever
  join the parse differently (format change), the gate is what stands
  between a crafted subject and argv.
- The `doCreateFile` refusal is a deliberate UX narrowing recorded here;
  if users ask for cross-folder creation, route them to the tree's
  target-folder selection instead of re-opening the escape.
