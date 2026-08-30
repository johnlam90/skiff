// =============================================================================
// File: internal/git/tree.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// tree.go is what the repository knows about its own working tree: where
// the root is, and which files it considers part of the project.

package git

import (
	"errors"
	"strings"
)

// Toplevel returns the absolute path of the working tree's root — the
// prefix every repo-relative path git reports is resolved against. It
// doubles as the repo test: a directory that is not inside a work tree
// fails here, which is how Status learns IsRepo.
func (r *Repo) Toplevel() (string, error) {
	out, err := r.read("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := strings.TrimRight(string(out), "\n\r")
	if top == "" {
		return "", errors.New("git rev-parse: empty toplevel")
	}
	return top, nil
}

// LsFiles lists every tracked and untracked-but-not-ignored file under
// the root, repo-relative with forward slashes, in git's own order. It
// honours .gitignore for free and costs one fork — git already has the
// index in memory — which is why the project finder prefers it to a
// directory walk. -z keeps a path containing a newline as one entry.
func (r *Repo) LsFiles() ([]string, error) {
	out, err := r.read("ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}
