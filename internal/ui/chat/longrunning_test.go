package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/tools"
)

// The model's dev server is answered with the redirect before any card; the
// same line typed by the person after ! takes the run confirm as any command
// does, because the person's own commands never go through the classifier.
func TestLongRunning_TheModelIsRedirectedAndThePersonIsNot(t *testing.T) {
	m := New(nil, nil, Wiring{
		Workspace: t.TempDir(),
		Runner:    func(context.Context, string) tools.ExecResult { return tools.ExecResult{} },
	})
	d, result, _ := m.StandingAnswer(execCall("c1", "npm run dev"))
	want := "error: " + tools.LongRunningError{}.Error()
	if d != agent.Deny || result != want {
		t.Fatalf("the model's dev server: %v %q, want a refusal reading %q", d, result, want)
	}
	if d, result, _ := m.StandingAnswer(execCall("c1", "go test ./...")); result != "" || d == agent.Deny {
		t.Fatalf("an ordinary command was refused: %v %q", d, result)
	}

	p := runCapableModel("no blocks here")
	p = sendText(t, p, "!npm run dev")
	if p.state != stateConfirmRun || p.pendingRun != "npm run dev" {
		t.Fatalf("the person's !npm run dev: state %d pending %q, want the run confirm", p.state, p.pendingRun)
	}
}

func TestLongRunning_TheSkippedRowNamesTheLine(t *testing.T) {
	m := New(nil, nil, Wiring{})
	e := m.skippedCallEntry(execCall("c1", "tail -f app.log"), tools.LongRunningError{})
	if e.text != "tail -f app.log" || e.skipped != longRunningReason || !strings.Contains(e.toolResult, "process tool") {
		t.Fatalf("row = text %q skipped %q result %q", e.text, e.skipped, e.toolResult)
	}
}
