package meter

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rfizzle/llm/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

const lifeModel = "claude-sonnet-5-5"

// scriptProvider answers each request with the next usage in its script.
type scriptProvider struct {
	script []provider.Usage
	n      int
}

func (s *scriptProvider) Name() string { return "gateway" }

func (s *scriptProvider) StreamCompletion(_ context.Context, _ []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	u := s.script[s.n]
	s.n++
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Usage: &u, Done: true}
	close(ch)
	return ch, nil
}

// conversation is a growing message list: each call to next returns the
// earlier messages with one more turn appended.
type conversation struct{ msgs []provider.Message }

func (c *conversation) next(text string) []provider.Message {
	c.msgs = append(c.msgs, provider.Message{Role: provider.RoleUser, Content: text})
	return append([]provider.Message(nil), c.msgs...)
}

// clocked is a ledger on a clock the test moves, priced at $3 a million
// input tokens and $15 a million output, on the lease asked.
func clocked(lease string) (*Ledger, *time.Time) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	l := New(pricing.NewTable(map[string]pricing.ModelPricing{
		lifeModel: {InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6},
	}))
	l.now = func() time.Time { return now }
	l.SetCacheLease(lease)
	return l, &now
}

func sendAs(t *testing.T, p provider.Provider, model string, msgs []provider.Message) {
	t.Helper()
	ev, err := p.StreamCompletion(context.Background(), msgs, provider.CompletionOpts{Model: model})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ev {
	}
}

func send(t *testing.T, p provider.Provider, msgs []provider.Message) {
	t.Helper()
	sendAs(t, p, lifeModel, msgs)
}

var (
	wrote = provider.Usage{PromptTokens: 100_000, CacheCreationTokens: 100_000}
	read  = provider.Usage{PromptTokens: 100_500, CachedTokens: 100_000, CacheCreationTokens: 500}
)

// A request that reads what the last one wrote, across a pause, bounds the
// lifetime from below by the pause.
func TestGate_ObservesSurvivalAcrossAnIdleGap(t *testing.T) {
	l, now := clocked("1h")
	p := l.For(&scriptProvider{script: []provider.Usage{wrote, read, read}}, SourceAgent)
	var c conversation
	send(t, p, c.next("a"))
	*now = now.Add(48 * time.Minute)
	send(t, p, c.next("b"))
	// A tool round seconds later is no evidence of a lifetime.
	*now = now.Add(5 * time.Second)
	send(t, p, c.next("c"))

	r := l.CacheReadingFor("gateway", lifeModel)
	if r.SurvivedMax != 48*time.Minute || r.SurvivedN != 1 || r.ExpiredN != 0 {
		t.Errorf("reading %+v", r)
	}
	if got := r.EffectiveLifetime(); got != time.Hour {
		t.Errorf("an hour asked for and no expiry seen is an hour, got %v", got)
	}
}

// A request that wrote the prompt over again after a pause says the cache
// was gone within it, and the session keeps the receipt for it.
func TestGate_ObservesAnExpiry(t *testing.T) {
	l, now := clocked("1h")
	full := provider.Usage{PromptTokens: 112_000, CacheCreationTokens: 112_000}
	p := l.For(&scriptProvider{script: []provider.Usage{full, full}}, SourceAgent)
	var c conversation
	send(t, p, c.next("a"))
	*now = now.Add(62 * time.Minute)
	send(t, p, c.next("b"))

	r := l.CacheReadingFor("gateway", lifeModel)
	if r.ExpiredMin != 62*time.Minute || r.ExpiredN != 1 || r.SurvivedN != 0 {
		t.Fatalf("reading %+v", r)
	}
	ex := l.Expiries()
	if len(ex) != 1 {
		t.Fatalf("expiries %+v", ex)
	}
	// 112k at twice $3 a million.
	if e := ex[0]; e.Rewritten != 112_000 || e.Mult != 2 || !e.Priced || math.Abs(e.Cost-0.672) > 1e-9 {
		t.Errorf("expiry %+v", e)
	}
	// Reading the prefix warm (0.1x), 1k of summary out, and 30% of the
	// prefix written at 2x on the next turn.
	if want := 112_000*0.1*3e-6 + 1000*15e-6 + 112_000*0.3*2*3e-6; math.Abs(ex[0].CompactCost-want) > 1e-9 {
		t.Errorf("an idle compaction priced at %v, want %v", ex[0].CompactCost, want)
	}
}

// Each of the four things that change the bytes inside the prefix reads as
// an expiry if compared, and so yields no observation at all.
func TestGate_NoObservationWhenThePrefixChanged(t *testing.T) {
	long := []provider.Message{
		{Role: provider.RoleSystem, Content: "workspace v1"},
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleAssistant, Content: "two"},
		{Role: provider.RoleUser, Content: "three"},
	}
	for _, tc := range []struct {
		name  string
		next  []provider.Message
		model string
	}{
		{"a trim", []provider.Message{long[0], long[2], long[3], {Role: provider.RoleUser, Content: "four"}}, lifeModel},
		{"a compaction", []provider.Message{long[0], {Role: provider.RoleUser, Content: "summary", Machine: true}, long[3]}, lifeModel},
		{"a workspace rewrite", []provider.Message{{Role: provider.RoleSystem, Content: "workspace v2"}, long[1], long[2], long[3], {Role: provider.RoleUser, Content: "four"}}, lifeModel},
		{"a model switch", append(append([]provider.Message(nil), long...), provider.Message{Role: provider.RoleUser, Content: "four"}), "claude-opus-5-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, now := clocked("1h")
			p := l.For(&scriptProvider{script: []provider.Usage{wrote, {PromptTokens: 50_000, CacheCreationTokens: 50_000}}}, SourceAgent)
			send(t, p, long)
			*now = now.Add(30 * time.Minute)
			sendAs(t, p, tc.model, tc.next)
			for _, r := range l.CacheReadings() {
				if r.SurvivedN+r.ExpiredN != 0 {
					t.Errorf("an observation was taken: %+v", r)
				}
			}
			if len(l.Expiries()) != 0 {
				t.Error("an expiry was recorded")
			}
		})
	}
}

