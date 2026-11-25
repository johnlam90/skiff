// =============================================================================
// File: internal/app/esctty.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// esctty.go makes a fast double-tap of Esc survive tcell's input parser.
//
// tcell treats a second ESC byte that arrives while it is still deciding
// what the first one meant as an "alt prefix": it sets a flag and keeps
// waiting, and when its 50 ms timer fires it posts ONE plain KeyEsc —
// the flag is never consulted. So "Esc Esc" tapped faster than ~100 ms
// reaches handleKey as a single Esc, and the gesture the README and the
// empty-editor hint both teach for opening the menu does nothing. The
// slow double-tap works because the two bytes fall in different parser
// windows; the fast one, which is what a thumb on a phone produces, is
// exactly the one that is lost. Measured, not guessed: the same pair
// under tmux with escape-time 0, 10 and 500 all deliver one event.
//
// The fix sits below tcell, on the tty byte stream: every adjacent ESC
// ESC pair that is not introducing a control sequence is rewritten into
// the CSI-u encoding of Alt+Esc, which tcell parses into one KeyEsc with
// ModAlt — the event handleKey ALREADY treats as "open the menu" because
// tmux with a non-zero escape-time produces it for the same gesture. No
// new key vocabulary, no timing heuristics: the terminal's bytes are
// made to say what the user did.
//
// The pair rarely arrives in one read: tmux and SSH clients write each
// keystroke separately, so a chunk that ends on a lone ESC is held for
// at most escMergeWindow (60 ms, just past tcell's own 50 ms timer) to see
// whether the next chunk begins with the second tap. A lone Esc is never
// held longer than that, so closing a menu or arming the leader is not
// perceptibly slower and a rune typed after the window cannot complete
// an Esc chord the leader had already expired.
//
// The same filter has a second job, for the same reason it has the
// first: it is the only place below tcell's parser. Drag-and-drop
// messages (OSC 72, internal/dnd) are lifted out of the stream here and
// handed to a sink, because a dropped file arrives as megabytes of
// base64 that tcell must never see — parsed, it is wasted work; given
// up on, it is keystrokes typed into the open buffer. The lone-ESC
// window above is the ONLY clock in this file: a message that has
// started is waited for however long the network takes, because the
// rest of it is what the stream delivers next. And every write to the
// tty goes through this wrapper's mutex, so the worker fetching a drop
// can send a request without it landing inside a frame.

package app

import (
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/dnd"
)

// altEscSeq is the CSI-u (kitty keyboard protocol) form of Esc with the
// Alt modifier: key 27, modifier field 3 (= 1 + Alt's bit 2). tcell's
// parser maps it to KeyEsc|ModAlt via csiUKeys.
const altEscSeq = "\x1b[27;3u"

// sequenceIntroducers are the bytes that, after an ESC, mean the terminal
// is sending a control sequence rather than the user tapping Esc again:
// CSI, SS3, OSC, DCS, APC, PM and SOS. A second ESC followed by one of
// these is left alone so an "ESC-prefixed" Alt+Arrow or a stray ESC
// before an OSC reply still parse as the terminal intended.
const sequenceIntroducers = "[O]P_^X"

// rewriteDoubleEsc returns b with every ESC ESC pair that does not
// introduce a control sequence replaced by altEscSeq. A pair at the very
// end of b counts as a double tap: nothing sequence-like can follow it
// inside this chunk. Pairs are consumed left to right and never overlap,
// so three ESCs are one pair plus one lone ESC.
func rewriteDoubleEsc(b []byte) []byte {
	if len(b) < 2 {
		return b
	}
	out := make([]byte, 0, len(b)+len(altEscSeq))
	for i := 0; i < len(b); i++ {
		if b[i] != 0x1b || i+1 >= len(b) || b[i+1] != 0x1b {
			out = append(out, b[i])
			continue
		}
		if i+2 < len(b) && isSequenceIntroducer(b[i+2]) {
			// ESC ESC [ ... — the second ESC starts a sequence.
			out = append(out, b[i])
			continue
		}
		out = append(out, altEscSeq...)
		i++
	}
	return out
}

