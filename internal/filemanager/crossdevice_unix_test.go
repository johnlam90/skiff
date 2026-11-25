// =============================================================================
// File: internal/filemanager/crossdevice_unix_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !windows

package filemanager

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// TestIsCrossDevice pins which rename failures may fall back to
// copy-then-delete: EXDEV in the shape os.Rename returns it, and
// nothing else — a permission error must stay a refused move.
func TestIsCrossDevice(t *testing.T) {
	if !isCrossDevice(&os.LinkError{Op: "rename", Err: syscall.EXDEV}) {
		t.Error("a wrapped EXDEV is a cross-device failure")
	}
	for _, err := range []error{os.ErrPermission, os.ErrNotExist, errors.New("other"), nil} {
		if isCrossDevice(err) {
			t.Errorf("%v is not a cross-device failure", err)
		}
	}
}
