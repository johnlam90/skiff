// =============================================================================
// File: internal/filemanager/crossdevice_unix.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !windows

// crossdevice_unix.go answers "did this rename fail because it crossed
// a filesystem?" on every platform but Windows, where the answer has a
// different errno (see crossdevice_windows.go).

package filemanager

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether err is rename(2)'s EXDEV — the one
// rename failure a copy-then-delete can stand in for.
func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
