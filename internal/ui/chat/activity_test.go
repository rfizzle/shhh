package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/web"
)

// searchHits is two matches in search's own format, which is the format the
// row's count is read out of: a fixture that only looks like output would
// count as nothing found.
const searchHits = "internal/agent/loop.go:88: \t\treturn ErrRoundLimit\n" +
	"internal/agent/agent.go:12: // ErrRoundLimit ends a turn."

// activityModel builds a ready model for feed-rendering tests.
func activityModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

// A notice is prose, and prose that ran past the right edge was not a long
// row: it was a row the terminal broke wherever it happened to run out, over
// the top of whatever the pane drew beside it. It wraps instead — and a
// notice that arrived already laid out over several lines is left alone,
// because re-flowing it by words takes a table apart to save a column
// (docs/interface/principles.md#one-grid).
func TestSystemRow_NoNoticeIsWiderThanThePane(t *testing.T) {
	m := activityModel(t)
	const long = "~/scratch/notes is not in a git repository and a run ends in a commit — " +
		"/todo run cache-ttl --no-commit runs it without one, or todo.commit = false makes that the default."
	const laid = "Keys:\n  enter          Send message\n  ctrl+n         Queue the draft"
	m.transcript = []entry{{kind: entrySystem, text: long}, {kind: entrySystem, text: laid}}
	m.invalidateRenderCache()

	width := m.transcriptWidth()
	lines := strings.Split(strings.TrimRight(stripANSI(m.renderHistory()), "\n"), "\n")
	for i, line := range lines {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d is %d columns in a %d-column pane: %q", i, w, width, line)
		}
	}
	if joined := strings.Join(lines, " "); !strings.Contains(joined, "makes that the default.") {
		t.Fatalf("wrapping drops nothing:\n%s", strings.Join(lines, "\n"))
	}
	// The block that laid itself out comes back with its own columns, line
	// for line, rather than re-flowed by words — on the content column, like
	// every other notice (docs/interface/surfaces.md#the-leading-columns).
	gutter := strings.Repeat(" ", components.GridPointerWidth)
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimPrefix(strings.TrimRight(l, " "), gutter)
	}
	for _, want := range strings.Split(laid, "\n") {
		if !slices.Contains(trimmed, want) {
			t.Fatalf("a notice that laid itself out is left alone, want %q in:\n%s",
				want, strings.Join(lines, "\n"))
		}
	}
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
		toolArgs: `{"pattern":"TODO"}`, toolResult: "a.go:1: // TODO\nb.go:2: // TODO\nc.go:3: // TODO"}, 80))
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

