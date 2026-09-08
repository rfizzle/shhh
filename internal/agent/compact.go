package agent

// Compaction: what a conversation that has filled its window is replaced
// with, and the step that replaces it where nobody is asking.
//
// A trim is the cheaper answer and it is the one that runs first. Eliding the
// oldest tool results costs the provider's cached prefix and nothing else,
// and most crossings are settled by it. It runs out of things to give — a
// conversation that is mostly prose, or one already elided to the bone — and
// then the only thing that recovers the window is replacing the conversation
// with a description of it. So compaction is what a trim could not do, never
// the first response to a full window.
//
// The loop never reaches for either. Compaction is a step a driver calls at a
// round boundary, which is the one place the conversation owes nothing: no
// stream is open, no tool call is outstanding, and what is kept can be cut at
// a turn rather than in the middle of one.
// See docs/architecture.md#one-agent-several-front-ends.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/provider"
)

// What a compaction keeps besides the summary.
const (
	// CompactKeepTurns is how many of the most recent user turns are carried
	// through verbatim. A summary is a description of a conversation, and the
	// turn you are in the middle of is the one place a description is not
	// good enough.
	CompactKeepTurns = 2
	// CompactKeepPercent bounds what those turns may occupy of the window. A
	// single turn that read half the repository is not a tail, and keeping it
	// would compact the conversation into the same corner it started in.
	CompactKeepPercent = 15
)

// CompactInstruction is the final user message of a summary request.
const CompactInstruction = "Summarize this conversation so it can be continued from the summary alone. " +
	"Capture the user's goals, key facts and decisions, work completed, current state, and open tasks. " +
	"Reply with only the summary text."

// CompactSummaryPrefix opens the message that carries a summary into the
// restarted conversation. It is a constant because surfaces read it back: a
// resumed session seeds its input history from the user-role messages it
// loads, and this is one of the few nobody typed.
const CompactSummaryPrefix = "Summary of the conversation so far (earlier messages were compacted):"

// CompactSummaryMessage is the user-role message that carries the summary.
func CompactSummaryMessage(summary string) string {
	return CompactSummaryPrefix + "\n\n" + summary
}

// errCompactToolCall is a summary request answered with a tool call. It is a
// backstop rather than an outcome anybody expects: the request forbids a call
// outright, so reaching it means a provider that did not honour that, and a
// summary assembled out of a call's arguments would be a description of the
// conversation written by the thing that was told not to write one.
var errCompactToolCall = errors.New("the model called a tool on a request that forbade one")

// errCompactEmpty is a summary request that came back with nothing in it. The
// conversation is left alone, because the alternative is replacing a session's
// history with an empty string.
var errCompactEmpty = errors.New("the summary came back empty")

// CompactRequest is the conversation with the instruction that asks for a
// summary of it appended.
//
// The caller sends it under provider.ToolChoiceNone, and that is what makes
// the request safe rather than a polite hope: the instruction sits under a
// whole session's worth of tool results, and a model reading it as one more
// turn answers with the call the turn was about to make. The tools stay on
// the request even so — they are the head of the prefix the provider caches,
// so a request that dropped them to prevent a call would rebuild the whole
// head to save a retry.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
func (a *Agent) CompactRequest() []provider.Message {
	return append(a.RequestMessages(),
		provider.Message{Role: provider.RoleUser, Content: CompactInstruction})
}

// CompactKeep is the tail of the conversation a compaction carries through
// verbatim: whole turns, most recent first, bounded by CompactKeepTurns and
// by budget. cal converts a message's estimated size into the units budget is
// measured in.
//
// The boundary is always a user message, and that is what keeps the rebuilt
// conversation well-formed on every dialect rather than merely lossy: a tail
// that started inside a tool round would open with results for calls the
// model can no longer see it made, which every dialect rejects. A tail that
// would be the whole conversation is no tail at all — there would be nothing
// left for the summary to have summarized.
func (a *Agent) CompactKeep(budget int64, cal Calibration) []provider.Message {
	msgs := a.messages
	first := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		first = 1
	}
	var starts []int
	for i := first; i < len(msgs); i++ {
		if msgs[i].Role == provider.RoleUser {
			starts = append(starts, i)
		}
	}
	at := -1
	for n := 1; n <= CompactKeepTurns && n <= len(starts); n++ {
		start := starts[len(starts)-n]
		if start <= first {
			break
		}
		if cal.Apply(EstimateMessageTokens(msgs[start:])) > budget {
			break
		}
		at = start
	}
	if at < 0 {
		return nil
	}
	return append([]provider.Message(nil), msgs[at:]...)
}

