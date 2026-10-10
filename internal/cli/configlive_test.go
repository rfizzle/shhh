package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// keepsSentence is the receipt's sentence for a write the session cannot take.
const keepsSentence = "This session keeps the settings it started on; the next one starts on these."

// settingRow is the settings row for key.
func settingRow(t *testing.T, rows []components.ConfigRow, key string) components.ConfigRow {
	t.Helper()
	for _, row := range rows {
		if row.Group != "FLOWS" && row.Key == key {
			return row
		}
	}
	t.Fatalf("no row for %s", key)
	return components.ConfigRow{}
}

// stage stages one value on the screen, as [enter] on a row does.
func stage(session chat.ConfigSession, key, value string) {
	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: key, Value: value}})
}

// Every key the table says a session takes at its next turn is one this file
// knows how to hand it or one its readers hold, and no key read at the open
// is handed over: the attribute and the session cannot come apart.
func TestSettings_EveryLiveKeyHasAReader(t *testing.T) {
	for _, s := range config.Settings() {
		_, taker := liveTakers[s.Key]
		switch {
		case s.Live() && !taker && !heldKey(s.Key):
			t.Errorf("%s is taken at the next turn, and nothing in the session takes it", s.Key)
		case !s.Live() && (taker || heldKey(s.Key)):
			t.Errorf("%s is read at the open, and the session is handed it anyway", s.Key)
		}
	}
}

// A live key staged on the screen is the session's as it is staged: it is
// handed to the session with that answer, a key read at the call is held for
// its reader, nothing reaches the file, and after the write the session keeps
// it with nothing left to hand over.
func TestConfigScreen_ALiveKeyIsTakenAsItIsStaged(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model",
		resolveWith: func(c config.Config) resolve.Resolved {
			return resolve.Resolved{Model: c.Provider.Model, Reasoning: c.Provider.Reasoning}
		}}
	_, session := stagedFlowScreen(t, "", env)
	if session.Take == nil {
		t.Fatal("a session's screen has nothing to hand the session")
	}

	stage(session, "behavior.max_tool_rounds", "40")
	stage(session, "behavior.default_mode", "auto")
	stage(session, "provider.model", "model-b")
	stage(session, "provider.reasoning", "high")
	stage(session, "behavior.progress_interval_seconds", "30")
	stage(session, "appearance.verbosity", "high")
	w := chat.Wiring{MaxToolRounds: 150}
	if !session.Take(&w) {
		t.Fatal("staging live keys handed the session nothing")
	}
	if w.MaxToolRounds != 40 || w.Mode != agent.ModeAuto || w.ModelName != "model-b" ||
		w.Effort != provider.EffortHigh || w.ProgressElapsed != 30*time.Second || w.Verbosity != "high" {
		t.Errorf("the session was handed %+v", struct {
			Rounds  int
			Mode    agent.Mode
			Model   string
			Effort  provider.Effort
			Elapsed time.Duration
			Density string
		}{w.MaxToolRounds, w.Mode, w.ModelName, w.Effort, w.ProgressElapsed, w.Verbosity})
	}
	if session.Take(&w) {
		t.Error("the same staging was handed over twice")
	}
	if row := settingRow(t, session.Screen.Rows, "behavior.max_tool_rounds"); row.Source != "unwritten" {
		t.Errorf("a staged live key reads %q, want `unwritten`: this session only", row.Source)
	}

	// The readings' cadence is read when a reading is scheduled, so it is
	// held where that reader asks.
	stage(session, "summary.interval_rounds", "4")
	cadence := env.cadenceAt(config.Config{})
	if rounds, _ := cadence(); rounds != 4 {
		t.Errorf("the next reading is scheduled every %d rounds, want the staged 4", rounds)
	}

	note := session.Answer(false, components.ConfigResult{Write: true})
	if strings.Contains(note, keepsSentence) {
		t.Errorf("a write of live keys alone says the session did not take them: %q", note)
	}
	if session.Take(&w) || w.MaxToolRounds != 40 {
		t.Errorf("the write moved the session: %d rounds", w.MaxToolRounds)
	}
	if rounds, _ := cadence(); rounds != 4 {
		t.Errorf("the session let go of the cadence once it was written: %d", rounds)
	}
}

// Leaving the screen over a taken value keeps it on the session, the screen
// opened again reads it as `session` and counts it as unwritten, and a later
// write carries it and names only what it carried.
func TestConfigScreen_LeavingKeepsAStagedLiveKey(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	path, session := stagedFlowScreen(t, "[behavior]\nmax_tool_rounds = 30\n", env)

	stage(session, "behavior.max_tool_rounds", "40")
	stage(session, "behavior.command_timeout_seconds", "90")
	if session.Screen.Held != 1 || session.Screen.Changed != 2 {
		t.Errorf("the screen holds %d of %d staged changes, want 1 of 2: the startup key waits for the next session",
			session.Screen.Held, session.Screen.Changed)
	}
	w := chat.Wiring{MaxToolRounds: 30}
	session.Take(&w)
	session.Answer(true, components.ConfigResult{Canceled: true})
	if session.Take(&w) || w.MaxToolRounds != 40 {
		t.Errorf("leaving moved the session off its 40 rounds: %d", w.MaxToolRounds)
	}

	again := reopenedScreen(t, env)
	row := settingRow(t, again.Screen.Rows, "behavior.max_tool_rounds")
	if row.Source != "session" || row.Value != "40" {
		t.Errorf("the reopened row reads %q from %q, want 40 from `session`", row.Value, row.Source)
	}
	if again.Screen.Changed != 1 || again.Screen.Held != 1 {
		t.Errorf("the reopened header counts %d changes, %d held, want 1 and 1", again.Screen.Changed, again.Screen.Held)
	}
	if row := settingRow(t, again.Screen.Rows, "behavior.command_timeout_seconds"); strings.Contains(row.Source, "session") {
		t.Errorf("a key the session never took reads %q", row.Source)
	}

	note := again.Answer(false, components.ConfigResult{Write: true})
	if !strings.Contains(note, "behavior.max_tool_rounds") || strings.Contains(note, "command_timeout") {
		t.Errorf("the receipt of the write reads %q", note)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "max_tool_rounds = 40") {
		t.Errorf("the write did not carry the held value:\n%s", got)
	}
	cfg := config.Config{}
	cfg.Behavior.MaxToolRounds = 40
	if held := heldAtExit(env, cfg); held != "" {
		t.Errorf("a written value is named on the way out: %q", held)
	}
}

