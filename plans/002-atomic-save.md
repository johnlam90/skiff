# Plan 002: Make Tab.Save atomic — never truncate the user's file before the new bytes are safe

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/editor/tab.go internal/atomicfile/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`Tab.Save` writes the user's source file with `os.WriteFile`, which opens with
`O_TRUNC`: the original content is destroyed *before* the new bytes are
written, with no fsync. If the process dies, the disk fills (ENOSPC), or a
network mount hiccups between the truncate and the write, the file on disk is
a prefix of the new content and the original is gone. Skiff's own
`internal/atomicfile` package exists to prevent exactly this — but it protects
only config/state files (session store, trust store, config.json). The one
file class where a torn write is unrecoverable — the user's code — is the one
class not protected. Skiff's stated habitat is SSH into remote boxes, where a
full `$HOME` partition is routine.

## Current state

- `internal/editor/tab.go` — `Tab.Save` at lines 390–414; the raw write is
  line 397:

  ```go
  // tab.go:397
  if err := os.WriteFile(t.Path, []byte(t.Buffer.TextWith(t.LineEnding.Newline())), 0644); err != nil {
      return err
  }
  ```

  Everything after the write (clear `Dirty`/`DiskGone`, `t.markSaved()`,
  refresh `t.Mtime`, `t.breakUndoGroup()`) must be preserved unchanged.

- `internal/atomicfile/atomicfile.go` — the whole package is `Write(path,
  data, perm)` (lines 44–71) + `writeAndSync` (77–85): temp file in the
  target's directory, write, chmod, `f.Sync()`, close, `os.Rename`. Two gaps
  for the source-file use case:
  1. **No directory fsync after the rename** — on a crash the rename itself
     can be lost even though the doc comment claims crash safety. (This gap
     affects the existing config/state callers too; fix it here for both.)
  2. **Rename replaces the inode**, which breaks saving *through a symlink*
     (the symlink would be replaced by a regular file) and discards the
     existing file's mode (a `0755` script would come back `0644`).

- Repo conventions that apply: every new function gets a doc comment
  (project-wide rule, see any function in `atomicfile.go` for tone); every
  source file has a same-package `_test.go`; new files start with the header
  block (see `internal/atomicfile/atomicfile.go:1-6` — use "John Lam
  <johnlam90@gmail.com>", created date 2026-08-29, copyright 2026). Tests use
  `t.TempDir()`, never `/tmp` directly. TDD: write each test first, watch it
  fail, then implement.

- `internal/atomicfile/atomicfile_test.go` — existing tests to model after:
  `TestWriteReplacesExistingFile` (line 38), `TestWriteCleansUpTempOnFailure`
  (line 97).

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite | `make test` | exit 0, all packages `ok` |
| Lint gates | `make lint` | exit 0, no output from gofmt/vet/staticcheck |
| Targeted | `go test ./internal/atomicfile/ ./internal/editor/ -run 'Replace\|Save'` | all pass |

## Scope

**In scope** (the only files you should modify):
- `internal/atomicfile/atomicfile.go`
- `internal/atomicfile/atomicfile_test.go`
- `internal/editor/tab.go` (only the write inside `Save`)
- `internal/editor/tab_test.go` (new tests)

**Out of scope** (do NOT touch, even though they look related):
- `internal/search/search.go` — its Replace-All write (`abs + ".skiff-replace"`
  at ~line 435) is plan 010's job; plan 010 will consume the writer you export
  here. Do not modify it.
- `internal/session/`, `internal/format/`, `internal/userconfig/` — existing
  `atomicfile.Write` callers keep their current call shape; they benefit from
  the directory-fsync fix automatically.
- `Tab.Save`'s post-write bookkeeping (`markSaved`, `breakUndoGroup`, mtime
  refresh) — behavior must not change.

## Git workflow

