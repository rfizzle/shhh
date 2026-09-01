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
)

// newSummarizer returns the summarizer for one surface. enabled is that
// surface's switch; a disabled one still returns a summarizer, which reports
// itself disabled rather than being nil — the callers all handle a disabled
// reader and none of them should have to handle a nil one as well.
func newSummarizer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger, enabled bool) *agent.Summarizer {
	model := cfg.Summary.Model
	if model == "" {
		model = env.modelName
	}
	return agent.NewSummarizer(ledger.For(env.prov, meter.SourceSummary), agent.SummaryConfig{
		Model:          model,
		Timeout:        time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		MaxTokens:      cfg.Summary.MaxTokens,
		IntervalRounds: cfg.Summary.IntervalRounds,
		MinGap:         time.Duration(cfg.Summary.MinGapSeconds) * time.Second,
		Disabled:       !enabled,
	})
}