// Three searches of one package are three questions, and the rows say which
// three. Led by the directory they would be the same row drawn three times,
// which is what a reader scanning a feed for a run going in circles reads as
// one — and the scope still has to be there, or the row says where nothing.
func TestActivityRow_SearchesOfOnePackageReadAsTheirPatterns(t *testing.T) {
	m := activityModel(t)
	seen := map[string]bool{}
	for _, pattern := range []string{"steeringItem", "queuedSteer", "authorOf"} {
		row := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: "search",
			toolArgs:   fmt.Sprintf(`{"pattern":%q,"path":"internal/ui/chat"}`, pattern),
			toolResult: "a.go:1"}, 80))
		if !strings.Contains(row, pattern+" ./internal/ui/chat") {
			t.Fatalf("the row should read as what was asked and where:\n%s", row)
		}
		seen[strings.TrimSpace(row)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("three questions drew %d distinct rows", len(seen))
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
		if got := (Model{}).toolKind(tool); got != want {
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
	if got := m.toolKind("docs__search"); got != components.ActivityTool {
		t.Fatalf("read-only server call kind = %d, want a read", got)
	}
	if got := m.toolKind("gh__create_issue"); got != components.ActivityRemote {
		t.Fatalf("gated server call kind = %d, want remote", got)
	}
	if got := m.callReceipt("gh__create_issue", "", "").Verb; got != "mcp" {
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

// A call the reader answered at a card says who answered, on the act's own
// row and after its counts. The row is where the decision is stated — an act
// and the approval of it are one row — and the reader's yes is not a rule's:
// scrolling back a week later is exactly when the calls they were shown
// stop being distinguishable from the ones they were not
// (docs/interface/principles.md#two-denials-are-not-one-denial).
func TestActivityRow_ACardYouAnsweredNamesYou(t *testing.T) {
	m := gatedModel(t, nil, nil).
		WithRunner(legacyRunner(func(context.Context, string) (string, int) { return "ok", 0 }))
	m, _ = runOnce(t, m, "go test ./internal/agent/...")

	answered := lastCommandRow(t, m, 110)
	if want := "approved by you"; !strings.Contains(answered, want) {
		t.Fatalf("a command you answered at the card states %q:\n%s", want, answered)
	}

	// The two never both hold, and neither is claimed by a call that reached
	// no card at all: a `/run` the reader typed has no decision behind it.
	auto := activityModel(t)
	rule := stripANSI(auto.renderEntry(entry{kind: entryCommand, text: "go test ./...",
		toolResult: "ok", allowedBy: "auto mode"}, 110))
	if strings.Contains(rule, "approved") || !strings.Contains(rule, "auto-allowed · auto mode") {
		t.Fatalf("a rule's yes keeps its own two words:\n%s", rule)
	}
	typed := stripANSI(auto.renderEntry(entry{kind: entryCommand, text: "ls", toolResult: "ok"}, 110))
	if strings.Contains(typed, "approved") {
		t.Fatalf("a command nobody gated carries no decision:\n%s", typed)
	}
}

// A command the reader rewrote at the card is still a command they answered,
// and the row has one field for how it came to be the act it is: the
// amendment is the more particular fact and takes it.
func TestActivityRow_AnAmendmentOutranksThePlainApproval(t *testing.T) {
	m := activityModel(t)
	row := stripANSI(m.renderEntry(entry{kind: entryCommand, text: "go test ./internal/agent/...",
		toolResult: "ok", approvedBy: decidedByYou, amendedFrom: "go test ./..."}, 110))
	if !strings.Contains(row, "amended · you") || strings.Contains(row, "approved by you") {
		t.Fatalf("an amended command says the reader rewrote it, once:\n%s", row)
	}
}

// lastCommandRow renders the last command entry in the transcript.
func lastCommandRow(t *testing.T, m Model, width int) string {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryCommand {
			return stripANSI(m.renderEntry(m.transcript[i], width))
		}
	}
	t.Fatal("the transcript has no command row")
	return ""
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
	if _, result = m.handleSlashCommand("/ui bogus"); !strings.Contains(result, "usage") {
		t.Fatalf("unknown /ui subcommand shows usage, got %q", result)
	}
}

// The rung a reader chose is a setting like the theme: it outlives the
// session that chose it, and the reply says so.
func TestSlashUI_VerbosityIsSaved(t *testing.T) {
	m := activityModel(t)
	written := map[string]string{}
	m.writeConfig = func(key, value string) error {
		written[key] = value
		return nil
	}
	_, result := m.handleSlashCommand("/ui verbosity low")
	if written["appearance.verbosity"] != "low" {
		t.Fatalf("the rung was not persisted: %v", written)
	}
	if !strings.Contains(result, "saved") {
		t.Fatalf("the reply should say the rung will last, got %q", result)
	}

	m.writeConfig = nil
	if _, result = m.handleSlashCommand("/ui verbosity high"); !strings.Contains(result, "this session only") {
		t.Fatalf("a session that cannot write says the rung is its own, got %q", result)
	}
	if m.verbosity != verbosityHigh {
		t.Fatal("a session with no writer still takes the rung")
	}
}

// The setting a session starts on is the config's word, and a word the
// ladder does not have starts it on normal rather than stopping it.
func TestWithVerbosity_StartsOnTheConfiguredRung(t *testing.T) {
	for word, want := range map[string]verbosity{
		"": verbosityNormal, "low": verbosityLow, "normal": verbosityNormal,
		"high": verbosityHigh, " high ": verbosityHigh, "loud": verbosityNormal,
	} {
		if got := activityModel(t).WithVerbosity(word).verbosity; got != want {
			t.Errorf("WithVerbosity(%q) = %v, want %v", word, got, want)
		}
	}
}

// The ladder is ordered: a rung draws everything the rungs below it draw.
func TestDensity_ARungIncludesTheOnesBelowIt(t *testing.T) {
	m := activityModel(t)
	for _, set := range []verbosity{verbosityLow, verbosityNormal, verbosityHigh} {
		m.verbosity = set
		for _, rung := range []verbosity{verbosityLow, verbosityNormal, verbosityHigh} {
			if got, want := m.density(rung), rung <= set; got != want {
				t.Errorf("at %v, density(%v) = %v, want %v", set, rung, got, want)
			}
		}
	}
}

func TestHelp_ListsUICommand(t *testing.T) {
	m := frameModel(t, 80, 30)
	if !strings.Contains(helpText(&m), "/ui") {
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
		WithRunner(legacyRunner(func(ctx context.Context, cmd string) (string, int) {
			t.Fatal("the tail runner should take precedence")
			return "", 0
		})).
		WithTailRunner(legacyTailRunner(func(ctx context.Context, cmd string, onLine func(string)) (string, int) {
			onLine("first line")
			onLine("second line")
			return "first line\nsecond line", 0
		}))
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
	m.steering = []steeringItem{{text: "queued note"}}

	bar := stripANSI(m.renderStatusBar(160))
	round := fmt.Sprintf("round 1 of %d", DefaultMaxToolRounds)
	for _, want := range []string{"⏸ manual", round, "ctx ", "%", "$0.51", "1 queued for this turn"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("cockpit rail should contain %q, got %q", want, bar)
		}
	}
	// Beside a price the token pair is the bill read a second way, and the
	// model is the header's (docs/interface/surfaces.md#the-input-frame).
	for _, gone := range []string{"↑41,200", "gpt-4o"} {
		if strings.Contains(bar, gone) {
			t.Fatalf("cockpit rail should not carry %q, got %q", gone, bar)
		}
	}
}

// Where no price is known the token pair stands in for the spend, and it is
// the only time it does. While a turn is spending them they print every digit.
func TestStatusBar_TokensStandInWhereNoPriceIsKnown(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(nil, "scripted-model")
	m.accumulateUsage(&provider.Usage{PromptTokens: 41200, CompletionTokens: 9800})
	m.state = stateStreaming

	bar := stripANSI(m.renderStatusBar(160))
	if !strings.Contains(bar, "↑41,200 ↓9,800") {
		t.Fatalf("an unpriced rail states the token pair, got %q", bar)
	}
	if strings.Contains(bar, "$") {
		t.Fatalf("an unpriced rail states no spend, got %q", bar)
	}
}

// The cockpit's spend is the ledger's billed total. Its live token counters
// include an estimate while a turn is still streaming, but pricing those
// counters again at the fresh input rate charges prompt-cache reads twice.
func TestStatusBar_CockpitSpendUsesTheBilledSessionTotal(t *testing.T) {
	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {
			InputCostPerToken:     0.00001,
			CacheReadCostPerToken: 0.000001,
			OutputCostPerToken:    0.00002,
		},
	})
	ledger := meter.New(table)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(table, "gpt-4o").
		WithLedger(ledger)
	usage := provider.Usage{PromptTokens: 1_000_000, CachedTokens: 900_000, CompletionTokens: 1_000}
	ledger.Record(meter.Origin{Source: meter.SourceAgent}, "gpt-4o", usage)
	m.accumulateUsage(&usage)

	cockpit := m.cockpitData(true)
	if want := m.totalsLabel(ledger.Total()); cockpit.Spend != want {
		t.Fatalf("cockpit spend = %q, want billed session total %q", cockpit.Spend, want)
	}
	if fresh := m.freshRateLabel(m.TotalTokensIn, m.TotalTokensOut); cockpit.Spend == fresh {
		t.Fatalf("cockpit spend must not re-price cached input at the fresh rate: %q", fresh)
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
// one blank line between things — a card, a notice, a block — never zero and
// never two, with a card's padding rows inside its band rather than spent as
// the blank between it and its neighbour.
func TestTranscriptSpacing_UniformRhythm(t *testing.T) {
	// The band is what tells a padding row from a blank one, so the rhythm
	// is measured where the terminal has a band to draw.
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
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

	// A blank line is empty; a padding row is the band's width of spaces.
	blank := func(i int) bool { return lines[i] == "" }
	pad := func(i int) bool { return lines[i] != "" && strings.TrimSpace(lines[i]) == "" }
	for i := range lines {
		if blank(i) && i > 0 && blank(i-1) {
			t.Fatalf("two blank lines in a row at %d:\n%s", i, strings.Join(lines, "\n"))
		}
	}

	// Every card opens and closes on a padding row, and stands one blank from
	// what is either side of it.
	cards := 0
	for i, line := range lines {
		if !strings.Contains(line, "◇ spawn") && !strings.Contains(line, "◇ agent") {
			continue
		}
		cards++
		if i < 2 || !pad(i-1) || !blank(i-2) {
			t.Errorf("a card should open on a padding row one blank below what is above it:\n%s", strings.Join(lines, "\n"))
		}
		if i+2 >= len(lines) || !pad(i+1) || !blank(i+2) {
			t.Errorf("a card should close on a padding row one blank above what follows:\n%s", strings.Join(lines, "\n"))
		}
	}
	if cards != 2 {
		t.Fatalf("the two calls should be two cards, found %d:\n%s", cards, strings.Join(lines, "\n"))
	}

	// Blocks keep their air: a blank line above the sent message is
	// impossible (it leads the transcript), but every later block opens with
	// one above it.
	for _, header := range []string{"Approved agent-4", "Multi-line notice:", "Done."} {
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
	if !strings.Contains(m.uiCommand([]string{"/ui"}), "terminal: not asked") {
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
		"terminal: ghostty 1.2.0",
		"inline images: kitty graphics",
		"desktop notifications: OSC 99",
		"focus events: reported",
		// 720/80 and 570/30 — the terminal's pixels over the session's own
		// columns and rows, which is the only place the two meet.
		"cell size: 9×19 px",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(m.uiCommand([]string{"/ui"}), "terminal: ghostty 1.2.0") {
		t.Error("the bare /ui summary should name the terminal it asked")
	}
}

func TestUITerminal_HeldQuestionsSayWhy(t *testing.T) {
	m := activityModel(t)
	m.caps.Asked = true
	m.caps.Held = "the reply would have to come back over ssh"
	m.caps.FocusEvents = true

	got := m.uiCommand([]string{"/ui", "terminal"})
	if !strings.Contains(got, "inline images: not asked") {
		t.Errorf("a held question is not a no, got %q", got)
	}
	if !strings.Contains(got, "over ssh") {
		t.Errorf("the readout has to name the reason it held back, got %q", got)
	}
	// The safe questions went out either way, so their answers stand.
	if !strings.Contains(got, "focus events: reported") {
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

// A fetch that is sitting out a host's refusal says so on its row, with the
// seconds left and the host that asked for them. A wait nobody can see is
// indistinguishable from a session that has hung.
func TestActivityRow_AWaitedFetchCountsDownOnItsRow(t *testing.T) {
	m := activityModel(t)
	pending := entry{kind: entryTool, toolName: web.FetchToolName,
		toolArgs: `{"url":"https://docs.rs/tokio/latest/tokio/"}`, toolResult: pendingToolResult}

	// Without a fetcher to ask, the row reads as the running call it is.
	view := stripANSI(m.renderEntry(pending, 80))
	if !strings.Contains(view, "running") || strings.Contains(view, "waiting") {
		t.Fatalf("a fetch nobody is waiting on should read as running:\n%s", view)
	}

	m = m.WithFetchWaits(func(host string) (time.Duration, bool) {
		if host != "docs.rs" {
			return 0, false
		}
		return 7500 * time.Millisecond, true
	}, func() {})
	view = stripANSI(m.renderEntry(pending, 80))
	if !strings.Contains(view, "waiting 8s · docs.rs asked") {
		t.Fatalf("the row does not say what it is waiting for:\n%s", view)
	}
	if strings.Contains(view, "running") {
		t.Fatalf("a waiting row still claims to be running:\n%s", view)
	}

	// Another host's fetch is not waiting for this one.
	other := entry{kind: entryTool, toolName: web.FetchToolName,
		toolArgs: `{"url":"https://pkg.go.dev/net/http"}`, toolResult: pendingToolResult}
	if view := stripANSI(m.renderEntry(other, 80)); strings.Contains(view, "waiting") {
		t.Fatalf("a fetch to another host was drawn as waiting:\n%s", view)
	}
}

// The session's own fetch has no transcript row until it finishes, so its
// wait is drawn as the live row under the transcript.
func TestFetchWaitRow_TheSessionsOwnFetchShowsItsWait(t *testing.T) {
	m := activityModel(t)
	m = m.WithFetchWaits(func(string) (time.Duration, bool) { return 3 * time.Second, true }, func() {})
	if _, ok := m.fetchWaitRow(80); ok {
		t.Fatal("a session with no call in flight drew a waiting row")
	}
	m.pendingApproval = &approvalRequest{call: provider.ToolCall{
		Name: web.FetchToolName, Arguments: `{"url":"https://docs.rs/tokio/latest/tokio/"}`}}
	row, ok := m.fetchWaitRow(80)
	if !ok {
		t.Fatal("the fetch in flight drew no waiting row")
	}
	if !strings.Contains(stripANSI(row), "waiting 3s · docs.rs asked") {
		t.Fatalf("the live row does not say what it is waiting for:\n%s", row)
	}
}

// Cancelling the turn gives up the wait: a person who stopped the turn is
// not asking to sit out the rest of a rate limit for a page nobody will read.
func TestCancel_TheTurnsCancelAbandonsAFetchWait(t *testing.T) {
	m := activityModel(t)
	var abandoned int
	m = m.WithFetchWaits(func(string) (time.Duration, bool) { return 0, false }, func() { abandoned++ })
	m.cancelStreaming()
	if abandoned != 1 {
		t.Fatalf("the cancel abandoned %d waits, want one", abandoned)
	}
}

// A steer spends no money and creates no worktree — it is one message onto
// the path the child's own lane already writes to — so it runs where reading
// the roster runs, on the auto-run path, and never behind a card. A redirect
// that has to wait for an approval arrives after the rounds it was meant to
// save. Spawning stays gated beside it, which is the contrast that makes this
// a decision rather than an omission.
func TestSteerToolNeedsNoApproval(t *testing.T) {
	m := gatedModel(t, nil, map[string]GatedPreviewFunc{
		subagent.SpawnToolName: func(json.RawMessage) (GatedPreview, error) {
			return GatedPreview{}, nil
		},
	})
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto, agent.ModePlan} {
		m.policy.mode = mode
		steer := provider.ToolCall{Name: subagent.SteerToolName,
			Arguments: `{"name":"writer-1","message":"read the exporter instead"}`}
		if m.requiresApproval(steer) {
			t.Errorf("steering an agent must never be approval-gated, and is in %s", mode)
		}
		if !m.requiresApproval(provider.ToolCall{Name: subagent.SpawnToolName}) {
			t.Errorf("spawning an agent stays gated, and is not in %s", mode)
		}
	}
}

// The row says who was redirected and what they were told, under the verb the
// lane's own note uses. The name alone would make every steer of a fan-out
// look alike; the message is bounded to its first line, marked when there was
// more, because the row is one line and the instruction need not be.
func TestSteerRowNamesTheAgentAndWhatItWasTold(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = updated.(Model)

	row := stripANSI(m.renderEntry(entry{kind: entryTool, toolName: subagent.SteerToolName,
		toolArgs:   `{"name":"writer-1","message":"read the exporter instead\nnot the importer"}`,
		toolResult: "Steered writer-1."}, 110))
	for _, want := range []string{"steer", "writer-1", "read the exporter instead", "…"} {
		if !strings.Contains(row, want) {
			t.Fatalf("a steer row should contain %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "not the importer") {
		t.Fatalf("the row is bounded to the message's first line:\n%s", row)
	}
}

// A rule's no is a different word from your own, and the rule that said it —
// with what the judgement cost where it cost anything — is the account beside
// it, in the field an allowed call already states its rule in
// (docs/interface/principles.md#two-denials-are-not-one-denial). Nothing ran
// either way, so the duration field says so.
func TestActivityRow_ARulesNoIsBlockedAndNamesTheRule(t *testing.T) {
	m := activityModel(t)
	call := entry{kind: entryTool, toolName: "execute_command",
		toolArgs: `{"command":"rm -rf ./dist"}`, deniedBy: decidedByAuto,
		denyRule: classifierRule, duration: 2100 * time.Millisecond}
	row := m.activityRowFor(call)
	if row.Outcome != components.OutcomeBlocked {
		t.Fatalf("a rule's no says %q, got %q", components.OutcomeBlocked, row.Outcome)
	}
	if !row.ByRule {
		t.Fatalf("the row should know a rule refused it: %+v", row)
	}
	if want := "classifier 2.1s"; row.Allowed != want {
		t.Fatalf("the judgement's cost is the account, want %q, got %q", want, row.Allowed)
	}
	if row.Duration != components.NoDuration {
		t.Fatalf("nothing ran, so the duration is %q, got %q", components.NoDuration, row.Duration)
	}

	yours := m.activityRowFor(entry{kind: entryTool, toolName: "execute_command",
		toolArgs: `{"command":"rm -rf ./dist"}`, deniedBy: decidedByYou})
	if want := components.OutcomeBy(components.OutcomeDenied, decidedByYou); yours.Outcome != want {
		t.Fatalf("your own no stays %q, got %q", want, yours.Outcome)
	}
	if yours.ByRule {
		t.Fatalf("your refusal is a preference, not a rule: %+v", yours)
	}
}

// A search's target is its pattern and then where it was put, and the row
// carries the place separately so the column can draw it behind the subject
// (docs/interface/principles.md#one-grid).
func TestActivityRow_SearchCarriesItsScopeApart(t *testing.T) {
	m := activityModel(t)
	row := m.activityRowFor(entry{kind: entryTool, toolName: "search",
		toolArgs: `{"pattern":"ErrRoundLimit","path":"internal/agent"}`, toolResult: "a.go:1"})
	if want := "./internal/agent"; row.Scope != want {
		t.Fatalf("the row should carry the place apart, want %q, got %q", want, row.Scope)
	}
	if !strings.HasSuffix(row.Target, " "+row.Scope) {
		t.Fatalf("and the target should end in it: %q", row.Target)
	}
	// A read's subject is its path, so there is no place behind it.
	read := m.activityRowFor(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/loop.go"}`, toolResult: "package agent"})
	if read.Scope != "" {
		t.Fatalf("a read's target is its subject whole, got scope %q", read.Scope)
	}
	// A search that named no directory has the whole tree for a scope, which
	// the target leaves off — so the row carries none either, and a pattern
	// that ends the way a scope does is still the subject whole.
	whole := m.activityRowFor(entry{kind: entryTool, toolName: "search",
		toolArgs: `{"pattern":"func loop() ."}`, toolResult: "a.go:1"})
	if whole.Scope != "" {
		t.Fatalf("a search of the whole tree marks no place, got scope %q", whole.Scope)
	}
}

// A command that never exited has no exit status: Go hands back -1 for a
// process a signal ended, and a row that printed that number would report
// `exit -1` as though some program had returned it. The three things that end
// one get three words, and the reader's own cancel is the quiet one — it is
// their decision and not a break.
func TestCommandRow_ANegativeExitCodeIsNeverPrintedAsAnExitStatus(t *testing.T) {
	m := activityModel(t)
	cases := []struct {
		name string
		code int
		end  commandEnd
		want []string
	}{
		{"the reader stopped it", -2,
			commandEnd{outcome: components.OutcomeStopped},
			[]string{"⊘", "stopped"}},
		{"a signal from off the machine", -9,
			commandEnd{outcome: components.OutcomeKilled, account: components.SignalAccount(9)},
			[]string{"✗", "killed · signal 9"}},
		{"the ceiling shhh set", -9,
			commandEnd{outcome: components.OutcomeTimedOut, account: "30s"},
			[]string{"✗", "timed out · 30s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := stripANSI(m.renderEntry(entry{kind: entryCommand,
				text:     "for i in 1 2 3 4; do echo round $i; sleep 1; done",
				exitCode: tc.code, end: tc.end, duration: 3700 * time.Millisecond}, 110))
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Fatalf("a command that never exited should read %q:\n%s", want, view)
				}
			}
			if strings.Contains(view, "exit -") {
				t.Fatalf("no row prints a negative exit status:\n%s", view)
			}
			if !strings.Contains(view, "3.7s") {
				t.Fatalf("a command that ran keeps how long it ran for:\n%s", view)
			}
		})
	}
}

// Which of the three ended it comes off the context the command ran on,
// because the signal cannot say who asked for it: the ceiling and the
// reader's chord end a command the same way.
func TestCommandRow_DidNotStartIsNotRenderedAsKilled(t *testing.T) {
	m := activityModel(t)
	view := stripANSI(m.renderEntry(entry{kind: entryCommand, text: "go test ./...",
		toolResult: "fork/exec sandbox-exec: no such file", exitCode: -1,
		commandResult: tools.ExecResult{ExitCode: -1, Outcome: tools.ExecDidNotStart}}, 110))
	if !strings.Contains(view, components.OutcomeDidNotStart) {
		t.Fatalf("pre-start failure must name its outcome:\n%s", view)
	}
	if strings.Contains(view, "killed") || strings.Contains(view, "exit -") {
		t.Fatalf("pre-start failure must not be rendered as a signal status:\n%s", view)
	}
}

func TestCommandEnding_NamesWhatEndedTheCommand(t *testing.T) {
	cases := []struct {
		name    string
		ctxErr  error
		code    int
		limit   time.Duration
		want    string
		account string
	}{
		{name: "a command that exited on its own says nothing", code: 1},
		{name: "the reader's cancel", ctxErr: context.Canceled, code: -2,
			want: components.OutcomeStopped},
		{name: "the ceiling", ctxErr: context.DeadlineExceeded, code: -9, limit: 30 * time.Second,
			want: components.OutcomeTimedOut, account: "30s"},
		{name: "a signal nobody here sent", code: -9,
			want: components.OutcomeKilled, account: "signal 9"},
		{name: "an ending with no signal to name", code: -1,
			want: components.OutcomeKilled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commandEnding(tc.ctxErr, tc.code, tc.limit)
			if got.outcome != tc.want || got.account != tc.account {
				t.Errorf("commandEnding = %q/%q, want %q/%q",
					got.outcome, got.account, tc.want, tc.account)
			}
		})
	}
}

// The target is the field a reader scans for what an act was about, so the
// one thing it may never be is the tool's own identifier: the verb column
// already carries that, and a row that repeats it there has said one thing
// twice and what the call touched not at all
// (docs/interface/principles.md#one-grid).
//
// The walk is over the receipt's verb table because that is the row's own
// register of every tool it can draw — a tool missing from it renders as itself and is a
// hole in the table — so a tool registered tomorrow is covered by this the
// day its verb is added. Both refusals a call can meet before it runs are put
// through it: the ordinary row, and the row the queue leaves behind when the
// arguments cannot be honoured at all.
func TestActivityRow_NoTargetIsTheToolsOwnName(t *testing.T) {
	m := activityModel(t)
	names := receipt.Names()
	// A server's tool goes through a branch of its own and is the one name
	// the table cannot hold, since the server half is not known until it
	// connects.
	names = append(names, "github"+mcp.Separator+"create_issue")
	for _, name := range names {
		for _, args := range []string{"", "{}", "not json", `{"path":`} {
			rows := []components.ActivityRow{
				m.activityRowFor(entry{kind: entryTool, toolName: name, toolArgs: args,
					toolResult: "error: invalid arguments"}),
				m.activityRowFor(m.skippedCallEntry(
					provider.ToolCall{Name: name, Arguments: args},
					errors.New("invalid arguments: the call could not be read"))),
			}
			for _, row := range rows {
				if row.Target == name {
					t.Errorf("%s called with %q renders its own name as the subject: %q",
						name, args, row.Target)
				}
			}
		}
	}
}

// The three tools whose arguments lead with an operation rather than a
// subject. The operation is the verb column's job, so a row that put it in
// the target as well read `run  run` and `read  read` — one word twice, and
// the suite, the entry or the process never named.
func TestActivityRow_AnActionIsNotASubject(t *testing.T) {
	m := activityModel(t)
	for _, tc := range []struct{ name, tool, args, want string }{
		{"the gate names its suite", quality.ToolName, `{"action":"run","suite":"default"}`,
			"quality gate · default"},
		{"a re-report names the run it re-reports", quality.ToolName, `{"action":"result"}`,
			"quality gate · last result"},
		{"an evidence read names the entry", evidence.ToolName,
			`{"action":"read","id":"ev-1a2b3c4d5e6f7089"}`, "ev-1a2b3c4d5e6f7089"},
		{"a process read names the process", process.ToolName,
			`{"action":"read","name":"web"}`, "web"},
		{"a status of everything says so", process.ToolName, `{"action":"status"}`,
			"all processes"},
	} {
		row := m.activityRowFor(entry{kind: entryTool, toolName: tc.tool, toolArgs: tc.args,
			toolResult: "ok"})
		if row.Target != tc.want {
			t.Errorf("%s: target = %q, want %q", tc.name, row.Target, tc.want)
		}
		if row.Target == row.Verb {
			t.Errorf("%s: the target repeats the verb column (%q)", tc.name, row.Verb)
		}
	}
}

// The gate row is the one row whose subject is finished by its own result:
// the call says which suite was asked for and the verdict says what that
// suite turned out to be, so the finished row states how much was verified
// without the reader opening anything.
func TestActivityRow_TheGateRowCountsItsChecks(t *testing.T) {
	m := activityModel(t)
	res := &quality.Result{Suite: "default", Verdict: quality.VerdictPass, Checks: []quality.CheckResult{
		{Name: "test"}, {Name: "vet"}, {Name: "lint"}, {Name: "fmt-check"}, {Name: "docs-check"},
	}}
	row := m.activityRowFor(entry{kind: entryTool, toolName: quality.ToolName,
		toolArgs: `{"action":"run","suite":"default"}`, toolResult: res.Format(res.Fingerprint)})
	if want := "quality gate · default · 5 checks"; row.Target != want {
		t.Errorf("finished gate row: target = %q, want %q", row.Target, want)
	}
	// A run still going has no verdict to read, and says what it can: which
	// suite it was asked for.
	running := m.activityRowFor(entry{kind: entryTool, toolName: quality.ToolName,
		toolArgs: `{"action":"run","suite":"default"}`, toolResult: pendingToolResult})
	if want := "quality gate · default"; running.Target != want {
		t.Errorf("running gate row: target = %q, want %q", running.Target, want)
	}
	// A run that named no suite fell back to the configured default, and the
	// verdict is the only thing that knows which suite that was.
	fell := &quality.Result{Suite: "fast", Verdict: quality.VerdictFail,
		Checks: []quality.CheckResult{{Name: "test", ExitCode: 1}}}
	row = m.activityRowFor(entry{kind: entryTool, toolName: quality.ToolName,
		toolArgs: `{"action":"run"}`, toolResult: fell.Format(fell.Fingerprint)})
	if want := "quality gate · fast · 1 check"; row.Target != want {
		t.Errorf("gate row that named no suite: target = %q, want %q", row.Target, want)
	}
}

// legacyRunner lets a test state a runner as the output and status it
// prints, which is all most of them care about: the session's runner seam
// takes the typed result, and a status of -1 reads as a command that never
// started, as it did before the seam carried the category.
func legacyRunner(run func(context.Context, string) (string, int)) RunFunc {
	return func(ctx context.Context, command string) tools.ExecResult {
		return tools.InferExecResult(run(ctx, command))
	}
}

// legacyTailRunner is legacyRunner for the tailed form.
func legacyTailRunner(run func(context.Context, string, func(string)) (string, int)) TailFunc {
	return func(ctx context.Context, command string, onLine func(string)) tools.ExecResult {
		return tools.InferExecResult(run(ctx, command, onLine))
	}
}
