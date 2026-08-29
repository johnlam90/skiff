// =============================================================================
// File: internal/app/crash.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-28
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// crash.go covers every exit path that is not a clean menu quit:
// termination signals delivered from outside, and (in later pieces of
// this file) panics in background goroutines. Skiff's habitat is
// SSH-into-tmux, where a process dying with raw mode and mouse tracking
// still on wrecks the pane — so each of these paths must funnel back
// through the normal teardown instead of letting the runtime kill the
// process mid-escape-sequence.

package app

import (
	"os"
	"os/signal"
	"syscall"
	"time"
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
	go func() {
		for range ch {
			_ = scr.PostEvent(&quitRequestEvent{when: time.Now()})
		}
	}()
	return func() {
		// After Stop returns the signal package guarantees no more
		// sends on ch, so closing it is safe and lets the forwarder
		// goroutine exit instead of leaking until process death.
		signal.Stop(ch)
		close(ch)
	}
}
