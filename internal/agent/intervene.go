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

// InterveneStale is the fourth word of that same closed set, and the only one
// no kind carries: it names an interruption that was owed and withheld
// because the reading that earned it described work the run had already left
// behind. It is spelled here rather than in the recorder so the three that
// were delivered and the one that was not are one vocabulary — a reader
// asking how often the machinery interrupts is asking about the same
// denominator either way.
const InterveneStale = "stale"

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
	// interval is the reading interval in force and cooldownIntervals how
	// many of them pass between two verdict-driven interventions. Both bounds
	// on acting are measured in the first, which is why it is the number the
	// surface hands over rather than the products of it.
	interval          int
	cooldownIntervals int
}

// SetInterveneBounds installs the two numbers every bound on interrupting a
// turn is measured in: the reading interval in force — which a surface
// backing off from a failing summariser has already widened — and how many
// intervals pass before another verdict may interrupt. Zero or less on either
// takes the built-in one.
//
// One call for both, because both are the same surface's answer to the same
// question and a surface that set one and forgot the other would judge a
// reading's age against an interval nobody is reading on. What it buys is
// worth stating: a reading stands for several rounds, so acting on each of
// them would be the same message three times while the turn is still acting
// on the first, and a reading that took longer than the interval to come back
// describes a turn that has since moved on.
func (a *Agent) SetInterveneBounds(interval, cooldownIntervals int) {
	a.intervene.interval = interval
	a.intervene.cooldownIntervals = cooldownIntervals
}

// readingInterval is the interval in force, defaulted. It is the age at which
// a verdict is too old to act on as well as the unit the cooldown is counted
// in, so an unset one falls back to the summarizer's own default rather than
// to zero — zero would drop every reading ever taken.
func (a *Agent) readingInterval() int {
	if a.intervene.interval <= 0 {
		return DefaultSummaryInterval
	}
	return a.intervene.interval
}

func (a *Agent) interveneCooldown() int {
	n := a.intervene.cooldownIntervals
	if n <= 0 {
		n = DefaultCooldownIntervals
	}
	return n * a.readingInterval()
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
//
// rounds is where the run stands now, which is the caller's to say and not
// this object's: a reading is offered where it is collected, and the two
// surfaces collect at different moments — an unattended run at the round
// boundary after the reading landed, a session the moment it lands. The
// verdict carries the round it was asked at, so the two together are the
// reading's age.
//
// It returns the word the record files a withheld interruption under, and
// empty when nothing was withheld or when what withheld it is not an event.
// The cooldown is not one — it is the mechanism working, and its rate is
// already readable from the interventions that did fire — and neither is a
// reading being offered twice. A reading that came back too old to act on is:
// nothing else in the record says the summariser is slower than the run it is
// reading, and a run that looks like it was never off target is exactly what
// that failure looks like from the outside.
func (a *Agent) ConsiderVerdict(v SummaryVerdict, rounds int, working bool) string {
	if !working || v.Failed {
		return ""
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
		return ""
	}
	if v.Round == a.intervene.verdictRound {
		return "" // this reading has already had its say
	}
	// A verdict has an age, and past one interval it is describing work the
	// run left behind. A reading is asked at a round, takes as long as the
	// summarizer takes, and is collected at a boundary after that — so a run
	// of fast read-only rounds against a slow reading can be five rounds on
	// from the departure by the time the verdict arrives. Delivered then, the
	// steer names something the next digest no longer shows, the model
	// compares the two and correctly answers that it is on target, and the
	// run has spent a round and started a cooldown for nothing.
	//
	// One interval is the bound because it is the run's own answer to how
	// long a reading stands for: past it there is another reading due, and
	// the case for interrupting should be made by that one instead. Judged
	// before the cooldown, which a stale verdict may also be inside, because
	// the age is a fact about the reading and the cooldown is a fact about
	// the run — and counting a late reading as a cooldown drop would hide
	// the very thing this is here to make countable.
	if rounds-v.Round >= a.readingInterval() {
		return InterveneStale
	}
	// The same now the age was judged against: one moment per call, so a
	// reading cannot be young enough to act on and old enough to be past a
	// cooldown at the same time.
	if a.intervene.lastRound > 0 && rounds-a.intervene.lastRound < a.interveneCooldown() {
		return ""
	}
	verdict := v
	a.intervene.pending = &verdict
	a.intervene.kind = kind
	return ""
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
