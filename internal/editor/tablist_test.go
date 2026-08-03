// =============================================================================
// File: internal/editor/tablist_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package editor

import "testing"

// memTab builds a detached tab for list tests — no file behind it.
func memTab(path string, preview bool) *Tab {
	t := &Tab{Path: path, Buffer: NewBuffer("")}
	t.Preview = preview
	return t
}

// TestTabList_ZeroValueIsEmpty pins the zero value: usable, empty, and
// nil-safe — no constructor to forget.
func TestTabList_ZeroValueIsEmpty(t *testing.T) {
	var l TabList
	if l.Len() != 0 || l.Active() != nil || l.At(0) != nil || l.Preview() != nil {
		t.Fatal("zero-value list must be empty and nil-safe")
	}
	if l.Activate(memTab("x", false)) {
		t.Fatal("activating an absent tab must fail")
	}
	if l.Remove(memTab("x", false)) {
		t.Fatal("removing an absent tab must fail")
	}
	l.ActivateAt(3) // must not panic
}

// TestTabList_AppendActivatesAndLooksUp pins the basic open path:
// append activates the new tab, and Lookup finds it by path.
func TestTabList_AppendActivatesAndLooksUp(t *testing.T) {
	var l TabList
	a, b := memTab("a.go", false), memTab("b.go", false)
	l.Append(a)
	l.Append(b)
	if l.Len() != 2 || l.Active() != b || l.ActiveIndex() != 1 {
		t.Fatalf("append should activate: active=%v idx=%d", l.Active(), l.ActiveIndex())
	}
	if l.Lookup("a.go") != a || l.Lookup("zzz") != nil {
		t.Fatal("Lookup should find open paths and only open paths")
	}
	if !l.Activate(a) || l.Active() != a {
		t.Fatal("Activate by identity should switch the active tab")
	}
}

// TestTabList_InsertPreviewReusesSlot pins the preview-slot rule: a new
// preview replaces the existing preview tab in place, so tab order —
// the user's spatial memory — survives browsing.
func TestTabList_InsertPreviewReusesSlot(t *testing.T) {
	var l TabList
	l.Append(memTab("a.go", false))
	p1 := memTab("p1.go", true)
	l.InsertPreview(p1)
	l.Append(memTab("z.go", false))

	p2 := memTab("p2.go", true)
	l.InsertPreview(p2)
	if l.Len() != 3 {
		t.Fatalf("preview replace should keep the count, got %d", l.Len())
	}
	if l.At(1) != p2 {
		t.Fatal("the new preview must take the old preview's slot")
	}
	if l.Active() != p2 || l.Preview() != p2 {
		t.Fatal("the new preview should be active and be the preview")
	}
	if l.IndexOf(p1) != -1 {
		t.Fatal("the replaced preview must leave the list")
	}
}

// TestTabList_InsertPreviewAppendsWithoutSlot pins the other half: with
// no preview open (or after the preview was pinned), a preview open
// appends like any other tab.
func TestTabList_InsertPreviewAppendsWithoutSlot(t *testing.T) {
	var l TabList
	l.Append(memTab("a.go", false))
	p := memTab("p.go", true)
	l.InsertPreview(p)
	if l.Len() != 2 || l.At(1) != p {
		t.Fatal("preview with no slot should append")
	}
	p.Pin()
	p2 := memTab("p2.go", true)
	l.InsertPreview(p2)
	if l.Len() != 3 || l.At(2) != p2 {
		t.Fatal("a pinned former preview must not be replaced")
	}
}

// TestTabList_RemoveKeepsActiveIdentity pins the fix the module exists
// for: closing a background tab must not change which file the user is
// looking at — the active TAB stays active even when its position
// shifts. (The old index-clamp bookkeeping switched files when a tab
// left of the active one closed.)
func TestTabList_RemoveKeepsActiveIdentity(t *testing.T) {
	var l TabList
	a, b, c := memTab("a.go", false), memTab("b.go", false), memTab("c.go", false)
	l.Append(a)
	l.Append(b)
	l.Append(c) // active: c at index 2
	if !l.Remove(a) {
		t.Fatal("remove should succeed")
	}
	if l.Active() != c || l.ActiveIndex() != 1 {
		t.Fatalf("active tab must survive a background close: active=%v idx=%d", l.Active(), l.ActiveIndex())
	}
}

// TestTabList_RemoveActiveActivatesNeighbour pins closing the active
// tab: the right neighbour takes over, and closing the last tab falls
// back to the new last one; the final close empties the list.
func TestTabList_RemoveActiveActivatesNeighbour(t *testing.T) {
	var l TabList
	a, b, c := memTab("a.go", false), memTab("b.go", false), memTab("c.go", false)
	l.Append(a)
	l.Append(b)
	l.Append(c)
	l.Activate(a)
	l.Remove(a)
	if l.Active() != b {
		t.Fatalf("closing the active tab should activate its right neighbour, got %v", l.Active())
	}
	l.Activate(c)
	l.Remove(c)
	if l.Active() != b {
		t.Fatalf("closing the last, active tab should activate the new last, got %v", l.Active())
	}
	l.Remove(b)
	if l.Len() != 0 || l.Active() != nil {
		t.Fatal("removing the final tab should empty the list")
	}
}

// TestTabList_ActivateAtClamps pins the tab-strip click contract: an
// index from a stale hit test lands on the nearest real tab instead of
// panicking or deactivating.
func TestTabList_ActivateAtClamps(t *testing.T) {
	var l TabList
	a, b := memTab("a.go", false), memTab("b.go", false)
	l.Append(a)
	l.Append(b)
	l.ActivateAt(99)
	if l.Active() != b {
		t.Fatal("past-the-end activates the last tab")
	}
	l.ActivateAt(-5)
	if l.Active() != a {
		t.Fatal("below-zero activates the first tab")
	}
}
