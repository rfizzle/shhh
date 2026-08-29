package chat

// Reasoning effort in the session (S-139): the chord walks the levels, the
// command says and sets them, and the cockpit states which one is live.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

func ctrlR() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl} }

// reasoningModel is a ready session wired the way the CLI wires one: a level,
// and the hook that carries a change to the next request.
func reasoningModel(t *testing.T) (Model, *provider.Effort) {
	t.Helper()
	m := activityModel(t)
	applied := new(provider.Effort)
	m = m.WithReasoning(provider.EffortOff, func(e provider.Effort) { *applied = e })
	return m, applied
}

func TestReasoning_ChordCyclesAndReachesTheNextRequest(t *testing.T) {
	m, applied := reasoningModel(t)

	for _, want := range []provider.Effort{provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortOff} {
		updated, _ := m.Update(ctrlR())
		m = updated.(Model)
		if m.effort != want {
			t.Fatalf("chord left the session on %v, want %v", m.effort, want)
		}
		if *applied != want {
			t.Fatalf("the session was told %v, the stream was told %v", want, *applied)
		}
	}
}

func TestReasoning_ChordLeavesTheDraftAlone(t *testing.T) {
	m, _ := reasoningModel(t)
	m.input.SetValue("half a sentence")

	updated, _ := m.Update(ctrlR())
	m = updated.(Model)
	if got := m.input.Value(); got != "half a sentence" {
		t.Fatalf("the chord touched the draft: %q", got)
	}
	if m.effort != provider.EffortLow {
		t.Fatalf("the chord did not cycle: %v", m.effort)
	}
}

func TestReasoning_CommandSetsAndReports(t *testing.T) {
	m, applied := reasoningModel(t)

	handled, out := m.handleSlashCommand("/reasoning high")
	if !handled {
		t.Fatal("/reasoning should be handled")
	}
	if m.effort != provider.EffortHigh || *applied != provider.EffortHigh {
		t.Fatalf("level = %v, applied = %v", m.effort, *applied)
	}
	if !strings.Contains(out, "high") {
		t.Errorf("the reply should name the level, got %q", out)
	}

	_, out = m.handleSlashCommand("/reasoning")
	if !strings.Contains(out, "high") {
		t.Errorf("bare /reasoning should report the level, got %q", out)
	}

	_, out = m.handleSlashCommand("/reasoning ultra")
	if !strings.Contains(out, "unknown reasoning level") {
		t.Errorf("an unknown level should be refused, not read as off: %q", out)
	}
	if m.effort != provider.EffortHigh {
		t.Errorf("a refused level must leave the session where it was, got %v", m.effort)
	}
}

func TestReasoning_DefaultWritesTheConfigKey(t *testing.T) {
	m, _ := reasoningModel(t)
	var wroteKey, wroteValue string
	m = m.WithConfigWriter(func(key, value string) error {
		wroteKey, wroteValue = key, value
		return nil
	})

	_, out := m.handleSlashCommand("/reasoning default medium")
	if wroteKey != "provider.reasoning" || wroteValue != "medium" {
		t.Fatalf("wrote %q = %q", wroteKey, wroteValue)
	}
	if strings.Contains(out, "could not") {
		t.Errorf("unexpected failure: %q", out)
	}
	// The session itself is untouched by a default, and says so.
	if m.effort != provider.EffortOff {
		t.Errorf("a default must not switch this session, got %v", m.effort)
	}
}

// A default that something else overrules was written and will still be
// ignored, which is the one outcome the reply must not hide (S-136's rule).
func TestReasoning_DefaultSaysWhenItIsOverruled(t *testing.T) {
	m, _ := reasoningModel(t)
	m = m.WithConfigWriter(func(string, string) error { return nil }).
		WithReasoningDefault("", "SHHH_REASONING is set to low")

	_, out := m.handleSlashCommand("/reasoning default high")
	if !strings.Contains(out, "SHHH_REASONING") {
		t.Errorf("the reply must name what outranks the file, got %q", out)
	}
}

func TestReasoning_CockpitStatesTheLevelBesideTheModel(t *testing.T) {
	m, _ := reasoningModel(t)
	m = m.WithPricing(nil, "claude-opus-5")

	if seg := m.cockpitData(true).Reasoning; seg != "" {
		t.Errorf("a session asking for no reasoning has nothing to state, got %q", seg)
	}

	updated, _ := m.Update(ctrlR())
	m = updated.(Model)
	c := m.cockpitData(true)
	if c.Reasoning != "think low" {
		t.Fatalf("cockpit reasoning segment = %q", c.Reasoning)
	}
	rail := c.View(100)
	if !strings.Contains(rail, "think low") || !strings.Contains(rail, "claude-opus-5") {
		t.Errorf("the rail should carry both halves, got %q", rail)
	}
}

// A session with no hook applies nothing and says so, rather than moving the
// rail to a level the requests will not use.
func TestReasoning_WithoutAHookTheLevelDoesNotMove(t *testing.T) {
	m := activityModel(t)
	updated, _ := m.Update(ctrlR())
	m = updated.(Model)
	if m.effort != provider.EffortOff {
		t.Fatalf("level moved without a hook: %v", m.effort)
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "cannot change") {
		t.Errorf("expected the session to say so, got %q", last.text)
	}
}
