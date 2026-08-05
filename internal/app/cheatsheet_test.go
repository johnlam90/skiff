// =============================================================================
// File: internal/app/cheatsheet_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for cheatsheet.go — the Esc ? shortcut overlay. The interesting
// property is not that the overlay opens; it is that its contents are
// derived from leaderBindings() and therefore cannot drift away from
// what the editor actually dispatches. Most of what follows asserts
// against that table rather than against literal expected text.

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/overlay"
)

// bindingRowKey pulls the trigger rune out of a generated cheat-sheet
// row and reports false for anything else. The shape test is strict on
// purpose: it is what lets a test count the rows, so an invented entry
// fails as loudly as a missing one.
func bindingRowKey(line string) (rune, bool) {
	rs := []rune(line)
	if len(rs) < 11 || string(rs[:6]) != "  Esc " || string(rs[7:10]) != "   " {
		return 0, false
	}
	return rs[6], true
}

// TestCheatSheetLines_ListsEveryLeaderBinding is the anti-drift guard
// the overlay exists for. It asserts in both directions against the live
// table: every binding in leaderBindings() gets exactly one row with its
// own description, and the sheet contains no binding row that isn't in
// the table. A hand-maintained cheat sheet passes the first half for a
// while and the second half never — which is why the content is
// generated and this test compares to the table, not to expected text.
func TestCheatSheetLines_ListsEveryLeaderBinding(t *testing.T) {
	lines := cheatSheetLines()

	got := make(map[rune]string)
	rows := 0
	for _, l := range lines {
		key, ok := bindingRowKey(l)
		if !ok {
			continue
		}
		rows++
		if prev, dup := got[key]; dup {
			t.Errorf("Esc %c listed twice (%q and %q)", key, prev, l)
		}
		got[key] = strings.TrimSpace(string([]rune(l)[10:]))
	}

	for _, b := range leaderBindings() {
		desc, ok := got[b.key]
		if !ok {
			t.Errorf("Esc %c (%s) is bound but missing from the cheat sheet", b.key, b.desc)
			continue
		}
		if desc != b.desc {
			t.Errorf("Esc %c described as %q, want %q", b.key, desc, b.desc)
		}
	}
	if rows != len(leaderBindings()) {
		t.Errorf("cheat sheet has %d binding rows, table has %d — an entry was invented or dropped",
			rows, len(leaderBindings()))
	}
}

// TestCheatSheetLines_GroupsEveryBindingUnderItsHeading pins the second
// half of the derivation: the rows are not one flat dump, and each row
// sits under the heading its binding declares. Ordering matters because
// the headings mirror the ≡ menu's groups — a user who knows Git lives
// under "Git" should find Esc g there.
func TestCheatSheetLines_GroupsEveryBindingUnderItsHeading(t *testing.T) {
	headings := make(map[string]bool)
	for _, g := range leaderDisplayGroups() {
		headings[g.title] = true
	}

	current := ""
	for _, l := range cheatSheetLines() {
		if headings[l] {
			current = l
			continue
		}
		key, ok := bindingRowKey(l)
		if !ok {
			continue
		}
		if current == "" {
			t.Fatalf("Esc %c is listed before any heading", key)
		}
		for _, b := range leaderBindings() {
			if b.key == key && b.group != current {
				t.Errorf("Esc %c filed under %q, want %q", key, current, b.group)
			}
		}
	}
}

// TestGroupLeaderBindings_DropsNothing pins the guarantee that makes the
// generated sheet trustworthy: no binding can vanish through grouping,
// even one tagged with a heading nobody registered in leaderGroupOrder.
// A typo'd group name must produce a visible extra heading, never a
// silently missing shortcut.
func TestGroupLeaderBindings_DropsNothing(t *testing.T) {
	in := []leaderBinding{
		{'a', (*App).menuSave, "alpha", "Edit"},
		{'b', (*App).menuUndo, "bravo", "Typo Group"},
		{'c', (*App).menuRedo, "charlie", "File"},
	}
	groups := groupLeaderBindings(in)

	total := 0
	for _, g := range groups {
		if g.title == "" {
			t.Error("a group was emitted with no title")
		}
		if len(g.bindings) == 0 {
			t.Errorf("group %q is empty — it should have been filtered out", g.title)
		}
		total += len(g.bindings)
	}
	if total != len(in) {
		t.Fatalf("grouping kept %d of %d bindings", total, len(in))
	}
	// The known headings keep their declared order, and the unknown one
	// is appended rather than dropped.
	want := []string{"File", "Edit", "Typo Group"}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups, want %v", len(groups), want)
	}
	for i, title := range want {
		if groups[i].title != title {
			t.Errorf("group %d = %q, want %q", i, groups[i].title, title)
		}
	}
}

// TestGroupLeaderBindings_EveryLiveBindingHasKnownGroup keeps the real
// table inside the registered headings. The fallback above means a typo
// is survivable, not that it is fine — an "Editt" heading with one row
// in it is a bug the moment someone reads the overlay.
func TestGroupLeaderBindings_EveryLiveBindingHasKnownGroup(t *testing.T) {
	known := make(map[string]bool)
	for _, title := range leaderGroupOrder() {
		known[title] = true
	}
	for _, b := range leaderBindings() {
		if !known[b.group] {
			t.Errorf("Esc %c declares group %q, which leaderGroupOrder does not list", b.key, b.group)
		}
	}
}

