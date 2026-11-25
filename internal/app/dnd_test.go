// =============================================================================
// File: internal/app/dnd_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the protocol route. The terminal is scripted: it is the
// Conn's writer, so it sees every sequence the editor sends, and it
// answers data requests through Conn.Ingest the way the tty filter
// delivers them. The wire details are internal/dnd's own tests; these
// pin what the editor does with a hover and a drop.

package app

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/dnd"
)

// dndTerm is the terminal end of the drag-and-drop protocol.
type dndTerm struct {
	mu   sync.Mutex
	wire []string // bodies the editor wrote, in order
	conn *dnd.Conn

	uriList string
	remote  bool           // flag the URI list as from another machine
	files   map[int]string // URI-list sub-index → file content
	failAs  string         // answer entry requests with this POSIX error
	silent  bool           // never answer entry requests
}

// Write receives the editor's sequences and answers data requests.
func (d *dndTerm) Write(p []byte) (int, error) {
	for _, seq := range strings.Split(string(p), "\x1b\\") {
		if seq == "" {
			continue
		}
		body := strings.TrimPrefix(seq, "\x1b]72;")
		d.mu.Lock()
		d.wire = append(d.wire, body)
		conn := d.conn
		d.mu.Unlock()
		if conn != nil {
			d.answer(conn, body)
		}
	}
	return len(p), nil
}

// answer replies to a request for the URI list or one of its entries.
func (d *dndTerm) answer(conn *dnd.Conn, body string) {
	m, err := dnd.Parse([]byte(body))
	if err != nil || m.Type != 'r' || m.HasOp || !m.HasX {
		return
	}
	if !m.HasY {
		keys := fmt.Sprintf("x=%d", m.X)
		if d.remote {
			keys += ":X=1"
		}
		conn.Ingest([]byte("t=r:" + keys + ";" + base64.StdEncoding.EncodeToString([]byte(d.uriList))))
		conn.Ingest([]byte("t=r:" + keys))
		return
	}
	keys := fmt.Sprintf("x=%d:y=%d", m.X, m.Y)
	switch content, ok := d.files[m.Y]; {
	case d.silent:
	case d.failAs != "":
		conn.Ingest([]byte("t=R:" + keys + ";" + d.failAs + ":scripted"))
	case !ok:
		conn.Ingest([]byte("t=R:" + keys + ";ENOENT"))
	default:
		conn.Ingest([]byte("t=r:" + keys + ";" + base64.StdEncoding.EncodeToString([]byte(content))))
		conn.Ingest([]byte("t=r:" + keys))
	}
}

// sent returns every body the editor has written.
func (d *dndTerm) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.wire...)
}

// count returns how many times body was written.
func (d *dndTerm) count(body string) int {
	n := 0
	for _, b := range d.sent() {
		if b == body {
			n++
		}
	}
	return n
}

// forget drops what has been written so far.
func (d *dndTerm) forget() {
	d.mu.Lock()
	d.wire = nil
	d.mu.Unlock()
}

// dndFixture is the drop fixture with a scripted terminal attached.
type dndFixture struct {
	dropFixture
	term *dndTerm
	conn *dnd.Conn
}

// newDndFixture attaches a scripted terminal to a fresh project. The
// machine-id lookup is stubbed out — the real one shells out — and the
// environment says "not inside a multiplexer", wherever the suite runs.
func newDndFixture(t *testing.T) dndFixture {
	t.Helper()
	for _, k := range []string{"TMUX", "STY", "ZELLIJ"} {
		t.Setenv(k, "")
	}
	f := dndFixture{dropFixture: newDropFixture(t), term: &dndTerm{files: map[int]string{}}}
	f.a.dndMachineID = func() (string, error) { return "", os.ErrNotExist }
	f.conn = f.a.attachDnd(f.term)
	f.term.mu.Lock()
	f.term.conn = f.conn
	f.term.mu.Unlock()
	// Nothing is accepted until the terminal has been told which
	// machine this is; wait for that, as a real session does within a
	// few milliseconds of starting.
	pumpUntil(t, f.a, "the machine id", func() bool { return f.a.dndIDSent })
	show(f.a)
	return f
}

