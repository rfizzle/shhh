package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
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
	// The ladder's top rung is a 130-column terminal, both directions. The
	// cases are written in the datum the constant is in, which is that width
	// less the surface's inset on each side.
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
	wide := inspectorModel(t, 144, 40) // content 140 → 49-column rail, 90-column pane
	if wide.contentWidth() != 140 {
		t.Fatalf("content width = %d, want 140", wide.contentWidth())
	}
	if got := wide.paneWidth(); got != 90 {
		t.Fatalf("transcript pane = %d columns, want 90", got)
	}
	// The pane holds the scroll gutter's columns back — the thumb's own and
	// the empty one that keeps it off the divider — so the transcript wraps
	// inside them, and so does the viewport, which is the selection's
	// coordinate space.
	if got := wide.transcriptWidth(); got != 88 {
		t.Fatalf("transcript wraps to %d columns, want 88", got)
	}
	if wide.viewport.Width() != 88 {
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
	if len(rail.Alerts) != 1 {
		t.Fatalf("the session's broken command: %+v", rail.Alerts)
	}
	if a := rail.Alerts[0]; a.Label != "go test" || a.Note != "exit 1" || a.Turn != 1 || a.Superseded {
		t.Fatalf("the alert names the turn that broke it: %+v", a)
	}
	if rail.Context == nil || rail.Context.Window != 200000 || rail.Context.Pct != 20 {
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
	if got := m.uiCommand([]string{"/ui"}); !strings.Contains(got, "two panes") || !strings.Contains(got, "90-column transcript") {
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
	m := inspectorModel(t, 200, 40) // content 196 → a 63-column rail on the ladder
	if got := m.columns().inspector.Dx(); got != 63 {
		t.Fatalf("the ladder gives a 200-column terminal a %d-column rail, want 63", got)
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
	if got := m.columns().inspector.Dx(); got != 63 {
		t.Fatalf("the rail is %d columns after /ui rail 72, want the ladder's 63", got)
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
	m.startNewSession()
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

// A file the session changed the permissions of and nothing else has a row on
// the rail, and the row says the two modes: the block is what the session did
// to the workspace, and leaving the file out to avoid printing `+0 −0` left
// the one thing that happened off the screen entirely.
func TestInspectorChanges_ModeOnlyFileIsOnTheRail(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.changes.Add(m.turnCount, changeset.Record{
		Path: "scripts/build.sh", BeforeExists: true, AfterExists: true,
		Before: "one\n", After: "one\n", BeforeMode: 0o644, AfterMode: 0o755,
	})

	c := m.inspectorChanges()
	var row components.InspectorFile
	for _, f := range c.Files {
		if f.Path == "scripts/build.sh" {
			row = f
		}
	}
	if row.Path == "" {
		t.Fatalf("CHANGES should hold the file: %+v", c)
	}
	if row.Mode != "mode 0644 → 0755" {
		t.Fatalf("the row carries the mode the session set: %+v", row)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "mode 0644 → 0755") {
		t.Fatalf("the rail states the mode:\n%s", view)
	}
	if strings.Contains(view, "+0 −0") {
		t.Fatalf("the rail counts nothing it did not measure:\n%s", view)
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
// stops being news when the same command comes back clean.
func TestInspectorAlerts_PersistUntilTheWorkspaceIsClean(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "fix it"})
	if live := m.inspectorAlerts().Live(); len(live) != 1 || live[0].Turn != 1 {
		t.Fatalf("turn 1's failure is still standing in turn 2: %+v", live)
	}
	// A second command breaks in turn 2; both are standing.
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2})
	live := m.inspectorAlerts().Live()
	if len(live) != 2 || live[1].Label != "go build" || live[1].Turn != 2 {
		t.Fatalf("both failures stand, with their own turns: %+v", live)
	}
	// The tests come back clean in turn 3: that alert is answered, the other
	// stands. Neither is deleted — the block counts what it took to get here.
	m.turnCount = 3
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 0})
	alerts := m.inspectorAlerts()
	live = alerts.Live()
	if len(live) != 1 || live[0].Label != "go build" {
		t.Fatalf("a clean run answers its own alert only: %+v", alerts)
	}
	if len(alerts) != 2 || !alerts[0].Superseded {
		t.Fatalf("an answered alert is marked and kept: %+v", alerts)
	}
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 0})
	if live = m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("a clean workspace has no live alerts: %+v", live)
	}
}

