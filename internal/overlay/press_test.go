// =============================================================================
// File: internal/overlay/press_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestPress_FreshOnlyOnTheFirstButton1Event pins the latch: the first
// Button1 event is a press, the Button1 events that follow it are
// motion, and a release re-arms the next press. A wheel or a bare
// motion event counts as "button up" too, so a lost release can never
// jam the latch shut for the rest of the overlay's life.
func TestPress_FreshOnlyOnTheFirstButton1Event(t *testing.T) {
	var p Press
	if !p.Fresh(tcell.Button1) {
		t.Fatal("first Button1 event must be a fresh press")
	}
	if p.Fresh(tcell.Button1) {
		t.Fatal("a second Button1 event without a release is motion, not a press")
	}
	if p.Fresh(tcell.ButtonNone) {
		t.Fatal("a release is never a press")
	}
	if !p.Fresh(tcell.Button1) {
		t.Fatal("a press after a release must be fresh again")
	}
	if p.Fresh(tcell.WheelDown) {
		t.Fatal("a wheel event is not a press")
	}
	if !p.Fresh(tcell.Button1) {
		t.Fatal("the wheel event must have re-armed the latch")
	}
}

// TestPress_SyncSeedsFromTheHeldMask pins the opener's seam: a latch
// synced to a held Button1 answers the next Button1 event as motion,
// and one synced to no button answers it as a fresh press — without
// Sync itself counting as an event.
func TestPress_SyncSeedsFromTheHeldMask(t *testing.T) {
	var p Press
	p.Sync(tcell.Button1)
	if p.Fresh(tcell.Button1) {
		t.Fatal("held motion after a Sync(Button1) must not read as a fresh press")
	}
	p.Sync(tcell.ButtonNone)
	if !p.Fresh(tcell.Button1) {
		t.Fatal("a press after a Sync(ButtonNone) must read as fresh")
	}
}
