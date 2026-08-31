package chat

import (
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// toolSourceModel is a session reaching four servers, one of every state, with
// a toolset of its own under them.
func toolSourceModel(t *testing.T, sources ...components.InspectorToolSource) Model {
	t.Helper()
	mcpNames := map[string]bool{"docs__search": true, "docs__lookup": true}
	return frameModel(t, 130, 40).
		WithToolDefinitions([]ToolTokens{
			{Name: "read_file"}, {Name: "edit_file"}, {Name: "search"},
			{Name: "docs__search"}, {Name: "docs__lookup"},
		}).
		WithMCP(MCP{
			Has:     func(name string) bool { return mcpNames[name] },
			Sources: sources,
		})
}

// The built-in toolset is a source like any other, and its count is the tools
// no server brought.
func TestInspectorTools_BuiltInCountsWhatNoServerBrought(t *testing.T) {
	m := toolSourceModel(t, components.InspectorToolSource{Name: "docs", State: components.ToolSourceUp, Note: "2 tools"})
	tools := m.inspectorTools()
	if tools == nil || len(tools.Sources) != 2 {
		t.Fatalf("tools block = %+v", tools)
	}
	if got := tools.Sources[0]; got.Name != "built-in" || got.Note != "3 tools" {
		t.Fatalf("built-in row = %+v, want the 3 tools no server brought", got)
	}
	if got := tools.Sources[1]; got.Name != "docs" || got.State != components.ToolSourceUp {
		t.Fatalf("server row = %+v", got)
	}
}

// A session with nothing but its own tools has no way to have lost one.
func TestInspectorTools_AbsentWithoutAServer(t *testing.T) {
	if tools := toolSourceModel(t).inspectorTools(); tools != nil {
		t.Fatalf("no server configured is no block: %+v", tools)
	}
}

// Past four rows the block counts the rest: the question is whether what was
// configured is up, and the whole listing is /mcp.
func TestInspectorTools_FoldsPastFourRows(t *testing.T) {
	m := toolSourceModel(t,
		components.InspectorToolSource{Name: "docs", State: components.ToolSourceUp, Note: "2 tools"},
		components.InspectorToolSource{Name: "tracker", State: components.ToolSourceBlocked, Note: "untrusted"},
		components.InspectorToolSource{Name: "linear", State: components.ToolSourceFailed, Note: "timeout"},
		components.InspectorToolSource{Name: "legacy", State: components.ToolSourceOff},
		components.InspectorToolSource{Name: "extra", State: components.ToolSourceUp, Note: "1 tool"},
	)
	tools := m.inspectorTools()
	if len(tools.Sources) != toolRailRows || tools.More != 2 {
		t.Fatalf("block shows %d rows and folds %d", len(tools.Sources), tools.More)
	}
	// built-in, docs and the folded extra: the heading counts what answered
	// over every source, not only the rows that fit.
	if tools.Up != 3 {
		t.Fatalf("heading counts %d up, want 3", tools.Up)
	}
}

// The fold takes the healthy rows first. A source that is up is the one the
// reader can afford not to see; a server that did not answer leaving no trace
// is the failure the block exists to prevent.
func TestInspectorTools_FoldKeepsWhatDidNotAnswer(t *testing.T) {
	m := toolSourceModel(t,
		components.InspectorToolSource{Name: "alpha", State: components.ToolSourceUp, Note: "2 tools"},
		components.InspectorToolSource{Name: "beta", State: components.ToolSourceUp, Note: "3 tools"},
		components.InspectorToolSource{Name: "gamma", State: components.ToolSourceUp, Note: "1 tool"},
		components.InspectorToolSource{Name: "zeta", State: components.ToolSourceFailed, Note: "timeout"},
	)
	var names []string
	for _, s := range m.inspectorTools().Sources {
		names = append(names, s.Name)
	}
	if !slices.Contains(names, "zeta") {
		t.Fatalf("the failed server keeps its row: %v", names)
	}
	// And the rows it kept are still in the catalog's order, so the block is
	// not a leaderboard.
	if !slices.IsSorted(names[1:]) {
		t.Fatalf("rows left the catalog's order: %v", names)
	}
}

// Without the half of the wiring that says which names are a server's, the
// built-in count would silently be every server's tools as well.
func TestInspectorTools_NoBuiltInRowWithoutTheServerNames(t *testing.T) {
	m := frameModel(t, 130, 40).
		WithToolDefinitions([]ToolTokens{{Name: "read_file"}, {Name: "docs__search"}}).
		WithMCP(MCP{Sources: []components.InspectorToolSource{
			{Name: "docs", State: components.ToolSourceUp, Note: "1 tool"},
		}})
	tools := m.inspectorTools()
	if len(tools.Sources) != 1 || tools.Sources[0].Name != "docs" {
		t.Fatalf("no count to vouch for is no row: %+v", tools.Sources)
	}
	if tools.Up != 1 {
		t.Fatalf("the heading counts %d up, want the one server", tools.Up)
	}
}

// /status says the same thing in words, so a terminal with no rail to draw the
// block in can still ask which sources are up.
func TestStatusCommand_NamesTheToolSources(t *testing.T) {
	m := toolSourceModel(t,
		components.InspectorToolSource{Name: "docs", State: components.ToolSourceUp, Note: "2 tools"},
		components.InspectorToolSource{Name: "linear", State: components.ToolSourceFailed, Note: "timeout"},
	)
	m.summarizer = agent.NewSummarizer(&readingProvider{}, agent.SummaryConfig{Model: "fast"})
	m.summary.last = &agent.SummaryVerdict{Text: "wiring the pause", State: agent.SummaryOnTarget, Round: 7}
	text, _ := m.statusCommand()
	for _, want := range []string{"wiring the pause", "Tools", "built-in — up · 3 tools", "docs — up · 2 tools", "linear — error · timeout"} {
		if !strings.Contains(text, want) {
			t.Fatalf("/status missing %q:\n%s", want, text)
		}
	}
}

// The sources are named whether or not there is a reading to name them under:
// a session whose summary is off still has servers that may not have started.
func TestStatusCommand_NamesTheSourcesWithoutAReading(t *testing.T) {
	m := toolSourceModel(t, components.InspectorToolSource{Name: "docs", State: components.ToolSourceUp, Note: "2 tools"})
	text, _ := m.statusCommand()
	if !strings.Contains(text, "docs — up · 2 tools") {
		t.Fatalf("/status missing the sources:\n%s", text)
	}
}

// A session with no server says nothing about sources at all.
func TestStatusCommand_SilentWithoutASource(t *testing.T) {
	m := toolSourceModel(t)
	text, _ := m.statusCommand()
	if strings.Contains(text, "Tools") {
		t.Fatalf("/status invents no source block:\n%s", text)
	}
}
