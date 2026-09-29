package cli

// Building the summarizer, once, for every surface that takes readings.
//
// The reading used to belong to the chat session alone, where it filled a
// rail block. It interrupts a turn now — a steer for a run that has left its
// instruction, an early check-in for one that has what it needs — and the
// surfaces that most need interrupting are the ones with nobody in front of
// them: a headless run, and every sub-agent.
//
// Which surfaces take readings is the reader's to decide, because the cost is
// per agent and a wide fan-out multiplies it
// (docs/capabilities/coding-agent.md#a-reading-for-a-run-nobody-is-watching).
// What must not vary between them is how a reading is asked for, so the
// bounds are assembled in one place and only the switch is passed in.

import (
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
)

// auxiliaryModel is the model a bounded call answers with when its own key is
// unset: provider.cheap_model where a person named one, the provider's small
// model where it names one, and the session's own where neither does.
//
// It used to be the session model outright, which put the frequent, bounded
// judgements on whatever the session had picked for the work itself — and
// the session model is the expensive one about as often as it is the right
// one for reading a digest. The cheap key sits in front of the provider's
// choice so a person who wants the machinery on one model names it once
// rather than once per flow.
// See docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model.
func auxiliaryModel(cfg config.Config, provName, sessionModel string) string {
	return modelOr(cfg.Provider.CheapModel, smallModel(provName, sessionModel))
}

// smallModel is the chain's last two links: the provider's compiled-in small
// model where it names one, and the session's own where it does not — a
// local endpoint serves whatever weights were pulled, and guessing a name
// there is a request that 404s.
func smallModel(provName, sessionModel string) string {
	if cheap := provider.Defaults(provName).CheapModel; cheap != "" {
		return cheap
	}
	return sessionModel
}

// modelOr puts the configured name ahead of that rule. A person who names a
// model in behavior.classifier_model or summary.model has decided the
// question, and the provider's small one does not get to reopen it.
func modelOr(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

// flowStep is which link of the chain answered for a flow. The words are what
// the doctor row states, so a reader asking why a model is on the bill is
// told which setting put it there.
type flowStep int

const (
	stepFlowKey flowStep = iota
	stepCheapKey
	stepSmallModel
	stepSessionModel
)

func (s flowStep) String() string {
	switch s {
	case stepFlowKey:
		return "flow key"
	case stepCheapKey:
		return "cheap key"
	case stepSmallModel:
		return "provider small model"
	default:
		return "session model"
	}
}

// boundedFlow is one bounded call outside the main agent and its children.
// keys are the settings that name its model, most specific first: the
// explanation reads the classifier's key behind its own, because the two are
// read one under the other at the approval card (gateModel says why). source
// is what the ledger bills it under, which is how /stats names it under the
// model it ran on.
type boundedFlow struct {
	name   string
	keys   []string
	source meter.Source
	// window marks the compaction summary. A model other than the
	// conversation's is taken there only when its window holds the
	// conversation, and nothing vouches for the provider's small model's, so
	// this flow's chain skips that link and lands on the session's own.
	window bool
}

// The flows, one variable each, so a caller names its flow rather than
// restating its keys.
var (
	flowClassifier  = boundedFlow{name: "classifier", keys: []string{"behavior.classifier_model"}, source: meter.SourceClassifier}
	flowExplanation = boundedFlow{name: "explanation", keys: []string{"behavior.explainer_model", "behavior.classifier_model"}, source: meter.SourceExplanation}
	flowDescription = boundedFlow{name: "description", keys: []string{"behavior.description_model"}, source: meter.SourceOneShot}
	flowReading     = boundedFlow{name: "reading", keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowTitle       = boundedFlow{name: "title", keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowAccount     = boundedFlow{name: "account", keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowCompaction  = boundedFlow{name: "compaction", keys: []string{"summary.model"}, source: meter.SourceSummary, window: true}
	flowBacklog     = boundedFlow{name: "backlog", keys: []string{"todo.model"}, source: meter.SourceBacklog}
	flowDrafter     = boundedFlow{name: "profile drafter", keys: []string{"agents.drafter_model"}, source: meter.SourcePersona}
)

// boundedFlows is every flow on the chain, in the order a listing reads them.
// It is the one table the doctor and /stats read, so a flow added here is
// reported wherever the question is asked.
var boundedFlows = []boundedFlow{
	flowClassifier, flowExplanation, flowDescription, flowReading,
	flowTitle, flowAccount, flowCompaction, flowBacklog, flowDrafter,
}

// flowModel is one flow's answer: the model, the link that gave it, and the
// key that link read — empty for the last two, which read no setting.
type flowModel struct {
	flow  boundedFlow
	model string
	step  flowStep
	key   string
}

// resolveFlow walks the chain for one flow: its own keys, then
// provider.cheap_model, the provider's small model and the session's own.
// Every bounded call's model is this function's answer, so what the doctor
// reports is what the call is sent with.
func resolveFlow(cfg config.Config, f boundedFlow, provName, sessionModel string) flowModel {
	for _, key := range f.keys {
		if name, _ := config.Value(cfg, key); name != "" {
			return flowModel{flow: f, model: name, step: stepFlowKey, key: key}
		}
	}
	if name := cfg.Provider.CheapModel; name != "" {
		return flowModel{flow: f, model: name, step: stepCheapKey, key: "provider.cheap_model"}
	}
	if !f.window {
		if cheap := provider.Defaults(provName).CheapModel; cheap != "" {
			return flowModel{flow: f, model: cheap, step: stepSmallModel}
		}
	}
	return flowModel{flow: f, model: sessionModel, step: stepSessionModel}
}

// resolveFlows answers for every flow, for a surface that lists them.
func resolveFlows(cfg config.Config, provName, sessionModel string) []flowModel {
	out := make([]flowModel, 0, len(boundedFlows))
	for _, f := range boundedFlows {
		out = append(out, resolveFlow(cfg, f, provName, sessionModel))
	}
	return out
}

// newSummarizer returns the summarizer for one surface. enabled is that
// surface's switch; a disabled one still returns a summarizer, which reports
// itself disabled rather than being nil — the callers all handle a disabled
// reader and none of them should have to handle a nil one as well.
func newSummarizer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger, enabled bool) *agent.Summarizer {
	model := resolveFlow(cfg, flowReading, env.provName, env.modelName).model
	return agent.NewSummarizer(ledger.For(env.prov, meter.SourceSummary), agent.SummaryConfig{
		Model:                      model,
		Timeout:                    time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		MaxTokens:                  cfg.Summary.MaxTokens,
		IntervalRounds:             cfg.Summary.IntervalRounds,
		MinGap:                     time.Duration(cfg.Summary.MinGapSeconds) * time.Second,
		InterveneCooldownIntervals: cfg.Summary.InterveneCooldownIntervals,
		Prompt:                     env.prompts.summary,
		Disabled:                   !enabled,
	})
}

// newAccountant returns the writer of the session's standing account for one
// surface: the two sentences every saved-chat listing shows and a reopened
// conversation is told. It is asked on the account's own flow and billed as
// a summary, so the rail's spend and /stats see it beside the title
// (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write). A
// session whose settings turned it off still gets one, reporting itself
// disabled, for newSummarizer's reason.
func newAccountant(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Accountant {
	return agent.NewAccountant(ledger.For(env.prov, flowAccount.source), agent.AccountConfig{
		Model:    resolveFlow(cfg, flowAccount, env.provName, env.modelName).model,
		Timeout:  time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		Prompt:   env.prompts.account,
		Disabled: cfg.AccountInterval() == 0,
	})
}
