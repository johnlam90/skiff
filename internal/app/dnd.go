// =============================================================================
// File: internal/app/dnd.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// dnd.go is the app half of the drag-and-drop protocol route (OSC 72,
// internal/dnd): where a terminal delivers real drag events, the folder
// under the pointer lights up while the user is still dragging, the
// drop lands exactly there with no bar in between, and a file from the
// user's laptop reaches an editor running over SSH because the terminal
// sends its bytes.
//
// It shares everything it can with the pasted-path route in
// filedrop.go: the drop target, the lit tree row, spring-loaded
// folders, the edge scroll, and the import that ends both.
//
// Three decisions shape the file.
//
// The editor registers and never asks. The protocol has a handshake,
// and it is not evidence: Rex answers it and cannot deliver a drop. So
// attachDnd announces "I accept file drops" once and then acts only on
// events that actually arrive. The pasted-path route stays on
// regardless — on Rex, Herdr and Ghostty it is the one that fires.
//
// Hover and drop events reach the loop as a Notify, like every other
// goroutine hand-off; file data does not come through the loop at all.
// It flows from the tty reader to the fetch on the fileOp job, so a
// hundred-megabyte drop is neither a hundred thousand events nor disk
// writes on the event loop.
//
// The drop is finished only after the copy. On macOS the files a drop
// names are guaranteed to exist only until the terminal is told the
// drop is over.

package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/dnd"
	"github.com/johnlam90/skiff/internal/filemanager"
)

// dndHoverHint opens the flash shown while a drag hovers; the folder
// follows it. A const so leaving the window can recognise — and clear —
// its own flash without wiping someone else's.
const dndHoverHint = "Drop to copy into "

// dndPostRetries and dndPostDelay bound how hard the tty reader tries
// to hand the loop a drop event when its queue is full: about a second,
// the same patience a job's landing gets.
const (
	dndPostRetries = 40
	dndPostDelay   = 25 * time.Millisecond
)

// multiplexed reports whether the editor runs inside tmux, screen or
// zellij. None of them can route a drag event to a pane, and tmux drops
// the registration on the floor, so the protocol route is not offered
// there — a drop still arrives as a pasted path, which they do pass.
func multiplexed() bool {
	return os.Getenv("TMUX") != "" || os.Getenv("STY") != "" || os.Getenv("ZELLIJ") != ""
}

// attachDnd connects the app to the terminal's drag-and-drop traffic
// and returns the Conn whose Ingest the tty filter should feed. In a
// project outside a multiplexer it also registers for file drops and
// looks up the machine id in the background — the lookup shells out on
// macOS, and startup does not wait for it.
func (a *App) attachDnd(out io.Writer) *dnd.Conn {
	scr := a.screen
	conn := dnd.NewConn(out, func(m dnd.Msg) {
		postDnd(scr, m.Type == 'M', func() { a.handleDnd(m) })
	})
	a.dnd = conn
	a.dndLastReply = -1
	if a.tree == nil || multiplexed() {
		return conn
	}
	if err := conn.Send(dnd.Accept(dnd.MIMEURIList)); err != nil {
		return conn
	}
	a.dndRegistered = true
	lookup := a.dndMachineID
	if lookup == nil {
		lookup = dnd.MachineID
	}
	a.safeGo("dnd-machine-id", func() {
		id, err := lookup()
		if err != nil {
			// Never "no id": see dnd.RandomMachineID for why silence
			// can copy the wrong machine's file.
			id = dnd.RandomMachineID()
		}
		_ = scr.PostEvent(asyncjob.Notify(func() {
			// Checked on the loop: an id that lands after the editor
			// has stopped accepting must not re-open the door.
			if a.dndRegistered && conn.Send(dnd.MachineIDMsg(id)) == nil {
				a.dndIDSent = true
			}
		}))
	})
	return conn
}

// detachDnd tells the terminal the editor no longer takes drops, and
// cancels a transfer the exit is cutting short. Close calls it on every
// way out, so the terminal does not go on sending hover events to
// whatever runs in the pane next.
func (a *App) detachDnd() {
	if a.dnd == nil || !a.dndRegistered {
		return
	}
	a.dndRegistered = false
	if a.dndFetching {
		_ = a.dnd.Send(dnd.Finish(dnd.OpNone))
	}
	_ = a.dnd.Send(dnd.Stop())
}

