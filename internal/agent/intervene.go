package agent

// Deciding when to interrupt a turn, and with what.
//
// Two failures, two messages, one place they arrive.
//
// A turn can fail by going somewhere it was not asked to go, and it can fail
// by arriving and not noticing. The summarizer reads for both — a departure
// is SummaryOffTarget, a run that has what it needs is SummarySufficient —
// and this is the policy that acts on those readings instead of only
// rendering them.
//
// The clock underneath is not replaced by either. A reading needs a
// summarizer that is configured, enabled and answering, and the runs with
// none of that are exactly the ones with nobody watching either, so the
// check-in still fires on the interval for every turn that has no reading to
// go on. What a reading buys is timing: the same question, asked as soon as
// there is a reason to ask it rather than when the counter comes round.
//
// It lives on the Agent because every surface has one. The chat session and a
// headless run — which is also every sub-agent — reach the same decision the
// same way, and a front-end's only job is to deliver what it is handed and
// show that it did.
// See docs/capabilities/coding-agent.md#two-failures-two-interruptions.

import (
	"fmt"
	"strings"
)

// DefaultInterveneCooldown is the minimum rounds between two verdict-driven
// interventions, for a caller that does not set one from its reading
// interval. A reading stands for several rounds, and acting on each of them
// would be the same message three times while the turn is still acting on the
// first.
const DefaultInterveneCooldown = 20

// InterveneKind is what a turn is being interrupted for.
type InterveneKind int

const (
	// InterveneCheckIn: the interval came round. No reading was involved and
	// none is needed — this is the one that runs where there is no
	// summarizer.
	InterveneCheckIn InterveneKind = iota
	// InterveneSteer: a reading says the run has left its instruction. The
	// message names the instruction and the reason.
	InterveneSteer
	// InterveneEnough: a reading says the run has what it needs. The message
	// is the ordinary check-in, arriving early — there is nothing to accuse
	// the turn of, only a question worth asking sooner.
	InterveneEnough
)

// Signal is the kind as the observability recorder's closed set.
func (k InterveneKind) Signal() string {
	switch k {
	case InterveneSteer:
		return "steer"
	case InterveneEnough:
		return "enough"
	}
	return "check-in"
}

// Word is the kind as the next reading's digest names it, a closed set like
// the signal's. The early check-in and the interval's own share a word
// because they share a message: what a sufficiency reading buys is the
// timing, and the turn is asked exactly the same question either way, so a
// digest that distinguished them would be telling the reader something the
// conversation does not say.
func (k InterveneKind) Word() string {
	if k == InterveneSteer {
		return "steered"
	}
	return "check-in"
}

// Intervention is one interruption, ready to deliver: the message that joins
// the conversation, and the one-line account a front-end shows beside it.
type Intervention struct {
	Kind InterveneKind
	// Message is appended as a user message at the round boundary.
	Message string
	// Notice is what the reader is told. An automatic message that changes
	// what their agent does is shown and attributed, because a transcript
	// that hides one is a transcript they cannot trust.
	Notice string
	// Reason is the reading's own account of why this was owed, empty for
	// the interval's own check-in, which is owed for no reason but the
	// clock.
	Reason string
}

// Row is this interruption as one row of the next reading's digest —
// "round 14 · steered · editing files outside the exporter" — so a reader
// judging the work judges it knowing the machinery interrupted, and when.
//
// Nothing a tool wrote can reach it, which is what makes telling the reader
// this affordable at all: the kind is a word from a closed set spelled in
// this package, and the reason is the earlier reading's own words, already
// bounded and already inside the digest's boundary. Neither ever passed
// through a fetched page or a command's output
// (docs/capabilities/coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary).
func (iv Intervention) Row(round int) string {
	parts := []string{fmt.Sprintf("round %d", round), iv.Kind.Word()}
	if reason := collapseSpace(iv.Reason); reason != "" {
		parts = append(parts, clampRunes(reason, maxSummaryReason))
	}
	return strings.Join(parts, " · ")
}

// interveneState is what an Agent knows about interrupting its own turn: the
// verdict it has decided to act on but not yet delivered, and the marks that
// stop it acting twice.
type interveneState struct {
	pending      *SummaryVerdict
	kind         InterveneKind
	verdictRound int
	lastRound    int
	cooldown     int
}

// SetInterveneCooldown sets the minimum rounds between two verdict-driven
// interventions. Callers derive it from the reading interval so it scales
// with whatever the summarizer is configured to; zero or less restores the
// default.
func (a *Agent) SetInterveneCooldown(n int) {
	if n <= 0 {
		n = DefaultInterveneCooldown
	}
	a.intervene.cooldown = n
}

