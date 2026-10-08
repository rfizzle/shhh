package subagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/tools"
)

// A child's turn is split by what it waited on, and the time its approval sat
// in front of the person is the person's share rather than the call's: the
// split is read off the same stamps as the turn's duration, so the four add up
// to it.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.
func TestTurnTime_AChildsApprovalIsThePersons(t *testing.T) {
	env := &scriptedEnv{
		steps:   gatedCommandSteps("echo hi"),
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "hi",
	}
	// A held clock that moves a second at every reading, so each mark has a
	// stamp of its own whatever the machine's pace.
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var ticks atomic.Int64
	now := func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }

	type row struct {
		took  time.Duration
		split agent.TurnSplit
	}
	var mu sync.Mutex
	var rows []row
	sup := New(context.Background(), Options{
		Root: t.TempDir(), NewEnv: env.factory(), Now: now,
		Record: func(Spec, string) Recorder {
			return Recorder{Observer: observe.Observer{
				TurnTimed: func(_, _ int64, took time.Duration, _ string, split agent.TurnSplit) {
					mu.Lock()
					rows = append(rows, row{took, split})
					mu.Unlock()
				},
			}}
		},
	})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"run something"}`)

	nextAsk(t, sup).Respond(true)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(rows) != 1 {
		t.Fatalf("turn rows = %+v, want one", rows)
	}
	r := rows[0]
	if r.split.Total() != r.took {
		t.Fatalf("the split adds up to %v, the turn took %v", r.split.Total(), r.took)
	}
	if r.split.Person == 0 || r.split.Tool == 0 || r.split.ModelFirst == 0 {
		t.Fatalf("a wait the turn spent time in is zero: %+v", r.split)
	}
}