// CompactKeptTurns counts the user messages in a kept tail — the turns it is.
func CompactKeptTurns(kept []provider.Message) int {
	n := 0
	for _, msg := range kept {
		if msg.Role == provider.RoleUser {
			n++
		}
	}
	return n
}

// Compact restarts the conversation from the summary: the system prompt, the
// summary as a user message, and the kept tail behind it.
//
// The round counter is deliberately left where it is. Compaction is not
// something the model achieved, and a turn handed a fresh budget every time
// its conversation was recycled would have no ceiling at all where nobody is
// watching. A surface whose user asked for the compaction resets the counter
// itself, because there the request is the user's and a user's message is
// what a fresh budget is for.
func (a *Agent) Compact(summary string, kept []provider.Message) {
	rebuilt := make([]provider.Message, 0, 2+len(kept))
	if len(a.messages) > 0 && a.messages[0].Role == provider.RoleSystem {
		rebuilt = append(rebuilt, a.messages[0])
	}
	rebuilt = append(rebuilt, provider.Message{
		Role: provider.RoleUser, Content: CompactSummaryMessage(summary), Machine: true})
	rebuilt = append(rebuilt, kept...)
	a.SetMessages(rebuilt)
}

// streamOn opens a completion over a stream of the caller's own. The scrub
// belongs to the conversation and not to the stream that happens to carry it,
// so a request asked of another model goes through the same door as the
// turn's own.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (a *Agent) streamOn(stream StreamFunc, msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	if a.scrub != nil {
		msgs = append([]provider.Message(nil), msgs...)
		for i := range msgs {
			msgs[i] = a.scrub(msgs[i])
		}
	}
	return stream(msgs, choice)
}

// CompactAsk opens the summary request and reads it back as prose. The step
// builds the messages and says what the request may do about tools; the
// driver owns the wait, because the wait is the part that has to stay
// interruptible and only the driver knows how its run is interrupted.
type CompactAsk func(msgs []provider.Message, choice string) (string, error)

// CompactNotice is what one recovery step did, for the surfaces that report
// it. The zero value is a step that found nothing to do, which is what almost
// every round boundary is.
type CompactNotice struct {
	// Elided is how many old tool results the trim replaced.
	Elided int
	// Compacted is whether the trim was not enough and the conversation was
	// replaced by a summary of itself.
	Compacted bool
	// Kept is how many turns that summary carried through verbatim.
	Kept int
	// BeforePct and AfterPct are the window occupancy either side of the
	// step, as a share of the window. A share and not a token count, because
	// the models a run may be on differ in window size by more than an order
	// of magnitude and a share is the only figure two of them can be read
	// side by side on.
	BeforePct, AfterPct int
	// Overflow is whether this step answered a request the provider had
	// already refused as too long, rather than a line an estimate had
	// crossed. The shares either side of it are measured against a window
	// that refusal has just corrected, so a reader who saw the round before
	// it report sixty per cent has to be told why this one says a hundred.
	Overflow bool
	// Notice is the line a surface without rails shows for what happened.
	Notice string
	// Err is why a compaction that was attempted did not happen. The
	// conversation is left exactly as it was, and the run goes on towards the
	// window it could not recover — which is what it did before this step
	// existed, so it is reported rather than fatal.
	Err error
}

