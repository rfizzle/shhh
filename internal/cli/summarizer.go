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
	"maps"
	"slices"
	"sync"
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
	name string
	// flow is the word the call's own request carries, from the provider's
	// closed set: what a profile scopes a declaration to. The name is how a
	// listing reads it.
	flow   provider.Flow
	keys   []string
	source meter.Source
	// sessionless marks a flow no interactive session asks: the one-shot's
	// description is `shhh cmd`'s, and the compaction summary is an
	// unattended run's — a session compacts on its own model. A model taken
	// for a session alone moves nothing such a flow sends, so its row never
	// says it does.
	sessionless bool
	// window marks the compaction summary. A model other than the
	// conversation's is taken there only when its window holds the
	// conversation, and nothing vouches for the provider's small model's, so
	// this flow's chain skips that link and lands on the session's own.
	window bool
}

// The flows, one variable each, so a caller names its flow rather than
// restating its keys.
var (
	flowClassifier  = boundedFlow{name: "classifier", flow: provider.FlowClassifier, keys: []string{"behavior.classifier_model"}, source: meter.SourceClassifier}
	flowExplanation = boundedFlow{name: "explanation", flow: provider.FlowExplanation, keys: []string{"behavior.explainer_model", "behavior.classifier_model"}, source: meter.SourceExplanation}
	flowDescription = boundedFlow{name: "description", flow: provider.FlowDescription, keys: []string{"behavior.description_model"}, source: meter.SourceOneShot, sessionless: true}
	flowReading     = boundedFlow{name: "reading", flow: provider.FlowReading, keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowTitle       = boundedFlow{name: "title", flow: provider.FlowTitle, keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowAccount     = boundedFlow{name: "account", flow: provider.FlowAccount, keys: []string{"summary.model"}, source: meter.SourceSummary}
	flowCompaction  = boundedFlow{name: "compaction", flow: provider.FlowCompaction, keys: []string{"summary.model"}, source: meter.SourceSummary, window: true, sessionless: true}
	flowBacklog     = boundedFlow{name: "backlog", flow: provider.FlowBacklog, keys: []string{"todo.model"}, source: meter.SourceBacklog}
	flowDrafter     = boundedFlow{name: "profile drafter", flow: provider.FlowProfileDrafter, keys: []string{"agents.drafter_model"}, source: meter.SourcePersona}
	// The toolchain draft reads the drafter's key: both turn a request
	// into one file on a card, and a person who moved one drafter onto a
	// stronger model meant the drafting, not the profile.
	flowToolchain = boundedFlow{name: "toolchain drafter", flow: provider.FlowToolchainDrafter, keys: []string{"agents.drafter_model"}, source: meter.SourceToolchain}
	// The next step offered in an empty draft has a key of its own: it is
	// asked at every turn's close, so it is the flow a person is likeliest
	// to want on the smallest model there is.
	flowSuggestion = boundedFlow{name: "suggestion", flow: provider.FlowSuggestion, keys: []string{"behavior.suggestion_model"}, source: meter.SourceSuggestion}
	// The start screen's reading has a key of its own beside it: it is asked
	// once per session open rather than per turn, and reads the instruction
	// block, so it is the one a person may want on a stronger model.
	flowStartOffers = boundedFlow{name: "start offers", flow: provider.FlowStartOffers, keys: []string{"behavior.start_offers_model"}, source: meter.SourceStartOffers}
	// The wording of a proposal made from what repeats has a key of its own
	// too: its words are what a memory keeps for every later session, so it
	// is another a person may want on a stronger model than the cheap one.
	flowPatterns = boundedFlow{name: "patterns", flow: provider.FlowPatterns, keys: []string{"behavior.patterns_model"}, source: meter.SourcePatterns}
)

// boundedFlows is every flow on the chain, in the order a listing reads them.
// It is the one table the doctor and /stats read, so a flow added here is
// reported wherever the question is asked.
var boundedFlows = []boundedFlow{
	flowClassifier, flowExplanation, flowDescription, flowReading,
	flowTitle, flowAccount, flowSuggestion, flowStartOffers, flowPatterns,
	flowCompaction, flowBacklog, flowDrafter, flowToolchain,
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
		if key == flowClassifier.keys[0] && f.name != flowClassifier.name && decisionsClassifier(cfg) {
			// The explanation reads with the classifier's model so the two
			// readings on a card come from one model — but a classifier on
			// the decisions backend may be on a model that answers nothing
			// else, and an explanation is prose. So that link is skipped
			// and the rest of the cheap chain answers.
			continue
		}
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

// decisionsClassifier reports whether the classifier is asked on the
// decisions backend.
func decisionsClassifier(cfg config.Config) bool {
	backend, err := agent.ParseClassifierBackend(cfg.Behavior.ClassifierBackend)
	return err == nil && backend == agent.BackendDecisions
}

// resolveFlows answers for every flow, for a surface that lists them.
func resolveFlows(cfg config.Config, provName, sessionModel string) []flowModel {
	out := make([]flowModel, 0, len(boundedFlows))
	for _, f := range boundedFlows {
		out = append(out, resolveFlow(cfg, f, provName, sessionModel))
	}
	return out
}

// flowOverrides are the values a session took for itself from the config
// screen that its readers ask for at the call: a flow's key and the model it
// answers with, and the readings' cadence, until the process ends, written
// to no file. The readers read them over the config they were built with,
// so one asked after the change is asked on the new value.
// See docs/capabilities/configuration.md#a-session-can-hold-a-value-no-file-does.
//
// The lock is there because the readers are not on the goroutine that
// writes: the classifier judges off the UI goroutine and the readings land
// as commands, while the screen that sets a value is answered on the UI
// goroutine.
type flowOverrides struct {
	mu   sync.Mutex
	keys map[string]string
}

// set takes value for key for the rest of the session. Only a key read at
// the call is taken (heldKey): an override is a value for a reader that asks
// for it, and a key outside them has no reader here to move.
func (o *flowOverrides) set(key, value string) {
	if !heldKey(key) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.keys == nil {
		o.keys = map[string]string{}
	}
	o.keys[key] = value
}

// over is cfg with the session's own values in place of the files'.
func (o *flowOverrides) over(cfg config.Config) config.Config {
	o.mu.Lock()
	keys := maps.Clone(o.keys)
	o.mu.Unlock()
	for key, value := range keys {
		// Every key here is a plain string or number field (heldKey), so a
		// copy of the config takes the value without reaching a map it
		// shares.
		_ = config.Set(&cfg, key, value)
	}
	return cfg
}

// holds reports whether the session took a value for key.
func (o *flowOverrides) holds(key string) bool {
	_, ok := o.value(key)
	return ok
}

// value is what the session took for key, and whether it took one.
func (o *flowOverrides) value(key string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.keys[key]
	return v, ok
}

// put takes value for key whatever kind of key it is: the store behind the
// settings a session reads at a turn boundary, which are handed to the
// session rather than asked for (liveTakers), but must be remembered so the
// screen opened again says what the session holds.
func (o *flowOverrides) put(key, value string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.keys == nil {
		o.keys = map[string]string{}
	}
	o.keys[key] = value
}

// drop lets go of key: the session is on the file's value of it again.
func (o *flowOverrides) drop(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.keys, key)
}

// names is every key held, in order.
func (o *flowOverrides) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Sorted(maps.Keys(o.keys))
}

// flowKey reports whether key names some flow's model.
func flowKey(key string) bool {
	for _, f := range boundedFlows {
		if slices.Contains(f.keys, key) {
			return true
		}
	}
	return false
}

// cadenceKeys are the readings' cadence: how many rounds pass between two,
// and the floor of wall-clock time between them. The summarizer asks for both
// each time it schedules one (cadenceAt), so a value the session took is
// what the next reading is scheduled on.
var cadenceKeys = []string{"summary.interval_rounds", "summary.min_gap_seconds"}

// heldKey reports whether key is one a running session reads at the call,
// so taking it for the session is holding it here: a flow's model, or the
// readings' cadence.
func heldKey(key string) bool {
	return flowKey(key) || slices.Contains(cadenceKeys, key)
}

// cadenceAt is the readings' cadence asked when a reading is scheduled
// rather than at construction, over whatever the session has taken since.
func (env *sessionEnv) cadenceAt(cfg config.Config) func() (int, time.Duration) {
	return func() (int, time.Duration) {
		in := env.flows.over(cfg)
		return in.Summary.IntervalRounds, time.Duration(in.Summary.MinGapSeconds) * time.Second
	}
}

// flowModelAt is a flow's model asked at the call rather than at
// construction: the chain walked over the config the surface was built with
// and whatever the session has taken for itself since.
func (env *sessionEnv) flowModelAt(cfg config.Config, f boundedFlow) func() string {
	return func() string {
		return resolveFlow(env.flows.over(cfg), f, env.provName, env.modelName).model
	}
}

// newSummarizer returns the summarizer for one surface. enabled is that
// surface's switch; a disabled one still returns a summarizer, which reports
// itself disabled rather than being nil — the callers all handle a disabled
// reader and none of them should have to handle a nil one as well.
func newSummarizer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger, enabled bool) *agent.Summarizer {
	return agent.NewSummarizer(ledger.For(env.prov, meter.SourceSummary), agent.SummaryConfig{
		// Model is the answer at start, which is what the settings readout
		// states; the reading itself asks ModelAt.
		Model:                      resolveFlow(cfg, flowReading, env.provName, env.modelName).model,
		ModelAt:                    env.flowModelAt(cfg, flowReading),
		Timeout:                    time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		MaxTokens:                  cfg.Summary.MaxTokens,
		IntervalRounds:             cfg.Summary.IntervalRounds,
		MinGap:                     time.Duration(cfg.Summary.MinGapSeconds) * time.Second,
		CadenceAt:                  env.cadenceAt(cfg),
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
		ModelAt:  env.flowModelAt(cfg, flowAccount),
		Timeout:  time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		Prompt:   env.prompts.account,
		Disabled: cfg.AccountInterval() == 0,
	})
}

