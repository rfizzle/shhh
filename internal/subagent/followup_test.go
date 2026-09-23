package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// A child that has answered is asked something else, and answers on the
// conversation it already holds: the question joins that conversation, the
// answer replaces the report, and the report it replaced is kept under the
// turn it closed. A wait started straight after the steer waits for the new
// answer rather than being handed the old one.
// See docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it.
func TestAFinishedChildTakesAFollowUpInItsOwnConversation(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "the exporter lives in otel.go"},
		{text: "it retries twice, then pauses"},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the exporter"}`)
	waitState(t, sup, "researcher-1", StateDone)

	st := statusOf(t, sup, "researcher-1")
	if !st.TakesFollowUp {
		t.Fatalf("a finished child should say it takes a follow-up, got %+v", st)
	}
	if roster := execTool(t, sup, ReportToolName, `{}`); !strings.Contains(roster, "researcher-1") ||
		!strings.Contains(roster, "done · takes a follow-up") {
		t.Fatalf("the roster should offer the follow-up:\n%s", roster)
	}

	reply := execTool(t, sup, SteerToolName, `{"name":"researcher-1","message":"how often does it retry?"}`)
	if !strings.Contains(reply, "follow-up") {
		t.Fatalf("the steer tool should say the message starts a follow-up, got %q", reply)
	}
	// Claimed where it was sent: the state and the words move at once.
	st = statusOf(t, sup, "researcher-1")
	if st.State == StateDone || st.FollowUp != "how often does it retry?" {
		t.Fatalf("the follow-up should be claimed as it is sent, got %+v", st)
	}

	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "it retries twice") || strings.Contains(report, "otel.go") {
		t.Fatalf("the wait should return the follow-up's answer alone:\n%s", report)
	}
	earlier := sup.EarlierReports("researcher-1")
	if len(earlier) != 1 || earlier[0].Turn != 1 || earlier[0].Text != "the exporter lives in otel.go" {
		t.Fatalf("the replaced report should be kept under its turn, got %+v", earlier)
	}

	// One conversation: the second request carried the first answer and the
	// question after it.
	env.mu.Lock()
	requests := env.requests
	env.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two requests, got %d", len(requests))
	}
	var sawAnswer, sawQuestion bool
	for _, m := range requests[1] {
		sawAnswer = sawAnswer || (m.Role == provider.RoleAssistant && strings.Contains(m.Content, "otel.go"))
		sawQuestion = sawQuestion || (m.Role == provider.RoleUser && strings.Contains(m.Content, "how often"))
	}
	if !sawAnswer || !sawQuestion {
		t.Fatalf("the follow-up should run on the child's own conversation: %+v", requests[1])
	}

	st = statusOf(t, sup, "researcher-1")
	if st.State != StateDone || st.FollowUp != "" || st.SteerFrom != SteerFromParent || st.Steers != 0 {
		t.Fatalf("the child should be done again, its follow-up said by the parent, got %+v", st)
	}
	if st.Summary != "it retries twice, then pauses" {
		t.Fatalf("the lane's summary should be the new report's, got %q", st.Summary)
	}
}

// A follow-up is one more turn on the same attempt: the record row it writes
// to is the one the first turn opened, and it gets a second turn event rather
// than a second row.
func TestAFollowUpIsAnotherTurnOnTheSameRecord(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{{text: "first"}, {text: "second"}}}
	rec := &testRecorder{}
	var opened atomic.Int32
	sup := New(t.Context(), Options{Root: t.TempDir(), NewEnv: env.factory(),
		Record: func(Spec, string) Recorder { opened.Add(1); return rec.recorder() }})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code"}`)
	waitState(t, sup, "researcher-1", StateDone)
	if err := sup.Steer("researcher-1", "and the tests?", SteerFromLane); err != nil {
		t.Fatal(err)
	}
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if n := opened.Load(); n != 1 {
		t.Fatalf("a follow-up should open no row of its own, got %d rows", n)
	}
	var turns []int64
	for _, e := range rec.of("turn") {
		if e.outcome == observe.TurnDone {
			turns = append(turns, e.pos.Turn)
		}
	}
	if !slices.Equal(turns, []int64{1, 2}) {
		t.Fatalf("the row should close turn 1 and then turn 2, got %v", turns)
	}
	var done int
	for _, e := range rec.of("signal") {
		if e.outcome == observe.SignalSubagent && e.reason == observe.ChildDone {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("answering again is not a second ending, got %d done signals", done)
	}
}

