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
// export without reading it first.
//
// Those two read agent's own values, so internal/agent can never import this
// package — which is deliberate and not a cost. An Observer is wired by
// whoever runs the loop, never held by it: a runner already knows its
// position, its ledger and how its turn ended, and the loop knows none of
// the three.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.
package observe

import (
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
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
	// SignalTrim: old tool results were elided to make room. Reason: how
	// many, as a number.
	SignalTrim = "context-trimmed"
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
