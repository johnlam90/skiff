// =============================================================================
// File: internal/asyncjob/asyncjob_test.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

package asyncjob

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// loop stands in for the event loop: Post appends to a queue, and the
// test lands events itself — on its own goroutine — the way handleEvent
// would. Spawn is a plain goroutine with a recover that records a
// panic, playing the crash guard's part so a panicking worker is
// observable instead of fatal.
type loop struct {
	mu     sync.Mutex
	queue  []tcell.Event
	panics []any
	wg     sync.WaitGroup

	// refuse makes post fail while it is > 0 (decremented per call) or
	// while refuseAll is set, playing a full tcell queue.
	refuse    atomic.Int32
	refuseAll atomic.Bool
	posts     atomic.Int32
}

// post is the Post seam.
func (l *loop) post(ev tcell.Event) error {
	l.posts.Add(1)
	if l.refuseAll.Load() {
		return tcell.ErrEventQFull
	}
	if l.refuse.Load() > 0 {
		l.refuse.Add(-1)
		return tcell.ErrEventQFull
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queue = append(l.queue, ev)
	return nil
}

// shortPostRetry shrinks the worker's retry delay for the test and
// restores it once every worker the loop spawned has exited — the
// workers read it, so restoring under a running one would race.
func shortPostRetry(t *testing.T, l *loop) {
	t.Helper()
	orig := postRetryDelay
	postRetryDelay = time.Millisecond
	t.Cleanup(func() {
		l.wg.Wait()
		postRetryDelay = orig
	})
}

// spawn is the Spawn seam: a guarded goroutine.
func (l *loop) spawn(_ string, fn func()) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				l.mu.Lock()
				l.panics = append(l.panics, r)
				l.mu.Unlock()
			}
		}()
		fn()
	}()
}

// pending reports how many posted events are waiting to be landed.
func (l *loop) pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

// waitPosted blocks until at least n events are queued, failing the
// test on a 5s deadline so a lost post cannot hang the suite.
func (l *loop) waitPosted(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for l.pending() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d events were posted", l.pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// landAll lands every queued event on the calling goroutine, in post
// order, and returns how many it landed. Events posted while landing
// (a Coalesce follow-up that finishes instantly) are left for the next
// call so a test can observe each landing step.
func (l *loop) landAll() int {
	l.mu.Lock()
	batch := l.queue
	l.queue = nil
	l.mu.Unlock()
	for _, ev := range batch {
		ev.(*Event).Land()
	}
	return len(batch)
}

// gate is a work body that blocks until released and counts its runs.
type gate struct {
	release chan struct{}
	runs    atomic.Int32
}

// newGate builds a closed gate.
func newGate() *gate { return &gate{release: make(chan struct{})} }

// work returns a worker that counts a run, waits for the gate (or the
// context), and reports which value it was started with.
func (g *gate) work(val int) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		g.runs.Add(1)
		select {
		case <-g.release:
			return val, nil
		case <-ctx.Done():
			return val, ctx.Err()
		}
	}
}

