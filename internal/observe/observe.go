// Package observe is the contract every surface reports a session through:
// the callbacks a runner calls, the position an event happened at, and the
// closed sets of codes those events are made of.
//
// It sits below the surfaces rather than inside one because the record is
// about the product, not about whichever front-end happened to be wired. A
// rate computed over a vocabulary that three runners each keep their own
// copy of is a rate over the copies: a class recorded as `bad-args` on one
// surface and `badargs` on another splits every aggregate that groups by it,
// and nothing fails when it happens.
//
// ClassFromResult, ReasonCode and AskReason are the boundary free text is
// stopped at. Every string that leaves this package is a fixed identifier or
// a code from a set declared here, which is what makes the record safe to
// export without reading it first. A gate suite's name passes through as an
// identifier, on the same footing as a skill's: the user wrote it in the
// project's trusted config, and it names a suite rather than describing
// anything.
//
// Those two read agent's own values, and GateVerdict reads quality's, so
// neither internal/agent nor internal/quality can ever import this package —
// which is deliberate and not a cost. An Observer is wired by whoever runs
// the loop, never held by it: a runner already knows its
// position, its ledger and how its turn ended, and the loop knows none of
// the three.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.
package observe

import (
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/quality"
)

// Pos is where in the session an event happened: the turn, and the tool
// round within it. It is what turns a pile of tool events into a shape —
// forty searches in one turn's round 30–70 is a circling investigation,
// forty searches across forty turns is a session.
//
// A surface that keeps no such accounting passes the zero value, which the
// store already reads as "the recorder had no position". Every surface
// shipping today keeps one: a session and a child count their own turns, and
// a headless run is one turn by construction.
type Pos struct {
	Turn  int64
	Round int64
}

// Observer receives content-free session events. Any callback may be nil.
// Tool names, decisions ("allow"/"deny"/"ask"), outcomes ("ok"/"error"),
// error classes, turn outcomes, signal codes and reason codes are all drawn
// from closed sets; the only free strings are identifiers — a skill's name,
// a saved session's name.
type Observer struct {
	// Usage reports the session's cumulative totals after each request —
	// every request, not just the agent's own, and already priced. The cost
	// comes with the tokens because the recorder cannot work it out for
	// itself: a session mixes models, and pricing one total against one of
	// them is how a mixed session gets misreported.
	Usage func(turns, tokensIn, tokensOut int64, cost float64, priced bool)
	// ToolCall reports one executed tool call. class is the error's class
	// when the outcome is an error, and "empty" for a search that found
	// nothing.
	ToolCall func(at Pos, tool string, duration time.Duration, outcome, class string)
	// Decision reports one mode-policy verdict for a gated tool call.
	Decision func(at Pos, decision, reason string)
	// Turn reports a turn closing: how it ended, how many tool rounds it
	// took and how long it ran. A turn that pauses at its round cap
	// reports once as paused and, if granted more rounds, once more when
	// it finally ends.
	Turn func(turn, rounds int64, duration time.Duration, outcome string)
	// Signal reports one of the loop's own safeguards or a workflow
	// transition firing, with a qualifier from a closed set.
	Signal func(at Pos, code, reason string)
	// Gate reports one completed quality-gate run: the suite that ran and
	// the verdict it came out with.
	//
	// It is its own callback rather than a Signal for two reasons. A
	// signal carries one qualifier and a gate run carries two — the
	// verdict is what a pass rate groups by, the suite is what says which
	// checks that rate is over. And a gate run takes no position: /gate
	// run starts one in the background between turns, so a turn and a
	// round would be real for some runs and invented for the rest.
	// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
	Gate func(suite, verdict string)
	// Session names the saved conversation this session is writing, so
	// metadata and transcript can be joined by someone who asks to.
	Session func(name string)
}

// Decision codes for Observer.Decision.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionAsk   = "ask"
)

