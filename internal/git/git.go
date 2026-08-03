// =============================================================================
// File: internal/git/git.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package git owns skiff's git process boundary: every git invocation
// in the editor goes through one Repo handle, so environment hardening
// (no credential prompts, no editor spawns) and read timeouts exist
// exactly once instead of per call site. The Runner seam has two
// adapters — the exec-backed runner and the in-memory Fake — so states
// real git can't produce on demand (credential hangs, scripted
// failures) are testable.
package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// readTimeout bounds every read command. A read that talks to a remote
// (or wedges on a lock file) fails visibly after this long instead of
// freezing the editor's refresh goroutine forever.
const readTimeout = 10 * time.Second

// Runner executes one git invocation in a working directory. Output
// returns stdout only (reads); Combined returns interleaved
// stdout+stderr with no timeout (writes — a slow push must be allowed
// to finish, and GIT_TERMINAL_PROMPT=0 already prevents hangs on
// credential prompts).
type Runner interface {
	Output(root string, timeout time.Duration, args ...string) ([]byte, error)
	Combined(root string, args ...string) ([]byte, error)
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

// Output runs a read command with the timeout applied. WaitDelay
// matters: killing git on timeout can orphan a child (a smudge filter,
// an aliased shell) that keeps the stdout pipe open — without the
// delay, Output would wait on that pipe and the timeout would be
// decorative.
func (execRunner) Output(root string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = hardenedEnv()
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// Combined runs a write command without a timeout, capturing everything
// git says for the error report.
func (execRunner) Combined(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = hardenedEnv()
	return cmd.CombinedOutput()
}

// Repo is the handle to one working tree's git. The zero value is not
// usable — construct with Open (production) or OpenWith (tests).
type Repo struct {
	root    string
	run     Runner
	timeout time.Duration
}

// Open returns a Repo backed by real git. It does not verify that root
// is a repository — commands against a non-repo fail per call, exactly
// as the raw invocations did.
func Open(root string) *Repo {
	return &Repo{root: root, run: execRunner{}, timeout: readTimeout}
}

// OpenWith returns a Repo backed by a custom Runner — the injection
// point for the Fake.
func OpenWith(root string, r Runner) *Repo {
	return &Repo{root: root, run: r, timeout: readTimeout}
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
		b, err := r.run.Combined(r.root, args...)
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
