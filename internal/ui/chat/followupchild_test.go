package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// answeringEnv builds children that answer every request at once with the
// next of answers, and block once the answers run out — so a finished child
// asked a follow-up is running for as long as a test needs to look at it.
func answeringEnv(answers ...string) subagent.EnvFactory {
	var mu sync.Mutex
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			mu.Lock()
			var text string
			if len(answers) > 0 {
				text, answers = answers[0], answers[1:]
			}
			mu.Unlock()
			if text == "" {
				ch := make(chan provider.StreamEvent)
				go func() {
					<-ctx.Done()
					close(ch)
				}()
				return ch, func() {}, nil
			}
			ch := make(chan provider.StreamEvent, 2)
			ch <- provider.StreamEvent{Token: text}
			ch <- provider.StreamEvent{Done: true}
			close(ch)
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
		}, nil
	}
}

// A finished child is asked again from its row in the manager: [s] reads as a
// follow-up there, what is typed reaches the child's own conversation, and
// the row goes back to running with the question under it.
// See docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it.
func TestAFinishedChildIsAskedAFollowUpFromItsRow(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: answeringEnv("the rails are one component")})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnInto(t, sup, `{"role":"researcher","task":"survey internal/ui"}`)
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateDone
	})

	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt}, tea.KeyPressMsg{Code: tea.KeyDown})
	if view := m.View().Content; !strings.Contains(ansi.Strip(view), "[s] follow up") {
		t.Fatalf("a finished child's row must offer the follow-up:\n%s", view)
	}
	m = pressKeys(t, m, key('s'))
	for _, r := range "which one owns the rail?" {
		m = pressKeys(t, m, key(r))
	}
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	st, _ := sup.Get("researcher-1")
	if st.State == subagent.StateDone || st.FollowUp != "which one owns the rail?" || st.SteerFrom != subagent.SteerFromLane {
		t.Fatalf("the follow-up must reach the child from the person's lane, got %+v", st)
	}
	m.agentList.Rows, _ = m.buildAgentRows()
	if view := m.View().Content; !strings.Contains(view, "follow-up · which one owns the rail?") {
		t.Fatalf("the row must be running on the question:\n%s", view)
	}
}

// A settled fan-out is frozen into the render cache, and a follow-up sets one
// of its children moving again with no row landing: the lane has to be drawn
// again rather than kept saying done.
func TestAFollowUpReopensAFrozenLane(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(),
		NewEnv: answeringEnv("first report", "second report")})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.beginSpawnBatch()
	for _, task := range []string{"survey the loop", "survey the tests"} {
		spawnInto(t, sup, `{"role":"researcher","task":"`+task+`"}`)
		m.appendSpawnEntry(spawnRowEntry(task))
	}
	waitFor(t, func() bool {
		a, _ := sup.Get("researcher-1")
		b, _ := sup.Get("researcher-2")
		return a.State == subagent.StateDone && b.State == subagent.StateDone
	})
	// The next turn after the block, so the settled block is behind the last
	// one and frozen.
	m.appendEntry(entry{kind: entryUser, text: "later"})
	laneLine := func() string {
		for _, line := range m.renderHistoryLines() {
			if strings.Contains(line, "researcher-1") {
				return line
			}
		}
		return ""
	}
	if line := laneLine(); !strings.Contains(line, "done") {
		t.Fatalf("the lane should say done before the follow-up: %q", line)
	}

	if err := sup.Steer("researcher-1", "and the retries?", subagent.SteerFromParent); err != nil {
		t.Fatal(err)
	}
	st, _ := sup.Get("researcher-1")
	updated, _ := m.Update(subagentEventMsg{ev: subagent.Event{Kind: subagent.EventUpdate, Status: st}})
	m = updated.(Model)
	if line := laneLine(); strings.Contains(line, "done") {
		t.Fatalf("the lane should be drawn again once its child is asked a follow-up: %q", line)
	}
}
