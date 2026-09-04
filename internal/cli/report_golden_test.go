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
	"github.com/rfizzle/shhh/internal/config"
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
		{"observe.compare", observeCompareReport(goldenObserveCompare()).Render(80)},
		{"observe.compare.small", observeCompareReport(goldenObserveCompareSmall()).Render(80)},
		{"observe.compare.empty", observeCompareReport(goldenObserveCompareEmpty()).Render(80)},
		{"observe.session", observeSessionReport(goldenObserveSession()).Render(80)},
		{"observe.session.empty", observeSessionReport(goldenObserveSessionRow(), nil).Render(80)},
		{"rate", rateReport(rateWalk(), rateScopeOf(false, false), goldenNow).Render(80)},
		{"rate.empty", rateReport(nil, rateScopeOf(false, false), goldenNow).Render(80)},
		{"sandbox.empty", goldenEmptySandbox().Render(80)},
		{"config.list", goldenConfigList().Render(80)},
		{"config.list.w60", goldenConfigList().Render(60)},
		{"config.get", goldenConfigGet().Render(80)},
		{"config.init", goldenConfigInit().Render(80)},
		{"config.scaffold", goldenScaffoldOpening()},
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
		{"rate", 80, rateReport(rateWalk(), rateScopeOf(false, false), goldenNow).Render(80)},
		{"config.list", 80, goldenConfigList().Render(80)},
		{"config.list.w60", 60, goldenConfigList().Render(60)},
		{"observe.compare", 80, observeCompareReport(goldenObserveCompare()).Render(80)},
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
		{Name: "otel", Subject: "http://localhost:4318", Detail: "content-free", Outcome: "ok"},
		{Name: "sandbox", Subject: "bwrap not found", Outcome: "UNCONTAINED",
			State:       components.DoctorFailed,
			Consequence: "commands run with your own permissions, in your own filesystem",
			Fix:         []string{"sudo apt install bubblewrap"}},
		{Name: "engine", Subject: "no container engine", Outcome: "not checked",
			State: components.DoctorSkipped},
		// The wordings row, built from the reading itself rather than typed
		// out: which of the three directories won is what the row is opened
		// with, so the fixture has to be able to go wrong the way the row
		// can.
		doctorCheck("prompts", doctorPrompts([]wordingRow{
			{key: "steer", from: ".shhh/prompts/steer.md"},
			{key: "summary", from: "config prompts.summary"},
			{key: "todo_commit", from: "~/.config/shhh/prompts/todo_commit.md"},
		}, builtinBacklogProfile()), 0),
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
		// A suite that mostly passes, and one whose runs never got a
		// verdict at all — blocked is named beside the failures and kept
		// out of the pass rate.
		Gates: []storage.AgentGateVerdict{
			{Suite: "default", Verdict: "pass", Count: 6},
			{Suite: "default", Verdict: "fail", Count: 2},
			{Suite: "lint", Verdict: "blocked", Count: 1},
		},
		Outcomes: []storage.AgentSessionOutcome{
			{Outcome: "completed", Count: 5},
			{Outcome: "abandoned", Count: 2},
			// A session killed before its first turn closed: a category of
			// its own, never folded into an abandonment.
			{Outcome: "unknown", Count: 1},
			{Outcome: "error", Count: 1},
		},
	}
}