// Compactor is the window-recovery step and the policy it runs under: how
// full is too full, what a trim may try first, and where the summary request
// goes. A driver calls Recover at a round boundary and does the waiting;
// every other part of the decision is here, so a session, a scripted run and
// a child cannot come to answer one question three ways.
type Compactor struct {
	// Window is the model's context size in tokens. Zero turns the step off,
	// and that is the honest reading of a model nothing could describe: a
	// window nobody can name is not a line anything can cross, and compacting
	// against a guessed one would throw away a conversation that had most of
	// its room left.
	Window int64
	// Model is whose window and whose tokenizer these are, so a correction
	// measured against one model's reports is never applied to another's
	// estimate.
	Model string
	// ToolTokens is what every request carries that the message list does not
	// account for: the tool definitions. Without it the estimate is short by
	// the whole toolset until the first response says what the prompt really
	// came to, which is exactly the stretch where a resumed conversation is
	// at its fullest.
	ToolTokens int64
	// Stream, when set, is where the summary request goes — the model a
	// configuration named for summaries. Nil sends it on the conversation's
	// own stream, which is the only door a surface running one model has.
	Stream StreamFunc
	// Workspace, when set, rewrites the system prompt once the conversation
	// has been rebuilt, so an unattended run goes on from the checkout as it
	// is now rather than as it was at launch, the way the session's own
	// compaction does. Nil keeps the launch prompt, which is right for a
	// child whose prompt names the worktree it stands in.
	// See docs/capabilities/coding-agent.md#the-agent-knows-where-and-when-it-is-standing.
	Workspace func(system string) string

	cal Calibration
	// asked records that a summary was already requested on this crossing. A
	// compaction that came back empty is not tried again until occupancy has
	// fallen back under the line and crossed it afresh: the request carries
	// the whole conversation, and asking for it once a round is the most
	// expensive way there is to keep failing.
	asked bool
}

// Estimate is what the next request will carry, in the units the window is
// measured in: the conversation and the tool definitions, corrected by what
// this model's own reports have said the estimate is worth.
func (c *Compactor) Estimate(msgs []provider.Message) int64 {
	if c == nil {
		return 0
	}
	return c.cal.Apply(c.raw(msgs))
}

// Observe folds one response's reported prompt size into the correction. It
// has to be called while the conversation still holds exactly the messages
// that report describes, which is before the response joins it.
func (c *Compactor) Observe(reported int64, msgs []provider.Message) {
	if c == nil {
		return
	}
	c.cal.Observe(c.Model, reported, c.raw(msgs))
}

// raw is the uncorrected estimate — the figure a report is compared against
// to work the correction out, so it is never taken through one.
func (c *Compactor) raw(msgs []provider.Message) int64 {
	return EstimateMessageTokens(msgs) + c.ToolTokens
}

func (c *Compactor) threshold() int64  { return c.Window * TrimThresholdPercent / 100 }
func (c *Compactor) lowWater() int64   { return c.Window * TrimLowWaterPercent / 100 }
func (c *Compactor) keepBudget() int64 { return c.Window * CompactKeepPercent / 100 }

// Calibrate hands the step a correction that was learned somewhere else. A
// session keeps one of its own — the rails, /stats and the pressure card all
// read it — and a policy built out of that session that started from nothing
// would trim against raw estimates while the screen beside it showed
// corrected ones.
func (c *Compactor) Calibrate(cal Calibration) {
	if c != nil {
		c.cal = cal
	}
}

// Recover is the step: nothing at all while the conversation is under the
// line, a trim once it is over, and a summary only where the trim could not
// bring it back. It reports what it did, and the zero notice is the ordinary
// case.
func (c *Compactor) Recover(a *Agent, ask CompactAsk) CompactNotice {
	if c == nil || a == nil {
		return CompactNotice{}
	}
	return c.step(a, c.Estimate(a.Messages()), ask)
}

