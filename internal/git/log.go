// =============================================================================
// File: internal/git/log.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// log.go is the history vocabulary: the commit list a log overlay
// shows, and the diff one commit introduced.

package git

import (
	"errors"
	"strconv"
	"strings"

	"github.com/johnlam90/skiff/internal/diff"
)

// Commit is one row of history: abbreviated hash, subject line, and
// git's human-readable relative age ("2 days ago") — what the log
// overlay shows, nothing it doesn't.
type Commit struct {
	Hash    string
	Subject string
	When    string
}

// logFormat separates the three fields with NUL rather than a tab: a
// subject can contain a tab, and a record split on tabs would then
// shift the age into the subject. A subject cannot contain a newline
// (%s is the first paragraph, folded), so newline stays the record
// terminator.
const logFormat = "--format=%h%x00%s%x00%cr"

// Log returns up to n commits, newest first. path narrows the log to
// one file (with --follow so renames don't truncate the story); empty
// path logs the whole branch. n must be positive — a zero limit would
// pull the entire history, never what a bounded overlay wants.
func (r *Repo) Log(n int, path string) ([]Commit, error) {
	if n <= 0 {
		return nil, errors.New("git log: limit must be positive")
	}
	args := []string{"log", logFormat, "-n", strconv.Itoa(n)}
	if path != "" {
		args = append(args, "--follow", "--", path)
	} else {
		args = append(args, "--")
	}
	out, err := r.read(args...)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out)), nil
}

// parseLog splits logFormat records into commits, dropping any line
// that doesn't carry all three fields.
func parseLog(out string) []Commit {
	var commits []Commit
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 || fields[0] == "" {
			continue
		}
		commits = append(commits, Commit{Hash: fields[0], Subject: fields[1], When: fields[2]})
	}
	return commits
}

// Show returns the diff a commit introduced — the whole commit, or just
// path's part of it when path is non-empty. `--format=` suppresses the
// message header so the output starts at the first `diff --git`. hash
// goes through SafeRef even though today it only ever holds Log's own
// %h output: the gate is what stands between a future caller and a
// repo-supplied string landing on git's argv as an option. A merge
// commit yields a combined diff the parser reads as empty, and a
// caller says so rather than opening a hollow view.
func (r *Repo) Show(hash, path string) (diff.Patch, error) {
	hash, err := SafeRef(hash)
	if err != nil {
		return diff.Patch{}, err
	}
	args := []string{"show", "--format=", "--src-prefix=a/", "--dst-prefix=b/", hash, "--"}
	if path != "" {
		args = append(args, path)
	}
	out, err := r.read(args...)
	if err != nil {
		return diff.Patch{}, err
	}
	return diff.Parse(out)
}
