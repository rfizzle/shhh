package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// One request, on the configured model, at a shallow thought, forbidding any
// tool, with the instruction and the evidence in two messages — and the
// prose that comes back is the suggestion.
func TestSuggester_AsksOnceAndReadsTheProse(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		if opts.Model != "small" || opts.Effort != provider.EffortLow || opts.ToolChoice != provider.ToolChoiceNone || len(opts.Tools) != 0 {
			t.Errorf("opts = %+v", opts)
		}
		return eventsOf(provider.StreamEvent{Token: "  \"Fix the failing timer test.\"\n", Usage: &provider.Usage{PromptTokens: 300, CompletionTokens: 9}, Done: true}), nil
	}}
	req := SuggestRequest{
		Instruction: "make the retry back off",
		Close:       "done · checks failed: go test ./internal/agent",
		Steps:       "[x] add the backoff\n[ ] fix the timer test",
		Verdict:     "on target",
		Account:     "Fixing the retry backoff.",
		Assistant:   "The backoff is in; one test still fails.",
	}
	v := NewSuggester(p, SuggestConfig{Model: "small"}).Suggest(context.Background(), req)
	if v.Failed || v.Suggestion != "Fix the failing timer test." {
		t.Fatalf("expected a clean suggestion, got %+v", v)
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want one", p.calls)
	}
	if v.Usage.PromptTokens != 300 {
		t.Errorf("usage = %+v", v.Usage)
	}
	if len(p.msgs) != 2 || p.msgs[0].Role != provider.RoleSystem || p.msgs[0].Content != SuggestWording() {
		t.Fatalf("messages = %+v", p.msgs)
	}
	for _, want := range []string{"make the retry back off", "checks failed", "fix the timer test", "on target", "Fixing the retry backoff.", "one test still fails"} {
		if !strings.Contains(p.msgs[1].Content, want) {
			t.Errorf("the evidence is missing %q: %s", want, p.msgs[1].Content)
		}
	}
}

// A configured wording replaces the instruction and nothing else.
func TestSuggester_AWordingReplacesTheInstruction(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(provider.StreamEvent{Token: "Commit it.", Done: true}), nil
	}}
	v := NewSuggester(p, SuggestConfig{Model: "m", Prompt: "say the next step"}).Suggest(context.Background(), SuggestRequest{Instruction: "q"})
	if v.Failed || v.Suggestion != "Commit it." {
		t.Fatalf("got %+v", v)
	}
	if p.msgs[0].Content != "say the next step" {
		t.Fatalf("instruction = %q", p.msgs[0].Content)
	}
}

// An empty answer is the model saying nothing obvious follows: no
// suggestion, not a blank one.
func TestSuggester_AnEmptyAnswerOffersNothing(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(provider.StreamEvent{Token: "  \"\" ", Done: true}), nil
	}}
	if v := NewSuggester(p, SuggestConfig{Model: "m"}).Suggest(context.Background(), SuggestRequest{Instruction: "q"}); !v.Failed || v.Suggestion != "" {
		t.Fatalf("got %+v", v)
	}
}

func TestSuggester_DisabledAsksNothing(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		t.Fatal("a disabled suggester asked")
		return nil, nil
	}}
	for _, s := range []*Suggester{nil, NewSuggester(p, SuggestConfig{}), NewSuggester(p, SuggestConfig{Model: "m", Disabled: true})} {
		if s.Enabled() {
			t.Fatal("reported enabled")
		}
		if v := s.Suggest(context.Background(), SuggestRequest{Instruction: "q"}); !v.Failed {
			t.Fatalf("a disabled reading did not fail: %+v", v)
		}
	}
}

// The model at the call wins over the one at construction, the way every
// bounded reader's does.
func TestSuggester_AsksTheModelAtTheCall(t *testing.T) {
	var got string
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		got = opts.Model
		return eventsOf(provider.StreamEvent{Token: "Go on.", Done: true}), nil
	}}
	s := NewSuggester(p, SuggestConfig{Model: "start", ModelAt: func() string { return "now" }})
	s.Suggest(context.Background(), SuggestRequest{Instruction: "q"})
	if got != "now" {
		t.Fatalf("model = %q, want the one asked at the call", got)
	}
}

func TestCleanSuggestion_BoundsTheAnswer(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := CleanSuggestion(long)
	if n := len([]rune(got)); n > maxSuggestionChars+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("a long answer was not cut at a word: %d %q", n, got)
	}
	if got := CleanSuggestion("Run\n  the   tests."); got != "Run the tests." {
		t.Fatalf("whitespace was not flattened: %q", got)
	}
}