// drop plays a drag that ends in a drop at (x, y): the hover the
// terminal sends first — which the editor has to accept, or the OS
// would never let go there — and then the drop itself.
func (f dndFixture) drop(t *testing.T, x, y, ops int) {
	t.Helper()
	f.hover(t, fmt.Sprintf("t=m:x=%d:y=%d:o=%d;text/uri-list", x, y, ops))
	f.term.forget()
	f.conn.Ingest([]byte(fmt.Sprintf("t=M:x=%d:y=%d:o=%d;text/uri-list", x, y, ops)))
}

// hover delivers a hover message through the same path the tty filter
// uses, and waits for the loop to have handled it. The wait is a marker
// event posted behind it: the queue is first-in first-out, so once the
// marker has landed the hover has too — whether or not it changed
// anything a test could otherwise watch for.
func (f dndFixture) hover(t *testing.T, body string) {
	t.Helper()
	f.conn.Ingest([]byte(body))
	landed := false
	if err := f.a.screen.PostEvent(asyncjob.Notify(func() { landed = true })); err != nil {
		t.Fatalf("post marker: %v", err)
	}
	pumpUntil(t, f.a, "the hover", func() bool { return landed })
}

// fileURI spells path as a text/uri-list entry.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// stagingLeftovers lists what is left under the staging root.
func stagingLeftovers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "skiff", "drops"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestDnd_RegistersForDrops pins startup: in a project the editor tells
// the terminal it accepts file drops. It asks nothing first — a
// terminal that answers the handshake may still deliver nothing.
func TestDnd_RegistersForDrops(t *testing.T) {
	f := newDndFixture(t)
	got := f.term.sent()
	if len(got) != 2 || got[0] != "t=a;text/uri-list" || !strings.HasPrefix(got[1], "t=a:x=1;1:") {
		t.Errorf("startup wrote %q, want the accept message and then a machine id", got)
	}
}

// TestDnd_UnknownMachineStillSendsAnID pins the fallback. With no id at
// all the terminal assumes the editor is on its own machine and hands
// over bare paths — and a path that happens to exist on BOTH machines
// would then be copied from the wrong one, silently. An id that can
// match nothing makes the terminal send the bytes instead.
func TestDnd_UnknownMachineStillSendsAnID(t *testing.T) {
	f := newDndFixture(t) // its lookup fails
	g := newDndFixture(t)
	idOf := func(sent []string) string {
		for _, b := range sent {
			if strings.HasPrefix(b, "t=a:x=1;") {
				return strings.TrimPrefix(b, "t=a:x=1;")
			}
		}
		return ""
	}
	a, b := idOf(f.term.sent()), idOf(g.term.sent())
	if len(a) != 2+64 || !strings.HasPrefix(a, "1:") {
		t.Fatalf("fallback id = %q, want version 1 and 64 hex digits", a)
	}
	if a == b {
		t.Errorf("two editors with no machine id sent the same fallback %q", a)
	}
}

// TestDnd_NothingIsAcceptedBeforeTheID pins the gate on that fallback:
// a drag hovering before the id has gone out is refused, so there is no
// window in which the terminal can mistake a remote editor for a local
// one.
func TestDnd_NothingIsAcceptedBeforeTheID(t *testing.T) {
	for _, k := range []string{"TMUX", "STY", "ZELLIJ"} {
		t.Setenv(k, "")
	}
	d := newDropFixture(t)
	release := make(chan struct{})
	d.a.dndMachineID = func() (string, error) { <-release; return "1:abc", nil }
	term := &dndTerm{}
	conn := d.a.attachDnd(term)
	defer close(release)

	conn.Ingest([]byte("t=m:x=3:y=3:o=1;text/uri-list"))
	pumpUntil(t, d.a, "the refusal", func() bool { return term.count("t=m:o=0") == 1 })
	if d.a.tree.DropTarget != "" {
		t.Error("a folder lit up before the machine id was sent")
	}
}

