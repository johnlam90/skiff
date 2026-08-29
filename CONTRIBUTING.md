# Contributing to Skiff

## Build and run

```sh
make build   # build to ./bin/skiff
make run     # go run . in the current dir
```

## Before you open a PR

CI runs these on every PR (Linux and macOS) and they gate merges:

```sh
make test    # go test ./... with the race detector
make lint    # gofmt + go vet + pinned staticcheck
```

Run both locally first. `make coverage` is also available if you want an
HTML coverage report.

## Tests are not optional

Every source file gets a corresponding `_test.go` file in the same
package. New code without tests should not be merged. Bug fixes need a
test that fails before the fix and passes after it. See `CLAUDE.md` for
the full bar and conventions (naming, `t.TempDir()`, doc comments on
`Test*` functions, etc.).

## Skiff is opinionated — read this before proposing UI changes

Skiff intentionally has **no `Ctrl+` editor shortcuts** — they conflict
with tmux and terminal emulators. All editor actions are reachable from
the `≡` action menu (click, right-click, or double-tap `Esc`) instead.
There's also no CGO, no tree-sitter, and no general plugin/config system.
See `CLAUDE.md`'s "What NOT to add" section for the full list before
proposing anything that touches keybindings, config, or dependencies —
it will very likely be rejected if it conflicts with these.

## Issues first

Please open or reference an issue before sending a PR. This repo doesn't
treat PRs as a request surface on their own (see
`docs/agents/issue-tracker.md`) — an issue is where the change gets
discussed and triaged first.

## Reporting a security issue

Do not open a public issue. See [SECURITY.md](SECURITY.md).
