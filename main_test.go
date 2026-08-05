// =============================================================================
// File: main_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveArgs_NoArgsRootsCurrentDir keeps the no-arg path simple:
// "." as rootDir, no file to open, action = edit.
func TestResolveArgs_NoArgsRootsCurrentDir(t *testing.T) {
	got := resolveArgs(nil)
	if got.Action != actionEdit {
		t.Fatalf("action: got %q, want edit", got.Action)
	}
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_DirectoryArgUsesAsRoot pins the existing behaviour:
// passing a directory uses it as the editor's root.
func TestResolveArgs_DirectoryArgUsesAsRoot(t *testing.T) {
	dir := t.TempDir()
	got := resolveArgs([]string{dir})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_FileArgRootsParent is the regression test for the
// "skiff main.go" bug: a file argument should root the editor at
// the file's parent and seed an OpenFile so the user's tab is ready.
func TestResolveArgs_FileArgRootsParent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_BarefilenameRootsCwd covers the common "skiff
// foo.go" form where the path has no directory component. The
// filepath.Dir of "foo.go" is "." — without the empty-string guard
// we'd hand the editor an empty rootDir and filetree.New would fail.
func TestResolveArgs_BarefilenameRootsCwd(t *testing.T) {
	// Use a real bare filename in a temp cwd so the stat path covers
	// the existing-file branch.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.WriteFile("bare.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{"bare.txt"})
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "bare.txt" {
		t.Fatalf("OpenFile: got %q, want bare.txt", got.OpenFile)
	}
}

// TestResolveArgs_MissingFileTreatsAsNew mirrors `vim foo.go` on a
// non-existent path: open the editor at the parent dir with the file
// queued for editing — first save creates it.
func TestResolveArgs_MissingFileTreatsAsNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.go")

	got := resolveArgs([]string{target})
	if got.Err != nil {
		t.Fatalf("missing file should not be an error, got %v", got.Err)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_FileLineSuffix covers `skiff main.go:42`: when the
// literal path doesn't exist but the prefix before ":42" does, the
// suffix is a line request, not part of the file name.
func TestResolveArgs_FileLineSuffix(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target + ":42"})
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
	if got.OpenLine != 42 {
		t.Fatalf("OpenLine: got %d, want 42", got.OpenLine)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
}

// TestResolveArgs_FileLineMissingPrefix pins the fallback: when neither
// "foo:9" nor "foo" exists on disk, the argument is a new-file name
// taken literally (colon and all) — same as vim's treatment.
func TestResolveArgs_FileLineMissingPrefix(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "foo:9")

	got := resolveArgs([]string{target})
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want the literal %q", got.OpenFile, target)
	}
	if got.OpenLine != 0 {
		t.Fatalf("OpenLine: got %d, want 0", got.OpenLine)
	}
}

// TestResolveArgs_VersionFlag covers every flavour of --version we
// accept. Failing here would mean a user typing `--version` lands in
// the editor instead of seeing a printed version.
func TestResolveArgs_VersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-V", "version"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionVersion {
			t.Errorf("flag %q: action = %q, want version", flag, got.Action)
		}
	}
}

// TestResolveArgs_HelpFlag is the equivalent for --help. Like version,
// the multi-spelling list keeps the CLI forgiving.
func TestResolveArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionHelp {
			t.Errorf("flag %q: action = %q, want help", flag, got.Action)
		}
	}
}

// TestResolveArgs_ExtraArgsRefused pins the chosen multi-argument
// behaviour: a second path is an error, not a silently dropped tab. The
// message must name every argument being refused, because a user who
// can't see what was dropped can't tell the difference between "refused"
// and "opened somewhere I haven't looked yet".
func TestResolveArgs_ExtraArgsRefused(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.go")
	if err := os.WriteFile(first, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{first, "b.go", "c.go"})
	if got.Err == nil {
		t.Fatal("extra arguments should be refused, got no error")
	}
	if got.Action == actionEdit {
		t.Fatalf("action: got %q, want the zero action (no editor start)", got.Action)
	}
	msg := got.Err.Error()
	for _, want := range []string{"b.go", "c.go", "arguments"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "flags must come first") {
		t.Errorf("plain paths should not get the flag hint: %q", msg)
	}
}

