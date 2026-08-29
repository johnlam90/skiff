// =============================================================================
// File: internal/filetree/render.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/icons"
	"github.com/johnlam90/skiff/internal/textdraw"
	"github.com/johnlam90/skiff/internal/theme"
)

// EmptyFolderLabel is the muted placeholder row drawn under the project
// name when the root has no visible children. Without it an empty
// project is indistinguishable from a tree that failed to load, which
// is the first thing a user hits after `mkdir proj && skiff proj`.
const EmptyFolderLabel = "(folder is empty)"

// UnreadableLabel is the muted marker appended to a directory row whose
// last read failed. It is deliberately text rather than a colour: the
// difference between "empty" and "I could not look" has to survive a
// monochrome terminal and a colourblind reader.
const UnreadableLabel = "(unreadable)"

// SymlinkLabel is the marker appended to a row whose entry is a symbolic
// link. Same reasoning as UnreadableLabel: "this row is not where it
// says it is" has to survive a monochrome terminal, so it is a glyph in
// the label rather than a colour.
const SymlinkLabel = "→"

// LoopLabel joins SymlinkLabel on a link whose target is already an
// ancestor of the row. The row deliberately loses its chevron too — the
// alternative, a chevron that opens onto nothing, reads as a bug.
const LoopLabel = "(link loop)"

// moreRowFormat renders the sentinel row's label from the count of
// entries the cap dropped.
const moreRowFormat = "… %d more"

// listHeaderRows is how many rows of a tree render rect sit above the
// scrollable list: the EXPLORER header and the project-root row. Both
// are pinned, which is why HitTest offsets by the same number and the
// scrollbar starts below them.
const listHeaderRows = 2

// listArea splits a render rect h rows tall into the pinned header
// block and the scrollable list below it, returning the list's row
// offset within the rect and its height (never negative).
func listArea(h int) (offset, height int) {
	height = h - listHeaderRows
	if height < 0 {
		height = 0
	}
	return listHeaderRows, height
}

// Render draws the tree into the rectangle (x, y, w, h). Each visible row
// is also remembered (in t.visible) so HitTest can map a click back to a
// node without re-walking the tree.
//
// A listing taller than the list area reserves the rect's rightmost
// column for the scrollbar and draws the rows one cell narrower, so
// labels and the git change letter stop where the bar starts. The bar
// spans only the scrollable rows: the EXPLORER header and the project
// root above it are pinned, and a bar drawn past them would claim they
// scroll.
func (t *Tree) Render(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	bg := th.SidebarBG
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Header — small all-caps label above the project name. The
	// project name itself is also a click target: it's the only way
	// to reset the active folder back to the root once a subfolder
	// has been selected. Render bold/Accent when it *is* the active
	// folder, plain text otherwise — same visual rule the children
	// rows follow, so the highlight is honest.
	headerStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Bold(true)
	drawString(scr, x, y, w, " EXPLORER", headerStyle)
	rootActive := t.ActiveFolder == "" || t.ActiveFolder == t.Root.Path
	rootStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text).Bold(true)
	if rootActive {
		rootStyle = tcell.StyleDefault.Background(bg).Foreground(th.Accent).Bold(true)
	}
	rootChange := t.DirtyFolders[t.Root.Path]
	if rootChange != GitChangeNone {
		rootStyle = rootStyle.Foreground(gitChangeColor(th, rootChange))
	}
	drawString(scr, x, y+1, w, " "+t.Root.Name, rootStyle)
	drawChangeLetter(scr, x, y+1, w, rootChange, rootStyle)

	// Build the flat list of visible rows from the root's children.
	flat := t.flatten()
	t.flatCount = len(flat)

	listOff, listH := listArea(h)
	listTop := y + listOff
	t.clampScroll(len(flat), listH)

	// Reserve the bar's column before any row is drawn so truncation
	// and the paint agree — the same order Tab.Render uses.
	rowW := w
	bar := t.scrollbarVisible(w, listH)
	if bar {
		rowW--
	}

	// An empty project renders as a bare root row with nothing under
	// it, which reads as "the tree failed to load" rather than "there
	// is nothing here". Say so explicitly, in the muted tone the row
	// deserves. drawString clips to w, so a narrow sidebar truncates
	// instead of bleeding into the editor.
	//
	// A root we could not read gets the other label: "empty" would be a
	// fabrication, and the permission problem is the one thing the user
	// needs to know to fix it.
	if len(flat) == 0 {
		if listH > 0 {
			label := EmptyFolderLabel
			if t.Root.ReadErr != nil {
				label = UnreadableLabel
			}
			emptyStyle := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Italic(true)
			drawString(scr, x, listTop, rowW, " "+label, emptyStyle)
		}
		t.visible = nil
		return
	}

	visible := make([]*Node, 0, listH)
	for row := 0; row < listH; row++ {
		idx := t.ScrollY + row
		if idx < 0 || idx >= len(flat) {
			visible = append(visible, nil)
			continue
		}
		item := flat[idx]
		active := item.Node.Path == t.ActiveFile || (item.Node.IsDir && item.containsPath(t.ActiveFolder))
		change := t.changeKind(item.Node)
		drawNodeRow(scr, th, x, listTop+row, rowW, item, active, change, t.IconsEnabled)
		visible = append(visible, item.Node)
	}
	t.visible = visible
	if bar {
		t.renderScrollbar(scr, th, x+w-1, listTop, listH)
	}
}

