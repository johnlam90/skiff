// =============================================================================
// File: internal/overlay/info_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package overlay

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"
)

// testInfo builds an Info with n body lines on a 100×12 screen — the
// same geometry the original app-level clamp test pinned.
func testInfo(lines int) (*Info, *int) {
	closed := 0
	n := &Info{
		Title: "T",
		Lines: make([]string, lines),
		Theme: theme.Default(),
		Size:  func() (int, int) { return 100, 12 },
	}
	n.Close = func() { closed++ }
	return n, &closed
}

// TestInfo_BodyRowsAndScrollClamp pins the viewport math: on a 12-row
// screen the body shows 5 lines (screen minus 7 chrome rows), and
// scrolling clamps to [0, len-rows].
func TestInfo_BodyRowsAndScrollClamp(t *testing.T) {
	n, _ := testInfo(20)
	if got := n.bodyRows(); got != 5 {
		t.Fatalf("body rows = %d, want 5", got)
	}
	n.ScrollBy(100)
	if want := 20 - 5; n.Scroll() != want {
		t.Fatalf("scroll = %d, want %d", n.Scroll(), want)
	}
	n.ScrollBy(-100)
	if n.Scroll() != 0 {
		t.Fatalf("scroll = %d, want 0", n.Scroll())
	}
}

// TestInfo_WheelScrollsWithoutDismissing is the regression pin for
// trackpad scrolling: tcell reports wheels as WheelUp/WheelDown (an
// earlier version listened for Button4/5, the X11 convention, so wheel
// events silently did nothing) — and a wheel must never dismiss.
func TestInfo_WheelScrollsWithoutDismissing(t *testing.T) {
	n, closed := testInfo(40)
	n.HandleMouse(50, 6, tcell.WheelDown)
	if n.Scroll() != 3 {
		t.Fatalf("WheelDown should scroll by 3, got %d", n.Scroll())
	}
	n.HandleMouse(50, 6, tcell.WheelUp)
	if n.Scroll() != 0 {
		t.Fatalf("WheelUp should scroll back, got %d", n.Scroll())
	}
	if *closed != 0 {
		t.Fatal("wheel events must never dismiss the overlay")
	}
}

// TestInfo_DismissKeys pins the "I'm done" keys: Esc, Enter, and Tab
// each close the single-button overlay.
func TestInfo_DismissKeys(t *testing.T) {
	for _, k := range []tcell.Key{tcell.KeyEsc, tcell.KeyEnter, tcell.KeyTab} {
		n, closed := testInfo(3)
		n.HandleKey(key(k, 0))
		if *closed != 1 {
			t.Errorf("key %v should dismiss", k)
		}
	}
}

// TestInfo_OKButtonAndOutsideClick pins the two mouse dismissal paths.
func TestInfo_OKButtonAndOutsideClick(t *testing.T) {
	n, closed := testInfo(3)
	r := n.rect()
	n.HandleMouse(r.X+(r.W-10)/2+1, r.Y+r.H-3, tcell.Button1)
	if *closed != 1 {
		t.Fatal("OK click should dismiss")
	}
	n, closed = testInfo(3)
	r = n.rect()
	n.HandleMouse(r.X-1, r.Y-1, tcell.Button1)
	if *closed != 1 {
		t.Fatal("outside click should dismiss")
	}
}

