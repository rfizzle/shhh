package resolve

// The provider search's own tests (S-106). Each place is exercised in both
// states, because the card's whole value is the difference between "looked and
// found nothing" and "looked and found something that did not work".

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearKeyEnv unexports every variable the walk reads, so a machine that has
// one exported does not change what the test sees.
func clearKeyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"SHHH_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY",
		"GEMINI_API_KEY", "OPENROUTER_API_KEY",
	} {
		t.Setenv(name, "")
	}
}

// placeOf finds one place in a survey.
func placeOf(t *testing.T, s Survey, kind PlaceKind) Place {
	t.Helper()
	for _, p := range s.Places {
		if p.Kind == kind {
			return p
		}
	}
	t.Fatalf("the survey never looked in %q", kind)
	return Place{}
}

// deadEnd is a port nothing is listening on, so the local probe reports what
// it reports on an ordinary machine without a runtime.
const deadEnd = "http://127.0.0.1:1/v1"

func TestSurvey_LooksInEveryPlace(t *testing.T) {
	clearKeyEnv(t)
	s := SurveyPlaces(context.Background(), SurveyOpts{
		Provider:     "openai",
		ConfigPaths:  []string{filepath.Join(t.TempDir(), "config.toml")},
		LocalBaseURL: deadEnd,
	})
	kinds := make([]string, 0, len(s.Places))
	for _, p := range s.Places {
		kinds = append(kinds, string(p.Kind))
	}
	if got := strings.Join(kinds, ","); got != "env,config,profiles,local" {
		t.Errorf("places = %q, want the four in search order", got)
	}
	for _, p := range s.Places {
		if p.Found {
			t.Errorf("%q should have found nothing, got %q %q", p.Kind, p.Finding, p.Detail)
		}
		if p.Finding == "" && p.Detail == "" {
			t.Errorf("%q reported neither a finding nor a reason", p.Kind)
		}
	}
	if s.Likely != "" {
		t.Errorf("nothing was found, so there is nothing to point at, got %q", s.Likely)
	}
}

func TestSurvey_NamesTheVariablesItRead(t *testing.T) {
	clearKeyEnv(t)
	s := SurveyPlaces(context.Background(), SurveyOpts{
		Provider: "anthropic", ConfigPaths: []string{filepath.Join(t.TempDir(), "c.toml")},
		LocalBaseURL: deadEnd,
	})
	env := placeOf(t, s, PlaceEnv)
	for _, want := range []string{"SHHH_API_KEY", "ANTHROPIC_API_KEY", "unset"} {
		if !strings.Contains(env.Detail, want) {
			t.Errorf("the env row should say %q, got %q", want, env.Detail)
		}
	}
	if strings.Contains(env.Detail, "OPENAI_API_KEY") {
		t.Errorf("the row should name this provider's variables, got %q", env.Detail)
	}
}

func TestSurvey_ReportsAnExportedKeyByItsTail(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-live-abcd4f9c")
	s := SurveyPlaces(context.Background(), SurveyOpts{
		Provider: "openai", ConfigPaths: []string{filepath.Join(t.TempDir(), "c.toml")},
		LocalBaseURL: deadEnd,
	})
	env := placeOf(t, s, PlaceEnv)
	if !env.Found {
		t.Fatalf("an exported key is something found, got %+v", env)
	}
	if !strings.Contains(env.Finding, "···4f9c") {
		t.Errorf("the row should name the key by its tail, got %q", env.Finding)
	}
	if strings.Contains(env.Finding, "sk-live") {
		t.Errorf("the row must never carry the key, got %q", env.Finding)
	}
	if !strings.Contains(env.Detail, "SHHH_API_KEY") {
		t.Errorf("the variables that were still unset should be named, got %q", env.Detail)
	}
	if !strings.Contains(s.Likely, "check it is the right provider's key") {
		t.Errorf("with a key exported, that is the likely fix, got %q", s.Likely)
	}
}

