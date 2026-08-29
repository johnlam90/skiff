# Plan 014: Make every install/platform/count claim in the docs true

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- README.md AGENTS.md .goreleaser.yml Makefile website/ .github/workflows/test.yml`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S-M
- **Risk**: LOW (docs and comments; the one structural decision — the website — is gated on the operator)
- **Depends on**: none
- **Category**: docs
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

The highest-traffic install surface contradicts itself: the website tells
macOS users to run exactly the `brew tap`-by-URL command the README calls the
broken legacy path, so following the website leaves a stale tap the README
then tells you to `brew untap`. Around it sit a cluster of smaller wrongs —
theme counts off by one-to-two, a windows/arm64 build that is deliberately
not shipped but promised in three places, a `make install` doc that names the
wrong directory, an `AGENTS.md` pointing at a directory that doesn't exist,
and a website whose deploy pipeline was deleted (its version badge is frozen
at v0.0.25 while the product is at 0.2.9+). Stale docs on a 971-line README
are worse than missing ones: readers extend their trust of the accurate parts
to the wrong parts.

## Current state

**(a) The Homebrew contradiction.** `README.md:212-218` (correct — matches
the pull-sync mechanism CLAUDE.md documents):

```
brew install johnlam90/skiff/skiff
```
> That's it — brew resolves the
> [johnlam90/homebrew-skiff](https://github.com/johnlam90/homebrew-skiff)
> tap automatically. (If you tried an older install command and it left a
> broken tap behind, `brew untap johnlam90/skiff` first.)

`website/content/docs/installation.md:13-18` (wrong — this IS the older
command):

```
The Homebrew formula lives in this repo's `Formula/` directory. Tap it by URL — there's no separate `homebrew-tap` repo to remember:

    brew tap johnlam90/skiff https://github.com/johnlam90/skiff
    brew install johnlam90/skiff/skiff
```

`.goreleaser.yml:63-67` (comment prescribing the same wrong form):

```yaml
# Homebrew formula committed straight back into this repo under Formula/.
# Users install via:
#
#   brew tap johnlam90/skiff https://github.com/johnlam90/skiff
#   brew install johnlam90/skiff/skiff
```

CLAUDE.md's "Module / repo" section states the real mechanism: the
`johnlam90/homebrew-skiff` tap is what users install from
(`brew install johnlam90/skiff/skiff`); `Formula/skiff.rb` in THIS repo is the
source of truth that the tap repo pull-syncs on a cron. README is right;
website and goreleaser comment are wrong.

**(b) Theme counts.** `internal/theme/palettes.go` registers 27 entries
(Tokyo Night + 26 more, including `offshore` added 2026-08-28). Three README
sites disagree with it and each other:

- `README.md:54` — "25 more themes one menu away"
- `README.md:162` — "**26 themes with live preview**"
- `README.md:918` — "`│   ├── theme/                # 26 palettes + the low-color fallback`"

**(c) Platform matrix.** `.goreleaser.yml:28-34` builds
linux/darwin/windows × amd64/arm64 **minus windows/arm64**, deliberately:

```yaml
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    # Skip windows/arm64 — less common, and we'd rather not ship binaries
    # we can't easily test. Add it back if there's demand.
    ignore:
      - goos: windows
        goarch: arm64
```

Three README sites promise the full matrix:

- `README.md:204-205` — "cross-compiled for macOS, Linux, and Windows on amd64 and arm64"
- `README.md:275` — "Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64)"
- `README.md:960` — "linux/darwin/windows × amd64/arm64"

(Windows/amd64 IS shipped — only the windows-arm64 half of the claim is wrong.)

**(d) `make install` target dir.** `README.md:285`:

```
make install        # builds and installs to $GOPATH/bin
```

`Makefile:55-57`:

```make
# install copies the binary into /usr/local/bin so you can launch it as `skiff`.
install: build
	install -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY)
```

`/usr/local/bin` typically needs sudo — a different failure than the doc
predicts. (CLAUDE.md's own build table says "make install → go install to
$GOPATH/bin", which matches neither; fix the README to match the Makefile,
and correct the CLAUDE.md table line too.)

