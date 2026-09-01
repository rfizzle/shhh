package cli

// The checked-in text of every listing the CLI prints. The unit tests around
// these assert substrings, which are blind to exactly what a report is about:
// a column that grew, a section that moved above another, a blank line that
// went missing between two blocks. A golden is the whole render, so any of
// those shows up as a diff (docs/interface/principles.md#one-grid).
//
// Reports are plain bytes by construction — colour is added on the way to a
// terminal and never reaches these — so a fixture is the text and nothing
// else, without the ANSI block the TUI goldens carry.

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// reportGoldenDir is where the fixtures live, relative to this package — `go
// test` runs a test binary in its own package directory.
const reportGoldenDir = "testdata/report"

var updateReportGolden = flag.Bool("update-golden", false,
	"rewrite the checked-in report fixtures from the current output")

// assertReportGolden compares one render against its fixture, or rewrites it
// when the run is updating. A missing fixture is a failure naming the flag
// rather than a silently created file: a new listing should be reviewed the
// first time it lands, not the second.
func assertReportGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(reportGoldenDir, name+".txt")
	body := got + "\n"
	if *updateReportGolden || os.Getenv("SHHH_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(reportGoldenDir, 0o755); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s is missing (%v)\nrun: go test ./internal/cli -update-golden", path, err)
	}
	if string(want) != body {
		t.Errorf("%s renders differently than %s:\n--- want ---\n%s\n--- got ---\n%s\n"+
			"if the change is intended: go test ./internal/cli -update-golden", name, path, want, body)
	}
	if strings.Contains(body, "\x1b") {
		t.Errorf("%s carries an escape code; a report is plain bytes", name)
	}
}

// goldenNow is a fixed clock, so a fixture holding "2h ago" says the same
// thing tomorrow.
var goldenNow = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

func TestReportGoldens(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"doctor", doctorReportOf("shhh doctor", "check", "checks", goldenChecks()).Render(80)},
		{"history", historyReport(goldenHistory(), "", goldenNow).Render(80)},
		{"history.w60", historyReport(goldenHistory(), "", goldenNow).Render(60)},
		{"history.empty", historyReport(nil, "", goldenNow).Render(80)},
		{"history.no-match", historyReport(nil, "ports", goldenNow).Render(80)},
		{"snippets", snippetsReport(goldenSnippets(), goldenNow).Render(80)},
		{"snippets.w60", snippetsReport(goldenSnippets(), goldenNow).Render(60)},
		{"snippets.empty", snippetsReport(nil, goldenNow).Render(80)},
		{"metrics", metricsReport(goldenMetrics()).Render(80)},
		{"metrics.empty", metricsReport(metricsData{Window: "last 7 days"}).Render(80)},
		{"memory", goldenMemoryReport().Render(80)},
		{"memory.empty", memoryReport(memory.NewStore(nil, "/repo"), nil, memoryWayOut, goldenNow).Render(80)},
		{"observe", observeReport(goldenObserve()).Render(80)},
		{"observe.w110", observeReport(goldenObserve()).Render(110)},
		{"observe.empty", observeReport(observeData{Window: "30d"}).Render(80)},
		{"observe.session", observeSessionReport(goldenObserveSession()).Render(80)},
		{"observe.session.empty", observeSessionReport(goldenObserveSessionRow(), nil).Render(80)},
		{"rate", rateReport(rateEntries(), goldenNow).Render(80)},
		{"rate.empty", rateReport(nil, goldenNow).Render(80)},
		{"sandbox.empty", goldenEmptySandbox().Render(80)},
	} {
		t.Run(c.name, func(t *testing.T) { assertReportGolden(t, c.name, c.body) })
	}
}

// No line of any fixture is wider than the width it was drawn at: a listing
// that soft-wraps in the terminal is the thing this whole shape replaced.
func TestReportGoldens_FitTheirWidth(t *testing.T) {
	for _, c := range []struct {
		name  string
		width int
		body  string
	}{
		{"metrics", 80, metricsReport(goldenMetrics()).Render(80)},
		{"history", 80, historyReport(goldenHistory(), "", goldenNow).Render(80)},
		{"history.w60", 60, historyReport(goldenHistory(), "", goldenNow).Render(60)},
		{"rate", 80, rateReport(rateEntries(), goldenNow).Render(80)},
	} {
		for _, line := range strings.Split(c.body, "\n") {
			if len([]rune(line)) > c.width {
				t.Errorf("%s: a line is %d columns wide at w%d: %q", c.name, len([]rune(line)), c.width, line)
			}
		}
	}
}

func goldenChecks() []components.DoctorCheck {
	return []components.DoctorCheck{
		{Name: "binary", Subject: "shhh 0.9.4", Detail: "linux/amd64", Outcome: "ok"},
		{Name: "sandbox", Subject: "bwrap not found", Outcome: "UNCONTAINED",
			State:       components.DoctorFailed,
			Consequence: "commands run with your own permissions, in your own filesystem",
			Fix:         []string{"sudo apt install bubblewrap"}},
		{Name: "engine", Subject: "no container engine", Outcome: "not checked",
			State: components.DoctorSkipped},
	}
}

