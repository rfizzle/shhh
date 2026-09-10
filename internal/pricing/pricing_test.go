package pricing

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCost_ExactMatch(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001},
	}}

	in, out, found := table.Cost("gpt-4o", 1000, 500)
	if !found {
		t.Fatal("expected to find gpt-4o")
	}
	if in != 0.0025 {
		t.Fatalf("input cost: want 0.0025, got %f", in)
	}
	if out != 0.005 {
		t.Fatalf("output cost: want 0.005, got %f", out)
	}
}

func TestCost_SuffixMatch(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{
		"openai/gpt-4o": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001},
	}}

	_, _, found := table.Cost("gpt-4o", 100, 50)
	if !found {
		t.Fatal("expected suffix match for gpt-4o")
	}
}

func TestCost_NotFound(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{}}

	_, _, found := table.Cost("nonexistent", 100, 50)
	if found {
		t.Fatal("expected not found")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	data := `{
		"gpt-4o": {"input_cost_per_token": 0.0000025, "output_cost_per_token": 0.00001},
		"gemini-2.5-flash": {"input_cost_per_token": 0.0000001, "output_cost_per_token": 0.0000004}
	}`
	must(t, os.WriteFile(path, []byte(data), 0o600))

	table, err := loadWithSnapshot(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, _, found := table.Cost("gpt-4o", 1, 1); !found {
		t.Error("expected to find gpt-4o")
	}
	if _, _, found := table.Cost("gemini-2.5-flash", 1, 1); !found {
		t.Error("expected to find gemini-2.5-flash")
	}
}

func TestShouldRefresh(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if !shouldRefresh(missing) {
		t.Error("expected refresh for missing file")
	}

	existing := filepath.Join(dir, "exists.json")
	must(t, os.WriteFile(existing, []byte("{}"), 0o600))
	if shouldRefresh(existing) {
		t.Error("expected no refresh for fresh file")
	}
}

func TestContextWindow(t *testing.T) {
	table := NewTable(map[string]ModelPricing{
		"gpt-4o":         {InputCostPerToken: 0.0000025, MaxInputTokens: 128000},
		"openai/gpt-4.1": {MaxInputTokens: 1047576},
		"no-window":      {InputCostPerToken: 0.000001},
	})

	if w, ok := table.ContextWindow("gpt-4o"); !ok || w != 128000 {
		t.Fatalf("exact match: want 128000, got %d (found=%v)", w, ok)
	}
	if w, ok := table.ContextWindow("gpt-4.1"); !ok || w != 1047576 {
		t.Fatalf("suffix match: want 1047576, got %d (found=%v)", w, ok)
	}
	if _, ok := table.ContextWindow("no-window"); ok {
		t.Fatal("a model without max_input_tokens should report no window")
	}
	if _, ok := table.ContextWindow("unknown"); ok {
		t.Fatal("an unknown model should report no window")
	}
}

func TestLoadFromFile_ContextWindowOnlyEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	// Free/local models carry a context window but zero cost; they must be
	// kept for ContextWindow while Cost still reports not-found.
	data := `{"ollama/llama3": {"max_input_tokens": 8192}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	table, err := loadWithSnapshot(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w, ok := table.ContextWindow("ollama/llama3"); !ok || w != 8192 {
		t.Fatalf("want window 8192, got %d (found=%v)", w, ok)
	}
	if _, _, found := table.Cost("ollama/llama3", 1, 1); found {
		t.Fatal("zero-cost entries should not report a cost")
	}
}

func TestSnapshot_CarriesReasoningFlags(t *testing.T) {
	table := Snapshot()
	if table.Len() == 0 {
		t.Fatal("the built-in snapshot is empty")
	}
	e, ok := table.Entry("claude-opus-5")
	if !ok {
		t.Fatal("snapshot should know claude-opus-5")
	}
	if !e.SupportsReasoning || !e.AdaptiveThinking || !e.XHighEffort || !e.MaxEffort {
		t.Errorf("claude-opus-5 flags = %+v", e)
	}
	if e.InputCostPerToken == 0 || e.MaxInputTokens == 0 || e.MaxOutputTokens == 0 {
		t.Errorf("claude-opus-5 should carry prices and windows, got %+v", e)
	}
	if e, ok := table.Entry("gpt-4o"); !ok || e.SupportsReasoning {
		t.Errorf("gpt-4o has no reasoning knob, got %+v (found=%v)", e, ok)
	}
}

func TestSnapshot_GPT56CarriesItsMaxEffort(t *testing.T) {
	table := Snapshot()
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		e, ok := table.Entry(model)
		if !ok || !e.MaxEffort {
			t.Errorf("%s must advertise max reasoning effort, got %+v (found=%v)", model, e, ok)
		}
	}
}

func TestLoadWithSnapshot_DownloadOverlaysButKeepsFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	// A download that reprices a model keeps the snapshot's flags when it
	// carries none of its own, and a missing download is not an error.
	data := `{"claude-opus-5": {"input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := loadWithSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := table.Entry("claude-opus-5")
	if e.InputCostPerToken != 0.000001 {
		t.Errorf("download should reprice, got %v", e.InputCostPerToken)
	}
	if !e.AdaptiveThinking || e.MaxInputTokens == 0 {
		t.Errorf("snapshot's flags and window should survive, got %+v", e)
	}
	if _, err := loadWithSnapshot(filepath.Join(dir, "missing.json")); err != nil {
		t.Errorf("a missing download should fall back to the snapshot, got %v", err)
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The rates a cached model carries, so the split can be checked against
// arithmetic rather than against whatever the shipped table happens to say.
func cachedTable(t *testing.T) *Table {
	t.Helper()
	tbl := &Table{models: map[string]ModelPricing{
		"cached-model": {
			InputCostPerToken:         10,
			OutputCostPerToken:        100,
			CacheReadCostPerToken:     1,
			CacheCreationCostPerToken: 12,
		},
		"uncached-model": {InputCostPerToken: 10, OutputCostPerToken: 100},
	}}
	return tbl
}

func TestCostTokensChargesEachPartAtItsOwnRate(t *testing.T) {
	in, out, found := cachedTable(t).CostTokens("cached-model",
		Tokens{Input: 1, Cached: 10, Created: 2, Output: 3})
	if !found {
		t.Fatal("priced model not found")
	}
	if want := float64(1*10 + 10*1 + 2*12); in != want {
		t.Errorf("input cost: want %v got %v", want, in)
	}
	if want := float64(3 * 100); out != want {
		t.Errorf("output cost: want %v got %v", want, out)
	}
}

// The whole point of the split: the same prompt costs less once it is being
// read back from the cache instead of fresh.
func TestCostTokensMakesACachedReadCheaper(t *testing.T) {
	tbl := cachedTable(t)
	fresh, _, _ := tbl.CostTokens("cached-model", Tokens{Input: 100})
	cached, _, _ := tbl.CostTokens("cached-model", Tokens{Cached: 100})
	if !(cached < fresh) {
		t.Fatalf("a cached read must cost less than a fresh one, fresh %v cached %v", fresh, cached)
	}
}

// A table that has not learned a model's cache rates must not price its
// cached tokens at zero: free is the one answer that is never right.
func TestCostTokensChargesFullRateWhenTheCacheRateIsUnknown(t *testing.T) {
	in, _, found := cachedTable(t).CostTokens("uncached-model",
		Tokens{Input: 1, Cached: 10, Created: 2})
	if !found {
		t.Fatal("priced model not found")
	}
	if want := float64(13 * 10); in != want {
		t.Errorf("unknown cache rates fall back to the input rate: want %v got %v", want, in)
	}
}

// Cost is CostTokens with no cache figures, and every existing caller depends
// on that staying true.
func TestCostIsCostTokensWithoutTheCacheParts(t *testing.T) {
	tbl := cachedTable(t)
	in, out, found := tbl.Cost("cached-model", 7, 3)
	if !found {
		t.Fatal("priced model not found")
	}
	tin, tout, _ := tbl.CostTokens("cached-model", Tokens{Input: 7, Output: 3})
	if in != tin || out != tout {
		t.Errorf("Cost and CostTokens disagree: %v/%v vs %v/%v", in, out, tin, tout)
	}
}

func TestOverlayKeepsCacheRatesAnEntryLeavesOut(t *testing.T) {
	tbl := cachedTable(t)
	tbl.Overlay(map[string]ModelPricing{
		"cached-model": {InputCostPerToken: 20, OutputCostPerToken: 200},
	})
	p, ok := tbl.lookup("cached-model")
	if !ok {
		t.Fatal("model lost in overlay")
	}
	if p.InputCostPerToken != 20 {
		t.Errorf("the overlay's own price must win, got %v", p.InputCostPerToken)
	}
	if p.CacheReadCostPerToken != 1 || p.CacheCreationCostPerToken != 12 {
		t.Errorf("cache rates the overlay did not mention must survive it, got %v/%v",
			p.CacheReadCostPerToken, p.CacheCreationCostPerToken)
	}
}

// The shipped snapshot has to actually carry the rates, or the split prices
// every model at the fallback and nothing is ever cheaper.
func TestSnapshotCarriesCacheRatesForTheCachingModels(t *testing.T) {
	tbl := Snapshot()
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5"} {
		p, ok := tbl.lookup(model)
		if !ok {
			t.Fatalf("%s missing from the snapshot", model)
		}
		if p.CacheReadCostPerToken <= 0 {
			t.Errorf("%s has no cached-read price", model)
		}
		if p.CacheReadCostPerToken >= p.InputCostPerToken {
			t.Errorf("%s prices a cached read at or above a fresh one: %v vs %v",
				model, p.CacheReadCostPerToken, p.InputCostPerToken)
		}
	}
}

// stubTransport puts fn in front of the download's client for one test, and
// answers how many times the download actually went out.
func stubTransport(t *testing.T, fn func() (*http.Response, error)) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	original := client.Transport
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return fn()
	})
	t.Cleanup(func() { client.Transport = original })
	return &calls
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// freshProcess gives the test the state a newly started process has: no
// download attempted yet. Load starts at most one per process, so a test that
// wants a second attempt has to ask for a second process.
func freshProcess(t *testing.T) {
	t.Helper()
	refreshing.Wait()
	refreshOnce = sync.Once{}
	t.Cleanup(func() { refreshing.Wait() })
}

// body is a 200 carrying data, as the table's host would answer.
func body(data string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(data)),
		Header:     make(http.Header),
	}, nil
}

