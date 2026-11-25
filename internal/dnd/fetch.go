// =============================================================================
// File: internal/dnd/fetch.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ErrNoFiles: the drop offers no file to copy — no URI list at all, or
// a list of nothing but links.
var ErrNoFiles = errors.New("the drop carries no files")

// errProtocol: a reply that answers no request this client made. The
// stream is out of step and nothing after it can be trusted.
var errProtocol = errors.New("the terminal sent a reply that matches no request")

// maxListBytes bounds a decoded URI list or directory listing. Both are
// names, not contents; eight megabytes of names is not a real folder.
const maxListBytes = 8 << 20

// maxLinkBytes bounds a symlink target — longer than any PATH_MAX.
const maxLinkBytes = 8 << 10

// Limits bounds what one drop may make this client do.
type Limits struct {
	// MaxEntries is how many files, links and folders a drop may
	// create in staging.
	MaxEntries int
	// MaxDepth is how many folder levels below a dropped folder are
	// followed.
	MaxDepth int
	// Idle is how long the terminal may stay silent while a reply is
	// owed before the drop is failed.
	Idle time.Duration
}

// DefaultLimits is what a zero Limits means.
var DefaultLimits = Limits{MaxEntries: 100000, MaxDepth: 64, Idle: 30 * time.Second}

// Link is the conversation Fetch holds: write a request, read the next
// data message. *Inbox is the real one.
type Link interface {
	Send(seq []byte) error
	Recv(ctx context.Context, idle time.Duration) (Msg, error)
}

// Request describes one drop to fetch.
type Request struct {
	// MIMEs is the drop event's MIME list, in the terminal's order —
	// requests index into it.
	MIMEs []string
	// Exists reports whether a path from the URI list is readable on
	// this machine. nil means nothing is.
	Exists func(path string) bool
	// Stage creates the staging directory. It is called at most once,
	// and only when an entry has to be transferred.
	Stage func() (string, error)
	// Limits bounds the fetch; the zero value means DefaultLimits.
	Limits Limits
	// Progress is called once per entry written to staging. May be nil.
	Progress func()
}

// Result is what a drop resolved to.
type Result struct {
	// Local are paths on this machine: copy from them.
	Local []string
	// Staged are entries transferred into StageDir: move them.
	Staged []string
	// StageDir is the staging directory, "" when nothing was staged.
	// It is set even when Fetch fails, so the caller can remove what a
	// half-finished transfer left.
	StageDir string
}

// Fetch turns a drop into files this machine can read. It asks for the
// URI list, then decides entry by entry: a path that is already
// readable here is reported as Local and left alone; anything else —
// always, when the terminal flagged the drop as coming from another
// machine — is transferred in-band into staging. Folders are walked
// breadth-first, which is what the spec asks of clients, with one
// request outstanding at a time so a reply can only ever be the answer
// to the last question.
//
// Fetch does not end the drop. The caller sends Finish once it has
// copied what Result names, because the platform may only keep dropped
// files alive until then.
func Fetch(ctx context.Context, l Link, req Request) (Result, error) {
	idx := 0
	for i, mime := range req.MIMEs {
		if mime == MIMEURIList {
			idx = i + 1
			break
		}
	}
	if idx == 0 {
		return Result{}, ErrNoFiles
	}
	f := &fetcher{ctx: ctx, link: l, req: req, lim: req.Limits}
	if f.lim == (Limits{}) {
		f.lim = DefaultLimits
	}
	res, err := f.run(idx)
	res.StageDir = f.stageDir
	return res, err
}

// fetcher is the state of one Fetch.
type fetcher struct {
	ctx      context.Context
	link     Link
	req      Request
	lim      Limits
	stageDir string
	entries  int
	queue    []dirJob
}

// dirJob is a transferred directory whose children are still to be
// fetched.
type dirJob struct {
	handle int
	dir    string
	names  []string
	depth  int
}

