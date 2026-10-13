package meter

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// long is a request long enough to have been cached anywhere, reporting no
// cache figure and no cache field.
var long = provider.Usage{PromptTokens: 20_000, CompletionTokens: 10}

// An endpoint with no cache answers nothing on every request, and so does
// the first request on a fresh prefix somewhere that has one; it takes three
// silent requests, each long enough to have been cached, to tell them apart.
func TestLedger_CacheReadingNeedsThreeSilentRequests(t *testing.T) {
	l := New(nil)
	l.ObserveCache("vllm-local", "qwen3", provider.Usage{PromptTokens: 900})
	for i := range 2 {
		l.ObserveCache("vllm-local", "qwen3", long)
		if got := l.CacheReadingFor("vllm-local", "qwen3").State(); got != CacheNoneYet {
			t.Fatalf("after %d silent requests the reading is %q", i+1, got)
		}
	}
	l.ObserveCache("vllm-local", "qwen3", long)
	r := l.CacheReadingFor("vllm-local", "qwen3")
	if r.State() != CacheNotSeen || r.Requests != 3 {
		t.Errorf("reading %+v, state %q; a short request is no evidence", r, r.State())
	}
	if r.Lease != "" || r.Reports != 0 {
		t.Errorf("a model that takes no lease and an endpoint that sent nothing: %+v", r)
	}
}

// One figure is enough: a read or a write means the cache is there.
func TestLedger_CacheReadingWorksOnOneFigure(t *testing.T) {
	at := time.Date(2026, 10, 10, 14, 2, 0, 0, time.UTC)
	for _, u := range []provider.Usage{
		{PromptTokens: 20_000, CacheCreationTokens: 19_000, CacheCreation1h: 19_000,
			Reports: provider.FieldCacheRead | provider.FieldCacheCreation | provider.FieldCacheLease},
		{PromptTokens: 2_000, CachedTokens: 1_900, Reports: provider.FieldCachedTokens},
	} {
		l := New(nil)
		l.now = func() time.Time { return at }
		l.SetCacheLease("1h")
		l.ObserveCache("gateway", "claude-sonnet-5-5", u)
		r := l.CacheReadingFor("gateway", "claude-sonnet-5-5")
		if r.State() != CacheWorks || !r.LastAt.Equal(at) || r.Reports != u.Reports {
			t.Errorf("one figure: %+v, state %q", r, r.State())
		}
		if r.Lease != "1h" {
			t.Errorf("a Messages API model keeps its reading under the lease asked for, got %q", r.Lease)
		}
	}

	l := New(nil)
	l.SetCacheLease("1h")
	l.ObserveCache("gateway", "claude-sonnet-5-5", provider.Usage{PromptTokens: 20_000, CachedTokens: 19_000, CacheCreationTokens: 500, CacheCreation1h: 500})
	r := l.CacheReadingFor("gateway", "claude-sonnet-5-5")
	if r.LastShare() != 95 || r.Confirmed != "1h" || r.Downgraded() {
		t.Errorf("an hour honoured: share %d, %+v", r.LastShare(), r)
	}
	l.ObserveCache("gateway", "claude-sonnet-5-5", provider.Usage{PromptTokens: 20_000, CacheCreationTokens: 20_000, CacheCreation5m: 20_000})
	if r := l.CacheReadingFor("gateway", "claude-sonnet-5-5"); r.Confirmed != "5m" || !r.Downgraded() || r.Requests != 2 || r.LastShare() != 0 {
		t.Errorf("an hour written as five minutes: %+v", r)
	}
	// A write that says nothing about its lease leaves the last word as it
	// was.
	l.ObserveCache("gateway", "claude-sonnet-5-5", provider.Usage{PromptTokens: 20_000, CachedTokens: 20_000})
	if r := l.CacheReadingFor("gateway", "claude-sonnet-5-5"); r.Confirmed != "5m" {
		t.Errorf("a read alone revised the lease: %+v", r)
	}
}

// A profile's prompt_cache = false outranks every figure: the reading says
// what the person declared, and the figures are still billed.
func TestLedger_ADeclaredOffOutranksAFigure(t *testing.T) {
	provider.RegisterDeclarations("stub", map[string]provider.Declared{
		"claude-sonnet-5-5": {Model: map[string]any{provider.KeyPromptCache: false}},
	})
	t.Cleanup(func() { provider.RegisterDeclarations("stub", nil) })

	l := New(testPrices())
	if r := l.CacheReadingFor("stub", "claude-sonnet-5-5"); r.State() != CacheOff {
		t.Errorf("before any request the declaration is the reading, got %q", r.State())
	}
	p := l.For(&stubProvider{usage: provider.Usage{PromptTokens: 20_000, CachedTokens: 19_000}}, SourceAgent)
	drain(t, p, "claude-sonnet-5-5")
	r := l.CacheReadingFor("stub", "claude-sonnet-5-5")
	if r.State() != CacheOff || len(r.OffBy) != 1 || r.OffBy[0] != "stub" || r.WithReads != 1 {
		t.Errorf("reading %+v, state %q", r, r.State())
	}
	// The declaration is the profile's own endpoint's: the same model
	// reached another way keeps the reading its responses give.
	l.ObserveCache("anthropic", "claude-sonnet-5-5", provider.Usage{PromptTokens: 20_000, CachedTokens: 19_000})
	if r := l.CacheReadingFor("anthropic", "claude-sonnet-5-5"); r.State() != CacheWorks {
		t.Errorf("another endpoint read as off: %+v", r)
	}
	if l.Total().Cached != 19_000 {
		t.Errorf("the figures are still billed: %+v", l.Total())
	}
}

// The gate reads the cache of the endpoint that answered, the way it bills
// the model that answered; a reading outlives /clear, which starts the
// accounting over and not what was learned about the endpoint.
func TestGate_ReadsTheCacheOfTheEndpointThatAnswered(t *testing.T) {
	l := New(nil)
	drain(t, l.For(&stubProvider{usage: provider.Usage{PromptTokens: 20_000, CachedTokens: 5_000}}, SourceClassifier), "gpt-5")
	l.Reset()
	got := l.CacheReadings()
	if len(got) != 1 || got[0].Provider != "stub" || got[0].Model != "gpt-5" || got[0].State() != CacheWorks {
		t.Errorf("readings %+v", got)
	}
	// A surface that knows the endpoint by another name still finds the
	// reading for the model it is on.
	if r := l.CacheReadingFor("openai-compatible", "gpt-5"); r.Provider != "stub" || r.State() != CacheWorks {
		t.Errorf("by another name: %+v", r)
	}
}
