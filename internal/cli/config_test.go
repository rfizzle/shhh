package cli

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The host half of the config screen: what a setting means, what its
// default is, and when any of it reaches the file. The screen's own rules are
// tested where the screen is.

func rowFor(rows []components.ConfigRow, key string) components.ConfigRow {
	for _, row := range rows {
		if row.Key == key {
			return row
		}
	}
	return components.ConfigRow{}
}

// Every row states where its value came from, and there are only three
// answers — the built-in default stands, the file set it, or this session
// staged something the file has not got.
func TestConfigRows_SourceStatesWhereTheValueCameFrom(t *testing.T) {
	var base config.Config
	base.Provider.Default = "openai"

	rows := configRows(base, base)
	if got := rowFor(rows, "provider.default").Source; got != "user" {
		t.Fatalf("a value the file set reads as user, got %q", got)
	}
	if got := rowFor(rows, "behavior.shell").Source; got != "default" {
		t.Fatalf("a value nothing set reads as default, got %q", got)
	}

	staged := base
	staged.Behavior.Shell = "/bin/zsh"
	rows = configRows(staged, base)
	if got := rowFor(rows, "behavior.shell").Source; got != "unwritten" {
		t.Fatalf("a staged edit says it is not in the file yet, got %q", got)
	}
}

// A setting nothing has set shows what will happen anyway rather than an
// empty column: the default is the answer to "why is this on".
func TestConfigRows_UnsetRowsShowTheirDefault(t *testing.T) {
	var cfg config.Config
	rows := configRows(cfg, cfg)
	for _, row := range rows {
		if row.Value == "" {
			t.Fatalf("%s renders nothing when it is unset", row.Key)
		}
	}
	if want := strconv.Itoa(agent.DefaultMaxToolRounds); rowFor(rows, "behavior.max_tool_rounds").Value != want {
		t.Fatalf("the round limit states the built-in default %q, got %q", want, rowFor(rows, "behavior.max_tool_rounds").Value)
	}
	if got := rowFor(rows, "behavior.default_mode").Value; got != "⏸ manual" {
		t.Fatalf("the unset mode is manual and says so with its glyph, got %q", got)
	}
}

// The mode glyph carries the distinction the colour also carries: `⏵⏵` for
// the two modes that let work through, `⏸` for the two that gate it.
func TestConfigRows_ModeGlyphMatchesTheCockpit(t *testing.T) {
	for _, tc := range []struct {
		mode, glyph string
		tone        components.FieldTone
	}{
		{"auto", "⏵⏵", components.ToneSafe},
		{"accept-edits", "⏵⏵", components.ToneSafe},
		{"manual", "⏸", components.ToneOpen},
		{"plan", "⏸", components.ToneOpen},
	} {
		var cfg config.Config
		cfg.Behavior.DefaultMode = tc.mode
		row := rowFor(configRows(cfg, cfg), "behavior.default_mode")
		if !strings.HasPrefix(row.Value, tc.glyph+" ") {
			t.Errorf("%s renders as %q, want the %s glyph", tc.mode, row.Value, tc.glyph)
		}
		if row.ValueTone != tc.tone {
			t.Errorf("%s is toned %v, want %v", tc.mode, row.ValueTone, tc.tone)
		}
		if row.Detail == "" {
			t.Errorf("%s says what it does", tc.mode)
		}
	}
}

// A mode the file holds that is not a mode says so rather than rendering as
// though it worked (invariant 4).
func TestConfigRows_AnUnreadableModeSaysSo(t *testing.T) {
	var cfg config.Config
	cfg.Behavior.DefaultMode = "yolo"
	row := rowFor(configRows(cfg, cfg), "behavior.default_mode")
	if row.ValueTone != components.ToneRisk || !strings.Contains(row.Detail, "not a mode") {
		t.Fatalf("an unreadable mode states the problem: %+v", row)
	}
}

// The key is masked on the row and marked so the screen opens the masked
// entry rather than a field showing it.
func TestConfigRows_TheKeyIsMasked(t *testing.T) {
	var cfg config.Config
	cfg.Provider.APIKey = "sk-live-0000-4f9c"
	row := rowFor(configRows(cfg, cfg), "provider.api_key")
	if !row.Secret {
		t.Fatal("the api key row is a secret")
	}
	if row.Value != "···4f9c" || strings.Contains(row.Value, "sk-") {
		t.Fatalf("the row shows the last four and nothing else, got %q", row.Value)
	}
}

