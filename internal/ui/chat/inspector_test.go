package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// inspectorModel is a ready model with usage, pricing and a turn's worth of
// transcript, so every rail block has something to show. The edit is recorded
// in the changeset store as well as drawn in the transcript, because that is
// what an applied edit does in a real session and what the rail's
// session-scoped CHANGES block reads.
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
	m.turnCount = 1
	m.changes.Add(1, changeset.Record{
		Path: "internal/agent/loop.go", BeforeExists: true, AfterExists: true,
		Before: "c\n", After: "a\nb\n",
	})
	m.transcript = []entry{
		{kind: entryUser, text: "do the thing", turn: 1},
		{kind: entryAssistant, text: "Reading the loop"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"agent/loop.go"}`, toolResult: "ok", duration: time.Second, turn: 1},
		{kind: entryDiff, diff: &components.DiffView{Path: "internal/agent/loop.go", Verb: "edit",
			Hunks: []diff.Hunk{{OldStart: 1, OldCount: 3, NewStart: 1, NewCount: 4, Lines: []diff.Line{
				{Kind: diff.Add, Text: "a"}, {Kind: diff.Add, Text: "b"}, {Kind: diff.Del, Text: "c"},
			}}}}},
		{kind: entryCommand, text: "go test ./...", toolResult: "FAIL", exitCode: 1, duration: 3 * time.Second, turn: 1},
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func TestTwoPane_WidthThreshold(t *testing.T) {
	// The ladder's top rung is 130 content columns, both directions.
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
	wide := inspectorModel(t, 144, 40) // content 140 → 48-column rail, 91-column pane
	if wide.contentWidth() != 140 {
		t.Fatalf("content width = %d, want 140", wide.contentWidth())
	}
	if got := wide.paneWidth(); got != 91 {
		t.Fatalf("transcript pane = %d columns, want 91", got)
	}
	// The pane holds one column back for the scroll gutter, so
	// the transcript wraps one column inside it — and so does the viewport,
	// which is the selection's coordinate space.
	if got := wide.transcriptWidth(); got != 90 {
		t.Fatalf("transcript wraps to %d columns, want 90", got)
	}
	if wide.viewport.Width() != 90 {
		t.Fatalf("viewport width = %d, want the wrap width", wide.viewport.Width())
	}
	narrow := inspectorModel(t, 120, 40)
	if got := narrow.paneWidth(); got != narrow.contentWidth() {
		t.Fatalf("single pane keeps the full content width: %d vs %d", got, narrow.contentWidth())
	}
	if got := narrow.transcriptWidth(); got != narrow.contentWidth()-components.ScrollGutterWidth {
		t.Fatalf("single pane still reserves the gutter: %d vs %d", got, narrow.contentWidth())
	}
}

func TestView_TwoPaneRendersRail(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	view := stripANSI(m.View().Content)
	for _, want := range []string{"THIS TURN", "CHANGES", "▎✎ internal/agent/loop.go", "CONTEXT", "SPEND", "│"} {
		if !strings.Contains(view, want) {
			t.Fatalf("two-pane view missing %q:\n%s", want, view)
		}
	}
	// Below the threshold nothing about the layout changes.
	narrow := stripANSI(inspectorModel(t, 120, 40).View().Content)
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
		if got := len(strings.Split(m.View().Content, "\n")); got != 30 {
			t.Fatalf("width %d: view is %d rows, want 30", width, got)
		}
		if m.viewport.Height() != m.viewportHeight() {
			t.Fatalf("width %d: viewport height %d != %d", width, m.viewport.Height(), m.viewportHeight())
		}
	}
	// Below the threshold the surface gains the one row that stands in for
	// the rail (statusrow.go), and that row is the whole difference — the
	// split itself still costs nothing.
	wide := inspectorModel(t, 144, 30)
	narrow := inspectorModel(t, 120, 30)
	if wide.statusRow() != "" || narrow.statusRow() == "" {
		t.Fatal("the status row stands in below the threshold and only there")
	}
	if wide.viewportHeight() != narrow.viewportHeight()+1 {
		t.Fatalf("the rail must cost no rows beyond the one standing in for it: %d vs %d",
			wide.viewportHeight(), narrow.viewportHeight())
	}
}

func TestView_TwoPaneRowsFitTheirPanes(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	// A long user message must wrap to the pane, not to the terminal.
	m.transcript = append(m.transcript, entry{kind: entryUser, text: strings.Repeat("wrap me ", 40)})
	m.viewport.SetLines(m.renderHistoryLines())
	for _, line := range strings.Split(m.renderHistory(), "\n") {
		if w := lipgloss.Width(line); w > m.transcriptWidth() {
			t.Fatalf("transcript line is %d columns, pane is %d: %q", w, m.transcriptWidth(), stripANSI(line))
		}
	}
	for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
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
		// A decision is a takeover only once it holds the keyboard (the
		// mid-sentence rule); until then the panes behind it are still what is
		// being read.
		m.decisionHeld = true
		if m.twoPane() {
			t.Fatalf("%s spans both panes and hides the rail", c.name)
		}
		if got := m.paneWidth(); got != m.contentWidth() {
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
	// A decision that has not been given the keyboard is not a takeover: the
	// card rides above a live frame and the rail stays.
	waiting := base
	waiting.state = stateConfirmRun
	if !waiting.twoPane() {
		t.Fatal("a decision still waiting for the keyboard must not reflow the panes")
	}
	// Attached is not a takeover: the child's transcript takes the pane, and
	// the rail — whose blocks are the session's either way — stays beside it.
	m.attachedTo = "writer-1"
	if !m.twoPane() {
		t.Fatal("an attached child's session must not take the rail with it")
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
	if rail.Turn.Files != 1 || rail.Turn.Added != 2 || rail.Turn.Removed != 1 {
		t.Fatalf("THIS TURN counts the turn's own files: %+v", rail.Turn)
	}
	if len(rail.Changes.Alerts) != 1 {
		t.Fatalf("the session's broken command: %+v", rail.Changes.Alerts)
	}
	if a := rail.Changes.Alerts[0]; a.Label != "go test ./..." || a.Note != "exit 1" || a.Turn != 1 {
		t.Fatalf("the alert names the turn that broke it: %+v", a)
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
	if got := stripANSI(fresh.View().Content); strings.Contains(got, "THIS TURN") {
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
	if got := m.uiCommand([]string{"/ui"}); !strings.Contains(got, "two panes") || !strings.Contains(got, "91-column transcript") {
		t.Fatalf("/ui reports the split layout: %q", got)
	}
	narrow := inspectorModel(t, 120, 40)
	if got := narrow.uiCommand([]string{"/ui"}); !strings.Contains(got, "one pane") {
		t.Fatalf("/ui reports the single-pane layout: %q", got)
	}
}

// TestUICommand_RailSetsTheSplit: the command is the keyboard equal of the
// config key, so what it changes is the next frame's columns and not a field
// only the readout can see.
func TestUICommand_RailSetsTheSplit(t *testing.T) {
	m := inspectorModel(t, 200, 40) // content 196 → a 62-column rail on the ladder
	if got := m.columns().inspector.Dx(); got != 62 {
		t.Fatalf("the ladder gives a 200-column terminal a %d-column rail, want 62", got)
	}
	if reply := m.uiCommand([]string{"/ui", "rail", "60"}); !strings.Contains(reply, "set to 60 columns") {
		t.Fatalf("/ui rail 60 should say what it set: %q", reply)
	}
	if got := m.columns().inspector.Dx(); got != 60 {
		t.Fatalf("the rail is %d columns after /ui rail 60, want 60", got)
	}
	if got := m.paneWidth(); got != 196-60-paneDividerWidth {
		t.Fatalf("the transcript pane is %d columns, want %d", got, 196-60-paneDividerWidth)
	}
	// A number the terminal cannot seat is cut to what it can, and the
	// readout says so rather than reporting the number back.
	if reply := m.uiCommand([]string{"/ui", "rail", "72"}); !strings.Contains(reply, "as wide as this terminal allows") {
		t.Fatalf("a rail wider than the ladder allows should say so: %q", reply)
	}
	if got := m.columns().inspector.Dx(); got != 62 {
		t.Fatalf("the rail is %d columns after /ui rail 72, want the ladder's 62", got)
	}
	// And a number under the rail's own floor is widened to it, which is the
	// other limit and says so in different words.
	if reply := m.uiCommand([]string{"/ui", "rail", "40"}); !strings.Contains(reply, "narrowest rail there is") {
		t.Fatalf("a rail narrower than the floor should say so: %q", reply)
	}
	if got := m.columns().inspector.Dx(); got != 46 {
		t.Fatalf("the rail is %d columns after /ui rail 40, want the floor's 46", got)
	}
	if reply := m.uiCommand([]string{"/ui", "rail", "auto"}); !strings.Contains(reply, "width ladder") {
		t.Fatalf("/ui rail auto should hand the width back: %q", reply)
	}
	if m.railCols != 0 {
		t.Fatalf("auto leaves no setting behind, got %d", m.railCols)
	}
	if reply := m.uiCommand([]string{"/ui", "rail", "wide"}); !strings.Contains(reply, "Error") {
		t.Fatalf("a value that is neither auto nor a count is refused: %q", reply)
	}
}

// TestUICommand_RailReflowsTheTranscript: a rail that changed width is a
// transcript that changed width. Nothing on the command's path resizes the
// viewport for it, so a feed left wrapped to the old pane is what a missing
// sync looks like — the rail moves and the text beside it does not.
func TestUICommand_RailReflowsTheTranscript(t *testing.T) {
	m := inspectorModel(t, 200, 40)
	next, _ := m.runCommand("/ui rail 60", "/ui")
	after := next.(Model)
	if after.railCols != 60 {
		t.Fatalf("the setting did not survive the dispatch: %d", after.railCols)
	}
	if got, want := after.viewport.Width(), after.transcriptWidth(); got != want {
		t.Fatalf("the viewport wraps to %d columns beside a %d-column transcript", got, want)
	}
}

// TestUICommand_RailBelowTheRungSaysWhyNothingMoved: the setting takes at
// any width, so a terminal too narrow to split has to be told that what it
// is looking at is the rung and not a command that failed.
func TestUICommand_RailBelowTheRungSaysWhyNothingMoved(t *testing.T) {
	m := inspectorModel(t, 120, 40)
	reply := m.uiCommand([]string{"/ui", "rail", "60"})
	if !strings.Contains(reply, "too narrow to split") {
		t.Fatalf("a narrow terminal should say why nothing moved: %q", reply)
	}
	if m.railCols != 60 {
		t.Fatalf("the setting still takes: %d", m.railCols)
	}
}

// TestInspector_AgentsMapsEverySession: AGENTS is the whole run, not the part
// of it still moving. The orchestrator leads, the children follow in spawn
// order, and a child that has stopped keeps its row with the word it ended on
// rather than being left to a surface you have to open.
func TestInspector_AgentsMapsEverySession(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithSubagents(sup)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m = updated.(Model)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	spawnChild(t, sup, subagent.RoleReviewer, "reviewer-1")
	killChild(t, sup, "reviewer-1")

	agents := m.inspectorAgents()
	if len(agents) != 3 {
		t.Fatalf("the map is the orchestrator and both children: %+v", agents)
	}
	if !agents[0].Self || agents[0].Name != "orchestrator" || !agents[0].Focused {
		t.Fatalf("the orchestrator leads the map and holds the keyboard: %+v", agents[0])
	}
	if agents[1].Name != "researcher-1" || agents[1].State != components.FanoutRunning {
		t.Fatalf("a running child keeps its state: %+v", agents[1])
	}
	if agents[2].Name != "reviewer-1" || agents[2].Outcome != "failed" {
		t.Fatalf("a finished child states the supervisor's own word: %+v", agents[2])
	}
	view := stripANSI(m.View().Content)
	for _, want := range []string{"AGENTS", "1 running", "\u25c7 researcher-1", "\u2717 reviewer-1", "failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the two-pane view is missing %q:\n%s", want, view)
		}
	}
}

// TestInspector_RailStaysMarkedWhileAttached is the pair of facts that have
// to hold together: the rail is still there beside a child's transcript, and
// the row the keyboard is in is the one marked, so the session-wide numbers
// under it cannot be read as that child's.
func TestInspector_RailStaysMarkedWhileAttached(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithSubagents(sup)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 144, Height: 40})
	m = updated.(Model)
	spawnChild(t, sup, subagent.RoleResearcher, "researcher-1")
	m.attach("researcher-1")

	if !m.twoPane() {
		t.Fatal("the rail stays up while attached")
	}
	agents := m.inspectorAgents()
	if agents[0].Focused || !agents[1].Focused {
		t.Fatalf("the mark is on the session the keyboard is in: %+v", agents)
	}
	marked := ""
	for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
		if strings.Contains(line, "\u25b8 ") && strings.Contains(line, "-1") {
			marked = line
		}
	}
	if !strings.Contains(marked, "researcher-1") {
		t.Fatalf("the attached child's row carries the mark:\n%s", stripANSI(m.View().Content))
	}
}

func TestInspectorContext_BurnSparkline(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	for i := 0; i < contextBurnSamples+4; i++ {
		m.accumulateUsage(&provider.Usage{PromptTokens: 1000 * (i + 1), CompletionTokens: 100})
	}
	if got := len(m.vitals.burn); got != contextBurnSamples {
		t.Fatalf("the burn series is bounded to %d samples, got %d", contextBurnSamples, got)
	}
	rail := m.inspectorData()
	if len(rail.Context.Burn) != contextBurnSamples {
		t.Fatalf("CONTEXT should carry the series: %+v", rail.Context.Burn)
	}
	view := stripANSI(m.View().Content)
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
	if view := stripANSI(m.View().Content); !strings.Contains(view, "THIS TURN") {
		t.Fatalf("focus mode still renders the rail:\n%s", view)
	}
}

// The rail is the session's overview, not a second copy of the turn: a file
// edited in an earlier turn is still on screen turns later, and a path edited
// twice is one row with the net counts and the turns behind it.
func TestInspectorChanges_SessionScoped(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	// Turn 2 edits the same file again and a new one.
	m.turnCount = 2
	m.changes.Add(2, changeset.Record{
		Path: "internal/agent/loop.go", BeforeExists: true, AfterExists: true,
		Before: "a\nb\n", After: "a\nb\nd\n",
	})
	m.changes.Add(2, changeset.Record{
		Path: "internal/ui/chat/model.go", BeforeExists: true, AfterExists: true,
		Before: "x\n", After: "x\ny\n",
	})
	// A new turn's transcript: the earlier turn's rows are behind it.
	m.transcript = append(m.transcript, entry{kind: entryUser, text: "and again", turn: 2})

	c := m.inspectorChanges()
	if c == nil || len(c.Files) != 2 {
		t.Fatalf("CHANGES should hold both paths: %+v", c)
	}
	loop := c.Files[0]
	if loop.Path != "internal/agent/loop.go" {
		t.Fatalf("first-edit order: %+v", c.Files)
	}
	// Net across both turns: "c" became "a b d" — three added, one removed.
	if loop.Added != 3 || loop.Removed != 1 {
		t.Fatalf("repeat edits collapse to the net change: %+v", loop)
	}
	if loop.Turns != 2 {
		t.Fatalf("the row carries the turns behind it: %+v", loop)
	}
	if !loop.ThisTurn || !c.Files[1].ThisTurn {
		t.Fatalf("both paths were touched this turn: %+v", c.Files)
	}
	if c.Added != 4 || c.Removed != 1 {
		t.Fatalf("the heading totals the session: %+v", c)
	}

	// Turn 3 touches neither: the earlier rows stay, and stop claiming to be
	// this turn's work.
	m.turnCount = 3
	c = m.inspectorChanges()
	if c == nil || len(c.Files) != 2 {
		t.Fatalf("earlier turns' files stay on screen: %+v", c)
	}
	for _, f := range c.Files {
		if f.ThisTurn {
			t.Fatalf("turn 3 changed nothing: %+v", f)
		}
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "▎✎ internal/agent/loop.go") || !strings.Contains(view, "session · ") {
		t.Fatalf("the rail still shows the session's changes:\n%s", view)
	}
}

// An alert follows the workspace, not the turn: it survives later turns and
// is cleared by the same command coming back clean.
func TestInspectorAlerts_PersistUntilTheWorkspaceIsClean(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "fix it"})
	if alerts := m.inspectorAlerts(); len(alerts) != 1 || alerts[0].Turn != 1 {
		t.Fatalf("turn 1's failure is still standing in turn 2: %+v", alerts)
	}
	// A second command breaks in turn 2; both are standing.
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2})
	alerts := m.inspectorAlerts()
	if len(alerts) != 2 || alerts[1].Label != "go build ./..." || alerts[1].Turn != 2 {
		t.Fatalf("both failures stand, with their own turns: %+v", alerts)
	}
	// The suite comes back clean in turn 3: its alert goes, the other stays.
	m.turnCount = 3
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 0})
	alerts = m.inspectorAlerts()
	if len(alerts) != 1 || alerts[0].Label != "go build ./..." {
		t.Fatalf("a clean run clears its own alert only: %+v", alerts)
	}
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 0})
	if alerts = m.inspectorAlerts(); len(alerts) != 0 {
		t.Fatalf("a clean workspace has no alerts: %+v", alerts)
	}
}

// appendEntry stamps the turn an entry belongs to, which is what lets a row
// that outlives its turn still name it.
func TestAppendEntry_StampsTheTurn(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 4
	m.appendEntry(entry{kind: entryCommand, text: "go vet ./...", exitCode: 1})
	if got := m.transcript[len(m.transcript)-1].turn; got != 4 {
		t.Fatalf("entry stamped with turn %d, want 4", got)
	}
	// An entry that already names its turn keeps it.
	m.appendEntry(entry{kind: entryTurnClose, turn: 2, close: &components.TurnClose{}})
	if got := m.transcript[len(m.transcript)-1].turn; got != 2 {
		t.Fatalf("an explicit turn is kept, got %d", got)
	}
}
