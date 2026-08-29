// =============================================================================
// File: internal/session/session_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the per-project session store: round-tripping, the
// isolation guarantee that one project's corrupt file can't harm its
// neighbours, the legacy single-file migration, filename hashing, and
// the most-recent-N pruning rule.

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain redirects every XDG base directory to a throwaway root before
// any test runs. Most tests here already call redirectStore per-test, but
// TestMain is the backstop that makes a forgotten redirect in a future
// test harmless instead of a write into the developer's real
// ~/.local/state/skiff/sessions/.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skiff-test-xdg-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// redirectStore points the store at a temp dir for the test.
func redirectStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

// pathFor resolves a project's session file, failing the test if the
// environment override isn't in place.
func pathFor(t *testing.T, root string) string {
	t.Helper()
	p, err := projectPath(root)
	if err != nil {
		t.Fatalf("projectPath(%q): %v", root, err)
	}
	return p
}

// TestSaveLoadRoundTrip pins the basic contract: what Save writes,
// Load returns, keyed by project root, without clobbering siblings.
func TestSaveLoadRoundTrip(t *testing.T) {
	redirectStore(t)
	p := Project{
		Tabs:         []TabState{{Path: "a.go", Line: 3, Col: 1, ScrollY: 2}},
		Active:       0,
		ActivePath:   "a.go",
		Expanded:     []string{"internal", "internal/app"},
		SidebarWidth: 28,
		SidebarShown: true,
		SavedAt:      time.Now(),
	}
	if err := Save("/proj/one", p); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := Save("/proj/two", Project{SavedAt: time.Now()}); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got, ok := Load("/proj/one")
	if !ok {
		t.Fatal("expected a session for /proj/one")
	}
	if len(got.Tabs) != 1 || got.Tabs[0] != p.Tabs[0] {
		t.Fatalf("tabs: got %+v", got.Tabs)
	}
	if got.ActivePath != "a.go" {
		t.Fatalf("activePath lost: %q", got.ActivePath)
	}
	if len(got.Expanded) != 2 || got.SidebarWidth != 28 || !got.SidebarShown {
		t.Fatalf("fields lost: %+v", got)
	}
	if _, ok := Load("/proj/absent"); ok {
		t.Fatal("unknown root should have no session")
	}
}

