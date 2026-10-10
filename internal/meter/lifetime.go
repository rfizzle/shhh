package meter

// How long the prompt cache stays alive, and what the lease costs. The
// lifetime is measured from the idle gaps a session already has, never by
// sending a request to find out: two requests on one origin whose earlier
// messages are a prefix of the later ones, with a gap between them, either
// read the cache the first wrote (it survived the gap) or wrote the prompt
// over again (it expired within it). The same observations price the lease
// the other way round, so a person choosing between five minutes and an hour
// sees what the choice cost.
// See docs/capabilities/providers.md#the-session-measures-how-long-the-cache-lives.

import (
	"fmt"
	"slices"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// survivedShare and expiredShare are the parts of the previous request's
	// cached and written tokens that a read must reach to say the cache
	// survived, and stay under to say it expired; between them is a partial
	// hit, which says neither. The allowance below one is the rolling mark's
	// tail, which is rewritten every round while the head and the body are
	// read (cache.go).
	survivedShare = 0.8
	expiredShare  = 0.2
	// idleFloor is the shortest gap that is evidence. A tool round follows
	// its predecessor in seconds, and counting those would drown the few
	// real pauses in observations that say nothing about a lifetime.
	idleFloor = time.Minute
)

// The multipliers of the base input price, from the Messages API's pricing:
// a five-minute write, an hour's write, and a read. The ledger prices the
// counterfactual with them rather than with the table's own cache rates so
// that both leases are priced by one rule from one base.
// See docs/capabilities/providers.md#the-session-measures-how-long-the-cache-lives.
const (
	write5m = 1.25
	write1h = 2.0
	readMul = 0.1
)

// An idle compaction is priced as a summary request over the warm prefix:
// the prefix read, a summary of idleSummaryTokens written out, and the
// rebuilt conversation, idleKeepShare of the old one, written on the next
// turn.
const (
	idleSummaryTokens = 1000
	idleKeepShare     = 0.3
)

// GapRequest is what the gate holds of a request until its response says
// what the cache did with it: when it started, and the messages and tool
// names it carried, which are what the cache's prefix is made of.
type GapRequest struct {
	start    time.Time
	messages []provider.Message
	tools    []string
}

// BeginGap seals a request at the moment it is sent.
func (l *Ledger) BeginGap(messages []provider.Message, opts provider.CompletionOpts) GapRequest {
	r := GapRequest{start: l.clock(), messages: slices.Clone(messages)}
	for _, t := range opts.Tools {
		r.tools = append(r.tools, t.Name)
	}
	return r
}

// prevRequest is the last request an origin made that answered.
type prevRequest struct {
	GapRequest
	provider, model string
	// total is what the request's response said the cache held for it: what
	// it read plus what it wrote.
	total int
}

// samePrefix reports whether the earlier request's messages are the head of
// this one's, and its tools the same. A trim, a compaction, a rewrite of the
// workspace, a model switch and a dropped thinking block each change the
// bytes inside the prefix, and would read as an expiry if compared.
func samePrefix(prev, next GapRequest) bool {
	if len(prev.messages) > len(next.messages) || !slices.Equal(prev.tools, next.tools) {
		return false
	}
	for i, a := range prev.messages {
		b := next.messages[i]
		if a.Role != b.Role || a.Content != b.Content || a.ToolCallID != b.ToolCallID ||
			len(a.ToolCalls) != len(b.ToolCalls) || len(a.Attachments) != len(b.Attachments) ||
			len(a.Reasoning) != len(b.Reasoning) {
			return false
		}
		for j := range a.ToolCalls {
			if a.ToolCalls[j].ID != b.ToolCalls[j].ID {
				return false
			}
		}
	}
	return true
}

// CacheExpiry is one expiry the session saw: the cache was gone when a
// request came back after Gap, and Rewritten tokens were written again.
type CacheExpiry struct {
	Origin Origin
	Model  string
	Lease  string
	Gap    time.Duration
	// Rewritten is the part of the earlier prefix the request wrote again,
	// at Mult times the input price for Cost, and CompactCost what an idle
	// compaction would have cost to have the next turn on a smaller prefix.
	// None of the costs are known where the model has no price (Priced).
	Rewritten   int
	Mult        float64
	Cost        float64
	CompactCost float64
	Priced      bool
}

