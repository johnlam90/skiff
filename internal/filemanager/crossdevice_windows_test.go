// =============================================================================
// File: internal/filemanager/crossdevice_windows_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build windows

package filemanager

import (
	"os"
	"syscall"
	"testing"
)

// TestIsCrossDevice pins which rename failures may fall back to
// copy-then-delete on Windows: ERROR_NOT_SAME_DEVICE as MoveFileEx
// reports it, the POSIX EXDEV, and nothing else.
func TestIsCrossDevice(t *testing.T) {
	if !isCrossDevice(&os.LinkError{Op: "rename", Err: errorNotSameDevice}) {
		t.Error("ERROR_NOT_SAME_DEVICE is a cross-device failure")
	}
	if !isCrossDevice(&os.LinkError{Op: "rename", Err: syscall.EXDEV}) {
		t.Error("EXDEV is a cross-device failure")
	}
	if isCrossDevice(os.ErrPermission) {
		t.Error("a permission error is not a cross-device failure")
	}
}
