// =============================================================================
// File: internal/dnd/machineid_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestHashMachineID pins the hash against a vector computed outside
// this code:
//
//	printf %s test-machine | openssl dgst -sha256 -hmac tty-dnd-protocol-machine-id
//
// The terminal computes the same value for its own machine and compares
// the two, so a different key, digest or prefix makes every local drop
// look remote.
func TestHashMachineID(t *testing.T) {
	const want = "1:e28887abefdd91d6054c8996f3f0489747b275806b9f3c1b385ffbf834bc0ad8"
	if got := HashMachineID("test-machine"); got != want {
		t.Errorf("HashMachineID = %q\nwant            %q", got, want)
	}
}

// TestParseIOPlatformUUID pins the one line read out of ioreg's
// output, among the neighbours that look like it.
func TestParseIOPlatformUUID(t *testing.T) {
	out := `+-o Root  <class IORegistryEntry>
    {
      "IOPlatformSerialNumber" = "C02XXXXXXXXX"
      "IOPlatformUUID" = "0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9"
      "manufacturer" = <"Apple Inc.">
    }
`
	got, ok := parseIOPlatformUUID([]byte(out))
	if !ok || got != "0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9" {
		t.Errorf("parseIOPlatformUUID = %q, %v; want the UUID", got, ok)
	}
	if got, ok := parseIOPlatformUUID([]byte("no such key here")); ok {
		t.Errorf("parseIOPlatformUUID without the key = %q, want not found", got)
	}
}

// TestReadMachineIDFile pins the Linux source: the first file that
// exists wins, and trailing whitespace is not part of the id — the
// spec says so, and the newline would change the hash.
func TestReadMachineIDFile(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "machine-id")
	fallback := filepath.Join(dir, "dbus-machine-id")
	if err := os.WriteFile(fallback, []byte("fallback-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readMachineIDFile(primary, fallback); err != nil || got != "fallback-id" {
		t.Errorf("with only the fallback: %q, %v; want fallback-id", got, err)
	}
	if err := os.WriteFile(primary, []byte("primary-id \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readMachineIDFile(primary, fallback); err != nil || got != "primary-id" {
		t.Errorf("with both: %q, %v; want primary-id", got, err)
	}
	if _, err := readMachineIDFile(filepath.Join(dir, "none")); err == nil {
		t.Error("with no file: want an error")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMachineIDFile(empty); err == nil {
		t.Error("with a blank file: want an error")
	}
}

// TestMachineID_Shape runs the real platform lookup. A machine with no
// id (a minimal container) is a skip, not a failure; where one exists
// it must be the versioned 64-hex-digit form the protocol specifies.
func TestMachineID_Shape(t *testing.T) {
	id, err := MachineID()
	if err != nil {
		t.Skip("no machine id on this system:", err)
	}
	if !regexp.MustCompile(`^1:[0-9a-f]{64}$`).MatchString(id) {
		t.Errorf("MachineID = %q, want 1: followed by 64 hex digits", id)
	}
}

// TestRandomMachineID pins the stand-in for a machine with no id: the
// same shape as a real one, so the terminal parses it, and different
// every time, so it can never equal the terminal's own.
func TestRandomMachineID(t *testing.T) {
	a, b := RandomMachineID(), RandomMachineID()
	shape := regexp.MustCompile(`^1:[0-9a-f]{64}$`)
	if !shape.MatchString(a) || !shape.MatchString(b) {
		t.Errorf("RandomMachineID = %q, %q; want 1: followed by 64 hex digits", a, b)
	}
	if a == b {
		t.Errorf("two random ids are equal: %q", a)
	}
}