// waitRuns blocks until the gate has counted n runs — a spawned
// goroutine is not scheduled by the time Start returns, so "exactly
// one run" is only checkable once that one has actually begun.
func (g *gate) waitRuns(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for g.runs.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d runs began", g.runs.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// newJob wires a Job of the given policy to the loop and records every
// OnDone into landed.
func newJob(l *loop, p Policy, landed *[]int) *Job[int] {
	return &Job[int]{
		Name:   "test",
		Policy: p,
		Spawn:  l.spawn,
		Post:   l.post,
		OnDone: func(v int, err error) {
			if err == nil {
				*landed = append(*landed, v)
			}
		},
	}
}

// TestCoalesce_ThreeRapidStartsRunTwice pins the burst rule: while one
// run is in flight, further starts collapse into a single queued
// follow-up that spawns when the first run lands — three starts, two
// runs, and the follow-up carries the most recent start's work.
func TestCoalesce_ThreeRapidStartsRunTwice(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Coalesce, &landed)
	g := newGate()

	if !j.Start(g.work(1)) || !j.Start(g.work(2)) || !j.Start(g.work(3)) {
		t.Fatal("Coalesce accepts every start")
	}
	if !j.Busy() || !j.Queued() {
		t.Fatalf("after three starts: busy=%v queued=%v, want both", j.Busy(), j.Queued())
	}
	g.waitRuns(t, 1)
	if got := g.runs.Load(); got != 1 {
		t.Fatalf("starts during a run must not spawn: %d runs", got)
	}

	close(g.release)
	l.waitPosted(t, 1)
	l.landAll()
	if j.Queued() {
		t.Fatal("landing should consume the queued follow-up")
	}
	if !j.Busy() {
		t.Fatal("landing with a queued start should spawn exactly one follow-up")
	}
	l.waitPosted(t, 1)
	l.landAll()

	if got := g.runs.Load(); got != 2 {
		t.Fatalf("three rapid starts should cost two runs, got %d", got)
	}
	if len(landed) != 2 || landed[0] != 1 || landed[1] != 3 {
		t.Fatalf("landed %v, want [1 3] — the follow-up is the newest start's work", landed)
	}
	if j.Busy() {
		t.Fatal("nothing should be in flight after the follow-up lands")
	}
}

// TestSupersede_StaleLandingDroppedNewestApplied is the click-twice
// rule: both runs spawn, the older run's context is cancelled and its
// landing dropped, and only the newest reaches OnDone — whichever order
// the two goroutines finish in.
func TestSupersede_StaleLandingDroppedNewestApplied(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Supersede, &landed)
	first, second := newGate(), newGate()

	var firstCtx context.Context
	j.Start(func(ctx context.Context) (int, error) {
		firstCtx = ctx
		return first.work(1)(ctx)
	})
	j.Start(second.work(2))

	// The older run is retired the moment the newer one starts.
	l.waitPosted(t, 1) // the first run exits on its cancelled context
	if firstCtx == nil || firstCtx.Err() == nil {
		t.Fatal("a superseding start must cancel the older run's context")
	}
	l.landAll()
	if len(landed) != 0 {
		t.Fatalf("the stale run landed %v; it must be dropped", landed)
	}
	if !j.Busy() {
		t.Fatal("the newest run is still in flight")
	}

	close(second.release)
	l.waitPosted(t, 1)
	l.landAll()
	if len(landed) != 1 || landed[0] != 2 {
		t.Fatalf("landed %v, want [2]", landed)
	}
	if j.Busy() {
		t.Fatal("nothing in flight once the newest run lands")
	}
}

// TestSupersede_StaleRunThatIgnoresContextIsStillDropped covers a
// worker that never looks at ctx (a git call with its own deadline):
// its landing must still be dropped by the generation check alone.
func TestSupersede_StaleRunThatIgnoresContextIsStillDropped(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Supersede, &landed)
	first, second := newGate(), newGate()

	j.Start(func(context.Context) (int, error) { <-first.release; return 1, nil })
	j.Start(func(context.Context) (int, error) { <-second.release; return 2, nil })

	close(second.release)
	l.waitPosted(t, 1)
	l.landAll()
	close(first.release)
	l.waitPosted(t, 1)
	l.landAll()

	if len(landed) != 1 || landed[0] != 2 {
		t.Fatalf("landed %v, want only the newest run's 2", landed)
	}
}

// TestRefuse_SecondStartReturnsFalse pins the one-at-a-time gate: a
// start during a run returns false and spawns nothing, and the gate
// reopens once the run lands.
func TestRefuse_SecondStartReturnsFalse(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Refuse, &landed)
	g := newGate()

	if !j.Start(g.work(1)) {
		t.Fatal("the first start must be accepted")
	}
	if j.Start(g.work(2)) {
		t.Fatal("a start during a run must be refused")
	}
	g.waitRuns(t, 1)
	if got := g.runs.Load(); got != 1 {
		t.Fatalf("a refused start must not spawn: %d runs", got)
	}
	if j.Queued() {
		t.Fatal("Refuse never queues")
	}

	close(g.release)
	l.waitPosted(t, 1)
	l.landAll()
	if j.Busy() {
		t.Fatal("the gate should reopen on landing")
	}
	if len(landed) != 1 || landed[0] != 1 {
		t.Fatalf("landed %v, want [1]", landed)
	}
	if !j.Start(g.work(3)) {
		t.Fatal("a start after the landing must be accepted again")
	}
	l.waitPosted(t, 1)
	l.landAll()
}

