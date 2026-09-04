package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The listing is the missing half of the outranked notices: a session says
// when a saved model is overruled, and this says the effective value of every
// other key and which rank decided it.
func TestConfigList_StatesWhereEachValueCameFrom(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"gpt-4o\"\n")
	t.Setenv("SHHH_REASONING", "high")

	out := runRoot(t, "config", "list", "provider")
	for _, want := range []string{
		"model", "gpt-4o", "[file]",
		"reasoning", "high", "[SHHH_REASONING]",
		"cache_ttl", "[default]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

// A section is one table of the file, and a name that is not one says so
// rather than printing nothing.
func TestConfigList_TakesOneSection(t *testing.T) {
	pointConfigAt(t, "")
	if out := runRoot(t, "config", "list", "todo"); strings.Contains(out, "PROVIDER") {
		t.Errorf("`config list todo` should list one table:\n%s", out)
	}
	var buf strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"config", "list", "behaviour"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a section that is not one should be refused")
	}
}

// --json carries the same figures the listing prints, so a script and a
// reader are looking at one reading.
func TestConfigList_JSONCarriesTheSameFigures(t *testing.T) {
	pointConfigAt(t, "[history]\nretention_days = 7\n")
	var readings []configReading
	if err := json.Unmarshal([]byte(runRoot(t, "config", "list", "history", "--json")), &readings); err != nil {
		t.Fatal(err)
	}
	if len(readings) != 1 {
		t.Fatalf("the history table has one key, got %d", len(readings))
	}
	got := readings[0]
	if got.Key != "history.retention_days" || got.Value != "7" || got.Source != "file" || !got.Set {
		t.Fatalf("reading = %+v", got)
	}
	if got.Default == "" || got.Desc == "" {
		t.Errorf("the JSON drops what the key decides: %+v", got)
	}
}

// `config get` is one key at length, and a key nobody has is the nearest one
// they might have meant — the same offer a misspelled key in the file gets.
func TestConfigGet_OneKeyAndTheNearestForAnUnknownOne(t *testing.T) {
	pointConfigAt(t, "")
	out := runRoot(t, "config", "get", "behavior.default_mode")
	if !strings.Contains(out, "manual") || !strings.Contains(out, "[default]") {
		t.Errorf("`config get` should state the value and its rank:\n%s", out)
	}
	if !strings.Contains(out, "accept-edits") {
		t.Errorf("`config get` on a word key should name the words:\n%s", out)
	}

	var buf strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"config", "get", "behaviour.shell"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an unknown key should be refused")
	}
	if !strings.Contains(err.Error(), "behavior.shell") {
		t.Errorf("the refusal should name the nearest key, got %q", err)
	}
}

// The spelling the role models used to take names the one to use instead, at
// every door: "unknown" would send a person looking for a setting they still
// have.
func TestConfigGet_TheOldRoleModelSpellingNamesTheNewKey(t *testing.T) {
	pointConfigAt(t, "")
	var buf strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"config", "get", "agents.reviewer_model"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "agents.profiles.reviewer.model") {
		t.Fatalf("the old spelling should name the new key, got %v", err)
	}
}

// Every key in the table has a row, and every row is a key in the table. The
// screen used to be a hand-kept list, and the keys nobody added to it were
// reachable only by opening the file.
func TestConfigRows_OneRowPerTableEntry(t *testing.T) {
	var cfg config.Config
	rows := configRows(cfg, cfg)
	if len(rows) != len(configEntries(cfg)) {
		t.Fatalf("%d rows for %d settings", len(rows), len(configEntries(cfg)))
	}
	for _, s := range configEntries(cfg) {
		row := rowFor(rows, s.Key)
		if row.Key != s.Key {
			t.Errorf("%s has no row", s.Key)
			continue
		}
		if row.Label == "" || row.Group == "" {
			t.Errorf("%s: label %q group %q", s.Key, row.Label, row.Group)
		}
	}
}

// A picker comes from the kind rather than from a case per key, so a word key
// that lands in the table lands on the screen with its words to choose from.
func TestConfigRows_PickersComeFromTheKind(t *testing.T) {
	var cfg config.Config
	rows := configRows(cfg, cfg)
	for _, s := range configEntries(cfg) {
		row := rowFor(rows, s.Key)
		switch s.Kind {
		case config.KindEnum, config.KindBool:
			if len(row.Options) == 0 {
				t.Errorf("%s takes a closed set of answers and offers none", s.Key)
			}
		case config.KindInt, config.KindList, config.KindPath:
			if len(row.Options) > 0 {
				t.Errorf("%s is typed into and offers %d answers", s.Key, len(row.Options))
			}
		}
	}
}

// Every key whose value is a word is judged against the vocabulary that owns
// it, so a word the session would not accept cannot be saved for it to read
// later — and the words the table carries are the words that owner takes.
func TestCheckConfigValue_EveryWordKeyIsJudged(t *testing.T) {
	const nonsense = "not-a-word-any-key-takes"
	for _, s := range config.Settings() {
		if len(s.Values) == 0 {
			continue
		}
		key := strings.ReplaceAll(s.Key, config.RoleWildcard, "reviewer")
		if err := checkConfigValue(key, nonsense); err == nil {
			t.Errorf("%s takes words and accepted %q", key, nonsense)
		}
		for _, word := range s.Values {
			if err := checkConfigValue(key, word); err != nil {
				t.Errorf("%s should take %q, its own vocabulary: %v", key, word, err)
			}
		}
	}
}

