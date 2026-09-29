package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// flowRow is the flows section's row for one flow.
func flowRow(t *testing.T, rows []components.ConfigRow, name string) components.ConfigRow {
	t.Helper()
	for _, row := range rows {
		if row.Group == "FLOWS" && row.Label == name {
			return row
		}
	}
	t.Fatalf("no flows row for %q", name)
	return components.ConfigRow{}
}

// flowScreen is the screen over cfg with the chain falling back on a
// provider that names a small model, and env as the session it was opened
// from (nil for `shhh config`).
func flowScreen(cfg config.Config, env *sessionEnv) *configModel {
	m := newConfigModel(cfg, config.Project{})
	m.flows = configFlows{provName: "anthropic", model: "session-model", env: env}
	m.refresh()
	return m
}

// Every row of the flows section states the model the flow will run on and
// which link of the chain gave it, read from the chain the calls are sent
// with — and a model this session holds says so instead.
func TestConfigScreen_FlowsSayWhichStepAnswered(t *testing.T) {
	small := provider.Defaults("anthropic").CheapModel
	if small == "" {
		t.Fatal("the fixture needs a provider that names a small model")
	}
	cfg := config.Config{}
	cfg.Behavior.ClassifierModel = "judge-model"
	rows := flowScreen(cfg, nil).screen.Rows
	for _, c := range []struct {
		flow, model, source, detail string
	}{
		{"classifier", "judge-model", "flow key", "behavior.classifier_model"},
		// The explanation reads the classifier's key behind its own.
		{"explanation", "judge-model", "flow key", "behavior.classifier_model"},
		{"reading", small, "provider small model", ""},
		// The compaction skips the small model: nothing vouches for its window.
		{"compaction", "session-model", "session model", ""},
	} {
		row := flowRow(t, rows, c.flow)
		if row.Value != c.model || row.Source != c.source || row.Detail != c.detail {
			t.Errorf("%s reads %q · %q · %q, want %q · %q · %q", c.flow,
				row.Value, row.Source, row.Detail, c.model, c.source, c.detail)
		}
	}

	cfg.Provider.CheapModel = "cheap-one"
	row := flowRow(t, flowScreen(cfg, nil).screen.Rows, "title")
	if row.Value != "cheap-one" || row.Source != "cheap key" || row.Detail != "provider.cheap_model" {
		t.Errorf("the title reads %q · %q · %q, want the cheap key's answer", row.Value, row.Source, row.Detail)
	}

	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	env.flows.set("behavior.classifier_model", "held-model")
	rows = flowScreen(cfg, env).screen.Rows
	for _, name := range []string{"classifier", "explanation"} {
		if row := flowRow(t, rows, name); row.Value != "held-model" || row.Source != "session" {
			t.Errorf("%s reads %q · %q, want the session's own model", name, row.Value, row.Source)
		}
	}
	if row := flowRow(t, rows, "reading"); row.Source != "cheap key" {
		t.Errorf("a flow the session holds nothing for says %q", row.Source)
	}
}

// Inside a session a flow's picker offers the three destinations, and a flow
// no session sends offers only the files; `shhh config` offers none, since
// there is no session and its own staging reaches both files.
func TestConfigScreen_AFlowsPickerOffersWhereTheModelCanGo(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	m := flowScreen(config.Config{}, env)
	if got := flowRow(t, m.screen.Rows, "classifier").Takes; !slices.Equal(got,
		[]components.ConfigTake{components.TakeSession, components.TakeMine}) {
		t.Errorf("the classifier offers %v outside a checkout", got)
	}
	if got := flowRow(t, m.screen.Rows, "description").Takes; !slices.Equal(got,
		[]components.ConfigTake{components.TakeMine}) {
		t.Errorf("the one-shot's description offers %v in a session that never sends it", got)
	}
	m.dir = t.TempDir()
	m.refresh()
	if got := flowRow(t, m.screen.Rows, "reading").Takes; !slices.Contains(got, components.TakeCheckout) {
		t.Errorf("in a checkout the reading offers %v", got)
	}
	if got := flowRow(t, flowScreen(config.Config{}, nil).screen.Rows, "classifier").Takes; got != nil {
		t.Errorf("`shhh config` offered %v", got)
	}
}

