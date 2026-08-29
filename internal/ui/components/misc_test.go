package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestCockpit_Segments(t *testing.T) {
	c := Cockpit{Mode: "accept edits", ModeKind: CockpitPermissive, Round: "round 7/25",
		CtxPct: 62, Tokens: "↑41.2k ↓9.8k", Spend: "$0.14", Model: "gpt-5.2",
		Agents: 2, AgentsBlocked: 1}
	view := c.View(120)
	for _, want := range []string{"⏵⏵ accept edits", "round 7/25", "ctx", "62%", "▰", "$0.14", "◇ 2 agents", "⚠1", "gpt-5.2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("cockpit should contain %q:\n%s", want, view)
		}
	}
	gated := Cockpit{Mode: "plan", ModeKind: CockpitGated, CtxPct: -1}
	if !strings.Contains(gated.View(80), "⏸ plan") {
		t.Fatal("gated modes render ⏸")
	}
	checking := Cockpit{Mode: "checking", ModeKind: CockpitChecking, CtxPct: -1}
	if !strings.Contains(checking.View(80), "✦ checking") {
		t.Fatal("classifier checks render ✦")
	}
}

func TestCockpit_CtxMeterFillAndThresholds(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 50}
	if view := c.View(120); !strings.Contains(view, "▰▰▰▰▱▱▱▱ 50%") {
		t.Fatalf("50%% should fill 4 of 8 cells:\n%s", view)
	}
	hidden := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: -1}
	if view := hidden.View(120); strings.Contains(view, "ctx") {
		t.Fatalf("a negative CtxPct hides the meter:\n%s", view)
	}
	// Host-supplied thresholds (S-055 trim warnings) override the defaults
	// without changing the bar's content.
	overridden := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 65, WarnPct: 60, AlertPct: 80}
	if view := overridden.View(120); !strings.Contains(view, "65%") {
		t.Fatalf("overridden thresholds keep the meter rendering:\n%s", view)
	}
}

func TestCockpit_DropsRightSideWhenNarrow(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 42,
		Tokens: "↑41.2k ↓9.8k", Spend: "$0.14", Model: "claude-sonnet-5"}
	view := c.View(30)
	if strings.Contains(view, "claude-sonnet-5") {
		t.Fatalf("narrow cockpit should drop the right-side model first:\n%s", view)
	}
	if !strings.Contains(view, "manual") {
		t.Fatalf("the mode segment survives narrowing:\n%s", view)
	}
}

// The right side sheds the model before the reasoning level and only then
// goes altogether: the level is what the session just changed, the model is
// the detail rank §8b drops first (S-139).
func TestCockpit_ShedsTheModelBeforeTheReasoningLevel(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: 42,
		Tokens: "↑41.2k ↓9.8k", Spend: "$0.14", Reasoning: "think high", Model: "claude-sonnet-5"}

	wide := c.View(90)
	if !strings.Contains(wide, "think high") || !strings.Contains(wide, "claude-sonnet-5") {
		t.Fatalf("a wide rail states both:\n%s", wide)
	}
	mid := c.View(65)
	if strings.Contains(mid, "claude-sonnet-5") {
		t.Fatalf("the model goes first:\n%s", mid)
	}
	if !strings.Contains(mid, "think high") {
		t.Fatalf("the level outlives the model:\n%s", mid)
	}
	if strings.Contains(c.View(30), "think high") {
		t.Fatalf("a rail with no room states neither:\n%s", c.View(30))
	}
}

// A session asking for no reasoning has nothing to state, and the rail is
// exactly what it was before the level existed.
func TestCockpit_NoReasoningSegmentWhenOff(t *testing.T) {
	c := Cockpit{Mode: "manual", ModeKind: CockpitGated, CtxPct: -1, Model: "gpt-4o"}
	if got := stripANSI(c.View(60)); !strings.HasSuffix(got, "gpt-4o") {
		t.Fatalf("expected the model alone on the right, got %q", got)
	}
}

