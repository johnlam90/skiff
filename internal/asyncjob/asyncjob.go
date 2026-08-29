// =============================================================================
// File: internal/asyncjob/asyncjob.go
// Author: John Lam <johnlam90@gmail.com>
// Created: 2026-08-29
// Copyright: 2026 John Lam. All rights reserved.
// =============================================================================

// Package asyncjob is the one shape every background job in the editor
// has: capture plain values on the event loop, run a worker on a
// goroutine, post the typed result back as a tcell event, and land it
// on the loop through OnDone. The module owns the correctness story
// that used to be re-derived per job — which run is current, whether a
// second start piles up, queues, or is refused, and the rule that a
// result never touches UI state anywhere but the loop.
//
// Four policies cover every job the editor has:
//
//   - Coalesce: one run in flight, at most one queued follow-up. A start
//     during a run does not spawn; it is remembered and runs once the
//     in-flight one lands. Bursts cost at most two runs. The 10s tree
//     sweep and the git status collection.
//   - Supersede: every start spawns and bumps the generation, so an
//     older run's landing is dropped and the newest wins. A discrete
//     click gesture (a diff load, a branch list) or a keystroke-driven
//     sweep (project find).
//   - Refuse: while a run is in flight, Start returns false and spawns
//     nothing. Mutations that must not race themselves — a git verb, a
//     file move, a project replace.
//   - Concurrent: every start spawns and every landing applies. Runs
//     that are independent of each other and each owe the user their
//     own report — a formatter per saved tab, a user's shell-outs.
//
// Invalidate is the verb every policy shares: it retires whatever is
// in flight (the landing is dropped, the run's context is cancelled)
// without starting anything — a main-thread mutation that made the
// sweep's answer stale, or a panel closing under its own search.
//
// The package is app-free by design. Spawning and posting are injected
// seams: the app passes its crash-guarded goroutine launcher and its
// screen's PostEvent, so this package never decides how a goroutine is
// guarded or how the loop is woken. OnDone runs only from Event.Land,
// which only the loop calls — never on the worker goroutine.
package asyncjob

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Policy is what a Job does when Start is called while a run is already
// in flight. The first three each say that at most one result matters —
// the latest state (Coalesce), the newest gesture (Supersede), or an
// exclusive mutation (Refuse) — which is why a job that had no gate at
// all could not be expressed in them: Refuse would refuse a second
// tab's save, Supersede would drop the first tab's reload, Coalesce
// would replace the middle member of a burst. Concurrent is the fourth
// member for exactly those jobs, where every run is independent and
// every landing owes the user its own report. It is not a default: a
// job that reads shared state or mutates the same resource belongs to
// one of the other three.
type Policy int

const (
	// Coalesce keeps one run in flight and at most one queued follow-up.
	Coalesce Policy = iota
	// Supersede spawns every start; the newest run's landing wins and
	// older ones are dropped.
	Supersede
	// Refuse spawns nothing while a run is in flight; Start returns false.
	Refuse
	// Concurrent spawns every start and applies every landing, in the
	// order they reach the loop. No queue, no refusal, and Start never
	// bumps the generation — only Invalidate does.
	Concurrent
)

// String names the policy for messages and test failures.
func (p Policy) String() string {
	switch p {
	case Coalesce:
		return "Coalesce"
	case Supersede:
		return "Supersede"
	case Refuse:
		return "Refuse"
	case Concurrent:
		return "Concurrent"
	}
	return fmt.Sprintf("Policy(%d)", int(p))
}

// postRetries and postRetryDelay bound how long a worker keeps trying to
// hand its landing to the loop when Post refuses it. tcell's terminal
// screen queues 256 events and PostEvent returns ErrEventQFull rather
// than block, so a refusal means the loop is badly behind; a second of
// retries from a goroutine that has nothing else to do rides that out
// without costing the loop anything. postRetryDelay is a var only so
// tests can shorten the wait — production never reassigns it.
const postRetries = 40

var postRetryDelay = 25 * time.Millisecond

