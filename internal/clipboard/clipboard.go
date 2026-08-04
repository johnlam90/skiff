// =============================================================================
// File: internal/clipboard/clipboard.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package clipboard pushes text onto the host system clipboard via the
// OSC 52 terminal escape sequence. OSC 52 is the right primitive for an
// SSH-first editor: it travels over the SSH channel, tmux forwards it
// (with set-clipboard on, the default since tmux 3.2), and every modern
// terminal we care about — iTerm2, WezTerm, Kitty, Alacritty, Ghostty,
// gnome-terminal, the macOS default — honors it.
//
// We deliberately do not try to read the system clipboard from a TUI:
// most terminals refuse to expose it for security reasons. Paste from
// outside the editor instead arrives via the user's terminal paste
// (Cmd-V / right-click), which delivers the text as keypresses; we
// handle those in the normal key-input path.
package clipboard

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// MaxPayloadBytes caps the text a single OSC 52 write will carry.
//
// The sequence has to survive every hop between skiff and the user's
// terminal, and the tightest hop we can actually name is tmux: input.c
// grows its OSC string buffer up to INPUT_BUF_DEFAULT_SIZE (1048576,
// tmux.h) and then silently discards the rest of the string — no
// error, no partial paste, just nothing on the clipboard. base64
// inflates text by 4/3, so tmux's 1 MiB of *sequence* is only ~768 KiB
// of text before the passthrough wrapper bytes. 512 KiB leaves real
// headroom under that ceiling while staying far larger than any
// selection a person makes on purpose.
//
// Terminals are stricter still and none of them advertise their limit
// (st capped OSC 52 at 382 bytes until 2019), so this is not a promise
// the copy lands — it is the one bound we can name. Below it we may
// still be truncated by a stingy terminal; above it we are guaranteed
// to be, and returning nil there is a lie.
const MaxPayloadBytes = 512 << 10

// ErrTooLarge reports a selection the terminal clipboard cannot carry.
// It exists so the app can say "too large" instead of the generic
// "system clipboard unavailable" — the two failures want different
// copy, and only this one is the user's to fix.
var ErrTooLarge = errors.New("payload too large for the terminal clipboard")

// ttyPath is the device CopyToSystem writes to. A var only so tests can
// point it at a temp file and assert what actually reached the wire;
// production never reassigns it.
var ttyPath = "/dev/tty"

// CopyToSystem pushes text onto the host system clipboard via OSC 52.
//
// We open /dev/tty directly rather than writing to stdout so we don't
// race tcell's screen rendering. The size check runs before the open so
// an oversized payload never puts a single byte of a doomed escape
// sequence on the terminal — a half-written OSC 52 leaves the parser
// eating subsequent output as clipboard data.
func CopyToSystem(text string) error {
	seq, err := osc52Sequence(text, os.Getenv("TMUX") != "")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(ttyPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(seq)
	return err
}

// osc52Sequence renders text as the escape sequence CopyToSystem writes,
// or ErrTooLarge when the terminal could not carry it.
//
// tmux is deliberately the ONLY multiplexer that gets a wrapper. herdr
// (HERDR_ENV=1) intercepts the pane's plain OSC 52 and bridges it to
// the host clipboard itself — wrapping there would break copy, so don't
// add a HERDR_ENV branch. See docs/research/2026-08-02-herdr-compatibility.md.
func osc52Sequence(text string, tmux bool) (string, error) {
	if len(text) > MaxPayloadBytes {
		return "", fmt.Errorf("%w: %d bytes exceeds the %d byte limit", ErrTooLarge, len(text), MaxPayloadBytes)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	if tmux {
		// tmux passthrough: wrap inner escape sequences so tmux forwards
		// them verbatim to the host terminal.
		seq = fmt.Sprintf("\x1bPtmux;\x1b%s\x1b\\", seq)
	}
	return seq, nil
}
