// =============================================================================
// File: internal/filetree/flatten_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import "testing"

// TestFlattenInto_Collapsed ensures a non-expanded directory contributes
// only itself to the flat list — its children stay hidden until the user
// expands it.
func TestFlattenInto_Collapsed(t *testing.T) {
	dir := &Node{Name: "d", IsDir: true, Expanded: false, Children: []*Node{
		{Name: "c1"}, {Name: "c2"},
	}}
	var out []flatNode
	flattenInto(dir, 0, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 row for collapsed dir, got %d", len(out))
	}
	if out[0].Depth != 0 {
		t.Fatalf("depth wrong: %d", out[0].Depth)
	}
}

// TestFlattenInto_Expanded checks the recursive case: an expanded directory
// flattens itself plus children at depth+1, and nested expansion compounds.
func TestFlattenInto_Expanded(t *testing.T) {
	leaf := &Node{Name: "leaf"}
	inner := &Node{Name: "inner", IsDir: true, Expanded: true, Children: []*Node{leaf}}
	root := &Node{Name: "root", IsDir: true, Expanded: true, Children: []*Node{inner}}

	var out []flatNode
	flattenInto(root, 0, &out)

	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[0].Node.Name != "root" || out[0].Depth != 0 {
		t.Fatalf("row 0 wrong: %+v", out[0])
	}
	if out[1].Node.Name != "inner" || out[1].Depth != 1 {
		t.Fatalf("row 1 wrong: %+v", out[1])
	}
	if out[2].Node.Name != "leaf" || out[2].Depth != 2 {
		t.Fatalf("row 2 wrong: %+v", out[2])
	}
}

// TestFlattenInto_NilSafe documents that a nil *Node is a tolerated input
// (defensive: avoids requiring callers to nil-check before recursing).
func TestFlattenInto_NilSafe(t *testing.T) {
	var out []flatNode
	flattenInto(nil, 0, &out)
	if len(out) != 0 {
		t.Fatalf("nil node should produce no rows, got %d", len(out))
	}
}

// TestFlattenInto_CompactsSingleDirChain is the core compact-folders
// behavior: a directory whose only child is another directory folds
// into one row carrying the joined path, anchored on the deepest node,
// with the deepest dir's children rendered directly beneath it.
func TestFlattenInto_CompactsSingleDirChain(t *testing.T) {
	leaf := &Node{Name: "inner.txt"}
	c := &Node{Name: "c", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{leaf}}
	b := &Node{Name: "b", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{c}}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{b}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 2 {
		t.Fatalf("expected chain row + leaf, got %d rows", len(out))
	}
	if out[0].Node != c || out[0].Top != a || out[0].Display != "a/b/c" {
		t.Fatalf("chain row wrong: %+v", out[0])
	}
	if out[1].Node != leaf || out[1].Depth != 1 {
		t.Fatalf("leaf should sit directly under the chain row: %+v", out[1])
	}
}

// TestFlattenInto_ChainStopsAtUnloadedDir gates compaction on
// knowledge: a directory whose children were never read cannot claim
// to have exactly one — the chain ends at the last loaded link, so a
// stale claim can never fold a dir whose siblings just haven't been
// seen yet.
func TestFlattenInto_ChainStopsAtUnloadedDir(t *testing.T) {
	c := &Node{Name: "c", IsDir: true}
	b := &Node{Name: "b", IsDir: true, Loaded: false, Children: []*Node{c}}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Children: []*Node{b}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 1 {
		t.Fatalf("expected 1 row, got %d", len(out))
	}
	if out[0].Node != b || out[0].Display != "a/b" {
		t.Fatalf("chain should stop at the unloaded dir: %+v", out[0])
	}
}

// TestFlattenInto_SiblingBreaksChain: two children means no chain —
// the dir renders alone and its children indent under it as before.
func TestFlattenInto_SiblingBreaksChain(t *testing.T) {
	b := &Node{Name: "b", IsDir: true}
	x := &Node{Name: "x.txt"}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{b, x}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[0].Node != a || out[0].Display != "" {
		t.Fatalf("row 0 should be plain a: %+v", out[0])
	}
}

// TestFlattenInto_FileChildDoesNotCompact: a single child that is a
// file keeps the dir as an ordinary row — only dir-into-dir folds.
func TestFlattenInto_FileChildDoesNotCompact(t *testing.T) {
	f := &Node{Name: "only.txt"}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Expanded: true, Children: []*Node{f}}

	var out []flatNode
	flattenInto(a, 0, &out)

	if len(out) != 2 || out[0].Node != a || out[0].Display != "" {
		t.Fatalf("file child must not fold: %+v", out)
	}
}

// TestFlattenInto_LinksNeverJoinChains: a symlink keeps its own row in
// both directions — a link is never absorbed as a chain segment (its
// "(symlink)" label would vanish mid-path) and a link never absorbs
// its child.
func TestFlattenInto_LinksNeverJoinChains(t *testing.T) {
	linkChild := &Node{Name: "b", IsDir: true, IsLink: true}
	a := &Node{Name: "a", IsDir: true, Loaded: true, Children: []*Node{linkChild}}
	var out []flatNode
	flattenInto(a, 0, &out)
	if len(out) != 1 || out[0].Node != a || out[0].Display != "" {
		t.Fatalf("link child must not be absorbed: %+v", out)
	}

	plain := &Node{Name: "d", IsDir: true}
	link := &Node{Name: "l", IsDir: true, IsLink: true, Loaded: true, Children: []*Node{plain}}
	out = out[:0]
	flattenInto(link, 0, &out)
	if len(out) != 1 || out[0].Node != link || out[0].Display != "" {
		t.Fatalf("a link must not absorb its child: %+v", out)
	}
}
