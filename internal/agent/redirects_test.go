package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

func execArgs(t *testing.T, command string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRedirects_AnEditIsAnsweredOnceAndTheSameLineRuns(t *testing.T) {
	round := 1
	r := NewRedirects("/work/proj", func() int { return round })
	answers := Answers{Redirects: r}
	line := "sed -i 's/a/b/' main.go"

	// Asking does not spend the answer: a call is classified more than once.
	var err error
	for i := 0; i < 2; i++ {
		_, err = ClassifyCall(tools.ExecCommandName, execArgs(t, line), answers)
		var edit tools.ShellEditError
		if !errors.As(err, &edit) {
			t.Fatalf("asking %d: err = %v, want the redirect", i, err)
		}
	}
	r.Spent(err)

	// The same line again in the same message: the model has read nothing yet.
	if _, err := ClassifyCall(tools.ExecCommandName, execArgs(t, line), answers); err == nil {
		t.Fatal("the same line in the same round ran before the model read the answer")
	}
	round = 2

	c, err := ClassifyCall(tools.ExecCommandName, execArgs(t, " "+line+"\n"), answers)
	if err != nil || !c.AfterRedirect {
		t.Fatalf("the same line: err %v after %v, want it to run after a redirect", err, c.AfterRedirect)
	}
	_, err = ClassifyCall(tools.ExecCommandName, execArgs(t, "sed -i 's/a/c/' main.go"), answers)
	if err == nil {
		t.Fatal("a different line was not answered")
	}
	c, err = ClassifyCall(tools.ExecCommandName, execArgs(t, "echo hi"), answers)
	if err != nil || c.AfterRedirect {
		t.Fatalf("an ordinary line: err %v after %v", err, c.AfterRedirect)
	}
}

func TestRedirects_ASurfaceWithoutTheMemoryAnswersNothing(t *testing.T) {
	c, err := ClassifyCall(tools.ExecCommandName, execArgs(t, "sed -i s/a/b/ f"), Answers{})
	if err != nil || c.AfterRedirect {
		t.Fatalf("err %v after %v, want the line to run as always", err, c.AfterRedirect)
	}
}
