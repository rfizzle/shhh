package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// contextModel is a session with a toolset, a couple of tool calls and their
// results — enough for both folds to have something to itemise.
func contextModel(t *testing.T, width int) Model {
	t.Helper()
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("system prompt. ", 40)},
		{Role: provider.RoleUser, Content: "find the round limit"},
		{Role: provider.RoleAssistant, Content: "Searching.", ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "search", Arguments: `{"query":"ErrRoundLimit"}`},
			{ID: "b", Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`},
		}},
		{Role: provider.RoleTool, ToolCallID: "a", Content: strings.Repeat("hit. ", 200)},
		{Role: provider.RoleTool, ToolCallID: "b", Content: strings.Repeat("line. ", 40)},
	}, mockStream).WithToolDefinitions([]ToolTokens{
		{Name: "execute_command", Tokens: 4100},
		{Name: "edit_file", Tokens: 3200},
		{Name: "search", Tokens: 2600},
		{Name: "read_file", Tokens: 1400},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return updated.(Model)
}

// TestContext_OpensAsATakeover checks /context borrows the screen rather than
// leaving a block in the transcript, and that esc gives it back.
func TestContext_OpensAsATakeover(t *testing.T) {
	m := sendText(t, contextModel(t, 110), "/context")
	if m.state != stateContext || m.context == nil {
		t.Fatalf("/context did not open the surface, state=%d screen=%v", m.state, m.context)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state == stateContext || m.context != nil {
		t.Fatal("esc did not give the screen back")
	}
}

// TestContext_QuotesTheSameTotalAsTheRails is the rule every occupancy
// surface shares: one accounting, so the rail, /stats and this cannot report
// three different numbers.
func TestContext_QuotesTheSameTotalAsTheRails(t *testing.T) {
	m := contextModel(t, 110)
	screen := m.contextScreenData()
	if want := "~" + formatTokenCount(m.estimatedContextTokens()); screen.Tokens != want {
		t.Errorf("the surface reports %q, the accounting %q", screen.Tokens, want)
	}
	if want := percentOf(m.estimatedContextTokens(), m.contextWindow()); screen.Pct != want {
		t.Errorf("the surface draws %d%%, the accounting %d%%", screen.Pct, want)
	}
}

// TestContext_ReportsTheLastRequestsCacheReads: the same occupancy costs
// wildly different money depending on whether the provider's prefix still
// matched, and this is the surface a reader comes to with the window on
// their mind.
func TestContext_ReportsTheLastRequestsCacheReads(t *testing.T) {
	m := contextModel(t, 110)
	if screen := m.contextScreenData(); screen.CacheRead != "" {
		t.Errorf("a session with no request reported a cache reading of %q", screen.CacheRead)
	}

	m.vitals.record("claude", provider.Usage{PromptTokens: 1000, CompletionTokens: 10}, 0, false)
	m.vitals.record("claude", provider.Usage{PromptTokens: 2000, CompletionTokens: 20, CachedTokens: 1500}, 0, false)

	// The latest request, not the turn: summing a hit and a miss reports an
	// average that describes neither request.
	screen := m.contextScreenData()
	if screen.CacheRead != formatTokenCount(1500) || screen.CacheInput != formatTokenCount(2000) {
		t.Errorf("the surface reports %q of %q, want the last request's 1500 of 2000",
			screen.CacheRead, screen.CacheInput)
	}
	if screen.CachePct != 75 {
		t.Errorf("the surface draws %d%%, want 75%%", screen.CachePct)
	}
}

// TestContext_ItemisesTheToolDefinitions is the thing the category total alone
// cannot answer: which tool is costing the window what.
func TestContext_ItemisesTheToolDefinitions(t *testing.T) {
	m := contextModel(t, 110)
	group, ok := m.toolDefGroup(m.toolDefTokens)
	if !ok {
		t.Fatal("a session with a toolset has no tool group")
	}
	if !strings.Contains(group.Summary, "4 tools") {
		t.Errorf("the folded row does not count what it swallowed: %q", group.Summary)
	}
	if len(group.Items) != 4 {
		t.Fatalf("itemised %d tools, want 4", len(group.Items))
	}
	// Largest first, so the tool worth acting on is the one at the top.
	if group.Items[0].Label != "execute_command" {
		t.Errorf("largest item is %q, want execute_command", group.Items[0].Label)
	}
}

// TestContext_AttributesResultsToTheCallsThatMadeThem checks the tool-result
// fold: a result carries only the id of the call it answers, so the names
// have to come from the assistant messages that made the calls.
func TestContext_AttributesResultsToTheCallsThatMadeThem(t *testing.T) {
	m := contextModel(t, 110)
	b := m.contextAccounting()
	group, ok := m.toolResultGroup(b.ToolResults)
	if !ok {
		t.Fatal("a session with tool results has no result group")
	}
	names := make([]string, 0, len(group.Items))
	for _, item := range group.Items {
		names = append(names, item.Label)
	}
	// The search returned five times what the read did, so it sorts first.
	if len(names) != 2 || names[0] != "search" || names[1] != "read_file" {
		t.Errorf("results attributed to %v, want [search read_file]", names)
	}
}

// TestContext_ItemisesTheConversationByTurn checks the fold the messages
// category earns by being most of the window: which exchange it was.
func TestContext_ItemisesTheConversationByTurn(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "find the round limit and tell me what it does when it trips"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("long answer. ", 200)},
		{Role: provider.RoleUser, Content: "thanks"},
		{Role: provider.RoleAssistant, Content: "any time"},
	}, mockStream)
	group, ok := m.messageTurnGroup(m.contextAccounting().Messages)
	if !ok {
		t.Fatal("a conversation with two turns has no message group")
	}
	if !strings.Contains(group.Summary, "2 turns") {
		t.Errorf("the folded row does not count the turns: %q", group.Summary)
	}
	if len(group.Items) != 2 {
		t.Fatalf("itemised %d turns, want 2", len(group.Items))
	}
	// The long turn sorts first, and its label is the opening of the message
	// that started it, cut to one short line.
	first := group.Items[0].Label
	if !strings.HasPrefix(first, "find the round limit") || !strings.HasSuffix(first, "…") {
		t.Errorf("first turn is labelled %q, want the opening of its own message, elided", first)
	}
	if group.Items[1].Label != "thanks" {
		t.Errorf("second turn is labelled %q, want %q", group.Items[1].Label, "thanks")
	}
}

// TestContext_NamesTheTurnsNobodyTyped keeps the labels honest: three
// user-role messages are written by the session, and quoting one back as if
// it were a question the reader asked would misreport their own session.
func TestContext_NamesTheTurnsNobodyTyped(t *testing.T) {
	cases := map[string]string{
		compactContextMessage("we were refactoring the loop"): "the compaction summary",
		commandContextPrefix + " go test ./...":               "a command's output",
		continuePrompt:                                        "carrying on from the round limit",
		"what does ErrRoundLimit do":                          "what does ErrRoundLimit do",
	}
	for content, want := range cases {
		if got := turnLabel(content); got != want {
			t.Errorf("turnLabel(%.30q) = %q, want %q", content, got, want)
		}
	}
}

// TestContext_ItemsSumToTheCategoryAboveThem is what makes an opened fold an
// answer rather than a second opinion: the parts are scaled by the same
// factor the category was, so they add up to the number in the legend.
func TestContext_ItemsSumToTheCategoryAboveThem(t *testing.T) {
	rows := []contextRow{{name: "a", tokens: 300}, {name: "b", tokens: 200}, {name: "c", tokens: 100}}
	// raw 600 estimated, reported as 1200: every part doubles.
	group := contextGroupFrom("tool definitions", "tool", rows, 600, 1200)
	want := []string{"600", "400", "200"}
	for i, item := range group.Items {
		if item.Tokens != want[i] {
			t.Errorf("item %d is %q, want %q", i, item.Tokens, want[i])
		}
	}
}

// TestContext_CountsWhatItDidNotName keeps invariant 4 through the tail: a
// group with more parts than it lists says how many were left and what they
// came to.
func TestContext_CountsWhatItDidNotName(t *testing.T) {
	rows := make([]contextRow, 0, contextItemsShown+3)
	for i := range contextItemsShown + 3 {
		rows = append(rows, contextRow{name: string(rune('a' + i)), tokens: int64(100 - i)})
	}
	group := contextGroupFrom("tool definitions", "tool", rows, 0, 0)
	if len(group.Items) != contextItemsShown {
		t.Fatalf("named %d parts, want %d", len(group.Items), contextItemsShown)
	}
	if !strings.Contains(group.More, "3 more") {
		t.Errorf("the unnamed parts are not counted: %q", group.More)
	}
}

// TestContext_CarriesOpenFoldsAcrossOpenings checks a reader who opened a
// group, left, and came back finds it the way they left it.
func TestContext_CarriesOpenFoldsAcrossOpenings(t *testing.T) {
	m := sendText(t, contextModel(t, 110), "/context")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.context.Groups[0].Open {
		t.Fatal("enter did not open the first group")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = sendText(t, updated.(Model), "/context")
	if !m.context.Groups[0].Open {
		t.Error("the fold the reader opened came back shut")
	}
}

// TestContext_WindowSizeReadsAsItIsSold checks the window is named the round
// number somebody chose rather than what arithmetic makes of it.
func TestContext_WindowSizeReadsAsItIsSold(t *testing.T) {
	cases := map[int64]string{
		1_000_000: "1m",
		200_000:   "200k",
		1_048_576: "1m",
		32_768:    "32.8k",
		512:       "512",
	}
	for n, want := range cases {
		if got := formatWindowSize(n); got != want {
			t.Errorf("%d formats as %q, want %q", n, got, want)
		}
	}
}

// TestContext_IsOfferedInTheRegistryAndTheHelp keeps the command reachable
// the three ways every command is: typed, completed, and listed.
func TestContext_IsOfferedInTheRegistryAndTheHelp(t *testing.T) {
	found := false
	for _, c := range slashCommands() {
		if c.name == "/context" {
			found = true
		}
	}
	if !found {
		t.Error("/context is not in the command registry, so the palette cannot offer it")
	}
	if !strings.Contains(helpText(), "/context") {
		t.Error("/help does not list /context")
	}
}
