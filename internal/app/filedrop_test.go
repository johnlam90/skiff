// =============================================================================
// File: internal/app/filedrop_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// shellEscape spells a path the way a terminal pastes a dropped file:
// every byte a shell would interpret gets a backslash.
func shellEscape(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(" ()'\"\\&;$`!*?[]{}<>|#~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mouseAt delivers one mouse event.
func mouseAt(a *App, x, y int, btn tcell.ButtonMask) {
	a.handleEvent(tcell.NewEventMouse(x, y, btn, tcell.ModNone))
}

// clickCell presses and releases Button1 on one cell.
func clickCell(a *App, x, y int) {
	mouseAt(a, x, y, tcell.Button1)
	mouseAt(a, x, y, tcell.ButtonNone)
}

// barUp reports whether the placement bar is the overlay on top.
func barUp(a *App) bool {
	_, ok := a.overlays.Top().(placeBar)
	return ok
}

// findOnScreen returns the cell where s starts on the last drawn frame.
func findOnScreen(t *testing.T, a *App, s string) (int, int) {
	t.Helper()
	cells, w, h := a.screen.(tcell.SimulationScreen).GetContents()
	want := []rune(s)
	for y := 0; y < h; y++ {
	next:
		for x := 0; x+len(want) <= w; x++ {
			for i, r := range want {
				c := cells[y*w+x+i]
				if len(c.Runes) == 0 || c.Runes[0] != r {
					continue next
				}
			}
			return x, y
		}
	}
	t.Fatalf("%q is not on screen", s)
	return -1, -1
}

// show draws a frame and flushes it so screen assertions see it.
func show(a *App) {
	a.draw()
	a.screen.Show()
}

// dropFixture is a project with two folders and an open file, plus a
// file outside it to drop. SSH is switched off so a missing path is
// judged the same way wherever the suite runs.
type dropFixture struct {
	a       *App
	root    string
	src     string // root/src
	docs    string // root/docs
	open    string // root/main.go, open in a tab
	outside string // a file outside the project
}

// newDropFixture builds the fixture.
func newDropFixture(t *testing.T) dropFixture {
	t.Helper()
	for _, k := range []string{"SSH_CONNECTION", "SSH_TTY", "SSH_CLIENT"} {
		t.Setenv(k, "")
	}
	outsideDir := t.TempDir()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "src"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	must(os.WriteFile(filepath.Join(root, "src", "keep.go"), []byte("package src\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "docs", "readme.md"), []byte("# docs\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "main.go"), []byte(""), 0o644))
	outside := filepath.Join(outsideDir, "my photo.png")
	must(os.WriteFile(outside, []byte("pixels"), 0o644))

	a := newTestApp(t, root)
	f := dropFixture{a: a, root: a.rootDir, outside: outside}
	f.src, f.docs, f.open = filepath.Join(f.root, "src"), filepath.Join(f.root, "docs"), filepath.Join(f.root, "main.go")
	a.openFile(f.open)
	a.setActiveFolder(f.root)
	show(a)
	return f
}

// buffer returns the open tab's first line — empty unless something
// typed into it.
func (f dropFixture) buffer() string {
	return f.a.activeTabPtr().Buffer.Lines[0]
}

// landed waits for the copy to finish and returns the content now at
// dir/name, or "" when nothing is there.
func (f dropFixture) landed(t *testing.T, dir, name string) string {
	t.Helper()
	pumpUntil(t, f.a, "the copy", idle(&f.a.fileOp))
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

// TestFileDrop_PastedPathOpensTheBar is the feature's first step: a
// paste that is nothing but an existing path is a dropped file, so it
// raises the placement bar aimed at the active folder — and types
// nothing into the open buffer, which is what it used to do.
func TestFileDrop_PastedPathOpensTheBar(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))

	if !barUp(f.a) {
		t.Fatal("the placement bar did not open")
	}
	if got := f.buffer(); got != "" {
		t.Errorf("the path was typed into the buffer: %q", got)
	}
	if f.a.tree.DropTarget != f.root {
		t.Errorf("DropTarget = %q, want the active folder %q", f.a.tree.DropTarget, f.root)
	}
	show(f.a)
	if !screenHasText(t, f.a, "Copy my photo.png into") {
		t.Error("the bar does not say what will be copied")
	}
	if exists := f.landed(t, f.root, "my photo.png"); exists != "" {
		t.Error("the file was copied before the user confirmed")
	}
}

// copyByKeyboard answers the bar "copy" from the keyboard: Tab moves
// the focus onto [ Copy ], Enter presses it.
func copyByKeyboard(a *App) {
	a.handleEvent(keyEv(tcell.KeyTab, 0))
	a.handleEvent(keyEv(tcell.KeyEnter, 0))
}

// TestFileDrop_TabThenEnterCopiesIntoTheHighlightedFolder pins the
// keyboard confirm: with the focus moved onto [ Copy ], Enter copies
// into the folder the tree is highlighting, the file is on disk with
// its bytes, the tree has it, and the flash says where it went.
func TestFileDrop_TabThenEnterCopiesIntoTheHighlightedFolder(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	copyByKeyboard(f.a)

	if got := f.landed(t, f.root, "my photo.png"); got != "pixels" {
		t.Fatalf("copied content = %q, want %q", got, "pixels")
	}
	if barUp(f.a) || f.a.tree.DropTarget != "" {
		t.Error("the bar or its highlight outlived the copy")
	}
	if f.a.tree.NodeFor(filepath.Join(f.root, "my photo.png")) == nil {
		t.Error("the copied file is not in the tree")
	}
	if !strings.Contains(f.a.statusMsg, "Copied my photo.png into") {
		t.Errorf("flash = %q, want it to report the copy", f.a.statusMsg)
	}
	if _, err := os.Stat(f.outside); err != nil {
		t.Error("the dropped file's original was removed")
	}
}

// TestFileDrop_AReflexEnterIsNotACopy is the anti-goal at the keyboard.
// Someone who pasted a path into their file and pressed Enter for a new
// line never saw the bar; if Enter meant "copy", that reflex would put
// the file — or a whole folder — into the repository. The bar's default
// answer is therefore the harmless one: the paste lands as the text it
// may well have been, and nothing is copied.
func TestFileDrop_AReflexEnterIsNotACopy(t *testing.T) {
	f := newDropFixture(t)
	text := shellEscape(f.outside)
	pasteInto(f.a, text)
	f.a.handleEvent(keyEv(tcell.KeyEnter, 0))

	if got := f.buffer(); got != text {
		t.Errorf("buffer = %q, want the pasted text %q", got, text)
	}
	if barUp(f.a) {
		t.Error("the bar stayed up")
	}
	if got := f.landed(t, f.root, "my photo.png"); got != "" {
		t.Error("a reflex Enter copied the file")
	}
}

// TestFileDrop_TypingAfterThePasteWins is the same anti-goal for
// someone who simply keeps typing: the keys are theirs. The first one
// answers "this was text" and is then typed like any other — none of
// them is swallowed, and none of them can mean "copy".
func TestFileDrop_TypingAfterThePasteWins(t *testing.T) {
	f := newDropFixture(t)
	text := shellEscape(f.outside)
	pasteInto(f.a, text)
	for _, r := range " is where last week went" {
		f.a.handleEvent(keyEv(tcell.KeyRune, r))
	}

	if got, want := f.buffer(), text+" is where last week went"; got != want {
		t.Errorf("buffer = %q\n   want %q", got, want)
	}
	if barUp(f.a) {
		t.Error("the bar stayed up")
	}
	if got := f.landed(t, f.root, "my photo.png"); got != "" {
		t.Error("typing copied the file")
	}
}

// TestFileDrop_ClickOnAFolderRowCopiesThere is the gesture the bar is
// built around: drop, then click the folder. The click is the pointer
// position a pasted path never carried.
func TestFileDrop_ClickOnAFolderRowCopiesThere(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	clickCell(f.a, 3, treeRowY(t, f.a, "src"))

	if got := f.landed(t, f.src, "my photo.png"); got != "pixels" {
		t.Fatalf("content in src/ = %q, want %q", got, "pixels")
	}
	if got := f.landed(t, f.root, "my photo.png"); got != "" {
		t.Error("the file also landed in the active folder")
	}
	if n := f.a.tree.NodeFor(f.src); n == nil || !n.Expanded {
		t.Error("the folder that received the file was not opened to show it")
	}
}

// TestFileDrop_AFileRowMeansItsFolder pins that the user does not have
// to aim: clicking a file inside docs/ drops beside it.
func TestFileDrop_AFileRowMeansItsFolder(t *testing.T) {
	f := newDropFixture(t)
	f.a.tree.Toggle(f.a.tree.NodeFor(f.docs))
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	clickCell(f.a, 5, treeRowY(t, f.a, "readme.md"))

	if got := f.landed(t, f.docs, "my photo.png"); got != "pixels" {
		t.Errorf("content in docs/ = %q, want %q", got, "pixels")
	}
}

// TestFileDrop_HoverMovesTheTarget pins the highlight following the
// pointer with no button held — which is why the bar asks the terminal
// for motion reports — without copying anything.
func TestFileDrop_HoverMovesTheTarget(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	mouseAt(f.a, 3, treeRowY(t, f.a, "docs"), tcell.ButtonNone)

	if f.a.tree.DropTarget != f.docs {
		t.Errorf("DropTarget = %q, want %q", f.a.tree.DropTarget, f.docs)
	}
	if !barUp(f.a) {
		t.Error("a hover confirmed the drop")
	}
	show(f.a)
	if !screenHasText(t, f.a, "into docs/") {
		t.Error("the bar does not name the hovered folder")
	}
}

// TestFileDrop_EscCancels pins the third answer: neither a copy nor
// typed text.
func TestFileDrop_EscCancels(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	f.a.handleEvent(keyEv(tcell.KeyEsc, 0))

	if barUp(f.a) || f.a.tree.DropTarget != "" {
		t.Error("Esc left the bar or its highlight up")
	}
	if got := f.buffer(); got != "" {
		t.Errorf("buffer = %q, want it untouched", got)
	}
	if got := f.landed(t, f.root, "my photo.png"); got != "" {
		t.Error("a cancelled drop copied the file")
	}
}

// TestFileDrop_ButtonsAnswerAClick pins the mouse path to all three
// answers — the editor is mouse-first, so the bar cannot be keys only.
func TestFileDrop_ButtonsAnswerAClick(t *testing.T) {
	t.Run("copy", func(t *testing.T) {
		f := newDropFixture(t)
		pasteInto(f.a, shellEscape(f.outside))
		show(f.a)
		x, y := findOnScreen(t, f.a, "[ Copy ]")
		clickCell(f.a, x+2, y)
		if got := f.landed(t, f.root, "my photo.png"); got != "pixels" {
			t.Errorf("content = %q, want %q", got, "pixels")
		}
	})
	t.Run("as text", func(t *testing.T) {
		f := newDropFixture(t)
		text := shellEscape(f.outside)
		pasteInto(f.a, text)
		show(f.a)
		x, y := findOnScreen(t, f.a, "[ As text ]")
		clickCell(f.a, x+2, y)
		if got := f.buffer(); got != text {
			t.Errorf("buffer = %q, want %q", got, text)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		f := newDropFixture(t)
		pasteInto(f.a, shellEscape(f.outside))
		show(f.a)
		x, y := findOnScreen(t, f.a, "[ Cancel ]")
		clickCell(f.a, x+2, y)
		if barUp(f.a) || f.buffer() != "" {
			t.Error("Cancel did not simply close the bar")
		}
	})
}

// TestFileDrop_AClickElsewhereIsNotAnAnswer pins that only the tree and
// the buttons act: a stray click in the editor neither confirms nor
// dismisses, and — because the bar owns the pointer — moves no caret.
func TestFileDrop_AClickElsewhereIsNotAnAnswer(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	ex, ey, _, _ := f.a.editorRect()
	clickCell(f.a, ex+10, ey+2)

	if !barUp(f.a) {
		t.Error("a click in the editor closed the bar")
	}
	if got := f.landed(t, f.root, "my photo.png"); got != "" {
		t.Error("a click in the editor confirmed the drop")
	}
}

// TestFileDrop_APasteForAnotherSurfaceIsStillText pins the boundary:
// only a paste headed for the editor is ever read as a drop. With the
// find bar up the path is a search query, exactly as before.
func TestFileDrop_APasteForAnotherSurfaceIsStillText(t *testing.T) {
	f := newDropFixture(t)
	f.a.openFind()
	pasteInto(f.a, f.root)

	if barUp(f.a) {
		t.Fatal("a paste into the find bar opened the placement bar")
	}
	fs, ok := f.a.strip.(*findStrip)
	if !ok {
		t.Fatal("the find bar closed")
	}
	if got := fs.query.Text(); got != f.root {
		t.Errorf("find query = %q, want the pasted path %q", got, f.root)
	}
}

// TestFileDrop_NotADropStaysAPaste pins what does not open the bar: a
// path that is not on this machine, and ordinary text.
func TestFileDrop_NotADropStaysAPaste(t *testing.T) {
	for _, text := range []string{"/no/such/place/file.txt", "plain words", "/api/v1/users"} {
		f := newDropFixture(t)
		pasteInto(f.a, text)
		if barUp(f.a) {
			t.Errorf("paste %q opened the placement bar", text)
		}
		if got := f.buffer(); got != text {
			t.Errorf("paste %q: buffer = %q, want it inserted", text, got)
		}
	}
}

// TestFileDrop_SingleFileModeHasNoExplorer pins that `skiff file.txt`
// — no tree, no project to copy into — keeps treating a pasted path as
// text.
func TestFileDrop_SingleFileModeHasNoExplorer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "one.txt")
	other := filepath.Join(dir, "other.txt")
	for _, p := range []string{file, other} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(100, 30)
	a := newSingleFileApp(scr, file)
	a.width, a.height = scr.Size()

	pasteInto(a, other)
	if barUp(a) {
		t.Fatal("single-file mode opened the placement bar")
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != other {
		t.Errorf("buffer = %q, want the pasted path", got)
	}
}

// TestFileDrop_ASecondDropJoinsTheFirst pins two things at once. A drop
// that arrives while the bar is still up adds its files to the pending
// one. And — the bug this guards — its characters are content, never
// keys: delivered one by one, the first of them would answer the bar
// "this was text" and type the rest into the buffer.
func TestFileDrop_ASecondDropJoinsTheFirst(t *testing.T) {
	f := newDropFixture(t)
	second := filepath.Join(filepath.Dir(f.outside), "test notes.txt")
	if err := os.WriteFile(second, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	pasteInto(f.a, shellEscape(f.outside))
	pasteInto(f.a, shellEscape(second))

	if !barUp(f.a) {
		t.Fatal("the second drop's characters closed the bar")
	}
	if got := f.buffer(); got != "" {
		t.Fatalf("the second drop was typed into the buffer: %q", got)
	}
	show(f.a)
	if !screenHasText(t, f.a, "Copy 2 items into") {
		t.Error("the bar does not show both files pending")
	}
	copyByKeyboard(f.a)
	if f.landed(t, f.root, "my photo.png") != "pixels" || f.landed(t, f.root, "test notes.txt") != "notes" {
		t.Error("both dropped files should have been copied")
	}
	if !strings.Contains(f.a.statusMsg, "Copied 2 items into") {
		t.Errorf("flash = %q, want it to count both", f.a.statusMsg)
	}
}

// TestFileDrop_TextPastedOverTheBarIsRefusedAloud pins that a paste the
// bar cannot use is not swallowed in silence.
func TestFileDrop_TextPastedOverTheBarIsRefusedAloud(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	pasteInto(f.a, "just some text")

	if !barUp(f.a) || f.buffer() != "" {
		t.Error("a text paste over the bar should leave the bar up and the buffer alone")
	}
	if f.a.statusMsg == "" {
		t.Error("the ignored paste was not reported")
	}
}

// TestFileDrop_TheTargetVanished pins the failure a slow hand can hit:
// the folder is deleted between the drop and the click. The editor
// says so and carries on.
func TestFileDrop_TheTargetVanished(t *testing.T) {
	f := newDropFixture(t)
	f.a.setActiveFolder(f.docs)
	pasteInto(f.a, shellEscape(f.outside))
	if err := os.RemoveAll(f.docs); err != nil {
		t.Fatal(err)
	}
	copyByKeyboard(f.a)
	pumpUntil(t, f.a, "the failed copy", idle(&f.a.fileOp))

	if !f.a.statusErr || !strings.Contains(f.a.statusMsg, "not a folder") {
		t.Errorf("flash = %q (error=%v), want an error naming the missing folder", f.a.statusMsg, f.a.statusErr)
	}
	if barUp(f.a) {
		t.Error("the bar is still up after the failed copy")
	}
}

// TestFileDrop_AFolderHoldingTheProject pins the refusal only a drop
// from outside can cause: the dropped folder contains the project, and
// copying it in would copy the project into its own growing copy.
func TestFileDrop_AFolderHoldingTheProject(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(filepath.Dir(f.root)))
	if !barUp(f.a) {
		t.Fatal("the bar did not open for a dropped folder")
	}
	copyByKeyboard(f.a)
	pumpUntil(t, f.a, "the refused copy", idle(&f.a.fileOp))

	if !f.a.statusErr || !strings.Contains(f.a.statusMsg, "into itself") {
		t.Errorf("flash = %q (error=%v), want the into-itself refusal", f.a.statusMsg, f.a.statusErr)
	}
	entries, _ := os.ReadDir(f.root)
	if len(entries) != 3 {
		t.Errorf("the project now holds %d entries, want its original 3", len(entries))
	}
}

// TestFileDrop_SpringLoadedFolders pins the expand-on-dwell: resting on
// a collapsed folder opens it, so a nested target is reachable without
// a click that would mean "drop here". A hover that has moved on must
// not open the folder it left.
func TestFileDrop_SpringLoadedFolders(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	src := f.a.tree.NodeFor(f.src)
	docs := f.a.tree.NodeFor(f.docs)

	mouseAt(f.a, 3, treeRowY(t, f.a, "src"), tcell.ButtonNone)
	stale := f.a.drop.seq
	mouseAt(f.a, 3, treeRowY(t, f.a, "docs"), tcell.ButtonNone)
	f.a.fireDropSpring(stale, src)
	if src.Expanded {
		t.Fatal("a folder the pointer had already left sprang open")
	}
	f.a.fireDropSpring(f.a.drop.seq, docs)
	if !docs.Expanded {
		t.Error("the folder under a resting pointer did not spring open")
	}
}

// TestFileDrop_ArrowKeysChooseTheFolder pins the keyboard path to a
// target: without it a session with no working mouse could only ever
// import into the active folder. Choosing a folder is an unmistakable
// "I am placing a drop", so the arrows also move the focus onto
// [ Copy ] and Enter then copies.
func TestFileDrop_ArrowKeysChooseTheFolder(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))

	f.a.handleEvent(keyEv(tcell.KeyDown, 0))
	if f.a.tree.DropTarget != f.docs {
		t.Fatalf("after Down: DropTarget = %q, want docs", f.a.tree.DropTarget)
	}
	f.a.handleEvent(keyEv(tcell.KeyDown, 0))
	if f.a.tree.DropTarget != f.src {
		t.Fatalf("after Down, Down: DropTarget = %q, want src", f.a.tree.DropTarget)
	}
	f.a.handleEvent(keyEv(tcell.KeyRight, 0))
	if !f.a.tree.NodeFor(f.src).Expanded {
		t.Error("Right did not expand the target")
	}
	f.a.handleEvent(keyEv(tcell.KeyLeft, 0))
	if f.a.tree.NodeFor(f.src).Expanded {
		t.Error("Left did not collapse the target")
	}
	f.a.handleEvent(keyEv(tcell.KeyUp, 0))
	if f.a.tree.DropTarget != f.docs {
		t.Fatalf("after Up: DropTarget = %q, want docs", f.a.tree.DropTarget)
	}
	f.a.handleEvent(keyEv(tcell.KeyEnter, 0))
	if got := f.landed(t, f.docs, "my photo.png"); got != "pixels" {
		t.Errorf("content in docs/ = %q, want %q", got, "pixels")
	}
}

// TestFileDrop_ForeignPathExplainsItself pins the Ghostty-over-SSH
// case: the terminal pasted a path from the user's own computer, the
// file is not here, and typing that path into the buffer would explain
// nothing. The bar says what happened and offers no copy.
func TestFileDrop_ForeignPathExplainsItself(t *testing.T) {
	f := newDropFixture(t)
	t.Setenv("SSH_CONNECTION", "10.0.0.2 50000 10.0.0.1 22")
	const foreign = "/Users/skiff-no-such-user/Downloads/photo.png"
	pasteInto(f.a, foreign)

	if !barUp(f.a) {
		t.Fatal("the bar did not open for a path from another machine")
	}
	show(f.a)
	if !screenHasText(t, f.a, "isn't on this machine") {
		t.Error("the bar does not say why the file cannot be copied")
	}
	if screenHasText(t, f.a, "[ Copy ]") {
		t.Error("the bar offers to copy a file it cannot read")
	}
	f.a.handleEvent(keyEv(tcell.KeyTab, 0))
	f.a.handleEvent(keyEv(tcell.KeyTab, 0)) // around the two buttons and back
	f.a.handleEvent(keyEv(tcell.KeyEnter, 0))
	if got := f.buffer(); got != foreign {
		t.Errorf("buffer = %q, want the pasted path — the default answer", got)
	}
	if got := f.landed(t, f.root, "photo.png"); got != "" {
		t.Error("a file that is not on this machine was somehow copied")
	}
}

// TestFileDrop_BarFitsAtTheMinimumSize pins the bar against the floor
// every surface is held to: at minWidth × minHeight all three buttons
// are on screen and none is clipped.
func TestFileDrop_BarFitsAtTheMinimumSize(t *testing.T) {
	f := newDropFixture(t)
	resizeTestApp(t, f.a, minWidth, minHeight)
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)

	for _, label := range []string{"[ Copy ]", "[ As text ]", "[ Cancel ]"} {
		if !screenHasText(t, f.a, label) {
			t.Errorf("%s is not fully on screen at %d×%d", label, minWidth, minHeight)
		}
	}
	x, y := findOnScreen(t, f.a, "[ Cancel ]")
	clickCell(f.a, x+2, y)
	if barUp(f.a) {
		t.Error("the Cancel button could not be clicked at the minimum size")
	}
}

// TestFileDrop_BarAsksForPointerMotion pins the mouse mode: the bar
// tints the folder under the pointer, so it needs motion reports while
// it is up — and must give them back the moment it closes.
func TestFileDrop_BarAsksForPointerMotion(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	if got := f.a.wantMouseFlags(); got != mouseHoverFlags {
		t.Errorf("with the bar up: mouse flags = %v, want the hover set", got)
	}
	f.a.handleEvent(keyEv(tcell.KeyEsc, 0))
	if got := f.a.wantMouseFlags(); got != mouseBaseFlags {
		t.Errorf("after the bar closed: mouse flags = %v, want the baseline", got)
	}
}

// TestFileDrop_WheelStillScrollsTheTree pins that the bar does not
// freeze the list the user is choosing from.
func TestFileDrop_WheelStillScrollsTheTree(t *testing.T) {
	f := newDropFixture(t)
	seedTreeFiles(t, f.root, 80)
	f.a.refreshTree()
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	before := f.a.tree.ScrollY
	mouseAt(f.a, 3, 6, tcell.WheelDown)
	if f.a.tree.ScrollY <= before {
		t.Errorf("ScrollY = %d after a wheel tick, want it past %d", f.a.tree.ScrollY, before)
	}
	if !barUp(f.a) {
		t.Error("the wheel closed the bar")
	}
}

// TestFileDrop_SwitchesTheSidebarToTheExplorer pins that a drop is an
// explorer gesture: with the Git panel showing there are no folder
// rows to click, so the sidebar turns back to the tree.
func TestFileDrop_SwitchesTheSidebarToTheExplorer(t *testing.T) {
	f := newDropFixture(t)
	f.a.gitPanel.active = true
	pasteInto(f.a, shellEscape(f.outside))
	if f.a.gitPanel.active {
		t.Error("the Git panel is still covering the tree")
	}
}

// TestFileDrop_AnotherOverlayTakesTheBarDown pins teardown through the
// stack: whatever replaces the bar must not inherit its highlight.
func TestFileDrop_AnotherOverlayTakesTheBarDown(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	f.a.closeAllModals()
	if f.a.tree.DropTarget != "" || f.a.dropPending() {
		t.Error("closing every modal left a drop pending")
	}
}

// TestFileDrop_MenuRowImportsAPath pins the route the project rule
// requires: every file action lives in the ≡ menu, so the copy is
// reachable with no drag at all.
func TestFileDrop_MenuRowImportsAPath(t *testing.T) {
	f := newDropFixture(t)
	f.a.setActiveFolder(f.src)
	items, _, _ := f.a.menuLayout()
	found := false
	for _, it := range items {
		if f.a.menuLabel(it) == "Import path (in src/)…" {
			found = true
		}
	}
	if !found {
		t.Fatal("the ≡ menu has no \"Import path (in src/)…\" row")
	}

	f.a.menuImportPath()
	for _, r := range f.outside {
		f.a.handleEvent(keyEv(tcell.KeyRune, r))
	}
	f.a.handleEvent(keyEv(tcell.KeyEnter, 0))
	if got := f.landed(t, f.src, "my photo.png"); got != "pixels" {
		t.Errorf("content in src/ = %q, want %q", got, "pixels")
	}
}

// TestFileDrop_MenuRowRejectsAMissingPath pins the prompt's failure: a
// path that is not there is an error flash, not a silent no-op.
func TestFileDrop_MenuRowRejectsAMissingPath(t *testing.T) {
	f := newDropFixture(t)
	f.a.menuImportPath()
	for _, r := range "/no/such/file.txt" {
		f.a.handleEvent(keyEv(tcell.KeyRune, r))
	}
	f.a.handleEvent(keyEv(tcell.KeyEnter, 0))
	if !f.a.statusErr {
		t.Errorf("flash = %q, want an error", f.a.statusMsg)
	}
}

// TestFileDrop_KeysThatAreNotAnswersChangeNothing pins the rest of the
// keyboard: keys that neither type nor answer — Backspace, a mangled
// Esc-leader chord — leave the bar exactly as it was.
func TestFileDrop_KeysThatAreNotAnswersChangeNothing(t *testing.T) {
	f := newDropFixture(t)
	pasteInto(f.a, shellEscape(f.outside))
	f.a.handleEvent(keyEv(tcell.KeyBackspace2, 0))
	f.a.handleEvent(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt))

	if !barUp(f.a) || f.buffer() != "" {
		t.Error("a key that is not an answer closed the bar or reached the buffer")
	}
}

// TestFileDrop_APressOnTheScrollbarScrolls pins that the tree's
// scrollbar is not a row: a press there moves the list — it is how a
// mouse reaches a folder that is off screen — and must not be read as
// "drop into whichever folder is at this height".
func TestFileDrop_APressOnTheScrollbarScrolls(t *testing.T) {
	f := newDropFixture(t)
	seedTreeFiles(t, f.root, 120)
	f.a.refreshTree()
	pasteInto(f.a, shellEscape(f.outside))
	show(f.a)
	sx, sy, sw, sh := f.a.sidebarRect()
	barX, barY := sx+sw-1, sy+sh-2
	if !f.a.treeScrollbarHit(barX, barY) {
		t.Fatalf("no scrollbar at (%d,%d); the fixture's tree is not tall enough", barX, barY)
	}
	before := f.a.tree.ScrollY
	clickCell(f.a, barX, barY)

	if !barUp(f.a) {
		t.Fatal("a press on the scrollbar answered the drop")
	}
	if f.a.fileOp.Busy() {
		t.Fatal("a press on the scrollbar started a copy")
	}
	if f.a.tree.ScrollY <= before {
		t.Errorf("ScrollY = %d, want the list moved down from %d", f.a.tree.ScrollY, before)
	}
}
