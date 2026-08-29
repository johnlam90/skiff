// =============================================================================
// File: internal/diff/lines.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// lines.go is the second constructor: two in-memory line slices in, a
// File out. It exists because skiff's disk-conflict prompt diffs a dirty
// buffer against the bytes on disk, where there is no git process to ask
// and no reason to invent one. It used to render its answer as git-diff
// TEXT so the diff view's own parser could read it back; it now builds
// hunks directly, which is both the round trip and a whole second parser
// deleted.

package diff

// contextLines is how many unchanged lines surround each hunk. Three is
// what `git diff` uses, and the diff view's side-by-side layout was
// tuned against that shape.
const contextLines = 3

// pairCap bounds the LCS table the aligner allocates. Beyond it the
// changed region is emitted as one coarse replace-everything hunk: a
// 1000x1000 table is already 4 MB, and a conflict that large is one the
// user will resolve by choosing a side, not by reading pairs.
const pairCap = 1_000_000

// op is one line of the alignment, before it is grouped into hunks.
type op struct {
	kind Kind
	text string
}

// Lines diffs old against new and returns the changed regions as hunks.
// The File carries no paths: the two sides are two revisions of the same
// thing (disk versus buffer), so naming them would be inventing a rename.
// An empty Hunks slice is the honest answer for identical input, and the
// caller's "matches after all" early-out reads exactly that.
//
// The common prefix and suffix are peeled off first, which keeps the
// expensive part small in the case this exists for: another pane rewrote
// a few lines of a long file.
func Lines(old, new []string) File {
	pre := 0
	for pre < len(old) && pre < len(new) && old[pre] == new[pre] {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(new)-pre &&
		old[len(old)-1-suf] == new[len(new)-1-suf] {
		suf++
	}
	midOld, midNew := old[pre:len(old)-suf], new[pre:len(new)-suf]
	if len(midOld) == 0 && len(midNew) == 0 {
		return File{}
	}

	ops := make([]op, 0, len(old)+len(new))
	for _, l := range old[:pre] {
		ops = append(ops, op{Context, l})
	}
	ops = append(ops, alignLines(midOld, midNew)...)
	for _, l := range old[len(old)-suf:] {
		ops = append(ops, op{Context, l})
	}
	return File{Hunks: groupHunks(ops, contextLines)}
}

// alignLines pairs up the differing middles of two files. Small regions
// get a real longest-common-subsequence alignment; oversized ones (see
// pairCap) collapse to "delete all of this, add all of that", which is
// still a truthful diff and costs no memory.
func alignLines(old, new []string) []op {
	if len(old) == 0 || len(new) == 0 || len(old)*len(new) > pairCap {
		ops := make([]op, 0, len(old)+len(new))
		for _, l := range old {
			ops = append(ops, op{Del, l})
		}
		for _, l := range new {
			ops = append(ops, op{Add, l})
		}
		return ops
	}

	n, m := len(old), len(new)
	stride := m + 1
	// dp[i*stride+j] is the LCS length of old[i:] and new[j:]. int32
	// halves the table against int on 64-bit and cannot overflow: the
	// cap above keeps both dimensions well under 2^31.
	dp := make([]int32, (n+1)*stride)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case old[i] == new[j]:
				dp[i*stride+j] = dp[(i+1)*stride+j+1] + 1
			case dp[(i+1)*stride+j] >= dp[i*stride+j+1]:
				dp[i*stride+j] = dp[(i+1)*stride+j]
			default:
				dp[i*stride+j] = dp[i*stride+j+1]
			}
		}
	}

	ops := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new[j]:
			ops = append(ops, op{Context, old[i]})
			i++
			j++
		case dp[(i+1)*stride+j] >= dp[i*stride+j+1]:
			// Deletions before additions on a tie: a modified line then
			// reads as "-old" immediately followed by "+new", which is
			// the run shape Rows pairs onto a single side-by-side row.
			ops = append(ops, op{Del, old[i]})
			i++
		default:
			ops = append(ops, op{Add, new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{Del, old[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{Add, new[j]})
	}
	return ops
}

// groupHunks turns a full-file op list into hunks: only the changed
// regions survive, each padded with context lines. Regions closer
// together than twice the context are merged so no line is listed twice.
func groupHunks(ops []op, context int) []Hunk {
	// Line numbers each op sits at, precomputed so hunk ranges don't
	// have to re-walk the list.
	oldNo := make([]int, len(ops))
	newNo := make([]int, len(ops))
	oldLine, newLine := 1, 1
	for k, o := range ops {
		oldNo[k], newNo[k] = oldLine, newLine
		switch o.kind {
		case Context:
			oldLine++
			newLine++
		case Del:
			oldLine++
		case Add:
			newLine++
		}
	}

	var hunks []Hunk
	k := 0
	for k < len(ops) {
		if ops[k].kind == Context {
			k++
			continue
		}
		start := k - context
		if start < 0 {
			start = 0
		}
		// Extend through every change reachable within 2*context
		// unchanged lines; anything further away earns its own hunk.
		end := k
		for scan := k; scan < len(ops); scan++ {
			if ops[scan].kind != Context {
				end = scan
				continue
			}
			if scan-end > 2*context {
				break
			}
		}
		stop := end + context + 1
		if stop > len(ops) {
			stop = len(ops)
		}

		oldCount, newCount := 0, 0
		for _, o := range ops[start:stop] {
			if o.kind != Add {
				oldCount++
			}
			if o.kind != Del {
				newCount++
			}
		}
		h := Hunk{
			OldStart: rangeStart(oldNo[start], oldCount),
			OldLen:   oldCount,
			NewStart: rangeStart(newNo[start], newCount),
			NewLen:   newCount,
			Lines:    make([]Line, 0, stop-start),
		}
		for idx := start; idx < stop; idx++ {
			ln := Line{Kind: ops[idx].kind, Text: ops[idx].text}
			if ln.Kind != Add {
				ln.OldNo = oldNo[idx]
			}
			if ln.Kind != Del {
				ln.NewNo = newNo[idx]
			}
			h.Lines = append(h.Lines, ln)
		}
		hunks = append(hunks, h)
		k = stop
	}
	return hunks
}

// rangeStart applies git's wire convention for an empty side: a range
// covering no lines is reported at the line BEFORE it, which is what
// keeps a pure insertion's "-0,0" honest instead of claiming a line the
// old file never had.
func rangeStart(start, count int) int {
	if count == 0 {
		return start - 1
	}
	return start
}
