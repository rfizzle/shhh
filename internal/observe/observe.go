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

	"github.com/rfizzle/agentcore/reading"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

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
	// ClassifierCost reports what the session's classifier has spent so far,
	// from the ledger's classifier origin. It is the session's running
	// figure, not a delta, so a report can price a verdict by it.
	ClassifierCost func(cost float64)
	// ToolCall reports one executed tool call. class is the error's class
	// when the outcome is an error, and "empty" for a search that found
	// nothing. purpose is what a command was for, from ToolPurpose or
	// CommandPurpose, and "" for every other tool.
	ToolCall func(at Pos, tool string, duration time.Duration, outcome, class, purpose string)
	// Decision reports one mode-policy verdict for a gated tool call.
	Decision func(at Pos, decision, reason string)
	// DecisionTimed is Decision for a verdict the classifier reached, with
	// how long the judgement took. A surface calls one or the other for a
	// row, never both — Decided picks — so the record can set one backend's
	// verdicts against another's by time as well as by answer.
	DecisionTimed func(at Pos, decision, reason string, took time.Duration)
	// Turn reports a turn closing: how it ended, how many tool rounds it
	// took and how long it ran. A turn that pauses at its round cap
	// reports once as paused and, if granted more rounds, once more when
	// it finally ends.
	Turn func(turn, rounds int64, duration time.Duration, outcome string)
	// TurnTimed is Turn from a surface that also split the turn's time by
	// what it waited on, and kept its longest quiet stretch. A surface that
	// splits calls it instead of Turn, never as well, so a turn is one row
	// either way; the split's four parts add up to duration.
	// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.
	TurnTimed func(turn, rounds int64, duration time.Duration, outcome string, split agent.TurnSplit)
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
	// WriteFailed reports the first write the record could not make, once:
	// it returns the error the first time it is asked after a failure and
	// nil every time after, so a surface that asks on each pass of its loop
	// raises one notice and then falls silent. The record never interrupts
	// the session; this is how the person learns it has stopped, and it is
	// never shown to the model.
	WriteFailed func() error
}

// Decided reports one verdict: through DecisionTimed where the observer takes
// one and the verdict has a duration, through Decision otherwise.
func (o Observer) Decided(at Pos, decision, reason string, took time.Duration) {
	switch {
	case took > 0 && o.DecisionTimed != nil:
		o.DecisionTimed(at, decision, reason, took)
	case o.Decision != nil:
		o.Decision(at, decision, reason)
	}
}

// InterveneOutcome is what became of a turn the machinery interrupted, as
// SignalOutcome's qualifier. turn is the turn's own closing word, steers how
// many steers it was delivered and withdrawn whether the reader took one
// back.
//
// The order is the order of what it tells a person changing the thresholds.
// A withdrawal is the reader saying the check was wrong, and it outranks
// everything after it because a turn that then finished well finished well
// in spite of the interruption rather than because of it. A second steer is
// the machinery saying its own first one did not work, which is the reading
// the drift thresholds are actually tuned against. Only when neither
// happened does how the turn ended stand for how the interruption went.
func InterveneOutcome(turn string, steers int, withdrawn bool) string {
	switch {
	case withdrawn:
		return InterveneWithdrawn
	case steers > 1:
		return InterveneSteeredAgain
	}
	return turn
}

// GroomReason is SignalTodo's reason for an item read against the tree: the
// word for the reading, and how many of its proposed lines the person
// accepted. The count is in the reason because the question the record
// answers is how often the backlog was wrong and by how much, which a bare
// "the backlog was groomed" cannot.
func GroomReason(accepted int) string {
	return "groom " + strconv.Itoa(accepted)
}

// PlanReason is SignalTodo's reason for a sprint the session recommended:
// the word for the reading, and how many items it put in the set. The size
// is in the reason so the record holds what was recommended beside what was
// accepted and what shipped — which is the one way to tell a planner that
// reads the backlog from one that agrees with whoever is holding the
// keyboard.
func PlanReason(items int) string {
	return "plan " + strconv.Itoa(items)
}

