// Package record is the record's vocabulary: the position an event happened
// at, the closed sets of codes an event is made of, the attribute keys the
// exporter files them under, the shape a child's end takes, and the lists the
// store reads them back through.
//
// It is a leaf of its own so that a package which only stores or serves the
// record, such as the store and the RPC surface, does not take a dependency
// on everything the mapping functions read: the agent loop, the quality gate,
// the web policy and the tools. The strings here are written to the database
// and read back by every build, so a value is never renamed. internal/observe
// re-exports each name, so its other callers are unchanged, and keeps the
// functions that map a result or a verdict onto these words.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.
package record

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
	// A fetch a conversation made, which it makes without a card because
	// nothing it reads can change anything. It is its own code rather than a
	// grant's, because nobody granted it: a comparison of a conversation's
	// reads with a coding session's grants has to tell the two apart
	// (docs/capabilities/chat.md#a-conversation-has-one-mode).
	ReasonConversationRead = "conversation-read"
	// A fetch whose host's standing changed the answer: a known host let
	// through where auto mode would have asked its classifier, or a young,
	// disposable or listed one put to the person where the classifier had
	// let it through. One code per standing, and none for unknown, which
	// never changes an answer. They are codes of their own rather than the
	// classifier's, because what a reader does about one is turn a list off
	// or put a host on their own list, and a rate that mixed them with the
	// classifier's verdicts could not say how often a list decided
	// (docs/capabilities/approvals-and-safety.md#a-host-is-read-against-the-world-before-it-is-judged).
	ReasonHostKnown      = "host-known"
	ReasonHostYoung      = "host-young"
	ReasonHostDisposable = "host-disposable"
	ReasonHostListed     = "host-listed"
	// A command the deny list refused. It is its own code rather than one
	// of the mode's, because what a reader does about it is edit a list and
	// not change a mode, and a rate that mixed the two would answer neither
	// question.
	ReasonDenylist = "denylist"
	// A call one of the person's own hooks answered — refused outright, or
	// handed back to them because the hook asked or failed. It is its own
	// code for the same reason the deny list's is: what a reader does about
	// it is edit a hook, and a rate that mixed it with a mode would answer
	// neither question.
	ReasonHook = "hook"
	// The two read-only modes keep separate codes for the same policy,
	// because what a reader does about a refusal differs: a plan-mode session
	// is on its way to a card, and a read-only one is not going to run
	// anything this conversation.
	ReasonPlanMode           = "plan-mode"
	ReasonPlanInspection     = "plan-inspection"
	ReasonReadOnlyMode       = "read-only-mode"
	ReasonReadOnlyInspection = "read-only-inspection"
	// AskReason's, for a call the policy hands to a person.
	ReasonSafety         = "safety"
	ReasonScopeSensitive = "scope-sensitive"
	ReasonOutOfScope     = "out-of-scope"
	ReasonPolicy         = "policy"
	// The person's own answer, and the shapes a session offers it in.
	ReasonUser      = "user"
	ReasonUserBatch = "user-batch"
	// ReasonUserAmended is an allow of a line the reader wrote in place of
	// the one the call carried
	// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
	// It is its own code rather than a plain allow because the two answer
	// different questions about how the product is used: a rate that mixed
	// them could not say how often what the model proposed was nearly right,
	// which is the whole reason the key exists.
	ReasonUserAmended = "user-amended"
	// The three grants the always-allow list offers, one code each. They are
	// three rather than one because they answer three different questions
	// about how the product is used — how often a reader wants a permission
	// only for the work in front of them, how often they want one that
	// stands, and how often the shape the card offered was wider than the
	// thing they meant to allow — and a rate that mixed them would answer
	// none of the three
	// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
	ReasonUserTurn   = "user-turn"
	ReasonUserAlways = "user-always"
	ReasonUserExact  = "user-exact"
	// ReasonUserStop is the person stopping a turn while a call the
	// classifier allowed was still running: the answer to a yes that had no
	// card to be answered at. Its outcome is deny. It is how often the person
	// overturned an allow, as the next decision under ReasonUser is how often
	// they overturned a deny.
	ReasonUserStop = "user-stop"
	ReasonMemory   = "memory"
	// The auto-mode classifier's. A classifier that could not decide is its
	// own code rather than a denial, because failing closed to a prompt is
	// not the same event as deciding no. A denial under this code is the
	// classifier's verdict: where a person was there to be asked, their
	// answer is the next decision, under ReasonUser, and the pair is how
	// often they overturned it
	// (docs/capabilities/approvals-and-safety.md#the-classifier-fails-closed).
	ReasonClassifier       = "classifier"
	ReasonClassifierFailed = "classifier-failed"
	// A headless run's, which has no person to ask: the flag opted in, or
	// the default refused.
	ReasonHeadlessYes     = "headless-yes"
	ReasonHeadlessDefault = "headless-default"
	// ReasonOther is a policy reason the map above does not name.
	ReasonOther = "other"
)

