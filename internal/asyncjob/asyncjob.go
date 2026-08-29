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
// Three policies cover every job the editor has:
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
//     file move, a project replace, a formatter.
//
// Invalidate is the fourth verb every policy shares: it retires whatever
// is in flight (the landing is dropped, the run's context is cancelled)
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
	"time"

	"github.com/gdamore/tcell/v2"
)

// Policy is what a Job does when Start is called while a run is already
// in flight. See the package comment for the three.
type Policy int

const (
	// Coalesce keeps one run in flight and at most one queued follow-up.
	Coalesce Policy = iota
	// Supersede spawns every start; the newest run's landing wins and
	// older ones are dropped.
	Supersede
	// Refuse spawns nothing while a run is in flight; Start returns false.
	Refuse
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
	}
	return fmt.Sprintf("Policy(%d)", int(p))
}

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
	// passes its screen's PostEvent. A post that fails is dropped the
	// way every hand-rolled job dropped it, and the run then never
	// lands — see the note on Start.
	Post func(tcell.Event) error
	// OnDone lands a current run's result on the event loop. A run
	// retired by Invalidate or superseded by a newer Start never
	// reaches it. Runs only from Event.Land.
	OnDone func(T, error)

	// gen is the generation the newest run belongs to. Invalidate bumps
	// it for every policy; Supersede bumps it on every Start too. A
	// landing whose gen no longer matches is stale and dropped.
	gen int
	// inFlight counts runs spawned but not yet landed: 0 or 1 under
	// Coalesce and Refuse, any number under Supersede.
	inFlight int
	// cancel retires the newest run's context. Invalidate and a
	// superseding Start call it so a cooperative worker can stop early.
	cancel context.CancelFunc
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
// If Post fails (a full or closed queue) the run never lands, which under
// Refuse leaves the gate closed. That is the standing behaviour of every
// job before this package and tcell's PostEvent contract, not a choice
// made here.
func (j *Job[T]) Start(work func(ctx context.Context) (T, error)) bool {
	if j.Spawn == nil || j.Post == nil {
		panic(fmt.Sprintf("asyncjob: job %q started without its Spawn/Post seams", j.Name))
	}
	if j.inFlight > 0 {
		switch j.Policy {
		case Refuse:
			return false
		case Coalesce:
			j.queued = work
			return true
		}
	}
	if j.Policy == Supersede {
		j.gen++
		if j.cancel != nil {
			j.cancel()
		}
	}
	gen := j.gen
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.inFlight++
	j.Spawn(j.Name, func() {
		defer cancel()
		res, err := work(ctx)
		_ = j.Post(&Event{when: time.Now(), land: func() { j.land(gen, res, err) }})
	})
	return true
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
	j.inFlight--
	if j.inFlight == 0 {
		j.cancel = nil
	}
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
func (j *Job[T]) Busy() bool { return j.inFlight > 0 }

// Queued reports whether a Coalesce follow-up is waiting on the
// in-flight run. Always false under the other policies.
func (j *Job[T]) Queued() bool { return j.queued != nil }

// Invalidate retires the in-flight run: its landing is dropped and its
// context cancelled. Nothing new starts, and a queued Coalesce follow-up
// is kept — it has not read anything yet, so its answer will be fresh.
// This is "a main-thread mutation made the sweep stale" and "the panel
// closed under its search", both of which used to be a hand-bumped
// generation. Main-thread only.
func (j *Job[T]) Invalidate() {
	j.gen++
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
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
