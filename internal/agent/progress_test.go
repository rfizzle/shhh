package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestProgressCheckpoint_AfterSilentToolActivity(t *testing.T) {
	now := time.Unix(100, 0)
	a := New(nil, noStream)
	a.now = func() time.Time { return now }
	a.SetProgressIntervals(2, time.Hour)
	a.StartTurn("find the loop")
	a.BeginToolRound("", []provider.ToolCall{{ID: "one", Name: "search"}}, nil)
	if _, ok := a.TakeProgressCheckpoint(); ok {
		t.Fatal("one call should not interrupt a coherent batch")
	}
	a.BeginToolRound("", []provider.ToolCall{{ID: "two", Name: "read_file"}}, nil)
	prompt, ok := a.TakeProgressCheckpoint()
	if !ok {
		t.Fatal("two silent calls should earn a public checkpoint")
	}
	for _, want := range []string{"objective", "evidence", "next action", "private reasoning", "every tool call"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("checkpoint prompt missing %q:\n%s", want, prompt)
		}
	}
	if !a.ProgressPending() {
		t.Fatal("checkpoint should remain pending until prose arrives")
	}
	if !a.NoteProgressProse("I found the loop; next I will change it.") {
		t.Fatal("checkpoint prose should be marked distinctly")
	}
	if a.ProgressPending() {
		t.Fatal("assistant prose should settle the checkpoint")
	}
}

func TestProgressCheckpoint_UsesElapsedTimeAndDoesNotResetInterventions(t *testing.T) {
	now := time.Unix(100, 0)
	a := New(nil, noStream)
	a.now = func() time.Time { return now }
	a.SetProgressIntervals(20, time.Minute)
	a.StartTurn("run the tests")
	a.BeginToolRound("", []provider.ToolCall{{ID: "one", Name: "execute_command"}}, nil)
	a.NoteIntervention()
	now = now.Add(time.Minute)
	if _, ok := a.TakeProgressCheckpoint(); !ok {
		t.Fatal("a long-running command should earn a progress checkpoint")
	}
	if got := a.lastIntervention; got != 1 {
		t.Fatalf("progress checkpoint reset intervention at round %d", got)
	}
}

func TestProgressCheckpoint_DoesNotTreatRestoredRoundAsOverdue(t *testing.T) {
	a := New(nil, noStream)
	a.SetProgressIntervals(1, time.Second)
	a.BeginToolRound("", []provider.ToolCall{{ID: "one", Name: "read_file"}}, nil)
	if _, ok := a.TakeProgressCheckpoint(); ok {
		t.Fatal("a restored round with no start time should begin its clock")
	}
}
