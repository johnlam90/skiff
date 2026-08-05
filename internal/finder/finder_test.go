// =============================================================================
// File: internal/finder/finder_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package finder

import (
	"math/rand/v2"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFinder_StartsIdle is the contract guard for the lazy-build
// design: New() must not kick off a goroutine on its own. The
// caller's first Rebuild is what arms the index, so an editor
// that boots and never opens the finder shouldn't pay any cost.
func TestFinder_StartsIdle(t *testing.T) {
	f := New("/tmp/whatever")
	if f.State() != StateIdle {
		t.Fatalf("state: got %v, want StateIdle", f.State())
	}
	if got := f.Search("anything", 10); got != nil {
		t.Fatalf("Search before Rebuild should return nil, got %v", got)
	}
}

// TestFinder_RebuildPopulates walks the happy path: Rebuild on a
// real tempdir → state goes Building → Ready, paths are populated,
// onDone fires. The 2-second timeout catches any future regression
// where the goroutine hangs (e.g. a lock that's never released).
func TestFinder_RebuildPopulates(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "a.go", "package a")
	mustWrite(t, dir, "sub/b.go", "package b")

	f := New(dir)
	done := make(chan struct{})
	f.Rebuild(func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild did not finish in 2s")
	}

	state, total, _ := f.Stats()
	if state != StateReady {
		t.Fatalf("state: got %v, want StateReady", state)
	}
	if total != 2 {
		t.Fatalf("total: got %d, want 2", total)
	}
}

// TestFinder_RebuildCoalescesConcurrent guarantees the in-flight
// gate works: ten back-to-back Rebuilds must produce *one* build,
// not ten. Without coalescing a fast-typing user could create a
// thundering herd of goroutines all walking the same project.
func TestFinder_RebuildCoalescesConcurrent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "a.go", "x")

	f := New(dir)
	var doneCount int
	var mu sync.Mutex
	cb := func() {
		mu.Lock()
		doneCount++
		mu.Unlock()
	}
	for i := 0; i < 10; i++ {
		f.Rebuild(cb)
	}

	// Wait for state to settle. The first Rebuild fires; the rest
	// are no-ops. We expect exactly one onDone call.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.State() == StateReady {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if f.State() != StateReady {
		t.Fatal("state never reached StateReady")
	}
	// Give any spurious extra callbacks a moment to fire.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := doneCount
	mu.Unlock()
	if got != 1 {
		t.Fatalf("onDone fired %d times, want 1", got)
	}
}

// TestFinder_SearchRanks is the integration check that the orchestr-
// ator's Search wires the scorer up correctly: a more-specific query
// beats a less-specific one, basename hits beat dir hits, results
// are limited.
func TestFinder_SearchRanks(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "tab.go", "x")
	mustWrite(t, dir, "internal/tabs/foo.go", "x")
	mustWrite(t, dir, "internal/app/tabbar.go", "x")
	mustWrite(t, dir, "unrelated.txt", "x")

	f := New(dir)
	done := make(chan struct{})
	f.Rebuild(func() { close(done) })
	<-done

	results := f.Search("tab", 5)
	if len(results) == 0 {
		t.Fatal("expected results for query 'tab'")
	}
	if results[0].Path != "tab.go" {
		t.Fatalf("top result: got %q, want tab.go", results[0].Path)
	}
	for _, r := range results {
		if r.Path == "unrelated.txt" {
			t.Fatal("non-matching path leaked into results")
		}
	}
}

// TestFinder_SearchEmptyQueryReturnsAlphabetical pins the "give me
// something to look at" promise: opening the modal with an empty
// input should show the first few paths alphabetically rather
// than a blank list. Otherwise the user has to type a character
// before they get any feedback that the index is even loaded.
func TestFinder_SearchEmptyQueryReturnsAlphabetical(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "z.go", "x")
	mustWrite(t, dir, "a.go", "x")
	mustWrite(t, dir, "m.go", "x")

	f := New(dir)
	done := make(chan struct{})
	f.Rebuild(func() { close(done) })
	<-done

	got := f.Search("", 10)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	if got[0].Path != "a.go" {
		t.Fatalf("first result: got %q, want a.go", got[0].Path)
	}
}

// TestFinder_InvalidateResetsState pins the invalidate-then-rebuild
// pattern app callers use after file mutations: after Invalidate,
// State drops back to Idle until Rebuild is called.
func TestFinder_InvalidateResetsState(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "a.go", "x")

	f := New(dir)
	done := make(chan struct{})
	f.Rebuild(func() { close(done) })
	<-done

	f.Invalidate()
	if f.State() != StateIdle {
		t.Fatalf("state after Invalidate: got %v, want StateIdle", f.State())
	}
}

