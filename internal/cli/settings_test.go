package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// The stamp, field by field, from a known config: what the config named is
// kept, what it left unset resolves the way the mechanism resolves it, and
// a mechanism the surface does not have leaves its fields empty.
func TestSessionSettings_FieldByField(t *testing.T) {
	var cfg config.Config
	cfg.Summary.Model = "small-model"
	cfg.Summary.IntervalRounds = 25
	cfg.Sandbox.Profile = "workspace-netless"

	got := sessionSettings(cfg, runSettings{
		mode: agent.ModeAcceptEdits.String(), effort: provider.EffortLow, rounds: 40,
		sandbox: "workspace-netless", model: "session-model", summary: true, classifier: true,
	})
	want := storage.AgentSettings{
		Mode: "accept-edits", Reasoning: "low", MaxRounds: 40,
		SummaryModel: "small-model", SummaryInterval: 25, SummaryEnabled: true,
		// Unset in the config, so the session model — the rule the
		// classifier itself resolves by.
		ClassifierModel: "session-model",
		SandboxProfile:  "workspace-netless",
		ConfigHash:      got.ConfigHash,
	}
	if got != want {
		t.Fatalf("settings:\ngot  %+v\nwant %+v", got, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(got.ConfigHash) {
		t.Fatalf("config hash %q is not a fingerprint", got.ConfigHash)
	}

	// The interval defaults the way the summariser defaults it, and a
	// surface with no readings and no classifier records neither model.
	bare := sessionSettings(config.Config{}, runSettings{effort: provider.EffortMedium})
	if bare.SummaryInterval != 0 || bare.SummaryModel != "" || bare.SummaryEnabled || bare.ClassifierModel != "" || bare.Mode != "" {
		t.Fatalf("a surface without the mechanisms must not record them: %+v", bare)
	}
	withSummary := sessionSettings(config.Config{}, runSettings{model: "m", summary: true})
	if withSummary.SummaryInterval != agent.DefaultSummaryInterval || withSummary.SummaryModel != "m" {
		t.Fatalf("summary defaults: %+v", withSummary)
	}
}

// roundCapFor spells maxRoundsFor's three answers the one way the record
// keeps them: the number, or 0 for none.
func TestRoundCapFor(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{agent.UnlimitedToolRounds, 0},
		{0, agent.DefaultMaxToolRounds},
		{35, 35},
	} {
		if got := roundCapFor(c.in); got != c.want {
			t.Errorf("roundCapFor(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// settingsAllowlist is every config key the stamp keeps whole. A key not on
// it reaches the store only through the hash, and a key added to the config
// is off it until someone adds it here and in sessionSettings both.
var settingsAllowlist = map[string]bool{
	"Summary.Model":            true,
	"Summary.IntervalRounds":   true,
	"Behavior.ClassifierModel": true,
}

// Nothing off the allowlist reaches the stamp. Every config field is filled
// with a marker that names it — by reflection, so a field added to the
// config is marked without anyone remembering to — and the stamped set is
// then checked to contain no marker but the allowlisted ones. The paths,
// the command lists and the secrets are the point; the test is written over
// every field so it does not have to know which those are. What arrives
// through runSettings — the mode, the profile — is the surface's own and is
// trusted here: each of those is parsed from a closed set before it is
// handed over.
func TestSessionSettings_KeepsNothingOffTheAllowlist(t *testing.T) {
	var cfg config.Config
	ints := map[string]int{}
	marked := markEveryField(reflect.ValueOf(&cfg).Elem(), "", ints)
	if len(marked) < 40 {
		t.Fatalf("marked only %d config fields; the walk is not reaching the config", len(marked))
	}

	run := runSettings{
		mode: "auto", effort: provider.EffortHigh, rounds: 60,
		sandbox: "workspace", model: "session-model", summary: true, classifier: true,
	}
	got := sessionSettings(cfg, run)

	// The string side: no marker but an allowlisted one appears anywhere
	// in the stamped set.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range marked {
		marker := "marker-" + path
		if strings.Contains(string(data), marker) && !settingsAllowlist[path] {
			t.Errorf("%s reached the stamp: %s", path, data)
		}
	}
	// The integer side: every stamped count is one the run named or an
	// allowlisted key's marker, never another key's.
	allowedInts := map[int]bool{run.rounds: true}
	for path, n := range ints {
		if settingsAllowlist[path] {
			allowedInts[n] = true
		}
	}
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.Int && f.Int() != 0 && !allowedInts[int(f.Int())] {
			t.Errorf("%s = %d is a value from a key off the allowlist", v.Type().Field(i).Name, f.Int())
		}
	}
	// And the allowlisted ones do come through, so the check above is not
	// passing because the walk marked nothing that mattered.
	if got.SummaryModel != "marker-Summary.Model" || got.ClassifierModel != "marker-Behavior.ClassifierModel" {
		t.Fatalf("allowlisted values did not reach the stamp: %+v", got)
	}
	if got.SummaryInterval != ints["Summary.IntervalRounds"] {
		t.Fatalf("interval = %d, want the marker %d", got.SummaryInterval, ints["Summary.IntervalRounds"])
	}
}

// The hash splits on a change to any key, allowlisted or not, and only on a
// change: the same config hashes the same.
func TestConfigHash_SplitsOnAnyKey(t *testing.T) {
	var a, b config.Config
	if configHash(a) != configHash(b) {
		t.Fatal("equal configs must hash equal")
	}
	b.Behavior.ScopeDirs = []string{"/somewhere"}
	if configHash(a) == configHash(b) {
		t.Fatal("a change to a key off the allowlist must change the hash")
	}
	var c config.Config
	c.Provider.APIKey = "sk-secret"
	if h := configHash(c); strings.Contains(h, "secret") || len(h) != 12 {
		t.Fatalf("hash %q", h)
	}
}

// markEveryField fills every leaf of a config struct with a value naming its
// path and returns the paths it marked; ints go in the map so the test can
// tell them apart from the run's own numbers.
func markEveryField(v reflect.Value, prefix string, ints map[string]int) []string {
	var marked []string
	for i := 0; i < v.NumField(); i++ {
		f, sf := v.Field(i), v.Type().Field(i)
		path := sf.Name
		if prefix != "" {
			path = prefix + "." + sf.Name
		}
		marked = append(marked, markValue(f, path, ints)...)
	}
	return marked
}

func markValue(f reflect.Value, path string, ints map[string]int) []string {
	marker := "marker-" + path
	switch f.Kind() {
	case reflect.Struct:
		return markEveryField(f, path, ints)
	case reflect.String:
		f.SetString(marker)
	case reflect.Bool:
		f.SetBool(true)
	case reflect.Int, reflect.Int64:
		// Distinct and far from any round cap or interval a test would
		// choose, so a leak is never a coincidence.
		n := 100000 + len(ints)*7 + 3
		ints[path] = n
		f.SetInt(int64(n))
	case reflect.Pointer:
		elem := reflect.New(f.Type().Elem())
		marked := markValue(elem.Elem(), path, ints)
		f.Set(elem)
		return append(marked, path)
	case reflect.Slice:
		elem := reflect.New(f.Type().Elem()).Elem()
		marked := markValue(elem, path, ints)
		f.Set(reflect.Append(reflect.MakeSlice(f.Type(), 0, 1), elem))
		return append(marked, path)
	case reflect.Map:
		elem := reflect.New(f.Type().Elem()).Elem()
		marked := markValue(elem, path, ints)
		m := reflect.MakeMap(f.Type())
		m.SetMapIndex(reflect.ValueOf(marker), elem)
		f.Set(m)
		return append(marked, path)
	default:
		panic(fmt.Sprintf("config field %s has a kind the walk does not mark: %s", path, f.Kind()))
	}
	return []string{path}
}
