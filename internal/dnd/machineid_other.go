// =============================================================================
// File: internal/dnd/machineid_other.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

//go:build !darwin && !linux

package dnd

// rawMachineID returns /etc/machine-id where a BSD has one. On Windows
// the protocol's id is a registry value; skiff does not run the
// protocol route there at all (there is no tty filter to carry it), so
// the lookup simply fails and no id is sent.
func rawMachineID() (string, error) {
	return readMachineIDFile("/etc/machine-id", "/var/lib/dbus/machine-id")
}