// The symptom the marker exists for: without it nothing on disk changes when
// a download fails, so every process in an unattended run pays the same
// timeout over again.
func TestLoad_AFailedDownloadIsNotRepeatedByTheNextProcess(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	calls := stubTransport(t, func() (*http.Response, error) {
		return nil, errors.New("no network")
	})

	for i := range 2 {
		freshProcess(t)
		if _, err := Load(); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		refreshing.Wait()
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("the second process should read the remembered failure: transport called %d times", got)
	}
}

func TestShouldRefresh_TheFailureMarkerExpires(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := cachePath()
	must(t, err)

	markFailure(path)
	if shouldRefresh(path) {
		t.Error("a fresh failure should hold the next download off")
	}

	old := time.Now().Add(-failTTL - time.Minute)
	must(t, os.Chtimes(failMarker(path), old, old))
	if !shouldRefresh(path) {
		t.Error("an expired failure should let the next download through")
	}
}

func TestDownload_AFailureKeepsAGoodCacheAndASuccessClearsTheMarker(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := cachePath()
	must(t, err)
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	good := `{"gpt-4o": {"input_cost_per_token": 0.0000025}}`
	must(t, os.WriteFile(path, []byte(good), 0o600))

	stubTransport(t, func() (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError,
			Body: io.NopCloser(strings.NewReader("nope")), Header: make(http.Header)}, nil
	})
	if err := download(path); err == nil {
		t.Fatal("a 500 should be an error")
	}
	kept, err := os.ReadFile(path)
	must(t, err)
	if string(kept) != good {
		t.Errorf("a failed download must leave the cache alone, got %q", kept)
	}
	if _, err := os.Stat(failMarker(path)); err != nil {
		t.Errorf("a failed download should write the marker: %v", err)
	}

	stubTransport(t, func() (*http.Response, error) {
		return body(`{"gpt-4o": {"input_cost_per_token": 0.000005}}`)
	})
	if err := download(path); err != nil {
		t.Fatalf("download: %v", err)
	}
	if _, err := os.Stat(failMarker(path)); !os.IsNotExist(err) {
		t.Errorf("a landed download should clear the marker, got %v", err)
	}
	table, err := loadWithSnapshot(path)
	must(t, err)
	if e, _ := table.Entry("gpt-4o"); e.InputCostPerToken != 0.000005 {
		t.Errorf("the new table should be on disk, got %v", e.InputCostPerToken)
	}
}

// The startup cost this all exists to remove: Load reads disk and returns,
// however long the download takes.
func TestLoad_DoesNotWaitForTheDownload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	release := make(chan struct{})
	stubTransport(t, func() (*http.Response, error) {
		<-release
		return body(`{}`)
	})
	freshProcess(t)
	// Registered after freshProcess so it runs before it: cleanups run last
	// in, first out, and the wait cannot end until the download is let go.
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := Load(); err != nil {
			t.Errorf("load: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Load waited for the download")
	}
}
