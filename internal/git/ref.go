// =============================================================================
// File: internal/git/ref.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsafeRef reports a ref that must never be placed on git's argv.
// It is a sentinel so a caller can tell "we refused to run this" from
// "git ran and failed" with errors.Is.
var ErrUnsafeRef = errors.New("unsafe git ref")

// ErrUnsafePath reports a filesystem path that must never be placed on
// git's argv in a position git parses options from — the worktree
// verbs take the path before their flags, where no `--` protects it.
var ErrUnsafePath = errors.New("unsafe path")

// safePath refuses a path git would read as an option or that cannot
// cross the exec boundary. It is the path-shaped half of SafeRef: a
// path is not a ref (a leading "-" is the only injection it has), so
// it gets its own sentinel rather than borrowing the ref's.
func safePath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: empty", ErrUnsafePath)
	case strings.HasPrefix(p, "-"):
		return fmt.Errorf("%w: %q would be read as an option", ErrUnsafePath, p)
	case strings.ContainsRune(p, 0):
		return fmt.Errorf("%w: %q contains NUL", ErrUnsafePath, p)
	}
	return nil
}

// SafeRef validates a branch/tag/commit name that came from outside the
// editor — a picker row, a prompt, or the refs a cloned repository
// shipped — before it lands in an argument vector.
//
// This is not ref-name validation (git owns that, and being stricter
// than git would reject refs the user legitimately has). It closes the
// one hole that argument position alone can't: git parses anything
// starting with "-" as an option, so a branch named `--output=/tmp/x`
// turns `git diff --name-status <base>` into an arbitrary file write in
// the user's account, triggered by nothing more than opening a cloned
// repo. Empty is rejected for the same reason — an empty positional
// silently shifts the meaning of everything after it. NUL is rejected
// because it cannot survive the exec boundary and Go's error for it is
// unreadable.
//
// Callers still put `--` after the ref where git accepts one; the two
// defenses cover different halves of the problem (this one covers the
// commands, like push, that have no separator to give).
func SafeRef(s string) (string, error) {
	switch {
	case s == "":
		return "", fmt.Errorf("%w: empty", ErrUnsafeRef)
	case strings.HasPrefix(s, "-"):
		return "", fmt.Errorf("%w: %q would be read as an option", ErrUnsafeRef, s)
	case strings.ContainsRune(s, 0):
		return "", fmt.Errorf("%w: %q contains NUL", ErrUnsafeRef, s)
	}
	return s, nil
}
