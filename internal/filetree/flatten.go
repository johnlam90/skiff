// =============================================================================
// File: internal/filetree/flatten.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

// flatNode pairs a Node with its render depth so the renderer can indent
// without re-walking the tree. A row that folds a single-child directory
// chain (VS Code's "compact folders") anchors on the chain's DEEPEST
// node — every interaction (toggle, active-folder, file ops, git
// letter) acts there — while Top and Display remember what was folded
// in so hit-independent consumers (the active-folder highlight,
// flatIndexOf) can still see the hidden middle dirs.
type flatNode struct {
	Node  *Node
	Depth int

	// Top is the shallowest directory folded into this row; equal to
	// Node on an ordinary row.
	Top *Node

	// Display is the joined "a/b/c" label of a folded chain, empty on
	// an ordinary row (render then falls back to Node.Name).
	Display string
}

// flattenInto appends node into out. If node is an expanded directory, it
// recursively appends its children at depth+1. A run of directories in
// which each is the next one's only entry folds into a single row
// (VS Code's "compact folders"): the row anchors on the deepest dir,
// carries the joined "a/b/c" label, and that deepest dir's expansion
// state decides whether children follow. Folding is knowledge-gated by
// compactChild, so a dir whose listing was never read keeps its own row
// until a load proves it heads a chain.
func flattenInto(n *Node, depth int, out *[]flatNode) {
	if n == nil {
		return
	}
	top := n
	display := ""
	for {
		c := compactChild(n)
		if c == nil {
			break
		}
		if display == "" {
			display = n.Name
		}
		display += "/" + c.Name
		n = c
	}
	*out = append(*out, flatNode{Node: n, Depth: depth, Top: top, Display: display})
	if n.IsDir && n.Expanded {
		for _, c := range n.Children {
			flattenInto(c, depth+1, out)
		}
	}
}

// compactChild returns the sole child that dir n folds over in a
// compact-folders chain, or nil when n keeps its own row. Extension
// demands knowledge and plainness at both ends: n's listing must have
// been read (an unread dir cannot claim to have exactly one entry), and
// neither end may be a symlink — a link's "(symlink)"/"(loop)" label
// must stay on a row of its own, never vanish mid-path. The sentinel
// check is defensive: today a capped listing always has many children,
// but the fold must not depend on that staying true.
func compactChild(n *Node) *Node {
	if !n.IsDir || !n.Loaded || n.IsLink || len(n.Children) != 1 {
		return nil
	}
	c := n.Children[0]
	if !c.IsDir || c.IsLink || c.Sentinel {
		return nil
	}
	return c
}

// containsPath reports whether path names this row's node or any
// directory folded into its chain. Ordinary rows reduce to one
// comparison; chain rows re-walk the same links flattenInto folded, so
// the two can never disagree about what a row contains.
func (f flatNode) containsPath(path string) bool {
	if path == "" || f.Node == nil {
		return false
	}
	if f.Top == nil {
		return f.Node.Path == path
	}
	for n := f.Top; n != nil; n = compactChild(n) {
		if n.Path == path {
			return true
		}
		if n == f.Node {
			return false
		}
	}
	return false
}

// flatten builds the renderer's visible row list from the root's
// children. Render and flatIndexOf both come through here, so the
// scroll math and the painted rows can never disagree about row order.
func (t *Tree) flatten() []flatNode {
	flat := make([]flatNode, 0, 128)
	for _, c := range t.Root.Children {
		flattenInto(c, 0, &flat)
	}
	return flat
}
