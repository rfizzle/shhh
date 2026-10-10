package cli

import (
	"os"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// The picker lists the flows a session asks, in the settings screen's order,
// and leaves out the two it cannot move.
func TestModelFlows_ListsTheFlowsASessionAsks(t *testing.T) {
	pointConfigAt(t, "")
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	var names []string
	for _, f := range modelFlowTargets(env) {
		names = append(names, f.Name)
	}
	want := []string{"classifier", "explanation", "reading", "title", "account", "suggestion",
		"start offers", "patterns", "backlog", "profile drafter"}
	if len(names) != len(want) {
		t.Fatalf("flows %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("flows %v, want %v", names, want)
		}
	}
}

// A flow chosen in the picker is held the way the settings screen's flow row
// holds one: the flow reads it, /config shows it as `session`, the header
// counts it as unwritten, and no file is written.
func TestModelFlows_AHeldChoiceReadsSessionOnConfig(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	path := pointConfigAt(t, "")
	var told int
	env.flowsMoved = func() { told++ }

	holdFlowModel(env, "behavior.classifier_model", "reader-model")
	if told != 1 {
		t.Errorf("the record was told %d times", told)
	}
	for _, f := range modelFlowTargets(env) {
		if f.Name == "classifier" && f.Model != "reader-model" {
			t.Errorf("the picker reads the classifier on %q after holding it", f.Model)
		}
	}
	session := reopenedScreen(t, env)
	row := flowRow(t, session.Screen.Rows, "classifier")
	if row.Value != "reader-model" || row.Source != "session" {
		t.Errorf("/config reads the classifier %q from %q, want reader-model from `session`", row.Value, row.Source)
	}
	if session.Screen.Changed != 1 || session.Screen.Held != 1 {
		t.Errorf("the header counts %d changes, %d held, want 1 and 1", session.Screen.Changed, session.Screen.Held)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("holding a flow wrote %s", path)
	}
}

// The row's reset gives a picker's choice back to the file.
func TestModelFlows_ResetPutsAHeldChoiceBack(t *testing.T) {
	env := &sessionEnv{provName: "anthropic", modelName: "session-model"}
	pointConfigAt(t, "")
	holdFlowModel(env, "behavior.classifier_model", "reader-model")
	session := reopenedScreen(t, env)
	session.Answer(false, components.ConfigResult{Change: &components.ConfigChange{Key: "behavior.classifier_model", Reset: true}})
	if env.flows.holds("behavior.classifier_model") {
		t.Error("the reset left the session holding the model")
	}
	if row := flowRow(t, session.Screen.Rows, "classifier"); row.Source == "session" || session.Screen.Changed != 0 {
		t.Errorf("the classifier still reads %q with %d changes", row.Source, session.Screen.Changed)
	}
}
