package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
)

// writersFixture is a store with three children: one that answered well
// inside its budget, one that spent it, one that was killed.
func writersFixture(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	parent, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatal(err)
	}
	child := func(name, reason string, budget, analysis int64) {
		id, err := db.StartChildAgentSession(parent, "writer", "openai", "gpt-test", name)
		if err != nil {
			t.Fatal(err)
		}
		end := observe.ChildEnd{Reason: reason, Attempt: 1, Budget: budget, AdmissionFloor: 200000,
			Tokens: observe.ChildTokens{Inherited: 10000, Setup: 20000, Tools: 30000, Analysis: analysis, Handoff: 40000, Fresh: analysis}}
		if err := db.EndChildAgentSession(id, "completed", end); err != nil {
			t.Fatal(err)
		}
	}
	child("writer-1", observe.ChildDone, 1200000, 300000)
	child("writer-2", observe.ChildBudget, 1200000, 1100000)
	child("writer-3", observe.ChildKilled, 600000, 200000)
	return db
}

func TestObserve_WritersAgainstTheirBudget(t *testing.T) {
	db := writersFixture(t)
	writers, err := db.AgentWriters(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(writers) != 3 {
		t.Fatalf("writers = %d, want 3", len(writers))
	}
	body := observeWritersReport(writers, "7d").Render(110)
	for _, want := range []string{"3 writers", "1 ended by the budget", "median 400k", "worst 1.2M",
		"400k of 1.2M", "33% · done", "1.2M of 1.2M", "100% · budget", "300k of 600k", "50% · stopped"} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q:\n%s", want, body)
		}
	}
	// The dashboard carries the same block.
	data := observeData{Window: "7d", Sessions: []storage.AgentSessionSummary{{ID: 1}}, Writers: writers}
	if dash := observeReport(data).Render(110); !strings.Contains(dash, "WRITERS") || !strings.Contains(dash, "3 writers") {
		t.Errorf("dashboard lacks the writers block:\n%s", dash)
	}
}
