package cli

// The root generates nothing: what used to be `shhh <prompt>` is `shhh cmd`,
// and both ways of arriving at the root with a prompt in hand have to say so.
// A bare `shhh` on a terminal still prints the tree, which help_test.go
// covers.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui"
)

func TestBarePromptNamesTheCommandThatGenerates(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list open ports"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("`shhh \"list open ports\"` should be refused, not generated")
	}
	if !strings.Contains(err.Error(), `shhh cmd "list open ports"`) {
		t.Errorf("the refusal should spell the command that would have run it, got %q", err)
	}
}

// A prompt on stdin used to be the whole no-TTY contract, and a script that
// still pipes into the root gets a failure rather than the help page: help on
// stdout is what `echo … | shhh | sh` would then run.
func TestPipedPromptNamesTheCommandThatReadsIt(t *testing.T) {
	// fang resolves its width once per process, so a render here asks for
	// the same one every other help render in this package does.
	t.Setenv("__FANG_TEST_WIDTH", "80")
	t.Setenv("NO_COLOR", "1")
	// `go test` hands the process a stdin that is not a terminal, which is
	// the condition the root branches on.
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)
	if err := execute(context.Background(), cmd); err == nil {
		t.Fatal("a piped `shhh` should be refused, not answered with the help page")
	}
	if !strings.Contains(stderr.String(), "shhh cmd") {
		t.Errorf("the refusal should name `shhh cmd`, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("nothing may reach the pipe on stdout, got:\n%s", stdout.String())
	}
}

// The one-shot's turn ends in that same set. Walking away from a command is
// cancelled and not done: the request was answered and the answer refused,
// and an outcome mix that could not tell those apart is the one figure this
// record exists to make readable.
func TestOneShotOutcome(t *testing.T) {
	for _, c := range []struct {
		name   string
		result ui.GenerateResult
		want   string
	}{
		{"ran it", ui.GenerateResult{Action: ui.ActionRun}, observe.TurnDone},
		{"copied it", ui.GenerateResult{Action: ui.ActionCopy}, observe.TurnDone},
		{"escaped the card", ui.GenerateResult{Action: ui.ActionCancel}, observe.TurnCancelled},
		{"cancelled the stream", ui.GenerateResult{Cancelled: true}, observe.TurnCancelled},
		// A failure outranks the action it failed on: a request that never
		// produced a command is not a command the user declined.
		{"failed", ui.GenerateResult{Action: ui.ActionCancel, Err: errors.New("no key")}, observe.TurnFailed},
	} {
		if got := oneShotOutcome(c.result); got != c.want {
			t.Errorf("%s: oneShotOutcome = %q, want %q", c.name, got, c.want)
		}
	}
}

// The one-shot joins the record as what it is: one request, so one turn, with
// no rounds and no tool mix. Both of those are true rather than missing.
func TestOneShotSessionIsOneTurn(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "cmd", "anthropic", "test-model", nil)
	rec.stamp("the one-shot prompt", 0, "/repo")
	rec.usagePriced(1, 900, 120, 0.004, true)
	rec.turn(1, 0, 2*time.Second, observe.TurnDone)
	rec.end()

	assertShapes(t, shapesOf(t, db, rec.sessionID()), []eventShape{
		{kind: storage.AgentEventTurn, outcome: observe.TurnDone, turn: 1, timed: true},
	})

	s, ok, err := db.AgentSession(rec.sessionID())
	if err != nil || !ok {
		t.Fatalf("session: %v (found=%v)", err, ok)
	}
	if s.Kind != "cmd" {
		t.Fatalf("kind = %q, want cmd", s.Kind)
	}
	if s.Turns != 1 || s.TokensIn != 900 || s.TokensOut != 120 || s.Cost != 0.004 {
		t.Fatalf("unexpected totals: %+v", s)
	}
	if s.PromptHash != fingerprint("the one-shot prompt") || s.Project != fingerprint("/repo") {
		t.Fatalf("one-shot was not stamped: %+v", s)
	}
	if s.EndedAt == nil {
		t.Fatal("the one-shot's row was never ended")
	}
}