// TestConcurrent_OverlappingStartsBothLand pins the no-gate policy: two
// starts during each other both spawn, both landings apply — in the
// order they reach the loop, the second-started run first here — and
// nothing is queued, refused or superseded.
func TestConcurrent_OverlappingStartsBothLand(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Concurrent, &landed)
	first, second := newGate(), newGate()

	if !j.Start(first.work(1)) || !j.Start(second.work(2)) {
		t.Fatal("Concurrent accepts every start")
	}
	first.waitRuns(t, 1)
	second.waitRuns(t, 1)
	if j.Queued() {
		t.Fatal("Concurrent never queues")
	}

	close(second.release)
	l.waitPosted(t, 1)
	l.landAll()
	if len(landed) != 1 || landed[0] != 2 {
		t.Fatalf("the second run's landing should apply on arrival, got %v", landed)
	}
	if !j.Busy() {
		t.Fatal("the first run is still in flight")
	}

	close(first.release)
	l.waitPosted(t, 1)
	l.landAll()
	if len(landed) != 2 || landed[1] != 1 {
		t.Fatalf("both landings should apply in arrival order, got %v", landed)
	}
	if j.Busy() {
		t.Fatal("nothing in flight once both have landed")
	}
}

// TestConcurrent_InvalidateRetiresEveryRun: the one verb Concurrent
// shares with the gated policies retires all of its in-flight runs at
// once — their shared context is cancelled and no landing applies.
func TestConcurrent_InvalidateRetiresEveryRun(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Concurrent, &landed)
	var ctxs [2]context.Context
	for i := range ctxs {
		i := i
		j.Start(func(ctx context.Context) (int, error) {
			ctxs[i] = ctx
			<-ctx.Done()
			return i, ctx.Err()
		})
	}
	j.Invalidate()
	l.waitPosted(t, 2)
	for i, ctx := range ctxs {
		if ctx == nil || ctx.Err() == nil {
			t.Fatalf("run %d's context should be cancelled by Invalidate", i)
		}
	}
	l.landAll()
	if len(landed) != 0 || j.Busy() {
		t.Fatalf("retired runs must not land: landed=%v busy=%v", landed, j.Busy())
	}
}

// TestPost_RefusedThenAccepted: a Post the loop refuses is retried from
// the worker, and a run whose retry succeeds lands exactly once, so a
// momentarily full queue costs nothing but the wait.
func TestPost_RefusedThenAccepted(t *testing.T) {
	l := &loop{}
	shortPostRetry(t, l)
	var landed []int
	j := newJob(l, Refuse, &landed)
	l.refuse.Store(3)

	j.Start(func(context.Context) (int, error) { return 1, nil })
	l.waitPosted(t, 1)
	if got := l.posts.Load(); got != 4 {
		t.Fatalf("the worker should have retried past 3 refusals, made %d posts", got)
	}
	l.landAll()
	if len(landed) != 1 || landed[0] != 1 {
		t.Fatalf("a retried landing should apply once, got %v", landed)
	}
	if j.Busy() {
		t.Fatal("nothing in flight after the retried landing")
	}
}

