// =============================================================================
// File: internal/git/git.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package git owns skiff's git process boundary: every git invocation
// in the editor goes through one Repo handle, so environment hardening
// (no credential prompts, no editor spawns), argv hygiene (SafeRef) and
// timeouts exist exactly once instead of per call site. The Runner seam
// has two adapters — the exec-backed runner and the in-memory Fake — so
// states real git can't produce on demand (credential hangs, scripted
// failures) are testable.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// readTimeout bounds every read command. A read that talks to a remote
// (or wedges on a lock file) fails visibly after this long instead of
// freezing the editor's refresh goroutine forever.
const readTimeout = 10 * time.Second

// writeTimeout bounds every write command. It is deliberately generous:
// a push of a big repo over a slow link must be allowed to finish, and
// a false timeout mid-push is worse than a slow one. It exists at all
// because GIT_TERMINAL_PROMPT=0 only silences git's *own* prompt — a
// wedged credential helper or askpass binary blocks on its own stdin
// forever, and since writes run one at a time behind a single busy
// gate, that one hang would retire the editor's git verbs for the rest
// of the session.
const writeTimeout = 5 * time.Minute

// ErrGitMissing marks the one failure that isn't about this repository:
// there is no git binary on PATH at all. Every other error means "git
// ran and said no"; this one means we never got to ask, and a caller
// that wants to explain the difference (no badges anywhere, forever,
// on every project) can test for it with errors.Is.
var ErrGitMissing = errors.New("git executable not found in PATH")

// Runner executes one git invocation in a working directory. Output
// returns stdout only (reads); Combined returns interleaved
// stdout+stderr for the error report (writes). Both take their deadline
// from the caller so the Repo owns the policy in one place and tests
// can shorten it.
type Runner interface {
	Output(root string, timeout time.Duration, args ...string) ([]byte, error)
	Combined(root string, timeout time.Duration, args ...string) ([]byte, error)
}

// execRunner is the production adapter: real git with the hardened
// environment on every call.
type execRunner struct{}

// hardenedEnv is the environment every invocation runs under:
// GIT_TERMINAL_PROMPT=0 keeps a credential prompt from hanging a
// goroutine forever; GIT_EDITOR=true keeps merge-ish commands from
// trying to open an editor we can't host.
func hardenedEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
}

// command builds the one shape of git process this package ever spawns:
// bound to ctx, hardened env, WaitDelay set. WaitDelay matters — killing
// git on timeout can orphan a child (a smudge filter, an aliased shell)
// that keeps the output pipe open, and without the delay the wait would
// block on that pipe and the timeout would be decorative.
func command(ctx context.Context, root string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = hardenedEnv()
	cmd.WaitDelay = time.Second
	return cmd
}

// Output runs a read command with the read deadline applied.
func (execRunner) Output(root string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := command(ctx, root, args).Output()
	return out, classify(err)
}

// Combined runs a write command with the (generous) write deadline
// applied, capturing everything git says for the error report.
func (execRunner) Combined(root string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := command(ctx, root, args).CombinedOutput()
	return out, classify(err)
}

// classify maps exec's "no such binary" onto ErrGitMissing so callers
// can tell a machine without git from a repository that said no.
// Everything else passes through untouched.
func classify(err error) error {
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrGitMissing, err)
	}
	return err
}

// Repo is the handle to one working tree's git. The zero value is not
// usable — construct with Open (production) or OpenWith (tests).
type Repo struct {
	root         string
	run          Runner
	timeout      time.Duration
	writeTimeout time.Duration
}

// Open returns a Repo backed by real git. It does not verify that root
// is a repository — commands against a non-repo fail per call, exactly
// as the raw invocations did.
func Open(root string) *Repo {
	return &Repo{root: root, run: execRunner{}, timeout: readTimeout, writeTimeout: writeTimeout}
}

// OpenWith returns a Repo backed by a custom Runner — the injection
// point for the Fake.
func OpenWith(root string, r Runner) *Repo {
	return &Repo{root: root, run: r, timeout: readTimeout, writeTimeout: writeTimeout}
}

// Root returns the working-tree path the handle is bound to.
func (r *Repo) Root() string { return r.root }

// Output runs one read-only git command, returning stdout. Hardened
// env, read timeout.
func (r *Repo) Output(args ...string) ([]byte, error) {
	return r.run.Output(r.root, r.timeout, args...)
}

// RunSequence runs a series of write commands, stopping at the first
// failure. Output accumulates across commands so an error report shows
// everything git said.
func (r *Repo) RunSequence(cmds [][]string) (string, error) {
	var out strings.Builder
	for _, args := range cmds {
		b, err := r.run.Combined(r.root, r.writeTimeout, args...)
		out.Write(b)
		if err != nil {
			return out.String(), err
		}
	}
	return out.String(), nil
}

// Output is the package-level convenience for call sites that hold only
// a root path: one read command against real git. The Repo handle is
// the seam — this is a doorway to it, not a second seam.
func Output(root string, args ...string) ([]byte, error) {
	return Open(root).Output(args...)
}

// RunSequence is the package-level convenience for the write side.
func RunSequence(root string, cmds [][]string) (string, error) {
	return Open(root).RunSequence(cmds)
}
