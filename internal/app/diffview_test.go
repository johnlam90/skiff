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
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/johnlam90/skiff/internal/theme"
)

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

// TestParseSideBySideDiff_AlignsModification pins the core pairing
// rule: a deletion run and the following addition run pair off
// line-for-line onto shared rows, with file headers dropped and the
// hunk header kept as its own row.
func TestParseSideBySideDiff_AlignsModification(t *testing.T) {
	rows := parseSideBySideDiff(sampleDiff())
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (hunk + ctx + change + ctx), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != diffRowHunk || !strings.HasPrefix(rows[0].Left, "@@") {
		t.Fatalf("row 0 should be the hunk header, got %+v", rows[0])
	}
	if rows[1].Kind != diffRowContext || rows[1].LeftNo != 1 || rows[1].RightNo != 1 || rows[1].Left != "ctx one" {
		t.Fatalf("row 1 context: %+v", rows[1])
	}
	ch := rows[2]
	if ch.Kind != diffRowChange || ch.LeftNo != 2 || ch.Left != "old line" || ch.RightNo != 2 || ch.Right != "new line" {
		t.Fatalf("row 2 should pair the modification, got %+v", ch)
	}
	if rows[3].Kind != diffRowContext || rows[3].LeftNo != 3 || rows[3].RightNo != 3 {
		t.Fatalf("row 3 context: %+v", rows[3])
	}
}

// TestParseSideBySideDiff_OneSidedRuns verifies pure additions leave
// the left side blank (LeftNo 0) and pure deletions the right — the
// gaps that make insert/delete blocks visually obvious.
func TestParseSideBySideDiff_OneSidedRuns(t *testing.T) {
	raw := []string{
		"@@ -1,2 +1,2 @@",
		"-gone",
		" kept",
		"+arrived",
	}
	rows := parseSideBySideDiff(raw)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d: %+v", len(rows), rows)
	}
	del := rows[1]
	if del.Kind != diffRowChange || del.LeftNo != 1 || del.Left != "gone" || del.RightNo != 0 {
		t.Fatalf("deletion row should be left-only, got %+v", del)
	}
	add := rows[3]
	if add.Kind != diffRowChange || add.RightNo != 1+1 || add.Right != "arrived" || add.LeftNo != 0 {
		t.Fatalf("addition row should be right-only, got %+v", add)
	}
}

// TestParseSideBySideDiff_UnevenRunsAndSecondHunk covers a 2-del/1-add
// hunk (the leftover deletion gets its own left-only row) and checks
// line numbering restarts correctly at a second hunk header.
func TestParseSideBySideDiff_UnevenRunsAndSecondHunk(t *testing.T) {
	raw := []string{
		"@@ -1,2 +1,1 @@",
		"-first",
		"-second",
		"+merged",
		"@@ -10,1 +9,1 @@",
		"-ten",
		"+nine",
	}
	rows := parseSideBySideDiff(raw)
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d: %+v", len(rows), rows)
	}
	if rows[1].Left != "first" || rows[1].Right != "merged" {
		t.Fatalf("row 1 should pair first/merged, got %+v", rows[1])
	}
	if rows[2].Left != "second" || rows[2].RightNo != 0 {
		t.Fatalf("row 2 should be the leftover deletion, got %+v", rows[2])
	}
	last := rows[4]
	if last.LeftNo != 10 || last.RightNo != 9 {
		t.Fatalf("second hunk numbering: got left %d right %d, want 10/9", last.LeftNo, last.RightNo)
	}
}

// TestParseSideBySideDiff_SkipsNoNewlineMarker keeps the "\ No newline
// at end of file" metadata out of the row stream — it is not content
// on either side.
func TestParseSideBySideDiff_SkipsNoNewlineMarker(t *testing.T) {
	raw := []string{
		"@@ -1 +1 @@",
		"-a",
		`\ No newline at end of file`,
		"+b",
		`\ No newline at end of file`,
	}
	rows := parseSideBySideDiff(raw)
	if len(rows) != 2 {
		t.Fatalf("expected hunk + one change row, got %d: %+v", len(rows), rows)
	}
	if rows[1].Left != "a" || rows[1].Right != "b" {
		t.Fatalf("change row: %+v", rows[1])
	}
}