// want identifies the request a reply has to answer. The protocol keys
// replies by the same x / y / Y values the request carried.
type want struct {
	x      int
	y      int
	hasY   bool
	handle int
	child  bool
}

// matches reports whether m answers the request w describes.
func (w want) matches(m Msg) bool {
	if w.child {
		return m.HasPY && m.PY == w.handle && m.HasX && m.X == w.x
	}
	if !m.HasX || m.X != w.x {
		return false
	}
	if w.hasY {
		return m.HasY && m.Y == w.y
	}
	return !m.HasY
}

// run is Fetch after the URI list's index is known.
func (f *fetcher) run(idx int) (Result, error) {
	listWant := want{x: idx}
	if err := f.link.Send(RequestMIME(idx)); err != nil {
		return Result{}, err
	}
	first, err := f.recv(listWant)
	if err != nil {
		return Result{}, err
	}
	remote := first.HasPX && first.PX == 1
	list, err := f.collect(first, listWant, maxListBytes)
	if err != nil {
		return Result{}, err
	}

	var res Result
	files := 0
	for i, e := range ParseURIList(list) {
		if !e.File {
			continue
		}
		files++
		local := filepath.Clean(filepath.FromSlash(e.Path))
		if !remote && e.Local && f.req.Exists != nil && f.req.Exists(local) {
			res.Local = append(res.Local, local)
			continue
		}
		name, err := EntryName(e.Path)
		if err != nil {
			return res, err
		}
		slot, err := f.slot(i + 1)
		if err != nil {
			return res, err
		}
		dest := filepath.Join(slot, name)
		if err := f.fetchNode(RequestEntry(idx, i+1), want{x: idx, y: i + 1, hasY: true}, dest, 0); err != nil {
			return res, err
		}
		if err := f.drain(); err != nil {
			return res, err
		}
		res.Staged = append(res.Staged, dest)
	}
	if files == 0 {
		return res, ErrNoFiles
	}
	return res, nil
}

// slot returns the staging directory for the n-th URI-list entry,
// creating staging itself on first use. Each top-level entry gets its
// own slot so two dropped files with the same name cannot collide here;
// giving them distinct names in the project is the importer's job.
func (f *fetcher) slot(n int) (string, error) {
	if f.stageDir == "" {
		if f.req.Stage == nil {
			return "", errors.New("dnd: no staging directory to receive the drop")
		}
		dir, err := f.req.Stage()
		if err != nil {
			return "", err
		}
		f.stageDir = dir
	}
	slot := filepath.Join(f.stageDir, strconv.Itoa(n))
	if err := os.Mkdir(slot, 0o700); err != nil {
		return "", err
	}
	return slot, nil
}

// recv returns the next reply to the request w describes. An error
// reply becomes a *RemoteError; a reply for anything else is a protocol
// error.
func (f *fetcher) recv(w want) (Msg, error) {
	m, err := f.link.Recv(f.ctx, f.lim.Idle)
	if err != nil {
		return Msg{}, err
	}
	if m.Type == 'R' {
		return Msg{}, ErrorOf(m.Payload)
	}
	if m.Type != 'r' || !w.matches(m) {
		return Msg{}, errProtocol
	}
	return m, nil
}

// stream decodes one reply's data, piece by piece, into sink. first is
// the piece already received. The data ends at a piece with an empty
// payload and no "more" flag — the spec's end marker — so a final chunk
// that carries data is followed by one more, empty, message.
//
// A message is one base64 unit: whatever is left of a quantum when a
// piece arrives without the "more" flag is that unit's unpadded tail,
// not the start of the next message. This is how kitty frames a file —
// every block it reads is encoded by itself and sent as a complete
// message — and a decoder that carried the tail across would scramble
// everything after the first block whose length is not a multiple of
// three. Within a chain (more set) the stream does continue, and the
// carry is kept.
func (f *fetcher) stream(first Msg, w want, sink func([]byte) error) error {
	var dec Decoder
	piece := first
	for {
		if len(piece.Payload) > 0 {
			data, err := dec.Decode(piece.Payload)
			if err != nil {
				return err
			}
			if err := sink(data); err != nil {
				return err
			}
		}
		if !piece.More {
			tail, err := dec.Close()
			if err != nil {
				return err
			}
			if err := sink(tail); err != nil {
				return err
			}
			if len(piece.Payload) == 0 {
				return nil
			}
		}
		next, err := f.recv(w)
		if err != nil {
			return err
		}
		piece = next
	}
}

