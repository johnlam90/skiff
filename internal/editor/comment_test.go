// =============================================================================
// File: internal/editor/comment_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-05-14
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import "testing"

// TestLineCommentPrefix_CommonExtensions pins the filename and extension
// lookup used by the toggle action before it mutates a buffer.
func TestLineCommentPrefix_CommonExtensions(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"main.go", "//", true},
		{"script.py", "#", true},
		{"query.sql", "--", true},
		{"config.ini", ";", true},
		{"Dockerfile", "#", true},
		{"index.html", "", false},
	}
	for _, c := range cases {
		got, ok := LineCommentPrefix(c.path)
		if got != c.want || ok != c.ok {
			t.Fatalf("LineCommentPrefix(%q) = %q, %v; want %q, %v", c.path, got, ok, c.want, c.ok)
		}
	}
}

// TestToggleLineComment_CommentsSelectedLines checks the headline path: every
// selected non-blank line gets a comment marker at the block's shared indent,
// which is column zero here because the block's shallowest line is flush left.
func TestToggleLineComment_CommentsSelectedLines(t *testing.T) {
	tab := commentTestTab("main.go", "package main\nfunc main() {\n\tprintln(\"x\")\n}\n")
	tab.Anchor = Position{Line: 1, Col: 0}
	tab.Cursor = Position{Line: 3, Col: 0}

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "package main\n// func main() {\n// \tprintln(\"x\")\n}\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
	if !tab.Dirty || !tab.StyleStale {
		t.Fatal("toggle should dirty the tab and invalidate highlighting")
	}
}

// TestToggleLineComment_UncommentsWhenAllLinesCommented proves the toggle
// flips direction only when every non-blank target line is already commented.
func TestToggleLineComment_UncommentsWhenAllLinesCommented(t *testing.T) {
	tab := commentTestTab("main.go", "// one\n// \ttwo\n")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 0}

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "one\n\ttwo\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_UncommentsIndentedExistingComments keeps the toggle
// tolerant of comments that already sit after indentation.
func TestToggleLineComment_UncommentsIndentedExistingComments(t *testing.T) {
	tab := commentTestTab("main.go", "\t// one\n  // two\n")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 0}

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "\tone\n  two\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_MixedSelectionCommentsAllLines locks in the common
// editor rule: a mixed selection comments every non-blank line.
func TestToggleLineComment_MixedSelectionCommentsAllLines(t *testing.T) {
	tab := commentTestTab("main.go", "// one\n\n  two")
	tab.SelectAll()

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "// // one\n\n//   two"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_IndentedLineKeepsIndentBeforeMarker is the fix for the
// marker landing at column zero on an indented line and stranding the
// indentation behind it ("//\tprintln" instead of "\t// println").
func TestToggleLineComment_IndentedLineKeepsIndentBeforeMarker(t *testing.T) {
	tab := commentTestTab("main.go", "func main() {\n\tprintln(\"x\")\n}\n")
	tab.Cursor = Position{Line: 1, Col: 3}
	tab.Anchor = tab.Cursor

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "func main() {\n\t// println(\"x\")\n}\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_MixedIndentAlignsToShallowestIndent pins the chosen
// rule for a mixed-indent block: one shared marker column (the block's
// shallowest indent) rather than a per-line indent-relative marker, so the
// commented region stays rectangular and the deeper lines keep their extra
// indentation verbatim after the marker.
func TestToggleLineComment_MixedIndentAlignsToShallowestIndent(t *testing.T) {
	tab := commentTestTab("main.go", "\tif x {\n\t\tdo()\n\t}\n")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 2}

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "\t// if x {\n\t// \tdo()\n\t// }\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_BlankLinesInsideSelectionStayBare guards the
// pre-existing rule that only non-blank lines get a marker, now that the
// marker column is derived from the block's non-blank lines.
func TestToggleLineComment_BlankLinesInsideSelectionStayBare(t *testing.T) {
	tab := commentTestTab("main.go", "    a\n\n  \n    b\n")
	tab.SelectAll()

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "    // a\n\n  \n    // b\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_RoundTripRestoresOriginalBytes is the reversibility
// contract the shallowest-indent rule exists to keep: comment then uncomment
// must hand back the original bytes for every indentation shape.
func TestToggleLineComment_RoundTripRestoresOriginalBytes(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"flush left", "one\ntwo\n"},
		{"uniform tabs", "\tone\n\ttwo\n"},
		{"mixed depth", "  one\n      two\n    three\n"},
		{"tab vs space indent", "\tone\n    two\n"},
		{"blank lines inside", "    one\n\n\n    two\n"},
		{"already commented", "// one\n  two\n"},
		{"trailing whitespace kept", "  one   \n    two\t\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := commentTestTab("main.go", tc.text)
			tab.SelectAll()
			if changed, ok := tab.ToggleLineComment(); !ok || !changed {
				t.Fatalf("comment pass = %v, %v; want changed and ok", changed, ok)
			}
			tab.SelectAll()
			if changed, ok := tab.ToggleLineComment(); !ok || !changed {
				t.Fatalf("uncomment pass = %v, %v; want changed and ok", changed, ok)
			}
			if got := tab.Buffer.String(); got != tc.text {
				t.Fatalf("round trip:\n%q\nwant:\n%q", got, tc.text)
			}
		})
	}
}

