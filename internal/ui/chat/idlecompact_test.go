package chat

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// idleReadUsage is a request through the hour's lease that read the prefix
// the one before it wrote.
var idleReadUsage = provider.Usage{PromptTokens: 112_500, CachedTokens: 112_000, CacheCreationTokens: 500}

// idleLastStart is when the last request of idleSession was sent: the ledger
// writes its first at 09:00 and its second twenty minutes later.
var idleLastStart = time.Date(2026, 10, 10, 9, 20, 0, 0, time.UTC)

// idleSession is a session whose cache works on an hour's lease, whose window
// is 61% full, that has switched compacting while idle on at the defaults,
// and whose clock stands at the moment the rule fires: the last request plus
// the hour less the five minutes. Nobody has typed since the request was sent.
func idleSession(t *testing.T, requests *int) Model {
	t.Helper()
	m := compactingModel(requests)
	m.wiring.Ledger = idledLedger(t, "1h", "claude-sonnet-5-5", 20*time.Minute, idleReadUsage)
	m.providerName, m.modelName = "agoda", "claude-sonnet-5-5"
	m.wiring.IdleCompact = IdleCompact{On: true, Margin: 5 * time.Minute, Floor: 30}
	m.wiring.EndpointWindows = func(string) (int64, bool) { return 32_768, true }
	m.contextTokens = 20_000
	m.lastKeypress = idleLastStart
	setIdleClock(t, idleLastStart.Add(55*time.Minute))
	return m
}

func setIdleClock(t *testing.T, at time.Time) {
	t.Helper()
	was := clock
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = was })
}

// closedTurn is the model before and after the turn that was running ended:
// the transition the tick is scheduled from.
func closedTurn(m Model) (prev, after Model) {
	prev = m
	prev.state = stateStreaming
	return prev, m
}

// The rule holds at the margin, and the compaction it starts is the session's
// own: the trigger says idle.
func TestIdleCompact_FiresAtTheMargin(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	if !m.idleCompactDue(clock()) {
		t.Fatal("every clause holds at lifetime less the margin, and the rule declined")
	}
	next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	m = next.(Model)
	if !m.compacting || cmd == nil {
		t.Fatalf("the tick should have started a compaction: compacting=%v", m.compacting)
	}
	if got := m.compactTrigger(); got != hook.TriggerIdle {
		t.Errorf("trigger %q, want %q", got, hook.TriggerIdle)
	}
	if m.compactResume {
		t.Error("an idle compaction resumes no round")
	}

	// The tick is scheduled when the turn closes, for lifetime less margin less
	// the time already idle, and the rule holds from that moment until the
	// lifetime and not outside it.
	prev, closed := closedTurn(idleSession(t, &requests))
	if tick := closed.idleCompactCmd(prev); tick == nil {
		t.Fatal("a closing turn should schedule the tick")
	}
	wait, ok := closed.idleNextFire(idleLastStart.Add(time.Minute))
	if !ok || wait != 54*time.Minute {
		t.Errorf("the next cycle fires in %v (%v), want 54m", wait, ok)
	}
	if closed.idleCompactDue(idleLastStart.Add(53 * time.Minute)) {
		t.Error("the rule fired before the margin")
	}
	if closed.idleCompactDue(idleLastStart.Add(61 * time.Minute)) {
		t.Error("the rule fired after the cache had expired")
	}
}

// Nothing fires where the cache has not been seen to work.
func TestIdleCompact_DeclinesWhenTheCacheIsNotSeen(t *testing.T) {
	requests := 0
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{"no ledger", func(m *Model) { m.wiring.Ledger = nil }},
		{"none yet", func(m *Model) { m.wiring.Ledger = meter.New(pricing.NewTable(nil)) }},
		{"not seen", func(m *Model) {
			l := meter.New(pricing.NewTable(nil))
			l.SetCacheLease("1h")
			for range 14 {
				l.ObserveCache("agoda", "claude-sonnet-5-5", provider.Usage{PromptTokens: 20_000})
			}
			m.wiring.Ledger = l
		}},
		{"an hour written as five minutes", func(m *Model) {
			m.wiring.Ledger = idledLedger(t, "1h", "claude-sonnet-5-5", 20*time.Minute,
				provider.Usage{PromptTokens: 20_000, CacheCreationTokens: 20_000, CacheCreation5m: 20_000})
		}},
		{"the switch is off", func(m *Model) { m.wiring.IdleCompact.On = false }},
	} {
		m := idleSession(t, &requests)
		tc.setup(&m)
		if m.idleCompactDue(clock()) {
			t.Errorf("%s: the rule fired", tc.name)
		}
		next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
		if cmd != nil || next.(Model).compacting {
			t.Errorf("%s: the tick started a compaction", tc.name)
		}
	}

	m := idleSession(t, &requests)
	provider.RegisterDeclarations("agoda", map[string]provider.Declared{
		"claude-sonnet-5-5": {Model: map[string]any{provider.KeyPromptCache: false}},
	})
	t.Cleanup(func() { provider.RegisterDeclarations("agoda", nil) })
	if m.idleCompactDue(clock()) {
		t.Error("a profile that declares the cache off: the rule fired")
	}
}

