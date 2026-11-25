// =============================================================================
// File: internal/app/esctty_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/dnd"
)

// TestRewriteDoubleEsc_PairBecomesAltEsc pins the whole point of the
// filter: two adjacent ESC bytes with nothing sequence-like after them
// turn into the CSI-u encoding of Alt+Esc, which tcell parses as one
// KeyEsc|ModAlt event and handleKey treats as "open the menu". Without
// the rewrite tcell folds the pair into a single plain Esc and the
// second tap is gone.
func TestRewriteDoubleEsc_PairBecomesAltEsc(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare pair", "\x1b\x1b", altEscSeq},
		{"pair then rune", "\x1b\x1bs", altEscSeq + "s"},
		{"rune then pair", "a\x1b\x1b", "a" + altEscSeq},
		{"pair then pair", "\x1b\x1b\x1b\x1b", altEscSeq + altEscSeq},
		{"triple: pair then lone", "\x1b\x1b\x1b", altEscSeq + "\x1b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(rewriteDoubleEsc([]byte(c.in))); got != c.want {
				t.Fatalf("rewriteDoubleEsc(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRewriteDoubleEsc_LeavesSequencesAlone guards the false positives:
// a second ESC that introduces a control sequence (CSI `[`, SS3 `O`,
// OSC `]`, DCS `P`, APC `_`, PM `^`, SOS `X`) is the terminal talking,
// not a second tap, and a lone ESC or a normal Alt+rune chord must pass
// through byte-for-byte.
func TestRewriteDoubleEsc_LeavesSequencesAlone(t *testing.T) {
	cases := []string{
		"",
		"\x1b",
		"\x1bs",            // Alt+s as terminals send it
		"\x1b[A",           // cursor up
		"\x1b\x1b[A",       // Alt+Up on terminals that prefix with ESC
		"\x1b\x1bOA",       // same, SS3 form
		"\x1b\x1b]52;c;\a", // an OSC after a stray ESC
		"\x1b\x1bP1$r\x1b\\",
		"\x1b\x1b_x\x1b\\",
		"\x1b\x1b^x\x1b\\",
		"\x1b\x1bXx\x1b\\",
		"plain text",
	}
	for _, in := range cases {
		if got := string(rewriteDoubleEsc([]byte(in))); got != in {
			t.Fatalf("rewriteDoubleEsc(%q) = %q, want unchanged", in, got)
		}
	}
}

// fakeTty is the minimum tcell.Tty an escTty can wrap in a test: a
// reader for the bytes and no-op lifecycle methods.
type fakeTty struct {
	io.Reader
}

func (fakeTty) Start() error                { return nil }
func (fakeTty) Stop() error                 { return nil }
func (fakeTty) Drain() error                { return nil }
func (fakeTty) Write(p []byte) (int, error) { return len(p), nil }
func (fakeTty) Close() error                { return nil }
func (fakeTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: 80, Height: 24}, nil
}
func (fakeTty) NotifyResize(func()) {}

// TestEscTty_ReadRewritesAndGrows checks the wrapper end to end: the
// rewritten stream is longer than the raw one (CSI-u is 7 bytes for a
// 2-byte pair), so Read must hand back the expansion across calls
// rather than truncating it to the caller's buffer.
func TestEscTty_ReadRewritesAndGrows(t *testing.T) {
	raw := "\x1b\x1b\x1b\x1bq"
	tty := &escTty{Tty: fakeTty{bytes.NewBufferString(raw)}}
	var out []byte
	buf := make([]byte, 5) // deliberately smaller than one expansion
	for {
		n, err := tty.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	want := altEscSeq + altEscSeq + "q"
	if string(out) != want {
		t.Fatalf("escTty stream = %q, want %q", out, want)
	}
}

// TestEscTty_MergesPairSplitAcrossReads is the tmux case: the two ESC
// bytes of a fast double tap arrive as two writes a few milliseconds
// apart. The wrapper must wait briefly after a chunk that ends on a lone
// ESC and rewrite the two chunks together into one Alt+Esc.
func TestEscTty_MergesPairSplitAcrossReads(t *testing.T) {
	r := newGatedReader([]byte("\x1b"), []byte("\x1bs"))
	tty := &escTty{Tty: fakeTty{r}, after: func(time.Duration) <-chan time.Time { return nil }}
	r.release() // first chunk
	r.release() // second chunk is already waiting when the wait starts
	buf := make([]byte, 32)
	n, err := tty.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != altEscSeq+"s" {
		t.Fatalf("merged stream = %q, want %q", got, altEscSeq+"s")
	}
}

// TestEscTty_LoneEscIsDeliveredAfterTheWindow pins the bound on the
// hold: when nothing follows a lone ESC inside escMergeWindow, the ESC
// is delivered as-is rather than waiting for the next keystroke, so a
// single Esc keeps closing menus and arming the leader on time.
func TestEscTty_LoneEscIsDeliveredAfterTheWindow(t *testing.T) {
	r := newGatedReader([]byte("a\x1b"), []byte("s"))
	fired := make(chan time.Time, 1)
	tty := &escTty{Tty: fakeTty{r}, after: func(time.Duration) <-chan time.Time { return fired }}
	r.release()
	fired <- time.Now() // the window elapses with no second chunk
	buf := make([]byte, 32)
	n, err := tty.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != "a\x1b" {
		t.Fatalf("lone esc stream = %q, want %q", got, "a\x1b")
	}
	r.release()
	n, _ = tty.Read(buf)
	if got := string(buf[:n]); got != "s" {
		t.Fatalf("follow-up stream = %q, want %q", got, "s")
	}
}

// TestEscTty_ReaderPanicBecomesError guards the one goroutine that does
// not run under safeGo: a panic in the wrapped tty's Read must surface
// as a Read error (which tcell turns into an EventError), not crash.
func TestEscTty_ReaderPanicBecomesError(t *testing.T) {
	tty := &escTty{Tty: fakeTty{panicReader{}}}
	buf := make([]byte, 8)
	_, err := tty.Read(buf)
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v, want a panicked error", err)
	}
}

// panicReader panics on Read.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

// gatedReader hands out its chunks one per release() call, blocking the
// reader goroutine in between so a test controls the split exactly.
type gatedReader struct {
	chunks [][]byte
	gate   chan struct{}
}

func newGatedReader(chunks ...[]byte) *gatedReader {
	return &gatedReader{chunks: chunks, gate: make(chan struct{}, len(chunks))}
}

func (g *gatedReader) release() { g.gate <- struct{}{} }

func (g *gatedReader) Read(p []byte) (int, error) {
	if len(g.chunks) == 0 {
		return 0, io.EOF
	}
	<-g.gate
	n := copy(p, g.chunks[0])
	g.chunks = g.chunks[1:]
	return n, nil
}

// ttyRec is a sink that records what the filter lifted and how many
// messages it reported lost. It is only ever called on Read's goroutine.
type ttyRec struct {
	bodies []string
	lost   int
}

// Ingest records one message body.
func (r *ttyRec) Ingest(body []byte) { r.bodies = append(r.bodies, string(body)) }

// Lost counts one abandoned message.
func (r *ttyRec) Lost() { r.lost++ }

// sinkOf wires a recording sink onto tty and returns the bodies it
// collects. The sink runs on whichever goroutine calls Read — the
// test's own — so no lock is needed.
func sinkOf(tty *escTty) *[]string {
	rec := &ttyRec{}
	tty.SetSink(rec)
	return &rec.bodies
}

// readOnce performs one Read and returns what it delivered.
func readOnce(t *testing.T, tty *escTty) string {
	t.Helper()
	buf := make([]byte, 256)
	n, err := tty.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read: %v", err)
	}
	return string(buf[:n])
}

// neverFires is the clock seam for tests where no wait may time out: a
// Read that depended on one would hang, which is the failure.
func neverFires(time.Duration) <-chan time.Time { return nil }

// TestEscTty_LiftsDragAndDropMessages pins the filter's second job: an
// OSC 72 message never reaches tcell — it goes to the sink — and the
// input around it is delivered in order, untouched.
func TestEscTty_LiftsDragAndDropMessages(t *testing.T) {
	r := newGatedReader([]byte("ab\x1b]72;t=m:x=1:y=2\x1b\\cd"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	got := sinkOf(tty)
	r.release()
	if out := readOnce(t, tty); out != "abcd" {
		t.Errorf("delivered %q, want %q", out, "abcd")
	}
	if len(*got) != 1 || (*got)[0] != "t=m:x=1:y=2" {
		t.Errorf("sink got %q, want the one message body", *got)
	}
}

// TestEscTty_MessageSplitAcrossReads pins the common shape of file
// data: the tty hands over 256 bytes at a time, so a 4096-byte chunk
// arrives over many reads. The pieces must be joined below tcell and
// only the byte after the terminator delivered.
func TestEscTty_MessageSplitAcrossReads(t *testing.T) {
	r := newGatedReader([]byte("\x1b]72;t=r;QU"), []byte("JD"), []byte("RUZH\x1b\\k"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	got := sinkOf(tty)
	r.release()
	r.release()
	r.release()
	if out := readOnce(t, tty); out != "k" {
		t.Errorf("delivered %q, want %q", out, "k")
	}
	if len(*got) != 1 || (*got)[0] != "t=r;QUJDRUZH" {
		t.Errorf("sink got %q, want the joined body", *got)
	}
}

// TestEscTty_ProtocolOnlyChunkIsNotAnEmptyRead pins that a chunk which
// was nothing but a message does not surface as a zero-byte Read: the
// wrapper keeps reading until there is input to hand tcell.
func TestEscTty_ProtocolOnlyChunkIsNotAnEmptyRead(t *testing.T) {
	r := newGatedReader([]byte("\x1b]72;t=A\x1b\\"), []byte("x"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	got := sinkOf(tty)
	r.release()
	r.release()
	if out := readOnce(t, tty); out != "x" {
		t.Errorf("delivered %q, want %q", out, "x")
	}
	if len(*got) != 1 {
		t.Errorf("sink got %q, want one message", *got)
	}
}

// TestEscTty_InputBeforeAMessageIsNotHeldForIt pins responsiveness: a
// key that arrives in the same read as the START of a message is
// delivered at once, not parked until the message's last chunk shows up.
func TestEscTty_InputBeforeAMessageIsNotHeldForIt(t *testing.T) {
	r := newGatedReader([]byte("x\x1b]72;t=r;QU"), []byte("JD\x1b\\"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	got := sinkOf(tty)
	r.release()
	if out := readOnce(t, tty); out != "x" {
		t.Fatalf("delivered %q, want %q before the message completed", out, "x")
	}
	if len(*got) != 0 {
		t.Fatalf("sink got %q before the terminator arrived", *got)
	}
	r.release()
	readOnce(t, tty) // the rest of the message, then EOF
	if len(*got) != 1 || (*got)[0] != "t=r;QUJD" {
		t.Errorf("sink got %q, want the completed body", *got)
	}
}

// TestEscTty_ATruncatedMessageEndsAtTheNextKey is the fence around the
// worst failure this filter could have: a message whose terminator
// never arrives must not go on swallowing the user's typing. There is
// no clock on it — a pause is far more often a stalled network, and
// then the rest does arrive — so what ends it is the stream itself: the
// first control byte or escape sequence, which is any Enter, Esc, arrow
// or mouse event. That key is delivered, and the loss is reported.
func TestEscTty_ATruncatedMessageEndsAtTheNextKey(t *testing.T) {
	r := newGatedReader([]byte("\x1b]72;t=r;QUJD"), []byte("\rk"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	rec := &ttyRec{}
	tty.SetSink(rec)
	r.release()
	r.release()
	if out := readOnce(t, tty); out != "\rk" {
		t.Errorf("delivered %q, want the Enter and the key after it", out)
	}
	if len(rec.bodies) != 0 || rec.lost != 1 {
		t.Errorf("bodies = %q, lost = %d; want no message and one reported loss", rec.bodies, rec.lost)
	}
}

// TestEscTty_AMessageInProgressHasNoDeadline pins that the filter never
// puts a timer on a message it is in the middle of: it waits on the
// tty and nothing else. (The first version gave up after two seconds,
// which turned an ordinary network stall into base64 typed into the
// open file.)
func TestEscTty_AMessageInProgressHasNoDeadline(t *testing.T) {
	r := newGatedReader([]byte("\x1b]72;t=r;QUJD"), []byte("RUZH\x1b\\k"))
	waits := make(chan time.Duration, 8)
	tty := &escTty{Tty: fakeTty{r}, after: func(d time.Duration) <-chan time.Time {
		waits <- d
		return nil
	}}
	got := sinkOf(tty)
	r.release()
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := tty.Read(buf)
		done <- string(buf[:n])
	}()
	time.Sleep(30 * time.Millisecond) // the stream is stalled mid-message
	select {
	case d := <-waits:
		t.Fatalf("the filter armed a %v timer on a message in progress", d)
	default:
	}
	r.release()
	if out := <-done; out != "k" {
		t.Errorf("delivered %q, want %q", out, "k")
	}
	if len(*got) != 1 || (*got)[0] != "t=r;QUJDRUZH" {
		t.Errorf("sink got %q, want the message whole", *got)
	}
}

// TestEscTty_PastedBytesAreNotLifted pins the injection fence at the
// tty: a forged protocol message inside a bracketed paste reaches tcell
// as the paste content it is, and the sink never hears of it.
func TestEscTty_PastedBytesAreNotLifted(t *testing.T) {
	const paste = "\x1b[200~\x1b]72;t=M:x=1:y=1:o=1;text/uri-list\x1b\\\x1b[201~"
	r := newGatedReader([]byte(paste))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	got := sinkOf(tty)
	r.release()
	if out := readOnce(t, tty); out != paste {
		t.Errorf("delivered %q, want the paste untouched", out)
	}
	if len(*got) != 0 {
		t.Errorf("sink got %q from inside a paste", *got)
	}
}

// TestEscTty_NoSinkDropsMessages pins the state before the app has
// attached: messages are still lifted out — they must never reach
// tcell — and simply go nowhere.
func TestEscTty_NoSinkDropsMessages(t *testing.T) {
	r := newGatedReader([]byte("a\x1b]72;t=m:x=1:y=1\x1b\\b"))
	tty := &escTty{Tty: fakeTty{r}, after: neverFires}
	r.release()
	if out := readOnce(t, tty); out != "ab" {
		t.Errorf("delivered %q, want %q", out, "ab")
	}
}

// overlapTty is a tty whose Write notices a second writer entering
// while one is still inside.
type overlapTty struct {
	fakeTty
	inside   atomic.Int32
	overlaps atomic.Int32
	writes   atomic.Int32
}

// Write records the call and whether it overlapped another.
func (o *overlapTty) Write(p []byte) (int, error) {
	if o.inside.Add(1) > 1 {
		o.overlaps.Add(1)
	}
	time.Sleep(time.Millisecond)
	o.inside.Add(-1)
	o.writes.Add(1)
	return len(p), nil
}

// TestEscTty_WritesAreSerialized pins the guarantee the drop worker
// relies on: its requests go out through the same Write tcell flushes
// frames through, one whole call at a time, so a request can never land
// in the middle of a frame's escape sequence.
func TestEscTty_WritesAreSerialized(t *testing.T) {
	under := &overlapTty{}
	tty := &escTty{Tty: under}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := tty.Write([]byte("\x1b]72;t=r:x=1\x1b\\")); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if n := under.overlaps.Load(); n != 0 {
		t.Errorf("%d writes overlapped another", n)
	}
	if n := under.writes.Load(); n != 40 {
		t.Errorf("%d writes reached the tty, want 40", n)
	}
}

// feedTty is a tty the test writes into as the "network": Read blocks
// while it is empty, so a test decides exactly where the stream stalls.
type feedTty struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	eof  bool
}

// newFeedTty returns an empty, open stream.
func newFeedTty() *feedTty {
	f := &feedTty{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// feed makes b available to the reader.
func (f *feedTty) feed(b []byte) {
	f.mu.Lock()
	f.buf = append(f.buf, b...)
	f.cond.Broadcast()
	f.mu.Unlock()
}

// end closes the stream once what was fed has been read.
func (f *feedTty) end() {
	f.mu.Lock()
	f.eof = true
	f.cond.Broadcast()
	f.mu.Unlock()
}

// Read hands out what has been fed, blocking while there is nothing.
func (f *feedTty) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.buf) == 0 && !f.eof {
		f.cond.Wait()
	}
	if len(f.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.buf)
	f.buf = f.buf[n:]
	return n, nil
}

// drained waits until the reader has taken everything fed so far and
// the filter has had a moment to act on it.
func (f *feedTty) drained() {
	for {
		f.mu.Lock()
		empty := len(f.buf) == 0
		f.mu.Unlock()
		if empty {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
}

// screenBytes collects everything the filter hands the screen library —
// which is to say, everything that would be parsed as keys.
type screenBytes struct {
	mu  sync.Mutex
	buf []byte
}

// drain reads tty until it ends, recording what comes out.
func (c *screenBytes) drain(tty *escTty) {
	buf := make([]byte, 128)
	for {
		n, err := tty.Read(buf)
		c.mu.Lock()
		c.buf = append(c.buf, buf[:n]...)
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// String returns what has reached the screen library so far.
func (c *screenBytes) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// kittyMsg spells one data message the way kitty sends file contents:
// the full header, m=0, and the block base64-encoded on its own with no
// padding.
func kittyMsg(keys string, data []byte) []byte {
	return []byte("\x1b]72;t=r:" + keys + ":m=0;" + base64.RawStdEncoding.EncodeToString(data) + "\x1b\\")
}

// kittyEnd is kitty's end-of-data marker: the header alone.
func kittyEnd(keys string) []byte { return []byte("\x1b]72;t=r:" + keys + "\x1b\\") }

// stalledTransfer plays an in-band transfer of a three-block file
// through the real chain — escTty, dnd.Conn, dnd.Fetch — with the
// stream stalling cut bytes into the middle block's message. When
// windowElapses is set the stall outlasts the lone-ESC window. It
// returns what Fetch staged, Fetch's error, and what reached the
// screen library.
func stalledTransfer(t *testing.T, cut int, windowElapses bool) (file []byte, typed string, fetchErr error) {
	t.Helper()
	in := newFeedTty()
	window := make(chan time.Time)
	tty := &escTty{Tty: fakeTty{in}, after: func(time.Duration) <-chan time.Time { return window }}
	conn := dnd.NewConn(io.Discard, nil)
	tty.SetSink(conn)
	var screen screenBytes
	drained := make(chan struct{})
	go func() {
		screen.drain(tty)
		close(drained)
	}()

	inbox := conn.Attach()
	defer inbox.Close()
	stage := t.TempDir()
	type outcome struct {
		res dnd.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := dnd.Fetch(context.Background(), inbox, dnd.Request{
			MIMEs:  []string{dnd.MIMEURIList},
			Stage:  func() (string, error) { return os.MkdirTemp(stage, "s-") },
			Limits: dnd.Limits{MaxEntries: 10, MaxDepth: 4, Idle: 5 * time.Second},
		})
		done <- outcome{res, err}
	}()

	blockA := bytes.Repeat([]byte{'A'}, 3072)
	blockB := bytes.Repeat([]byte{'B'}, 3072)
	blockC := bytes.Repeat([]byte{'C'}, 3071) // an odd tail: the last block is rarely full
	in.feed(kittyMsg("x=1:X=1", []byte("file:///remote/big.bin\n")))
	in.feed(kittyEnd("x=1:X=1"))
	in.feed(kittyMsg("x=1:y=1", blockA))
	middle := kittyMsg("x=1:y=1", blockB)
	in.feed(middle[:cut])
	in.drained()
	if windowElapses {
		select {
		case window <- time.Now():
		case <-time.After(3 * time.Second):
			t.Fatal("the filter was not waiting on the lone-ESC window")
		}
		time.Sleep(20 * time.Millisecond)
	} else {
		select {
		case window <- time.Now():
			t.Fatalf("cut %d: the filter put a timer on bytes that are not a lone ESC", cut)
		default:
		}
	}
	in.feed(middle[cut:])
	in.feed(kittyMsg("x=1:y=1", blockC))
	in.feed(kittyEnd("x=1:y=1"))

	var o outcome
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Fetch never returned")
	}
	in.end()
	<-drained
	if len(o.res.Staged) == 1 {
		file, _ = os.ReadFile(o.res.Staged[0])
	}
	return file, screen.String(), o.err
}

// TestEscTty_AStallAnywhereInAMessageCorruptsNothing is the end-to-end
// fence for in-band transfers over a real network. The stream stalls —
// inside the introducer, inside the base64, inside the terminator — and
// in every case the file must arrive byte for byte and not one byte of
// it may reach the screen library, where it would be typed into the
// open buffer. This is the case the first version failed.
func TestEscTty_AStallAnywhereInAMessageCorruptsNothing(t *testing.T) {
	want := append(append(bytes.Repeat([]byte{'A'}, 3072), bytes.Repeat([]byte{'B'}, 3072)...), bytes.Repeat([]byte{'C'}, 3071)...)
	middleLen := len(kittyMsg("x=1:y=1", bytes.Repeat([]byte{'B'}, 3072)))
	for _, cut := range []int{2, 3, 4, 5, 6, 20, 2000, middleLen - 2, middleLen - 1} {
		file, typed, err := stalledTransfer(t, cut, false)
		if err != nil {
			t.Errorf("cut %d: Fetch failed: %v", cut, err)
			continue
		}
		if !bytes.Equal(file, want) {
			t.Errorf("cut %d: staged %d bytes that differ from the %d sent", cut, len(file), len(want))
		}
		if typed != "" {
			t.Errorf("cut %d: %d bytes reached the screen library: %q…", cut, len(typed), typed[:min(len(typed), 24)])
		}
	}
}

// TestEscTty_AStallRightAfterEscStillDeliversTheMessage pins the one
// cut the filter cannot wait out: a lone ESC might be the Esc key, so
// it is released when the window elapses. The message it began must
// still arrive whole — the cost is one spurious Esc keypress, never a
// lost block or typed base64.
func TestEscTty_AStallRightAfterEscStillDeliversTheMessage(t *testing.T) {
	want := append(append(bytes.Repeat([]byte{'A'}, 3072), bytes.Repeat([]byte{'B'}, 3072)...), bytes.Repeat([]byte{'C'}, 3071)...)
	file, typed, err := stalledTransfer(t, 1, true)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if !bytes.Equal(file, want) {
		t.Errorf("staged %d bytes that differ from the %d sent", len(file), len(want))
	}
	if typed != "\x1b" {
		t.Errorf("the screen library received %q, want only the released ESC", typed)
	}
}

// TestEscTty_ATruncatedMessageFailsTheTransfer pins the loud failure: a
// message cut off at the source leaves a hole, and Fetch must say so
// instead of staging a short file as if it were whole.
func TestEscTty_ATruncatedMessageFailsTheTransfer(t *testing.T) {
	in := newFeedTty()
	tty := &escTty{Tty: fakeTty{in}, after: neverFires}
	conn := dnd.NewConn(io.Discard, nil)
	tty.SetSink(conn)
	var screen screenBytes
	go screen.drain(tty)

	inbox := conn.Attach()
	defer inbox.Close()
	stage := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := dnd.Fetch(context.Background(), inbox, dnd.Request{
			MIMEs:  []string{dnd.MIMEURIList},
			Stage:  func() (string, error) { return os.MkdirTemp(stage, "s-") },
			Limits: dnd.Limits{MaxEntries: 10, MaxDepth: 4, Idle: 5 * time.Second},
		})
		done <- err
	}()
	in.feed(kittyMsg("x=1:X=1", []byte("file:///remote/big.bin\n")))
	in.feed(kittyEnd("x=1:X=1"))
	whole := kittyMsg("x=1:y=1", bytes.Repeat([]byte{'A'}, 3072))
	in.feed(whole[:1000]) // the terminal stops mid-message…
	in.feed([]byte("\r")) // …and the user presses Enter
	select {
	case err := <-done:
		if !errors.Is(err, dnd.ErrInterrupted) {
			t.Errorf("Fetch err = %v, want ErrInterrupted", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Fetch did not notice the truncated message")
	}
	in.drained()
	in.end()
	if got := screen.String(); got != "\r" {
		t.Errorf("the screen library received %q, want only the Enter", got)
	}
}

// preDropFilter is the Read loop as it was before the filter learned
// about drag-and-drop, frozen here as the reference: a chunk that ends
// on ESC is joined with the next one, and the double-ESC rewrite runs
// on the result. It models the case where the next chunk is already
// waiting, which is the only case that has no clock in it.
func preDropFilter(chunks [][]byte) []byte {
	var out []byte
	for i := 0; i < len(chunks); i++ {
		raw := append([]byte(nil), chunks[i]...)
		if len(raw) > 0 && raw[len(raw)-1] == 0x1b && i+1 < len(chunks) {
			i++
			raw = append(raw, chunks[i]...)
		}
		out = append(out, rewriteDoubleEsc(raw)...)
	}
	return out
}

// TestEscTty_OrdinaryInputIsUnchangedByTheScanner is the regression
// fence for every keystroke. Random input — escape sequences, runs of
// ESC, text, cut into random chunks — must come out of the new filter
// byte for byte as it came out of the old one. The alphabet leaves out
// ';' and '~' so no drag-and-drop introducer or paste marker can ever
// complete: this is purely "the scanner in the path changes nothing".
func TestEscTty_OrdinaryInputIsUnchangedByTheScanner(t *testing.T) {
	alphabet := []byte{0x1b, 0x1b, 0x1b, '[', 'O', 'A', 'a', ']', '7', '2', '0', 'x', '\\'}
	rng := rand.New(rand.NewSource(20261124))
	for round := 0; round < 3000; round++ {
		var chunks [][]byte
		for n := 1 + rng.Intn(6); n > 0; n-- {
			chunk := make([]byte, 1+rng.Intn(5))
			for i := range chunk {
				chunk[i] = alphabet[rng.Intn(len(alphabet))]
			}
			chunks = append(chunks, chunk)
		}
		r := newGatedReader(chunks...)
		for range chunks {
			r.release()
		}
		tty := &escTty{Tty: fakeTty{r}, after: neverFires}
		var got []byte
		buf := make([]byte, 64)
		for {
			n, err := tty.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		if want := preDropFilter(chunks); !bytes.Equal(got, want) {
			t.Fatalf("round %d, chunks %q:\n got %q\nwant %q", round, chunks, got, want)
		}
	}
}