// TestToggleLineComment_UncommentsLegacyColumnZeroComments keeps files written
// by older builds — marker at column zero, indentation stranded behind it —
// uncommenting back to their exact original bytes.
func TestToggleLineComment_UncommentsLegacyColumnZeroComments(t *testing.T) {
	tab := commentTestTab("main.go", "//     four spaces\n// \ttabbed\n")
	tab.SelectAll()

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "    four spaces\n\ttabbed\n"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestBlockIndent_SharedPrefixOnly pins the helper that picks the marker
// column: it is a literal byte prefix of every non-blank line, so a tab-vs-
// space mixture correctly collapses to column zero instead of inventing an
// indent that is not actually there.
func TestBlockIndent_SharedPrefixOnly(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"uniform", []string{"    a", "    b"}, "    "},
		{"nested", []string{"  a", "      b"}, "  "},
		{"flush left member", []string{"a", "    b"}, ""},
		{"tabs vs spaces", []string{"\ta", "    b"}, ""},
		{"blank lines ignored", []string{"  a", "", "\t", "    b"}, "  "},
		{"all blank", []string{"", "   "}, ""},
		{"single line", []string{"\t\ta"}, "\t\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blockIndent(tc.lines, 0, len(tc.lines)-1); got != tc.want {
				t.Fatalf("blockIndent() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestToggleLineComment_SelectionEndingAtColumnZeroExcludesThatLine keeps
// whole-line selections from unexpectedly changing the first untouched line.
func TestToggleLineComment_SelectionEndingAtColumnZeroExcludesThatLine(t *testing.T) {
	tab := commentTestTab("main.go", "one\ntwo\nthree")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 2, Col: 0}

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "// one\n// two\nthree"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_NoSelectionUsesCursorLine makes the menu item useful
// even when the user has not highlighted text first.
func TestToggleLineComment_NoSelectionUsesCursorLine(t *testing.T) {
	tab := commentTestTab("main.go", "one\ntwo\nthree")
	tab.Cursor = Position{Line: 1, Col: 1}
	tab.Anchor = tab.Cursor

	changed, ok := tab.ToggleLineComment()

	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	want := "one\n// two\nthree"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

// TestToggleLineComment_BlankSelectionIsNoop avoids adding comment markers
// to whitespace-only lines just because they were inside the selection.
func TestToggleLineComment_BlankSelectionIsNoop(t *testing.T) {
	tab := commentTestTab("main.go", "  \n\t")
	tab.SelectAll()

	changed, ok := tab.ToggleLineComment()

	if !ok {
		t.Fatal("blank Go selection should still have a known comment syntax")
	}
	if changed {
		t.Fatal("blank-only selection should not change the buffer")
	}
	if tab.Dirty || tab.CanUndo() {
		t.Fatal("blank-only selection should not dirty the tab or push undo")
	}
}

// TestToggleLineComment_UnsupportedFileTypeIsNoop protects formats like HTML
// where a line-comment marker would be wrong.
func TestToggleLineComment_UnsupportedFileTypeIsNoop(t *testing.T) {
	tab := commentTestTab("index.html", "<main></main>")

	changed, ok := tab.ToggleLineComment()

	if ok || changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want unsupported noop", changed, ok)
	}
	if got := tab.Buffer.String(); got != "<main></main>" {
		t.Fatalf("buffer changed for unsupported type: %q", got)
	}
}

// TestToggleLineComment_UndoRestoresSelectionAndText confirms the action is
// one structural undo step, including the cursor and active selection.
func TestToggleLineComment_UndoRestoresSelectionAndText(t *testing.T) {
	tab := commentTestTab("main.go", "one\ntwo")
	tab.Anchor = Position{Line: 0, Col: 1}
	tab.Cursor = Position{Line: 1, Col: 2}

	changed, ok := tab.ToggleLineComment()
	if !ok || !changed {
		t.Fatalf("ToggleLineComment() = %v, %v; want changed and ok", changed, ok)
	}
	if !tab.Undo() {
		t.Fatal("Undo should restore the pre-toggle snapshot")
	}
	if got := tab.Buffer.String(); got != "one\ntwo" {
		t.Fatalf("undo buffer = %q, want original", got)
	}
	if tab.Anchor != (Position{Line: 0, Col: 1}) || tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("undo selection = anchor %+v cursor %+v", tab.Anchor, tab.Cursor)
	}
}

// commentTestTab constructs a text tab with undo initialized, without touching
// the filesystem.
func commentTestTab(path, text string) *Tab {
	t := &Tab{
		Path:       path,
		Buffer:     NewBuffer(text),
		StyleStale: false,
	}
	t.initUndo()
	return t
}
