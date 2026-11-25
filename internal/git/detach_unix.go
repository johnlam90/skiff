// =============================================================================
// File: internal/git/detach_unix.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !windows

// detach_unix.go takes the controlling terminal away from git and its
// children. The Windows half (detach_windows.go) is a no-op: there is
// no /dev/tty there to guard.

package git

import (
	"os/exec"
	"syscall"
)

// detachCmd starts cmd in a new session, which has no controlling
// terminal. GIT_TERMINAL_PROMPT=0 silences git's own prompts but not
// its children's: ssh opens /dev/tty itself for a key passphrase or an
// unknown host key, painting over the editor and taking its keystrokes
// until the write timed out minutes later. With no terminal to open,
// ssh fails at once and the failure reaches the user as an OpError.
//
// An askpass program is deliberately left alone. With no terminal it is
// the one way left to ask for a secret, and a desktop user's graphical
// helper (SSH_ASKPASS, GIT_ASKPASS) draws its own window — it cannot
// touch the TUI, so disabling it would only break a push that works.
func detachCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
