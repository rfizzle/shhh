package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/chat"
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

// stagedFlowScreen is `/config` opened from env, the way a session opens it,
// over a user file holding fileText.
// reopenedScreen opens the screen again over the file already pointed at.
func reopenedScreen(t *testing.T, env *sessionEnv) chat.ConfigSession {
	t.Helper()
	session, err := configSessionOpener(env)([]string{"model-a", "model-b", "reader-model"})
	must(t, err)
	return session
}

func stagedFlowScreen(t *testing.T, fileText string, env *sessionEnv) (path string, session chat.ConfigSession) {
	t.Helper()
	path = pointConfigAt(t, fileText)
	session, err := configSessionOpener(env)([]string{"model-a", "model-b", "reader-model"})
	must(t, err)
	return path, session
}

// A flow's row stages like every other row: the header counts it, the source
// column reads `<link> · unwritten`, and the write chord puts it in the file
// the header names, receipt and all. The sentence that the session keeps the
// settings it started on is not said of a write that only wrote flow keys,
// because the session took them as they were staged.
func TestConfigScreen_AFlowStagesLikeEveryOtherRow(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	path, session := stagedFlowScreen(t, "", env)
	screen := session.Screen

	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{
		Key: "summary.model", Value: "reader-model",
	}})
	if screen.Changed != 1 {
		t.Fatalf("the header counts %d changes after a flow was staged, want 1", screen.Changed)
	}
	row := flowRow(t, screen.Rows, "reading")
	if row.Value != "reader-model" || !strings.HasSuffix(row.Source, " · unwritten") {
		t.Errorf("the staged row reads %q · %q, want the model and `<link> · unwritten`", row.Value, row.Source)
	}
	if got, err := os.ReadFile(path); err == nil && strings.Contains(string(got), "reader-model") {
		t.Fatalf("staging reached the file:\n%s", got)
	}

	// The write chord pressed inside the picker arrives with the choice it took.
	note := session.Answer(false, components.ConfigResult{Write: true})
	got, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(got), `model = "reader-model"`) {
		t.Fatalf("the write did not land in %s: %v\n%s", path, err, got)
	}
	if !strings.HasPrefix(note, "wrote 1 change to "+screen.Path+" · summary.model") {
		t.Errorf("the receipt reads %q", note)
	}
	if strings.Contains(note, "keeps the settings it started on") {
		t.Errorf("a write of a flow key alone says the session did not take it: %q", note)
	}
	if screen.Changed != 0 {
		t.Errorf("%d changes still standing after the write", screen.Changed)
	}
	if row := flowRow(t, screen.Rows, "reading"); row.Value != "reader-model" || strings.Contains(row.Source, "unwritten") {
		t.Errorf("the written row reads %q · %q", row.Value, row.Source)
	}
	if !env.flows.holds("summary.model") {
		t.Error("the session let go of the model once it was written")
	}

	// A write that carries a key the session cannot take live keeps the sentence.
	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "summary.model", Value: "model-a"}})
	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "behavior.command_timeout_seconds", Value: "90"}})
	if note := session.Answer(false, components.ConfigResult{Write: true}); !strings.HasSuffix(note,
		"This session keeps the settings it started on; the next one starts on these.") {
		t.Errorf("a mixed write lost the sentence: %q", note)
	}
}

// A staged flow model is taken by the session as it is staged: the flow's
// reader asks the new model at its next call, the record is told and stamped
// with it, and nothing reaches a file until the write.
func TestConfigScreen_AFlowTakenForTheSessionReachesItsReader(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	moved := 0
	env.flowsMoved = func() { moved++ }
	classifier := env.flowModelAt(config.Config{}, flowClassifier)
	before := classifier()

	path, session := stagedFlowScreen(t, "", env)
	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{
		Key: "behavior.classifier_model", Value: "held-model",
	}})
	if got := classifier(); got != "held-model" || before == got {
		t.Fatalf("the classifier's reader asks %q after the staging (was %q)", got, before)
	}
	if moved != 1 {
		t.Errorf("the record was told %d times", moved)
	}
	if got, err := os.ReadFile(path); err == nil && strings.Contains(string(got), "held-model") {
		t.Fatalf("staging reached the file:\n%s", got)
	}
	in := env.flows.over(config.Config{})
	stamp := sessionSettings(in, runSettings{summary: true, classifier: true, model: auxiliaryModel(in, env.provName, env.modelName)})
	if stamp.ClassifierModel != "held-model" {
		t.Errorf("the record would stamp the classifier as %q", stamp.ClassifierModel)
	}
}

// Leaving the screen does not give a flow's model back: the session keeps it
// as it was staged, the screen opened again says so, and the row's reset is
// what puts the reader back on the file's.
func TestConfigScreen_AResetFlowGoesBackToTheFile(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	moved := 0
	env.flowsMoved = func() { moved++ }
	path, first := stagedFlowScreen(t, "[summary]\nmodel = \"file-model\"\n", env)
	cfg := config.Config{}
	cfg.Summary.Model = "file-model"
	reader := env.flowModelAt(cfg, flowReading)

	first.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "summary.model", Value: "reader-model"}})
	first.Answer(true, components.ConfigResult{Canceled: true})
	if got := reader(); got != "reader-model" {
		t.Fatalf("the reader asks %q after leaving, want the staged one", got)
	}

	again := reopenedScreen(t, env)
	if row := flowRow(t, again.Screen.Rows, "reading"); row.Source != "session" {
		t.Errorf("the flow's row reads %q on the screen opened again, want `session`", row.Source)
	}
	again.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "summary.model", Reset: true}})
	if got := reader(); got != "file-model" {
		t.Errorf("the reader asks %q after the reset, want the file's", got)
	}
	if moved != 2 {
		t.Errorf("the record was told %d times, want once for the take and once for the reset", moved)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "file-model") {
		t.Errorf("the reset touched the file:\n%s", got)
	}
}
