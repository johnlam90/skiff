// =============================================================================
// File: internal/app/filedrop.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// filedrop.go turns a file dropped on the editor into a copy in the
// project. It holds the drop model both routes share — which sources,
// which folder, which tree row is lit — and the whole of the pasted-path
// route; the protocol route's wire work lives in dnd.go.
//
// The pasted-path route exists because of what terminals actually do.
// Rex, Herdr and Ghostty do not tell a program a file was dropped on
// it: they paste the file's path. Two things follow, and the placement
// bar is the answer to both.
//
// The paste carries no pointer position, so the editor cannot know
// which folder the user let go over. The bar waits for the one event
// that does carry a position: a click. The pointer is still resting on
// the row the user dropped on, so the gesture is "drop, click".
//
// And the paste is byte-identical to the same path pasted as text.
// Copying on sight would turn a pasted /etc/hosts — or a pasted folder
// path — into a file copy, so nothing is copied until the user answers
// the bar, and the bar is built so that someone who never looked at it
// cannot answer "copy" by accident: its default answer is "as text",
// and typing means "as text". A copy takes a click on a folder or on
// [ Copy ], or the keyboard's deliberate path to that button. See
// docs/adr/0003-a-pasted-path-is-a-drop-only-once-confirmed.md.
//
// The bar is an overlay (it owns the keyboard and the pointer, and it
// wants motion reports for its hover), adapted over App like the action
// menu because its state — the tree's lit row, the pending sources —
// is App state.

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/asyncjob"
	"github.com/johnlam90/skiff/internal/droppath"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/textdraw"
)

const (
	// placeBarRows is the bar's height: the sentence saying what goes
	// where, and the buttons under it.
	placeBarRows = 2

	// dropSpringDelay is how long the pointer rests on a collapsed
	// folder before it springs open. Long enough that sweeping across
	// the tree opens nothing, short enough to feel like a response.
	dropSpringDelay = 700 * time.Millisecond

	// dropEdgeTick is the pace of the scroll while the pointer is held
	// against the top or bottom of the tree: about eight rows a second.
	dropEdgeTick = 120 * time.Millisecond
)

// dropMode says which route, if any, has a drop pending.
type dropMode uint8

const (
	// dropIdle: nothing is being dropped.
	dropIdle dropMode = iota
	// dropPlacing: a pasted path is waiting on the placement bar.
	dropPlacing
	// dropHovering: a protocol drag is over the window (dnd.go).
	dropHovering
)

// fileDrop is the pending drop. The zero value is "none".
type fileDrop struct {
	mode dropMode

	// srcs are the paths a pasted drop names; text is the paste itself,
	// kept so "As text" can insert exactly what arrived.
	srcs []string
	text string
	// foreign marks a pasted drop whose paths are on another machine:
	// there is nothing to copy, only something to explain.
	foreign bool

	// base is where the drop lands when the pointer is not on a tree
	// row — the active folder when the drop began. target is the
	// folder currently lit in the tree; the two differ once the user
	// hovers or steps.
	base   string
	target string

	// px, py is the pointer's last cell and hoverNode the tree row
	// under it, for the spring-load and the edge scroll.
	px, py    int
	hoverNode *filetree.Node
	// seq retires timers: it moves whenever the hover changes or the
	// drop ends, and survives clearDrop so a timer armed for one drop
	// can never match the next.
	seq int
	// edgeDir is the scroll the pointer is currently asking for at the
	// tree's edge (-1, 0, +1) and edgeSeq its timer generation.
	edgeDir int
	edgeSeq int

	// press is the bar's click latch and hoverBtn the button under the
	// pointer, -1 for none. focus is the button Enter presses: it
	// starts on "As text" and moves only when the user moves it.
	press    overlay.Press
	hoverBtn int
	focus    int
	// scrolling is true while a press that began on the tree's
	// scrollbar is still held: the drag stays the scrollbar's.
	scrolling bool
}

// dropPending reports whether a drop is waiting on the user or hovering
// over the window.
func (a *App) dropPending() bool { return a.drop.mode != dropIdle }

// clearDrop ends whatever drop is pending: the tree's lit row goes dark
// and every armed timer is retired.
func (a *App) clearDrop() {
	a.drop = fileDrop{seq: a.drop.seq + 1, edgeSeq: a.drop.edgeSeq + 1, hoverBtn: -1}
	if a.tree != nil {
		a.tree.DropTarget = ""
	}
}