// A window under the floor folds too little to pay for what it loses.
func TestIdleCompact_DeclinesUnderTheFloor(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	m.contextTokens = 5_000
	if m.idleCompactDue(clock()) {
		t.Fatalf("a window at %d%% was folded under a floor of 30", m.contextPercent())
	}
	next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	if cmd != nil || next.(Model).compacting {
		t.Error("a declined tick started something")
	}
	m.contextTokens = 20_000
	m.wiring.IdleCompact.Floor = 90
	if m.idleCompactDue(clock()) {
		t.Error("the floor setting was not read")
	}
}

// A person typing inside the margin is about to send, and the send refreshes
// the cache: this pause's fold is cancelled and not asked again.
func TestIdleCompact_DeclinesWhileTyping(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	m.lastKeypress = clock().Add(-2 * time.Minute)
	next, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	got := next.(Model)
	if cmd != nil || got.compacting || got.idle != m.idle {
		t.Fatalf("a keypress inside the margin should cancel the cycle: compacting=%v cmd=%v", got.compacting, cmd != nil)
	}
	if _, again := got.Update(idleCompactMsg{gen: m.idle.gen}); again != nil {
		t.Error("a declined cycle was retried")
	}

	// And the other ways it can be busy: a turn in flight, a compaction under
	// way, a screen somebody else is using, and nothing to fold.
	for name, set := range map[string]func(m *Model){
		"a turn in flight":    func(m *Model) { m.state = stateStreaming },
		"a compaction":        func(m *Model) { m.compacting = true },
		"the screen is taken": func(m *Model) { m.attachedTo = "child" },
		"nothing to fold":     func(m *Model) { m.agent.SetMessages(m.agent.Messages()[:1]) },
	} {
		m := idleSession(t, &requests)
		set(&m)
		if m.idleCompactDue(clock()) {
			t.Errorf("%s: the rule fired", name)
		}
	}
}

// With no lifetime known the rule never fires: no lease on the requests and
// nothing measured.
func TestIdleCompact_DeclinesWithNoLifetime(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	// A model that takes no lease has no lifetime until one is measured.
	m.wiring.Ledger = idledLedger(t, "1h", "qwen3", 20*time.Minute, idleReadUsage)
	m.modelName = "qwen3"
	if r := m.wiring.Ledger.CacheReadingFor("agoda", "qwen3"); r.State() != meter.CacheWorks || r.EffectiveLifetime() != 0 {
		t.Fatalf("the fixture has a lifetime of %v", r.EffectiveLifetime())
	}
	if m.idleCompactDue(clock()) {
		t.Error("the rule fired with no lifetime")
	}
	if _, ok := m.idleNextFire(clock()); ok {
		t.Error("a cycle was scheduled with no lifetime")
	}
	// A margin as long as the lifetime leaves nothing to wait for.
	m = idleSession(t, &requests)
	m.wiring.IdleCompact.Margin = time.Hour
	if _, ok := m.idleNextFire(clock()); ok || m.idleCompactDue(clock()) {
		t.Error("a margin as long as the lifetime still scheduled a cycle")
	}
}

// One tick per turn close: a later close moves the generation, and the tick
// of the earlier one matches nothing when it arrives.
func TestIdleCompact_ALaterTurnSupersedesTheTick(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	prev, closed := closedTurn(m)
	if closed.idleCompactCmd(prev) == nil {
		t.Fatal("the first close scheduled nothing")
	}
	first := closed.idle.gen
	prev, closed = closedTurn(closed)
	if closed.idleCompactCmd(prev) == nil {
		t.Fatal("the second close scheduled nothing")
	}
	if closed.idle.gen == first {
		t.Fatal("a later close did not move the generation")
	}
	next, cmd := closed.Update(idleCompactMsg{gen: first})
	if cmd != nil || next.(Model).compacting {
		t.Error("the superseded tick compacted")
	}
	next, cmd = closed.Update(idleCompactMsg{gen: closed.idle.gen})
	if cmd == nil || !next.(Model).compacting {
		t.Error("the current tick did not compact")
	}
}

// The compaction's own close does not schedule another cycle: one pause is
// one fold, however long the person stays away.
func TestIdleCompact_TheFoldDoesNotScheduleAnother(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	next, _ := m.Update(idleCompactMsg{gen: m.idle.gen})
	folding := next.(Model)
	ended := folding
	ended.state = stateInput
	ended.compacting = false
	if tick := ended.idleCompactCmd(folding); tick != nil {
		t.Error("the compaction's own close scheduled a tick")
	}
	if ended.idle.own {
		t.Error("the close left the compaction marked as the session's own")
	}
}

