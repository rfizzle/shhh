package agent

// The bounded retry every driver reaches for.
//
// A request the provider never answered — rate limited, overloaded, a
// connection that died before a token — has nothing to keep, and waiting is
// the whole remedy. How long to wait and how many times lives here because
// the session, the unattended runner and every child have to agree about it:
// three copies of that answer is three answers, and nothing fails when they
// drift apart.
//
// The waiting itself deliberately does not live here. The loop is a passive
// state machine and cannot block on a timer, so a retry is a state the driver
// enters — a meter row the session ticks down and can be pressed out of, a
// plain sleep in a run with nobody in front of it.
// See docs/architecture.md#one-agent-several-front-ends.

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/provider"
)

// MaxRetryAttempts bounds one stall when nothing has said otherwise. Three
// is the number the session's countdown states out loud, because a limit you
// cannot see is indistinguishable from a hang. A setting may raise it, lower
// it, or take the waiting away altogether — see SetLimit.
const MaxRetryAttempts = 3

// retrySpread is the fraction of a wait that is decided at random. It is
// added and never subtracted, because a provider that named its own window
// must not be asked again before that window has rolled over.
//
// The number it has to beat is a request's round trip. A fan-out whose
// children were all refused in the same second are handed the same wait and
// come back in the same second, which re-creates the limit they just sat
// out; only a spread wider than the time a request takes actually pulls them
// apart. A quarter of the two-second first wait is half a second, which is
// several round trips, and it grows with the wait — up to the cap, which
// the spread is not allowed to carry a wait past.
// See docs/capabilities/providers.md#a-stall-is-waited-out-on-one-schedule.
const retrySpread = 0.25

const (
	// minRetryWait floors a provider that asks for an implausibly short wait;
	// maxRetryWait caps one that asks for an hour, because a wait longer than
	// this is a decision for the user, not a countdown.
	minRetryWait = time.Second
	maxRetryWait = 60 * time.Second
	// capReachedAt is the attempt whose doubling already exceeds maxRetryWait
	// — 1<<6 seconds is 64 — and so the last one worth computing.
	capReachedAt = 6
)

// RetryNotice is one attempt a driver is about to make: what failed, how long
// to wait first, and where in the bound this attempt falls. Notice is what a
// surface says about it — the same sentence on stderr, on a child's lane and
// in a transcript, so a reader who has seen one recognises the others.
type RetryNotice struct {
	Failure *provider.Failure
	Wait    time.Duration
	Attempt int
	Max     int
	Notice  string
	// Partial is what the failed request had already streamed and is about to
	// be thrown away. It is the difference between a request that was never
	// answered and a reply that stopped halfway: a surface that has already
	// shown those words has to close them off, or the answer that replaces
	// them runs on from the middle of a sentence nothing will finish.
	Partial string
}

// Signal is what was waited out, as the observability recorder's closed set:
// a word for each of the three classes Failure.Recoverable admits, since
// those are the only ones a wait can follow. "other" answers for a notice
// that carries no failure and for a fourth recoverable class added later —
// counting an unnamed wait under a word that is not its own would report a
// rate limit that never happened, and an empty reason reads as a row nobody
// can explain.
func (n RetryNotice) Signal() string {
	if n.Failure == nil {
		return "other"
	}
	switch n.Failure.Class {
	case provider.ClassRateLimit:
		return "rate-limit"
	case provider.ClassOverloaded:
		return "overloaded"
	case provider.ClassNetwork:
		return "network"
	}
	return "other"
}

// Backoff counts the attempts one stall has used and says how long to wait
// before each. It is the decision and not the wait: Next reports what to do,
// and the driver is what does it.
//
// The count outlives each individual wait, which is what makes the bound a
// bound — a counter that died with the wait it belonged to would grant every
// attempt a fresh three and retry forever. The zero value is ready to use and
// bounded at MaxRetryAttempts.
type Backoff struct {
	attempt int
	// limit and limited are the configured bound and whether there is one.
	// Two fields rather than one, because zero is an answer a setting can
	// give — no attempt at all — and a single count could not tell that
	// apart from a Backoff nobody has configured.
	limit   int
	limited bool
	// jitter is where the spread comes from; nil is the process's own source
	// of randomness. It is a field so that a test can pin it: a schedule
	// whose waits are partly random is otherwise a schedule nothing can
	// assert an exact number about.
	jitter func() float64
}