// changeKind returns the git status color category for a tree node.
func (t *Tree) changeKind(n *Node) GitChangeKind {
	if n == nil {
		return GitChangeNone
	}
	if n.IsDir {
		return t.DirtyFolders[n.Path]
	}
	return t.DirtyFiles[n.Path]
}

// drawNodeRow renders one tree row with proper indent, chevron, and color.
// active=true marks the active file or current working folder. change marks
// uncommitted git status and overrides the normal foreground so changed names
// stand out in the tree like other modern editors.
// withIcons=true prefixes the name with a Nerd Font glyph + space; off
// renders the legacy chevron-only look for terminals that can't show
// the private-use glyphs.
//
// When icons are enabled the row is rendered in three segments
// (prefix → glyph → name) so the glyph can take its own per-language
// colour while the name keeps the row's normal fg/dirty/active
// styling. That's the visual cue you find in nvim-tree and friends:
// a quick eye-scan picks out Go from Ruby from Markdown without
// reading any text.
func drawNodeRow(scr tcell.Screen, th theme.Theme, x, y, w int, item flatNode, active bool, change GitChangeKind, withIcons bool) {
	bg := th.SidebarBG
	indent := strings.Repeat("  ", item.Depth)

	// The "… N more" sentinel is not a filesystem entry: no chevron, no
	// glyph, no git badge, and italic-muted so it reads as a note about
	// the list rather than another row in it.
	if item.Node.Sentinel {
		st := tcell.StyleDefault.Background(bg).Foreground(th.Muted).Italic(true)
		drawString(scr, x, y, w, " "+indent+"  "+item.Node.Name, st)
		return
	}

	// A directory whose last read failed is dimmed and labelled. The
	// dimming is the glance-level cue; the label is what survives a
	// monochrome terminal, and it is placed before active/dirty in the
	// cascade below so a loud row still keeps the text.
	unreadable := item.Node.ReadErr != nil

	// Compute the row-level foreground via this priority cascade
	// (highest wins last):
	//
	//   1. base = FolderColor / FileColor for the node type
	//   2. dotfile/dotdir → Muted, so .gitignore / .github read as
	//      "metadata, not source" without disappearing
	//   3. active folder → Accent, so the current target is loud
	//   4. dirty → Modified, so uncommitted work always stands out
	//
	// Active/dirty deliberately override the dotfile dimming — a
	// modified .env or the active .github/ folder is still the most
	// important thing on the row.
	// A chain row shows the folded "a/b/c" label instead of the node's
	// own name; the dotfile check below keys off this displayed label,
	// so ".config/app" reads as metadata exactly like ".config" would.
	name := item.Node.Name
	if item.Display != "" {
		name = item.Display
	}

	var fg tcell.Color
	if item.Node.IsDir {
		fg = th.FolderColor
	} else {
		fg = th.FileColor
	}
	if strings.HasPrefix(name, ".") || unreadable {
		fg = th.Muted
	}
	if active {
		fg = th.Accent
	}
	if change != GitChangeNone {
		fg = gitChangeColor(th, change)
	}
	rowStyle := tcell.StyleDefault.Background(bg).Foreground(fg)
	if active {
		rowStyle = rowStyle.Bold(true)
	}

	// Build the left chunk (indent + chevron + space) and right chunk
	// (name, with a trailing slash for dirs). Both render in rowStyle;
	// only the glyph between them gets its own colour.
	//
	// A looping link is drawn without a chevron: it is a directory the
	// tree will never open, and a chevron that expands onto nothing
	// reads as a bug rather than as a decision.
	var prefix, suffix string
	if item.Node.IsDir && !item.Node.Loop {
		chev := "▸"
		if item.Node.Expanded {
			chev = "▾"
		}
		prefix = " " + indent + chev + " "
		suffix = name + "/"
	} else {
		prefix = " " + indent + "  "
		suffix = name
		if item.Node.IsDir {
			suffix += "/"
		}
	}
	if item.Node.IsLink {
		suffix += " " + SymlinkLabel
		if item.Node.Loop {
			suffix += " " + LoopLabel
		}
	}
	if unreadable {
		suffix += " " + UnreadableLabel
	}

	if !withIcons {
		drawString(scr, x, y, w, prefix+suffix, rowStyle)
		drawChangeLetter(scr, x, y, w, change, rowStyle)
		return
	}

	glyph := icons.For(item.Node.Name, item.Node.IsDir, item.Node.Expanded)
	glyphFg := icons.ColorFor(item.Node.Name, item.Node.IsDir, fg)
	// Dirty files keep their per-language glyph colour — the language
	// hue is the at-a-glance cue, and the name turning Modified is
	// already enough to flag "this is dirty".
	glyphStyle := tcell.StyleDefault.Background(bg).Foreground(glyphFg)
	if active {
		glyphStyle = glyphStyle.Bold(true)
	}

	drawString(scr, x, y, w, prefix, rowStyle)
	// Cell widths, not rune counts: a Nerd Font glyph or a CJK chain
	// label advancing by its rune count would overdraw the name.
	px := textdraw.Width(prefix)
	drawString(scr, x+px, y, w-px, glyph, glyphStyle)
	gx := textdraw.Width(glyph)
	drawString(scr, x+px+gx, y, w-px-gx, "  "+suffix, rowStyle)
	drawChangeLetter(scr, x, y, w, change, rowStyle)
}

