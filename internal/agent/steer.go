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
	return buildSteer(clampRunes(strings.TrimSpace(target), DefaultSteerTargetChars), reason)
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

// TakeSteer returns the steer for a drifting reading, and marks it as an
// intervention so the check-in interval restarts from here. It is the same
// one-call shape as TakeCheckIn, for the same reason.
//
// The caller decides whether a steer is warranted at all — that is a policy
// about readings and rate limits, and it lives with the front-end that holds
// them.
func (a *Agent) TakeSteer(target, reason string) string {
	a.NoteIntervention()
	return a.steering.steerPrompt(target, reason)
}
