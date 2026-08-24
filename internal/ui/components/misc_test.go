package components

import (
	"strings"
	"testing"
)

func TestActivityRow_Collapsed(t *testing.T) {
	r := ActivityRow{Kind: ActivityTool, Name: "search", Arg: "advanceExecQueue",
		Counts: "3 matches", Duration: "0.1s", Detail: []string{"model.go:152"}}
	view := r.View(80)
	if strings.Contains(view, "model.go:152") {
		t.Fatalf("collapsed row must not show detail:\n%s", view)
	}
	for _, want := range []string{"⚙", "search", "advanceExecQueue", "3 matches", "0.1s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("row should contain %q:\n%s", want, view)
		}
	}
}

func TestActivityRow_ExpandedAndFailed(t *testing.T) {
	r := ActivityRow{Kind: ActivityCommand, Name: "go vet ./...", Outcome: "exit 1",
		Failed: true, Detail: []string{"vet: unreachable code"}}
	view := r.View(80)
	if !strings.Contains(view, "✗") || !strings.Contains(view, "vet: unreachable code") {
		t.Fatalf("failed rows auto-expand with the error glyph:\n%s", view)
	}

	r = ActivityRow{Kind: ActivityEdit, Name: "loop.go", Outcome: "+12 −4",
		Expanded: true, Detail: []string{"hunk 1", "hunk 2"}, MaxDetail: 1}
	view = r.View(80)
	if !strings.Contains(view, "hunk 1") || strings.Contains(view, "hunk 2") {
		t.Fatalf("expanded detail should respect MaxDetail:\n%s", view)
	}
}

func TestActivityRow_RunningTail(t *testing.T) {
	r := ActivityRow{Kind: ActivityCommand, Name: "go test ./...", Running: true,
		Outcome: "running…", Tail: "ok  internal/agent  0.31s"}
	view := r.View(80)
	if !strings.Contains(view, "▸") || !strings.Contains(view, "ok  internal/agent") {
		t.Fatalf("running rows show the live tail:\n%s", view)
	}
}

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