// logRecovery writes down what the step did, in the shares of the window it
// did it at. The two figures are the point: a count on its own cannot tell a
// run that trimmed once and bought real headroom from one that shaved itself
// back to just under its trigger and will do it again next round, and the
// second is the shape that turns a long run into a bill.
//
// It is written here rather than at the surfaces because here is where the
// step happens, and it is written at all because every caller of this one is
// a run with nobody in front of it: the session's own recovery is a line in
// the transcript and a block on the rail, in front of the person it happened
// to, and the log is for what has no surface of its own.
// See docs/capabilities/configuration.md#a-failure-is-written-down.
func logRecovery(n CompactNotice) {
	switch {
	case n.Compacted:
		logs.Logger().Info("conversation compacted",
			"before_pct", n.BeforePct, "after_pct", n.AfterPct, "kept_turns", n.Kept)
	case n.Err != nil:
		// The one half of this that is a failure: the window is over the
		// line, the conversation could not be replaced, and the run goes on
		// towards a request the provider will refuse for its size.
		logs.Logger().Warn("conversation not compacted",
			"before_pct", n.BeforePct, "error", n.Err)
	case n.Elided > 0:
		logs.Logger().Info("context trimmed",
			"elided", n.Elided, "before_pct", n.BeforePct, "after_pct", n.AfterPct)
	}
}

// RecoverFrom is the same step measured against an occupancy the caller
// already knows better than Estimate could work it out. A session's figure is
// the provider's own count of the messages that count described, with only
// what has been appended since estimated on top of it — a measurement where
// Estimate is arithmetic — and a policy recovering against a worse number
// than the one on the screen beside it is one question with two answers.
// See docs/capabilities/providers.md#how-full-the-window-is-corrected-by-what-it-cost.
func (c *Compactor) RecoverFrom(a *Agent, before int64, ask CompactAsk) CompactNotice {
	if c == nil || a == nil {
		return CompactNotice{}
	}
	return c.step(a, before, ask)
}

// RecoverOverflow is the step a request the provider refused as too long asks
// for. It is the one failure in the loop that waiting cannot fix and this
// package can: what did not fit is the conversation, and shrinking the
// conversation is what everything above does.
//
// The refusal is also the only hard fact about the window anything here ever
// gets. A pricing table a generation behind, an endpoint serving a quantized
// build under the full model's name, a gateway with a cap of its own — all of
// them look exactly like this from inside, and none of them can be read any
// other way. So the belief is corrected once, downwards, and every round
// after this one is measured against the smaller figure.
func (c *Compactor) RecoverOverflow(a *Agent, ask CompactAsk) CompactNotice {
	if c == nil || a == nil {
		return CompactNotice{}
	}
	est := c.Estimate(a.Messages())
	if est > 0 && (c.Window <= 0 || est < c.Window) {
		c.Window = est
	}
	if c.Window <= 0 {
		// Nothing to measure against and nothing measured: a conversation
		// that estimates at nothing did not overflow anything this step
		// could have shrunk.
		return CompactNotice{}
	}
	// The corrected window is what forces the step past its own threshold:
	// the conversation now measures a full window or more, however far under
	// the line the figure it was refused under put it. And a refusal is a
	// crossing no estimate saw, so the one attempt a summary that failed
	// spent on the last crossing is not what this one inherits.
	c.asked = false
	n := c.step(a, est, ask)
	n.Overflow = true
	n.Notice = compactNoticeText(n)
	return n
}

// step is the recovery itself, against whatever occupancy the caller reached
// it with. Recover, RecoverFrom and RecoverOverflow all come through here, so
// the reading logRecovery takes is taken once, off the notice, rather than
// at each of the returns below.
func (c *Compactor) step(a *Agent, before int64, ask CompactAsk) CompactNotice {
	n := c.recover(a, before, ask)
	logRecovery(n)
	return n
}

