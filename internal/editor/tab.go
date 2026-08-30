// =============================================================================
// File: internal/editor/tab.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/atomicfile"
	"github.com/johnlam90/skiff/internal/theme"
)

// defaultGutterWidth is the line-number column width for files up to 9999
// lines: five digits plus a one-cell pad on the right, with the git
// change-bar sitting in the blank cell at the far-left of the right-aligned
// number. Larger files grow the gutter via gutterWidthFor so the marker
// never overlaps the first digit.
const defaultGutterWidth = 6

// gutterWidthFor returns the line-number column width for a buffer of
// lineCount lines. It keeps defaultGutterWidth for files that fit and grows
// by one cell per extra digit so the git change-bar always has a blank
// leading cell to sit in. Without this, a 10000-line file would render
// "10000" as "▌0000" with the bar overwriting the first digit, because the
// right-aligned number fills every cell the marker shares.
func gutterWidthFor(lineCount int) int {
	if lineCount <= 0 {
		return defaultGutterWidth
	}
	if w := len(strconv.Itoa(lineCount)) + 2; w > defaultGutterWidth {
		return w
	}
	return defaultGutterWidth
}

// GitLineChange describes the marker rendered in the editor gutter for a line.
type GitLineChange int

const (
	GitLineNone GitLineChange = iota
	GitLineModified
	GitLineAdded
	GitLineDeleted
)

// LineEnding is the newline convention a file uses on disk. Buffers are
// normalised to bare LF-separated lines on load (see NewBuffer) and the
// recorded ending is written back on save, so editing one line of a
// CRLF file doesn't turn the whole file into a diff.
type LineEnding int

const (
	// LineEndingLF is "\n". It is the zero value, so a Tab built by hand
	// (tests, scratch buffers) writes POSIX-style.
	LineEndingLF LineEnding = iota
	// LineEndingCRLF is "\r\n" — the convention Windows-authored files
	// arrive with.
	LineEndingCRLF
)

// Newline returns the bytes this ending writes between lines.
func (e LineEnding) Newline() string {
	if e == LineEndingCRLF {
		return "\r\n"
	}
	return "\n"
}

// Tab is a single open file. It owns the on-disk path, the in-memory buffer,
// the per-tab view state (scroll position, cursor, selection anchor), the
// cached syntax-highlight styles, and a dirty flag.
type Tab struct {
	Path    string // Empty for an unsaved/scratch tab.
	Buffer  *Buffer
	Cursor  Position // Where new typed text appears.
	Anchor  Position // Selection anchor; equals Cursor when nothing is selected.
	ScrollY int      // Index of the first visible line.
	ScrollX int      // Index of the first visible column (rune-indexed). Always 0 with Wrap on.
	Dirty   bool

	// LineEnding is the convention the file had on disk. The buffer
	// holds unterminated lines, so this is the only record of how the
	// file wants to be written back; Save re-joins with it.
	LineEnding LineEnding

	// Wrap turns on soft wrap: long lines flow onto continuation rows
	// instead of panning horizontally. Stamped by the app from the user
	// config on tab creation and flipped via SetWrap. ScrollSeg is the
	// wrap-mode half of the scroll anchor — the segment index within
	// ScrollY's line of the first visible row (always 0 with Wrap off).
	// lastWrapW caches the content width of the last wrap-mode render so
	// wheel scrolling between frames can do visual-row math; 0 means
	// "never rendered", which falls back to line scrolling. See wrap.go.
	Wrap      bool
	ScrollSeg int
	lastWrapW int

	// ScrollbarActive is a pure presentational flag: the app sets it
	// while the user is dragging this tab's scrollbar thumb so
	// renderScrollbar can brighten the thumb to Accent, exactly the way
	// drawSplitter brightens the sidebar splitter mid-drag. It never
	// affects geometry or scroll state — see App.setScrollbarDrag.
	ScrollbarActive bool

	Styles     [][]tcell.Style
	StyleStale bool
	GitLines   map[int]GitLineChange

	// hlWinStart / hlWinEnd bound the span of lines Styles currently
	// carries (tokenised as one window, viewport + lead each side), and
	// lastHighlightHeight records the view height it was computed for.
	// Render re-tokenises only when the content changes (StyleStale),
	// the height changes, or the viewport nears the window's edge — so
	// scrolling inside the window is free. See styleWindowStale.
	hlWinStart          int
	hlWinEnd            int
	lastHighlightHeight int

	// Mtime is the file's modification time as of the last successful
	// read or write. The app's periodic disk-reconcile loop compares it
	// against the live mtime to detect external edits.
	Mtime time.Time

	// DiskGone is set when the most recent disk check found the file
	// missing. It exists so we only flash the "deleted on disk" warning
	// once, instead of re-flashing every reconcile tick.
	DiskGone bool

	// cursorMoved is set by every method that changes Cursor; Render
	// consumes it to decide whether to scroll the viewport so the cursor
	// is visible. Without this flag, mouse-wheel scrolling is fought by
	// every redraw — EnsureVisible would snap us back to the cursor.
	cursorMoved bool

	// Undo / redo stacks plus the original on-open snapshot used by
	// RevertFile and the baseline the dirty flag is measured against.
	// savedBaseline tracks the LAST WRITE, not the open — after a save,
	// "does this differ from disk" is a different question from "does
	// this differ from what was opened". undoBytes is the running size
	// of undoStack that trimUndoStack enforces the budget against. See
	// undo.go for the push / coalescing rules and the public
	// Undo / Redo / RevertFile entry points.
	undoStack     []snapshot
	redoStack     []snapshot
	undoOriginal  snapshot
	savedBaseline snapshot
	undoBytes     int
	lastUndoGroup undoGroup
	lastUndoAt    time.Time

	// Mode is "" for a normal text tab and imageMode (= "image") for a
	// read-only image preview. Image tabs reuse the Tab type so the
	// app's tab list, switcher, and modal-routing all just work — the
	// content-mutating methods short-circuit on imageMode and Render
	// delegates to renderImage. See image.go for the render path.
	Mode     string
	Image    image.Image // populated when Mode == imageMode
	ImageFmt string      // "png" / "jpeg" / "gif" — for the status bar

	// Find state — populated when the user opens the find bar and
	// types a query. The UI layer (App) owns the bar geometry and
	// keystroke routing; the tab owns the query, the resolved match
	// list, and the index of the "current" match so the query
	// survives switching tabs and re-opening the bar.
	FindQuery   string
	FindMatches []Match
	FindIndex   int // -1 = no current match; otherwise an index into FindMatches.

	// findRows indexes FindMatches by buffer line so the renderer's
	// per-cell lookup stays sub-linear; findRowsFor is the match count
	// it was built from, which is how a direct write to the exported
	// FindMatches gets caught. See find.go's matchAtRune.
	findRows    map[int]findRowSpan
	findRowsFor int

	// Preview marks a tab opened by single-clicking the file tree: the
	// next single-click preview replaces it in place instead of piling
	// up tabs (VS Code / druk behavior). Editing or an explicit open
	// "pins" the tab. Always read through IsPreview(), which treats a
	// dirty buffer as pinned regardless of this flag.
	Preview bool

	// IndentUnit is the string the editor inserts when the user presses
	// Tab. Detected on file open (DetectIndent) so the editor matches
	// whatever the file already does — a tab-indented Go file gets a
	// real tab; a 2-space-indented file gets two spaces. Mixed-style
	// files take the dominant signal.
	IndentUnit string

	// Bracket-match cache. bracket holds the pair the caret is touching
	// (see bracket.go), bracketFor is the cursor it was computed for,
	// and bracketCached distinguishes "computed, and the answer was no
	// bracket" from "never computed" — Position{} is a legitimate
	// cursor, so the zero value can't carry that on its own.
	bracket       BracketMatch
	bracketFor    Position
	bracketCached bool
}

