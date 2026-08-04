// =============================================================================
// File: internal/git/git_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package git

import (
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

// TestRepo_OutputReadsRealGit is the contract test: a read command
// against a real repo returns git's stdout.
func TestRepo_OutputReadsRealGit(t *testing.T) {
	r := Open(initRepo(t))
	out, err := r.Output("symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("branch = %q, want main", out)
	}
}

// TestRepo_OutputFailsOutsideARepo pins the non-repo contract: the
// command fails per call, same as the raw invocations did — Open never
// pre-validates.
func TestRepo_OutputFailsOutsideARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	r := Open(t.TempDir())
	if _, err := r.Output("symbolic-ref", "--short", "HEAD"); err == nil {
		t.Fatal("read in a non-repo must fail")
	}
}

// TestRepo_RunSequenceStopsAtFirstFailure pins the write contract: the
// sequence stops at the first failing command and the accumulated
// output includes what git said before and during the failure.
func TestRepo_RunSequenceStopsAtFirstFailure(t *testing.T) {
	dir := initRepo(t)
	r := Open(dir)
	out, err := r.RunSequence([][]string{
		{"checkout", "-q", "-b", "feature"},
		{"checkout", "definitely-not-a-branch"},
		{"checkout", "-q", "main"}, // must not run
	})
	if err == nil {
		t.Fatal("sequence with a failing step must error")
	}
	if !strings.Contains(out, "definitely-not-a-branch") {
		t.Fatalf("output should carry git's words, got %q", out)
	}
	// The third command didn't run: we're still on feature.
	b, _ := r.Output("symbolic-ref", "--short", "HEAD")
	if strings.TrimSpace(string(b)) != "feature" {
		t.Fatalf("sequence must stop at the failure; on %q", b)
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
	_, err := r.Output("-c", "alias.slow=!sleep 5", "slow")
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
	_, err := r.RunSequence([][]string{{"-c", "alias.slow=!sleep 5", "slow"}})
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
	out, err := r.Output("symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("scripted read failed: %q %v", out, err)
	}
	if _, err := r.Output("status", "--porcelain"); err == nil {
		t.Fatal("unscripted command must fail")
	}
	if fake.CallCount() != 2 {
		t.Fatalf("calls = %d, want 2", fake.CallCount())
	}
}
