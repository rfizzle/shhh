package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// activityModel builds a ready model for feed-rendering tests.
func activityModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

func TestActivityRow_CollapsedNeverShowsOutput(t *testing.T) {
	m := activityModel(t)
	e := entry{kind: entryTool, toolName: "read_file",
		toolArgs:   `{"path":"main.go","start_line":10,"end_line":20}`,
		toolResult: "package main\nfunc main() {}", duration: 120 * time.Millisecond}

	view := stripANSI(m.renderEntry(e, 80))
	if strings.Contains(view, "package main") {
		t.Fatalf("collapsed rows must not show raw output:\n%s", view)
	}
	for _, want := range []string{"⚙", "read", "main.go:10–20", "2 lines"} {
		if !strings.Contains(view, want) {
			t.Fatalf("row should contain %q:\n%s", want, view)
		}
	}
	// Under 0.5s the duration field stays blank rather than spending a
	// column on 0.1s (§6a).
	if strings.Contains(view, "0.1s") {
		t.Fatalf("sub-0.5s calls omit their duration:\n%s", view)
	}
	e.duration = 700 * time.Millisecond
	if slow := stripANSI(m.renderEntry(e, 80)); !strings.Contains(slow, "0.7s") {
		t.Fatalf("a call worth timing keeps its duration:\n%s", slow)
	}
	if lines := strings.Split(strings.TrimRight(view, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("collapsed rendering should be one row, got %d lines:\n%s", len(lines), view)
	}
}

func TestActivityRow_ToolNounsAndKinds(t *testing.T) {
	m := activityModel(t)

	search := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "search",
		toolArgs: `{"pattern":"TODO"}`, toolResult: "a.go:1\nb.go:2\nc.go:3"}, 80))
	for _, want := range []string{"search", "TODO", "3 matches"} {
		if !strings.Contains(search, want) {
			t.Fatalf("search row should contain %q:\n%s", want, search)
		}
	}

	edit := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "edit_file",
		toolArgs: `{"path":"a.go"}`, toolResult: "edited a.go"}, 80))
	if !strings.Contains(edit, "✎") || !strings.Contains(edit, "edit") {
		t.Fatalf("mutating tools render the edit glyph:\n%s", edit)
	}

	child := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "spawn_agent",
		toolArgs: `{"role":"researcher"}`, toolResult: pendingToolResult}, 80))
	if !strings.Contains(child, "◇") && !strings.Contains(child, "▸") {
		t.Fatalf("sub-agent rows render the agent glyph (or running state):\n%s", child)
	}
	if !strings.Contains(child, "running…") {
		t.Fatalf("pending child calls render as running:\n%s", child)
	}
}

// TestActivityVerbs_ClosedVocabulary pins the §6c table: every tool this
// session can call maps onto one of the thirteen verbs, and an unmapped name
// falls through as itself — the signal that the table is stale.
func TestActivityVerbs_ClosedVocabulary(t *testing.T) {
	closed := map[string]bool{"read": true, "search": true, "glob": true, "lsp": true,
		"web": true, "edit": true, "write": true, "patch": true, "run": true,
		"memory": true, "spawn": true, "fan-out": true, "agent": true}
	for tool, verb := range activityVerbs {
		if !closed[verb] {
			t.Fatalf("%s maps onto %q, which is not one of the thirteen verbs", tool, verb)
		}
	}
	for tool, want := range map[string]string{
		"list_directory": "read", "ast_grep": "search", "fd": "glob",
		"references": "lsp", "web_fetch": "web", "web_search": "web",
		"sd": "patch", "quality_gate": "run", "process": "run",
		"remember": "memory", "spawn_agent": "spawn", "agent_report": "agent",
	} {
		if got := activityVerb(tool); got != want {
			t.Fatalf("%s should render as %q, got %q", tool, want, got)
		}
	}
	if got := activityVerb("mystery_tool"); got != "mystery_tool" {
		t.Fatalf("an unmapped tool renders as itself, got %q", got)
	}
}