// TestInfo_DrawWrapsLongLines pins the body's reflow: a 200-cell line
// on an 80-cell body is read whole on three rows, the neighbour that
// fits follows on the row after them, and no row overruns the body.
// Info used to truncate to an ellipsis, which for a stderr path or a
// formatter's argv meant losing exactly the part that mattered.
func TestInfo_DrawWrapsLongLines(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(100, 12)

	long := strings.Repeat("x", 200)
	n, _ := testInfo(1)
	n.Lines = []string{long, "short"}
	n.Draw(scr)
	scr.Show()

	r := n.rect()
	body := n.BodyTextWidth()
	if body != r.W-4 {
		t.Fatalf("BodyTextWidth = %d, want frame minus chrome %d", body, r.W-4)
	}
	if got, want := n.RowCount(), 3+1; got != want {
		t.Fatalf("wrapped row count = %d, want %d (200 cells over %d, plus the short line)", got, want, body)
	}
	cells, w, _ := scr.GetContents()
	rowText := func(y int) string {
		row := make([]rune, 0, body)
		for i := range body {
			row = append(row, cells[(r.Y+3+y)*w+r.X+2+i].Runes[0])
		}
		return string(row)
	}
	joined := strings.TrimSpace(rowText(0)) + strings.TrimSpace(rowText(1)) + strings.TrimSpace(rowText(2))
	if joined != long {
		t.Fatalf("the three wrapped rows do not reassemble the line: %d cells, want 200", len(joined))
	}
	if got := strings.TrimSpace(rowText(3)); got != "short" {
		t.Fatalf("row after the wrapped line = %q, want %q", got, "short")
	}
}

// TestInfo_DrawKeepsDiffColorOnContinuationRows guards the style pick:
// a wrapped continuation row carries no +/- marker of its own, so the
// color must come from the SOURCE line, not from the row's own text —
// otherwise the tail of a long addition would paint as plain context.
func TestInfo_DrawKeepsDiffColorOnContinuationRows(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(100, 12)

	n, _ := testInfo(1)
	n.Lines = []string{"+" + strings.Repeat("y", 200)}
	n.Draw(scr)
	scr.Show()

	r := n.rect()
	cells, w, _ := scr.GetContents()
	for y := 0; y < 3; y++ {
		fg, _, _ := cells[(r.Y+3+y)*w+r.X+2].Style.Decompose()
		if fg != n.Theme.GitAdded {
			t.Fatalf("row %d fg = %v, want the addition color %v", y, fg, n.Theme.GitAdded)
		}
	}
}

// TestInfo_WrapsToThePhoneWidth is the case the wrap exists for: a
// 120-cell error line on a 40-column terminal wraps onto as many rows
// as its body width needs (here four of 36), the scroll clamp and the
// indicator count those rows — not the one authored line — and every
// painted row stays inside the frame.
func TestInfo_WrapsToThePhoneWidth(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(40, 10)

	n, _ := testInfo(1)
	n.Size = func() (int, int) { return 40, 10 }
	n.Lines = []string{strings.Repeat("e", 120)}
	body := n.BodyTextWidth()
	if body != 36 {
		t.Fatalf("BodyTextWidth at 40 columns = %d, want 36", body)
	}
	want := (120 + body - 1) / body
	if got := n.RowCount(); got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
	n.Draw(scr)
	scr.Show()
	r := n.rect()
	if b := n.bar(r); b.total != want || b.viewH != n.bodyRows() {
		t.Fatalf("indicator total/view = %d/%d, want %d/%d", b.total, b.viewH, want, n.bodyRows())
	}
	n.ScrollBy(100)
	if n.Scroll() != want-n.bodyRows() {
		t.Fatalf("scroll clamped to %d, want %d rows minus %d visible", n.Scroll(), want, n.bodyRows())
	}
	cells, w, _ := scr.GetContents()
	for y := r.Y + 3; y < r.Y+3+n.bodyRows(); y++ {
		if c := cells[y*w+r.X+r.W-1]; len(c.Runes) == 0 || c.Runes[0] != '│' {
			t.Fatalf("row %d lost its right border to the text: %q", y, c.Runes)
		}
	}
}