// TestPost_LostLandingDoesNotStrandTheGate is the self-heal: a landing
// the worker gives up on never reaches OnDone, but the run stops
// counting as in flight — a Refuse gate reopens and a Coalesce job's
// next kick spawns instead of queueing behind a run that will never
// land. Before, every hand-rolled busy flag stayed set for the session.
func TestPost_LostLandingDoesNotStrandTheGate(t *testing.T) {
	for _, p := range []Policy{Refuse, Coalesce, Supersede, Concurrent} {
		t.Run(p.String(), func(t *testing.T) {
			l := &loop{}
			shortPostRetry(t, l)
			var landed []int
			j := newJob(l, p, &landed)
			g := newGate()
			l.refuseAll.Store(true)

			j.Start(func(context.Context) (int, error) { return 1, nil })
			if p == Coalesce {
				// A kick during the doomed run queues behind it.
				j.Start(g.work(2))
				if !j.Queued() {
					t.Fatal("a kick during the run should queue")
				}
			}
			l.wg.Wait() // the worker exhausted its retries
			if got := l.posts.Load(); got != postRetries+1 {
				t.Fatalf("the worker should try %d times before giving up, made %d posts", postRetries+1, got)
			}
			if len(landed) != 0 {
				t.Fatalf("a lost landing must not apply, got %v", landed)
			}
			if j.Busy() {
				t.Fatal("a lost landing must not count as in flight")
			}

			// The loop is healthy again: the next start must run and land.
			l.refuseAll.Store(false)
			if !j.Start(g.work(3)) {
				t.Fatal("the gate must reopen after a lost landing")
			}
			if p == Coalesce && j.Queued() {
				t.Fatal("the kick that spawns is the follow-up; nothing should stay queued")
			}
			close(g.release)
			l.waitPosted(t, 1)
			l.landAll()
			if len(landed) != 1 || landed[0] != 3 {
				t.Fatalf("the run after a lost landing should land, got %v", landed)
			}
			if j.Busy() {
				t.Fatal("nothing in flight after the healthy landing")
			}
		})
	}
}

// TestInvalidate_DropsInFlightResult is the "a mutation retired the
// sweep" verb: the in-flight run's context is cancelled, its landing
// is dropped, and the job is idle afterwards — nothing new starts.
func TestInvalidate_DropsInFlightResult(t *testing.T) {
	for _, p := range []Policy{Coalesce, Supersede, Refuse, Concurrent} {
		t.Run(p.String(), func(t *testing.T) {
			l := &loop{}
			var landed []int
			j := newJob(l, p, &landed)
			g := newGate()

			var runCtx context.Context
			j.Start(func(ctx context.Context) (int, error) {
				runCtx = ctx
				return g.work(1)(ctx)
			})
			j.Invalidate()
			l.waitPosted(t, 1) // the worker exits on the cancelled context
			if runCtx.Err() == nil {
				t.Fatal("Invalidate must cancel the run's context")
			}
			l.landAll()
			if len(landed) != 0 {
				t.Fatalf("an invalidated run landed %v", landed)
			}
			if j.Busy() {
				t.Fatal("the invalidated run still counts as in flight after landing")
			}
			if g.runs.Load() != 1 {
				t.Fatalf("Invalidate must not start anything: %d runs", g.runs.Load())
			}
		})
	}
}

// TestInvalidate_KeepsCoalesceFollowUp: retiring the in-flight sweep
// must not lose a queued follow-up — it has read nothing yet, so its
// answer will be current, and it is the only refresh some caller asked
// for.
func TestInvalidate_KeepsCoalesceFollowUp(t *testing.T) {
	l := &loop{}
	var landed []int
	j := newJob(l, Coalesce, &landed)
	g := newGate()

	j.Start(g.work(1))
	j.Start(g.work(2))
	j.Invalidate()
	if !j.Queued() {
		t.Fatal("Invalidate must keep the queued follow-up")
	}
	close(g.release)
	l.waitPosted(t, 1)
	l.landAll() // stale first run: dropped, follow-up spawns
	l.waitPosted(t, 1)
	l.landAll()
	if len(landed) != 1 || landed[0] != 2 {
		t.Fatalf("landed %v, want the follow-up's [2] and not the retired run's 1", landed)
	}
}

// goroutineID parses the current goroutine's id out of its stack header
// — the only way to prove which goroutine a callback ran on.
func goroutineID() int {
	buf := make([]byte, 64)
	n := runtime.Stack(buf, false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return -1
	}
	id, err := strconv.Atoi(string(fields[1]))
	if err != nil {
		return -1
	}
	return id
}