// recover is the step's body.
func (c *Compactor) recover(a *Agent, before int64, ask CompactAsk) CompactNotice {
	if c.Window <= 0 {
		return CompactNotice{}
	}
	if before <= c.threshold() {
		// Back under the line: the next crossing is a new crossing, and gets
		// its own attempt at a summary.
		c.asked = false
		return CompactNotice{}
	}
	n := CompactNotice{BeforePct: percentOfWindow(before, c.Window)}
	elided, after := a.TrimOldToolResults(before, c.threshold(), c.lowWater(), c.cal)
	n.Elided, n.AfterPct = elided, percentOfWindow(after, c.Window)
	if after <= c.threshold() {
		// The trim cleared the line, so the crossing is over here as surely
		// as if the conversation had never reached it. Both ways back under
		// re-arm, and they have to: a flag cleared only at the top of the
		// function would stay set through a crossing that a trim happened to
		// settle, and the next one — a genuinely new one — would find the
		// one attempt already spent on a summary that failed rounds ago.
		c.asked = false
		n.Notice = compactNoticeText(n)
		return n
	}
	if ask == nil || c.asked {
		n.Notice = compactNoticeText(n)
		return n
	}
	c.asked = true
	summary, err := ask(a.CompactRequest(), provider.ToolChoiceNone)
	if summary = strings.TrimSpace(summary); err != nil || summary == "" {
		if errors.Is(err, ErrInterrupted) {
			// A request nobody waited for was not an attempt. The run is
			// ending, so there is nothing to report either — and a child
			// that goes on to another turn must not find its one attempt
			// spent on a request that was cancelled out from under it.
			c.asked = false
			return n
		}
		n.Err = err
		if n.Err == nil {
			n.Err = errCompactEmpty
		}
		n.Notice = compactNoticeText(n)
		return n
	}
	kept := a.CompactKeep(c.keepBudget(), c.cal)
	a.Compact(summary, kept)
	c.rewriteSystem(a)
	n.Compacted, n.Kept = true, CompactKeptTurns(kept)
	n.AfterPct = percentOfWindow(c.Estimate(a.Messages()), c.Window)
	n.Notice = compactNoticeText(n)
	return n
}

// open is where the summary request goes: the stream the compactor was given,
// or the conversation's own when it was given none.
func (c *Compactor) open(a *Agent, msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	stream := c.Stream
	if stream == nil {
		stream = a.stream
	}
	return a.streamOn(stream, msgs, choice)
}

// percentOfWindow is a share of the window, floored at nothing so a notice
// never states a negative occupancy.
func percentOfWindow(n, window int64) int {
	if window <= 0 || n <= 0 {
		return 0
	}
	return int(min(n*100/window, 100))
}

// overflowRefusal opens the line an overflow recovery reports under, and is
// the whole of it where there was nothing left to recover. It is said before
// the figures rather than instead of them: a run that recovered from a
// refusal and one that trimmed at a line it estimated for itself did the same
// thing for opposite reasons, and only this sentence tells them apart.
const overflowRefusal = "The provider refused the request as too long for the window."

// compactNoticeText is the one wording every unattended surface reports this
// step in. Two vocabularies for one event is two things for a reader to learn
// about something that happened once.
func compactNoticeText(n CompactNotice) string {
	text := ""
	switch {
	case n.Compacted && n.Kept > 0:
		text = fmt.Sprintf("Context compacted at %d%%: continuing from a summary and the last %d %s (now %d%%).",
			n.BeforePct, n.Kept, plural(n.Kept, "turn"), n.AfterPct)
	case n.Compacted:
		text = fmt.Sprintf("Context compacted at %d%%: continuing from a summary (now %d%%).",
			n.BeforePct, n.AfterPct)
	case n.Err != nil:
		text = fmt.Sprintf("Context is at %d%% and could not be compacted: %s.", n.BeforePct, n.Err)
	case n.Elided > 0:
		text = fmt.Sprintf("Context trimmed at %d%%: %d older tool %s elided (now %d%%).",
			n.BeforePct, n.Elided, plural(n.Elided, "result"), n.AfterPct)
	}
	switch {
	case !n.Overflow:
		return text
	case text == "":
		return overflowRefusal
	}
	return overflowRefusal + " " + text
}

// rewriteSystem hands the system prompt to the workspace rewriter and puts
// the result back through the agent's own door, copied first: the
// conversation is the agent's to hold, and a write through the slice it
// handed back is one nothing in the agent can see.
func (c *Compactor) rewriteSystem(a *Agent) {
	if c.Workspace == nil {
		return
	}
	msgs := a.Messages()
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		return
	}
	rebuilt := c.Workspace(msgs[0].Content)
	if rebuilt == msgs[0].Content {
		return
	}
	updated := append([]provider.Message(nil), msgs...)
	updated[0].Content = rebuilt
	a.SetMessages(updated)
}
