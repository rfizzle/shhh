package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// endpointProvider is a provider whose endpoint reports the context length it
// serves each model at. The embedded interface is nil: nothing under test
// streams or names it.
type endpointProvider struct {
	provider.Provider
	windows map[string]int64
	err     error
}

func (e endpointProvider) ModelWindows(context.Context) (map[string]int64, error) {
	return e.windows, e.err
}

// await polls the lookup until the background query lands, which is how the
// session reads it: on a later frame, not on the one that asked. The wait is
// what a passing test costs when the answer never comes, so the callers that
// expect no answer pass a short one.
func await(t *testing.T, lookup func(string) (int64, bool), model string, wait time.Duration) (int64, bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		if w, ok := lookup(model); ok {
			return w, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEndpointWindowsFor(t *testing.T) {
	lookup := endpointWindowsFor(endpointProvider{windows: map[string]int64{"qwen3:8b": 262_144}})
	if lookup == nil {
		t.Fatal("a provider that can report its windows should get a lookup")
	}
	// The catalog's ids are lower-cased, so the session's own spelling of the
	// model still finds them.
	if w, ok := await(t, lookup, "Qwen3:8B", 2*time.Second); !ok || w != 262_144 {
		t.Fatalf("window = %d, %v; want 262144, true", w, ok)
	}
	if _, ok := lookup("claude-opus-5"); ok {
		t.Error("a model the endpoint did not describe must fall through to the table")
	}
}

// A failed probe is silent: the session reads the table, which is what it did
// before anything was asked.
func TestEndpointWindowsFor_FailureLeavesTheLookupEmpty(t *testing.T) {
	lookup := endpointWindowsFor(endpointProvider{err: errors.New("no catalog here")})
	if lookup == nil {
		t.Fatal("expected a lookup")
	}
	if _, ok := await(t, lookup, "qwen3:8b", 250*time.Millisecond); ok {
		t.Error("a failed query must not answer")
	}
}

func TestEndpointWindowsFor_ProviderWithoutTheCapability(t *testing.T) {
	if lookup := endpointWindowsFor(struct{ provider.Provider }{}); lookup != nil {
		t.Error("a provider whose endpoint cannot answer should get no lookup")
	}
}
