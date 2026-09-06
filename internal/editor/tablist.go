// =============================================================================
// File: internal/editor/tablist.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package editor

// TabList owns the ordered set of open tabs, the active tab, and the
// preview-slot rules. Its interface speaks tab identity (*Tab) — never
// list position and never path — so a reference held across a mutation
// (a dirty-close modal's callback, an async format result) can never
// act on the wrong tab. Indexes appear only as transient values for the
// tab strip's geometry; they must not be stored.
type TabList struct {
	tabs   []*Tab
	active int
}

// Len returns how many tabs are open.
func (l *TabList) Len() int { return len(l.tabs) }

// At returns the tab at position i, or nil when i is out of range —
// for the tab strip's row-by-row drawing only.
func (l *TabList) At(i int) *Tab {
	if i < 0 || i >= len(l.tabs) {
		return nil
	}
	return l.tabs[i]
}

// Tabs returns the ordered tabs for ranging. Callers must not mutate
// the returned slice — every mutation goes through this list.
func (l *TabList) Tabs() []*Tab { return l.tabs }

// Active returns the active tab, or nil when the list is empty.
func (l *TabList) Active() *Tab { return l.At(l.active) }

// ActiveIndex returns the active tab's position — transient, for the
// tab strip's scroll math.
func (l *TabList) ActiveIndex() int { return l.active }

// IndexOf returns t's position, or -1 when t is not in the list.
func (l *TabList) IndexOf(t *Tab) int {
	for i, cur := range l.tabs {
		if cur == t {
			return i
		}
	}
	return -1
}

// Lookup returns the open tab for path, or nil. Paths are unique in the
// list: the open path always reuses an existing tab.
func (l *TabList) Lookup(path string) *Tab {
	for _, cur := range l.tabs {
		if cur.Path == path {
			return cur
		}
	}
	return nil
}

// Activate makes t the active tab. Returns false when t is not in the
// list — a stale reference to a closed tab activates nothing.
func (l *TabList) Activate(t *Tab) bool {
	i := l.IndexOf(t)
	if i < 0 {
		return false
	}
	l.active = i
	return true
}

// ActivateAt activates the tab at position i, clamped into range —
// for tab strip clicks, where the index comes fresh from the hit test.
func (l *TabList) ActivateAt(i int) {
	if len(l.tabs) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(l.tabs) {
		i = len(l.tabs) - 1
	}
	l.active = i
}

// Append adds t at the end and activates it.
func (l *TabList) Append(t *Tab) {
	l.tabs = append(l.tabs, t)
	l.active = len(l.tabs) - 1
}

// InsertPreview places the preview tab t: reusing the existing preview
// tab's slot when there is one — tab order is part of the user's
// spatial memory, so browsing must not reshuffle it — and appending
// otherwise. Activates t either way.
func (l *TabList) InsertPreview(t *Tab) {
	for i, cur := range l.tabs {
		if cur.IsPreview() {
			l.tabs[i] = t
			l.active = i
			return
		}
	}
	l.Append(t)
}

// Preview returns the current preview tab, or nil. There is at most
// one: every preview open either replaces or pins it.
func (l *TabList) Preview() *Tab {
	for _, cur := range l.tabs {
		if cur.IsPreview() {
			return cur
		}
	}
	return nil
}

// Remove takes t out of the list by identity. Removing a background
// tab keeps the current active tab active; removing the active tab
// activates its right neighbour (or the new last tab). Returns false
// when t is not in the list — a stale reference removes nothing.
func (l *TabList) Remove(t *Tab) bool {
	i := l.IndexOf(t)
	if i < 0 {
		return false
	}
	l.tabs = append(l.tabs[:i], l.tabs[i+1:]...)
	switch {
	case i < l.active:
		// A tab left of the active one left — follow the active tab to
		// its new position so the user's file doesn't change under them.
		l.active--
	case l.active >= len(l.tabs):
		l.active = len(l.tabs) - 1
	}
	if l.active < 0 {
		l.active = 0
	}
	return true
}

// Next activates the tab to the right of the active one, wrapping from
// the last tab to the first, and returns it — nil on an empty list. The
// switch goes through Activate so the identity rule holds: the caller
// gets the tab, never a position to store.
func (l *TabList) Next() *Tab {
	return l.step(1)
}

// Prev activates the tab to the left of the active one, wrapping from
// the first tab to the last, and returns it — nil on an empty list.
func (l *TabList) Prev() *Tab {
	return l.step(-1)
}

// step is the shared half of Next / Prev: the wrapped neighbour at
// delta from the active index, activated by identity.
func (l *TabList) step(delta int) *Tab {
	n := len(l.tabs)
	if n == 0 {
		return nil
	}
	t := l.tabs[((l.active+delta)%n+n)%n]
	l.Activate(t)
	return t
}