// Job is one background job's whole lifecycle: its policy, the seams it
// spawns and posts through, and the on-loop landing. The exported fields
// are configuration, set once where the app wires its seams; the rest
// is main-thread-only state that Start, Invalidate and the landing keep
// in step. The zero value is not usable — Start panics without both
// seams, because a job that silently ran unguarded goroutines would be
// exactly the bug the seams exist to prevent.
type Job[T any] struct {
	// Name labels the goroutine for the crash guard and the crash log.
	Name string
	// Policy decides what a Start during a run does.
	Policy Policy
	// Spawn launches the worker goroutine. The app passes its crash
	// guard (safeGo); a panic in work reaches it unwrapped.
	Spawn func(name string, fn func())
	// Post hands the finished run's Event to the event loop. The app
	// passes its screen's PostEvent. A refusal is retried from the
	// worker (postRetries × postRetryDelay); a run whose landing still
	// cannot be posted is recorded as lost and stops counting as in
	// flight, so a dropped event can never strand a gate.
	Post func(tcell.Event) error
	// OnDone lands a current run's result on the event loop. A run
	// retired by Invalidate or superseded by a newer Start never
	// reaches it. Runs only from Event.Land.
	OnDone func(T, error)

	// gen is the generation the newest run belongs to. Invalidate bumps
	// it for every policy; Supersede bumps it on every Start too. A
	// landing whose gen no longer matches is stale and dropped.
	gen int
	// genCtx is the context every run of the current generation gets,
	// and genCancel retires it. One context per generation rather than
	// per run keeps "retire whatever is in flight" a single call under
	// every policy: Invalidate and a superseding Start cancel it and
	// let the next Start mint a fresh one.
	genCtx    context.Context
	genCancel context.CancelFunc
	// inFlight counts runs spawned but not yet landed: 0 or 1 under
	// Coalesce and Refuse, any number under Supersede and Concurrent.
	// A run whose landing was lost (see lost) is subtracted the next
	// time the main thread looks.
	inFlight int
	// lost counts runs whose landing the worker could not post after
	// every retry. Written by workers, drained by the main thread in
	// reconcileLost — the one piece of job state that crosses
	// goroutines, and it crosses atomically.
	lost atomic.Int32
	// queued is the Coalesce follow-up: the work of the most recent
	// Start that arrived during a run, captured with the values that
	// Start saw. It spawns when the in-flight run lands.
	queued func(context.Context) (T, error)
}

// Start runs work on a goroutine according to the job's policy and
// reports whether the work was accepted: spawned now, or (Coalesce)
// queued to spawn when the current run lands. Refuse returns false and
// does nothing while a run is in flight. Main-thread only.
//
// work receives the run's context, cancelled by Invalidate and — under
// Supersede — by the next Start; a long worker may watch it to stop
// early, and a worker that ignores it still has its landing dropped.
// work must close over nothing but plain values captured by the caller
// on the loop: it never sees the job or the app, and its return value is
// the only thing that crosses back.
//
// A Post the loop refuses (tcell's queue is bounded and PostEvent does
// not block) is retried from the worker for about a second. If it still
// fails the landing is lost — OnDone never sees it — but the run stops
// counting as in flight, so a Refuse gate reopens and a Coalesce job
// runs again on its next kick rather than queueing behind a run that
// will never land. Every hand-rolled job before this package left its
// busy flag set forever in that case.
func (j *Job[T]) Start(work func(ctx context.Context) (T, error)) bool {
	if j.Spawn == nil || j.Post == nil {
		panic(fmt.Sprintf("asyncjob: job %q started without its Spawn/Post seams", j.Name))
	}
	j.reconcileLost()
	if j.inFlight > 0 {
		switch j.Policy {
		case Refuse:
			return false
		case Coalesce:
			j.queued = work
			return true
		}
	}
	if j.Policy == Coalesce {
		// Spawning now with the latest capture makes any follow-up
		// queued behind a lost run redundant: this IS the follow-up.
		j.queued = nil
	}
	if j.Policy == Supersede {
		j.retire()
	}
	if j.genCtx == nil {
		j.genCtx, j.genCancel = context.WithCancel(context.Background())
	}
	ctx, gen := j.genCtx, j.gen
	j.inFlight++
	j.Spawn(j.Name, func() {
		res, err := work(ctx)
		ev := &Event{when: time.Now(), land: func() { j.land(gen, res, err) }}
		if !j.post(ev) {
			j.lost.Add(1)
		}
	})
	return true
}

