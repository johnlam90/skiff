# Plan 013: Harden the release pipeline — pin the toolchain, guard re-runs, sign artifacts, drop dead scopes

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- .github/workflows/release.yml .goreleaser.yml install.sh README.md CLAUDE.md .github/dependabot.yml`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (release-pipeline changes are only fully provable on a real release; every step below has a dry-run verification precisely because of that)
- **Depends on**: none
- **Category**: security / dx
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

Every merge to `main` auto-releases: the pipeline runs unattended, constantly.
Today it builds with whatever GoReleaser shipped that day (`version: latest`),
cannot recover from a failed run once the tag exists, grants a workflow scope
nothing uses anymore, and ships unsigned artifacts while `install.sh` tells
users a checksum mismatch means the download "has been tampered with" — a
stronger claim than same-origin checksums can back. This plan pins the
toolchain, makes failed releases re-runnable, adds cosign signing + build
provenance, softens the overclaim until signing lands, and adds dependabot so
staleness is surfaced automatically instead of by audit.

## Current state

Relevant files:

- `.github/workflows/release.yml` — the push-to-main release pipeline (test gate → version bump → tag → GoReleaser).
- `.goreleaser.yml` — build matrix, archives, checksums, changelog, brew formula.
- `install.sh` — curl-pipe-sh installer; verifies `checksums.txt` (mandatory, no skip flag — that part is good and stays).
- `CLAUDE.md` — the "## Releases (don't break this)" section (line 467) documents the pipeline for future agents; it gains a recovery note.
- `.github/dependabot.yml` — does not exist yet.

**(a) Unpinned GoReleaser** — `.github/workflows/release.yml:149-153`:

```yaml
      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
```

**(b) The "skipping" exit that doesn't skip** — `.github/workflows/release.yml:133-147`:

```yaml
      - name: Tag release
        run: |
          set -euo pipefail
          NEW="${{ steps.version.outputs.version }}"
          if git rev-parse -q --verify "refs/tags/v$NEW" >/dev/null; then
            echo "Tag v$NEW already exists, skipping."
            exit 0
          fi
```

`exit 0` ends that *step* successfully; the GoReleaser step below has no `if:`
guard, so on a re-run of a failed release (tag exists, publish didn't finish)
GoReleaser runs again unconditionally and fails against the half-published
release. There is no documented recovery.

**(c) Unused elevated scope** — `.github/workflows/release.yml:44-51`:

```yaml
# Pushes back to main need write access to the repo's contents. The
# `actions: write` scope lets the final step kick off the Pages
# workflow via workflow_dispatch — the version-bump commit carries
# [skip ci] so the auto-deploy on internal/version/version.go won't
# fire on its own.
permissions:
  contents: write
  actions: write
```

The Pages-dispatch step this comment describes was deleted in commit
`5ddba99` ("chore: drop Pages redeploy step from release workflow"); no
`pages.yml` exists under `.github/workflows/` (only `release.yml` and
`test.yml`). `actions: write` is now dead weight on a workflow that pushes to
`main`. The comment block at `release.yml:37-42` ("Bumps, tags, and dispatches
Pages exactly the same…") is likewise stale.

**(d) Mutable action tags** — every `uses:` in both workflows references a
major tag, not a commit SHA: `actions/checkout@v4` (test.yml:56,
release.yml:69), `actions/setup-go@v5` (test.yml:62, release.yml:81),
`actions/upload-artifact@v4` (test.yml:116), `goreleaser/goreleaser-action@v6`
(release.yml:150).

**(e) No signing** — `.goreleaser.yml:49-50` is the only integrity block:

```yaml
checksum:
  name_template: "checksums.txt"
```

There is no `signs:` and no `sboms:` stanza. `install.sh` fetches
`checksums.txt` from the same GitHub release as the archive — a real defense
against truncation/corruption/stale mirrors, but not against a compromised
publishing path. Yet `install.sh:162` says:

```sh
		fatal "the download is corrupt or has been tampered with — aborting"
