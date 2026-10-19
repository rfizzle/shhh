package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/agentcore/meter"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/cache"
	"github.com/rfizzle/llm/capability"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// contextModel is a session with a toolset, a couple of tool calls and their
// results — enough for both folds to have something to itemise.
func contextModel(t *testing.T, width int) Model {
	t.Helper()
	return contextModelWith(t, width, func(*Wiring) {})
}

// cacheContextModel is the same session on a gateway profile's endpoint,
// after one reply whose usage carried cache figures: the event the gate
// reads into the ledger and the stream into the vitals.
func cacheContextModel(t *testing.T, width int, u llm.Usage) (Model, *meter.Ledger) {
	t.Helper()
	ledger := meter.New(nil)
	ledger.SetCacheLease("1h")
	m := contextModelWith(t, width, func(w *Wiring) {
		w.Ledger, w.ProviderName, w.ModelName = ledger, "gateway", "claude-sonnet-5-5"
	})
	ledger.ObserveCache("gateway", "claude-sonnet-5-5", u)
	m.accumulateUsage(&u)
	return m, ledger
}

// cacheHit is a request through the hour's lease that read most of its
// prefix and wrote the rest, and was told it was written for the hour.
var cacheHit = llm.Usage{PromptTokens: 20_000, CompletionTokens: 200, CachedTokens: 19_000,
	CacheCreationTokens: 600, CacheCreation1h: 600,
	Reports: cache.FieldRead | cache.FieldCreation | cache.FieldLease}

func contextModelWith(t *testing.T, width int, wire func(*Wiring)) Model {
	t.Helper()
	w := Wiring{
		ToolDefinitions: []ToolTokens{
			{Name: "execute_command", Tokens: 4100},
			{Name: "edit_file", Tokens: 3200},
			{Name: "search", Tokens: 2600},
			{Name: "read_file", Tokens: 1400},
		},
	}
	wire(&w)
	m := New([]llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("system prompt. ", 40)},
		{Role: llm.RoleUser, Content: "find the round limit"},
		{Role: llm.RoleAssistant, Content: "Searching.", ToolCalls: []llm.ToolCall{
			{ID: "a", Name: "search", Arguments: `{"query":"ErrRoundLimit"}`},
			{ID: "b", Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: strings.Repeat("hit. ", 200)},
		{Role: llm.RoleTool, ToolCallID: "b", Content: strings.Repeat("line. ", 40)},
	}, mockStream, w)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return updated.(Model)
}

// TestContext_OpensAsATakeover checks /context borrows the screen rather than
// leaving a block in the transcript, and that esc gives it back.
func TestContext_OpensAsATakeover(t *testing.T) {
	m := sendText(t, contextModel(t, 110), "/context")
	if m.state != stateContext || m.screens.contextScreen() == nil {
		t.Fatalf("/context did not open the surface, state=%d screen=%v", m.state, m.screens.contextScreen())
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state == stateContext || m.screens.contextScreen() != nil {
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

	m.vitals.record("claude", llm.Usage{PromptTokens: 1000, CompletionTokens: 10}, 0, false)
	m.vitals.record("claude", llm.Usage{PromptTokens: 2000, CompletionTokens: 20, CachedTokens: 1500}, 0, false)

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
	m := New([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "find the round limit and tell me what it does when it trips"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("long answer. ", 200)},
		{Role: llm.RoleUser, Content: "thanks"},
		{Role: llm.RoleAssistant, Content: "any time"},
	}, mockStream, Wiring{})
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

// TestContext_NamesTheTurnsNobodyTyped keeps the labels honest: a message the
// session wrote for itself is named, and quoting one back as if it were a
// question the reader asked would misreport their own session. A steer has no
// opening to match, so it falls to what all of them have in common rather
// than to the wrong name.
func TestContext_NamesTheTurnsNobodyTyped(t *testing.T) {
	cases := []struct {
		msg  llm.Message
		want string
	}{
		{llm.Message{Content: compactContextMessage("we were refactoring the loop"), Machine: true}, "the compaction summary"},
		{llm.Message{Content: commandContextPrefix + " go test ./...", Machine: true}, "a command's output"},
		{llm.Message{Content: continuePrompt, Machine: true}, "carrying on from a cut-off reply"},
		{llm.Message{Content: "You have been asked to stop and check in.", Machine: true}, "what the session said for itself"},
		{llm.Message{Content: "what does ErrRoundLimit do"}, "what does ErrRoundLimit do"},
	}
	for _, tc := range cases {
		if got := turnLabel(tc.msg); got != tc.want {
			t.Errorf("turnLabel(%.30q) = %q, want %q", tc.msg.Content, got, tc.want)
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
	if !m.screens.contextScreen().Groups[0].Open {
		t.Fatal("enter did not open the first group")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = sendText(t, updated.(Model), "/context")
	if !m.screens.contextScreen().Groups[0].Open {
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
	m := frameModel(t, 80, 30)
	if !strings.Contains(helpText(&m), "/context") {
		t.Error("/help does not list /context")
	}
}

// The cache row says whether the endpoint's cache works, in each of the
// reading's words, and is left out before the first reply like the cache
// reads above it.
func TestContext_SaysWhetherTheCacheWorked(t *testing.T) {
	m, _ := cacheContextModel(t, 110, cacheHit)
	got := m.contextScreenData().Cache
	want := components.ContextCache{Verdict: "works on gateway", Model: "claude-sonnet-5-5", Lease: "1h", Confirmed: "1h", Share: "95%"}
	if got != want {
		t.Errorf("works: %+v, want %+v", got, want)
	}

	m, _ = cacheContextModel(t, 110, llm.Usage{PromptTokens: 20_000, CacheCreationTokens: 20_000, CacheCreation5m: 20_000})
	if got := m.contextScreenData().Cache; got.Confirmed != "5m" || got.Lease != "1h" {
		t.Errorf("an hour written as five minutes: %+v", got)
	}
	m, ledger := cacheContextModel(t, 110, llm.Usage{PromptTokens: 20_000})
	if got := m.contextScreenData().Cache.Verdict; got != "none yet" {
		t.Errorf("one silent request: %q", got)
	}
	for range 13 {
		ledger.ObserveCache("gateway", "claude-sonnet-5-5", llm.Usage{PromptTokens: 20_000})
	}
	if got := m.contextScreenData().Cache.Verdict; got != "not seen on gateway in 14 requests" {
		t.Errorf("not seen: %q", got)
	}

	capability.RegisterDeclarations("gateway", map[string]capability.Declared{
		"claude-sonnet-5-5": {Model: map[string]any{capability.KeyPromptCache: false}},
	})
	t.Cleanup(func() { capability.RegisterDeclarations("gateway", nil) })
	if got := m.contextScreenData().Cache; got != (components.ContextCache{Verdict: "off (profile gateway)"}) {
		t.Errorf("off: %+v", got)
	}

	if got := contextModel(t, 110).contextScreenData().Cache; got.Verdict != "" {
		t.Errorf("before the first reply the row is left out: %+v", got)
	}
}
