// =============================================================================
// File: internal/app/perf_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-09-06
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Benchmarks for the paths a remote user feels as latency: an idle
// redraw, a keystroke, a wheel tick, a tree paint, and project startup.
// They are the before/after ruler for performance work — run with
// `go test ./internal/app -run xxx -bench Perf -benchmem`.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/finder"
)

// perfSource builds a ~lines-line Go file from this package's own source
// so the lexer sees realistic tokens rather than synthetic filler.
func perfSource(tb testing.TB, lines int) string {
	tb.Helper()
	var sb strings.Builder
	files, _ := filepath.Glob("*.go")
	for sb.Len() == 0 || strings.Count(sb.String(), "\n") < lines {
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				tb.Fatal(err)
			}
			sb.Write(b)
			sb.WriteByte('\n')
			if strings.Count(sb.String(), "\n") >= lines {
				break
			}
		}
	}
	return sb.String()
}

// perfApp opens one large Go file in a 200x50 app whose sidebar shows a
// populated tree — the everyday editing shape.
func perfApp(tb testing.TB, lines int) *App {
	tb.Helper()
	root := tb.(*testing.B).TempDir()
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%02d.go", i)), []byte("package x\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	big := filepath.Join(root, "big.go")
	if err := os.WriteFile(big, []byte(perfSource(tb, lines)), 0o644); err != nil {
		tb.Fatal(err)
	}
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { scr.Fini() })
	scr.SetSize(200, 50)
	tree, err := filetree.New(root)
	if err != nil {
		tb.Fatal(err)
	}
	a := newApp(scr, tree.Root.Path, tree, true)
	a.width, a.height = scr.Size()
	a.openFile(big)
	a.wrapOn = false
	if tab := a.activeTabPtr(); tab != nil {
		tab.Wrap = false
		tab.RestoreView(editor.Position{Line: lines / 2}, lines/2-10)
	}
	a.draw()
	scr.Show()
	return a
}

// BenchmarkPerfDrawIdle is a redraw with nothing changed: the cost of
// every event that isn't an edit or a scroll (mouse move, tick, flash).
func BenchmarkPerfDrawIdle(b *testing.B) {
	a := perfApp(b, 8000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.draw()
		a.screen.Show()
	}
}

// BenchmarkPerfKeystroke types one rune and redraws — the per-keystroke
// latency while editing a large file.
func BenchmarkPerfKeystroke(b *testing.B) {
	a := perfApp(b, 8000)
	ev := tcell.NewEventKey(tcell.KeyRune, 'x', 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.handleEvent(ev)
		a.draw()
		a.screen.Show()
	}
}

// BenchmarkPerfWheel scrolls one tick down and redraws, sweeping the
// whole file so the highlight window is crossed regularly.
func BenchmarkPerfWheel(b *testing.B) {
	a := perfApp(b, 8000)
	ex, ey, _, _ := a.editorRect()
	down := tcell.NewEventMouse(ex+10, ey+5, tcell.WheelDown, 0)
	up := tcell.NewEventMouse(ex+10, ey+5, tcell.WheelUp, 0)
	if tab := a.activeTabPtr(); tab != nil {
		tab.ScrollY = 0
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := down
		if (i/2000)%2 == 1 {
			ev = up
		}
		a.handleEvent(ev)
		a.draw()
		a.screen.Show()
	}
}

// perfTreeRoot lays out dirs×files entries so tree work has something
// to chew on; every directory is expanded by the caller.
func perfTreeRoot(tb testing.TB, dirs, files int) string {
	tb.Helper()
	root := tb.(*testing.B).TempDir()
	for d := 0; d < dirs; d++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%03d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		for f := 0; f < files; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%03d.go", f)), []byte("package x\n"), 0o644); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return root
}

// BenchmarkPerfTreeDraw paints a fully expanded 100-dir tree behind an
// empty editor: the sidebar's share of every frame.
func BenchmarkPerfTreeDraw(b *testing.B) {
	root := perfTreeRoot(b, 100, 30)
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		b.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(200, 50)
	tree, err := filetree.New(root)
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range tree.Root.Children {
		tree.Toggle(c)
	}
	a := newApp(scr, tree.Root.Path, tree, true)
	a.width, a.height = scr.Size()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.draw()
		a.screen.Show()
	}
}

// BenchmarkPerfTreeScan is the 10s refresh sweep's disk walk plus the
// on-loop merge over an expanded 100-dir tree.
func BenchmarkPerfTreeScan(b *testing.B) {
	root := perfTreeRoot(b, 100, 30)
	tree, err := filetree.New(root)
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range tree.Root.Children {
		tree.Toggle(c)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dirs := tree.LoadedDirs()
		res := filetree.ScanDirs(dirs)
		tree.ApplyScan(res)
	}
}

// BenchmarkPerfStartup is the synchronous part of New for a 3000-file
// project: tree load, app wiring, and the finder index build.
func BenchmarkPerfStartup(b *testing.B) {
	root := perfTreeRoot(b, 100, 30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scr := tcell.NewSimulationScreen("UTF-8")
		if err := scr.Init(); err != nil {
			b.Fatal(err)
		}
		tree, err := filetree.New(root)
		if err != nil {
			b.Fatal(err)
		}
		a := newApp(scr, tree.Root.Path, tree, true)
		a.width, a.height = scr.Size()
		if _, _, err := finder.BuildIndex(root); err != nil {
			b.Fatal(err)
		}
		a.draw()
		scr.Show()
		scr.Fini()
	}
}
