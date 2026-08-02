---
description: Cut a new Skiff release (patch by default; pass minor / major / x.y.z for a manual bump)
allowed-tools: Bash(git:*), Bash(gh:*), Bash(make:*), Bash(grep:*)
---

Cut a release. `$ARGUMENTS` may be empty (patch), `minor`, `major`, or an explicit `x.y.z`.

CI owns the release: any push to `main` triggers `.github/workflows/release.yml`, which resolves the version, tags `v<x.y.z>`, runs GoReleaser (6 assets: linux/darwin × amd64/arm64 tarballs, windows amd64 zip, `checksums.txt`), and commits `Formula/skiff.rb` back into this repo with `[skip ci]`. Never create tags by hand and never push to the `homebrew-skiff` tap — it pull-syncs the formula on its own 15-minute cron.

1. **Preflight.** `git status --porcelain` empty; `git fetch origin && git status -sb` shows main not behind (`git pull --ff-only` if it is); `make test` green. Don't push red.
2. **Version.**
   - No argument → CI auto-bumps the patch. Just make sure the outgoing tip commit's message does not contain `[skip ci]` / `[ci skip]`, or the workflow never fires.
   - `minor` / `major` / explicit → edit `const Version` in `internal/version/version.go` and commit it as `Release v<x.y.z>`. This edit **must be the tip commit of the push**: the workflow only inspects `HEAD~1..HEAD`, so a buried version edit gets a patch bump stacked on top (an intended 0.2.0 silently ships as 0.2.1). Also confirm the target tag is free: `git ls-remote --tags origin 'v<x.y.z>'` → empty.
3. **Push `main`.** That is the trigger — no tag, no other command.
4. **Watch.** `gh run list --workflow=release.yml --limit 1` for the run id, then `gh run watch <id> --exit-status`. For a manual bump, check the "Determine version" step's log says `manually edited … using as-is` — if it says `Auto-bumping`, stop and investigate before anything else.
5. **Verify.** `gh release view v<x.y.z>` lists all 6 assets. Then `git pull --ff-only` and confirm the write-back commits arrived (`Release v… [skip ci]` on the auto path, `Update skiff brew formula … [skip ci]`) and that no second release run started — the `[skip ci]` markers are the loop-breaker.
6. **Report** the released version, and note the brew tap mirrors the formula within ~15 minutes (`brew install johnlam90/skiff/skiff` serves it after that).

If the push doesn't trigger the workflow (a skip marker snuck into the tip commit), rerun with `gh workflow run release.yml` — then re-check the Determine-version log, since the tip commit may no longer be the version edit.
