// =============================================================================
// File: internal/filetree/filetree.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package filetree implements the left-hand sidebar's file explorer. It is a
// lazy directory tree: children are only read from disk when their parent is
// expanded, so opening the editor on a huge repo is still instant. The tree
// also keeps a flat list of "currently visible" rows so that hit-testing a
// click against rendered rows is O(1).
package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/scrollbar"
	"github.com/johnlam90/skiff/internal/theme"

	"github.com/johnlam90/skiff/internal/git"
)

// Node is a single entry in the file tree. Directories also carry their
// children (loaded lazily on first expansion); files carry only their path.
type Node struct {
	Path     string
	Name     string
	IsDir    bool
	Expanded bool
	Loaded   bool
	Children []*Node

	// ReadErr records the most recent failed read of this directory —
	// permissions, a vanished path, an I/O error. Without it an
	// unreadable directory renders exactly like an empty one and the
	// tree quietly lies about a permission problem. Cleared by the next
	// successful merge, so a chmod +r shows up on the following refresh.
	ReadErr error

	// Sentinel marks the synthetic "… N more" row appended to a
	// truncated directory. It is not a filesystem entry: Path is empty
	// and HitTest refuses to return it, so a click lands on nothing.
	Sentinel bool
}

// TrashPrefix marks in-place session-trash entries: when moving a
// deleted item to the app's temp trash dir fails (cross-filesystem
// rename), the item is renamed to a hidden sibling carrying this
// prefix instead. The tree and the finder both filter the prefix so
// a trashed item never resurfaces in the UI before Undo restores it.
const TrashPrefix = ".skifftrash-"

// EmptyFolderLabel is the muted placeholder row drawn under the project
// name when the root has no visible children. Without it an empty
// project is indistinguishable from a tree that failed to load, which
// is the first thing a user hits after `mkdir proj && skiff proj`.
const EmptyFolderLabel = "(folder is empty)"

// UnreadableLabel is the muted marker appended to a directory row whose
// last read failed. It is deliberately text rather than a colour: the
// difference between "empty" and "I could not look" has to survive a
// monochrome terminal and a colourblind reader.
const UnreadableLabel = "(unreadable)"

// MaxDirChildren caps how many entries of one directory the tree
// retains. The sidebar is a navigation aid, not a file manager — past a
// few hundred rows nobody is scrolling to find anything, they are using
// the finder (Esc-p), which indexes the whole project regardless of
// this cap. 1000 keeps the retained node graph, the flatten walk and
// the render pass bounded no matter what a build directory contains,
// while sitting far above any hand-maintained directory.
//
// Over the cap the directory gets a trailing "… N more" sentinel row so
// the truncation is visible instead of silent. The sentinel is inert:
// clicking it does nothing (HitTest returns ok=false). Raising the cap
// on click was the alternative and was rejected — it makes a row that
// silently reshapes the tree under the cursor, and it re-opens the
// unbounded case the cap exists to close.
const MaxDirChildren = 1000

// moreRowFormat renders the sentinel row's label from the count of
// entries the cap dropped.
const moreRowFormat = "… %d more"

// GitChangeKind describes the strongest git status a tree row should
// show — the git package's ChangeKind under its historical tree-side
// name, so the badge rendering below reads unchanged.
type GitChangeKind = git.ChangeKind

const (
	GitChangeNone     = git.ChangeNone
	GitChangeModified = git.ChangeModified
	GitChangeAdded    = git.ChangeAdded
	GitChangeDeleted  = git.ChangeDeleted
	GitChangeRenamed  = git.ChangeRenamed
	GitChangeMixed    = git.ChangeMixed
)

