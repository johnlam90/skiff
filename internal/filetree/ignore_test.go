// =============================================================================
// File: internal/filetree/ignore_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package filetree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitignore_HidesIgnoredEntriesUntilToggledOff is the headline
// contract: with filtering on the sidebar answers "is this project
// noise?" the way the finder's index already does, and with it off the
// same directory shows everything again. The toggle is only meaningful
// if both directions work, so both are asserted from one fixture.
func TestGitignore_HidesIgnoredEntriesUntilToggledOff(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !tr.HideIgnored {
		t.Fatal("filtering must default on so the tree and the finder agree out of the box")
	}
	for _, name := range []string{"dist", "app.log"} {
		if findChild(tr.Root, name) != nil {
			t.Fatalf("%q is gitignored and must not be a row", name)
		}
	}
	if findChild(tr.Root, "main.go") == nil {
		t.Fatal("main.go is not ignored and must stay visible")
	}

	tr.HideIgnored = false
	tr.Refresh()
	for _, name := range []string{"dist", "app.log", "main.go"} {
		if findChild(tr.Root, name) == nil {
			t.Fatalf("%q must come back once filtering is off", name)
		}
	}
}

// TestGitignore_DotfilesStayVisibleInBothStates pins the deliberate
// split between the two axes. .env is the most commonly gitignored file
// there is and the least acceptable one to lose over SSH, so gitignore
// filtering never removes a dotfile — including .gitignore itself,
// which would otherwise hide the very rule set the user is debugging.
func TestGitignore_DotfilesStayVisibleInBothStates(t *testing.T) {
	tr, err := New(mkIgnoreTree(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, on := range []bool{true, false} {
		tr.HideIgnored = on
		tr.Refresh()
		for _, name := range []string{".env", ".gitignore"} {
			if findChild(tr.Root, name) == nil {
				t.Fatalf("HideIgnored=%v hid the dotfile %q", on, name)
			}
		}
	}
}

// TestGitignore_NestedFileAppliesToItsSubtreeOnly is the nested-support
// claim stated as behaviour: a rule written in src/.gitignore hides the
// matching name below src/ and leaves the identically-named file at the
// root alone. Getting only one of those two right is the failure mode
// worth catching.
func TestGitignore_NestedFileAppliesToItsSubtreeOnly(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "*.tmp\n")
	mustWrite(t, filepath.Join(root, "generated.go"), "root copy")
	mustMkdir(t, filepath.Join(root, "src"))
	mustWrite(t, filepath.Join(root, "src", ".gitignore"), "generated.go\n")
	mustWrite(t, filepath.Join(root, "src", "generated.go"), "built")
	mustWrite(t, filepath.Join(root, "src", "real.go"), "package src")
	mustWrite(t, filepath.Join(root, "src", "scratch.tmp"), "x")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "generated.go") == nil {
		t.Fatal("the root's generated.go is outside src/.gitignore's reach and must stay")
	}
	src := findChild(tr.Root, "src")
	if src == nil {
		t.Fatal("src/ missing")
	}
	tr.Toggle(src)

	if findChild(src, "generated.go") != nil {
		t.Fatal("src/.gitignore must hide src/generated.go")
	}
	if findChild(src, "scratch.tmp") != nil {
		t.Fatal("the root .gitignore's *.tmp must still reach into src/")
	}
	if findChild(src, "real.go") == nil {
		t.Fatal("src/real.go matches nothing and must stay visible")
	}
}