// TestActivityKinds_GlyphPerAct pins which glyph — and so which rows carry
// the mutation rail (§14) — each tool gets.
func TestActivityKinds_GlyphPerAct(t *testing.T) {
	for tool, want := range map[string]components.ActivityKind{
		"read_file":    components.ActivityTool,
		"references":   components.ActivityTool,
		"write_file":   components.ActivityEdit,
		"sd":           components.ActivityEdit,
		"remember":     components.ActivityEdit,
		"process":      components.ActivityCommand,
		"quality_gate": components.ActivityCommand,
		"spawn_agent":  components.ActivitySubagent,
		"agent_report": components.ActivitySubagent,
	} {
		if got := activityKind(tool); got != want {
			t.Fatalf("%s should render as kind %d, got %d", tool, want, got)
		}
	}
}

// TestActivityRow_CancelledReadsAsYourRefusal: a call abandoned by ctrl+c
// never ran, so it renders ⊘ rather than ✗.
func TestActivityRow_CancelledReadsAsYourRefusal(t *testing.T) {
	m := activityModel(t)
	view := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "write_file",
		toolArgs: `{"path":"a.go"}`, toolResult: cancelledToolResult}, 80))
	for _, want := range []string{"⊘", "denied · you", "—"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a cancelled call should read %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, cancelledToolResult) {
		t.Fatalf("the synthetic result is not output to show:\n%s", view)
	}
}

func TestActivityRow_FailedAutoExpandsBounded(t *testing.T) {
	m := activityModel(t)
	var long strings.Builder
	for i := 0; i < 20; i++ {
		long.WriteString("error detail line\n")
	}
	e := entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"gone.go"}`,
		toolResult: "error: open gone.go: no such file\n" + long.String()}

	view := stripANSI(m.renderEntry(e, 80))
	if !strings.Contains(view, "✗") || !strings.Contains(view, "error: open gone.go") {
		t.Fatalf("failed rows auto-expand with the error first:\n%s", view)
	}
	if n := strings.Count(view, "error detail line"); n >= 20 {
		t.Fatalf("failure auto-expansion must stay bounded, got %d detail lines", n)
	}
}

func TestActivityRow_CommandOutcomes(t *testing.T) {
	m := activityModel(t)

	ok := stripANSI(m.renderEntry(entry{kind: entryCommand, text: "go test ./...",
		toolResult: "ok\tshhh\t0.1s", exitCode: 0, duration: 12 * time.Second}, 80))
	for _, want := range []string{"$", "go test ./...", "ok", "12s"} {
		if !strings.Contains(ok, want) {
			t.Fatalf("command row should contain %q:\n%s", want, ok)
		}
	}
	if strings.Contains(ok, "ok\tshhh") {
		t.Fatalf("successful command output stays collapsed:\n%s", ok)
	}

	failed := stripANSI(m.renderEntry(entry{kind: entryCommand, text: "go vet ./...",
		toolResult: "vet: unreachable code", exitCode: 1}, 80))
	if !strings.Contains(failed, "exit 1") || !strings.Contains(failed, "vet: unreachable code") {
		t.Fatalf("failed commands show the exit code and auto-expand:\n%s", failed)
	}
}

func TestActivityRow_ExpansionShowsFullDetail(t *testing.T) {
	m := activityModel(t)
	e := entry{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"x"}`,
		toolResult: "line one\nline two", expanded: true}
	view := stripANSI(m.renderEntry(e, 80))
	if !strings.Contains(view, "line one") || !strings.Contains(view, "line two") {
		t.Fatalf("expanded rows show the stored result:\n%s", view)
	}
}

