// =============================================================================
// File: internal/filemanager/filemanager.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package filemanager is the one owner of skiff's file operations: create,
// rename, trash / restore, move, copy and duplicate, over a single
// project root. Every operation returns a Changeset — the paths it
// added, removed or moved — so the app has exactly one thing to react
// to (App.applyChangeset repoints tabs, refreshes the tree, the git
// tint, the finder index and the session) instead of six hand-copied
// tails that drift apart.
//
// The guards are the module's, not the callers': no operation touches
// the project root, escapes it, pastes a folder into itself or accepts
// a name with a separator. Callers get an error to flash, never a
// half-applied op to clean up.
//
// The package has no tcell, theme, app or filetree imports on purpose.
// It is a pure disk module: everything here is testable against a
// t.TempDir() with no screen, and the app is its only adapter.
package filemanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TrashPrefix marks in-place session-trash entries: when moving a
// deleted item to the per-session temp trash dir fails (a cross-device
// rename), the item is renamed to a hidden sibling carrying this prefix
// instead. The manager produces the names, so it owns the constant; the
// tree and the finder filter it (filetree.TrashPrefix is this value) so
// a trashed item never resurfaces in the UI before Restore puts it back.
const TrashPrefix = ".skifftrash-"

// The refusal sentinels. Each is a complete sentence the app can flash
// after a "<Verb> failed: " prefix, and callers can errors.Is on them
// when a refusal deserves a different flash than a disk error.
var (
	// ErrProjectRoot: the project root is never renamed, trashed,
	// moved, copied or duplicated — it is the editor's own ground.
	ErrProjectRoot = errors.New("refusing to touch the project root")
	// ErrOutsideRoot: a path (source or destination) that climbs out
	// of the project root. The sidebar never shows anything out there,
	// so an op landing there would be invisible to the user.
	ErrOutsideRoot = errors.New("path is outside the project")
	// ErrSeparator: a rename is a sibling rename only; a name with a
	// separator would be a move in disguise.
	ErrSeparator = errors.New("name can't contain a path separator")
	// ErrEmptyName: a rename to "" is a typo, not a request.
	ErrEmptyName = errors.New("name is empty")
	// ErrIntoItself: pasting a folder into itself (or a descendant,
	// through any symlink) would copy the folder into its own growing
	// output forever.
	ErrIntoItself = errors.New("can't paste a folder into itself")
	// ErrNoTrash: Restore with nothing in the session trash.
	ErrNoTrash = errors.New("nothing to restore")
)

// Move records one path that changed name: a file rename, a folder
// rename, or a paste-move. A folder move is ONE Move for the folder
// pair; the app widens it to every open tab underneath.
type Move struct {
	Old string
	New string
}

// Changeset is what an operation did to the disk, in absolute cleaned
// paths. A create, a copy and a restore are one Added; a trash is one
// Removed; a rename and a move are one Moved. Empty when an operation
// refused or failed before touching anything.
type Changeset struct {
	Added   []string
	Removed []string
	Moved   []Move
}

// Empty reports whether the changeset names no path at all.
func (cs Changeset) Empty() bool {
	return len(cs.Added) == 0 && len(cs.Removed) == 0 && len(cs.Moved) == 0
}

// Manager performs file operations under one project root and owns the
// session trash that makes Trash undoable.
//
// Thread-safety, by field rather than by lock: root and rename are
// immutable after New, and Move, Copy and Duplicate read nothing else
// — so the app runs those three on a background goroutine (a
// node_modules-sized paste must not freeze the editor) while the main
// loop keeps calling HasTrash / LastTrashed to draw the menu. The trash
// methods mutate the entry stack and are main-loop only. A mutex would
// make the claim structural, but it would also park the main loop
// behind a multi-gigabyte copy the moment it asked for the menu label.
type Manager struct {
	// root is the absolute, cleaned project root every guard measures
	// against.
	root string

	// trashDir is the per-session temp directory trashed items move
	// into, created lazily on the first Trash so a session that never
	// deletes never litters $TMPDIR. "" until then, or when MkdirTemp
	// failed and every entry fell back to an in-place sibling.
	trashDir string
	// trashed is the restore stack, newest last.
	trashed []trashEntry
	// seq numbers stored trash entries so two deletes of the same
	// basename never collide in the trash dir — and never reuse a
	// number, even after a Restore pops the stack.
	seq int

	// rename is the os.Rename seam. Tests override it to simulate a
	// cross-device rename (EXDEV), which is the one failure the primary
	// trash path and the paste-move path both fall back from; it cannot
	// be provoked inside one t.TempDir().
	rename func(oldpath, newpath string) error
}