// Tree owns the root node and the most recently rendered flat list of
// visible rows. Click hit-testing maps a screen row index back to the Node
// drawn at that row.
type Tree struct {
	Root    *Node
	visible []*Node // index = screen row in the list area; nil for blank rows.
	ScrollY int

	// flatCount is how many rows the last Render flattened the tree
	// into — the "total" the scrollbar's proportions are measured
	// against. It is render-derived state exactly like visible above,
	// so the bar's hit-test answers for the tree the user is actually
	// looking at rather than re-walking the node graph on every click.
	flatCount int

	// ScrollbarActive is a pure presentational flag: the app sets it
	// while the user drags the tree's scrollbar thumb so the thumb
	// brightens to Accent, the same idle/active language the sidebar
	// splitter uses. Never affects geometry.
	ScrollbarActive bool

	// ActiveFolder is the absolute path of the folder the user is
	// currently "working in" — the default target for actions like New
	// File. The Render() method bolds the matching row so the choice is
	// always visible. The app updates this whenever the user clicks a
	// tree node or opens a file.
	ActiveFolder string
	ActiveFile   string

	// DirtyFiles and DirtyFolders carry the project's git status — both
	// indexed by absolute path. Files in DirtyFiles render in the theme's
	// Modified color; folders in DirtyFolders do the same so a collapsed
	// branch still signals there's a change inside. Both maps are nil
	// when the project isn't a git repo or when git status hasn't been
	// loaded yet, and the renderer treats nil as "everything clean".
	DirtyFiles   map[string]GitChangeKind
	DirtyFolders map[string]GitChangeKind

	// IconsEnabled toggles the Nerd Font glyph that prefixes each row.
	// Set by App.loadUserConfig at startup based on the user's
	// config.json + auto-detection. Off means the row is rendered with
	// only the existing chevron (the legacy look) — important for
	// terminals or fonts that can't render the private-use glyphs.
	IconsEnabled bool
}

// New creates a tree rooted at root and pre-loads its top-level children so
// the user sees something immediately. Hidden entries (dotfiles) are kept
// because they're often what people actually want to inspect over SSH.
func New(root string) (*Tree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrInvalid
	}
	n := &Node{Path: abs, Name: filepath.Base(abs), IsDir: true, Expanded: true}
	if err := loadChildren(n); err != nil {
		return nil, err
	}
	return &Tree{Root: n}, nil
}

// loadChildren is the lazy-load entry point used the first time a directory
// is expanded. It defers to reload, which knows how to merge fresh disk
// state with whatever (if anything) we already had cached.
func loadChildren(n *Node) error {
	if !n.IsDir || n.Loaded {
		return nil
	}
	return n.reload()
}

// reload re-reads the directory's children from disk and replaces n.Children
// with the new list. Existing child Nodes whose names still appear on disk
// are kept by-pointer so their Expanded state, loaded grandchildren, etc.
// survive a refresh. New names get fresh Nodes; vanished names are dropped.
//
// A failed read is recorded on the node rather than discarded: callers
// throughout the package ignore this error (an expand or a refresh has
// nowhere to report it), and without the mark the row would render as a
// plain empty directory.
func (n *Node) reload() error {
	if !n.IsDir {
		return nil
	}
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		n.ReadErr = err
		return err
	}
	n.merge(scanEntries(entries))
	return nil
}

// ScanEntry is all the merge needs from a dirent: the name and whether
// it is a directory. Reducing os.DirEntry to this is what lets a
// directory listing cross a goroutine boundary — an os.DirEntry can lazily
// stat behind Info(), a ScanEntry cannot.
type ScanEntry struct {
	Name  string
	IsDir bool
}

// DirScan is one directory's freshly-read contents. Err records a read
// that failed (permissions, the directory vanished mid-sweep) so the
// merge can leave that branch alone rather than emptying it.
type DirScan struct {
	Path    string
	Entries []ScanEntry
	Err     error
}

// scanEntries reduces a ReadDir result to the fields the merge uses,
// dropping the names the tree refuses to show while we're already
// walking the list.
func scanEntries(entries []os.DirEntry) []ScanEntry {
	out := make([]ScanEntry, 0, len(entries))
	for _, e := range entries {
		if shouldHide(e.Name()) {
			continue
		}
		out = append(out, ScanEntry{Name: e.Name(), IsDir: e.IsDir()})
	}
	return out
}