func TestVerbosity_HighExpandsLowHidesCounts(t *testing.T) {
	m := activityModel(t)
	e := entry{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"x"}`,
		toolResult: "match line"}

	m.verbosity = verbosityHigh
	if view := stripANSI(m.renderEntry(e, 80)); !strings.Contains(view, "match line") {
		t.Fatalf("high verbosity renders rows expanded:\n%s", view)
	}

	m.verbosity = verbosityLow
	view := stripANSI(m.renderEntry(e, 80))
	if strings.Contains(view, "1 match") {
		t.Fatalf("low verbosity hides counts:\n%s", view)
	}
	if strings.Contains(view, "match line") {
		t.Fatalf("low verbosity stays collapsed:\n%s", view)
	}
}

func TestSlashUI_VerbositySetting(t *testing.T) {
	m := activityModel(t)

	handled, result := m.handleSlashCommand("/ui")
	if !handled || !strings.Contains(result, "normal") {
		t.Fatalf("bare /ui should show the current verbosity, got %q", result)
	}

	handled, result = m.handleSlashCommand("/ui verbosity high")
	if !handled || !strings.Contains(result, "high") {
		t.Fatalf("expected confirmation, got %q", result)
	}
	if m.verbosity != verbosityHigh {
		t.Fatalf("verbosity should update, got %v", m.verbosity)
	}
	if m.cachedCount != 0 || m.cachedRender != "" {
		t.Fatal("changing verbosity must invalidate the render cache")
	}

	if _, result = m.handleSlashCommand("/ui verbosity extreme"); !strings.Contains(result, "unknown verbosity") {
		t.Fatalf("invalid level should error, got %q", result)
	}
	if m.verbosity != verbosityHigh {
		t.Fatal("invalid level must not change the setting")
	}
	if _, result = m.handleSlashCommand("/ui bogus"); !strings.Contains(result, "Usage") {
		t.Fatalf("unknown /ui subcommand shows usage, got %q", result)
	}
}

func TestHelp_ListsUICommand(t *testing.T) {
	if !strings.Contains(helpText(), "/ui") {
		t.Fatal("/help must list /ui")
	}
}

func TestRunningCommandRow_LiveTail(t *testing.T) {
	m := activityModel(t)
	m.state = stateRunningCmd
	m.runningCommand = "go test ./..."
	m.runStart = time.Now().Add(-2 * time.Second)
	m.runTail = &commandTail{}
	m.runTail.Set("ok  internal/agent  0.31s")

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "go test ./...") || !strings.Contains(view, "running…") {
		t.Fatalf("running commands render as a live row:\n%s", view)
	}
	if !strings.Contains(view, "ok  internal/agent") {
		t.Fatalf("the running row shows the live output tail:\n%s", view)
	}
}

func TestExecuteRun_FeedsTailRunner(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) {
			t.Fatal("the tail runner should take precedence")
			return "", 0
		}).
		WithTailRunner(func(ctx context.Context, cmd string, onLine func(string)) (string, int) {
			onLine("first line")
			onLine("second line")
			return "first line\nsecond line", 0
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.pendingRun = "echo hi"

	updated, cmd := m.executeRun()
	m = updated.(Model)
	if m.runningCommand != "echo hi" || m.runTail == nil {
		t.Fatal("executeRun should arm the live row state")
	}
	done := driveCmdDone(t, cmd)
	if done.output != "first line\nsecond line" || done.exitCode != 0 {
		t.Fatalf("tail runner result should flow through, got %+v", done)
	}
	if m.runTail.Line() != "second line" {
		t.Fatalf("the tail should hold the last reported line, got %q", m.runTail.Line())
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	if m.runningCommand != "" || m.runTail != nil {
		t.Fatal("completion should clear the live row state")
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryCommand || last.duration <= 0 {
		t.Fatalf("the finished command entry should carry its duration, got %+v", last)
	}
}

func TestStatusBar_CockpitSegments(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.00001, OutputCostPerToken: 0.00001},
	})
	m := New(msgs, mockStream).WithPricing(table, "gpt-4o")
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	m.state = stateStreaming
	m.agent.BeginToolRound("", nil, func(provider.ToolCall) bool { return false })
	m.steering = []string{"queued note"}

	bar := stripANSI(m.renderStatusBar(160))
	round := fmt.Sprintf("round 1/%d", DefaultMaxToolRounds)
	for _, want := range []string{"⏸ manual", round, "ctx ", "%", "↑41.2k ↓9.8k", "$0.51", "queued 1", "gpt-4o"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("cockpit rail should contain %q, got %q", want, bar)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(120 * time.Millisecond); got != "0.1s" {
		t.Fatalf("want 0.1s, got %q", got)
	}
	if got := formatDuration(42 * time.Second); got != "42s" {
		t.Fatalf("want 42s, got %q", got)
	}
}

func TestActivityArg_Fallbacks(t *testing.T) {
	if got := activityArg("search", `{"pattern":"needle"}`); got != "needle" {
		t.Fatalf("pattern should win, got %q", got)
	}
	if got := activityArg("mystery", `{"depth":3}`); !strings.Contains(got, "depth=3") {
		t.Fatalf("unknown shapes fall back to key=value, got %q", got)
	}
	if got := activityArg("mystery", "not json"); got != "not json" {
		t.Fatalf("unparseable args pass through, got %q", got)
	}
	if got := activityArg("read_file", `{"path":"a.go"}`); got != "a.go" {
		t.Fatalf("plain read shows the path, got %q", got)
	}
}

// TestTranscriptSpacing_UniformRhythm pins the transcript's vertical rhythm:
// one-line notices sit flush against the activity rows they belong to, and
// blocks (turns, diffs, multi-line notices) get exactly one blank line on
// either side — never zero, never two.
func TestTranscriptSpacing_UniformRhythm(t *testing.T) {
	m := activityModel(t)
	row := func(name string) entry {
		return entry{kind: entryTool, toolName: name, toolArgs: `{"name":"agent-4"}`,
			toolResult: "ok", duration: 120 * time.Millisecond}
	}
	m.transcript = []entry{
		{kind: entryUser, text: "Do the same again"},
		{kind: entrySystem, text: "Auto-approved (classifier, 3.4s): writer agent"},
		row("spawn_agent"),
		{kind: entrySystem, text: "Approved agent-4 ▸ run echo hello"},
		row("agent_report"),
		{kind: entrySystem, text: "Multi-line notice:\n  second line"},
		{kind: entryAssistant, text: "Done."},
	}
	lines := strings.Split(strings.TrimRight(stripANSI(m.renderHistory()), "\n"), "\n")

	blank := func(i int) bool { return strings.TrimSpace(lines[i]) == "" }
	var blanks []int
	for i := range lines {
		if blank(i) {
			blanks = append(blanks, i)
			if i > 0 && blank(i-1) {
				t.Fatalf("two blank lines in a row at %d:\n%s", i, strings.Join(lines, "\n"))
			}
		}
	}
	if len(blanks) == 0 {
		t.Fatalf("blocks should be separated by blank lines:\n%s", strings.Join(lines, "\n"))
	}

	// The feed — notice, row, notice, row — packs tight with no gaps.
	feed := []string{"Auto-approved", "◇ spawn", "Approved agent-4", "◇ agent"}
	start := -1
	for i, line := range lines {
		if strings.Contains(line, feed[0]) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("first notice missing:\n%s", strings.Join(lines, "\n"))
	}
	for off, want := range feed {
		if got := lines[start+off]; !strings.Contains(got, want) {
			t.Fatalf("feed line %d should contain %q, got %q:\n%s",
				off, want, got, strings.Join(lines, "\n"))
		}
	}

	// Blocks keep their air: a blank line before "You" is impossible (it leads
	// the transcript), but every later block header has one above it.
	for _, header := range []string{"Multi-line notice:", "Assistant"} {
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), header) {
				if i == 0 || !blank(i-1) {
					t.Fatalf("%q should have one blank line above it:\n%s",
						header, strings.Join(lines, "\n"))
				}
				break
			}
		}
	}
}