// TestSaveWritesSelfDescribingFile: the filename is only a hash, so the
// contents must carry the version and the root they belong to, with the
// project fields flattened alongside them.
func TestSaveWritesSelfDescribingFile(t *testing.T) {
	redirectStore(t)
	if err := Save("/proj/one/", Project{SidebarWidth: 22, SavedAt: time.Now()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(pathFor(t, "/proj/one"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["version"] != float64(storeVersion) {
		t.Fatalf("version: got %v", raw["version"])
	}
	// The trailing slash must normalise away, or the same project would
	// get two files depending on how it was opened.
	if raw["root"] != "/proj/one" {
		t.Fatalf("root: got %v", raw["root"])
	}
	if raw["sidebarWidth"] != float64(22) {
		t.Fatalf("project fields should be inline: %v", raw)
	}
}

// TestDistinctRootsDistinctFiles: two project roots must never share a
// file, and a trailing separator must not fork one project into two.
func TestDistinctRootsDistinctFiles(t *testing.T) {
	if a, b := projectFileName("/proj/one"), projectFileName("/proj/two"); a == b {
		t.Fatalf("distinct roots collided on %q", a)
	}
	if a, b := projectFileName("/proj/one"), projectFileName("/proj/one/"); a != b {
		t.Fatalf("trailing slash forked the file: %q vs %q", a, b)
	}
	if got := projectFileName("/proj/one"); len(got) != len("0123456789abcdef.json") {
		t.Fatalf("unexpected filename shape: %q", got)
	}
}

// TestLoadCorruptFileFreshStart: a mangled session file must read as
// empty, must not be deleted, and the next Save must recover it.
func TestLoadCorruptFileFreshStart(t *testing.T) {
	redirectStore(t)
	path := pathFor(t, "/proj")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{nope"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := Load("/proj"); ok {
		t.Fatal("corrupt file should load as empty")
	}
	// Reading must never destroy user data — a parse bug should be
	// recoverable from what's still on disk.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Load deleted the corrupt file: %v", err)
	}
	if err := Save("/proj", Project{SavedAt: time.Now()}); err != nil {
		t.Fatalf("save over corrupt file: %v", err)
	}
	if _, ok := Load("/proj"); !ok {
		t.Fatal("save should have recovered the file")
	}
}

// TestCorruptFileLeavesSiblingsAlone is the regression test for the bug
// that motivated one-file-per-project: with a shared store, a corrupt
// file read as an empty map and the next Save rewrote the store with
// only the current project in it, wiping every other project's tabs.
func TestCorruptFileLeavesSiblingsAlone(t *testing.T) {
	redirectStore(t)
	other := Project{
		Tabs:    []TabState{{Path: "keep.go", Line: 7}},
		SavedAt: time.Now(),
	}
	if err := Save("/proj/other", other); err != nil {
		t.Fatalf("seed sibling: %v", err)
	}
	otherPath := pathFor(t, "/proj/other")
	before, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}

	corrupt := pathFor(t, "/proj/corrupt")
	if err := os.WriteFile(corrupt, []byte("}}truncated"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, ok := Load("/proj/corrupt"); ok {
		t.Fatal("corrupt file should load as empty")
	}
	if err := Save("/proj/corrupt", Project{SavedAt: time.Now()}); err != nil {
		t.Fatalf("save after corrupt load: %v", err)
	}

	after, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatalf("sibling file gone: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("sibling file rewritten:\nbefore %s\nafter  %s", before, after)
	}
	got, ok := Load("/proj/other")
	if !ok || len(got.Tabs) != 1 || got.Tabs[0].Line != 7 {
		t.Fatalf("sibling session damaged: %+v ok=%v", got, ok)
	}
}

// TestMigrateLegacyStore: the old shared sessions.json is fanned out
// into per-project files on first use and renamed aside (never deleted),
// so upgrading doesn't lose anyone's tabs and doesn't re-run forever.
func TestMigrateLegacyStore(t *testing.T) {
	dir := redirectStore(t)
	legacy := filepath.Join(dir, "skiff", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store := map[string]Project{
		"/proj/a": {Tabs: []TabState{{Path: "a.go", Line: 4}}, SidebarWidth: 21, SavedAt: time.Now()},
		"/proj/b": {SidebarWidth: 99, SavedAt: time.Now()},
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(legacy, data, 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// A project that already has a per-project file must win: the
	// legacy copy is staler by definition.
	if err := Save("/proj/b", Project{SidebarWidth: 30, SavedAt: time.Now()}); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	got, ok := Load("/proj/a")
	if !ok || got.SidebarWidth != 21 || len(got.Tabs) != 1 || got.Tabs[0].Line != 4 {
		t.Fatalf("legacy project not migrated: %+v ok=%v", got, ok)
	}
	if b, ok := Load("/proj/b"); !ok || b.SidebarWidth != 30 {
		t.Fatalf("migration clobbered a newer per-project file: %+v ok=%v", b, ok)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy store should have been renamed away, stat err=%v", err)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Fatalf("legacy store should be kept as .migrated: %v", err)
	}
	if _, err := os.Stat(pathFor(t, "/proj/a")); err != nil {
		t.Fatalf("per-project file missing after migration: %v", err)
	}
}

// TestMigrateLegacyStoreCorrupt: an unparseable legacy store is still
// renamed aside, otherwise every Load and Save would retry it forever.
func TestMigrateLegacyStoreCorrupt(t *testing.T) {
	dir := redirectStore(t)
	legacy := filepath.Join(dir, "skiff", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("{nope"), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if _, ok := Load("/proj/a"); ok {
		t.Fatal("corrupt legacy store should yield no session")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("corrupt legacy store should be renamed aside, stat err=%v", err)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Fatalf("corrupt legacy store should be kept as .migrated: %v", err)
	}
}

// TestPruneKeepsNewestN: the sessions dir caps at maxProjects, dropping
// the stalest SavedAt files first.
func TestPruneKeepsNewestN(t *testing.T) {
	redirectStore(t)
	base := time.Now().Add(-time.Hour)
	for i := range maxProjects + 10 {
		root := fmt.Sprintf("/proj/%03d", i)
		if err := Save(root, Project{SavedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	// The oldest ten (000–009) must be gone; the newest survive.
	if _, ok := Load("/proj/000"); ok {
		t.Fatal("stalest entry should have been pruned")
	}
	if _, ok := Load(fmt.Sprintf("/proj/%03d", maxProjects+9)); !ok {
		t.Fatal("newest entry must survive pruning")
	}
	dir, err := sessionsDir()
	if err != nil {
		t.Fatalf("sessionsDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != maxProjects {
		t.Fatalf("session files: got %d, want %d", len(entries), maxProjects)
	}
}

// TestReadProjectFileRejectsFutureVersion: an unknown schema version
// reads as "no session" rather than being mis-parsed into half-state.
func TestReadProjectFileRejectsFutureVersion(t *testing.T) {
	redirectStore(t)
	path := pathFor(t, "/proj")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(projectFile{Version: storeVersion + 1, Root: "/proj"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := Load("/proj"); ok {
		t.Fatal("future version should not load")
	}
}

// TestStateDirIsRedirectedDuringTests pins the isolation contract this
// suite relies on: with TestMain's XDG redirect in place, the session
// store must never resolve under the developer's real home. If this
// fails, some test cleared XDG_STATE_HOME or TestMain was removed —
// either way the suite is about to write into ~/.local/state/skiff.
func TestStateDirIsRedirectedDuringTests(t *testing.T) {
	dir, err := stateDir()
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if strings.HasPrefix(dir, filepath.Join(home, ".local")) {
		t.Fatalf("stateDir resolved under the real home during tests: %s", dir)
	}
}
