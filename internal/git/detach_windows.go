// =============================================================================
// File: internal/git/detach_windows.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build windows

package git

import "os/exec"

// detachCmd is a no-op on Windows: there is no controlling terminal for
// a child of git to open behind the editor's back. See detach_unix.go.
func detachCmd(*exec.Cmd) {}
