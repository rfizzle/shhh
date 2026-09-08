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
	// changes is what the surface has written so far, in the three numbers a
	// changeset states, and nil where the surface keeps no count of it at
	// all — a conversation, which has no editor. A surface that counts and
	// has written nothing answers zero, which is the same empty field. It is
	// set once before the run starts and read from the reading's goroutine,
	// so it is outside the lock with the summarizer and the recorder; what
	// it reaches for holds its own.
	changes func() (files, added, removed int)
	// alerts is the standing bad news — the checks that came back broken and
	// have not come back green since — and plan the approved plan's steps
	// with their states. Both are nil where the surface has no such thing to
	// answer with, and both are read on the same terms as changes: set once
	// before the run starts, called from the reading's goroutine, and
	// responsible for their own guarding.
	//
	// They are the two fields that tell "off target" from "on target and the
	// tests are red". A reading that cannot see either has one verdict for
	// both, and only one of them is worth a steer: work that has drifted
	// needs redirecting, work that is on the plan with a failing check needs
	// leaving alone to fix it.
	alerts func() []string
	plan   func() []string

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
	// previous is the last reading's own text, which the next one is asked to
	// revise rather than write again from nothing — what stops a run that has
	// not changed course being described four different ways. It is turn-
	// scoped for the reason the schedule is: a reading of the turn before
	// judged work this turn has not done, and offered as this turn's previous
	// it would be a claim the reader has no evidence for and every reason to
	// carry forward. Only a reading that landed is kept — one dropped for
	// judging an instruction a steer had already extended was never anybody's
	// word on the run.
	previous  string
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

// WithChanges names where the run's changeset is counted, and returns the
// runner so a caller wires it in one expression. A surface with no changeset
// calls nothing, and its digest carries no changed-file line, as before.
//
// It is the count rather than the wording because there is one wording
// (SummaryChanges): a surface that spelled its own would be handing the
// reader evidence in a dialect the instruction it judges against was not
// written for. Safe on a nil runner, which is why it may be chained onto
// NewSummaryRun.
func (r *SummaryRun) WithChanges(count func() (files, added, removed int)) *SummaryRun {
	if r == nil {
		return nil
	}
	r.changes = count
	return r
}

// WithAlerts names where the surface's standing bad news is read from, and
// returns the runner so a caller wires it in one expression. An unattended
// turn's source is the gate that runs at its close: a suite that came back
// failing is what makes "the work is on target and the tests are red" a
// different reading from "the work has drifted", which are the two the
// intervention policy must not confuse. A surface with no checks calls
// nothing and its digest carries no failing-checks field.
//
// What the supplier answers with is rows, not a verdict object, for the
// reason the digest is rows everywhere: a check's own output is what must
// never reach the thing that steers, so the caller states the check and how
// it came back and nothing it printed. Safe on a nil runner.
func (r *SummaryRun) WithAlerts(alerts func() []string) *SummaryRun {
	if r == nil {
		return nil
	}
	r.alerts = alerts
	return r
}

// WithPlan names where an approved plan's steps and their states are read
// from, so a reading judging "on target" has the declared list in front of it
// rather than only the work. It is the same field a session fills from its own
// checklist, and the same wording, so one instruction judges both.
//
// A surface that never had a plan approved calls nothing, which is every run
// with nobody in front of it today: plan mode's refusals need somebody to
// approve them. Safe on a nil runner.
func (r *SummaryRun) WithPlan(plan func() []string) *SummaryRun {
	if r == nil {
		return nil
	}
	r.plan = plan
	return r
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
// same rate for the rest of the run. Caller holds the lock.
func (r *SummaryRun) interval() int {
	n := r.summarizer.Config().Interval()
	if r.failures >= 2 {
		n *= 2
	}
	return n
}

// Bounds are the two numbers this run's intervention policy measures in: the
// reading interval in force, and how many of them a cooldown runs for. Zeroes
// on a nil runner, which leaves the Agent's defaults in place.
//
// The interval in force, not the configured one: a run backing off from a
// failing summariser reads half as often, so a cooldown that did not widen
// with it would let two interventions land on consecutive readings, and a
// verdict would be called too old to act on while the next reading was still
// half an interval away.
func (r *SummaryRun) Bounds() (interval, cooldownIntervals int) {
	if r == nil {
		return 0, 0
	}
	// Under the lock because the failure count the interval widens on is
	// written by the reading's own goroutine, and this is read from the round
	// boundary while one is out.
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interval(), r.summarizer.Config().CooldownIntervals()
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
	r.previous = ""
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
	target, gen, previous := r.target, r.gen, r.previous
	r.cancel = cancel
	r.mu.Unlock()

	req := SummaryRequest{
		Target:        target,
		Activity:      r.recorder.Rows(),
		Assistant:     r.recorder.LastAssistant(),
		Changes:       r.changed(),
		Alerts:        supplied(r.alerts),
		Plan:          supplied(r.plan),
		Interventions: interventions,
		Round:         rounds,
		Elapsed:       time.Since(r.started),
		Previous:      previous,
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
	// The reading that landed is what the next one revises. The line is
	// above the branch rather than inside it because that sentence is the
	// whole rule, and a second place to remember it is a second place to
	// forget it: a closing reading has no next one in this turn, and the
	// turn after starts with none whether it was kept here or dropped for
	// landing after that turn had begun.
	r.previous = v.Text
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

// changed is the run's changeset for the digest, empty where the surface
// keeps none. It is called off the lock, on the reading's own goroutine: what
// it counts is the caller's state, guarded by the caller, and holding this
// runner's lock across somebody else's would be an ordering nothing here can
// see.
func (r *SummaryRun) changed() string {
	if r.changes == nil {
		return ""
	}
	return SummaryChanges(r.changes())
}

// supplied is one of the list-valued digest fields, empty where the surface
// names no source for it. Called off the lock for the reason changed is: what
// it reads is the caller's state and holding this runner's lock across the
// caller's own would be an ordering nothing here can see.
func supplied(f func() []string) []string {
	if f == nil {
		return nil
	}
	return f()
}