// TestResolveArgs_ExtraArgSingularNamesIt covers the one-extra case: the
// noun goes singular and the argument is quoted so a name with spaces
// reads unambiguously.
func TestResolveArgs_ExtraArgSingularNamesIt(t *testing.T) {
	got := resolveArgs([]string{".", "some file.go"})
	if got.Err == nil {
		t.Fatal("extra argument should be refused, got no error")
	}
	msg := got.Err.Error()
	if !strings.Contains(msg, `"some file.go"`) {
		t.Errorf("error %q should quote the refused argument", msg)
	}
	if strings.Contains(msg, "arguments:") {
		t.Errorf("single extra argument should use the singular noun: %q", msg)
	}
}

// TestResolveArgs_MisplacedFlagIsRefused is the fix for `skiff main.go
// --version` quietly editing main.go and dropping the flag. The flag is
// still position-sensitive, but now it's reported instead of ignored,
// and the message says where flags belong.
func TestResolveArgs_MisplacedFlagIsRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target, "--version"})
	if got.Err == nil {
		t.Fatal("trailing --version should be refused, got no error")
	}
	if got.OpenFile != "" {
		t.Errorf("no file should be queued when the line is refused, got %q", got.OpenFile)
	}
	if !strings.Contains(got.Err.Error(), "flags must come first") {
		t.Errorf("error %q should tell the user where flags go", got.Err)
	}
}

// TestResolveArgs_FlagWithTrailingArgsRefused keeps the rule uniform in
// the other direction: `skiff --version junk` must not print a version
// and pretend "junk" was never typed.
func TestResolveArgs_FlagWithTrailingArgsRefused(t *testing.T) {
	for _, flag := range []string{"--version", "--help"} {
		got := resolveArgs([]string{flag, "junk"})
		if got.Err == nil {
			t.Errorf("%s junk: want an error, got action %q", flag, got.Action)
		}
	}
}

// TestPrintHelpMatchesBehaviour guards the specific lie this change
// removed: help used to claim a file argument's parent "becomes the
// project root", when main actually enters single-file mode with no
// sidebar and no project index.
func TestPrintHelpMatchesBehaviour(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	printHelp()
	_ = w.Close()
	os.Stdout = stdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	help := buf.String()
	for _, want := range []string{"single-file mode", "no sidebar", "Extra arguments are refused"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should mention %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "becomes the project root") {
		t.Errorf("help still claims a file's parent becomes the project root:\n%s", help)
	}
}

// TestMainExitCodes runs the real binary because exit status is main's
// behaviour, not resolveArgs' — the refusal has to actually be
// non-zero for `skiff a.go b.go && …` in a script to stop.
func TestMainExitCodes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; can't build the binary under test")
	}
	bin := filepath.Join(t.TempDir(), "skiff-under-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	t.Run("extra args exit 1", func(t *testing.T) {
		cmd := exec.Command(bin, "a.go", "b.go")
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() != 1 {
			t.Fatalf("exit code: got %d, want 1 (err %v, output %s)", cmd.ProcessState.ExitCode(), err, out)
		}
		if !strings.Contains(string(out), "b.go") {
			t.Errorf("stderr should name the refused argument, got %q", out)
		}
	})

	t.Run("version exits 0", func(t *testing.T) {
		out, err := exec.Command(bin, "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("--version: %v\n%s", err, out)
		}
		if !strings.HasPrefix(string(out), "skiff ") {
			t.Errorf("--version output: got %q", out)
		}
	})

	t.Run("help exits 0", func(t *testing.T) {
		out, err := exec.Command(bin, "--help").CombinedOutput()
		if err != nil {
			t.Fatalf("--help: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "Usage:") {
			t.Errorf("--help output: got %q", out)
		}
	})
}
