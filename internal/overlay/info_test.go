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

// TestInfo_DrawTruncatesWithEllipsis pins the truncation marker. The
// body used to be hard-cut with a rune slice, so a clipped stderr path
// read as a complete-but-wrong path — the one failure mode where the
// user is reading the text character by character. Every line now goes
// through trimRunes, which spends the last cell on "…".
func TestInfo_DrawTruncatesWithEllipsis(t *testing.T) {
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
	cells, w, _ := scr.GetContents()
	body := r.W - 4
	row := make([]rune, 0, body)
	for i := range body {
		row = append(row, cells[(r.Y+3)*w+r.X+2+i].Runes[0])
	}
	if got := string(row); !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated body line = %q, want a trailing ellipsis", got)
	}
	// The untruncated neighbour must be left exactly as it came in.
	next := make([]rune, 0, 5)
	for i := range 5 {
		next = append(next, cells[(r.Y+4)*w+r.X+2+i].Runes[0])
	}
	if string(next) != "short" {
		t.Fatalf("short line = %q, want %q", string(next), "short")
	}
}

// TestInfo_DrawKeepsDiffColorAfterTruncation guards the style pick:
// truncation must not repaint a clipped diff line, so the color is
// chosen from the original text and not from the ellipsised copy.
func TestInfo_DrawKeepsDiffColorAfterTruncation(t *testing.T) {
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
	fg, _, _ := cells[(r.Y+3)*w+r.X+2+r.W-5].Style.Decompose()
	if fg != n.Theme.GitAdded {
		t.Fatalf("ellipsis cell fg = %v, want the addition color %v", fg, n.Theme.GitAdded)
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
