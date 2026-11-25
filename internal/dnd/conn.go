// =============================================================================
// File: internal/dnd/conn.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// ErrSilent: the terminal sent nothing for the whole idle window while
// a reply was owed.
var ErrSilent = errors.New("the terminal stopped answering")

// ErrOverrun: the receiver fell so far behind that the reader had to
// drop a piece. The stream has a hole in it and cannot be trusted.
var ErrOverrun = errors.New("the transfer overran its buffer")

// ErrInterrupted: a message was cut off before its terminator — the
// terminal stopped mid-sequence. Something is missing from the stream.
var ErrInterrupted = errors.New("the transfer was interrupted")

// maxControlPayload bounds an assembled hover or drop message. Its
// payload is a MIME list; a megabyte of one is not a list.
const maxControlPayload = 1 << 20

// inboxDepth is how many data pieces may wait for the worker — about a
// quarter-megabyte of encoded file at the protocol's chunk size.
const inboxDepth = 64

// ingestPatience is how long the reader waits on a full inbox before it
// gives the stream up. The reader is the tty's reader: while it waits,
// no keystroke gets through either. A var so tests need not wait it
// out.
var ingestPatience = 10 * time.Second

// Conn is one program's end of the protocol: it writes requests to the
// terminal and routes what comes back.
//
// The two kinds of traffic go to different places because they have
// different consumers. Hover and drop events ("where is the drag?",
// "it was dropped") belong to the UI, so they are assembled whole and
// handed to control. File data belongs to whoever is fetching the drop,
// can be arbitrarily large, and must not pass through an event queue —
// so it is streamed piece by piece to the attached Inbox, and the
// bounded channel between them is the backpressure: a slow disk slows
// the reader, which slows the terminal.
//
// Ingest and Lost are called by exactly one goroutine, the tty reader —
// together they make a Conn the Scanner's Sink. Send and Attach are
// safe from any.
type Conn struct {
	out     io.Writer
	control func(Msg)

	// Reader-side chain state. A payload over 4096 bytes arrives as a
	// chain of chunks; only the first is guaranteed to carry metadata,
	// so the head is remembered and every later chunk answers for it.
	inChain bool
	head    Msg
	buf     []byte
	tooBig  bool

	mu    sync.Mutex // guards inbox
	inbox *Inbox

	wmu sync.Mutex // one whole sequence per write
}

// NewConn returns a Conn writing to out. control receives each hover
// and drop message, on the reader's goroutine; it must hand off rather
// than block.
func NewConn(out io.Writer, control func(Msg)) *Conn {
	return &Conn{out: out, control: control}
}

// Send writes one whole sequence to the terminal.
func (c *Conn) Send(seq []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.out.Write(seq)
	return err
}

// Ingest routes the body of one message lifted from the input stream.
// A body that does not parse is handled as a lost message: it is
// dropped, it takes the chain it interrupted with it, and it fails a
// transfer in flight — nobody can say it was not a block of the file.
// A query reply is skipped without touching the chain — it is the one
// message the spec lets a terminal send in the middle of one.
func (c *Conn) Ingest(body []byte) {
	m, err := Parse(body)
	if err != nil {
		c.Lost()
		return
	}
	if m.Type == 'q' || m.Type == 'Q' {
		return
	}
	if !c.inChain {
		c.head = m
		c.head.Payload = nil
		c.buf = c.buf[:0]
		c.tooBig = false
	}
	piece := c.head
	piece.Payload, piece.More = m.Payload, m.More
	c.inChain = m.More

	switch piece.Type {
	case 'm', 'M':
		if len(c.buf)+len(m.Payload) > maxControlPayload {
			c.tooBig = true
		} else {
			c.buf = append(c.buf, m.Payload...)
		}
		if m.More || c.tooBig || c.control == nil {
			return
		}
		piece.Payload = append([]byte(nil), c.buf...)
		c.control(piece)
	case 'r', 'R':
		c.mu.Lock()
		in := c.inbox
		c.mu.Unlock()
		if in != nil {
			in.push(piece)
		}
	}
}

// Lost records that the tty filter gave up on a message. Nobody can say
// which message it was, so the answer is the cautious one: a transfer
// in flight is failed — a file with a block missing must not be
// reported as copied — and a chain in progress is over, so the next
// message is read as itself and not as that chain's next piece.
func (c *Conn) Lost() {
	c.inChain = false
	c.mu.Lock()
	in := c.inbox
	c.mu.Unlock()
	if in != nil {
		in.fail(ErrInterrupted)
	}
}

// Attach starts delivering data messages to a new Inbox and returns it.
// One fetch runs at a time; attaching again retires the previous inbox.
// Data that arrives with nothing attached is dropped — a late chunk
// from a drop that already failed must not sit waiting to be misread as
// the start of the next one.
func (c *Conn) Attach() *Inbox {
	in := &Inbox{
		conn:   c,
		ch:     make(chan Msg, inboxDepth),
		done:   make(chan struct{}),
		broken: make(chan struct{}),
	}
	c.mu.Lock()
	prev := c.inbox
	c.inbox = in
	c.mu.Unlock()
	if prev != nil {
		prev.shut()
	}
	return in
}

// Inbox is the data half of a Conn for the duration of one fetch. It
// implements Link.
type Inbox struct {
	conn *Conn
	ch   chan Msg

	done      chan struct{} // closed by Close: stop feeding me
	closeOnce sync.Once

	broken    chan struct{} // closed once the stream is known to have a hole
	breakOnce sync.Once
	why       error // set before broken is closed, read only after
}

// Send writes one whole sequence to the terminal.
func (in *Inbox) Send(seq []byte) error { return in.conn.Send(seq) }

// Recv returns the next data message. It fails with the context's error
// when the fetch is cancelled, ErrSilent when nothing arrives for idle,
// and ErrOverrun or ErrInterrupted once the stream has lost a piece —
// checked first, so a corrupt stream is never drained as if it were
// whole.
func (in *Inbox) Recv(ctx context.Context, idle time.Duration) (Msg, error) {
	select {
	case <-in.broken:
		return Msg{}, in.why
	default:
	}
	if err := ctx.Err(); err != nil {
		return Msg{}, err
	}
	timer := time.NewTimer(idle)
	defer timer.Stop()
	select {
	case m := <-in.ch:
		return m, nil
	case <-in.broken:
		return Msg{}, in.why
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	case <-timer.C:
		return Msg{}, ErrSilent
	}
}

// Close detaches the inbox: the reader stops feeding it, and a push
// already waiting on it returns.
func (in *Inbox) Close() {
	in.conn.mu.Lock()
	if in.conn.inbox == in {
		in.conn.inbox = nil
	}
	in.conn.mu.Unlock()
	in.shut()
}

// shut releases anything blocked on this inbox. Idempotent, because
// Close and a replacing Attach can both reach it.
func (in *Inbox) shut() {
	in.closeOnce.Do(func() { close(in.done) })
}

// push hands one piece to the worker, waiting up to ingestPatience for
// room. Giving up drops the piece and marks the stream broken: the
// alternative is a tty reader parked forever behind a worker that died.
func (in *Inbox) push(m Msg) {
	select {
	case in.ch <- m:
		return
	default:
	}
	timer := time.NewTimer(ingestPatience)
	defer timer.Stop()
	select {
	case in.ch <- m:
	case <-in.done:
	case <-timer.C:
		in.fail(ErrOverrun)
	}
}

// fail marks the stream broken for good, with the reason Recv reports.
// The first failure wins.
func (in *Inbox) fail(err error) {
	in.breakOnce.Do(func() {
		in.why = err
		close(in.broken)
	})
}