// Another origin's request is not this origin's previous one: a sub-agent's
// prompt is a different prefix from the agent's.
func TestGate_ObservesEachOriginAgainstItsOwnPrevious(t *testing.T) {
	l, now := clocked("1h")
	sp := &scriptProvider{script: []provider.Usage{wrote, {PromptTokens: 1_000, CacheCreationTokens: 1_000}, read}}
	agentP, childP := l.For(sp, SourceAgent), l.ForOrigin(sp, Origin{Source: SourceSubagent, Label: "critic"})
	var c conversation
	send(t, agentP, c.next("a"))
	*now = now.Add(10 * time.Minute)
	send(t, childP, []provider.Message{{Role: provider.RoleUser, Content: "other"}})
	send(t, agentP, c.next("b"))
	if r := l.CacheReadingFor("gateway", lifeModel); r.SurvivedN != 1 || r.SurvivedMax != 10*time.Minute || r.ExpiredN != 0 {
		t.Errorf("reading %+v", r)
	}
}

func TestReading_EffectiveLifetime(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    CacheReading
		want time.Duration
	}{
		{"the lease where nothing contradicts it", CacheReading{CacheKey: CacheKey{Lease: "1h"}}, time.Hour},
		{"the measurement where the lease is contradicted",
			CacheReading{CacheKey: CacheKey{Lease: "1h"}, ExpiredMin: 12 * time.Minute, ExpiredN: 1, SurvivedMax: 8 * time.Minute, SurvivedN: 2}, 8 * time.Minute},
		{"nothing from one survival", CacheReading{SurvivedMax: 8 * time.Minute, SurvivedN: 1}, 0},
		{"nothing where an expiry is inside the survival",
			CacheReading{SurvivedMax: 20 * time.Minute, SurvivedN: 3, ExpiredMin: 10 * time.Minute, ExpiredN: 1}, 0},
		{"the survival with no lease and two gaps", CacheReading{SurvivedMax: 20 * time.Minute, SurvivedN: 2}, 20 * time.Minute},
	} {
		if got := tc.r.EffectiveLifetime(); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// On the hour every write is dearer than five minutes' by 0.75x, and each
// pause past five minutes that the hour carried the prefix across is a
// rewrite five minutes would have paid for: 1.25x less a read.
func TestLedger_CounterfactualOnTheHour(t *testing.T) {
	l, now := clocked("1h")
	p := l.For(&scriptProvider{script: []provider.Usage{wrote, read}}, SourceAgent)
	var c conversation
	send(t, p, c.next("a"))
	*now = now.Add(20 * time.Minute)
	send(t, p, c.next("b"))

	got := l.LeaseCosts()
	if len(got) != 1 || got[0].Lease != "1h" || got[0].OtherLease != "5m" || got[0].Pauses != 1 {
		t.Fatalf("lease costs %+v", got)
	}
	writes := 100_500 * 2 * 3e-6
	other := 100_500*1.25*3e-6 + 100_000*(1.25-0.1)*3e-6
	if math.Abs(got[0].Writes-writes) > 1e-9 || math.Abs(got[0].Other-other) > 1e-9 {
		t.Errorf("writes %v other %v, want %v and %v", got[0].Writes, got[0].Other, writes, other)
	}
}

// On five minutes every write is cheaper than the hour's, and an expiry the
// hour would have read through is a saving: its rewrite at 2x less a read.
func TestLedger_CounterfactualOnFiveMinutes(t *testing.T) {
	l, now := clocked("5m")
	p := l.For(&scriptProvider{script: []provider.Usage{wrote, wrote}}, SourceAgent)
	var c conversation
	send(t, p, c.next("a"))
	*now = now.Add(20 * time.Minute)
	send(t, p, c.next("b"))

	got := l.LeaseCosts()
	if len(got) != 1 || got[0].Lease != "5m" || got[0].OtherLease != "1h" || got[0].Pauses != 1 {
		t.Fatalf("lease costs %+v", got)
	}
	writes := 200_000 * 1.25 * 3e-6
	other := 200_000*2*3e-6 - 100_000*(2-0.1)*3e-6
	if math.Abs(got[0].Writes-writes) > 1e-9 || math.Abs(got[0].Other-other) > 1e-9 {
		t.Errorf("writes %v other %v, want %v and %v", got[0].Writes, got[0].Other, writes, other)
	}
}

// A model with no price has no cost to count the lease against.
func TestLedger_CounterfactualNeedsAPrice(t *testing.T) {
	l := New(nil)
	l.SetCacheLease("1h")
	p := l.For(&scriptProvider{script: []provider.Usage{wrote}}, SourceAgent)
	send(t, p, []provider.Message{{Role: provider.RoleUser, Content: "a"}})
	if got := l.LeaseCosts(); len(got) != 0 {
		t.Errorf("unpriced writes were costed: %+v", got)
	}
}
