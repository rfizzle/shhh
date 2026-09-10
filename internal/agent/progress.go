package agent

import (
	"strings"
	"time"
)

// Public progress is the bounded status a long, otherwise silent tool run asks
// the model to put in its next ordinary reply. It is not a check-in: a check-in
// interrupts the turn to change its course, while this only makes the course
// visible and never changes the round protocol.
// See docs/capabilities/coding-agent.md#public-progress-during-a-long-turn.

// DefaultProgressIntervalCalls and DefaultProgressInterval are the two clocks
// that make an otherwise silent run visible. A dozen calls is long enough that
// ordinary parallel reads stay quiet; ninety seconds catches one long command
// even when it was only one call.
const (
	DefaultProgressIntervalCalls = 12
	DefaultProgressInterval      = 90 * time.Second
)

// ProgressPrompt is appended as a machine message at a round boundary when a
// public status is due. It asks for reportable status rather than reasoning: a
// model's private chain of thought is neither dependable nor appropriate for
// the transcript.
const ProgressPrompt = `Before making further tool calls, write one short public progress note (at most three sentences) that states:
- the current objective or hypothesis
- the evidence established so far
- the next action

This is a status update for the person watching. Do not reveal private reasoning, narrate every tool call, or stop working.`

type progressState struct {
	calls     int
	lastProse time.Time
	pending   bool
}

func (a *Agent) progressNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *Agent) resetProgress() {
	a.progress = progressState{lastProse: a.progressNow()}
}

// NoteProgressProse records assistant prose and reports whether it answered a
// checkpoint. Empty tool-call rounds leave the checkpoint pending: accepting
// silence as a status would let a long run evade the only prompt asking it to
// speak to the person watching.
func (a *Agent) NoteProgressProse(text string) (checkpoint bool) {
	if strings.TrimSpace(text) == "" {
		return false
	}
	checkpoint = a.progress.pending
	a.progress.calls = 0
	a.progress.lastProse = a.progressNow()
	a.progress.pending = false
	return checkpoint
}

// TakeProgressCheckpoint reports the public-status prompt owed at a safe
// boundary. It marks that one prompt is outstanding rather than resetting the
// clocks: only actual assistant prose proves that the person received it.
func (a *Agent) TakeProgressCheckpoint() (string, bool) {
	if a.progress.pending || a.progress.calls == 0 {
		return "", false
	}
	if a.progress.lastProse.IsZero() {
		// BeginToolRound is also used by callers restoring an in-flight round.
		// That round has no known silent span, so start its clock here rather
		// than mistaking the zero time for an overdue public update.
		a.progress.lastProse = a.progressNow()
		return "", false
	}
	calls := a.progressCalls
	if calls <= 0 {
		calls = DefaultProgressIntervalCalls
	}
	elapsed := a.progressElapsed
	if elapsed <= 0 {
		elapsed = DefaultProgressInterval
	}
	if a.progress.calls < calls && a.progressNow().Sub(a.progress.lastProse) < elapsed {
		return "", false
	}
	a.progress.pending = true
	return ProgressPrompt, true
}

// ProgressPending reports whether the request about to open was asked to emit
// public status. The headless driver uses it to keep that status out of its
// text-only answer while still emitting a distinct JSONL event.
func (a *Agent) ProgressPending() bool { return a.progress.pending }

// SetProgressIntervals overrides the public-progress clocks. Non-positive
// values restore their defaults, matching the session's other interval knobs.
func (a *Agent) SetProgressIntervals(calls int, elapsed time.Duration) {
	a.progressCalls, a.progressElapsed = calls, elapsed
}