**(e) Phantom `samples/`.** `AGENTS.md:5` ends: "Release packaging includes
`Formula/skiff.rb`, `install.sh`, and samples under `samples/`." No
`samples/` directory exists anywhere in the repo.

**(f) The undeployed website.** Commit `5ddba99` removed the Pages redeploy
step; no `pages.yml` exists (`.github/workflows/` holds only `release.yml`
and `test.yml`) and there is no `gh-pages` branch. Yet:

- `website/hugo.toml:28-32` — `version = "v0.0.25"` under the comment "The
  Pages workflow rewrites this from internal/version/version.go on every
  deploy" (actual version at planning time: 0.2.9).
- `Makefile:128-129` — "site-build produces the production-ready static site…
  This is what the GitHub Pages workflow ships."
- `website/README.md:19` — "The `static/CNAME` ships through to
  `public/CNAME` automatically" and `:56` lists `CNAME` under `static/`;
  `website/static/` contains no CNAME file.
- The site's 13 docs pages drift with no signal (that is how (a) happened),
  while `test.yml:84-86` and `Makefile:89` both special-case `website/` out of
  the gofmt gate.

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| Full test suite | `make test` | exit 0 |
| Lint gates | `make lint` | exit 0 |
| Theme registry count | `grep -c "^	{ID:" internal/theme/palettes.go` | current count (27 at planning; re-run, do not assume) |
| No stale tap form | `grep -rn "brew tap johnlam90/skiff https" README.md website/ .goreleaser.yml` | no matches (after Step 2) |

## Scope

**In scope** (the only files you should modify):
- `README.md`
- `AGENTS.md`
- `CLAUDE.md` (the one `make install` line in the build table)
- `.goreleaser.yml` (comment block only — lines 63-72)
- `website/content/docs/installation.md`
- `website/README.md`, `website/hugo.toml`, `Makefile`, `.github/workflows/test.yml`, `.github/workflows/` — ONLY under the operator-chosen branch of Step 5
- everything else under `website/` — ONLY under Step 5's delete branch

**Out of scope** (do NOT touch, even though they look related):
- `Formula/skiff.rb` — generated.
- Any Go source or test file (the theme count is fixed by removing numerals
  from prose, not by adding a count-pinning test — see Step 3).
- `install.sh` — its wording is owned by plan 013.

## Git workflow

