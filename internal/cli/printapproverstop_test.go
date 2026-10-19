package cli

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/web"
)

// A gated call a run's flags allowed runs under the run's context: stopping
// the run reaches a fetch still waiting on the network, and the model reads
// the stop rather than a failure.
func TestHeadless_AGatedCallStopsWithTheRun(t *testing.T) {
	fetcher := web.NewFetcher(web.Policy{})
	started := make(chan struct{})
	fetcher.Resolve = func(ctx context.Context, _ string) ([]netip.Addr, error) {
		close(started)
		<-ctx.Done()
		return nil, errors.New("lookup ended: " + ctx.Err().Error())
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	resolve := headlessApprover(ctx, headlessApproval{
		opts:     printOpts{yes: true},
		webTools: web.NewToolset(fetcher, nil),
	})
	call := llm.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"https://example.test/page"}`}

	done := make(chan string, 1)
	go func() { done <- resolve(call) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch never started")
	}
	stop()
	select {
	case got := <-done:
		if got != agent.CancelledResult {
			t.Fatalf("the model was told %q, want %q", got, agent.CancelledResult)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not reach the gated fetch")
	}
}
