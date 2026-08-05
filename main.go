// =============================================================================
// File: main.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Command skiff is Skiff — an opinionated, mouse-first terminal code editor.
// It is designed for the SSH-into-a-box workflow: a single static binary,
// drop it on the remote host, run it inside tmux/zellij, and you get a
// VS-Code-shaped UI (file tree, tabs, syntax highlighting, status bar) you
// can drive almost entirely with the mouse.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnlam90/skiff/internal/app"
	"github.com/johnlam90/skiff/internal/version"
)

// cliAction is the high-level decision the arg parser hands back: edit
// (start the editor), version (print and exit), or help (print and exit).
// Pulling this out of main keeps the arg-resolution pure and testable
// without dragging in tcell.
type cliAction string

const (
	actionEdit    cliAction = "edit"
	actionVersion cliAction = "version"
	actionHelp    cliAction = "help"
)

// cliResult bundles everything resolveArgs hands back: which top-level
// action to run, where to root the editor, which file (if any) to open
// in the first tab, and any user-facing error to surface before exit.
type cliResult struct {
	Action   cliAction
	RootDir  string
	OpenFile string // empty when no file was named (or for non-edit actions)
	OpenLine int    // 1-based line from a "file:42" argument; 0 = none
	Err      error
}

// resolveArgs parses the editor's tiny CLI surface. Exactly one
// argument is accepted, and it can be:
//
//   - a flag (--version / -v / --help / -h) → print-and-exit action
//   - a directory path → use as the editor's root
//   - a file path → single-file mode: open the file, no sidebar, no
//     project index (see main)
//   - a missing path → assume "skiff foo.go" means "create foo.go" —
//     same intuition as `vim foo.go` on a non-existent file.
//
// Anything beyond that first argument is REFUSED with an error naming
// it, rather than opened as extra tabs. Two reasons, in order:
//
//  1. A file argument means single-file mode, which exists precisely so
//     that `skiff notes.md` doesn't walk and index the directory around
//     it. Opening N files as N tabs forces a project root out of thin
//     air, and the only principled candidate — the common ancestor of
//     the arguments — is `/` or `$HOME` for the very ordinary
//     `skiff pkg/a.go ../other/b.go`. Indexing those to satisfy an
//     argument list is a worse surprise than an error.
//  2. Skiff is mouse-first: the tree and the file finder (Esc p) are the
//     designed way to open a second file, and they're one gesture away
//     once the editor is up. A batch-open CLI would be a second, worse
//     door to something the UI already does well.
//
// The refusal is recoverable in one keystroke (up-arrow, edit the line);
// silently dropping `b.go` is not even detectable.
//
// Pure function; no IO beyond os.Stat. Returns a result the caller acts
// on — keeps main() short and lets tests pin behavior without launching
// a real tcell screen.
func resolveArgs(args []string) cliResult {
	if len(args) == 0 {
		return cliResult{Action: actionEdit, RootDir: "."}
	}
	// Flags are matched before any path work so `--version` never
	// touches the disk. They're deliberately still position-sensitive:
	// with the arity check below, `skiff main.go --version` now reports
	// the stray flag instead of quietly editing main.go and dropping it,
	// which is the whole complaint — a two-token CLI doesn't need a
	// general flag parser to stop lying to the user.
	switch args[0] {
	case "--version", "-v", "-V", "version":
		if len(args) == 1 {
			return cliResult{Action: actionVersion}
		}
	case "--help", "-h", "help":
		if len(args) == 1 {
			return cliResult{Action: actionHelp}
		}
	}
	if len(args) > 1 {
		return cliResult{Err: extraArgsError(args[1:])}
	}

	target := args[0]
	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		return cliResult{Action: actionEdit, RootDir: target}
	case err == nil:
		// Existing file — root at its parent so the file tree shows
		// useful context, then open the file as the first tab.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	case os.IsNotExist(err):
		// Missing path — before treating it as a new file, try a
		// trailing ":<line>" spec (`skiff main.go:42`). Only when the
		// prefix names an existing file does the suffix count as a line
		// number; otherwise the colon is just an unusual file name.
		if prefix, line, ok := splitLineSuffix(target); ok {
			if pInfo, pErr := os.Stat(prefix); pErr == nil && !pInfo.IsDir() {
				dir := filepath.Dir(prefix)
				if dir == "" {
					dir = "."
				}
				return cliResult{Action: actionEdit, RootDir: dir, OpenFile: prefix, OpenLine: line}
			}
		}
		// Genuinely new file (same intent as vim). The Tab buffer starts
		// empty and is written to disk on first save.
		dir := filepath.Dir(target)
		if dir == "" {
			dir = "."
		}
		return cliResult{Action: actionEdit, RootDir: dir, OpenFile: target}
	default:
		// Real IO error (permissions, EIO, etc.) — surface it instead of
		// silently swallowing it into a "directory not found" later.
		return cliResult{Err: err}
	}
}

