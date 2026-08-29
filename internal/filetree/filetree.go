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
//
// What the tree shows is decided on three independent axes, and keeping
// them separate is the point:
//
//   - shouldHide is a small fixed list of universal noise (.git,
//     node_modules, editor metadata). Never configurable.
//   - Dotfiles are always visible, only muted. Reading .env, .github and
//     .gitignore over SSH is the job; see drawNodeRow's colour cascade.
//   - HideIgnored filters entries the project's own .gitignore files
//     exclude, so the sidebar agrees with the finder's index. On by
//     default, persisted in config.json, and deliberately blind to
//     dotfiles — see ignoreChain for the nested-.gitignore rules the
//     tree honours and the ones it does not.
//
// Symlinks are classified through the link (a directory link expands
// like a directory) and marked in the row. A link whose target is
// already an ancestor is refused rather than followed — see Node.loops.
package filetree

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

	// Parent is the directory this entry was merged into — nil on the
	// root. It exists for one reason: the symlink-loop check has to walk
	// the resolved paths of every ancestor, and a graph that only points
	// downwards cannot answer "have I already been here".
	Parent *Node

	// IsLink marks an entry that is a symbolic link. IsDir is resolved
	// *through* the link (Stat, not the dirent's own type), so a
	// symlinked directory expands like any other — which is exactly why
	// the two are separate bits: the row still has to say it is a link.
	IsLink bool

	// Real is this entry's path with symlinks resolved. Ordinary entries
	// derive it from the parent's Real by string join (no syscall);
	// symlinked directories get filepath.EvalSymlinks. Empty when
	// resolution failed, which the loop check reads as "unknown".
	Real string

	// Loop marks a symlinked directory whose target is already on its own
	// ancestor chain — `a -> ..` and friends. Such a node renders as a
	// link that cannot be opened and never loads children, which is what
	// stops both the expand path and the 10s background sweep from
	// walking forever.
	Loop bool

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

	// lastScan fingerprints the raw listing (plus the HideIgnored flag)
	// this directory's children were last merged from — see
	// scanFingerprint for the byte format. merge compares the next scan
	// against it and returns before the filter/sort/alloc pipeline when
	// nothing changed, which is the 10-second tick's steady state on
	// every directory nobody is touching. Empty until the first merge.
	lastScan []byte

	// mergeEpoch is Tree.filterEpoch as of this node's last merge. A
	// mismatch means a filter input the listing cannot see (an edited
	// .gitignore anywhere, a pinned open file) moved since, so the
	// fingerprint alone must not be trusted and the full merge runs.
	mergeEpoch int
}

// TrashPrefix marks in-place session-trash entries: when moving a
// deleted item to the app's temp trash dir fails (cross-filesystem
// rename), the item is renamed to a hidden sibling carrying this
// prefix instead. The tree and the finder both filter the prefix so
// a trashed item never resurfaces in the UI before Undo restores it.
const TrashPrefix = ".skifftrash-"

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

	// HideIgnored filters .gitignore'd entries out of the tree so the
	// sidebar and the finder (which has always filtered through
	// go-gitignore) answer "is this project noise?" the same way.
	// Defaults to on; App.loadUserConfig overrides it from config.json's
	// "gitignore" key and the ≡ View row flips it.
	//
	// Flipping it only changes what the NEXT directory read keeps, so a
	// caller that wants the change on screen must follow it with
	// Refresh. Dotfiles are exempt — see ignoreChain.
	HideIgnored bool

	// openFiles is the set of absolute paths the app has open in tabs.
	// Nothing on the way to one of them is ever hidden by HideIgnored:
	// a file the user is editing must stay reachable in the sidebar even
	// when the project ignores its whole directory. Replaced wholesale
	// by SetOpenFiles on every refresh, so closing a tab un-pins it.
	openFiles map[string]struct{}

	// filterEpoch versions the merge inputs that live outside the
	// directory's own DirScan: the compiled ignore chain (an edited
	// .gitignore in ANY directory changes what its whole subtree
	// filters to) and the pinned open-file set. Bumped by cacheIgnore,
	// SetOpenFiles and pin only when their input actually changed, and
	// stamped onto each node at merge time — so one bump pushes every
	// loaded directory through exactly one full merge on the next
	// sweep, then the fast-path re-arms. Both refresh walks visit
	// ancestors before descendants, which is what lets a parent's
	// cacheIgnore bump reach its children in the same sweep.
	filterEpoch int

	// ignoreCache holds one compiled .gitignore per directory that has
	// one, keyed by the directory's absolute path. Entries are written
	// by merge, which is the same moment that directory's children are
	// replaced — the matcher and the listing it filters can never be
	// from different reads. Directories without a .gitignore hold no
	// entry at all, so the map is sized by the project's ignore-file
	// count rather than its directory count; an entry for a directory
	// that later leaves the tree lingers until the process exits, which
	// is a handful of strings and not worth a sweep to reclaim.
	ignoreCache map[string]ignoreEntry
}

