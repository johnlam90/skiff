// =============================================================================
// File: internal/session/session.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-01
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package session persists per-project editor state — open tabs (with
// cursor and scroll), the active tab, expanded tree folders, and the
// sidebar — so reopening a project puts the user back where they left
// off. This is state, not configuration: skiff stays dotfile-free, but
// remembering where you were is table stakes.
//
// Storage is one file per project, $XDG_STATE_HOME/skiff/sessions/
// <hash>.json (falling back to ~/.local/state/skiff/), where <hash> is
// derived from the project's absolute root path. One file per project is
// deliberate: a single shared store meant a corrupt or half-written file
// took every *other* project's state down with it on the next save, and
// the read-modify-write it forced lost updates between concurrent skiff
// instances. Now a save touches exactly one project's file and nothing
// else can be collateral damage.
//
// Every failure mode degrades to "no session" — a corrupt file must
// never block startup, and must never be deleted behind the user's
// back. The directory is pruned to the most recent maxProjects files.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/johnlam90/skiff/internal/atomicfile"
)

// TabState is one remembered tab: a project-relative path plus the view
// state needed to land the user back on their line.
type TabState struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	ScrollY int    `json:"scrollY"`
	Preview bool   `json:"preview,omitempty"`
}

// Project is everything remembered about one project root. ActivePath
// is the authority for which tab gets focus; Active (an index into
// Tabs) is kept only so files written by older builds still restore
// something sane, because an index silently points at the wrong tab as
// soon as one saved file has vanished from disk.
type Project struct {
	Tabs         []TabState `json:"tabs,omitempty"`
	Active       int        `json:"active"`
	ActivePath   string     `json:"activePath,omitempty"`
	Expanded     []string   `json:"expanded,omitempty"`
	SidebarWidth int        `json:"sidebarWidth"`
	SidebarShown bool       `json:"sidebarShown"`
	SavedAt      time.Time  `json:"savedAt"`
}

// maxProjects caps the sessions directory: the 50 most recently saved
// projects survive, older ones age out on the next save.
const maxProjects = 50

// storeVersion stamps each file so a future format change can be
// recognised (and ignored) instead of mis-parsed.
const storeVersion = 1

// projectFile is the on-disk shape of one project's session: the
// Project fields inlined (embedding is how encoding/json flattens them,
// keeping the file readable), wrapped in enough metadata — version, the
// root it belongs to — that it explains itself to anyone who cats it.
// The filename is only a hash.
type projectFile struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Project
}

// stateDir resolves skiff's state directory, honouring $XDG_STATE_HOME
// so tests (and unusual setups) can redirect it.
func stateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "skiff"), nil
}

// sessionsDir is the directory holding the per-project session files.
func sessionsDir() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// legacyStorePath is the pre-split single-file store, kept only so it
// can be migrated once and renamed aside.
func legacyStorePath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions.json"), nil
}

// rootKey normalises a project root into the stable identity the
// filename hashes, so "/proj/one" and "/proj/one/" share one session.
func rootKey(rootAbs string) string {
	return filepath.Clean(rootAbs)
}

// projectFileName hashes the project root into a short, filesystem-safe
// name. A hash (not the escaped path) keeps names bounded on every OS;
// 16 hex chars of SHA-256 is far past collision risk for a directory
// that holds at most maxProjects entries.
func projectFileName(rootAbs string) string {
	sum := sha256.Sum256([]byte(rootKey(rootAbs)))
	return hex.EncodeToString(sum[:])[:16] + ".json"
}

// projectPath is the full path of one project's session file.
func projectPath(rootAbs string) (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectFileName(rootAbs)), nil
}

// readProjectFile loads one session file, mapping every failure
// (missing, unreadable, bad JSON, unknown version) to "no session". It
// never removes the file: a user who hits a parse bug should still have
// their state on disk when the bug is fixed.
func readProjectFile(path string) (Project, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Project{}, false
	}
	var pf projectFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return Project{}, false
	}
	if pf.Version != storeVersion {
		return Project{}, false
	}
	return pf.Project, true
}

// writeProjectFile serialises one project's session and swaps it into
// place atomically, so a crash mid-write can never leave half-written
// JSON — and, because the file belongs to this project alone, can never
// damage another project's state. Owner-only (0600): the file records
// every open file path and cursor position for the project, which a
// shared multi-user box shouldn't hand to other local accounts.
func writeProjectFile(path, rootAbs string, p Project) error {
	data, err := json.MarshalIndent(projectFile{
		Version: storeVersion,
		Root:    rootKey(rootAbs),
		Project: p,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0600)
}

// Load returns the remembered session for the project rooted at
// rootAbs, and whether one existed.
func Load(rootAbs string) (Project, bool) {
	migrateLegacyStore()
	path, err := projectPath(rootAbs)
	if err != nil {
		return Project{}, false
	}
	return readProjectFile(path)
}

// Save writes rootAbs's session to its own file and prunes the
// directory. No shared state is read or rewritten, which is what makes
// concurrent skiff instances and corrupt neighbours harmless.
func Save(rootAbs string, p Project) error {
	migrateLegacyStore()
	path, err := projectPath(rootAbs)
	if err != nil {
		return err
	}
	if err := writeProjectFile(path, rootAbs, p); err != nil {
		return err
	}
	pruneSessions(path)
	return nil
}

// pruneSessions keeps the maxProjects most recently saved files, oldest
// SavedAt first out. Best-effort and silent: failing to prune is not a
// reason to fail a save. keep is never removed — it is the file this
// save just wrote.
func pruneSessions(keep string) {
	dir := filepath.Dir(keep)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type stamp struct {
		name string
		at   time.Time
	}
	// Only pay for reading SavedAt out of every file once the directory
	// is actually over the cap; the common case exits here.
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) <= maxProjects {
		return
	}
	stamps := make([]stamp, 0, len(files))
	for _, name := range files {
		p, ok := readProjectFile(filepath.Join(dir, name))
		if !ok {
			// Unreadable files sort oldest so they age out naturally
			// rather than being deleted on sight.
			stamps = append(stamps, stamp{name: name})
			continue
		}
		stamps = append(stamps, stamp{name: name, at: p.SavedAt})
	}
	sort.Slice(stamps, func(i, j int) bool {
		if stamps[i].at.Equal(stamps[j].at) {
			return stamps[i].name < stamps[j].name
		}
		return stamps[i].at.After(stamps[j].at)
	})
	keepName := filepath.Base(keep)
	for _, s := range stamps[maxProjects:] {
		if s.name == keepName {
			continue
		}
		_ = os.Remove(filepath.Join(dir, s.name))
	}
}

// migrateLegacyStore converts the old single-file sessions.json into one
// file per project, exactly once: the legacy file is renamed to
// sessions.json.migrated afterwards (renamed, not deleted — it is user
// data, and a migration bug should be recoverable). Projects that
// already have a per-project file win; the legacy copy is staler by
// definition. Every failure is non-fatal — the worst case is starting a
// project fresh.
func migrateLegacyStore() {
	legacy, err := legacyStorePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		return
	}
	var store map[string]Project
	if err := json.Unmarshal(data, &store); err == nil {
		for root, p := range store {
			path, err := projectPath(root)
			if err != nil {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				continue
			}
			_ = writeProjectFile(path, root, p)
		}
	}
	// Rename even when the contents were unparseable: leaving the file
	// in place would retry the migration on every Load and Save.
	_ = os.Rename(legacy, legacy+".migrated")
}
