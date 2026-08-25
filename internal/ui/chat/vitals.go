package chat

// Session vitals and context accounting (S-093, DESIGN-TUI.md §15a). Two
// numbers the session already half-knew get a shape here:
//
//   - the per-turn usage history — input, output, cached, cost, wall time —
//     kept as a bounded ring with running totals that survive eviction, so
//     the burn sparkline has a series and the session total stays exact;
//   - context occupancy by category — system prompt, project context, tool
//     definitions, transcript, tool output — so the rail, /stats and the
//     context-pressure card render a breakdown instead of one percentage.
//
// Both are derived from what the session already tracks: usage reports as
// they arrive, and the byte lengths of messages already in the list. There is
// no second tokenizer pass — agent.EstimateTokens is len(s)/4 — and nothing
// here does per-keystroke work beyond that arithmetic.

import (
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// vitalsHistory bounds the per-turn ring. It only has to outlive the
// sparkline's cell count; the session totals are running counters, so
// eviction costs history, never the total.
const vitalsHistory = 32

// turnVitals is one turn's accounting: what it spent, how long it took, and
// the context it left behind.
type turnVitals struct {
	In, Out, Cached int64
	// Cost is the turn's dollar cost and Priced whether the pricing table
	// knew the model — an unpriced turn reports tokens, never a made-up zero.
	Cost    float64
	Priced  bool
	Elapsed time.Duration
	Context int64
}

// vitals is the session's usage history: the closed turns, the turn under
// assembly, the per-round burn series behind the sparkline, and the running
// totals.
type vitals struct {
	turns   []turnVitals
	evicted int
	current turnVitals
	open    bool
	// burn is the per-round context estimate, bounded to the sparkline's
	// cells — the rail labels it "per round" because that is what it is.
	burn        []float64
	lastContext int64

	totalIn, totalOut, totalCached int64
	totalCost                      float64
	priced                         bool
}

// startTurn opens a fresh turn's accounting. A turn still open (a cancelled
// one, say) is closed with whatever it spent rather than dropped.
func (v *vitals) startTurn() {
	if v.open {
		v.closeTurn(0)
	}
	v.current = turnVitals{}
	v.open = true
}

// record folds one request's usage into the open turn and the session
// totals, and appends the round's context estimate to the burn series.
func (v *vitals) record(u provider.Usage, cost float64, priced bool) {
	in, out := int64(u.PromptTokens), int64(u.CompletionTokens)
	cached := int64(u.CachedTokens)

	v.current.In += in
	v.current.Out += out
	v.current.Cached += cached
	v.current.Cost += cost
	v.current.Priced = v.current.Priced || priced

	v.totalIn += in
	v.totalOut += out
	v.totalCached += cached
	v.totalCost += cost
	v.priced = v.priced || priced

	// The latest request's prompt plus its completion is what the next
	// request will roughly carry as context.
	v.lastContext = in + out
	v.current.Context = v.lastContext
	v.burn = append(v.burn, float64(v.lastContext))
	if n := len(v.burn); n > contextBurnSamples {
		v.burn = v.burn[n-contextBurnSamples:]
	}
}

// endTurn closes the open turn with its wall time and pushes it into the
// ring. A turn that never spent anything is still history — it took time.
func (v *vitals) endTurn(elapsed time.Duration) {
	if !v.open {
		return
	}
	v.closeTurn(elapsed)
}

func (v *vitals) closeTurn(elapsed time.Duration) {
	t := v.current
	t.Elapsed = elapsed
	v.turns = append(v.turns, t)
	if n := len(v.turns); n > vitalsHistory {
		v.turns = append(v.turns[:0], v.turns[n-vitalsHistory:]...)
		v.evicted += n - vitalsHistory
	}
	v.current = turnVitals{}
	v.open = false
}

// series is the burn sparkline's input. One sample is a dot, not a trend, so
// a single round reports nothing to draw.
func (v vitals) series() []float64 {
	if len(v.burn) < 2 {
		return nil
	}
	return v.burn
}

// clearBurn drops the burn series, for a compaction that discarded the
// conversation the series described.
func (v *vitals) clearBurn() { v.burn = nil }

// reset clears the whole history — /clear starts a session's accounting over.
func (v *vitals) reset() { *v = vitals{} }

// lastTurn is the most recently closed turn.
func (v vitals) lastTurn() (turnVitals, bool) {
	if len(v.turns) == 0 {
		return turnVitals{}, false
	}
	return v.turns[len(v.turns)-1], true
}

// usageCost prices one request against the session's model, reporting
// whether the table knew it.
func (m Model) usageCost(u provider.Usage) (float64, bool) {
	if m.prices == nil || m.modelName == "" {
		return 0, false
	}
	in, out, found := m.prices.Cost(m.modelName, int64(u.PromptTokens), int64(u.CompletionTokens))
	if !found {
		return 0, false
	}
	return in + out, true
}

// contextBreakdown is context occupancy by category: where the window is
// actually going. The categories are exhaustive by construction — every
// message lands in exactly one — so they always sum to total().
type contextBreakdown struct {
	// System is the system prompt without the project context injected into
	// it, which is broken out separately because it is the one category the
	// user can shrink by editing a file.
	System      int64
	Project     int64
	Tools       int64
	Messages    int64
	ToolResults int64
	// Reported is true when the categories were scaled to the provider's
	// reported context size. False means the total is our own estimate, and
	// every surface showing it says so.
	Reported bool
}

func (b contextBreakdown) total() int64 {
	return b.System + b.Project + b.Tools + b.Messages + b.ToolResults
}

// contextAccounting is the single source for context occupancy: the rail's
// CONTEXT block, /stats' breakdown and the trim thresholds all read it.
func (m Model) contextAccounting() contextBreakdown {
	b := contextBreakdown{Tools: m.toolDefTokens}
	for i, msg := range m.agent.Messages() {
		switch {
		case i == 0 && msg.Role == provider.RoleSystem:
			b.System += agent.EstimateTokens(msg.Content)
		case msg.Role == provider.RoleTool:
			b.ToolResults += agent.EstimateTokens(msg.Content)
		default:
			b.Messages += agent.EstimateTokens(msg.Content)
			for _, tc := range msg.ToolCalls {
				b.Messages += agent.EstimateTokens(tc.Arguments)
			}
		}
	}
	// The project context rides inside the system prompt; split it back out.
	if p := min(m.projectTokens, b.System); p > 0 {
		b.System -= p
		b.Project = p
	}
	if m.contextTokens > 0 {
		return b.scaledTo(m.contextTokens)
	}
	return b
}

// scaledTo rescales the estimated categories so they sum to the provider's
// reported context size. The shares are ours, the total is the provider's,
// and the rounding remainder goes to the largest category so the parts never
// disagree with the whole.
func (b contextBreakdown) scaledTo(target int64) contextBreakdown {
	est := b.total()
	if target <= 0 {
		return b
	}
	if est <= 0 {
		// Nothing to apportion — a reported context with no message list
		// behind it (a resumed session mid-stream) is all prompt.
		return contextBreakdown{System: target, Reported: true}
	}
	out := contextBreakdown{Reported: true}
	src := []int64{b.System, b.Project, b.Tools, b.Messages, b.ToolResults}
	dst := []*int64{&out.System, &out.Project, &out.Tools, &out.Messages, &out.ToolResults}
	largest := 0
	for i, v := range src {
		*dst[i] = v * target / est
		if v > src[largest] {
			largest = i
		}
	}
	*dst[largest] += target - out.total()
	return out
}

// WithProjectContextTokens sets the estimated token cost of the project
// context (AGENTS.md and friends) injected into the system prompt, so the
// occupancy breakdown can name it separately (S-093).
func (m Model) WithProjectContextTokens(n int64) Model {
	m.projectTokens = n
	return m
}
