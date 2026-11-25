// =============================================================================
// File: internal/dnd/scanner_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"reflect"
	"strings"
	"testing"
)

// recSink records what a Scanner lifts out and how many messages it
// gave up on.
type recSink struct {
	bodies []string
	lost   int
}

// Ingest records one message body.
func (r *recSink) Ingest(body []byte) { r.bodies = append(r.bodies, string(body)) }

// Lost counts one abandoned message.
func (r *recSink) Lost() { r.lost++ }

// feedAll pushes chunks through s and returns the bytes it passed on
// and the bodies it lifted out, in order.
func feedAll(s *Scanner, chunks ...string) (pass string, bodies []string) {
	pass, rec := feedRec(s, chunks...)
	return pass, rec.bodies
}

// feedRec is feedAll for a test that also needs to know what was lost.
func feedRec(s *Scanner, chunks ...string) (pass string, rec *recSink) {
	rec = &recSink{}
	var out []byte
	for _, c := range chunks {
		out = append(out, s.Feed([]byte(c), rec)...)
	}
	return string(out), rec
}

// TestScanner_PlainInputPassesThrough is the property every keystroke
// depends on: input that is not an OSC 72 message comes out byte for
// byte, escape sequences included.
func TestScanner_PlainInputPassesThrough(t *testing.T) {
	const in = "hello\r\x1b[A\x1b[<0;10;5M\x1bOP\x1b[200~pasted\x1b[201~"
	var s Scanner
	pass, bodies := feedAll(&s, in)
	if pass != in || bodies != nil {
		t.Errorf("pass = %q, bodies = %q; want the input untouched and no bodies", pass, bodies)
	}
	if s.Held() || s.Capturing() {
		t.Error("scanner left holding state after plain input")
	}
}

// TestScanner_OtherOSCPassesThrough pins that only OSC 72 is lifted.
// tcell reads OSC 52 clipboard replies off this same stream, and an
// OSC whose number merely starts with 72 is somebody else's.
func TestScanner_OtherOSCPassesThrough(t *testing.T) {
	for _, in := range []string{
		"\x1b]52;c;QUJD\x07",
		"\x1b]7;file://host/path\x1b\\",
		"\x1b]720;x\x07",
		"\x1b]7x",
	} {
		var s Scanner
		pass, bodies := feedAll(&s, in)
		if pass != in || bodies != nil {
			t.Errorf("Feed(%q): pass = %q, bodies = %q; want it untouched", in, pass, bodies)
		}
	}
}

// TestScanner_LiftsAMessage pins the core job: the message is removed
// from the stream, its body delivered, and the text around it survives
// in order.
func TestScanner_LiftsAMessage(t *testing.T) {
	var s Scanner
	pass, bodies := feedAll(&s, "ab\x1b]72;t=m:x=1:y=2\x1b\\cd")
	if pass != "abcd" {
		t.Errorf("pass = %q, want %q", pass, "abcd")
	}
	if want := []string{"t=m:x=1:y=2"}; !reflect.DeepEqual(bodies, want) {
		t.Errorf("bodies = %q, want %q", bodies, want)
	}
}

// TestScanner_SplitAnywhere pins that the scanner is a stream: a
// 4096-byte chunk arrives across many 256-byte reads, so a message cut
// at ANY byte — inside the introducer, the body or the terminator —
// must come out the same as one delivered whole.
func TestScanner_SplitAnywhere(t *testing.T) {
	const in = "ab\x1b]72;t=r:x=1;QUJDRA==\x1b\\cd"
	for i := 0; i <= len(in); i++ {
		var s Scanner
		pass, bodies := feedAll(&s, in[:i], in[i:])
		pass += string(s.Flush())
		if pass != "abcd" || !reflect.DeepEqual(bodies, []string{"t=r:x=1;QUJDRA=="}) {
			t.Errorf("split at %d: pass = %q, bodies = %q", i, pass, bodies)
		}
	}
	var s Scanner
	pass, bodies := feedAll(&s, strings.Split(in, "")...)
	if pass != "abcd" || len(bodies) != 1 {
		t.Errorf("byte by byte: pass = %q, bodies = %q", pass, bodies)
	}
}