// merge replaces n.Children from a directory listing, keeping surviving
// child Nodes by pointer so their Expanded state and loaded
// grandchildren live on. This is the half of a refresh that mutates the
// node graph the renderer walks, so it must run on the main thread —
// see Tree.ApplyScan.
//
// Entries are sorted (and truncated to MaxDirChildren) before any Node
// is allocated, so expanding a directory with 100k entries costs 1000
// nodes rather than 100k. Sorting first is what makes the cap keep the
// rows a user would actually look at — directories, then names — instead
// of whatever order the filesystem happened to hand back. The sort is
// in place: entries belongs to the caller's DirScan and is not shared.
//
// A successful merge clears ReadErr: this is the point where a
// directory that was unreadable last tick proves it is readable again.
func (n *Node) merge(entries []ScanEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	hidden := 0
	if len(entries) > MaxDirChildren {
		hidden = len(entries) - MaxDirChildren
		entries = entries[:MaxDirChildren]
	}

	existing := make(map[string]*Node, len(n.Children))
	for _, c := range n.Children {
		if c.Sentinel {
			continue // synthetic; never matches a dirent
		}
		existing[c.Name] = c
	}

	children := make([]*Node, 0, len(entries)+1)
	for _, e := range entries {
		if old, ok := existing[e.Name]; ok && old.IsDir == e.IsDir {
			children = append(children, old)
			continue
		}
		children = append(children, &Node{
			Path:  filepath.Join(n.Path, e.Name),
			Name:  e.Name,
			IsDir: e.IsDir,
		})
	}
	if hidden > 0 {
		children = append(children, &Node{
			Name:     fmt.Sprintf(moreRowFormat, hidden),
			Sentinel: true,
		})
	}
	n.Children = children
	n.Loaded = true
	n.ReadErr = nil
}

// Refresh re-reads every directory in the tree that has been loaded at
// least once (i.e. anywhere the user has previously expanded). Surviving
// entries keep their Node pointers so deeper Expanded state is preserved;
// new files appear, deleted files vanish. Scan and merge both happen
// here, so this is the synchronous flavour — right after a file
// operation, where the tree must be correct before the next draw. The
// periodic background refresh uses LoadedDirs / ScanDirs / ApplyScan
// instead so the ReadDir walk doesn't land on the event loop.
func (t *Tree) Refresh() {
	refreshNode(t.Root)
}

// refreshNode is Tree.Refresh's recursive worker. It reloads only
// directories the tree has read at least once — there's no value in
// reading directories the user has never seen — plus any directory
// carrying a ReadErr, which never got its first successful read and is
// exactly the node whose mark should clear the moment it becomes
// readable again.
func refreshNode(n *Node) {
	if !n.IsDir || (!n.Loaded && n.ReadErr == nil) {
		return
	}
	_ = n.reload()
	for _, c := range n.Children {
		refreshNode(c)
	}
}

// LoadedDirs returns the path of every directory the tree has read at
// least once — exactly the set Refresh would re-read, in the same
// depth-first order. It walks the in-memory node graph and touches no
// disk, so the main loop can hand a background sweep its work list
// without stalling on it.
func (t *Tree) LoadedDirs() []string {
	var paths []string
	collectLoadedDirs(t.Root, &paths)
	return paths
}

// collectLoadedDirs is LoadedDirs' recursive worker. It mirrors
// refreshNode's guard, marked-but-unloaded directories included, so the
// background sweep retries them and clears the mark on its own.
func collectLoadedDirs(n *Node, paths *[]string) {
	if n == nil || !n.IsDir || (!n.Loaded && n.ReadErr == nil) {
		return
	}
	*paths = append(*paths, n.Path)
	for _, c := range n.Children {
		collectLoadedDirs(c, paths)
	}
}

// ScanDirs reads each directory in paths and returns the listings. It
// touches no Node and no Tree field, which is the whole point: this is
// the expensive, latency-prone half of a refresh (one ReadDir per loaded
// directory, brutal over NFS) and it is safe to run on a background
// goroutine while the renderer walks the tree. Hand the result to
// Tree.ApplyScan on the main thread.
func ScanDirs(paths []string) []DirScan {
	scans := make([]DirScan, 0, len(paths))
	for _, p := range paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			scans = append(scans, DirScan{Path: p, Err: err})
			continue
		}
		scans = append(scans, DirScan{Path: p, Entries: scanEntries(entries)})
	}
	return scans
}

// ApplyScan merges a completed background scan into the node graph with
// the same identity-preserving semantics as Refresh. Main-thread only —
// it rewrites Children on the nodes the renderer walks.
//
// The scan may be slightly stale by the time it lands, so the merge is
// deliberately conservative: only directories that are still Loaded (or
// still marked unreadable) and still present in the scan are touched. A
// directory the user expanded after the scan started isn't in the
// listing and keeps the fresh children its own read just produced; a
// directory that failed to read keeps the children it had rather than
// blinking empty, and picks up the ReadErr mark so the row says so.
func (t *Tree) ApplyScan(scans []DirScan) {
	byPath := make(map[string]DirScan, len(scans))
	for _, s := range scans {
		byPath[s.Path] = s
	}
	applyScanNode(t.Root, byPath)
}

