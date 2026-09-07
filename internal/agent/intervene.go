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

	"github.com/rfizzle/shhh/internal/digest"
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

// targetJoin separates the things a person has asked for in one turn. It is a
// blank line because the target is quoted back to the model whole ("What was
// asked:"), and two instructions run together on one line read as one
// sentence that contradicts itself.
const targetJoin = "\n\n"

// ExtendTarget adds a steer a person typed into a running turn to the
// instruction that turn's readings are judged against: what was asked, then
// what was said since, in the order it was said.
//
// The target is anchored at the turn's start so a run that has drifted cannot
// drag its own yardstick along, and that rule is written against the run. A
// person is the one authority the rest of this machinery already defers to —
// only a human message lifts the round cap — and they were the one input the
// anchor ignored: type "actually, do Y instead" into a running turn and every
// later reading judges Y against X, calls the person's own correction a
// departure, and quotes X back at the model as what was asked.
//
// It extends rather than replaces because a steer is usually a refinement
// ("also check the tests", "skip the docs"). Judged against the refinement
// alone, the work the turn was asked for first reads as off target, which is
// the same argument with the roles swapped.
func ExtendTarget(target, steer string) string {
	steer = strings.TrimSpace(steer)
	if steer == "" {
		return target
	}
	if trimmed := strings.TrimSpace(target); trimmed != "" {
		return trimmed + targetJoin + steer
	}
	return steer
}

// TargetLine is an extended target as the one line a surface quotes it on:
// the first line of each thing the person asked, in order, separated the way
// every other row separates its fields.
//
// It lives beside ExtendTarget because it is the inverse of that join and the
// two cannot be allowed to drift: a surface that took only the first line
// would show a steer as though it had never been typed, which is the failure
// this whole pair exists to end. The caller bounds the result — how much room
// a rail or a row has is the surface's own question.
func TargetLine(target string) string {
	parts := strings.Split(strings.TrimSpace(target), targetJoin)
	lines := make([]string, 0, len(parts))
	for _, p := range parts {
		if line := digest.FirstLine(p); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " · ")
}

// clampTargetParts bounds an extended target for a field that has a budget,
// and returns what was asked as separate parts for the caller to lay out.
//
// Every bound in this package truncates the tail, and the tail of an extended
// target is the newest thing the person said. A long instruction with a steer
// after it would reach the reader as the instruction alone, and the reader
// would go on judging the work against words the person had already replaced
// — the failure ExtendTarget exists to end, arriving by way of the bound
// instead. So the budget is shared out evenly instead: one instruction is
// clamped exactly as it always was, and every further one is guaranteed a
// share of what is left.
func clampTargetParts(target string, limit int) []string {
	parts := strings.Split(strings.TrimSpace(target), targetJoin)
	share := limit
	if len(parts) > 1 {
		share = (limit - len(targetJoin)*(len(parts)-1)) / len(parts)
	}
	if share < 2 {
		// Below two runes there is no share to give: a clamped string is at
		// least the ellipsis that marks it. The newest instruction is the one
		// that changes the answer, so it takes the whole budget and the rest
		// are dropped rather than every one of them being reduced to a mark.
		parts = parts[len(parts)-1:]
		share = limit
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, clampRunes(strings.TrimSpace(p), share))
	}
	return out
}

// clampTarget is the target bounded and joined again as the model is shown
// it — what was asked, then what was said since, a blank line apart.
func clampTarget(target string, limit int) string {
	return strings.Join(clampTargetParts(target, limit), targetJoin)
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
			Message: a.steering.checkInPrompt(a.rounds),
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
//
// A steer typed into a running turn calls it for both of those reasons and not
// only the second: steering resets the round counter the cooldown is counted
// in, and a queued verdict about the work before the steer would be delivered
// after the person had already said the same thing better — the machinery
// arguing with them about an instruction they have moved on from.
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