// newHandoffWriter returns the writer of the handoff `/handoff` asks for. It
// is asked on the reading's flow, because a handoff is a reading of the
// session the person asked for by name, and billed as a summary beside it
// (docs/capabilities/sessions-and-memory.md#a-session-can-leave-a-handoff).
func newHandoffWriter(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.HandoffWriter {
	return agent.NewHandoffWriter(ledger.For(env.prov, flowReading.source), agent.HandoffConfig{
		ModelAt: env.flowModelAt(cfg, flowReading),
		Timeout: time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
	})
}

// newSuggester returns the writer of the next step an idle session offers in
// its empty draft. Only the interactive session builds one: an unattended
// run, a served session and a child have no draft to offer it in, so none of
// them makes the request
// (docs/capabilities/chat.md#the-next-step-is-offered-not-typed). It is
// asked on its own flow and billed under its own source, so the bill names
// what the offer costs. Whether it is asked at all is the session's switch
// (behavior.suggestions, then /ui suggest), not the writer's: the setting
// starts the session off and the command can still turn it on.
func newSuggester(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.Suggester {
	return agent.NewSuggester(ledger.For(env.prov, flowSuggestion.source), agent.SuggestConfig{
		ModelAt: env.flowModelAt(cfg, flowSuggestion),
		Timeout: time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		Prompt:  env.prompts.suggestion,
	})
}

// newStartOfferer returns the writer of the start screen's read-only offers,
// read once at session open
// (docs/capabilities/chat.md#the-start-screen-is-read-for-this-checkout).
// Only the interactive coding session builds one, since only it has the
// screen. It is asked on its own flow and billed under its own source;
// whether it is asked at all is the next step's switch, which the screen
// reads.
func newStartOfferer(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.StartOfferer {
	return agent.NewStartOfferer(ledger.For(env.prov, flowStartOffers.source), agent.StartOffersConfig{
		ModelAt: env.flowModelAt(cfg, flowStartOffers),
		Timeout: time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
	})
}

// newPatternWriter returns the writer of a proposal's words, asked when the
// person opens a memory or a skill in /patterns
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project).
// It is asked on its own flow and billed under its own source.
func newPatternWriter(cfg config.Config, env *sessionEnv, ledger *meter.Ledger) *agent.PatternWriter {
	return agent.NewPatternWriter(ledger.For(env.prov, flowPatterns.source), agent.PatternsConfig{
		ModelAt: env.flowModelAt(cfg, flowPatterns),
		Timeout: time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
	})
}
