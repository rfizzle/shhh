package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// heldEnv is an EnvFactory whose children each answer only once the test lets
// that child go, so the order a fan-out finishes in is the test's to choose.
type heldEnv struct {
	mu      sync.Mutex
	release map[string]chan struct{}
}

func (h *heldEnv) gate(name string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.release == nil {
		h.release = map[string]chan struct{}{}
	}
	ch, ok := h.release[name]
	if !ok {
		ch = make(chan struct{})
		h.release[name] = ch
	}
	return ch
}

// finish lets one child answer.
func (h *heldEnv) finish(name string) { close(h.gate(name)) }

func (h *heldEnv) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			select {
			case <-h.gate(spec.Name):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			ch := make(chan provider.StreamEvent, 2)
			ch <- provider.StreamEvent{Token: "report of " + spec.Name}
			ch <- provider.StreamEvent{Done: true}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "test system prompt",
			Stream:       stream,
			Executor: func(name string, _ json.RawMessage) (string, error) {
				return "auto:" + name, nil
			},
			ExecuteGated: func(name string, _ json.RawMessage) (string, error) {
				return "gated:" + name, nil
			},
			RunCommand: func(context.Context, string) tools.ExecResult { return tools.ExecResult{Outcome: tools.ExecSucceeded} },
		}, nil
	}
}

func newHeldSupervisor(t *testing.T) (*Supervisor, *heldEnv) {
	t.Helper()
	env := &heldEnv{}
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory()})
	t.Cleanup(sup.Close)
	return sup, env
}

// waitAsync runs an agent_report call as caller in the background.
func waitAsync(sup *Supervisor, caller, args string) <-chan string {
	out := make(chan string, 1)
	exec := sup.WrapExecutor(caller, func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	go func() {
		text, err := exec(ReportToolName, json.RawMessage(args))
		if err != nil {
			text = "error: " + err.Error()
		}
		out <- text
	}()
	return out
}

func stillWaiting(t *testing.T, out <-chan string) {
	t.Helper()
	select {
	case text := <-out:
		t.Fatalf("the wait returned before anything it waits for happened:\n%s", text)
	case <-time.After(50 * time.Millisecond):
	}
}

func answer(t *testing.T, out <-chan string) string {
	t.Helper()
	select {
	case text := <-out:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("the wait never returned")
		return ""
	}
}

// A wait on a set returns the first of them to finish, whichever that is, with
// a line for each of the others saying where it stands.
// See docs/capabilities/subagents.md#a-wait-only-ever-points-down-the-tree.
func TestAWaitOnASetReturnsTheFirstToFinish(t *testing.T) {
	sup, env := newHeldSupervisor(t)
	for _, name := range []string{"one", "two", "three"} {
		execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","name":"`+name+`"}`)
	}
	for _, name := range []string{"one", "two", "three"} {
		waitState(t, sup, name, StateRunning)
	}

	out := waitAsync(sup, "", `{"names":["one","two","three"]}`)
	stillWaiting(t, out)
	env.finish("two")
	text := answer(t, out)
	if !strings.Contains(text, "report of two") || strings.Contains(text, "report of one") {
		t.Fatalf("the wait should return the one that finished:\n%s", text)
	}
	for _, want := range []string{"The others you named:", "\none · running", "\nthree · running"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the wait should say where the others stand (%q):\n%s", want, text)
		}
	}

	// Nothing waits on three when it finishes; it is not lost for that, and a
	// wait naming it returns at once, ahead of one named before it.
	env.finish("three")
	waitState(t, sup, "three", StateDone)
	text = answer(t, waitAsync(sup, "", `{"name":"one","names":["three"]}`))
	if !strings.Contains(text, "report of three") || !strings.Contains(text, "\none · running") {
		t.Fatalf("a finished child should be collected at once:\n%s", text)
	}
	if text := answer(t, waitAsync(sup, "", `{"name":"three"}`)); !strings.Contains(text, "report of three") {
		t.Fatalf("a wait on one finished name should return its report:\n%s", text)
	}

	// wait=false with names is the roster narrowed to them.
	peek := execTool(t, sup, ReportToolName, `{"names":["one","three"],"wait":false}`)
	if lines := strings.Split(peek, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "one (") ||
		!strings.HasPrefix(lines[1], "three (") {
		t.Fatalf("a peek at a set should be their roster lines alone:\n%s", peek)
	}
	env.finish("one")
}

