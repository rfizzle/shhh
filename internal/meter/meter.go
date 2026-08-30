package meter

// Session spend accounting. Every request shhh makes — the agent's own
// rounds, the permission classifier, the session summary, each sub-agent's
// turns — is a call to a provider, so the provider is the one thing a feature
// cannot route around. The gate wraps a provider and attributes what passes
// through it to an origin; the ledger behind the gate adds it up.
//
// The alternative, and what this replaces, is each feature remembering to
// report what it spent. That works until it doesn't: a feature added later
// counts nothing, and nothing fails — the session simply under-reports, which
// is the one kind of wrong a spend meter must never be.
//
// See docs/architecture.md#spend-is-counted-at-the-provider.

import (
	"sync"

	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

// Source is what kind of thing made a request. It is one axis the session is
// reported along, because "what is this costing me" and "what is the
// background costing me" are different questions with the same tokens in
// them.
type Source string

const (
	SourceAgent      Source = "agent"
	SourceClassifier Source = "classifier"
	SourceSummary    Source = "summary"
	// SourceBacklog is the reading that turns a session into backlog items.
	SourceBacklog  Source = "backlog"
	SourceSubagent Source = "sub-agent"
	SourceOneShot  Source = "one-shot"
	// SourceUnattributed is where a gated request with no source lands. It
	// exists so that a feature wired through the gate without declaring
	// itself is visible in the total and named in the breakdown, rather than
	// being silently dropped or silently filed under the agent.
	SourceUnattributed Source = "unattributed"
)

// Origin is who spent, to the finest grain the session can name: the kind of
// requester and, where there is more than one of that kind, which one. A
// fan-out is the case that forces it — "sub-agents cost $2.40" is not an
// answer to "which of them cost that", and the child that ran away with the
// budget is the one worth naming.
type Origin struct {
	Source Source
	// Label distinguishes one requester of a kind from another — a
	// sub-agent's name. Empty where the kind has only one member, as the
	// classifier and the summary do.
	Label string
}

func (o Origin) String() string {
	if o.Label == "" {
		return string(o.Source)
	}
	return string(o.Source) + ":" + o.Label
}

// Entry is one origin's spend on one model. All three of origin, label and
// model are part of the key: the classifier and the summary routinely run on
// a cheaper model than the session, and a fan-out bills several models at
// once, so a single figure priced against "the" model is a number nobody can
// reconcile.
type Entry struct {
	Origin   Origin
	Model    string
	In       int64
	Out      int64
	Cached   int64
	Cost     float64
	Priced   bool
	Requests int
}

// Totals is a roll-up. Priced is false when the pricing table knew none of
// the models involved, which every surface reports as tokens rather than as a
// cost of zero.
type Totals struct {
	In       int64
	Out      int64
	Cached   int64
	Cost     float64
	Priced   bool
	Requests int
}

func (t *Totals) add(e Entry) {
	t.In += e.In
	t.Out += e.Out
	t.Cached += e.Cached
	t.Cost += e.Cost
	t.Priced = t.Priced || e.Priced
	t.Requests += e.Requests
}

// Ledger is the session's spend, accumulated in first-use order. Sub-agents
// run on their own goroutines, so every method is safe to call from any of
// them.
type Ledger struct {
	mu      sync.Mutex
	prices  *pricing.Table
	entries []Entry
}

func New(prices *pricing.Table) *Ledger { return &Ledger{prices: prices} }

// Record folds one request's usage into the ledger, pricing it against the
// model that actually answered rather than whichever model the session
// happens to be on when someone asks.
func (l *Ledger) Record(o Origin, model string, u provider.Usage) {
	if l == nil {
		return
	}
	if o.Source == "" {
		o.Source = SourceUnattributed
	}
	in, out := int64(u.PromptTokens), int64(u.CompletionTokens)

	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(o, model)
	e.In += in
	e.Out += out
	e.Cached += int64(u.CachedTokens)
	e.Requests++
	if l.prices != nil && model != "" {
		if inCost, outCost, found := l.prices.Cost(model, in, out); found {
			e.Cost += inCost + outCost
			e.Priced = true
		}
	}
}

// entry returns the running total for an origin and model, creating it in
// first-use order. The caller holds the lock.
func (l *Ledger) entry(o Origin, model string) *Entry {
	for i := range l.entries {
		if l.entries[i].Origin == o && l.entries[i].Model == model {
			return &l.entries[i]
		}
	}
	l.entries = append(l.entries, Entry{Origin: o, Model: model})
	return &l.entries[len(l.entries)-1]
}

// Total is the whole session: every origin, every model.
func (l *Ledger) Total() Totals { return l.totalWhere(func(Entry) bool { return true }) }

// SourceTotal is one kind of requester's spend, across every one of them and
// every model they used.
func (l *Ledger) SourceTotal(s Source) Totals {
	return l.totalWhere(func(e Entry) bool { return e.Origin.Source == s })
}

// OriginTotal is one named requester's spend — one sub-agent's, say.
func (l *Ledger) OriginTotal(o Origin) Totals {
	return l.totalWhere(func(e Entry) bool { return e.Origin == o })
}

func (l *Ledger) totalWhere(keep func(Entry) bool) Totals {
	if l == nil {
		return Totals{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var t Totals
	for _, e := range l.entries {
		if keep(e) {
			t.add(e)
		}
	}
	return t
}

// Entries is the full per-origin, per-model breakdown in first-use order.
func (l *Ledger) Entries() []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Entry(nil), l.entries...)
}

// BySource, ByOrigin and ByModel are three roll-ups of the same entries, in
// first-use order. They are separate views because the questions they answer
// — what is the background costing, which sub-agent cost that, and what is
// each model costing — do not decompose into one another.
func (l *Ledger) BySource() []Entry {
	return l.rollUp(func(e Entry) Entry { return Entry{Origin: Origin{Source: e.Origin.Source}} })
}

func (l *Ledger) ByOrigin() []Entry {
	return l.rollUp(func(e Entry) Entry { return Entry{Origin: e.Origin} })
}

func (l *Ledger) ByModel() []Entry {
	return l.rollUp(func(e Entry) Entry { return Entry{Model: e.Model} })
}

func (l *Ledger) rollUp(key func(Entry) Entry) []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for _, e := range l.entries {
		k := key(e)
		idx := -1
		for i := range out {
			if out[i].Origin == k.Origin && out[i].Model == k.Model {
				idx = i
				break
			}
		}
		if idx < 0 {
			out = append(out, k)
			idx = len(out) - 1
		}
		out[idx].In += e.In
		out[idx].Out += e.Out
		out[idx].Cached += e.Cached
		out[idx].Cost += e.Cost
		out[idx].Priced = out[idx].Priced || e.Priced
		out[idx].Requests += e.Requests
	}
	return out
}

// Reset clears the ledger — /clear starts a session's accounting over.
func (l *Ledger) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
}