// Turn outcomes for Observer.Turn.
const (
	TurnDone      = "done"
	TurnCancelled = "cancelled"
	TurnFailed    = "failed"
	TurnCapPaused = "cap-paused"
	// TurnRejected is the turn the provider ended by objecting to the
	// request rather than by failing to answer it: a key it would not take,
	// an account with nothing left on it, a model id it does not serve, a
	// request larger than the window. Asking again unchanged gets the same
	// answer, which is the whole of what separates it from TurnFailed —
	// there the provider stopped answering and later it may not.
	//
	// The two add up to "turns that broke", and an aggregate that wants that
	// has to add them. Splitting them is worth that because the unattended
	// surfaces project the process's exit status off this value, and the one
	// distinction a script cannot make for itself from a status is whether
	// waiting is a plan.
	// See docs/capabilities/headless.md#the-exit-code-is-the-contract.
	//
	// Only a surface holding a classified provider failure at the seam that
	// closes the turn writes it. A session's own broken turn is TurnFailed
	// whatever broke it, so a population read across surfaces should read
	// the two together.
	TurnRejected = "rejected"
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
	// SignalSteer: instructions reached a running turn from outside it.
	// Reason: how many messages, as a number, where a person typed them into
	// their own session's turn. Where the subject is a sub-agent the reason is
	// the source instead — "lane" (the person opened the child's lane and
	// typed) or "parent" (the orchestrator that wrote the task redirected
	// it) — because a fan-out has two of them and a rate that cannot tell
	// them apart cannot say whether the orchestrator is answering what it
	// sees. "landing" is the third: a patch another writer landed would not
	// carry into this writer's copy, and the writer was told where the two
	// met. "session" is the fourth, and the one whose subject is a session
	// rather than a child: another session on the machine handed this one a
	// line, and a count would file a colleague's note as the person's own
	// typing. The supervisor owns those words, the way SignalIntervene's are
	// the loop's. A child's own reading is not among them: an interruption
	// the machinery delivered is SignalIntervene's "steer" below, and a
	// second record here would count one steer twice.
	SignalSteer = "steered"
	// SignalIntervene: the session interrupted its own turn to ask it to take
	// stock, or owed itself an interruption and withheld it. Reason: "steer"
	// (a drift verdict was acted on), "enough" (a reading said the turn had
	// what it needs, so the check-in came early), "check-in" (the round
	// interval came round), or "stale" (a reading earned an interruption and
	// came back describing rounds the turn had already passed, so none was
	// delivered). The withheld one shares the code because it is the same
	// population: a run whose summariser is slower than its rounds looks from
	// the outside exactly like one that never drifted, and the qualifier is
	// what tells them apart. Separate from SignalSteer because the question a
	// drift rate asks is what the session did on its own, and folding the two
	// together would put the user's own messages in the numerator.
	SignalIntervene = "intervened"
	// SignalOutcome: a turn the machinery interrupted has ended. Reason:
	// what became of it, from InterveneOutcome — the turn's own closing word
	// where the interruption was answered, "steered-again" where a second
	// reading found the same departure, "withdrawn" where the reader took
	// the interruption back.
	//
	// Separate from SignalIntervene because that one counts interruptions
	// and this one counts turns: a turn steered three times is three rows
	// there and one row here, and folding them together would weight a
	// single turn's outcome by how often the machinery spoke during it. One
	// row per interrupted turn is what makes this a rate — the interrupted
	// turns are the denominator, and their closing words are the numerator.
	SignalOutcome = "intervention-outcome"
	// SignalTree: the session told its turn the working tree moved in a way
	// its own edits do not explain. Reason: "head" (the commit or branch
	// moved), "paths" (the changed set did), or "both".
	SignalTree = "tree-moved"
	// SignalProgress: the model supplied the public status a long tool run
	// owed. Reason: "checkpoint". The record deliberately holds no status
	// prose: it is content-free, and provider reasoning is a different row.
	SignalProgress = "progress"
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
	// SignalPlan: a plan card was answered, or the run of an approved plan
	// left it. Reason: "approved", "kept", "rejected" or "off-plan".
	//
	// One code for both because the answers are the denominator the
	// departure is read against: "12 approved, 5 off-plan" is the rate a
	// reader of plan mode is asking for, and it is only a rate while the two
	// are counted the same way. The departure is filed once per run, at the
	// first step the plan never named — a run that wanders for twenty steps
	// has drifted once as far as this rate is concerned, and counting each
	// one would let a single bad run outweigh a hundred good ones.
	SignalPlan = "plan"
	// SignalRounds: a round-cap pause was answered. Reason: "granted" or
	// "uncapped".
	SignalRounds = "rounds"
	// SignalSubagent: a child's attempt ended, or another began in its
	// place. Reason: one of the words below.
	//
	// It is filed on the child's own record rather than on the parent's,
	// which is where the row that carries the attempt's model, spend and
	// budget already is. A parent's copy would say the same thing about the
	// same attempt, and every rate taken over a window would count a
	// fan-out's children twice.
	SignalSubagent = "subagent"
	// SignalRun: the backlog runner moved. Reason: the action taken, or
	// "replan", "stopped", "kept", "lane-refused".
	SignalRun = "run"
	// SignalSkill: the user activated a skill by command. Reason: its name.
	SignalSkill = "skill"
	// SignalTodo: the backlog gained or changed an item, or was read
	// against the tree. Reason: how, from the words and helpers below. It is separate from SignalRun, which is the runner
	// working an item that already exists: a rate over how a backlog grows
	// and a rate over how it is worked are different questions, and folding
	// them together would put every stage of every run in the numerator of
	// the first.
	SignalTodo = "todo"
	// SignalGate: a quality-gate run finished. Reason: its verdict, from
	// GateVerdict. It reaches the record through Observer.Gate rather than
	// Observer.Signal, because it names a subject — the suite — as well as
	// a qualifier.
	SignalGate = "gate"
	// SignalSearch: a web search left the machine. Reason: the backend that
	// answered it, from SearchBackend.
	//
	// The backend and nothing else. What was searched for is the person's
	// question, and it stays in the session's own ledger and transcript;
	// this record is exported and read by people who were not there. The
	// backend is the part a reading of the record needs — a session
	// searching a self-hosted instance and one spending a paid key are
	// different facts about what research costs — and it is drawn from the
	// closed set below so the tool's own configuration can never put a
	// string of the person's here.
	SignalSearch = "web-search"
	// SignalHook: one of the person's own hooks said something at a seam.
	// Reason: the note the hook wrote, which is the person's own text rather
	// than a word from a closed set — so this is the one code here that
	// reaches a surface's event stream and never the record, which is
	// content-free by construction. It exists because the stream is the only
	// surface some sessions have: a run at a terminal says what a hook said
	// on stderr, and a session driven over a socket has a client that cannot
	// see this process's stderr at all.
	SignalHook = "hook"
	// SignalSuggestion: the next step offered in an empty draft left it.
	// Reason: SuggestionTaken (the person took it into the draft) or
	// SuggestionIgnored (a sent message, a later offer or the session's
	// end dropped it). Every offer ends as one of the two, so the pair is
	// the take rate's numerator and denominator in one code — and a rate is
	// the only thing that says whether the offer is worth its request.
	SignalSuggestion = "suggestion"
	// SignalStartOffer: the start screen's reading landed too late to be
	// drawn. Reason: StartOfferDropped — the person had typed, chosen a row
	// or started a turn while it was out, so the rows they were looking at
	// stayed. A reading that failed is not filed: it changed nothing.
	SignalStartOffer = "start-offer"
)

