// =============================================================================
// File: internal/dnd/base64.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"bytes"
	"encoding/base64"
	"errors"
)

// Decoder decodes a base64 stream that arrives in pieces.
//
// The protocol chunks a payload after encoding it, at 4096 bytes, so
// most boundaries fall on a four-byte quantum — but the spec only
// promises that, not requires it of a terminal, and padding "may or may
// not be present". A stream is therefore decoded as one stream: the
// tail of a chunk that does not complete a quantum is carried into the
// next. Padding closes whatever block it ends, which also makes a
// terminal that encodes each piece separately (with padding) come out
// right. The zero value is ready to use.
type Decoder struct {
	carry []byte
}

// Decode returns the bytes chunk completes. Bytes that do not yet make
// up a quantum are held for the next call or for Close.
func (d *Decoder) Decode(chunk []byte) ([]byte, error) {
	buf := chunk
	if len(d.carry) > 0 {
		buf = append(d.carry, chunk...)
		d.carry = nil
	}
	var out []byte
	for len(buf) > 0 {
		pad := bytes.IndexByte(buf, '=')
		if pad < 0 {
			whole := len(buf) / 4 * 4
			piece, err := decodeRaw(buf[:whole])
			if err != nil {
				return nil, err
			}
			out = append(out, piece...)
			d.carry = append([]byte(nil), buf[whole:]...)
			break
		}
		piece, err := decodeRaw(buf[:pad])
		if err != nil {
			return nil, err
		}
		out = append(out, piece...)
		for pad < len(buf) && buf[pad] == '=' {
			pad++
		}
		buf = buf[pad:]
	}
	return out, nil
}

// Close returns the bytes of an unpadded final quantum. A single
// leftover character cannot encode anything and is an error: the
// stream was cut short.
func (d *Decoder) Close() ([]byte, error) {
	rest := d.carry
	d.carry = nil
	if len(rest) == 0 {
		return nil, nil
	}
	return decodeRaw(rest)
}

// decodeRaw decodes unpadded base64.
func decodeRaw(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b)%4 == 1 {
		return nil, errors.New("dnd: truncated base64 data")
	}
	out := make([]byte, base64.RawStdEncoding.DecodedLen(len(b)))
	n, err := base64.RawStdEncoding.Decode(out, b)
	if err != nil {
		return nil, errors.New("dnd: corrupt base64 data")
	}
	return out[:n], nil
}