// TestScanner_BELTerminator pins the other legal string terminator.
func TestScanner_BELTerminator(t *testing.T) {
	var s Scanner
	pass, bodies := feedAll(&s, "\x1b]72;t=A\x07x")
	if pass != "x" || !reflect.DeepEqual(bodies, []string{"t=A"}) {
		t.Errorf("pass = %q, bodies = %q; want x and [t=A]", pass, bodies)
	}
}

// TestScanner_TwoMessagesInOneFeed pins that a burst of hover events in
// one read is two messages, not one swallowing the other.
func TestScanner_TwoMessagesInOneFeed(t *testing.T) {
	var s Scanner
	_, bodies := feedAll(&s, "\x1b]72;t=m:x=1:y=1\x1b\\\x1b]72;t=m:x=2:y=1\x1b\\")
	if want := []string{"t=m:x=1:y=1", "t=m:x=2:y=1"}; !reflect.DeepEqual(bodies, want) {
		t.Errorf("bodies = %q, want %q", bodies, want)
	}
}

// TestScanner_LoneEscIsHeldAndReleased pins the one undecided state a
// clock may end: a chunk that stops on ESC. It might open a message, or
// it might be the Esc key — and nothing in the stream says which — so
// the caller releases it after a short wait and it comes back as the
// byte it was.
func TestScanner_LoneEscIsHeldAndReleased(t *testing.T) {
	var s Scanner
	pass, _ := feedAll(&s, "x\x1b")
	if pass != "x" || !s.Held() {
		t.Fatalf("pass = %q, Held = %v; want x and a held ESC", pass, s.Held())
	}
	if got := string(s.Release()); got != "\x1b" {
		t.Errorf("Release = %q, want the ESC", got)
	}
	if s.Held() {
		t.Error("still holding after Release")
	}
}

// TestScanner_LongerPrefixWaitsForItsNextByte pins what is NOT
// releasable. Once "ESC ]" has arrived the sender is mid-sequence and
// the next byte is on its way, however long the network takes: handing
// those bytes downstream after a pause would make the rest of a message
// arrive as typed text. They wait, and the next byte settles it either
// way.
func TestScanner_LongerPrefixWaitsForItsNextByte(t *testing.T) {
	for _, prefix := range []string{"\x1b]", "\x1b]7", "\x1b]72"} {
		var s Scanner
		pass, _ := feedAll(&s, "x"+prefix)
		if pass != "x" || s.Held() || s.Release() != nil {
			t.Fatalf("%q: pass = %q, Held = %v; want it kept back and not releasable", prefix, pass, s.Held())
		}
		rest := "\x1b]72;t=A\x1b\\"[len(prefix):]
		pass, bodies := feedAll(&s, rest+"k")
		if pass != "k" || !reflect.DeepEqual(bodies, []string{"t=A"}) {
			t.Errorf("%q then the rest: pass = %q, bodies = %q; want the message lifted", prefix, pass, bodies)
		}

		var other Scanner
		feedAll(&other, prefix)
		if pass, bodies := feedAll(&other, "z"); pass != prefix+"z" || bodies != nil {
			t.Errorf("%q then z: pass = %q, bodies = %q; want the prefix handed on in order", prefix, pass, bodies)
		}
	}
}

// TestScanner_IntroducerAfterAReleasedEsc pins the other half of the
// lone-ESC rule. If the stream paused right after the ESC of a message,
// that ESC has already been released as a key — but the rest of the
// introducer then arrives in one piece, which no typist produces, and
// the message must still be lifted rather than typed.
func TestScanner_IntroducerAfterAReleasedEsc(t *testing.T) {
	var s Scanner
	feedAll(&s, "\x1b")
	s.Release()
	pass, bodies := feedAll(&s, "]72;t=r:x=1;QUJD\x1b\\k")
	if pass != "k" || !reflect.DeepEqual(bodies, []string{"t=r:x=1;QUJD"}) {
		t.Errorf("pass = %q, bodies = %q; want the message lifted", pass, bodies)
	}

	// Typed one key at a time, the same characters are text.
	var typed Scanner
	feedAll(&typed, "\x1b")
	typed.Release()
	if pass, bodies := feedAll(&typed, "]", "7", "2", ";"); pass != "]72;" || bodies != nil {
		t.Errorf("typed after Esc: pass = %q, bodies = %q; want plain text", pass, bodies)
	}

	// And the rule covers only what comes straight after the ESC.
	var later Scanner
	feedAll(&later, "\x1b")
	later.Release()
	feedAll(&later, "a")
	if pass, bodies := feedAll(&later, "]72;t=A\x1b\\"); pass != "]72;t=A\x1b\\" || bodies != nil {
		t.Errorf("after other input: pass = %q, bodies = %q; want plain text", pass, bodies)
	}
}

