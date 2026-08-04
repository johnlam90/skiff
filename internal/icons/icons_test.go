// =============================================================================
// File: internal/icons/icons_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package icons

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/userconfig"
)

// TestForFolders pins the folder open/closed glyph pairing — flipping
// these by accident would silently invert what every directory in the
// tree displays.
func TestForFolders(t *testing.T) {
	if got := For("anything", true, false); got != FolderClosed {
		t.Fatalf("collapsed dir = %q, want FolderClosed", got)
	}
	if got := For("anything", true, true); got != FolderOpen {
		t.Fatalf("expanded dir = %q, want FolderOpen", got)
	}
}

// TestForByExtension covers the common extension lookups — happy path
// for the bulk of files in any project.
func TestForByExtension(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"main.go", extIcons[".go"]},
		{"app.py", extIcons[".py"]},
		{"index.JS", extIcons[".js"]}, // case-insensitive
		{"style.css", extIcons[".css"]},
		{"README.markdown", extIcons[".markdown"]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := For(tc.name, false, false); got != tc.want {
				t.Fatalf("For(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestForByFullName verifies extension-less files resolve via the
// full-name table — Dockerfile and Makefile are the canonical reasons
// this lookup tier exists at all.
func TestForByFullName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Dockerfile", nameIcons["dockerfile"]},
		{"Makefile", nameIcons["makefile"]},
		{"go.mod", nameIcons["go.mod"]},
		{".gitignore", nameIcons[".gitignore"]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := For(tc.name, false, false); got != tc.want {
				t.Fatalf("For(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestForFallback verifies an unknown extension returns FileDefault
// rather than the empty string — the renderer relies on a non-empty
// glyph to keep the indent column-aligned.
func TestForFallback(t *testing.T) {
	if got := For("mystery.xyzzy", false, false); got != FileDefault {
		t.Fatalf("unknown ext = %q, want FileDefault", got)
	}
	if got := For("no_extension_at_all", false, false); got != FileDefault {
		t.Fatalf("no-ext file = %q, want FileDefault", got)
	}
}

// TestResolveExplicitOverrides verifies the on/off modes bypass
// detection entirely — important for users on a terminal where
// detection would lie either way.
func TestResolveExplicitOverrides(t *testing.T) {
	if !Resolve(userconfig.IconsOn) {
		t.Fatalf("IconsOn should always resolve true")
	}
	if Resolve(userconfig.IconsOff) {
		t.Fatalf("IconsOff should always resolve false")
	}
}

// TestResolveAutoIsBoolean is a smoke test for the "auto" path: it
// just runs Detect() on the test machine and asserts the result is a
// real bool. We can't assert true or false here because CI may or may
// not have Nerd Fonts installed.
func TestResolveAutoIsBoolean(t *testing.T) {
	got := Resolve(userconfig.IconsAuto)
	_ = got // any bool is fine; the assertion is "doesn't panic"
}

// TestDetectViaFilesystemFindsNerdFont sets up a fake font directory
// containing a file whose name carries "Nerd" and verifies the walker
// picks it up — proves the fallback path works without depending on
// the host's actual font install.
func TestDetectViaFilesystemFindsNerdFont(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HackNerdFont-Regular.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !walkForNerdFont(context.Background(), dir) {
		t.Fatalf("expected to find Nerd Font in %s", dir)
	}
}

// TestDetectViaFilesystemMissesNonMatching verifies the walker
// doesn't false-positive on plain fonts — important so we don't
// claim icons-OK for users whose system has only stock fonts.
func TestDetectViaFilesystemMissesNonMatching(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Arial.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if walkForNerdFont(context.Background(), dir) {
		t.Fatalf("Arial.ttf should not match a Nerd Font search")
	}
}

// TestDetectViaFilesystemRejectsWrongExtension covers the case of a
// "Nerd"-named file that isn't actually a font — we don't want a
// stray "nerd-readme.txt" to accidentally enable icons.
func TestDetectViaFilesystemRejectsWrongExtension(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nerd-readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if walkForNerdFont(context.Background(), dir) {
		t.Fatalf("non-font with 'nerd' in name should not match")
	}
}

// TestColorForFolderUsesFallback pins the rule that folders inherit
// the row's row-level fg — the file tree relies on this so the active
// folder still tints with th.Accent and dirty folders still flip to
// th.Modified instead of getting overwritten by a language colour.
func TestColorForFolderUsesFallback(t *testing.T) {
	fb := tcell.NewRGBColor(1, 2, 3)
	if got := ColorFor("anything", true, fb); got != fb {
		t.Fatalf("folder ColorFor = %v, want fallback %v", got, fb)
	}
}

// TestColorForKnownExtension verifies the extension lookup actually
// returns a per-language colour distinct from the fallback — proves
// the map is wired up rather than every call returning fallback.
func TestColorForKnownExtension(t *testing.T) {
	fb := tcell.NewRGBColor(1, 2, 3)
	got := ColorFor("main.go", false, fb)
	if got == fb {
		t.Fatalf("ColorFor(main.go) returned fallback; expected the .go entry")
	}
	if want := extColors[".go"]; got != want {
		t.Fatalf("ColorFor(main.go) = %v, want %v", got, want)
	}
}

// TestColorForKnownName covers the full-name lookup tier — Dockerfile
// has no extension, so without nameColors it would fall through to
// the fallback colour, which is exactly the bug we're guarding here.
func TestColorForKnownName(t *testing.T) {
	fb := tcell.NewRGBColor(1, 2, 3)
	got := ColorFor("Dockerfile", false, fb)
	if got == fb {
		t.Fatalf("Dockerfile fell through to fallback; nameColors wiring broken")
	}
	if want := nameColors["dockerfile"]; got != want {
		t.Fatalf("ColorFor(Dockerfile) = %v, want %v", got, want)
	}
}

// TestColorForUnknownReturnsFallback verifies the silent-fallback
// path: anything we haven't mapped renders in the row's regular fg
// rather than some default colour that would visually lie about the
// file's type.
func TestColorForUnknownReturnsFallback(t *testing.T) {
	fb := tcell.NewRGBColor(7, 8, 9)
	if got := ColorFor("mystery.xyzzy", false, fb); got != fb {
		t.Fatalf("unknown ext ColorFor = %v, want fallback %v", got, fb)
	}
}

// TestDetectViaFilesystemMissingDir confirms a non-existent dir is a
// quiet no-match rather than an error — the walker is called for
// every candidate path and most won't exist on any given system.
func TestDetectViaFilesystemMissingDir(t *testing.T) {
	if walkForNerdFont(context.Background(), "/definitely/does/not/exist/at/all") {
		t.Fatalf("missing dir should return false, not panic or true")
	}
}

// TestWalkForNerdFontStopsAtMaxDepth pins the startup-stall guard: a
// font dir with a deep tree under it (a symlinked asset folder, a
// checked-out repo of fonts) must not turn Detect() into a full
// subtree crawl. Files within the bound still match.
func TestWalkForNerdFontStopsAtMaxDepth(t *testing.T) {
	root := t.TempDir()
	shallow := filepath.Join(root, "a", "b")
	deep := filepath.Join(shallow, "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deep, "HackNerdFont.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed deep: %v", err)
	}
	if walkForNerdFont(context.Background(), root) {
		t.Fatalf("walk descended past maxFontWalkDepth=%d", maxFontWalkDepth)
	}

	if err := os.WriteFile(filepath.Join(shallow, "HackNerdFont.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed shallow: %v", err)
	}
	if !walkForNerdFont(context.Background(), root) {
		t.Fatal("a font two levels down should still be found")
	}
}

// TestWalkForNerdFontHonoursContext: the walk shares the detection
// deadline, so an already-expired context must abort it even when a
// match is sitting right there. This is what keeps a font dir on a
// stalled network mount from blocking the first frame.
func TestWalkForNerdFontHonoursContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HackNerdFont.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if walkForNerdFont(ctx, dir) {
		t.Fatal("expired context should abort the walk")
	}
}

// TestDetectViaFcListParsesFamilies covers the happy path of the
// fontconfig probe without needing fontconfig on the host: any family
// line containing "nerd font" or "nerdfont" is a hit, and nothing
// else is.
func TestDetectViaFcListParsesFamilies(t *testing.T) {
	t.Cleanup(func() { fcList = runFcList })
	cases := []struct {
		out  string
		want bool
	}{
		{"DejaVu Sans\nHack Nerd Font Mono\n", true},
		{"JetBrainsMono NerdFont\n", true},
		{"DejaVu Sans\nLiberation Mono\n", false},
	}
	for _, tc := range cases {
		fcList = func(context.Context) ([]byte, error) { return []byte(tc.out), nil }
		if got := detectViaFcList(context.Background()); got != tc.want {
			t.Fatalf("detectViaFcList(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

// TestDetectFcListTimeoutFallsThrough is the regression test for the
// audit finding: a wedged fc-list used to hang startup forever
// because the exec had no deadline. Detect() must give up on the
// deadline and still answer, well inside the time the hung command
// would have taken.
func TestDetectFcListTimeoutFallsThrough(t *testing.T) {
	prevList, prevTimeout := fcList, detectTimeout
	t.Cleanup(func() { fcList, detectTimeout = prevList, prevTimeout })

	detectTimeout = 50 * time.Millisecond
	started := make(chan struct{})
	fcList = func(ctx context.Context) ([]byte, error) {
		close(started)
		<-ctx.Done() // a hung fontconfig: only the deadline frees us
		return nil, ctx.Err()
	}

	done := make(chan bool, 1)
	go func() { done <- Detect() }()
	<-started
	select {
	case got := <-done:
		if got {
			t.Fatal("a timed-out probe must not report a Nerd Font")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Detect() never returned; the fc-list deadline is not wired up")
	}
}