// TestDnd_MachineIDFollowsRegistration pins the second message: once
// the id is known it is sent, so the terminal can tell a local drop
// from one it must transfer.
func TestDnd_MachineIDFollowsRegistration(t *testing.T) {
	for _, k := range []string{"TMUX", "STY", "ZELLIJ"} {
		t.Setenv(k, "")
	}
	d := newDropFixture(t)
	term := &dndTerm{}
	d.a.dndMachineID = func() (string, error) { return "1:abc123", nil }
	d.a.attachDnd(term)
	pumpUntil(t, d.a, "the machine id", func() bool { return term.count("t=a:x=1;1:abc123") == 1 })
}

// TestDnd_NotRegisteredInsideAMultiplexer pins the tmux rule: tmux
// swallows the registration and cannot route a drop back to a pane, so
// the editor does not pretend. The pasted-path route still works there.
func TestDnd_NotRegisteredInsideAMultiplexer(t *testing.T) {
	for _, env := range []string{"TMUX", "STY", "ZELLIJ"} {
		t.Run(env, func(t *testing.T) {
			for _, k := range []string{"TMUX", "STY", "ZELLIJ"} {
				t.Setenv(k, "")
			}
			d := newDropFixture(t)
			t.Setenv(env, "1")
			term := &dndTerm{}
			d.a.attachDnd(term)
			if got := term.sent(); len(got) != 0 {
				t.Errorf("inside %s the editor wrote %q, want nothing", env, got)
			}
		})
	}
}

// TestDnd_DetachStopsAccepting pins the exit path: the terminal is told
// drops are no longer wanted, so it does not send hover events to
// whatever runs in the pane next.
func TestDnd_DetachStopsAccepting(t *testing.T) {
	f := newDndFixture(t)
	f.term.forget()
	f.a.detachDnd()
	if got := f.term.sent(); len(got) != 1 || got[0] != "t=A" {
		t.Errorf("detach wrote %q, want only t=A", got)
	}
	f.term.forget()
	f.a.detachDnd()
	if got := f.term.sent(); len(got) != 0 {
		t.Errorf("a second detach wrote %q, want nothing", got)
	}
}

// TestDnd_HoverLightsTheRowAndAnswersOnce pins live feedback: the
// folder under the drag is lit, the terminal is told the drop would be
// accepted — and told once, not on every one of the dozens of hover
// messages a drag produces, each of which would be a write over SSH.
func TestDnd_HoverLightsTheRowAndAnswersOnce(t *testing.T) {
	f := newDndFixture(t)
	f.term.forget()
	y := treeRowY(t, f.a, "src")
	f.hover(t, fmt.Sprintf("t=m:x=3:y=%d:o=3;text/uri-list", y))

	if f.a.tree.DropTarget != f.src {
		t.Errorf("DropTarget = %q, want %q", f.a.tree.DropTarget, f.src)
	}
	if !strings.Contains(f.a.statusMsg, "Drop to copy into src/") {
		t.Errorf("flash = %q, want it to name the folder", f.a.statusMsg)
	}
	f.hover(t, fmt.Sprintf("t=m:x=4:y=%d:o=3", y))
	f.hover(t, fmt.Sprintf("t=m:x=5:y=%d:o=3", y))
	if n := f.term.count("t=m:o=1;text/uri-list"); n != 1 {
		t.Errorf("accepted %d times across three hovers on one row, want 1; wire = %q", n, f.term.sent())
	}
}

// TestDnd_HoverOffTheTreeMeansTheActiveFolder pins the rule for a drag
// over the editor: it is still accepted, and the active folder is lit
// so the user can see where it would go.
func TestDnd_HoverOffTheTreeMeansTheActiveFolder(t *testing.T) {
	f := newDndFixture(t)
	f.a.setActiveFolder(f.docs)
	ex, ey, _, _ := f.a.editorRect()
	f.hover(t, fmt.Sprintf("t=m:x=%d:y=%d:o=1;text/uri-list", ex+10, ey+3))
	if f.a.tree.DropTarget != f.docs {
		t.Errorf("DropTarget = %q, want the active folder %q", f.a.tree.DropTarget, f.docs)
	}
}

