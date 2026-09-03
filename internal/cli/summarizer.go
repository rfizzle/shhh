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

// auxiliaryModel is the model the classifier, the summariser and the title
// answer with when their own key is unset: the provider's small model where
// it names one, and the session's own where it does not.
//
// It used to be the session model outright, which put the frequent, bounded
// judgements on whatever the session had picked for the work itself — and
// the session model is the expensive one about as often as it is the right
// one for reading a digest.
// See docs/capabilities/providers.md#a-bounded-call-runs-on-the-small-model.
func auxiliaryModel(provName, sessionModel string) string {
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

// newSummarizer returns the summarizer for one surface. enabled is that
// surface's switch; a disabled one still returns a summarizer, which reports
// itself disabled rather than being nil — the callers all handle a disabled
// reader and none of them should have to handle a nil one as well.
func newSummarizer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger, enabled bool) *agent.Summarizer {
	model := modelOr(cfg.Summary.Model, auxiliaryModel(env.provName, env.modelName))
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