// isSequenceIntroducer reports whether c opens a control sequence when
// it follows ESC.
func isSequenceIntroducer(c byte) bool {
	for j := 0; j < len(sequenceIntroducers); j++ {
		if sequenceIntroducers[j] == c {
			return true
		}
	}
	return false
}

// escMergeWindow is how long a chunk that ends on a lone ESC waits for
// the next chunk before being delivered. tmux and SSH clients write
// each keystroke separately, so a double tap arrives as two reads a few
// milliseconds apart; without the wait the rewrite never sees the pair.
// The window is deliberately LONGER than tcell's 50 ms escape timer: a
// shorter one leaves a band (taps 40-50 ms apart, measured under tmux)
// where the ESC we release lands inside tcell's still-open timer and is
// folded into one plain Esc again. A lone Esc therefore reaches
// handleKey ~60 ms later than before — under tmux's own default 500 ms
// escape-time, and well under what a menu close can show.
const escMergeWindow = 60 * time.Millisecond

// escChunk is one result of the background read: the bytes and the error
// that ended the read, if any.
type escChunk struct {
	b   []byte
	err error
}

// escTty wraps a tcell.Tty and rewrites the bytes it reads through
// rewriteDoubleEsc. A background goroutine does the blocking reads so
// Read can wait a bounded escMergeWindow for the second half of a split
// pair; the goroutine is the one goroutine in skiff that does not go
// through App.safeGo, because it exists before the App does. A panic in
// it is recovered into an error, which tcell's input loop turns into an
// EventError rather than a crash. Because the rewrite can grow the
// stream, output that does not fit the caller's buffer is kept in
// pending and handed back on the following Read calls.
//
// Read is called by one goroutine, tcell's input loop, and scan belongs
// to it. Write and SetSink are safe from any goroutine.
type escTty struct {
	tcell.Tty
	pending    []byte
	pendingErr error
	chunks     chan escChunk
	once       sync.Once
	// now and after are seams for tests; nil means the real clock.
	after func(time.Duration) <-chan time.Time

	// scan lifts drag-and-drop messages out of the stream before the
	// double-Esc rewrite and tcell ever see them.
	scan dnd.Scanner
	// sink receives what scan lifts, on Read's goroutine. An atomic
	// pointer because the app attaches it after the screen — and with
	// it the reader — is already running.
	sink atomic.Pointer[sinkBox]
	// wmu makes each Write call exclusive. tcell hands a frame over in
	// Write calls that each hold whole escape sequences, so holding
	// this across one is enough to keep a worker's request out of the
	// middle of a sequence.
	wmu sync.Mutex
}

// sinkBox holds a dnd.Sink for the atomic pointer, which needs a
// concrete type to point at.
type sinkBox struct{ s dnd.Sink }

// SetSink sets where lifted drag-and-drop messages go; nil drops them.
// Until it is called they are still lifted out of the stream — they
// must never reach tcell — and simply discarded.
func (t *escTty) SetSink(s dnd.Sink) {
	if s == nil {
		t.sink.Store(nil)
		return
	}
	t.sink.Store(&sinkBox{s})
}

// ttySink is the Sink the scanner is fed: it forwards to whatever the
// app has attached, or to nothing.
type ttySink struct{ t *escTty }

// Ingest forwards one message body.
func (k ttySink) Ingest(body []byte) {
	if b := k.t.sink.Load(); b != nil {
		b.s.Ingest(body)
	}
}

// Lost forwards the news that a message was cut off.
func (k ttySink) Lost() {
	if b := k.t.sink.Load(); b != nil {
		b.s.Lost()
	}
}

// Write sends p to the terminal, one caller at a time.
func (t *escTty) Write(p []byte) (int, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return t.Tty.Write(p)
}

// start launches the reader goroutine exactly once.
func (t *escTty) start() {
	t.once.Do(func() {
		t.chunks = make(chan escChunk, 4)
		go t.readLoop()
	})
}

