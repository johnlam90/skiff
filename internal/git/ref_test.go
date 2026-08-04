// =============================================================================
// File: internal/git/ref_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"testing"
)

// TestSafeRef_RejectsOptionLookalikes is the regression test for the
// flag-injection hole: a cloned repository can ship a branch named
// `--output=/tmp/x`, and placing that name after `git diff
// --name-status` turned a read into an arbitrary file write. Anything
// that git's option parser would claim must never reach the argv.
func TestSafeRef_RejectsOptionLookalikes(t *testing.T) {
	for _, bad := range []string{
		"--output=/tmp/x",
		"-o/tmp/x",
		"--upload-pack=touch /tmp/pwn",
		"-",
		"",
		"main\x00--output=/tmp/x",
	} {
		got, err := SafeRef(bad)
		if err == nil {
			t.Fatalf("SafeRef(%q) accepted the ref", bad)
		}
		if !errors.Is(err, ErrUnsafeRef) {
			t.Fatalf("SafeRef(%q) err = %v, want ErrUnsafeRef", bad, err)
		}
		if got != "" {
			t.Fatalf("SafeRef(%q) returned %q alongside its error", bad, got)
		}
	}
}

// TestSafeRef_PassesRealRefsThrough pins the other half: SafeRef is not
// ref-name validation. Being stricter than git would reject refs the
// user legitimately has, so every spelling git itself accepts — remote
// tracking names, tags, SHAs, revision syntax, dashes anywhere but the
// front — must survive unchanged.
func TestSafeRef_PassesRealRefsThrough(t *testing.T) {
	for _, ok := range []string{
		"main",
		"origin/feature-branch",
		"release/v1.2.3",
		"HEAD~1",
		"@{upstream}",
		"9f2c1ab",
		"feature--with--dashes",
	} {
		got, err := SafeRef(ok)
		if err != nil {
			t.Fatalf("SafeRef(%q) rejected a legitimate ref: %v", ok, err)
		}
		if got != ok {
			t.Fatalf("SafeRef(%q) = %q, want it returned verbatim", ok, got)
		}
	}
}
