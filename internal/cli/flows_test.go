package cli

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/agent/sidejobs"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/todo"
)

// flowRecorder keeps the options of every request it is sent and answers
// each with an empty reply, which every builder reads as a failed reading.
type flowRecorder struct {
	mu   sync.Mutex
	sent []llm.CompletionOpts
}

func (r *flowRecorder) Name() string { return "recorder" }

func (r *flowRecorder) StreamCompletion(_ context.Context, _ []llm.Message, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	r.mu.Lock()
	r.sent = append(r.sent, opts)
	r.mu.Unlock()
	events := make(chan llm.StreamEvent, 1)
	events <- llm.StreamEvent{Done: true}
	close(events)
	return events, nil
}

// flowBuilders drives each bounded flow's own builder once, through p. The
// reading and the backlog are each built in two places, and both are
// driven.
var flowBuilders = []struct {
	flow  llm.Flow
	where string
	drive func(ctx context.Context, p llm.Provider)
}{
	{llm.FlowClassifier, "agent.Classifier", func(ctx context.Context, p llm.Provider) {
		agent.NewClassifier(p, agent.ClassifierConfig{Model: "m"}).Judge(ctx, agent.ClassifierRequest{Tool: "execute_command", Arguments: `{"command":"ls"}`})
	}},
	{llm.FlowExplanation, "sidejobs.Explainer", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewExplainer(p, sidejobs.ExplainConfig{Model: "m", Prompt: "explain"}).Explain(ctx, sidejobs.ExplainRequest{Command: "ls"})
	}},
	{llm.FlowDescription, "generateDescription", func(ctx context.Context, p llm.Provider) {
		generateDescription(ctx, p, "m", "m", "ls")
	}},
	{llm.FlowReading, "sidejobs.Summarizer", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewSummarizer(p, sidejobs.SummaryConfig{Model: "m"}).Summarize(ctx, sidejobs.SummaryRequest{})
	}},
	{llm.FlowReading, "sidejobs.HandoffWriter", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewHandoffWriter(p, sidejobs.HandoffConfig{Model: "m"}).Write(ctx, sidejobs.HandoffRequest{First: "fix it"})
	}},
	{llm.FlowTitle, "sidejobs.Titler", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewTitler(p, sidejobs.TitleConfig{Model: "m"}).Title(ctx, sidejobs.TitleRequest{User: "hi"})
	}},
	{llm.FlowAccount, "sidejobs.Accountant", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewAccountant(p, sidejobs.AccountConfig{Model: "m"}).Account(ctx, sidejobs.AccountRequest{First: "fix it"})
	}},
	{llm.FlowSuggestion, "sidejobs.Suggester", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewSuggester(p, sidejobs.SuggestConfig{Model: "m"}).Suggest(ctx, sidejobs.SuggestRequest{Instruction: "fix it"})
	}},
	{llm.FlowStartOffers, "agent.StartOfferer", func(ctx context.Context, p llm.Provider) {
		agent.NewStartOfferer(p, agent.StartOffersConfig{Model: "m"}).Offer(ctx, agent.StartOffersRequest{Facts: "a checkout"})
	}},
	{llm.FlowPatterns, "agent.PatternWriter", func(ctx context.Context, p llm.Provider) {
		agent.NewPatternWriter(p, agent.PatternsConfig{Model: "m"}).Word(ctx, agent.PatternRequest{Want: agent.WordMemory, Lines: []string{"go test"}})
	}},
	{llm.FlowCompaction, "summaryModelStream", func(ctx context.Context, p llm.Provider) {
		events, cancel, err := summaryModelStream(ctx, &sessionEnv{prov: p}, meter.New(nil), nil, "m")([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, llm.ToolChoiceNone)
		if err == nil {
			for range events {
			}
			cancel()
		}
	}},
	{llm.FlowBacklog, "todo.Extractor", func(ctx context.Context, p llm.Provider) {
		todo.NewExtractor(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Extract(ctx, todo.ExtractRequest{})
	}},
	{llm.FlowBacklog, "todo.Drafter", func(ctx context.Context, p llm.Provider) {
		todo.NewDrafter(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Draft(ctx, todo.DraftRequest{Sentence: "fix the parser"})
	}},
	{llm.FlowProfileDrafter, "persona.Drafter", func(ctx context.Context, p llm.Provider) {
		persona.NewDrafter(p, persona.Config{Model: "m"}).Draft(ctx, persona.Request{Kind: persona.KindCode, Brief: "a reviewer"})
	}},
	{llm.FlowToolchainDrafter, "toolchainDrafter", func(ctx context.Context, p llm.Provider) {
		_, _ = toolchainDrafter{prov: p, model: func() string { return "m" }}.ask(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	}},
}

// sendsSchema drives every builder and records, per flow, whether any of
// its requests carried a schema, failing on a request that named the wrong
// flow or none.
func sendsSchema(t *testing.T) map[llm.Flow]bool {
	t.Helper()
	out := map[llm.Flow]bool{}
	for _, b := range flowBuilders {
		rec := &flowRecorder{}
		b.drive(context.Background(), rec)
		if len(rec.sent) == 0 {
			t.Errorf("%s sent no request", b.where)
			continue
		}
		for _, opts := range rec.sent {
			if opts.Flow != b.flow {
				t.Errorf("%s sent flow %q, want %q", b.where, opts.Flow, b.flow)
			}
			out[b.flow] = out[b.flow] || opts.ResponseSchema != nil
		}
	}
	return out
}

func TestFlows_EachBoundedFlowNamesItselfAndSaysWhetherItSendsASchema(t *testing.T) {
	schema := sendsSchema(t)
	var covered []llm.Flow
	for _, b := range flowBuilders {
		if !slices.Contains(covered, b.flow) {
			covered = append(covered, b.flow)
		}
	}
	if !reflect.DeepEqual(covered, llm.Flows()) {
		t.Errorf("the builders drive %v, want every flow %v", covered, llm.Flows())
	}
	var with []llm.Flow
	for _, f := range llm.Flows() {
		if schema[f] {
			with = append(with, f)
		}
	}
	if want := []llm.Flow{llm.FlowClassifier, llm.FlowBacklog}; !reflect.DeepEqual(with, want) {
		t.Errorf("the flows that send a schema are %v, want %v", with, want)
	}
}

func TestFlows_TheSetAgreesWithTheChain(t *testing.T) {
	var words []llm.Flow
	for _, f := range boundedFlows {
		if slices.Contains(words, f.flow) {
			t.Errorf("flow %q is on the chain twice", f.flow)
		}
		words = append(words, f.flow)
	}
	if !reflect.DeepEqual(words, llm.Flows()) {
		t.Errorf("the chain's flows are %v, want %v", words, llm.Flows())
	}
}

// Each declaration may be scoped to exactly the flows whose request carries
// what it narrows: a flow that starts sending a schema without being listed
// fails here, and so does a listed flow that sends none.
func TestDeclarations_OnlyFlowsThatSendASchemaMayScopeIt(t *testing.T) {
	schema := sendsSchema(t)
	d, ok := llm.DeclarationFor("structured_outputs")
	if !ok {
		t.Fatal("structured_outputs is not in the registry")
	}
	for _, f := range llm.Flows() {
		if d.Scopes(f) != schema[f] {
			t.Errorf("flow %q: scopable %v, sends a schema %v", f, d.Scopes(f), schema[f])
		}
	}
}