```

and the same authenticity framing appears in the `install.sh` header comment
(lines 9-16: "verifies it against the release's published checksums.txt …
there is deliberately no 'skip verification' escape hatch") and in
`README.md:250-256` ("there is deliberately no way to skip verification,
because this is remote code about to land on your `$PATH`"). The hygiene is
genuinely good — mandatory verification, POSIX sh, function-wrapped `main
"$@"` so a truncated pipe can't execute a partial script. Only the
*authenticity* implication outruns the mechanism.

**(f) Deprecated `brews:` key** — `.goreleaser.yml:73` uses the top-level
`brews:` block. GoReleaser v2 has deprecated it in favor of Cask-based
publishing (`homebrew_casks`); the removal timeline is not verified offline,
so migration is investigate-then-do (Step 7), not a blind swap.

**(g) No dependabot** — `.github/` contains only `workflows/`. The repo's one
problematic dependency (an unmaintained 2021 pseudo-versioned gitignore
library — handled by a separate plan) is exactly the kind of thing a bot
surfaces as "no updates in N years".

**Constraint that must survive every step** — CLAUDE.md:481-484:

> If you're touching the workflow or `.goreleaser.yml`, make sure both
> auto-commits keep their `[skip ci]` markers — without them the workflow
> loops forever.

And CLAUDE.md's "What NOT to add": **no cross-repo release tokens** — the
`homebrew-skiff` tap repo pull-syncs `Formula/skiff.rb` from this repo on its
own cron. Nothing in this plan may introduce a PAT or push to another repo.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full test suite | `make test` | exit 0, all packages `ok` |
| Lint gates | `make lint` | exit 0, no output after staticcheck line |
| GoReleaser config check | `go run github.com/goreleaser/goreleaser/v2@v2.11.2 check` | `1 configuration file(s) validated` |
| GoReleaser dry run | `go run github.com/goreleaser/goreleaser/v2@v2.11.2 release --snapshot --clean --skip=publish,homebrew` | exit 0, archives under `dist/` |
| Workflow syntax | `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml'))"` | exit 0 |
| Shell lint | `sh -n install.sh` | exit 0 |

(If `v2.11.2` is not the version you pinned in Step 1, use the pinned one in
the two goreleaser commands — the point is that the dry run and the workflow
use the SAME version.)

## Scope

**In scope** (the only files you should modify):
- `.github/workflows/release.yml`
- `.github/workflows/test.yml` (SHA-pinning its three actions only)
- `.goreleaser.yml`
- `install.sh` (wording of the tamper claim; optional cosign verification branch)
- `README.md` (the one sentence at ~:250-256 making the same claim)
- `CLAUDE.md` (Releases section: recovery note; keep everything else)
- `.github/dependabot.yml` (create)

**Out of scope** (do NOT touch, even though they look related):
- `Formula/skiff.rb` — generated by GoReleaser on release; hand edits are overwritten.
- Any Go source or test file.
- The tap repo or any cross-repo mechanism (CLAUDE.md forbids it).
- `website/` — a separate plan owns doc content.
- The auto-patch-bump-per-merge release cadence — decided tradeoff, keep it.

## Git workflow

- Branch: `advisor/013-release-pipeline-hardening`
- One commit per step below (each is independently shippable); imperative
  messages in the repo's style (e.g. "Pin GoReleaser and guard the re-run
  path"). Do NOT add Claude/Co-Authored-By trailers — CLAUDE.md forbids them.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Pin GoReleaser to an exact version

In `.github/workflows/release.yml`, change `version: latest` (line 152) to an
exact tag. Resolve the current v2 release first:
`gh release view --repo goreleaser/goreleaser --json tagName -q .tagName`
(expect something like `v2.x.y`; use exactly what it prints). Add a one-line
comment mirroring the staticcheck pin's rationale (see `Makefile:78-87` and
`test.yml`'s pinned staticcheck for the house style): the tool that builds
every shipped binary must not float.

**Verify**: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"` → exit 0, and `grep -n "version: v2" .github/workflows/release.yml` shows the pin.

### Step 2: Make a failed release re-runnable

In the `Tag release` step (`release.yml:133-147`):
1. Give the step `id: tag`.
2. Replace the `exit 0` branch with an output instead of a fake skip:

```yaml
          if git rev-parse -q --verify "refs/tags/v$NEW" >/dev/null; then
            echo "Tag v$NEW already exists — release will retry against it."
            echo "tag_existed=true" >> "$GITHUB_OUTPUT"
            exit 0
          fi
          echo "tag_existed=false" >> "$GITHUB_OUTPUT"
```

3. The GoReleaser step stays unguarded (it must run in both cases — that is
   the recovery), but a retry against an existing tag needs the half-made
   release cleared first. Add a step between `Tag release` and
   `Run GoReleaser`:

```yaml
      - name: Clear half-published release on retry
        if: steps.tag.outputs.tag_existed == 'true'
        run: |
          set -euo pipefail
          NEW="${{ steps.version.outputs.version }}"
          gh release delete "v$NEW" --yes || echo "No release object for v$NEW — nothing to clear."
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

(`gh release delete` removes the release object and its assets but NOT the
git tag, which is exactly what a retry wants: GoReleaser recreates the
release against the existing tag.)

4. In CLAUDE.md's "## Releases (don't break this)" section (line 467), append
   a short paragraph after the existing numbered list:

> A failed GoReleaser run (API blip, upload timeout) is re-runnable: the tag
> step records that the tag already exists and the next step deletes the
> half-published release object (never the tag) before GoReleaser retries.
> "Re-run failed jobs" on the Actions run is the whole recovery.

**Verify**: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"` → exit 0; `grep -n "tag_existed" .github/workflows/release.yml` → 3 matches (two writes, one `if:`); `grep -n "re-runnable" CLAUDE.md` → 1 match.

### Step 3: Drop the dead `actions: write` scope and its stale comments

In `release.yml`: delete `actions: write` from `permissions:` (line 51),
rewrite the comment block above it (lines 44-48) to just explain
`contents: write` (pushes of the bump/formula commits back to main), and fix
the `workflow_dispatch` comment at lines 37-42 to drop "and dispatches Pages".

**Verify**: `grep -n "actions: write\|Pages" .github/workflows/release.yml` → no matches.

### Step 4: Pin all actions to commit SHAs

For each `uses:` in `release.yml` and `test.yml` (inventory in Current state
(d)), resolve the SHA of the major tag currently referenced:
`gh api repos/actions/checkout/git/ref/tags/v4 --jq .object.sha` (dereference
annotated tags via `gh api repos/actions/checkout/git/tags/<sha> --jq
.object.sha` if the first call returns a tag object type). Rewrite as
`uses: actions/checkout@<full-sha> # v4` — keep the human-readable tag as a
trailing comment. Do the same for setup-go, upload-artifact, and
goreleaser-action. The local `uses: ./.github/workflows/test.yml` needs no pin.

**Verify**: `grep -n "uses:" .github/workflows/*.yml` → every external action shows a 40-char SHA plus a `# vN` comment.

### Step 5: Sign artifacts and attest provenance

1. In `.goreleaser.yml`, after the `checksum:` block, add cosign keyless
   signing of the checksum file:

```yaml
# Keyless cosign signature over checksums.txt: signing the checksum file
# covers every archive it lists. Verification is optional in install.sh
# (when cosign is on PATH) so BusyBox/minimal targets keep working.
signs:
  - cmd: cosign
    args: ["sign-blob", "--yes", "--output-signature=${signature}", "--output-certificate=${certificate}", "${artifact}"]
    artifacts: checksum
    certificate: "${artifact}.pem"
```

2. In `release.yml`'s release job: add `id-token: write` and
   `attestations: write` to `permissions:`; add
   `- name: Install cosign` using `sigstore/cosign-installer` (SHA-pinned per
   Step 4) before the GoReleaser step; after GoReleaser, add
   `actions/attest-build-provenance` (SHA-pinned) with
   `subject-path: dist/*.tar.gz` and `dist/*.zip`.
3. In `install.sh`, add an optional verification in `verify_checksum`'s
   caller path: when `command -v cosign` succeeds, download
   `checksums.txt.sig` and `checksums.txt.pem` from the same release and run
   `cosign verify-blob` with `--certificate-identity-regexp` matching this
   repo's workflow (`https://github.com/johnlam90/skiff/.github/workflows/release.yml@.*`)
   and `--certificate-oidc-issuer https://token.actions.githubusercontent.com`.
   On verification failure: `fatal`. When cosign is absent or the `.sig`
   asset does not exist (releases predating this plan): print one informative
   line and continue — checksums remain the floor, signature the ceiling.

**Verify**: `go run github.com/goreleaser/goreleaser/v2@<pinned> check` → validated. (`release --snapshot` does NOT exercise `signs:` against real OIDC — that is expected; note it in the commit message.) `sh -n install.sh` → exit 0.

### Step 6: Right-size the tamper claim until a signed release exists

