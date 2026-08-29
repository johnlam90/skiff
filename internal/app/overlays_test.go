// =============================================================================
// File: internal/app/overlays_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/overlay"
)

// initRepoWithCommit builds on initRepo with one committed file, since
// the commit-history modal refuses to open on an empty history.
func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

// TestOpeners_PushOverlayStack pins the stack-sync contract: every modal
// opener must leave its shim on the overlay stack, and closeAllModals
// must empty it. The stack is the single routing truth — an opener that
// forgets to push would leave a modal painted but deaf.
func TestOpeners_PushOverlayStack(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T) string // returns the app root dir
		open  func(*App)
	}{
		{"menu", nil, func(a *App) { a.openMenu() }},
		{"prompt", nil, func(a *App) { a.openPrompt("T", "", "", nil) }},
		{"confirm", nil, func(a *App) { a.openConfirm("T", "m", nil) }},
		{"info", nil, func(a *App) { a.openInfo("T", []string{"l"}) }},
		{"dirty", nil, func(a *App) { a.openDirtyClose("T", "m", nil, nil) }},
		{"form", nil, func(a *App) {
			a.openForm("T", []customactions.Prompt{{Key: "k", Label: "L"}}, nil)
		}},
		{"treeContext", nil, func(a *App) { a.openTreeContext(a.tree.Root, 2, 2) }},
		{"gitExtras", nil, func(a *App) { a.openGitExtras(2, 2) }},
		{"listPick", nil, func(a *App) {
			a.openListPick("T", []listPickItem{{Label: "one"}}, nil, nil, nil)
		}},
		{"finder", nil, func(a *App) { a.openFinder() }},
		{"diff", nil, func(a *App) { a.openDiffView("T", patchOf("@@ -1,0 +1,1 @@", "+x"), "", "") }},
		// openGitLog refuses to open with no commits, so it needs a real
		// repo with history — initRepo skips when git isn't installed.
		{"gitLog", initRepoWithCommit,
			func(a *App) { a.openGitLog("T", "") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := ""
			if c.setup != nil {
				dir = c.setup(t)
			} else {
				dir = t.TempDir()
			}
			a := newTestApp(t, dir)
			c.open(a)
			if !a.overlays.IsOpen() {
				t.Fatalf("%s opener did not push onto the overlay stack", c.name)
			}
			a.closeAllModals()
			if a.overlays.IsOpen() {
				t.Fatalf("closeAllModals left the %s overlay on the stack", c.name)
			}
		})
	}
}

// TestClosers_PopTheirOverlay pins that each dedicated closer empties
// the stack it pushed onto — without one, a dismissed modal would keep
// swallowing every key and click.
func TestClosers_PopTheirOverlay(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.openMenu()
	a.closeMenu()
	if a.overlays.IsOpen() {
		t.Fatal("closeMenu must pop the menu overlay")
	}

	a.openFinder()
	pressEsc(a)
	if a.overlays.IsOpen() {
		t.Fatal("Esc must pop the finder overlay")
	}

	a.openListPick("T", []listPickItem{{Label: "one"}}, nil, nil, nil)
	pickPrefab(t, a).Cancel()
	if a.overlays.IsOpen() {
		t.Fatal("cancelling the pick must pop it off the stack")
	}
}

// TestCloseGitLog_PopsOverlay is the git-gated twin of
// TestClosers_PopTheirOverlay: the commit-history modal only opens on a
// repo with history, so its closer is pinned separately against a real
// repo.
func TestCloseGitLog_PopsOverlay(t *testing.T) {
	a := newTestApp(t, initRepoWithCommit(t))
	a.openGitLog("T", "")
	if !a.overlays.IsOpen() {
		t.Fatal("openGitLog should have pushed onto the stack")
	}
	pressEsc(a)
	if a.overlays.IsOpen() {
		t.Fatal("Esc must pop the git log overlay")
	}
}

// TestDropOverlay_OnlyPopsOwnShim pins the chained-open guard: a stray
// dedicated closer running after the next modal has already opened must
// not dismiss that newer overlay. Identity, not recency, decides the pop.
func TestDropOverlay_OnlyPopsOwnShim(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPrompt("T", "", "", nil)
	a.closeMenu() // menu is not on top — must be a no-op
	if !a.overlays.IsOpen() {
		t.Fatal("closeMenu popped a prompt it does not own")
	}
	if _, ok := a.overlays.Top().(*overlay.Prompt); !ok {
		t.Fatalf("expected the prompt overlay on top, got %T", a.overlays.Top())
	}
}

// TestStackRouting_KeyReachesPrompt pins end-to-end key routing: a rune
// typed while the prompt is up must land in the prompt's input, whether
// routing goes through the old cascade or the overlay stack.
func TestStackRouting_KeyReachesPrompt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPrompt("T", "", "", nil)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if got := promptPrefab(t, a).Field.Text(); got != "x" {
		t.Fatalf("typed rune did not reach the prompt input: %q", got)
	}
}

// TestStackRouting_MenuNavAndEsc pins the menu's key path across the
// extraction into handleMenuKey: Down moves the highlight off -1, and
// Esc closes the menu and empties the stack.
func TestStackRouting_MenuNavAndEsc(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.hoveredMenuRow < 0 {
		t.Fatal("Down while menu open must move the highlight")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.menuOpen || a.overlays.IsOpen() {
		t.Fatal("Esc while menu open must close it and empty the stack")
	}
}

// TestStackDraw_PaintsTopOverlay pins draw()'s delegation: after opening
// the prompt, a full draw pass must actually paint the prompt chrome —
// its title — not just track state.
func TestStackDraw_PaintsTopOverlay(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPrompt("Rename file", "", "", nil)
	a.draw()
	if !containsRunes(screenText(t, a), "Rename file") {
		t.Fatal("draw() did not paint the open prompt's title")
	}
}

// containsRunes reports whether the flattened screen cells contain s as
// a contiguous run — enough for asserting painted labels in tests.
func containsRunes(cells []rune, s string) bool {
	target := []rune(s)
	for i := 0; i+len(target) <= len(cells); i++ {
		ok := true
		for j := range target {
			if cells[i+j] != target[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// The app's own overlays satisfy the whole contract too — the bespoke
// four alongside the package's prefabs. Listing them here is the fence
// against a new App-side surface shipping without WantsMotion or
// Dismiss, which is exactly the gap the two type switches used to hide.
var (
	_ overlay.Overlay = menuOverlay{}
	_ overlay.Overlay = (*finderOverlay)(nil)
	_ overlay.Overlay = (*gitLogOverlay)(nil)
	_ overlay.Overlay = (*diffOverlay)(nil)
)