// Nothing reaches the file until [w]. The old wizard saved on every
// keystroke; the screen asks for the opposite, and the header counts what is
// standing against the file in the meantime.
func TestConfigModel_StagesUntilWrite(t *testing.T) {
	var cfg config.Config
	cfg.Provider.Default = "openai"
	m := newConfigModel(cfg)
	if m.screen.Changed != 0 {
		t.Fatalf("a freshly loaded config has nothing standing against it, got %d", m.screen.Changed)
	}

	m.apply(components.ConfigChange{Key: "behavior.max_tool_rounds", Value: "40"})
	if m.cfg.Behavior.MaxToolRounds != 40 {
		t.Fatalf("the edit is staged, got %d", m.cfg.Behavior.MaxToolRounds)
	}
	if m.base.Behavior.MaxToolRounds != 0 {
		t.Fatal("the loaded copy is what a row is compared against and never moves")
	}
	if m.screen.Changed != 1 {
		t.Fatalf("the header counts one change, got %d", m.screen.Changed)
	}
	if m.saved {
		t.Fatal("nothing is written until [w]")
	}
}

// [r] puts one key back to its default, which is the zero value the loader
// would have left there.
func TestConfigModel_ResetClearsOneKey(t *testing.T) {
	var cfg config.Config
	cfg.Behavior.Shell = "/bin/zsh"
	m := newConfigModel(cfg)
	m.apply(components.ConfigChange{Key: "behavior.shell", Reset: true})
	if m.cfg.Behavior.Shell != "" {
		t.Fatalf("reset clears the key, got %q", m.cfg.Behavior.Shell)
	}
	if !strings.Contains(m.screen.Notice, "behavior.shell") {
		t.Fatalf("the reset says which key it was: %q", m.screen.Notice)
	}
}

// A key the config package does not know is reported on the screen rather
// than dropped silently.
func TestConfigModel_AnUnknownKeyIsReported(t *testing.T) {
	m := newConfigModel(config.Config{})
	m.apply(components.ConfigChange{Key: "behavior.nonsense", Value: "1"})
	if !strings.Contains(m.screen.Notice, "unknown config key") {
		t.Fatalf("the screen says what went wrong: %q", m.screen.Notice)
	}
}

// Leaving without writing quits and saves nothing.
func TestConfigModel_EscWritesNothing(t *testing.T) {
	m := newConfigModel(config.Config{})
	m.apply(components.ConfigChange{Key: "behavior.shell", Value: "/bin/zsh"})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc quits")
	}
	if next.(configModel).saved {
		t.Fatal("esc discards rather than writing")
	}
}

// The paste thresholds are on the screen, with the default stated where
// nothing is set — the screen is where a reader finds out what this machine
// actually does, and /help can only name the defaults.
func TestConfigRows_PasteThresholdsStateTheirDefault(t *testing.T) {
	rows := configRows(config.Config{}, config.Config{})
	lines := rowFor(rows, "appearance.paste_lines")
	if lines.Value != "10 lines" || lines.Source != "default" {
		t.Fatalf("unset paste_lines row = %q/%q, want the default", lines.Value, lines.Source)
	}
	columns := rowFor(rows, "appearance.paste_columns")
	if columns.Value != "1000 columns" {
		t.Fatalf("unset paste_columns row = %q", columns.Value)
	}

	// A negative is not a threshold, and the row has to read it as the
	// answer it is rather than as "-1".
	var off config.Config
	off.Appearance.PasteLines = -1
	row := rowFor(configRows(off, config.Config{}), "appearance.paste_lines")
	if !strings.Contains(row.Value, "never") {
		t.Fatalf("a negative paste_lines reads %q, want it stated in words", row.Value)
	}
}

// The interruption machinery's keys are on the screen, each stating the
// built-in value that stands while nothing is set — the screen is where a
// reader finds out what this machine actually does.
func TestConfigRows_SteeringKeysStateTheirDefaults(t *testing.T) {
	rows := configRows(config.Config{}, config.Config{})
	for key, want := range map[string]string{
		"behavior.check_in_interval_rounds":    strconv.Itoa(agent.DefaultCheckInInterval) + " rounds",
		"behavior.check_in_max_doublings":      strconv.Itoa(agent.DefaultCheckInDoublings) + " doublings",
		"summary.intervene_cooldown_intervals": strconv.Itoa(agent.DefaultCooldownIntervals) + " readings",
		"summary.steer_target_chars":           strconv.Itoa(agent.DefaultSteerTargetChars) + " characters",
		"prompts.steer":                        "(the built-in wording)",
		"prompts.check_in":                     "(the built-in wording)",
		"prompts.summary":                      "(the built-in wording)",
		"prompts.classifier":                   "(the built-in wording)",
	} {
		row := rowFor(rows, key)
		if row.Value != want || row.Source != "default" {
			t.Errorf("%s row = %q/%q, want %q/default", key, row.Value, row.Source, want)
		}
	}

	// The two numbers where a negative is not a bound read it as the answer
	// it is rather than as "-1".
	var off config.Config
	off.Behavior.CheckInMaxDoublings = -1
	off.Summary.SteerTargetChars = -1
	rows = configRows(off, config.Config{})
	if got := rowFor(rows, "behavior.check_in_max_doublings").Value; !strings.Contains(got, "never") {
		t.Errorf("a negative widening reads %q, want it stated in words", got)
	}
	if got := rowFor(rows, "summary.steer_target_chars").Value; !strings.Contains(got, "whole") {
		t.Errorf("a negative bound reads %q, want it stated in words", got)
	}
}

