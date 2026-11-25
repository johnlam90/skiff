// =============================================================================
// File: internal/dnd/machineid.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// machineIDKey is the HMAC key the protocol fixes. Hashing keeps the
// real machine id off the wire while still letting the terminal compare
// "is the program on my machine?".
const machineIDKey = "tty-dnd-protocol-machine-id"

// MachineID returns this machine's id in the protocol's form. The
// terminal compares it with its own to tell a local drop — hand over
// paths — from a remote one, where it has to send the file contents.
//
// A caller that gets an error should send RandomMachineID instead of
// sending nothing — see there for what "nothing" costs.
func MachineID() (string, error) {
	raw, err := rawMachineID()
	if err != nil {
		return "", err
	}
	return HashMachineID(raw), nil
}

// RandomMachineID returns an id in the protocol's form that matches no
// machine: the stand-in for a system that has none (a minimal
// container, an Alpine box without /etc/machine-id).
//
// It exists because saying nothing is the dangerous answer. With no id
// from the program, a terminal treats it as local and hands over bare
// paths — and a dropped /home/me/notes.txt that also exists on the
// remote machine is then copied from the remote, the wrong file, with
// no error anywhere. An id that cannot match makes the terminal send
// the bytes. The cost is that a genuinely local drop on such a system
// is transferred in-band instead of read from disk.
func RandomMachineID() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// No entropy source: any value that is not this machine's real
		// id serves, and the clock is one.
		return HashMachineID("no-machine-id-" + strconv.FormatInt(time.Now().UnixNano(), 16))
	}
	return "1:" + hex.EncodeToString(raw)
}

// HashMachineID turns a platform machine id into the value sent on the
// wire: version 1, then the HMAC-SHA256 of the id in hex.
func HashMachineID(raw string) string {
	mac := hmac.New(sha256.New, []byte(machineIDKey))
	mac.Write([]byte(raw))
	return "1:" + hex.EncodeToString(mac.Sum(nil))
}

// parseIOPlatformUUID picks the IOPlatformUUID value out of `ioreg`
// output, where it appears as  "IOPlatformUUID" = "…".  It lives here
// rather than in the darwin file so it is tested on every platform CI
// runs.
func parseIOPlatformUUID(out []byte) (string, bool) {
	const key = `"IOPlatformUUID"`
	i := bytes.Index(out, []byte(key))
	if i < 0 {
		return "", false
	}
	rest := out[i+len(key):]
	open := bytes.IndexByte(rest, '"')
	if open < 0 {
		return "", false
	}
	rest = rest[open+1:]
	end := bytes.IndexByte(rest, '"')
	if end <= 0 {
		return "", false
	}
	return string(rest[:end]), true
}

// readMachineIDFile returns the contents of the first of paths that
// exists and is not blank, trailing whitespace removed as the spec
// requires.
func readMachineIDFile(paths ...string) (string, error) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}
	return "", errors.New("dnd: this machine has no machine id")
}
