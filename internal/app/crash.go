// =============================================================================
// File: internal/app/crash.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-28
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// crash.go covers every exit path that is not a clean menu quit:
// termination signals delivered from outside, and panics — in a
// background goroutine or in the event loop itself. Skiff's habitat is
// SSH-into-tmux, where a process dying with raw mode and mouse tracking
// still on wrecks the pane — so each of these paths must funnel back
// through the normal teardown instead of letting the runtime kill the
// process mid-escape-sequence. A panic additionally writes a crash log
// under the state dir, because a stack trace scrolled into a wrecked
// screen leaves nothing to paste into a bug report.

package app

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/johnlam90/skiff/internal/version"
)

// quitRequestEvent asks the main loop to exit. Posted by the signal
// forwarder; handleEvent reacts by setting a.quit directly — never by
// opening the dirty-tab modal, because a SIGTERM has nobody at the
// keyboard to answer it. Modeled on flashExpiryEvent: reaching the event
// loop is the point.
type quitRequestEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *quitRequestEvent) When() time.Time { return e.when }

// watchSignals forwards termination signals into the event loop as
// quitRequestEvents and returns the stop function Run defers. SIGTERM and
// SIGHUP are what `tmux kill-pane` / `pkill skiff` / a dropped SSH
// connection actually deliver. SIGINT is included for `kill -INT` from
// outside only: tcell puts the terminal in raw mode, so Ctrl-C arrives as
// a key event, never as a signal. PostEvent is tcell's documented
// thread-safe wake-up for a loop blocked in PollEvent — the same
// mechanism every background goroutine here already uses.
func (a *App) watchSignals() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP, os.Interrupt)
	scr := a.screen
	a.safeGo("signal-watch", func() {
		for range ch {
			_ = scr.PostEvent(&quitRequestEvent{when: time.Now()})
		}
	})
	return func() {
		// After Stop returns the signal package guarantees no more
		// sends on ch, so closing it is safe and lets the forwarder
		// goroutine exit instead of leaking until process death.
		signal.Stop(ch)
		close(ch)
	}
}

// crashLogPath resolves where a crash report lands:
// <state>/skiff/crash-<unix-timestamp>.log, honouring $XDG_STATE_HOME so
// tests (and unusual setups) can redirect it. Mirrors the XDG logic of
// session's stateDir, which is unexported there on purpose — ten
// duplicated lines beat exporting a path helper between packages.
func crashLogPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skiff",
		fmt.Sprintf("crash-%d.log", time.Now().Unix())), nil
}

// writeCrashLog persists what a bug report needs — version, terminal,
// platform, which task died, the recovered value, and the stack — and
// returns the path it wrote ("" on any failure). Best-effort by design:
// it runs inside a recover, so it must never panic, and a failure to
// write must not stop the terminal restore that follows. Owner-only
// permissions, matching every other per-user state file.
func writeCrashLog(name string, r any, stack []byte) string {
	path, err := crashLogPath()
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return ""
	}
	body := fmt.Sprintf("skiff %s\nTERM=%s\nGOOS/GOARCH=%s/%s\ngoroutine=%s\npanic: %v\n\n%s",
		version.Version, os.Getenv("TERM"), runtime.GOOS, runtime.GOARCH,
		name, r, stack)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		return ""
	}
	return path
}

// handleGoroutinePanic is the log-writing half of the crash guard,
// shared by runGuarded and Run's own recover. Factored out of safeGo so
// tests can drive it directly — the real guard ends in os.Exit, which
// no test can survive. Called from inside the deferred recover, so
// debug.Stack still sees the panicking frames.
func handleGoroutinePanic(name string, r any) (logPath string) {
	return writeCrashLog(name, r, debug.Stack())
}

// safeGo launches fn on a goroutine with the crash guard installed.
// Every `go` statement in internal/app goes through here — reviewers
// should reject a bare one: a panic in an unguarded goroutine kills the
// process without unwinding main's defers, leaving the terminal in raw
// mode with mouse tracking on.
func (a *App) safeGo(name string, fn func()) {
	go a.runGuarded(name, fn)
}

// runGuarded is safeGo's goroutine body: run fn, and on panic restore
// the terminal, write the crash log, tell the user where it is, and
// exit non-zero.
//
// Calling Fini from the panicking goroutine is safe on the vendored
// tcell (verified against v2.13.9): Fini is finiOnce.Do(finish) — a
// sync.Once, so it cannot double-run against Close — and finish closes
// the quit channel, which makes the main loop's blocked PollEvent
// return nil, a value Run already treats as quit. If a tcell upgrade
// ever breaks that, switch to posting a fatal event and doing the Fini
// and stderr print from Run's exit path.
func (a *App) runGuarded(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if a.screen != nil {
			a.screen.Fini()
		}
		path := handleGoroutinePanic(name, r)
		if path == "" {
			path = "(crash log could not be written)"
		}
		fmt.Fprintf(os.Stderr, "skiff: background task %q crashed — log at %s\n", name, path)
		os.Exit(2)
	}()
	fn()
}