// TestScanner_FlushHandsBackEverythingUndecided pins the end of the
// stream: at EOF nothing more can settle a prefix, so all of it goes
// downstream and no byte is lost.
func TestScanner_FlushHandsBackEverythingUndecided(t *testing.T) {
	var s Scanner
	feedAll(&s, "\x1b]7")
	if got := string(s.Flush()); got != "\x1b]7" {
		t.Errorf("Flush = %q, want the held prefix", got)
	}
	if got := s.Flush(); got != nil {
		t.Errorf("a second Flush = %q, want nothing", got)
	}
}

// TestScanner_DoubleEscStaysTwoEscs pins the interaction with the
// double-tap rewrite that runs after the scanner: two ESC bytes must
// come out as two ESC bytes, in order, however they were chunked.
func TestScanner_DoubleEscStaysTwoEscs(t *testing.T) {
	var s Scanner
	pass, _ := feedAll(&s, "\x1b")
	if pass != "" || !s.Held() {
		t.Fatalf("after one ESC: pass = %q, Held = %v", pass, s.Held())
	}
	pass, _ = feedAll(&s, "\x1b")
	pass += string(s.Release())
	if pass != "\x1b\x1b" {
		t.Errorf("two ESCs came out as %q", pass)
	}
}

// TestScanner_ControlByteAbandonsCapture is half of the stuck-transfer
// escape hatch: a message never contains a control byte, so one inside
// a capture means the sequence was cut off — and the byte the user
// typed (Ctrl-C here) must reach the editor.
func TestScanner_ControlByteAbandonsCapture(t *testing.T) {
	for _, ctl := range []string{"\x03", "\r", "\x7f"} {
		var s Scanner
		pass, rec := feedRec(&s, "\x1b]72;t=r;QUJD", ctl+"more")
		if rec.bodies != nil {
			t.Errorf("%q: bodies = %q, want none from a cut-off message", ctl, rec.bodies)
		}
		if pass != ctl+"more" {
			t.Errorf("%q: pass = %q, want the control byte and what followed", ctl, pass)
		}
		if s.Capturing() {
			t.Errorf("%q: still capturing after a control byte", ctl)
		}
		if rec.lost != 1 {
			t.Errorf("%q: reported %d lost messages, want 1 — a transfer in flight has a hole in it", ctl, rec.lost)
		}
	}
}

// TestScanner_EscNotFollowedByBackslashAbandonsCapture pins the other
// half: ESC inside a body is only ever the start of the terminator, so
// ESC followed by anything else is a new sequence — an arrow key — and
// has to be delivered whole.
func TestScanner_EscNotFollowedByBackslashAbandonsCapture(t *testing.T) {
	var s Scanner
	pass, rec := feedRec(&s, "\x1b]72;t=r;QU\x1b[Az")
	if rec.bodies != nil {
		t.Errorf("bodies = %q, want none", rec.bodies)
	}
	if pass != "\x1b[Az" {
		t.Errorf("pass = %q, want the arrow key and z", pass)
	}
	if rec.lost != 1 {
		t.Errorf("reported %d lost messages, want 1", rec.lost)
	}
}

// TestScanner_OversizeIsDiscardedNotReleased pins the one thing an
// overlong body must never do: come out as input. Released, a megabyte
// of base64 would be typed straight into the open file.
func TestScanner_OversizeIsDiscardedNotReleased(t *testing.T) {
	var s Scanner
	big := strings.Repeat("A", MaxBody+100)
	pass, rec := feedRec(&s, "\x1b]72;t=r;"+big+"\x1b\\z")
	if rec.bodies != nil {
		t.Errorf("an oversize body was delivered (%d bytes)", len(rec.bodies[0]))
	}
	if pass != "z" {
		t.Errorf("pass = %d bytes %q…, want just z", len(pass), pass[:min(len(pass), 12)])
	}
	if rec.lost != 1 {
		t.Errorf("reported %d lost messages, want 1", rec.lost)
	}
}

