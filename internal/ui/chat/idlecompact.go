package chat

// Compacting while the person is away, before the prompt cache expires.
//
// A cache that outlives a pause turns the next turn's prompt into a read at a
// tenth of the price; one that does not turns it into a write of the whole
// window. When the person has asked for it, the session folds the conversation
// into a summary a few minutes before the cache would expire, so the next turn
// writes the small conversation instead of the large one. The fold is the
// price (the folded turns leave the model's view), which is why the switch is
// off until somebody turns it on.
// See docs/capabilities/coding-agent.md#the-session-can-compact-while-you-are-away.

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/meter"
)

// IdleCompact is the three settings that govern it, as the session was wired
// with them and as the settings screen moves them. Margin is how long before
// the cache's lifetime ends the fold fires; Floor is the occupancy, in
// percent, at or under which it never does.
type IdleCompact struct {
	On     bool
	Margin time.Duration
	Floor  int
}

// idleSlack is how early the tick may land against the cache's clock and
// still be on time: the two clocks are read a moment apart.
const idleSlack = time.Second

// idleCompactMsg is the one tick a turn's close schedules. It carries the
// generation it was scheduled in, so a later close, which bumps the
// generation, leaves it matching nothing.
type idleCompactMsg struct{ gen int }

// idleState is the tick's bookkeeping: the generation of the latest close, and
// whether the compaction now under way is the idle one's own, whose close
// does not schedule a cycle of its own (one pause is one fold).
type idleState struct {
	gen int
	own bool
	// refreshed is how many refreshes this pause has sent, and cold whether
	// one found the cache gone or failed; both start over at the next close
	// (keepalive.go).
	refreshed int
	cold      bool
}

// idleCompactCmd is derived from the model before against the model after,
// for the reason the desktop notification is (model.go): a turn closing is a
// transition, and every path that makes one passes here. Every close bumps
// the generation, which supersedes the tick the one before scheduled; a close
// then schedules a single tick for when the rule might first hold. The tick is
// only a wake-up: the rule is read again when it fires.
func (m *Model) idleCompactCmd(prev Model) tea.Cmd {
	if !prev.working() || m.working() {
		return nil
	}
	m.idle.gen++
	m.idle.refreshed, m.idle.cold = 0, false
	if m.idle.own {
		m.idle.own = false
		return nil
	}
	if !m.wiring.IdleCompact.On && m.wiring.KeepAliveMax <= 0 {
		return nil
	}
	wait, ok := m.idleNextFire(clock())
	if !ok {
		return nil
	}
	gen := m.idle.gen
	return tea.Tick(wait, func(time.Time) tea.Msg { return idleCompactMsg{gen: gen} })
}

// idleNextFire is when the next cycle fires, as a wait from now: the cache's
// lifetime less the margin less the time it has already been idle. It is the
// one place that answers the question, so whatever else is to act in the
// window before the cache expires asks here. False where there is nothing to
// wait for: no reading that works, no lifetime known, or a margin that leaves
// no wait.
func (m Model) idleNextFire(now time.Time) (time.Duration, bool) {
	r, ok := m.idleReading()
	if !ok {
		return 0, false
	}
	at, ok := m.idleFireAt(r)
	if !ok {
		return 0, false
	}
	return max(at.Sub(now), time.Second), true
}

// idleReading is the endpoint's cache reading when it is one the rule may act
// on: the cache works there, and the lease asked for is the lease written.
func (m Model) idleReading() (meter.CacheReading, bool) {
	if m.wiring.Ledger == nil {
		return meter.CacheReading{}, false
	}
	r := m.wiring.Ledger.CacheReadingFor(m.providerName, m.modelName)
	if r.State() != meter.CacheWorks || r.Downgraded() {
		return r, false
	}
	return r, true
}

// idleFireAt is the moment the cache's own clock reaches lifetime less the
// margin: the start of the last request that used it, plus the lifetime less
// the margin. False with no lifetime known, nothing to count from, or a
// margin as long as the lifetime.
func (m Model) idleFireAt(r meter.CacheReading) (time.Time, bool) {
	life := r.EffectiveLifetime()
	since := r.LastStart
	if since.IsZero() {
		since = r.LastAt
	}
	if life <= 0 || since.IsZero() || m.wiring.IdleCompact.Margin >= life {
		return time.Time{}, false
	}
	return since.Add(life - m.wiring.IdleCompact.Margin), true
}

// idleCompactDue re-reads every part of the rule at the moment of firing and
// says whether the session should fold now. Nothing it reads was decided when
// the tick was scheduled.
func (m Model) idleCompactDue(now time.Time) bool {
	if !m.wiring.IdleCompact.On || !m.idleWindowOpen(now) {
		return false
	}
	// Under the floor a fold gives back too little to pay for what it loses.
	return m.contextPercent() > m.wiring.IdleCompact.Floor
}

// idleWindowOpen is the part of the rule that is about the moment and not
// about what is done in it, shared by whatever acts in the window before the
// cache expires: there is a conversation, the cache works and is ahead of its
// expiry, and nobody is here.
func (m Model) idleWindowOpen(now time.Time) bool {
	w := m.wiring.IdleCompact
	if m.agent == nil || len(m.agent.Messages()) <= 1 {
		return false
	}
	// The cache is worth the trouble only where it works, and only until it
	// expires: past the lifetime there is nothing left to save.
	r, ok := m.idleReading()
	if !ok {
		return false
	}
	at, ok := m.idleFireAt(r)
	if !ok || now.Add(idleSlack).Before(at) || !now.Before(at.Add(w.Margin)) {
		return false
	}
	// Somebody is here, or something is under way: a turn, a compaction, a
	// screen of somebody else's. And a person typing is about to send, and
	// the send refreshes the cache for free.
	if m.turnInFlight() || m.compacting || !m.screenIsFree() {
		return false
	}
	return now.Sub(m.lastKeypress) >= w.Margin
}

// fireIdleCompact answers the tick. A tick from an earlier generation is
// somebody else's; one that fires into a rule that no longer holds does
// nothing and is not retried, since the close of the next turn schedules the
// next cycle. Where the fold is due it wins over a refresh: it makes the
// next write small for good, and a refresh only puts the same write off. The
// refresh is for the pause the fold declines, a window under its floor or a
// fold that is off.
func (m Model) fireIdleCompact(msg idleCompactMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.idle.gen {
		return m, nil
	}
	now := clock()
	if m.idleCompactDue(now) {
		m.idle.own = true
		return m.startCompactFor(true)
	}
	if m.keepAliveDue(now) {
		return m, m.keepAliveCmd(msg.gen)
	}
	return m, nil
}