// setDropTarget lights dir in the tree as where the drop would land.
// The single write path, so the bar's sentence and the tree's row
// cannot name different folders.
func (a *App) setDropTarget(dir string) {
	a.drop.target = dir
	if a.tree != nil {
		a.tree.DropTarget = dir
	}
}

// dropBase is the folder a drop lands in when the user points at
// nothing: the active folder, or the root when that has gone stale.
func (a *App) dropBase() string {
	folder := a.activeFolder
	if folder == "" || !a.files.Contains(folder) {
		return a.rootDir
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return a.rootDir
	}
	return folder
}

// treeListHeight is the row count of the tree's scrollable list — the
// number Reveal, ShowDir and EdgeScroll all measure against.
func (a *App) treeListHeight() int {
	_, _, _, sh := a.sidebarRect()
	return max(sh-2, 0)
}

// overTree reports whether (x, y) is on the explorer: the sidebar is
// showing, it is showing the tree, and the cell is inside it.
func (a *App) overTree(x, y int) bool {
	if a.tree == nil || !a.sidebarShown || a.gitPanel.active {
		return false
	}
	sx, sy, sw, sh := a.sidebarRect()
	return sw > 0 && x >= sx && x < sx+sw && y >= sy && y < sy+sh
}

// dropFolderAt resolves a cell to the folder a drop there means, and
// the tree node under it. On the tree a folder row is that folder and a
// file row the folder it lives in; anywhere else the answer is the
// drop's base folder and there is no node.
func (a *App) dropFolderAt(x, y int) (string, *filetree.Node) {
	if !a.overTree(x, y) {
		return a.drop.base, nil
	}
	sx, sy, _, _ := a.sidebarRect()
	n, _ := a.tree.HitTest(x-sx, y-sy)
	return a.tree.DirAt(x-sx, y-sy), n
}

// sshSession reports whether the editor was started over SSH — the one
// place a pasted path can belong to a different machine.
func sshSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != ""
}

// lstatPath is the existence probe classification uses. Lstat, not
// Stat: a dangling symlink is still an entry that can be copied.
func lstatPath(p string) error {
	_, err := os.Lstat(p)
	return err
}

// classifyPaste asks droppath whether text is a file drop.
func classifyPaste(text string) (droppath.Kind, []string) {
	return droppath.Classify(text, droppath.Options{
		Stat:    lstatPath,
		Windows: runtime.GOOS == "windows",
		Remote:  sshSession(),
	})
}

// tryDropPaste reads an editor-bound paste as a possible file drop and
// reports whether it took it. A paste that is nothing but existing
// paths raises the placement bar; so does one whose paths are plainly
// on another machine, to say why nothing can be copied. Single-file
// mode has no explorer to drop on, so there every paste is text.
func (a *App) tryDropPaste(text string) bool {
	if a.tree == nil {
		return false
	}
	switch kind, paths := classifyPaste(text); kind {
	case droppath.Paths:
		a.openPlaceBar(paths, text, false)
		return true
	case droppath.Foreign:
		a.openPlaceBar(paths, text, true)
		return true
	}
	return false
}

// joinDropPaste handles a paste that arrives while the bar is already
// up — a second file dropped before the first was placed. Its paths
// join the pending drop. Anything else is refused out loud: the bar
// has no text field, and a paste swallowed in silence looks like a
// broken clipboard.
func (a *App) joinDropPaste(text string) {
	kind, paths := classifyPaste(text)
	if kind != droppath.Paths {
		a.flash("Paste ignored — place or cancel the drop first")
		return
	}
	if a.drop.foreign {
		// The first drop had nothing to copy; this one does.
		a.drop.foreign, a.drop.srcs, a.drop.text = false, nil, ""
		a.setDropTarget(a.drop.base)
	}
	have := make(map[string]bool, len(a.drop.srcs))
	for _, p := range a.drop.srcs {
		have[p] = true
	}
	for _, p := range paths {
		if !have[p] && len(a.drop.srcs) < droppath.MaxPaths {
			have[p] = true
			a.drop.srcs = append(a.drop.srcs, p)
		}
	}
	if a.drop.text != "" {
		a.drop.text += " "
	}
	a.drop.text += text
}

