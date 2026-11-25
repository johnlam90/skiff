// =============================================================================
// File: internal/git/detach_unix_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !windows

package git

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestCommand_RunsGitWithoutAControllingTerminal pins the detach. Git's
// children — ssh above all — open /dev/tty directly to ask for a
// passphrase or a host-key answer, and GIT_TERMINAL_PROMPT does not
// stop them. Inside a TUI that prompt is drawn over the screen and
// reads the user's keystrokes out from under the editor. In its own
// session git has no controlling terminal, so the open fails and the
// operation fails fast with a message the editor can show.
func TestCommand_RunsGitWithoutAControllingTerminal(t *testing.T) {
	cmd := command(context.Background(), t.TempDir(), []string{"--version"})
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("git must be started in its own session")
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	// What a child of git sees: an alias run through the shell tries to
	// open the controlling terminal the way ssh does.
	probe := "alias.ttyprobe=!sh -c 'if (exec 3</dev/tty) 2>/dev/null; then echo has-tty; else echo no-tty; fi'"
	out, err := command(context.Background(), t.TempDir(), []string{"-c", probe, "ttyprobe"}).Output()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "no-tty" {
		t.Fatalf("a child of git could open /dev/tty (%q)", got)
	}
}
