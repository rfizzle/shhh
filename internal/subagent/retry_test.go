package subagent

// Retrying a failed child. The manager's `[r]` is only worth offering
// if the retry is a real second attempt: the same task, a fresh conversation,
// a fresh budget, and the failed attempt still there to read.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestRetryRunsTheFailedChildAgainOnItsTask(t *testing.T) {
	// No scripted steps: the first stream fails, which fails the child.
	env := &scriptedEnv{}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	failed, _ := sup.Get("researcher-1")
	if !strings.HasPrefix(failed.Detail, "failed · ") {
		t.Fatalf("a failed child must say why: %q", failed.Detail)
	}

	env.mu.Lock()
	env.steps = []streamStep{{text: "the loop lives in internal/agent"}}
	env.mu.Unlock()

	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	st, _ := sup.Get("researcher-1")
	if st.Task != "survey the loop" {
		t.Fatalf("the retry must run the original task, got %q", st.Task)
	}
	if st.Summary != "the loop lives in internal/agent" {
		t.Fatalf("the retry's report did not land: %q", st.Summary)
	}
	// The attempt restarts; the record of it does not. The failed attempt and
	// the reason it failed stay readable above the retry.
	entries := sup.Transcript("researcher-1")
	if !transcriptHas(entries, EntrySystem, "Retrying — the previous attempt failed · ") {
		t.Fatalf("transcript missing the retry note: %+v", entries)
	}
	if !transcriptHas(entries, EntryUser, "survey the loop") {
		t.Fatalf("transcript lost the original task: %+v", entries)
	}
}