// -----------------------------------------------------------------------------
// The placement bar
// -----------------------------------------------------------------------------

// placeBar adapts the placement bar onto the overlay stack. A one-word
// value like menuOverlay: its state is App's, so two of them compare
// equal, which is what the dispatcher's press-ownership check needs.
type placeBar struct{ a *App }

// HandleKey routes a key to the bar's keyboard.
func (o placeBar) HandleKey(ev *tcell.EventKey) { o.a.handlePlaceBarKey(ev) }

// HandleMouse routes a pointer event to the bar's hit-testing.
func (o placeBar) HandleMouse(x, y int, btn tcell.ButtonMask) { o.a.handlePlaceBarMouse(x, y, btn) }

// Draw paints the bar over the frame the base UI just drew.
func (o placeBar) Draw(tcell.Screen) { o.a.drawPlaceBar() }

// WantsMotion is true: the bar lights the folder under the pointer, and
// the first thing it has to learn — where the user just let go — only
// arrives as a button-less motion report.
func (o placeBar) WantsMotion() bool { return true }

// Dismiss runs when the stack tears the bar down for another overlay:
// the pending drop goes with it, so no later surface inherits a lit
// tree row.
func (o placeBar) Dismiss() { o.a.clearDrop() }

// placeBarUp reports whether the placement bar is the overlay on top.
func (a *App) placeBarUp() bool {
	_, ok := a.overlays.Top().(placeBar)
	return ok
}

// openPlaceBar raises the bar for a pasted drop. A drop is an explorer
// gesture, so a sidebar showing the Git panel is turned back to the
// tree — there are no folder rows to click otherwise. A hidden sidebar
// is left hidden: the bar still works from the keyboard.
func (a *App) openPlaceBar(srcs []string, text string, foreign bool) {
	a.closeAllModals()
	if a.sidebarShown && a.gitPanel.active {
		a.showExplorerPanel()
	}
	a.clearDrop()
	a.drop.mode = dropPlacing
	a.drop.srcs, a.drop.text, a.drop.foreign = srcs, text, foreign
	a.drop.base = a.dropBase()
	a.drop.press.Sync(a.mouse.held)
	// The default answer is the harmless one: a path typed into a
	// buffer is one undo away, a folder copied into a repository is
	// not. The same reasoning starts the disk-conflict prompt on
	// "Keep mine".
	a.drop.focus = placeBarText
	if !foreign {
		a.setDropTarget(a.drop.base)
	}
	a.overlays.Open(placeBar{a})
}

// closePlaceBar takes the bar down and ends the drop.
func (a *App) closePlaceBar() {
	if a.placeBarUp() {
		a.overlays.Close()
	}
	a.clearDrop()
}

// placeBarButton is one clickable answer on the bar.
type placeBarButton struct {
	label string
	act   func(*App)
}

// The bar's buttons, left to right, in the order every prompt in the
// editor uses — the way out first, the action last (compare the
// dirty-close prompt's Cancel / Discard / Save). The focus starts in
// the middle, so one Tab reaches [ Copy ].
const (
	placeBarCancel = iota
	placeBarText
	placeBarCopy
)

// placeBarButtons lists the bar's answers. A foreign drop has no copy
// to offer, so its list simply ends one short.
func (a *App) placeBarButtons() []placeBarButton {
	buttons := []placeBarButton{
		placeBarCancel: {"[ Cancel ]", (*App).closePlaceBar},
		placeBarText:   {"[ As text ]", (*App).dropAsText},
		placeBarCopy:   {"[ Copy ]", (*App).dropCommitTarget},
	}
	if a.drop.foreign {
		return buttons[:placeBarCopy]
	}
	return buttons
}

// dropCommitTarget copies into the folder the tree is lighting.
func (a *App) dropCommitTarget() { a.commitDrop(a.drop.target) }

// commitDrop copies the pending sources into dir. Capture, then close,
// then act: the copy's own flash must not be torn down with the bar.
func (a *App) commitDrop(dir string) {
	srcs := a.drop.srcs
	a.closePlaceBar()
	a.startImport(srcs, dir)
}

// dropAsText answers "this was text": the paste goes into the buffer
// exactly as it would have with no bar in the way.
func (a *App) dropAsText() {
	text := a.drop.text
	a.closePlaceBar()
	a.insertPastedText(text)
}

