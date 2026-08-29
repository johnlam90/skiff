# Plan 006: Gate image decoding and Tab.Reload behind the same size/content guards NewTab has

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- internal/editor/tab.go internal/editor/image.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition. NOTE: plan 005 intentionally lands
> before this one and refactors `Reload` into `reloadFromDisk` — that exact
> drift is expected; the guard from Step 3 then goes inside `reloadFromDisk`.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plans/005-undo-preserving-reload.md (both edit `Tab.Reload`; 005 first avoids conflicting refactors)
- **Category**: bug / security-adjacent (resource exhaustion)
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

`NewTab` carefully refuses files over 32 MiB and binary content — comments in
the code record that a large zip once froze the editor outright. But two doors
skip those locks. (1) Image extensions branch to `newImageTab` *before* the
size guard, and `image.Decode` runs with no dimension cap: a small file whose
header declares enormous dimensions makes Go allocate the full raster
(4×w×h bytes) on the event loop — one click on a tree row can OOM-kill the
editor and take every unsaved buffer with it, and the 10-second reconcile
re-decodes on every external change. (2) `Tab.Reload` re-reads with a bare
`os.ReadFile`: a file that was 20 KB of Go when opened but became a 2 GB
artifact or a binary after `git checkout`/`make` is pulled whole into the
text buffer by the silent reload path. After this plan, every road into a
buffer — open, image decode, reload — passes the same guards.

## Current state

- `internal/editor/tab.go:281-284` — the image branch runs before the guards:

  ```go
  func NewTab(path string) (*Tab, error) {
      if path != "" && isImageExt(path) {
          return newImageTab(path)
      }
  ```

  The stat + `maxOpenBytes` guard is at :293-298 and the `looksBinary`
  refusal at :305-312, both *after* this branch.

- Sentinels and caps (tab.go): `ErrBinaryFile` (:215), `maxOpenBytes =
  32 << 20` (:225), `ErrFileTooLarge` (:231), `looksBinary` (:244),
  `mibString` (:236). App-side callers already flash these errors nicely, so
  a new sentinel that follows the same shape needs no app changes.

- `internal/editor/image.go:69-80` — `decodeImageFile` opens and calls
  `image.Decode(f)` directly; no stat, no `image.DecodeConfig` pre-check.
  `newImageTab` (tab.go:332-354) calls it, and `Reload`'s image branch
  (tab.go:426-440) calls it again on every external-change reconcile.

- `internal/editor/tab.go:441-449` — Reload's text branch (post-plan-005:
  inside `reloadFromDisk`):

  ```go
  data, err := os.ReadFile(t.Path)
  if err != nil { return err }
  info, err := os.Stat(t.Path)
  if err != nil { return err }
  t.Buffer = NewBuffer(string(data))
  ```

  No size cap, no binary check, and the buffer is replaced before any
  refusal could happen.

- Conventions: doc comment on every function; header block on any new file
  (there are no new files in this plan); same-package `_test.go`; TDD; test
  fixtures via `t.TempDir()`. `internal/editor/tab_test.go` and
  `image_test.go` exist — model new tests on their style.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full suite | `make test` | exit 0 |
| Lint gates | `make lint` | exit 0 |
| Targeted | `go test ./internal/editor/ -run 'NewTab\|Reload\|Image'` | all pass |

## Scope

**In scope**:
- `internal/editor/tab.go`
- `internal/editor/image.go`
- `internal/editor/tab_test.go`, `internal/editor/image_test.go`

**Out of scope**:
- `Reload`'s history handling (`initUndo` / `ReloadKeepHistory`) — plan 005
  owns it. Touch only the disk-read half.
- `internal/app/refresh.go` / `preview.go` — their error handling already
  flashes; no changes needed.
- Raising or lowering `maxOpenBytes` itself.
- UTF-8 validation — plan 008 adds it to the gate you build here.

## Git workflow

- Branch: `advisor/006-open-reload-guards`
- Imperative subject + body; NO Claude trailers.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Cap image decoding (tests first)

In `image_test.go`, add `TestDecodeImageFile_RefusesHugeDimensions` using a
crafted GIF header — GIF is the easy fixture because its logical screen
descriptor is plain little-endian with no checksum:

```go
// 6-byte signature, then width/height uint16 LE: 65535 × 65535
hdr := append([]byte("GIF89a"), 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00)
```

Write it to `t.TempDir()/huge.gif`; `decodeImageFile` must refuse with the
new sentinel, not attempt a full decode. Also add
`TestDecodeImageFile_RefusesOversizeFile` (a file whose *byte size* exceeds
the cap — create with `f.Truncate(maxOpenBytes+1)` on a sparse file so the
test is fast). Watch both fail, then implement in `decodeImageFile`:

1. Stat first; refuse `size > maxOpenBytes` wrapping `ErrFileTooLarge`
   (same message shape as tab.go:295-297, reuse `mibString`).