Three sites, one wording. Replace the authenticity claim with a corruption
claim plus the new signature story:

- `install.sh:162`: `fatal "the download does not match the published checksum — refusing to install (corrupt or incomplete download)"`.
- `install.sh` header (lines 9-16): keep the mandatory-verification sentence;
  add "and, when cosign is installed, verifies the release signature."
- `README.md:250-256`: same adjustment — checksums guarantee integrity, the
  optional cosign check adds authenticity.

**Verify**: `grep -rn "tampered" install.sh README.md` → no matches.

### Step 7 (flagged — do NOT execute, document only): brews→casks migration

Add a comment above the `brews:` block in `.goreleaser.yml`:

```yaml
# NOTE(deprecation): goreleaser v2 deprecates top-level `brews:`. Migration to
# `homebrew_casks` changes the install mechanism for existing formula users
# (quarantine/caveats differ) and the tap pull-sync consumes Formula/skiff.rb
# — migrate ONLY on a prerelease tag first, and verify `brew upgrade` from an
# existing formula install before letting a real release use it.
```

Do not perform the migration in this plan.

**Verify**: `grep -n "NOTE(deprecation)" .goreleaser.yml` → 1 match; `go run github.com/goreleaser/goreleaser/v2@<pinned> check` still validates.

### Step 8: Add dependabot

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule: {interval: monthly}
    groups:
      go-deps: {patterns: ["*"]}
  - package-ecosystem: github-actions
    directory: /
    schedule: {interval: monthly}
    groups:
      actions: {patterns: ["*"]}
```

(Grouped + monthly to keep PR noise near zero; `test.yml` already gates every
PR. SHA-pinned actions from Step 4 are still updatable — dependabot rewrites
SHA pins and their version comments.)

**Verify**: `python3 -c "import yaml; yaml.safe_load(open('.github/dependabot.yml'))"` → exit 0.

### Step 9: Full gates + snapshot build

**Verify**: `make test` → exit 0. `make lint` → exit 0. `go run github.com/goreleaser/goreleaser/v2@<pinned> release --snapshot --clean --skip=publish,homebrew` → exit 0 and `ls dist/ | grep -c tar.gz` ≥ 4. `git status --short` → only in-scope files modified.

## Test plan

No Go code changes, so no new Go tests. The verification story is:
config validation (`goreleaser check`), a full snapshot build, YAML parses,
`sh -n`, and the greps above. The first real release after merge is the true
end-to-end test — Step 2's recovery path should be exercised deliberately by
re-running that release's job once ("Re-run failed jobs") and confirming it
converges instead of failing.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `grep -n "version: latest" .github/workflows/release.yml` → no matches
- [ ] `grep -c "tag_existed" .github/workflows/release.yml` → 3
- [ ] `grep -n "actions: write" .github/workflows/release.yml` → no matches
- [ ] every external `uses:` in both workflows is SHA-pinned with a `# vN` comment
- [ ] `.goreleaser.yml` contains a `signs:` block and `goreleaser check` validates
- [ ] `grep -rn "tampered" install.sh README.md` → no matches
- [ ] `.github/dependabot.yml` exists and parses
- [ ] `grep -c "skip ci" .github/workflows/release.yml .goreleaser.yml` → both markers still present (release.yml commit message, goreleaser `commit_msg_template`)
- [ ] `make test` and `make lint` exit 0
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- Any excerpt in "Current state" no longer matches the live file (drift).
- `goreleaser check` rejects the `signs:` stanza under the pinned version —
  the args schema may differ by version; report the pinned version and the
  error rather than guessing at flags.
- You cannot resolve action SHAs (no network / no `gh` auth) — deliver the
  plan minus Step 4/5's SHA lookups and list the unresolved pins.
- Anything would require a cross-repo token or touching the tap repo.
- Either `[skip ci]` marker would be lost by your edit.

## Maintenance notes

- The GoReleaser pin and the action SHAs are now dependabot's to bump —
  review those PRs like code (the release pipeline is the supply chain).
- After the first signed release lands, `install.sh`'s cosign branch can be
  promoted from warn-and-continue to hard-fail; that promotion is deliberately
  deferred (older releases have no `.sig` asset, and pinned-VERSION installs
  must keep working).
- Step 7's cask migration stays parked until GoReleaser announces removal;
  test on a prerelease tag first and watch the tap pull-sync consume the
  result before trusting it.