// SummaryCode is a summarizer reading's state as the closed set
// SignalSummary's reason is drawn from. It lives here rather than beside the
// scheduler that happens to take the reading because every unattended
// surface takes the same one: a chat session, a headless run and every
// sub-agent all report this signal, and three spellings of "the run has
// drifted" is three columns nothing can add up.
func SummaryCode(s reading.SummaryState) string {
	switch s {
	case reading.SummaryOnTarget:
		return "on-target"
	case reading.SummaryOffTarget:
		return "off-target"
	case reading.SummarySufficient:
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
// permission.ModePolicy.Decide) to its enum-like storage code, so free text can
// never leak into the metrics.
func ReasonCode(raw string) string {
	switch raw {
	case permission.ModeAcceptEdits.String() + " mode":
		return ReasonModeAcceptEdits
	case permission.ModeAuto.String() + " mode":
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
	case permission.ConversationReadReason:
		return ReasonConversationRead
	case permission.DenyReasonDenylist, permission.DenyReasonHost:
		// Both lists are the same fact for the metrics: a rule the person
		// wrote answered before anything could allow.
		return ReasonDenylist
	case permission.ModePlan.String() + " mode":
		return ReasonPlanMode
	case permission.ModePlan.String() + " mode inspection":
		return ReasonPlanInspection
	case permission.ModeReadOnly.String() + " mode":
		return ReasonReadOnlyMode
	case permission.ModeReadOnly.String() + " mode inspection":
		return ReasonReadOnlyInspection
	}
	if code := HostReason(web.StandingOf(raw)); code != "" {
		return code
	}
	// A refusal for what the call reaches carries the directory in
	// its reason, so it is matched by shape rather than by equality — the
	// free text still never reaches the metrics.
	if strings.HasPrefix(raw, "outside the working scope") {
		return ReasonOutOfScope
	}
	return ReasonOther
}

// HostReason is the code a host's standing is filed under, and "" for an
// unknown standing, which never changes an answer and so is never a reason.
func HostReason(s web.Standing) string {
	switch s {
	case web.StandingKnown:
		return ReasonHostKnown
	case web.StandingYoung:
		return ReasonHostYoung
	case web.StandingDisposable:
		return ReasonHostDisposable
	case web.StandingListed:
		return ReasonHostListed
	}
	return ""
}

// AskReason is the reason code recorded when policy falls through to
// prompting the user.
func AskReason(a permission.Action) string {
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

// ContainmentRequired is the clause every refusal of a command that must be
// contained, on a host with nothing to contain it, carries after whose
// requirement it is. One function words those refusals — the session's, a
// headless run's and a child's alike — round this clause, so ClassFromResult
// files all of them under ClassHarnessContainment without the sentence the
// model reads growing a marker.
// See docs/capabilities/containment.md#containment-can-be-required.
const ContainmentRequired = "containment and no mechanism is in force"

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
	// A command that never started is classified where it failed, and the
	// category travels in the result the model was shown. It is read before
	// the keyword ladder below because the operating system's own words for
	// a missing directory or a refused spawn are exactly the words that
	// ladder reads as a stale path or a denied tool.
	if prereq := harnessClass(tools.ExecPrereqOf(result)); prereq != "" {
		return prereq
	}
	if tools.ExecDidNotCompleteOf(result) {
		return ClassDidNotComplete
	}
	// A refusal for want of containment is read on its first line and before
	// the ladder, for the reason above: the host's reason beside it names a
	// program "not found", which the ladder would file as a stale path.
	if first, _, _ := strings.Cut(result, "\n"); strings.Contains(first, ContainmentRequired) {
		return ClassHarnessContainment
	}
	r := strings.ToLower(result)
	switch {
	case strings.Contains(r, "declined") || strings.Contains(r, "not approved") || strings.Contains(r, "denied"):
		return ClassDeclined
	case strings.Contains(r, "plan mode"):
		return ClassPlanMode
	case strings.Contains(r, "read-only mode"):
		return ClassReadOnlyMode
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

// harnessClass is the record's code for a harness prerequisite that failed,
// or "" for a result that names none. It is the same read as GateVerdict and
// SummaryCode: one producer's closed enum, mapped into the closed set the
// store is allowed to hold, so the two can be renamed independently.
func harnessClass(p tools.ExecPrereq) string {
	switch p {
	case tools.PrereqWorkingDir:
		return ClassHarnessWorkingDir
	case tools.PrereqShell:
		return ClassHarnessShell
	case tools.PrereqContainment:
		return ClassHarnessContainment
	case tools.PrereqPermission:
		return ClassHarnessPermission
	case tools.PrereqSpawn:
		return ClassHarnessSpawn
	}
	return ""
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

// SearchBackend maps the configured search backend's name onto the closed
// set the record keeps of it.
func SearchBackend(name string) string {
	switch name {
	case SearchBrave:
		return SearchBrave
	case SearchSearXNG:
		return SearchSearXNG
	}
	return SearchOther
}

// SearchHook is what a surface points the web toolset at so every search
// reaches the record under the backend that answered it. It returns nil —
// which the toolset reads as "record nothing" — for an observer that takes
// no signals, so a surface wires it unconditionally.
//
// It carries no position, for the reason a gate run carries none: the
// toolset a session builds is the object its children search through, and it
// holds no turn or round of its own. The zero position is what the store
// already reads as "the recorder had no position".
func SearchHook(o Observer) func(string) {
	if o.Signal == nil {
		return nil
	}
	return func(name string) {
		o.Signal(Pos{}, SignalSearch, SearchBackend(name))
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
	case TurnFailed, TurnRejected:
		return SessionError
	}
	return ""
}