// TestCheatSheetLines_ExplainTheMenu covers the half that cannot be
// generated: the ≡ menu, its type-to-filter, and the warning that tmux
// and macOS Terminal eat right-click. That last line is the whole reason
// the section is hand-written — no amount of key-pressing teaches a user
// that their right-click never arrived.
func TestCheatSheetLines_ExplainTheMenu(t *testing.T) {
	body := strings.Join(cheatSheetLines(), "\n")
	for _, want := range []string{"The ≡ menu", "Esc twice", "filter", "tmux", "Right-click"} {
		if !strings.Contains(body, want) {
			t.Errorf("cheat sheet never mentions %q", want)
		}
	}
}

// TestCheatSheetLines_FitNarrowTerminals pins the column budget. Info
// truncates rather than wraps, and the overlay's whole job is to be
// readable in the cramped tmux pane where someone forgot a binding, so
// a line that only fits on a wide screen is a broken line.
func TestCheatSheetLines_FitNarrowTerminals(t *testing.T) {
	for _, l := range cheatSheetLines() {
		if n := len([]rune(l)); n > cheatSheetWidth {
			t.Errorf("line %q is %d cells, budget is %d", l, n, cheatSheetWidth)
		}
	}
}

// TestCheatSheetLines_NoDiffMarkers guards a coupling that is easy to
// trip over: overlay.Info styles every body line with DiffLineStyle, so
// a line starting with + or - or @@ would render as green, red, or bold
// accent for no reason. Keep the sheet out of that vocabulary.
func TestCheatSheetLines_NoDiffMarkers(t *testing.T) {
	for _, l := range cheatSheetLines() {
		for _, marker := range []string{"+", "-", "@@"} {
			if strings.HasPrefix(l, marker) {
				t.Errorf("line %q starts with %q — Info would colour it as a diff line", l, marker)
			}
		}
	}
}

// TestMenuKeyboardShortcuts_OpensScrollableOverlay drives the feature end
// to end on a simulation screen: the reference paints its title and a
// generated binding row, scrolling reaches the ≡ section below the fold,
// and Esc puts it away. The scroll half matters — the sheet is longer
// than a 24-row terminal by design, so a non-scrollable surface would
// hide the tmux warning forever.
func TestMenuKeyboardShortcuts_OpensScrollableOverlay(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, 80, 24)

	a.menuKeyboardShortcuts()
	if !a.anyModalOpen() {
		t.Fatal("the shortcuts row must open an overlay")
	}
	info, ok := a.overlays.Top().(*overlay.Info)
	if !ok {
		t.Fatalf("top overlay is %T, want *overlay.Info", a.overlays.Top())
	}

	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, "Keyboard shortcuts") {
		t.Error("the overlay never painted its title")
	}
	save := fmt.Sprintf("Esc %c   %s", 's', "save")
	if !screenHasText(t, a, save) {
		t.Errorf("the overlay never painted %q", save)
	}

	// Page to the bottom and confirm the hand-written section is
	// reachable rather than clipped off the end.
	info.ScrollBy(len(cheatSheetLines()))
	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, "Esc again closes the menu.") {
		t.Error("scrolling never reveals the ≡ menu section")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.anyModalOpen() {
		t.Error("Esc must dismiss the reference")
	}
}

// TestMenuKeyboardShortcuts_FitsNarrowFrame is the companion to the
// column budget. overlay.Info's natural frame is wider than skiff's own
// minWidth, so without a clamp the box's right-hand border and every
// line's tail simply fall off the terminal — in the narrow pane where
// someone is most likely to be looking a shortcut up. Both corners of
// the frame must land on screen.
func TestMenuKeyboardShortcuts_FitsNarrowFrame(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	resizeTestApp(t, a, minWidth, minHeight)

	a.menuKeyboardShortcuts()
	a.draw()
	a.screen.Show()

	scr := a.screen.(tcell.SimulationScreen)
	var opened, closed bool
	for y := range minHeight {
		row := screenLine(scr, y)
		opened = opened || strings.ContainsRune(row, '┌')
		closed = closed || strings.ContainsRune(row, '┐')
	}
	if !opened {
		t.Error("the frame's top-left corner never painted")
	}
	if !closed {
		t.Errorf("the frame's top-right corner is off-screen at %d columns", minWidth)
	}
}

// TestHandleKey_LeaderQuestionMarkOpensReference pins the Esc ? gesture
// itself: it is the binding a lost user reaches for, and it must survive
// the leader window like every other one. No Ctrl key is involved,
// which is the point.
func TestHandleKey_LeaderQuestionMarkOpensReference(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))

	if _, ok := a.overlays.Top().(*overlay.Info); !ok {
		t.Fatalf("Esc ? left %T on the stack, want *overlay.Info", a.overlays.Top())
	}
}

// TestMenuRow_KeyboardShortcutsIsReachable closes CLAUDE.md's loop: the
// leader is a shortcut, and every action must also be reachable from the
// ≡ menu for the SSH sessions where a shortcut gets eaten.
func TestMenuRow_KeyboardShortcutsIsReachable(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	item := menuItemByLabel(t, a, "Keyboard shortcuts…")
	if item.shortcut != "Esc ?" {
		t.Errorf("row advertises %q, want %q", item.shortcut, "Esc ?")
	}
	if !item.enabled(a) {
		t.Error("the shortcut reference should always be available")
	}

	item.action(a)
	if _, ok := a.overlays.Top().(*overlay.Info); !ok {
		t.Fatalf("the menu row left %T on the stack, want *overlay.Info", a.overlays.Top())
	}
}