// TestDnd_HoverIsRefused pins the two refusals: a drag that carries no
// files (text from a browser), and a drag over a modal the user has to
// answer first. Refusing is what makes the OS show "not here".
func TestDnd_HoverIsRefused(t *testing.T) {
	t.Run("no files offered", func(t *testing.T) {
		f := newDndFixture(t)
		f.term.forget()
		f.hover(t, "t=m:x=3:y=3:o=1;text/plain text/html")
		if f.term.count("t=m:o=0") != 1 || f.a.tree.DropTarget != "" {
			t.Errorf("wire = %q, DropTarget = %q; want one refusal and no highlight", f.term.sent(), f.a.tree.DropTarget)
		}
	})
	t.Run("an overlay is up", func(t *testing.T) {
		f := newDndFixture(t)
		f.a.openMenu()
		f.term.forget()
		f.hover(t, "t=m:x=3:y=3:o=1;text/uri-list")
		if f.term.count("t=m:o=0") != 1 || f.a.tree.DropTarget != "" {
			t.Errorf("wire = %q, DropTarget = %q; want one refusal and no highlight", f.term.sent(), f.a.tree.DropTarget)
		}
		if !f.a.menuOpen {
			t.Error("the hover closed the menu")
		}
	})
}

// TestDnd_LeavingClearsTheHighlight pins the -1,-1 message: a drag that
// goes elsewhere must not leave a folder lit.
func TestDnd_LeavingClearsTheHighlight(t *testing.T) {
	f := newDndFixture(t)
	f.hover(t, fmt.Sprintf("t=m:x=3:y=%d:o=1;text/uri-list", treeRowY(t, f.a, "src")))
	f.hover(t, "t=m:x=-1:y=-1")
	if f.a.tree.DropTarget != "" || f.a.dropPending() {
		t.Error("the highlight survived the drag leaving")
	}
	if strings.Contains(f.a.statusMsg, "Drop to copy") {
		t.Errorf("flash = %q, want the hover hint gone", f.a.statusMsg)
	}
}

// TestDnd_LocalDropCopiesThenFinishes pins a same-machine drop end to
// end: the list is requested, the file is copied into the folder under
// the drop, and only then is the drop finished — macOS only keeps
// dropped files alive until the terminal is told it is over.
func TestDnd_LocalDropCopiesThenFinishes(t *testing.T) {
	f := newDndFixture(t)
	f.term.uriList = fileURI(f.outside) + "\r\n"
	y := treeRowY(t, f.a, "src")
	f.hover(t, fmt.Sprintf("t=m:x=3:y=%d:o=3;text/uri-list", y))
	f.term.forget()
	f.conn.Ingest([]byte(fmt.Sprintf("t=M:x=3:y=%d:o=3;text/uri-list", y)))

	pumpUntil(t, f.a, "the drop", func() bool { return strings.Contains(f.a.statusMsg, "Copied") })
	if got := f.landed(t, f.src, "my photo.png"); got != "pixels" {
		t.Fatalf("content in src/ = %q, want %q", got, "pixels")
	}
	if got, want := f.term.sent(), []string{"t=r:x=1", "t=r:o=1"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("wire = %q, want %q", got, want)
	}
	if f.a.tree.DropTarget != "" || f.a.dropPending() {
		t.Error("the highlight outlived the drop")
	}
}

// TestDnd_RemoteDropIsTransferred pins the reason the protocol route
// exists: the file is on another machine, its bytes arrive in-band, and
// it ends up in the project with nothing left in staging.
func TestDnd_RemoteDropIsTransferred(t *testing.T) {
	f := newDndFixture(t)
	f.term.uriList, f.term.remote = "file:///Users/someone/Desktop/report.txt\r\n", true
	f.term.files[1] = "quarterly numbers"
	f.drop(t, 3, treeRowY(t, f.a, "docs"), 1)

	pumpUntil(t, f.a, "the drop", func() bool { return strings.Contains(f.a.statusMsg, "Copied") })
	if got := f.landed(t, f.docs, "report.txt"); got != "quarterly numbers" {
		t.Fatalf("content in docs/ = %q, want the transferred bytes", got)
	}
	if left := stagingLeftovers(t); len(left) != 0 {
		t.Errorf("staging still holds %q", left)
	}
	sent := f.term.sent()
	if last := sent[len(sent)-1]; last != "t=r:o=1" {
		t.Errorf("last message = %q, want the drop finished as a copy", last)
	}
}

