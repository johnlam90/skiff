// =============================================================================
// File: internal/diff/rows.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// rows.go answers the one question a side-by-side view asks of a diff:
// which old line sits beside which new one. That pairing is identical
// whichever constructor built the patch, so it lives here rather than in
// the overlay that paints it — the overlay keeps colors, rectangles and
// scrolling, and gets the geometry-free answer handed to it.

package diff

// RowKind classifies one aligned row of the side-by-side layout.
type RowKind int

const (
	// RowContext is an unchanged line present on both sides.
	RowContext RowKind = iota
	// RowChange holds a deletion (left), an addition (right), or a
	// modification (both) — a zero line number marks the missing side.
	RowChange
	// RowHunk is an @@ hunk header spanning the full width.
	RowHunk
	// RowFile is a file boundary in a multi-file patch (a commit diff) —
	// the file's path spans the width.
	RowFile
)

// Span is a half-open rune range [Start, End) inside a line. The zero
// value means "no range".
type Span struct {
	Start, End int
}

// Row is one display row of a side-by-side diff. LeftNo/RightNo are
// 1-based file line numbers; 0 means that side is blank for this row (a
// pure addition or deletion). Hunk and file rows carry their text in
// Left. LeftEmph/RightEmph mark the intra-line changed span on paired
// modification rows — the word-level highlight that makes "which
// characters moved" readable at a glance.
type Row struct {
	Kind            RowKind
	LeftNo, RightNo int
	Left, Right     string
	LeftEmph        Span
	RightEmph       Span
}

// Rows pairs a patch into side-by-side rows: context lines occupy both
// sides, each run of deletions pairs line-for-line with the following
// run of additions (the standard hunk shape for a modification), and
// leftovers become one-sided rows.
//
// A single-file patch gets no boundary rows — the diff view's title
// already names the file, and a lone header row would be a heading over
// nothing. Multi-file patches (a commit's diff) keep theirs, because
// there the reader genuinely needs to know which file they are in.
func Rows(p Patch) []Row {
	var rows []Row
	multi := len(p.Files) > 1
	for _, f := range p.Files {
		if multi {
			rows = append(rows, Row{Kind: RowFile, Left: f.Path()})
		}
		for _, h := range f.Hunks {
			rows = append(rows, Row{Kind: RowHunk, Left: h.Header()})
			rows = appendHunkRows(rows, h)
		}
	}
	annotateSpans(rows)
	return rows
}

// appendHunkRows pairs one hunk's lines onto rows. Pairing never crosses
// a hunk boundary: an addition in the next hunk is a different part of
// the file and lining it up against this hunk's deletion would invent a
// modification nobody made.
func appendHunkRows(rows []Row, h Hunk) []Row {
	var dels, adds []Line
	flush := func() {
		n := len(dels)
		if len(adds) > n {
			n = len(adds)
		}
		for i := 0; i < n; i++ {
			row := Row{Kind: RowChange}
			if i < len(dels) {
				row.LeftNo, row.Left = dels[i].OldNo, dels[i].Text
			}
			if i < len(adds) {
				row.RightNo, row.Right = adds[i].NewNo, adds[i].Text
			}
			rows = append(rows, row)
		}
		dels, adds = nil, nil
	}
	for _, ln := range h.Lines {
		switch ln.Kind {
		case Del:
			dels = append(dels, ln)
		case Add:
			adds = append(adds, ln)
		default:
			flush()
			rows = append(rows, Row{
				Kind:    RowContext,
				LeftNo:  ln.OldNo,
				RightNo: ln.NewNo,
				Left:    ln.Text,
				Right:   ln.Text,
			})
		}
	}
	flush()
	return rows
}

// annotateSpans stamps intra-line emphasis onto paired modification
// rows. One-sided rows are left alone: the whole line is the change, so
// highlighting part of it would claim the rest is shared with something
// that is not there.
func annotateSpans(rows []Row) {
	for i := range rows {
		r := &rows[i]
		if r.Kind != RowChange || r.LeftNo == 0 || r.RightNo == 0 {
			continue
		}
		r.LeftEmph, r.RightEmph = changedSpans([]rune(r.Left), []rune(r.Right))
	}
}

// changedSpans locates the changed portion of a modified line pair: the
// longest common prefix and suffix are unchanged, and whatever sits
// between differs. The suffix never overlaps the prefix, so a pure
// insertion yields an empty span on the old side at the insertion point.
func changedSpans(old, new []rune) (oldSpan, newSpan Span) {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	return Span{Start: p, End: len(old) - s}, Span{Start: p, End: len(new) - s}
}

// FirstChanged returns the row list's own answer to "what changed
// first" — the 1-based file line a jump-to-the-diff button should land
// on. The new-file number is preferred; a pure deletion has no new side,
// so the old one is the fallback. Zero means the rows carry no change at
// all, which a caller reads as "nowhere to jump".
func FirstChanged(rows []Row) int {
	for _, r := range rows {
		if r.Kind != RowChange {
			continue
		}
		if r.RightNo > 0 {
			return r.RightNo
		}
		if r.LeftNo > 0 {
			return r.LeftNo
		}
	}
	return 0
}
