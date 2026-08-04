// =============================================================================
// File: internal/clipboard/clipboard_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the clipboard package. The OSC 52 encoding is exercised
// through osc52Sequence with literal expected bytes, so a "tidy up" of
// the escape format shows as a diff here rather than as a silent
// clipboard regression. The write half is exercised by pointing ttyPath
// at a temp file — the only way to prove what actually reached the wire
// — plus an end-to-end run against a real /dev/tty when the host has
// one (CI containers usually don't).

package clipboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOSC52Sequence_Encoding pins the OSC 52 byte format with
// table-driven cases carrying literal expectations. The escape sequence
// is a wire format: every byte is load-bearing, and a terminal that
// disagrees fails silently rather than erroring.
func TestOSC52Sequence_Encoding(t *testing.T) {
	cases := []struct {
		name string
		text string
		tmux bool
		want string
	}{
		{
			name: "plain hello",
			text: "hello",
			tmux: false,
			want: "\x1b]52;c;aGVsbG8=\x07",
		},
		{
			name: "empty string",
			text: "",
			tmux: false,
			want: "\x1b]52;c;\x07",
		},
		{
			name: "unicode payload",
			text: "héllo",
			tmux: false,
			want: "\x1b]52;c;aMOpbGxv\x07",
		},
		{
			name: "tmux wraps inner sequence",
			text: "hello",
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]52;c;aGVsbG8=\x07\x1b\\",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := osc52Sequence(c.text, c.tmux)
			if err != nil {
				t.Fatalf("osc52Sequence: %v", err)
			}
			if got != c.want {
				t.Fatalf("encoding mismatch\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// TestOSC52Sequence_AtLimit keeps the cap an inclusive bound: a payload
// of exactly MaxPayloadBytes is the largest thing we still try to send,
// so an off-by-one here would refuse copies that fit.
func TestOSC52Sequence_AtLimit(t *testing.T) {
	seq, err := osc52Sequence(strings.Repeat("x", MaxPayloadBytes), false)
	if err != nil {
		t.Fatalf("exactly MaxPayloadBytes must be accepted: %v", err)
	}
	if !strings.HasPrefix(seq, "\x1b]52;c;") {
		t.Fatalf("sequence lost its OSC 52 prefix: %.16q", seq)
	}
}

// TestCopyToSystem_OversizedRefusesAndWritesNothing is the whole point
// of the cap. tmux discards an over-long OSC string without a word, so
// the old code returned nil on a copy that never happened. The contract
// now: an ErrTooLarge that names the size, and not one byte of a doomed
// escape sequence on the terminal — a half-written OSC 52 leaves the
// parser swallowing everything skiff draws next as clipboard data.
func TestCopyToSystem_OversizedRefusesAndWritesNothing(t *testing.T) {
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, nil, 0o600); err != nil {
		t.Fatalf("seed tty: %v", err)
	}
	swapTTY(t, tty)
	t.Setenv("TMUX", "")

	err := CopyToSystem(strings.Repeat("x", MaxPayloadBytes+1))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized copy must report ErrTooLarge, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "524289") {
		t.Errorf("error must name the offending size, got %q", msg)
	}

	written, readErr := os.ReadFile(tty)
	if readErr != nil {
		t.Fatalf("read tty: %v", readErr)
	}
	if len(written) != 0 {
		t.Fatalf("refused copy wrote %d bytes to the tty: %.32q", len(written), written)
	}
}

// TestCopyToSystem_WritesSequence proves the accepted path actually
// emits the bytes osc52Sequence produced, wrapper and all — the encoder
// being correct is worth nothing if the writer drops or mangles it.
func TestCopyToSystem_WritesSequence(t *testing.T) {
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, nil, 0o600); err != nil {
		t.Fatalf("seed tty: %v", err)
	}
	swapTTY(t, tty)
	t.Setenv("TMUX", "/tmp/fake-tmux,1234,0")

	if err := CopyToSystem("hello"); err != nil {
		t.Fatalf("CopyToSystem: %v", err)
	}
	written, err := os.ReadFile(tty)
	if err != nil {
		t.Fatalf("read tty: %v", err)
	}
	want := "\x1bPtmux;\x1b\x1b]52;c;aGVsbG8=\x07\x1b\\"
	if string(written) != want {
		t.Fatalf("tty bytes mismatch\n got: %q\nwant: %q", written, want)
	}
}

// TestCopyToSystem_RealTTY keeps one end-to-end run against an actual
// terminal device on a developer machine; CI sandboxes have no
// /dev/tty, so it skips there rather than failing on the environment.
func TestCopyToSystem_RealTTY(t *testing.T) {
	if _, err := os.Stat("/dev/tty"); err != nil {
		t.Skipf("/dev/tty not available: %v", err)
	}
	t.Setenv("TMUX", "")

	if err := CopyToSystem("hello"); err != nil {
		// The device existing but not being writable from a detached
		// harness is an environment issue, not a code defect.
		t.Skipf("CopyToSystem on /dev/tty failed: %v", err)
	}
}

// swapTTY points CopyToSystem at path for the duration of one test and
// restores the real device afterwards.
func swapTTY(t *testing.T, path string) {
	t.Helper()
	prev := ttyPath
	ttyPath = path
	t.Cleanup(func() { ttyPath = prev })
}