// TestGitignore_MatcherCacheFollowsTheChildren pins the cache's one
// invalidation rule: a directory's compiled matcher is replaced exactly
// when its listing is. Editing .gitignore and refreshing must move the
// filter, and an unchanged file must reuse the compiled matcher rather
// than re-parsing it — and re-merging its whole subtree — every tick.
func TestGitignore_MatcherCacheFollowsTheChildren(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "a.txt\n")
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "a.txt") != nil || findChild(tr.Root, "b.txt") == nil {
		t.Fatal("initial state should hide a.txt only")
	}

	compiled := tr.ignoreCache[tr.Root.Path].gi
	if compiled == nil {
		t.Fatal("the root .gitignore should be cached after the first read")
	}
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi != compiled {
		t.Fatal("unchanged .gitignore bytes must reuse the compiled matcher")
	}

	mustWrite(t, filepath.Join(root, ".gitignore"), "b.txt\n")
	tr.Refresh()
	if tr.ignoreCache[tr.Root.Path].gi == compiled {
		t.Fatal("edited .gitignore must recompile")
	}
	if findChild(tr.Root, "a.txt") == nil {
		t.Fatal("a.txt is no longer ignored and must reappear")
	}
	if findChild(tr.Root, "b.txt") != nil {
		t.Fatal("b.txt is newly ignored and must disappear")
	}

	// Deleting the file drops the entry entirely rather than leaving a
	// stale matcher behind.
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	tr.Refresh()
	if _, ok := tr.ignoreCache[tr.Root.Path]; ok {
		t.Fatal("a removed .gitignore must leave no cache entry")
	}
	if findChild(tr.Root, "b.txt") == nil {
		t.Fatal("b.txt must return once the rules are gone")
	}
}

// TestGitignore_OpenTabNeverVanishes is the safety rule: the user can
// legitimately be editing a file inside an ignored directory (a
// generated file, a vendored copy), and the sidebar must not pretend it
// does not exist. The ignored directory reappears carrying exactly the
// pinned file — the rest of the build output stays filtered.
func TestGitignore_OpenTabNeverVanishes(t *testing.T) {
	root := mkIgnoreTree(t)
	mustWrite(t, filepath.Join(root, "dist", "other.js"), "// also built")
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("test setup: dist/ should start hidden")
	}

	open := filepath.Join(root, "dist", "bundle.js")
	tr.SetOpenFiles([]string{open})
	tr.Refresh()

	dist := findChild(tr.Root, "dist")
	if dist == nil {
		t.Fatal("the directory holding an open tab must be reachable")
	}
	tr.Toggle(dist)
	if findChild(dist, "bundle.js") == nil {
		t.Fatal("the open file must be a row")
	}
	if findChild(dist, "other.js") != nil {
		t.Fatal("pinning one file must not un-ignore its whole directory")
	}

	// Reveal takes the same path from cold: it pins the target itself and
	// re-reads the ancestors the filter had emptied.
	fresh, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fresh.Reveal(open, 20)
	revealed := findChild(fresh.Root, "dist")
	if revealed == nil || findChild(revealed, "bundle.js") == nil {
		t.Fatal("Reveal must surface a target inside an ignored directory")
	}

	// Closing the tab un-pins it again.
	tr.SetOpenFiles(nil)
	tr.Refresh()
	if findChild(tr.Root, "dist") != nil {
		t.Fatal("dist/ should return to hidden once nothing inside it is open")
	}
}

// TestGitignore_DeeperNegationReincludes pins git's cross-file
// precedence: the deepest .gitignore with a matching pattern decides, so
// sub/.gitignore's `!keep.log` brings back a file the root's `*.log`
// excluded — while a sibling the nested file does not mention stays
// hidden. The old "first level that says ignored wins" walk could not
// express this and hid a file git tracks.
func TestGitignore_DeeperNegationReincludes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "*.log\n")
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWrite(t, filepath.Join(root, "sub", ".gitignore"), "!keep.log\n")
	mustWrite(t, filepath.Join(root, "sub", "keep.log"), "wanted")
	mustWrite(t, filepath.Join(root, "sub", "noise.log"), "noise")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sub := findChild(tr.Root, "sub")
	if sub == nil {
		t.Fatal("sub/ must be visible")
	}
	tr.Toggle(sub)
	if findChild(sub, "keep.log") == nil {
		t.Fatal("sub/.gitignore's !keep.log must re-include it over the root's *.log")
	}
	if findChild(sub, "noise.log") != nil {
		t.Fatal("the root's *.log must still hide what the nested file does not mention")
	}
}

