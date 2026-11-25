// =============================================================================
// File: internal/dnd/msg.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package dnd is the client side of kitty's drag-and-drop protocol
// (OSC 72): what a terminal program speaks to be told that something is
// being dragged over it, where, and to be handed the dropped files —
// in-band, so a drop from the user's laptop reaches a program running
// over SSH. Spec: https://sw.kovidgoyal.net/kitty/dnd-protocol/
//
// The package is written from that specification and covers the half
// skiff uses: accepting drops. Starting a drag out of the editor is
// not implemented.
//
// It has no tcell, theme or app imports. msg.go is the wire format,
// scanner.go lifts messages out of the tty's byte stream, conn.go
// routes them, and fetch.go is the conversation that turns a drop into
// files on disk — each testable with bytes and a temp directory.
//
// A caveat worth knowing before trusting any of it: a terminal can
// answer this protocol's handshake and still never deliver a drop (Rex
// 0.1.0-preview.20 does exactly that). Nothing here asks "do you
// support this?" for that reason — the client registers and then
// believes only the events that actually arrive.
package dnd

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// MIMEURIList is the one MIME type skiff accepts: the list of dropped
// files.
const MIMEURIList = "text/uri-list"

// The drop operations of the o key.
const (
	OpNone = 0 // not accepted, or cancelled
	OpCopy = 1
	OpMove = 2
)

// introducer and terminator wrap every message: OSC 72 ; … ST.
const (
	introducer = "\x1b]72;"
	terminator = "\x1b\\"
)

// Msg is one OSC 72 message with its metadata keys decoded.
//
// The lower-case x and y keys are a cell in a hover or drop event and
// an index (into the MIME list, then into the URI list or a directory
// listing) in a data message. The upper-case X and Y keys — PX and PY
// here, because Go cannot have both spellings — are a pixel in an
// event, and in a data message a flag or a directory handle. The Has
// fields matter: an absent X means "a regular file" while X=1 means "a
// symlink", and zero cannot tell "absent" from "0".
type Msg struct {
	// Type is the t key; zero when the message carried none, which is
	// how a continuation chunk looks.
	Type byte
	// More is m=1: further chunks of this payload follow.
	More bool
	Op   int // o
	X, Y int // x, y
	PX   int // X
	PY   int // Y

	HasOp, HasX, HasY, HasPX, HasPY bool

	// Payload is the raw bytes after the first semicolon — base64, a
	// MIME list or an error name, depending on Type. It aliases the
	// body Parse was given.
	Payload []byte
}

// Parse decodes the body of one message: the bytes between the
// introducer and the terminator. Only the first semicolon separates
// metadata from payload. Unknown keys are skipped — the protocol is
// still growing, and a key this client has never heard of must not
// cost it the message — but a key it does know with a malformed value
// is an error, so nothing is ever acted on with a silently zeroed
// field.
func Parse(body []byte) (Msg, error) {
	meta, payload := body, []byte(nil)
	if i := bytes.IndexByte(body, ';'); i >= 0 {
		meta, payload = body[:i], body[i+1:]
	}
	m := Msg{Payload: payload}
	for _, kv := range bytes.Split(meta, []byte{':'}) {
		kv = bytes.TrimSpace(kv)
		if len(kv) == 0 {
			continue
		}
		if len(kv) < 3 || kv[1] != '=' {
			return Msg{}, fmt.Errorf("dnd: malformed metadata %q", kv)
		}
		key, val := kv[0], bytes.TrimSpace(kv[2:])
		if len(val) == 0 {
			return Msg{}, fmt.Errorf("dnd: malformed metadata %q", kv)
		}
		if key == 't' {
			if len(val) != 1 {
				return Msg{}, fmt.Errorf("dnd: malformed type %q", val)
			}
			m.Type = val[0]
			continue
		}
		if !strings.ContainsRune("moxyXY", rune(key)) {
			continue
		}
		n, err := strconv.Atoi(string(val))
		if err != nil {
			return Msg{}, fmt.Errorf("dnd: key %c is not a number: %q", key, val)
		}
		switch key {
		case 'm':
			m.More = n == 1
		case 'o':
			m.Op, m.HasOp = n, true
		case 'x':
			m.X, m.HasX = n, true
		case 'y':
			m.Y, m.HasY = n, true
		case 'X':
			m.PX, m.HasPX = n, true
		case 'Y':
			m.PY, m.HasPY = n, true
		}
	}
	return m, nil
}

