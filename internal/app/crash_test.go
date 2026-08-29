// =============================================================================
// File: internal/app/crash_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-28
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the not-a-clean-quit exit paths in crash.go: signal-driven
// quit, the goroutine panic guard, and the crash log it writes. os.Exit
// can't run under `go test`, so the tests exercise handleGoroutinePanic —
// the factored-out body safeGo calls — rather than a real panicking
// goroutine.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/version"
)

// TestSignalRequestsQuit pins the signal path's contract with the event
// loop: a quitRequestEvent delivered through handleEvent sets a.quit
// directly — no dirty-tab modal in between, because a SIGTERM must never
// hang the process on a prompt nobody is there to answer. Real signals
// are not sent in tests; the event is the seam.
func TestSignalRequestsQuit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleEvent(&quitRequestEvent{when: time.Now()})
	if !a.quit {
		t.Fatal("quitRequestEvent should set a.quit")
	}
}

// TestSafeGo_RecoversAndWritesCrashLog pins what a crash leaves behind
// for the bug report: handleGoroutinePanic — the recover body safeGo
// runs, factored out because os.Exit can't run under go test — writes a
// log under the XDG state dir that names the version, the goroutine,
// and the recovered value.
func TestSafeGo_RecoversAndWritesCrashLog(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := handleGoroutinePanic("boom-worker", "synthetic failure")
	if path == "" {
		t.Fatal("expected a crash log path, got \"\"")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	body := string(data)
	for _, want := range []string{version.Version, "boom-worker", "synthetic failure"} {
		if !strings.Contains(body, want) {
			t.Fatalf("crash log should contain %q, got:\n%s", want, body)
		}
	}
}

// TestSafeGo_RunsFnNormally pins the guard's zero cost on the happy
// path: a fn that doesn't panic runs to completion and no crash log
// appears — wrapping a goroutine site must not change its behavior.
func TestSafeGo_RunsFnNormally(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	a := newTestApp(t, t.TempDir())

	done := make(chan struct{})
	a.safeGo("no-panic", func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("safeGo should run fn")
	}

	entries, err := os.ReadDir(filepath.Join(stateHome, "skiff"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read state dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "crash-") {
			t.Fatalf("no crash log expected, found %s", e.Name())
		}
	}
}
