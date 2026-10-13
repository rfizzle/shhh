package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

// keepAliveSession is idleSession on the hour's lease against Anthropic's own
// endpoint with the fold off and three refreshes allowed. send is what the
// sender saw, one request per refresh.
func keepAliveSession(t *testing.T, usage provider.Usage, sent *[][]provider.Message) Model {
	t.Helper()
	requests := 0
	m := idleSession(t, &requests)
	m.providerName = "anthropic"
	m.wiring.IdleCompact.On = false
	m.wiring.KeepAliveMax = 3
	m.wiring.Prices = pricing.NewTable(map[string]pricing.ModelPricing{
		"claude-sonnet-5-5": {InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6},
	})
	m.wiring.KeepAliveSend = func(_ context.Context, msgs []provider.Message) (provider.Usage, error) {
		*sent = append(*sent, msgs)
		return usage, nil
	}
	return m
}

// fireKeepAlive runs the tick and the refresh it starts, and hands the answer
// to the model.
func fireKeepAlive(t *testing.T, m Model) (Model, tea.Cmd, bool) {
	t.Helper()
	next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	if cmd == nil {
		return next.(Model), nil, false
	}
	msg, ok := cmd().(keepAliveMsg)
	if !ok {
		t.Fatalf("the tick started something other than a refresh")
	}
	next, tick := next.(Model).Update(msg)
	return next.(Model), tick, true
}

// The refresh is bounded by the count, ends at the first cache found gone,
// and is Anthropic's own endpoint on the hour's lease only.
func TestKeepAlive_IsBoundedAndAnthropicOnly(t *testing.T) {
	var sent [][]provider.Message
	m := keepAliveSession(t, idleReadUsage, &sent)

	for i := 1; i <= 3; i++ {
		var ok bool
		var tick tea.Cmd
		m, tick, ok = fireKeepAlive(t, m)
		if !ok {
			t.Fatalf("refresh %d of 3 was declined", i)
		}
		if tick == nil {
			t.Errorf("refresh %d should schedule the next", i)
		}
	}
	if _, _, ok := fireKeepAlive(t, m); ok || len(sent) != 3 {
		t.Errorf("a fourth refresh went out past a bound of 3 (%d sent)", len(sent))
	}
	last := m.transcript[len(m.transcript)-1].text
	if want := "kept the cache warm · 112.0k read · $"; !strings.HasPrefix(last, want) {
		t.Errorf("receipt %q, want it to start %q", last, want)
	}

	// A refresh that read nothing found the cache gone: the run ends, and
	// says what it paid.
	sent = nil
	cold := keepAliveSession(t, provider.Usage{PromptTokens: 112_000, CacheCreationTokens: 112_000}, &sent)
	cold, tick, ok := fireKeepAlive(t, cold)
	if !ok || tick != nil || !cold.idle.cold {
		t.Fatalf("a cold refresh should end the run (sent %v, tick %v, cold %v)", ok, tick != nil, cold.idle.cold)
	}
	if got := cold.transcript[len(cold.transcript)-1].text; !strings.Contains(got, "stopped keeping it warm") {
		t.Errorf("a cold refresh said %q", got)
	}
	if _, _, ok := fireKeepAlive(t, cold); ok {
		t.Error("a refresh followed the first expiry")
	}

	// A failed one ends it too.
	failing := keepAliveSession(t, idleReadUsage, &sent)
	failing.wiring.KeepAliveSend = func(context.Context, []provider.Message) (provider.Usage, error) {
		return provider.Usage{}, errors.New("overloaded")
	}
	if failing, _, ok = fireKeepAlive(t, failing); !ok || !failing.idle.cold {
		t.Error("a failed refresh should end the run")
	}

	// Not Anthropic's own endpoint, not the hour, not asked for.
	for name, mutate := range map[string]func(*Model){
		"a gateway, not Claude": func(m *Model) { m.providerName, m.modelName = "gateway", "gpt-5" },
		"the off setting":       func(m *Model) { m.wiring.KeepAliveMax = 0 },
		"no sender":             func(m *Model) { m.wiring.KeepAliveSend = nil },
		"a downgraded one": func(m *Model) {
			m.wiring.Ledger = idledLedger(t, "5m", "claude-sonnet-5-5", 20*time.Minute, idleReadUsage)
		},
	} {
		sent = nil
		v := keepAliveSession(t, idleReadUsage, &sent)
		mutate(&v)
		if _, _, ok := fireKeepAlive(t, v); ok || len(sent) != 0 {
			t.Errorf("%s: a refresh went out", name)
		}
	}
}

// The fold wins where it is due, and the refresh is for the pause the fold
// declines: a window under its floor.
func TestKeepAlive_YieldsToTheFold(t *testing.T) {
	var sent [][]provider.Message
	m := keepAliveSession(t, idleReadUsage, &sent)
	m.wiring.IdleCompact.On = true
	next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	if !next.(Model).compacting || cmd == nil || len(sent) != 0 {
		t.Fatalf("a due fold should win (compacting %v, sent %d)", next.(Model).compacting, len(sent))
	}

	m.wiring.IdleCompact.Floor = 90
	if _, _, ok := fireKeepAlive(t, m); !ok || len(sent) != 1 {
		t.Error("a window under the floor should be refreshed instead")
	}
}

// The word and its answer are nobody's turn: the conversation is as it was,
// the request is a copy of it with the word after, and nothing starts.
func TestKeepAlive_NeverJoinsTheConversation(t *testing.T) {
	var sent [][]provider.Message
	m := keepAliveSession(t, idleReadUsage, &sent)
	before := m.agent.RequestMessages()

	m, _, ok := fireKeepAlive(t, m)
	if !ok || len(sent) != 1 {
		t.Fatalf("no refresh went out")
	}
	after := m.agent.RequestMessages()
	if len(after) != len(before) {
		t.Errorf("the conversation grew from %d to %d messages", len(before), len(after))
	}
	for _, msg := range after {
		if msg.Content == keepAliveWord {
			t.Error("the one-word turn joined the conversation")
		}
	}
	got := sent[0]
	if len(got) != len(before)+1 || got[len(got)-1].Role != provider.RoleUser || got[len(got)-1].Content != keepAliveWord {
		t.Errorf("the request was not the conversation and the word: %v", got)
	}
	if m.working() || m.turnInFlight() || m.compacting {
		t.Error("a refresh started a turn")
	}
	if n := len(m.transcript); n != 1 || m.transcript[0].kind != entrySystem {
		t.Errorf("the transcript should hold the receipt alone, has %d rows", n)
	}
}

// A gateway on the OpenAI dialect is included when its cache has been seen to
// work and the model is a Claude one; the lease is the configured one, which
// a gateway cannot confirm.
func TestKeepAlive_FiresThroughAGatewayWhoseCacheWorks(t *testing.T) {
	var sent [][]provider.Message
	m := keepAliveSession(t, idleReadUsage, &sent)
	m.providerName = "gateway"
	if !m.keepAliveDue(clock()) {
		t.Fatal("a gateway whose cache works, on a Claude model and the hour, was declined")
	}

	other := m
	other.modelName = "gpt-5"
	other.wiring.Ledger = idledLedger(t, "1h", "gpt-5", 20*time.Minute, idleReadUsage)
	if other.keepAliveDue(clock()) {
		t.Error("a model that is not Claude's was refreshed")
	}

	silent := m
	silent.wiring.Ledger = meter.New(pricing.NewTable(nil))
	if silent.keepAliveDue(clock()) {
		t.Error("a cache with no figure of its own was refreshed")
	}
}