- Branch: `advisor/014-docs-truth-pass`
- One commit per step; imperative messages ("Fix Homebrew instructions to
  match the tap pull-sync"); no Claude trailers (CLAUDE.md forbids them).
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Fix the website's Homebrew instructions

Rewrite `website/content/docs/installation.md:13-18` to the README's form:
`brew install johnlam90/skiff/skiff` resolves the
`johnlam90/homebrew-skiff` tap automatically; drop the "Tap it by URL" framing
entirely; keep exactly one recovery note ("if an older install left a
`johnlam90/skiff` tap behind, `brew untap johnlam90/skiff` first").

**Verify**: `grep -n "brew tap" website/content/docs/installation.md` → no matches.

### Step 2: Fix the `.goreleaser.yml` comment

Rewrite the comment block at `.goreleaser.yml:63-72`: the formula lives in
`Formula/` here as the source of truth, the `johnlam90/homebrew-skiff` tap
repo pull-syncs it, and users install with the single
`brew install johnlam90/skiff/skiff` command. Keep the `[skip ci]` sentence.

**Verify**: `grep -rn "brew tap johnlam90/skiff https" README.md website/ .goreleaser.yml` → no matches; `grep -n "skip ci" .goreleaser.yml` → still present.

### Step 3: Drop the theme numerals

Replace the three counted claims with count-free phrasing so they can never
go stale again:

- `README.md:54` → "a couple dozen more themes one menu away"
- `README.md:162` → "**Themes with live preview**" (keep the rest of the bullet)
- `README.md:918` → "`# the theme registry + the low-color fallback`"

**Verify**: `grep -n "25 more\|26 themes\|26 palettes" README.md` → no matches.

### Step 4: Fix the platform matrix and `make install` lines

- `README.md:204-205` → "cross-compiled for macOS and Linux (amd64 + arm64)
  and Windows (amd64)".
- `README.md:275` → "Pre-built binaries for Linux and macOS (amd64 + arm64)
  and Windows (amd64) are attached…".
- `README.md:960` → "linux/darwin (amd64+arm64) and windows/amd64".
- `README.md:285` → `make install        # builds and installs to /usr/local/bin (may need sudo)`.
- CLAUDE.md build table: change "make install → go install to $GOPATH/bin" to
  "make install → install to /usr/local/bin".
- `AGENTS.md:5`: delete the ", and samples under `samples/`" clause.

**Verify**: `grep -n "arm64" README.md` shows no windows-arm64 pairing; `grep -rn "samples/" AGENTS.md` → no matches; `grep -n "GOPATH/bin" README.md CLAUDE.md` → no matches.

### Step 5: DECISION — publish or delete the website (STOP and ask)

Present both branches to the operator and **STOP until they choose**. Steps
1-4 land regardless of the answer.

**Branch A — publish**: add `.github/workflows/pages.yml` that (on release, or
manual dispatch) runs `make site-install site-build`, rewrites
`params.version` in `website/hugo.toml` from `internal/version/version.go`
(one `sed`, mirroring release.yml's version parse), and deploys `website/public`
via `actions/deploy-pages` (SHA-pinned per the conventions plan 013
establishes). Fix `website/README.md`'s CNAME lines to match reality (no
custom domain unless the operator names one). Content of the 13 docs pages
should then get its own review pass (flag as follow-up, not this plan).

**Branch B — delete**: `git rm -r website/`; remove the four `site-*` targets
and `SITE_DIR` from `Makefile` (lines ~117-140 plus the help lines ~39-42 and
the `.PHONY` entries at ~20); remove the `website/` special-cases from the
gofmt gates (`Makefile:89`, `test.yml:84-86` — the grep -v filter becomes
unnecessary); update `AGENTS.md`'s "Website assets and docs live in
`website/`" sentence and add `website/` removal to README's project-structure
tree if listed (it is not, at planning time).

**Verify (A)**: pages workflow YAML parses; `make site-build` exits 0 locally.
**Verify (B)**: `ls website` → not found; `make lint` and `make test` exit 0; `grep -rn "website/" Makefile .github/workflows/` → no matches.

## Test plan

Doc-only (plus the Step 5 branch): the greps above are the tests. After all
steps: `make test` and `make lint` must still exit 0 (they should be
untouched by Steps 1-4; Step 5B modifies the gofmt gate and must stay green).

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `grep -rn "brew tap johnlam90/skiff https" README.md website/ .goreleaser.yml` → no matches (website greps skipped if Step 5B deleted it)
- [ ] `grep -n "25 more\|26 themes\|26 palettes" README.md` → no matches
- [ ] no README line pairs "Windows" with "arm64"
- [ ] `grep -rn "samples/" AGENTS.md` → no matches
- [ ] `grep -n "GOPATH/bin" README.md CLAUDE.md` → no matches
- [ ] Step 5 decision recorded (which branch, executed or explicitly deferred by the operator)
- [ ] `make test` and `make lint` exit 0
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- Any "Current state" excerpt no longer matches the live file.
- The theme registry count differs from what a fresh
  `grep -c "^	{ID:" internal/theme/palettes.go` reports vs. what README now
  implies — re-check Step 3's phrasing still holds, then proceed.
- The operator has not answered the Step 5 decision — complete Steps 1-4,
  record Step 5 as pending in `plans/README.md`, and stop.
- Executing Branch B would delete anything git reports as modified/untracked
  under `website/` (someone's work in progress) — report instead.

## Maintenance notes

- The counted-claims problem recurs with every new theme; Step 3's count-free
  phrasing is the durable fix. If a future maintainer reintroduces a numeral,
  point them here.
- If Branch A (publish) is chosen, the website's 13 docs pages need a
  content-accuracy pass before the first deploy — this plan only fixed the
  install page; the rest was NOT audited line-by-line.
- Plan 013 rewords README's checksum claim in the same file — coordinate
  merges (both touch README.md but disjoint lines).