// Three runs of one command in one turn are one alert. The row is the
// command's name, carries the run count, and reports the last run's outcome —
// the last run is what the workspace is currently like.
func TestInspectorAlerts_RunsOfOneCommandAreOneRow(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "format it"})
	for _, dir := range []string{"internal/ui", "internal/agent", "internal/cli"} {
		m.appendEntry(entry{kind: entryCommand, text: "gofmt -w " + dir, exitCode: 2})
	}
	live := m.inspectorAlerts().Live()
	// Turn 1's own failure is still there; the formatter is the new row.
	if len(live) != 2 {
		t.Fatalf("three runs of the formatter are one alert: %+v", live)
	}
	if a := live[1]; a.Label != "gofmt" || a.Runs != 3 || a.Turn != 2 || a.Note != "exit 2" {
		t.Fatalf("the alert is the command, the turn and its runs: %+v", a)
	}
	// The same command breaking again in a later turn is the alert going on
	// rather than a second one: the runs behind it go up, the turn it broke
	// in stays where it was, and the earlier turn is part of it rather than a
	// row of its own.
	m.turnCount = 3
	m.appendEntry(entry{kind: entryCommand, text: "gofmt -l .", exitCode: 2})
	alerts := m.inspectorAlerts()
	live = alerts.Live()
	if len(live) != 2 {
		t.Fatalf("a later failure is the standing alert, not a second one: %+v", live)
	}
	if a := live[1]; a.Label != "gofmt" || a.Runs != 4 || a.Turn != 2 || a.Turns != 2 {
		t.Fatalf("the alert spans the turns the command broke in: %+v", a)
	}
	if len(alerts) != 2 {
		t.Fatalf("the earlier turn is not a superseded entry of its own: %+v", alerts)
	}
}

// A command still broken in the fourth turn running is one standing alert and
// not four: the row says the turn it first broke in and every run behind it,
// and the heading's count is the rows under it
// (docs/interface/surfaces.md#the-inspector-rail).
func TestInspectorAlerts_ACommandBrokenInFourTurnsIsOneAlert(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	for turn := int64(2); turn <= 4; turn++ {
		m.turnCount = turn
		m.appendEntry(entry{kind: entryUser, text: "try it again"})
		m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 1})
	}
	alerts := m.inspectorAlerts()
	live := alerts.Live()
	if len(live) != 1 {
		t.Fatalf("four turns of one broken suite are one standing alert: %+v", alerts)
	}
	if a := live[0]; a.Label != "go test" || a.Runs != 4 || a.Turn != 1 || a.Turns != 4 {
		t.Fatalf("the alert carries the turn it broke in and the runs since: %+v", a)
	}
	if len(alerts) != 1 {
		t.Fatalf("the turns it stood in are one entry, not one each: %+v", alerts)
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "1 standing") || !strings.Contains(view, "since turn 1") {
		t.Fatalf("the heading counts what is under it:\n%s", view)
	}
	if strings.Count(view, "✗ go test") != 1 {
		t.Fatalf("one broken command is one row:\n%s", view)
	}
}

// An answered episode is one superseded entry however many turns it stood in:
// the fold counts what was fixed, not the attempts behind it, and the entry
// keeps the account the standing row carried
// (docs/interface/surfaces.md#the-inspector-rail).
func TestInspectorAlerts_AnAnsweredEpisodeIsOneSupersededEntry(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	for turn := int64(2); turn <= 3; turn++ {
		m.turnCount = turn
		m.appendEntry(entry{kind: entryUser, text: "try it again"})
		m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 1})
	}
	m.appendEntry(entry{kind: entryCommand, text: "go vet ./...", exitCode: 1})
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "2 standing") || strings.Contains(view, "superseded") {
		t.Fatalf("before its answer the episode is one standing alert and nothing folded:\n%s", view)
	}
	m.turnCount = 4
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 0})
	alerts := m.inspectorAlerts()
	if len(alerts) != 2 {
		t.Fatalf("three turns of one failure are one entry once answered: %+v", alerts)
	}
	a := alerts[0]
	if a.Label != "go test" || !a.Superseded || a.Runs != 3 || a.Turn != 1 || a.Turns != 3 {
		t.Fatalf("the superseded entry carries the episode's runs and turns: %+v", a)
	}
	view = stripANSI(m.View().Content)
	if !strings.Contains(view, "1 standing") || !strings.Contains(view, "… 1 superseded") {
		t.Fatalf("after its answer the episode is one superseded entry:\n%s", view)
	}
}

// A failure answered inside its own turn is an episode too: it is never a
// row, because it never stood, but the fold counts it — it was red on the way
// to green (docs/interface/surfaces.md#the-inspector-rail).
func TestInspectorAlerts_AFailureAnsweredInItsOwnTurnIsCounted(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "build it"})
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2})
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2})
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 0})
	alerts := m.inspectorAlerts()
	if len(alerts) != 2 {
		t.Fatalf("turn 1's failure and the answered build: %+v", alerts)
	}
	if a := alerts[1]; a.Label != "go build" || !a.Superseded || a.Runs != 2 || a.Turn != 2 || a.Turns != 1 {
		t.Fatalf("the answered build is one superseded entry of two runs: %+v", a)
	}
	if live := alerts.Live(); len(live) != 1 || live[0].Label != "go test" {
		t.Fatalf("it is never a live alert: %+v", live)
	}
}

