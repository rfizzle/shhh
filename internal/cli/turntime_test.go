package cli

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// steppingClock is a held clock that moves a second on every reading, so
// every mark a turn takes lands on a stamp of its own and no part of the
// split depends on how fast the test ran.
type steppingClock struct {
	base time.Time
	n    atomic.Int64
}

func (c *steppingClock) now() time.Time {
	return c.base.Add(time.Duration(c.n.Add(1)) * time.Second)
}

func newSteppingClock() *steppingClock {
	return &steppingClock{base: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
}

// scriptedRounds is an agent whose requests replay the given rounds, with
// every auto call answered "contents".
func scriptedRounds(t *testing.T, rounds ...[]provider.StreamEvent) *agent.Agent {
	t.Helper()
	var next atomic.Int64
	a := agent.New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		i := int(next.Add(1)) - 1
		if i >= len(rounds) {
			t.Errorf("unexpected stream request #%d", i+1)
			return nil, nil, context.Canceled
		}
		ch := make(chan provider.StreamEvent, len(rounds[i]))
		for _, ev := range rounds[i] {
			ch <- ev
		}
		close(ch)
		return ch, func() {}, nil
	})
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "contents", nil })
	return a
}

// splitTurn is the one turn row a session wrote with its split.
func splitTurn(t *testing.T, db *storage.DB, rec *observeRecorder) storage.AgentTiming {
	t.Helper()
	var turns []storage.AgentTiming
	for _, r := range timings(t, db, rec) {
		if r.Kind == storage.AgentEventTurn {
			turns = append(turns, r)
		}
	}
	if len(turns) != 1 {
		t.Fatalf("split turn rows = %+v, want one", turns)
	}
	return turns[0]
}