// readLoop feeds chunks from the wrapped tty until a read fails, then
// delivers the error and exits. Stop/Drain on the wrapped tty end the
// blocking read with an error, so the loop cannot outlive the screen.
func (t *escTty) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			t.chunks <- escChunk{err: fmt.Errorf("skiff: tty reader panicked: %v", r)}
		}
		close(t.chunks)
	}()
	for {
		buf := make([]byte, 256)
		n, err := t.Tty.Read(buf)
		t.chunks <- escChunk{b: buf[:n], err: err}
		if err != nil {
			return
		}
	}
}

// wait returns a channel that fires after d, through the test seam when
// one is set.
func (t *escTty) wait(d time.Duration) <-chan time.Time {
	if t.after != nil {
		return t.after(d)
	}
	return time.After(d)
}

// Read implements io.Reader over the wrapped tty with drag-and-drop
// messages lifted out and the ESC ESC rewrite applied. The tty is only
// consulted when nothing rewritten is still waiting, so bytes are
// delivered in order.
func (t *escTty) Read(p []byte) (int, error) {
	t.start()
	if len(t.pending) == 0 {
		pass, err := t.fill()
		t.pending = rewriteDoubleEsc(pass)
		if len(t.pending) == 0 {
			return 0, err
		}
		// Deliver what was read before surfacing the error on the
		// next call, so no byte is lost to a trailing EOF.
		t.pendingErr = err
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	if len(t.pending) == 0 && t.pendingErr != nil {
		err := t.pendingErr
		t.pendingErr = nil
		return n, err
	}
	return n, nil
}

// fill blocks until there is input to hand tcell and returns it with
// every drag-and-drop message removed. There is exactly one bounded
// wait in it:
//
//   - a chunk that ends on a lone ESC is held for one escMergeWindow.
//     That is the original double-tap wait — long enough to see whether
//     the second tap, or the rest of an escape sequence, follows — and
//     whatever is still a lone ESC once the next chunk has arrived is
//     released rather than waited on again;
//   - input already in hand is returned at once, even with a message
//     half-received behind it: a key must not wait on a file;
//   - otherwise it blocks on the tty, exactly as a plain read would —
//     including in the middle of a message. A stalled message is never
//     given up on: what the stream delivers after a stall is the rest
//     of it, and abandoning the capture would hand those bytes to tcell
//     as typed text. See dnd.Scanner for what does end a message early.
func (t *escTty) fill() ([]byte, error) {
	sink := ttySink{t}
	var pass []byte
	for {
		var window <-chan time.Time
		held := t.scan.Held()
		switch {
		case held:
			window = t.wait(escMergeWindow)
		case len(pass) > 0:
			return pass, nil
		}
		select {
		case c, ok := <-t.chunks:
			if !ok {
				return append(pass, t.scan.Flush()...), io.EOF
			}
			pass = append(pass, t.scan.Feed(c.b, sink)...)
			if held {
				pass = append(pass, t.scan.Release()...)
			}
			if c.err != nil {
				return append(pass, t.scan.Flush()...), c.err
			}
		case <-window:
			pass = append(pass, t.scan.Release()...)
		}
	}
}

// newDoubleEscScreen builds the terminfo screen over a /dev/tty wrapped
// in escTty. It returns (nil, nil) when the platform has no usable dev
// tty — Windows, or a session with no controlling terminal — so the
// caller falls back to tcell.NewScreen and loses only the double-tap
// rewrite and the drag-and-drop protocol route, never the editor. The
// wrapper is returned beside the screen because the app needs both of
// its other faces: SetSink for the messages it lifts, Write for the
// requests sent back.
func newDoubleEscScreen() (tcell.Screen, *escTty, error) {
	if runtime.GOOS == "windows" {
		return nil, nil, nil
	}
	tty, err := tcell.NewDevTty()
	if err != nil {
		return nil, nil, nil
	}
	wrapped := &escTty{Tty: tty}
	scr, err := tcell.NewTerminfoScreenFromTty(wrapped)
	if err != nil {
		_ = tty.Close()
		return nil, nil, nil
	}
	return scr, wrapped, nil
}
