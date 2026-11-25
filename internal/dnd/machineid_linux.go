// =============================================================================
// File: internal/dnd/machineid_linux.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

// rawMachineID returns the contents of /etc/machine-id, the protocol's
// machine id everywhere but macOS and Windows. The D-Bus copy is the
// same value on systems that predate systemd's file.
func rawMachineID() (string, error) {
	return readMachineIDFile("/etc/machine-id", "/var/lib/dbus/machine-id")
}
