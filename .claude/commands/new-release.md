---
description: Cut a new Skiff release (patch by default; pass minor / major / x.y.z for a manual bump)
allowed-tools: Bash(git:*), Bash(gh:*), Bash(make:*), Bash(grep:*)
---

Cut a release. `$ARGUMENTS` may be empty (patch), `minor`, `major`, or an explicit `x.y.z`.

CI owns the release: any push to `main` triggers `.github/workflows/release.yml`, which resolves the version, tags `v<x.y.z>`, runs GoReleaser (7 assets: linux/darwin × amd64/arm64 tarballs, windows amd64 zip, `checksums.txt`, and its cosign signature `checksums.txt.cosign.bundle`), and commits `Formula/skiff.rb` back into this repo with `[skip ci]`. Never create tags by hand and never push to the `homebrew-skiff` tap — it pull-syncs the formula on its own 15-minute cron.

1. **Preflight.** `git status --porcelain` empty; `git fetch origin && git status -sb` shows main not behind (`git pull --ff-only` if it is); `make test` green. Don't push red.
2. **Version.**
   - No argument → CI auto-bumps the patch. Just make sure the outgoing tip commit's message does not contain `[skip ci]` / `[ci skip]` — **anywhere, subject or body, even quoted descriptively** (a commit whose body merely *mentioned* the marker has silently skipped a release before), or the workflow never fires.
   - `minor` / `major` / explicit → edit `const Version` in `internal/version/version.go` and commit it as `Release v<x.y.z>`. The workflow decides by the VALUE, not by which commit touched the file: a version with no tag yet is taken as hand-set and released as-is, wherever in the push the edit sits; a version that is already tagged gets its patch bumped. So the one thing to confirm is that the target tag is free: `git ls-remote --tags origin 'v<x.y.z>'` → empty. If it is taken, the run will bump past it instead of releasing it.
3. **Push `main`.** That is the trigger — no tag, no other command.
4. **Watch.** `gh run list --workflow=release.yml --limit 1` for the run id, then `gh run watch <id> --exit-status`. For a manual bump, check the "Determine version" step's log says `v<x.y.z> is not tagged yet — a hand-set version, using as-is.` — if it says `Auto-bumping`, the tag already existed; stop and investigate before anything else.
5. **Verify.** `gh release view v<x.y.z>` lists all 7 assets. Then `git pull --ff-only` and confirm the write-back commits arrived (`Release v… [skip ci]` on the auto path, `Update skiff brew formula … [skip ci]`) and that no second release run started — the `[skip ci]` markers are the loop-breaker.
6. **Report** the released version, and note the brew tap mirrors the formula within ~15 minutes (`brew install johnlam90/skiff/skiff` serves it after that).

If the push doesn't trigger the workflow (a skip marker snuck into the tip commit), rerun with `gh workflow run release.yml` — then re-check the Determine-version log.
