// =============================================================================
// File: internal/dnd/machineid_darwin.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// ioregTimeout bounds the one process this package starts. ioreg
// answers in milliseconds; the bound only matters on a machine where
// it hangs, and there a missing machine id costs nothing but speed.
const ioregTimeout = 2 * time.Second

// rawMachineID returns the IOPlatformUUID, which is what the protocol
// names as the machine id on macOS. There is no syscall for it short of
// IOKit, and skiff ships without CGO, so it is read from ioreg — the
// one place this package shells out. (sysctl kern.uuid looks like the
// same thing and is not: it is a different UUID.)
func rawMachineID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	id, ok := parseIOPlatformUUID(out)
	if !ok {
		return "", errors.New("dnd: ioreg reported no IOPlatformUUID")
	}
	return id, nil
}