func (a *Agent) interveneCooldown() int {
	if a.intervene.cooldown <= 0 {
		return DefaultInterveneCooldown
	}
	return a.intervene.cooldown
}

// ConsiderVerdict decides whether a fresh reading is worth interrupting the
// turn for, and as what. It only ever queues: delivery is NextIntervention, at
// the round boundary, because a reading lands whenever it lands and a user
// message may not come between an assistant's tool calls and their results.
//
// working is the caller's answer to whether a turn is still running — a
// closing reading arrives after one has stopped, and there is nothing left to
// interrupt. SummaryOnTarget and SummaryUncertain do nothing: an intervention
// on a shrug is worse than no intervention.
func (a *Agent) ConsiderVerdict(v SummaryVerdict, working bool) {
	if !working || v.Failed {
		return
	}
	var kind InterveneKind
	switch {
	case v.State.Drifting():
		kind = InterveneSteer
	case v.State.Sufficient():
		kind = InterveneEnough
	default:
		// A reading that earns nothing still retires a queued one it is
		// newer than. The queue holds a claim about a turn that has since
		// been read again and found not to be drifting, and delivering it at
		// the boundary would steer a session against evidence the session
		// itself already has — the accusation the steer is written to avoid,
		// made after the case for it was withdrawn.
		if p := a.intervene.pending; p != nil && v.Round > p.Round {
			a.intervene.pending = nil
		}
		return
	}
	if v.Round == a.intervene.verdictRound {
		return // this reading has already had its say
	}
	if a.intervene.lastRound > 0 && a.rounds-a.intervene.lastRound < a.interveneCooldown() {
		return
	}
	verdict := v
	a.intervene.pending = &verdict
	a.intervene.kind = kind
}

// NextIntervention returns the interruption this round boundary owes, if any,
// and marks it delivered. A queued reading wins over the clock: it is the same
// question asked for a reason, and asking both in one round is asking twice.
//
// target is the instruction the turn is serving, anchored at its start, for a
// steer to quote back. The caller appends Message and shows Notice; nothing
// here touches the conversation, because the two front-ends record a message
// differently.
func (a *Agent) NextIntervention(target string) (Intervention, bool) {
	if v := a.intervene.pending; v != nil {
		kind := a.intervene.kind
		a.intervene.pending = nil
		a.intervene.verdictRound = v.Round
		a.intervene.lastRound = a.rounds
		a.NoteIntervention()
		if kind == InterveneSteer {
			return Intervention{
				Kind:    InterveneSteer,
				Message: a.steering.steerPrompt(target, v.Reason),
				Notice:  steerNotice(v.Reason),
				Reason:  v.Reason,
			}, true
		}
		return Intervention{
			Kind:    InterveneEnough,
			Message: a.steering.checkInPrompt(a.rounds, FinishedInSession),
			Notice:  enoughNotice(v.Reason),
			Reason:  v.Reason,
		}, true
	}
	if prompt, ok := a.TakeCheckIn(); ok {
		return Intervention{
			Kind:    InterveneCheckIn,
			Message: prompt,
			Notice:  fmt.Sprintf("Check-in — %d rounds used. Taking stock, then carrying on.", a.rounds),
		}, true
	}
	return Intervention{}, false
}

// StartInterveneTurn scopes intervening to a turn that is beginning: a verdict
// about the last instruction must never be delivered against the next one, and
// the cooldown is measured in a round counter that has just gone back to zero.
// The configured cooldown survives, being a setting rather than turn state.
func (a *Agent) StartInterveneTurn() {
	a.intervene.pending = nil
	a.intervene.kind = InterveneCheckIn
	a.intervene.verdictRound = 0
	a.intervene.lastRound = 0
}

// steerNotice and enoughNotice are what the reader is told. The steer is
// attributed on purpose: a message the reader did not write, changing what
// their agent does, is how a transcript stops being something they can trust.
func steerNotice(reason string) string {
	if reason == "" {
		return "Steered — the session looked off target, so it was asked to check its work against the instruction."
	}
	return fmt.Sprintf("Steered — %s. The session was asked to check its work against the instruction.", reason)
}

func enoughNotice(reason string) string {
	if reason == "" {
		return "Check-in — the session looked to have what it needs, so it was asked to take stock early."
	}
	return fmt.Sprintf("Check-in — %s. Asked to take stock early.", reason)
}
