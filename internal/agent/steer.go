package agent

// Acting on the drift verdict.
//
// The summarizer (summary.go) has always read whether a run is still serving
// the instruction it started from, and the reading was rendered and nothing
// more. This is the half that acts on it: when a reading comes back
// off-target, the turn is told, at the next round boundary, with the
// instruction it was judged against and the reason the reading gave.
//
// Two things about the wording are deliberate, and both come from the judge
// being cheap and fallible. It never asserts that the work has gone wrong —
// the reading is a cheap model's view of a digest, not of the agent's
// reasoning, and a confident accusation against a session that is in fact on
// task is a worse outcome than no steer at all. And it asks the turn to
// compare and continue rather than to stop and re-plan: the failure being
// corrected is a run that has wandered, not one that has to start again.
//
// CheckInPrompt (checkin.go) is the unconditional floor beneath this. That
// one asks whether a turn already has enough; this one asks whether it is
// still answering the question. They are different failures, and a steer
// counts as a check-in (NoteIntervention) so the two never arrive together.
// See docs/capabilities/coding-agent.md#two-failures-two-interruptions.

import (
	"fmt"
	"strings"
)

// DefaultSteerTargetChars bounds the instruction quoted back into the steer.
// The anchor is whatever the user typed, which has no length limit; the steer
// is a message in a conversation that already contains it.
// Steering.SteerTargetChars is the bound a surface can put in its place.
const DefaultSteerTargetChars = 400

// SteerPrompt is the built-in message a drifting turn is given. Target is the
// instruction the turn was judged against, anchored at turn start; reason is
// the reading's own short account of the departure, and may be empty.
func SteerPrompt(target, reason string) string {
	return buildSteer(clampTarget(target, DefaultSteerTargetChars), reason)
}

// SteerWording is the built-in message with its substitutions left standing,
// which is the same text a file replacing it would hold. It is what a
// scaffold writes to start from, and what a wording is compared against to
// decide whether it replaced anything: a file holding exactly this asks the
// model for exactly what the built-in asks, and a record that split the
// sessions either side of it would be reporting a change nobody made.
func SteerWording() string {
	return buildSteer(PlaceholderTarget, PlaceholderReason)
}

// buildSteer assembles the built-in wording from a target that has already
// been bounded, so the bound is the surface's setting and the sentences are
// this function's.
func buildSteer(target, reason string) string {
	var b strings.Builder
	b.WriteString("A background check on this session's activity suggests the work may have moved away from what was asked.\n\n")
	if t := strings.TrimSpace(target); t != "" {
		b.WriteString("What was asked:\n" + t + "\n\n")
	}
	if r := strings.TrimSpace(reason); r != "" {
		fmt.Fprintf(&b, "What the check noticed: %s\n\n", r)
	}
	b.WriteString("The check reads a digest of tool activity, not your reasoning, so it can be wrong. " +
		"Compare what you are doing now against the instruction above, then either say in one line how the current work serves it and carry on, or return to it. " +
		"Do not restart work you have already finished.")
	return b.String()
}

// steerRepeat is the sentence a second and later steer in one turn carries:
// how many times the check has now said the same thing, and what that leaves
// the turn to do about it.
//
// Repetition is the whole of what it adds, because repetition is the whole of
// what the second steer knows that the first did not. The reading that earned
// this one was taken after the answer to the last one — the schedule pulls
// one forward for exactly that purpose — so a turn hearing this has been read
// again and read the same way. It still offers the same two ways out as the
// steer above it, in the same order, because the judge is no more reliable
// the second time: two readings of a digest are two readings of a digest.
// What it withdraws is only the option of answering in words again, which is
// the answer that demonstrably did not move what the check sees.
func steerRepeat(count int) string {
	return fmt.Sprintf("The check has read the work again since and said this %d times this turn. "+
		"If it is wrong, say in one line what the current work does for the instruction above; "+
		"if it is not, the answer is in what you do next rather than in another reply.", count)
}

// TakeSteer returns the steer for a drifting reading, and marks it as an
// intervention so the check-in interval restarts from here. It is the same
// one-call shape as TakeCheckIn, for the same reason.
//
// The caller decides whether a steer is warranted at all — that is a policy
// about readings and rate limits, and it lives with the front-end that holds
// them.
func (a *Agent) TakeSteer(target, reason string) string {
	a.NoteIntervention()
	return a.steerMessage(target, reason)
}

// steerMessage counts this steer against the turn and builds the message for
// it. Every route that delivers one goes through here, because a route that
// built the message itself would deliver a second steer word for word
// identical to the first: the count is the only thing the turn has to tell
// the two apart, and the only thing anything above the turn has to see that
// the first one was not answered.
func (a *Agent) steerMessage(target, reason string) string {
	a.intervene.steers++
	return a.steering.steerPrompt(target, reason, a.intervene.steers)
}