// Reasons for SignalSuggestion: how an offered next step left the draft.
const (
	SuggestionTaken   = "taken"
	SuggestionIgnored = "ignored"
	// StartOfferDropped is SignalStartOffer's reason.
	StartOfferDropped = "dropped"
)

// Search backends for SignalSearch. They are spelled out here rather than
// read from the tools' own constants, which is the same decision the tool
// lists further down make: this classifies rows written by every build that
// ever wrote one, and a name read through today's constant would silently
// reclassify every old session the day a backend is renamed.
const (
	SearchBrave   = "brave"
	SearchSearXNG = "searxng"
	// SearchOther is a backend this build has no word for. The name reaches
	// this package from configuration, so it is replaced rather than
	// stored: a record that is content-free by construction cannot have one
	// path where a setting chooses the string.
	SearchOther = "other"
)

// PlanOffPlan is SignalPlan's fourth word: an approved plan's run announced
// work the plan never named. The three answers beside it are what a person
// said to the card; this is what the run then did, and it is the only one of
// the four a person did not choose.
const PlanOffPlan = "off-plan"

// Ends for SignalSubagent: how a child's attempt stopped, and the one word
// among them that says an attempt began rather than ended.
//
// They are the child's ends and not its lifecycle states. "failed" was the
// state four of these arrived as — a budget spent, a kill, a round limit and
// a provider that stopped answering are one word on a lane and four
// different questions about a fan-out, and the one they are all asked for is
// whether the budget is set right. A state cannot answer it; these can.
const (
	// ChildDone: the child answered and reported.
	ChildDone = "done"
	// ChildBudget: the child spent the token budget it was spawned with.
	ChildBudget = "budget"
	// ChildKilled: a person ended the child from the agent manager.
	ChildKilled = "killed"
	// ChildCancelled: the child's context went with the turn or the session
	// that owned it — nobody ended this child, something above it ended.
	ChildCancelled = "cancelled"
	// ChildCap: the round cap ended the attempt. It is not the ordinary cap,
	// which is a check-in the child carries on from; it is the one reached
	// with no way on.
	ChildCap = "cap"
	// ChildProvider: the provider stopped answering, classified.
	ChildProvider = "provider"
	// ChildFailed: the attempt broke for a reason this build has no word
	// for — a workspace that could not be taken, a loop that returned an
	// error nothing above classified.
	ChildFailed = "failed"
	// ChildHook: one of the person's own subagent_start hooks refused the
	// child before its first request. It is a rule answering, not the child
	// breaking, and a fan-out whose children a hook turns away is a question
	// about the hook rather than about the budget.
	ChildHook = "hook"
	// ChildRetry: an attempt began in place of one that ended. It is filed
	// on the new attempt's own record, because the row that ended is closed
	// by the time anything replaces it, and it is what makes a retry visible
	// to a reader who is not joining rows by their attempt number.
	ChildRetry = "retry"
)