// TestGitignore_ExcludedDirCannotBeReincludedFromInside pins the other
// half of that precedence: once a directory is excluded git never looks
// inside it, so a negation in the parent's file and a .gitignore inside
// the directory are both powerless. The directory is on screen here only
// because a tab pins it — the one way the tree ever filters the contents
// of an excluded directory — and its unpinned files must stay hidden.
func TestGitignore_ExcludedDirCannotBeReincludedFromInside(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "out/\n!out/kept.txt\n")
	mustMkdir(t, filepath.Join(root, "out"))
	mustMkdir(t, filepath.Join(root, "out", "deep"))
	mustWrite(t, filepath.Join(root, "out", ".gitignore"), "!also.txt\n")
	mustWrite(t, filepath.Join(root, "out", "open.txt"), "in a tab")
	mustWrite(t, filepath.Join(root, "out", "kept.txt"), "negated by the parent")
	mustWrite(t, filepath.Join(root, "out", "also.txt"), "negated from inside")
	mustWrite(t, filepath.Join(root, "out", "deep", "far.txt"), "two levels in")

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.SetOpenFiles([]string{filepath.Join(root, "out", "open.txt")})
	tr.Refresh()
	out := findChild(tr.Root, "out")
	if out == nil {
		t.Fatal("the directory holding an open tab must be reachable")
	}
	tr.Toggle(out)
	if findChild(out, "open.txt") == nil {
		t.Fatal("the open file must be a row")
	}
	for _, name := range []string{"kept.txt", "also.txt", "deep"} {
		if findChild(out, name) != nil {
			t.Fatalf("%q sits in an excluded directory and no negation can re-include it", name)
		}
	}
	if !tr.dirExcluded(filepath.Join(root, "out", "deep")) {
		t.Fatal("dirExcluded must see an excluded ancestor, not only the directory itself")
	}
	if tr.dirExcluded(root) {
		t.Fatal("the project root is never excluded")
	}
}

// TestGitignore_SymlinkIsMatchedAsAFile pins that a trailing-slash
// ("directories only") pattern leaves a symlink alone even when it
// points at a directory. Git sees the link, not its target, so it
// tracks such a link; the tree resolves links to decide how a row
// expands, and must not let that leak into the ignore decision.
func TestGitignore_SymlinkIsMatchedAsAFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".gitignore"), "dist/\n")
	mustMkdir(t, filepath.Join(root, "real"))
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "dist"))

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if findChild(tr.Root, "dist") == nil {
		t.Fatal("a symlink named dist is not a directory to git and `dist/` must not hide it")
	}
}

// treeGitRootIgnore is the root .gitignore of the repo the tree-vs-git
// differential runs in: every pattern class the audit found the old
// matcher misreading, next to the ordinary ones. The escaped-trailing-
// space line is spliced in as an interpreted string so that no editor's
// strip-trailing-whitespace pass can quietly delete the case.
const treeGitRootIgnore = `# comment line
*.log
!keep.log
build/
/root-only
doc/**/*.pdf
**/tmp
src/gen
*/b
a+b
m|n
/
  lead
a?c
[!ab].txt
(z)
x$y
x^
` + "trail\\ \n" + `\#hash
nested/*.dat
`

// treeGitNestedIgnore is nested/.gitignore for the differential: its
// negations override the root file and its anchor is relative to
// nested/, not to the repo.
const treeGitNestedIgnore = `!important.log
!kept.dat
local.cfg
/anchored
`

// treeGitCandidates lists every path the differential asks about. A
// trailing slash marks a directory; everything else is a file. Each
// audited divergence appears as a pair — the path the old matcher
// wrongly hid or showed, and its counterpart on the other side — and no
// candidate is a dotfile or a name on the tree's fixed noise list, since
// those are filtered (or exempted) on a different axis than gitignore.
func treeGitCandidates() []string {
	return []string{
		"main.go",
		"app.log", "keep.log", "sub/app.log", "sub/keep.log",
		"build/", "build/out.o", "build/kept.o", "sub/build/", "sub/build/x",
		"root-only", "sub/root-only",
		"doc/x.pdf", "doc/a/b/x.pdf", "x.pdf", "other/doc/x.pdf", "doc/a/readme.md",
		"tmp/", "tmp/f", "q/r/tmp/", "q/r/tmp/f", "q/r/tmpl",
		"src/gen/", "src/gen/f", "p/src/gen/", "p/src/gen/f",
		"y/b", "x/a/b",
		"a+b", "aab",
		"m|n", "m",
		"lead", "  lead",
		"abc", "ac",
		"c.txt", "a.txt", "b.txt",
		"(z)", "z",
		"x$y", "xy",
		"x^",
		"trail ", "trail",
		"#hash",
		"nested/one.dat", "nested/kept.dat", "nested/important.log", "nested/other.log",
		"nested/local.cfg", "nested/deep/local.cfg",
		"nested/anchored", "nested/deep/anchored",
		"nested/deep/two.dat",
	}
}