// A set with a name that is not the caller's is refused, as one name is.
func TestAWaitOnASetIsBoundedToTheCallersOwn(t *testing.T) {
	sup, env := newHeldSupervisor(t)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","name":"two"}`)
	if _, err := spawnFromAgent(sup, "one", `{"role":"researcher","task":"c","name":"one-a"}`); err != nil {
		t.Fatal(err)
	}
	exec := sup.WrapExecutor("one", func(string, json.RawMessage) (string, error) {
		return "", errors.New("unexpected passthrough")
	})
	if _, err := exec(ReportToolName, json.RawMessage(`{"names":["one-a","two"]}`)); err == nil ||
		!strings.Contains(err.Error(), `"two"`) {
		t.Fatalf("a set naming a sibling should be refused naming it, got %v", err)
	}
	if _, err := exec(ReportToolName, json.RawMessage(`{"names":["nobody"]}`)); err == nil {
		t.Fatal("a set naming no agent should be refused")
	}
	for _, name := range []string{"one", "two", "one-a"} {
		env.finish(name)
	}
}

// The session steered while it waits comes out of the wait, saying so first,
// with where each named agent stands; a wait started while a steer is queued
// returns at once, and once the queue is drained a wait waits again.
func TestASessionWaitEndsWhenTheSessionIsSteered(t *testing.T) {
	sup, env := newHeldSupervisor(t)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","name":"two"}`)
	waitState(t, sup, "one", StateRunning)
	waitState(t, sup, "two", StateRunning)

	for _, args := range []string{`{"names":["one","two"]}`, `{"name":"one"}`} {
		out := waitAsync(sup, "", args)
		stillWaiting(t, out)
		sup.SessionSteering(1)
		text := answer(t, out)
		if !strings.HasPrefix(text, "Woken by a steer") || !strings.Contains(text, "\none · running") {
			t.Fatalf("%s: a steered wait should say so first and where each stands:\n%s", args, text)
		}
		if text := answer(t, waitAsync(sup, "", args)); !strings.HasPrefix(text, "Woken by a steer") {
			t.Fatalf("%s: a wait started with a steer queued should return at once:\n%s", args, text)
		}
		sup.SessionSteering(0)
	}

	out := waitAsync(sup, "", `{"names":["one","two"]}`)
	stillWaiting(t, out)
	env.finish("one")
	if text := answer(t, out); !strings.Contains(text, "report of one") {
		t.Fatalf("with the queue drained the wait should be on the children again:\n%s", text)
	}
	env.finish("two")
}

// An agent steered while it waits on its own children comes out of the wait,
// and so does every wait it is inside, not one of them.
func TestAnAgentsWaitEndsWhenTheAgentIsSteered(t *testing.T) {
	sup, env := newHeldSupervisor(t)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	for _, kid := range []string{"one-a", "one-b"} {
		if _, err := spawnFromAgent(sup, "one", `{"role":"researcher","task":"c","name":"`+kid+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	waitState(t, sup, "one-a", StateRunning)

	first := waitAsync(sup, "one", `{"names":["one-a","one-b"]}`)
	second := waitAsync(sup, "one", `{"name":"one-b"}`)
	stillWaiting(t, first)
	stillWaiting(t, second)
	if err := sup.Steer("one", "look at the importer instead", SteerFromParent); err != nil {
		t.Fatal(err)
	}
	for _, out := range []<-chan string{first, second} {
		if text := answer(t, out); !strings.HasPrefix(text, "Woken by a steer") {
			t.Fatalf("a steered agent's wait should end:\n%s", text)
		}
	}
	// The message itself is still the agent's, for its next round.
	if n := sup.QueuedSteering("one"); n != 1 {
		t.Fatalf("the steer should stay queued for the agent, got %d", n)
	}
	for _, name := range []string{"one", "one-a", "one-b"} {
		env.finish(name)
	}
}

// A follow-up started while a set is waited on is waited for: the wait reads
// the child's current turn, not the one it answered before.
func TestAWaitOnASetWaitsForAFollowUp(t *testing.T) {
	sup, env := newHeldSupervisor(t)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","name":"two"}`)
	env.finish("one")
	waitState(t, sup, "one", StateDone)
	// The release is closed, so the follow-up answers at once; what is being
	// checked is that the wait returns the follow-up's turn and not a stale
	// channel's, which the report carries in its turn count.
	if err := sup.Steer("one", "and the tests?", SteerFromParent); err != nil {
		t.Fatal(err)
	}
	text := answer(t, waitAsync(sup, "", `{"names":["two","one"]}`))
	if !strings.Contains(text, "report of one") {
		t.Fatalf("the wait should return the follow-up's answer:\n%s", text)
	}
	if st := statusOf(t, sup, "one"); st.State != StateDone {
		t.Fatalf("the wait should have returned after the follow-up answered, got %+v", st)
	}
	env.finish("two")
}

// Close while a set is waited on ends the wait.
func TestAWaitOnASetEndsWhenTheSupervisorCloses(t *testing.T) {
	sup, _ := newHeldSupervisor(t)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"a","name":"one"}`)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"b","name":"two"}`)
	out := waitAsync(sup, "", `{"names":["one","two"]}`)
	stillWaiting(t, out)
	go sup.Close()
	if text := answer(t, out); !strings.Contains(text, "cancelled") {
		t.Fatalf("a wait should end with the supervisor:\n%s", text)
	}
}