// Reason codes for Observer.Decision — why a call was allowed, denied or
// put to the user. Four surfaces now decide (the session, the headless run,
// every sub-agent, and the classifier under each of them) and they must
// spell one verdict one way: "safety" on one surface and "safety-flagged" on
// another is a silent split in every aggregate that groups by reason, and
// nothing fails when it happens.
//
// ReasonCode and AskReason below produce the policy's own subset of these;
// the rest name a decision a surface made for itself. They are deliberately
// not the error classes that happen to share a spelling — a decision's
// reason and a failure's class are separate vocabularies, and pointing one
// at the other renames a reason the day a class is renamed.
const (
	// The static policy's, from ReasonCode.
	ReasonModeAcceptEdits = "mode-accept-edits"
	ReasonModeAuto        = "mode-auto"
	ReasonSessionGrant    = "session-grant"
	ReasonSessionScope    = "session-scope"
	ReasonAllowlist       = "allowlist"
	ReasonPlanMode        = "plan-mode"
	ReasonPlanInspection  = "plan-inspection"
	// AskReason's, for a call the policy hands to a person.
	ReasonSafety         = "safety"
	ReasonScopeSensitive = "scope-sensitive"
	ReasonOutOfScope     = "out-of-scope"
	ReasonPolicy         = "policy"
	// The person's own answer, and the shapes a session offers it in.
	ReasonUser       = "user"
	ReasonUserBatch  = "user-batch"
	ReasonUserAlways = "user-always"
	ReasonMemory     = "memory"
	// The auto-mode classifier's. A classifier that could not decide is its
	// own code rather than a denial, because failing closed to a prompt is
	// not the same event as deciding no.
	ReasonClassifier       = "classifier"
	ReasonClassifierFailed = "classifier-failed"
	// A headless run's, which has no person to ask: the flag opted in, or
	// the default refused.
	ReasonHeadlessYes     = "headless-yes"
	ReasonHeadlessDefault = "headless-default"
	// ReasonOther is a policy reason the map above does not name.
	ReasonOther = "other"
)

// Tool outcomes for Observer.ToolCall. They are the digest's words rather
// than a second pair spelled the same way: the reading a session is steered
// by and the record it is measured by must agree on what "it failed" means.
const (
	OutcomeOK    = digest.OutcomeOK
	OutcomeError = digest.OutcomeError
)

// Turn outcomes for Observer.Turn.
const (
	TurnDone      = "done"
	TurnCancelled = "cancelled"
	TurnFailed    = "failed"
	TurnCapPaused = "cap-paused"
)

// Signal codes for Observer.Signal. Each names the thing that fired; the
// reason beside it is its qualifier, from the closed set the comment gives.
const (
	// SignalRepeat: the repeat detector told the model it was circling.
	// Reason: the tool name.
	SignalRepeat = "repeat-notice"
	// SignalTrim: old tool results were elided to make room. Reason: what
	// TrimReason builds — how many, and where the context estimate stood
	// either side of the elision.
	SignalTrim = "context-trimmed"
	// SignalCompact: the conversation was replaced by a summary of itself
	// and the last turns verbatim. Reason: "asked" (a person compacted it) or
	// "pressure" (the window crossed its line where nobody was watching).
	//
	// The two are one event and one code because they do the same thing to a
	// conversation, and two codes would need adding up before anyone could
	// ask how often a session's history is being thrown away. They are told
	// apart by the qualifier because who asked is the whole question a rate
	// over them is for: an automatic compaction is the mechanism working, and
	// a run of asked ones is a window somebody keeps having to rescue by
	// hand.
	SignalCompact = "compacted"
	// SignalSummary: the summarizer read the session. Reason is the
	// reading's state, from SummaryCode. Every reading is recorded, not just
	// the drifting ones — a drift rate needs its denominator.
	SignalSummary = "summary"
	// SignalSteer: the user sent instructions into a running turn. Reason:
	// how many messages, as a number.
	SignalSteer = "steered"
	// SignalIntervene: the session interrupted its own turn to ask it to take
	// stock. Reason: "steer" (a drift verdict was acted on) or "check-in"
	// (the round interval came round). Separate from SignalSteer because the
	// question a drift rate asks is what the session did on its own, and
	// folding the two together would put the user's own messages in the
	// numerator.
	SignalIntervene = "intervened"
	// SignalTree: the session told its turn the working tree moved in a way
	// its own edits do not explain. Reason: "head" (the commit or branch
	// moved), "paths" (the changed set did), or "both".
	SignalTree = "tree-moved"
	// SignalRetry: a request the provider never answered was waited out and
	// asked again. Reason: "rate-limit", "overloaded", "network", or "other"
	// for a wait this build has no word for — which is what separates a
	// surface being throttled from one losing its connection. One per attempt
	// rather than one per stall, so a count over a population is a count of
	// waits and comparable between surfaces that bound them alike. The
	// session reports it too: it is the one surface where a wait is visible
	// while it happens, which is exactly why it would go unrecorded.
	SignalRetry = "retry"
	// SignalUndo: the user took a turn's edits back. Reason: "".
	SignalUndo = "undo"
	// SignalMode: the permission mode changed. Reason: the new mode.
	SignalMode = "mode"
	// SignalPlan: a plan card was answered. Reason: "approved", "kept" or
	// "rejected".
	SignalPlan = "plan"
	// SignalRounds: a round-cap pause was answered. Reason: "granted" or
	// "uncapped".
	SignalRounds = "rounds"
	// SignalSubagent: a child finished. Reason: its final state.
	SignalSubagent = "subagent"
	// SignalRun: the backlog runner moved. Reason: the action taken, or
	// "replan", "stopped", "kept", "lane-refused".
	SignalRun = "run"
	// SignalSkill: the user activated a skill by command. Reason: its name.
	SignalSkill = "skill"
	// SignalGate: a quality-gate run finished. Reason: its verdict, from
	// GateVerdict. It reaches the record through Observer.Gate rather than
	// Observer.Signal, because it names a subject — the suite — as well as
	// a qualifier.
	SignalGate = "gate"
)