// The defaults the table states are the constants the code runs on. They are
// words in the table because the config package is a leaf and cannot import
// the packages that hold them, so this is what keeps the two from drifting —
// the failure it prevents is a reference section confidently describing a
// machine that does not exist.
func TestSettings_StatedDefaultsAreTheConstants(t *testing.T) {
	for key, want := range map[string]string{
		"provider.default":                     resolve.DefaultProvider,
		"provider.reasoning":                   provider.DefaultEffort.String(),
		"provider.cache_ttl":                   string(provider.DefaultCacheTTL),
		"behavior.max_tool_rounds":             strconv.Itoa(agent.DefaultMaxToolRounds),
		"behavior.context_max_tokens":          strconv.Itoa(config.DefaultContextMaxTokens) + " tokens",
		"behavior.check_in_interval_rounds":    strconv.Itoa(agent.DefaultCheckInInterval) + " rounds",
		"behavior.check_in_max_doublings":      strconv.Itoa(agent.DefaultCheckInDoublings) + " doublings",
		"behavior.provider_retries":            strconv.Itoa(agent.MaxRetryAttempts) + " attempts",
		"behavior.memory_max_entries":          strconv.Itoa(config.DefaultMemoryMaxEntries),
		"behavior.memory_max_tokens":           strconv.Itoa(config.DefaultMemoryMaxTokens),
		"summary.intervene_cooldown_intervals": strconv.Itoa(agent.DefaultCooldownIntervals) + " readings",
		"summary.steer_target_chars":           strconv.Itoa(agent.DefaultSteerTargetChars) + " characters",
		"appearance.paste_lines":               strconv.Itoa(attachment.DefaultPasteLines) + " lines",
		"appearance.paste_columns":             strconv.Itoa(attachment.DefaultPasteColumns) + " columns",
		"appearance.rail_width":                components.RailWidthAuto,
		"agents.model":                         config.InheritModel,
		"history.retention_days":               strconv.Itoa(config.DefaultRetentionDays) + " days",
		"reports.retention_days":               strconv.Itoa(config.DefaultRetentionDays) + " days",
		"observe.retention_days":               strconv.Itoa(config.DefaultObserveRetentionDays) + " days",
	} {
		s, ok := config.Lookup(key)
		if !ok {
			t.Errorf("%s is not in the table", key)
			continue
		}
		if s.Default != want {
			t.Errorf("%s states %q as its default, the code runs on %q", key, s.Default, want)
		}
	}
}

// A key something can overrule says so, and a key whose value already came
// from that rank does not repeat itself: the row has just stated it.
func TestConfigGet_NamesWhatOutranksTheFile(t *testing.T) {
	pointConfigAt(t, "[provider]\nmodel = \"gpt-4o\"\n")
	out := runRoot(t, "config", "get", "provider.model")
	if !strings.Contains(out, "--model") || !strings.Contains(out, "SHHH_MODEL") {
		t.Errorf("the row should name both ranks above the file:\n%s", out)
	}

	t.Setenv("SHHH_MODEL", "o3")
	out = runRoot(t, "config", "get", "provider.model")
	if !strings.Contains(out, "[SHHH_MODEL]") {
		t.Errorf("the environment should be the source:\n%s", out)
	}
	if strings.Contains(out, "outrank") {
		t.Errorf("the source rank should not also be listed as outranking:\n%s", out)
	}
}

// The listing says the same as the screen about a variable: the name, and
// whether the environment has it. The state rides in the JSON as well, so a
// script checking a machine's setup reads what the row printed.
func TestConfigReading_AVariableCarriesItsStateIntoTheListing(t *testing.T) {
	t.Setenv("SHHH_TEST_LIST_VAR", "sk-exported")
	var cfg config.Config
	cfg.Provider.APIKeyEnv = "SHHH_TEST_LIST_VAR"
	s, ok := config.Lookup("provider.api_key_env")
	if !ok {
		t.Fatal("provider.api_key_env is not a setting")
	}
	reading := configReadingOf(cfg, s)
	if reading.Value != "SHHH_TEST_LIST_VAR" || reading.Source != "file" {
		t.Fatalf("the reading does not name the variable the file set: %+v", reading)
	}
	if reading.EnvSet == nil || !*reading.EnvSet {
		t.Fatalf("the reading does not say the variable is exported: %+v", reading)
	}
	if got := configRow(reading).Detail; got != "set in the environment" {
		t.Fatalf("the row does not print the state, got %q", got)
	}

	unnamed := configReadingOf(cfg, mustLookup(t, "provider.model"))
	if unnamed.EnvSet != nil {
		t.Fatalf("a key naming no variable claims one is missing: %+v", unnamed)
	}
}

func mustLookup(t *testing.T, key string) config.Setting {
	t.Helper()
	s, ok := config.Lookup(key)
	if !ok {
		t.Fatalf("%s is not a setting", key)
	}
	return s
}