// TestDnd_FailedTransferCancelsTheDrop pins the failure path: the
// terminal refuses the file, so the drop is finished as cancelled, the
// user is told why, and neither the project nor staging keeps anything.
func TestDnd_FailedTransferCancelsTheDrop(t *testing.T) {
	f := newDndFixture(t)
	f.term.uriList, f.term.remote, f.term.failAs = "file:///Users/someone/secret.txt\r\n", true, "EPERM"
	f.drop(t, 3, treeRowY(t, f.a, "docs"), 1)

	pumpUntil(t, f.a, "the failure", func() bool { return f.a.statusErr })
	pumpUntil(t, f.a, "the job", idle(&f.a.fileOp))
	if !strings.Contains(f.a.statusMsg, "EPERM") {
		t.Errorf("flash = %q, want it to carry the terminal's reason", f.a.statusMsg)
	}
	sent := f.term.sent()
	if last := sent[len(sent)-1]; last != "t=r:o=0" {
		t.Errorf("last message = %q, want the drop cancelled", last)
	}
	if left := stagingLeftovers(t); len(left) != 0 {
		t.Errorf("staging still holds %q", left)
	}
	entries, _ := os.ReadDir(f.docs)
	if len(entries) != 1 {
		t.Errorf("docs/ holds %d entries, want its original 1", len(entries))
	}
}

// TestDnd_DropWithoutFilesIsCancelled pins a drop the editor cannot
// use: it is finished as cancelled at once, with no file op started.
func TestDnd_DropWithoutFilesIsCancelled(t *testing.T) {
	f := newDndFixture(t)
	f.term.forget()
	f.conn.Ingest([]byte("t=M:x=3:y=3:o=1;text/plain"))
	pumpUntil(t, f.a, "the refusal", func() bool { return f.term.count("t=r:o=0") == 1 })
	if f.a.fileOp.Busy() {
		t.Error("a drop with no files started a file operation")
	}
}

// TestDnd_DropTheEditorNeverAcceptedIsRefused pins the second fence
// behind the paste rule. A real drop can only follow a hover the editor
// accepted — until then the OS shows "not here" and will not let go —
// so a drop event with no accepted hover before it did not come from a
// drag. It is cancelled, and nothing is requested or copied.
func TestDnd_DropTheEditorNeverAcceptedIsRefused(t *testing.T) {
	f := newDndFixture(t)
	f.term.uriList = fileURI(f.outside) + "\r\n"
	f.term.forget()
	f.conn.Ingest([]byte(fmt.Sprintf("t=M:x=3:y=%d:o=1;text/uri-list", treeRowY(t, f.a, "src"))))
	pumpUntil(t, f.a, "the refusal", func() bool { return f.term.count("t=r:o=0") == 1 })
	if got := f.term.sent(); len(got) != 1 {
		t.Errorf("wire = %q, want only the cancellation", got)
	}
	if f.a.fileOp.Busy() || f.landed(t, f.src, "my photo.png") != "" {
		t.Error("an unaccepted drop copied a file")
	}
}

