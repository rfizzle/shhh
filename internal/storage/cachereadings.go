package storage

// What each endpoint's responses have said about its prompt cache, across
// sessions. A session reads it off the requests it was sending anyway and
// writes what it learned here at a turn's close and at its exit, so that
// `shhh providers` and `shhh doctor` can say whether a gateway's cache works
// with no session open
// (docs/capabilities/providers.md#the-session-says-whether-the-cache-worked).

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// CacheReading is one endpoint's row.
type CacheReading struct {
	Provider, Endpoint, Model, Lease string
	// Requests, WithReads and WithWrites are counts: an upsert adds to them,
	// so a writer hands in what it learned since it last wrote.
	Requests, WithReads, WithWrites int
	// LastAt is when the latest request answered, and LastPct the share of
	// its input read from the cache. Confirmed is the lease the provider
	// said it wrote for, the latest that said.
	LastAt    time.Time
	LastPct   int
	Confirmed string
	// SurvivedMax is the longest idle gap a cache was seen to survive and
	// ExpiredMin the shortest it was seen to expire across, each with how
	// many gaps were seen; zero is a bound nothing has measured. An upsert
	// keeps the wider of the two survivals and the narrower expiry.
	SurvivedMax time.Duration
	SurvivedN   int
	ExpiredMin  time.Duration
	ExpiredN    int
	// Reports is the cache field names the endpoint has sent, in order; an
	// upsert keeps the union.
	Reports []string
	// UpdatedAt is when the row was last written.
	UpdatedAt time.Time
}

// UpsertCacheReading folds r into its endpoint's row, at the moment at.
func (db *DB) UpsertCacheReading(r CacheReading, at time.Time) error {
	return retryBusy(func() error {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		var held string
		err = tx.QueryRow(`SELECT reports FROM cache_readings
			WHERE provider = ? AND endpoint = ? AND model = ? AND lease = ?`,
			r.Provider, r.Endpoint, r.Model, r.Lease).Scan(&held)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		reports := unionFields(held, r.Reports)

		last := ""
		if !r.LastAt.IsZero() {
			last = r.LastAt.UTC().Format(flakeTime)
		}
		// The latest request's figures replace the row's only when they are
		// later than the row's own, so a writer that lags another cannot
		// put an older reading back.
		if _, err := tx.Exec(`INSERT INTO cache_readings (provider, endpoint, model, lease,
				requests, with_reads, with_writes, last_at, last_pct, confirmed,
				survived_max_s, survived_n, expired_min_s, expired_n, reports, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(provider, endpoint, model, lease) DO UPDATE SET
				requests = requests + excluded.requests,
				with_reads = with_reads + excluded.with_reads,
				with_writes = with_writes + excluded.with_writes,
				last_pct = CASE WHEN excluded.last_at > last_at THEN excluded.last_pct ELSE last_pct END,
				last_at = MAX(last_at, excluded.last_at),
				confirmed = CASE WHEN excluded.confirmed != '' THEN excluded.confirmed ELSE confirmed END,
				survived_max_s = MAX(survived_max_s, excluded.survived_max_s),
				survived_n = survived_n + excluded.survived_n,
				expired_min_s = CASE
					WHEN expired_min_s = 0 THEN excluded.expired_min_s
					WHEN excluded.expired_min_s = 0 THEN expired_min_s
					ELSE MIN(expired_min_s, excluded.expired_min_s) END,
				expired_n = expired_n + excluded.expired_n,
				reports = excluded.reports,
				updated_at = excluded.updated_at`,
			r.Provider, r.Endpoint, r.Model, r.Lease,
			r.Requests, r.WithReads, r.WithWrites, last, r.LastPct, r.Confirmed,
			int64(r.SurvivedMax/time.Second), r.SurvivedN, int64(r.ExpiredMin/time.Second), r.ExpiredN,
			reports, at.UTC().Format(flakeTime)); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// unionFields is the comma-separated set held, with names added, sorted.
func unionFields(held string, names []string) string {
	set := map[string]bool{}
	for _, n := range strings.Split(held, ",") {
		if n != "" {
			set[n] = true
		}
	}
	for _, n := range names {
		if n != "" {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// CacheReadings is every endpoint's row, the most recently answered first.
func (db *DB) CacheReadings() ([]CacheReading, error) {
	rows, err := db.sql.Query(`SELECT provider, endpoint, model, lease, requests, with_reads,
			with_writes, last_at, last_pct, confirmed, survived_max_s, survived_n,
			expired_min_s, expired_n, reports, updated_at
		FROM cache_readings ORDER BY last_at DESC, provider, endpoint, model, lease`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CacheReading
	for rows.Next() {
		var r CacheReading
		var last, reports, updated string
		var survived, expired int64
		if err := rows.Scan(&r.Provider, &r.Endpoint, &r.Model, &r.Lease, &r.Requests, &r.WithReads,
			&r.WithWrites, &last, &r.LastPct, &r.Confirmed, &survived, &r.SurvivedN,
			&expired, &r.ExpiredN, &reports, &updated); err != nil {
			return nil, err
		}
		if last != "" {
			r.LastAt = parseFlakeTime(last)
		}
		r.UpdatedAt = parseFlakeTime(updated)
		r.SurvivedMax, r.ExpiredMin = time.Duration(survived)*time.Second, time.Duration(expired)*time.Second
		if reports != "" {
			r.Reports = strings.Split(reports, ",")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
