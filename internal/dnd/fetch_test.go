// =============================================================================
// File: internal/dnd/fetch_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for Fetch against a scripted terminal. The terminal here is a
// real Conn fed by a goroutine, so every reply travels the same path it
// does in the editor — encoded, chunked, parsed, routed — and the only
// thing faked is the machine on the far side of the tty.

package dnd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// node is one entry on the scripted terminal's side: a file, a symlink,
// a directory, or an error it answers with.
type node struct {
	name     string
	data     []byte // file contents
	target   string // symlink target
	isLink   bool
	isDir    bool
	children []*node
	errName  string // answer with t=R and this POSIX name
	wrongKey bool   // answer with keys that match no request
}

// fakeTerm is the terminal end of the protocol. Requests arrive through
// Write (it is the Conn's writer), a goroutine answers them through
// Conn.Ingest.
type fakeTerm struct {
	conn    *Conn
	uriList string
	remote  bool
	top     map[int]*node // URI-list sub-index → node
	silent  bool          // stop answering entry requests
	// kittyRead, when set, frames replies the way kitty itself does
	// instead of the spec's chunk-one-stream reading: file data goes
	// out as one message per read of this many bytes, each encoded on
	// its own with the padding stripped, and each marked m=0.
	kittyRead int

	reqs chan string

	mu      sync.Mutex
	log     []string // request bodies, in arrival order
	handles map[int]*node
	next    int
	closed  []int
}

// newFakeTerm wires a scripted terminal to a fresh Conn and starts it.
func newFakeTerm(t *testing.T) *fakeTerm {
	t.Helper()
	f := &fakeTerm{top: map[int]*node{}, handles: map[int]*node{}, next: 2, reqs: make(chan string, 256)}
	f.conn = NewConn(f, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for body := range f.reqs {
			f.answer(body)
		}
	}()
	t.Cleanup(func() {
		close(f.reqs)
		<-done
	})
	return f
}

// Write receives what the client sends: one or more whole sequences.
func (f *fakeTerm) Write(p []byte) (int, error) {
	for _, seq := range strings.Split(string(p), "\x1b\\") {
		if seq == "" {
			continue
		}
		body := strings.TrimPrefix(seq, "\x1b]72;")
		f.mu.Lock()
		f.log = append(f.log, body)
		f.mu.Unlock()
		f.reqs <- body
	}
	return len(p), nil
}

// requests returns the bodies the client has sent so far.
func (f *fakeTerm) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// closedHandles returns the directory handles the client released.
func (f *fakeTerm) closedHandles() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.closed...)
}

// answer replies to one request the way the spec says a terminal does.
func (f *fakeTerm) answer(body string) {
	m, err := Parse([]byte(body))
	if err != nil || m.Type != 'r' {
		return
	}
	switch {
	case m.HasPY && !m.HasX: // t=r:Y=handle — done with a directory
		f.mu.Lock()
		f.closed = append(f.closed, m.PY)
		f.mu.Unlock()
	case m.HasPY: // t=r:Y=handle:x=num — a directory's child
		f.mu.Lock()
		dir := f.handles[m.PY]
		f.mu.Unlock()
		keys := fmt.Sprintf("Y=%d:x=%d", m.PY, m.X)
		if dir == nil || m.X < 1 || m.X > len(dir.children) {
			f.conn.Ingest([]byte("t=R:" + keys + ";ENOENT"))
			return
		}
		f.sendNode(keys, dir.children[m.X-1])
	case m.HasX && m.HasY: // t=r:x=idx:y=sub — a URI-list entry
		keys := fmt.Sprintf("x=%d:y=%d", m.X, m.Y)
		n := f.top[m.Y]
		if n == nil {
			f.conn.Ingest([]byte("t=R:" + keys + ";ENOENT:no such entry"))
			return
		}
		f.sendNode(keys, n)
	case m.HasX: // t=r:x=idx — the MIME data
		keys := fmt.Sprintf("x=%d", m.X)
		if f.remote {
			keys += ":X=1"
		}
		f.sendData(keys, []byte(f.uriList))
	}
}

