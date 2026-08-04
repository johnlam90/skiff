// =============================================================================
// File: internal/editor/undo_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for undo.go — the per-Tab snapshot stack, the typing/backspace/
// delete coalescing, redo invalidation, and the one-shot RevertFile.
//
// The fixture is intentionally a pure in-memory Tab built without going
// through NewTab so we don't have to touch disk. initUndo is called
// explicitly to mirror what NewTab does.

package editor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newScratchTab builds a Tab whose buffer is initialised from the given
// string, without doing any disk IO. The on-open snapshot is captured
// just like NewTab would do.
func newScratchTab(initial string) *Tab {
	t := &Tab{Buffer: NewBuffer(initial), StyleStale: true}
	t.initUndo()
	return t
}

// TestNewTabCapturesOriginal proves that calling initUndo at construction
// time stamps the on-open snapshot. RevertFile relies on this — without
// it, a freshly-opened tab would have nothing to rewind to.
func TestNewTabCapturesOriginal(t *testing.T) {
	tab := newScratchTab("hello\nworld")
	if got := tab.undoOriginal.Lines; len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("undoOriginal not captured: %v", got)
	}
	if tab.CanRevert() {
		t.Fatal("CanRevert should be false for a freshly captured original")
	}
}

// TestInsertRune_CoalescesIntoSingleStep types five characters in quick
// succession and asserts there is exactly one undo entry — the burst.
// One Undo should restore the empty buffer rather than removing chars
// one at a time.
func TestInsertRune_CoalescesIntoSingleStep(t *testing.T) {
	tab := newScratchTab("")
	for _, r := range []rune{'h', 'e', 'l', 'l', 'o'} {
		tab.InsertRune(r)
	}
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("buffer = %q, want hello", got)
	}
	if !tab.CanUndo() {
		t.Fatal("expected something to undo")
	}
	if got := len(tab.undoStack); got != 1 {
		t.Fatalf("expected 1 undo entry after a typing burst, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("after undo buffer = %q, want empty", got)
	}
}