// extraArgsError builds the message for a command line carrying more
// than one argument. It quotes every argument it refuses — the point is
// that the user sees exactly what would otherwise have been dropped —
// and names the two in-editor ways to open the rest. An extra argument
// starting with "-" earns a second clause: that case is a misplaced
// flag, not a second file.
func extraArgsError(extra []string) error {
	quoted := make([]string, len(extra))
	flagLike := false
	for i, arg := range extra {
		quoted[i] = strconv.Quote(arg)
		if strings.HasPrefix(arg, "-") {
			flagLike = true
		}
	}
	noun := "argument"
	if len(extra) > 1 {
		noun = "arguments"
	}
	msg := fmt.Sprintf("unexpected %s: %s — skiff opens one file or directory at a time; open the rest from the file tree or the finder (Esc p)",
		noun, strings.Join(quoted, ", "))
	if flagLike {
		msg += "; flags must come first, as in `skiff --version`"
	}
	return errors.New(msg)
}

// splitLineSuffix splits "path:42" into ("path", 42, true). Line numbers
// are 1-based, so ":0", a bare trailing ":", or non-digits after the last
// colon all return ok=false and the argument stays a literal file name.
func splitLineSuffix(s string) (prefix string, line int, ok bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return s[:i], n, true
}

// printHelp writes a short usage block to stdout. Kept brief on purpose:
// the editor is itself the help — once running, the ≡ menu lists every
// action.
func printHelp() {
	fmt.Println(`Skiff — opinionated mouse-first terminal code editor.

Usage:
  skiff                     Open the current directory.
  skiff <directory>         Open a project directory.
  skiff <file>              Open one file in single-file mode: no sidebar and
                            no project index, so the surrounding directory is
                            never walked.
  skiff <file>:<line>       Open a file at a line, e.g. skiff main.go:42.
  skiff --version           Print the version and exit.
  skiff --help              Print this help and exit.

Exactly one directory or file is accepted, and a flag must come first.
Extra arguments are refused rather than ignored — open the rest from the
file tree or the finder (Esc p) once the editor is up.

Once running, click ≡ (top-left), right-click anywhere, or double-tap Esc
for the action menu. See https://github.com/johnlam90/skiff for
hotkeys and the full feature list.`)
}

// main routes to the action resolveArgs picked. Edit is by far the
// common path; the print-and-exit branches stay tiny and side-effect
// free so a sanity script or CI check can call --version without
// initialising a tcell screen.
func main() {
	res := resolveArgs(os.Args[1:])
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "skiff:", res.Err)
		os.Exit(1)
	}

	switch res.Action {
	case actionVersion:
		fmt.Println("skiff", version.Version)
		return
	case actionHelp:
		printHelp()
		return
	}

	// Single-file mode: when the user invoked `skiff somefile.md`,
	// skip building the file tree and project file index entirely.
	// They asked for one file — don't pay the CPU to walk the
	// surrounding directory just so we can render a sidebar they
	// didn't ask for. The action-menu sidebar toggle is filtered out
	// in this mode too; see (*App).hasTree.
	var (
		a   *app.App
		err error
	)
	if res.OpenFile != "" {
		a, err = app.NewSingleFile(res.OpenFile)
	} else {
		a, err = app.New(res.RootDir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "skiff: failed to start:", err)
		os.Exit(1)
	}
	defer a.Close()

	// A `file:42` argument jumps the freshly-opened tab before the first
	// draw; Render's EnsureVisible brings the line on screen.
	if res.OpenLine > 0 {
		a.OpenFileAtLine(res.OpenFile, res.OpenLine)
	}

	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "skiff:", err)
		os.Exit(1)
	}
}
