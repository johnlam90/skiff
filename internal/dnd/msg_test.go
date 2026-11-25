// =============================================================================
// File: internal/dnd/msg_test.go
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

// TestParse_HoverEvent pins the event the terminal sends while a drag
// moves over the window: the cell, the pixel, the allowed operations
// and the offered MIME types all have to land in their own fields.
func TestParse_HoverEvent(t *testing.T) {
	m, err := Parse([]byte("t=m:x=3:y=7:X=30:Y=140:o=3;text/uri-list text/plain"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Msg{
		Type: 'm', Op: 3, X: 3, Y: 7, PX: 30, PY: 140,
		HasOp: true, HasX: true, HasY: true, HasPX: true, HasPY: true,
		Payload: []byte("text/uri-list text/plain"),
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("Parse = %+v\nwant    %+v", m, want)
	}
}

// TestParse_LeaveEvent pins the "drag left the window" spelling: cell
// -1,-1 with nothing else. Dropping the sign would park the highlight
// on the top-left row forever.
func TestParse_LeaveEvent(t *testing.T) {
	m, err := Parse([]byte("t=m:x=-1:y=-1"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != 'm' || m.X != -1 || m.Y != -1 || len(m.Payload) != 0 {
		t.Errorf("Parse = %+v, want a move to -1,-1 with no payload", m)
	}
}

// TestParse_ContinuationChunk pins that a chunk carrying only the m key
// parses with no type: the reader has to recognise it as the next piece
// of the chain in progress, not as a fresh "start accepting" message
// (t defaults to a in the spec's table).
func TestParse_ContinuationChunk(t *testing.T) {
	m, err := Parse([]byte("m=1;QUJD"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != 0 || !m.More || string(m.Payload) != "QUJD" {
		t.Errorf("Parse = %+v, want an untyped chunk with More and payload QUJD", m)
	}
}

// TestParse_PayloadKeepsItsOwnSemicolons pins that only the FIRST
// semicolon separates metadata from payload: an error description may
// contain more of them.
func TestParse_PayloadKeepsItsOwnSemicolons(t *testing.T) {
	m, err := Parse([]byte("t=R:x=1;EIO:disk; gone"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != 'R' || string(m.Payload) != "EIO:disk; gone" {
		t.Errorf("Parse = %+v, want type R and the whole description", m)
	}
}

// TestParse_Tolerant pins the two leniencies: the spec writes its
// examples with spaces around the separators, and a newer terminal may
// send keys this client has never heard of. Neither may fail a message.
func TestParse_Tolerant(t *testing.T) {
	m, err := Parse([]byte(" t=r : x=2 : z=hello : i=4 ;QUJD"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != 'r' || !m.HasX || m.X != 2 || string(m.Payload) != "QUJD" {
		t.Errorf("Parse = %+v, want type r, x=2, payload QUJD", m)
	}
	bare, err := Parse([]byte("t=A"))
	if err != nil || bare.Type != 'A' || len(bare.Payload) != 0 {
		t.Errorf("Parse(t=A) = %+v, %v; want type A and no payload", bare, err)
	}
}

// TestParse_Malformed pins what is refused: metadata that is not
// key=value, a type that is not one character, and a number that is
// not one. A refused message is dropped by the reader rather than
// acted on with zeroed fields.
func TestParse_Malformed(t *testing.T) {
	for _, body := range []string{"t=mm;x", "x=abc;", "tm;", "t=;", "=1;"} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q): want an error", body)
		}
	}
}

// TestEncode_ClientMessages pins every sequence this client writes,
// byte for byte. This is the wire contract with the terminal: one wrong
// key and the request is ignored without any error to notice.
func TestEncode_ClientMessages(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"accept", Accept("text/uri-list"), "\x1b]72;t=a;text/uri-list\x1b\\"},
		{"accept two", Accept("text/uri-list", "text/plain"), "\x1b]72;t=a;text/uri-list text/plain\x1b\\"},
		{"accept none", Accept(), "\x1b]72;t=a\x1b\\"},
		{"machine id", MachineIDMsg("1:abcd"), "\x1b]72;t=a:x=1;1:abcd\x1b\\"},
		{"stop", Stop(), "\x1b]72;t=A\x1b\\"},
		{"reply copy", Reply(OpCopy, "text/uri-list"), "\x1b]72;t=m:o=1;text/uri-list\x1b\\"},
		{"reply reject", Reply(OpNone), "\x1b]72;t=m:o=0\x1b\\"},
		{"request mime", RequestMIME(2), "\x1b]72;t=r:x=2\x1b\\"},
		{"request entry", RequestEntry(2, 5), "\x1b]72;t=r:x=2:y=5\x1b\\"},
		{"request child", RequestChild(7, 3), "\x1b]72;t=r:Y=7:x=3\x1b\\"},
		{"close dir", CloseDir(7), "\x1b]72;t=r:Y=7\x1b\\"},
		{"finish copy", Finish(OpCopy), "\x1b]72;t=r:o=1\x1b\\"},
		{"finish cancelled", Finish(OpNone), "\x1b]72;t=r:o=0\x1b\\"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestEncode_RoundTrips pins that what this client writes is what its
// own parser reads — the scripted terminal in the fetch tests relies on
// reading requests back.
func TestEncode_RoundTrips(t *testing.T) {
	seq := RequestChild(7, 3)
	body := strings.TrimSuffix(strings.TrimPrefix(string(seq), "\x1b]72;"), "\x1b\\")
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != 'r' || !m.HasPY || m.PY != 7 || !m.HasX || m.X != 3 {
		t.Errorf("round trip = %+v, want type r, Y=7, x=3", m)
	}
}

// TestMIMEs pins the space-separated list, blank-tolerant, with an
// empty payload meaning "no list sent" rather than one empty type.
func TestMIMEs(t *testing.T) {
	got := MIMEs([]byte("text/uri-list  text/plain\n"))
	if want := []string{"text/uri-list", "text/plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MIMEs = %q, want %q", got, want)
	}
	if got := MIMEs(nil); got != nil {
		t.Errorf("MIMEs(nil) = %q, want nil", got)
	}
}

// TestErrorOf pins the error reply's two halves: the POSIX name the
// client branches on and the optional description it shows.
func TestErrorOf(t *testing.T) {
	cases := []struct {
		payload    string
		name, desc string
	}{
		{"ENOENT:no such file", "ENOENT", "no such file"},
		{"EIO", "EIO", ""},
		{"EPERM:a: b", "EPERM", "a: b"},
		{"", "EUNKNOWN", ""},
	}
	for _, c := range cases {
		e := ErrorOf([]byte(c.payload))
		if e.Name != c.name || e.Desc != c.desc {
			t.Errorf("ErrorOf(%q) = %+v, want {%s %s}", c.payload, e, c.name, c.desc)
		}
		if !strings.Contains(e.Error(), c.name) {
			t.Errorf("ErrorOf(%q).Error() = %q, want it to name %s", c.payload, e.Error(), c.name)
		}
	}
}