2. `cfg, _, err := image.DecodeConfig(f)`; refuse when
   `cfg.Width*cfg.Height > maxImagePixels` with a new sentinel in image.go:

   ```go
   // maxImagePixels caps the decoded raster. The preview renders into a
   // terminal grid of at most a few hundred cells per side, so nothing
   // legitimate needs more than a large camera photo (~48 MP ≈ 192 MB of
   // RGBA) — while a crafted header can request an arbitrary allocation
   // from a tiny file. Multiplication is safe: both dims are decoded from
   // fixed-width fields far below overflow range.
   const maxImagePixels = 48_000_000

   // ErrImageTooLarge marks a refusal to decode an image whose declared
   // dimensions exceed maxImagePixels — a guard against decode bombs, not
   // a judgment about the file. Callers flash it like ErrFileTooLarge.
   var ErrImageTooLarge = errors.New("image dimensions too large to preview")
   ```

3. `f.Seek(0, io.SeekStart)` before the real `image.Decode` (DecodeConfig
   consumed the header).

Both `newImageTab` and Reload's image branch get the guards for free since
both go through `decodeImageFile`.

**Verify**: `go test ./internal/editor/ -run Image` → all pass.

### Step 2: Move the size guard ahead of the image branch (test first)

`TestNewTab_ImageOverSizeCapRefused` in `tab_test.go`: a sparse
`maxOpenBytes+1` file named `x.png` → `NewTab` returns `ErrFileTooLarge`
without decoding. This *already* passes after Step 1 (the stat lives in
`decodeImageFile`) — that is acceptable; the test pins the contract either
way. Keep `NewTab`'s structure as-is (the image branch at :282 stays; the
guard lives inside the image path now). Do NOT hoist the text-path stat
above the branch — image tabs and text tabs legitimately share only the
size cap, which Step 1 placed.

**Verify**: `go test ./internal/editor/ -run NewTab` → all pass.

### Step 3: Share the text-open gate with Reload (tests first)

Tests in `tab_test.go`, watch each fail first:
- `TestReload_RefusesGrownFile` — open a small file, externally rewrite it
  to `maxOpenBytes+1` (sparse `Truncate`), `Reload()` (and, if plan 005
  landed, `ReloadKeepHistory()`) returns an error wrapping
  `ErrFileTooLarge`, **and the buffer still holds the original content**.
- `TestReload_RefusesNowBinaryFile` — externally rewrite the file with a
  NUL byte in the first bytes; reload refused wrapping `ErrBinaryFile`;
  buffer intact.

Implement by factoring the gate out of `NewTab`:

```go
// readTextFile is the single gate every text buffer fills through: it
// stats path against maxOpenBytes before reading (the guard must not be
// paid for by the read), reads, and refuses content looksBinary rejects.
// A missing file returns empty data and a zero mtime — NewTab treats that
// as a brand-new buffer; Reload's callers stat again and error earlier.
func readTextFile(path string) (data []byte, mtime time.Time, err error)
```

`NewTab`'s body (:287-314) becomes a call to it (preserving the
missing-file tolerance it has today); Reload's text read (:441-449, or
`reloadFromDisk` post-005) calls it and returns the error BEFORE touching
`t.Buffer`, `t.LineEnding`, or any other field.

**Verify**: `go test ./internal/editor/ -run 'NewTab|Reload'` → all pass,
including the two new refusal tests.

### Step 4: Full gates

**Verify**: `make test` → exit 0. `make lint` → exit 0. Also
`go test ./internal/app/ -run Reconcile` → still green (the silent-reload
flash path in refresh.go:393-395 now reports refusals as "reload failed:
…", which is correct and needs no change).

## Test plan

Five new tests, named per step. Fixtures: crafted GIF header bytes (no
image library needed to build it), sparse files via `Truncate` (fast, no
real 32 MiB writes), NUL-byte rewrite. Model style on existing
`tab_test.go`/`image_test.go` tests.

## Done criteria

- [ ] `make test` exits 0; `make lint` exits 0
- [ ] `grep -n "os.ReadFile" internal/editor/tab.go` shows no call in the
      Reload/reloadFromDisk path (only inside `readTextFile`)
- [ ] `go doc ./internal/editor ErrImageTooLarge` prints the sentinel
- [ ] The five new tests exist and pass
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back if:

- `Reload` matches neither this plan's excerpt nor plan 005's
  `reloadFromDisk` shape — unknown drift.
- `image.DecodeConfig` cannot read the crafted GIF header (unexpected
  stdlib behavior) — try a hand-built PNG IHDR with a computed CRC32 only
  if you can verify it against `image/png` locally; otherwise report.
- Refusing in Reload breaks an existing test that *depends* on reloading
  binary/huge content — that would mean a real caller wants ungated
  reloads; report which test.

## Maintenance notes

- Plan 008 adds `!utf8.Valid` refusal inside `readTextFile` — keep the gate
  as the single choke point.
- `maxImagePixels` is tunable; if users report legitimate refusals (satellite
  imagery, panoramas), raise it with a measurement, not a guess.
- Reviewer: check the Seek-after-DecodeConfig, and that Reload's refusal
  path leaves every Tab field untouched (no partial state).
