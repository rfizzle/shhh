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
	"strings"
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
	started    time.Time

	mu sync.Mutex
	// target is the instruction every reading is judged against: the task the
	// run started on, and any steer a person has sent into it since
	// (Extend). It is under the lock because a reading in flight is reading
	// it from its own goroutine.
	target   string
	inFlight bool
	// gen counts the times the target has been extended. A reading carries
	// the generation it was asked under, so one that was already out when a
	// person steered is discarded when it lands instead of being acted on:
	// it judged the work against an instruction that is no longer all of
	// what was asked.
	gen int
	// cancel stops the reading in flight, and is nil when there is none. A
	// reading whose verdict will be discarded is not worth paying the rest
	// of; the discard is gen's, because a cancelled request comes back as a
	// failure and a failure the run caused itself must not put the
	// summarizer into its backoff.
	cancel  context.CancelFunc
	verdict *SummaryVerdict
	// sched is when the next reading is due, which is the session's schedule
	// too — one predicate, so a rule added to it cannot be forgotten on one
	// of the two surfaces (schedule.go).
	sched SummarySchedule
	// interventions are the interruptions delivered this run, for the next
	// reading's digest.
	interventions []string
	failures      int
	tokensIn      int64
	tokensOut     int64
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

// Target is the instruction this run's readings are judged against, as it
// stands now. Empty on a nil runner, which is what a surface with no readings
// configured hands a steer to quote back — there is nothing to quote.
func (r *SummaryRun) Target() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.target
}

// Extend adds a steer a person sent into the running turn to the instruction
// the readings are judged against, and retires everything that was queued or
// in flight against the shorter one (ExtendTarget explains why it extends
// rather than replaces). The schedule goes back to a turn's start, because
// the round counter a steer resets is the one it counts in and a schedule
// left anchored ahead of it takes no further reading this turn.
//
// The caller retires the Agent's own queue in the same breath
// (StartInterveneTurn): this half is the reading, that half is the verdict a
// boundary would otherwise deliver. Safe on a nil runner.
func (r *SummaryRun) Extend(steer string) {
	if r == nil || strings.TrimSpace(steer) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.target = ExtendTarget(r.target, steer)
	r.gen++
	r.verdict = nil
	r.sched = SummarySchedule{}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
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
	// The interval in force, not the configured one: a run backing off from
	// a failing summariser reads half as often, and a cooldown that did not
	// widen with it would let two interventions land on consecutive
	// readings.
	return r.summarizer.Config().CooldownIntervals() * r.interval()
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
// Everything but "is one already in flight" is the shared schedule's.
func (r *SummaryRun) due(rounds int) bool {
	if r.inFlight {
		return false
	}
	return r.sched.Due(rounds, r.interval(), r.summarizer.Config().Gap())
}

// Intervened records an interruption delivered at this round: the next
// reading is told what was said, and falls due sooner for it, so a run nobody
// is watching judges whether the steer took instead of repeating the verdict
// that earned it. Safe on a nil runner.
func (r *SummaryRun) Intervened(rounds int, iv Intervention) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sched.Intervened(rounds)
	r.interventions = append(r.interventions, iv.Row(rounds))
}

// read takes one reading and parks the result for the next Tick.
func (r *SummaryRun) read(rounds int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.mu.Lock()
	// Copied under the lock rather than shared: the request outlives this
	// call, and a boundary delivering another interruption while it is out
	// would otherwise be appending to a slice the request is reading. The
	// target is copied for the same reason, now that a steer can extend it.
	interventions := append([]string(nil), r.interventions...)
	target, gen := r.target, r.gen
	r.cancel = cancel
	r.mu.Unlock()

	req := SummaryRequest{
		Target:        target,
		Activity:      r.recorder.Rows(),
		Assistant:     r.recorder.LastAssistant(),
		Interventions: interventions,
		Round:         rounds,
		Elapsed:       time.Since(r.started),
	}
	v := r.summarizer.Summarize(ctx, req)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight = false
	r.tokensIn += int64(v.Usage.PromptTokens)
	r.tokensOut += int64(v.Usage.CompletionTokens)
	if gen != r.gen {
		// A person steered while this was out. It judged the work against
		// part of what has been asked, so its verdict is dropped and its
		// failure is not the summarizer's — what it cost was still spent,
		// and the schedule Extend reset is left where it was put.
		return
	}
	r.cancel = nil
	r.sched.Read(rounds)
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
