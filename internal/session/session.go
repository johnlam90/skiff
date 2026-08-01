// =============================================================================
// File: internal/session/session.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-08-01
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package session persists per-project editor state — open tabs (with
// cursor and scroll), the active tab, expanded tree folders, and the
// sidebar — so reopening a project puts the user back where they left
// off. This is state, not configuration: skiff stays dotfile-free, but
// remembering where you were is table stakes.
//
// Storage is one JSON file, $XDG_STATE_HOME/skiff/sessions.json
// (falling back to ~/.local/state/skiff/), keyed by the project's
// absolute root path and pruned to the most recent maxProjects entries.
// Every failure mode degrades to "no session" — a corrupt file must
// never block startup.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// TabState is one remembered tab: a project-relative path plus the view
// state needed to land the user back on their line.
type TabState struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	ScrollY int    `json:"scrollY"`
}

// Project is everything remembered about one project root.
type Project struct {
	Tabs         []TabState `json:"tabs,omitempty"`
	Active       int        `json:"active"`
	Expanded     []string   `json:"expanded,omitempty"`
	SidebarWidth int        `json:"sidebarWidth"`
	SidebarShown bool       `json:"sidebarShown"`
	SavedAt      time.Time  `json:"savedAt"`
}

// maxProjects caps the store: the 50 most recently saved projects
// survive, older ones age out on the next save.
const maxProjects = 50

// storePath resolves the sessions file location, honouring
// $XDG_STATE_HOME so tests (and unusual setups) can redirect it.
func storePath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skiff", "sessions.json"), nil
}

// readStore loads the whole store, mapping every failure (missing file,
// bad JSON) to an empty map — sessions are a comfort, never an error.
func readStore() map[string]Project {
	path, err := storePath()
	if err != nil {
		return map[string]Project{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]Project{}
	}
	var store map[string]Project
	if err := json.Unmarshal(data, &store); err != nil || store == nil {
		return map[string]Project{}
	}
	return store
}

// Load returns the remembered session for the project rooted at
// rootAbs, and whether one existed.
func Load(rootAbs string) (Project, bool) {
	store := readStore()
	p, ok := store[rootAbs]
	return p, ok
}

// Save upserts rootAbs's session and rewrites the store, pruning to the
// maxProjects most recent by SavedAt. Read-modify-write keeps sibling
// projects' sessions intact.
func Save(rootAbs string, p Project) error {
	path, err := storePath()
	if err != nil {
		return err
	}
	store := readStore()
	store[rootAbs] = p

	if len(store) > maxProjects {
		type entry struct {
			key string
			at  time.Time
		}
		entries := make([]entry, 0, len(store))
		for k, v := range store {
			entries = append(entries, entry{k, v.SavedAt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
		for _, e := range entries[maxProjects:] {
			delete(store, e.key)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