// placeBarRect is the bar's place: the two rows above the status bar,
// across the whole window. Not just the editor's width — with the
// sidebar re-shown in a narrow terminal the editor is 22 cells, and
// the three buttons need 33 (the same arithmetic that sets minWidth
// for the dirty-close prompt). The bar therefore lies over the
// sidebar's footer and one tree row, and sidebarRect gives that row up
// while the bar is shown so it never hides a folder the user could
// otherwise click.
func (a *App) placeBarRect() rect {
	return rect{x: 0, y: a.height - 1 - placeBarRows, w: a.width, h: placeBarRows}
}

// placeBarButtonAt returns the index of the button at (x, y), or -1.
func (a *App) placeBarButtonAt(x, y int) int {
	r := a.placeBarRect()
	if y != r.y+1 {
		return -1
	}
	bx := r.x + 1
	for i, b := range a.placeBarButtons() {
		w := textdraw.Width(b.label)
		if x >= bx && x < bx+w {
			return i
		}
		bx += w + 1
	}
	return -1
}

// handlePlaceBarKey is the bar's keyboard. The rule behind every branch
// is that the bar may be up in front of someone who pasted text and
// never looked down, so nothing they would do next can mean "copy":
//
//   - Enter presses the focused button, and the focus starts on
//     "As text";
//   - a typed character answers "this was text" and is then typed, so
//     nobody's sentence loses its middle;
//   - Tab moves the focus, and the arrows choose a folder — which is an
//     unmistakable "I am placing a drop", so they also put the focus on
//     [ Copy ]. That is the keyboard's way to a copy, and it cannot be
//     walked by reflex.
//
// Esc cancels. Keys that arrive inside a paste are content, never
// answers — handleKey already buffers those, and this guard is the
// second lock on the same door.
func (a *App) handlePlaceBarKey(ev *tcell.EventKey) {
	if a.pasting {
		return
	}
	buttons := a.placeBarButtons()
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closePlaceBar()
	case tcell.KeyEnter:
		buttons[a.drop.focus].act(a)
	case tcell.KeyTab:
		a.drop.focus = (a.drop.focus + 1) % len(buttons)
	case tcell.KeyBacktab:
		a.drop.focus = (a.drop.focus + len(buttons) - 1) % len(buttons)
	case tcell.KeyUp:
		a.stepDropTarget(-1)
	case tcell.KeyDown:
		a.stepDropTarget(1)
	case tcell.KeyRight:
		a.expandDropTarget(true)
	case tcell.KeyLeft:
		a.expandDropTarget(false)
	case tcell.KeyRune:
		// An Alt rune is a mangled Esc chord, not typing (see
		// handleKey); it is neither text nor an answer.
		if ev.Modifiers()&tcell.ModAlt != 0 {
			return
		}
		a.dropAsText()
		a.handleKey(ev)
	}
}

// stepDropTarget moves the target one folder row up or down and keeps
// it on screen.
func (a *App) stepDropTarget(delta int) {
	if a.drop.foreign || a.tree == nil {
		return
	}
	a.setDropTarget(a.tree.StepDir(a.drop.target, delta))
	a.tree.ShowDir(a.drop.target, a.treeListHeight())
	a.drop.focus = placeBarCopy // choosing a folder is choosing to copy
}

// expandDropTarget is Right and Left on the target: open it, or close
// it — and Left on a folder that is already closed steps out to its
// parent, the way a tree's keyboard usually works.
func (a *App) expandDropTarget(open bool) {
	if a.drop.foreign || a.tree == nil {
		return
	}
	a.drop.focus = placeBarCopy // as in stepDropTarget
	n := a.tree.NodeFor(a.drop.target)
	if n == nil || n == a.tree.Root {
		return
	}
	switch {
	case open && !n.Expanded, !open && n.Expanded:
		a.tree.Toggle(n)
	case !open:
		if parent := filepath.Dir(n.Path); a.files.Contains(parent) {
			a.setDropTarget(parent)
			a.tree.ShowDir(parent, a.treeListHeight())
		}
	}
}

