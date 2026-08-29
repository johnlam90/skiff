// =============================================================================
// File: internal/filetree/nav.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"path/filepath"
	"strings"
)

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