// post hands one landing to the loop, retrying a refusal on a short
// bounded schedule. Runs on the worker goroutine; reports whether the
// event was accepted.
func (j *Job[T]) post(ev *Event) bool {
	for attempt := 0; ; attempt++ {
		if err := j.Post(ev); err == nil {
			return true
		}
		if attempt >= postRetries {
			return false
		}
		time.Sleep(postRetryDelay)
	}
}

// reconcileLost folds landings the workers gave up on into the in-flight
// count. Called on the main thread before any decision that reads the
// count, so a lost landing is invisible to callers: Busy clears, a
// Refuse gate reopens, a Coalesce kick spawns instead of queueing.
func (j *Job[T]) reconcileLost() {
	if n := j.lost.Swap(0); n > 0 {
		j.inFlight -= int(n)
	}
}

// retire bumps the generation and cancels the context every in-flight
// run of the old one shares; the next Start mints a fresh one.
func (j *Job[T]) retire() {
	j.gen++
	if j.genCancel != nil {
		j.genCancel()
		j.genCtx, j.genCancel = nil, nil
	}
}

// land is the on-loop half of a run: count it finished, hand a current
// result to OnDone, and (Coalesce) spawn the queued follow-up. The count
// is settled before OnDone runs so a handler that starts the same job
// again — a git verb whose follow-up confirm runs another verb — sees
// the gate open, exactly as the hand-rolled handlers cleared their busy
// flag first. The follow-up spawns only when nothing is in flight, so a
// Start made by OnDone is not raced by it; that run's landing picks the
// queued work up instead.
func (j *Job[T]) land(gen int, res T, err error) {
	j.reconcileLost()
	j.inFlight--
	if gen == j.gen && j.OnDone != nil {
		j.OnDone(res, err)
	}
	if j.Policy == Coalesce && j.queued != nil && j.inFlight == 0 {
		work := j.queued
		j.queued = nil
		j.Start(work)
	}
}

// Busy reports whether any run is in flight — a landing still owed to
// the loop. Tests drain on it; the git panel greys its buttons on it.
func (j *Job[T]) Busy() bool {
	j.reconcileLost()
	return j.inFlight > 0
}

// Queued reports whether a Coalesce follow-up is waiting on the
// in-flight run. Always false under the other policies.
func (j *Job[T]) Queued() bool { return j.queued != nil }

// Invalidate retires every in-flight run: their landings are dropped and
// their shared context cancelled. Nothing new starts, and a queued
// Coalesce follow-up is kept — it has not read anything yet, so its
// answer will be fresh. This is "a main-thread mutation made the sweep
// stale" and "the panel closed under its search", both of which used to
// be a hand-bumped generation. Main-thread only.
func (j *Job[T]) Invalidate() {
	j.retire()
}

// Event is the one tcell event every job lands through. The loop
// handles it with a single case — Land — and never learns which job it
// belongs to; the generation check, OnDone and any follow-up are the
// job's own business, bound into the event when the run finished.
type Event struct {
	when time.Time
	land func()
}

// When satisfies tcell.Event.
func (e *Event) When() time.Time { return e.when }

// Land runs the event's on-loop half. Call it from the event loop only.
func (e *Event) Land() { e.land() }

// Notify builds an Event that runs fn on the loop, for a goroutine with
// no result to land and no policy to apply: a progress tick mid-copy, an
// index rebuild's "done" wake-up from a package that owns its own
// goroutine. It is the same one event type the loop already routes, so
// a notification costs no new case there. fn runs on the loop and may
// touch UI state; the goroutine that posts it must not.
func Notify(fn func()) *Event {
	return &Event{when: time.Now(), land: fn}
}