// TestInsertRune_BreakOnCursorMove ensures a manual cursor move closes
// the current typing group: undo should now peel off only the second
// burst, leaving the first intact.
func TestInsertRune_BreakOnCursorMove(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.InsertRune('b')
	tab.MoveCursorTo(Position{Line: 0, Col: 0}, false)
	tab.InsertRune('X')
	tab.InsertRune('Y')

	if got := tab.Buffer.String(); got != "XYab" {
		t.Fatalf("buffer = %q, want XYab", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "ab" {
		t.Fatalf("after first undo = %q, want ab", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("after second undo = %q, want empty", got)
	}
}

// TestInsertRune_BreakAfterTimeout verifies a 500ms idle gap closes the
// group even with no other intervening op. Forces lastUndoAt into the
// past instead of sleeping so the suite stays fast.
func TestInsertRune_BreakAfterTimeout(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.lastUndoAt = time.Now().Add(-2 * undoCoalesceWindow)
	tab.InsertRune('b')

	if got := len(tab.undoStack); got != 2 {
		t.Fatalf("expected two undo entries after timeout gap, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "a" {
		t.Fatalf("after undo = %q, want a", got)
	}
}

// TestInsertString_AlwaysStructural confirms that explicit InsertString
// calls (paste, indentation insert, "\n" on Enter) never coalesce with
// the surrounding typing burst.
func TestInsertString_AlwaysStructural(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.InsertString("PASTE")
	tab.InsertRune('b')

	if got := tab.Buffer.String(); got != "aPASTEb" {
		t.Fatalf("buffer = %q, want aPASTEb", got)
	}
	if got := len(tab.undoStack); got != 3 {
		t.Fatalf("expected 3 distinct undo entries, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "aPASTE" {
		t.Fatalf("undo 1: got %q, want aPASTE", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "a" {
		t.Fatalf("undo 2: got %q, want a", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("undo 3: got %q, want empty", got)
	}
}

// TestBackspace_CoalescesWithItself verifies repeated backspaces inside
// the window produce a single undo step but never merge into a typing
// step on either side.
func TestBackspace_CoalescesWithItself(t *testing.T) {
	tab := newScratchTab("hello")
	tab.MoveCursorTo(Position{Line: 0, Col: 5}, false)
	tab.Backspace()
	tab.Backspace()
	tab.Backspace()
	if got := tab.Buffer.String(); got != "he" {
		t.Fatalf("after 3 backspaces = %q, want he", got)
	}
	if got := len(tab.undoStack); got != 1 {
		t.Fatalf("expected 1 entry from backspace burst, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("after undo = %q, want hello", got)
	}
}

// TestBackspace_BreaksTypingGroup makes sure switching from typing to
// backspace mid-burst opens a new group. Otherwise undoing the backspace
// would also undo the typing — which would be very confusing.
func TestBackspace_BreaksTypingGroup(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('h')
	tab.InsertRune('i')
	tab.Backspace()

	if got := tab.Buffer.String(); got != "h" {
		t.Fatalf("buffer = %q, want h", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hi" {
		t.Fatalf("after undo = %q, want hi (only the backspace), got %q", got, got)
	}
}

// TestDelete_CoalescesAndBreaksProperly mirrors the backspace check for
// the forward-delete group.
func TestDelete_CoalescesAndBreaksProperly(t *testing.T) {
	tab := newScratchTab("abcdef")
	tab.MoveCursorTo(Position{Line: 0, Col: 0}, false)
	tab.Delete()
	tab.Delete()
	if got := tab.Buffer.String(); got != "cdef" {
		t.Fatalf("after 2 deletes = %q, want cdef", got)
	}
	if got := len(tab.undoStack); got != 1 {
		t.Fatalf("expected 1 entry from delete burst, got %d", got)
	}
}

// TestDeleteSelection_IsItsOwnStep proves selection deletes always get
// their own entry — coalescing them would let an Undo recover content
// the user clearly wants gone-then-undoable.
func TestDeleteSelection_IsItsOwnStep(t *testing.T) {
	tab := newScratchTab("hello world")
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.DeleteSelection()
	if got := tab.Buffer.String(); got != "hello " {
		t.Fatalf("buffer = %q, want 'hello '", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello world" {
		t.Fatalf("after undo = %q, want 'hello world'", got)
	}
}

// TestInsertRune_OverSelectionPushesOnce verifies that typing a char
// while a selection is active pushes a single undo step (the
// DeleteSelection one) and then replaces — so undoing once gives back
// the full pre-replace state, not the empty selection.
func TestInsertRune_OverSelectionPushesOnce(t *testing.T) {
	tab := newScratchTab("hello world")
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.InsertRune('X')
	if got := tab.Buffer.String(); got != "hello X" {
		t.Fatalf("buffer = %q, want 'hello X'", got)
	}
	if got := len(tab.undoStack); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello world" {
		t.Fatalf("after undo = %q, want 'hello world'", got)
	}
}

// TestRedo_RoundTrips writes some text, undoes it, redoes it, and
// confirms the buffer comes back identical.
func TestRedo_RoundTrips(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertString("alpha")
	tab.InsertString("beta")
	if !tab.Undo() {
		t.Fatal("expected first undo to succeed")
	}
	if got := tab.Buffer.String(); got != "alpha" {
		t.Fatalf("after undo = %q, want alpha", got)
	}
	if !tab.Redo() {
		t.Fatal("expected redo to succeed")
	}
	if got := tab.Buffer.String(); got != "alphabeta" {
		t.Fatalf("after redo = %q, want alphabeta", got)
	}
}

// TestRedo_InvalidatedByNewEdit asserts that any new mutation after an
// undo wipes the redo stack — otherwise the user could stumble into a
// branched timeline they can't easily reason about.
func TestRedo_InvalidatedByNewEdit(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertString("a")
	tab.InsertString("b")
	tab.Undo()
	if !tab.CanRedo() {
		t.Fatal("expected CanRedo before new edit")
	}
	tab.InsertRune('X')
	if tab.CanRedo() {
		t.Fatal("new edit should have wiped the redo stack")
	}
}

// TestUndo_NoopOnEmptyStack returns false and leaves state untouched
// when there's nothing to undo.
func TestUndo_NoopOnEmptyStack(t *testing.T) {
	tab := newScratchTab("hi")
	if tab.Undo() {
		t.Fatal("Undo on empty stack should return false")
	}
	if got := tab.Buffer.String(); got != "hi" {
		t.Fatalf("buffer changed: %q", got)
	}
}

// TestRedo_NoopOnEmptyStack same idea for Redo.
func TestRedo_NoopOnEmptyStack(t *testing.T) {
	tab := newScratchTab("hi")
	if tab.Redo() {
		t.Fatal("Redo on empty stack should return false")
	}
}

// TestRevertFile_GoesAllTheWayBack types a long edit history then
// reverts in one step. The buffer should equal the on-open snapshot
// and a single Undo should bring back the pre-revert state.
func TestRevertFile_GoesAllTheWayBack(t *testing.T) {
	tab := newScratchTab("original")
	tab.MoveCursorTo(Position{Line: 0, Col: 8}, false)
	for _, r := range " edits" {
		tab.InsertRune(r)
	}
	tab.InsertString("\nmore")
	if got := tab.Buffer.String(); got != "original edits\nmore" {
		t.Fatalf("setup buffer = %q", got)
	}

	if !tab.RevertFile() {
		t.Fatal("expected RevertFile to report a change")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("after revert = %q, want original", got)
	}
	if tab.CanRevert() {
		t.Fatal("CanRevert should be false right after Revert")
	}

	if !tab.Undo() {
		t.Fatal("expected Undo to be able to recover from a Revert")
	}
	if got := tab.Buffer.String(); got != "original edits\nmore" {
		t.Fatalf("after undo of revert = %q, want pre-revert state", got)
	}
}

// TestRevertFile_NoopWhenAlreadyOriginal returns false and doesn't
// pollute the undo stack.
func TestRevertFile_NoopWhenAlreadyOriginal(t *testing.T) {
	tab := newScratchTab("clean")
	before := len(tab.undoStack)
	if tab.RevertFile() {
		t.Fatal("RevertFile on a clean buffer should return false")
	}
	if len(tab.undoStack) != before {
		t.Fatal("undo stack should not have grown")
	}
}

// TestUndoStack_CapsAtMaxEntries forces the stack past its FIFO cap and
// verifies it stops growing rather than running away. The cap is small
// enough (500) that we can hit it in a fast test.
func TestUndoStack_CapsAtMaxEntries(t *testing.T) {
	tab := newScratchTab("")
	// Each iteration: structural push (no coalescing).
	for i := 0; i < maxUndoEntries+50; i++ {
		tab.InsertString(".")
	}
	if got := len(tab.undoStack); got != maxUndoEntries {
		t.Fatalf("undo stack length = %d, want capped at %d", got, maxUndoEntries)
	}
}

// TestUndoStack_CapsAtMaxBytes proves depth is not the only bound. Every
// entry is a whole-buffer snapshot, so 500 entries of a big file is
// gigabytes of history behind a session that never felt heavy — the byte
// budget has to evict oldest-first long before the depth cap fires.
func TestUndoStack_CapsAtMaxBytes(t *testing.T) {
	big := strings.Repeat("x", 4<<20) // 4MiB, shared by every snapshot
	tab := &Tab{Buffer: &Buffer{Lines: []string{big, "v"}}}
	tab.initUndo()

	const pushes = 12 // 48MiB of counted history against a 32MiB budget
	for i := range pushes {
		tab.Buffer.Lines[1] = "v" + strconv.Itoa(i)
		tab.pushUndo(undoGroupStructural)
	}

	if len(tab.undoStack) >= pushes {
		t.Fatalf("byte budget never fired: %d entries held", len(tab.undoStack))
	}
	if len(tab.undoStack) < minUndoEntries {
		t.Fatalf("trimmed below the floor: %d entries", len(tab.undoStack))
	}
	if tab.undoBytes > maxUndoBytes {
		t.Fatalf("stack holds %d bytes, over the %d budget", tab.undoBytes, maxUndoBytes)
	}
	// Eviction is oldest-first, so the most recent push must survive.
	newest := tab.undoStack[len(tab.undoStack)-1]
	if want := "v" + strconv.Itoa(pushes-1); newest.Lines[1] != want {
		t.Fatalf("newest entry is %q, want %q", newest.Lines[1], want)
	}
	// And the running total has to agree with what is actually held,
	// otherwise the budget drifts and stops bounding anything.
	sum := 0
	for _, s := range tab.undoStack {
		sum += s.size()
	}
	if sum != tab.undoBytes {
		t.Fatalf("undoBytes = %d, stack actually holds %d", tab.undoBytes, sum)
	}
}

// TestUndoStack_ByteBudgetKeepsFloor pins the escape hatch: when single
// snapshots are each bigger than a fair share of the budget, the stack
// keeps minUndoEntries anyway. Undoing the paste that just went wrong has
// to work even when that one snapshot blows the budget on its own.
func TestUndoStack_ByteBudgetKeepsFloor(t *testing.T) {
	huge := strings.Repeat("y", 12<<20) // 12MiB each — 3 already overflow
	tab := &Tab{Buffer: &Buffer{Lines: []string{huge}}}
	tab.initUndo()
	for range 6 {
		tab.pushUndo(undoGroupStructural)
	}
	if got := len(tab.undoStack); got != minUndoEntries {
		t.Fatalf("stack holds %d entries, want the %d floor", got, minUndoEntries)
	}
}

// TestUndo_DirtyMeasuredAgainstDisk is the regression for deriving Dirty
// from CanRevert. CanRevert compares against the ON-OPEN snapshot, so
// after a save an undo would report a clean buffer while disk held the
// newer text — and the tab would then close with no prompt, dropping the
// edit. Dirty has to track the last write, not the open.
func TestUndo_DirtyMeasuredAgainstDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}

	tab.MoveCursorTo(Position{Line: 0, Col: 8}, false)
	tab.InsertString(" edited")
	if !tab.Dirty {
		t.Fatal("an edit must mark the tab dirty")
	}
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if tab.Dirty {
		t.Fatal("save must clear dirty")
	}

	// Undo now walks BACK past the saved state: the buffer says
	// "original" while disk says "original edited".
	if !tab.Undo() {
		t.Fatal("expected an undo step")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("after undo buffer = %q", got)
	}
	if !tab.Dirty {
		t.Fatal("undoing past a save leaves the buffer different from disk — must be dirty")
	}

	// Redo returns to the saved bytes, so the tab is clean again.
	if !tab.Redo() {
		t.Fatal("expected a redo step")
	}
	if tab.Dirty {
		t.Fatal("redo back to the saved text must clear dirty")
	}
}

// TestUndo_DirtyClearsBackAtOpenedState covers the unsaved half: undo all
// the way to what was read from disk and the tab is clean, so closing it
// doesn't nag about changes the user already took back.
func TestUndo_DirtyClearsBackAtOpenedState(t *testing.T) {
	tab := newScratchTab("original")
	tab.MoveCursorTo(Position{Line: 0, Col: 8}, false)
	tab.InsertString(" edited")
	if !tab.Dirty {
		t.Fatal("an edit must mark the tab dirty")
	}
	if !tab.Undo() {
		t.Fatal("expected an undo step")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("after undo buffer = %q", got)
	}
	if tab.Dirty {
		t.Fatal("back at the opened text the tab must be clean")
	}
}

// TestRevertFile_DirtyMeasuredAgainstDisk mirrors the Undo case for the
// revert hatch: rewinding to the on-open text after a save leaves the
// buffer different from disk, which is exactly when the user needs the
// unsaved-changes prompt.
func TestRevertFile_DirtyMeasuredAgainstDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.MoveCursorTo(Position{Line: 0, Col: 8}, false)
	tab.InsertString(" edited")
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !tab.RevertFile() {
		t.Fatal("expected RevertFile to rewind")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("after revert buffer = %q", got)
	}
	if !tab.Dirty {
		t.Fatal("reverting past a save must leave the tab dirty")
	}
}

// TestSave_BreaksTypingGroup confirms a save closes the current
// coalescing window: the next typed char should produce a fresh undo
// entry rather than merging with what came before.
func TestSave_BreaksTypingGroup(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	// Manually trigger the break behavior — we can't actually call
	// Save() without a path, but it just calls breakUndoGroup at the
	// end which is what we're testing the effect of.
	tab.breakUndoGroup()
	tab.InsertRune('b')

	if got := len(tab.undoStack); got != 2 {
		t.Fatalf("expected 2 entries after group break, got %d", got)
	}
}

// TestReload_ResetsUndoBaseline verifies that Reload picks up the new
// on-disk content as the new "original" — Revert after a Reload should
// rewind to that, not to the pre-reload state.
func TestReload_ResetsUndoBaseline(t *testing.T) {
	tab := newScratchTab("v1")
	tab.InsertString("xyz")
	if !tab.CanRevert() {
		t.Fatal("expected revert available before reload")
	}

	// Simulate Reload's effect without actually doing disk IO.
	tab.Buffer = NewBuffer("disk-version")
	tab.Cursor = Position{}
	tab.Anchor = Position{}
	tab.initUndo()

	if tab.CanUndo() {
		t.Fatal("undo stack should be empty after baseline reset")
	}
	if tab.CanRevert() {
		t.Fatal("freshly reset baseline should have nothing to revert")
	}

	tab.InsertString("more")
	if !tab.RevertFile() {
		t.Fatal("expected revert to fire after a fresh edit")
	}
	if got := tab.Buffer.String(); got != "disk-version" {
		t.Fatalf("after revert = %q, want disk-version", got)
	}
}

// TestUndo_RestoresCursorAndAnchor checks that the snapshot mechanism
// also restores selection state — a user undoing a change shouldn't
// lose their cursor position or active selection.
func TestUndo_RestoresCursorAndAnchor(t *testing.T) {
	tab := newScratchTab("hello")
	tab.MoveCursorTo(Position{Line: 0, Col: 2}, false)
	tab.Anchor = Position{Line: 0, Col: 5}
	tab.InsertRune('Z') // replaces "llo" with "Z" → "heZ"

	if got := tab.Buffer.String(); got != "heZ" {
		t.Fatalf("buffer = %q, want heZ", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("after undo = %q, want hello", got)
	}
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Errorf("cursor after undo = %+v, want {0,2}", tab.Cursor)
	}
	if tab.Anchor != (Position{Line: 0, Col: 5}) {
		t.Errorf("anchor after undo = %+v, want {0,5}", tab.Anchor)
	}
}

// TestApplySnapshot_DoesNotShareLineStorage protects against an aliasing
// bug: if the live buffer were to share the snapshot's slice header,
// the next edit would silently corrupt history. We mutate a restored
// line and verify the original snapshot still has the old contents.
func TestApplySnapshot_DoesNotShareLineStorage(t *testing.T) {
	tab := newScratchTab("alpha\nbeta")
	snap := tab.captureSnapshot()
	tab.Buffer.Lines[0] = "MUTATED"
	if snap.Lines[0] != "alpha" {
		t.Fatalf("snapshot was clobbered: %v", snap.Lines)
	}
	tab.applySnapshot(snap)
	tab.Buffer.Lines[0] = "MUTATED-AGAIN"
	if snap.Lines[0] != "alpha" {
		t.Fatalf("apply leaked storage: %v", snap.Lines)
	}
}

// TestCanRevert_DetectsLineCountAndContent covers both branches of the
// CanRevert comparison — adding a line, and editing one in place.
func TestCanRevert_DetectsLineCountAndContent(t *testing.T) {
	tab := newScratchTab("one\ntwo")
	if tab.CanRevert() {
		t.Fatal("brand new tab should not report revert needed")
	}

	// Same line count, different content.
	tab.Buffer.Lines[0] = "ONE"
	if !tab.CanRevert() {
		t.Fatal("content change should trip CanRevert")
	}

	// Restore content, change line count.
	tab.Buffer.Lines[0] = "one"
	tab.Buffer.Lines = append(tab.Buffer.Lines, "extra")
	if !tab.CanRevert() {
		t.Fatal("line count change should trip CanRevert")
	}
}

// TestEmptyBuffer_HandledGracefully — the helpers should not panic on a
// zero-state Tab without a buffer (e.g. one constructed without
// NewBuffer for some reason). Captures + applies still produce
// sensible cursor/anchor copies.
func TestEmptyBuffer_HandledGracefully(t *testing.T) {
	tab := &Tab{Cursor: Position{Line: 1, Col: 2}, Anchor: Position{Line: 3, Col: 4}}
	snap := tab.captureSnapshot()
	if snap.Cursor != tab.Cursor || snap.Anchor != tab.Anchor {
		t.Fatal("captureSnapshot dropped cursor/anchor on a buffer-less tab")
	}
	tab.applySnapshot(snap)
	if tab.Buffer == nil {
		t.Fatal("applySnapshot should have produced a buffer")
	}
}

// TestBufferContentsAfterMixedHistory drives a longer realistic
// workflow — type, paste, type, save-marker, type, backspace bursts —
// and walks Undo all the way back to confirm the timeline is sane at
// every step. The biggest concern this catches is silent state drift:
// not what one undo does in isolation, but whether N undos compose to
// "exactly the on-open buffer."
func TestBufferContentsAfterMixedHistory(t *testing.T) {
	tab := newScratchTab("seed")
	tab.MoveCursorTo(Position{Line: 0, Col: 4}, false)

	tab.InsertRune(' ')         // typing-1
	tab.InsertRune('h')         // typing-1
	tab.InsertRune('i')         // typing-1
	tab.InsertString("[paste]") // structural
	tab.MoveCursorTo(Position{Line: 0, Col: len(tab.Buffer.LineRunes(0))}, false)
	tab.InsertRune('!') // typing-2
	tab.Backspace()     // backspace-1

	want := "seed hi[paste]"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("after edits = %q, want %q", got, want)
	}

	// Walk every Undo and stop on no-op.
	steps := 0
	for tab.Undo() {
		steps++
		if steps > 50 {
			t.Fatal("undo did not terminate")
		}
	}
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after fully unwinding (%d steps) = %q, want seed", steps, got)
	}
	// Trailing newlines / whitespace shouldn't sneak in via snapshot copies.
	if strings.TrimSpace(tab.Buffer.String()) != "seed" {
		t.Fatal("trailing junk crept into restored buffer")
	}
}
