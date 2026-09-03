package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/digest"
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
	// column on 0.1s.
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

// TestActivityVerbs_ClosedVocabulary pins the closed verb table: every tool
// this session can call maps onto one of the fourteen verbs, and an unmapped
// name falls through as itself — the signal that the table is stale.
func TestActivityVerbs_ClosedVocabulary(t *testing.T) {
	closed := map[string]bool{"read": true, "search": true, "glob": true, "lsp": true,
		"web": true, "edit": true, "write": true, "patch": true, "run": true,
		"memory": true, "spawn": true, "fan-out": true, "agent": true,
		"report": true}
	for tool, verb := range activityVerbs {
		if !closed[verb] {
			t.Fatalf("%s maps onto %q, which is not one of the fourteen verbs", tool, verb)
		}
	}
	for tool, want := range map[string]string{
		"list_directory": "read", "ast_grep": "search", "fd": "glob",
		"references": "lsp", "workspace_symbol": "lsp", "document_symbol": "lsp",
		"hover": "lsp", "web_fetch": "web", "web_search": "web",
		"sd": "patch", "quality_gate": "run", "process": "run",
		"remember": "memory", "spawn_agent": "spawn", "agent_report": "agent",
		"report": "report",
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
// the mutation rail — each tool gets.
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
		"report":       components.ActivityReport,
	} {
		if got := (Model{}).activityKind(tool); got != want {
			t.Fatalf("%s should render as kind %d, got %d", tool, want, got)
		}
	}
}

