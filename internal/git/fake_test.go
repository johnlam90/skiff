// =============================================================================
// File: internal/git/fake_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFake_ScriptedLookupByArgv pins the Fake's addressing scheme: a
// response is keyed by the joined argument list, so scripting the wrong
// spelling of a command must not accidentally answer a different one.
// Both halves of the Runner interface resolve through the same table —
// a write scripted once answers whether the caller reached for Output
// or Combined.
func TestFake_ScriptedLookupByArgv(t *testing.T) {
	f := &Fake{}
	f.Script("status --porcelain -z", "M  a.go\x00", nil)

	out, err := f.Output("/repo", time.Second, "status", "--porcelain", "-z")
	if err != nil || string(out) != "M  a.go\x00" {
		t.Fatalf("Output = %q, %v; want the scripted bytes", out, err)
	}
	out, err = f.Combined("/repo", time.Minute, "status", "--porcelain", "-z")
	if err != nil || string(out) != "M  a.go\x00" {
		t.Fatalf("Combined = %q, %v; want the scripted bytes", out, err)
	}
	// A prefix of the scripted argv is a different command entirely.
	if _, err := f.Output("/repo", time.Second, "status", "--porcelain"); err == nil {
		t.Fatal("a shorter argv must not match a longer script")
	}
}

// TestFake_UnscriptedCommandFailsLoudly pins the deliberate choice that
// an unscripted command errors instead of returning empty output: a
// silent "" is indistinguishable from a clean repo, and a test built on
// that would pass for the wrong reason. The error must name the command
// so the failure points at the missing script.
func TestFake_UnscriptedCommandFailsLoudly(t *testing.T) {
	f := &Fake{}
	out, err := f.Output("/repo", time.Second, "rev-parse", "--show-toplevel")
	if err == nil {
		t.Fatal("an unscripted command must fail")
	}
	if len(out) != 0 {
		t.Fatalf("a failed lookup must return no output, got %q", out)
	}
	if !strings.Contains(err.Error(), "rev-parse --show-toplevel") {
		t.Fatalf("error should name the command, got %v", err)
	}
}

// TestFake_ScriptedErrorReachesTheCaller pins the reason the Fake
// exists: failures real git can't be asked for on demand (a wedged
// remote, a rejected push) are scripted, and both the output and the
// error travel together — explainGit-style callers need git's words
// alongside the non-zero exit.
func TestFake_ScriptedErrorReachesTheCaller(t *testing.T) {
	boom := errors.New("exit status 1")
	f := &Fake{}
	f.Script("push", "! [rejected] main -> main\n", boom)

	out, err := OpenWith("/repo", f).RunSequence([][]string{{"push"}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the scripted error", err)
	}
	if !strings.Contains(out, "rejected") {
		t.Fatalf("scripted output must accompany the error, got %q", out)
	}
}

// TestFake_ScriptOverwritesAndRecordsEveryCall pins CallCount as an
// exact tally — the caching and coalescing tests assert on it, so an
// off-by-one here would silently validate a refresh storm. Re-scripting
// a command replaces the previous answer rather than stacking.
func TestFake_ScriptOverwritesAndRecordsEveryCall(t *testing.T) {
	f := &Fake{}
	f.Script("branch --show-current", "main\n", nil)
	f.Script("branch --show-current", "feature\n", nil)

	for range 3 {
		out, err := f.Output("/repo", time.Second, "branch", "--show-current")
		if err != nil || strings.TrimSpace(string(out)) != "feature" {
			t.Fatalf("re-scripting must replace: got %q, %v", out, err)
		}
	}
	_, _ = f.Output("/repo", time.Second, "unscripted")
	if got := f.CallCount(); got != 4 {
		t.Fatalf("CallCount = %d, want 4 (failed lookups count too)", got)
	}
	if len(f.Calls) != 4 || f.Calls[3][0] != "unscripted" {
		t.Fatalf("Calls must record every invocation in order, got %v", f.Calls)
	}
}

// TestFake_RecordedArgsAreCopied pins a subtle trap: Runner callers
// build argv with append and reuse the backing array, so a Fake that
// stored the caller's slice would rewrite its own history. The recorded
// call must survive the caller mutating the slice afterwards.
func TestFake_RecordedArgsAreCopied(t *testing.T) {
	f := &Fake{}
	args := make([]string, 0, 4)
	args = append(args, "diff", "--name-status")
	_, _ = f.Output("/repo", time.Second, args...)
	args[1] = "--stat"

	if f.Calls[0][1] != "--name-status" {
		t.Fatalf("recorded args must be a copy, got %v", f.Calls[0])
	}
}

// TestFake_ConcurrentCallersAreSafe pins the concurrency claim in the
// Fake's doc comment. Git commands run from background goroutines, so a
// test double that raced would turn a real bug into a flaky suite. Run
// under -race, this fails immediately if the mutex ever stops covering
// both the Calls append and the Responses read.
func TestFake_ConcurrentCallersAreSafe(t *testing.T) {
	f := &Fake{}
	f.Script("status", "clean\n", nil)

	const workers, each = 8, 25
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		go func(w int) {
			defer wg.Done()
			for range each {
				_, _ = f.Output("/repo", time.Second, "status")
				_, _ = f.Combined("/repo", time.Minute, "commit")
				f.Script("scripted-by-worker", "ok", nil)
				_ = f.CallCount()
				_ = w
			}
		}(w)
	}
	wg.Wait()

	if got := f.CallCount(); got != workers*each*2 {
		t.Fatalf("CallCount = %d, want %d — a lost append means a lost lock",
			got, workers*each*2)
	}
}