// Every surface that runs a turn without the chat's screen — a `-p` run, a
// served session, a sub-agent and the one-shot — splits it by what it waited
// on, as the chat does: the four columns are written, and they add up to the
// turn's own duration. Each part a surface's turn spent time in is non-zero.
func TestTurnTime_EverySurfaceSplitsItsTurn(t *testing.T) {
	type parts struct{ first, stream, tool, person bool }
	check := func(t *testing.T, r storage.AgentTiming, want parts) {
		t.Helper()
		if r.DurationMs == nil || r.ModelFirstMs == nil || r.ModelStreamMs == nil || r.ToolMs == nil || r.PersonMs == nil {
			t.Fatalf("the turn row is missing a part: %+v", r)
		}
		sum := *r.ModelFirstMs + *r.ModelStreamMs + *r.ToolMs + *r.PersonMs
		if sum != *r.DurationMs || *r.DurationMs == 0 {
			t.Fatalf("parts %d+%d+%d+%d = %d, duration %d", *r.ModelFirstMs, *r.ModelStreamMs, *r.ToolMs, *r.PersonMs, sum, *r.DurationMs)
		}
		for name, c := range map[string]struct {
			ms   int64
			want bool
		}{
			"model-first": {*r.ModelFirstMs, want.first}, "model-stream": {*r.ModelStreamMs, want.stream},
			"tool": {*r.ToolMs, want.tool}, "person": {*r.PersonMs, want.person},
		} {
			if c.want && c.ms == 0 {
				t.Errorf("%s is zero in %+v", name, r)
			}
		}
	}
	toolThenAnswer := [][]provider.StreamEvent{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}}},
		{{Token: "done"}, {Done: true}},
	}

	t.Run("headless", func(t *testing.T) {
		db, rec := startupStore(t)
		clock := newSteppingClock()
		a := scriptedRounds(t, toolThenAnswer...)
		// Wired the way runPrintSession wires it.
		turnTime := turnTimer{now: clock.now}
		turnTime.begin()
		h := &agent.Headless{Agent: a}
		turnTime.drive(h)
		_, err := h.Run("read x")
		turnTime.close(rec, 1, int64(a.Rounds()), headlessTurnOutcome(err))
		check(t, splitTurn(t, db, rec), parts{first: true, stream: true, tool: true})
	})

	// The same, through the real commands on the wall clock: what the
	// cases above prove of the parts, these prove of the wiring.
	for _, c := range []struct {
		kind, name string
		args       []string
	}{
		{"print", "headless command", []string{"code", "-p", "list files"}},
		{"cmd", "one-shot command", []string{"cmd", "--raw", "list files"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var turns []storage.AgentTiming
			for _, r := range runSurface(t, c.kind, "", c.args...) {
				if r.Kind == storage.AgentEventTurn {
					turns = append(turns, r)
				}
			}
			if len(turns) != 1 {
				t.Fatalf("split turn rows = %+v, want one", turns)
			}
			check(t, turns[0], parts{})
		})
	}

	t.Run("serve", func(t *testing.T) {
		db, rec := startupStore(t)
		clock := newSteppingClock()
		a := scriptedRounds(t,
			[]provider.StreamEvent{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"make"}`}}}},
			[]provider.StreamEvent{{Token: "built"}, {Done: true}},
		)
		l := &serveLoop{
			agent:    a,
			recorder: rec,
			events:   newJSONLStream(&syncLines{}),
			own:      &writtenByCalls{},
			saved:    &headlessChat{},
			timing:   turnTimer{now: clock.now},
		}
		l.obs = headlessObserver{rounds: a.Rounds, turn: l.turnNow, stream: l.events}
		client := func(rpc.Call) bool { return true }
		l.headless = &agent.Headless{
			Agent: a,
			Gate:  func(tc provider.ToolCall) bool { return tc.Name == tools.ExecCommandName },
			// The call is put to the client the way the assembly's resolver
			// puts it (serve.go).
			Resolve: func(tc provider.ToolCall) string {
				if l.putToClient(client, rpc.Call{Tool: tc.Name}) {
					return "ok"
				}
				return declinedByClient(tc)
			},
		}
		if _, err := l.Run(1, "build it"); err != nil {
			t.Fatalf("the turn: %v", err)
		}
		check(t, splitTurn(t, db, rec), parts{first: true, stream: true, tool: true, person: true})
	})

	t.Run("subagent", func(t *testing.T) {
		db, rec := startupStore(t)
		clock := newSteppingClock()
		env := &scriptedChildren{steps: []childStep{
			{calls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}}},
			{text: "surveyed"},
		}}
		sup := subagent.New(t.Context(), subagent.Options{
			Root: t.TempDir(), NewEnv: env.factory(), Now: clock.now,
			Record: func(subagent.Spec, string) subagent.Recorder {
				return subagent.Recorder{Observer: rec.observer()}
			},
		})
		t.Cleanup(sup.Close)
		env.sup = sup
		exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) { return "", context.Canceled })
		if _, err := exec(subagent.SpawnToolName, json.RawMessage(`{"role":"researcher","task":"survey the exporter"}`)); err != nil {
			t.Fatalf("spawning a child: %v", err)
		}
		waitFor(t, "the child's turn row", func() bool {
			for _, r := range timings(t, db, rec) {
				if r.Kind == storage.AgentEventTurn {
					return true
				}
			}
			return false
		})
		check(t, splitTurn(t, db, rec), parts{first: true, stream: true, tool: true})
	})

	t.Run("one-shot", func(t *testing.T) {
		db, rec := startupStore(t)
		clock := newSteppingClock()
		timer := &oneShotTimer{now: clock.now, answered: true}
		timer.begin()
		p := timedProvider{Provider: oneShotProvider{answer: "ls -la", asked: &[]time.Time{}}, timer: timer}
		run := &oneShotRun{
			pending: startRecord(func() (*storage.DB, *observeRecorder) { return db, rec }),
			ledger:  meter.New(nil),
			timer:   timer,
			outcome: observe.TurnDone,
		}
		events, err := p.StreamCompletion(t.Context(), nil, provider.CompletionOpts{})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		for range events {
		}
		// The person's key carries the command out (act).
		run.timer.working()
		run.finish()
		check(t, splitTurn(t, db, rec), parts{first: true, stream: true, tool: true, person: true})
	})
}
