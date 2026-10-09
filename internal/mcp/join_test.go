package mcp

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// heldDial is a dial each server's connect waits on until the test lets it
// go: an answer on the server's channel is the server answering, a closed
// one the dial failing. Nothing here is a process or a listener.
type heldDial struct {
	mu      sync.Mutex
	answers map[string]chan *Server
}

func newHeldDial(names ...string) *heldDial {
	h := &heldDial{answers: map[string]chan *Server{}}
	for _, n := range names {
		h.answers[n] = make(chan *Server, 1)
	}
	return h
}

func (h *heldDial) dial(ctx context.Context, def Definition, _ func(string) bool) (*Server, error) {
	h.mu.Lock()
	ch := h.answers[def.Name]
	h.mu.Unlock()
	select {
	case s, ok := <-ch:
		if !ok {
			return nil, errors.New("connection refused")
		}
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answer lets one server's connect end with a server publishing tools named
// after it.
func (h *heldDial) answer(name string, tools ...string) {
	s := &Server{}
	for _, t := range tools {
		s.Tools = append(s.Tools, Tool{Name: name + Separator + t, Remote: t})
	}
	h.answers[name] <- s
}

// statusOf waits for one report to leave starting, on the toolset's own
// reading: the connect ends on its goroutine, and this is the reader the
// rail is.
func statusOf(t *testing.T, ts *Toolset, i int) Report {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := ts.Reports()[i]; r.Status != StatusStarting {
			return r
		}
		runtime.Gosched()
	}
	t.Fatalf("report %d never left starting: %+v", i, ts.Reports()[i])
	return Report{}
}

// Start returns with every admitted server starting, a report turns the
// moment its connect ends, and its tools reach the session's tables only at
// the boundary Join is — never while a round's call is out.
func TestJoin_TakesTheServerAtTheBoundaryNotMidRound(t *testing.T) {
	h := newHeldDial("docs", "tracker")
	cat := &Catalog{Servers: []Definition{
		{Name: "docs", Scope: ScopeUser, Transport: TransportStdio, Command: "docs-mcp", ReadOnly: true},
		{Name: "tracker", Scope: ScopeUser, Transport: TransportStdio, Command: "tracker-mcp"},
		{Name: "legacy", Scope: ScopeUser, Transport: TransportStdio, Command: "legacy-mcp", Disabled: true},
	}}
	ts := Start(context.Background(), cat, Options{Dial: h.dial, Timeout: time.Minute})
	defer ts.Close()

	reps := ts.Reports()
	if reps[0].Status != StatusStarting || reps[1].Status != StatusStarting || reps[2].Status != StatusDisabled {
		t.Fatalf("reports at the start = %+v", reps)
	}
	if reps[0].Began.IsZero() || reps[0].Bound != time.Minute || !reps[0].BoundBySession {
		t.Fatalf("a starting report carries no clock or bound: %+v", reps[0])
	}
	if ts.Len() != 0 || ts.Join() != nil {
		t.Fatal("a server nobody has heard from has tools")
	}

	h.answer("docs", "search", "lookup")
	if r := statusOf(t, ts, 0); r.Status != StatusConnected {
		t.Fatalf("docs answered and reads %s", r.Status)
	}
	if ts.Len() != 0 || ts.Has("docs__search") {
		t.Fatal("an answer reached the model's tools before a boundary")
	}

	// A round's call is out: the boundary is not here yet.
	ts.mu.Lock()
	ts.inflight++
	ts.mu.Unlock()
	if got := ts.Join(); got != nil {
		t.Fatalf("a join was taken under a call in flight: %+v", got)
	}
	ts.mu.Lock()
	ts.inflight--
	ts.mu.Unlock()

	got := ts.Join()
	if len(got) != 1 || got[0].Definition.Name != "docs" {
		t.Fatalf("join took %+v", got)
	}
	if !ts.Has("docs__search") || ts.Len() != 2 {
		t.Fatalf("docs did not join: %d tools", ts.Len())
	}
	if ts.Join() != nil {
		t.Fatal("a join was taken twice")
	}

	// docs re-lists at a round boundary: the tables move, and what the
	// model is offered does not until the next join.
	docs, _, _ := ts.Lookup("docs__search")
	docs.mu.Lock()
	docs.pending = &catalog{tools: append(docs.Tools, Tool{Name: "docs__later", Remote: "later"})}
	docs.mu.Unlock()
	if !ts.Refresh() || !ts.Has("docs__later") {
		t.Fatal("the re-listing was not taken")
	}
	if n := len(ts.Offered()); n != 2 {
		t.Fatalf("a re-listing moved what the model is offered: %d tools", n)
	}

	// tracker refuses: its row says so at once, and the boundary hands it
	// back for its line, with nothing to index.
	close(h.answers["tracker"])
	if r := statusOf(t, ts, 1); r.Status != StatusFailed {
		t.Fatalf("tracker refused and reads %s", r.Status)
	}
	if got := ts.Join(); len(got) != 1 || got[0].Status != StatusFailed || ts.Len() != 3 {
		t.Fatalf("join took %+v with %d tools", got, ts.Len())
	}
}

// A server that runs out its bound is failed and timed out, with the reason
// it has always carried, and its row says so when the bound ends.
func TestJoin_ABoundThatRunsOutIsAFailure(t *testing.T) {
	h := newHeldDial("tracker")
	cat := &Catalog{Servers: []Definition{
		{Name: "tracker", Scope: ScopeUser, Transport: TransportStdio, Command: "tracker-mcp", Timeout: 20 * time.Millisecond},
	}}
	ts := Start(context.Background(), cat, Options{Dial: h.dial})
	defer ts.Close()
	r := statusOf(t, ts, 0)
	if r.Status != StatusFailed || !r.TimedOut || r.Error != "server tracker: no answer within 20ms" {
		t.Fatalf("report = %+v", r)
	}
	if r.Bound != 20*time.Millisecond || r.BoundBySession {
		t.Fatalf("the bound came from the definition: %+v", r)
	}
}

// The reports change after Start returns, from the connects' own goroutines,
// while the rail and the listings read them and a boundary joins: every read
// goes through the toolset's lock, so the race detector has nothing to say.
func TestToolset_AJoinDuringAListingIsNotARace(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	h := newHeldDial(names...)
	var defs []Definition
	for _, n := range names {
		defs = append(defs, Definition{Name: n, Scope: ScopeUser, Transport: TransportStdio, Command: n, ReadOnly: n == "a"})
	}
	ts := Start(context.Background(), &Catalog{Servers: defs}, Options{Dial: h.dial, Timeout: time.Minute})
	defer ts.Close()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 3 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, r := range ts.Reports() {
					_ = r.Status
					if r.Server != nil {
						_ = r.Server.RegisteredTools()
					}
				}
				_ = ts.Definitions()
				_, _ = ts.ReadOnlyView()
				_ = PromptBlock(ts)
			}
		}()
	}
	for _, n := range names {
		h.answer(n, "one")
		ts.Join()
	}
	ts.Wait()
	ts.Join()
	close(stop)
	readers.Wait()
	if ts.Len() != len(names) {
		t.Fatalf("%d tools joined, want %d", ts.Len(), len(names))
	}
}

// Connect is Start that waits: everything it returns has settled and joined.
func TestConnect_WaitsForEveryServerAndJoinsThem(t *testing.T) {
	h := newHeldDial("docs")
	h.answer("docs", "search")
	ts := Connect(context.Background(), &Catalog{Servers: []Definition{
		{Name: "docs", Scope: ScopeUser, Transport: TransportStdio, Command: "docs-mcp"},
	}}, Options{Dial: h.dial})
	defer ts.Close()
	if r := ts.Reports()[0]; r.Status != StatusConnected || !ts.Has("docs__search") {
		t.Fatalf("report = %+v, has = %v", r, ts.Has("docs__search"))
	}
	if ts.Join() != nil {
		t.Fatal("Connect left a join for a boundary to take")
	}
}