// applyScanNode is ApplyScan's recursive worker. It walks the live graph
// rather than the scan list because the graph is the authority on what
// is still Loaded and still reachable.
func applyScanNode(n *Node, byPath map[string]DirScan) {
	if n == nil || !n.IsDir || (!n.Loaded && n.ReadErr == nil) {
		return
	}
	if scan, ok := byPath[n.Path]; ok {
		if scan.Err != nil {
			n.ReadErr = scan.Err
		} else {
			n.merge(scan.Entries)
		}
	}
	for _, c := range n.Children {
		applyScanNode(c, byPath)
	}
}

// shouldHide is the project's small, opinionated list of names the file
// tree refuses to show. These are universally noise: VCS metadata, OS
// junk, language-specific build caches — plus in-place session-trash
// entries, which are deleted content awaiting Undo, not files.
func shouldHide(name string) bool {
	if strings.HasPrefix(name, TrashPrefix) {
		return true
	}
	switch name {
	case ".git", ".svn", ".hg",
		".DS_Store",
		"node_modules",
		".idea", ".vscode":
		return true
	}
	return false
}

// flatNode pairs a Node with its render depth so the renderer can indent
// without re-walking the tree.
type flatNode struct {
	Node  *Node
	Depth int
}

// flattenInto appends node into out. If node is an expanded directory, it
// recursively appends its children at depth+1.
func flattenInto(n *Node, depth int, out *[]flatNode) {
	if n == nil {
		return
	}
	*out = append(*out, flatNode{Node: n, Depth: depth})
	if n.IsDir && n.Expanded {
		for _, c := range n.Children {
			flattenInto(c, depth+1, out)
		}
	}
}

// listHeaderRows is how many rows of a tree render rect sit above the
// scrollable list: the EXPLORER header and the project-root row. Both
// are pinned, which is why HitTest offsets by the same number and the
// scrollbar starts below them.
const listHeaderRows = 2

// listArea splits a render rect h rows tall into the pinned header
// block and the scrollable list below it, returning the list's row
// offset within the rect and its height (never negative).
func listArea(h int) (offset, height int) {
	height = h - listHeaderRows
	if height < 0 {
		height = 0
	}
	return listHeaderRows, height
}

// minScrollbarWidth is the narrowest tree rect that still gets a
// scrollbar. At the app's minSidebarWidth (18) the rect is 17 columns
// and labels keep 16 — plenty — so the bar is present at every width a
// user can drag to. The floor only exists so a pathologically narrow
// rect (a tiny terminal, a test fixture) spends its cells on names
// instead of on a bar with nothing left to point at.
const minScrollbarWidth = 6