// ChildEnd is what a child's attempt is closed with, beside the outcome
// every session row carries: how it ended, the last reading of its work,
// how many steers it was given and which attempt it was.
//
// They ride the row rather than the event because the question they answer
// is about the attempt as a whole — was 200k enough, did steering help, did
// the second attempt do better than the first — and that question is asked
// of the row that holds the attempt's spend and its model. An event would
// have to be joined back to it to say anything at all.
// See docs/capabilities/sessions-and-memory.md#a-child-ends-for-a-reason.
type ChildTokens struct {
	Inherited, Setup, Tools, Analysis, Handoff int64
	// Fresh is the provider-reported total the budget was measured against.
	// It is kept beside the split rather than summed from it because the
	// split does not have to add up: the phases are estimated, Analysis is
	// whatever is left over, and a leftover cannot go negative — so a child
	// whose estimates overshot its real intake reports phases summing past a
	// total it never reached. Reading the budget off the sum turned that
	// into a child that looks to have overspent a budget it came in under.
	Fresh int64
}

type ChildEnd struct {
	// Reason is one of the words above, empty on a row that is not a
	// child's.
	Reason string
	// Verdict is the last reading of the child's work, from SummaryCode —
	// the same word the reading itself was filed under, never the wording a
	// lane shows a person, so a child's end and the readings that led to it
	// can be read against each other.
	Verdict string
	// Steers is how many times the child was told it had left its task — the
	// check's interruptions over the attempt, SignalIntervene's "steer", and
	// never a person's or the orchestrator's redirect, which is a
	// SignalSteer row of its own naming its source. Counting those here as
	// well would file each of them twice and mix a party's redirects into
	// the figure that says whether the check's steering helps.
	Steers int
	// Attempt is which attempt this row is, from 1. A retry's row and the
	// one it replaces share a parent and differ here, which is the whole of
	// what joins them.
	Attempt int
	// Budget is the effective fresh-token cap. AdmissionFloor is the fixed
	// prompt and task cost plus the working reserve required to start.
	Budget, AdmissionFloor int64
	// Tokens attributes fresh token use by the phase that consumed it.
	Tokens ChildTokens
}

