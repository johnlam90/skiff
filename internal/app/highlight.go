// =============================================================================
// File: internal/app/highlight.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-09-06
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// highlight.go is the app half of off-loop syntax highlighting: the
// draw pass asks the active tab whether its grid is a patched
// approximation (editor/hlpatch.go), runs the re-lex the tab hands back
// on the highlight job, and lands the result through the one asyncjob
// event case. The tab decides whether a landing still applies.

package app

import (
	"context"

	"github.com/johnlam90/skiff/internal/editor"
)

// highlightResult is one landed re-lex and the tab it was for.
type highlightResult struct {
	tab *editor.Tab
	res editor.HighlightResult
}

// requestHighlight starts a background re-lex for tab if its grid is
// pending one for a viewH-row viewport. The request copies the window's
// text on the loop, so the goroutine never touches the buffer.
func (a *App) requestHighlight(tab *editor.Tab, viewH int) {
	req, ok := tab.HighlightRequest(viewH)
	if !ok {
		return
	}
	th := a.theme
	a.highlight.Start(func(context.Context) (highlightResult, error) {
		return highlightResult{tab: tab, res: req.Run(th)}, nil
	})
}

// handleHighlightDone installs a landed re-lex on its tab. A tab closed
// while the lex ran is skipped; a stale generation is dropped by the
// tab itself.
func (a *App) handleHighlightDone(r highlightResult, err error) {
	if err != nil || r.tab == nil || a.tabs.IndexOf(r.tab) < 0 {
		return
	}
	r.tab.ApplyHighlight(r.res)
}
