package meter

// Whether the prompt cache worked. Every request already says so in its
// usage — what was read from the cache, what was written to it, and which
// fields the endpoint sent at all — so the reading is taken off the requests
// the session was sending anyway, at the seam that sees every one of them,
// and never by sending one to find out. It is kept per endpoint rather than
// per session because the cache is a property of the thing answering: a
// gateway that strips the marks, or drops the fields, does it on every
// request and to every session.
// See docs/capabilities/providers.md#the-session-says-whether-the-cache-worked.

import (
	"slices"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// CacheState is the reading's verdict.
type CacheState string

const (
	// CacheNoneYet is no verdict: too few requests long enough to have
	// been cached have gone out, and none of them reported a figure.
	CacheNoneYet CacheState = "none yet"
	// CacheNotSeen is cacheNotSeenAfter requests or more, each long enough
	// to be cached, and not one cache figure among them.
	CacheNotSeen CacheState = "not seen"
	// CacheWorks is at least one request that reported a read or a write.
	CacheWorks CacheState = "works"
	// CacheOff is a profile's declaration that the endpoint has no cache.
	// It outranks the figures: they are still priced, and the reading
	// still says what the person declared.
	CacheOff CacheState = "off"
)

// cacheNotSeenAfter is how many silent requests make a verdict. Not one,
// because the first request on a fresh prefix can legitimately report only
// a write, and an endpoint with no cache reports nothing on all of them —
// one request cannot tell those apart, and three can.
const cacheNotSeenAfter = 3

// cacheableFloor is the prompt length, in tokens, above which a request is
// evidence about the cache. It is the largest minimum any provider documents
// for a cacheable prefix (4,096 on Anthropic's and Gemini's strictest
// models), so a request above it could have been cached wherever it went; a
// lower floor per model would only bring the verdict a request or two
// sooner, and a wrong one is the thing to avoid.
const cacheableFloor = 4096

// CacheKey is where a cache lives: the endpoint that answered, by the name
// its provider goes by (a built-in's, or a gateway profile's), the model,
// and the lease the request asked for where the model takes one. The lease
// is part of the key because on the Messages API five minutes and an hour
// are two caches, and empty where no lease was asked for.
type CacheKey struct {
	Provider, Model, Lease string
}

// CacheReading is what one endpoint's responses have said about its cache.
type CacheReading struct {
	CacheKey
	// Requests is how many requests were evidence: long enough to have been
	// cached, or carrying a figure whatever their length. WithReads and
	// WithWrites are how many of them reported a read and a write.
	Requests, WithReads, WithWrites int
	// LastAt is when the latest of them answered, and LastIn, LastCached and
	// LastCreated are its input and the parts of it read from and written
	// to the cache.
	LastAt                          time.Time
	LastIn, LastCached, LastCreated int
	// Confirmed is the lease the provider says it wrote for, on the latest
	// request that wrote and said: "5m", "1h", or empty where nothing has
	// said. A write split across both is read as "5m", since a request asks
	// for one lease and any part of it written for five minutes is the part
	// the person did not ask for.
	Confirmed string
	// Reports is every cache field the endpoint has sent.
	Reports provider.CacheFields
	// SurvivedMax is the longest gap between two requests the cache was seen
	// to outlast and ExpiredMin the shortest it was seen to expire within,
	// each with how many gaps were seen; zero is a bound nothing has
	// measured (lifetime.go). LastStart is when the latest request was sent,
	// which is the cache's own clock: an idle cache is measured from there.
	SurvivedMax, ExpiredMin time.Duration
	SurvivedN, ExpiredN     int
	LastStart               time.Time
	// OffBy names the profile that declared the model has no prompt cache
	// on this endpoint: the endpoint's own, since a declaration about one
	// gateway's cache says nothing about another's.
	OffBy []string
}

// State is the verdict, the declaration first.
func (r CacheReading) State() CacheState {
	switch {
	case len(r.OffBy) > 0:
		return CacheOff
	case r.WithReads+r.WithWrites > 0:
		return CacheWorks
	case r.Requests >= cacheNotSeenAfter:
		return CacheNotSeen
	}
	return CacheNoneYet
}

// LastShare is the percentage of the latest request's input that was read
// from the cache.
func (r CacheReading) LastShare() int {
	if r.LastIn <= 0 {
		return 0
	}
	return min(r.LastCached*100/r.LastIn, 100)
}

// Downgraded reports a request that asked for the hour and was written for
// five minutes: the dearer write paid for the shorter cache.
func (r CacheReading) Downgraded() bool {
	return r.Lease == string(provider.CacheTTL1h) && r.Confirmed == string(provider.CacheTTL5m)
}

// SetCacheLease names the lease the session's requests ask for, so a
// reading can be kept under it.
func (l *Ledger) SetCacheLease(lease string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lease = lease
}

// SetClock replaces the clock readings and gaps are stamped with, for a test
// or a replayed session that needs to say how long a pause was.
func (l *Ledger) SetClock(now func() time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// ObserveCache folds one response's usage into its endpoint's reading.
func (l *Ledger) ObserveCache(providerName, model string, u provider.Usage) {
	if l == nil {
		return
	}
	cached, created := u.CachedTokens > 0, u.CacheCreationTokens > 0
	if u.PromptTokens < cacheableFloor && !cached && !created {
		return
	}
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.cacheReading(l.cacheKey(providerName, model, u))
	r.Requests++
	if cached {
		r.WithReads++
	}
	if created {
		r.WithWrites++
	}
	r.LastAt = now
	r.LastIn, r.LastCached, r.LastCreated = u.PromptTokens, u.CachedTokens, u.CacheCreationTokens
	switch {
	case u.CacheCreation5m > 0:
		r.Confirmed = string(provider.CacheTTL5m)
	case u.CacheCreation1h > 0:
		r.Confirmed = string(provider.CacheTTL1h)
	}
	r.Reports |= u.Reports
}

// cacheReading returns the reading under key, creating it in first-use
// order. The caller holds the lock.
func (l *Ledger) cacheReading(key CacheKey) *CacheReading {
	for i := range l.caches {
		if l.caches[i].CacheKey == key {
			return &l.caches[i]
		}
	}
	l.caches = append(l.caches, CacheReading{CacheKey: key})
	return &l.caches[len(l.caches)-1]
}

// CacheReadings is every endpoint's reading, in first-use order, each with
// its profile's declaration that the model has no cache, as it stands now.
func (l *Ledger) CacheReadings() []CacheReading {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	out := slices.Clone(l.caches)
	l.mu.Unlock()
	for i := range out {
		out[i].OffBy = provider.PromptCacheOff(out[i].Provider, out[i].Model)
	}
	return out
}

// CacheReadingFor is the reading for one endpoint and model, under whatever
// lease it was kept. A model with no reading yet is answered with the
// declaration alone, so a profile's "off" is said before the first reply.
//
// The name a surface knows the provider by can differ from the name the
// provider answers to — a configured endpoint behind the generic dialect
// goes by the name its configuration gave it — so where no reading is kept
// under the name asked, the latest reading for the model stands in: a
// session sends one model to one endpoint at a time.
func (l *Ledger) CacheReadingFor(providerName, model string) CacheReading {
	var latest *CacheReading
	readings := l.CacheReadings()
	for i, r := range readings {
		if r.Model != model {
			continue
		}
		if r.Provider == providerName {
			return r
		}
		if latest == nil || r.LastAt.After(latest.LastAt) {
			latest = &readings[i]
		}
	}
	if latest != nil {
		return *latest
	}
	return CacheReading{
		CacheKey: CacheKey{Provider: providerName, Model: model},
		OffBy:    provider.PromptCacheOff(providerName, model),
	}
}
