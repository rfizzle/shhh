package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/tools"
)

// slotChecks scripts children that each run one heavy command and answer.
// A command holds until the test lets that child's run finish, so how many
// are running at once, and in what order they began, can be read while the
// rest wait.
type slotChecks struct {
	mu      sync.Mutex
	running int
	most    int
	began   []string
	finish  map[string]chan struct{}
}

func (w *slotChecks) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		round := 0
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			round++
			ch := make(chan provider.StreamEvent, 2)
			if round == 1 {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: spec.Name + "-c",
					Name: tools.ExecCommandName, Arguments: `{"command":"go test ./..."}`}}}
			} else {
				ch <- provider.StreamEvent{Token: spec.Name + " done"}
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", nil },
			Gated:        map[string]bool{tools.ExecCommandName: true},
			RunCommand: func(ctx context.Context, command string) tools.ExecResult {
				w.mu.Lock()
				w.running++
				w.most = max(w.most, w.running)
				w.began = append(w.began, spec.Name)
				done := w.finish[spec.Name]
				w.mu.Unlock()
				select {
				case <-done:
				case <-ctx.Done():
				}
				w.mu.Lock()
				w.running--
				w.mu.Unlock()
				return tools.InferExecResult("ok", 0)
			},
		}, nil
	}
}

func (w *slotChecks) started() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.began...)
}

// Four children whose commands are test runs, under two check slots: two run
// at once and no more, the other two hold — parked, their lanes saying why —
// and they start in the order they asked, each as a slot comes back.
// See docs/capabilities/subagents.md#what-they-share.
func TestChecksTakeTurnsForTheSessionsSlots(t *testing.T) {
	names := []string{"researcher-1", "researcher-2", "researcher-3", "researcher-4"}
	w := &slotChecks{finish: map[string]chan struct{}{}}
	for _, n := range names {
		w.finish[n] = make(chan struct{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sup := New(ctx, Options{Root: t.TempDir(), NewEnv: w.factory(), MaxConcurrent: 4,
		CheckSlots: 2, CommandAllowlist: []string{"go test"}})
	t.Cleanup(sup.Close)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == EventAsk {
					ev.Ask.Respond(true)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	waiting := func(name string) bool {
		st, ok := sup.Get(name)
		return ok && st.Held && st.SlotWait == 2 && st.State == StateRunning &&
			st.Detail == "waiting for a check slot (2 running)"
	}
	// One at a time, so the order the four ask for a slot is the order they
	// were spawned in.
	for i, n := range names {
		execTool(t, sup, SpawnToolName, fmt.Sprintf(`{"role":"researcher","task":"run the tests %d"}`, i))
		if i < 2 {
			waitFor(t, func() bool { return len(w.started()) == i+1 })
		} else {
			waitFor(t, func() bool { return waiting(n) })
		}
	}
	if got := w.started(); !slices.Equal(got, names[:2]) {
		t.Fatalf("running = %v, want the first two alone", got)
	}

	close(w.finish["researcher-1"])
	waitFor(t, func() bool { return len(w.started()) == 3 })
	if got := w.started()[2]; got != "researcher-3" {
		t.Fatalf("the first slot back went to %s, want researcher-3", got)
	}
	if !waiting("researcher-4") {
		t.Fatal("researcher-4 stopped holding before a slot came back for it")
	}
	if st, _ := sup.Get("researcher-3"); st.Held || st.SlotWait != 0 {
		t.Fatalf("researcher-3 still reads as waiting once it runs: %+v", st)
	}

	close(w.finish["researcher-2"])
	waitFor(t, func() bool { return len(w.started()) == 4 })
	close(w.finish["researcher-3"])
	close(w.finish["researcher-4"])
	for _, n := range names {
		waitFor(t, func() bool { st, _ := sup.Get(n); return st.State == StateDone })
	}
	w.mu.Lock()
	most := w.most
	w.mu.Unlock()
	if most != 2 {
		t.Fatalf("at most %d ran at once, want 2", most)
	}
	if got := w.started(); !slices.Equal(got, names) {
		t.Fatalf("began in %v, want %v", got, names)
	}
}

// A command that is not a check takes no slot, and a quality gate run does,
// while re-reading the last verdict does not.
func TestOnlyChecksTakeASlot(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), CheckSlots: 1,
		CheckCommands: func() []string { return []string{"make lint"} }})
	t.Cleanup(sup.Close)
	for cmd, want := range map[string]bool{
		"go test ./...":               true,
		"cd api && go build ./cmd/x":  true,
		"make lint":                   true,
		"make lint FIX=1":             true,
		"git status":                  false,
		"echo go test":                false,
		"gofmt -l internal/subagent/": false,
	} {
		if got := sup.isCheck(cmd); got != want {
			t.Errorf("isCheck(%q) = %v, want %v", cmd, got, want)
		}
	}
	if !gateRun(json.RawMessage(`{"action":"run"}`)) || gateRun(json.RawMessage(`{"action":"result"}`)) {
		t.Error("a gate run takes a slot and a re-read of its result does not")
	}

	// The session's own gate draws from the same slots: held by it, a
	// child's run has to wait; given back, the child's goes.
	release, ok := sup.CheckSlot(context.Background())
	if !ok {
		t.Fatal("the session's gate could not take a free slot")
	}
	c := &child{name: "researcher-1"}
	ran := make(chan struct{})
	env := sup.throttled(context.Background(), c, Env{
		Executor: func(name string, _ json.RawMessage) (string, error) {
			close(ran)
			return "", nil
		},
	})
	go func() { _, _ = env.Executor(quality.ToolName, json.RawMessage(`{"action":"run"}`)) }()
	waitFor(t, func() bool { return c.status().SlotWait == 1 })
	select {
	case <-ran:
		t.Fatal("the child's gate run went while the session's held the only slot")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	<-ran
}

// A wait given up hands the slot it was about to be given on, rather than
// leaving the throttle a slot short.
func TestAWaitGivenUpLosesNoSlot(t *testing.T) {
	s := NewCheckSlots(1)
	release, _ := s.Take(context.Background(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() {
		_, ok := s.Take(ctx, nil)
		done <- ok
	}()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.queue) == 1 })
	cancel()
	if <-done {
		t.Fatal("a cancelled wait reported a slot")
	}
	release()
	if _, ok := s.Take(context.Background(), nil); !ok {
		t.Fatal("the slot was lost with the wait")
	}
}

// One round can ask for several checks at once, and the child reads as
// waiting while any of them does, not only until the first is let through.
func TestAChildWithTwoChecksWaitingStillReadsAsWaiting(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), CheckSlots: 1})
	t.Cleanup(sup.Close)
	c := &child{name: "writer-1"}
	hold := make(chan struct{})
	var mu sync.Mutex
	ran := 0
	env := sup.throttled(context.Background(), c, Env{
		RunCommand: func(context.Context, string) tools.ExecResult {
			mu.Lock()
			ran++
			mu.Unlock()
			<-hold
			return tools.InferExecResult("ok", 0)
		},
	})
	release, _ := sup.CheckSlot(context.Background())
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			env.RunCommand(context.Background(), "go test ./...")
			done <- struct{}{}
		}()
	}
	waitFor(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.slotWaiters == 2 })
	release()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return ran == 1 })
	if st := c.status(); !st.Held || st.SlotWait != 1 {
		t.Fatalf("one check still waits, yet the child reads %+v", st)
	}
	close(hold)
	<-done
	<-done
	if st := c.status(); st.Held || st.SlotWait != 0 {
		t.Fatalf("no check waits, yet the child reads held: %+v", st)
	}
}