// A pre_compact hook is told the trigger is idle, and may refuse it: the
// conversation stays as it was and no summary is paid for.
func TestHooks_IdleCompactionIsATrigger(t *testing.T) {
	var seen []hook.Payload
	requests := 0
	m := idleSession(t, &requests)
	m.wiring.Hooks = hookRunner(t, map[string]hook.Entry{
		"hold": {Event: hook.PreCompact, Command: "hold"},
	}, "stay warm\n", hook.DenyExit, &seen)

	updated, cmd := m.Update(idleCompactMsg{gen: m.idle.gen})
	m = updated.(Model)
	var answered bool
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(preCompactMsg); ok {
			updated, _ = m.Update(msg)
			m, answered = updated.(Model), true
		}
	}
	if !answered {
		t.Fatal("an idle compaction with a hook in front of it should have produced the hook's cmd")
	}
	if len(seen) != 1 || seen[0].Trigger != hook.TriggerIdle {
		t.Fatalf("the hook should be told the session folded itself: %+v", seen)
	}
	if requests != 0 || m.compacting || len(m.Messages()) != 3 {
		t.Errorf("a refused idle compaction should leave the conversation: requests=%d compacting=%v", requests, m.compacting)
	}

	// And the seam behind one hears the same word.
	seen = nil
	m = idleSession(t, &requests)
	m.wiring.Hooks = hookRunner(t, map[string]hook.Entry{
		"after": {Event: hook.PostCompact, Command: "after"},
	}, "", 0, &seen)
	m.compacting, m.compactRun, m.streaming = true, &compactStart{pct: 61, idle: true}, "the summary"
	updated, cmd = m.finishCompact()
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(hookNotesMsg); ok {
			m.Update(msg)
		}
	}
	if len(seen) != 1 || seen[0].Event != hook.PostCompact || seen[0].Trigger != hook.TriggerIdle {
		t.Errorf("post_compact should carry trigger idle: %+v", seen)
	}
}

// The receipt of a compaction the session began itself says why first.
func TestGolden_IdleCompactReceipt(t *testing.T) {
	captureGolden(t, "idle-compact-receipt", "the receipt an idle compaction leaves", goldenWidths, func(width int) []golden.Panel {
		const summary = "Rounds are counted in the round loop; the limit lived in three places and " +
			"disagreed. The first turns established the loop as the owner, the fourth moved the " +
			"constant, and the fifth's tests pass except the one on the limit itself. The " +
			"constant now lives beside the counter and nothing else declares one."
		receipt := &compactReceipt{
			first: 1, last: 7, was: 88, now: 28, idle: true,
			tokens: 74000, cost: "$0.03", duration: 6 * time.Second,
		}
		panel := func(open bool) string {
			m := frameModel(t, width, 40)
			m.appendEntry(entry{kind: entryCompactSummary, text: summary, expanded: open, compact: receipt})
			return m.renderHistory()
		}
		return []golden.Panel{
			{Label: "the receipt, its fold open", View: panel(true)},
			{Label: "folded back up", View: panel(false)},
		}
	})
}

// A compaction driven through the model leaves the idle receipt on the
// transcript, timed by the held clock, and the line reads as the item says.
func TestIdleCompact_TheReceiptSaysWhy(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	updated, _ := m.Update(idleCompactMsg{gen: m.idle.gen})
	m = updated.(Model)
	m.streaming = "the summary"
	setIdleClock(t, clock().Add(6*time.Second))
	updated, _ = m.finishCompact()
	m = updated.(Model)
	var r *compactReceipt
	for _, e := range m.transcript {
		if e.kind == entryCompactSummary {
			r = e.compact
		}
	}
	if r == nil || !r.idle || r.duration != 6*time.Second {
		t.Fatalf("the receipt should be the idle one, 6s long: %+v", r)
	}
	line := compactNotice(compactReceipt{first: 1, last: 7, was: 88, now: 28, idle: true,
		tokens: 74000, cost: "$0.03", duration: 6 * time.Second}, 9, "context")
	want := "compacted while idle, before the cache expired · turns 1–7 into a 9-line summary · 74k tokens · context 88% → 28% · $0.03 · 6.0s"
	if line != want {
		t.Errorf("the receipt reads\n%q\nwant\n%q", line, want)
	}
}

// While the switch is on the idle clock is read against the fire point.
func TestContextCache_IdleReadsAgainstTheFirePoint(t *testing.T) {
	requests := 0
	m := idleSession(t, &requests)
	setIdleClock(t, idleLastStart.Add(12*time.Minute))
	if got := m.contextCache().Idle; got != "12m of 55m" {
		t.Errorf("switch on: idle %q, want %q", got, "12m of 55m")
	}
	m.wiring.IdleCompact.On = false
	if got := m.contextCache().Idle; got != "12m" {
		t.Errorf("switch off: idle %q, want %q", got, "12m")
	}
}