// sendNode answers an entry request with the node's kind and data.
func (f *fakeTerm) sendNode(keys string, n *node) {
	switch {
	case f.silent:
	case n.errName != "":
		f.conn.Ingest([]byte("t=R:" + keys + ";" + n.errName + ":scripted failure"))
	case n.wrongKey:
		f.sendData("x=99:y=99", n.data)
	case n.isLink:
		f.sendData(keys+":X=1", []byte(n.target))
	case n.isDir:
		f.mu.Lock()
		h := f.next
		f.next++
		f.handles[h] = n
		f.mu.Unlock()
		var names []string
		for _, c := range n.children {
			names = append(names, c.name)
		}
		f.sendData(fmt.Sprintf("%s:X=%d", keys, h), []byte(strings.Join(names, "\x00")))
	case f.kittyRead > 0:
		f.sendKittyFile(keys, n.data)
	default:
		f.sendData(keys, n.data)
	}
}

// sendKittyFile transmits file contents the way kitty does: it reads
// the file a block at a time and sends every block as a complete
// message of its own — full header, m=0, the block base64-encoded by
// itself with no padding — then the bare header as the end marker. A
// block whose length is not a multiple of three therefore ends in a
// partial quantum that the NEXT message does not continue.
func (f *fakeTerm) sendKittyFile(keys string, data []byte) {
	for len(data) > 0 {
		n := min(len(data), f.kittyRead)
		f.conn.Ingest([]byte("t=r:" + keys + ":m=0;" + base64.RawStdEncoding.EncodeToString(data[:n])))
		data = data[n:]
	}
	f.conn.Ingest([]byte("t=r:" + keys))
}

// sendData transmits one payload the two ways the spec allows: small
// data as a single message, large data as a chain whose later chunks
// carry only the m key. Both end with an empty m=0 message. With
// kittyRead set, a long payload is chained kitty's way instead: every
// chunk carries the full header and is 3072 bytes encoded on its own.
func (f *fakeTerm) sendData(keys string, data []byte) {
	if f.kittyRead > 0 && len(data) > 3072 {
		for len(data) > 0 {
			n := min(len(data), 3072)
			more := "0"
			if n < len(data) {
				more = "1"
			}
			f.conn.Ingest([]byte("t=r:" + keys + ":m=" + more + ";" + base64.RawStdEncoding.EncodeToString(data[:n])))
			data = data[n:]
		}
		f.conn.Ingest([]byte("t=r:" + keys))
		return
	}
	enc := base64.StdEncoding.EncodeToString(data)
	if len(enc) <= 4096 {
		if enc != "" {
			f.conn.Ingest([]byte("t=r:" + keys + ";" + enc))
		}
		f.conn.Ingest([]byte("t=r:" + keys))
		return
	}
	f.conn.Ingest([]byte("t=r:" + keys + ":m=1;" + enc[:4096]))
	for enc = enc[4096:]; len(enc) > 0; {
		n := min(len(enc), 4096)
		f.conn.Ingest([]byte("m=1;" + enc[:n]))
		enc = enc[n:]
	}
	f.conn.Ingest([]byte("m=0;"))
}

// run performs one Fetch against the scripted terminal, staging under a
// fresh temp directory. It returns the staging parent so a test can
// prove nothing was written outside it.
func (f *fakeTerm) run(t *testing.T, mimes []string, tweak func(*Request)) (Result, string, error) {
	t.Helper()
	parent := t.TempDir()
	req := Request{
		MIMEs:  mimes,
		Exists: func(string) bool { return false },
		Stage:  func() (string, error) { return os.MkdirTemp(parent, "stage-") },
		Limits: Limits{MaxEntries: 1000, MaxDepth: 16, Idle: 2 * time.Second},
	}
	if tweak != nil {
		tweak(&req)
	}
	in := f.conn.Attach()
	defer in.Close()
	res, err := Fetch(context.Background(), in, req)
	return res, parent, err
}

