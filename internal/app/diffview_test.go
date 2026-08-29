// =============================================================================
// File: internal/app/diffview_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-07-30
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the diff view modal: the pure unified→side-by-side parser,
// the responsive layout switch, and the button/scroll plumbing. Drawing
// runs on a tcell.SimulationScreen.

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/diff"
	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/theme"
)

// diffOv returns the open diff overlay, failing the test when none is
// up.
func diffOv(t *testing.T, a *App) *diffOverlay {
	t.Helper()
	d, ok := a.overlays.Top().(*diffOverlay)
	if !ok {
		t.Fatalf("no diff overlay open; top = %T", a.overlays.Top())
	}
	return d
}

// diffIsOpen reports whether the diff overlay is up.
func diffIsOpen(a *App) bool {
	_, ok := a.overlays.Top().(*diffOverlay)
	return ok
}

// pumpDiffLoad applies the diffLoadEvent an async diff request posted.
func pumpDiffLoad(t *testing.T, a *App) {
	t.Helper()
	pumpUntil(t, a, "diffLoadEvent", func(ev tcell.Event) bool {
		_, ok := ev.(*diffLoadEvent)
		return ok
	})
}

// sampleDiff is a small captured `git diff` with one modification
// flanked by context — the standard hunk shape the parser must align.
func sampleDiff() []string {
	return []string{
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,3 @@",
		" ctx one",
		"-old line",
		"+new line",
		" ctx two",
	}
}

// patchOf parses captured diff lines into the model openDiffView takes,
// so the fixtures below stay readable as the git output they came from.
func patchOf(lines ...string) diff.Patch {
	p, err := diff.Parse([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		panic(err)
	}
	return p
}

// samplePatch is sampleDiff parsed — the shape every diff-view test
// opens the modal with.
func samplePatch() diff.Patch { return patchOf(sampleDiff()...) }

// TestDrawDiffView_WordLevelHighlight checks the changed span renders
// in reverse video while the common prefix keeps the plain change
// color — the cell-level proof the word-level highlight works.
func TestDrawDiffView_WordLevelHighlight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	mx, my, _, _ := diffOv(t, a).modalRect()
	rowY := my + 5 // hunk, ctx, then the change row
	leftTextX := mx + 2 + diffNoGutter
	tints, ok := a.theme.DiffTints()
	if !ok {
		t.Fatal("test theme must yield tints")
	}

	// "old line" vs "new line": span [0,3) — "old" gets the louder
	// emphasis tint, " line" the row tint, and nothing is reversed.
	_, bgEmph, attrsChanged := cells[rowY*w+leftTextX].Style.Decompose()
	if bgEmph != tints.DelEmph {
		t.Fatalf("changed span bg = %v, want the DelEmph tint %v", bgEmph, tints.DelEmph)
	}
	if attrsChanged&tcell.AttrReverse != 0 {
		t.Fatal("tinted emphasis must not also reverse")
	}
	_, bgCommon, _ := cells[rowY*w+leftTextX+4].Style.Decompose()
	if bgCommon != tints.DelRow {
		t.Fatalf("common suffix bg = %v, want the DelRow tint %v", bgCommon, tints.DelRow)
	}
}

// TestDrawDiffView_LowColorKeepsLegacyPainting pins the fallback: on a
// palette the terminal can't blend for, changed rows keep the colored
// foreground and reverse-video emphasis on the plain modal surface —
// tinting is an upgrade, never a requirement.
func TestDrawDiffView_LowColorKeepsLegacyPainting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.theme.LowColor = true
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	mx, my, _, _ := diffOv(t, a).modalRect()
	rowY := my + 5
	leftTextX := mx + 2 + diffNoGutter

	fg, bg, attrs := cells[rowY*w+leftTextX].Style.Decompose()
	if fg != a.theme.GitDeleted {
		t.Fatalf("low-color deletion fg = %v, want GitDeleted", fg)
	}
	if bg != a.theme.LineHL {
		t.Fatalf("low-color deletion bg = %v, want the plain modal surface", bg)
	}
	if attrs&tcell.AttrReverse == 0 {
		t.Fatal("low-color emphasis must keep reverse video")
	}
}

