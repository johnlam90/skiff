// =============================================================================
// File: internal/dnd/urilist.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// Entry is one line of a dropped text/uri-list (RFC 2483).
type Entry struct {
	// URI is the line as sent.
	URI string
	// Path is the decoded path of a file URI; empty otherwise. For a
	// URI naming another host it is still set — it is where the name
	// comes from — but it is not a path on this machine.
	Path string
	// File reports a file:// URI, the only kind whose contents the
	// terminal will send.
	File bool
	// Local reports a file URI with no host or "localhost": its Path
	// may be readable here.
	Local bool
}

// ParseURIList splits a text/uri-list into its entries. Blank lines and
// "#" comments are not entries, but every URI line is one whether or
// not it is a file: the terminal numbers the URI lines, and the
// requests this client sends carry those numbers.
func ParseURIList(data []byte) []Entry {
	var out []Entry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e := Entry{URI: line}
		if u, err := url.Parse(line); err == nil && u.Scheme == "file" && u.Path != "" {
			e.File = true
			e.Path = u.Path
			e.Local = u.Host == "" || u.Host == "localhost"
		}
		out = append(out, e)
	}
	return out
}

// EntryName returns the name a dropped entry lands under: the last
// component of its URI path, a folder's trailing slash ignored. URI
// paths are slash-separated whatever the source OS, so this is path,
// not filepath.
func EntryName(p string) (string, error) {
	name := path.Base(strings.TrimRight(p, "/"))
	if !ValidName(name) {
		return "", errors.New("dnd: the dropped entry has no usable name")
	}
	return name, nil
}

// ValidName reports whether name is safe to join onto a directory this
// client is writing into: one path component, and not one that means
// the directory itself or its parent. Every name the terminal sends —
// a URI's last component, each line of a directory listing — goes
// through here before it touches the disk, because a listing entry of
// "../../.ssh/authorized_keys" is otherwise a write outside staging.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\x00") || strings.ContainsRune(name, filepath.Separator) {
		return false
	}
	return filepath.IsLocal(name)
}
