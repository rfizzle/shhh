package cli

// The prompt cache's reading, kept past the session that took it. The ledger
// reads each response's cache figures at the gate; this writes what it learned
// to the store at a turn's close and at the session's end, and reads it back
// for the two surfaces that run with no session open: `shhh providers` and
// `shhh doctor`.
// See docs/capabilities/providers.md#the-session-says-whether-the-cache-worked.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/llm/cache"
	"github.com/rfizzle/llm/capability"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/storage"
)

// cacheKeeper writes a ledger's cache readings to the store. The store's
// counts are sums across sessions, so each write hands in only what was
// learned since the last one; the readings it last wrote are what it
// subtracts.
type cacheKeeper struct {
	db     *storage.DB
	ledger *meter.Ledger
	now    func() time.Time

	mu      sync.Mutex
	written map[meter.CacheKey]meter.CacheReading
}

func newCacheKeeper(db *storage.DB, ledger *meter.Ledger) *cacheKeeper {
	return &cacheKeeper{db: db, ledger: ledger, now: time.Now, written: map[meter.CacheKey]meter.CacheReading{}}
}

// keep writes what each endpoint's reading gained since the last write. It
// is safe to call from the autosave's goroutine and the session's own at
// once, and a write that fails is tried again with the next one, since
// what it would have added is still unwritten.
func (k *cacheKeeper) keep() {
	if k == nil || k.db == nil || k.ledger == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, r := range k.ledger.CacheReadings() {
		prev := k.written[r.CacheKey]
		if r.Requests == prev.Requests && r.SurvivedN == prev.SurvivedN && r.ExpiredN == prev.ExpiredN {
			continue
		}
		row := storage.CacheReading{
			Provider:   r.Provider,
			Endpoint:   cacheEndpoint(r.Provider, r.Model),
			Model:      r.Model,
			Lease:      r.Lease,
			Requests:   r.Requests - prev.Requests,
			WithReads:  r.WithReads - prev.WithReads,
			WithWrites: r.WithWrites - prev.WithWrites,
			LastAt:     r.LastAt,
			LastPct:    r.LastShare(),
			Confirmed:  r.Confirmed,
			// The bounds are the session's own, which the store narrows or
			// widens its row by; the counts are what the session added.
			SurvivedMax: r.SurvivedMax,
			SurvivedN:   r.SurvivedN - prev.SurvivedN,
			ExpiredMin:  r.ExpiredMin,
			ExpiredN:    r.ExpiredN - prev.ExpiredN,
			Reports:     r.Reports.Names(),
		}
		if err := k.db.UpsertCacheReading(row, k.now()); err != nil {
			continue
		}
		k.written[r.CacheKey] = r
	}
}

// cacheEndpoint names the endpoint inside a provider that a model is sent
// to: a gateway profile's route for it, and a built-in provider's own name,
// since a built-in has one.
func cacheEndpoint(providerName, model string) string {
	for _, p := range profile.Loaded() {
		if p.Name == providerName {
			return p.Route(model).Label
		}
	}
	return providerName
}

// storedCacheState is a stored row's verdict, by the ledger's own rule.
func storedCacheState(r storage.CacheReading) meter.CacheState {
	return meter.CacheReading{
		Requests: r.Requests, WithReads: r.WithReads, WithWrites: r.WithWrites,
		OffBy: capability.PromptCacheOff(r.Provider, r.Model),
	}.State()
}

// cacheRow is one stored reading as a `shhh providers` row: the verdict, when
// the endpoint last answered, the share of that request read from the cache,
// and which cache fields the endpoint sends — the provider's own side of it,
// so a gateway that strips them says so.
func cacheRow(r storage.CacheReading) report.Row {
	row := report.Row{State: report.Skip, Name: "cache", Subject: string(storedCacheState(r))}
	var facts []string
	switch row.Subject {
	case string(meter.CacheWorks):
		row.State = report.Pass
	case string(meter.CacheNotSeen):
		row.State = report.Warn
		facts = append(facts, fmt.Sprintf("in %d requests", r.Requests))
	case string(meter.CacheOff):
		row.Subject = "off (profile " + strings.Join(capability.PromptCacheOff(r.Provider, r.Model), ", ") + ")"
	}
	if !r.LastAt.IsZero() {
		facts = append(facts, "last "+r.LastAt.Local().Format("2006-01-02 15:04"))
	}
	if r.WithReads+r.WithWrites > 0 {
		facts = append(facts, fmt.Sprintf("%d%% read", r.LastPct))
	}
	// In the order the wire names them, read before written, rather than
	// the order the store keeps the set in.
	reports := cache.FieldsNamed(r.Reports).Names()
	if len(reports) == 0 {
		reports = r.Reports
	}
	if len(reports) > 0 {
		facts = append(facts, "reports "+strings.Join(reports, ", "))
	} else {
		facts = append(facts, "reports no cache fields")
	}
	if lifetime := storedLifetime(r); lifetime != "" {
		facts = append(facts, lifetime)
	}
	row.Detail = strings.Join(facts, " · ")
	row.Outcome = r.Model
	if r.Lease != "" {
		row.Outcome += " · lease " + r.Lease
	}
	return row
}

// storedLifetime is what the store holds of the cache's measured lifetime,
// `survived 48m, expired by 72m`, or nothing where no gap has been seen.
func storedLifetime(r storage.CacheReading) string {
	var parts []string
	if r.SurvivedN > 0 {
		parts = append(parts, "survived "+meter.GapLabel(r.SurvivedMax))
	}
	if r.ExpiredN > 0 {
		parts = append(parts, "expired by "+meter.GapLabel(r.ExpiredMin))
	}
	return strings.Join(parts, ", ")
}

// cacheRowsFor is the stored readings under one provider and endpoint.
func cacheRowsFor(readings []storage.CacheReading, providerName, endpoint string) []report.Row {
	var rows []report.Row
	for _, r := range readings {
		if r.Provider == providerName && r.Endpoint == endpoint {
			rows = append(rows, cacheRow(r))
		}
	}
	return rows
}

// storedCacheReadings is what the store holds, or nothing where it will not
// open: a listing that cannot read the store leaves the rows out rather than
// failing. A machine with no store yet has no readings, and a listing is not
// what should create one.
func storedCacheReadings() []storage.CacheReading {
	dir, err := storage.Dir()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "shhh.db")); err != nil {
		return nil
	}
	db, err := openStore()
	if err != nil {
		return nil
	}
	defer db.Close()
	readings, err := db.CacheReadings()
	if err != nil {
		return nil
	}
	return readings
}

// cacheNote is what the doctor's model row adds about the model's cache: the
// declaration first, then the latest stored reading with a verdict for it on
// the provider it resolves to. A model with neither adds nothing.
func cacheNote(readings []storage.CacheReading, providerName, model string) string {
	if model == "" {
		return ""
	}
	if by := capability.PromptCacheOff(providerName, model); len(by) > 0 {
		return "cache off (profile " + strings.Join(by, ", ") + ")"
	}
	for _, r := range readings {
		if r.Provider != providerName || r.Model != model {
			continue
		}
		switch storedCacheState(r) {
		case meter.CacheWorks:
			return fmt.Sprintf("cache works (%d%% read last request)", r.LastPct)
		case meter.CacheNotSeen:
			return fmt.Sprintf("cache not seen in %d requests", r.Requests)
		}
	}
	return ""
}