// Render draws the tree into the rectangle (x, y, w, h). Each visible row
// is also remembered (in t.visible) so HitTest can map a click back to a
// node without re-walking the tree.
//
// A listing taller than the list area reserves the rect's rightmost
// column for the scrollbar and draws the rows one cell narrower, so
// labels and the git change letter stop where the bar starts. The bar
// spans only the scrollable rows: the EXPLORER header and the project
// root above it are pinned, and a bar drawn past them would claim they
// scroll.
func (t *Tree) Render(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	bg := th.SidebarBG
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Header — small all-caps label above the project name. The
	// project name itself is also a click target: it's the only way
	// to reset the active folder back to the root once a subfolder
	// has been selected. Render bold/Accent when it *is* the active
	// folder, plain text otherwise — same visual rule the children
	// rows follow, so the highlight is honest.
	headerStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Bold(true)
	drawString(scr, x, y, w, " EXPLORER", headerStyle)
	rootActive := t.ActiveFolder == "" || t.ActiveFolder == t.Root.Path
	rootStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text).Bold(true)
	if rootActive {
		rootStyle = tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	}
	rootChange := t.DirtyFolders[t.Root.Path]
	if rootChange != GitChangeNone {
		rootStyle = rootStyle.Foreground(gitChangeColor(th, rootChange))
	}
	drawString(scr, x, y+1, w, " "+t.Root.Name, rootStyle)
	drawChangeLetter(scr, x, y+1, w, rootChange, rootStyle)

	// Build the flat list of visible rows from the root's children.
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}
	t.flatCount = len(flat)

	listOff, listH := listArea(h)
	listTop := y + listOff
	t.clampScroll(len(flat), listH)

	// Reserve the bar's column before any row is drawn so truncation
	// and the paint agree — the same order Tab.Render uses.
	rowW := w
	bar := t.scrollbarVisible(w, listH)
	if bar {
		rowW--
	}

	// An empty project renders as a bare root row with nothing under
	// it, which reads as "the tree failed to load" rather than "there
	// is nothing here". Say so explicitly, in the muted tone the row
	// deserves. drawString clips to w, so a narrow sidebar truncates
	// instead of bleeding into the editor.
	//
	// A root we could not read gets the other label: "empty" would be a
	// fabrication, and the permission problem is the one thing the user
	// needs to know to fix it.
	if len(flat) == 0 {
		if listH > 0 {
			label := EmptyFolderLabel
			if t.Root.ReadErr != nil {
				label = UnreadableLabel
			}
			emptyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Italic(true)
			drawString(scr, x, listTop, rowW, " "+label, emptyStyle)
		}
		t.visible = nil
		return
	}

	visible := make([]*Node, 0, listH)
	for row := 0; row < listH; row++ {
		idx := t.ScrollY + row
		if idx < 0 || idx >= len(flat) {
			visible = append(visible, nil)
			continue
		}
		item := flat[idx]
		active := item.Node.Path == t.ActiveFile || (item.Node.IsDir && item.Node.Path == t.ActiveFolder)
		change := t.changeKind(item.Node)
		drawNodeRow(scr, th, x, listTop+row, rowW, item, active, change, t.IconsEnabled)
		visible = append(visible, item.Node)
	}
	t.visible = visible
	if bar {
		t.renderScrollbar(scr, th, x+w-1, listTop, listH)
	}
}

// scrollbarVisible reports whether a w-wide rect with a listH-row list
// area draws a bar: only when the last flatten produced more rows than
// fit, and only when the rect can spare the column. A full-height thumb
// says nothing, so it is not drawn — the same rule the editor follows.
func (t *Tree) scrollbarVisible(w, listH int) bool {
	if w < minScrollbarWidth {
		return false
	}
	_, _, ok := scrollbar.Geom(t.flatCount, listH, t.ScrollY)
	return ok
}

// ScrollbarVisible reports whether the tree draws a scrollbar in a w×h
// render rect. The app uses it to decide whether the sidebar's
// rightmost column is a scroll target or an ordinary tree row.
func (t *Tree) ScrollbarVisible(w, h int) bool {
	_, listH := listArea(h)
	return t.scrollbarVisible(w, listH)
}

// ScrollbarHit reports whether a click at rect-local (localX, localY)
// inside a w×h render rect landed on the scrollbar. The bar owns the
// rect's rightmost column, which is one to the LEFT of the sidebar's
// resize splitter — the splitter lives outside this rect entirely (see
// App.sidebarRect), so the two can never contend for a cell.
func (t *Tree) ScrollbarHit(localX, localY, w, h int) bool {
	listOff, listH := listArea(h)
	if !t.scrollbarVisible(w, listH) {
		return false
	}
	return localX == w-1 && localY >= listOff && localY < listOff+listH
}

// ScrollToBarRow scrolls the list so the thumb centers on the rect-local
// row localY of a w×h render rect — the click-to-jump and drag path,
// which are the same gesture as far as the bar is concerned. No-op when
// no bar is drawn.
func (t *Tree) ScrollToBarRow(w, h, localY int) {
	listOff, listH := listArea(h)
	if !t.scrollbarVisible(w, listH) {
		return
	}
	t.ScrollY = scrollbar.TargetForThumb(t.flatCount, listH, localY-listOff)
}

// renderScrollbar paints the list area's one-column bar at x: the same
// shaded track and solid thumb the editor draws, on the sidebar's own
// background so the column reads as part of the panel. The thumb
// brightens to Accent while the user drags it, matching both the editor
// bar and the resize splitter.
func (t *Tree) renderScrollbar(scr tcell.Screen, th theme.Theme, x, y, listH int) {
	thumbStart, thumbLen, ok := scrollbar.Geom(t.flatCount, listH, t.ScrollY)
	if !ok {
		return
	}
	thumbFg := th.Muted
	if t.ScrollbarActive {
		thumbFg = th.Accent
	}
	trackStyle := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	thumbStyle := tcell.StyleDefault.Background(th.SidebarBG).Foreground(thumbFg)
	for row := 0; row < listH; row++ {
		r, st := scrollbar.Track, trackStyle
		if row >= thumbStart && row < thumbStart+thumbLen {
			r, st = scrollbar.Thumb, thumbStyle
		}
		scr.SetContent(x, y+row, r, nil, st)
	}
}

