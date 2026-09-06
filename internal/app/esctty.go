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

package app

import (
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
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
type escTty struct {
	tcell.Tty
	pending    []byte
	pendingErr error
	chunks     chan escChunk
	once       sync.Once
	// now and after are seams for tests; nil means the real clock.
	after func(time.Duration) <-chan time.Time
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

// Read implements io.Reader over the wrapped tty with the ESC ESC
// rewrite applied. The tty is only consulted when nothing rewritten is
// still waiting, so bytes are delivered in order. When the raw chunk
// ends on a lone ESC, Read waits up to escMergeWindow for the next
// chunk and rewrites the two together, so a double tap split across
// writes still becomes one Alt+Esc.
func (t *escTty) Read(p []byte) (int, error) {
	t.start()
	if len(t.pending) == 0 {
		raw, err := t.next()
		if len(raw) > 0 && raw[len(raw)-1] == 0x1b && err == nil {
			// One bounded wait for the other half of a possible pair.
			select {
			case c, ok := <-t.chunks:
				if ok {
					raw = append(raw, c.b...)
					err = c.err
				}
			case <-t.wait(escMergeWindow):
			}
		}
		t.pending = rewriteDoubleEsc(raw)
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

// next blocks for the following raw chunk. A closed channel after the
// reader exited reports EOF so tcell's loop stops cleanly.
func (t *escTty) next() ([]byte, error) {
	c, ok := <-t.chunks
	if !ok {
		return nil, io.EOF
	}
	return c.b, c.err
}

// newDoubleEscScreen builds the terminfo screen over a /dev/tty wrapped
// in escTty. It returns (nil, nil) when the platform has no usable dev
// tty — Windows, or a session with no controlling terminal — so the
// caller falls back to tcell.NewScreen and loses only the double-tap
// rewrite, never the editor.
func newDoubleEscScreen() (tcell.Screen, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	tty, err := tcell.NewDevTty()
	if err != nil {
		return nil, nil
	}
	scr, err := tcell.NewTerminfoScreenFromTty(&escTty{Tty: tty})
	if err != nil {
		_ = tty.Close()
		return nil, nil
	}
	return scr, nil
}