// TestDiffLineStyle keeps git previews readable: additions, deletions,
// hunk headers, and file headers each get their diff color.
func TestDiffLineStyle(t *testing.T) {
	th := theme.Default()
	bg := th.LineHL
	cases := []struct {
		line string
		want tcell.Color
	}{
		{line: "+new code", want: th.GitAdded},
		{line: "-old code", want: th.GitDeleted},
		{line: "@@ -1 +1 @@", want: th.AccentSoft},
		{line: " context", want: th.Text},
		{line: "+++ b/f.go", want: th.Muted},
		{line: "--- a/f.go", want: th.Muted},
	}
	for _, tc := range cases {
		fg, _, _ := DiffLineStyle(th, bg, tc.line).Decompose()
		if fg != tc.want {
			t.Fatalf("%q fg = %v, want %v", tc.line, fg, tc.want)
		}
	}
}

// -----------------------------------------------------------------------------
// Body scroll indicator
// -----------------------------------------------------------------------------

// infoScreen builds the 100×12 simulation screen testInfo's geometry
// assumes, so the drawn indicator column can be read back.
func infoScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(100, 12)
	return scr
}

// infoBarColumn draws n and reads its indicator column back across the
// body rows.
func infoBarColumn(t *testing.T, scr tcell.SimulationScreen, n *Info) string {
	t.Helper()
	n.Draw(scr)
	scr.Show()
	return barCol(scr, n.bar(n.rect()))
}

// TestInfo_NoIndicatorWhenBodyFits pins the no-noise half: the short
// reports (a one-line error, a three-line summary) keep a blank padding
// column, so the bar's presence is itself the signal.
func TestInfo_NoIndicatorWhenBodyFits(t *testing.T) {
	scr := infoScreen(t)
	n, _ := testInfo(3)
	for i := range n.Lines {
		n.Lines[i] = "ok"
	}
	col := infoBarColumn(t, scr, n)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a 3-line report must draw no bar, got %q", col)
	}
}

// TestInfo_IndicatorTracksScroll covers the surface this matters most
// on: Info is where command stderr and git diff previews land, where a
// 300-line body is ordinary. The bar has to appear, sit at the top of
// its track at scroll 0, and follow the body down.
func TestInfo_IndicatorTracksScroll(t *testing.T) {
	scr := infoScreen(t)
	n, _ := testInfo(300)
	for i := range n.Lines {
		n.Lines[i] = "stderr line"
	}

	top := infoBarColumn(t, scr, n)
	if !strings.HasPrefix(top, string(scrollbar.Thumb)) || !strings.ContainsRune(top, scrollbar.Track) {
		t.Fatalf("300 lines in a 5-row window: got %q", top)
	}

	n.ScrollBy(len(n.Lines))
	bottom := infoBarColumn(t, scr, n)
	if !strings.HasSuffix(bottom, string(scrollbar.Thumb)) {
		t.Fatalf("at the end the thumb must finish the track, got %q", bottom)
	}
	wantStart, wantLen, ok := scrollbar.Geom(len(n.Lines), n.bodyRows(), n.Scroll())
	if !ok {
		t.Fatal("fixture should overflow")
	}
	for row, got := range []rune(bottom) {
		want := scrollbar.Track
		if row >= wantStart && row < wantStart+wantLen {
			want = scrollbar.Thumb
		}
		if got != want {
			t.Fatalf("bar row %d: got %q, want %q (col %q)", row, got, want, bottom)
		}
	}
}

// TestInfo_IndicatorIsNotDiffStyled guards the one styling trap this
// surface has: body lines run through DiffLineStyle, so a diff preview
// would happily paint the whole row green. The bar keeps its own
// Subtle/Muted roles because the text is clipped two cells short of it
// and the bar is painted afterwards.
func TestInfo_IndicatorIsNotDiffStyled(t *testing.T) {
	scr := infoScreen(t)
	n, _ := testInfo(300)
	for i := range n.Lines {
		n.Lines[i] = "+" + strings.Repeat("y", 200)
	}
	n.Draw(scr)
	scr.Show()

	b := n.bar(n.rect())
	cells, w, _ := scr.GetContents()
	for row := range b.viewH {
		fg, _, _ := cells[(b.top+row)*w+b.x].Style.Decompose()
		if fg == n.Theme.GitAdded {
			t.Fatalf("bar row %d took the diff addition color", row)
		}
		if fg != n.Theme.Subtle && fg != n.Theme.Muted {
			t.Fatalf("bar row %d fg = %v, want Subtle or Muted", row, fg)
		}
	}
}

