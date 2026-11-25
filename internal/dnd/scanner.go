// =============================================================================
// File: internal/dnd/scanner.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import "bytes"

// MaxBody caps the body of one message. The protocol limits a payload
// to 4096 bytes and the metadata is a handful of short keys, so 16 KiB
// is generous; anything longer is not a message and is thrown away.
const MaxBody = 16 << 10

// Sink is where a Scanner delivers what it finds in the stream.
type Sink interface {
	// Ingest receives the body of one complete message: the bytes
	// between "ESC ] 72 ;" and the terminator. The slice is the
	// sink's to keep.
	Ingest(body []byte)
	// Lost reports that a message was started and never finished —
	// cut off, or too long to be one. Whatever was being transferred
	// now has a hole in it.
	Lost()
}

// scanState is where a Scanner is in the byte stream.
type scanState uint8

const (
	// scanNormal: plain input, passed through.
	scanNormal scanState = iota
	// scanPrefix: the last n bytes match the start of the introducer.
	// They are kept back, undecided, until the next byte settles it.
	scanPrefix
	// scanCapture: inside a body, collecting it.
	scanCapture
	// scanCaptureEsc: inside a body, and the last byte was ESC — the
	// terminator if a backslash follows, a new sequence otherwise.
	scanCaptureEsc
	// scanDiscard and scanDiscardEsc mirror the capture pair for a body
	// that outgrew MaxBody: its bytes are dropped up to the terminator.
	scanDiscard
	scanDiscardEsc
)

// pasteHead is what the two bracketed-paste markers share: ESC [ 2 0,
// then 0 for the start or 1 for the end, then ~.
const pasteHead = "\x1b[20"

// Scanner lifts OSC 72 messages out of a terminal's input stream and
// passes everything else through untouched. It sits under the screen
// library's own parser because file data must never reach it: tcell
// would spend a megabyte of base64 as a megabyte of parse work, and a
// sequence it gave up on would arrive as keystrokes.
//
// It is a stream: a message may be cut at any byte across calls. The
// zero value is ready to use. It is not safe for concurrent use — one
// reader owns it.
//
// The scanner has no clock, and that is the design. The stream is
// ordered, so when it stalls in the middle of a message — a congested
// link, a laptop lid — the rest of that message is what arrives next,
// however long it takes. A scanner that "gave up" after a pause could
// only turn a file's remaining bytes into typed text. So a message in
// progress waits, and three things end it early, all of them evidence
// in the stream itself rather than a guess about time:
//
//   - a control byte: a body never contains one, so the message was cut
//     off at the source, and the byte — Enter, Ctrl-C — is the user's
//     and is passed through;
//   - ESC followed by anything but a backslash: inside a body ESC is
//     only ever the first half of the terminator, so this is a new
//     sequence — an arrow key, a mouse report — passed through whole;
//   - a body past MaxBody, whose remaining bytes are discarded up to
//     the terminator and never released.
//
// Each of those is reported through Sink.Lost. The one thing a caller's
// clock decides is a chunk that ends on a lone ESC, which is just as
// likely to be the Esc key: see Held and Release.
//
// Nothing is lifted between the bracketed-paste markers. Pasted bytes
// are content, and content must not be able to speak the protocol.
type Scanner struct {
	state scanState
	n     int // introducer bytes matched while in scanPrefix
	body  []byte

	// ghost is set when a lone ESC was just released: if the next bytes
	// are the rest of the introducer, arriving in one piece, that ESC
	// was a message's first byte after all.
	ghost bool

	// paste counts the bytes of a paste marker matched in the stream
	// passed downstream, pasteEnd says which marker it is turning out
	// to be, and inPaste is true between a start marker and its end.
	paste    int
	pasteEnd bool
	inPaste  bool
}