// ErrBinaryFile marks a refusal to open non-text content into a text
// buffer. Callers surface it as a flash; image formats never hit it —
// they take the image-tab path first.
var ErrBinaryFile = errors.New("looks like a binary file")

// maxOpenBytes is the largest file NewTab will pull into a text buffer.
// Opening costs the file's bytes several times over — the []byte from
// ReadFile, the buffer's per-line strings, and the on-open undo
// snapshot — and every later stage (Chroma lexing, per-rune style
// grids, soft-wrap math) walks all of it. The binary probe only reads
// the first 8KB, so a NUL-free multi-hundred-megabyte log sails past it
// and used to load synchronously on a single click. 32 MiB is far past
// any real source file while making that case impossible.
const maxOpenBytes = 32 << 20

// ErrFileTooLarge marks a refusal to open a file above maxOpenBytes.
// Callers surface it as a flash exactly the way they surface
// ErrBinaryFile; the wrapped message names the file's size and the cap
// so the user can tell "seen and declined" from "missing".
var ErrFileTooLarge = errors.New("file is too large to open")

// ErrNotUTF8 marks a refusal to open content that is not valid UTF-8.
// The buffer edits text through rune slices, and Go maps invalid bytes
// to U+FFFD on decode — so editing any line holding such bytes would
// silently rewrite them on save. Refusing at the door is the same
// tradeoff as ErrBinaryFile: skiff edits UTF-8 text, and says so
// instead of corrupting quietly.
var ErrNotUTF8 = errors.New("not valid UTF-8 — convert the file to UTF-8 to edit it")

// mibString renders a byte count as MiB for the too-large message.
// Sizes that trip the cap are only ever discussed in MiB, so a full
// unit ladder would be dead code.
func mibString(n int64) string {
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MiB"
}

// looksBinary applies git's own heuristic: a NUL byte in the first 8KB
// means binary. UTF-8 text of any language never contains NUL, so
// multibyte files sail through; UTF-16 is refused, exactly as git
// refuses to diff it.
func looksBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// detectLineEnding picks the ending a freshly-read file should be saved
// with: whichever convention most of its newlines already use. A tie —
// including a file with no newline at all — goes to LF, which is what
// the editor writes for brand-new files. A mixed file is normalised to
// its dominant ending on the next save, which is the same call every
// other editor makes.
func detectLineEnding(data []byte) LineEnding {
	crlf, lf := 0, 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		if i > 0 && data[i-1] == '\r' {
			crlf++
			continue
		}
		lf++
	}
	if crlf > lf {
		return LineEndingCRLF
	}
	return LineEndingLF
}

// readTextFile is the single gate every text buffer fills through: it
// stats path against maxOpenBytes before reading (the guard must not be
// paid for by the read), reads, and refuses content looksBinary rejects.
// A missing file returns empty data and a zero mtime — NewTab treats that
// as a brand-new buffer; Reload's callers stat again via reloadFromDisk
// and error earlier since a reload target that vanished is itself a
// failure.
func readTextFile(path string) (data []byte, mtime time.Time, err error) {
	// Stat first: the size guard exists to avoid reading the file, so it
	// cannot be paid for by reading it. The same call supplies the
	// on-disk mtime the app compares against to detect external edits —
	// a missing file leaves it as the zero value, which callers handle
	// explicitly.
	if info, statErr := os.Stat(path); statErr == nil {
		if info.Size() > maxOpenBytes {
			return nil, time.Time{}, fmt.Errorf("%s is %s (limit %s): %w",
				filepath.Base(path), mibString(info.Size()), mibString(maxOpenBytes),
				ErrFileTooLarge)
		}
		mtime = info.ModTime()
	}
	b, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, time.Time{}, readErr
	}
	if looksBinary(b) {
		// A text buffer must never load binary content: every
		// downstream stage — Chroma lexing, per-rune style grids,
		// soft-wrap math — scales with line length, and binary data
		// has pathological "lines". Opening a large zip used to
		// freeze the editor outright.
		return nil, time.Time{}, fmt.Errorf("%s: %w", filepath.Base(path), ErrBinaryFile)
	}
	if !utf8.Valid(b) {
		// Checked after the binary probe on purpose: a zip or other
		// binary blob is also invalid UTF-8, and it should still be
		// reported as "binary", not "not UTF-8" — the binary message
		// is the more accurate one for that content.
		return nil, time.Time{}, fmt.Errorf("%s: %w", filepath.Base(path), ErrNotUTF8)
	}
	return b, mtime, nil
}