// collect gathers one reply's data in memory, refusing past limit.
func (f *fetcher) collect(first Msg, w want, limit int) ([]byte, error) {
	var buf bytes.Buffer
	err := f.stream(first, w, func(b []byte) error {
		if buf.Len()+len(b) > limit {
			return errors.New("the terminal sent an oversized listing")
		}
		buf.Write(b)
		return nil
	})
	return buf.Bytes(), err
}

// fetchNode requests one entry and writes it to dest: a regular file, a
// symlink, or a directory — which is created empty and queued, its
// children fetched later by drain. The first reply says which: X absent
// or 0 is a file, 1 a symlink, anything else a directory handle.
func (f *fetcher) fetchNode(request []byte, w want, dest string, depth int) error {
	f.entries++
	if f.entries > f.lim.MaxEntries {
		return fmt.Errorf("the drop has more than %d entries", f.lim.MaxEntries)
	}
	if err := f.link.Send(request); err != nil {
		return err
	}
	first, err := f.recv(w)
	if err != nil {
		return err
	}
	switch {
	case !first.HasPX || first.PX == 0:
		err = f.writeFile(first, w, dest)
	case first.PX == 1:
		var target []byte
		if target, err = f.collect(first, w, maxLinkBytes); err == nil {
			err = os.Symlink(string(target), dest)
		}
	default:
		err = f.openDir(first, w, dest, depth)
	}
	if err == nil && f.req.Progress != nil {
		f.req.Progress()
	}
	return err
}

// writeFile streams a regular file's data to dest. The file is created
// exclusively: every destination is a fresh name inside staging, so one
// that already exists means the listing named an entry twice.
func (f *fetcher) writeFile(first Msg, w want, dest string) error {
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	out := bufio.NewWriterSize(file, 64<<10)
	err = f.stream(first, w, func(b []byte) error {
		_, werr := out.Write(b)
		return werr
	})
	if err == nil {
		err = out.Flush()
	}
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	return err
}

// openDir creates the directory dest and queues its listing. Every
// name is validated before anything is written under it — a listing is
// the terminal's word for what to create, and "../x" must not be taken
// at it.
func (f *fetcher) openDir(first Msg, w want, dest string, depth int) error {
	if depth >= f.lim.MaxDepth {
		return fmt.Errorf("the dropped folder nests deeper than %d levels", f.lim.MaxDepth)
	}
	listing, err := f.collect(first, w, maxListBytes)
	if err != nil {
		return err
	}
	var names []string
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := string(raw)
		if !ValidName(name) {
			return fmt.Errorf("the dropped folder lists an unusable name %q", name)
		}
		names = append(names, name)
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	f.queue = append(f.queue, dirJob{handle: first.PX, dir: dest, names: names, depth: depth + 1})
	return nil
}

// drain fetches the children of every queued directory, first in first
// out — breadth-first — and tells the terminal each directory is done
// with as soon as its last child has arrived.
func (f *fetcher) drain() error {
	for len(f.queue) > 0 {
		job := f.queue[0]
		f.queue = f.queue[1:]
		for n, name := range job.names {
			w := want{child: true, handle: job.handle, x: n + 1}
			if err := f.fetchNode(RequestChild(job.handle, n+1), w, filepath.Join(job.dir, name), job.depth); err != nil {
				return err
			}
		}
		if err := f.link.Send(CloseDir(job.handle)); err != nil {
			return err
		}
	}
	return nil
}