// changeKind returns the git status color category for a tree node.
func (t *Tree) changeKind(n *Node) GitChangeKind {
	if n == nil {
		return GitChangeNone
	}
	if n.IsDir {
		return t.DirtyFolders[n.Path]
	}
	return t.DirtyFiles[n.Path]
}

// drawNodeRow renders one tree row with proper indent, chevron, and color.
// active=true marks the active file or current working folder. change marks
// uncommitted git status and overrides the normal foreground so changed names
// stand out in the tree like other modern editors.
// withIcons=true prefixes the name with a Nerd Font glyph + space; off
// renders the legacy chevron-only look for terminals that can't show
// the private-use glyphs.
//
// When icons are enabled the row is rendered in three segments
// (prefix → glyph → name) so the glyph can take its own per-language
// colour while the name keeps the row's normal fg/dirty/active
// styling. That's the visual cue you find in nvim-tree and friends:
// a quick eye-scan picks out Go from Ruby from Markdown without
// reading any text.
func drawNodeRow(scr tcell.Screen, th theme.Theme, x, y, w int, item flatNode, active bool, change GitChangeKind, withIcons bool) {
	bg := th.SidebarBG
	indent := strings.Repeat("  ", item.Depth)

	// The "… N more" sentinel is not a filesystem entry: no chevron, no
	// glyph, no git badge, and italic-muted so it reads as a note about
	// the list rather than another row in it.
	if item.Node.Sentinel {
		st := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Italic(true)
		drawString(scr, x, y, w, " "+indent+"  "+item.Node.Name, st)
		return
	}

	// A directory whose last read failed is dimmed and labelled. The
	// dimming is the glance-level cue; the label is what survives a
	// monochrome terminal, and it is placed before active/dirty in the
	// cascade below so a loud row still keeps the text.
	unreadable := item.Node.ReadErr != nil

	// Compute the row-level foreground via this priority cascade
	// (highest wins last):
	//
	//   1. base = FolderColor / FileColor for the node type
	//   2. dotfile/dotdir → Muted, so .gitignore / .github read as
	//      "metadata, not source" without disappearing
	//   3. active folder → Accent, so the current target is loud
	//   4. dirty → Modified, so uncommitted work always stands out
	//
	// Active/dirty deliberately override the dotfile dimming — a
	// modified .env or the active .github/ folder is still the most
	// important thing on the row.
	var fg tcell.Color
	if item.Node.IsDir {
		fg = th.FolderColor
	} else {
		fg = th.FileColor
	}
	if strings.HasPrefix(item.Node.Name, ".") || unreadable {
		fg = th.Muted
	}
	if active {
		fg = th.Accent
	}
	if change != GitChangeNone {
		fg = gitChangeColor(th, change)
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(fg)
	if active {
		rowStyle = rowStyle.Bold(true)
	}

	// Build the left chunk (indent + chevron + space) and right chunk
	// (name, with a trailing slash for dirs). Both render in rowStyle;
	// only the glyph between them gets its own colour.
	var prefix, suffix string
	if item.Node.IsDir {
		chev := "▸"
		if item.Node.Expanded {
			chev = "▾"
		}
		prefix = " " + indent + chev + " "
		suffix = item.Node.Name + "/"
	} else {
		prefix = " " + indent + "  "
		suffix = item.Node.Name
	}
	if unreadable {
		suffix += " " + UnreadableLabel
	}

	if !withIcons {
		drawString(scr, x, y, w, prefix+suffix, rowStyle)
		drawChangeLetter(scr, x, y, w, change, rowStyle)
		return
	}

	glyph := icons.For(item.Node.Name, item.Node.IsDir, item.Node.Expanded)
	glyphFg := icons.ColorFor(item.Node.Name, item.Node.IsDir, fg)
	// Dirty files keep their per-language glyph colour — the language
	// hue is the at-a-glance cue, and the name turning Modified is
	// already enough to flag "this is dirty".
	glyphStyle := tcell.StyleDefault.Background(bg).Foreground(glyphFg)
	if active {
		glyphStyle = glyphStyle.Bold(true)
	}

	drawString(scr, x, y, w, prefix, rowStyle)
	px := len([]rune(prefix))
	drawString(scr, x+px, y, w-px, glyph, glyphStyle)
	gx := len([]rune(glyph))
	drawString(scr, x+px+gx, y, w-px-gx, "  "+suffix, rowStyle)
	drawChangeLetter(scr, x, y, w, change, rowStyle)
}

// drawChangeLetter paints the git status letter (with one leading space
// so it survives long truncated names) at the row's right edge. No-op
// when the row is clean or the row is too narrow to fit it.
func drawChangeLetter(scr tcell.Screen, x, y, w int, change GitChangeKind, st tcell.Style) {
	letter := gitChangeLetter(change)
	if letter == 0 || w < 4 {
		return
	}
	scr.SetContent(x+w-3, y, ' ', nil, st)
	scr.SetContent(x+w-2, y, letter, nil, st)
}

// gitChangeLetter maps git status kinds to the one-cell letter drawn at
// the row's right edge — the same vocabulary the GIT panel uses (M/A/D/R)
// plus '~' for a folder's "mixed changes" state. Hue alone can't carry
// git status for colorblind users; the letter is the non-color channel.
func gitChangeLetter(change GitChangeKind) rune {
	switch change {
	case GitChangeAdded:
		return 'A'
	case GitChangeDeleted:
		return 'D'
	case GitChangeRenamed:
		return 'R'
	case GitChangeMixed:
		return '~'
	case GitChangeModified:
		return 'M'
	}
	return 0
}

// gitChangeColor maps git status kinds to the tree row foreground.
func gitChangeColor(th theme.Theme, change GitChangeKind) tcell.Color {
	switch change {
	case GitChangeAdded:
		return th.GitAdded
	case GitChangeDeleted:
		return th.GitDeleted
	case GitChangeRenamed:
		return th.GitRenamed
	case GitChangeMixed:
		return th.GitMixed
	case GitChangeModified:
		return th.GitModified
	}
	return th.FileColor
}

// drawString writes s left-aligned within [x, x+w). Excess content is
// truncated; short content is implicitly padded by the row's pre-painted bg.
func drawString(scr tcell.Screen, x, y, w int, s string, st tcell.Style) {
	col := 0
	for _, r := range s {
		if col >= w {
			return
		}
		scr.SetContent(x+col, y, r, nil, st)
		col++
	}
}

// clampScroll keeps ScrollY within bounds for the current visible-row count.
func (t *Tree) clampScroll(total, viewH int) {
	if total <= viewH {
		t.ScrollY = 0
		return
	}
	max := total - viewH
	if t.ScrollY > max {
		t.ScrollY = max
	}
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// HitTest maps a click within the tree's render rectangle to a Node.
// Row 0 is the "EXPLORER" header (not clickable). Row 1 is the project
// root name — clicking it returns t.Root so the caller can set the
// active folder back to the project root, which is otherwise
// unreachable once the user has selected any subfolder. Rows 2+ map
// into the rendered children list.
//
// ok=false means the click landed on the EXPLORER header, empty space
// below the last entry, or the "… N more" sentinel. The sentinel is
// deliberately inert: it is a note about the list, not an entry, and
// every caller of HitTest goes on to open, expand, or target the node
// it gets back.
func (t *Tree) HitTest(localX, localY int) (*Node, bool) {
	_ = localX
	if localY < 1 {
		return nil, false
	}
	if localY == 1 {
		return t.Root, true
	}
	row := localY - listHeaderRows
	if row < 0 || row >= len(t.visible) {
		return nil, false
	}
	n := t.visible[row]
	if n == nil || n.Sentinel {
		return nil, false
	}
	return n, true
}

// Toggle expands or collapses a directory node, lazily loading its children
// the first time it is expanded.
func (t *Tree) Toggle(n *Node) {
	if !n.IsDir {
		return
	}
	if !n.Expanded {
		_ = loadChildren(n)
	}
	n.Expanded = !n.Expanded
}

// Scroll moves the file tree's viewport by delta rows (negative = up).
func (t *Tree) Scroll(delta int) {
	t.ScrollY += delta
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// Reveal expands every directory from the tree root down to path's parent so
// the file becomes visible in the sidebar, then scrolls the viewport so the
// row lands on screen. Opening a file via the finder (Esc-p) or the command
// line lands on a path whose ancestors are still collapsed — without this,
// the active-file highlight is set but the row itself is invisible, leaving
// the sidebar out of sync with the editor like a tab with no tab bar entry.
//
// When the target row is already inside the current viewport the scroll
// position is left untouched, so clicking a visible row in the tree (which
// also routes through openFile) doesn't snap it to the top.
//
// No-op when path isn't under the root, escapes it, or lives inside a hidden
// directory the tree refuses to show (e.g. .git). viewH is the row count the
// renderer will hand Render's list area; pass 0 to expand ancestors without
// scrolling (used when the sidebar is hidden).
func (t *Tree) Reveal(path string, viewH int) {
	if t.Root == nil {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(t.Root.Path, abs)
	if err != nil {
		return
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")

	// Walk every directory component, lazily loading + expanding each so the
	// next step can descend into it. The final component is the target row
	// itself; it doesn't need expanding — revealing is about visibility, not
	// auto-opening directories.
	n := t.Root
	for i := 0; i < len(parts)-1; i++ {
		if !n.Loaded {
			_ = loadChildren(n)
		}
		child := childByName(n, parts[i])
		if child == nil {
			return // hidden or gone — can't descend further
		}
		if !child.Expanded {
			child.Expanded = true
			if !child.Loaded {
				_ = loadChildren(child)
			}
		}
		n = child
	}

	// Find the target row among its parent's children so we can scroll to it.
	if !n.Loaded {
		_ = loadChildren(n)
	}
	target := childByName(n, parts[len(parts)-1])
	if target == nil {
		return
	}

	idx := t.flatIndexOf(target)
	if idx < 0 {
		return
	}
	if viewH <= 0 {
		return
	}
	// Leave the viewport alone when the row is already on screen — a click on
	// a visible row shouldn't snap it to the top.
	if idx >= t.ScrollY && idx < t.ScrollY+viewH {
		return
	}
	t.ScrollY = idx
}

// flatIndexOf returns the row index of target in the renderer's flat list
// (the same pre-order walk Render builds via flattenInto), or -1 when target
// isn't currently visible. Mirrors the render order exactly so the index we
// scroll to is the row the user actually sees.
func (t *Tree) flatIndexOf(target *Node) int {
	idx := 0
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if n == target {
			return true
		}
		idx++
		if n.IsDir && n.Expanded {
			for _, c := range n.Children {
				if walk(c) {
					return true
				}
			}
		}
		return false
	}
	for _, c := range t.Root.Children {
		if walk(c) {
			return idx
		}
	}
	return -1
}

// ExpandedDirs returns the project-relative path of every expanded
// directory below the root, in depth-first order — the shape the
// session store persists. The root itself is excluded (it is always
// expanded).
func (t *Tree) ExpandedDirs() []string {
	var out []string
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if !c.IsDir {
				continue
			}
			if c.Expanded {
				if rel, err := filepath.Rel(t.Root.Path, c.Path); err == nil {
					out = append(out, rel)
				}
			}
			walk(c)
		}
	}
	walk(t.Root)
	return out
}

// ExpandDirs re-expands each project-relative directory path, lazily
// loading children along the way — the restore half of ExpandedDirs.
// Paths that no longer exist (or point at files now) are skipped; a
// saved session may be stale and restore must shrug that off.
func (t *Tree) ExpandDirs(rels []string) {
	for _, rel := range rels {
		n := t.Root
		ok := true
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if part == "" || part == "." {
				continue
			}
			_ = loadChildren(n)
			child := childByName(n, part)
			if child == nil || !child.IsDir {
				ok = false
				break
			}
			n = child
		}
		if ok && n != t.Root {
			n.Expanded = true
			_ = loadChildren(n)
		}
	}
}

// childByName returns the direct child of n named name, or nil when no such
// child exists. Reveal uses it to descend the path component by component.
// The "… N more" sentinel is skipped: it has no path on disk, so
// descending into it would walk off the filesystem.
func childByName(n *Node, name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name && !c.Sentinel {
			return c
		}
	}
	return nil
}
