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
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// MaxRetryAttempts bounds one stall. Three is the number the session's
// countdown states out loud, because a limit you cannot see is
// indistinguishable from a hang.
const MaxRetryAttempts = 3

const (
	// minRetryWait floors a provider that asks for an implausibly short wait;
	// maxRetryWait caps one that asks for an hour, because a wait longer than
	// this is a decision for the user, not a countdown.
	minRetryWait = time.Second
	maxRetryWait = 60 * time.Second
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
// attempt a fresh three and retry forever. The zero value is ready to use.
type Backoff struct{ attempt int }

// Next reports whether err earns another attempt and what that attempt is.
// It is false for a failure waiting cannot fix — a rejected key, a request
// that did not fit the window — and false once the bound is used up, which is
// where the driver stops and reports the failure it has.
func (b *Backoff) Next(err error) (RetryNotice, bool) {
	f, ok := provider.AsFailure(err)
	if !ok || !f.Recoverable() {
		return RetryNotice{}, false
	}
	b.attempt++
	if b.attempt > MaxRetryAttempts {
		return RetryNotice{}, false
	}
	wait := retryDelay(f, b.attempt)
	return RetryNotice{
		Failure: f,
		Wait:    wait,
		Attempt: b.attempt,
		Max:     MaxRetryAttempts,
		Notice: fmt.Sprintf("%s — asking again in %s (attempt %d of %d)",
			f.Headline(), wait.Round(time.Second), b.attempt, MaxRetryAttempts),
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

// retryDelay is how long to wait before attempt n. A provider that named its
// own wait is believed — it knows when its window rolls over — and one that
// did not gets backoff doubling off a one-second base, so the first wait is
// two seconds and the third is eight.
func retryDelay(f *provider.Failure, attempt int) time.Duration {
	if d := f.RetryAfter; d > 0 {
		return min(max(d, minRetryWait), maxRetryWait)
	}
	return min(time.Duration(1<<attempt)*time.Second, maxRetryWait)
}