// LeaseCost is what one lease cost in cache writes over the session, and
// what the other lease would have cost for the same writes and the pauses
// the session had.
type LeaseCost struct {
	Lease string
	// Writes is what the session's cache writes cost at Lease. Other is
	// what the writes, and the reads they displace, would have cost on
	// OtherLease. Pauses is the count that tipped it: gaps past five
	// minutes that the hour carried the cache across, or expiries the hour
	// would have read through.
	Writes, Other float64
	OtherLease    string
	Pauses        int
}

// leaseCounter is the running figures behind a LeaseCost.
type leaseCounter struct {
	writes, delta float64
	pauses        int
}

func leaseMultiplier(lease string) float64 {
	switch lease {
	case string(provider.CacheTTL1h):
		return write1h
	case string(provider.CacheTTL5m):
		return write5m
	}
	return 1
}

// leaseDuration is how long a lease says it lives, or zero.
func leaseDuration(lease string) time.Duration {
	switch lease {
	case string(provider.CacheTTL1h):
		return time.Hour
	case string(provider.CacheTTL5m):
		return 5 * time.Minute
	}
	return 0
}

func (l *Ledger) clock() time.Time {
	if l != nil && l.now != nil {
		return l.now()
	}
	return time.Now()
}

// cacheKey is the key a response's reading is kept under. The caller holds
// the lock.
func (l *Ledger) cacheKey(providerName, model string, u provider.Usage) CacheKey {
	key := CacheKey{Provider: providerName, Model: model}
	if provider.TakesCacheLease(model) || u.Reports&provider.FieldCacheLease != 0 {
		// A lease that will not parse went out as the default; the
		// provider package says so where it is sent.
		lease, _ := provider.ParseCacheTTL(l.lease)
		key.Lease = string(lease)
	}
	return key
}

// ObserveGap folds one response into the lifetime reading and the lease
// counterfactual: the cache's behaviour across the gap since the origin's
// previous request, where that gap is evidence. It is called after
// ObserveCache for the same response.
func (l *Ledger) ObserveGap(o Origin, providerName, model string, req GapRequest, u provider.Usage) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, had := l.prev[o]
	if l.prev == nil {
		l.prev = map[Origin]prevRequest{}
	}
	l.prev[o] = prevRequest{GapRequest: req, provider: providerName, model: model,
		total: u.CachedTokens + u.CacheCreationTokens}

	key := l.cacheKey(providerName, model, u)
	var inPrice, outPrice float64
	if l.prices != nil && model != "" {
		if p, ok := l.prices.Entry(model); ok {
			inPrice, outPrice = p.InputCostPerToken, p.OutputCostPerToken
		}
	}
	if key.Lease != "" && inPrice > 0 && u.CacheCreationTokens > 0 {
		// Every write is dearer on the hour and cheaper on five minutes.
		c := l.leaseCounter(key.Lease)
		c.writes += float64(u.CacheCreationTokens) * leaseMultiplier(key.Lease) * inPrice
		c.delta += float64(u.CacheCreationTokens) * (leaseMultiplier(otherLease(key.Lease)) - leaseMultiplier(key.Lease)) * inPrice
	}
	if r := l.findReading(key); r != nil && req.start.After(r.LastStart) {
		r.LastStart = req.start
	}

	if !had || prev.provider != providerName || prev.model != model || prev.total == 0 || !samePrefix(prev.GapRequest, req) {
		return
	}
	gap := req.start.Sub(prev.start)
	if gap < idleFloor {
		return
	}
	cached, created := float64(u.CachedTokens), float64(u.CacheCreationTokens)
	switch {
	case cached >= survivedShare*float64(prev.total):
		r := l.cacheReading(key)
		r.SurvivedMax = max(r.SurvivedMax, gap)
		r.SurvivedN++
		// The hour carried the prefix across a pause that five minutes
		// would have rewritten: the read is worth the difference between
		// the five-minute write and a read.
		if key.Lease == string(provider.CacheTTL1h) && gap > 5*time.Minute && inPrice > 0 {
			c := l.leaseCounter(key.Lease)
			c.delta += min(cached, float64(prev.total)) * (write5m - readMul) * inPrice
			c.pauses++
		}
	case cached < expiredShare*float64(prev.total) && u.PromptTokens > 0 && created*2 >= float64(u.PromptTokens):
		r := l.cacheReading(key)
		if r.ExpiredMin == 0 || gap < r.ExpiredMin {
			r.ExpiredMin = gap
		}
		r.ExpiredN++
		// Only the earlier prefix was rewritten for the pause; what the
		// request added would have been written either way.
		rewritten := int(min(created, float64(prev.total)))
		ex := CacheExpiry{Origin: o, Model: model, Lease: key.Lease, Gap: gap,
			Rewritten: rewritten, Mult: leaseMultiplier(key.Lease), Priced: inPrice > 0}
		if ex.Priced {
			ex.Cost = float64(rewritten) * ex.Mult * inPrice
			ex.CompactCost = float64(rewritten)*readMul*inPrice +
				idleSummaryTokens*outPrice +
				float64(rewritten)*idleKeepShare*ex.Mult*inPrice
		}
		l.expiries = append(l.expiries, ex)
		// On five minutes, an hour would have read what was rewritten. The
		// per-write difference above already charged the hour's premium on
		// those tokens, so what is taken back is the hour's write less a
		// read.
		if key.Lease == string(provider.CacheTTL5m) && gap < time.Hour && inPrice > 0 {
			c := l.leaseCounter(key.Lease)
			c.delta -= float64(rewritten) * (write1h - readMul) * inPrice
			c.pauses++
		}
	}
}

