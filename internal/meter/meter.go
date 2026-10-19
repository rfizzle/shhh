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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/pricing"
)

// Source is what kind of thing made a request. It is one axis the session is
// reported along, because "what is this costing me" and "what is the
// background costing me" are different questions with the same tokens in
// them.
type Source string

const (
	SourceAgent   Source = "agent"
	SourceSummary Source = "summary"
	// SourceSubagent is a sub-agent's turns, named per child by Origin.Label.
	SourceSubagent Source = "sub-agent"
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
	Origin Origin
	Model  string
	// Provider names the dialect that answered, where the request went
	// through the gate; empty for spend recorded without one. It is what
	// tells a request paid for by a ChatGPT plan from one billed to a key.
	Provider string
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
	// Plan is the part of In and Out a plan paid for, which no price applies
	// to: a reader pricing the remainder must leave it out.
	Plan int64
}

// Plus is two roll-ups summed — an attempt's spend added to what the attempts
// before it cost, say. Priced carries if either side was priced, because that
// is the question every reader of it asks: was any of this billed at a rate
// the table knew, or is the cost a zero standing in for "no idea".
func (t Totals) Plus(o Totals) Totals {
	return Totals{
		In:       t.In + o.In,
		Out:      t.Out + o.Out,
		Cached:   t.Cached + o.Cached,
		Cost:     t.Cost + o.Cost,
		Priced:   t.Priced || o.Priced,
		Requests: t.Requests + o.Requests,
		Plan:     t.Plan + o.Plan,
	}
}

func (t *Totals) add(e Entry) {
	var plan int64
	if e.Provider == PlanProvider {
		plan = e.In + e.Out
	}
	*t = t.Plus(Totals{In: e.In, Out: e.Out, Cached: e.Cached, Cost: e.Cost, Priced: e.Priced, Requests: e.Requests, Plan: plan})
}

// Ledger is the session's spend, accumulated in first-use order. Sub-agents
// run on their own goroutines, so every method is safe to call from any of
// them.
type Ledger struct {
	mu      sync.Mutex
	prices  *pricing.Table
	entries []Entry
	budget  Budget
	warned  bool
	// caches is each endpoint's cache reading, and lease the lease the
	// session's requests ask for (cache.go). They outlive Reset: a reading
	// is about the endpoint, and /clear starts the accounting over, not
	// what has been learned about where it is sent.
	caches []CacheReading
	lease  string
	// prev, expiries and leases are what the lifetime measurement keeps
	// (lifetime.go): each origin's last request, the expiries seen, and the
	// lease counterfactual's running figures.
	prev     map[Origin]prevRequest
	expiries []CacheExpiry
	taken    int
	leases   map[string]*leaseCounter
	// now is the clock a reading is stamped with; nil is time.Now.
	now func() time.Time
}

func New(prices *pricing.Table) *Ledger { return &Ledger{prices: prices} }

// Budget is expressed in whole cents so configuration never turns a decimal
// display value into a different hard limit. Zero leaves that rail disabled.
// A warning is advisory; a cap refuses the request after the recorded spend
// has reached it.
type Budget struct {
	WarningCents int64
	CapCents     int64
}

func (b Budget) warning() float64 { return float64(b.WarningCents) / 100 }
func (b Budget) cap() float64     { return float64(b.CapCents) / 100 }

// SetBudget changes the rails for future requests. It is separate from New
// because a session learns its resolved configuration after opening its
// ledger, before it hands the provider to any requester.
func (l *Ledger) SetBudget(b Budget) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.budget = b
	l.warned = false
}

