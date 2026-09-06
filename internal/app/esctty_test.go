// =============================================================================
// File: internal/app/esctty_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
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