// Event kinds for the stream a run with nobody in front of it writes while
// it works. They are declared here, beside the codes those events carry,
// because the stream and the record are meant to be one vocabulary: a script
// that learns `deny` from a decision line is reading the same word the table
// keeps, so a reader of one already knows the other.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
//
// They are finer than the four kinds the store's own rows are made of, and
// deliberately so. The table holds what a run did, so a call and its result
// are one row; the stream carries what it is doing, where they are two
// events with the wait between them — which is the whole reason to read a
// stream rather than the transcript at the end.
const (
	// EventText: a piece of the answer, as it was written.
	EventText = "text"
	// EventToolCall: a call the model asked for, before it ran or was
	// resolved.
	EventToolCall = "tool-call"
	// EventToolResult: what one call came back with, under the outcome and
	// class ToolOutcome reads off it.
	EventToolResult = "tool-result"
	// EventDecision: one approval verdict, in the Decision and Reason codes
	// above.
	EventDecision = "decision"
	// EventSignal: one of the loop's own safeguards firing, in the Signal
	// codes above and their qualifiers.
	EventSignal = "signal"
	// EventUsage: what the run has spent so far, as Observer.Usage reports
	// it.
	EventUsage = "usage"
	// EventClose: the turn ending, carrying the turn outcome above. It is the
	// last line of every stream and the only one that is always written.
	EventClose = "close"
)

// Compaction reasons for SignalCompact: who asked for it. A trim
// is not one of them — eliding old tool results is SignalTrim, and it is a
// different event with a different cost.
const (
	// CompactAsked: a person compacted the conversation, by command or by
	// answering the card the window pressure raises.
	CompactAsked = "asked"
	// CompactPressure: the window crossed its line in a run with nobody in
	// front of it, and a trim could not bring it back.
	CompactPressure = "pressure"
)

// Gate verdicts for Observer.Gate. The gate's own four, which are already a
// closed set: blocked and cancelled stay apart from fail so an
// infrastructure problem or an interrupted run is never counted as the
// checks having failed, which is the whole reason the gate distinguishes
// them.
const (
	GatePass      = "pass"
	GateFail      = "fail"
	GateBlocked   = "blocked"
	GateCancelled = "cancelled"
	// GateUnknown is a verdict this build has no word for. A gate that
	// grows a fifth one records it as unknown rather than as a pass.
	GateUnknown = "unknown"
	// GateSuiteUnknown stands in for a run whose suite name matched nothing
	// in the project's config. The gate tool takes the name from the model,
	// so an unmatched one is text the model wrote, and it is replaced here
	// rather than stored: a record that is content-free by construction
	// cannot have one path where the model chooses the string.
	GateSuiteUnknown = "unknown-suite"
)

// Session outcomes: how a whole session came out, as against how one turn
// did. A turn outcome is about the loop — it ran, it was cancelled, it hit
// its cap — and the session outcome is about the work, which is the thing
// every other number in the record wants to be correlated against.
//
// It is inferred and never asked for. A card on the way out is answered by
// the people who were pleased and dismissed by the people who were not,
// which is the wrong bias for the one field the record correlates by.
// See docs/capabilities/sessions-and-memory.md#whether-it-worked.
const (
	// SessionCompleted: the last turn to close finished its work.
	SessionCompleted = "completed"
	// SessionInterrupted: the last turn to close was cancelled.
	SessionInterrupted = "interrupted"
	// SessionError: the last turn to close failed.
	SessionError = "error"
	// SessionAbandoned: the session reached its own exit with nothing
	// finished — no turn ever closed, or the only ones that did were
	// pauses at the round cap that nobody granted more rounds to.
	SessionAbandoned = "abandoned"
	// SessionUnknown is the reading of a session that has no outcome
	// recorded, and it is never written. A session killed before its first
	// turn closed is the one that leaves the field empty, and it is a
	// visible category of its own rather than an abandonment: the record
	// does not know what happened, which is a different fact from knowing
	// that nothing was finished.
	SessionUnknown = "unknown"
)