func goldenHistory() []storage.HistoryEntry {
	exit1 := int64(1)
	return []storage.HistoryEntry{
		{ID: 1, CreatedAt: goldenNow.Add(-2 * time.Hour), Provider: "openai", Model: "gpt-5.2",
			Prompt: "delete every log file older than a week", Command: "find . -mtime +7 -delete",
			Action: "run", ExitCode: new(int64), Success: true},
		{ID: 2, CreatedAt: goldenNow.Add(-26 * time.Hour), Provider: "openai", Model: "gpt-5.2",
			Prompt:  "rebuild the index and restart the service",
			Command: "make index && systemctl restart indexer",
			Action:  "run", ExitCode: &exit1, Success: true},
		{ID: 3, CreatedAt: goldenNow.Add(-4 * time.Minute), Provider: "anthropic", Model: "claude-sonnet-5",
			Prompt: "show the ten biggest files", Command: "du -ah . | sort -rh | head -10",
			Action: "copy", Success: true},
	}
}

func goldenSnippets() []storage.Snippet {
	return []storage.Snippet{
		{ID: 1, Name: "ports", Description: "everything listening", Command: "ss -tulpn",
			UpdatedAt: goldenNow.Add(-3 * time.Hour)},
		{ID: 2, Name: "biggest", Description: "", Command: "du -ah . | sort -rh | head -10",
			UpdatedAt: goldenNow.Add(-50 * time.Hour)},
	}
}

func goldenMetrics() metricsData {
	ttft, p95 := 420.0, 1180.0
	in, out := int64(41200), int64(9800)
	rate := 0.88
	return metricsData{
		Window: "last 7 days",
		Summary: []storage.ProviderMetrics{
			{Provider: "openai", Model: "gpt-5.2", Count: 34, SuccessRate: 0.94,
				AvgTTFT: &ttft, P95TTFT: &p95, TotalTokensIn: &in, TotalTokensOut: &out,
				ExecCount: 12, ExecSuccessRate: &rate},
			// The second pair was never timed, so its latency lines are left
			// out rather than dashed.
			{Provider: "anthropic", Model: "claude-sonnet-5", Count: 3, SuccessRate: 1},
		},
		Trend: []storage.MetricsDayTokens{
			{Provider: "openai", Model: "gpt-5.2", Day: "2026-08-30", TokensIn: 20000, TokensOut: 5000},
			{Provider: "openai", Model: "gpt-5.2", Day: "2026-08-31", TokensIn: 21200, TokensOut: 4800},
		},
		Actions: []storage.MetricsActionUsage{
			{Provider: "openai", Model: "gpt-5.2", Action: "run", Success: true, Count: 22},
			{Provider: "openai", Model: "gpt-5.2", Action: "copy", Success: true, Count: 9},
		},
		Now: goldenNow,
	}
}

func goldenMemoryReport() report.Report {
	entries := []memory.Entry{
		{ID: 1, Scope: "/repo", Kind: "preference", Provenance: "user",
			Text: "prefers short answers", UpdatedAt: goldenNow.Add(-50 * time.Hour)},
		{ID: 2, Scope: memory.GlobalScope, Kind: "convention", Provenance: "agent",
			Text: "commit subjects are imperative", UpdatedAt: goldenNow.Add(-3 * time.Hour)},
	}
	return memoryReport(memory.NewStore(nil, "/repo"), entries, memoryWayOut, goldenNow)
}

// goldenObserve is a dashboard with something in every section, including a
// live session and a tool that fails part of the time.
func goldenObserve() observeData {
	ended := goldenNow.Add(-14 * time.Minute)
	started := ended.Add(-14 * time.Minute)
	fast, slow := 42.0, 2400.0
	return observeData{
		Window: "30d",
		Sessions: []storage.AgentSessionSummary{
			{ID: 12, Kind: "code", Model: "claude-sonnet-5", StartedAt: started, EndedAt: &ended,
				Turns: 6, TokensIn: 41200, TokensOut: 9800, Cost: 0.51},
			{ID: 13, Kind: "chat", Model: "gpt-5.2", StartedAt: goldenNow.Add(-time.Minute),
				Turns: 1, TokensIn: 900, TokensOut: 120},
		},
		ByDay: []storage.AgentDayUsage{
			{Day: "2026-08-31", Sessions: 2, TokensIn: 42100, TokensOut: 9920, Cost: 0.51},
		},
		ByModel: []storage.AgentModelUsage{
			{Provider: "anthropic", Model: "claude-sonnet-5", Sessions: 1,
				TokensIn: 41200, TokensOut: 9800, Cost: 0.51},
			// An unpriced model reports its tokens and no money at all,
			// rather than $0.0000.
			{Provider: "openai", Model: "gpt-5.2", Sessions: 1, TokensIn: 900, TokensOut: 120},
		},
		ToolMix: []storage.AgentToolUsage{
			{Tool: "read_file", Count: 31, AvgDurationMs: &fast},
			{Tool: "execute_command", Count: 8, AvgDurationMs: &slow, ErrorRate: 0.25},
		},
		ToolErrors: []storage.AgentToolErrorCount{
			{Tool: "execute_command", Class: "exit-status", Count: 2},
		},
		Decisions: []storage.AgentDecisionCount{
			{Decision: "allow", Reason: "mode-accept-edits", Count: 14},
			{Decision: "deny", Reason: "", Count: 2},
			{Decision: "deny", Reason: "plan-mode", Count: 1},
		},
		Turns: []storage.AgentTurnOutcome{
			{Outcome: "done", Count: 6, AvgRounds: 3.2, MaxRounds: 9, AvgDurationMs: &slow},
			{Outcome: "cap-paused", Count: 1, AvgRounds: 40, MaxRounds: 40},
		},
		Signals: []storage.AgentSignalCount{
			{Signal: "summary", Reason: "on-target", Count: 5},
			{Signal: "context-trimmed", Reason: "4", Count: 1},
		},
	}
}