// encode wraps metadata and an optional payload as one message. The
// separating semicolon is written only when there is a payload, the
// form the spec uses for its payload-less examples.
func encode(meta, payload string) []byte {
	b := make([]byte, 0, len(introducer)+len(meta)+1+len(payload)+len(terminator))
	b = append(b, introducer...)
	b = append(b, meta...)
	if payload != "" {
		b = append(b, ';')
		b = append(b, payload...)
	}
	return append(b, terminator...)
}

// Accept tells the terminal this program takes drops of the given MIME
// types.
func Accept(mimes ...string) []byte {
	return encode("t=a", strings.Join(mimes, " "))
}

// MachineIDMsg tells the terminal which machine this program runs on,
// so it can tell a local drop (hand over paths) from a remote one
// (send the bytes). id is the value MachineID returns.
func MachineIDMsg(id string) []byte {
	return encode("t=a:x=1", id)
}

// Stop tells the terminal this program no longer takes drops.
func Stop() []byte {
	return encode("t=A", "")
}

// Reply answers a hover: the operation this program would perform if
// the drag were dropped here (OpNone refuses it), and the offered MIME
// types it wants, most preferred first.
func Reply(op int, mimes ...string) []byte {
	return encode("t=m:o="+strconv.Itoa(op), strings.Join(mimes, " "))
}

// RequestMIME asks for the data of one MIME type. idx is its 1-based
// position in the drop event's MIME list.
func RequestMIME(idx int) []byte {
	return encode("t=r:x="+strconv.Itoa(idx), "")
}

// RequestEntry asks for the contents of one entry of a dropped URI
// list: idx is the list's MIME index, sub the entry's 1-based position
// in the list.
func RequestEntry(idx, sub int) []byte {
	return encode("t=r:x="+strconv.Itoa(idx)+":y="+strconv.Itoa(sub), "")
}

// RequestChild asks for one entry of a directory the terminal has
// listed: handle is the directory's, num the entry's 1-based position
// in its listing.
func RequestChild(handle, num int) []byte {
	return encode("t=r:Y="+strconv.Itoa(handle)+":x="+strconv.Itoa(num), "")
}

// CloseDir tells the terminal this program is done with a directory,
// so it can free whatever the handle held.
func CloseDir(handle int) []byte {
	return encode("t=r:Y="+strconv.Itoa(handle), "")
}

// Finish ends the drop, reporting what was done with it. OpNone means
// it was abandoned; the terminal discards any request still queued.
func Finish(op int) []byte {
	return encode("t=r:o="+strconv.Itoa(op), "")
}

// MIMEs splits a payload that is a space-separated MIME list. An empty
// payload is nil — "no list was sent", which in a hover means "the same
// list as before".
func MIMEs(payload []byte) []string {
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// RemoteError is an error the terminal reported for a request: a POSIX
// symbolic name and an optional description.
type RemoteError struct {
	Name string
	Desc string
}

// Error renders the report for a flash.
func (e *RemoteError) Error() string {
	if e.Desc == "" {
		return "the terminal reported " + e.Name
	}
	return "the terminal reported " + e.Name + ": " + e.Desc
}

// ErrorOf decodes the payload of an error reply, "NAME:description".
// A reply with no name at all is EUNKNOWN, the spec's own catch-all.
func ErrorOf(payload []byte) *RemoteError {
	name, desc, _ := strings.Cut(strings.TrimSpace(string(payload)), ":")
	if name == "" {
		name = "EUNKNOWN"
	}
	return &RemoteError{Name: name, Desc: strings.TrimSpace(desc)}
}
