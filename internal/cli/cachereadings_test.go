package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/cli/doctor"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func cacheTestStore(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "shhh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The session writes what it learned at each turn's close and at its end;
// `shhh providers` reads it back under the endpoint it was taken on, with
// what the endpoint itself reports, and says so where it reports nothing.
func TestProviders_CacheRowReadsTheStore(t *testing.T) {
	db := cacheTestStore(t)
	at := time.Date(2026, 10, 10, 14, 2, 0, 0, time.UTC)
	ledger := meter.New(nil)
	ledger.SetCacheLease("1h")
	gw := profile.Profile{Name: "corp-gw", API: "openai-chat", BaseURL: "https://gw.example.com/v1"}
	profile.Register([]profile.Profile{gw})
	t.Cleanup(func() { profile.Register(nil) })

	keeper := newCacheKeeper(db, ledger)
	keeper.now = func() time.Time { return at }
	hit := llm.Usage{PromptTokens: 20_000, CachedTokens: 19_000, CacheCreationTokens: 500,
		Reports: llm.FieldCachedTokens | llm.FieldCacheCreation}
	ledger.ObserveCache("corp-gw", "anthropic/claude-sonnet-5-5", hit)
	keeper.keep()
	keeper.keep() // nothing new: nothing added twice
	for range 3 {
		ledger.ObserveCache("vllm", "qwen3", llm.Usage{PromptTokens: 20_000})
	}
	keeper.keep()

	stored, err := db.CacheReadings()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored %+v, %v", stored, err)
	}
	r := providersReport([]string{"corp-gw", "vllm"}, nil, []profile.Profile{gw}, nil, "", stored)
	out := r.Render(200)
	last := stored[0].LastAt.Local().Format("2006-01-02 15:04")
	for _, want := range []string{
		"works · last " + last + " · 95% read · reports cache_creation_input_tokens, cached_tokens",
		"[anthropic/claude-sonnet-5-5 · lease 1h]",
		"not seen · in 3 requests · last " + last + " · reports no cache fields",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing lacks %q:\n%s", want, out)
		}
	}
	if stored[0].Requests+stored[1].Requests != 4 {
		t.Errorf("a second write with nothing new added to the counts: %+v", stored)
	}
}

// The doctor's model row says whether the cache works on the model it
// resolves to, from the store, and a profile's declaration first.
func TestDoctorModel_NamesTheCacheReading(t *testing.T) {
	readings := []storage.CacheReading{
		{Provider: "gateway", Endpoint: "default", Model: "claude-sonnet-5-5", Lease: "1h", Requests: 9, WithReads: 8, LastPct: 95},
		{Provider: "vllm-local", Endpoint: "vllm-local", Model: "qwen3", Requests: 14},
	}
	for _, tc := range []struct{ prov, model, want string }{
		{"gateway", "claude-sonnet-5-5", "anthropic · cache works (95% read last request)"},
		{"vllm-local", "qwen3", "anthropic · cache not seen in 14 requests"},
		{"openai", "gpt-5", "anthropic"},
	} {
		f := withCacheNote(doctor.Finding{Detail: "anthropic"}, readings, tc.prov, tc.model)
		if f.Detail != tc.want {
			t.Errorf("%s/%s: %q, want %q", tc.prov, tc.model, f.Detail, tc.want)
		}
	}

	llm.RegisterDeclarations("gateway", map[string]llm.Declared{
		"claude-sonnet-5-5": {Model: map[string]any{llm.KeyPromptCache: false}},
	})
	t.Cleanup(func() { llm.RegisterDeclarations("gateway", nil) })
	if f := withCacheNote(doctor.Finding{}, readings, "gateway", "claude-sonnet-5-5"); f.Detail != "cache off (profile gateway)" {
		t.Errorf("a declared off: %q", f.Detail)
	}
}

// `shhh providers` says on the cache row what has been measured of the
// lifetime, and says nothing where no gap has been seen.
func TestProviders_CacheRowCarriesTheMeasuredLifetime(t *testing.T) {
	row := cacheRow(storage.CacheReading{Provider: "gateway", Model: "claude-sonnet-5-5", Lease: "1h",
		Requests: 9, WithReads: 8, LastPct: 95,
		SurvivedMax: 48 * time.Minute, SurvivedN: 6, ExpiredMin: 72 * time.Minute, ExpiredN: 1})
	if !strings.Contains(row.Detail, "survived 48m, expired by 72m") {
		t.Errorf("the cache row lacks the lifetime: %q", row.Detail)
	}
	quiet := cacheRow(storage.CacheReading{Provider: "gateway", Model: "m", Requests: 9, WithReads: 8})
	if strings.Contains(quiet.Detail, "survived") || strings.Contains(quiet.Detail, "expired") {
		t.Errorf("a row nothing was measured on says a lifetime: %q", quiet.Detail)
	}
}

// The doctor warns when compacting while idle is on and the cache has not
// been seen to work where the model resolves to: there it will never fire.
func TestDoctorModel_WarnsWhenIdleCompactionCannotFire(t *testing.T) {
	readings := []storage.CacheReading{
		{Provider: "gateway", Endpoint: "default", Model: "claude-sonnet-5-5", Lease: "1h", Requests: 9, WithReads: 8, LastPct: 95},
		{Provider: "vllm-local", Endpoint: "vllm-local", Model: "qwen3", Requests: 14},
	}
	var on config.Config
	on.Behavior.CompactWhenIdle = true
	ok := withIdleCompactWarning(doctor.Finding{Outcome: "ok"}, on, readings, "gateway", "claude-sonnet-5-5")
	if ok.State != components.DoctorPassed || ok.Outcome != "ok" {
		t.Errorf("a cache that works needs no warning: %+v", ok)
	}
	warned := withIdleCompactWarning(doctor.Finding{Outcome: "ok"}, on, readings, "vllm-local", "qwen3")
	if warned.State != components.DoctorWarned ||
		warned.Consequence != "compact_when_idle is on but the cache has not been seen on vllm-local; it will not fire" {
		t.Errorf("a cache not seen should warn: %+v", warned)
	}
	if none := withIdleCompactWarning(doctor.Finding{}, on, nil, "openai", "gpt-5"); none.State != components.DoctorWarned {
		t.Errorf("no reading at all should warn too: %+v", none)
	}
	if off := withIdleCompactWarning(doctor.Finding{}, config.Config{}, readings, "vllm-local", "qwen3"); off.State != components.DoctorPassed {
		t.Errorf("the switch off should say nothing: %+v", off)
	}
}