// otherLease is the lease a counterfactual prices against.
func otherLease(lease string) string {
	if lease == string(provider.CacheTTL1h) {
		return string(provider.CacheTTL5m)
	}
	return string(provider.CacheTTL1h)
}

// leaseCounter returns the running figures for a lease. The caller holds the
// lock.
func (l *Ledger) leaseCounter(lease string) *leaseCounter {
	if l.leases == nil {
		l.leases = map[string]*leaseCounter{}
	}
	c := l.leases[lease]
	if c == nil {
		c = &leaseCounter{}
		l.leases[lease] = c
	}
	return c
}

// findReading is the reading under key, or nil. The caller holds the lock.
func (l *Ledger) findReading(key CacheKey) *CacheReading {
	for i := range l.caches {
		if l.caches[i].CacheKey == key {
			return &l.caches[i]
		}
	}
	return nil
}

// LeaseCosts is what each lease the session wrote under cost, against the
// other, for the leases that wrote anything.
func (l *Ledger) LeaseCosts() []LeaseCost {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LeaseCost
	for _, lease := range []string{string(provider.CacheTTL5m), string(provider.CacheTTL1h)} {
		c := l.leases[lease]
		if c == nil || c.writes <= 0 {
			continue
		}
		out = append(out, LeaseCost{Lease: lease, Writes: c.writes, Other: c.writes + c.delta,
			OtherLease: otherLease(lease), Pauses: c.pauses})
	}
	return out
}

// Expiries is every expiry the session has seen, in the order seen.
func (l *Ledger) Expiries() []CacheExpiry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.expiries)
}

// TakeExpiries is the expiries seen since it was last called, each handed
// out once: the surface that says so to the person calls it as responses
// arrive, and a /clear that starts the accounting over does not bring the
// old ones back.
func (l *Ledger) TakeExpiries() []CacheExpiry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := slices.Clone(l.expiries[l.taken:])
	l.taken = len(l.expiries)
	return out
}

// ExpiredUnderLease reports an expiry seen at a gap shorter than the lease
// the requests asked for: the gateway is not honouring it.
func (r CacheReading) ExpiredUnderLease() bool {
	d := leaseDuration(r.Lease)
	return d > 0 && r.ExpiredMin > 0 && r.ExpiredMin < d
}

// EffectiveLifetime is how long the cache is taken to live: the lease the
// requests asked for, while no expiry has been seen inside it, and otherwise
// the longest gap it survived, once that has been seen twice and is not
// contradicted by an expiry at a shorter gap. Zero is a lifetime nobody
// knows, and whatever acts on the lifetime does nothing with it.
func (r CacheReading) EffectiveLifetime() time.Duration {
	if d := leaseDuration(r.Lease); d > 0 && !r.ExpiredUnderLease() {
		return d
	}
	if r.SurvivedN >= 2 && (r.ExpiredN == 0 || r.ExpiredMin > r.SurvivedMax) {
		return r.SurvivedMax
	}
	return 0
}

// GapLabel is a gap in the whole minutes the lifetime is quoted in, and in
// seconds below one.
func GapLabel(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
}