// What is left of the budget has to cover the reserve a turn is admitted
// with, and the refusal names the figure the way admission does.
func TestAFollowUpIsRefusedOnAnExhaustedBudget(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "found it", usage: &provider.Usage{PromptTokens: 150_000, CompletionTokens: 10}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the code","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateDone)

	err := sup.Steer("researcher-1", "one more thing", SteerFromLane)
	if err == nil || !strings.Contains(err.Error(), "working reserve") || !strings.Contains(err.Error(), "150k") {
		t.Fatalf("a child short of the reserve should refuse naming what is left, got %v", err)
	}
	if st := statusOf(t, sup, "researcher-1"); st.State != StateDone {
		t.Fatalf("a refused follow-up should leave the child done, got %s", st.State)
	}
}

// A steer racing the supervisor's close is either refused or taken, and
// never leaves a child claimed by a follow-up nobody will run: the close
// still returns, and a steer after it is refused as closed.
func TestAFollowUpRacingCloseIsRefusedOrEnded(t *testing.T) {
	for range 20 {
		env := &scriptedEnv{steps: []streamStep{{text: "first"}, {text: "second"}}}
		sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: env.factory()})
		execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey"}`)
		waitState(t, sup, "researcher-1", StateDone)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sup.Steer("researcher-1", "again", SteerFromParent)
		}()
		sup.Close()
		wg.Wait()
		if err := sup.Steer("researcher-1", "after", SteerFromParent); !errors.Is(err, ErrClosed) {
			t.Fatalf("a steer after close should be refused as closed, got %v", err)
		}
		switch st := statusOf(t, sup, "researcher-1"); st.State {
		case StateDone, StateFailed:
		default:
			t.Fatalf("a closed supervisor left its child %s", st.State)
		}
	}
}

// A writer asked a follow-up keeps its copy of the workspace, and what it
// hands back the second time is what it wrote the second time: the patch
// that already landed is the copy's base, not part of the next one.
func TestAFollowedUpWriterHandsBackOnlyItsNewWork(t *testing.T) {
	repo := initTestRepo(t)
	w := &followUpWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(ctx, Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	asks := make(chan *Ask, 2)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				if ev.Kind == EventAsk && ev.Ask.Kind == AskPatch {
					asks <- ev.Ask
					ev.Ask.Respond(true)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if _, err := spawnRaw(sup, `{"role":"writer","task":"add a.go","paths":["a.go","b.go"]}`); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "writer-1", StateDone)
	first := <-asks
	if !slices.Equal(first.Files, []string{"a.go"}) {
		t.Fatalf("the first patch should be a.go, got %v", first.Files)
	}
	if _, err := sup.WorktreeDiff("writer-1"); err != nil {
		t.Fatalf("a finished writer should keep its copy while it can be spoken to: %v", err)
	}

	if err := sup.Steer("writer-1", "now add b.go", SteerFromParent); err != nil {
		t.Fatal(err)
	}
	execTool(t, sup, ReportToolName, `{"name":"writer-1"}`)
	select {
	case second := <-asks:
		if !slices.Equal(second.Files, []string{"b.go"}) {
			t.Fatalf("the follow-up's patch should be its own work, got %v", second.Files)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the follow-up's patch was never put to the card")
	}
	for _, f := range []string{"a.go", "b.go"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s should have landed: %v", f, err)
		}
	}
}

// followUpWriter writes a.go on its first turn and b.go on its second, one
// round each, then answers.
type followUpWriter struct {
	mu    sync.Mutex
	round int
}

func (w *followUpWriter) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			w.mu.Lock()
			w.round++
			round := w.round
			w.mu.Unlock()
			ch := make(chan provider.StreamEvent, 2)
			switch round {
			case 1, 3:
				file := map[int]string{1: "a.go", 3: "b.go"}[round]
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
					{ID: fmt.Sprintf("w%d", round), Name: "write_file", Arguments: `{"path":"` + file + `"}`},
				}}
			default:
				ch <- provider.StreamEvent{Token: "wrote it"}
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor: func(_ string, args json.RawMessage) (string, error) {
				var a struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(args, &a)
				return "written", os.WriteFile(filepath.Join(spec.Root, a.Path), []byte("package x\n"), 0o644)
			},
		}, nil
	}
}