// goDiff is a .go-flavored diff for the syntax-on-changes tests: one
// paired modification and one pure addition.
func goDiff() []string {
	return []string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,2 +1,3 @@",
		" package m",
		"-var old = 1",
		"+var renamed = 2",
		"+func added() {}",
	}
}

// TestDrawDiffView_ChangedRowsKeepSyntaxColors is the screenshot look:
// a changed row paints the whole side — gutter, text, trailing pad —
// with the change tint while the text keeps its Chroma colors, so the
// diff reads like highlighted code on a wash rather than flat
// red/green text. The blank side of a pure addition stays untinted:
// the gap is what says "nothing was here".
func TestDrawDiffView_ChangedRowsKeepSyntaxColors(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · main.go", patchOf(goDiff()...), "", "main.go")
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	mx, my, mw, _ := diffOv(t, a).modalRect()
	tints, _ := a.theme.DiffTints()
	bodyX, bodyW := mx+2, mw-4
	leftW := (bodyW - 3) / 2
	rightX := bodyX + leftW + 3
	changeY := my + 5 // hunk, context, then the paired change
	addY := my + 6    // the pure addition

	// The right side of the pair: "var" is a keyword — syntax fg, tint bg.
	fg, bg, _ := cells[changeY*w+rightX+diffNoGutter].Style.Decompose()
	if bg != tints.AddRow {
		t.Fatalf("added text bg = %v, want the AddRow tint %v", bg, tints.AddRow)
	}
	if fg == a.theme.Text || fg == a.theme.GitAdded {
		t.Fatalf("added keyword should keep its syntax color, got %v", fg)
	}
	// Gutter and trailing pad carry the tint too — full-row wash.
	if _, bg, _ = cells[changeY*w+rightX].Style.Decompose(); bg != tints.AddRow {
		t.Fatalf("gutter bg = %v, want the AddRow tint", bg)
	}
	padX := rightX + diffNoGutter + runeLen("var renamed = 2") + 2
	if _, bg, _ = cells[changeY*w+padX].Style.Decompose(); bg != tints.AddRow {
		t.Fatalf("trailing pad bg = %v, want the AddRow tint", bg)
	}
	// The blank left side of the pure addition keeps the plain surface.
	if _, bg, _ = cells[addY*w+bodyX+diffNoGutter].Style.Decompose(); bg != a.theme.LineHL {
		t.Fatalf("blank side bg = %v, want the plain modal surface", bg)
	}
}

// TestScrollDiffH_ClampsAndSlides pins the horizontal scroll bounds
// and that the drawn body actually slides: after scrolling right, the
// visible change-row text starts mid-line while line numbers stay put.
func TestScrollDiffH_ClampsAndSlides(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")

	diffOv(t, a).scrollByH(-5)
	if diffOv(t, a).scrollX != 0 {
		t.Fatalf("left of origin should clamp to 0, got %d", diffOv(t, a).scrollX)
	}
	diffOv(t, a).scrollByH(1000)
	if want := diffOv(t, a).maxLen - 10; diffOv(t, a).scrollX != want {
		t.Fatalf("overscroll should clamp to %d, got %d", want, diffOv(t, a).scrollX)
	}

	diffOv(t, a).scrollX = 4 // "old line" → " line" visible
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := diffOv(t, a).modalRect()
	change := screenLine(scr, my+5)
	if strings.Contains(change, "old line") || !strings.Contains(change, "line") {
		t.Fatalf("scrolled row should start mid-line, got %q", change)
	}
	if !strings.Contains(change, "2") {
		t.Fatalf("line numbers should stay put while text slides, got %q", change)
	}
}

// TestHandleDiffKey_ArrowsScrollHorizontally verifies ←/→ slide the
// body — the keyboard sibling of Shift+wheel.
func TestHandleDiffKey_ArrowsScrollHorizontally(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	a.handleKey(keyEv(tcell.KeyRight, 0))
	if diffOv(t, a).scrollX != wheelCols {
		t.Fatalf("Right should scroll by wheelCols, got %d", diffOv(t, a).scrollX)
	}
	a.handleKey(keyEv(tcell.KeyLeft, 0))
	if diffOv(t, a).scrollX != 0 {
		t.Fatalf("Left should scroll back, got %d", diffOv(t, a).scrollX)
	}
}