// postDnd hands one protocol event to the loop from the tty reader's
// goroutine. A hover that finds the queue full is dropped — the next
// one replaces it — but a drop event is retried, because losing it
// leaves the terminal waiting on a drop nobody will ever finish.
func postDnd(scr tcell.Screen, must bool, land func()) {
	ev := asyncjob.Notify(land)
	tries := 1
	if must {
		tries = dndPostRetries
	}
	for i := 0; i < tries; i++ {
		if scr.PostEvent(ev) == nil {
			return
		}
		time.Sleep(dndPostDelay)
	}
}

// handleDnd is the on-loop half of a protocol event: a hover (which
// includes the drag leaving) or the drop itself.
func (a *App) handleDnd(m dnd.Msg) {
	if a.dnd == nil || !a.dndRegistered {
		return
	}
	switch m.Type {
	case 'm':
		a.dndHover(m)
	case 'M':
		a.dndDrop(m)
	}
}

// dndOp decides what the editor would do with a drop of mimes right
// now: OpNone when it cannot take it — no project, the terminal not yet
// told which machine this is, a modal the user has to answer first, a
// copy already running, or nothing file-shaped on offer — and otherwise
// copy, or move when the source allows only that.
func (a *App) dndOp(mimes []string, m dnd.Msg) int {
	if a.tree == nil || !a.dndIDSent || a.overlays.Top() != nil || a.fileOp.Busy() {
		return dnd.OpNone
	}
	if !slices.Contains(mimes, dnd.MIMEURIList) {
		return dnd.OpNone
	}
	switch {
	case !m.HasOp || m.Op&dnd.OpCopy != 0:
		return dnd.OpCopy
	case m.Op&dnd.OpMove != 0:
		return dnd.OpMove
	}
	return dnd.OpNone
}

// dndHover handles the drag moving over the window. The offered MIME
// list is only sent with the first hover of a drag, so it is remembered
// outside the drop state, which a refusal clears. The folder under the
// pointer is lit and named in a flash, and the terminal is told whether
// a drop here would be taken.
func (a *App) dndHover(m dnd.Msg) {
	if (m.HasX && m.X < 0) || (m.HasY && m.Y < 0) {
		a.dndLeave()
		return
	}
	if mimes := dnd.MIMEs(m.Payload); mimes != nil {
		a.dndMimes = mimes
	}
	op := a.dndOp(a.dndMimes, m)
	if op == dnd.OpNone {
		a.endDndHover()
		a.dndReply(dnd.OpNone)
		return
	}
	if a.drop.mode != dropHovering {
		a.clearDrop()
		a.drop.mode = dropHovering
		a.drop.base = a.dropBase()
		if a.sidebarShown && a.gitPanel.active {
			a.showExplorerPanel()
		}
	}
	dir, node := a.dropFolderAt(m.X, m.Y)
	if dir != a.drop.target {
		a.setDropTarget(dir)
		a.flash(dndHoverHint + a.relativeFolderLabel(dir))
	}
	a.dropHoverAt(node, m.X, m.Y)
	a.dndReply(op)
}

// dndReply tells the terminal what a drop would do here — but only
// when the answer changes. A drag produces a hover message per cell
// crossed, and answering each one is a write per cell over SSH for an
// answer the terminal already has.
func (a *App) dndReply(op int) {
	if op == a.dndLastReply {
		return
	}
	a.dndLastReply = op
	if op == dnd.OpNone {
		_ = a.dnd.Send(dnd.Reply(dnd.OpNone))
		return
	}
	_ = a.dnd.Send(dnd.Reply(op, dnd.MIMEURIList))
}

// endDndHover takes down a protocol hover's highlight and its flash. A
// pending placement bar is not a hover and is left alone.
func (a *App) endDndHover() {
	if a.drop.mode != dropHovering {
		return
	}
	a.clearDrop()
	if strings.HasPrefix(a.statusMsg, dndHoverHint) {
		a.statusMsg = ""
	}
}

// dndLeave handles the drag leaving the window: nothing stays lit, and
// the next drag starts with no memory of this one's offer or answer.
func (a *App) dndLeave() {
	a.endDndHover()
	a.dndMimes = nil
	a.dndLastReply = -1
}