// trashEntry records one deleted item: where it lived, and where the
// session trash is keeping it.
type trashEntry struct {
	orig   string
	stored string
}

// New returns a Manager over root. root is made absolute here, once,
// so a project opened as "." compares equal to the tree's absolute root
// in every guard — the spelling mismatch is exactly how the project
// root once passed as a deletable subfolder.
func New(root string) *Manager {
	return &Manager{root: abs(root), rename: os.Rename}
}

// Root returns the absolute project root the manager guards.
func (m *Manager) Root() string { return m.root }

// IsRoot reports whether path names the project root in any spelling.
func (m *Manager) IsRoot(path string) bool { return abs(path) == m.root }

// Contains reports whether path lives under the project root — the root
// itself included, because a session entry naming the root is
// legitimate, not an escape. See Within for the rule.
func (m *Manager) Contains(path string) bool { return Within(m.root, abs(path)) }

// Within reports whether candidate, made absolute and cleaned, still
// lives under root. root itself counts as within (filepath.Rel returns
// "."). Exported because the app applies the same containment rule to
// what it will not do for the user — a "New file" name that climbs out
// of the folder the prompt promised.
func Within(root, candidate string) bool {
	rel, err := filepath.Rel(abs(root), abs(candidate))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// guard is the preamble every op on an existing entry shares: the path
// made absolute, then refused when it is the root or escapes it.
func (m *Manager) guard(path string) (string, error) {
	p := abs(path)
	if p == m.root {
		return "", ErrProjectRoot
	}
	if !Within(m.root, p) {
		return "", ErrOutsideRoot
	}
	return p, nil
}

// Create makes an empty file at path. Parent directories must already
// exist — the manager never silently mkdirs, so the user can't create
// folders they didn't realise they were making. O_EXCL refuses to
// clobber an existing file.
func (m *Manager) Create(path string) (Changeset, error) {
	p := abs(path)
	if p == m.root || !Within(m.root, p) {
		return Changeset{}, ErrOutsideRoot
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		// ENOENT here means the parent doesn't exist; say that instead
		// of the noisy "open <path>: no such file or directory".
		if os.IsNotExist(err) {
			return Changeset{}, fmt.Errorf("%s doesn't exist — create it first", filepath.Dir(p))
		}
		return Changeset{}, err
	}
	if err := f.Close(); err != nil {
		return Changeset{}, err
	}
	return Changeset{Added: []string{p}}, nil
}

// Rename gives the file or directory at path the sibling name newName.
// Sibling only: a separator in newName is refused rather than treated
// as a move. Renaming to the current name is a no-op with an empty
// Changeset — the prompt pre-fills the old name, so an unedited submit
// must not fail. An existing destination is never clobbered.
func (m *Manager) Rename(path, newName string) (Changeset, error) {
	p, err := m.guard(path)
	if err != nil {
		return Changeset{}, err
	}
	if newName == "" {
		return Changeset{}, ErrEmptyName
	}
	if strings.ContainsAny(newName, string(os.PathSeparator)+`/\`) {
		return Changeset{}, ErrSeparator
	}
	np := filepath.Join(filepath.Dir(p), newName)
	if np == p {
		return Changeset{}, nil
	}
	if _, err := os.Lstat(np); err == nil {
		return Changeset{}, fmt.Errorf("a file named %q already exists", newName)
	}
	if err := m.rename(p, np); err != nil {
		return Changeset{}, err
	}
	return Changeset{Moved: []Move{{Old: p, New: np}}}, nil
}

// abs returns path absolute and cleaned, falling back to a plain Clean
// when the working directory cannot be resolved — the same best-effort
// rule every other path helper in the editor follows.
func abs(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return a
	}
	return filepath.Clean(path)
}
