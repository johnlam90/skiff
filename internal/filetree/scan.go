// =============================================================================
// File: internal/filetree/scan.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"os"
	"path/filepath"
	"strings"
)

// ScanEntry is all the merge needs from a dirent: the name, whether it
// is a directory, and — for symlinks — where it actually points.
// Reducing os.DirEntry to this is what lets a directory listing cross a
// goroutine boundary: an os.DirEntry can lazily stat behind Info(), a
// ScanEntry cannot.
type ScanEntry struct {
	Name  string
	IsDir bool

	// IsLink marks a symlinked entry. IsDir above is the *target's*
	// answer, resolved by the scan, so a symlinked directory arrives at
	// the merge already classified as a directory.
	IsLink bool

	// Real is the symlink target's fully resolved path, filled only for
	// symlinked directories — the only entries a loop can be built out
	// of. Empty when the link is broken or resolution failed.
	Real string

	// lowerName is Name lowercased once at scan time — the merge's sort
	// key. Computing it here (off-thread for the background sweep)
	// replaces two ToLower allocations per comparison inside the sort.
	// Hand-built entries may leave it empty; merge fills the gap.
	lowerName string
}

// DirScan is one directory's freshly-read contents. Err records a read
// that failed (permissions, the directory vanished mid-sweep) so the
// merge can leave that branch alone rather than emptying it.
type DirScan struct {
	Path    string
	Entries []ScanEntry
	Err     error

	// Ignore is the raw bytes of this directory's own .gitignore, or nil
	// when it has none. It rides along with the listing so the merge can
	// refresh the compiled matcher without an event-loop file read, and
	// so the matcher and the entries it filters always come from the
	// same moment on disk.
	Ignore []byte
}

// readDir performs one directory's worth of disk work: the ReadDir, the
// per-entry symlink resolution, and the directory's own .gitignore bytes
// when the listing shows there is one. Everything latency-prone about a
// refresh lives here, which is why ScanDirs can run it on a background
// goroutine and Tree.reload can share the exact same code path.
func readDir(path string) DirScan {
	entries, err := os.ReadDir(path)
	if err != nil {
		return DirScan{Path: path, Err: err}
	}
	scan := DirScan{Path: path, Entries: scanEntries(path, entries)}
	// Only read the ignore file when the listing we already have proves
	// it exists — the alternative is a failing open per directory per
	// tick, which over NFS is the same cost as the ReadDir itself.
	for _, e := range entries {
		if e.Name() == gitignoreName && !e.IsDir() {
			scan.Ignore, _ = os.ReadFile(filepath.Join(path, gitignoreName))
			break
		}
	}
	return scan
}

// scanEntries reduces a ReadDir result to the fields the merge uses,
// dropping the names the tree refuses to show while we're already
// walking the list.
//
// A symlink costs two extra syscalls (Stat to classify through the link,
// EvalSymlinks to name the target) and an ordinary entry costs none, so
// the price is paid only by the directories that actually contain links.
// Classifying with the dirent's own IsDir would report the link rather
// than its target and render every symlinked package as an unopenable
// file row.
func scanEntries(dir string, entries []os.DirEntry) []ScanEntry {
	out := make([]ScanEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if shouldHide(name) {
			continue
		}
		se := ScanEntry{Name: name, IsDir: e.IsDir(), lowerName: strings.ToLower(name)}
		if e.Type()&os.ModeSymlink != 0 {
			se.IsLink = true
			full := filepath.Join(dir, name)
			// A broken link stats as an error and stays a file row;
			// there is nothing to open and nothing to loop through.
			if info, err := os.Stat(full); err == nil && info.IsDir() {
				se.IsDir = true
				if real, err := filepath.EvalSymlinks(full); err == nil {
					se.Real = real
				}
			}
		}
		out = append(out, se)
	}
	return out
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
// background sweep retries them and clears the mark on its own. A Loop
// node is never Loaded, so it never reaches the sweep's work list — the
// same guard that stops the synchronous expand also keeps a `a -> ..`
// link from handing the background goroutine an endless path list.
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
// directory, plus the symlink resolution and .gitignore read each one
// needs, brutal over NFS) and it is safe to run on a background
// goroutine while the renderer walks the tree. Hand the result to
// Tree.ApplyScan on the main thread.
func ScanDirs(paths []string) []DirScan {
	scans := make([]DirScan, 0, len(paths))
	for _, p := range paths {
		scans = append(scans, readDir(p))
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
//
// Returns whether any directory's membership actually changed — the
// signal handleTreeScan gates the finder rebuild on, so a quiet tick
// stops re-indexing a project nobody touched.
func (t *Tree) ApplyScan(scans []DirScan) bool {
	byPath := make(map[string]DirScan, len(scans))
	for _, s := range scans {
		byPath[s.Path] = s
	}
	return t.applyScanNode(t.Root, byPath)
}

// applyScanNode is ApplyScan's recursive worker. It walks the live graph
// rather than the scan list because the graph is the authority on what
// is still Loaded and still reachable. The walk never short-circuits on
// a change: every merged directory must stamp its fingerprint even
// after the answer is already true, or the next sweep re-merges it.
func (t *Tree) applyScanNode(n *Node, byPath map[string]DirScan) bool {
	if n == nil || !n.IsDir || (!n.Loaded && n.ReadErr == nil) {
		return false
	}
	changed := false
	if scan, ok := byPath[n.Path]; ok {
		if scan.Err != nil {
			n.ReadErr = scan.Err
		} else if t.merge(n, scan) {
			changed = true
		}
	}
	for _, c := range n.Children {
		if t.applyScanNode(c, byPath) {
			changed = true
		}
	}
	return changed
}