// buildTreeGitRepo materialises the differential fixture under a fresh
// t.TempDir() as a real git repository and returns its root. It also
// drops a .gitignore inside the excluded build/ directory, which git
// never reads — its `!kept.o` must re-include nothing.
func buildTreeGitRepo(t *testing.T, env []string) string {
	t.Helper()
	root := t.TempDir()
	initCmd := exec.Command("git", "-C", root, "init", "-q")
	initCmd.Env = env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		mustWrite(t, abs, body)
	}
	for _, c := range treeGitCandidates() {
		if dir, ok := strings.CutSuffix(c, "/"); ok {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
			continue
		}
		write(c, "x")
	}
	write(".gitignore", treeGitRootIgnore)
	write("nested/.gitignore", treeGitNestedIgnore)
	write("build/.gitignore", "!kept.o\n")
	return root
}

// treeVisiblePaths expands every directory the tree is willing to show
// and returns the set of root-relative, slash-separated paths that
// ended up as nodes — i.e. everything the sidebar could ever paint.
func treeVisiblePaths(t *testing.T, tr *Tree) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			rel, err := filepath.Rel(tr.Root.Path, c.Path)
			if err != nil {
				t.Fatalf("rel %s: %v", c.Path, err)
			}
			seen[filepath.ToSlash(rel)] = true
			if c.IsDir {
				if !c.Expanded {
					tr.Toggle(c)
				}
				walk(c)
			}
		}
	}
	walk(tr.Root)
	return seen
}

// TestGitignore_TreeAgreesWithGitCheckIgnore is the fence for the whole
// filter: for every candidate, "is it missing from the fully expanded
// tree" must equal `git check-ignore -q`. It runs the real path — scan,
// cacheIgnore, the nested chain, dirExcluded — against real git in one
// repo where all the patterns coexist, so a matcher that is right line
// by line but wrong in how files and directories combine still fails.
//
// Under the library this replaced, the bare "/" line alone hid every
// candidate in this fixture; its pattern-by-pattern misreadings (both
// tracked paths hidden and ignored paths shown) are pinned one row at a
// time in internal/gitignore's corpus. git is a hard requirement for
// the comparison, hence the skip.
func TestGitignore_TreeAgreesWithGitCheckIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; cannot compare against ground truth")
	}
	// No global or system git config: a user-level excludesFile is git
	// configuration the tree deliberately does not read, and it must not
	// leak into the ground truth either.
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home, "XDG_CONFIG_HOME="+home,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	root := buildTreeGitRepo(t, env)

	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	visible := treeVisiblePaths(t, tr)

	hidden, shown := 0, 0
	for _, c := range treeGitCandidates() {
		rel := strings.TrimSuffix(c, "/")
		// Asked without the trailing slash: git reads the type off the
		// disk, and a slash-terminated query matches patterns against
		// an empty basename (a bare "/" then "ignores" every directory).
		cmd := exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel)
		cmd.Env = env
		runErr := cmd.Run()
		var exitErr *exec.ExitError
		gitIgnored := runErr == nil
		if runErr != nil && !(errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1) {
			t.Fatalf("git check-ignore -- %q: %v (harness assumes exit 0 or 1)", rel, runErr)
		}
		treeHidden := !visible[rel]
		if treeHidden != gitIgnored {
			t.Errorf("%q: tree hidden=%v but git ignored=%v", c, treeHidden, gitIgnored)
		}
		if gitIgnored {
			hidden++
		} else {
			shown++
		}
	}
	// A fixture that drifted into all-ignored or all-tracked would pass
	// the loop above while proving nothing.
	if hidden < 15 || shown < 15 {
		t.Fatalf("fixture lost its balance: %d ignored, %d not — both sides need real coverage", hidden, shown)
	}
	t.Logf("tree and git agree on %d candidates (%d ignored, %d not)", hidden+shown, hidden, shown)
}