// A model taken for this session is written to no file, reaches the flow's
// reader at its next call, and is what the record is stamped with.
func TestConfigScreen_AFlowTakenForTheSessionReachesItsReader(t *testing.T) {
	userPath := pointConfigAt(t, "")
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	moved := 0
	env.flowsMoved = func() { moved++ }
	cfg := config.Config{}
	classifier := env.flowModelAt(cfg, flowClassifier)
	before := classifier()

	m := flowScreen(cfg, env)
	m.apply(components.ConfigChange{Key: "behavior.classifier_model", Value: "held-model", Take: components.TakeSession})
	if got := classifier(); got != "held-model" || before == got {
		t.Fatalf("the classifier's reader asks %q after the take (was %q)", got, before)
	}
	if moved != 1 {
		t.Errorf("the record was told %d times", moved)
	}
	if row := flowRow(t, m.screen.Rows, "classifier"); row.Source != "session" {
		t.Errorf("the row reads %q after the take", row.Source)
	}
	if m.screen.Changed != 0 {
		t.Errorf("a session take staged %d edits against the file", m.screen.Changed)
	}
	if got, err := os.ReadFile(userPath); err == nil && strings.Contains(string(got), "held-model") {
		t.Fatalf("a session take reached the file:\n%s", got)
	}
	in := env.flows.over(cfg)
	stamp := sessionSettings(in, runSettings{summary: true, classifier: true, model: auxiliaryModel(in, env.provName, env.modelName)})
	if stamp.ClassifierModel != "held-model" {
		t.Errorf("the record would stamp the classifier as %q", stamp.ClassifierModel)
	}
}

// My settings writes the one key to the person's file at once, and the
// session takes it too; the row then reads the file's answer rather than
// `session`, because the file holds it.
func TestConfigScreen_AFlowWrittenToMySettingsLandsAndTheSessionTakesIt(t *testing.T) {
	userPath := pointConfigAt(t, "")
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	m := flowScreen(config.Config{}, env)
	m.apply(components.ConfigChange{Key: "summary.model", Value: "reader-model", Take: components.TakeMine})
	got, err := os.ReadFile(userPath)
	if err != nil || !strings.Contains(string(got), `model = "reader-model"`) {
		t.Fatalf("my settings did not land in %s: %v\n%s", userPath, err, got)
	}
	if held := resolveFlow(env.flows.over(config.Config{}), flowReading, env.provName, env.modelName).model; held != "reader-model" {
		t.Errorf("the session reads on %q after the write", held)
	}
	row := flowRow(t, m.screen.Rows, "reading")
	if row.Value != "reader-model" || row.Source != "flow key" {
		t.Errorf("the row reads %q · %q after the write", row.Value, row.Source)
	}
	if m.screen.Changed != 0 {
		t.Errorf("the written key is still counted as an edit: %d", m.screen.Changed)
	}
}

// This checkout writes the checkout's own file where it is trusted, and is
// refused with the writer's own sentence where it is not.
func TestConfigScreen_AFlowForThisCheckoutNeedsATrustedCheckout(t *testing.T) {
	_, checkout := scopeFixture(t, scopeCases[0])
	path := filepath.Join(checkout, filepath.FromSlash(project.ConfigFile))
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	take := func() *components.ConfigScreen {
		session, err := configSessionOpener(env)([]string{"model-a", "model-b"})
		must(t, err)
		session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{
			Key: "todo.model", Value: "model-b", Take: components.TakeCheckout,
		}})
		return session.Screen
	}

	trusting(t, checkout, false)
	if screen := take(); screen.Notice != projectTrustNote() || exists(path) {
		t.Fatalf("an untrusted checkout was written, or refused in other words: %q", screen.Notice)
	}
	if env.flows.holds("todo.model") {
		t.Error("a refused take moved the session")
	}

	trusting(t, checkout, true)
	screen := take()
	if got, err := os.ReadFile(path); err != nil || !strings.Contains(string(got), `model = "model-b"`) {
		t.Fatalf("this checkout did not land in %s: %v %s", path, err, got)
	}
	if row := flowRow(t, screen.Rows, "backlog"); row.Value != "model-b" || len(row.Options) != 2 {
		t.Errorf("the backlog row reads %q with %d options", row.Value, len(row.Options))
	}
}
