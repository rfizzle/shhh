package profile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rfizzle/llm/capability"
	"github.com/rfizzle/llm/flow"
)

// flowsProfile is a two-model profile whose second model carries line, so a
// refusal is seen to take the whole file with it.
func flowsProfile(line string) string {
	return "name = \"gw\"\nbase_url = \"http://x/v1\"\n\n[[models]]\nid = \"fine\"\n\n[[models]]\nid = \"m\"\n" + line + "\n"
}

func refusedWith(t *testing.T, line string, want ...string) {
	t.Helper()
	got, err := LoadFile(writeProfile(t, t.TempDir(), "gw.toml", flowsProfile(line)))
	if err == nil {
		t.Fatalf("%s: loaded %+v, want a refusal", line, got)
	}
	if got != nil {
		t.Fatalf("a refused profile is refused whole, got %+v", got)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("%s: %q does not say %q", line, err, w)
		}
	}
}

func TestProfile_FlowScopeRefusesTrue(t *testing.T) {
	refusedWith(t, "flows = { classifier = { structured_outputs = true } }",
		"flows.classifier", "structured_outputs can only be false")
	refusedWith(t, "flows = { classifier = { structured_outputs = \"false\" } }", "can only be false")
}

func TestProfile_FlowScopeRefusesAnUnknownFlow(t *testing.T) {
	refusedWith(t, "flows = { extraction = { structured_outputs = false } }",
		`unknown flow "extraction"`, flow.Words())
}

func TestProfile_FlowScopeRefusesAnUnknownSetting(t *testing.T) {
	refusedWith(t, "flows = { classifier = { structured_output = false } }",
		`flows.classifier: unknown setting "structured_output"`, "structured_outputs")
}

func TestProfile_FlowScopeRefusesAFlowWithNothingToScope(t *testing.T) {
	refusedWith(t, "flows = { title = { structured_outputs = false } }",
		"`title` sends no schema; there is nothing to narrow")
}

// A scope loads at the top level and under an endpoint, and registering the
// profile registers what each line narrows; registering again withdraws it.
func TestProfile_RegisterRegistersWhatALineNarrows(t *testing.T) {
	body := `name = "gw"
base_url = "http://x/v1"

[[models]]
id = "wide"
structured_outputs = false

[[endpoint]]
api = "anthropic-messages"
base_url = "http://x/anthropic"

  [[endpoint.models]]
  id    = "scoped"
  flows = { classifier = { structured_outputs = false } }
`
	loaded, err := LoadFile(writeProfile(t, t.TempDir(), "gw.toml", body))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]capability.Declared{
		"wide":   {Model: map[string]any{"structured_outputs": false}},
		"scoped": {Flows: map[flow.Flow]map[string]any{flow.Classifier: {"structured_outputs": false}}},
	}
	if got := loaded[0].declarations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("declarations = %+v", got)
	}
	Register(loaded)
	t.Cleanup(func() { Register(nil) })
	if got := capability.DeclaredOn("scoped", flow.Classifier, "structured_outputs"); !reflect.DeepEqual(got, []string{"gw"}) {
		t.Fatalf("DeclaredOn scoped = %v", got)
	}
	if got := capability.DeclaredOn("scoped", flow.Backlog, "structured_outputs"); got != nil {
		t.Fatalf("DeclaredOn another flow = %v", got)
	}
	Register(nil)
	if got := capability.DeclaredOn("wide", flow.Backlog, "structured_outputs"); got != nil {
		t.Fatalf("a second registration withdraws the first's declarations, got %v", got)
	}
}