// searchFullSort is a verbatim copy of the pre-top-N Search body — score every
// candidate, sort.SliceStable the whole match list, then truncate to limit. It
// is the reference the bounded selection has to reproduce byte for byte, and
// the "before" side of the benchmark pair.
func searchFullSort(paths []string, query string, limit int) []Result {
	results := make([]Result, 0, 64)
	for _, p := range paths {
		score, idx := Score(query, p)
		if score == 0 {
			continue
		}
		results = append(results, Result{Path: p, Score: score, MatchedIndexes: idx})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// readyFinder returns a Finder that reports StateReady over paths without
// touching the filesystem or arming the build goroutine.
func readyFinder(paths []string) *Finder {
	return &Finder{rootDir: ".", paths: paths, state: StateReady}
}

// tieHeavyCorpus builds n project-relative paths out of a deliberately tiny
// alphabet of directory and file names. A short query then matches thousands
// of them with many exactly-equal scores, which is precisely where a
// truncating full sort and a bounded top-N selection could disagree: at the
// cutoff, among rows the comparator calls equal.
func tieHeavyCorpus(n int, seed uint64) []string {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	dirs := []string{"a", "b", "ab", "ba", "app", "api", "cmd", "core", "internal"}
	names := []string{"a.go", "ab.go", "b.go", "main.go", "tab.go", "util.go", "abc_test.go"}
	paths := make([]string, 0, n)
	var sb strings.Builder
	for range n {
		sb.Reset()
		for d := rng.IntN(3); d > 0; d-- {
			sb.WriteString(dirs[rng.IntN(len(dirs))])
			sb.WriteByte('/')
		}
		sb.WriteString(names[rng.IntN(len(names))])
		paths = append(paths, sb.String())
	}
	return paths
}

// TestFinder_SearchMatchesFullSortOrdering is the equivalence proof for
// swapping the full sort out for a bounded selection: over a large randomised
// tie-heavy corpus, at several limits, and with the index both sorted (what
// the real builder hands us) and shuffled (so the result can't be an accident
// of input order), Search must return exactly what the old implementation did.
func TestFinder_SearchMatchesFullSortOrdering(t *testing.T) {
	corpusSize := 20000
	if testing.Short() {
		corpusSize = 2000
	}
	sorted := tieHeavyCorpus(corpusSize, 1)
	sort.Strings(sorted)
	shuffled := append([]string(nil), sorted...)
	rand.New(rand.NewPCG(2, 0x9e3779b97f4a7c15)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	queries := []string{"a", "b", "ab", "ba", "go", "ag", "abg", "tab", "main", "utilgo", "zzz"}
	limits := []int{1, 10, 50}
	orders := map[string][]string{"sorted": sorted, "shuffled": shuffled}

	for name, corpus := range orders {
		f := readyFinder(corpus)
		for _, q := range queries {
			for _, limit := range limits {
				got := f.Search(q, limit)
				want := searchFullSort(corpus, q, limit)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s corpus, query %q limit %d:\ngot  %v\nwant %v", name, q, limit, got, want)
				}
			}
		}
	}
}

// TestFinder_SearchNeverExceedsLimit is the cheap invariant the bounded
// selection could plausibly break on its own (an off-by-one in the shift), so
// it is worth pinning separately from the equivalence sweep.
func TestFinder_SearchNeverExceedsLimit(t *testing.T) {
	f := readyFinder(tieHeavyCorpus(500, 3))
	for _, limit := range []int{1, 2, 7, 10, 500, 5000} {
		if got := len(f.Search("a", limit)); got > limit {
			t.Fatalf("Search(limit=%d) returned %d results", limit, got)
		}
	}
}

// benchCorpus is a 50k-path index — a large monorepo, a quarter of the
// 200k index cap — built once and shared so the two Search benchmarks
// measure the same work over the same data.
var benchCorpus = sync.OnceValue(func() []string {
	paths := tieHeavyCorpus(50000, 7)
	sort.Strings(paths)
	return paths
})

// BenchmarkSearchFullSort measures the old score-everything-then-sort path:
// one keystroke's worth of work at a realistic index size.
func BenchmarkSearchFullSort(b *testing.B) {
	paths := benchCorpus()
	b.ReportAllocs()
	for b.Loop() {
		if got := len(searchFullSort(paths, "ab", 10)); got != 10 {
			b.Fatalf("got %d results, want 10", got)
		}
	}
}

// BenchmarkSearchTopN measures the bounded selection over the same corpus and
// query, so the two numbers are directly comparable.
func BenchmarkSearchTopN(b *testing.B) {
	f := readyFinder(benchCorpus())
	b.ReportAllocs()
	for b.Loop() {
		if got := len(f.Search("ab", 10)); got != 10 {
			b.Fatalf("got %d results, want 10", got)
		}
	}
}