// goldenObserveSession is one recorded session carrying an event of every
// kind the timeline can draw — including one at the zero position, which is
// what a surface that keeps no turn or round accounting records.
func goldenObserveSession() (storage.AgentSessionSummary, []storage.AgentExportEvent) {
	fast, slow, turn := int64(42), int64(2400), int64(94000)
	// The page's fixture is a session somebody has answered for, so the
	// rating sits beside the outcome it is there to check. The bare row the
	// empty page is built from carries none, which is the other case.
	row := goldenObserveSessionRow()
	worked := true
	row.Rating = &worked
	return row, []storage.AgentExportEvent{
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
		// The gate's verdict: a signal that names a subject as well as a
		// qualifier, and the only one that takes no position.
		{CreatedAt: "2026-08-31T11:33:10.000Z", Kind: storage.AgentEventSignal,
			Tool: "default", Outcome: "gate", Reason: "pass"},
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
		Project: "3d81ee0a5c62", ChatSession: "2026-08-31 11:31:58", Outcome: "completed",
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

// goldenProject is a checkout carrying one setting of its own, so the
// fixture covers the source a repository's file supplies.
func goldenProject() config.Project {
	return config.Project{
		Path: "/repo/.shhh/config.toml", Display: ".shhh/config.toml",
		Keys: []string{"provider.default"},
	}
}

// goldenConfig is one table of the file answered four ways: a key the
// checkout set, a key the person's own file set, a key an environment
// variable outranks both on, and the rest standing at their defaults. Every
// source the listing can print is therefore in the fixture.
func goldenConfig() []configReading {
	var cfg config.Config
	cfg.Provider.Default = "anthropic"
	cfg.Provider.Model = "gpt-4o"
	var out []configReading
	for _, s := range configEntries(cfg) {
		if s.Group() != "provider" {
			continue
		}
		reading := configReadingOf(cfg, goldenProject(), s)
		if s.Key == "provider.reasoning" {
			reading.Value, reading.Source, reading.Set = "high", s.Env, true
		}
		out = append(out, reading)
	}
	return out
}

func goldenConfigList() report.Report {
	return configListReport(goldenConfig(), "provider",
		configListSubject("~/.config/shhh/config.toml", goldenProject()))
}

func goldenConfigGet() report.Report {
	for _, reading := range goldenConfig() {
		if reading.Key == "provider.model" {
			return configGetReport(reading)
		}
	}
	return report.Report{}
}

// goldenConfigInit is what the command answers with after it writes: the two
// things it made and what each of them holds.
func goldenConfigInit() report.Report {
	return initPlan{
		settings: "/home/dev/.config/shhh/config.toml",
		prompts:  "/home/dev/.config/shhh/prompts",
		files:    make([]initFile, len(wordingKeys())),
	}.wrote()
}

// goldenScaffoldOpening is the head of the file that command writes: what it
// says about itself and the first table, which is where a reader finds out
// what the shape of the rest is. The whole file is a hundred keys and the
// opening is what has to read well.
func goldenScaffoldOpening() string {
	text := config.Scaffold(config.Config{}, false)
	if at := strings.Index(text, "\n[behavior]"); at > 0 {
		return text[:at+1]
	}
	return text
}

// goldenObserveCompare is two cohorts either side of a prompt edit, both
// large enough to read: the later one takes stock more often and is broken
// into by its user less, finishes more of its sessions, and costs less for
// each one it finishes.
func goldenObserveCompare() observeCompareData {
	fast, mid, slow := 38.0, 260.0, 2100.0
	earlier := &observeCohortData{
		AgentCohort: storage.AgentCohort{
			Value: "9f2a1c04bb7e", Sessions: 11,
			TokensIn: 402000, TokensOut: 96000, Cost: 5.12,
			First: goldenNow.AddDate(0, 0, -21), Last: goldenNow.AddDate(0, 0, -10),
		},
		Reading: storage.AgentCohortReading{
			Turns: []storage.AgentTurnOutcome{
				{Outcome: "done", Count: 48, AvgRounds: 4.6, MaxRounds: 22, AvgDurationMs: &slow},
				{Outcome: "cap-paused", Count: 6, AvgRounds: 40, MaxRounds: 40},
				{Outcome: "failed", Count: 4, AvgRounds: 6.5, MaxRounds: 12},
			},
			Tools: []storage.AgentToolUsage{
				{Tool: "read_file", Count: 180, AvgDurationMs: &fast},
				{Tool: "execute_command", Count: 62, AvgDurationMs: &slow, ErrorRate: 0.145},
				{Tool: "edit_file", Count: 40, AvgDurationMs: &mid, ErrorRate: 0.05},
			},
			ToolErrors: []storage.AgentToolErrorCount{
				{Tool: "execute_command", Class: "exit-status", Count: 7},
				{Tool: "execute_command", Class: "timeout", Count: 2},
				{Tool: "edit_file", Class: "bad-args", Count: 2},
			},
			Decisions: []storage.AgentDecisionCount{
				{Decision: "allow", Reason: "mode-accept-edits", Count: 96},
				{Decision: "ask", Reason: "safety", Count: 14},
				{Decision: "deny", Count: 5},
			},
			Signals: []storage.AgentSignalCount{
				{Signal: "summary", Reason: "on-target", Count: 40},
				{Signal: "intervened", Reason: "steer", Count: 12},
				{Signal: "steered", Count: 9},
				{Signal: "intervened", Reason: "check-in", Count: 7},
				{Signal: "repeat-notice", Reason: "execute_command", Count: 5},
			},
			Gates: []storage.AgentGateVerdict{
				{Suite: "default", Verdict: "pass", Count: 6},
				{Suite: "default", Verdict: "fail", Count: 3},
			},
			Outcomes: []storage.AgentSessionOutcome{
				{Outcome: "completed", Count: 7},
				{Outcome: "abandoned", Count: 3},
				{Outcome: "error", Count: 1},
			},
		},
	}
	later := &observeCohortData{
		AgentCohort: storage.AgentCohort{
			Value: "0b13ee42aa90", Sessions: 14,
			TokensIn: 470000, TokensOut: 104000, Cost: 5.60,
			First: goldenNow.AddDate(0, 0, -9), Last: goldenNow,
		},
		Reading: storage.AgentCohortReading{
			Turns: []storage.AgentTurnOutcome{
				{Outcome: "done", Count: 66, AvgRounds: 3.9, MaxRounds: 18, AvgDurationMs: &slow},
				{Outcome: "cap-paused", Count: 3, AvgRounds: 40, MaxRounds: 40},
				{Outcome: "failed", Count: 3, AvgRounds: 5.8, MaxRounds: 11},
			},
			Tools: []storage.AgentToolUsage{
				{Tool: "read_file", Count: 210, AvgDurationMs: &fast},
				{Tool: "execute_command", Count: 60, AvgDurationMs: &slow, ErrorRate: 0.1},
				{Tool: "edit_file", Count: 52, AvgDurationMs: &mid, ErrorRate: 0.038},
			},
			ToolErrors: []storage.AgentToolErrorCount{
				{Tool: "execute_command", Class: "exit-status", Count: 5},
				{Tool: "edit_file", Class: "bad-args", Count: 2},
				{Tool: "execute_command", Class: "timeout", Count: 1},
			},
			Decisions: []storage.AgentDecisionCount{
				{Decision: "allow", Reason: "mode-accept-edits", Count: 120},
				{Decision: "ask", Reason: "safety", Count: 10},
				{Decision: "deny", Count: 3},
			},
			Signals: []storage.AgentSignalCount{
				{Signal: "summary", Reason: "on-target", Count: 52},
				{Signal: "intervened", Reason: "steer", Count: 18},
				{Signal: "intervened", Reason: "check-in", Count: 9},
				{Signal: "steered", Count: 5},
				{Signal: "repeat-notice", Reason: "execute_command", Count: 2},
				// A signal the earlier cohort never raised: drawn, and with
				// no ratio, because there is nothing to divide by.
				{Signal: "tree-moved", Reason: "head", Count: 1},
			},
			Gates: []storage.AgentGateVerdict{
				{Suite: "default", Verdict: "pass", Count: 10},
				{Suite: "default", Verdict: "fail", Count: 2},
			},
			Outcomes: []storage.AgentSessionOutcome{
				{Outcome: "completed", Count: 11},
				{Outcome: "abandoned", Count: 2},
				{Outcome: "unknown", Count: 1},
			},
		},
	}
	return observeCompared(observeCompareData{
		Window: "30d", Split: "prompt_hash", Sessions: 27,
		Earlier: earlier, Later: later,
		Others:      []string{"7cd0a1b2ff31"},
		MinSessions: compareMinSessions,
	})
}

// goldenObserveCompareSmall is the same window with one cohort too small to
// read: it prints both counts and no rate at all.
func goldenObserveCompareSmall() observeCompareData {
	data := goldenObserveCompare()
	small := *data.Earlier
	small.Sessions = 6
	data.Earlier, data.Others = &small, nil
	data.Sessions = 20
	return observeCompared(observeCompareData{
		Window: data.Window, Split: data.Split, Sessions: data.Sessions,
		Earlier: data.Earlier, Later: data.Later, MinSessions: compareMinSessions,
	})
}

// goldenObserveCompareEmpty is a window whose sessions all ran under one
// value: an empty state, and not every rate at a hundred percent.
func goldenObserveCompareEmpty() observeCompareData {
	return observeCompared(observeCompareData{
		Window: "30d", Split: "prompt_hash", Sessions: 11, MinSessions: compareMinSessions,
	})
}
