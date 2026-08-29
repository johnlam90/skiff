# Plan 015: Give humans a bug-report path — issue templates, CONTRIBUTING, SECURITY

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat 2616761..HEAD -- .github/ README.md CONTRIBUTING.md SECURITY.md docs/agents/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: dx
- **Planned at**: commit `2616761`, 2026-08-29

## Why this matters

A terminal editor's bugs are environment-shaped: `$TERM`, the emulator, the
multiplexer, SSH-or-local, OS/arch, color depth. Today the repo has excellent
*agent*-facing process docs but zero human contributor surface — no issue
templates, no CONTRIBUTING, no SECURITY policy — so every bug report starts a
round trip asking for the environment, and there is no private channel for a
vulnerability report. Templates that require the environment fields up front
convert one-line reports into actionable ones.

## Current state

- `.github/` contains only `workflows/` — no `ISSUE_TEMPLATE/`, no
  `PULL_REQUEST_TEMPLATE.md`. `ls CONTRIBUTING* SECURITY*` at the repo root:
  nothing.
- The repo is **PUBLIC** (`gh repo view --json visibility` → `PUBLIC`).
- `docs/agents/issue-tracker.md` — agent-facing `gh` conventions ("Issues and
  PRDs for this repo live as GitHub issues. Use the `gh` CLI for all
  operations."), including: "**PRs as a request surface: no.**" Keep the new
  human docs consistent with it, not duplicating it.
- `docs/agents/triage-labels.md` — the label vocabulary, verbatim:

  | Label | Meaning |
  | --- | --- |
  | `needs-triage` | Maintainer needs to evaluate this issue |
  | `needs-info` | Waiting on reporter for more information |
  | `ready-for-agent` | Fully specified, ready for an AFK agent |
  | `ready-for-human` | Requires human implementation |
  | `wontfix` | Will not be actioned |

- CLAUDE.md's test bar (line 153-155): "### Tests — required, not optional /
  **Every source file gets a corresponding `_test.go` file in the same
  package.** New code without tests should not be merged."
- CLAUDE.md's identity rule (line 22-24): "There are intentionally **no
  `Ctrl+` shortcuts** for editor actions — they conflict with `tmux` and
  terminal emulators."
- `README.md` has no Contributing section; `## License` sits at line 967.
- CLI facts for the template fields: `skiff --version` prints
  `skiff <x.y.z>` (see `main.go`, flag handling around lines 85-103).

## Commands you will need

| Purpose | Command | Expected on success |
|---------|---------|---------------------|
| YAML validity | `python3 -c "import yaml,glob; [yaml.safe_load(open(p)) for p in glob.glob('.github/ISSUE_TEMPLATE/*.yml')]"` | exit 0 |
| Repo gates | `make test && make lint` | exit 0 (should be untouched) |
| Private vuln reporting status | `gh api repos/johnlam90/skiff/private-vulnerability-reporting --jq .enabled` | `true` / `false` |

## Scope

**In scope** (create):
- `.github/ISSUE_TEMPLATE/bug_report.yml`
- `.github/ISSUE_TEMPLATE/feature_request.yml`
- `.github/ISSUE_TEMPLATE/config.yml`
- `CONTRIBUTING.md`
- `SECURITY.md`

**In scope** (modify):
- `README.md` — add a short Contributing section immediately before
  `## License` (line 967).

**Out of scope** (do NOT touch):
- `docs/agents/*` — the agent-facing process docs stay the source of truth
  for label semantics; link, don't copy beyond the table above.
- `PULL_REQUEST_TEMPLATE.md` — the tracker doc says PRs are not a request
  surface; a PR template is deliberately skipped (note in CONTRIBUTING that
  PRs should reference an issue).
- Any Go source, workflows, or Makefile.

## Git workflow

- Branch: `advisor/015-issue-templates-contributing`
- One commit ("Add issue templates, CONTRIBUTING, and SECURITY policy");
  no Claude trailers (CLAUDE.md forbids them).
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Bug report template

Create `.github/ISSUE_TEMPLATE/bug_report.yml` — GitHub issue-forms schema,
`labels: ["needs-triage"]` (matching the triage vocabulary above). Required
fields, in order:

1. `input`: **skiff version** — "paste the output of `skiff --version`" (required)
2. `input`: **$TERM** — "paste the output of `echo $TERM`" (required)
3. `input`: **Terminal emulator** — e.g. iTerm2, macOS Terminal, Ghostty, Windows Terminal, kitty (required)
4. `dropdown`: **Multiplexer** — options: tmux, zellij, herdr, none, other (required)
5. `dropdown`: **Connection** — options: local, SSH (required)
6. `input`: **OS / arch** — e.g. macOS arm64, Ubuntu 24.04 amd64 (required)
7. `textarea`: **What happened** (required)
8. `textarea`: **Steps to reproduce** (required)
9. `textarea`: **What you expected** (optional)

Keep descriptions one line each; this is a form, not documentation.

**Verify**: the YAML-validity command above → exit 0.

### Step 2: Feature request template + config

Create `feature_request.yml` (`labels: ["needs-triage"]`; textareas: the
problem, the proposed behavior, and "how does this fit a mouse-first,
no-Ctrl-shortcuts editor?" — that last prompt filters requests the identity
rules will reject). Create `config.yml`:

```yaml
blank_issues_enabled: true
```

**Verify**: YAML-validity command → exit 0.

### Step 3: CONTRIBUTING.md

Short — under ~60 lines. Contents:

- Build/run: `make build`, `make run`; the gates every PR must pass:
  `make test` (race suite) and `make lint` (gofmt + vet + pinned
  staticcheck), both enforced by CI on Linux and macOS.
- The test bar, quoting CLAUDE.md:153-155: every source file gets a paired
  `_test.go` in the same package; new code without tests should not be merged;
  bug fixes add a test that fails before the fix.
- The identity rules a PR must respect: no `Ctrl+` editor shortcuts
  (CLAUDE.md:22-24), every action reachable from the ≡ menu, no CGO, no
  config-system sprawl — link to CLAUDE.md's "What NOT to add" for the full
  list.
- Issues first: PRs should reference an issue (per
  `docs/agents/issue-tracker.md`, PRs are not a request surface).

**Verify**: `wc -l CONTRIBUTING.md` → ≤ 80; `grep -n "make test" CONTRIBUTING.md` → present.

### Step 4: SECURITY.md

Contents: report vulnerabilities privately via GitHub's private vulnerability
reporting (the Security tab → "Report a vulnerability"), not public issues;
supported version = latest release only (auto-released per merge); a
one-line scope note (the formatter/custom-action trust prompts are security
surfaces — reports about bypassing them are in scope).

Then check whether private reporting is actually enabled:
`gh api repos/johnlam90/skiff/private-vulnerability-reporting --jq .enabled`.
If `false`, attempt to enable it:
`gh api -X PUT repos/johnlam90/skiff/private-vulnerability-reporting` — if
that fails for permission reasons, add a line to your final report telling the
operator to flip it in Settings → Security; do not reword SECURITY.md (the
doc describes the intended channel either way).

**Verify**: `test -f SECURITY.md` → exit 0; report records the enabled-state.

### Step 5: Link from README

Immediately before `## License` (README.md:967), add:

```markdown
## Contributing

Bug reports and feature requests go through the
[issue templates](https://github.com/johnlam90/skiff/issues/new/choose) —
they ask for the terminal/multiplexer details a TUI bug always needs. Code
contributions: see [CONTRIBUTING.md](CONTRIBUTING.md); security reports: see
[SECURITY.md](SECURITY.md).
```

**Verify**: `grep -n "CONTRIBUTING.md" README.md` → 1 match; `make test && make lint` → exit 0.

## Test plan

No Go changes. Tests are: YAML parse of all three templates, the greps above,
and (post-merge, manual) opening the "New issue" chooser on GitHub renders
both forms — note that final render check as a post-merge step for the
operator since it needs the files on the default branch.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `.github/ISSUE_TEMPLATE/{bug_report.yml,feature_request.yml,config.yml}` exist and parse as YAML
- [ ] `bug_report.yml` contains required fields for version, `$TERM`, emulator, multiplexer (with `herdr` in the options), connection, OS/arch
- [ ] both templates carry `needs-triage` in `labels:`
- [ ] `CONTRIBUTING.md` and `SECURITY.md` exist; CONTRIBUTING quotes the test bar and names `make test` / `make lint`
- [ ] README links all of it before `## License`
- [ ] `make test` and `make lint` exit 0; `git status --short` shows only in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `.github/ISSUE_TEMPLATE/` already exists with content (another plan or a
  human got there first — reconcile, don't overwrite).
- The label vocabulary in `docs/agents/triage-labels.md` has changed from the
  table quoted above (use the live table, and note the drift).
- README's `## License` heading is no longer at/near line 967 and you cannot
  find an equivalent anchor.

## Maintenance notes

- The template's multiplexer dropdown includes `herdr` because the repo
  carries compatibility research for it (`docs/research/2026-08-02-herdr-compatibility.md`);
  if new multiplexers get documented support, extend the dropdown.
- If a future plan enables GitHub's dependabot security updates or secret
  scanning (both currently disabled per the API), SECURITY.md needn't change —
  it describes the reporting channel, not the scanning posture.
- The `needs-triage` label must actually exist in the tracker for the
  templates to apply it; `gh label create needs-triage` is idempotent
  insurance the executor may run (`gh label list` first).
