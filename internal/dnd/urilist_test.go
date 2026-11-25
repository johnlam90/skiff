// =============================================================================
// File: internal/dnd/urilist_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package dnd

import (
	"reflect"
	"testing"
)

// TestParseURIList pins the list's shape. Comments and blank lines are
// not entries, but a non-file URI IS one: the terminal indexes every
// URI line, so skipping the https line here would shift every later
// request onto the wrong file.
func TestParseURIList(t *testing.T) {
	data := "# from Files\r\n" +
		"file:///a/b%20c.txt\r\n" +
		"https://example.com/y\r\n" +
		"\r\n" +
		"file://localhost/d/\r\n" +
		"file://otherhost/e.txt\n"
	got := ParseURIList([]byte(data))
	want := []Entry{
		{URI: "file:///a/b%20c.txt", Path: "/a/b c.txt", File: true, Local: true},
		{URI: "https://example.com/y"},
		{URI: "file://localhost/d/", Path: "/d/", File: true, Local: true},
		{URI: "file://otherhost/e.txt", Path: "/e.txt", File: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseURIList =\n %+v\nwant\n %+v", got, want)
	}
	if got := ParseURIList(nil); got != nil {
		t.Errorf("ParseURIList(nil) = %+v, want nil", got)
	}
}

// TestEntryName pins the name a dropped entry lands under: the last
// component of its path, with a folder's trailing slash ignored.
func TestEntryName(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"/a/b c.txt", "b c.txt"},
		{"/d/", "d"},
		{"/a/.hidden", ".hidden"},
	}
	for _, c := range cases {
		got, err := EntryName(c.path)
		if err != nil || got != c.want {
			t.Errorf("EntryName(%q) = %q, %v; want %q", c.path, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "/", "/a/..", "/a/."} {
		if got, err := EntryName(bad); err == nil {
			t.Errorf("EntryName(%q) = %q, want an error", bad, got)
		}
	}
}

// TestValidName is the path-traversal fence: a name from the terminal
// is joined onto a directory this client is writing into, so anything
// that could climb out of it or name a parent has to be refused.
func TestValidName(t *testing.T) {
	for _, ok := range []string{"a.txt", ".env", "with space", "ünïcode", "a..b"} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "/abs", "../up", "nul\x00byte"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true, want false", bad)
		}
	}
}