// TestInfo_BarClickJumpsWithoutDismissing pins the mouse contract: the
// indicator's column scrolls and nothing else. A press that dismissed
// the report would throw away the stderr the user was mid-way through
// reading.
func TestInfo_BarClickJumpsWithoutDismissing(t *testing.T) {
	scr := infoScreen(t)
	n, closed := testInfo(300)
	for i := range n.Lines {
		n.Lines[i] = "stderr line"
	}
	infoBarColumn(t, scr, n)

	b := n.bar(n.rect())
	n.HandleMouse(b.x, b.top+b.viewH-1, tcell.Button1)
	if want := len(n.Lines) - n.bodyRows(); n.Scroll() != want {
		t.Fatalf("press at the foot of the bar: scroll %d, want %d", n.Scroll(), want)
	}
	n.HandleMouse(b.x, b.top, tcell.Button1)
	if n.Scroll() != 0 {
		t.Fatalf("press at the head of the bar should return to the top, got %d", n.Scroll())
	}
	if *closed != 0 {
		t.Fatalf("a bar press must not dismiss the report, closed %d times", *closed)
	}
}

// TestInfo_ClampsToPhoneSizedScreen pins the report surface at skiff's
// floor. Info is the cheat sheet and the failed-command stderr, so it is
// the overlay most likely to be opened on the narrowest terminal someone
// owns: its 84-cell natural frame has to collapse onto the screen, and
// its body window has to stay inside the rows the frame actually got.
func TestInfo_ClampsToPhoneSizedScreen(t *testing.T) {
	const scrW, scrH = 40, 10
	n, _ := testInfo(40)
	n.Size = func() (int, int) { return scrW, scrH }

	r := n.rect()
	if r.X < 0 || r.X+r.W > scrW || r.Y < 0 || r.Y+r.H > scrH {
		t.Fatalf("frame off screen: %+v on %dx%d", r, scrW, scrH)
	}
	if n.bodyRows() < 1 {
		t.Fatal("no body rows at all")
	}
	if last := r.Y + 3 + n.bodyRows(); last > r.Y+r.H-1 {
		t.Fatalf("body window ends at %d, past the frame's last inner row %d", last, r.Y+r.H-1)
	}
}

// TestDiffLineStyle_TintsChangedRows: the unified fallback and the git
// previews get the same wash the side-by-side view paints — additions
// and deletions carry their derived row tint as background while the
// foreground keeps its change color. Low-color palettes opt out and
// keep the passed background untouched.
func TestDiffLineStyle_TintsChangedRows(t *testing.T) {
	th := theme.Default()
	tints, ok := th.DiffTints()
	if !ok {
		t.Fatal("test theme must yield tints")
	}
	if _, bg, _ := DiffLineStyle(th, th.LineHL, "+added").Decompose(); bg != tints.AddRow {
		t.Fatalf("addition bg = %v, want the AddRow tint %v", bg, tints.AddRow)
	}
	if _, bg, _ := DiffLineStyle(th, th.LineHL, "-gone").Decompose(); bg != tints.DelRow {
		t.Fatalf("deletion bg = %v, want the DelRow tint %v", bg, tints.DelRow)
	}
	if _, bg, _ := DiffLineStyle(th, th.LineHL, " context").Decompose(); bg != th.LineHL {
		t.Fatalf("context bg = %v, want the passed surface", bg)
	}
	low := th
	low.LowColor = true
	if _, bg, _ := DiffLineStyle(low, low.LineHL, "+added").Decompose(); bg != low.LineHL {
		t.Fatalf("low-color bg = %v, must keep the passed surface", bg)
	}
}