func TestSurvey_ConfigSaysWhichFileItRead(t *testing.T) {
	clearKeyEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[behavior]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	present := placeOf(t, SurveyPlaces(context.Background(), SurveyOpts{
		ConfigPaths: []string{path}, LocalBaseURL: deadEnd,
	}), PlaceConfig)
	if !strings.Contains(present.Detail, "no provider api_key") {
		t.Errorf("a config file with no key should say so, got %q", present.Detail)
	}

	absent := placeOf(t, SurveyPlaces(context.Background(), SurveyOpts{
		ConfigPaths: []string{filepath.Join(dir, "nope.toml")}, LocalBaseURL: deadEnd,
	}), PlaceConfig)
	if !strings.Contains(absent.Detail, "no such file") {
		t.Errorf("an absent config should say so, got %q", absent.Detail)
	}

	keyed := placeOf(t, SurveyPlaces(context.Background(), SurveyOpts{
		ConfigAPIKey: "sk-config-9876", ConfigPaths: []string{path}, LocalBaseURL: deadEnd,
	}), PlaceConfig)
	if !keyed.Found || !strings.Contains(keyed.Finding, "···9876") {
		t.Errorf("a configured key should be found and named by its tail, got %+v", keyed)
	}
}

func TestSurvey_FindsALocalRuntime(t *testing.T) {
	clearKeyEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("the probe should read the catalog, got %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"llama3.3"},{"id":"qwen2.5-coder"}]}`))
	}))
	defer srv.Close()

	s := SurveyPlaces(context.Background(), SurveyOpts{
		ConfigPaths:  []string{filepath.Join(t.TempDir(), "c.toml")},
		LocalBaseURL: srv.URL + "/v1",
		HTTPClient:   srv.Client(),
	})
	local := placeOf(t, s, PlaceLocal)
	if !local.Found {
		t.Fatalf("an answering endpoint is something found, got %+v", local)
	}
	if !strings.Contains(local.Detail, "llama3.3") {
		t.Errorf("the row should name what the endpoint serves, got %q", local.Detail)
	}
	if s.LocalBaseURL == "" || s.LocalModel != "llama3.3" {
		t.Errorf("the offer needs the endpoint and a model, got %q %q", s.LocalBaseURL, s.LocalModel)
	}
	if !strings.Contains(s.Likely, "local runtime") {
		t.Errorf("a local runtime that answers is the quickest way in, got %q", s.Likely)
	}
}

func TestSurvey_LocalProbeIgnoresSomethingElseOnThePort(t *testing.T) {
	clearKeyEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>a web server, not a model runtime</html>"))
	}))
	defer srv.Close()

	local := placeOf(t, SurveyPlaces(context.Background(), SurveyOpts{
		ConfigPaths:  []string{filepath.Join(t.TempDir(), "c.toml")},
		LocalBaseURL: srv.URL + "/v1",
		HTTPClient:   srv.Client(),
	}), PlaceLocal)
	if local.Found {
		t.Errorf("a body that is not a catalog is not a model runtime, got %+v", local)
	}
}

func TestKeyVars_PerProvider(t *testing.T) {
	for _, tc := range []struct{ provider, want string }{
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
	} {
		vars := keyVars(tc.provider)
		if len(vars) != 2 || vars[0] != "SHHH_API_KEY" || vars[1] != tc.want {
			t.Errorf("keyVars(%q) = %v, want SHHH_API_KEY then %s", tc.provider, vars, tc.want)
		}
	}
	// A gateway profile's provider has no built-in variable of its own; the
	// row says what was read rather than inventing a name.
	if got := keyVars("litellm"); len(got) != 1 {
		t.Errorf("keyVars for an unknown provider = %v, want just the shared variable", got)
	}
}

func TestHostOf(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://localhost:11434/v1", "localhost:11434"},
		{"https://api.openai.com/v1", "api.openai.com"},
		{"localhost:11434", "localhost:11434"},
	} {
		if got := hostOf(tc.url); got != tc.want {
			t.Errorf("hostOf(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
