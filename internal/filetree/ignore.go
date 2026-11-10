// =============================================================================
// File: internal/filetree/ignore.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/johnlam90/skiff/internal/gitignore"
)

// gitignoreName is the per-directory ignore file the tree honours. Only
// this name: .git/info/exclude and core.excludesFile are git
// configuration the tree deliberately does not read (see ignoreChain).
const gitignoreName = ".gitignore"

// ignoreEntry is one directory's compiled .gitignore alongside the bytes
// it was compiled from. Keeping the source bytes lets merge skip the
// re-parse — and, more to the point, skip the filterEpoch bump that
// would force a full merge of the whole subtree — whenever a re-read
// hands back an unchanged file, which is every tick on every project
// that isn't actively editing its .gitignore.
type ignoreEntry struct {
	raw []byte
	gi  *gitignore.Matcher
}

// cacheIgnore refreshes the compiled matcher for dir from the bytes the
// scan read out of its .gitignore. This is the cache's only invalidation
// rule, and it is deliberately the rule the node children already
// follow: a directory's matcher is replaced at exactly the moment its
// listing is. A directory with no ignore file holds no entry, so the
// map's size tracks the project's real ignore-file count rather than its
// directory count.
//
// Unchanged bytes reuse the compiled matcher: re-parsing every ten
// seconds a file nobody edited is the one cost here worth avoiding.
//
// Returns whether the matcher actually moved (created, recompiled, or
// dropped). A move bumps filterEpoch, because this directory's matcher
// filters its whole subtree: descendants with byte-identical listings
// must still take a full merge on this sweep — both refresh walks
// visit parents first, so the bump lands before any child's merge.
func (t *Tree) cacheIgnore(dir string, raw []byte) bool {
	if len(raw) == 0 {
		if _, ok := t.ignoreCache[dir]; !ok {
			return false
		}
		delete(t.ignoreCache, dir)
		t.filterEpoch++
		return true
	}
	if t.ignoreCache == nil {
		t.ignoreCache = map[string]ignoreEntry{}
	}
	if old, ok := t.ignoreCache[dir]; ok && bytes.Equal(old.raw, raw) {
		return false
	}
	t.ignoreCache[dir] = ignoreEntry{
		raw: raw,
		gi:  gitignore.Compile(raw),
	}
	t.filterEpoch++
	return true
}

// cachedIgnoreRaw returns the raw .gitignore bytes dir's matcher was
// compiled from, or nil when the cache holds no entry — the exact
// value a scan's Ignore field must equal for the fast-path to trust
// the cached matcher.
func (t *Tree) cachedIgnoreRaw(dir string) []byte {
	return t.ignoreCache[dir].raw
}

// ignoreLevel is one .gitignore's matcher plus the prefix that turns a
// name inside the directory being filtered into a path relative to that
// .gitignore's own directory — the only form git patterns are written
// against.
type ignoreLevel struct {
	prefix string
	gi     *gitignore.Matcher
}

// ignoreChain returns the matchers that apply inside dir, deepest first.
//
// Nested .gitignore files are supported for the whole chain from the
// project root down to dir, so a rule in src/.gitignore applies below
// src/ and nowhere else. That is what git does, and therefore what
// `git ls-files --exclude-standard` — the finder's primary index path —
// already reflects, which is why the tree matches it rather than the
// finder's root-only non-git fallback.
//
// The limits, stated plainly because a half-answer is worse than none:
// .git/info/exclude, core.excludesFile and any global excludes are git
// configuration the tree does not read, and nothing above the project
// root is consulted.
func (t *Tree) ignoreChain(dir string) []ignoreLevel {
	if len(t.ignoreCache) == 0 || t.Root == nil {
		return nil
	}
	var levels []ignoreLevel
	prefix := ""
	for d := dir; ; {
		if e, ok := t.ignoreCache[d]; ok && e.gi != nil {
			levels = append(levels, ignoreLevel{prefix: prefix, gi: e.gi})
		}
		if d == t.Root.Path {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break // walked past the project without meeting its root
		}
		prefix = filepath.Base(d) + "/" + prefix
		d = parent
	}
	return levels
}