// drawChangeLetter paints the git status letter (with one leading space
// so it survives long truncated names) at the row's right edge. No-op
// when the row is clean or the row is too narrow to fit it.
func drawChangeLetter(scr tcell.Screen, x, y, w int, change GitChangeKind, st tcell.Style) {
	letter := gitChangeLetter(change)
	if letter == 0 || w < 4 {
		return
	}
	scr.SetContent(x+w-3, y, ' ', nil, st)
	scr.SetContent(x+w-2, y, letter, nil, st)
}

// gitChangeLetter maps git status kinds to the one-cell letter drawn at
// the row's right edge — the same vocabulary the GIT panel uses (M/A/D/R)
// plus '~' for a folder's "mixed changes" state. Hue alone can't carry
// git status for colorblind users; the letter is the non-color channel.
func gitChangeLetter(change GitChangeKind) rune {
	switch change {
	case GitChangeAdded:
		return 'A'
	case GitChangeDeleted:
		return 'D'
	case GitChangeRenamed:
		return 'R'
	case GitChangeMixed:
		return '~'
	case GitChangeModified:
		return 'M'
	}
	return 0
}

// gitChangeColor maps git status kinds to the tree row foreground.
func gitChangeColor(th theme.Theme, change GitChangeKind) tcell.Color {
	switch change {
	case GitChangeAdded:
		return th.GitAdded
	case GitChangeDeleted:
		return th.GitDeleted
	case GitChangeRenamed:
		return th.GitRenamed
	case GitChangeMixed:
		return th.GitMixed
	case GitChangeModified:
		return th.GitModified
	}
	return th.FileColor
}

// drawString writes s left-aligned within [x, x+w). Excess content is
// truncated; short content is implicitly padded by the row's pre-painted
// bg. Cluster-aware via textdraw, so a CJK filename clips at the sidebar
// edge instead of drifting past it.
func drawString(scr tcell.Screen, x, y, w int, s string, st tcell.Style) {
	textdraw.DrawClipped(scr, x, y, w, s, st)
}
