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

// The mark goes on the message, because the message is the only part of a
// round that outlives it: a conversation reopened from the store has the
// words and nothing else, and prose that answered a status request has to
// come back as the note it was rather than as an answer.
func TestProgressCheckpoint_MarksTheMessageThatCarriedTheStatus(t *testing.T) {
	now := time.Unix(100, 0)
	a := New(nil, noStream)
	a.now = func() time.Time { return now }
	a.SetProgressIntervals(2, time.Hour)
	a.StartTurn("find the loop")
	a.BeginToolRound("", []provider.ToolCall{{ID: "one", Name: "search"}}, nil)
	a.BeginToolRound("", []provider.ToolCall{{ID: "two", Name: "read_file"}}, nil)
	if _, ok := a.TakeProgressCheckpoint(); !ok {
		t.Fatal("two silent calls should earn a public checkpoint")
	}

	const note = "The objective is the loop. The evidence is two reads. Next I will change it."
	if !a.NoteProgressProse(note) {
		t.Fatal("the prose should be read as the status that was asked for")
	}
	a.BeginToolRound(note, []provider.ToolCall{{ID: "three", Name: "read_file"}}, nil)

	var marked []string
	for _, msg := range a.Messages() {
		if msg.Checkpoint {
			marked = append(marked, msg.Content)
		}
	}
	if len(marked) != 1 || marked[0] != note {
		t.Fatalf("the conversation marks %q as public status, want just the note", marked)
	}

	// And the latch is the round's own: the next round's prose is ordinary
	// prose, and a latch left set would mark it too.
	const ordinary = "Now changing the loop."
	if a.NoteProgressProse(ordinary) {
		t.Fatal("nothing asked for a second status")
	}
	a.BeginToolRound(ordinary, []provider.ToolCall{{ID: "four", Name: "edit_file"}}, nil)
	for _, msg := range a.Messages() {
		if msg.Checkpoint && msg.Content != note {
			t.Errorf("prose nobody asked for was marked as public status: %q", msg.Content)
		}
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
