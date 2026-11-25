// =============================================================================
// File: internal/git/detach_windows_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build windows

package git

import (
	"context"
	"testing"
)

// TestCommand_DetachIsANoOpOnWindows pins that the Windows half leaves
// the command as command() built it — nothing to detach from.
func TestCommand_DetachIsANoOpOnWindows(t *testing.T) {
	if cmd := command(context.Background(), t.TempDir(), []string{"--version"}); cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %+v, want nil", cmd.SysProcAttr)
	}
}
