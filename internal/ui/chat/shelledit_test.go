package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/tools"
)

// An edit written as a command is answered with the editor the first time;
// the identical line the second time runs, and a different edit is answered
// again. The person's own ! is not asked.
func TestShellEdit_TheFirstAskingIsAnsweredAndTheSameLineRuns(t *testing.T) {
	m := New(nil, nil, Wiring{
		Workspace: t.TempDir(),
		Runner:    func(context.Context, string) tools.ExecResult { return tools.ExecResult{} },
	})
	d, result, _ := m.StandingAnswer(execCall("c1", "sed -i 's/a/b/' main.go"))
	if d != permission.Deny || !strings.Contains(result, "edit_file") || !strings.Contains(result, "send the same line again") {
		t.Fatalf("first asking: %v %q, want the redirect", d, result)
	}
	// Another call of the same message: the model has read nothing yet.
	if d, _, _ := m.StandingAnswer(execCall("c1b", "sed -i 's/a/b/' main.go")); d != permission.Deny {
		t.Fatalf("the same line in the same message: %v, want it answered too", d)
	}
	m.agent.BeginToolRound("", nil, nil)
	if d, result, _ := m.StandingAnswer(execCall("c2", "  sed -i 's/a/b/' main.go ")); d == permission.Deny || result != "" {
		t.Fatalf("the same line again: %v %q, want it to run", d, result)
	}
	if d, _, _ := m.StandingAnswer(execCall("c3", "sed -i 's/a/c/' main.go")); d != permission.Deny {
		t.Fatalf("a different edit: %v, want it answered", d)
	}
	if r := m.approval.redirects; r == nil {
		t.Fatal("a session carries the memory")
	}
}

func TestShellEdit_TheRowOfTheRunSaysAfterARedirect(t *testing.T) {
	m := New(nil, nil, Wiring{})
	e := entry{kind: entryCommand, text: "sed -i s/a/b/ f", afterRedirect: true, approvedBy: decidedByYou}
	if got := m.activityRowDetail(e, false, 100).Allowed; !strings.HasSuffix(got, afterRedirectWord) {
		t.Fatalf("row account = %q, want it to end with %q", got, afterRedirectWord)
	}
	e = m.skippedCallEntry(execCall("c1", "sed -i s/a/b/ f"), tools.ShellEditError{Command: "sed -i s/a/b/ f"})
	if e.text != "sed -i s/a/b/ f" || e.skipped != shellEditReason || !strings.Contains(e.toolResult, "edit_file") {
		t.Fatalf("skipped row = %+v", e)
	}
}
