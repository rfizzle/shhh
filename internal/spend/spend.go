// Package spend names the sources of spend that belong to shhh alone.
//
// The generic sources (the agent, a summary, a sub-agent, and the
// unattributed catch-all) live in internal/meter, because any program that
// meters a provider has them. The ones here are shhh's own features: the
// permission classifier, the approval card's explanation, the backlog reader,
// and so on. They live apart so the meter holds nothing a second program would
// have to carry. Each is a meter.Source with the string the bill prints, so a
// gate attributes to it exactly as it does to a generic source.
package spend

import "github.com/rfizzle/shhh/internal/meter"

const (
	SourceClassifier meter.Source = "classifier"
	// SourceExplanation is a command explained at the approval card. It is
	// its own source and not the classifier's because the two answer
	// different questions with the same model: one judges a call the reader
	// never saw, and the other is a key the reader pressed. A session where
	// "classifier" costs more than expected is a session to change the mode
	// of; one where "explanation" does is a reader learning a tool, and
	// folding the second into the first would report the wrong advice.
	SourceExplanation meter.Source = "explanation"
	// SourceBacklog is the reading that turns a session into backlog items.
	SourceBacklog meter.Source = "backlog"
	// SourcePersona is the drafting of an agent profile from a brief.
	SourcePersona meter.Source = "persona"
	// SourceToolchain is the drafting or review of a checkout's toolchain
	// declaration, which a person asked for on the start screen or with
	// /toolchain.
	SourceToolchain meter.Source = "toolchain"
	// SourceSuggestion is the next step offered in an empty draft at a
	// turn's close. It is its own source rather than a summary's because it
	// is the one background reading a person can turn off for its cost
	// alone, and they can only judge that cost if the bill names it.
	SourceSuggestion meter.Source = "suggestion"
	// SourceStartOffers is the start screen's reading at session open: the
	// one request a session makes before anyone has asked it anything, so
	// the bill names it apart from the next step offered after a turn.
	SourceStartOffers meter.Source = "start-offers"
	// SourcePatterns is the wording of a proposal made from what repeats
	// across sessions: asked only when the person opens one, and named apart
	// so a reader of the bill can see what reading their own record cost.
	SourcePatterns meter.Source = "patterns"
	SourceOneShot  meter.Source = "one-shot"
)