// NewTab opens path and returns a Tab. If the file does not exist, the tab
// is created with an empty buffer that will be written on first save —
// matching what most editors do when you "open" a brand-new file path.
// When path looks like an image we recognise (PNG / JPEG / GIF), the tab
// is opened in read-only image-preview mode instead of as text.
func NewTab(path string) (*Tab, error) {
	if path != "" && isImageExt(path) {
		return newImageTab(path)
	}
	var data []byte
	var mtime time.Time
	if path != "" {
		d, m, err := readTextFile(path)
		if err != nil {
			return nil, err
		}
		data, mtime = d, m
	}
	t := &Tab{
		Path:       path,
		Buffer:     NewBuffer(string(data)),
		StyleStale: true,
		Mtime:      mtime,
		LineEnding: detectLineEnding(data),
	}
	t.IndentUnit = DetectIndent(t.Buffer.Lines, path)
	// Record the on-open buffer state so RevertFile has somewhere to
	// rewind to even after the user has typed away.
	t.initUndo()
	return t, nil
}

// newImageTab decodes path as an image and returns a Tab in image
// preview mode. The buffer is left empty (image tabs ignore it) but
// allocated so any code that pokes at t.Buffer doesn't have to nil-check.
func newImageTab(path string) (*Tab, error) {
	img, format, err := decodeImageFile(path)
	if err != nil {
		return nil, err
	}
	var mtime time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		mtime = info.ModTime()
	}
	t := &Tab{
		Path:     path,
		Buffer:   NewBuffer(""),
		Mtime:    mtime,
		Mode:     imageMode,
		Image:    img,
		ImageFmt: format,
	}
	// Capture the empty original snapshot so CanRevert / RevertFile
	// behave sensibly even though image tabs are read-only — they'll
	// just always report "nothing to revert".
	t.initUndo()
	return t, nil
}

// IsImage reports whether the tab is an image-preview, not a text editor.
// Callers use this to skip text-only behaviour (cursor placement, key
// dispatch, save, etc.) without having to know about Mode strings.
func (t *Tab) IsImage() bool {
	return t.Mode == imageMode
}

// IsPreview reports whether the tab is still replaceable by the next
// tree-click preview. A dirty buffer is never a preview — the user's
// edits pin it implicitly, so a half-typed change can't be silently
// swapped out from under them.
func (t *Tab) IsPreview() bool {
	return t.Preview && !t.Dirty
}

// Pin makes a preview tab permanent (drops the italic, stops the
// replace-in-place behavior). Safe to call on any tab.
func (t *Tab) Pin() {
	t.Preview = false
}

// DisplayName returns the basename of Path, or "untitled" for unsaved tabs.
func (t *Tab) DisplayName() string {
	if t.Path == "" {
		return "untitled"
	}
	return filepath.Base(t.Path)
}

// Save writes the buffer to disk and clears Dirty. It is an error to call
// Save on an untitled tab — callers should prompt for a path first. Mtime
// is refreshed so the disk-reconcile loop doesn't immediately think the
// file we just wrote was changed by someone else. Image tabs return an
// error since the editor only knows how to read those, not re-encode them.
func (t *Tab) Save() error {
	if t.IsImage() {
		return fmt.Errorf("image tabs are read-only")
	}
	if t.Path == "" {
		return fmt.Errorf("no path set for tab")
	}
	if err := atomicfile.Replace(t.Path, []byte(t.Buffer.TextWith(t.LineEnding.Newline()))); err != nil {
		return err
	}
	t.Dirty = false
	t.DiskGone = false
	// Disk now holds exactly this buffer, so re-baseline the dirty
	// comparison: after a save, "differs from what was opened" is the
	// wrong question and only "differs from what was written" matters.
	t.markSaved()
	if info, err := os.Stat(t.Path); err == nil {
		t.Mtime = info.ModTime()
	}
	// Save is a natural logical-step boundary: the next typing burst is
	// clearly a separate intent, so don't let it merge into whatever was
	// in flight before the save.
	t.breakUndoGroup()
	return nil
}

// reloadFromDisk re-reads the file from disk into the buffer, through the
// same readTextFile gate NewTab uses: a file that grew past maxOpenBytes
// or turned binary since it was opened (a `git checkout`/`make` swapping
// in a build artifact at the same path) is refused exactly like a first
// open would be, and — since the gate runs before any field on t is
// touched — a refusal leaves the existing buffer completely intact. The
// separate up-front Stat exists only to turn a vanished file into an
// error; readTextFile tolerates a missing path (that's what lets NewTab
// treat one as a brand-new buffer), but a reload target disappearing is
// a failure, not a fresh start. Cursor and anchor are clamped to the new
// content (so the user keeps roughly their place instead of getting
// snapped to line 0); ScrollY is left alone and gets clamped on the next
// render. Dirty is cleared and the syntax cache is invalidated. It does
// NOT touch undo history — that decision belongs to the caller, which is
// why Reload and ReloadKeepHistory both wrap this and differ only in what
// they do to the stacks afterwards.
func (t *Tab) reloadFromDisk() error {
	if _, err := os.Stat(t.Path); err != nil {
		return err
	}
	data, mtime, err := readTextFile(t.Path)
	if err != nil {
		return err
	}
	t.Buffer = NewBuffer(string(data))
	t.LineEnding = detectLineEnding(data)
	t.Cursor = t.Buffer.Clamp(t.Cursor)
	t.Anchor = t.Cursor // drop any selection — line indices may have shifted.
	t.Dirty = false
	t.DiskGone = false
	t.Mtime = mtime
	t.StyleStale = true
	t.cursorMoved = true
	return nil
}

// Reload re-reads the file from disk into the buffer and resets undo
// history. Image tabs decode the file again instead of replacing the
// text buffer. Use this for reloads the user explicitly chose (the
// disk-conflict prompt's "Reload" button) — history is discarded because
// the user is knowingly taking the disk version. For reloads the user did
// NOT ask for (format-on-save, the background external-change reconcile),
// use ReloadKeepHistory instead so their prior edits stay undoable.
func (t *Tab) Reload() error {
	if t.Path == "" {
		return fmt.Errorf("no path set for tab")
	}
	if t.IsImage() {
		img, format, err := decodeImageFile(t.Path)
		if err != nil {
			return err
		}
		info, err := os.Stat(t.Path)
		if err != nil {
			return err
		}
		t.Image = img
		t.ImageFmt = format
		t.Mtime = info.ModTime()
		t.DiskGone = false
		return nil
	}
	if err := t.reloadFromDisk(); err != nil {
		return err
	}
	// Reload re-establishes "what's on disk" as the new baseline. Any
	// prior undo history is meaningless now (the line indices may have
	// shifted, and the user explicitly asked to take the disk version),
	// so reset both stacks and the revert anchor.
	t.initUndo()
	return nil
}

