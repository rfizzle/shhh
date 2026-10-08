package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// seedTimingSession opens a session and writes the rows it is given.
func seedTimingSession(t *testing.T, db *storage.DB, events ...storage.AgentEvent) int64 {
	t.Helper()
	id, err := db.StartAgentSession("chat", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	for _, e := range events {
		if err := db.RecordAgentEvent(id, e); err != nil {
			t.Fatalf("record event: %v", err)
		}
	}
	return id
}

func ms(v int64) *int64 { return &v }

func startupEvent(phase, name, outcome string, took int64) storage.AgentEvent {
	return storage.AgentEvent{Kind: storage.AgentEventStartup, Reason: phase, Tool: name, Outcome: outcome, DurationMs: ms(took)}
}

func TestObserveStartup_RanksServersAndNamesTheTimeoutKey(t *testing.T) {
	db := fixtureStore(t)
	for _, tooks := range [][2]int64{{300, 6000}, {500, 12000}, {400, 20000}} {
		outcome := observe.ServerConnected
		if tooks[1] == 20000 {
			outcome = observe.ServerTimeout
		}
		seedTimingSession(t, db,
			startupEvent(observe.PhaseConfig, "", "", 10),
			startupEvent(observe.PhaseStore, "", "", tooks[0]),
			startupEvent(observe.PhaseMCP, "slow", outcome, tooks[1]),
			startupEvent(observe.PhaseMCP, "quick", observe.ServerConnected, 80),
		)
	}
	rows, err := db.AgentStartupTimings(time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := observeStartupReport(rows, "30d", config.Config{}).Render(100)

	for _, want := range []string{
		"store", "median 400ms · worst 500ms",
		"slow", "median 12.0s · worst 20.0s", "2 connected, 1 timeout",
		"mcp.startup_timeout_seconds", "[mcp.servers.slow]", "timeout_seconds",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("startup report missing %q:\n%s", want, out)
		}
	}
	// Ranked by the worst: the slow server is above the quick one, and the
	// quick one, far inside its bound, is not told to change anything.
	if strings.Index(out, "slow") > strings.Index(out, "quick") {
		t.Errorf("servers are not ranked by their worst:\n%s", out)
	}
	if strings.Contains(out, "[mcp.servers.quick]") {
		t.Errorf("a quick server was given a timeout key:\n%s", out)
	}

	// Over half its bound without timing out is said too, under the bound the
	// config now sets.
	cfg := config.Config{MCP: config.MCPConfig{StartupTimeoutSeconds: 10}}
	over := observeStartupReport([]storage.AgentStartupTiming{
		{Phase: observe.PhaseMCP, Name: "half", Outcome: observe.ServerConnected, DurationMs: 6000},
	}, "30d", cfg).Render(100)
	if !strings.Contains(over, "[mcp.servers.half]") {
		t.Errorf("a connect over half its bound was not said:\n%s", over)
	}
	if got := observeStartupReport(nil, "30d", config.Config{}).Render(100); !strings.Contains(got, "no startup recorded") {
		t.Errorf("an empty window = %q", got)
	}
}

func TestObserveQuiet_ListsTheLongestWithWhatTheyWaitedOn(t *testing.T) {
	db := fixtureStore(t)
	quiet := func(turn int64, outcome, wait string, took, delivered int64) storage.AgentEvent {
		return storage.AgentEvent{Kind: storage.AgentEventQuiet, Turn: turn, Outcome: outcome, Reason: wait,
			DurationMs: ms(took), Delivered: ms(delivered)}
	}
	// A turn granted more rounds wrote two stretches; it is listed once, by its longest.
	a := seedTimingSession(t, db,
		quiet(1, observe.StretchSilent, observe.WaitModelFirst, 90000, 0),
		quiet(2, observe.StretchWaiting, observe.WaitTool, 5000, 0),
		quiet(2, observe.StretchWaiting, observe.WaitTool, 240000, 0))
	seedTimingSession(t, db, quiet(1, observe.StretchQuiet, observe.WaitModelStream, 30000, 41))
	for i := 0; i < 12; i++ {
		seedTimingSession(t, db, quiet(1, observe.StretchQuiet, observe.WaitModelStream, int64(1000+i), 1))
	}

	stretches, err := db.AgentQuietStretches(time.Now().AddDate(0, 0, -30), observeQuietLimit)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(stretches) != observeQuietLimit {
		t.Fatalf("listed %d stretches, want %d", len(stretches), observeQuietLimit)
	}
	if first := stretches[0]; first.SessionID != a || first.Turn != 2 || first.DurationMs != 240000 {
		t.Errorf("longest = %+v, want session %d turn 2 at 240s", first, a)
	}
	out := observeQuietReport(stretches, "30d").Render(120)
	for _, want := range []string{
		"4m0s", "waited on a tool", "the stream delivered nothing",
		"1m30s", "waited on the model's first event", "silent",
		"30.0s", "waited on the model writing", "the stream delivered 41 events",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("quiet report missing %q:\n%s", want, out)
		}
	}

	// The dashboard names the longest and the way in.
	notes := observeLongestQuiet(stretches[:1])
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "4m0s") || !strings.Contains(notes[0].Text, "shhh observe quiet") {
		t.Errorf("dashboard line = %+v", notes)
	}
}

func TestObserveSession_ShowsTheTurnSplit(t *testing.T) {
	db := fixtureStore(t)
	id := seedTimingSession(t, db,
		// A turn from a surface that splits its time, paused then granted more
		// rounds: the later row carries the whole split.
		storage.AgentEvent{Kind: storage.AgentEventTurn, Turn: 1, Round: 20, Outcome: observe.TurnCapPaused, DurationMs: ms(100000),
			ModelFirstMs: ms(1000), ModelStreamMs: ms(9000), ToolMs: ms(50000), PersonMs: ms(40000)},
		storage.AgentEvent{Kind: storage.AgentEventTurn, Turn: 1, Round: 30, Outcome: observe.TurnDone, DurationMs: ms(480000),
			ModelFirstMs: ms(4000), ModelStreamMs: ms(56000), ToolMs: ms(360000), PersonMs: ms(60000)},
		// A turn from a surface that does not.
		storage.AgentEvent{Kind: storage.AgentEventTurn, Turn: 2, Round: 3, Outcome: observe.TurnDone, DurationMs: ms(2000)},
	)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := renderObserveSession(cmd, db, id, false); err != nil {
		t.Fatalf("render session: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"model first 4.0s", "model writing 56.0s", "tools 6m0s", "person 1m0s",
		"split not recorded on this surface",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("session page missing %q:\n%s", want, out)
		}
	}
	// Each row draws its own split, and the turn without one carries no figure
	// rather than zeros.
	if strings.Count(out, "tools ") != 2 || !strings.Contains(out, "tools 50.0s") {
		t.Errorf("only the two split rows of turn 1 carry a split, each its own:\n%s", out)
	}
}