// TestHandleDiffMouse_ShiftWheelScrollsHorizontally pins the editor's
// Shift+wheel convention inside the diff: with shift recently seen, a
// vertical wheel impulse moves the body sideways instead of down.
func TestHandleDiffMouse_ShiftWheelScrollsHorizontally(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Tall enough to have vertical scroll room, wide enough for
	// horizontal — so both axes are observable.
	long := []string{"@@ -1,60 +1,60 @@"}
	for i := 0; i < 60; i++ {
		long = append(long, " context line padded well past the horizontal clamp margin")
	}
	a.openDiffView("Diff · f.txt", patchOf(long...), "", "f.txt")

	a.lastShiftAt = time.Now()
	diffOv(t, a).HandleMouse(50, 10, tcell.WheelDown)
	if diffOv(t, a).scrollX != wheelCols || diffOv(t, a).scroll != 0 {
		t.Fatalf("shift+wheel should scroll sideways only, x=%d y=%d", diffOv(t, a).scrollX, diffOv(t, a).scroll)
	}
	a.lastShiftAt = time.Time{}
	diffOv(t, a).HandleMouse(50, 10, tcell.WheelDown)
	if diffOv(t, a).scroll == 0 {
		t.Fatal("plain wheel should scroll vertically again")
	}
}

// TestDiffContextStyles_HighlightsContextOnly verifies Chroma styles
// land on context rows (a Go keyword picks up a non-default color) and
// never on changed rows, whose git coloring is the diff's whole point.
func TestDiffSideStyles_CoverContextAndChanges(t *testing.T) {
	raw := []string{
		"@@ -1,3 +1,3 @@",
		" func main() {",
		"-\told := 1",
		"+\tnew := 2",
		" }",
	}
	rows := diff.Rows(patchOf(raw...))
	th := theme.Default()
	left, right := diffSideStyles(rows, "main.go", th)
	if left == nil || right == nil {
		t.Fatal("expected styles for both sides of a .go diff")
	}
	if left[2] == nil || right[2] == nil {
		t.Fatal("changed rows must carry syntax styles on both sides")
	}
	// Each side is lexed from its own text: the change row's grids
	// cover "\told := 1" on the left and "\tnew := 2" on the right.
	if len(left[2]) != runeLen("\told := 1") || len(right[2]) != runeLen("\tnew := 2") {
		t.Fatalf("per-side grids sized %d/%d, want each side's own text", len(left[2]), len(right[2]))
	}
	ctx := left[1] // "func main() {"
	if len(ctx) < 4 {
		t.Fatalf("context row should have per-rune styles, got %d", len(ctx))
	}
	fg, bg, _ := ctx[0].Decompose() // the 'f' of func — a keyword
	if fg == th.Text {
		t.Fatal("keyword should not render in the plain text color")
	}
	if bg != th.LineHL {
		t.Fatalf("styles should be re-backgrounded onto the modal, got %v", bg)
	}
}

// TestDiffContextStyles_SkipsHugeDiffs pins the cost cap: past
// diffHighlightCap rows the helper opts out entirely rather than lex
// megabytes at open time.
func TestDiffContextStyles_SkipsHugeDiffs(t *testing.T) {
	rows := make([]diff.Row, diffHighlightCap+1)
	left, right := diffSideStyles(rows, "main.go", theme.Default())
	if left != nil || right != nil {
		t.Fatal("huge diffs should skip highlighting")
	}
}