// New creates a tree rooted at root and pre-loads its top-level children so
// the user sees something immediately. Hidden entries (dotfiles) are kept
// because they're often what people actually want to inspect over SSH.
//
// Gitignore filtering starts on, matching config.json's default, so the
// very first render already agrees with the finder. The root's Real is
// resolved once here and every descendant derives from it, which is what
// keeps the symlink-loop check syscall-free below the root.
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
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	n := &Node{Path: abs, Name: filepath.Base(abs), IsDir: true, Expanded: true, Real: real}
	t := &Tree{Root: n, HideIgnored: true, ignoreCache: map[string]ignoreEntry{}}
	if err := t.loadChildren(n); err != nil {
		return nil, err
	}
	return t, nil
}

// SetOpenFiles records the absolute paths the app currently has open in
// tabs. Gitignore filtering never removes an entry on the way to one of
// them, so opening a file inside an ignored directory makes that
// directory appear rather than leaving the user editing a file with no
// row in the sidebar. Only that path is exempt, though — the directory
// reappears carrying the open file, not its whole build output. Call it
// before a refresh; the next directory read is what acts on it.
func (t *Tree) SetOpenFiles(paths []string) {
	var set map[string]struct{}
	if len(paths) > 0 {
		set = make(map[string]struct{}, len(paths))
		for _, p := range paths {
			if p == "" {
				continue
			}
			set[filepath.Clean(p)] = struct{}{}
		}
	}
	// The tick calls this with the same tab set on every sweep; only a
	// genuine change may bump the epoch, or the merge fast-path would
	// never arm.
	if openFilesEqual(t.openFiles, set) {
		return
	}
	t.openFiles = set
	t.filterEpoch++
}

// openFilesEqual reports whether two open-file sets pin the same paths.
// nil and empty compare equal — both mean "nothing pinned".
func openFilesEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			return false
		}
	}
	return true
}

// pin adds one absolute path to the open-files set without waiting for
// the app's next SetOpenFiles. Reveal uses it so the directory read it
// is about to do already knows the target must survive the filter.
func (t *Tree) pin(path string) {
	p := filepath.Clean(path)
	if _, ok := t.openFiles[p]; ok {
		return
	}
	if t.openFiles == nil {
		t.openFiles = map[string]struct{}{}
	}
	t.openFiles[p] = struct{}{}
	// A new pin changes what the filter keeps, so the re-read Reveal is
	// about to do must not be swallowed by the merge fast-path.
	t.filterEpoch++
}