// ReloadKeepHistory re-reads the file from disk like Reload but keeps the
// tab's undo history, pushing the pre-reload buffer as its own undoable
// step. This is the right shape for reloads the user did NOT explicitly
// ask for — format-on-save and the background external-change reconcile —
// where wiping history would destroy work the user never chose to give up.
func (t *Tab) ReloadKeepHistory() error {
	if t.Path == "" {
		return fmt.Errorf("no path set for tab")
	}
	if t.IsImage() {
		return t.Reload() // image tabs have no text history to keep.
	}
	pre := t.captureSnapshot()
	if err := t.reloadFromDisk(); err != nil {
		return err
	}
	t.pushUndoSnapshot(pre)
	t.redoStack = nil
	t.lastUndoGroup = undoGroupNone // the reload never coalesces with typing
	t.markSaved()                   // disk == buffer now; dirty is measured against this state
	return nil
}

// HasSelection reports whether the tab currently has a non-empty selection.
func (t *Tab) HasSelection() bool {
	return t.Cursor != t.Anchor
}

// SelectionText returns the currently selected text, or "" if nothing is
// selected. The text is always returned in document order.
func (t *Tab) SelectionText() string {
	if !t.HasSelection() {
		return ""
	}
	return t.Buffer.Substring(t.Anchor, t.Cursor)
}

// edit is the one seam every text mutation goes through. It records the
// pre-state for undo under group, runs mutate, then applies the trailer
// the rest of the editor reads: the buffer differs from disk (Dirty), the
// highlight cache no longer describes it (StyleStale), the caret needs
// scrolling back into view (cursorMoved), and the find match list has to
// be re-run against the new text. That trailer used to be hand-typed at
// ten call sites and had already drifted — only the Replace* trio
// refreshed the matches, so typing with the find bar open painted the
// highlights at stale offsets.
//
// Two things stay the caller's job. The mutator keeps its own undo group
// (typing coalesces, structural edits don't), and it keeps every no-op
// guard: reaching edit always records undo state — pushUndo either
// opens a new entry or, when the group coalesces, extends the one
// already open (so a typing burst is one entry, not one per keystroke)
// — so an edit call must only happen when a mutation will actually
// happen.
func (t *Tab) edit(group undoGroup, mutate func()) {
	t.pushUndo(group)
	mutate()
	t.Dirty = true
	t.StyleStale = true
	t.cursorMoved = true
	t.refreshFindMatches()
}

// dropSelection is the raw half of DeleteSelection: it removes the
// selected range and collapses the caret to its start WITHOUT the edit
// trailer. It exists so the insert paths can replace a selection inside
// their own single edit step — pushing a second undo entry there would
// make the user undo twice to get back to the pre-paste state.
func (t *Tab) dropSelection() {
	if !t.HasSelection() {
		return
	}
	pos := t.Buffer.DeleteRange(t.Anchor, t.Cursor)
	t.Cursor = pos
	t.Anchor = pos
}

// DeleteSelection removes the selected range and collapses the cursor to the
// start of the selection. A no-op when nothing is selected.
func (t *Tab) DeleteSelection() {
	if t.IsImage() || !t.HasSelection() {
		return
	}
	// Selection deletes are always their own undo step — they can wipe
	// out a lot in one stroke, and merging them into adjacent typing
	// would make the next undo recover content the user thought was
	// just-deleted.
	t.edit(undoGroupStructural, t.dropSelection)
}

// InsertString inserts s at the cursor (replacing any selection first) and
// advances the cursor past the inserted text. Always recorded as a
// structural undo step — pasted text or "\n" presses shouldn't merge
// with the surrounding typing burst. No-op on image tabs.
func (t *Tab) InsertString(s string) {
	if t.IsImage() {
		return
	}
	// Replacing a selection is one step: the delete and the insert share
	// this edit's single undo entry, so one undo gets back to the
	// pre-paste-with-selection state.
	t.edit(undoGroupStructural, func() {
		t.dropSelection()
		t.Cursor = t.Buffer.InsertString(t.Cursor, s)
		t.Anchor = t.Cursor
	})
}

// InsertNewline is what Enter does: split the line at the caret and open
// the new line with the same indentation the old one had, plus one level
// when the caret was sitting after an opening brace / bracket / paren (or,
// in Python and YAML, a colon). Without this, every line in an indented
// block starts at column 0 and the user re-types the leading whitespace by
// hand, which is the single most-noticed thing a terminal editor can get
// wrong.
//
// The whole press is one InsertString call and therefore exactly one undo
// step: Enter-then-undo returns the buffer to where it was, rather than
// stranding the user on a half-indented line.
//
// The indent is read from the text BEFORE the caret at the point the split
// will happen — the start of the selection, when there is one. Deleting a
// selection never touches the runes ahead of its start, so reading the
// prefix up front gives the same answer as reading it after the delete,
// and avoids splitting the operation into two undo entries.
//
// Only "\n" is ever inserted; the file's own ending is restored by Save
// (see Tab.LineEnding), so a CRLF file must not get a CR spliced into the
// middle of a line here.
func (t *Tab) InsertNewline() {
	if t.IsImage() {
		return
	}
	at, _ := PosOrdered(t.Anchor, t.Cursor)
	prefix := t.Buffer.LineRunes(at.Line)
	if at.Col < len(prefix) {
		prefix = prefix[:at.Col]
	}
	t.InsertString("\n" + autoIndentFor(prefix, t.IndentUnit, t.Path))
}

// InsertRune inserts a single typed character at the cursor. Coalesces
// with adjacent runes inside the undo window so a typed word collapses
// into a single undo step rather than one entry per keystroke. No-op
// on image tabs.
func (t *Tab) InsertRune(r rune) {
	if t.IsImage() {
		return
	}
	// The first rune typed over a selection is structural, not typing:
	// coalescing it into the surrounding burst would let one undo bring
	// the replaced text back in the middle of a word.
	group := undoGroupTyping
	if t.HasSelection() {
		group = undoGroupStructural
	}
	t.edit(group, func() {
		t.dropSelection()
		t.Cursor = t.Buffer.InsertString(t.Cursor, string(r))
		t.Anchor = t.Cursor
	})
}

