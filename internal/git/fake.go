// =============================================================================
// File: internal/git/fake.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Fake is the in-memory Runner adapter: commands are scripted by their
// argument list, calls are recorded, and unscripted commands fail —
// exactly what tests need for states real git can't produce on demand
// (a hanging remote, a scripted failure) or shouldn't pay for (process
// spawns in a hot loop). Safe for concurrent use; git commands run from
// background goroutines.
type Fake struct {
	mu sync.Mutex
	// Responses maps strings.Join(args, " ") to a scripted result.
	Responses map[string]FakeResponse
	// calls records every invocation's args in order. Read it through
	// Calls: the verbs run on background goroutines, so a bare field
	// read from a test would race the append.
	calls [][]string
}

// FakeResponse scripts one command's result.
type FakeResponse struct {
	Out []byte
	Err error
}

// Script registers a response for the exact argument list.
func (f *Fake) Script(args string, out string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]FakeResponse{}
	}
	f.Responses[args] = FakeResponse{Out: []byte(out), Err: err}
}

// lookup records the call and resolves its scripted response.
func (f *Fake) lookup(args []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	key := strings.Join(args, " ")
	if resp, ok := f.Responses[key]; ok {
		return resp.Out, resp.Err
	}
	return nil, fmt.Errorf("git fake: no script for %q", key)
}

// Output implements Runner.
func (f *Fake) Output(root string, timeout time.Duration, args ...string) ([]byte, error) {
	return f.lookup(args)
}

// Combined implements Runner. The write deadline is recorded by the
// caller's choice, not honored here — the Fake answers instantly, and
// a test that wants a slow write scripts it at the Repo level.
func (f *Fake) Combined(root string, timeout time.Duration, args ...string) ([]byte, error) {
	return f.lookup(args)
}

// CallCount returns how many commands ran — for asserting coalescing
// and caching behavior.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Calls returns a copy of every recorded argv in call order — for
// asserting that a verb chose the argv it should have, and in which
// order its probes ran relative to the caller returning.
func (f *Fake) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// Called reports whether a command with exactly this argv (joined by
// single spaces, the same key Script uses) ran.
func (f *Fake) Called(args string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Join(c, " ") == args {
			return true
		}
	}
	return false
}
