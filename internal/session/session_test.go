// =============================================================================
// File: internal/session/session_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the per-project session store: round-tripping, corrupt-file
// tolerance, and the most-recent-N pruning rule.

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// redirectStore points the store at a temp dir for the test.
func redirectStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

// TestSaveLoadRoundTrip pins the basic contract: what Save writes,
// Load returns, keyed by project root, without clobbering siblings.
func TestSaveLoadRoundTrip(t *testing.T) {
	redirectStore(t)
	p := Project{
		Tabs:         []TabState{{Path: "a.go", Line: 3, Col: 1, ScrollY: 2}},
		Active:       0,
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
	if len(got.Expanded) != 2 || got.SidebarWidth != 28 || !got.SidebarShown {
		t.Fatalf("fields lost: %+v", got)
	}
	if _, ok := Load("/proj/absent"); ok {
		t.Fatal("unknown root should have no session")
	}
}

// TestLoadCorruptFileFreshStart: a mangled store must read as empty,
// and the next Save must recover it — never an error, never a crash.
func TestLoadCorruptFileFreshStart(t *testing.T) {
	dir := redirectStore(t)
	path := filepath.Join(dir, "skiff", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{nope"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := Load("/proj"); ok {
		t.Fatal("corrupt store should load as empty")
	}
	if err := Save("/proj", Project{SavedAt: time.Now()}); err != nil {
		t.Fatalf("save over corrupt store: %v", err)
	}
	if _, ok := Load("/proj"); !ok {
		t.Fatal("save should have recovered the store")
	}
}

// TestPruneKeepsNewestN: the store caps at maxProjects, dropping the
// stalest SavedAt entries first.
func TestPruneKeepsNewestN(t *testing.T) {
	redirectStore(t)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxProjects+10; i++ {
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
}
