package agent

// The round tail: the boundary after a tool round's results are recorded and
// before the next request is asked. Every driver of the loop crosses it, and
// what it owes the next round is the same whoever is driving: a hold, then
// steering, then the round cap, then the tree, the reading and the check-ins
// the next request carries. It is written here once, over the seams a driver
// supplies, so the unattended run and the screen cannot come to disagree
// about the order.
//
// It runs on the caller's goroutine. A driver that parks, stops or asks for a
// summary is told so and does it in its own vocabulary — a wait, a row on the
// screen, a command — and comes back to Next when it is let go.

// Step is what a driver does after the round tail has run.
type Step int

const (
	// StepRequest asks the model for the next round.
	StepRequest Step = iota
	// StepHeld parks the turn; the driver calls Next again once the hold
	// is released.
	StepHeld
	// StepCapped is a turn that has used its rounds. What a ceiling means
	// is the driver's to say: a failure for a run with nobody watching, a
	// checkpoint for one with somebody to grant more.
	StepCapped
	// StepCompact asks for a summary of the conversation before the
	// request; the driver calls Next again once it has one.
	StepCompact
)

// RoundTail is the boundary after a tool round's results are recorded, over
// the seams a driver supplies. Next runs it on the caller's goroutine and
// answers what the driver does next. A driver that parks or asks for a
// summary calls Next again when it is let go; what arrived meanwhile joins
// at this boundary, not one round later.
type RoundTail struct {
	Agent *Agent

	// Hold returns nil to go on, or a channel closed at release. A driver
	// that waits (Headless) waits on it; one that parks by returning to its
	// own loop (the screen) hands back any non-nil channel and ignores it.
	Hold func() <-chan struct{}
	// Steer appends whatever arrived while the round ran and reports
	// whether a person's words joined. The tail then treats them as fresh
	// input: Agent.Steered.
	Steer func() bool
	// Target is the instruction a steer quotes back.
	Target func() string
	// Summary, when set, takes the readings the tail ticks and is told the
	// interruptions it delivers. A driver whose readings are weighed where
	// they land leaves it nil: the Agent's queue is the shared state.
	Summary *SummaryRun
	// Recover takes the window-recovery step a driver takes at the tail and
	// reports whether a summary must be asked for before the request. A
	// driver that recovers in front of every request leaves it nil.
	Recover func() bool
	// HoldProgress reports whether the status request must wait for a later
	// boundary. Nil never defers.
	HoldProgress func() bool

	OnTree      func(TreeNotice)
	OnIntervene func(Intervention)
	OnSummary   func(SummaryVerdict)
	OnWithheld  func(reason string)
}

// Next runs the round tail and answers what the driver does next, with the
// channel to wait on where the answer is StepHeld.
func (t RoundTail) Next() (Step, <-chan struct{}) {
	// A hold parks the turn here and nowhere else. The round's results are
	// in the conversation and nothing has been asked of the model yet, so
	// the wait holds no stream, owes no results and leaves the conversation
	// exactly as the round left it. An open stream cannot be paused — a
	// reader that stops reading backs the socket up until the provider gives
	// up on the request — which is why a hold waits for the boundary rather
	// than taking effect where it is asked for.
	//
	// It comes before the steering below, so that what arrived while the
	// turn was parked — a person's redirect, or what the hold had to say
	// about a tree it moved — joins the conversation at this boundary
	// rather than one round after the turn is let go.
	if t.Hold != nil {
		if release := t.Hold(); release != nil {
			return StepHeld, release
		}
	}

	// Steering queued mid-turn joins the conversation between tool rounds;
	// it counts as fresh input, so it also resets the round counter.
	if t.Steer != nil && t.Steer() {
		t.Agent.Steered()
	}

	// The cap is checked after a round's results are recorded, before the
	// next stream request. An uncapped agent never reaches it.
	if t.Agent.CapReached() {
		return StepCapped, nil
	}

	// The tree first, then the question: a check-in asked against a tree
	// the turn has not been told about is answered against the wrong one.
	if n, ok := t.Agent.NextTreeNotice(false); ok {
		t.Agent.AppendMachine(n.Message)
		if t.OnTree != nil {
			t.OnTree(n)
		}
	}

	// A reading due now goes out in the background; whatever an earlier
	// one returned is offered to the policy here. The reading never blocks
	// the round, so a run is never slower for having one.
	if t.Summary != nil {
		t.Agent.SetInterveneBounds(t.Summary.Bounds())
		if v, ok := t.Summary.Tick(t.Agent.Rounds()); ok {
			if t.OnSummary != nil {
				t.OnSummary(v)
			}
			// The age is judged here, where the reading is collected: a
			// verdict is parked when it lands and taken off at the next
			// boundary, so this round is the first one it could have
			// acted on and the only honest place to ask how old it is.
			if reason := t.Agent.ConsiderVerdict(v, t.Agent.Rounds(), true); reason != "" && t.OnWithheld != nil {
				t.OnWithheld(reason)
			}
		}
	}
	if t.HoldProgress == nil || !t.HoldProgress() {
		if prompt, ok := t.Agent.TakeProgressCheckpoint(); ok {
			t.Agent.AppendProgressRequest(prompt)
		}
	}
	// The take-stock check-in, or a steer a reading earned. A run with
	// nobody watching needs it more, not less: there is nobody to ask a
	// turn whether it has enough yet, so the run itself has to ask.
	target := ""
	if t.Target != nil {
		target = t.Target()
	}
	if iv, ok := t.Agent.NextIntervention(target); ok {
		t.Agent.AppendMachine(iv.Message)
		// The reading that judges what happens next is told what was just
		// said, and comes sooner for it. Without that it is handed the
		// evidence that earned the interruption and the verdict it earned,
		// and says the same thing again while the cooldown holds the next
		// one an interval away. A nil Summary is told nothing.
		t.Summary.Intervened(t.Agent.Rounds(), iv)
		if t.OnIntervene != nil {
			t.OnIntervene(iv)
		}
	}

	// The tail is where a turn recovers its window: the boundary in front of
	// a request rather than behind a round, which is the only one that can
	// keep a request from being the one that does not fit.
	if t.Recover != nil && t.Recover() {
		return StepCompact, nil
	}
	return StepRequest, nil
}