// Backspace deletes the character before the cursor (or the selection if any).
// "Character" means grapheme cluster, not rune: backspacing over "é" takes
// the accent with the e rather than stranding a combining mark on the
// letter in front of it, and one press removes one thing the user can see.
// Coalesces with adjacent backspaces inside the undo window. No-op on
// image tabs.
func (t *Tab) Backspace() {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		t.DeleteSelection()
		return
	}
	if t.Cursor.Line == 0 && t.Cursor.Col == 0 {
		return
	}
	t.edit(undoGroupBackspace, func() {
		t.Cursor = t.Buffer.DeleteRange(t.clusterLeftOf(t.Cursor), t.Cursor)
		t.Anchor = t.Cursor
	})
}

// Delete removes the character after the cursor (or the selection if any),
// again a whole grapheme cluster so a forward delete can't behead a
// character and leave its marks behind. Coalesces with adjacent
// forward-deletes inside the undo window. No-op on image tabs.
func (t *Tab) Delete() {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		t.DeleteSelection()
		return
	}
	if t.Cursor == t.Buffer.EndPos() {
		return
	}
	t.edit(undoGroupDelete, func() {
		t.Cursor = t.Buffer.DeleteRange(t.Cursor, t.clusterRightOf(t.Cursor))
		t.Anchor = t.Cursor
	})
}

// clusterLeftOf returns the position one grapheme cluster before p,
// wrapping to the end of the previous line at column 0 and stopping at the
// start of the buffer. Shared by Backspace and leftward caret motion so
// the two can never disagree about what "one character" is.
func (t *Tab) clusterLeftOf(p Position) Position {
	if p.Col <= 0 {
		if p.Line <= 0 {
			return Position{}
		}
		prev := p.Line - 1
		return Position{Line: prev, Col: len(t.Buffer.LineRunes(prev))}
	}
	return Position{Line: p.Line, Col: PrevCluster(t.Buffer.LineRunes(p.Line), p.Col)}
}

// clusterRightOf returns the position one grapheme cluster after p,
// wrapping to column 0 of the next line past the last rune and stopping at
// the end of the buffer. The mirror of clusterLeftOf, shared by Delete and
// rightward caret motion.
func (t *Tab) clusterRightOf(p Position) Position {
	runes := t.Buffer.LineRunes(p.Line)
	if p.Col >= len(runes) {
		if p.Line >= t.Buffer.LineCount()-1 {
			return Position{Line: p.Line, Col: len(runes)}
		}
		return Position{Line: p.Line + 1}
	}
	return Position{Line: p.Line, Col: NextCluster(runes, p.Col)}
}

// MoveCursor shifts the cursor by dLine lines and dCol characters. When
// extend is true the anchor is left in place so the user is extending a
// selection.
//
// dCol counts grapheme clusters, taken one step at a time: an arrow key
// walks past "é" or an emoji in a single press, and a multi-column delta
// that runs off the end of a line keeps stepping onto the next line
// instead of losing the overshoot. Vertical motion keeps the rune column
// (the historical behaviour — it is not a "sticky visual column") but
// snaps it onto a cluster boundary in the new line.
func (t *Tab) MoveCursor(dLine, dCol int, extend bool) {
	cur := t.Cursor
	if dLine != 0 {
		cur.Line += dLine
		if cur.Line < 0 {
			cur.Line = 0
		}
		if cur.Line >= t.Buffer.LineCount() {
			cur.Line = t.Buffer.LineCount() - 1
		}
		runes := t.Buffer.LineRunes(cur.Line)
		if cur.Col > len(runes) {
			cur.Col = len(runes)
		}
		cur.Col = ClusterStart(runes, cur.Col)
	}
	for n := dCol; n > 0; n-- {
		cur = t.clusterRightOf(cur)
	}
	for n := dCol; n < 0; n++ {
		cur = t.clusterLeftOf(cur)
	}
	t.Cursor = cur
	if !extend {
		t.Anchor = cur
	}
	t.cursorMoved = true
	// Cursor moved on the user's explicit command — close any open
	// coalescing window so the next typing burst is a fresh undo step.
	t.breakUndoGroup()
}

// MoveCursorTo sets the cursor to a specific buffer position. Position is
// clamped within the buffer and snapped back to a grapheme boundary — a
// caret parked between a base rune and its accent would render a cell to
// the left of where it edits — and extend=true preserves the selection
// anchor.
func (t *Tab) MoveCursorTo(p Position, extend bool) {
	p = t.Buffer.Clamp(p)
	p.Col = ClusterStart(t.Buffer.LineRunes(p.Line), p.Col)
	t.Cursor = p
	if !extend {
		t.Anchor = p
	}
	t.cursorMoved = true
	t.breakUndoGroup()
}

// MoveLineHome moves the cursor to column 0 of the current line.
func (t *Tab) MoveLineHome(extend bool) {
	t.Cursor.Col = 0
	if !extend {
		t.Anchor = t.Cursor
	}
	t.cursorMoved = true
	t.breakUndoGroup()
}

// MoveLineEnd moves the cursor to the last column of the current line.
func (t *Tab) MoveLineEnd(extend bool) {
	t.Cursor.Col = len([]rune(t.Buffer.Lines[t.Cursor.Line]))
	if !extend {
		t.Anchor = t.Cursor
	}
	t.cursorMoved = true
	t.breakUndoGroup()
}

// JumpToLine moves the cursor to column 0 of the 1-based line n,
// clamping to the buffer. The selection collapses — a goto is
// navigation, not extension — and cursorMoved is set so the next
// Render scrolls the target into view even if the caller never
// centers the viewport.
func (t *Tab) JumpToLine(n int) {
	if t.IsImage() {
		return
	}
	p := t.Buffer.Clamp(Position{Line: n - 1, Col: 0})
	p.Col = 0
	t.Cursor = p
	t.Anchor = p
	t.cursorMoved = true
	t.breakUndoGroup()
}

// CenterOnCursor scrolls so the cursor's line sits mid-viewport. Used
// by goto-line so the target lands with context above and below it
// instead of hugging the edge the way plain EnsureVisible leaves it.
// viewH <= 0 (headless callers, pre-first-draw) is a no-op — the next
// Render's EnsureVisible still guarantees visibility.
func (t *Tab) CenterOnCursor(viewH int) {
	if viewH <= 0 {
		return
	}
	t.ScrollY = t.Cursor.Line - viewH/2
	// Wrap mode: centering by buffer line is an approximation (segments
	// above may push the target lower), but cursorMoved's EnsureVisible
	// still guarantees the cursor lands on screen next render.
	t.ScrollSeg = 0
	t.clampScroll(viewH)
}