// A passing suite ends an episode: the same command failing after it is a
// fact about a tree the pass never saw, so it is news of its own rather than
// the answered episode going on.
func TestInspectorAlerts_AFailureAfterAPassIsANewEpisode(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "check it"})
	m.appendCloseGateRow("default", gateResult("PASS", 5, 5))
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: 1})
	alerts := m.inspectorAlerts()
	if len(alerts) != 2 || !alerts[0].Superseded || alerts[1].Superseded {
		t.Fatalf("the answered episode and the new one: %+v", alerts)
	}
	if a := alerts[1]; a.Turn != 2 || a.Runs != 1 || a.Turns != 1 {
		t.Fatalf("the new episode starts at its own failure: %+v", a)
	}
}

// A command whose first word is a multiplexer is named by its subcommand
// too: a broken build and a broken test suite are two things wrong with the
// workspace, and one row saying `go` would name neither.
func TestAlertName_KeepsTheSubcommandAndDropsTheArguments(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{"gofmt -w internal/ui/chat/inspector.go", "gofmt"},
		{"go test ./internal/agent/...", "go test"},
		{"go build ./...", "go build"},
		{"make test", "make test"},
		{"./checks/unit.sh --verbose", "./checks/unit.sh"},
		{"npm run build", "npm run"},
		{"CGO_ENABLED=0 go build", "go build"},
		{"CGO_ENABLED=0 GOFLAGS=-mod=mod go test ./...", "go test"},
		{"gofmt", "gofmt"},
		{"", ""},
	} {
		if got := alertName(c.command); got != c.want {
			t.Fatalf("alertName(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

// A command the reader stopped is neither bad news nor an answer: nobody let
// it reach a verdict, so it neither raises an alert nor clears one — and it
// does not overwrite what the run before it reported either.
func TestInspectorAlerts_AStoppedRunNeitherRaisesNorClears(t *testing.T) {
	m := inspectorModel(t, 144, 40)
	m.turnCount = 2
	m.appendEntry(entry{kind: entryUser, text: "try again"})
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", exitCode: -1,
		end: commandEnd{outcome: components.OutcomeStopped}})
	live := m.inspectorAlerts().Live()
	if len(live) != 1 || live[0].Turn != 1 {
		t.Fatalf("a cancelled run answers nothing: %+v", live)
	}
	if live[0].Superseded {
		t.Fatalf("turn 1's failure is still standing: %+v", live[0])
	}
	// A failure and then a cancelled re-run, in one turn and under one key:
	// the failure is what the workspace is still like, and the stop says
	// nothing about it at all.
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2})
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: -1,
		end: commandEnd{outcome: components.OutcomeStopped}})
	live = m.inspectorAlerts().Live()
	if len(live) != 2 {
		t.Fatalf("a stop cannot take a standing failure off the rail: %+v", live)
	}
	if a := live[1]; a.Label != "go build" || a.Note != "exit 2" || a.Runs != 1 {
		t.Fatalf("the alert is the run that reached a verdict: %+v", a)
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

// A frame tiles a run of entries once. Reading mode's paint asked for the
// session's tiling eight times before this, and the scan is over every entry
// in the session, so a long session paid for all eight on every keystroke.
// The map counts the tilings a frame took.
func TestFrameMemo_TilesEachRunOnce(t *testing.T) {
	m := inspectorModel(t, 160, 40)
	m.framed = &frame{}

	first := m.blocksOf(m.transcript)
	again := m.blocksOf(m.transcript)
	if len(m.framed.blocks) != 1 {
		t.Fatalf("one run asked for twice is one tiling, took %d", len(m.framed.blocks))
	}
	if len(first) == 0 || &first[0] != &again[0] {
		t.Fatal("the second reader should have been handed the first one's tiling")
	}

	// A different run is a different tiling, not the same one reused: a tail
	// of the transcript and an attached child's own are different lists of
	// entries that one shared answer would confuse.
	m.blocksOf(m.transcript[1:])
	if len(m.framed.blocks) != 2 {
		t.Fatalf("two runs is two tilings, took %d", len(m.framed.blocks))
	}
}

// Outside a paint there is no frame to reuse, and every caller tiles as it
// always did.
func TestFrameMemo_TilesFreshWithNoFrame(t *testing.T) {
	m := inspectorModel(t, 160, 40)
	first := m.blocksOf(m.transcript)
	again := m.blocksOf(m.transcript)
	if len(first) == 0 || &first[0] == &again[0] {
		t.Fatal("with no frame, each call tiles for itself")
	}
}

// The rail is keyed on the spinner's frame, what the transcript reads and the
// turn count, so a frame that asks twice builds it once and a reading that
// moved builds it again.
func TestRailMemo_IsKeyedRatherThanRebuilt(t *testing.T) {
	m := inspectorModel(t, 160, 40)
	m.framed = &frame{}

	first := m.inspectorData()
	held := m.framed.rail
	if held == nil {
		t.Fatal("the rail should have been resolved onto the frame")
	}
	if got := m.inspectorData(); m.framed.rail != held {
		t.Fatalf("a second reader should have been handed the resolved rail, got %+v", got.Frame)
	}

	// The spinner ticking is a reading that moved, and the rail draws it.
	m.spinFrame++
	if m.inspectorData(); m.framed.rail == held {
		t.Fatal("a spinner tick should have resolved the rail again")
	}
	if m.framed.rail.rail.Frame != first.Frame+1 {
		t.Fatalf("the rebuilt rail should carry the new frame, got %d", m.framed.rail.rail.Frame)
	}
}

// alertMemoModel is a session with one failing command on the transcript and
// one big tool result behind it that a trim can take. The failing command is
// the rail's standing alert; the big result is what gives the trim something
// to elide, so the trim rewrites a row where it lies without the transcript
// getting any shorter — which is the case a length-keyed memo gets wrong.
func alertMemoModel(t *testing.T) Model {
	t.Helper()
	big := strings.Repeat("line\n", 8000) // ~10k estimated tokens
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.turnCount = 1
	m.appendEntry(entry{kind: entryCommand, text: "go test ./internal/agent/...", exitCode: 1})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolResult: big})
	m.contextTokens = 30000
	if live := m.inspectorAlerts().Live(); len(live) != 1 {
		t.Fatalf("the failing command is the session's standing alert, got %+v", live)
	}
	return m
}

