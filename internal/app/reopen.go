// =============================================================================
// File: internal/app/reopen.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// reopen.go implements the reopen-closed-tab stack. closeTab records
// where the user was; menuReopenTab (≡ menu / Esc-o) walks the records
// newest-first. Entries whose file has since vanished are consumed
// silently-ish (a flash) instead of resurrecting ghost buffers.

package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnlam90/skiff/internal/editor"
)

// maxClosedTabs caps the reopen stack. Twenty is more "whoops, undo
// that close" depth than anyone uses while staying too small to matter
// memory-wise.
const maxClosedTabs = 20

// closedTabRecord is one closed tab's comeback ticket: the path plus
// the view state needed to put the user back where they were.
type closedTabRecord struct {
	Path    string
	Cursor  editor.Position
	ScrollY int
}

// recordClosedTab pushes tab onto the reopen stack (newest last),
// dropping the oldest record past the cap. Untitled tabs have no path
// to come back to, so they're skipped.
func (a *App) recordClosedTab(tab *editor.Tab) {
	if tab == nil || tab.Path == "" {
		return
	}
	a.closedTabs = append(a.closedTabs, closedTabRecord{
		Path:    tab.Path,
		Cursor:  tab.Cursor,
		ScrollY: tab.ScrollY,
	})
	if len(a.closedTabs) > maxClosedTabs {
		a.closedTabs = a.closedTabs[len(a.closedTabs)-maxClosedTabs:]
	}
}

// hasClosedTab gates the menu row: anything on the stack means the
// action can do something.
func (a *App) hasClosedTab() bool { return len(a.closedTabs) > 0 }

// menuReopenTab pops the most recent record and reopens it, restoring
// cursor and scroll. A record whose file has been deleted since is
// consumed with a flash — popping again on the next invocation keeps
// the gesture useful instead of jamming on the dead entry.
func (a *App) menuReopenTab() {
	a.closeMenu()
	for len(a.closedTabs) > 0 {
		rec := a.closedTabs[len(a.closedTabs)-1]
		a.closedTabs = a.closedTabs[:len(a.closedTabs)-1]
		if _, err := os.Stat(rec.Path); err != nil {
			a.flash(fmt.Sprintf("%s is gone — skipped", filepath.Base(rec.Path)))
			continue
		}
		a.openFile(rec.Path)
		if tab := a.activeTabPtr(); tab != nil && tab.Path == rec.Path && !tab.IsImage() {
			tab.Cursor = tab.Buffer.Clamp(rec.Cursor)
			tab.Anchor = tab.Cursor
			tab.ScrollY = rec.ScrollY
		}
		return
	}
}
