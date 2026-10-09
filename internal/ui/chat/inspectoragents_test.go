package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// TestInspectorAgents_CarriesWhatAChildIsAboutToRunOutOf: the three facts the
// map draws that nothing else on the rail can say about a child — how much of
// its budget it has taken in, how many times this turn it has been told it
// left its task, and whether a replacement could pick up where it stopped.
// They are read off the supervisor's own status rather than derived here, so
// the row cannot come to disagree with the manager beside it.
func TestInspectorAgents_CarriesWhatAChildIsAboutToRunOutOf(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")

	agents := m.inspectorAgents()
	if len(agents) != 2 {
		t.Fatalf("the map is the orchestrator and its child: %+v", agents)
	}
	child, st := agents[1], func() subagent.Status {
		st, ok := sup.Get("researcher-1")
		if !ok {
			t.Fatal("the child is in the snapshot")
		}
		return st
	}()
	if child.Budget != st.Budget || child.Fresh != st.Tokens.Fresh {
		t.Fatalf("the budget and the intake are the supervisor's: %+v against %d/%d",
			child, st.Tokens.Fresh, st.Budget)
	}
	if child.Budget <= 0 {
		t.Fatalf("a bounded child has a ceiling to draw against: %d", child.Budget)
	}
	if child.Steers != st.Steers || child.Handoff != (st.Handoff != "") {
		t.Fatalf("the steer count and the handoff are the supervisor's: %+v", child)
	}
	// A child the session started itself is one level down, which is the
	// depth that draws no corner.
	if child.Depth != 1 {
		t.Fatalf("a child the session spawned sits one level under it: %d", child.Depth)
	}
	if agents[0].Depth != 0 {
		t.Fatalf("the orchestrator is the level everything else is under: %d", agents[0].Depth)
	}
}

// TestInspectorAgents_TrailerIsTheRegistersOwnLetters: the row under the map
// names the keys that reach the sessions it draws, and it names them from the
// register rather than spelling them out, so a rebind moves the row with it.
func TestInspectorAgents_TrailerIsTheRegistersOwnLetters(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	updated, _ := newSubagentModel(t, sup).Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m := updated.(Model)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")

	hint := m.resolveInspector().AgentsHint
	for _, want := range []string{
		keys.Shown(keys.Draft.Agents) + " agents",
		keys.Shown(keys.Draft.NextAgent) + " next",
		"click to attach",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("the trailer is missing %q: %q", want, hint)
		}
	}
	if !strings.Contains(stripANSI(m.View().Content), hint) {
		t.Fatalf("the trailer reaches the screen:\n%s", stripANSI(m.View().Content))
	}
}

// TestInspectorAgents_TheMapFloatsAndTheChordDoesNot is the one rule by which
// the map and the chord may differ. A blocked child is drawn directly under
// the orchestrator because it is the row that wants something; the chord goes
// on walking spawn order, because a key whose destination moved every time a
// child blocked would be a key nobody could aim.
func TestInspectorAgents_TheMapFloatsAndTheChordDoesNot(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")

	if got := m.sessionMap(); len(got) != 3 ||
		got[0] != "" || got[1] != "researcher-1" || got[2] != "reviewer-1" {
		t.Fatalf("the chord walks spawn order: %v", got)
	}
	// Where nothing nests, the rail is handed the children in spawn order,
	// so the two orders are one reading of the supervisor rather than two
	// that have to be kept in step.
	agents := m.inspectorAgents()
	if agents[1].Name != "researcher-1" || agents[2].Name != "reviewer-1" {
		t.Fatalf("the host hands the map spawn order: %+v", agents)
	}
}

// oneToolEnv scripts children that make one read and then block on their
// next request, so a test can observe a child that is working and has a
// tool count to state.
func oneToolEnv() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		calls := 0
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			calls++
			if calls == 1 {
				ch := make(chan provider.StreamEvent, 1)
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "c1",
					Name: "read_file", Arguments: `{"path":"docs/loop.md"}`}}}
				close(ch)
				return ch, func() {}, nil
			}
			ch := make(chan provider.StreamEvent)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, func() {}, nil
		}
		return subagent.Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", nil },
		}, nil
	}
}

// TestInspectorAgents_AWorkingChildSaysItsToolCountOnce: the supervisor's
// line for a working child counts its tools for the model's roster, and the
// map row counts them in a field of its own; the row is handed the one and
// not the other, so it reads `running · 1 tool` rather than saying the count
// twice.
func TestInspectorAgents_AWorkingChildSaysItsToolCountOnce(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: oneToolEnv()})
	t.Cleanup(sup.Close)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{Subagents: sup})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m = updated.(Model)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	waitFor(t, func() bool { st, _ := sup.Get("researcher-1"); return st.ToolCalls == 1 })

	child := m.inspectorAgents()[1]
	if child.Detail != "running" || child.Tools != 1 {
		t.Fatalf("the row is handed the state word and the count apart: %+v", child)
	}
	view := stripANSI(m.View().Content)
	row := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "running ·") {
			row = line
		}
	}
	if row == "" || strings.Count(row, "1 tool") != 1 {
		t.Fatalf("the map row should say its tool count once:\n%s", view)
	}
}