// clearBehindTheMemo rewrites the row the alert was read off — the command's
// exit code — with no row landing and no revision recorded, which is a thing
// only a test can do. It is how the two tests below tell a scan that ran from
// an answer handed back out of the box.
func clearBehindTheMemo(m *Model) {
	m.transcript[0].exitCode = 0
}

// The context trim replaces bodies in place and leaves exactly as many rows
// as it found, so the transcript's length says nothing happened. The rail's
// alert scan is memoised on the revision as well as the count, so the trim
// makes the next reader scan again — a memo keyed on the length alone would
// answer with the session as it was before the trim, which is how a failure
// the quality gate has already answered comes back red.
func TestAlertMemo_ATrimMakesTheRailReadAgain(t *testing.T) {
	m := alertMemoModel(t)
	was := m.transcriptReading()
	clearBehindTheMemo(&m)

	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	now := m.transcriptReading()
	if now.entries != was.entries {
		t.Fatalf("the trim left %d rows, was %d — the length is the reading that cannot see it",
			now.entries, was.entries)
	}
	if now.rev == was.rev {
		t.Fatal("the trim rewrote a row where it lies and recorded no revision")
	}
	if _, elided := agent.Elided(m.transcript[1].toolResult); !elided {
		t.Fatal("the trim should have taken the big result out of the row")
	}
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("the rail read the transcript as it was before the trim, got %+v", live)
	}
}

// Two frames with nothing between them but a spinner tick read the scan once.
// The rail itself is resolved again on every tick — it draws the spinner —
// and the scan walks every command in the session, so a two-hour transcript
// would pay for its own length on every frame if the tick reached it.
func TestAlertMemo_TwoFramesWithNoMutationScanOnce(t *testing.T) {
	m := alertMemoModel(t)
	clearBehindTheMemo(&m)

	m.spinFrame++
	if live := m.inspectorAlerts().Live(); len(live) != 1 {
		t.Fatalf("the second frame walked the transcript again, got %+v", live)
	}
	// A recorded revision is the other half of the same rule: it is what the
	// rewrite above would have carried had anything but a test made it.
	m.transcriptRev++
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("a recorded revision makes the next reader scan, got %+v", live)
	}
}

// Every rewrite in place already drops the render caches, and that call is
// what records the revision — so a path nobody wrote a bump for (the mouse
// opening a row, the output view, a backlog run's row) carries one anyway.
func TestInvalidateRenderCache_RecordsATranscriptRevision(t *testing.T) {
	m := alertMemoModel(t)
	was := m.transcriptReading()
	clearBehindTheMemo(&m)
	m.invalidateRenderCache()
	if m.transcriptReading() == was {
		t.Fatal("dropping the render caches recorded no transcript revision")
	}
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("the rail answered from before the rewrite, got %+v", live)
	}
}
