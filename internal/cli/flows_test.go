package cli

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/rfizzle/llm/capability"
	"github.com/rfizzle/llm/flow"
	"github.com/rfizzle/shhh/internal/sidejobs"

	"github.com/rfizzle/llm"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/permission"
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
	flow  flow.Flow
	where string
	drive func(ctx context.Context, p llm.Provider)
}{
	{flow.Classifier, "permission.Classifier", func(ctx context.Context, p llm.Provider) {
		permission.NewClassifier(p, permission.ClassifierConfig{Model: "m"}).Judge(ctx, permission.ClassifierRequest{Tool: "execute_command", Arguments: `{"command":"ls"}`})
	}},
	{flow.Explanation, "sidejobs.Explainer", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewExplainer(p, sidejobs.ExplainConfig{Model: "m", Prompt: "explain"}).Explain(ctx, sidejobs.ExplainRequest{Command: "ls"})
	}},
	{flow.Description, "generateDescription", func(ctx context.Context, p llm.Provider) {
		generateDescription(ctx, p, "m", "m", "ls")
	}},
	{flow.Reading, "sidejobs.Summarizer", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewSummarizer(p, sidejobs.SummaryConfig{Model: "m"}).Summarize(ctx, sidejobs.SummaryRequest{})
	}},
	{flow.Reading, "sidejobs.HandoffWriter", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewHandoffWriter(p, sidejobs.HandoffConfig{Model: "m"}).Write(ctx, sidejobs.HandoffRequest{First: "fix it"})
	}},
	{flow.Title, "sidejobs.Titler", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewTitler(p, sidejobs.TitleConfig{Model: "m"}).Title(ctx, sidejobs.TitleRequest{User: "hi"})
	}},
	{flow.Account, "sidejobs.Accountant", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewAccountant(p, sidejobs.AccountConfig{Model: "m"}).Account(ctx, sidejobs.AccountRequest{First: "fix it"})
	}},
	{flow.Suggestion, "sidejobs.Suggester", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewSuggester(p, sidejobs.SuggestConfig{Model: "m"}).Suggest(ctx, sidejobs.SuggestRequest{Instruction: "fix it"})
	}},
	{flow.StartOffers, "agent.StartOfferer", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewStartOfferer(p, sidejobs.StartOffersConfig{Model: "m"}).Offer(ctx, sidejobs.StartOffersRequest{Facts: "a checkout"})
	}},
	{flow.Patterns, "agent.PatternWriter", func(ctx context.Context, p llm.Provider) {
		sidejobs.NewPatternWriter(p, sidejobs.PatternsConfig{Model: "m"}).Word(ctx, sidejobs.PatternRequest{Want: sidejobs.WordMemory, Lines: []string{"go test"}})
	}},
	{flow.Compaction, "summaryModelStream", func(ctx context.Context, p llm.Provider) {
		events, cancel, err := summaryModelStream(ctx, &sessionEnv{prov: p}, meter.New(nil), nil, "m")([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, llm.ToolChoiceNone)
		if err == nil {
			for range events {
			}
			cancel()
		}
	}},
	{flow.Backlog, "todo.Extractor", func(ctx context.Context, p llm.Provider) {
		todo.NewExtractor(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Extract(ctx, todo.ExtractRequest{})
	}},
	{flow.Backlog, "todo.Drafter", func(ctx context.Context, p llm.Provider) {
		todo.NewDrafter(p, todo.ExtractConfig{Model: "m"}, todo.BuiltinCode()).Draft(ctx, todo.DraftRequest{Sentence: "fix the parser"})
	}},
	{flow.ProfileDrafter, "persona.Drafter", func(ctx context.Context, p llm.Provider) {
		persona.NewDrafter(p, persona.Config{Model: "m"}).Draft(ctx, persona.Request{Kind: persona.KindCode, Brief: "a reviewer"})
	}},
	{flow.ToolchainDrafter, "toolchainDrafter", func(ctx context.Context, p llm.Provider) {
		_, _ = toolchainDrafter{prov: p, model: func() string { return "m" }}.ask(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	}},
}

// sendsSchema drives every builder and records, per flow, whether any of
// its requests carried a schema, failing on a request that named the wrong
// flow or none.
func sendsSchema(t *testing.T) map[flow.Flow]bool {
	t.Helper()
	out := map[flow.Flow]bool{}
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
	var covered []flow.Flow
	for _, b := range flowBuilders {
		if !slices.Contains(covered, b.flow) {
			covered = append(covered, b.flow)
		}
	}
	if !reflect.DeepEqual(covered, flow.All()) {
		t.Errorf("the builders drive %v, want every flow %v", covered, flow.All())
	}
	var with []flow.Flow
	for _, f := range flow.All() {
		if schema[f] {
			with = append(with, f)
		}
	}
	if want := []flow.Flow{flow.Classifier, flow.Backlog}; !reflect.DeepEqual(with, want) {
		t.Errorf("the flows that send a schema are %v, want %v", with, want)
	}
}

func TestFlows_TheSetAgreesWithTheChain(t *testing.T) {
	var words []flow.Flow
	for _, f := range boundedFlows {
		if slices.Contains(words, f.flow) {
			t.Errorf("flow %q is on the chain twice", f.flow)
		}
		words = append(words, f.flow)
	}
	if !reflect.DeepEqual(words, flow.All()) {
		t.Errorf("the chain's flows are %v, want %v", words, flow.All())
	}
}

// Each declaration may be scoped to exactly the flows whose request carries
// what it narrows: a flow that starts sending a schema without being listed
// fails here, and so does a listed flow that sends none.
func TestDeclarations_OnlyFlowsThatSendASchemaMayScopeIt(t *testing.T) {
	schema := sendsSchema(t)
	d, ok := capability.DeclarationFor("structured_outputs")
	if !ok {
		t.Fatal("structured_outputs is not in the registry")
	}
	for _, f := range flow.All() {
		if d.Scopes(f) != schema[f] {
			t.Errorf("flow %q: scopable %v, sends a schema %v", f, d.Scopes(f), schema[f])
		}
	}
}