// SetLimit bounds one stall at the attempts a setting names. A nil limit is
// a file that named none and keeps MaxRetryAttempts — which is deliberately
// what a driver that never calls this gets, because the opposite default
// fails silently: an unattended run that stopped on its first rate limit
// would read as a provider outage rather than as a bound nobody set. Zero is
// a real answer, for a machine that would rather see the failure than sit
// out a wait, and a negative is that same answer since fewer than none is
// not a thing to ask for.
// See docs/capabilities/providers.md#a-stall-is-waited-out-on-one-schedule.
func (b *Backoff) SetLimit(n *int) {
	if n == nil {
		b.limit, b.limited = 0, false
		return
	}
	b.limit, b.limited = max(*n, 0), true
}

// bound is how many attempts this stall may make.
func (b *Backoff) bound() int {
	if !b.limited {
		return MaxRetryAttempts
	}
	return b.limit
}

// spread is this attempt's share of the jitter, in [0, 1).
func (b *Backoff) spread() float64 {
	if b.jitter == nil {
		return rand.Float64()
	}
	return b.jitter()
}

// Next reports whether err earns another attempt and what that attempt is.
// It is false for a failure waiting cannot fix — a rejected key, a request
// that did not fit the window — and false once the bound is used up, which is
// where the driver stops and reports the failure it has.
func (b *Backoff) Next(err error) (RetryNotice, bool) {
	f, ok := provider.AsFailure(err)
	if !ok || !f.Recoverable() {
		return RetryNotice{}, false
	}
	limit := b.bound()
	if limit <= 0 {
		return RetryNotice{}, false
	}
	b.attempt++
	if b.attempt > limit {
		return RetryNotice{}, false
	}
	wait := retryDelay(f, b.attempt, b.spread())
	// Written down here and nowhere else, so that the record of a wait does
	// not depend on which driver was running: a run that went quiet is read
	// back from the log afterwards, and stderr, a lane and a transcript are
	// all gone by then.
	logs.Logger().Info("provider request retried",
		"provider", f.Provider, "class", string(f.Class),
		"wait", wait, "attempt", b.attempt, "of", limit)
	return RetryNotice{
		Failure: f,
		Wait:    wait,
		Attempt: b.attempt,
		Max:     limit,
		Notice: fmt.Sprintf("%s — asking again in %s (attempt %d of %d)",
			f.Headline(), wait.Round(time.Second), b.attempt, limit),
	}, true
}

// Attempt is which attempt of the bound the driver is on, for a surface that
// says so out loud.
func (b *Backoff) Attempt() int { return b.attempt }

// Reset forgets the attempts so far. A request the provider actually answered
// ends the stall, whatever happens next — and so does any decision a user
// made to start, retry or continue a turn, which is not the automatic policy
// the bound exists to limit.
func (b *Backoff) Reset() { b.attempt = 0 }

// retryDelay is how long to wait before attempt n, given that attempt's
// share of the spread. A provider that named its own wait is believed — it
// knows when its window rolls over — and one that did not gets backoff
// doubling off a one-second base, so the first wait is two seconds and the
// third is eight. The spread is added last and to both, because the schedule
// two children that failed together follow is the same schedule.
func retryDelay(f *provider.Failure, attempt int, spread float64) time.Duration {
	// The doubling stops doubling at the cap rather than shifting past it.
	// The bound is a setting, so the attempt number is one a person picked:
	// left to run, the shift walks off the top of the integer and comes back
	// as a wait of no time at all, which is a retry storm rather than a
	// backoff and would read as the provider being hammered.
	wait := min(time.Duration(1<<min(attempt, capReachedAt))*time.Second, maxRetryWait)
	if d := f.RetryAfter; d > 0 {
		wait = min(max(d, minRetryWait), maxRetryWait)
	}
	// The cap is on the wait that actually happens, so the spread cannot
	// carry one past it: a countdown that says a minute and runs for
	// seventy-five seconds is a countdown nobody trusts twice. A wait
	// already at the cap therefore gets no spread, which is the one place a
	// fan-out can still come back in step — and the cheaper of the two
	// mistakes, since the alternative is a bound that is not one.
	return min(wait+time.Duration(spread*retrySpread*float64(wait)), maxRetryWait)
}
