package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/tools"
)

// A served session in auto mode records a classifier's verdict with the time
// the judgement took, as the chat session and a `-p` run do, so its
// classifier-coded rows are not read as zero beside theirs.
func TestServe_ClassifierVerdictsCarryTheirTime(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	rec := startObserveRecorder(db, "serve", "openai", "gpt-test", nil)

	calls := 0
	a := agent.New(nil, nil)
	l := &serveLoop{agent: a, recorder: rec, own: &writtenByCalls{}, saved: &headlessChat{}}
	l.events = newJSONLStream(&syncLines{})
	l.obs = headlessObserver{rec: rec, rounds: a.Rounds, turn: l.turnNow, stream: l.events}
	l.verdict.Store(&lastVerdict{})
	judge := &autoJudge{ctx: t.Context(),
		classifier: agent.NewClassifier(verdictJudge{decision: "deny", calls: &calls}, agent.ClassifierConfig{Model: "m"})}
	resolve := headlessApprover(context.Background(), headlessApproval{
		record:     l.verdict.Load().wrap(l.obs.decision),
		recordTook: l.recordTook,
		un:         unattended{judge: judge, at: l.obs.pos},
	})
	resolve(provider.ToolCall{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"go install ./cmd/tool"}`})
	rec.end()

	if calls != 1 {
		t.Fatalf("the classifier was asked %d times, want once", calls)
	}
	events, err := db.AgentSessionEvents(rec.id)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var seen int
	for _, e := range events {
		if e.Kind != storage.AgentEventDecision {
			continue
		}
		seen++
		if e.Reason != observe.ReasonClassifier || e.DurationMs == nil {
			t.Fatalf("a classifier verdict in a served session lost its time: %+v", e)
		}
	}
	if seen != 1 {
		t.Fatalf("recorded %d decisions, want one: %+v", seen, events)
	}
}
