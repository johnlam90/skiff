// =============================================================================
// File: internal/app/conflict.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-04
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// conflict.go owns the one situation where skiff can silently destroy
// somebody else's work: a file changed on disk while the buffer holding
// it is dirty. That is not exotic — it is a Tuesday in the tmux window
// this editor was built for, where the pane next door is running
// `git pull`, `sed -i`, or a second skiff.
//
// The old behaviour was a one-line status flash and then nothing: the
// next Save wrote the buffer straight over the other writer's changes.
// A flash is the wrong instrument for a decision, so this file replaces
// it with a real three-way prompt — Keep mine / Reload / Diff — built on
// the existing overlay.Dirty prefab, plus a status-bar marker that
// outlives the prompt so a dismissed conflict is not a forgotten one.
//
// The acknowledgement is recorded when the prompt *opens*, not when a
// button is pressed. Every dismissal path (a button, Esc, a click
// outside) therefore stops the ten-second tick from re-nagging about the
// same disk revision, while a genuinely new write to the same file has a
// new mtime and legitimately asks again.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/overlay"
)

// diffContextLines is how many unchanged lines surround each hunk in the
// buffer-vs-disk diff. Three is what `git diff` uses, and the diff
// viewer's side-by-side layout was tuned against that shape.
const diffContextLines = 3

// diffPairCap bounds the LCS table the line differ allocates. Beyond it
// the changed region is emitted as one coarse replace-everything hunk:
// a 1000x1000 table is already 4 MB, and a conflict that large is one
// the user will resolve by choosing a side, not by reading pairs.
const diffPairCap = 1_000_000

// noteDiskConflict records that path is in conflict at the given disk
// mtime, which is what suppresses re-prompting on the next tick.
func (a *App) noteDiskConflict(path string, mtime time.Time) {
	if a.diskConflicts == nil {
		a.diskConflicts = make(map[string]time.Time)
	}
	a.diskConflicts[path] = mtime
}

// clearDiskConflict forgets path's conflict — the user resolved it by
// reloading or by saving over it, and the marker has to go with it.
func (a *App) clearDiskConflict(path string) {
	delete(a.diskConflicts, path)
}

// tabDiskConflict reports whether tab still has an unresolved
// divergence from disk. Dirty is part of the test on purpose: both ways
// out of a conflict (Save and Reload) clear the flag, so a clean buffer
// cannot still be in conflict no matter what the map remembers, and the
// status bar never shows a stale warning while it waits for the next
// reconcile tick to prune the entry.
func (a *App) tabDiskConflict(tab *editor.Tab) bool {
	if tab == nil || tab.Path == "" || !tab.Dirty {
		return false
	}
	_, ok := a.diskConflicts[tab.Path]
	return ok
}

// openDiskConflict prompts for the dirty-buffer-versus-changed-file
// decision. The three buttons reuse overlay.Dirty's shape and its color
// grammar — left neutral, middle destructive, right the productive
// action:
//
//	Keep mine → dismiss; the buffer wins on the next save.
//	Reload    → throw away the in-memory edits, take what is on disk.
//	Diff      → show buffer against disk in the existing diff viewer.
//
// Focus starts on Keep mine so a reflexive Enter cannot discard work.
func (a *App) openDiskConflict(tab *editor.Tab, mtime time.Time) {
	a.closeAllModals()
	a.noteDiskConflict(tab.Path, mtime)

	name := filepath.Base(tab.Path)
	d := &overlay.Dirty{
		Title:   "Changed on disk",
		Message: name + " changed on disk and your copy has unsaved edits",
		Labels:  [3]string{"[ Keep mine ]", "[ Reload ]", "[ Diff ]"},
		Theme:   a.theme,
	}
	d.Size = func() (int, int) { return a.width, a.height }
	d.Close = func() { a.closeAllModals() }
	d.OnCancel = func() {
		a.flash(name + " — keeping your edits; saving will overwrite the file")
	}
	d.OnDiscard = func() { a.reloadConflictedTab(tab) }
	d.OnSave = func() { a.openConflictDiff(tab) }
	a.overlays.Open(d)
}

// menuResolveDiskConflict is the ≡ → "Resolve disk conflict…" row: it
// reopens the prompt for the active tab. Without it the status-bar
// marker is a dead end — once the prompt is dismissed the only way back
// would be to wait for somebody else to write the file again. Every
// action in skiff has to be reachable from the menu; this one doubly so,
// because the alternative is losing work.
func (a *App) menuResolveDiskConflict() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if !a.tabDiskConflict(tab) {
		return
	}
	// Re-stat rather than trusting the remembered mtime: the file may
	// have moved on again since the prompt was dismissed, and the
	// user deserves the current state.
	mtime := a.diskConflicts[tab.Path]
	if info, err := os.Stat(tab.Path); err == nil {
		mtime = info.ModTime()
		tab.Mtime = mtime
	}
	a.openDiskConflict(tab, mtime)
}

// hasDiskConflict is the menu row's enable predicate: only offer the
// resolution prompt when there is actually something to resolve.
func (a *App) hasDiskConflict() bool {
	return a.tabDiskConflict(a.activeTabPtr())
}

