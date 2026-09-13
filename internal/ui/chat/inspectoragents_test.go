package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
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
		keys.Shown(keys.Draft.Agents) + " manager",
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

// TestInspectorAgents_TheTrailerNamesWhatAltCosts: every clause of the
// trailer is a chord, so where it is the first thing in the session to offer
// one it carries the notice about the Option key — the same sentence, in the
// same words, a transcript row carries when the row is first.
func TestInspectorAgents_TheTrailerNamesWhatAltCosts(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	updated, _ := newSubagentModel(t, sup).Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m := updated.(Model)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")

	if !m.resolveInspector().AgentsOption {
		t.Fatal("with nothing above it offering a chord, the trailer is the row that says what one costs")
	}
	notice := components.KeyRunOption([]components.TurnKey{{Key: "[r]", Chord: "[alt+r]"}}, true, true)
	if notice == "" {
		t.Fatal("the notice a row raises is what this row raises")
	}
	if view := stripANSI(m.View().Content); !strings.Contains(view, stripANSI(notice)) {
		t.Fatalf("the notice should reach the screen under the map:\n%s", view)
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
	// The rail is handed the children in spawn order and does the floating
	// itself, so the two orders are one reading of the supervisor rather than
	// two that have to be kept in step.
	agents := m.inspectorAgents()
	if agents[1].Name != "researcher-1" || agents[2].Name != "reviewer-1" {
		t.Fatalf("the host hands the map spawn order: %+v", agents)
	}
}
