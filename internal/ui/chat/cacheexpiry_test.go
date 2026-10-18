package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/llm/pricing"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// idleProvider answers each request with the next usage in its script.
type idleProvider struct{ script []provider.Usage }

func (*idleProvider) Name() string { return "gateway" }

func (p *idleProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	u := p.script[0]
	p.script = p.script[1:]
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Usage: &u, Done: true}
	close(ch)
	return ch, nil
}

// idledLedger is a ledger on the model asked, after two requests a pause
// apart: the first wrote the prefix and the second came back with the usage
// given. The model is priced at $3 a million input tokens unless it is
// "unpriced".
func idledLedger(t *testing.T, lease, model string, pause time.Duration, second provider.Usage) *meter.Ledger {
	t.Helper()
	l := meter.New(pricing.NewTable(map[string]pricing.ModelPricing{
		"claude-sonnet-5-5": {InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6},
	}))
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })
	l.SetCacheLease(lease)
	wrote := provider.Usage{PromptTokens: 112_000, CacheCreationTokens: 112_000}
	p := l.For(&idleProvider{script: []provider.Usage{wrote, second}}, meter.SourceAgent)
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "one"}}
	for i := range 2 {
		ev, err := p.StreamCompletion(context.Background(), msgs, provider.CompletionOpts{Model: model})
		if err != nil {
			t.Fatal(err)
		}
		for range ev {
		}
		now = now.Add(pause)
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: string(rune('a' + i))})
	}
	return l
}

// The stats screen says what the lease cost: the writes at the lease asked
// for, and the other lease's figure with the pauses that make the difference.
func TestStats_PrintsTheLeaseLine(t *testing.T) {
	read := provider.Usage{PromptTokens: 112_500, CachedTokens: 112_000, CacheCreationTokens: 500}
	m, _ := spendModel(t)
	m.wiring.Ledger = idledLedger(t, "1h", "claude-sonnet-5-5", 20*time.Minute, read)
	rows := m.spendScreenData().Rows
	if len(rows) == 0 || len(rows[0].Cache) != 2 {
		t.Fatalf("the total row has no lease line: %+v", rows)
	}
	if got := rows[0].Cache[0]; got != "writes $0.68 at 1h" {
		t.Errorf("writes line %q", got)
	}
	if got := rows[0].Cache[1]; got != "at 5m about $0.81 (1 pause over 5 min)" {
		t.Errorf("other line %q", got)
	}
	screen := m.spendScreenData()
	if out := screen.View(120); !strings.Contains(out, "at 5m about $0.81 (1 pause over 5 min)") {
		t.Errorf("the screen does not print it:\n%s", out)
	}
}

// What the person is told when the cache was gone after a pause.
func TestGolden_CacheExpiredNotice(t *testing.T) {
	captureGolden(t, "cache-expired-notice", "the receipt an expired cache leaves", goldenWidths, func(width int) []golden.Panel {
		rewrite := provider.Usage{PromptTokens: 112_000, CacheCreationTokens: 112_000}
		panel := func(model string) string {
			m := frameModel(t, width, 40)
			m.wiring.Ledger = idledLedger(t, "1h", model, 62*time.Minute, rewrite)
			m.noteCacheExpiries()
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "priced, on the hour", View: panel("claude-sonnet-5-5")},
			{Label: "a model with no price", View: panel("claude-fable-9")},
		}
	})
}

// The /context row quotes the bound and its counts, and an expiry inside the
// lease apart from them.
func TestContextCache_LifetimeWords(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	at := now.Add(-12 * time.Minute)
	for _, tc := range []struct {
		name                  string
		r                     meter.CacheReading
		lifetime, expired, id string
	}{
		{"survived and nothing else", meter.CacheReading{SurvivedMax: 48 * time.Minute, SurvivedN: 6, LastStart: at},
			"survived 48m (6), no expiry seen", "", "12m"},
		{"an expiry past the lease", meter.CacheReading{CacheKey: meter.CacheKey{Lease: "5m"}, SurvivedMax: 4 * time.Minute, SurvivedN: 2, ExpiredMin: 72 * time.Minute, ExpiredN: 1, LastStart: at},
			"survived 4m (2), expired by 72m (1)", "", "12m"},
		{"an expiry inside the lease", meter.CacheReading{CacheKey: meter.CacheKey{Lease: "1h"}, ExpiredMin: 12 * time.Minute, ExpiredN: 1},
			"", "expired by 12m, lease 1h", ""},
		{"nothing measured", meter.CacheReading{}, "", "", ""},
	} {
		lifetime, expired, idle := cacheLifetimeWords(tc.r, now)
		if lifetime != tc.lifetime || expired != tc.expired || idle != tc.id {
			t.Errorf("%s: %q %q %q", tc.name, lifetime, expired, idle)
		}
	}
	_ = components.ContextCache{}
}