// handlePlaceBarMouse is the bar's pointer. The wheel scrolls the tree
// and so does its scrollbar; a button answers a fresh press; a tree row
// is lit while the pointer is over it and takes the drop when clicked.
// Everything else — the editor, the tab bar — is inert while the bar is
// up: a stray click neither confirms nor cancels.
func (a *App) handlePlaceBarMouse(x, y int, btn tcell.ButtonMask) {
	fresh := a.drop.press.Fresh(btn)
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if a.overTree(x, y) {
			delta := wheelLines
			if btn&tcell.WheelUp != 0 {
				delta = -wheelLines
			}
			a.tree.Scroll(delta)
		}
		return
	}
	// The scrollbar column is not a row. A press there scrolls, and the
	// drag it starts stays the scrollbar's until the button comes up,
	// wherever the pointer wanders — the same contract the bar has
	// outside a drop. Read as a row it would drop the file into
	// whichever folder happened to be at that height.
	if btn&tcell.Button1 == 0 {
		a.drop.scrolling = false
	} else if a.drop.scrolling || (fresh && a.treeScrollbarHit(x, y)) {
		a.drop.scrolling = true
		a.treeScrollbarTo(y)
		return
	}
	a.drop.hoverBtn = a.placeBarButtonAt(x, y)
	if a.drop.hoverBtn >= 0 {
		a.dropHoverAt(nil, x, y)
		if fresh {
			a.placeBarButtons()[a.drop.hoverBtn].act(a)
		}
		return
	}
	if a.drop.foreign || !a.overTree(x, y) || a.treeScrollbarHit(x, y) {
		a.dropHoverAt(nil, x, y)
		return
	}
	dir, node := a.dropFolderAt(x, y)
	a.setDropTarget(dir)
	a.dropHoverAt(node, x, y)
	if fresh {
		a.commitDrop(dir)
	}
}

// drawPlaceBar paints the bar: what will be copied where, then the
// answers. Every cell of its rect is filled first, like any surface.
func (a *App) drawPlaceBar() {
	r := a.placeBarRect()
	if r.w <= 0 || r.y < 0 {
		return
	}
	bg := a.theme.LineHL
	base := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	textdraw.Fill(a.screen, r.x, r.y, r.w, r.h, base)

	room := r.w - 2
	if a.drop.foreign {
		msg := dropSubject(a.drop.srcs) + " isn't on this machine — this terminal pasted your computer's path"
		textdraw.DrawClipped(a.screen, r.x+1, r.y, room, textdraw.ClipEllipsis(msg, room), base.Foreground(a.theme.Error).Bold(true))
	} else {
		lead, folder := a.placeBarSentence(room)
		next := textdraw.DrawClipped(a.screen, r.x+1, r.y, room, lead, base)
		textdraw.DrawClipped(a.screen, next, r.y, r.x+1+room-next, folder, base.Foreground(a.theme.Accent).Bold(true))
	}

	x := r.x + 1
	for i, b := range a.placeBarButtons() {
		// The inverted button is the one Enter presses — unless the
		// pointer is on another, which a click would press.
		focused := a.drop.hoverBtn == i || (a.drop.hoverBtn < 0 && i == a.drop.focus)
		fg := a.theme.Text
		if i == placeBarCopy {
			fg = a.theme.Accent
		}
		overlay.DrawButton(a.screen, x, r.y+1, b.label, bg, fg, focused)
		x += textdraw.Width(b.label) + 1
	}
	if hint := a.placeBarHint(); hint != "" && x+2+textdraw.Width(hint) <= r.x+r.w {
		textdraw.DrawClipped(a.screen, x+2, r.y+1, textdraw.Width(hint), hint, base.Foreground(a.theme.Muted))
	}
}

// placeBarHint is the one line of teaching that fits beside the
// buttons. It names the mouse gesture only when there are rows to
// click; with the sidebar hidden the arrows are the way to choose.
func (a *App) placeBarHint() string {
	switch {
	case a.drop.foreign:
		return ""
	case a.sidebarShown && !a.gitPanel.active:
		return "click a folder, or Tab then Enter"
	default:
		return "↑↓ pick a folder, then Enter"
	}
}

