// =============================================================================
// File: internal/filemanager/crossdevice_windows.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build windows

// crossdevice_windows.go is the Windows half of the cross-device test:
// MoveFileEx reports a rename across volumes as ERROR_NOT_SAME_DEVICE,
// which Go does not translate to EXDEV.

package filemanager

import (
	"errors"
	"syscall"
)

// errorNotSameDevice is Windows' ERROR_NOT_SAME_DEVICE ("The system
// cannot move the file to a different disk drive").
const errorNotSameDevice = syscall.Errno(17)

// isCrossDevice reports whether err is a rename refused for crossing
// volumes — the one rename failure a copy-then-delete can stand in for.
// EXDEV is accepted too, so a caller that reports the POSIX errno is
// read the same way on every platform.
func isCrossDevice(err error) bool {
	return errors.Is(err, errorNotSameDevice) || errors.Is(err, syscall.EXDEV)
}