// TestParseSideBySideDiff_MultiFileBoundaries pins commit-diff support:
// each `diff --git` boundary becomes a file row, hunk numbering resets
// per file, and — the flip side — a single-file diff keeps no boundary
// row at all because the modal title already names the file.
func TestParseSideBySideDiff_MultiFileBoundaries(t *testing.T) {
	raw := []string{
		"diff --git a/one.go b/one.go",
		"index 111..222 100644",
		"--- a/one.go",
		"+++ b/one.go",
		"@@ -1,1 +1,1 @@",
		"-alpha",
		"+ALPHA",
		"diff --git a/two.go b/two.go",
		"index 333..444 100644",
		"--- a/two.go",
		"+++ b/two.go",
		"@@ -5,1 +5,1 @@",
		"-beta",
		"+BETA",
	}
	rows := parseSideBySideDiff(raw)
	// file, hunk, change, file, hunk, change.
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != diffRowFile || rows[0].Left != "one.go" {
		t.Fatalf("row 0 should announce one.go, got %+v", rows[0])
	}
	if rows[3].Kind != diffRowFile || rows[3].Left != "two.go" {
		t.Fatalf("row 3 should announce two.go, got %+v", rows[3])
	}
	if rows[5].LeftNo != 5 || rows[5].RightNo != 5 {
		t.Fatalf("numbering should reset per file, got %+v", rows[5])
	}

	single := parseSideBySideDiff(sampleDiff())
	for _, r := range single {
		if r.Kind == diffRowFile {
			t.Fatal("single-file diffs must not carry a boundary row")
		}
	}
}

// TestDiffGitPath_ExtractsBSide pins the boundary-line parsing,
// including the rename case where the b side is the name to show.
func TestDiffGitPath_ExtractsBSide(t *testing.T) {
	if got := diffGitPath("diff --git a/old/name.go b/new/name.go"); got != "new/name.go" {
		t.Fatalf("rename boundary: got %q", got)
	}
	if got := diffGitPath("diff --git mangled"); got != "mangled" {
		t.Fatalf("fallback should return the remainder, got %q", got)
	}
}

// TestDiffSpan_LocatesChangedRunes pins the intra-line diff math:
// common prefix and suffix are excluded, and what remains is the span
// that gets the word-level highlight.
func TestDiffSpan_LocatesChangedRunes(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		oldSpan  span
		newSpan  span
	}{
		{"middle word", "println(\"two\")", "println(\"TWO\")", span{9, 12}, span{9, 12}},
		{"pure insertion", "ab", "aXb", span{1, 1}, span{1, 2}},
		{"pure removal", "aXb", "ab", span{1, 2}, span{1, 1}},
		{"whole line", "abc", "xyz", span{0, 3}, span{0, 3}},
		{"identical", "same", "same", span{4, 4}, span{4, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, n := diffSpan([]rune(tt.old), []rune(tt.new))
			if o != tt.oldSpan || n != tt.newSpan {
				t.Fatalf("got old %+v new %+v, want %+v / %+v", o, n, tt.oldSpan, tt.newSpan)
			}
		})
	}
}

// TestAnnotateDiffSpans_OnlyPairedRows verifies emphasis lands on
// paired modification rows and skips one-sided rows, where the whole
// line already reads as the change.
func TestAnnotateDiffSpans_OnlyPairedRows(t *testing.T) {
	rows := parseSideBySideDiff(sampleDiff())
	annotateDiffSpans(rows)
	ch := rows[2] // "old line" → "new line"
	if ch.LeftEmph != (span{0, 3}) || ch.RightEmph != (span{0, 3}) {
		t.Fatalf("paired row spans: left %+v right %+v, want {0 3} both", ch.LeftEmph, ch.RightEmph)
	}
	oneSided := parseSideBySideDiff([]string{"@@ -1,1 +1,2 @@", " keep", "+added"})
	annotateDiffSpans(oneSided)
	if oneSided[2].RightEmph != (span{}) {
		t.Fatalf("one-sided row should carry no span, got %+v", oneSided[2].RightEmph)
	}
}

// TestDrawDiffView_WordLevelHighlight checks the changed span renders
// in reverse video while the common prefix keeps the plain change
// color — the cell-level proof the word-level highlight works.
func TestDrawDiffView_WordLevelHighlight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")
	a.drawDiffView()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	mx, my, mw, _ := a.diffModalRect()
	rowY := my + 5 // hunk, ctx, then the change row
	leftTextX := mx + 2 + diffNoGutter

	// "old line" vs "new line": span [0,3) — "old" reversed, " line" not.
	_, _, attrsChanged := cells[rowY*w+leftTextX].Style.Decompose()
	if attrsChanged&tcell.AttrReverse == 0 {
		t.Fatal("changed span should render in reverse video")
	}
	_, _, attrsCommon := cells[rowY*w+leftTextX+4].Style.Decompose()
	if attrsCommon&tcell.AttrReverse != 0 {
		t.Fatal("common suffix should not be reversed")
	}
	_ = mw
}