// placeBarSentence returns the bar's first line in two parts — the
// lead and the folder, which is drawn accented — fitted to maxW cells.
// The folder is the answer the user is checking, so the subject gives
// way first; only when that is not enough is the folder shortened from
// the left, keeping its last component.
func (a *App) placeBarSentence(maxW int) (lead, folder string) {
	subject := dropSubject(a.drop.srcs)
	folder = a.relativeFolderLabel(a.drop.target)
	const fixed = len("Copy  into ")
	if over := fixed + textdraw.Width(subject) + textdraw.Width(folder) - maxW; over > 0 {
		subject = textdraw.ClipEllipsis(subject, max(textdraw.Width(subject)-over, 8))
	}
	if room := maxW - fixed - textdraw.Width(subject); textdraw.Width(folder) > room {
		if short, ok := leftEllipsisPath(folder, room); ok {
			folder = short
		} else {
			folder = textdraw.ClipEllipsis(folder, max(room, 0))
		}
	}
	return "Copy " + subject + " into ", folder
}

// dropSubject names what is being dropped: the file's own name, or a
// count when there are several.
func dropSubject(srcs []string) string {
	if len(srcs) == 1 {
		return filepath.Base(srcs[0])
	}
	return fmt.Sprintf("%d items", len(srcs))
}

// -----------------------------------------------------------------------------
// Hover: spring-loaded folders and the edge scroll
// -----------------------------------------------------------------------------

// afterOnLoop runs fn on the event loop after d. The timer's goroutine
// only posts; the mutation happens on the loop, under the crash guard —
// the same shape as the tmux mouse probe.
func (a *App) afterOnLoop(name string, d time.Duration, fn func()) {
	scr := a.screen
	time.AfterFunc(d, func() {
		a.runGuarded(name, func() {
			_ = scr.PostEvent(asyncjob.Notify(fn))
		})
	})
}

// dropHoverAt records where the pointer is during a drop: node is the
// tree row under it, nil when it is not on one. A change of row retires
// the previous row's spring timer and, when the new row is a collapsed
// folder, arms one for it. Both routes call this — the placement bar on
// mouse motion, the protocol route on a hover message.
func (a *App) dropHoverAt(node *filetree.Node, x, y int) {
	a.drop.px, a.drop.py = x, y
	if node != a.drop.hoverNode {
		a.drop.hoverNode = node
		a.drop.seq++
		if node != nil && node.IsDir && !node.Expanded && !node.Loop && node != a.tree.Root {
			seq := a.drop.seq
			a.afterOnLoop("drop-spring", dropSpringDelay, func() { a.fireDropSpring(seq, node) })
		}
	}
	a.armDropEdge()
}

// fireDropSpring is the spring timer's on-loop half: open node if the
// pointer is still resting on it. A timer armed for a row the pointer
// has left, or for a drop that has ended, finds seq moved on.
func (a *App) fireDropSpring(seq int, node *filetree.Node) {
	if seq != a.drop.seq || !a.dropPending() || a.drop.hoverNode != node {
		return
	}
	if !node.Expanded {
		a.tree.Toggle(node)
	}
}

// armDropEdge starts or stops the edge scroll for the pointer's current
// row. During a real drag there is no wheel, so resting against the top
// or bottom of the tree is the only way to reach a folder that is
// scrolled out of view.
func (a *App) armDropEdge() {
	dir := 0
	if a.overTree(a.drop.px, a.drop.py) {
		_, sy, _, _ := a.sidebarRect()
		dir = a.tree.EdgeScroll(a.drop.py-sy, a.treeListHeight())
	}
	if dir == a.drop.edgeDir {
		return
	}
	a.drop.edgeDir = dir
	a.drop.edgeSeq++
	if dir != 0 {
		seq := a.drop.edgeSeq
		a.afterOnLoop("drop-edge", dropEdgeTick, func() { a.fireDropEdge(seq) })
	}
}

// fireDropEdge is one tick of the edge scroll. Each tick first re-reads
// the row under the still pointer — the previous tick moved the list
// and a frame has been drawn since, so a different folder is there now
// — then scrolls again if the edge still has rows beyond it. The tick
// after the last scroll is what leaves the highlight on the right row.
func (a *App) fireDropEdge(seq int) {
	if seq != a.drop.edgeSeq || !a.dropPending() {
		return
	}
	dir, node := a.dropFolderAt(a.drop.px, a.drop.py)
	a.setDropTarget(dir)
	if node != a.drop.hoverNode {
		a.drop.hoverNode = node
		a.drop.seq++
	}
	step := 0
	if a.overTree(a.drop.px, a.drop.py) {
		_, sy, _, _ := a.sidebarRect()
		step = a.tree.EdgeScroll(a.drop.py-sy, a.treeListHeight())
	}
	scrolled := a.drop.edgeDir != 0
	a.drop.edgeDir = step
	if step != 0 {
		a.tree.Scroll(step)
	}
	if step != 0 || scrolled {
		a.afterOnLoop("drop-edge", dropEdgeTick, func() { a.fireDropEdge(seq) })
	}
}

