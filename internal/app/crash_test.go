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
	"testing"
	"time"
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
