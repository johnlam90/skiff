// =============================================================================
// File: internal/git/git_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepo builds a real repo with one commit — the contract tests run
// against actual git so the adapter's behavior is pinned to the real
// thing, not to our assumptions about it.
func initRepo(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("forks a real git process per call — slow; run without -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
		{"checkout", "-q", "-b", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := exec.Command("git", "-C", dir, "add", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "commit", "-q", "-m", "seed")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	return dir
}

// gitT runs one git command against dir and fails the test if git
// does. Test setup wants the opposite of the production runner's
// best-effort posture: a seed step that silently no-ops produces a test
// that passes for the wrong reason.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeSeed writes a fixture file, failing the test on any error.
func writeSeed(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestRepo_ReadReachesRealGit is the contract test: a read command
// against a real repo returns git's stdout.
func TestRepo_ReadReachesRealGit(t *testing.T) {
	r := Open(initRepo(t))
	out, err := r.read("symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("branch = %q, want main", out)
	}
}

// TestRepo_ReadFailsOutsideARepo pins the non-repo contract: the
// command fails per call, same as the raw invocations did — Open never
// pre-validates.
func TestRepo_ReadFailsOutsideARepo(t *testing.T) {
	if testing.Short() {
		t.Skip("forks a real git process — slow; run without -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	r := Open(t.TempDir())
	if _, err := r.read("symbolic-ref", "--short", "HEAD"); err == nil {
		t.Fatal("read in a non-repo must fail")
	}
}

// TestRepo_OpRunAccumulatesOutputIntoOpError pins the write contract
// every verb is built on: a failing command comes back as *OpError
// whose Output holds what git said across the whole run — the words
// from the commands before the failure included — so a multi-command
// verb like Commit or PullAndPush reports the whole story.
func TestRepo_OpRunAccumulatesOutputIntoOpError(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	o := r.op("Test")
	if err := o.run("checkout", "-b", "feature"); err != nil {
		t.Fatalf("first step: %v", err)
	}
	err := o.run("checkout", "definitely-not-a-branch")
	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("a failing write must be an *OpError, got %T %v", err, err)
	}
	if opErr.Op != "Test" {
		t.Fatalf("Op = %q, want the verb name", opErr.Op)
	}
	if !strings.Contains(opErr.Output, "definitely-not-a-branch") {
		t.Fatalf("output should carry git's words, got %q", opErr.Output)
	}
	if !strings.Contains(opErr.Output, "feature") {
		t.Fatalf("output should carry the earlier command's words too, got %q", opErr.Output)
	}
	if opErr.Unwrap() == nil {
		t.Fatal("OpError must unwrap to the process error")
	}
}

// TestRepo_ReadTimeoutKillsHangingCommands pins the deliberate behavior
// change of the seam: a read that wedges fails after the timeout
// instead of freezing a refresh goroutine forever. The hang is a shell
// alias that sleeps well past the shortened timeout.
func TestRepo_ReadTimeoutKillsHangingCommands(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	r.timeout = 150 * time.Millisecond
	start := time.Now()
	_, err := r.read("-c", "alias.slow=!sleep 5", "slow")
	if err == nil {
		t.Fatal("a hanging read must fail")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout should kill the command quickly, took %v", elapsed)
	}
}

// TestRepo_WriteTimeoutKillsWedgedWrites pins the deadline added to the
// write path. GIT_TERMINAL_PROMPT=0 silences git's own prompt but not a
// third-party credential helper or askpass binary blocking on its own
// stdin — and because writes run one at a time behind a single busy
// gate, one such hang would retire the editor's git verbs for the rest
// of the session. The real bound is minutes; the test shortens it.
func TestRepo_WriteTimeoutKillsWedgedWrites(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	r.writeTimeout = 150 * time.Millisecond
	start := time.Now()
	err := r.op("slow").run("-c", "alias.slow=!sleep 5", "slow")
	if err == nil {
		t.Fatal("a wedged write must fail")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("write timeout should kill the command quickly, took %v", elapsed)
	}
}

// TestRepo_WriteTimeoutIsGenerous pins the tradeoff the deadline makes:
// it is a hang guard, not a performance budget. A push over a slow link
// takes minutes legitimately, and a bound tight enough to interrupt one
// would corrupt the user's mental model of whether their work landed.
func TestRepo_WriteTimeoutIsGenerous(t *testing.T) {
	if writeTimeout < time.Minute {
		t.Fatalf("writeTimeout = %v; a real push must not be cut short", writeTimeout)
	}
	if got := Open("/repo").writeTimeout; got != writeTimeout {
		t.Fatalf("Open must apply the write deadline, got %v", got)
	}
	if got := OpenWith("/repo", &Fake{}).writeTimeout; got != writeTimeout {
		t.Fatalf("OpenWith must apply the write deadline, got %v", got)
	}
}

// TestOpenWith_RoutesThroughTheRunner pins the seam: a Repo built over
// the Fake never touches real git, scripted responses come back, and
// unscripted commands fail loudly.
func TestOpenWith_RoutesThroughTheRunner(t *testing.T) {
	fake := &Fake{}
	fake.Script("symbolic-ref --short HEAD", "main\n", nil)
	r := OpenWith("/nowhere", fake)
	out, err := r.read("symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("scripted read failed: %q %v", out, err)
	}
	if _, err := r.read("status", "--porcelain"); err == nil {
		t.Fatal("unscripted command must fail")
	}
	if fake.CallCount() != 2 {
		t.Fatalf("calls = %d, want 2", fake.CallCount())
	}
}