// loadChildren is the lazy-load entry point used the first time a directory
// is expanded. It defers to reload, which knows how to merge fresh disk
// state with whatever (if anything) we already had cached.
func (t *Tree) loadChildren(n *Node) error {
	if !n.IsDir || n.Loaded {
		return nil
	}
	return t.reload(n)
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
//
// A node marked Loop is refused outright: reading it would list the same
// directory its own ancestor already occupies, and doing so on every
// expand and every background tick is how a `a -> ..` symlink turns into
// an infinite tree.
func (t *Tree) reload(n *Node) error {
	if !n.IsDir || n.Loop {
		return nil
	}
	ds := readDir(n.Path)
	if ds.Err != nil {
		n.ReadErr = ds.Err
		return ds.Err
	}
	t.merge(n, ds)
	return nil
}

// merge replaces n.Children from a directory listing, keeping surviving
// child Nodes by pointer so their Expanded state and loaded
// grandchildren live on. This is the half of a refresh that mutates the
// node graph the renderer walks, so it must run on the main thread —
// see Tree.ApplyScan. It is also the only writer of the ignore cache,
// which is what keeps that cache main-thread-only and lock-free.
//
// Order matters: the directory's own .gitignore is recompiled from the
// scan's bytes first, then the listing is filtered against the ancestor
// chain, then sorted, then capped. Filtering before the cap is
// deliberate — MaxDirChildren should count rows the user can see, not
// the build output that was about to be dropped anyway.
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
//
// The fast-path at the top is the 10-second tick's steady state: when
// the raw listing, the directory's own ignore bytes and every
// Tree-level filter input are byte-for-byte what the last merge
// consumed, the pipeline below would reproduce the children it already
// built (the pipeline is deterministic in exactly those inputs), so
// merge returns before touching anything. See scanUnchanged for what
// "unchanged" has to mean for that claim to hold — symlinks included.
//
// The return value reports a membership change: false from the
// fast-path and from a full merge that reproduced the same raw names;
// true when names actually came or went — or when the ignore bytes
// moved, because those change what the finder's index keeps even when
// this directory's names did not. handleTreeScan gates the finder
// rebuild on it.
func (t *Tree) merge(n *Node, ds DirScan) bool {
	if t.scanUnchanged(n, ds) {
		return false
	}
	// The fingerprint is taken before filterIgnored/sort touch the
	// slice: the comparison above runs against a raw listing, so the
	// stamp must record raw order too.
	fp := scanFingerprint(ds.Entries, t.HideIgnored)
	ignoreChanged := t.cacheIgnore(n.Path, ds.Ignore)
	changed := ignoreChanged || !bytes.Equal(fp, n.lastScan)
	entries := t.filterIgnored(n.Path, ds.Entries)

	// Hand-built entries (tests, future callers) may lack the scan-time
	// sort key; fill it here so the comparator never re-lowers.
	for i := range entries {
		if entries[i].lowerName == "" {
			entries[i].lowerName = strings.ToLower(entries[i].Name)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].lowerName < entries[j].lowerName
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
		child, ok := existing[e.Name]
		// A name that swapped kind — file to directory, or plain entry
		// to symlink — is a different thing wearing the same label, so
		// it gets a fresh node rather than inheriting stale state.
		if !ok || child.IsDir != e.IsDir || child.IsLink != e.IsLink {
			child = &Node{
				Path:   filepath.Join(n.Path, e.Name),
				Name:   e.Name,
				IsDir:  e.IsDir,
				IsLink: e.IsLink,
			}
		}
		child.Parent = n
		child.Real = resolvedPath(n, e)
		// Recomputed every merge because an ancestor's own link target
		// can change under us; the check is pure string work.
		child.Loop = child.IsDir && child.IsLink && child.loops()
		children = append(children, child)
	}
	if hidden > 0 {
		children = append(children, &Node{
			Name:     fmt.Sprintf(moreRowFormat, hidden),
			Sentinel: true,
			Parent:   n,
		})
	}
	n.Children = children
	n.Loaded = true
	n.ReadErr = nil
	n.lastScan = fp
	n.mergeEpoch = t.filterEpoch
	return changed
}

// scanUnchanged reports whether ds would merge into n's existing
// children unchanged, so merge can return before the filter/sort/alloc
// pipeline. True only when every input that pipeline consumes is
// byte-identical to the last merge: the node holds a healthy listing
// (Loaded, no ReadErr on either side), the directory's own ignore
// bytes match the cache, no Tree-level filter input moved
// (mergeEpoch), and the raw entries match the stamped fingerprint.
//
// Symlinks disqualify wholesale — a node that is a link, sits under
// one (Real != Path), or lists one. Real and Loop are recomputed on
// every full merge precisely because an ancestor's link target can
// move without this directory's listing changing, and the fast-path
// must not cache through that. Plain directories, the overwhelming
// majority, still win.
func (t *Tree) scanUnchanged(n *Node, ds DirScan) bool {
	if !n.Loaded || n.ReadErr != nil || ds.Err != nil {
		return false
	}
	if n.IsLink || n.Real != n.Path {
		return false
	}
	if n.mergeEpoch != t.filterEpoch {
		return false
	}
	if !bytes.Equal(ds.Ignore, t.cachedIgnoreRaw(n.Path)) {
		return false
	}
	return fingerprintMatches(n.lastScan, ds.Entries, t.HideIgnored)
}

// fingerprintSep terminates each entry record inside a scan
// fingerprint; the NUL between name and flag can never appear in a
// file name, so records cannot alias across entries.
const fingerprintSep = 0x1e

// entryFlag packs a ScanEntry's kind bits into the fingerprint's one
// flag byte: '0' file, '1' dir, '2' file link, '3' dir link.
func entryFlag(e ScanEntry) byte {
	f := byte('0')
	if e.IsDir {
		f |= 1
	}
	if e.IsLink {
		f |= 2
	}
	return f
}

// scanFingerprint serialises a raw listing for the fast-path
// comparison: one leading HideIgnored flag byte ('1'/'0'), then per
// entry Name, NUL, entryFlag, fingerprintSep. Raw scan order, before
// filtering — os.ReadDir hands entries back name-sorted, so two reads
// of an unchanged directory serialise identically. Symlink targets are
// deliberately not recorded: any link disables the fast-path outright
// (see scanUnchanged), so a stale target can never hide behind a
// matching fingerprint.
func scanFingerprint(entries []ScanEntry, hideIgnored bool) []byte {
	size := 1
	for _, e := range entries {
		size += len(e.Name) + 3
	}
	fp := make([]byte, 0, size)
	if hideIgnored {
		fp = append(fp, '1')
	} else {
		fp = append(fp, '0')
	}
	for _, e := range entries {
		fp = append(fp, e.Name...)
		fp = append(fp, 0, entryFlag(e), fingerprintSep)
	}
	return fp
}

// fingerprintMatches walks entries against a stamped fingerprint in
// lockstep, allocating nothing — the comparison every loaded directory
// pays on every tick, so it must stay cheaper than the merge it
// short-circuits. Any IsLink entry fails the match by fiat, keeping
// the symlink refusal in one place whether the link is new or was
// there at stamp time. An empty fingerprint (never merged) never
// matches.
func fingerprintMatches(fp []byte, entries []ScanEntry, hideIgnored bool) bool {
	if len(fp) == 0 {
		return false
	}
	flag := byte('0')
	if hideIgnored {
		flag = '1'
	}
	if fp[0] != flag {
		return false
	}
	i := 1
	for _, e := range entries {
		if e.IsLink {
			return false
		}
		n := len(e.Name)
		if len(fp)-i < n+3 {
			return false
		}
		if string(fp[i:i+n]) != e.Name || fp[i+n] != 0 ||
			fp[i+n+1] != entryFlag(e) || fp[i+n+2] != fingerprintSep {
			return false
		}
		i += n + 3
	}
	return i == len(fp)
}

// resolvedPath returns the fully-resolved path of entry e inside parent
// dir. Symlinked directories carry the answer the scan already paid for;
// everything else derives it from the parent by string join, so the deep
// interior of a project costs no syscalls at all.
func resolvedPath(parent *Node, e ScanEntry) string {
	if e.Real != "" {
		return e.Real
	}
	if parent.Real == "" {
		return ""
	}
	return filepath.Join(parent.Real, e.Name)
}

// loops reports whether expanding n would re-enter a directory already
// sitting on n's own ancestor chain. Both shapes count: a link straight
// back to an ancestor (`a -> ..`) and a link to a directory that
// *contains* one (which is the same cycle one level up). Comparing
// resolved paths rather than link paths is what catches the mutual case,
// where two links point at each other's parents and no single hop looks
// suspicious.
func (n *Node) loops() bool {
	if n.Real == "" {
		return false
	}
	for a := n.Parent; a != nil; a = a.Parent {
		if a.Real == "" {
			continue
		}
		if a.Real == n.Real || strings.HasPrefix(a.Real, n.Real+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
	t.refreshNode(t.Root)
}

// refreshNode is Tree.Refresh's recursive worker. It reloads only
// directories the tree has read at least once — there's no value in
// reading directories the user has never seen — plus any directory
// carrying a ReadErr, which never got its first successful read and is
// exactly the node whose mark should clear the moment it becomes
// readable again.
func (t *Tree) refreshNode(n *Node) {
	if !n.IsDir || (!n.Loaded && n.ReadErr == nil) {
		return
	}
	_ = t.reload(n)
	for _, c := range n.Children {
		t.refreshNode(c)
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
// the first time it is expanded. A link whose target is already an
// ancestor never opens — it renders chevron-less for exactly that
// reason, so the click has nothing to act on.
func (t *Tree) Toggle(n *Node) {
	if !n.IsDir || n.Loop {
		return
	}
	if !n.Expanded {
		_ = t.loadChildren(n)
	}
	n.Expanded = !n.Expanded
	if n.Expanded {
		t.expandChain(n)
	}
}

// maxChainProbe caps how many single-child directories one expand click
// will load ahead. 32 folds any real src/main/java/... nesting while
// bounding the IO a pathological tree can demand from a single click.
const maxChainProbe = 32

// expandChain loads and expands the single-child directory run under a
// just-expanded dir, so a compact chain opens to its deepest link in
// one click — without this, each click would only lengthen the folded
// label by one segment. Interaction-time IO only: Render never loads,
// so an unclicked dir still costs nothing.
func (t *Tree) expandChain(n *Node) {
	for range maxChainProbe {
		c := compactChild(n)
		if c == nil {
			return
		}
		if !c.Loaded {
			_ = t.loadChildren(c)
		}
		c.Expanded = true
		n = c
	}
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
//
// The target is pinned before the walk starts, so a path inside a
// gitignored directory reveals rather than dead-ends: each missing
// component triggers one re-read of its parent, which now keeps the
// entry because it leads somewhere the user is going. That re-read also
// covers the plain stale case — a file created outside the editor
// between ticks.
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
	t.pin(abs)
	parts := strings.Split(filepath.ToSlash(rel), "/")

	// Walk every directory component, lazily loading + expanding each so the
	// next step can descend into it. The final component is the target row
	// itself; it doesn't need expanding — revealing is about visibility, not
	// auto-opening directories.
	n := t.Root
	for i := range len(parts) - 1 {
		child := t.descend(n, parts[i])
		if child == nil {
			return // hidden or gone — can't descend further
		}
		if !child.Expanded {
			child.Expanded = true
			if !child.Loaded {
				_ = t.loadChildren(child)
			}
		}
		n = child
	}

	// Find the target row among its parent's children so we can scroll to it.
	target := t.descend(n, parts[len(parts)-1])
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

// flatIndexOf returns the row index of target in the renderer's flat
// list, or -1 when target isn't currently visible. It builds the list
// with the very flattenInto walk Render uses — sharing the walk is what
// keeps reveal-scrolling and the render agreeing about row positions —
// and a directory folded into a compact chain resolves to the chain row
// that contains it, since that row is the dir's only presence on screen.
func (t *Tree) flatIndexOf(target *Node) int {
	for i, f := range t.flatten() {
		if f.Node == target || (target.IsDir && f.containsPath(target.Path)) {
			return i
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
			child := t.descend(n, part)
			if child == nil || !child.IsDir {
				ok = false
				break
			}
			n = child
		}
		if ok && n != t.Root {
			n.Expanded = true
			_ = t.loadChildren(n)
		}
	}
}

// descend returns the child of n named name, loading n's children first
// and re-reading them once when the name isn't there. The retry is what
// lets a pinned path inside a gitignored directory surface: the entry
// was filtered out of the last listing, and only a fresh read — taken
// now that the path is pinned — can bring it back. Costs one extra
// ReadDir per genuinely absent component, which is the case that was
// about to return nil anyway.
func (t *Tree) descend(n *Node, name string) *Node {
	_ = t.loadChildren(n)
	if child := childByName(n, name); child != nil {
		return child
	}
	if n.Loop {
		return nil
	}
	_ = t.reload(n)
	return childByName(n, name)
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
