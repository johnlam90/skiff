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

	gitignore "github.com/sabhiram/go-gitignore"
)

// gitignoreName is the per-directory ignore file the tree honours. Only
// this name: .git/info/exclude and core.excludesFile are git
// configuration the tree deliberately does not read (see ignoreChain).
const gitignoreName = ".gitignore"

// ignoreEntry is one directory's compiled .gitignore alongside the bytes
// it was compiled from. Keeping the source bytes lets merge skip the
// regex compilation (go-gitignore builds one regexp per pattern line)
// whenever a re-read hands back an unchanged file, which is every tick
// on every project that isn't actively editing its .gitignore.
type ignoreEntry struct {
	raw []byte
	gi  *gitignore.GitIgnore
}

// cacheIgnore refreshes the compiled matcher for dir from the bytes the
// scan read out of its .gitignore. This is the cache's only invalidation
// rule, and it is deliberately the rule the node children already
// follow: a directory's matcher is replaced at exactly the moment its
// listing is. A directory with no ignore file holds no entry, so the
// map's size tracks the project's real ignore-file count rather than its
// directory count.
//
// Unchanged bytes reuse the compiled matcher. go-gitignore builds one
// regexp per pattern line, and re-paying that every ten seconds for a
// file nobody edited is the one cost here worth avoiding.
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
		gi:  gitignore.CompileIgnoreLines(strings.Split(string(raw), "\n")...),
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
	gi     *gitignore.GitIgnore
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
// configuration the tree does not read; nothing above the project root
// is consulted; and a "!" negation in a deeper file cannot un-ignore
// what a shallower file already ignored, because the walk stops at the
// first level that says "ignored". Git permits that last case only when
// no parent directory is itself excluded, and honouring it would mean
// rebuilding cross-file pattern precedence on top of a library that
// reports one boolean per file.
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
	pinned := t.pinnedNames(dir)
	out := make([]ScanEntry, 0, len(entries))
	for _, e := range entries {
		if !ignoredByChain(chain, e, pinned) {
			out = append(out, e)
		}
	}
	return out
}

// ignoredByChain reports whether e is excluded by any level of chain,
// with two carve-outs that are the whole reason this is not a one-liner.
//
// Dotfiles are never filtered. Showing .env, .github and .gitignore
// itself is a standing decision for SSH work — they render muted, not
// hidden — and gitignore membership is a separate axis from dotfile
// visibility; .env is the most commonly ignored file there is and the
// least acceptable one to lose. Anything under pinned is exempt too: it
// is on the path to a file the user has open in a tab.
//
// Directory entries are tested with a trailing slash so a `dist/`
// pattern — which git means as "directories only" — matches, while a
// plain `dist` pattern still does.
func ignoredByChain(chain []ignoreLevel, e ScanEntry, pinned map[string]struct{}) bool {
	if strings.HasPrefix(e.Name, ".") {
		return false
	}
	if _, ok := pinned[e.Name]; ok {
		return false
	}
	rel := e.Name
	if e.IsDir {
		rel += "/"
	}
	for _, lv := range chain {
		if lv.gi.MatchesPath(lv.prefix + rel) {
			return true
		}
	}
	return false
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