// reloadConflictedTab is the Reload button: take the disk copy and drop
// the in-memory edits. Destructive by design — it is the middle,
// red-tinted button and never the default focus.
func (a *App) reloadConflictedTab(tab *editor.Tab) {
	if tab == nil {
		return
	}
	name := filepath.Base(tab.Path)
	if err := tab.Reload(); err != nil {
		a.flash(fmt.Sprintf("%s reload failed: %v", name, err))
		return
	}
	a.clearDiskConflict(tab.Path)
	a.flash(name + " reloaded from disk")
}

// openConflictDiff is the Diff button: render the buffer against the
// bytes currently on disk in the ordinary diff viewer, so the user can
// answer "whose change matters?" before choosing. Disk is the left/old
// side and the buffer the right/new side — the same orientation as a
// working-tree diff, where the right column is always "what I have".
//
// The conflict marker deliberately survives this: looking is not
// resolving.
func (a *App) openConflictDiff(tab *editor.Tab) {
	if tab == nil {
		return
	}
	name := filepath.Base(tab.Path)
	data, err := os.ReadFile(tab.Path)
	if err != nil {
		a.flash(fmt.Sprintf("%s unreadable: %v", name, err))
		return
	}
	// NewBuffer, not a bare Split, so the disk side is split exactly
	// the way the buffer side was — same CRLF handling, same trailing
	// newline rule. Anything else invents differences.
	disk := editor.NewBuffer(string(data)).Lines
	lines := unifiedDiff(disk, tab.Buffer.Lines)
	if len(lines) == 0 {
		// Mtime moved but the contents match — someone touched the
		// file or wrote it back byte-identical. Nothing to resolve.
		a.clearDiskConflict(tab.Path)
		a.flash(name + " matches disk after all")
		return
	}
	// No [ Open file ] button: the file is already open in front of
	// the user — that is the entire problem.
	a.openDiffView("Disk vs buffer · "+name, lines, "", tab.Path)
}

// diffOp is one line of a line-level diff: kind is ' ' for context,
// '-' for a line only on the old side, '+' for one only on the new.
type diffOp struct {
	kind byte
	text string
}

// unifiedDiff renders old and new as `git diff`-shaped unified output,
// one string per line, ready for openDiffView. Returns nil when the two
// sides are identical.
//
// There is no git process involved here — the two sides are a buffer in
// memory and a file we just read — so the diff is computed locally. The
// common prefix and suffix are peeled off first, which is what keeps the
// expensive part small in the case this exists for: another pane
// rewrote a few lines of a long file.
func unifiedDiff(old, new []string) []string {
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
		return nil
	}

	ops := make([]diffOp, 0, len(old)+len(new))
	for _, l := range old[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, alignLines(midOld, midNew)...)
	for _, l := range old[len(old)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return unifiedHunks(ops, diffContextLines)
}

// alignLines pairs up the differing middles of two files. Small regions
// get a real longest-common-subsequence alignment; oversized ones (see
// diffPairCap) collapse to "delete all of this, add all of that", which
// is still a truthful diff and costs no memory.
func alignLines(old, new []string) []diffOp {
	if len(old) == 0 || len(new) == 0 || len(old)*len(new) > diffPairCap {
		ops := make([]diffOp, 0, len(old)+len(new))
		for _, l := range old {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range new {
			ops = append(ops, diffOp{'+', l})
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

	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new[j]:
			ops = append(ops, diffOp{' ', old[i]})
			i++
			j++
		case dp[(i+1)*stride+j] >= dp[i*stride+j+1]:
			// Deletions before additions on a tie: a modified line then
			// reads as "-old" immediately followed by "+new", which is
			// the shape parseSideBySideDiff pairs into one row.
			ops = append(ops, diffOp{'-', old[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', old[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', new[j]})
	}
	return ops
}

// unifiedHunks turns a full-file op list into unified-diff text: only
// the changed regions survive, each padded with context lines and
// introduced by an @@ header. Regions closer together than twice the
// context are merged so the output never repeats a line.
func unifiedHunks(ops []diffOp, context int) []string {
	// Line numbers each op sits at, precomputed so hunk headers don't
	// have to re-walk the list.
	oldNo := make([]int, len(ops))
	newNo := make([]int, len(ops))
	o, n := 1, 1
	for k, op := range ops {
		oldNo[k], newNo[k] = o, n
		switch op.kind {
		case ' ':
			o++
			n++
		case '-':
			o++
		case '+':
			n++
		}
	}

	var out []string
	k := 0
	for k < len(ops) {
		if ops[k].kind == ' ' {
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
			if ops[scan].kind != ' ' {
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
		for _, op := range ops[start:stop] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}
		out = append(out, hunkHeader(oldNo[start], oldCount, newNo[start], newCount))
		for _, op := range ops[start:stop] {
			out = append(out, string(op.kind)+op.text)
		}
		k = stop
	}
	return out
}

// hunkHeader formats an @@ line. A zero-length side is reported at the
// line before it, which is what `git diff` does and what keeps the
// numbers in the diff viewer's gutter honest.
func hunkHeader(oldStart, oldCount, newStart, newCount int) string {
	side := func(start, count int) string {
		if count == 0 {
			start--
		}
		return strconv.Itoa(start) + "," + strconv.Itoa(count)
	}
	var b strings.Builder
	b.WriteString("@@ -")
	b.WriteString(side(oldStart, oldCount))
	b.WriteString(" +")
	b.WriteString(side(newStart, newCount))
	b.WriteString(" @@")
	return b.String()
}
