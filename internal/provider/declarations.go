package provider

// Which bounded call a request is. The flow is named on the request by the
// code that builds it, so the provider never guesses which call it is
// serving.
// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.

import "slices"

// Flow names one bounded call outside the main agent and its children. The
// empty Flow is the main agent and every child.
type Flow string

// The closed set of flows: the word the call that builds the request sets.
const (
	FlowClassifier       Flow = "classifier"
	FlowExplanation      Flow = "explanation"
	FlowDescription      Flow = "description"
	FlowReading          Flow = "reading"
	FlowTitle            Flow = "title"
	FlowAccount          Flow = "account"
	FlowSuggestion       Flow = "suggestion"
	FlowStartOffers      Flow = "start_offers"
	FlowPatterns         Flow = "patterns"
	FlowCompaction       Flow = "compaction"
	FlowBacklog          Flow = "backlog"
	FlowProfileDrafter   Flow = "profile_drafter"
	FlowToolchainDrafter Flow = "toolchain_drafter"
)

var flows = []Flow{
	FlowClassifier, FlowExplanation, FlowDescription, FlowReading,
	FlowTitle, FlowAccount, FlowSuggestion, FlowStartOffers, FlowPatterns,
	FlowCompaction, FlowBacklog, FlowProfileDrafter, FlowToolchainDrafter,
}

// Flows is the closed set, in the order a listing reads it.
func Flows() []Flow { return slices.Clone(flows) }