// A row the session holds goes back to what the file holds on its reset, and
// the session is handed the file's value.
func TestConfigScreen_AResetRowGoesBackToTheFile(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	_, first := stagedFlowScreen(t, "[behavior]\nmax_tool_rounds = 30\n[summary]\nmin_gap_seconds = 50\n", env)
	stage(first, "behavior.max_tool_rounds", "40")
	stage(first, "summary.min_gap_seconds", "5")
	w := chat.Wiring{MaxToolRounds: 30}
	first.Take(&w)
	first.Answer(true, components.ConfigResult{Canceled: true})
	cfg := config.Config{}
	cfg.Summary.MinGapSeconds = 50
	if _, gap := env.cadenceAt(cfg)(); gap != 5*time.Second {
		t.Fatalf("the readings' floor is %s after leaving", gap)
	}

	again := reopenedScreen(t, env)
	again.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "behavior.max_tool_rounds", Reset: true}})
	again.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "summary.min_gap_seconds", Reset: true}})
	if !again.Take(&w) || w.MaxToolRounds != 30 {
		t.Errorf("the session runs on %d rounds after the reset, want the file's 30", w.MaxToolRounds)
	}
	if _, gap := env.cadenceAt(cfg)(); gap != 50*time.Second {
		t.Errorf("the readings' floor is %s after the reset, want the file's", gap)
	}
	if again.Screen.Changed != 0 {
		t.Errorf("the header counts %d changes with every row back on the file", again.Screen.Changed)
	}
}

// A value the session holds is `session` on the screen opened again, and a
// session that ends holding values names each once; a value the file has
// caught up with is not named.
func TestConfigScreen_ASessionValueIsNamedSession(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	_, session := stagedFlowScreen(t, "", env)
	stage(session, "appearance.verbosity", "high")
	stage(session, "summary.model", "reader-model")
	if row := settingRow(t, session.Screen.Rows, "appearance.verbosity"); row.Source != "unwritten" {
		t.Errorf("a value staged now reads %q", row.Source)
	}
	session.Answer(true, components.ConfigResult{Canceled: true})
	again := reopenedScreen(t, env)
	if row := settingRow(t, again.Screen.Rows, "appearance.verbosity"); row.Source != "session" {
		t.Errorf("a value the session holds reads %q", row.Source)
	}
	got := heldAtExit(env, config.Config{})
	if !strings.Contains(got, "appearance.verbosity") || !strings.Contains(got, "summary.model") ||
		strings.Count(got, "appearance.verbosity") != 1 {
		t.Errorf("the way out says %q", got)
	}
	cfg := config.Config{}
	cfg.Appearance.Verbosity = "high"
	if got := heldAtExit(env, cfg); strings.Contains(got, "verbosity") {
		t.Errorf("a value the file holds is named: %q", got)
	}
}

// A staged key the session wires when it opens is not taken: its row says the
// next session is the one it reaches, and the receipt's sentence that the
// session keeps the settings it started on is said of a write that carried
// one, and only of such a write.
func TestSettings_TheReceiptNamesOnlyWhatTheSessionCannotTake(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	_, session := stagedFlowScreen(t, "", env)

	stage(session, "behavior.check_in_interval_rounds", "12")
	// The session asks after every answer, as the chat does.
	session.Take(&chat.Wiring{})
	if note := session.Answer(false, components.ConfigResult{Write: true}); strings.Contains(note, keepsSentence) {
		t.Errorf("a write of a live key says the session keeps what it started on: %q", note)
	}

	stage(session, "behavior.command_timeout_seconds", "90")
	if row := settingRow(t, session.Screen.Rows, "behavior.command_timeout_seconds"); row.Source != "unwritten · next session" {
		t.Errorf("a staged startup key reads %q, want `unwritten · next session`", row.Source)
	}
	w := chat.Wiring{}
	if session.Take(&w) {
		t.Error("a key read at the open was handed to the session")
	}
	stage(session, "appearance.notify", "false")
	if row := settingRow(t, session.Screen.Rows, "appearance.notify"); row.Source != "unwritten" {
		t.Errorf("a staged live key beside it reads %q", row.Source)
	}
	if note := session.Answer(false, components.ConfigResult{Write: true}); !strings.HasSuffix(note, keepsSentence) {
		t.Errorf("a write carrying a startup key lost the sentence: %q", note)
	}

	// `shhh config` has no session: a staged row says nothing of one.
	m := newConfigModel(config.Config{}, config.Project{})
	m.apply(components.ConfigChange{Key: "behavior.command_timeout_seconds", Value: "90"})
	if row := settingRow(t, m.screen.Rows, "behavior.command_timeout_seconds"); row.Source != "unwritten" {
		t.Errorf("outside a session a staged row reads %q", row.Source)
	}
}
