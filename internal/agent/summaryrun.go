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
	// gen counts the times a person has extended the target. A reading carries
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
	// onClose is where a reading goes once the run has returned, and nil
	// while it is still running. A parked verdict is collected by the next
	// round boundary, and after the last one there is no next: without this
	// the reading a run ends on would be paid for and read by nobody.
	onClose func(SummaryVerdict)
	// turn counts the turns this runner has served and readTurn is the one
	// the reading in flight was asked in. Readings are turn-scoped, because
	// the round counter they are scheduled and stamped in goes back to zero
	// with every turn: one that outlives its own turn is dropped rather than
	// delivered stamped with rounds another turn is counting, and the two
	// counters are what tell them apart whatever order they lock in.
	turn, readTurn int
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
		r.readTurn = r.turn
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

// StartTurn hands the runner to the next turn of a run that has more than
// one — a child given another instruction at the boundary runs it on
// everything the turn before ran on. The schedule starts again, because the
// round counter it counts in has, and a reading of the turn before goes no
// further, whether it is still out or came back to a verdict nobody
// collected: delivered or ticked off into this turn it would be read
// as this turn's, stamped with rounds this turn is counting, and a verdict is
// what queues an interruption — the turn before's reading would steer the
// turn after it. A run that ended on a round cap or an interrupt is the
// ordinary way one is left parked, since neither path takes a closing
// reading.
//
// What a reading in flight cost is still counted; its request is cancelled
// because nobody can use the answer, and the drop is the turn's and not the
// cancellation's, since a cancelled request comes back failed and a failure
// the run caused itself must not put the summarizer into its backoff. It
// still holds the one in-flight slot until it lands, so the first rounds of
// the new turn may go unread — bounded by one request, and the alternative is
// two readings out at once.
//
// Safe on a nil runner, and on the ordinary run of one turn it is the first
// call and does nothing.
func (r *SummaryRun) StartTurn() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn++
	r.onClose = nil
	r.verdict = nil
	r.sched = SummarySchedule{}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

// Close is the reading a run ends on, and is called where the run returns an
// answer. It ignores the interval and asks what a session's close asks
// (SummarySchedule.CloseDue): a turn long enough to be worth reading, with
// something in it since the last reading. A reading already in flight is
// collected instead of a second one being asked for, and a verdict that
// landed after the last round boundary goes out here too — nothing will ever
// Tick either of them off the parking spot now. Both can happen in one call
// and both are real readings: the parked one is the turn's middle and the
// fresh one is its end.
//
// deliver is handed each verdict on the reading's own goroutine, which for a
// reading that was not back yet is after the run has returned. That is the
// whole shape of this: a summary is never the reason a run is slower, and a
// child's report to its parent is the run's deliverable, so waiting out a
// reading on the end of every child would be the summariser costing the
// fan-out. A surface that outlives the run — a lane under its supervisor, a
// served session — records the verdict when it arrives; a one-shot run that
// has already exited loses it and says nothing. A nil deliver takes no
// closing reading at all: there would be nowhere for it to go, and a reading
// nobody can read is a request nobody should pay for.
//
// The verdict is never offered to the intervention policy. There is no turn
// left to interrupt, which is the same reason a session's close reading is
// applied with the turn already idle. Safe on a nil runner.
func (r *SummaryRun) Close(rounds int, deliver func(SummaryVerdict)) {
	if r == nil || deliver == nil {
		return
	}
	r.mu.Lock()
	parked := r.verdict
	r.verdict = nil
	start := !r.inFlight && r.sched.CloseDue(rounds)
	// A reading in flight is this turn's only if it was asked in this turn.
	// One left over from the turn before was dropped when that turn ended,
	// and adopting it back would deliver the turn before's verdict as this
	// one's.
	adopt := r.inFlight && r.readTurn == r.turn
	if start {
		r.inFlight = true
		r.readTurn = r.turn
	}
	if start || adopt {
		r.onClose = deliver
	}
	r.mu.Unlock()

	if parked != nil {
		deliver(*parked)
	}
	if start {
		go r.read(rounds)
	}
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
	r.inFlight = false
	r.tokensIn += int64(v.Usage.PromptTokens)
	r.tokensOut += int64(v.Usage.CompletionTokens)
	// Whether the run has returned since this went out. Taken under the lock
	// with everything else, and cleared as it is taken: it is one reading's
	// delivery and not a mode the runner stays in.
	deliver := r.onClose
	r.onClose = nil
	if gen != r.gen {
		// A person steered while this was out. It judged the work against
		// part of what has been asked, so its verdict is dropped and its
		// failure is not the summarizer's — what it cost was still spent,
		// and the schedule Extend reset is left where it was put.
		r.mu.Unlock()
		return
	}
	if r.readTurn != r.turn {
		// The turn this read is over and another has started. Its rounds
		// count nothing in this one, so it is neither delivered nor parked
		// — and for the same reason as above, its cost stands and its
		// failure is not the summarizer's.
		r.mu.Unlock()
		return
	}
	r.cancel = nil
	// The turn a closing reading was taken of is over, so it leaves the
	// schedule where the last reading inside the turn put it: the rounds of
	// a turn that has ended have nothing to tell the next one, and a Headless
	// is reused across a child's turns.
	closing := deliver != nil
	if !closing {
		r.sched.Read(rounds)
	}
	if v.Failed {
		// A failed reading changes nothing. The clock still moves, so a
		// provider that is down is retried on the interval rather than on
		// every round.
		r.failures++
		r.mu.Unlock()
		return
	}
	r.failures = 0
	if !closing {
		r.verdict = &v
		r.mu.Unlock()
		return
	}
	// Nothing will Tick this off the parking spot — that was the last round
	// boundary there will be — so it goes straight out, with the lock
	// released first: deliver is the caller's code and holding a lock across
	// it would make every one of them a deadlock waiting to be written.
	r.mu.Unlock()
	deliver(v)
}