// goldenObserveSession is one recorded session carrying an event of every
// kind the timeline can draw — including one at the zero position, which is
// what a surface that keeps no turn or round accounting records.
func goldenObserveSession() (storage.AgentSessionSummary, []storage.AgentExportEvent) {
	fast, slow, turn := int64(42), int64(2400), int64(94000)
	return goldenObserveSessionRow(), []storage.AgentExportEvent{
		// A decision taken before the first turn opened: the round and turn
		// are zero because nothing was running yet.
		{CreatedAt: "2026-08-31T11:31:58.000Z", Kind: storage.AgentEventDecision, Outcome: "allow", Reason: "mode-accept-edits"},
		{CreatedAt: "2026-08-31T11:32:04.000Z", Kind: storage.AgentEventTool, Turn: 1, Round: 1,
			Tool: "read_file", DurationMs: &fast, Outcome: "ok"},
		{CreatedAt: "2026-08-31T11:32:19.000Z", Kind: storage.AgentEventTool, Turn: 1, Round: 3,
			Tool: "execute_command", DurationMs: &slow, Outcome: "error", Reason: "exit-status"},
		{CreatedAt: "2026-08-31T11:32:21.000Z", Kind: storage.AgentEventDecision, Turn: 1, Round: 3,
			Outcome: "ask", Reason: "safety"},
		{CreatedAt: "2026-08-31T11:32:40.000Z", Kind: storage.AgentEventSignal, Turn: 1, Round: 10,
			Outcome: "summary", Reason: "off-target"},
		{CreatedAt: "2026-08-31T11:32:41.000Z", Kind: storage.AgentEventSignal, Turn: 1, Round: 10,
			Outcome: "intervened", Reason: "steer"},
		{CreatedAt: "2026-08-31T11:33:32.000Z", Kind: storage.AgentEventTurn, Turn: 1, Round: 14,
			Outcome: "done", DurationMs: &turn},
	}
}

// goldenObserveSessionRow is the session those events belong to, alone —
// which is also the page a session that recorded nothing renders.
func goldenObserveSessionRow() storage.AgentSessionSummary {
	ended := goldenNow.Add(-26 * time.Minute)
	return storage.AgentSessionSummary{
		ID: 12, Kind: "code", Provider: "anthropic", Model: "claude-sonnet-5",
		StartedAt: goldenNow.Add(-28 * time.Minute), EndedAt: &ended,
		Turns: 1, TokensIn: 41200, TokensOut: 9800, Cost: 0.51,
		Version: "v1.4.0", PromptHash: "9f2a1c04bb7e", Skills: 2,
		Project: "3d81ee0a5c62", ChatSession: "2026-08-31 11:31:58",
		Settings: &storage.AgentSettings{
			Mode: "accept-edits", Reasoning: "medium", MaxRounds: 150,
			SummaryModel: "claude-haiku-4-5", SummaryInterval: 10, SummaryEnabled: true,
			ClassifierModel: "claude-haiku-4-5", SandboxProfile: "workspace", ConfigHash: "c0ffee0ddba1",
		},
	}
}

// goldenEmptySandbox is the empty state of a listing that lives only behind a
// slash command, so the one shape is captured on both sides of the TUI line.
func goldenEmptySandbox() report.Report {
	return emptyInto(
		report.Report{Title: "/sandbox list", Subject: countOf(0, "container", "containers")},
		"no sandbox containers", "/sandbox doctor")
}

// A NO_COLOR run and a piped run are the same bytes, so a listing a script
// reads and a listing a person reads cannot drift
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func TestReport_NoColorIsByteIdenticalToAPipe(t *testing.T) {
	r := metricsReport(goldenMetrics())
	piped := r.Render(report.FallbackWidth)

	t.Setenv("NO_COLOR", "1")
	var buf strings.Builder
	if err := report.Fprint(&buf, r); err != nil {
		t.Fatal(err)
	}
	if buf.String() != piped+"\n" {
		t.Fatalf("NO_COLOR output differs from the piped render:\n%q\n%q", buf.String(), piped)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatal("the output carries an escape code")
	}
}