// uriMIMEs is the MIME list of an ordinary file drop.
var uriMIMEs = []string{MIMEURIList}

// TestFetch_LocalPathsAreNotTransferred pins the same-machine case: the
// files are already readable here, so the only request is for the list
// and nothing is staged.
func TestFetch_LocalPathsAreNotTransferred(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList = "file:///src/a.txt\r\nfile:///src/dir/\r\n"
	staged := false
	res, _, err := f.run(t, uriMIMEs, func(r *Request) {
		r.Exists = func(string) bool { return true }
		stage := r.Stage
		r.Stage = func() (string, error) { staged = true; return stage() }
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := []string{"/src/a.txt", "/src/dir"}; !reflect.DeepEqual(res.Local, want) {
		t.Errorf("Local = %q, want %q", res.Local, want)
	}
	if len(res.Staged) != 0 || res.StageDir != "" || staged {
		t.Errorf("a local drop staged something: %+v", res)
	}
	if got := f.requests(); !reflect.DeepEqual(got, []string{"t=r:x=1"}) {
		t.Errorf("requests = %q, want only the list request", got)
	}
}

// TestFetch_RemoteFile pins the in-band transfer: a drop the terminal
// flags as remote has its bytes fetched, across a chunk chain, into
// staging — byte for byte.
func TestFetch_RemoteFile(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1000) // 16 KB: several chunks
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/big.bin\r\n", true
	f.top[1] = &node{data: data}

	res, _, err := f.run(t, uriMIMEs, func(r *Request) {
		r.Exists = func(string) bool { return true } // remote wins over a same-named local path
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Staged) != 1 || filepath.Base(res.Staged[0]) != "big.bin" {
		t.Fatalf("Staged = %q, want one entry named big.bin", res.Staged)
	}
	got, err := os.ReadFile(res.Staged[0])
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("staged %d bytes that differ from the %d sent", len(got), len(data))
	}
	if !strings.HasPrefix(res.Staged[0], res.StageDir+string(filepath.Separator)) {
		t.Errorf("staged path %q is not under StageDir %q", res.Staged[0], res.StageDir)
	}
}

// TestFetch_UnreadableLocalPathIsFetched pins the fallback that makes
// the machine id an optimisation: a terminal that did not flag the drop
// remote, but named a path that is not here, is asked for the bytes.
func TestFetch_UnreadableLocalPathIsFetched(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList = "file:///Users/me/note.txt\r\n"
	f.top[1] = &node{data: []byte("hi")}

	res, _, err := f.run(t, uriMIMEs, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Local) != 0 || len(res.Staged) != 1 {
		t.Fatalf("result = %+v, want the one entry staged", res)
	}
	if got, _ := os.ReadFile(res.Staged[0]); string(got) != "hi" {
		t.Errorf("staged content = %q, want %q", got, "hi")
	}
}

// TestFetch_RemoteTree pins a dropped folder: every level arrives, a
// symlink is re-created as a link, an empty folder exists, the walk is
// breadth-first, and each directory handle is released — a terminal
// holds resources per open handle.
func TestFetch_RemoteTree(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/kit/\r\n", true
	f.top[1] = &node{isDir: true, children: []*node{
		{name: "a.txt", data: []byte("A")},
		{name: "sub", isDir: true, children: []*node{
			{name: "b.txt", data: []byte("B")},
			{name: "link", isLink: true, target: "b.txt"},
		}},
		{name: "empty", isDir: true},
		{name: "z.txt", data: []byte("Z")},
	}}

	res, _, err := f.run(t, uriMIMEs, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	kit := res.Staged[0]
	for rel, want := range map[string]string{"a.txt": "A", "z.txt": "Z", "sub/b.txt": "B"} {
		if got, err := os.ReadFile(filepath.Join(kit, rel)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	if target, err := os.Readlink(filepath.Join(kit, "sub", "link")); err != nil || target != "b.txt" {
		t.Errorf("link target = %q, %v; want b.txt", target, err)
	}
	if info, err := os.Stat(filepath.Join(kit, "empty")); err != nil || !info.IsDir() {
		t.Errorf("the empty folder is missing: %v", err)
	}

	// Breadth-first: all four children of kit (handle 2) are requested
	// before either child of sub (handle 3).
	want := []string{
		"t=r:x=1", "t=r:x=1:y=1",
		"t=r:Y=2:x=1", "t=r:Y=2:x=2", "t=r:Y=2:x=3", "t=r:Y=2:x=4", "t=r:Y=2",
		"t=r:Y=3:x=1", "t=r:Y=3:x=2", "t=r:Y=3",
		"t=r:Y=4",
	}
	if got := f.requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests =\n %q\nwant\n %q", got, want)
	}
	if got := f.closedHandles(); !reflect.DeepEqual(got, []int{2, 3, 4}) {
		t.Errorf("closed handles = %v, want [2 3 4]", got)
	}
}

// TestFetch_TopLevelSymlink pins a dropped link: it lands as a link.
func TestFetch_TopLevelSymlink(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/latest\r\n", true
	f.top[1] = &node{isLink: true, target: "releases/v2"}

	res, _, err := f.run(t, uriMIMEs, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if target, err := os.Readlink(res.Staged[0]); err != nil || target != "releases/v2" {
		t.Errorf("link target = %q, %v; want releases/v2", target, err)
	}
}

// TestFetch_SameNameTwice pins that two dropped files with one basename
// are both staged: each top-level entry gets its own staging slot, and
// the " copy" ladder is the importer's business, not a staging clash.
func TestFetch_SameNameTwice(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///a/x.txt\r\nfile:///b/x.txt\r\n", true
	f.top[1] = &node{data: []byte("from a")}
	f.top[2] = &node{data: []byte("from b")}

	res, _, err := f.run(t, uriMIMEs, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Staged) != 2 {
		t.Fatalf("Staged = %q, want two entries", res.Staged)
	}
	a, _ := os.ReadFile(res.Staged[0])
	b, _ := os.ReadFile(res.Staged[1])
	if string(a) != "from a" || string(b) != "from b" {
		t.Errorf("staged contents = %q, %q", a, b)
	}
}

// TestFetch_NonFileEntriesKeepTheirIndex pins the numbering: the
// terminal counts every URI line, so the file after an https link is
// entry 2, and asking for entry 1 would fetch the wrong thing.
func TestFetch_NonFileEntriesKeepTheirIndex(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "https://example.com/page\r\nfile:///Users/me/doc.txt\r\n", true
	f.top[2] = &node{data: []byte("doc")}

	res, _, err := f.run(t, []string{"text/plain", MIMEURIList}, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, _ := os.ReadFile(res.Staged[0]); string(got) != "doc" {
		t.Errorf("staged content = %q, want %q", got, "doc")
	}
	if got, want := f.requests(), []string{"t=r:x=2", "t=r:x=2:y=2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

// TestFetch_NoFiles pins the two ways a drop carries nothing to copy:
// no URI list at all (nothing is even requested), and a list of links.
func TestFetch_NoFiles(t *testing.T) {
	f := newFakeTerm(t)
	if _, _, err := f.run(t, []string{"text/plain"}, nil); !errors.Is(err, ErrNoFiles) {
		t.Errorf("no text/uri-list: err = %v, want ErrNoFiles", err)
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("requests = %q, want none", got)
	}

	g := newFakeTerm(t)
	g.uriList = "https://example.com/a\r\n"
	if _, _, err := g.run(t, uriMIMEs, nil); !errors.Is(err, ErrNoFiles) {
		t.Errorf("a list of links: err = %v, want ErrNoFiles", err)
	}
}

// TestFetch_ErrorReply pins that the terminal's own refusal reaches the
// caller by name — EPERM and ENOENT want different words in a flash.
func TestFetch_ErrorReply(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/secret\r\n", true
	f.top[1] = &node{errName: "EPERM"}

	_, _, err := f.run(t, uriMIMEs, nil)
	var re *RemoteError
	if !errors.As(err, &re) || re.Name != "EPERM" {
		t.Errorf("err = %v, want a RemoteError named EPERM", err)
	}
}

// TestFetch_HostileNames is the traversal fence, end to end: a listing
// that names "../evil" — or a URI whose last component is ".." — fails
// the drop, and nothing exists outside the staging directory.
func TestFetch_HostileNames(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/kit/\r\n", true
	f.top[1] = &node{isDir: true, children: []*node{
		{name: "ok.txt", data: []byte("ok")},
		{name: "../evil", data: []byte("escaped")},
	}}
	res, parent, err := f.run(t, uriMIMEs, nil)
	if err == nil {
		t.Fatal("a listing with ../evil: want an error")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Errorf("staging parent holds %d entries, want only the staging directory", len(entries))
	}
	if res.StageDir == "" {
		t.Error("a failed fetch must still report StageDir so the caller can remove it")
	}

	g := newFakeTerm(t)
	g.uriList, g.remote = "file:///Users/me/..\r\n", true
	g.top[1] = &node{data: []byte("x")}
	if _, _, err := g.run(t, uriMIMEs, nil); err == nil {
		t.Error("a URI ending in ..: want an error")
	}
}

// TestFetch_Silence pins that a terminal which stops answering fails
// the drop after the idle window instead of holding the file-op gate —
// and every later paste — forever.
func TestFetch_Silence(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote, f.silent = "file:///Users/me/a.txt\r\n", true, true
	f.top[1] = &node{data: []byte("x")}

	_, _, err := f.run(t, uriMIMEs, func(r *Request) { r.Limits.Idle = 30 * time.Millisecond })
	if !errors.Is(err, ErrSilent) {
		t.Errorf("err = %v, want ErrSilent", err)
	}
}

// TestFetch_MismatchedReply pins that a reply for a request that was
// never made ends the drop: writing it into the file being received
// would silently corrupt it.
func TestFetch_MismatchedReply(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/a.txt\r\n", true
	f.top[1] = &node{data: []byte("x"), wrongKey: true}

	if _, _, err := f.run(t, uriMIMEs, nil); err == nil {
		t.Error("a reply with the wrong keys: want an error")
	}
}

// TestFetch_Limits pins the two bounds on what a drop can make this
// client create: how many entries, and how deep.
func TestFetch_Limits(t *testing.T) {
	wide := newFakeTerm(t)
	wide.uriList, wide.remote = "file:///Users/me/wide/\r\n", true
	dir := &node{isDir: true}
	for i := 0; i < 5; i++ {
		dir.children = append(dir.children, &node{name: fmt.Sprintf("f%d", i), data: []byte("x")})
	}
	wide.top[1] = dir
	_, _, err := wide.run(t, uriMIMEs, func(r *Request) { r.Limits.MaxEntries = 3 })
	if err == nil || !strings.Contains(err.Error(), "3") {
		t.Errorf("past MaxEntries: err = %v, want one naming the limit", err)
	}

	deep := newFakeTerm(t)
	deep.uriList, deep.remote = "file:///Users/me/deep/\r\n", true
	leaf := &node{name: "d4", isDir: true}
	deep.top[1] = &node{isDir: true, children: []*node{
		{name: "d1", isDir: true, children: []*node{
			{name: "d2", isDir: true, children: []*node{
				{name: "d3", isDir: true, children: []*node{leaf}},
			}},
		}},
	}}
	_, _, err = deep.run(t, uriMIMEs, func(r *Request) { r.Limits.MaxDepth = 2 })
	if err == nil {
		t.Error("past MaxDepth: want an error")
	}
}

// TestFetch_Cancelled pins that cancelling the job ends the fetch with
// the context's error rather than waiting out the idle window.
func TestFetch_Cancelled(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote, f.silent = "file:///Users/me/a.txt\r\n", true, true
	f.top[1] = &node{data: []byte("x")}

	ctx, cancel := context.WithCancel(context.Background())
	in := f.conn.Attach()
	defer in.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Fetch(ctx, in, Request{
		MIMEs:  uriMIMEs,
		Stage:  func() (string, error) { return os.MkdirTemp(t.TempDir(), "stage-") },
		Limits: Limits{MaxEntries: 10, MaxDepth: 4, Idle: time.Minute},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestFetch_ReportsProgress pins the narration hook: once per entry
// written, so a long transfer can say how far along it is.
func TestFetch_ReportsProgress(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/kit/\r\n", true
	f.top[1] = &node{isDir: true, children: []*node{
		{name: "a.txt", data: []byte("A")},
		{name: "b.txt", data: []byte("B")},
	}}
	calls := 0
	if _, _, err := f.run(t, uriMIMEs, func(r *Request) { r.Progress = func() { calls++ } }); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 3 {
		t.Errorf("progress called %d times, want 3 (the folder and its two files)", calls)
	}
}

// TestFetch_ZeroLimitsMeanTheDefaults pins that a caller who leaves
// Limits unset gets DefaultLimits, not a fetch that fails on its first
// entry for exceeding a limit of zero.
func TestFetch_ZeroLimitsMeanTheDefaults(t *testing.T) {
	f := newFakeTerm(t)
	f.uriList, f.remote = "file:///Users/me/a.txt\r\n", true
	f.top[1] = &node{data: []byte("x")}
	if _, _, err := f.run(t, uriMIMEs, func(r *Request) { r.Limits = Limits{} }); err != nil {
		t.Errorf("Fetch with zero Limits: %v", err)
	}
}

// TestFetch_KittyFraming pins interoperability with the terminal this
// protocol comes from. kitty does not encode a file once and cut the
// result: every block it reads becomes a complete message, encoded by
// itself. Carrying a partial quantum from one message into the next —
// right for a single chunked stream — scrambles every byte after the
// first block whose length is not a multiple of three.
func TestFetch_KittyFraming(t *testing.T) {
	data := make([]byte, 10_000)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	for _, read := range []int{3072, 1000, 1} {
		t.Run(fmt.Sprintf("%d-byte reads", read), func(t *testing.T) {
			f := newFakeTerm(t)
			f.kittyRead = read
			f.uriList, f.remote = "file:///Users/me/data.bin\r\n", true
			f.top[1] = &node{data: data[:min(len(data), read*40)]}
			want := f.top[1].data

			res, _, err := f.run(t, uriMIMEs, nil)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			got, err := os.ReadFile(res.Staged[0])
			if err != nil {
				t.Fatalf("read staged file: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("staged %d bytes that differ from the %d sent", len(got), len(want))
			}
		})
	}
}

// TestFetch_KittyFramingLongList pins the other kitty shape: a URI list
// longer than one chunk arrives as a chain in which every message
// repeats the header, and the list has to come out whole.
func TestFetch_KittyFramingLongList(t *testing.T) {
	f := newFakeTerm(t)
	f.kittyRead = 3072
	var list strings.Builder
	var want []string
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("/Users/me/projects/some-long-folder-name/file-%03d.txt", i)
		want = append(want, p)
		list.WriteString("file://" + p + "\r\n")
	}
	f.uriList = list.String()

	res, _, err := f.run(t, uriMIMEs, func(r *Request) { r.Exists = func(string) bool { return true } })
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !reflect.DeepEqual(res.Local, want) {
		t.Errorf("got %d local paths, want %d; first = %q", len(res.Local), len(want), res.Local[:min(len(res.Local), 1)])
	}
}