// TestOnDone_RunsOnTheLoopNotTheWorker is the contract every landing
// depends on: OnDone does not fire when the worker finishes, only when
// the loop lands the posted event — and then on the loop's goroutine.
func TestOnDone_RunsOnTheLoopNotTheWorker(t *testing.T) {
	l := &loop{}
	var onDoneGoroutine atomic.Int64
	onDoneGoroutine.Store(-1)
	j := &Job[int]{
		Name:   "loop-check",
		Policy: Refuse,
		Spawn:  l.spawn,
		Post:   l.post,
		OnDone: func(int, error) { onDoneGoroutine.Store(int64(goroutineID())) },
	}

	j.Start(func(context.Context) (int, error) { return 1, nil })
	l.waitPosted(t, 1)
	l.wg.Wait() // the worker has exited; only the loop can land now
	if onDoneGoroutine.Load() != -1 {
		t.Fatal("OnDone ran before the loop landed the event")
	}
	l.landAll()
	if got, want := onDoneGoroutine.Load(), int64(goroutineID()); got != want {
		t.Fatalf("OnDone ran on goroutine %d, want the landing goroutine %d", got, want)
	}
}

// TestOnDone_ReceivesTheError: a failed run lands its error alongside
// the zero result, so the handler decides what a failure looks like.
func TestOnDone_ReceivesTheError(t *testing.T) {
	l := &loop{}
	want := errors.New("boom")
	var got error
	j := &Job[int]{Name: "err", Policy: Refuse, Spawn: l.spawn, Post: l.post,
		OnDone: func(_ int, err error) { got = err }}
	j.Start(func(context.Context) (int, error) { return 0, want })
	l.waitPosted(t, 1)
	l.landAll()
	if !errors.Is(got, want) {
		t.Fatalf("OnDone got %v, want %v", got, want)
	}
}

// TestStart_PanicInWorkReachesSpawnGuard pins that the package adds no
// recover of its own: a panic in work unwinds into the Spawn seam, where
// the app's crash guard restores the terminal and writes the log.
func TestStart_PanicInWorkReachesSpawnGuard(t *testing.T) {
	l := &loop{}
	j := &Job[int]{Name: "panicky", Policy: Refuse, Spawn: l.spawn, Post: l.post}
	j.Start(func(context.Context) (int, error) { panic("worker died") })
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.panics) != 1 || l.panics[0] != "worker died" {
		t.Fatalf("Spawn's guard saw %v, want the worker's panic", l.panics)
	}
	if len(l.queue) != 0 {
		t.Fatal("a panicking run must not post a landing")
	}
}

// TestStart_WithoutSeamsPanics: a job nobody wired is a programming
// error, and it must fail loudly on the loop rather than run an
// unguarded goroutine or post into nothing.
func TestStart_WithoutSeamsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Start on a zero Job should panic")
		}
	}()
	var j Job[int]
	j.Start(func(context.Context) (int, error) { return 0, nil })
}

// TestNotify_LandsOnTheLoop: a Notify event carries its own on-loop
// closure and a timestamp, and runs nothing until landed.
func TestNotify_LandsOnTheLoop(t *testing.T) {
	ran := false
	ev := Notify(func() { ran = true })
	if ev.When().IsZero() {
		t.Fatal("Notify should stamp the event")
	}
	if ran {
		t.Fatal("Notify must not run its closure eagerly")
	}
	var _ tcell.Event = ev
	ev.Land()
	if !ran {
		t.Fatal("Land should run the closure")
	}
}

// TestPolicy_String keeps the names readable in test output.
func TestPolicy_String(t *testing.T) {
	if Coalesce.String() != "Coalesce" || Supersede.String() != "Supersede" ||
		Refuse.String() != "Refuse" || Concurrent.String() != "Concurrent" {
		t.Fatal("policy names drifted")
	}
	if Policy(9).String() != "Policy(9)" {
		t.Fatalf("unknown policy = %q", Policy(9).String())
	}
}