// TestMenuDiffFile_OpensActiveTabDiff pins the ≡ → Diff this file flow:
// with a modified file in front the row is enabled and opens that file's
// diff (no Open button — the file is already open); with a clean tab it
// stays greyed out. The row lives one level down in the git drill-in, so
// the lookup goes through drillInItemByLabel — that keeps the
// reachability half of the check, which asserting hasDiffableTab alone
// would lose.
func TestMenuDiffFile_OpensActiveTabDiff(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\ntwo\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	a := newTestApp(t, dir)
	a.openFile(target)

	item := drillInItemByLabel(t, a, "Diff this file")
	if item.enabled(a) {
		t.Fatal("clean file should grey out Diff this file")
	}

	writeFileT(t, target, "one\nTWO\n")
	a.refreshGitStatus()
	if !item.enabled(a) {
		t.Fatal("modified file should enable Diff this file")
	}

	a.menuDiffFile()
	if diffIsOpen(a) {
		t.Fatal("the git read is off-thread; nothing should open inline")
	}
	pumpDiffLoad(t, a)
	if !diffIsOpen(a) {
		t.Fatal("menu action should open the diff view")
	}
	if diffOv(t, a).openPath != "" {
		t.Fatal("already-open file should have no Open button")
	}
	if body := strings.Join(diffOv(t, a).unified, "\n"); !strings.Contains(body, "+TWO") {
		t.Fatalf("diff body should show the change, got:\n%s", body)
	}
}

// TestMenuDiffFile_CleanFileFlashes guards the race where the row was
// enabled but the file went clean before the click: the action
// flashes instead of opening an empty diff.
func TestMenuDiffFile_CleanFileFlashes(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	a := newTestApp(t, dir)
	a.openFile(target)

	a.menuDiffFile()
	pumpDiffLoad(t, a)
	if diffIsOpen(a) {
		t.Fatal("clean file should not open a diff")
	}
	if !strings.Contains(a.statusMsg, "No uncommitted changes") {
		t.Fatalf("expected a flash, got %q", a.statusMsg)
	}
}

// TestOpenDiffView_FocusFollowsOpenPath pins the button defaults: with
// a file to open, Open file starts focused so Enter is the fast path;
// without one (deleted files), Close is the only and default target.
func TestOpenDiffView_FocusFollowsOpenPath(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "/tmp/f.txt", "f.txt")
	if !diffIsOpen(a) || diffOv(t, a).hover != 1 {
		t.Fatalf("open with path: open=%v hover=%d, want open+hover 1", diffIsOpen(a), diffOv(t, a).hover)
	}
	a.openDiffView("Diff · gone.txt", samplePatch(), "", "gone.txt")
	if diffOv(t, a).hover != 0 {
		t.Fatalf("open without path: hover=%d, want 0", diffOv(t, a).hover)
	}
	openRect, _ := diffOv(t, a).buttonRects()
	if openRect.w != 0 {
		t.Fatal("no open path → no Open file button zone")
	}
}

// TestHandleDiffKey_EnterOpensFocusedFile verifies the keyboard fast
// path end-to-end: Enter on the focused Open file button closes the
// modal and opens the file in a tab.
func TestHandleDiffKey_EnterOpensFocusedFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openDiffView("Diff · f.txt", samplePatch(), target, target)
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if diffIsOpen(a) {
		t.Fatal("Enter should close the diff view")
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != target {
		t.Fatalf("expected %s open", target)
	}
}