// RestoreView puts a tab back on a remembered place: the caret at cursor
// (clamped, selection collapsed) and the viewport at scrollY. It is the
// seam the app restores a session, a reopened tab, or any other saved
// spot through, so nothing outside this package has to assign Cursor,
// Anchor and ScrollY by hand — which is what used to let a restore skip
// cursorMoved (the next Render would not scroll the caret into view) and
// skip the undo-group break (typing right after a restore would coalesce
// into whatever burst was in flight before it).
//
// ScrollSeg resets because scrollY names a BUFFER LINE: a remembered
// place carries no segment to go with it, and wrap.go's anchor is the
// pair (ScrollY, ScrollSeg) — a stale segment left behind would open the
// file part-way down a wrapped line. Row 0 of that line is the honest
// reading of "scrolled to line N", and it is what CenterOnCursor settles
// on for the same reason.
func (t *Tab) RestoreView(cursor Position, scrollY int) {
	t.MoveCursorTo(cursor, false)
	if scrollY < 0 {
		scrollY = 0
	}
	t.ScrollY = scrollY
	t.ScrollSeg = 0
}

// hlEdgeGuard is how close (in lines) the viewport may drift toward the
// cached highlight window's edge before Render re-tokenises. The
// cushion keeps multi-line constructs that straddle the edge colored
// correctly and turns "re-lex per wheel tick" into "re-lex per ~190
// scrolled lines".
const hlEdgeGuard = 64

// styleWindowStale reports whether the cached highlight window still
// covers the viewport, with guard cushions at interior edges (the
// file's own start and end need no cushion — there is nothing beyond
// them to mis-color).
func (t *Tab) styleWindowStale(viewH int) bool {
	if t.StyleStale || viewH != t.lastHighlightHeight {
		return true
	}
	if t.hlWinEnd > t.Buffer.LineCount() {
		return true // buffer shrank under the cache — defensive
	}
	top, bottom := t.ScrollY, t.ScrollY+viewH
	if t.hlWinStart > 0 && top < t.hlWinStart+hlEdgeGuard {
		return true
	}
	if t.hlWinEnd < t.Buffer.LineCount() && bottom > t.hlWinEnd-hlEdgeGuard {
		return true
	}
	return false
}

// EnsureVisible scrolls the viewport so the cursor is on screen. The
// caller passes the editor area's width and height because the Tab itself
// doesn't know its render rect.
func (t *Tab) EnsureVisible(viewW, viewH int) {
	if t.Wrap {
		wrapW := viewW - gutterWidthFor(t.Buffer.LineCount()) - 1
		t.ensureVisibleWrapped(wrapW, viewH)
		t.ScrollX = 0
		return
	}
	contentW := viewW - gutterWidthFor(t.Buffer.LineCount()) - 1
	if contentW < 1 {
		contentW = 1
	}
	if t.Cursor.Line < t.ScrollY {
		t.ScrollY = t.Cursor.Line
	}
	if t.Cursor.Line >= t.ScrollY+viewH {
		t.ScrollY = t.Cursor.Line - viewH + 1
	}
	// Horizontal scrolling is measured in cells, not runes: ScrollX is a
	// rune index, but "is the caret off the right edge" is a question
	// about the cells between them, and a line of CJK answers it very
	// differently from a line of ASCII. RuneColAtVisual turns the target
	// cell back into a rune index, which lands on a cluster boundary by
	// construction so the pan never starts mid-character.
	runes := t.Buffer.LineRunes(t.Cursor.Line)
	cursorVisual := LineVisualCol(runes, t.Cursor.Col)
	scrollVisual := LineVisualCol(runes, t.ScrollX)
	if cursorVisual < scrollVisual {
		t.ScrollX = RuneColAtVisual(runes, cursorVisual)
	} else if cursorVisual >= scrollVisual+contentW {
		sx := RuneColAtVisual(runes, cursorVisual-contentW+1)
		// A wide glyph straddling the new left edge would leave the caret
		// one cell past the right edge; start at the next cluster instead.
		if LineVisualCol(runes, sx)+contentW <= cursorVisual {
			sx = NextCluster(runes, sx)
		}
		t.ScrollX = sx
	}
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	if t.ScrollX < 0 {
		t.ScrollX = 0
	}
}