// TestDnd_MoveIsReportedOnlyWhenEverythingArrived pins what the source
// is told. A source that allows only "move" deletes its originals when
// it hears the drop was a move — so when one of two files failed to
// land, saying "move" would delete a file that exists nowhere else.
// The drop is reported as a copy instead, and the source keeps both.
func TestDnd_MoveIsReportedOnlyWhenEverythingArrived(t *testing.T) {
	t.Run("everything landed", func(t *testing.T) {
		f := newDndFixture(t)
		f.term.uriList = fileURI(f.outside) + "\r\n"
		f.drop(t, 3, treeRowY(t, f.a, "src"), 2)
		pumpUntil(t, f.a, "the drop", func() bool { return strings.Contains(f.a.statusMsg, "Copied") })
		pumpUntil(t, f.a, "the job", idle(&f.a.fileOp))
		if sent := f.term.sent(); sent[len(sent)-1] != "t=r:o=2" {
			t.Errorf("last message = %q, want the move the source asked for", sent[len(sent)-1])
		}
	})
	t.Run("one of two failed", func(t *testing.T) {
		f := newDndFixture(t)
		pipe := filepath.Join(filepath.Dir(f.outside), "a-pipe")
		if err := syscall.Mkfifo(pipe, 0o644); err != nil {
			t.Skip("mkfifo unavailable:", err)
		}
		f.term.uriList = fileURI(f.outside) + "\r\n" + fileURI(pipe) + "\r\n"
		f.drop(t, 3, treeRowY(t, f.a, "src"), 2)
		pumpUntil(t, f.a, "the partial copy", func() bool { return f.a.statusErr })
		pumpUntil(t, f.a, "the job", idle(&f.a.fileOp))
		if !strings.Contains(f.a.statusMsg, "Copied 1 of 2") {
			t.Errorf("flash = %q, want the partial result", f.a.statusMsg)
		}
		if sent := f.term.sent(); sent[len(sent)-1] != "t=r:o=1" {
			t.Errorf("last message = %q, want a copy so the source keeps its originals", sent[len(sent)-1])
		}
	})
}

// TestDnd_SecondDropWhileOneIsRunning pins the file-op gate: one copy
// at a time. The drop that loses is cancelled on the wire and the user
// is told, instead of two transfers interleaving their replies.
func TestDnd_SecondDropWhileOneIsRunning(t *testing.T) {
	f := newDndFixture(t)
	f.a.dndLimits = dnd.Limits{MaxEntries: 10, MaxDepth: 4, Idle: 300 * time.Millisecond}
	f.term.uriList, f.term.remote, f.term.silent = "file:///Users/someone/slow.bin\r\n", true, true
	f.drop(t, 3, 3, 1)
	pumpUntil(t, f.a, "the first drop to start", f.a.fileOp.Busy)

	f.term.forget()
	f.conn.Ingest([]byte("t=M:x=3:y=3:o=1;text/uri-list"))
	pumpUntil(t, f.a, "the refusal", func() bool { return f.term.count("t=r:o=0") >= 1 })
	if !strings.Contains(f.a.statusMsg, "still running") {
		t.Errorf("flash = %q, want the busy notice", f.a.statusMsg)
	}
	pumpUntil(t, f.a, "the first drop to time out", idle(&f.a.fileOp))
}

// TestDnd_SingleFileModeDoesNotRegister pins that `skiff file.txt` has
// no explorer to drop on and so asks the terminal for nothing.
func TestDnd_SingleFileModeDoesNotRegister(t *testing.T) {
	for _, k := range []string{"TMUX", "STY", "ZELLIJ"} {
		t.Setenv(k, "")
	}
	file := filepath.Join(t.TempDir(), "one.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scr.Fini)
	a := newSingleFileApp(scr, file)
	term := &dndTerm{}
	a.attachDnd(term)
	if got := term.sent(); len(got) != 0 {
		t.Errorf("single-file mode wrote %q, want nothing", got)
	}
}

// TestStageRoot_LivesUnderTheStateDirectory pins where transfers are
// staged: skiff's own state directory — on disk and out of the project
// — and a fresh directory per drop.
func TestStageRoot_LivesUnderTheStateDirectory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	first, err := a.stageRoot()
	if err != nil {
		t.Fatalf("stageRoot: %v", err)
	}
	second, err := a.stageRoot()
	if err != nil {
		t.Fatalf("stageRoot: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(first); os.RemoveAll(second) })
	want := filepath.Join(os.Getenv("XDG_STATE_HOME"), "skiff", "drops")
	if filepath.Dir(first) != want {
		t.Errorf("staging at %q, want it under %q", first, want)
	}
	if first == second {
		t.Error("two drops share one staging directory")
	}
}