// chainVerdict asks the chain about one entry of the directory the
// chain was built for. The deepest ignore file with ANY matching pattern
// decides, and a "!" match there is a decision too — it is how
// sub/.gitignore's `!keep.log` re-includes a file the root's `*.log`
// excluded. That is git's precedence: patterns in a lower-level file
// override those in a higher-level one.
func chainVerdict(chain []ignoreLevel, name string, isDir bool) bool {
	for _, lv := range chain {
		switch lv.gi.Match(lv.prefix+name, isDir) {
		case gitignore.Ignored:
			return true
		case gitignore.Included:
			return false
		}
	}
	return false
}

// dirExcluded reports whether dir itself, or any directory between it
// and the project root, is gitignored.
//
// This is the other half of git's precedence and the reason
// chainVerdict alone is not the answer: once a directory is excluded,
// git never looks inside it, so nothing below can be re-included — not
// by a "!" in a parent's file, and not by a .gitignore inside the
// excluded directory. The tree normally gets that for free (a hidden
// directory has no rows to filter), but an excluded directory can still
// be on screen: a dot-directory is never hidden, and neither is one on
// the path to an open tab. Its contents are ignored all the same.
func (t *Tree) dirExcluded(dir string) bool {
	if t.Root == nil {
		return false
	}
	for d := dir; d != t.Root.Path; {
		parent := filepath.Dir(d)
		if parent == d {
			return false // walked past the project without meeting its root
		}
		if chainVerdict(t.ignoreChain(parent), filepath.Base(d), true) {
			return true
		}
		d = parent
	}
	return false
}

// filterIgnored drops the entries of dir that the project's .gitignore
// files exclude. Returns the input untouched when filtering is off or no
// ignore file applies, so the overwhelmingly common directory allocates
// nothing and pays one map lookup per ancestor.
func (t *Tree) filterIgnored(dir string, entries []ScanEntry) []ScanEntry {
	if !t.HideIgnored || len(entries) == 0 {
		return entries
	}
	chain := t.ignoreChain(dir)
	if len(chain) == 0 {
		return entries
	}
	excluded := t.dirExcluded(dir)
	pinned := t.pinnedNames(dir)
	out := make([]ScanEntry, 0, len(entries))
	for _, e := range entries {
		if !ignoredByChain(chain, excluded, e, pinned) {
			out = append(out, e)
		}
	}
	return out
}

// ignoredByChain reports whether e is excluded — because the directory
// it sits in is (dirExcluded), or by the chain's own verdict — with two
// carve-outs that are the whole reason this is not a one-liner.
//
// Dotfiles are never filtered. Showing .env, .github and .gitignore
// itself is a standing decision for SSH work — they render muted, not
// hidden — and gitignore membership is a separate axis from dotfile
// visibility; .env is the most commonly ignored file there is and the
// least acceptable one to lose. Anything under pinned is exempt too: it
// is on the path to a file the user has open in a tab.
//
// A symlink is matched as a file whatever it points at, because that is
// what git sees: a `dist/` pattern — "directories only" — does not
// exclude a symlink named dist.
func ignoredByChain(chain []ignoreLevel, dirExcluded bool, e ScanEntry, pinned map[string]struct{}) bool {
	if strings.HasPrefix(e.Name, ".") {
		return false
	}
	if _, ok := pinned[e.Name]; ok {
		return false
	}
	if dirExcluded {
		return true
	}
	return chainVerdict(chain, e.Name, e.IsDir && !e.IsLink)
}

// pinnedNames returns the immediate child names of dir that lead to a
// file the app has open. Computed once per directory read rather than
// once per entry: the open-tab set is small and the entry list is not.
func (t *Tree) pinnedNames(dir string) map[string]struct{} {
	if len(t.openFiles) == 0 {
		return nil
	}
	prefix := dir + string(filepath.Separator)
	var names map[string]struct{}
	for p := range t.openFiles {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		seg := p[len(prefix):]
		if i := strings.IndexRune(seg, filepath.Separator); i >= 0 {
			seg = seg[:i]
		}
		if seg == "" {
			continue
		}
		if names == nil {
			names = make(map[string]struct{}, len(t.openFiles))
		}
		names[seg] = struct{}{}
	}
	return names
}
