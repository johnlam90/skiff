// =============================================================================
// File: internal/dnd/base64_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// decodeAll runs chunks through one Decoder and returns everything it
// produced, Close included.
func decodeAll(t *testing.T, chunks ...string) []byte {
	t.Helper()
	var d Decoder
	var out []byte
	for _, c := range chunks {
		piece, err := d.Decode([]byte(c))
		if err != nil {
			t.Fatalf("Decode(%q): %v", c, err)
		}
		out = append(out, piece...)
	}
	tail, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return append(out, tail...)
}

// TestDecoder_SplitAnywhere pins the streaming property: the terminal
// chunks the ENCODED stream, so a boundary can fall inside a four-byte
// quantum. Every split point must decode to the same bytes — decoding
// each chunk on its own would corrupt the file at every odd boundary.
func TestDecoder_SplitAnywhere(t *testing.T) {
	const encoded = "aGVsbG8gd29ybGQ=" // "hello world"
	for i := 0; i <= len(encoded); i++ {
		got := decodeAll(t, encoded[:i], encoded[i:])
		if string(got) != "hello world" {
			t.Errorf("split at %d: decoded %q, want %q", i, got, "hello world")
		}
	}
}

// TestDecoder_UnpaddedTail pins the spec's "padding is optional": a
// stream that ends mid-quantum without "=" is complete, and its last
// bytes arrive from Close.
func TestDecoder_UnpaddedTail(t *testing.T) {
	if got := decodeAll(t, "aGVsbG8gd29ybGQ"); string(got) != "hello world" {
		t.Errorf("decoded %q, want %q", got, "hello world")
	}
}

// TestDecoder_PaddingEndsABlock pins tolerance for a terminal that
// encodes each piece on its own: padding in mid-stream closes that
// block instead of poisoning the rest.
func TestDecoder_PaddingEndsABlock(t *testing.T) {
	if got := decodeAll(t, "aGk=", "eW8="); string(got) != "hiyo" {
		t.Errorf("decoded %q, want %q", got, "hiyo")
	}
	if got := decodeAll(t, "aGk=eW8="); string(got) != "hiyo" {
		t.Errorf("one chunk, two blocks: decoded %q, want %q", got, "hiyo")
	}
}

// TestDecoder_LargePayloadInProtocolChunks pins the real shape: binary
// data, encoded once, cut into the protocol's 4096-byte pieces.
func TestDecoder_LargePayloadInProtocolChunks(t *testing.T) {
	data := make([]byte, 100_003)
	for i := range data {
		data[i] = byte(i * 31)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	var chunks []string
	for len(encoded) > 4096 {
		chunks = append(chunks, encoded[:4096])
		encoded = encoded[4096:]
	}
	chunks = append(chunks, encoded)
	if got := decodeAll(t, chunks...); !bytes.Equal(got, data) {
		t.Errorf("decoded %d bytes that differ from the %d sent", len(got), len(data))
	}
}

// TestDecoder_RejectsGarbage pins that corrupt input is an error, not
// silently skipped bytes: a file with a hole in it is worse than a
// failed drop.
func TestDecoder_RejectsGarbage(t *testing.T) {
	var d Decoder
	if _, err := d.Decode([]byte("!!!!")); err == nil {
		t.Error("Decode of non-base64 input: want an error")
	}
	var short Decoder
	if _, err := short.Decode([]byte("a")); err != nil {
		t.Fatalf("Decode of a partial quantum: %v", err)
	}
	if _, err := short.Close(); err == nil {
		t.Error("Close with one dangling character: want an error")
	}
}
