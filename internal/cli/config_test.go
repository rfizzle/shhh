package cli

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The host half of the config screen (S-127): what a setting means, what its
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

// §19a: every row states where its value came from, and there are only three
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
// keystroke; §19a asks for the opposite, and the header counts what is
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
