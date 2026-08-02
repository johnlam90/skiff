// =============================================================================
// File: internal/app/listpick_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Tests for the generic pick-one-of-N modal in listpick.go. The picker's
// hooks (OnPick / OnMove / OnCancel) are exercised end-to-end by the
// theme-picker tests; here we pin the modal's own input behaviour.

package app

import (
	"strings"
	"testing"
)

// TestDrawListPick_ScrollsFilterToCaret pins the filter input's scroll
// window: a query longer than the field used to draw from index 0 with
// the caret placed past the field edge — typing appeared dead. The
// window must keep the caret inside the visible field.
func TestDrawListPick_ScrollsFilterToCaret(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	items := []listPickItem{{Label: "one"}, {Label: "two"}}
	a.openListPick("Theme", items, nil, nil, nil)
	a.listPickQuery = []rune(strings.Repeat("q", 60))
	a.listPickCursor = len(a.listPickQuery)

	a.drawListPick()
	if a.listPickInputScroll == 0 {
		t.Fatal("filter input did not scroll; caret sits off-field")
	}
	mx, _, mw, _ := a.listPickRect()
	fieldStart, fieldEnd := mx+3, mx+mw-3
	caret := fieldStart + (a.listPickCursor - a.listPickInputScroll)
	if caret < fieldStart || caret > fieldEnd {
		t.Fatalf("caret column %d outside the field [%d, %d]", caret, fieldStart, fieldEnd)
	}

	a.listPickCursor = 0
	a.drawListPick()
	if a.listPickInputScroll != 0 {
		t.Fatalf("scroll should follow the caret home, got %d", a.listPickInputScroll)
	}
}
