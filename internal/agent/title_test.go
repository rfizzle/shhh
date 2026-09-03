package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func titleCall(args string) provider.StreamEvent {
	return provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{ID: "t1", Name: TitleToolName, Arguments: args}},
		Usage:     &provider.Usage{PromptTokens: 300, CompletionTokens: 8},
		Done:      true,
	}
}

func TestTitler_ToolCallReading(t *testing.T) {
	var sawUser bool
	p := &fakeClassifierProvider{fn: func(_ int, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		sawUser = opts.Model == "small"
		return eventsOf(titleCall(`{"title":"Flaky retry test"}`)), nil
	}}
	v := NewTitler(p, TitleConfig{Model: "small"}).Title(context.Background(), TitleRequest{User: "why does the retry test flake", Assistant: "it races the timer"})
	if v.Failed || v.Title != "Flaky retry test" {
		t.Fatalf("expected a clean reading, got %+v", v)
	}
	if !sawUser {
		t.Fatal("the reading should go to the configured model")
	}
	if v.Usage.PromptTokens != 300 || v.Model != "small" {
		t.Fatalf("usage and model should be captured, got %+v", v)
	}
	// The instruction is the system message and the exchange the user turn,
	// so a conversation that tries to name itself is not sitting in the
	// same message as the instruction that names it.
	if len(p.msgs) != 2 || p.msgs[0].Role != provider.RoleSystem || p.msgs[1].Role != provider.RoleUser {
		t.Fatalf("messages = %+v", p.msgs)
	}
	if !strings.Contains(p.msgs[0].Content, "You name conversations") || strings.Contains(p.msgs[0].Content, "UNTRUSTED EXCHANGE") {
		t.Errorf("the instruction should be the system message alone, got %q", p.msgs[0].Content)
	}
	if !strings.Contains(p.msgs[1].Content, "why does the retry test flake") || strings.Contains(p.msgs[1].Content, "You name conversations") {
		t.Errorf("the exchange should be the user turn alone, got %q", p.msgs[1].Content)
	}
}

// Sixty-four tokens does not reach the end of a thought, which is why titles
// stopped appearing: the reading came back empty and the slot stayed
// untitled with nothing anywhere saying why.
func TestTitler_CeilingLeavesRoomForTheThought(t *testing.T) {
	answer := titleCall(`{"title":"Flaky retry test"}`)

	cramped := &thinkingProvider{spend: lowBudget(), answer: answer}
	v := NewTitler(cramped, TitleConfig{Model: "small", MaxTokens: 64}).Title(context.Background(), TitleRequest{User: "why", Assistant: "because"})
	if !v.Failed {
		t.Fatalf("a ceiling under the thought should name nothing, got %+v", v)
	}

	roomy := &thinkingProvider{spend: lowBudget(), answer: answer}
	v = NewTitler(roomy, TitleConfig{Model: "small"}).Title(context.Background(), TitleRequest{User: "why", Assistant: "because"})
	if v.Failed || v.Title != "Flaky retry test" {
		t.Fatalf("the default ceiling should produce a title, got %+v", v)
	}
	if roomy.seen.Effort != provider.EffortLow {
		t.Errorf("effort = %v", roomy.seen.Effort)
	}
	if DefaultTitleMaxTokens <= lowBudget() {
		t.Fatalf("the default ceiling %d does not clear a low thought", DefaultTitleMaxTokens)
	}
}

func TestTitler_ProseFallbackAndBounds(t *testing.T) {
	p := &fakeClassifierProvider{fn: func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
		return eventsOf(provider.StreamEvent{Token: "\"One two three four five six seven eight.\"\nmore", Done: true}), nil
	}}
	v := NewTitler(p, TitleConfig{Model: "m"}).Title(context.Background(), TitleRequest{User: "q"})
	if v.Failed {
		t.Fatalf("prose should still read, got %+v", v)
	}
	if v.Title != "One two three four five six" {
		t.Fatalf("the title should be bounded to six words with the quotes and period gone, got %q", v.Title)
	}
}

func TestTitler_FailsSoft(t *testing.T) {
	cases := map[string]func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error){
		"provider error": func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return nil, errors.New("down")
		},
		"empty answer": func(int, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
			return eventsOf(titleCall(`{"title":"   "}`)), nil
		},
	}
	for name, fn := range cases {
		v := NewTitler(&fakeClassifierProvider{fn: fn}, TitleConfig{Model: "m"}).Title(context.Background(), TitleRequest{User: "q"})
		if !v.Failed || v.Title != "" || v.Err == "" {
			t.Errorf("%s: expected a failed reading with a reason, got %+v", name, v)
		}
	}
	if v := NewTitler(nil, TitleConfig{Model: "m"}).Title(context.Background(), TitleRequest{}); !v.Failed {
		t.Fatal("no provider is a failed reading, not a panic")
	}
	if NewTitler(&fakeClassifierProvider{}, TitleConfig{}).Enabled() {
		t.Fatal("no model means disabled")
	}
}

func TestCleanTitle(t *testing.T) {
	cases := map[string]string{
		"  Fix   the\nbuild.  ": "Fix the build",
		`"Quoted title"`:        "Quoted title",
		"a b c d e f g h":       "a b c d e f",
		strings.Repeat("x", 80): strings.Repeat("x", 60),
		"":                      "",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