// -----------------------------------------------------------------------------
// The copy
// -----------------------------------------------------------------------------

// startImport copies srcs into dst on the file-op job and reports
// whether it started. The job is Refuse, so a drop that arrives during
// another copy is turned away with the same flash a second paste gets.
func (a *App) startImport(srcs []string, dst string) bool {
	if len(srcs) == 0 {
		return false
	}
	files := a.files
	progress := a.fileOpProgress("Copy")
	started := a.fileOp.Start(func(context.Context) (fileOpResult, error) {
		cs, err := files.Import(srcs, dst, false, progress)
		return fileOpResult{label: "Copy", imported: true, want: len(srcs), dst: dst, cs: cs}, err
	})
	if !started {
		a.flash("Another file operation is still running")
		return false
	}
	a.flash("Copying " + dropSubject(srcs) + "…")
	return true
}

// handleImportDone lands a finished import. The Changeset is applied
// whatever the error says — Import keeps going past a failed source, so
// "error" and "some files landed" are both true at once — then the
// folder that received them becomes the active one and opens to show
// the first arrival.
func (a *App) handleImportDone(r fileOpResult, err error) {
	// The file-op job runs one thing at a time, so an import landing
	// means no protocol transfer is in flight any more.
	a.dndFetching = false
	a.applyChangeset(r.cs)
	n := len(r.cs.Added)
	if n > 0 {
		a.setActiveFolder(r.dst)
		a.tree.Reveal(r.cs.Added[0], a.treeListHeight())
	}
	folder := a.relativeFolderLabel(r.dst)
	switch {
	case err == nil && n == 1:
		a.flash(fmt.Sprintf("Copied %s into %s", filepath.Base(r.cs.Added[0]), folder))
	case err == nil:
		a.flash(fmt.Sprintf("Copied %d items into %s", n, folder))
	case n == 0:
		a.flashError("Copy failed: " + oneLine(err))
	default:
		a.flashError(fmt.Sprintf("Copied %d of %d into %s — %s", n, r.want, folder, oneLine(err)))
	}
}

// oneLine flattens an error for a flash. A batch joins its failures
// with newlines, and the status bar has one row.
func oneLine(err error) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(err.Error(), "\n", "; ")), " ")
}

// -----------------------------------------------------------------------------
// The ≡ row
// -----------------------------------------------------------------------------

// menuImportPath is ≡ → Import path…: a prompt for a path to copy into
// the active folder. It is the menu path the project
// rule asks every file action to have, and the one that works when a
// drop cannot — no mouse, or a terminal that hands over nothing.
func (a *App) menuImportPath() {
	a.closeMenu()
	if a.tree == nil {
		return
	}
	folder := a.dropBase()
	a.openPrompt("Import path", "into "+a.relativeFolderLabel(folder), "", func(app *App, value string) {
		app.importTypedPath(folder, value)
	})
}

// importPathLabel names the row after its target folder exactly the
// way the New-file row does: bare at the project root, "(in src/)"
// below it. Deliberately not "Import path into …": the menu filter
// ranks a word that starts with the query, and "into" would put this
// row ahead of "Find in file" for anyone who types "in".
func (a *App) importPathLabel() string {
	return "Import path" + a.activeFolderSuffix() + "…"
}

// importTypedPath copies the path the prompt returned into folder. The
// value goes through the same classification a paste does, so a path
// dropped INTO the prompt — escaped, quoted or as a file:// URI — reads
// the same as one typed by hand. A leading ~/ is expanded: in a prompt
// that asks for a path it can only mean home.
func (a *App) importTypedPath(folder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if rest, ok := strings.CutPrefix(value, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, rest)
		}
	}
	kind, paths := classifyPaste(value)
	if kind != droppath.Paths {
		a.flashError("No such file or folder: " + value)
		return
	}
	a.startImport(paths, folder)
}