// TestScanner_BodyAtTheLimitIsDelivered pins the boundary itself, so
// the cap cannot quietly eat a legal chunk.
func TestScanner_BodyAtTheLimitIsDelivered(t *testing.T) {
	var s Scanner
	body := strings.Repeat("A", MaxBody)
	_, bodies := feedAll(&s, "\x1b]72;"+body+"\x1b\\")
	if len(bodies) != 1 || bodies[0] != body {
		t.Errorf("a body of exactly MaxBody bytes was not delivered intact")
	}
}

// TestScanner_APauseInsideAMessageLosesNothing pins the property the
// first version got wrong. The scanner has no clock: however long the
// stream stops in the middle of a message, the rest still belongs to
// that message when it arrives. A network stall is always followed by
// the remainder — the stream is ordered — so "giving up" after a pause
// only ever turned a file's bytes into typed text.
func TestScanner_APauseInsideAMessageLosesNothing(t *testing.T) {
	var s Scanner
	pass, rec := feedRec(&s, "\x1b]72;t=r:x=1;QUJD")
	if pass != "" || !s.Capturing() || s.Held() {
		t.Fatalf("mid-message: pass = %q, Capturing = %v, Held = %v", pass, s.Capturing(), s.Held())
	}
	if got := s.Release(); got != nil {
		t.Fatalf("Release mid-message handed back %q", got)
	}
	out := s.Feed([]byte("RUZH\x1b\\k"), rec)
	if string(out) != "k" || !reflect.DeepEqual(rec.bodies, []string{"t=r:x=1;QUJDRUZH"}) || rec.lost != 0 {
		t.Errorf("after the pause: pass = %q, bodies = %q, lost = %d", out, rec.bodies, rec.lost)
	}
}

// TestScanner_NothingIsLiftedInsideAPaste pins the injection fence.
// Pasted bytes are content, and content must not be able to speak the
// protocol: a paste carrying a forged "a file was dropped" message
// would otherwise make the editor fetch and copy with no bar, no key
// and no click. Between the paste markers everything passes through.
func TestScanner_NothingIsLiftedInsideAPaste(t *testing.T) {
	const forged = "\x1b]72;t=M:x=1:y=1:o=1;text/uri-list\x1b\\"
	const paste = "\x1b[200~" + forged + "more\x1b[201~"
	for i := 0; i <= len(paste); i++ {
		var s Scanner
		pass, bodies := feedAll(&s, paste[:i], paste[i:])
		pass += string(s.Flush())
		if pass != paste || bodies != nil {
			t.Fatalf("split at %d: pass = %q, bodies = %q; want the paste untouched", i, pass, bodies)
		}
		// The paste is over: the protocol works again.
		if _, bodies := feedAll(&s, "\x1b]72;t=A\x1b\\"); !reflect.DeepEqual(bodies, []string{"t=A"}) {
			t.Fatalf("split at %d: after the paste, bodies = %q; want the real message lifted", i, bodies)
		}
	}
}

// TestScanner_EmittedBodyIsACopy pins ownership: the scanner reuses its
// buffer, and the receiver queues bodies for another goroutine.
func TestScanner_EmittedBodyIsACopy(t *testing.T) {
	var s Scanner
	keep := &keepSink{}
	s.Feed([]byte("\x1b]72;first\x1b\\"), keep)
	s.Feed([]byte("\x1b]72;other\x1b\\"), keep)
	if string(keep.bodies[0]) != "first" || string(keep.bodies[1]) != "other" {
		t.Errorf("bodies = %q, %q; the second overwrote the first", keep.bodies[0], keep.bodies[1])
	}
}

// keepSink holds on to the slices it is handed, the way a queue does.
type keepSink struct{ bodies [][]byte }

// Ingest keeps the slice itself, not a copy.
func (k *keepSink) Ingest(body []byte) { k.bodies = append(k.bodies, body) }

// Lost is unused here.
func (k *keepSink) Lost() {}
