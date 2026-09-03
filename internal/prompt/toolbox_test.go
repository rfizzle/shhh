package prompt

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
)

func testShell() shell.Info {
	return shell.Info{Shell: "bash", OS: "linux", Cwd: "/tmp"}
}

func toolList(names ...string) []provider.Tool {
	out := make([]provider.Tool, len(names))
	for i, n := range names {
		out[i] = provider.Tool{Name: n}
	}
	return out
}

func TestToolbox_NamesOnlyWhatIsRegistered(t *testing.T) {
	got := Toolbox(toolList("read_file", "search", "definition", "references", "fd"))

	for _, want := range []string{"definition", "references", "fd"} {
		if !strings.Contains(got, "- "+want+" — ") {
			t.Errorf("expected %q to be described, got:\n%s", want, got)
		}
	}
	for _, absent := range []string{"ast_grep", "web_search", "spawn_agent", "jaq"} {
		if strings.Contains(got, absent) {
			t.Errorf("described %q, which this session does not have:\n%s", absent, got)
		}
	}
}

func TestToolbox_EmptyWithoutOptionalTools(t *testing.T) {
	// The base toolset is described by BuildAgent itself; a session with
	// nothing else has nothing to add.
	if got := Toolbox(toolList("read_file", "list_directory", "glob", "search", "execute_command", "write_file", "edit_file")); got != "" {
		t.Errorf("expected no toolbox section, got:\n%s", got)
	}
	if got := Toolbox(nil); got != "" {
		t.Errorf("expected no toolbox section for no tools, got:\n%s", got)
	}
}

func TestToolbox_StableOrder(t *testing.T) {
	// Navigation leads: it is where a session wastes the most rounds.
	got := Toolbox(toolList("remember", "fd", "definition"))
	def, fd, rem := strings.Index(got, "- definition"), strings.Index(got, "- fd"), strings.Index(got, "- remember")
	if def >= fd || fd >= rem {
		t.Errorf("expected definition < fd < remember, got:\n%s", got)
	}
}

func TestBuildAgent_CarriesTheToolboxAsExtra(t *testing.T) {
	box := Toolbox(toolList("references"))
	if box == "" {
		t.Fatal("expected a toolbox section")
	}
	if !strings.Contains(BuildAgent(testShell(), box), "- references — ") {
		t.Error("the toolbox should reach the agent prompt through extra")
	}
}

func TestBuildAgent_TellsItNotToRepeatCalls(t *testing.T) {
	got := BuildAgent(testShell())
	for _, want := range []string{"Never repeat a call", "Batch independent calls", "files_only", "Know when to stop looking"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected the agent prompt to say %q", want)
		}
	}
}

// The rules are load-bearing and were four near-copies that had drifted. Every
// prompt that investigates has to carry them, and from the one source.
func TestFindingThings_ReachesEveryInvestigatingPrompt(t *testing.T) {
	profile := BuildProfile(testShell(), ProfileSpec{
		Name:  "auditor",
		Tools: []string{"read_file", "search"},
	})
	for _, tc := range []struct {
		name, prompt, rules string
	}{
		{"agent", BuildAgent(testShell()), findingThings},
		{"researcher", BuildResearcher(testShell()), findingThingsBrief},
		{"writer", BuildWriter(testShell()), findingThingsBrief},
		{"profile", profile, findingThingsBrief},
	} {
		if !strings.Contains(tc.prompt, tc.rules) {
			t.Errorf("%s prompt does not carry the shared investigation rules", tc.name)
		}
	}
}

// Both forms have to say the three things the harness cannot say for them.
func TestFindingThings_KeepsTheThreeRules(t *testing.T) {
	for name, rules := range map[string]string{"full": findingThings, "brief": findingThingsBrief} {
		for _, want := range []string{"Batch independent", "Never repeat a call", "stop looking"} {
			if !strings.Contains(rules, want) {
				t.Errorf("%s rules dropped %q", name, want)
			}
		}
	}
}

// The two find-and-replace tools have to be told apart. sd never writes a
// file — it previews a replacement across the tree — and the edit tool now
// changes several places in one file in a single call, so a note that still
// described that as a call per site would send a one-file rename to the
// wrong tool.
func TestToolbox_SplitsSdFromABatchedEdit(t *testing.T) {
	got := Toolbox(toolList("sd"))
	for _, want := range []string{"spans files", "edits array"} {
		if !strings.Contains(got, want) {
			t.Errorf("the sd note should name the split, missing %q:\n%s", want, got)
		}
	}
}
