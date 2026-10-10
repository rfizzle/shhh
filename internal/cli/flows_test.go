package cli

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/todo"
)

// flowRecorder keeps the options of every request it is sent and answers
// each with an empty reply, which every builder reads as a failed reading.
type flowRecorder struct {
	mu   sync.Mutex
	sent []provider.CompletionOpts
}

func (r *flowRecorder) Name() string { return "recorder" }

func (r *flowRecorder) StreamCompletion(_ context.Context, _ []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	r.mu.Lock()
	r.sent = append(r.sent, opts)
	r.mu.Unlock()
	events := make(chan provider.StreamEvent, 1)
	events <- provider.StreamEvent{Done: true}
	close(events)
	return events, nil
}

// flowBuilders drives each bounded flow's own builder once, through p. The
// reading and the backlog are each built in two places, and both are
// driven.
var flowBuilders = []struct {
	flow  provider.Flow
	where string
	drive func(ctx context.Context, p provider.Provider)
}{
	{provider.FlowClassifier, "agent.Classifier", func(ctx context.Context, p provider.Provider) {
		agent.NewClassifier(p, agent.ClassifierConfig{Model: "m"}).Judge(ctx, agent.ClassifierRequest{Tool: "execute_command", Arguments: `{"command":"ls"}`})
	}},
	{provider.FlowExplanation, "agent.Explainer", func(ctx context.Context, p provider.Provider) {
		agent.NewExplainer(p, agent.ExplainConfig{Model: "m", Prompt: "explain"}).Explain(ctx, agent.ExplainRequest{Command: "ls"})
	}},
	{provider.FlowDescription, "generateDescription", func(ctx context.Context, p provider.Provider) {
		generateDescription(ctx, p, "m", "m", "ls")
	}},
	{provider.FlowReading, "agent.Summarizer", func(ctx context.Context, p provider.Provider) {
		agent.NewSummarizer(p, agent.SummaryConfig{Model: "m"}).Summarize(ctx, agent.SummaryRequest{})
	}},
	{provider.FlowReading, "agent.HandoffWriter", func(ctx context.Context, p provider.Provider) {
		agent.NewHandoffWriter(p, agent.HandoffConfig{Model: "m"}).Write(ctx, agent.HandoffRequest{First: "fix it"})
	}},
	{provider.FlowTitle, "agent.Titler", func(ctx context.Context, p provider.Provider) {
		agent.NewTitler(p, agent.TitleConfig{Model: "m"}).Title(ctx, agent.TitleRequest{User: "hi"})
	}},
	{provider.FlowAccount, "agent.Accountant", func(ctx context.Context, p provider.Provider) {
		agent.NewAccountant(p, agent.AccountConfig{Model: "m"}).Account(ctx, agent.AccountRequest{First: "fix it"})
	}},
	{provider.FlowSuggestion, "agent.Suggester", func(ctx context.Context, p provider.Provider) {
		agent.NewSuggester(p, agent.SuggestConfig{Model: "m"}).Suggest(ctx, agent.SuggestRequest{Instruction: "fix it"})
	}},
	{provider.FlowStartOffers, "agent.StartOfferer", func(ctx context.Context, p provider.Provider) {
		agent.NewStartOfferer(p, agent.StartOffersConfig{Model: "m"}).Offer(ctx, agent.StartOffersRequest{Facts: "a checkout"})
	}},
	{provider.FlowPatterns, "agent.PatternWriter", func(ctx context.Context, p provider.Provider) {
		agent.NewPatternWriter(p, agent.PatternsConfig{Model: "m"}).Word(ctx, agent.PatternRequest{Want: agent.WordMemory, Lines: []string{"go test"}})
	}},
	{provider.FlowCompaction, "summaryModelStream", func(ctx context.Context, p provider.Provider) {
		events, cancel, err := summaryModelStream(ctx, &sessionEnv{prov: p}, meter.New(nil), nil, "m")([]provider.Message{{Role: provider.RoleUser, Content: "hi"}}, provider.ToolChoiceNone)
		if err == nil {
			for range events {
			}
			cancel()
		}
	}},
	{provider.FlowBacklog, "todo.Extractor", func(ctx context.Context, p provider.Provider) {
		todo.NewExtractor(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Extract(ctx, todo.ExtractRequest{})
	}},
	{provider.FlowBacklog, "todo.Drafter", func(ctx context.Context, p provider.Provider) {
		todo.NewDrafter(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Draft(ctx, todo.DraftRequest{Sentence: "fix the parser"})
	}},
	{provider.FlowProfileDrafter, "persona.Drafter", func(ctx context.Context, p provider.Provider) {
		persona.NewDrafter(p, persona.Config{Model: "m"}).Draft(ctx, persona.Request{Kind: persona.KindCode, Brief: "a reviewer"})
	}},
	{provider.FlowToolchainDrafter, "toolchainDrafter", func(ctx context.Context, p provider.Provider) {
		_, _ = toolchainDrafter{prov: p, model: func() string { return "m" }}.ask(ctx, []provider.Message{{Role: provider.RoleUser, Content: "hi"}})
	}},
}

// sendsSchema drives every builder and records, per flow, whether any of
// its requests carried a schema, failing on a request that named the wrong
// flow or none.
func sendsSchema(t *testing.T) map[provider.Flow]bool {
	t.Helper()
	out := map[provider.Flow]bool{}
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
	var covered []provider.Flow
	for _, b := range flowBuilders {
		if !slices.Contains(covered, b.flow) {
			covered = append(covered, b.flow)
		}
	}
	if !reflect.DeepEqual(covered, provider.Flows()) {
		t.Errorf("the builders drive %v, want every flow %v", covered, provider.Flows())
	}
	var with []provider.Flow
	for _, f := range provider.Flows() {
		if schema[f] {
			with = append(with, f)
		}
	}
	if want := []provider.Flow{provider.FlowClassifier, provider.FlowBacklog}; !reflect.DeepEqual(with, want) {
		t.Errorf("the flows that send a schema are %v, want %v", with, want)
	}
}

func TestFlows_TheSetAgreesWithTheChain(t *testing.T) {
	var words []provider.Flow
	for _, f := range boundedFlows {
		if slices.Contains(words, f.flow) {
			t.Errorf("flow %q is on the chain twice", f.flow)
		}
		words = append(words, f.flow)
	}
	if !reflect.DeepEqual(words, provider.Flows()) {
		t.Errorf("the chain's flows are %v, want %v", words, provider.Flows())
	}
}
