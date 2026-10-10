package chat

// Keeping the prompt cache warm through a pause, a bounded number of times.
//
// A request that reads the whole prefix and asks for one token costs a tenth
// of what writing the prefix again does, and moves the cache's expiry an hour
// on. For a pause the idle compaction declines (the window is under its
// floor, or the fold is off) a few of them cost less than the write they put
// off, and lose nothing. They are also requests nobody asked for, sent with
// nobody there, which is why the number is the person's to set, zero is the
// default, and the first refresh that finds the cache gone ends the run of
// them. The word and its answer never join the conversation: the request is
// built from a copy of it, the answer is not read, and nothing here is a turn.
// See docs/capabilities/providers.md#a-bounded-refresh-keeps-the-cache-warm.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// keepAliveWord is the whole of the refresh's own turn.
const keepAliveWord = "ok"

// keepAliveTimeout bounds one refresh: it asks for one token and nobody is
// waiting on it.
const keepAliveTimeout = 2 * time.Minute

// keepAliveMsg is a refresh's answer: what it was billed, or why it was not.
// It carries the generation it was sent in, so a turn that has closed since
// takes the receipt but not the next refresh.
type keepAliveMsg struct {
	gen   int
	usage provider.Usage
	err   error
}

// keepAliveDue re-reads every part of the rule at the moment of firing: the
// window is open as for a fold, the fold is not what this pause is for, the
// endpoint is Anthropic's, or a gateway's with a working cache on a Claude model, on the hour's lease, and the bound and the
// first expiry have not ended the run.
func (m Model) keepAliveDue(now time.Time) bool {
	if m.wiring.KeepAliveMax <= 0 || m.wiring.KeepAliveSend == nil ||
		m.idle.cold || m.idle.refreshed >= m.wiring.KeepAliveMax {
		return false
	}
	r, ok := m.idleReading()
	if !ok || r.Lease != string(provider.CacheTTL1h) {
		return false
	}
	// Anthropic's own endpoint, or a gateway whose cache has been seen to
	// work on a Claude model: the reading already proves the marks reached
	// Anthropic, though a gateway cannot confirm the lease. A wrong guess is
	// bounded to one cold refresh, which ends the run.
	if m.providerName != "anthropic" && !strings.Contains(strings.ToLower(m.modelName), "claude") {
		return false
	}
	if w := m.wiring.IdleCompact; w.On && m.contextPercent() > w.Floor {
		return false
	}
	return m.idleWindowOpen(now)
}

// keepAliveCmd sends the refresh off the goroutine drawing the screen. The
// messages are a copy of the conversation with the word after it; the
// conversation itself is not touched.
func (m Model) keepAliveCmd(gen int) tea.Cmd {
	send := m.wiring.KeepAliveSend
	msgs := append(m.agent.RequestMessages(), provider.Message{Role: provider.RoleUser, Content: keepAliveWord})
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), keepAliveTimeout)
		defer cancel()
		u, err := send(ctx, msgs)
		return keepAliveMsg{gen: gen, usage: u, err: err}
	}
}

// finishKeepAlive writes the receipt for a refresh and schedules the next.
// A refresh that read nothing found the cache already gone, so what it paid
// was a write: the run ends there, and the line says so. A failed one ends
// it too.
func (m Model) finishKeepAlive(msg keepAliveMsg) (tea.Model, tea.Cmd) {
	current := msg.gen == m.idle.gen
	if current {
		m.idle.refreshed++
	}
	cost, priced := m.usageCost(msg.usage)
	paid := ""
	if priced {
		paid = " · " + formatCost(cost)
	}
	switch {
	case msg.err != nil:
		m.idle.cold = m.idle.cold || current
		m.appendEntry(entry{kind: entrySystem, text: "could not keep the cache warm · " + msg.err.Error()})
	case msg.usage.CachedTokens <= 0:
		m.idle.cold = m.idle.cold || current
		m.appendEntry(entry{kind: entrySystem,
			text: "the cache was already gone · stopped keeping it warm" + paid})
	default:
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("kept the cache warm · %s read%s",
			formatTokenCount(int64(msg.usage.CachedTokens)), paid)})
	}
	m.syncViewport()
	if !current || m.idle.cold {
		return m, nil
	}
	wait, ok := m.idleNextFire(clock())
	if !ok {
		return m, nil
	}
	gen := msg.gen
	return m, tea.Tick(wait, func(time.Time) tea.Msg { return idleCompactMsg{gen: gen} })
}