// Render draws the editor's content (line numbers, code with syntax
// highlighting, selection, cursor) into the rectangle (x, y, w, h).
// Image tabs delegate to renderImage instead of drawing text.
func (t *Tab) Render(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	if t.IsImage() {
		t.renderImage(scr, th, x, y, w, h)
		return
	}
	// Long files reserve the rightmost column for the scrollbar before
	// any width-dependent math runs, so EnsureVisible and the overflow
	// chevrons agree with what's actually paintable.
	barVisible := t.ScrollbarVisible(h) && w > 2
	barX := x + w - 1
	if barVisible {
		w--
	}
	// Only re-center on the cursor if the cursor moved this tick. Doing it
	// every render fights the user when they scroll with the wheel.
	if t.Wrap {
		wrapW := w - gutterWidthFor(t.Buffer.LineCount()) - 1
		if wrapW < 1 {
			wrapW = 1
		}
		t.lastWrapW = wrapW
		t.ScrollX = 0
		if t.cursorMoved {
			t.ensureVisibleWrapped(wrapW, h)
			t.cursorMoved = false
		}
		t.clampScrollWrapped(wrapW, h)
	} else {
		t.ScrollSeg = 0
		if t.cursorMoved {
			t.EnsureVisible(w, h)
			t.cursorMoved = false
		}
		t.clampScroll(h)
	}
	// Resolve the caret's bracket pair before the highlight block below
	// clears StyleStale — that flag is how the cache learns the buffer
	// changed. One scan per frame at most; cellStyle then just compares
	// positions.
	t.refreshBracketMatch()
	// Re-tokenise only when the cached highlight window no longer
	// covers the viewport (content edit, resize, or the view scrolled
	// near the window's edge). Inside the window, scrolling reuses the
	// cache — re-lexing ~500 lines per wheel tick is what made
	// scrolling crawl on remote machines.
	if t.styleWindowStale(h) {
		t.Styles, t.hlWinStart, t.hlWinEnd = HighlightWindow(t.Path, t.Buffer.Lines, t.ScrollY, h, th)
		t.StyleStale = false
		t.lastHighlightHeight = h
	}

	bg := th.BG
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)

	// Paint the entire editor rectangle with the base background first so
	// any cells we don't draw (short lines, blank rows) still get themed.
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Wrap mode draws its own body (wrapped rows, gutter, cursor) and
	// shares everything above (scroll upkeep, highlight window, base
	// paint) plus the scrollbar below with the line path.
	if t.Wrap {
		t.renderWrappedBody(scr, th, x, y, w, h)
		if barVisible {
			t.renderScrollbar(scr, th, barX, y, h)
		}
		return
	}

	selStart, selEnd := PosOrdered(t.Anchor, t.Cursor)
	hasSel := t.HasSelection()

	gw := gutterWidthFor(t.Buffer.LineCount())
	contentX := x + gw + 1
	contentW := w - gw - 1
	if contentW < 1 {
		contentW = 1
	}

	for row := 0; row < h; row++ {
		lineIdx := t.ScrollY + row
		if lineIdx >= t.Buffer.LineCount() {
			break
		}
		cy := y + row
		isCursorLine := lineIdx == t.Cursor.Line

		// Pick the row background — a hair lighter on the cursor's line so
		// the eye can catch where the caret is from across the screen.
		lineBg := bg
		if isCursorLine {
			lineBg = th.LineHL
		}
		lineBgStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Text)

		// Re-paint this row with its (possibly highlighted) bg.
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, lineBgStyle)
		}

		// Gutter / line number, right-aligned with one trailing space.
		numStr := fmt.Sprintf("%*d", gw-1, lineIdx+1)
		gutterStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
		if isCursorLine {
			gutterStyle = gutterStyle.Foreground(th.AccentSoft)
		}
		if marker, ok := t.GitLines[lineIdx]; ok && marker != GitLineNone {
			scr.SetContent(x, cy, gitLineMarkerRune(marker), nil, gutterStyle.Foreground(gitLineMarkerColor(th, marker)))
		}
		for i, r := range numStr {
			if i == 0 && t.GitLines[lineIdx] != GitLineNone {
				continue
			}
			scr.SetContent(x+i, cy, r, nil, gutterStyle)
		}

		// Line content, with syntax styles, selection bg, and line bg.
		// We walk from the start of the line so tab stops anchor to col 0
		// — a tab one cell into the line still expands to the next stop,
		// not the next-stop-from-the-scroll-offset — and we walk by
		// grapheme cluster so a combining mark rides in its base's cell
		// instead of claiming one of its own. Clipping is done in visual
		// cells rather than rune indices, which is what stops a wide glyph
		// straddling the left edge from painting its right half as a
		// half-character: cell 0 falls off, cell 1 paints a blank.
		runes := t.Buffer.LineRunes(lineIdx)
		var styles []tcell.Style
		if lineIdx < len(t.Styles) {
			styles = t.Styles[lineIdx]
		}
		scrollVisual := LineVisualCol(runes, t.ScrollX)
		visualCol := 0 // visual cell offset from the start of the LINE
		for runeIdx := 0; runeIdx < len(runes); {
			next, width := ClusterAt(runes, runeIdx, visualCol)
			if visualCol+width > scrollVisual {
				st := t.cellStyle(th, styles, lineIdx, runeIdx, lineBg, hasSel, selStart, selEnd)
				glyph, comb := runes[runeIdx], runes[runeIdx+1:next]
				if glyph == '\t' {
					glyph, comb = ' ', nil
				}
				for cell := 0; cell < width; cell++ {
					sc := visualCol - scrollVisual + cell
					if sc < 0 {
						continue
					}
					if sc >= contentW {
						break
					}
					// The cluster's first cell carries the glyph and its
					// combining marks; padding cells carry a space so the
					// area under a wide glyph or a tab still gets the row
					// background.
					if cell > 0 {
						scr.SetContent(contentX+sc, cy, ' ', nil, st)
						continue
					}
					scr.SetContent(contentX+sc, cy, glyph, comb, st)
				}
			}
			visualCol += width
			runeIdx = next
		}

		// Overflow affordance: paint a muted '‹' / '›' over the leftmost /
		// rightmost content cell when the line extends past the viewport
		// in that direction. Without this hint a terminal user has no way
		// to tell that more content exists off-screen — there's no
		// scrollbar to clue them in. visualCol now equals the total
		// visual width of the line; scrollVisual is the visual cell
		// corresponding to ScrollX.
		overflowStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
		if t.ScrollX > 0 {
			scr.SetContent(contentX, cy, '‹', nil, overflowStyle)
		}
		if visualCol-scrollVisual > contentW {
			scr.SetContent(contentX+contentW-1, cy, '›', nil, overflowStyle)
		}
	}

	if barVisible {
		t.renderScrollbar(scr, th, barX, y, h)
	}

	// Position the hardware cursor at its visual column (so a cursor
	// past a tab lands at the tab's *end* cell, not just rune-Col cells
	// to the right of ScrollX).
	cy := y + (t.Cursor.Line - t.ScrollY)
	cursorRunes := t.Buffer.LineRunes(t.Cursor.Line)
	cursorVisual := LineVisualCol(cursorRunes, t.Cursor.Col)
	scrollVisual := LineVisualCol(cursorRunes, t.ScrollX)
	cx := contentX + (cursorVisual - scrollVisual)
	if cy >= y && cy < y+h && cx >= contentX && cx < contentX+contentW {
		scr.ShowCursor(cx, cy)
	} else {
		scr.HideCursor()
	}
}