- Branch: `advisor/002-atomic-save`
- One commit per step or logical unit. Imperative subject + body explaining
  why (match `git log`, e.g. "Fold single-child dir chains into compact tree
  rows"). **No** "Generated with Claude Code" trailers and no Co-Authored-By
  Claude — CLAUDE.md forbids them.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add a directory fsync to the existing writer (test first)

In `atomicfile_test.go`, add `TestWriteSyncsDirectory` — this cannot assert
the fsync happened from userspace, so pin the seam instead: extract the
post-rename sync into an unexported `syncDir(dir string) error` (open the
directory read-only, `Sync()`, close; on platforms/filesystems where opening
a directory fails, return nil — degrading quietly is correct, refusing to
save is not) and unit-test `syncDir` directly against `t.TempDir()` (returns
nil) and a non-existent path (returns nil too — best-effort). Then call
`syncDir(dir)` at the end of `Write` after the successful rename, ignoring
its error (comment why: the data rename already succeeded; a failed dir sync
must not report the save as failed).

**Verify**: `go test ./internal/atomicfile/` → all pass.

### Step 2: Add `Replace` — the user-file variant (tests first)

New exported function in `atomicfile.go`:

```go
// Replace atomically replaces the file at path with data, following the
// rules a *user's* file needs that Write's config-file contract doesn't:
// the path is resolved through symlinks first (saving through a link must
// update the target, not replace the link with a regular file), and the
// existing file's permission bits are preserved (a 0755 script stays
// executable). A file that doesn't exist yet is created 0644.
func Replace(path string, data []byte) error
```

Implementation shape:
1. `resolved, err := filepath.EvalSymlinks(path)`; on `os.IsNotExist(err)`
   resolve the *parent* directory instead and rejoin the base name (a first
   save of a new file has nothing to resolve); any other error returns.
2. `perm := fs.FileMode(0644)`; if `os.Stat(resolved)` succeeds, use
   `info.Mode().Perm()`.
3. Reuse the existing temp-write machinery (`os.CreateTemp` in
   `filepath.Dir(resolved)`, `writeAndSync`, close, `os.Rename(tmp,
   resolved)`, `syncDir`). Factor the shared body out of `Write` rather than
   duplicating it — `Write` keeps its MkdirAll+fixed-perm contract, `Replace`
   keeps resolve+preserve-perm; both share one temp/rename/sync core.

Tests in `atomicfile_test.go` (write each first, watch it fail):
- `TestReplacePreservesMode` — create a file `0755`, `Replace`, assert
  `Stat().Mode().Perm() == 0755`.
- `TestReplaceWritesThroughSymlink` — create `real.txt`, symlink `link.txt` →
  `real.txt`, `Replace("link.txt", ...)`, assert `link.txt` is **still a
  symlink** (`os.Lstat`) and `real.txt` holds the new bytes.
- `TestReplaceCreatesMissingFile` — `Replace` on a fresh path in `t.TempDir()`
  creates it `0644`.
- `TestReplaceFailureKeepsOriginal` — make the parent directory read-only
  (`os.Chmod(dir, 0555)`, restore in `t.Cleanup`; `t.Skip` when
  `os.Geteuid() == 0` since root ignores the mode), `Replace` fails, original
  content intact, no `.tmp-*` droppings left.

**Verify**: `go test ./internal/atomicfile/ -run Replace` → all pass;
`go test ./internal/atomicfile/ -cover` → coverage above the current 60.9%.

### Step 3: Point `Tab.Save` at `Replace` (test first)

In `internal/editor/tab_test.go` add `TestSavePreservesExecBitAndSymlink`
(model after existing tab tests): a `0755` file saved via a Tab keeps its
bit; a tab opened on a symlink path saves through it. Then change line 397:

```go
if err := atomicfile.Replace(t.Path, []byte(t.Buffer.TextWith(t.LineEnding.Newline()))); err != nil {
    return err
}
```

Add the `github.com/johnlam90/skiff/internal/atomicfile` import. Also add
`TestSaveCRLFRoundTrip` if no equivalent exists (grep `tab_test.go` for CRLF
first): open a CRLF file, edit, save, assert bytes still CRLF-joined.

**Verify**: `go test ./internal/editor/ -run Save` → all pass.

### Step 4: Full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0.

## Test plan

Covered per step above. New tests: `TestWriteSyncsDirectory`(seam),
`TestReplacePreservesMode`, `TestReplaceWritesThroughSymlink`,
`TestReplaceCreatesMissingFile`, `TestReplaceFailureKeepsOriginal`,
`TestSavePreservesExecBitAndSymlink`, `TestSaveCRLFRoundTrip`.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -n "os.WriteFile" internal/editor/tab.go` returns no matches
- [ ] `go doc ./internal/atomicfile Replace` prints the new function
- [ ] `go test ./internal/atomicfile/ -cover` reports > 60.9%
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `Tab.Save` no longer matches the excerpt (drifted).
- `filepath.EvalSymlinks` semantics force a behavior change for the
  common no-symlink case (they must not — resolved == path there).
- The read-only-dir failure test cannot be made reliable on this platform.
- You find another raw `os.WriteFile`/`os.Create` writing *user* content in
  `internal/editor/` — report it; do not widen scope.

## Maintenance notes

- Known, accepted limitations of rename-based replace (document in the
  `Replace` doc comment): hard links to the file are broken; xattrs/ACLs and
  ownership are not copied (chown needs privileges). If users hit these,
  the alternative is write-in-place+fsync with a backup file — a different
  plan.
- Plan 010 (Replace-All parity) must switch `internal/search/search.go`'s
  `.skiff-replace` write to this `Replace`.
- Reviewer: scrutinize the EvalSymlinks-on-missing-file path and that
  `Save`'s post-write bookkeeping is untouched.