func TestRetryOnlyAppliesToAFailedAgent(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: blockedForeverEnv()})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"long survey"}`)
	waitState(t, sup, "researcher-1", StateRunning)

	if err := sup.Retry("researcher-1"); err == nil {
		t.Fatal("a running agent must not be retried")
	} else if !strings.Contains(err.Error(), "running") {
		t.Fatalf("the refusal must name the state, got %q", err)
	}
	if err := sup.Retry("nobody"); err == nil {
		t.Fatal("an unknown agent must not be retried")
	}
}

// TestRetryCarriesSpendAndResetsTheBudget covers the one case a naive retry
// gets wrong: a child killed by its token budget, retried against the spend
// that killed it, fails again before it has done anything. Each attempt is
// measured against its own budget; the money is still counted.
func TestRetryCarriesSpendAndResetsTheBudget(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "over budget", usage: &provider.Usage{PromptTokens: 300100, CompletionTokens: 0}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	spent, _ := sup.Get("researcher-1")
	if spent.Spend.In != 300100 {
		t.Fatalf("first attempt spend = %d, want 300100", spent.Spend.In)
	}

	env.mu.Lock()
	env.steps = []streamStep{{text: "done", usage: &provider.Usage{PromptTokens: 100}}}
	env.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	st, _ := sup.Get("researcher-1")
	if st.Spend.In != 300200 {
		t.Fatalf("spend after the retry = %d, want 300200 (the earlier attempt is still counted)", st.Spend.In)
	}
	if st.ToolCalls != 0 || st.Step != 0 {
		t.Fatalf("the retry must start its own progress, got %d tools / step %d", st.ToolCalls, st.Step)
	}
}

// blockedForeverEnv builds children whose stream stays open until the child's
// context is cancelled, so a child is observably running rather than racing
// to finish.
func blockedForeverEnv() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
		}, nil
	}
}

// TestRetryWaitsForTheAttemptItReplaces drives a retry into the window
// between a child failing and its attempt letting go. The recorder's End is
// the teardown step the test holds open; a real one is a worktree being
// removed, which is a git process and is exactly as slow as the machine is
// busy.
func TestRetryWaitsForTheAttemptItReplaces(t *testing.T) {
	env := &scriptedEnv{}
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	sup := New(context.Background(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(Spec, string) Recorder {
			return Recorder{End: func(observe.ChildEnd) { <-release }}
		},
	})
	t.Cleanup(sup.Close)
	t.Cleanup(releaseOnce)

	// No scripted steps: the first stream fails, which fails the child.
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = []streamStep{{text: "the loop lives in internal/agent"}}
	env.mu.Unlock()

	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("a retry pressed while the attempt is still stopping must wait, not refuse: %v", err)
	}
	st, _ := sup.Get("researcher-1")
	if st.State != StateQueued || !strings.Contains(st.Detail, "waiting for the last attempt to stop") {
		t.Fatalf("the lane must say what it is waiting for, got %s / %q", st.State, st.Detail)
	}

	releaseOnce()
	waitState(t, sup, "researcher-1", StateDone)
	done, _ := sup.Get("researcher-1")
	if done.Summary != "the loop lives in internal/agent" {
		t.Fatalf("the waiting retry never ran: %q", done.Summary)
	}
}

// TestTheRetryOfferArrivesAfterTheAttemptHasStopped covers the other half:
// the ordinary press answers the failure event, so that event must not go out
// while the attempt is still holding its workspace. Nobody pressing `[r]` can
// see a teardown, and a refusal they can only answer by pressing again is not
// an answer.
func TestTheRetryOfferArrivesAfterTheAttemptHasStopped(t *testing.T) {
	env := &scriptedEnv{}
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	sup := New(context.Background(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(Spec, string) Recorder {
			return Recorder{End: func(observe.ChildEnd) { <-release }}
		},
	})
	t.Cleanup(sup.Close)
	t.Cleanup(releaseOnce)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	settle := time.After(100 * time.Millisecond)
	for waiting := true; waiting; {
		select {
		case ev := <-sup.Events():
			if ev.Kind == EventDone {
				t.Fatal("the retry was offered while the attempt was still stopping")
			}
		case <-settle:
			waiting = false
		}
	}

	releaseOnce()
	c := sup.byName["researcher-1"]
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sup.Events():
			if ev.Kind != EventDone {
				continue
			}
			if ev.Status.State != StateFailed {
				t.Fatalf("the failure event says %s", ev.Status.State)
			}
			select {
			case <-c.done:
			default:
				t.Fatal("the failure event went out before the attempt let go")
			}
			return
		case <-deadline:
			t.Fatal("the failure event never arrived")
		}
	}
}

// TestARetryThatCannotStartLeavesTheOfferStanding covers the way out of the
// wait. A teardown that never finishes must not leave a child queued behind
// it with no way to ask again: the child goes back to the failure it had,
// with the reason on its transcript, and `[r]` still means something.
func TestARetryThatCannotStartLeavesTheOfferStanding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	env := &scriptedEnv{}
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	sup := New(ctx, Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(Spec, string) Recorder {
			return Recorder{End: func(observe.ChildEnd) { <-release }}
		},
	})
	t.Cleanup(sup.Close)
	t.Cleanup(releaseOnce)
	t.Cleanup(cancel)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)
	failed, _ := sup.Get("researcher-1")

	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// A second press while the first is still waiting starts nothing: the
	// child is queued, and a queued child is refused like any other live one.
	if err := sup.Retry("researcher-1"); err == nil {
		t.Fatal("a second retry must not start a second attempt")
	} else if !strings.Contains(err.Error(), "queued") {
		t.Fatalf("the refusal must name the state, got %q", err)
	}

	cancel()
	waitState(t, sup, "researcher-1", StateFailed)
	back, _ := sup.Get("researcher-1")
	if back.Detail != failed.Detail {
		t.Fatalf("the child must go back to the failure it had, got %q want %q", back.Detail, failed.Detail)
	}
	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "The retry did not start — ") {
		t.Fatalf("the transcript must say why nothing happened: %+v", sup.Transcript("researcher-1"))
	}
}

// TestAKillDuringTheWaitStopsTheRetry: a child waiting out its own teardown
// is not finished, so the manager still offers to kill it. The kill has to
// land on the retry — the attempt it would otherwise cancel has already
// stopped — or the child starts again after being told to stop.
func TestAKillDuringTheWaitStopsTheRetry(t *testing.T) {
	env := &scriptedEnv{}
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	sup := New(context.Background(), Options{
		Root:   t.TempDir(),
		NewEnv: env.factory(),
		Record: func(Spec, string) Recorder {
			return Recorder{End: func(observe.ChildEnd) { <-release }}
		},
	})
	t.Cleanup(sup.Close)
	t.Cleanup(releaseOnce)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	// A second attempt would succeed, so a retry that ran anyway would show.
	env.mu.Lock()
	env.steps = []streamStep{{text: "the loop lives in internal/agent"}}
	env.mu.Unlock()

	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := sup.Kill("researcher-1"); err != nil {
		t.Fatalf("a waiting child must be killable: %v", err)
	}
	waitState(t, sup, "researcher-1", StateFailed)
	releaseOnce()

	// Nothing starts after the kill: the teardown it was waiting for has
	// finished and the child is still where the kill left it.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if st, _ := sup.Get("researcher-1"); st.State != StateFailed {
			t.Fatalf("the killed retry started anyway (%s)", st.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "The retry did not start — the agent was killed.") {
		t.Fatalf("the transcript must say the kill stopped it: %+v", sup.Transcript("researcher-1"))
	}
}

// openingTurn is the user message the child's first request of this attempt
// carried — what the attempt was actually given, as against the task it was
// spawned on.
func (s *scriptedEnv) openingTurn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return ""
	}
	for _, m := range s.requests[0] {
		if m.Role == provider.RoleUser {
			return m.Content
		}
	}
	return ""
}

// A retry on the identical prompt is the same attempt run twice: it takes the
// same first steps and, on a budget failure, spends the same budget the same
// way. The conversation is still fresh — an attempt that inherited the
// context that killed it would die of it again — but it opens with the two
// facts that cost nothing to carry, and the task follows verbatim so the
// child cannot read the prologue as an amendment to what it was asked for.
func TestARetryIsToldHowTheLastAttemptEndedAndWhatItLeft(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "the exporter is half converted; the CSV writer is untouched",
			usage: &provider.Usage{PromptTokens: 300100}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"convert the exporter","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = []streamStep{{text: "converted the CSV writer"}}
	env.requests = nil
	env.mu.Unlock()

	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	opening := env.openingTurn()
	if !strings.HasPrefix(opening, "A previous attempt at this task ended: failed · token budget") {
		t.Fatalf("the retry must open with how the last attempt ended:\n%s", opening)
	}
	if !strings.Contains(opening, "the exporter is half converted") {
		t.Fatalf("the retry must carry what the last attempt handed over:\n%s", opening)
	}
	if !strings.HasSuffix(opening, "convert the exporter") {
		t.Fatalf("the task must follow verbatim, last:\n%s", opening)
	}
	// The task itself is untouched: it is what every reading of this child is
	// judged against and what its roster row states.
	if st, _ := sup.Get("researcher-1"); st.Task != "convert the exporter" {
		t.Fatalf("the prologue reached the task, got %q", st.Task)
	}
	// This child is a reader, which never had a workspace of its own: a
	// prologue that told one its copy of the checkout was gone would send it
	// off re-establishing something it never lost.
	if strings.Contains(opening, "workspace") {
		t.Fatalf("a reader is told about a workspace it never had:\n%s", opening)
	}
}

// A child stopped by its budget and restarted on the same one stops at the
// same place, which makes the retry a full budget spent to learn nothing. It
// grows by the step the round cap already grows by, and the lane says so —
// a budget that changed silently is one nobody can reconcile against the
// spawn that set it.
func TestABudgetExhaustedRetryIsGivenMoreThanKilledIt(t *testing.T) {
	env := &scriptedEnv{steps: []streamStep{
		{text: "out of room", usage: &provider.Usage{PromptTokens: 300100}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"convert the exporter","max_tokens":300000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = []streamStep{{text: "converted it"}}
	env.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	if !transcriptHas(sup.Transcript("researcher-1"), EntrySystem, "~600k new tokens, up from ~300k") {
		t.Fatalf("the retry row must say the budget grew and by how much: %+v", sup.Transcript("researcher-1"))
	}
	c := sup.byName["researcher-1"]
	c.mu.Lock()
	budget := c.maxTokens
	c.mu.Unlock()
	if budget != 600000 {
		t.Fatalf("the second attempt's budget = %d, want 600000", budget)
	}

	// A child that failed for any other reason was not short of attention.
	if got, grew := retryBudget(200_000, false); grew || got != 200_000 {
		t.Fatalf("a budget that did not stop the child must not grow, got %d (%v)", got, grew)
	}
	// And the growth stops where a spawn's own ceiling does — including from
	// under it, where the step would carry a budget past the bound. The row
	// says both numbers rather than the step for this case: the attempt above
	// half the ceiling grows, and by less than the step.
	if got, grew := retryBudget(MaxTokensCeiling/2*3/2, true); got != MaxTokensCeiling || !grew {
		t.Fatalf("a budget under the ceiling must grow to it, got %d (%v)", got, grew)
	}
	if got, grew := retryBudget(MaxTokensCeiling, true); got != MaxTokensCeiling || grew {
		t.Fatalf("a budget at the ceiling has nowhere to grow, got %d (%v)", got, grew)
	}
}

// The parent can act on a failed child itself. A replacement spawn costs one
// of the session's sixteen slots and starts from nothing; a retry costs none
// and starts from what the failed attempt left, which is why the tool exists
// at all.
func TestTheParentCanRetryAFailedChildWithoutSpendingASlot(t *testing.T) {
	env := &scriptedEnv{}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey the loop"}`)
	waitState(t, sup, "researcher-1", StateFailed)

	env.mu.Lock()
	env.steps = []streamStep{{text: "the loop lives in internal/agent"}}
	env.mu.Unlock()

	out := execTool(t, sup, RetryToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(out, "no agent slot") {
		t.Fatalf("the parent must be told a retry costs no slot: %s", out)
	}
	waitState(t, sup, "researcher-1", StateDone)

	if n := len(sup.Snapshot()); n != 1 {
		t.Fatalf("a retry must not start a second agent, got %d", n)
	}
	report := execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)
	if !strings.Contains(report, "the loop lives in internal/agent") {
		t.Fatalf("the retried child's report never reached the parent:\n%s", report)
	}
	if !strings.Contains(execTool(t, sup, ReportToolName, `{}`), "1 of 16 agent slots used") {
		t.Fatal("a retried child holds the one slot it always held")
	}
}

// The refusals are the supervisor's, so the parent is told what state the
// child is in rather than quietly given nothing.
func TestTheRetryToolRefusesWhatTheSupervisorRefuses(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: blockedForeverEnv()})
	t.Cleanup(sup.Close)
	exec := sup.WrapExecutor(nil)

	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"long survey"}`)
	waitState(t, sup, "researcher-1", StateRunning)
	if _, err := exec(RetryToolName, json.RawMessage(`{"name":"researcher-1"}`)); err == nil {
		t.Fatal("a running agent must not be retried")
	} else if !strings.Contains(err.Error(), "running") {
		t.Fatalf("the refusal must name the state, got %q", err)
	}
	if _, err := exec(RetryToolName, json.RawMessage(`{"name":"ghost"}`)); err == nil {
		t.Fatal("an unknown agent must not be retried")
	}
	if _, err := exec(RetryToolName, json.RawMessage(`{}`)); err == nil {
		t.Fatal("a retry with no name has nothing to run")
	}
}