// Warning reports the first configured warning once priced spend has crossed
// it. The state is held on the ledger so every surface sees the same warning
// even when a sub-agent was the request that caused it.
func (l *Ledger) Warning() (Totals, bool) {
	if l == nil {
		return Totals{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.warned {
		return Totals{}, false
	}
	return l.totalLocked(), true
}

// AllowRequest is the hard rail at the one seam all model calls share. A cap
// cannot reserve an unknown future response, but it does prevent every later
// request after the priced total has crossed the configured amount.
func (l *Ledger) AllowRequest() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.totalLocked()
	if l.budget.CapCents > 0 && t.Priced && t.Cost >= l.budget.cap() {
		return &CapError{Cap: l.budget.cap(), Spent: t.Cost}
	}
	return nil
}

// CapError is a request the cap refused, with the ledger's two figures on
// it. It is a type rather than a sentence because a backlog run ends a step
// on it — as blocked, with the figures as its evidence — where every other
// failed request is a turn that broke.
type CapError struct {
	Cap, Spent float64
}

func (e *CapError) Error() string {
	return fmt.Sprintf("LLM cost cap of $%.2f reached (spent $%.2f)", e.Cap, e.Spent)
}

// capFormat is the refusal as ReadCap parses it back, beside the place it is
// written, because the unattended runner meets the refusal as a
// stage process's error text and not as a value it can match on.
const capFormat = "LLM cost cap of $%f reached (spent $%f)"

// AsCap is the refusal inside err, where a request was refused at the cap.
func AsCap(err error) (*CapError, bool) {
	var c *CapError
	if errors.As(err, &c) {
		return c, true
	}
	return nil, false
}

// ReadCap finds the refusal in an error's text, however it was wrapped on
// the way out of the process that met it.
func ReadCap(text string) (*CapError, bool) {
	i := strings.Index(text, "LLM cost cap of $")
	if i < 0 {
		return nil, false
	}
	var c CapError
	if _, err := fmt.Sscanf(text[i:], capFormat, &c.Cap, &c.Spent); err != nil {
		return nil, false
	}
	return &c, true
}

// Record folds one request's usage into the ledger, pricing it against the
// model that actually answered rather than whichever model the session
// happens to be on when someone asks.
func (l *Ledger) Record(o Origin, model string, u llm.Usage) {
	l.RecordOn("", o, model, u)
}

// PlanProvider is the provider whose requests are paid for by a plan and not
// billed: the ledger counts their tokens and never prices them, so no surface
// shows a dollar figure nobody was charged.
// See docs/capabilities/chatgpt-plan.md#what-it-costs.
const PlanProvider = "chatgpt"

// RecordOn is Record for a request the named provider answered.
func (l *Ledger) RecordOn(providerName string, o Origin, model string, u llm.Usage) {
	if l == nil {
		return
	}
	if o.Source == "" {
		o.Source = SourceUnattributed
	}
	in, out := int64(u.PromptTokens), int64(u.CompletionTokens)
	cached, created := int64(u.CachedTokens), int64(u.CacheCreationTokens)

	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(o, model, providerName)
	e.In += in
	e.Out += out
	e.Cached += cached
	e.Requests++
	if l.prices != nil && model != "" && providerName != PlanProvider {
		// The input is charged in three parts, because that is how it is
		// billed: what a session saves by caching its prompt has to show up
		// here or the saving is invisible and the total is wrong.
		// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
		if cost, found := l.prices.Price(model, in, out, cached, created); found {
			e.Cost += cost
			e.Priced = true
		}
	}
	if l.budget.WarningCents > 0 && !l.warned {
		t := l.totalLocked()
		l.warned = t.Priced && t.Cost >= l.budget.warning()
	}
}

// entry returns the running total for an origin and model, creating it in
// first-use order. The caller holds the lock.
func (l *Ledger) entry(o Origin, model, providerName string) *Entry {
	for i := range l.entries {
		if l.entries[i].Origin == o && l.entries[i].Model == model && l.entries[i].Provider == providerName {
			return &l.entries[i]
		}
	}
	l.entries = append(l.entries, Entry{Origin: o, Model: model, Provider: providerName})
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
	return l.totalWhereLocked(keep)
}

func (l *Ledger) totalLocked() Totals { return l.totalWhereLocked(func(Entry) bool { return true }) }

func (l *Ledger) totalWhereLocked(keep func(Entry) bool) Totals {
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
	l.warned = false
}