// Error classes for Observer.ToolCall, from the shape of the result text.
// Every executor reports failure as an "error: ..." result, and the classes
// below are the ones a reader can act on differently: a bad-args failure is
// a prompt's fault, an out-of-scope one is policy's, a not-found one is the
// model's picture of the tree being stale.
const (
	ClassDeclined   = "declined"
	ClassPlanMode   = "plan-mode"
	ClassOutOfScope = "out-of-scope"
	ClassNotFound   = "not-found"
	ClassPermission = "permission"
	ClassTimeout    = "timeout"
	ClassCancelled  = "cancelled"
	ClassBadArgs    = "bad-args"
	ClassUnknown    = "unknown-tool"
	ClassOther      = "other"
	// ClassExitStatus is a command that ran and exited non-zero.
	ClassExitStatus = "exit-status"
	// ClassEmpty qualifies a successful search that matched nothing.
	ClassEmpty = "empty"
)

// SummaryCode is a summarizer reading's state as the closed set
// SignalSummary's reason is drawn from. It lives here rather than beside the
// scheduler that happens to take the reading because every unattended
// surface takes the same one: a chat session, a headless run and every
// sub-agent all report this signal, and three spellings of "the run has
// drifted" is three columns nothing can add up.
func SummaryCode(s agent.SummaryState) string {
	switch s {
	case agent.SummaryOnTarget:
		return "on-target"
	case agent.SummaryOffTarget:
		return "off-target"
	case agent.SummarySufficient:
		return "sufficient"
	}
	return "unclear"
}

// ToolOutcome reads one tool result into the two words the record keeps of
// it: whether it worked, and — when it did not — what kind of failure it
// was. It is the whole of what a surface needs to report a call, so no
// surface has a reason to read a result for itself.
func ToolOutcome(result string) (outcome, class string) {
	return digest.Outcome(result), ClassFromResult(result)
}

// TrimReason is the qualifier a context trim reports: how many old tool
// results were elided, and where the context estimate stood before and after
// the elision, each as a share of the window.
//
// The two estimates are what make a run of trims readable. A count on its
// own cannot tell a session that trimmed once and bought real headroom from
// one that shaved itself back to just under its trigger and will do it again
// next round, which is the shape that costs money: every trim throws away
// the prompt prefix the provider was caching.
// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
//
// A share and not a token count, for two reasons, and both of them are about
// what the record is for. Sessions here run on models whose windows differ
// by more than an order of magnitude, so 143002 tokens is a full window on
// one and a seventh of one on the next, and the share is the only figure two
// of them can be read side by side on. And the dashboard counts events by
// their qualifier: raw estimates repeat approximately never, so a qualifier
// built out of them would draw one row per trim that ever happened instead
// of one row per shape of trim. Grouping the ones that look alike is exactly
// what is wanted — a session trimming to just under its own trigger reports
// the same qualifier every round, so the run arrives as one row with a
// count on it rather than as a hundred rows saying "1 time".
func TrimReason(elided, beforePct, afterPct int) string {
	return strconv.Itoa(elided) + " " +
		strconv.Itoa(beforePct) + "%→" + strconv.Itoa(afterPct) + "%"
}

// ReasonCode maps a mode-policy reason string (a closed set produced by
// agent.ModePolicy.Decide) to its enum-like storage code, so free text can
// never leak into the metrics.
func ReasonCode(raw string) string {
	switch raw {
	case agent.ModeAcceptEdits.String() + " mode":
		return ReasonModeAcceptEdits
	case agent.ModeAuto.String() + " mode":
		return ReasonModeAuto
	case "session policy":
		// The blanket grants: every edit, every command (/permissions allow).
		return ReasonSessionGrant
	case "session grant":
		// The scoped ones [a] records — a command's leading words, a file's
		// directory. They are a different decision and count separately.
		return ReasonSessionScope
	case "allowlist":
		return ReasonAllowlist
	case "plan mode":
		return ReasonPlanMode
	case "plan mode inspection":
		return ReasonPlanInspection
	}
	// A refusal for what the call reaches carries the directory in
	// its reason, so it is matched by shape rather than by equality — the
	// free text still never reaches the metrics.
	if strings.HasPrefix(raw, "outside the working scope") {
		return ReasonOutOfScope
	}
	return ReasonOther
}

