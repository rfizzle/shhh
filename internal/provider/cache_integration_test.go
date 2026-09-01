package provider

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestPromptCacheIntegration checks that the markers cache.go places are
// honoured by a real Messages API endpoint: the first request writes the
// prefix and the second reads it back.
//
// Everything else about caching is asserted offline — that the markers land in
// the right places, stay under the API's limit, and change nothing else about
// the request. None of that can tell you whether the other end honours them,
// and a marker that is silently ignored looks exactly like one that works: the
// answer is identical and only the bill differs.
//
// It costs real requests, so it runs only when pointed at an endpoint:
//
//	SHHH_CACHE_IT_URL=https://api.anthropic.com \
//	SHHH_CACHE_IT_KEY=sk-… \
//	go test ./internal/provider -run CacheIntegration -v
//
// The prefix is deliberately far above the minimum cacheable size. That
// minimum differs by model — it is larger for the small ones — and a prefix
// near it produces a run that reports no caching and does not say whether the
// cause was the size or the endpoint.
func TestPromptCacheIntegration(t *testing.T) {
	base, key := os.Getenv("SHHH_CACHE_IT_URL"), os.Getenv("SHHH_CACHE_IT_KEY")
	if base == "" || key == "" {
		t.Skip("set SHHH_CACHE_IT_URL and SHHH_CACHE_IT_KEY to run")
	}
	model := os.Getenv("SHHH_CACHE_IT_MODEL")
	if model == "" {
		model = defaultAnthropicModel
	}

	p, err := NewAnthropic(ResolveOpts{APIKey: key, BaseURL: base, Model: model})
	if err != nil {
		t.Fatal(err)
	}

	system := "You are terse. Answer in one word.\n" + strings.Repeat(
		"Background: the operator maintains a command-line coding agent with a passive "+
			"loop, a provider registry, a permission classifier and a golden-file suite. ", 800)

	ask := func(msgs []Message) *Usage {
		t.Helper()
		ch, err := p.StreamCompletion(context.Background(), msgs, CompletionOpts{Model: model, MaxTokens: 32})
		if err != nil {
			t.Fatal(err)
		}
		var usage *Usage
		for ev := range ch {
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
		}
		if usage == nil {
			t.Fatal("no usage reported")
		}
		return usage
	}

	first := []Message{
		{Role: RoleSystem, Content: system},
		{Role: RoleUser, Content: "Say one."},
	}
	u1 := ask(first)
	t.Logf("first:  prompt=%d fresh=%d written=%d read=%d",
		u1.PromptTokens, u1.PromptTokens-u1.CachedTokens-u1.CacheCreationTokens,
		u1.CacheCreationTokens, u1.CachedTokens)

	// The same prefix with two more messages on the end, which is the shape
	// every round after the first has.
	second := append(append([]Message{}, first...),
		Message{Role: RoleAssistant, Content: "One."},
		Message{Role: RoleUser, Content: "Say two."},
	)
	u2 := ask(second)
	t.Logf("second: prompt=%d fresh=%d written=%d read=%d",
		u2.PromptTokens, u2.PromptTokens-u2.CachedTokens-u2.CacheCreationTokens,
		u2.CacheCreationTokens, u2.CachedTokens)

	if u1.CacheCreationTokens == 0 {
		t.Fatal("the first request cached nothing: the endpoint is ignoring the markers")
	}
	if u2.CachedTokens == 0 {
		t.Fatal("the second request read nothing back, so every round pays full price")
	}
	if u2.CachedTokens < u1.CacheCreationTokens {
		t.Errorf("the second request should read back what the first wrote: wrote %d, read %d",
			u1.CacheCreationTokens, u2.CachedTokens)
	}
	// The parts are disjoint subsets of the prompt count, which is the
	// invariant every reader of a ledger depends on (provider.go).
	if fresh := u2.PromptTokens - u2.CachedTokens - u2.CacheCreationTokens; fresh < 0 {
		t.Errorf("the cached parts must be inside the prompt count, not beside it: %+v", u2)
	}
}
