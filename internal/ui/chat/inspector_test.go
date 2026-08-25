package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// inspectorModel is a ready model with usage, pricing and a turn's worth of
// transcript, so every rail block has something to show.
func inspectorModel(t *testing.T, width, height int) Model {
	t.Helper()
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00001, MaxInputTokens: 200000},
	})
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(table, "gpt-4o")
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	m.turnStarted = time.Now().Add(-64 * time.Second)
	m.turnEnded = time.Now()
	m.transcript = []entry{
		{kind: entryUser, text: "do the thing"},
		{kind: entryAssistant, text: "Reading the loop"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"agent/loop.go"}`, toolResult: "ok", duration: time.Second},
		{kind: entryDiff, diff: &components.DiffView{Path: "internal/agent/loop.go", Verb: "edit",
			Hunks: []diff.Hunk{{OldStart: 1, OldCount: 3, NewStart: 1, NewCount: 4, Lines: []diff.Line{
				{Kind: diff.Add, Text: "a"}, {Kind: diff.Add, Text: "b"}, {Kind: diff.Del, Text: "c"},
			}}}}},
		{kind: entryCommand, text: "go test ./...", toolResult: "FAIL", exitCode: 1, duration: 3 * time.Second},
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func TestTwoPane_WidthThreshold(t *testing.T) {
	// The ladder's top rung is 130 content columns (§8c), both directions.
	for _, c := range []struct {
		width int
		want  bool
	}{
		{components.InspectorMinContentWidth + horizontalPadding*2 - 1, false},
		{components.InspectorMinContentWidth + horizontalPadding*2, true},
		{160, true},
		{80, false},
	} {
		m := inspectorModel(t, c.width, 40)
		if got := m.twoPane(); got != c.want {
			t.Fatalf("width %d (content %d): twoPane = %v, want %v", c.width, m.contentWidth(), got, c.want)
		}
	}
}

func TestTranscriptWidth_ReducedByTheRail(t *testing.T) {
	wide := inspectorModel(t, 144, 40) // content 140 → 93-column pane
	if wide.contentWidth() != 140 {
		t.Fatalf("content width = %d, want 140", wide.contentWidth())
	}
	if got := wide.transcriptWidth(); got != 93 {
		t.Fatalf("transcript pane = %d columns, want 93", got)
	}
	if wide.viewport.Width != 93 {
		t.Fatalf("viewport width = %d, want the pane width", wide.viewport.Width)
	}
	narrow := inspectorModel(t, 120, 40)
	if got := narrow.transcriptWidth(); got != narrow.contentWidth() {
		t.Fatalf("single pane keeps the full content width: %d vs %d", got, narrow.contentWidth())
	}
}

func TestView_TwoPaneRendersRail(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	view := stripANSI(m.View())
	for _, want := range []string{"THIS TURN", "CHANGES", "▎✎ internal/agent/loop.go", "CONTEXT", "SPEND", "│"} {
		if !strings.Contains(view, want) {
			t.Fatalf("two-pane view missing %q:\n%s", want, view)
		}
	}
	// Below the threshold nothing about the layout changes.
	narrow := stripANSI(inspectorModel(t, 120, 40).View())
	for _, absent := range []string{"THIS TURN", "CHANGES", "SPEND"} {
		if strings.Contains(narrow, absent) {
			t.Fatalf("single-pane view should not show %q:\n%s", absent, narrow)
		}
	}
}

func TestView_SplitKeepsTheRowBudget(t *testing.T) {
	// The split is horizontal only: the surface still fills exactly the
	// terminal's rows, and the viewport height is what the chrome left it.
	for _, width := range []int{120, 144} {
		m := inspectorModel(t, width, 30)
		if got := len(strings.Split(m.View(), "\n")); got != 30 {
			t.Fatalf("width %d: view is %d rows, want 30", width, got)
		}
		if m.viewport.Height != m.viewportHeight() {
			t.Fatalf("width %d: viewport height %d != %d", width, m.viewport.Height, m.viewportHeight())
		}
	}
	wide := inspectorModel(t, 144, 30)
	narrow := inspectorModel(t, 120, 30)
	if wide.viewportHeight() != narrow.viewportHeight() {
		t.Fatalf("the rail must not cost rows: %d vs %d", wide.viewportHeight(), narrow.viewportHeight())
	}
}

func TestView_TwoPaneRowsFitTheirPanes(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	// A long user message must wrap to the pane, not to the terminal.
	m.transcript = append(m.transcript, entry{kind: entryUser, text: strings.Repeat("wrap me ", 40)})
	m.viewport.SetContent(m.renderHistory())
	for _, line := range strings.Split(m.renderHistory(), "\n") {
		if w := lipgloss.Width(line); w > m.transcriptWidth() {
			t.Fatalf("transcript line is %d columns, pane is %d: %q", w, m.transcriptWidth(), stripANSI(line))
		}
	}
	for _, line := range strings.Split(stripANSI(m.View()), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("view line is %d columns, terminal is %d: %q", w, m.width, line)
		}
	}
}

func TestTwoPane_HiddenByTakeoverSurfaces(t *testing.T) {
	base := inspectorModel(t, 144, 40)
	for _, c := range []struct {
		name  string
		state state
	}{
		{"approval card", stateConfirmRun},
		{"plan approval", statePlanApprove},
		{"picker", statePick},
		{"rewind picker", stateRewindPick},
		{"full-screen diff", stateDiffFull},
		{"model list", stateModelList},
	} {
		m := base
		m.state = c.state
		if m.twoPane() {
			t.Fatalf("%s spans both panes and hides the rail", c.name)
		}
		if got := m.transcriptWidth(); got != m.contentWidth() {
			t.Fatalf("%s: transcript should span the full width, got %d", c.name, got)
		}
	}
	// The agent list is a takeover surface too, and dismissing it restores
	// the rail.
	m := base
	m.agentList = &components.AgentList{}
	if m.twoPane() {
		t.Fatal("the agent list hides the rail")
	}
	m.agentList = nil
	if !m.twoPane() {
		t.Fatal("dismissing a takeover surface restores the rail")
	}
	// Attached, the surface is a child's session, not this turn's rail.
	m.attachedTo = "writer-1"
	if m.twoPane() {
		t.Fatal("the attached view hides the rail")
	}
}

func TestInspectorData_BlocksFromTheSession(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	rail := m.inspectorData()
	if rail.Turn == nil || rail.Turn.Tools != 3 || rail.Turn.Step != 1 {
		t.Fatalf("THIS TURN: %+v", rail.Turn)
	}
	if rail.Turn.Elapsed < 60*time.Second {
		t.Fatalf("elapsed should be the turn's own clock: %s", rail.Turn.Elapsed)
	}
	if rail.Changes == nil || len(rail.Changes.Files) != 1 {
		t.Fatalf("CHANGES: %+v", rail.Changes)
	}
	if f := rail.Changes.Files[0]; f.Path != "internal/agent/loop.go" || f.Added != 2 || f.Removed != 1 {
		t.Fatalf("changed file: %+v", f)
	}
	if rail.Changes.Added != 2 || rail.Changes.Removed != 1 {
		t.Fatalf("changeset totals: %+v", rail.Changes)
	}
	if rail.Changes.Failure != "go test ./..." || rail.Changes.FailureNote != "exit 1" {
		t.Fatalf("the turn's broken command: %+v", rail.Changes)
	}
	if rail.Context == nil || rail.Context.Window != 200000 || rail.Context.Pct != 25 {
		t.Fatalf("CONTEXT: %+v", rail.Context)
	}
	if rail.Context.Tokens1 != "↑41.2k" || rail.Context.Tokens2 != "↓9.8k" {
		t.Fatalf("CONTEXT tokens: %+v", rail.Context)
	}
	if len(rail.Context.Burn) != 0 {
		t.Fatal("one round is a dot, not a trend: no sparkline yet")
	}
	if rail.Spend == nil || rail.Spend.Model != "gpt-4o" || rail.Spend.Main == "" {
		t.Fatalf("SPEND: %+v", rail.Spend)
	}
	// No children in this session: the block is omitted, not empty.
	if len(rail.Agents) != 0 {
		t.Fatalf("AGENTS should be omitted: %+v", rail.Agents)
	}
}

func TestInspectorData_OmitsBlocksWithNothingToSay(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	fresh := updated.(Model)
	rail := fresh.inspectorData()
	if !rail.Empty() {
		t.Fatalf("a fresh session has nothing to inspect: %+v", rail)
	}
	if got := stripANSI(fresh.View()); strings.Contains(got, "THIS TURN") {
		t.Fatalf("an empty rail draws nothing:\n%s", got)
	}
}

func TestTurnClockAndSpend(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnTokensIn, m.turnTokensOut = 1000, 500
	updated, _ := m.sendUserMessage("next")
	next := updated.(Model)
	if next.turnTokensIn != 0 || next.turnTokensOut != 0 {
		t.Fatalf("a new turn starts its spend at zero: %d/%d", next.turnTokensIn, next.turnTokensOut)
	}
	if next.turnStarted.IsZero() || !next.turnEnded.IsZero() {
		t.Fatal("a new turn starts its clock and clears the end stamp")
	}
	next.accumulateUsage(&provider.Usage{PromptTokens: 100, CompletionTokens: 50})
	if next.turnTokensIn != 100 || next.turnTokensOut != 50 {
		t.Fatalf("turn usage: %d/%d", next.turnTokensIn, next.turnTokensOut)
	}
	if next.TotalTokensIn != 41300 {
		t.Fatalf("session usage still accumulates: %d", next.TotalTokensIn)
	}
	live := next.turnElapsed()
	next.setTurnState(stateInput)
	if next.turnEnded.IsZero() {
		t.Fatal("a turn going idle stamps its end")
	}
	if frozen := next.turnElapsed(); frozen < live {
		t.Fatalf("a finished turn's elapsed time freezes: %s < %s", frozen, live)
	}
}

func TestUICommand_ReportsTheLayout(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	if got := m.uiCommand([]string{"/ui"}); !strings.Contains(got, "two panes") || !strings.Contains(got, "93-column transcript") {
		t.Fatalf("/ui reports the split layout: %q", got)
	}
	narrow := inspectorModel(t, 120, 40)
	if got := narrow.uiCommand([]string{"/ui"}); !strings.Contains(got, "one pane") {
		t.Fatalf("/ui reports the single-pane layout: %q", got)
	}
}

func TestInspector_AgentsBlockFromRunningChildren(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithSubagents(sup)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m = updated.(Model)
	spawnBlockedChild(t, sup)

	agents := m.inspectorAgents()
	if len(agents) != 1 || agents[0].Name != "researcher-1" {
		t.Fatalf("AGENTS should list the running child: %+v", agents)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "AGENTS") || !strings.Contains(view, "1 running") || !strings.Contains(view, "◇ researcher-1") {
		t.Fatalf("two-pane view missing the AGENTS block:\n%s", view)
	}
}

func TestInspectorContext_BurnSparkline(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	for i := 0; i < 12; i++ {
		m.accumulateUsage(&provider.Usage{PromptTokens: 1000 * (i + 1), CompletionTokens: 100})
	}
	if got := len(m.vitals.burn); got != contextBurnSamples {
		t.Fatalf("the burn series is bounded to %d samples, got %d", contextBurnSamples, got)
	}
	rail := m.inspectorData()
	if len(rail.Context.Burn) != contextBurnSamples {
		t.Fatalf("CONTEXT should carry the series: %+v", rail.Context.Burn)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "per round") || !strings.Contains(view, "█") {
		t.Fatalf("the CONTEXT block should draw the burn sparkline:\n%s", view)
	}
	// A cleared conversation has no history to plot.
	m.clearConversation()
	if len(m.vitals.burn) != 0 {
		t.Fatalf("/clear resets the burn series: %+v", m.vitals.burn)
	}
}

func TestFocusMode_KeepsTheRail(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	// Focus mode reads the transcript; it is not a takeover surface.
	m.enterSurface(stateFocus)
	m.focusIdx = 2
	if !m.twoPane() {
		t.Fatal("focus mode keeps the inspector rail")
	}
	content, _, _ := m.renderFocusHistory()
	for _, line := range strings.Split(content, "\n") {
		if w := lipgloss.Width(line); w > m.transcriptWidth() {
			t.Fatalf("focus row is %d columns, pane is %d: %q", w, m.transcriptWidth(), stripANSI(line))
		}
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "THIS TURN") {
		t.Fatalf("focus mode still renders the rail:\n%s", view)
	}
}