// dndDrop handles the drop itself. The event carries its own cell, so
// the target is resolved from it rather than from the last hover's. A
// drop the editor cannot take is finished as cancelled at once; the
// terminal is waiting on that answer either way.
//
// A drop is only honoured after a hover the editor accepted. That is
// not a formality: until a program accepts, the OS shows "not here" and
// will not let the user drop, so a drop event with no acceptance before
// it did not come from a drag — and this route copies with no bar and
// no click, so it must not be reachable by anything but one.
func (a *App) dndDrop(m dnd.Msg) {
	mimes := dnd.MIMEs(m.Payload)
	if mimes == nil {
		mimes = a.dndMimes
	}
	busy := a.fileOp.Busy()
	op := a.dndOp(mimes, m)
	if a.dndLastReply != dnd.OpCopy && a.dndLastReply != dnd.OpMove {
		op = dnd.OpNone
	}
	if a.drop.mode == dropIdle {
		a.drop.base = a.dropBase()
	}
	dir, _ := a.dropFolderAt(m.X, m.Y)
	a.dndLeave()
	if op == dnd.OpNone {
		_ = a.dnd.Send(dnd.Finish(dnd.OpNone))
		if busy {
			a.flash("Another file operation is still running")
		}
		return
	}
	a.startDndFetch(mimes, op, dir)
}

// startDndFetch runs one drop on the file-op job: fetch what it names
// (dnd.Fetch decides, entry by entry, between a path on this machine
// and an in-band transfer into staging), import both kinds into dst,
// and only then tell the terminal the drop is finished. Staging is
// removed whatever happens.
func (a *App) startDndFetch(mimes []string, op int, dst string) {
	conn, files := a.dnd, a.files
	limits := a.dndLimits
	stage := a.stageRoot
	progress := a.fileOpProgress("Copy")
	started := a.fileOp.Start(func(ctx context.Context) (fileOpResult, error) {
		res := fileOpResult{label: "Copy", imported: true, dst: dst}
		in := conn.Attach()
		defer in.Close()
		got, err := dnd.Fetch(ctx, in, dnd.Request{
			MIMEs: mimes, Exists: pathExists, Stage: stage, Limits: limits, Progress: progress,
		})
		if got.StageDir != "" {
			defer os.RemoveAll(got.StageDir)
		}
		if err != nil {
			_ = conn.Send(dnd.Finish(dnd.OpNone))
			return res, err
		}
		res.want = len(got.Local) + len(got.Staged)
		var errs []error
		for _, batch := range []struct {
			srcs []string
			move bool
		}{{got.Local, false}, {got.Staged, true}} {
			if len(batch.srcs) == 0 {
				continue
			}
			var cs filemanager.Changeset
			cs, err = files.Import(batch.srcs, dst, batch.move, progress)
			res.cs.Added = append(res.cs.Added, cs.Added...)
			if err != nil {
				errs = append(errs, err)
			}
		}
		// What the source hears decides what it does with its
		// originals. "Move" tells it to delete them, so that is only
		// said when every one of them is now here; a partial result is
		// reported as the copy it was, and nothing as a cancellation.
		switch landed := len(res.cs.Added); {
		case landed == 0:
			op = dnd.OpNone
		case landed < res.want:
			op = dnd.OpCopy
		}
		_ = conn.Send(dnd.Finish(op))
		return res, errors.Join(errs...)
	})
	if !started {
		_ = conn.Send(dnd.Finish(dnd.OpNone))
		a.flash("Another file operation is still running")
		return
	}
	a.dndFetching = true
	a.flash("Receiving the drop…")
}

// pathExists reports whether a path the terminal named is on this
// machine. Lstat, like the paste route: a dangling link is still there.
func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// stageRoot creates a fresh directory for one drop's transferred files
// under skiff's state directory — $XDG_STATE_HOME/skiff/drops, the same
// convention the session store follows. Not the project: a transfer in
// flight would show up in the tree and in `git status`. And not the
// system temp directory unless there is no home at all: on many Linux
// boxes /tmp is RAM, and a dropped folder can be bigger than that.
func (a *App) stageRoot() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return os.MkdirTemp("", "skiff-drop-")
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "skiff", "drops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.MkdirTemp("", "skiff-drop-")
	}
	return os.MkdirTemp(dir, "drop-")
}