// AskReason is the reason code recorded when policy falls through to
// prompting the user.
func AskReason(a agent.Action) string {
	switch {
	case a.SafetyFlagged:
		return ReasonSafety
	case a.ScopeSensitive:
		return ReasonScopeSensitive
	case len(a.OutOfScope) > 0:
		return ReasonOutOfScope
	}
	return ReasonPolicy
}

// ClassFromResult names the class of a failed result, or "empty" for a
// search that found nothing, by matching the shape of the text. The text
// itself never leaves this function.
func ClassFromResult(result string) string {
	if !strings.HasPrefix(result, "error:") {
		if strings.HasPrefix(result, "No matches") {
			return ClassEmpty
		}
		return ""
	}
	r := strings.ToLower(result)
	switch {
	case strings.Contains(r, "declined") || strings.Contains(r, "not approved") || strings.Contains(r, "denied"):
		return ClassDeclined
	case strings.Contains(r, "plan mode"):
		return ClassPlanMode
	case strings.Contains(r, "outside the") || strings.Contains(r, "scope"):
		return ClassOutOfScope
	case strings.Contains(r, "cancelled") || strings.Contains(r, "canceled"):
		return ClassCancelled
	case strings.Contains(r, "no such file") || strings.Contains(r, "not found") || strings.Contains(r, "does not exist"):
		return ClassNotFound
	case strings.Contains(r, "permission"):
		return ClassPermission
	case strings.Contains(r, "timed out") || strings.Contains(r, "timeout") || strings.Contains(r, "deadline exceeded"):
		return ClassTimeout
	case strings.Contains(r, "unknown tool") || strings.Contains(r, "no tool executor"):
		return ClassUnknown
	case strings.Contains(r, "invalid") || strings.Contains(r, "missing") || strings.Contains(r, "required") ||
		strings.Contains(r, "unmarshal") || strings.Contains(r, "parse") || strings.Contains(r, "argument"):
		return ClassBadArgs
	}
	return ClassOther
}

// GateVerdict maps a gate run's verdict to the code the record keeps of it,
// so the gate's enum and the record's vocabulary can be renamed
// independently of each other. It is the same shape as SummaryCode: one
// producer's enum, read into the closed set the store is allowed to hold.
func GateVerdict(v quality.Verdict) string {
	switch v {
	case quality.VerdictPass:
		return GatePass
	case quality.VerdictFail:
		return GateFail
	case quality.VerdictBlocked:
		return GateBlocked
	case quality.VerdictCancelled:
		return GateCancelled
	}
	return GateUnknown
}

// GateHook is what a surface assigns to quality.Runner.Observe so every
// verdict reaches the record. It returns nil — which the runner reads as
// "record nothing" — for an observer that takes no gate verdicts, so a
// surface wires it unconditionally.
//
// The adaptation lives here rather than at each surface because there is
// more than one surface and only one right mapping, and two spellings of a
// verdict is two columns nothing can add up. It is also the second half of
// the boundary against free text: the runner hands over no name it did not
// read out of the trusted config, and the empty string it sends instead
// becomes a code from the set above rather than a blank.
func GateHook(o Observer) func(string, quality.Verdict) {
	if o.Gate == nil {
		return nil
	}
	return func(suite string, v quality.Verdict) {
		if suite == "" {
			suite = GateSuiteUnknown
		}
		o.Gate(suite, GateVerdict(v))
	}
}

// SessionOutcome reads a closing turn's outcome as the session's, which is
// the whole of how a session's outcome is inferred: the session came out the
// way the last thing it did came out.
//
// A turn that stopped at its round cap reads as nothing at all, and that is
// deliberate. A cap is a pause and not a close: a session waits for a person
// to grant more rounds, and a sub-agent's supervisor grants them by itself
// and runs on, so a pause read as an abandonment would mark every child that
// took a check-in as abandoned while it was in fact working. Leaving the
// standing outcome alone says what is true — nothing has finished since the
// last thing that did — and a session that quits at the pause is called
// abandoned on the way out, where the pause turns out to have been the end.
//
// Any other turn outcome returns the empty string for the same reason.
func SessionOutcome(turn string) string {
	switch turn {
	case TurnDone:
		return SessionCompleted
	case TurnCancelled:
		return SessionInterrupted
	case TurnFailed:
		return SessionError
	}
	return ""
}
