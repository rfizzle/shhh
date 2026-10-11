package observe

import (
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe/record"
)

// The record's vocabulary is declared in internal/observe/record, a leaf the
// store and the RPC surface import without taking this package's closure.
// Each name is re-exported here so a surface that builds the record reads
// one package; the doc comment on each is the one on its declaration there.

// Pos, ChildTokens and ChildEnd are the shapes the vocabulary is carried in.
type (
	Pos         = record.Pos
	ChildTokens = record.ChildTokens
	ChildEnd    = record.ChildEnd
)

// Tool outcomes for Observer.ToolCall. They are the digest's words rather
// than a second pair spelled the same way: the reading a session is steered
// by and the record it is measured by must agree on what "it failed" means.
const (
	OutcomeOK    = digest.OutcomeOK
	OutcomeError = digest.OutcomeError
)

const (
	DecisionAllow               = record.DecisionAllow
	DecisionDeny                = record.DecisionDeny
	DecisionAsk                 = record.DecisionAsk
	ReasonModeAcceptEdits       = record.ReasonModeAcceptEdits
	ReasonModeAuto              = record.ReasonModeAuto
	ReasonSessionGrant          = record.ReasonSessionGrant
	ReasonSessionScope          = record.ReasonSessionScope
	ReasonAllowlist             = record.ReasonAllowlist
	ReasonConversationRead      = record.ReasonConversationRead
	ReasonHostKnown             = record.ReasonHostKnown
	ReasonHostYoung             = record.ReasonHostYoung
	ReasonHostDisposable        = record.ReasonHostDisposable
	ReasonHostListed            = record.ReasonHostListed
	ReasonDenylist              = record.ReasonDenylist
	ReasonHook                  = record.ReasonHook
	ReasonPlanMode              = record.ReasonPlanMode
	ReasonPlanInspection        = record.ReasonPlanInspection
	ReasonReadOnlyMode          = record.ReasonReadOnlyMode
	ReasonReadOnlyInspection    = record.ReasonReadOnlyInspection
	ReasonSafety                = record.ReasonSafety
	ReasonScopeSensitive        = record.ReasonScopeSensitive
	ReasonOutOfScope            = record.ReasonOutOfScope
	ReasonPolicy                = record.ReasonPolicy
	ReasonUser                  = record.ReasonUser
	ReasonUserBatch             = record.ReasonUserBatch
	ReasonUserAmended           = record.ReasonUserAmended
	ReasonUserTurn              = record.ReasonUserTurn
	ReasonUserAlways            = record.ReasonUserAlways
	ReasonUserExact             = record.ReasonUserExact
	ReasonUserStop              = record.ReasonUserStop
	ReasonMemory                = record.ReasonMemory
	ReasonClassifier            = record.ReasonClassifier
	ReasonClassifierFailed      = record.ReasonClassifierFailed
	ReasonHeadlessYes           = record.ReasonHeadlessYes
	ReasonHeadlessDefault       = record.ReasonHeadlessDefault
	ReasonOther                 = record.ReasonOther
	TurnDone                    = record.TurnDone
	TurnCancelled               = record.TurnCancelled
	TurnFailed                  = record.TurnFailed
	TurnCapPaused               = record.TurnCapPaused
	TurnRejected                = record.TurnRejected
	SignalRepeat                = record.SignalRepeat
	SignalTrim                  = record.SignalTrim
	SignalCompact               = record.SignalCompact
	SignalSummary               = record.SignalSummary
	SignalSteer                 = record.SignalSteer
	SignalIntervene             = record.SignalIntervene
	SignalOutcome               = record.SignalOutcome
	SignalTree                  = record.SignalTree
	SignalProgress              = record.SignalProgress
	SignalRetry                 = record.SignalRetry
	SignalUndo                  = record.SignalUndo
	SignalMode                  = record.SignalMode
	SignalPlan                  = record.SignalPlan
	SignalRounds                = record.SignalRounds
	SignalSubagent              = record.SignalSubagent
	SignalRun                   = record.SignalRun
	SignalSkill                 = record.SignalSkill
	SignalTodo                  = record.SignalTodo
	SignalGate                  = record.SignalGate
	SignalSearch                = record.SignalSearch
	SignalHook                  = record.SignalHook
	SignalSuggestion            = record.SignalSuggestion
	SignalStartOffer            = record.SignalStartOffer
	SuggestionTaken             = record.SuggestionTaken
	SuggestionIgnored           = record.SuggestionIgnored
	StartOfferDropped           = record.StartOfferDropped
	SearchBrave                 = record.SearchBrave
	SearchSearXNG               = record.SearchSearXNG
	SearchOther                 = record.SearchOther
	PlanOffPlan                 = record.PlanOffPlan
	ChildDone                   = record.ChildDone
	ChildBudget                 = record.ChildBudget
	ChildKilled                 = record.ChildKilled
	ChildCancelled              = record.ChildCancelled
	ChildCap                    = record.ChildCap
	ChildProvider               = record.ChildProvider
	ChildFailed                 = record.ChildFailed
	ChildHook                   = record.ChildHook
	ChildRetry                  = record.ChildRetry
	InterveneSteeredAgain       = record.InterveneSteeredAgain
	InterveneWithdrawn          = record.InterveneWithdrawn
	TodoNew                     = record.TodoNew
	TodoAdd                     = record.TodoAdd
	TodoEdit                    = record.TodoEdit
	EventText                   = record.EventText
	EventProgress               = record.EventProgress
	EventToolCall               = record.EventToolCall
	EventToolResult             = record.EventToolResult
	EventDecision               = record.EventDecision
	EventSignal                 = record.EventSignal
	EventUsage                  = record.EventUsage
	EventAgent                  = record.EventAgent
	EventSource                 = record.EventSource
	EventClose                  = record.EventClose
	EventDocumentRecommendation = record.EventDocumentRecommendation
	EventDocumentRipple         = record.EventDocumentRipple
	EventDocumentGate           = record.EventDocumentGate
	EventDocumentScore          = record.EventDocumentScore
	ProgressCheckpoint          = record.ProgressCheckpoint
	CompactAsked                = record.CompactAsked
	CompactPressure             = record.CompactPressure
	GatePass                    = record.GatePass
	GateFail                    = record.GateFail
	GateBlocked                 = record.GateBlocked
	GateCancelled               = record.GateCancelled
	GateUnknown                 = record.GateUnknown
	GateSuiteUnknown            = record.GateSuiteUnknown
	SessionCompleted            = record.SessionCompleted
	SessionInterrupted          = record.SessionInterrupted
	SessionError                = record.SessionError
	SessionAbandoned            = record.SessionAbandoned
	SessionUnknown              = record.SessionUnknown
	ClassDeclined               = record.ClassDeclined
	ClassPlanMode               = record.ClassPlanMode
	ClassReadOnlyMode           = record.ClassReadOnlyMode
	ClassOutOfScope             = record.ClassOutOfScope
	ClassNotFound               = record.ClassNotFound
	ClassPermission             = record.ClassPermission
	ClassTimeout                = record.ClassTimeout
	ClassCancelled              = record.ClassCancelled
	ClassBadArgs                = record.ClassBadArgs
	ClassUnknown                = record.ClassUnknown
	ClassOther                  = record.ClassOther
	ClassExitStatus             = record.ClassExitStatus
	ClassEmpty                  = record.ClassEmpty
	ClassHarnessWorkingDir      = record.ClassHarnessWorkingDir
	ClassHarnessShell           = record.ClassHarnessShell
	ClassHarnessContainment     = record.ClassHarnessContainment
	ClassHarnessPermission      = record.ClassHarnessPermission
	ClassHarnessSpawn           = record.ClassHarnessSpawn
	ClassDidNotComplete         = record.ClassDidNotComplete
	PurposeRead                 = record.PurposeRead
	PurposeSearch               = record.PurposeSearch
	PurposeList                 = record.PurposeList
	PurposeWrite                = record.PurposeWrite
	PurposeEdit                 = record.PurposeEdit
	PurposeBuild                = record.PurposeBuild
	PurposeVCS                  = record.PurposeVCS
	PurposeOther                = record.PurposeOther
	AttrKind                    = record.AttrKind
	AttrProvider                = record.AttrProvider
	AttrModel                   = record.AttrModel
	AttrOutcome                 = record.AttrOutcome
	AttrTurns                   = record.AttrTurns
	AttrTokensIn                = record.AttrTokensIn
	AttrTokensOut               = record.AttrTokensOut
	AttrCost                    = record.AttrCost
	AttrParent                  = record.AttrParent
	AttrTool                    = record.AttrTool
	AttrEventCode               = record.AttrEventCode
	AttrReason                  = record.AttrReason
	AttrDurationMs              = record.AttrDurationMs
	AttrTurn                    = record.AttrTurn
	AttrRound                   = record.AttrRound
)