// Feed consumes b and returns the bytes that are not part of an OSC 72
// message, in order. Complete messages and abandoned ones are reported
// to sink. The returned slice may alias b.
func (s *Scanner) Feed(b []byte, sink Sink) []byte {
	ghost := s.ghost
	s.ghost = false
	if len(b) == 0 {
		s.ghost = ghost
		return b
	}
	if s.paste == 0 && bytes.IndexByte(b, 0x1b) < 0 {
		if s.inPaste || (s.state == scanNormal && !(ghost && bytes.HasPrefix(b, []byte(introducer[1:])))) {
			return b
		}
	}
	pass := make([]byte, 0, len(b)+len(introducer))
	i := 0
	if ghost && s.state == scanNormal && !s.inPaste && bytes.HasPrefix(b, []byte(introducer[1:])) {
		s.state, s.body = scanCapture, s.body[:0]
		i = len(introducer) - 1
	}
	for ; i < len(b); i++ {
		c := b[i]
		if s.inPaste {
			pass = s.pass(pass, c)
			continue
		}
		switch s.state {
		case scanNormal:
			if c == 0x1b {
				s.state, s.n = scanPrefix, 1
			} else {
				pass = s.pass(pass, c)
			}
		case scanPrefix:
			if c == introducer[s.n] {
				s.n++
				if s.n == len(introducer) {
					s.state, s.body = scanCapture, s.body[:0]
				}
				continue
			}
			pass = s.passPrefix(pass)
			i-- // c is ordinary input, or the ESC of the next attempt
		case scanCapture, scanDiscard:
			capturing := s.state == scanCapture
			switch {
			case c == 0x1b:
				if capturing {
					s.state = scanCaptureEsc
				} else {
					s.state = scanDiscardEsc
				}
			case c == 0x07:
				if capturing {
					sink.Ingest(append([]byte(nil), s.body...))
				}
				s.state = scanNormal
			case c < 0x20 || c == 0x7f:
				if capturing {
					sink.Lost()
				}
				s.state = scanNormal
				i-- // the control byte is the user's; deliver it
			case capturing:
				s.body = append(s.body, c)
				if len(s.body) > MaxBody {
					sink.Lost()
					s.state, s.body = scanDiscard, s.body[:0]
				}
			}
		case scanCaptureEsc, scanDiscardEsc:
			if c == '\\' {
				if s.state == scanCaptureEsc {
					sink.Ingest(append([]byte(nil), s.body...))
				}
				s.state = scanNormal
				continue
			}
			// Not a terminator: the ESC opened something else.
			if s.state == scanCaptureEsc {
				sink.Lost()
			}
			s.state, s.n = scanPrefix, 1
			i--
		}
	}
	return pass
}

// pass appends one byte to the output and shows it to the paste
// matcher. Every byte that goes downstream goes through here, so the
// matcher sees exactly the stream tcell will.
func (s *Scanner) pass(out []byte, c byte) []byte {
	s.notePaste(c)
	return append(out, c)
}

// passPrefix hands the held introducer prefix downstream as ordinary
// input and returns to plain scanning.
func (s *Scanner) passPrefix(out []byte) []byte {
	for j := 0; j < s.n; j++ {
		out = s.pass(out, introducer[j])
	}
	s.state, s.n = scanNormal, 0
	return out
}

// notePaste advances the bracketed-paste matcher by one downstream
// byte. It only watches: nothing is held for it, so no key is ever
// delayed on its account.
func (s *Scanner) notePaste(c byte) {
	switch {
	case s.paste < len(pasteHead) && c == pasteHead[s.paste]:
		s.paste++
		return
	case s.paste == len(pasteHead) && (c == '0' || c == '1'):
		s.pasteEnd = c == '1'
		s.paste++
		return
	case s.paste == len(pasteHead)+1 && c == '~':
		s.inPaste = !s.pasteEnd
	}
	s.paste = 0
	if c == 0x1b {
		s.paste = 1
	}
}

// Held reports whether the scanner is sitting on a lone ESC: the one
// undecided byte that may be the user's — the Esc key — and so the one
// a caller should release if nothing follows promptly. A longer prefix
// ("ESC ]" and beyond) is not Held: its sender is mid-sequence, the
// next byte is coming, and it waits for it.
func (s *Scanner) Held() bool { return s.state == scanPrefix && s.n == 1 }

// Release hands back a held lone ESC as plain input. The caller's clock
// decides when: nothing in a byte stream says "no more is coming". If
// the rest of an introducer then arrives in one piece, the scanner
// still lifts that message — see the ghost field.
func (s *Scanner) Release() []byte {
	if !s.Held() {
		return nil
	}
	s.state, s.n = scanNormal, 0
	s.ghost = true
	s.notePaste(0x1b)
	return []byte{0x1b}
}

// Flush hands back everything undecided, whatever its length, and drops
// a message in progress. It is for the end of the stream, when nothing
// more can arrive to settle anything.
func (s *Scanner) Flush() []byte {
	var out []byte
	if s.state == scanPrefix {
		out = s.passPrefix(nil)
	}
	s.state, s.n, s.body = scanNormal, 0, s.body[:0]
	return out
}

// Capturing reports whether the scanner is inside a message, collecting
// or discarding it.
func (s *Scanner) Capturing() bool {
	return s.state >= scanCapture
}