// InterveneSteeredAgain and InterveneWithdrawn are the two words SignalOutcome
// adds to the turn's own closing set. Everything else it files is the word
// the turn ended on, so a rate over interrupted turns and a rate over all
// turns are read in the same vocabulary.
const (
	InterveneSteeredAgain = "steered-again"
	InterveneWithdrawn    = "withdrawn"
)

// Reasons for SignalTodo: how the backlog grew or changed. Drafting from a
// sentence and reading a session into items are told apart because they are
// different acts with different costs — one is a person naming work, the
// other is a session proposing it — and an editor's pass over an item that
// already exists is neither.
const (
	TodoNew  = "new"
	TodoAdd  = "add"
	TodoEdit = "edit"
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
	// EventProgress: public status the model wrote before more tool calls.
	// It is distinct from EventText so JSONL consumers can present it without
	// confusing it with the run's final answer or private reasoning.
	EventProgress = "progress"
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
	// EventAgent: one child of this run, as it stands now. It is written
	// wherever the run has a reader that cannot see the progress rows a
	// terminal draws, and carries the record those rows are drawn from.
	EventAgent = "agent"
	// EventSource: one row of the run's sources ledger — a page fetched, a
	// search made, or a page a server's tool handed back — written as the
	// row is recorded rather than read off the transcript at the end, so a
	// reader of the stream alone can still judge a write-up's citations.
	EventSource = "source"
	// EventClose: the turn ending, carrying the turn outcome above. It is the
	// last line of every stream and the only one that is always written.
	EventClose = "close"
)