// TestOpenFileButton_JumpsToFirstChangeWithoutGitLines pins the plan
// 009 addendum: the Open file button derives its jump target from the
// diff overlay's own parsed rows (d.rows), never from tab.GitLines —
// which is nil right after a fresh open now that the inline `git diff`
// is off the tab-open path (see TestNewTab_DoesNotBlockOnGit). Without
// this, "click Open file → cursor jumps to what changed" would
// silently stop working the moment GitLines went async: the tab would
// open but the cursor would just sit at 0,0.
func TestOpenFileButton_JumpsToFirstChangeWithoutGitLines(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("ctx one\nnew line\nctx two\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openDiffView("Diff · f.txt", samplePatch(), target, target)

	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if diffIsOpen(a) {
		t.Fatal("Enter should close the diff view")
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != target {
		t.Fatalf("expected %s open", target)
	}
	if tab.GitLines != nil {
		t.Fatalf("the jump must not depend on GitLines, but it was populated: %v", tab.GitLines)
	}
	// sampleDiff's one change row pairs "old line"/"new line" at file
	// line 2 (1-based) — zero-based Cursor.Line 1.
	if tab.Cursor.Line != 1 {
		t.Fatalf("cursor should land on the diff's first changed line, got %d", tab.Cursor.Line)
	}
}

// TestHandleDiffKey_TabDeclinesAction verifies Tab moves focus to
// Close and Enter then dismisses without opening anything; Esc always
// dismisses.
func TestHandleDiffKey_TabDeclinesAction(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "/tmp/f.txt", "f.txt")
	a.handleKey(keyEv(tcell.KeyTab, 0))
	if diffOv(t, a).hover != 0 {
		t.Fatalf("Tab should focus Close, hover=%d", diffOv(t, a).hover)
	}
	tabsBefore := a.tabs.Len()
	a.handleKey(keyEv(tcell.KeyEnter, 0))
	if diffIsOpen(a) || a.tabs.Len() != tabsBefore {
		t.Fatal("Enter on Close should dismiss without opening a tab")
	}

	a.openDiffView("Diff · f.txt", samplePatch(), "/tmp/f.txt", "f.txt")
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if diffIsOpen(a) {
		t.Fatal("Esc should dismiss the diff view")
	}
}

// TestHandleDiffMouse_WheelAndButtons drives the modal with the mouse:
// wheel events scroll (never dismiss), clicking Open file opens the
// file, and a click outside dismisses.
func TestHandleDiffMouse_WheelAndButtons(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	long := []string{"@@ -1,60 +1,60 @@"}
	for i := 0; i < 60; i++ {
		long = append(long, " ctx")
	}
	a.openDiffView("Diff · f.txt", patchOf(long...), target, target)

	diffOv(t, a).HandleMouse(50, 10, tcell.WheelDown)
	if diffOv(t, a).scroll != 3 || !diffIsOpen(a) {
		t.Fatalf("WheelDown should scroll and keep the modal, scroll=%d", diffOv(t, a).scroll)
	}
	diffOv(t, a).HandleMouse(50, 10, tcell.WheelUp)
	if diffOv(t, a).scroll != 0 {
		t.Fatalf("WheelUp should scroll back, scroll=%d", diffOv(t, a).scroll)
	}

	openRect, _ := diffOv(t, a).buttonRects()
	diffOv(t, a).HandleMouse(openRect.x+1, openRect.y, tcell.Button1)
	if diffIsOpen(a) {
		t.Fatal("clicking Open file should close the modal")
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != target {
		t.Fatalf("expected %s open", target)
	}

	a.openDiffView("Diff · f.txt", patchOf(long...), target, target)
	diffOv(t, a).HandleMouse(0, 0, tcell.Button1)
	if diffIsOpen(a) {
		t.Fatal("outside click should dismiss")
	}
}

// TestDiffSideBySide_AdaptsToWidth pins the responsive rule: wide
// terminals get two columns, narrow ones fall back to unified — and
// the scroll clamp follows the active layout's row count.
func TestDiffSideBySide_AdaptsToWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	a.width = 120
	if !diffOv(t, a).sideBySide() {
		t.Fatal("120 columns should render side-by-side")
	}
	a.width = 70
	if diffOv(t, a).sideBySide() {
		t.Fatal("70 columns should fall back to unified")
	}
	// Unified counts body lines (5) vs side-by-side rows (4): the clamp
	// must use the active layout so scrolling can reach the unified tail.
	if got := diffOv(t, a).bodyCount(); got != len(diffOv(t, a).unified) {
		t.Fatalf("unified body count = %d, want %d", got, len(diffOv(t, a).unified))
	}
	a.width = 120
	if got := diffOv(t, a).bodyCount(); got != 4 {
		t.Fatalf("side-by-side body count = %d, want 4", got)
	}
}

// TestDrawDiffView_SideBySideSmoke renders the wide layout and checks
// the paired modification shares one screen row — old text on the
// left, new text on the right — with the hunk header above it.
func TestDrawDiffView_SideBySideSmoke(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := diffOv(t, a).modalRect()
	if title := screenLine(scr, my+1); !strings.Contains(title, "Diff · f.txt") {
		t.Fatalf("title row: %q", title)
	}
	if hunk := screenLine(scr, my+3); !strings.Contains(hunk, "@@ -1,3 +1,3 @@") {
		t.Fatalf("hunk row: %q", hunk)
	}
	change := screenLine(scr, my+5)
	if !strings.Contains(change, "old line") || !strings.Contains(change, "new line") {
		t.Fatalf("change row should show both sides, got %q", change)
	}
	left := strings.Index(change, "old line")
	right := strings.Index(change, "new line")
	if left >= right {
		t.Fatalf("old text should sit left of new text: %q", change)
	}
}

// TestDrawDiffView_NarrowFallsBackToUnified renders the same diff on a
// narrow layout and checks the raw unified lines (prefixes intact)
// appear instead of the two-column pairing.
func TestDrawDiffView_NarrowFallsBackToUnified(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width = 70
	a.openDiffView("Diff · f.txt", samplePatch(), "", "f.txt")
	diffOv(t, a).Draw(a.screen)
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := diffOv(t, a).modalRect()
	body := ""
	for i := 0; i < len(diffOv(t, a).unified); i++ {
		body += screenLine(scr, my+3+i) + "\n"
	}
	if !strings.Contains(body, "-old line") || !strings.Contains(body, "+new line") {
		t.Fatalf("unified body should keep raw prefixes, got:\n%s", body)
	}
}

// TestDiffBodyLines_ReproducesTheDiffItWasParsedFrom pins the narrow
// layout's body: a patch that came from git is written back out with
// git's own framing and prefixes (the `index` line, which names blobs
// nobody can read here, is the one thing dropped), while a buffer
// measured against disk carries no paths and so gets hunks alone.
func TestDiffBodyLines_ReproducesTheDiffItWasParsedFrom(t *testing.T) {
	want := []string{
		"diff --git a/f.txt b/f.txt",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,3 @@",
		" ctx one",
		"-old line",
		"+new line",
		" ctx two",
	}
	if got := diffBodyLines(samplePatch()); !reflect.DeepEqual(got, want) {
		t.Fatalf("git body =\n%q\nwant\n%q", got, want)
	}

	local := diff.Patch{Files: []diff.File{diff.Lines([]string{"a"}, []string{"b"})}}
	wantLocal := []string{"@@ -1,1 +1,1 @@", "-a", "+b"}
	if got := diffBodyLines(local); !reflect.DeepEqual(got, wantLocal) {
		t.Fatalf("buffer-vs-disk body = %q, want %q", got, wantLocal)
	}
}

// TestDiffBodyLines_KeepsDevNullAndBinaryNotes covers the two shapes a
// path alone cannot express: a deleted file, whose new side git spells
// /dev/null, and a binary file, which has no hunks yet still owes the
// reader a sentence.
func TestDiffBodyLines_KeepsDevNullAndBinaryNotes(t *testing.T) {
	gone := diffBodyLines(patchOf(
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,1 +0,0 @@",
		"-farewell",
	))
	if len(gone) < 3 || gone[2] != "+++ /dev/null" {
		t.Fatalf("deleted file body = %q", gone)
	}
	bin := diffBodyLines(patchOf(
		"diff --git a/img.png b/img.png",
		"Binary files a/img.png and b/img.png differ",
	))
	if len(bin) != 4 || bin[3] != "Binary files a/img.png and b/img.png differ" {
		t.Fatalf("binary body = %q", bin)
	}
}

// TestDiffBodyLines_KeepsTheNoNewlineNote checks git's "\ No newline at
// end of file" survives the round out: it is a fact about the file, and
// a body that quietly dropped it would claim a trailing newline the file
// does not have.
func TestDiffBodyLines_KeepsTheNoNewlineNote(t *testing.T) {
	got := diffBodyLines(patchOf("@@ -1 +1 @@", "-a", `\ No newline at end of file`, "+b"))
	want := []string{"@@ -1,1 +1,1 @@", "-a", `\ No newline at end of file`, "+b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestRequestDiff_PostsEventWithoutBlocking is the responsiveness
// contract shared by both click paths: the git read runs on a goroutine,
// so the call a mouse click makes returns with nothing open and only the
// posted event raises the modal. internal/git's read timeout is ten
// seconds — this is what keeps one click on a gutter marker in a slow or
// network-mounted repo from freezing the editor for that long. The Fake
// makes it exact: no subprocess, no repository, one scripted answer.
func TestRequestDiff_PostsEventWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\ntwo\n")
	a := newTestApp(t, dir)
	a.openFile(target)

	fake := &git.Fake{}
	fake.Script("diff --unified=3 --src-prefix=a/ --dst-prefix=b/ HEAD -- "+target, strings.Join(sampleDiff(), "\n")+"\n", nil)
	a.gitRunner = fake

	a.menuDiffFile()
	if diffIsOpen(a) {
		t.Fatal("the diff must not open inline — that is the ten-second freeze")
	}
	if !strings.Contains(a.statusMsg, "Loading") {
		t.Fatalf("the click needs acknowledging immediately, flash = %q", a.statusMsg)
	}

	pumpDiffLoad(t, a)
	if !diffIsOpen(a) {
		t.Fatal("the posted event should open the diff")
	}
	if body := strings.Join(diffOv(t, a).unified, "\n"); !strings.Contains(body, "+new line") {
		t.Fatalf("scripted diff body missing, got:\n%s", body)
	}
	if got := fake.CallCount(); got != 1 {
		t.Fatalf("one diff request = one git call, got %d", got)
	}
}

// TestHandleDiffLoaded_DropsStaleResults pins the three ways a finished
// load can arrive too late. Each would otherwise throw a modal over
// whatever the user moved on to in the meantime, which is worse than
// showing no diff at all. The live case at the end proves the guards
// aren't simply "never open".
func TestHandleDiffLoaded_DropsStaleResults(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\n")
	other := filepath.Join(dir, "g.txt")
	writeFileT(t, other, "two\n")
	a := newTestApp(t, dir)
	a.openFile(target)

	landed := func(gen int, path string) *diffLoadEvent {
		return &diffLoadEvent{
			when:    time.Now(),
			gen:     gen,
			kind:    diffLoadHunk,
			title:   "Git change · f.txt",
			tabPath: path,
			patch:   samplePatch(),
		}
	}

	// A newer click bumped the generation while git worked.
	a.diffLoadGen = 7
	a.handleDiffLoaded(landed(6, target))
	if diffIsOpen(a) {
		t.Fatal("a superseded load must not open")
	}

	// An overlay went up meanwhile.
	a.openInfo("Busy", []string{"working"})
	a.handleDiffLoaded(landed(7, target))
	if diffIsOpen(a) {
		t.Fatal("a load landing under an open overlay must not steal it")
	}
	a.closeAllModals()

	// The user switched tabs; the diff would describe a file that is no
	// longer in front of them.
	a.openFile(other)
	a.handleDiffLoaded(landed(7, target))
	if diffIsOpen(a) {
		t.Fatal("a load for a no-longer-active tab must not open")
	}

	a.handleDiffLoaded(landed(7, other))
	if !diffIsOpen(a) {
		t.Fatal("a current load should open")
	}
}

// TestHandleDiffLoaded_EmptyResultExplainsPerSurface pins why the event
// carries its kind: a menu action that turns up nothing flashes inline,
// while a gutter-marker click that turns up nothing owes the user a
// dialog — the marker promised a change, so its absence is surprising.
func TestHandleDiffLoaded_EmptyResultExplainsPerSurface(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\n")
	a := newTestApp(t, dir)
	a.openFile(target)

	empty := func(kind diffLoadKind) *diffLoadEvent {
		return &diffLoadEvent{when: time.Now(), gen: a.diffLoadGen, kind: kind, tabPath: target}
	}

	a.statusMsg = ""
	a.handleDiffLoaded(empty(diffLoadFile))
	if infoIsOpen(a) {
		t.Fatal("the menu action should flash, not open a dialog")
	}
	if !strings.Contains(a.statusMsg, "No uncommitted changes") {
		t.Fatalf("flash = %q, want the no-changes message", a.statusMsg)
	}

	a.handleDiffLoaded(empty(diffLoadHunk))
	if !infoIsOpen(a) {
		t.Fatal("a marker click with no hunk should explain itself in a dialog")
	}
}