func TestAgentList_ViewAndKeys(t *testing.T) {
	l := &AgentList{Rows: []AgentRow{
		{State: AgentCurrent, Name: "orchestrator", Status: "round 7", Spend: "$0.14"},
		{State: AgentRunning, Name: "researcher-1", Task: "auth flow survey", Status: "running…", Spend: "$0.02"},
		{State: AgentBlocked, Name: "writer-1", Status: "waiting approval", Spend: "$0.05"},
		{State: AgentDone, Name: "researcher-2", Status: "done · 14 tools"},
		{State: AgentFailed, Name: "writer-2", Status: "failed · round limit"},
	}}
	view := l.View(90)
	for _, want := range []string{"●", "◇", "✓", "✗", "⚠ waiting approval", "orchestrator", "enter attach"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agent list should contain %q:\n%s", want, view)
		}
	}

	l.Update(key("j"))
	l.Update(key("j"))
	if l.Focus != 2 {
		t.Fatalf("j should move focus, got %d", l.Focus)
	}
	if done, result := l.Update(key("x")); done || result.(AgentListResult).Action != AgentCancel {
		t.Fatal("x should request cancel and keep the list open")
	}
	if done, result := l.Update(key("X")); done || result.(AgentListResult).Action != AgentKill {
		t.Fatal("X should request kill and keep the list open")
	}
	done, result := l.Update(key("enter"))
	if !done || result.(AgentListResult) != (AgentListResult{Action: AgentAttach, Index: 2}) {
		t.Fatalf("enter should attach to the focused agent, got %v", result)
	}
	done, result = l.Update(key("esc"))
	if !done || result.(AgentListResult).Action != AgentBack {
		t.Fatal("esc should dismiss the list")
	}
}

func TestRenderCard_FrameAndClip(t *testing.T) {
	card := renderCard("Title", []string{"row one", strings.Repeat("x", 200)}, 40)
	lines := strings.Split(card, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected top, two rows, bottom; got %d lines", len(lines))
	}
	plain := stripANSI(card)
	if !strings.Contains(plain, "┌─ Title") || !strings.Contains(plain, "└") {
		t.Fatalf("card should be framed with its title:\n%s", plain)
	}
	for i, l := range strings.Split(plain, "\n") {
		if w := len([]rune(l)); w != 40 {
			t.Fatalf("line %d should be exactly 40 cells, got %d: %q", i, w, l)
		}
	}
	if !strings.Contains(plain, "…") {
		t.Fatal("overlong rows clip with an ellipsis")
	}
}

func TestHintRows_StackWhenNarrow(t *testing.T) {
	segments := []string{"space toggle", "a all/none", "enter apply", "esc cancel"}
	if rows := hintRows(segments, 80); len(rows) != 1 {
		t.Fatalf("wide terminals join hints on one row, got %d", len(rows))
	}
	if rows := hintRows(segments, 30); len(rows) != len(segments) {
		t.Fatalf("narrow terminals stack hints instead of truncating, got %d", len(rows))
	}
}

// The rail must terminate at every width, including the ones where the right
// side has just been emptied: a shedding chain that can re-widen loops for
// ever, and it loops inside the render path of a narrow terminal.
func TestCockpit_ViewTerminatesAtEveryWidth(t *testing.T) {
	c := Cockpit{Mode: "accept edits", ModeKind: CockpitPermissive, Round: "round 7/25",
		CtxPct: 62, Tokens: "↑41.2k ↓9.8k", Spend: "$0.14", Agents: 2, AgentsBlocked: 1,
		Extra: []string{"1 queued"}, Reasoning: "think medium", Model: "claude-opus-5"}
	for w := 0; w <= 120; w++ {
		if got := lipgloss.Width(c.View(w)); got > w && w > 0 {
			t.Fatalf("width %d rendered %d columns", w, got)
		}
	}
}
