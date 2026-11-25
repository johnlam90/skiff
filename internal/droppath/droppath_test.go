// =============================================================================
// File: internal/droppath/droppath_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-11-24
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package droppath

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

// statFor builds a Stat over a fixed set of existing paths. Anything
// else is "no such file", so a test states its whole filesystem in one
// line and never touches the real disk.
func statFor(exists ...string) func(string) error {
	set := make(map[string]bool, len(exists))
	for _, p := range exists {
		set[p] = true
	}
	return func(p string) error {
		if set[p] {
			return nil
		}
		return fs.ErrNotExist
	}
}

// TestClassify_PathForms pins every spelling a terminal uses for a
// dropped path: each one must come back as the plain absolute path the
// file actually has, because that is what the copy opens.
func TestClassify_PathForms(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		exists []string
		want   []string
	}{
		{"backslash-escaped space", `/Users/me/My\ File.txt`, []string{"/Users/me/My File.txt"}, []string{"/Users/me/My File.txt"}},
		{"backslash-escaped parens", `/tmp/shot\ \(1\).png`, []string{"/tmp/shot (1).png"}, []string{"/tmp/shot (1).png"}},
		{"single-quoted", `'/tmp/a b.txt'`, []string{"/tmp/a b.txt"}, []string{"/tmp/a b.txt"}},
		{"single-quoted with an apostrophe", `'/tmp/it'\''s.png'`, []string{"/tmp/it's.png"}, []string{"/tmp/it's.png"}},
		{"double-quoted", `"/tmp/a b.txt"`, []string{"/tmp/a b.txt"}, []string{"/tmp/a b.txt"}},
		{"double-quoted with an escaped quote", `"/tmp/say \"hi\".txt"`, []string{`/tmp/say "hi".txt`}, []string{`/tmp/say "hi".txt`}},
		{"double quotes keep other backslashes", `"/tmp/a\b.txt"`, []string{`/tmp/a\b.txt`}, []string{`/tmp/a\b.txt`}},
		{"file URI", `file:///tmp/a%20b.txt`, []string{"/tmp/a b.txt"}, []string{"/tmp/a b.txt"}},
		{"file URI on localhost", `file://localhost/tmp/x.txt`, []string{"/tmp/x.txt"}, []string{"/tmp/x.txt"}},
		{"several, space separated", `/a/one.txt /a/two\ words.txt`, []string{"/a/one.txt", "/a/two words.txt"}, []string{"/a/one.txt", "/a/two words.txt"}},
		{"trailing newline", "/a/one.txt\n", []string{"/a/one.txt"}, []string{"/a/one.txt"}},
		{"surrounding blanks", "  /a/one.txt \r\n", []string{"/a/one.txt"}, []string{"/a/one.txt"}},
		{"one raw path per line", "/a/My File.txt\n/a/other.txt\n", []string{"/a/My File.txt", "/a/other.txt"}, []string{"/a/My File.txt", "/a/other.txt"}},
		{"uri-list with a comment", "# from Files\r\nfile:///a/x.txt\r\nfile:///a/y.txt\r\n", []string{"/a/x.txt", "/a/y.txt"}, []string{"/a/x.txt", "/a/y.txt"}},
		{"one raw path with spaces", "/a/My Great File.txt", []string{"/a/My Great File.txt"}, []string{"/a/My Great File.txt"}},
		{"duplicates collapse", "/a/one.txt /a/one.txt", []string{"/a/one.txt"}, []string{"/a/one.txt"}},
		{"dot segments are cleaned", "/a/b/../one.txt", []string{"/a/one.txt"}, []string{"/a/one.txt"}},
		{"a folder", "/a/dir/", []string{"/a/dir"}, []string{"/a/dir"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, got := Classify(c.text, Options{Stat: statFor(c.exists...)})
			if kind != Paths {
				t.Fatalf("Classify(%q) kind = %v, want Paths", c.text, kind)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Classify(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

// TestClassify_TextStaysText is the anti-goal: ordinary pasted text —
// including text that merely looks path-like — must never be read as a
// drop, or a paste into a script becomes a file copy.
func TestClassify_TextStaysText(t *testing.T) {
	exists := []string{"/a/one.txt"}
	cases := []struct{ name, text string }{
		{"empty", ""},
		{"blank", "  \n\t"},
		{"prose", "hello world"},
		{"a route, not a file", "/api/v1/users"},
		{"a missing path", "/a/missing.txt"},
		{"one real path among code", "x := 1\n/a/one.txt"},
		{"one real and one missing", "/a/one.txt /a/missing.txt"},
		{"relative", "one.txt"},
		{"dot-relative", "./one.txt"},
		{"home-relative", "~/one.txt"},
		{"a path inside a sentence", "see /a/one.txt for details"},
		{"file URI on another host", "file://otherhost/a/one.txt"},
		{"an http URL", "https://example.com/a/one.txt"},
		{"unterminated quote", "'/a/one.txt"},
		{"a NUL byte", "/a/one.txt\x00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, got := Classify(c.text, Options{Stat: statFor(exists...)})
			if kind != Text || got != nil {
				t.Errorf("Classify(%q) = %v %q, want Text and no paths", c.text, kind, got)
			}
		})
	}
}

// TestClassify_PermissionErrorCountsAsExisting pins the macOS case: a
// file in a privacy-protected folder fails stat with a permission
// error, and treating that as "missing" would type the path into the
// buffer instead of offering the copy that then reports the real error.
func TestClassify_PermissionErrorCountsAsExisting(t *testing.T) {
	stat := func(string) error { return fmt.Errorf("stat: %w", fs.ErrPermission) }
	kind, got := Classify("/Users/me/Downloads/x.pdf", Options{Stat: stat})
	if kind != Paths || !reflect.DeepEqual(got, []string{"/Users/me/Downloads/x.pdf"}) {
		t.Errorf("Classify = %v %q, want Paths with the one path", kind, got)
	}
}

// TestClassify_Limits pins the bounds: a paste past MaxBytes or naming
// more than MaxPaths entries is text, so classification can never be
// made to stat an unbounded list on the event loop.
func TestClassify_Limits(t *testing.T) {
	always := func(string) error { return nil }

	big := "/a/" + strings.Repeat("x", MaxBytes)
	if kind, _ := Classify(big, Options{Stat: always}); kind != Text {
		t.Errorf("a %d-byte paste classified as %v, want Text", len(big), kind)
	}

	var many []string
	for i := 0; i <= MaxPaths; i++ {
		many = append(many, fmt.Sprintf("/a/f%d", i))
	}
	if kind, _ := Classify(strings.Join(many, " "), Options{Stat: always}); kind != Text {
		t.Errorf("%d paths classified as %v, want Text", len(many), kind)
	}
	if kind, got := Classify(strings.Join(many[:MaxPaths], " "), Options{Stat: always}); kind != Paths || len(got) != MaxPaths {
		t.Errorf("%d paths classified as %v with %d paths, want Paths with %d", MaxPaths, kind, len(got), MaxPaths)
	}
}

// TestClassify_Windows pins the one platform where backslash is a
// separator: reading it as an escape would turn C:\Users into C:Users.
func TestClassify_Windows(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"quoted with spaces", `"C:\Users\me\My File.txt"`, []string{`C:\Users\me\My File.txt`}},
		{"bare", `C:\a\b.txt`, []string{`C:\a\b.txt`}},
		{"two", `C:\a\b.txt D:\c.txt`, []string{`C:\a\b.txt`, `D:\c.txt`}},
		{"UNC", `\\server\share\x.txt`, []string{`\\server\share\x.txt`}},
		{"file URI", `file:///C:/a/b%20c.txt`, []string{`C:/a/b c.txt`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, got := Classify(c.text, Options{Stat: statFor(c.want...), Windows: true})
			if kind != Paths || !reflect.DeepEqual(got, c.want) {
				t.Errorf("Classify(%q) = %v %q, want Paths %q", c.text, kind, got, c.want)
			}
		})
	}
	if kind, _ := Classify(`/unix/style.txt`, Options{Stat: statFor("/unix/style.txt"), Windows: true}); kind != Text {
		t.Errorf("a Unix path on Windows classified as %v, want Text", kind)
	}
}

// TestClassify_Foreign pins the "dropped from another machine" verdict:
// only in a remote session, only for paths under a desktop root, and
// only when none of them exists here. A web route pasted over SSH must
// stay text.
func TestClassify_Foreign(t *testing.T) {
	none := statFor()
	cases := []struct {
		name   string
		text   string
		opts   Options
		kind   Kind
		expect []string
	}{
		{"a Mac path on a remote box", `/Users/me/Downloads/photo.png`, Options{Stat: none, Remote: true}, Foreign, []string{"/Users/me/Downloads/photo.png"}},
		{"escaped, two files", `/Users/me/a\ b.png /Volumes/USB/c.png`, Options{Stat: none, Remote: true}, Foreign, []string{"/Users/me/a b.png", "/Volumes/USB/c.png"}},
		{"a Linux home path", `/home/me/x.txt`, Options{Stat: none, Remote: true}, Foreign, []string{"/home/me/x.txt"}},
		{"not remote", `/Users/me/Downloads/photo.png`, Options{Stat: none}, Text, nil},
		{"a route over SSH", `/api/v1/users`, Options{Stat: none, Remote: true}, Text, nil},
		{"one of two exists", `/Users/me/a.png /Users/me/b.png`, Options{Stat: statFor("/Users/me/a.png"), Remote: true}, Text, nil},
		{"prose over SSH", `see /Users/me/a.png`, Options{Stat: none, Remote: true}, Text, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, got := Classify(c.text, c.opts)
			if kind != c.kind || !reflect.DeepEqual(got, c.expect) {
				t.Errorf("Classify(%q) = %v %q, want %v %q", c.text, kind, got, c.kind, c.expect)
			}
		})
	}
}