// cellStyle resolves the final style for the rune at (lineIdx, runeIdx):
// the cached syntax style re-based onto the row background, then the
// selection tint, then the bracket-pair marker, then the find-match tint
// on top. Shared by the line and wrap render paths so the two can't drift
// on styling rules.
//
// Find wins over the bracket marker on purpose: a find sweep is a
// deliberate, transient mode, and its amber background would fight an
// accent-colored bracket sitting inside a hit.
func (t *Tab) cellStyle(th theme.Theme, styles []tcell.Style, lineIdx, runeIdx int, lineBg tcell.Color, hasSel bool, selStart, selEnd Position) tcell.Style {
	st := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	if runeIdx < len(styles) {
		st = styles[runeIdx]
	}
	st = st.Background(lineBg)
	if hasSel {
		p := Position{Line: lineIdx, Col: runeIdx}
		if !PosLess(p, selStart) && PosLess(p, selEnd) {
			st = st.Background(th.Selection)
			// Several syntax colors fall below WCAG AA on the selection
			// blue (comments worst at ~1.5:1, keywords/functions/builtins
			// ~3.4-4.5:1) — SelectionFg trades exactly those for Text and
			// keeps the ones that stay readable.
			fg, _, _ := st.Decompose()
			st = st.Foreground(th.SelectionFg(fg))
		}
	}
	if t.bracket.Found {
		p := Position{Line: lineIdx, Col: runeIdx}
		if p == t.bracket.At {
			st = bracketCellStyle(th, st, t.bracket.Matched)
		} else if t.bracket.Matched && p == t.bracket.Match {
			st = bracketCellStyle(th, st, true)
		}
	}
	if mIdx := t.matchAtRune(lineIdx, runeIdx); mIdx >= 0 {
		if mIdx == t.FindIndex {
			st = st.Background(th.FindCurrent).Foreground(th.BG)
		} else {
			// The match tint drops syntax coloring entirely: several
			// syntax colors (comments worst, ~1.2:1) are illegible on the
			// amber, and a find sweep should read as "here are your
			// hits", not as code that happens to be tinted.
			st = st.Background(th.FindMatch).Foreground(th.Text)
		}
	}
	return st
}

// gitLineMarkerRune returns the gutter glyph for a git line change.
func gitLineMarkerRune(change GitLineChange) rune {
	if change == GitLineDeleted {
		return '▁'
	}
	return '▌'
}

// gitLineMarkerColor returns the gutter color for a git line change.
func gitLineMarkerColor(th theme.Theme, change GitLineChange) tcell.Color {
	if change == GitLineAdded {
		return th.GitAdded
	}
	if change == GitLineDeleted {
		return th.GitDeleted
	}
	return th.GitModified
}

// HitTest converts screen coordinates within this tab's render area to a
// buffer position. ok=false means the click was outside any line.
func (t *Tab) HitTest(localX, localY, w, h int) (Position, bool) {
	if t.Wrap {
		return t.hitTestWrapped(localX, localY, w, h)
	}
	if localY < 0 || localY >= h {
		return Position{}, false
	}
	contentX := gutterWidthFor(t.Buffer.LineCount()) + 1
	line := t.ScrollY + localY
	if line < 0 || line >= t.Buffer.LineCount() {
		return Position{}, false
	}
	if localX < contentX {
		// Click in the gutter — treat as click at column 0 of that line.
		return Position{Line: line, Col: 0}, true
	}
	runes := []rune(t.Buffer.Lines[line])
	// Convert the click's screen column back to a rune column. With tabs
	// expanding to multi-cell tab stops we can't just subtract ScrollX
	// from localX — we have to walk the runes counting visual cells.
	scrollVisual := LineVisualCol(runes, t.ScrollX)
	targetVisual := scrollVisual + (localX - contentX)
	col := RuneColAtVisual(runes, targetVisual)
	if col > len(runes) {
		col = len(runes)
	}
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Col: col}, true
}

// Scroll moves the viewport by delta lines (negative = up). Render runs
// clampScroll afterwards so the user never scrolls into pure void; here we
// just adjust the raw value. In wrap mode a "line" of scrolling is a
// visual row, so a wheel tick over a long wrapped line moves one row,
// not one whole paragraph; before the first render (no cached width
// yet) we fall back to buffer-line motion.
func (t *Tab) Scroll(deltaLines int) {
	if t.Wrap && t.lastWrapW > 0 {
		t.scrollWrapped(deltaLines, t.lastWrapW)
		return
	}
	t.ScrollY += deltaLines
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// ClampCursorToView moves the caret to the nearest visible line when a
// viewport-only scroll (wheel, scrollbar) has left it off screen — the
// optional "caret follows scroll" behavior, called by the app only when
// the user turned it on. Guards, in order: an active selection is never
// clobbered (silently moving the selection head mid-scroll would
// destroy what the user highlighted), and a caret already inside the
// viewport is left untouched so the render pass sees no cursor motion
// at all. The clamped position is inside the viewport by construction,
// which keeps EnsureVisible a no-op afterwards — scroll → cursor never
// feeds back into cursor → scroll, so the one-directional cursorMoved
// contract survives with this feature enabled. Wrap width comes from
// lastWrapW, the same source Scroll uses.
func (t *Tab) ClampCursorToView(viewH int) {
	if t.IsImage() || t.HasSelection() || viewH < 1 {
		return
	}
	if t.Wrap && t.lastWrapW > 0 {
		t.clampCursorWrapped(t.lastWrapW, viewH)
		return
	}
	first := t.ScrollY
	last := t.ScrollY + viewH - 1
	if m := t.Buffer.LineCount() - 1; last > m {
		last = m
	}
	if last < first {
		return // overscrolled past EOF — no visible line to land on.
	}
	switch {
	case t.Cursor.Line < first:
		t.MoveCursorTo(Position{Line: first, Col: t.Cursor.Col}, false)
	case t.Cursor.Line > last:
		t.MoveCursorTo(Position{Line: last, Col: t.Cursor.Col}, false)
	}
}

// ScrollH moves the viewport horizontally by delta rune-columns (negative
// = left). Clamped at zero; the right side is naturally bounded by
// Render's contentW window — scrolling past the longest visible line just
// shows blank space, which is fine. Lives next to Scroll so the app's
// mouse-wheel dispatcher can treat horizontal and vertical wheels
// symmetrically. A no-op in wrap mode — nothing extends past the right
// edge, so a horizontal wheel has nothing to reveal.
func (t *Tab) ScrollH(deltaCols int) {
	if t.Wrap {
		return
	}
	t.ScrollX += deltaCols
	if t.ScrollX < 0 {
		t.ScrollX = 0
	}
}

// clampScroll keeps ScrollY inside a sensible range for the current viewport
// height. The max is "last line still on screen, plus a small overscroll
// pad" so the user can scroll the bottom of the file up to the middle of
// the viewport — which feels much better than abruptly stopping when the
// last line hits the bottom row.
func (t *Tab) clampScroll(viewH int) {
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	overscroll := viewH / 2
	if overscroll < 3 {
		overscroll = 3
	}
	max := t.Buffer.LineCount() - viewH + overscroll
	if max < 0 {
		max = 0
	}
	if t.ScrollY > max {
		t.ScrollY = max
	}
}