// TestScrollDiffH_ClampsAndSlides pins the horizontal scroll bounds
// and that the drawn body actually slides: after scrolling right, the
// visible change-row text starts mid-line while line numbers stay put.
func TestScrollDiffH_ClampsAndSlides(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")

	a.scrollDiffH(-5)
	if a.diffScrollX != 0 {
		t.Fatalf("left of origin should clamp to 0, got %d", a.diffScrollX)
	}
	a.scrollDiffH(1000)
	if want := a.diffMaxLen - 10; a.diffScrollX != want {
		t.Fatalf("overscroll should clamp to %d, got %d", want, a.diffScrollX)
	}

	a.diffScrollX = 4 // "old line" → " line" visible
	a.drawDiffView()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := a.diffModalRect()
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
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")
	a.handleDiffKey(keyEv(tcell.KeyRight, 0))
	if a.diffScrollX != wheelCols {
		t.Fatalf("Right should scroll by wheelCols, got %d", a.diffScrollX)
	}
	a.handleDiffKey(keyEv(tcell.KeyLeft, 0))
	if a.diffScrollX != 0 {
		t.Fatalf("Left should scroll back, got %d", a.diffScrollX)
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
	a.openDiffView("Diff · f.txt", long, "", "f.txt")

	a.lastShiftAt = time.Now()
	a.handleDiffMouse(50, 10, tcell.WheelDown)
	if a.diffScrollX != wheelCols || a.diffScroll != 0 {
		t.Fatalf("shift+wheel should scroll sideways only, x=%d y=%d", a.diffScrollX, a.diffScroll)
	}
	a.lastShiftAt = time.Time{}
	a.handleDiffMouse(50, 10, tcell.WheelDown)
	if a.diffScroll == 0 {
		t.Fatal("plain wheel should scroll vertically again")
	}
}

// TestDiffContextStyles_HighlightsContextOnly verifies Chroma styles
// land on context rows (a Go keyword picks up a non-default color) and
// never on changed rows, whose git coloring is the diff's whole point.
func TestDiffContextStyles_HighlightsContextOnly(t *testing.T) {
	raw := []string{
		"@@ -1,3 +1,3 @@",
		" func main() {",
		"-\told := 1",
		"+\tnew := 2",
		" }",
	}
	rows := parseSideBySideDiff(raw)
	th := theme.Default()
	styles := diffContextStyles(rows, "main.go", th)
	if styles == nil {
		t.Fatal("expected styles for a .go diff")
	}
	if styles[2] != nil {
		t.Fatal("changed rows must not carry syntax styles")
	}
	ctx := styles[1] // "func main() {"
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
	rows := make([]diffRow, diffHighlightCap+1)
	if got := diffContextStyles(rows, "main.go", theme.Default()); got != nil {
		t.Fatal("huge diffs should skip highlighting")
	}
}

// TestMenuDiffFile_OpensActiveTabDiff pins the ≡ → Diff this file flow:
// with a modified file in front, the row is enabled and opens that
// file's diff (no Open button — the file is already open); with a
// clean tab it stays greyed out.
func TestMenuDiffFile_OpensActiveTabDiff(t *testing.T) {
	requireGit(t)
	dir := initRepo(t)
	target := filepath.Join(dir, "f.txt")
	writeFileT(t, target, "one\ntwo\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	a := newTestApp(t, dir)
	a.openFile(target)

	item := menuItemByLabel(t, a, "Diff this file")
	if item.enabled(a) {
		t.Fatal("clean file should grey out Diff this file")
	}

	writeFileT(t, target, "one\nTWO\n")
	a.refreshGitStatus()
	if !item.enabled(a) {
		t.Fatal("modified file should enable Diff this file")
	}

	a.menuDiffFile()
	if !a.diffOpen {
		t.Fatal("menu action should open the diff view")
	}
	if a.diffOpenPath != "" {
		t.Fatal("already-open file should have no Open button")
	}
	if body := strings.Join(a.diffRaw, "\n"); !strings.Contains(body, "+TWO") {
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
	if a.diffOpen {
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
	a.openDiffView("Diff · f.txt", sampleDiff(), "/tmp/f.txt", "f.txt")
	if !a.diffOpen || a.diffHover != 1 {
		t.Fatalf("open with path: open=%v hover=%d, want open+hover 1", a.diffOpen, a.diffHover)
	}
	a.openDiffView("Diff · gone.txt", sampleDiff(), "", "gone.txt")
	if a.diffHover != 0 {
		t.Fatalf("open without path: hover=%d, want 0", a.diffHover)
	}
	openRect, _ := a.diffButtonRects()
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
	a.openDiffView("Diff · f.txt", sampleDiff(), target, target)
	a.handleDiffKey(keyEv(tcell.KeyEnter, 0))
	if a.diffOpen {
		t.Fatal("Enter should close the diff view")
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != target {
		t.Fatalf("expected %s open", target)
	}
}

// TestHandleDiffKey_TabDeclinesAction verifies Tab moves focus to
// Close and Enter then dismisses without opening anything; Esc always
// dismisses.
func TestHandleDiffKey_TabDeclinesAction(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", sampleDiff(), "/tmp/f.txt", "f.txt")
	a.handleDiffKey(keyEv(tcell.KeyTab, 0))
	if a.diffHover != 0 {
		t.Fatalf("Tab should focus Close, hover=%d", a.diffHover)
	}
	tabsBefore := len(a.tabs)
	a.handleDiffKey(keyEv(tcell.KeyEnter, 0))
	if a.diffOpen || len(a.tabs) != tabsBefore {
		t.Fatal("Enter on Close should dismiss without opening a tab")
	}

	a.openDiffView("Diff · f.txt", sampleDiff(), "/tmp/f.txt", "f.txt")
	a.handleDiffKey(keyEv(tcell.KeyEsc, 0))
	if a.diffOpen {
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
	a.openDiffView("Diff · f.txt", long, target, target)

	a.handleDiffMouse(50, 10, tcell.WheelDown)
	if a.diffScroll != 3 || !a.diffOpen {
		t.Fatalf("WheelDown should scroll and keep the modal, scroll=%d", a.diffScroll)
	}
	a.handleDiffMouse(50, 10, tcell.WheelUp)
	if a.diffScroll != 0 {
		t.Fatalf("WheelUp should scroll back, scroll=%d", a.diffScroll)
	}

	openRect, _ := a.diffButtonRects()
	a.handleDiffMouse(openRect.x+1, openRect.y, tcell.Button1)
	if a.diffOpen {
		t.Fatal("clicking Open file should close the modal")
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != target {
		t.Fatalf("expected %s open", target)
	}

	a.openDiffView("Diff · f.txt", long, target, target)
	a.handleDiffMouse(0, 0, tcell.Button1)
	if a.diffOpen {
		t.Fatal("outside click should dismiss")
	}
}

// TestDiffSideBySide_AdaptsToWidth pins the responsive rule: wide
// terminals get two columns, narrow ones fall back to unified — and
// the scroll clamp follows the active layout's row count.
func TestDiffSideBySide_AdaptsToWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")
	a.width = 120
	if !a.diffSideBySide() {
		t.Fatal("120 columns should render side-by-side")
	}
	a.width = 70
	if a.diffSideBySide() {
		t.Fatal("70 columns should fall back to unified")
	}
	// Unified counts raw lines (9) vs side-by-side rows (4): the clamp
	// must use the active layout so scrolling can reach the raw tail.
	if got := a.diffBodyCount(); got != len(sampleDiff()) {
		t.Fatalf("unified body count = %d, want %d", got, len(sampleDiff()))
	}
	a.width = 120
	if got := a.diffBodyCount(); got != 4 {
		t.Fatalf("side-by-side body count = %d, want 4", got)
	}
}

// TestDrawDiffView_SideBySideSmoke renders the wide layout and checks
// the paired modification shares one screen row — old text on the
// left, new text on the right — with the hunk header above it.
func TestDrawDiffView_SideBySideSmoke(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")
	a.drawDiffView()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := a.diffModalRect()
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
	a.openDiffView("Diff · f.txt", sampleDiff(), "", "f.txt")
	a.drawDiffView()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	_, my, _, _ := a.diffModalRect()
	body := ""
	for i := 0; i < len(sampleDiff()); i++ {
		body += screenLine(scr, my+3+i) + "\n"
	}
	if !strings.Contains(body, "-old line") || !strings.Contains(body, "+new line") {
		t.Fatalf("unified body should keep raw prefixes, got:\n%s", body)
	}
}