// TestActivityRow_ReportLinkIsTheOutcome: a published report's row carries
// the page URL in the outcome field — the one that never clips — and keeps
// nothing else, because the page is the body.
func TestActivityRow_ReportLinkIsTheOutcome(t *testing.T) {
	m := activityModel(t)
	e := entry{kind: entryTool, toolName: "report",
		toolArgs:   `{"title":"suite timing breakdown","blocks":[]}`,
		toolResult: "http://127.0.0.1:52104/r/rp-8f3a11c04b2d9e61\nreport \"suite timing breakdown\" published (id rp-8f3a11c04b2d9e61).",
		duration:   800 * time.Millisecond}

	view := stripANSI(m.renderEntry(e, 120))
	for _, want := range []string{"⛁", "report", "suite timing breakdown", "→ http://127.0.0.1:52104/r/rp-8f3a11c04b2d9e61"} {
		if !strings.Contains(view, want) {
			t.Fatalf("row should contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "published") {
		t.Fatalf("the result body belongs to the page, not the row:\n%s", view)
	}
	if lines := strings.Split(strings.TrimRight(view, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("a report row is one line, got %d:\n%s", len(lines), view)
	}

	// An error result keeps the failure grammar: ✗ and the error outcome,
	// never a dead link.
	e.toolResult = "error: block 2 (freehand): freehand rejected: <script> is not allowed"
	failed := stripANSI(m.renderEntry(e, 120))
	for _, want := range []string{"✗", "error"} {
		if !strings.Contains(failed, want) {
			t.Fatalf("failed report row should contain %q:\n%s", want, failed)
		}
	}
	if strings.Contains(failed, "→ ") {
		t.Fatalf("a failed report must not offer a link:\n%s", failed)
	}
}

// TestActivityKinds_ServerCallsDrawByTheUsersWord: a server the person
// marked read-only draws as a read; any other server's call is ⇄ with the
// rail, because shhh cannot see what the far end did.
func TestActivityKinds_ServerCallsDrawByTheUsersWord(t *testing.T) {
	m := activityModel(t).WithMCP(MCP{
		Has:      func(name string) bool { return strings.HasPrefix(name, "docs__") || strings.HasPrefix(name, "gh__") },
		ReadOnly: func(name string) bool { return strings.HasPrefix(name, "docs__") },
	})
	if got := m.activityKind("docs__search"); got != components.ActivityTool {
		t.Fatalf("read-only server call kind = %d, want a read", got)
	}
	if got := m.activityKind("gh__create_issue"); got != components.ActivityRemote {
		t.Fatalf("gated server call kind = %d, want remote", got)
	}
	if got := activityVerb("gh__create_issue"); got != "mcp" {
		t.Fatalf("verb = %q", got)
	}
	if got := digest.Arg("gh__create_issue", `{"title":"Bug","body":"long\ntext"}`); got != "gh create_issue body=long text title=Bug" {
		t.Fatalf("target = %q", got)
	}
	view := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "gh__create_issue",
		toolArgs: `{"title":"Bug"}`, toolResult: "created #42"}, 80))
	for _, want := range []string{"⇄", "▎", "mcp", "gh create_issue title=Bug"} {
		if !strings.Contains(view, want) {
			t.Fatalf("server call row lacks %q:\n%s", want, view)
		}
	}
	view = stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "docs__search",
		toolArgs: `{"q":"x"}`, toolResult: "hit"}, 80))
	if strings.Contains(view, "⇄") || strings.Contains(view, "▎") {
		t.Fatalf("read-only server call carries a rail or the remote glyph:\n%s", view)
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
	if m.cached.count != 0 || m.cached.lines != nil {
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

// What the terminal can do (
// docs/architecture.md#only-one-place-speaks-to-the-terminal). The readout is
// a diagnostic, so what it must never do is let "shhh did not ask" read as
// "the terminal said no".
func TestUITerminal_NeverAskedIsNotANo(t *testing.T) {
	m := activityModel(t)
	// The zero value is a session whose probe has not gone out.
	got := m.uiCommand([]string{"/ui", "terminal"})
	if !strings.Contains(got, "not asked") {
		t.Errorf("an unasked terminal must say so, got %q", got)
	}
	for _, no := range []string{"neither kitty graphics nor sixel", "not reported"} {
		if strings.Contains(got, no) {
			t.Errorf("%q reads as an answer the terminal never gave: %q", no, got)
		}
	}
	if !strings.Contains(m.uiCommand([]string{"/ui"}), "Terminal: not asked") {
		t.Error("the bare /ui summary should name the terminal, or say it was not asked")
	}
}

func TestUITerminal_ReportsWhatCameBack(t *testing.T) {
	m := activityModel(t)
	m.caps.Asked = true
	m.caps.Name = "ghostty 1.2.0"
	m.caps.Kitty = true
	m.caps.Notifications = true
	m.caps.FocusEvents = true
	m.caps.PixelWidth, m.caps.PixelHeight = 720, 570

	got := m.uiCommand([]string{"/ui", "terminal"})
	for _, want := range []string{
		"Terminal: ghostty 1.2.0.",
		"Inline images: kitty graphics.",
		"Desktop notifications: OSC 99.",
		"Focus events: reported.",
		// 720/80 and 570/30 — the terminal's pixels over the session's own
		// columns and rows, which is the only place the two meet.
		"Cell size: 9×19 px.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(m.uiCommand([]string{"/ui"}), "Terminal: ghostty 1.2.0") {
		t.Error("the bare /ui summary should name the terminal it asked")
	}
}

func TestUITerminal_HeldQuestionsSayWhy(t *testing.T) {
	m := activityModel(t)
	m.caps.Asked = true
	m.caps.Held = "the reply would have to come back over ssh"
	m.caps.FocusEvents = true

	got := m.uiCommand([]string{"/ui", "terminal"})
	if !strings.Contains(got, "Inline images: not asked.") {
		t.Errorf("a held question is not a no, got %q", got)
	}
	if !strings.Contains(got, "over ssh") {
		t.Errorf("the readout has to name the reason it held back, got %q", got)
	}
	// The safe questions went out either way, so their answers stand.
	if !strings.Contains(got, "Focus events: reported.") {
		t.Errorf("holding the graphics questions must not withhold the rest, got %q", got)
	}
}

// The probe is asked once, when the program hands over its environment — and
// it is the program's environment, not the process's, because over ssh those
// are two different machines.
func TestTerminalProbe_AsksOnTheProgramsEnvironment(t *testing.T) {
	// A test binary's stdout is not a terminal, and the probe reads that off
	// the profile shhh already settled: with nothing on the other
	// end there is nothing to ask.
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })

	m := activityModel(t)
	updated, cmd := m.Update(tea.EnvMsg{"TERM=xterm-256color"})
	next := updated.(Model)
	if !next.caps.Asked {
		t.Fatal("the environment arriving is the moment to ask")
	}
	if cmd == nil {
		t.Fatal("nothing was written to the terminal")
	}
	// The replies land wherever they land, and the model folds them in
	// without any of them being routed anywhere else.
	updated, _ = next.Update(tea.TerminalVersionMsg{Name: "ghostty 1.2.0"})
	if got := updated.(Model).caps.Name; got != "ghostty 1.2.0" {
		t.Errorf("the reply did not reach the probe, Name = %q", got)
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
