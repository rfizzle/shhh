package agent

// Taking readings of a run nobody is watching.
//
// The chat session schedules its own readings and holds the result on a rail.
// A headless run — which is also every sub-agent — has no rail and nobody in
// front of it, which is precisely why it is the surface that most needs the
// reading: the verdict is what interrupts a turn that has drifted or that
// already has what it needs, and there is no person here to do either by hand.
//
// The rule the chat scheduler is built around holds here too, and is the
// reason this is not a plain synchronous call: **a summary is never the
// reason a run is slower.** The reading goes out in the background at a round
// boundary, and whatever has come back is collected at a later one. A run
// that finishes before a reading lands simply never uses it.
// See docs/capabilities/coding-agent.md#a-reading-for-a-run-nobody-is-watching.

import (
	"context"
	"sync"
	"time"
)

// SummaryRun takes periodic readings of one unattended run and hands the
// verdicts to the Agent's intervention policy. The zero value is not usable;
// call NewSummaryRun. A nil *SummaryRun takes no readings, so a surface that
// was not configured for them wires it unconditionally and pays nothing.
type SummaryRun struct {
	summarizer *Summarizer
	recorder   *Recorder
	// target is the instruction every reading is judged against, captured
	// when the run starts and never re-derived — a run that drifts must not
	// drag its own yardstick along.
	target  string
	started time.Time

	mu        sync.Mutex
	inFlight  bool
	verdict   *SummaryVerdict
	lastRound int
	lastAt    time.Time
	failures  int
	tokensIn  int64
	tokensOut int64
}

// NewSummaryRun returns a runner, or nil when readings are not to be taken —
// no summarizer, a disabled one, or no recorder to read from. Callers do not
// branch on that; a nil runner is safe everywhere.
func NewSummaryRun(s *Summarizer, rec *Recorder, target string) *SummaryRun {
	if !s.Enabled() || rec == nil {
		return nil
	}
	return &SummaryRun{summarizer: s, recorder: rec, target: target, started: time.Now()}
}

// Recorder is where a caller sends the run's activity. Safe on a nil runner.
func (r *SummaryRun) Recorder() *Recorder {
	if r == nil {
		return nil
	}
	return r.recorder
}

// Spend is what the readings have cost this run, for the caller's accounting.
func (r *SummaryRun) Spend() (in, out int64) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokensIn, r.tokensOut
}

// interval is the round interval in force, doubled while readings are
// failing: a provider that is refusing should be asked less often, not at the
// same rate for the rest of the run.
func (r *SummaryRun) interval() int {
	n := r.summarizer.Config().Interval()
	if r.failures >= 2 {
		n *= 2
	}
	return n
}

// Cooldown is the minimum rounds between two verdict-driven interventions,
// derived from the reading interval so it scales with the configuration.
// Zero on a nil runner, which leaves the Agent's default in place.
func (r *SummaryRun) Cooldown() int {
	if r == nil {
		return 0
	}
	return 2 * r.summarizer.Config().Interval()
}

// Tick is called at a round boundary. It starts a reading if one is due and
// none is in flight, and returns whatever earlier reading has since come
// back. Nothing here blocks on a request.
func (r *SummaryRun) Tick(rounds int) (SummaryVerdict, bool) {
	if r == nil {
		return SummaryVerdict{}, false
	}
	r.mu.Lock()
	v := r.verdict
	r.verdict = nil
	due := r.due(rounds)
	if due {
		r.inFlight = true
	}
	r.mu.Unlock()

	if due {
		go r.read(rounds)
	}
	if v == nil {
		return SummaryVerdict{}, false
	}
	return *v, true
}

// due reports whether a reading should go out now. Caller holds the lock.
func (r *SummaryRun) due(rounds int) bool {
	if r.inFlight {
		return false
	}
	if r.lastRound == 0 {
		// The first reading comes early, so a long run has a verdict to act
		// on well before a whole interval has gone by.
		if rounds < FirstSummaryRound {
			return false
		}
	} else if rounds-r.lastRound < r.interval() {
		return false
	}
	return time.Since(r.lastAt) >= r.summarizer.Config().Gap()
}

// read takes one reading and parks the result for the next Tick.
func (r *SummaryRun) read(rounds int) {
	req := SummaryRequest{
		Target:    r.target,
		Activity:  r.recorder.Rows(),
		Assistant: r.recorder.LastAssistant(),
		Round:     rounds,
		Elapsed:   time.Since(r.started),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := r.summarizer.Summarize(ctx, req)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight = false
	r.lastRound = rounds
	r.lastAt = time.Now()
	r.tokensIn += int64(v.Usage.PromptTokens)
	r.tokensOut += int64(v.Usage.CompletionTokens)
	if v.Failed {
		// A failed reading changes nothing. The clock still moves, so a
		// provider that is down is retried on the interval rather than on
		// every round.
		r.failures++
		return
	}
	r.failures = 0
	r.verdict = &v
}