// Reserved event kinds for document-shaped events: a recommendation, a ripple,
// a gate and a score, about a document the session has open. Nothing emits
// them yet. They are named here so the vocabulary is decided with the rest of
// the stream's rather than beside it, and they ride session/event like every
// other kind.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
const (
	EventDocumentRecommendation = "document/recommendation"
	EventDocumentRipple         = "document/ripple"
	EventDocumentGate           = "document/gate"
	EventDocumentScore          = "document/score"
)

// EventKinds is every kind the stream may carry, the reserved document kinds
// included. A kind is added here and in the documented list together.
func EventKinds() []string {
	return []string{
		EventText, EventProgress, EventToolCall, EventToolResult, EventDecision,
		EventSignal, EventUsage, EventAgent, EventSource, EventClose,
		EventDocumentRecommendation, EventDocumentRipple, EventDocumentGate, EventDocumentScore,
	}
}

// ProgressCheckpoint is SignalProgress's only reason. It is a closed word
// because the content-free record may say that a checkpoint happened without
// carrying the model's public prose.
const ProgressCheckpoint = "checkpoint"

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
	ClassDeclined     = "declined"
	ClassPlanMode     = "plan-mode"
	ClassReadOnlyMode = "read-only-mode"
	ClassOutOfScope   = "out-of-scope"
	ClassNotFound     = "not-found"
	ClassPermission   = "permission"
	ClassTimeout      = "timeout"
	ClassCancelled    = "cancelled"
	ClassBadArgs      = "bad-args"
	ClassUnknown      = "unknown-tool"
	ClassOther        = "other"
	// ClassExitStatus is a command that ran and exited non-zero.
	ClassExitStatus = "exit-status"
	// ClassEmpty qualifies a successful search that matched nothing.
	ClassEmpty = "empty"
	// The harness classes are a command that never ran, one per prerequisite
	// the machine failed to provide (tools.ExecPrereq). They are their own
	// classes rather than one, because they are acted on differently and by
	// different people: a working directory that went is the session's own
	// doing and often recoverable within it, a missing shell or mechanism is
	// the host's and is the same for every session on it, and a refusal is
	// a boundary rather than a fault. Folded into one code, a run of them
	// would say only that something about the machine was wrong.
	//
	// They are spelled out here, like every other class, so a rename in the
	// vocabulary they are read from cannot silently reclassify rows already
	// written.
	ClassHarnessWorkingDir  = "harness-working-directory"
	ClassHarnessShell       = "harness-execution-shell"
	ClassHarnessContainment = "harness-containment"
	ClassHarnessPermission  = "harness-permission"
	ClassHarnessSpawn       = "harness-spawn"
	// ClassDidNotComplete is a command that ran and whose ending nobody
	// could read (tools.ExecDidNotComplete): it is not a harness class,
	// because something did run, and not an exit status, because there is
	// none. Without a class of its own it would be filed under whatever the
	// keyword ladder made of the error the wait failed with.
	// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
	ClassDidNotComplete = "did-not-complete"
)

// The two halves of one reading: the tools a session finds its way around
// with, and the tools that change something. How many of the first a session
// spends before the first of the second is what says whether finding a place
// to start is what sessions are costing, and it is a question about the
// product that only the record can answer.
// See docs/capabilities/sessions-and-memory.md#how-much-looking-comes-before-the-first-write.
//
// They are spelled out here rather than read from the constants those tools
// are registered under, which is the same decision the dashboard's outcome
// words are: this classifies rows written by every build that ever wrote
// one. A name read through today's constant would silently reclassify every
// old session the day a tool is renamed — the rows keep the spelling they
// were written with, and nothing fails when they stop matching.
//
// A command is on neither list, because one tool name covers a grep and a
// build and the record cannot tell them apart. Neither are git's reading
// half or the structural rewriter — which always previews and never writes,
// so it is neither the looking nor the end of it. Both answer a question
// about a file somebody has already found, which is not the looking this
// measures. The data readers are reads: query and sqlite take the place of a
// read_file of a whole manifest, lockfile or database dump, and a session
// that moved to them from read_file would otherwise read as one that looked
// less.
var (
	searchTools = []string{
		// Reads and listings, the structured-data and database readers
		// among them.
		"read_file", "list_directory", "query", "sqlite",
		// Searches and globs, structural and otherwise.
		"search", "ast_grep", "glob", "fd",
		// The language server's six questions.
		"definition", "references", "workspace_symbol",
		"document_symbol", "hover", "diagnostics",
	}
	writeTools = []string{"write_file", "edit_file", "git_write"}
)

