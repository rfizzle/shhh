package storage

import (
	"reflect"
	"testing"
	"time"
)

// A row adds what each writer learned, keeps the latest request's figures,
// the union of the fields the endpoint sent, the widest survival and the
// narrowest expiry; another lease of the same model is another row.
func TestStore_CacheReadingUpsertKeepsTheBound(t *testing.T) {
	db := openTestDB(t)
	t0 := time.Date(2026, 10, 10, 14, 2, 0, 0, time.UTC)
	key := CacheReading{Provider: "gateway", Endpoint: "default", Model: "claude-sonnet-5-5", Lease: "1h"}

	first := key
	first.Requests, first.WithReads, first.WithWrites = 3, 2, 3
	first.LastAt, first.LastPct, first.Confirmed = t0, 95, "1h"
	first.SurvivedMax, first.SurvivedN = 48*time.Minute, 6
	first.Reports = []string{"cache_read_input_tokens"}
	if err := db.UpsertCacheReading(first, t0); err != nil {
		t.Fatal(err)
	}

	second := key
	second.Requests, second.WithReads = 2, 1
	second.LastAt, second.LastPct = t0.Add(time.Minute), 40
	second.SurvivedMax, second.SurvivedN = 12*time.Minute, 1
	second.ExpiredMin, second.ExpiredN = 72*time.Minute, 1
	second.Reports = []string{"cache_creation_input_tokens", "cache_read_input_tokens"}
	if err := db.UpsertCacheReading(second, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// A writer that lags puts nothing older back, and an expiry it measured
	// narrows the bound.
	late := key
	late.Requests = 1
	late.LastAt, late.LastPct = t0.Add(-time.Hour), 3
	late.ExpiredMin, late.ExpiredN = 70*time.Minute, 1
	if err := db.UpsertCacheReading(late, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	other := key
	other.Lease, other.Requests = "5m", 3
	if err := db.UpsertCacheReading(other, t0); err != nil {
		t.Fatal(err)
	}

	got, err := db.CacheReadings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows %+v", got)
	}
	r := got[0]
	want := key
	want.Requests, want.WithReads, want.WithWrites = 6, 3, 3
	want.LastAt, want.LastPct, want.Confirmed = t0.Add(time.Minute), 40, "1h"
	want.SurvivedMax, want.SurvivedN = 48*time.Minute, 7
	want.ExpiredMin, want.ExpiredN = 70*time.Minute, 2
	want.Reports = []string{"cache_creation_input_tokens", "cache_read_input_tokens"}
	want.UpdatedAt = t0.Add(2 * time.Minute)
	if !reflect.DeepEqual(r, want) {
		t.Errorf("row\n got %+v\nwant %+v", r, want)
	}
	if got[1].Lease != "5m" || got[1].Requests != 3 || got[1].Reports != nil || !got[1].LastAt.IsZero() {
		t.Errorf("the other lease's row %+v", got[1])
	}
}