// The keys whose value is a word rather than a shape are judged by the
// parsers the running session judges them with, so a name the session would
// not accept cannot be saved for it to read later.
func TestCheckConfigValue_RefusesAWordOutsideItsKeysVocabulary(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"behavior.default_mode", "yolo"},
		{"behavior.mode_cycle", "manual, sometimes"},
		{"provider.reasoning", "sometimes"},
		{"provider.cache_ttl", "10m"},
		{"sandbox.profile", "wide-open"},
		{"sandbox.container_engine", "containerd"},
		{"sandbox.require_isolation", "some"},
	} {
		err := checkConfigValue(tc.key, tc.value)
		if err == nil {
			t.Errorf("%s = %q should be refused", tc.key, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s = %q: %q does not name the key", tc.key, tc.value, err)
		}
	}
	for _, tc := range []struct{ key, value string }{
		{"behavior.default_mode", "accept-edits"},
		{"behavior.mode_cycle", "manual, auto, plan"},
		{"provider.reasoning", "xhigh"},
		{"provider.cache_ttl", "1h"},
		{"sandbox.profile", "workspace-netless"},
		{"sandbox.container_engine", "podman"},
		{"sandbox.require_isolation", "container"},
		// An empty value is a reset, which every one of these keys takes.
		{"behavior.default_mode", ""},
		{"sandbox.require_isolation", ""},
	} {
		if err := checkConfigValue(tc.key, tc.value); err != nil {
			t.Errorf("%s = %q: %v", tc.key, tc.value, err)
		}
	}
}

// The screen refuses the same word `config set` does, on the row rather than
// after the write, so nothing is staged that the file would not take.
func TestConfigModel_AModeOutsideTheFourIsRefused(t *testing.T) {
	m := newConfigModel(config.Config{})
	m.apply(components.ConfigChange{Key: "behavior.default_mode", Value: "yolo"})
	if !strings.Contains(m.screen.Notice, "yolo") {
		t.Fatalf("the screen says what was wrong: %q", m.screen.Notice)
	}
	if m.cfg.Behavior.DefaultMode != "" || len(m.staged) != 0 {
		t.Fatalf("nothing is staged: %q %v", m.cfg.Behavior.DefaultMode, m.staged)
	}
}

// All three built-in roles have a model row, because a screen that offers
// one for a role and not its siblings reads as the others not being settable.
func TestConfigRows_TheRoleModelsAreListed(t *testing.T) {
	var cfg config.Config
	cfg.Agents.Profiles = map[string]config.AgentProfile{"reviewer": {Model: "claude-haiku-4-5"}}
	rows := configRows(cfg, config.Config{})
	for _, key := range []string{"agents.researcher_model", "agents.writer_model", "agents.reviewer_model"} {
		if rowFor(rows, key).Key != key {
			t.Errorf("%s is not on the screen", key)
		}
	}
	if got := rowFor(rows, "agents.reviewer_model").Value; got != "claude-haiku-4-5" {
		t.Errorf("the reviewer's model reads back as %q", got)
	}
	if got := rowFor(rows, "agents.writer_model").Source; got != "default" {
		t.Errorf("a role with no profile reads as default, got %q", got)
	}
}

// The lifetime of the repeated opening is on the screen, stating the default
// that stands while nothing is set — the screen is where a reader finds out
// what this machine actually does.
func TestConfigRows_TheCacheLifetimeStatesItsDefault(t *testing.T) {
	row := rowFor(configRows(config.Config{}, config.Config{}), "provider.cache_ttl")
	if row.Value != string(provider.DefaultCacheTTL) || row.Source != "default" {
		t.Fatalf("unset cache_ttl row = %q/%q, want the default", row.Value, row.Source)
	}

	var set config.Config
	set.Provider.CacheTTL = "5m"
	if got := rowFor(configRows(set, config.Config{}), "provider.cache_ttl").Value; got != "5m" {
		t.Errorf("a set cache_ttl reads back as %q", got)
	}
}

// The backlog run's commit row states its default rather than reading as
// unset, because unset is on and only false is a fact worth showing as set.
func TestConfigRows_TheBacklogCommitStatesItsDefault(t *testing.T) {
	row := rowFor(configRows(config.Config{}, config.Config{}), "todo.commit")
	if row.Value != "on" || row.Source != "default" {
		t.Fatalf("unset todo.commit row = %q/%q, want the default", row.Value, row.Source)
	}
	var off config.Config
	no := false
	off.Todo.Commit = &no
	if got := rowFor(configRows(off, config.Config{}), "todo.commit").Value; got != "false" {
		t.Errorf("todo.commit = false reads back as %q", got)
	}
}