// SearchToolNames is the tools a session finds its way around with: reads,
// listings, searches, globs, and the language server's questions.
func SearchToolNames() []string {
	return append([]string(nil), searchTools...)
}

// WriteToolNames is the tools that change the tree.
func WriteToolNames() []string {
	return append([]string(nil), writeTools...)
}

// Purpose words for a command the model ran: what the shell line did, in one
// word, so the record can say how many commands were reads without holding a
// single one of them. The command text is content and the record is
// content-free by construction; the word is a code from this set and nothing
// else, which is what keeps a tool event exportable with it on
// (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
//
// The set is the question the record is asked, not a taxonomy of programs:
// the built-in tools answer read, search, list, write and edit without a
// card or a classifier round, so a command that did one of those is a
// command a tool could have taken, and build, vcs and other are the ones no
// built-in tool answers.
const (
	// PurposeRead: printed a file or a part of one, or a fact about it —
	// cat, head, tail, wc, sed without -i, jq.
	PurposeRead = "read"
	// PurposeSearch: looked for text inside files — grep, rg.
	PurposeSearch = "search"
	// PurposeList: named files rather than reading them — ls, find, tree.
	PurposeList = "list"
	// PurposeWrite: created, replaced, appended to, moved or removed a file —
	// a redirection to one, tee, cp, mv, rm, mkdir.
	PurposeWrite = "write"
	// PurposeEdit: changed a file where it stood — sed -i, perl -i, patch.
	PurposeEdit = "edit"
	// PurposeBuild: ran the project's toolchain — a build, a test run, a
	// formatter, a package manager.
	PurposeBuild = "build"
	// PurposeVCS: asked version control — git, gh.
	PurposeVCS = "vcs"
	// PurposeOther: anything this reading does not recognise, a script run by
	// its path, and a line that could not be read at all.
	PurposeOther = "other"
)

// The attribute keys. They are the record's own columns under a prefix, and
// they are constants for the same reason the codes are: a key spelled
// two ways is two columns nothing can add up, and the reader doing the
// adding is on the other side of the network where nothing here can fix it.
const (
	// On the span: what the session was.
	AttrKind     = "shhh.session.kind"
	AttrProvider = "shhh.session.provider"
	AttrModel    = "shhh.session.model"
	AttrOutcome  = "shhh.session.outcome"
	// On the span: what it spent, from the last usage report to arrive.
	AttrTurns     = "shhh.session.turns"
	AttrTokensIn  = "shhh.tokens.in"
	AttrTokensOut = "shhh.tokens.out"
	AttrCost      = "shhh.cost.usd"
	// On the span of a session another one started: what kind of session
	// that parent was. The trace already carries the parenthood — a child's
	// span hangs under its parent's — and this is the half a dashboard
	// groups by without walking a trace to find out whose child a row is.
	// The parent's kind and not its identity: an id would be a row number
	// on one machine's database, which means nothing on the other side of
	// the network.
	AttrParent = "shhh.session.parent"
	// On an event: the row's own four columns and its position.
	AttrTool       = "shhh.tool"
	AttrEventCode  = "shhh.outcome"
	AttrReason     = "shhh.reason"
	AttrDurationMs = "shhh.duration_ms"
	AttrTurn       = "shhh.turn"
	AttrRound      = "shhh.round"
)
