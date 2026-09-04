package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/provider"
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

// promptModel is a session reaching one server that publishes two prompts
// and a resource: two commands and one more tool.
func promptModel(t *testing.T) Model {
	t.Helper()
	prompts := []mcp.Prompt{
		{Name: "docs:brief", Server: "docs", Description: "Two voices."},
		{Name: "docs:review", Server: "docs", Description: "Review a change.", Arguments: []mcp.PromptArgument{
			{Name: "ref", Description: "What to review.", Required: true},
			{Name: "depth", Description: "How closely."},
		}},
	}
	names := map[string]bool{"docs__search": true, "mcp_resource": true}
	return frameModel(t, 130, 40).
		WithToolDefinitions([]ToolTokens{{Name: "read_file"}, {Name: "docs__search"}, {Name: "mcp_resource"}}).
		WithMCP(MCP{
			Has:      func(name string) bool { return names[name] },
			ReadOnly: func(name string) bool { return name == "mcp_resource" },
			Prompts:  func() []mcp.Prompt { return prompts },
			Render: func(_ context.Context, name string, args map[string]string) (string, error) {
				if name != "docs:review" {
					return "what changed?", nil
				}
				return "Review " + args["ref"] + ".", nil
			},
		})
}

// A server's prompts are commands of the session: the menu offers them, and
// after the registry's own rows, because what the session promises to answer
// outranks what a server happens to publish today.
func TestCompletion_OffersAServersPromptsAfterTheRegistry(t *testing.T) {
	m := promptModel(t)
	m.input.SetValue("/docs:")
	m.syncCompletions()
	var names []string
	for _, c := range m.complete.items {
		names = append(names, c.name)
	}
	if strings.Join(names, " ") != "/docs:brief /docs:review" {
		t.Fatalf("menu = %v", names)
	}

	m.input.SetValue("/d")
	m.syncCompletions()
	names = nil
	for _, c := range m.complete.items {
		names = append(names, c.name)
	}
	if len(names) < 3 || names[0] != "/diff" {
		t.Fatalf("a server's prompt outranked a command: %v", names)
	}
	if !containsString(names, "/docs:review") {
		t.Fatalf("the prompt is not offered at all: %v", names)
	}
}

// The arguments a prompt declares complete from the menu the way every other
// command's do. The protocol gives them no order, so every position offers
// the same keys.
func TestCompletion_OffersAPromptsArgumentsAsKeys(t *testing.T) {
	m := promptModel(t)
	m.input.SetValue("/docs:review ")
	m.syncCompletions()
	var got []string
	for _, c := range m.complete.items {
		got = append(got, c.name+"|"+c.desc)
	}
	if strings.Join(got, " ") != "ref=|required · What to review. depth=|How closely." {
		t.Fatalf("argument menu = %v", got)
	}

	m.input.SetValue("/docs:review ref=HEAD ")
	m.syncCompletions()
	got = nil
	for _, c := range m.complete.items {
		got = append(got, c.name)
	}
	if strings.Join(got, " ") != "ref= depth=" {
		t.Fatalf("second position = %v", got)
	}
}

// A prompt whose arguments are wrong is refused here rather than sent: the
// server would refuse it a round later, in its own words.
func TestMCPPromptValues_RefuseWhatTheServerWould(t *testing.T) {
	p := promptModel(t).mcpPrompts()[1]
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"HEAD"}, "name=value"},
		{[]string{"branch=main"}, "no argument called branch"},
		{nil, "needs ref"},
	} {
		if _, err := mcpPromptValues(p, c.words); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v = %v, want %q", c.words, err, c.want)
		}
	}
	args, err := mcpPromptValues(p, []string{"ref=HEAD", "depth=quick"})
	if err != nil || args["ref"] != "HEAD" || args["depth"] != "quick" {
		t.Errorf("args = %v, %v", args, err)
	}
}

// Typing a prompt renders it at the server and starts a turn on what comes
// back. The transcript shows the command, because that is what the reader
// typed; the model gets the server's text.
func TestRunMCPPrompt_StartsATurnOnTheRenderedText(t *testing.T) {
	m := promptModel(t)
	m.input.SetValue("/docs:review ref=HEAD")
	next, cmd := m.submitInput()
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the prompt was not sent to the server")
	}
	msg, ok := cmd().(mcpPromptMsg)
	if !ok || msg.err != nil || msg.text != "Review HEAD." || msg.shown != "/docs:review ref=HEAD" {
		t.Fatalf("rendered = %+v", msg)
	}
	next, _ = m.applyMCPPrompt(msg)
	m = next.(Model)
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryUser || last.text != "/docs:review ref=HEAD" {
		t.Fatalf("transcript row = %+v", last)
	}
	if got := m.agent.Messages(); got[len(got)-1].Content != "Review HEAD." {
		t.Fatalf("the model was sent %q", got[len(got)-1].Content)
	}
}

// A prompt that lands while the agent is working joins the next round as
// steering, the way an activated skill's text does: the alternative is a
// turn started on top of the running one.
func TestApplyMCPPrompt_QueuesWhileTheAgentWorks(t *testing.T) {
	m := promptModel(t)
	m.setTurnState(stateStreaming)
	next, _ := m.applyMCPPrompt(mcpPromptMsg{shown: "/docs:brief", text: "what changed?"})
	m = next.(Model)
	if len(m.steering) != 1 || m.steering[0] != "what changed?" {
		t.Fatalf("steering = %v", m.steering)
	}
}

// A server that could not render says so where the reader is, and starts no
// turn on nothing.
func TestApplyMCPPrompt_ReportsAFailureAndSendsNothing(t *testing.T) {
	m := promptModel(t)
	turns := m.turnCount
	next, _ := m.applyMCPPrompt(mcpPromptMsg{shown: "/docs:brief", err: errors.New("server docs: closed")})
	m = next.(Model)
	if m.turnCount != turns {
		t.Fatal("a failed render started a turn")
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "server docs: closed") {
		t.Fatalf("transcript row = %+v", last)
	}
}

// A resource read auto-runs the way read_file does: no card, in every mode.
// It draws as a read too, rather than with the outbound-call rail every
// other server tool carries.
func TestResourceToolAutoRunsAndDrawsAsARead(t *testing.T) {
	m := promptModel(t)
	m.policy.mode = agent.ModeManual
	call := provider.ToolCall{Name: "mcp_resource", Arguments: `{"uri":"docs://guide"}`}
	if m.requiresApproval(call) {
		t.Fatal("a resource read asked for approval")
	}
	if got := m.activityKind("mcp_resource"); got != components.ActivityTool {
		t.Fatalf("resource row kind = %v, want the read's", got)
	}
	if got := m.activityKind("docs__search"); got != components.ActivityRemote {
		t.Fatalf("a server call's row kind = %v", got)
	}
}
