package subagent

// Retrying a failed child. The manager's `[r]` is only worth offering
// if the retry is a real second attempt: the same task, a fresh conversation,
// a fresh budget, and the failed attempt still there to read.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
		{text: "over budget", usage: &provider.Usage{PromptTokens: 4000, CompletionTokens: 0}},
	}}
	sup := newTestSupervisor(t, env)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"survey","max_tokens":2000}`)
	waitState(t, sup, "researcher-1", StateFailed)

	spent, _ := sup.Get("researcher-1")
	if spent.TokensIn != 4000 {
		t.Fatalf("first attempt spend = %d, want 4000", spent.TokensIn)
	}

	env.mu.Lock()
	env.steps = []streamStep{{text: "done", usage: &provider.Usage{PromptTokens: 100}}}
	env.mu.Unlock()
	if err := sup.Retry("researcher-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitState(t, sup, "researcher-1", StateDone)

	st, _ := sup.Get("researcher-1")
	if st.TokensIn != 4100 {
		t.Fatalf("spend after the retry = %d, want 4100 (the earlier attempt is still counted)", st.TokensIn)
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
		stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
