// =============================================================================
// File: internal/dnd/conn_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a bytes.Buffer two goroutines may share — the worker
// writes requests while the test reads what reached the wire.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p under the lock.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// collectControl returns a Conn whose control messages land in a slice.
func collectControl() (*Conn, *[]Msg, *lockedBuffer) {
	var got []Msg
	out := &lockedBuffer{}
	c := NewConn(out, func(m Msg) { got = append(got, m) })
	return c, &got, out
}

// recvNow receives with a short idle bound so a missing message fails
// the test instead of hanging it.
func recvNow(t *testing.T, in *Inbox) Msg {
	t.Helper()
	m, err := in.Recv(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	return m
}

// TestConn_ControlMessageIsDeliveredWhole pins the simple case: one
// hover message in, one control call out, fields intact.
func TestConn_ControlMessageIsDeliveredWhole(t *testing.T) {
	c, got, _ := collectControl()
	c.Ingest([]byte("t=m:x=3:y=4:o=3;text/uri-list"))
	if len(*got) != 1 {
		t.Fatalf("control called %d times, want 1", len(*got))
	}
	m := (*got)[0]
	if m.Type != 'm' || m.X != 3 || m.Y != 4 || m.Op != 3 || string(m.Payload) != "text/uri-list" || m.More {
		t.Errorf("control got %+v", m)
	}
}

// TestConn_ControlIsAssembledAcrossAChain pins chunk reassembly for the
// messages the event loop consumes: a long MIME list arrives in pieces,
// the later pieces carry only the m key, and the loop must see ONE
// message with the first piece's coordinates and the whole list.
func TestConn_ControlIsAssembledAcrossAChain(t *testing.T) {
	c, got, _ := collectControl()
	c.Ingest([]byte("t=M:x=1:y=2:o=1:m=1;text/uri-"))
	if len(*got) != 0 {
		t.Fatalf("control called before the chain ended")
	}
	c.Ingest([]byte("m=1;list text/"))
	c.Ingest([]byte("m=0;plain"))
	if len(*got) != 1 {
		t.Fatalf("control called %d times, want 1", len(*got))
	}
	m := (*got)[0]
	if m.Type != 'M' || m.X != 1 || m.Y != 2 || m.Op != 1 || m.More {
		t.Errorf("assembled message lost its head's metadata: %+v", m)
	}
	if string(m.Payload) != "text/uri-list text/plain" {
		t.Errorf("assembled payload = %q", m.Payload)
	}
}

// TestConn_QueryReplyDoesNotBreakAChain pins the one message the spec
// allows in the middle of a chain: it must be skipped, not mistaken for
// the next piece.
func TestConn_QueryReplyDoesNotBreakAChain(t *testing.T) {
	c, got, _ := collectControl()
	c.Ingest([]byte("t=m:x=1:y=1:o=1:m=1;text/"))
	c.Ingest([]byte("t=q"))
	c.Ingest([]byte("m=0;uri-list"))
	if len(*got) != 1 || string((*got)[0].Payload) != "text/uri-list" {
		t.Errorf("control = %+v, want one message with the whole list", *got)
	}
}

// TestConn_OversizeControlIsDropped pins the bound on what the loop is
// handed: a MIME list that never ends must not grow without limit.
func TestConn_OversizeControlIsDropped(t *testing.T) {
	c, got, _ := collectControl()
	piece := []byte("m=1;" + strings.Repeat("x", 4096))
	c.Ingest([]byte("t=m:x=1:y=1:o=1:m=1;start"))
	for i := 0; i < 300; i++ {
		c.Ingest(piece)
	}
	c.Ingest([]byte("m=0;end"))
	if len(*got) != 0 {
		t.Errorf("a %d-byte control payload was delivered", len((*got)[0].Payload))
	}
	// The chain is over: the next message is its own.
	c.Ingest([]byte("t=m:x=2:y=2:o=1;text/uri-list"))
	if len(*got) != 1 || (*got)[0].X != 2 {
		t.Errorf("the message after an oversize chain was lost: %+v", *got)
	}
}

// TestConn_DataIsStreamedWithInheritedKeys pins the data plane: pieces
// reach the worker one by one — a file is never buffered whole — and a
// piece that carries only the m key still says which request it
// answers, because the worker matches on those keys.
func TestConn_DataIsStreamedWithInheritedKeys(t *testing.T) {
	c, _, _ := collectControl()
	in := c.Attach()
	defer in.Close()
	c.Ingest([]byte("t=r:x=2:y=5:X=1:m=1;QUJD"))
	c.Ingest([]byte("m=1;RUZH"))
	c.Ingest([]byte("m=0;"))

	wants := []struct {
		payload string
		more    bool
	}{{"QUJD", true}, {"RUZH", true}, {"", false}}
	for i, w := range wants {
		m := recvNow(t, in)
		if m.Type != 'r' || m.X != 2 || m.Y != 5 || !m.HasPX || m.PX != 1 {
			t.Errorf("piece %d lost the head's keys: %+v", i, m)
		}
		if string(m.Payload) != w.payload || m.More != w.more {
			t.Errorf("piece %d = %q more=%v, want %q more=%v", i, m.Payload, m.More, w.payload, w.more)
		}
	}
}

// TestConn_DataWithNoReceiverIsDropped pins that data arriving with no
// fetch in flight — a late chunk after a failed drop — neither blocks
// the tty reader nor waits around for the next fetch to misread.
func TestConn_DataWithNoReceiverIsDropped(t *testing.T) {
	c, _, _ := collectControl()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			c.Ingest([]byte("t=r:x=1;QUJD"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ingest blocked with no receiver attached")
	}
	in := c.Attach()
	defer in.Close()
	if _, err := in.Recv(context.Background(), 20*time.Millisecond); !errors.Is(err, ErrSilent) {
		t.Errorf("Recv after stale data: err = %v, want ErrSilent", err)
	}
}

// TestConn_MalformedMessageIsIgnored pins that one bad message costs
// only itself.
func TestConn_MalformedMessageIsIgnored(t *testing.T) {
	c, got, _ := collectControl()
	c.Ingest([]byte("x=abc;"))
	c.Ingest([]byte("t=m:x=1:y=1:o=1;text/uri-list"))
	if len(*got) != 1 {
		t.Errorf("control called %d times, want 1 (the valid message only)", len(*got))
	}
}

// TestConn_SendWritesWholeSequences pins the write side, through both
// doors the worker and the loop use.
func TestConn_SendWritesWholeSequences(t *testing.T) {
	c, _, out := collectControl()
	if err := c.Send(Finish(OpCopy)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	in := c.Attach()
	defer in.Close()
	if err := in.Send(RequestMIME(1)); err != nil {
		t.Fatalf("Inbox.Send: %v", err)
	}
	if got, want := out.String(), "\x1b]72;t=r:o=1\x1b\\\x1b]72;t=r:x=1\x1b\\"; got != want {
		t.Errorf("wire = %q, want %q", got, want)
	}
}

// TestInbox_RecvHonoursContextAndIdle pins the two ways a wait ends
// without a message: the job was cancelled, or the terminal went quiet.
func TestInbox_RecvHonoursContextAndIdle(t *testing.T) {
	c, _, _ := collectControl()
	in := c.Attach()
	defer in.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := in.Recv(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("Recv on a cancelled context: err = %v, want context.Canceled", err)
	}
	if _, err := in.Recv(context.Background(), 10*time.Millisecond); !errors.Is(err, ErrSilent) {
		t.Errorf("Recv with nothing arriving: err = %v, want ErrSilent", err)
	}
}

// TestInbox_CloseDetaches pins teardown: once the worker is gone the
// reader must stop feeding it, or the tty reader blocks on a channel
// nobody will ever read.
func TestInbox_CloseDetaches(t *testing.T) {
	c, _, _ := collectControl()
	in := c.Attach()
	in.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			c.Ingest([]byte("t=r:x=1;QUJD"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ingest blocked after the inbox was closed")
	}
}

// TestInbox_StalledReceiverFailsTheStream pins backpressure's limit: a
// worker that stops reading cannot hold the tty reader — and with it
// every keystroke — forever. The reader gives up, and the stream it
// had to drop a piece of is reported broken rather than short.
func TestInbox_StalledReceiverFailsTheStream(t *testing.T) {
	saved := ingestPatience
	ingestPatience = 10 * time.Millisecond
	defer func() { ingestPatience = saved }()

	c, _, _ := collectControl()
	in := c.Attach()
	defer in.Close()
	for i := 0; i < inboxDepth+1; i++ {
		c.Ingest([]byte("t=r:x=1:m=1;QUJD"))
	}
	if _, err := in.Recv(context.Background(), time.Second); !errors.Is(err, ErrOverrun) {
		t.Errorf("Recv after an overrun: err = %v, want ErrOverrun", err)
	}
}

// TestConn_LostFailsTheFetch pins what happens when the tty filter had
// to give up on a message: a transfer in flight has a hole in it, and
// the worker must be told instead of finishing "successfully" with a
// short file. The failure outranks pieces already queued — a stream
// known to be broken is not drained as if it were whole.
func TestConn_LostFailsTheFetch(t *testing.T) {
	c, _, _ := collectControl()
	in := c.Attach()
	defer in.Close()
	c.Ingest([]byte("t=r:x=1:m=0;QUJD"))
	c.Lost()
	if _, err := in.Recv(context.Background(), time.Second); !errors.Is(err, ErrInterrupted) {
		t.Errorf("Recv after a lost message: err = %v, want ErrInterrupted", err)
	}
}

// TestConn_LostEndsTheChain pins that the message after a lost one
// starts fresh: it must not be read as the next piece of a chain whose
// tail never arrived.
func TestConn_LostEndsTheChain(t *testing.T) {
	c, got, _ := collectControl()
	c.Ingest([]byte("t=m:x=1:y=1:o=1:m=1;text/"))
	c.Lost()
	c.Ingest([]byte("t=m:x=5:y=6:o=1;text/uri-list"))
	if len(*got) != 1 || (*got)[0].X != 5 || string((*got)[0].Payload) != "text/uri-list" {
		t.Errorf("control = %+v, want the second message on its own", *got)
	}
}

// TestConn_LostWithNothingAttachedIsHarmless pins the idle case: a
// garbled hover message outside any transfer breaks nothing.
func TestConn_LostWithNothingAttachedIsHarmless(t *testing.T) {
	c, _, _ := collectControl()
	c.Lost()
	in := c.Attach()
	defer in.Close()
	c.Ingest([]byte("t=r:x=1;QUJD"))
	if m := recvNow(t, in); string(m.Payload) != "QUJD" {
		t.Errorf("a fetch attached after an unrelated loss got %+v", m)
	}
}
